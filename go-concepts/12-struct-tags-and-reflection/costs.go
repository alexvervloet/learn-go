package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// What reflection costs
// =====================
//
// Reflection is slow because it defers to runtime everything the compiler
// normally settles: which field, what type, how to convert. Each access is a
// lookup rather than an offset.
//
// It is still the right answer at a boundary. encoding/json is reflection all
// the way down and is fast enough for nearly every service. The rule is
// "once per request, not once per row".
//
// costs_test.go measures three ways of doing the same job.

// Record is the type the three approaches operate on.
type Record struct {
	ID    int64
	Name  string
	Email string
	Score float64
	Admin bool
}

// directFieldAccess is the baseline: the compiler knows every offset.
func directFieldAccess(r Record) string {
	var b strings.Builder
	b.WriteString(r.Name)
	b.WriteString(r.Email)
	return b.String()
}

// reflectFieldAccess does the same through reflect, by NAME, which is the
// slowest common form because each lookup searches the field list.
func reflectFieldAccess(r Record) string {
	rv := reflect.ValueOf(r)

	var b strings.Builder
	b.WriteString(rv.FieldByName("Name").String())
	b.WriteString(rv.FieldByName("Email").String())
	return b.String()
}

// reflectFieldAccessByIndex is the same thing by index, skipping the name
// lookup. This is what a library does after caching the field positions once,
// and the gap between it and FieldByName is the entire reason that caching
// exists.
func reflectFieldAccessByIndex(r Record) string {
	rv := reflect.ValueOf(r)

	var b strings.Builder
	b.WriteString(rv.Field(1).String())
	b.WriteString(rv.Field(2).String())
	return b.String()
}

// fieldIndexCache is how a real library avoids FieldByName in a hot path:
// resolve names to indexes once per TYPE, then use indexes forever.
type fieldIndexCache struct {
	byName map[string]int
}

// newFieldIndexCache builds the map once.
func newFieldIndexCache(t reflect.Type) *fieldIndexCache {
	c := &fieldIndexCache{byName: make(map[string]int, t.NumField())}

	for i := 0; i < t.NumField(); i++ {
		c.byName[t.Field(i).Name] = i
	}
	return c
}

// get reads a field by name through the cached index.
func (c *fieldIndexCache) get(rv reflect.Value, name string) (reflect.Value, bool) {
	i, ok := c.byName[name]
	if !ok {
		return reflect.Value{}, false
	}
	return rv.Field(i), true
}

// recordCache is built once at init, which is what makes the cached benchmark
// a fair comparison rather than a measurement of map construction.
var recordCache = newFieldIndexCache(reflect.TypeOf(Record{}))

// cachedReflectFieldAccess uses it.
func cachedReflectFieldAccess(r Record) string {
	rv := reflect.ValueOf(r)

	var b strings.Builder
	if v, ok := recordCache.get(rv, "Name"); ok {
		b.WriteString(v.String())
	}
	if v, ok := recordCache.get(rv, "Email"); ok {
		b.WriteString(v.String())
	}
	return b.String()
}

// genericFieldAccess is the lesson 11 alternative: when the operation is the
// same for every type but the TYPE varies, a type parameter does it at compile
// time. This is not always a substitute (it cannot read tags), and where it is,
// it is free.
func genericFieldAccess[T any](items []T, extract func(T) string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, extract(item))
	}
	return out
}

// manualJSON hand-writes the encoding, and is the benchmark's cautionary tale:
// it is SLOWER than encoding/json (330 ns against 309 ns), because fmt.Sprint
// is itself reflective. "Hand-written must be faster" is not automatic. Real
// code generation (easyjson and friends) does win, and costs a build step.
//
// Left deliberately naive, because the naive version is what people actually
// write when they decide to hand-roll an encoder.
func manualJSON(r Record) string {
	var b strings.Builder

	b.WriteString(`{"ID":`)
	b.WriteString(strconv.FormatInt(r.ID, 10))
	b.WriteString(`,"Name":`)
	b.WriteString(strconvQuote(r.Name))
	b.WriteString(`,"Email":`)
	b.WriteString(strconvQuote(r.Email))
	b.WriteString(`,"Score":`)
	// 'g' with precision -1 is what encoding/json uses for float64, and it is
	// the reason the two encoders produce byte-identical output.
	b.WriteString(strconv.FormatFloat(r.Score, 'g', -1, 64))
	b.WriteString(`,"Admin":`)
	b.WriteString(strconv.FormatBool(r.Admin))
	b.WriteString("}")

	return b.String()
}

// strconvQuote is a minimal JSON string quoter, enough for the benchmark's
// inputs. Real code uses strconv.Quote or encoding/json.
func strconvQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// reflectiveJSON is encoding/json, for the comparison.
func reflectiveJSON(r Record) (string, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	return string(data), nil
}

// whenNotToReflect is the guidance.
func whenNotToReflect() []string {
	return []string{
		"when generics will do: since 1.18 much old reflection is a type parameter, checked at compile time",
		"when an interface will do: switching on Kind over your OWN types is a method waiting to be written",
		"in a hot loop: at a boundary once per request, not once per row",
		"to reach unexported fields: possible with unsafe, and it will break",
		"when the alternative is codegen: that is a real trade, not an obvious win",
	}
}

// demoCosts prints the approaches producing identical output.
func demoCosts() {
	r := Record{ID: 1, Name: "Ana", Email: "ana@example.com", Score: 92.5, Admin: true}

	fmt.Printf("  direct:            %q\n", directFieldAccess(r))
	fmt.Printf("  reflect ByName:    %q\n", reflectFieldAccess(r))
	fmt.Printf("  reflect by index:  %q\n", reflectFieldAccessByIndex(r))
	fmt.Printf("  reflect cached:    %q\n", cachedReflectFieldAccess(r))

	names := genericFieldAccess([]Record{r, r}, func(v Record) string { return v.Name })
	fmt.Printf("  generic extractor: %v\n", names)

	manual := manualJSON(r)
	reflective, err := reflectiveJSON(r)
	fmt.Printf("\n  manual JSON:     %s\n", manual)
	fmt.Printf("  encoding/json:   %s\n", reflective)
	fmt.Printf("  identical: %t (err=%v)\n", manual == reflective, err)

	fmt.Println("\n  when not to reflect:")
	for _, s := range whenNotToReflect() {
		fmt.Printf("    %s\n", s)
	}
	fmt.Println("    run `go test -bench . -benchmem -run '^$' ./12-struct-tags-and-reflection` for the numbers")
}

// reflectTypeOfRecord and reflectValueOfRecord keep costs_test.go from needing
// a reflect import of its own.
func reflectTypeOfRecord() reflect.Type { return reflect.TypeOf(Record{}) }

func reflectValueOfRecord(r Record) reflect.Value { return reflect.ValueOf(r) }

// handWrittenValidate is the SignupRequest validator without reflection, for
// the benchmark to compare against. It is faster and it is also five times the
// code, rewritten per type, with nothing stopping a field being forgotten.
//
// That trade is the actual argument, and it is why tag-driven validation won
// despite being slower.
func handWrittenValidate(r SignupRequest) error {
	var errs []error

	if r.Email == "" {
		errs = append(errs, &ValidationError{Field: "email", Rule: "required", Value: r.Email})
	} else {
		at := strings.Index(r.Email, "@")
		if at <= 0 || at == len(r.Email)-1 || !strings.Contains(r.Email[at:], ".") {
			errs = append(errs, &ValidationError{Field: "email", Rule: "email", Value: r.Email})
		}
	}

	switch {
	case r.Password == "":
		errs = append(errs, &ValidationError{Field: "password", Rule: "required", Value: r.Password})
	case len(r.Password) < 8:
		errs = append(errs, &ValidationError{Field: "password", Rule: "min=8", Value: r.Password})
	case len(r.Password) > 72:
		errs = append(errs, &ValidationError{Field: "password", Rule: "max=72", Value: r.Password})
	}

	if r.Age < 18 {
		errs = append(errs, &ValidationError{Field: "age", Rule: "min=18", Value: r.Age})
	}
	if r.Age > 120 {
		errs = append(errs, &ValidationError{Field: "age", Rule: "max=120", Value: r.Age})
	}

	if r.Plan == "" {
		errs = append(errs, &ValidationError{Field: "plan", Rule: "required", Value: r.Plan})
	} else if r.Plan != "free" && r.Plan != "pro" && r.Plan != "enterprise" {
		errs = append(errs, &ValidationError{
			Field: "plan", Rule: "oneof=free pro enterprise", Value: r.Plan,
		})
	}

	if len(r.Tags) > 5 {
		errs = append(errs, &ValidationError{Field: "tags", Rule: "max=5", Value: r.Tags})
	}

	return errors.Join(errs...)
}

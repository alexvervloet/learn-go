package main

import (
	"encoding/json"
	"slices"
	"testing"
)

var benchRecord = Record{
	ID:    1,
	Name:  "Ana",
	Email: "ana@example.com",
	Score: 92.5,
	Admin: true,
}

// TestAllAccessMethodsAgree keeps the benchmark honest: if the four approaches
// stop producing the same answer, the comparison is measuring different work.
func TestAllAccessMethodsAgree(t *testing.T) {
	want := "Ana" + "ana@example.com"

	tests := []struct {
		name string
		got  string
	}{
		{"direct", directFieldAccess(benchRecord)},
		{"reflect by name", reflectFieldAccess(benchRecord)},
		{"reflect by index", reflectFieldAccessByIndex(benchRecord)},
		{"reflect cached", cachedReflectFieldAccess(benchRecord)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != want {
				t.Errorf("got %q, want %q", tt.got, want)
			}
		})
	}
}

func TestFieldIndexCache(t *testing.T) {
	c := newFieldIndexCache(reflectTypeOfRecord())

	for _, name := range []string{"ID", "Name", "Email", "Score", "Admin"} {
		if _, ok := c.byName[name]; !ok {
			t.Errorf("cache is missing %q", name)
		}
	}

	if _, ok := c.byName["Missing"]; ok {
		t.Error("cache should not contain a field that does not exist")
	}

	rv := reflectValueOfRecord(benchRecord)
	if v, ok := c.get(rv, "Name"); !ok || v.String() != "Ana" {
		t.Errorf("get(Name) = %v, %t", v, ok)
	}
	if _, ok := c.get(rv, "Missing"); ok {
		t.Error("get on a missing field should report false")
	}
}

func TestGenericFieldAccess(t *testing.T) {
	records := []Record{
		{Name: "Ana"},
		{Name: "Bo"},
	}

	got := genericFieldAccess(records, func(r Record) string { return r.Name })

	if want := []string{"Ana", "Bo"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestManualAndReflectiveJSONAgree: the hand-written encoder must produce
// exactly what encoding/json does, or the benchmark compares different output.
func TestManualAndReflectiveJSONAgree(t *testing.T) {
	tests := []Record{
		benchRecord,
		{},
		{Name: `quote"inside`, Email: `back\slash`},
		{ID: -1, Score: 0.5, Admin: false},
	}

	for _, r := range tests {
		manual := manualJSON(r)

		reflective, err := reflectiveJSON(r)
		if err != nil {
			t.Fatalf("marshal %+v: %v", r, err)
		}

		if manual != reflective {
			t.Errorf("for %+v:\n  manual: %s\n  json:   %s", r, manual, reflective)
		}

		// And the manual output must actually parse.
		var round Record
		if err := json.Unmarshal([]byte(manual), &round); err != nil {
			t.Errorf("manual output does not parse: %v (%s)", err, manual)
		}
	}
}

func TestWhenNotToReflectIsDocumented(t *testing.T) {
	if got := whenNotToReflect(); len(got) < 4 {
		t.Errorf("expected at least 4 documented cases, got %d", len(got))
	}
}

// The benchmarks behind the README's cost claims.
//
//	go test -bench . -benchmem -run '^$' ./12-struct-tags-and-reflection

func BenchmarkFieldAccessDirect(b *testing.B) {
	for b.Loop() {
		_ = directFieldAccess(benchRecord)
	}
}

func BenchmarkFieldAccessReflectByName(b *testing.B) {
	for b.Loop() {
		_ = reflectFieldAccess(benchRecord)
	}
}

func BenchmarkFieldAccessReflectByIndex(b *testing.B) {
	for b.Loop() {
		_ = reflectFieldAccessByIndex(benchRecord)
	}
}

func BenchmarkFieldAccessReflectCached(b *testing.B) {
	for b.Loop() {
		_ = cachedReflectFieldAccess(benchRecord)
	}
}

func BenchmarkJSONManual(b *testing.B) {
	for b.Loop() {
		_ = manualJSON(benchRecord)
	}
}

func BenchmarkJSONReflective(b *testing.B) {
	for b.Loop() {
		_, _ = reflectiveJSON(benchRecord)
	}
}

// The validator, which is tags plus reflection on every field. This is the
// number that matters for "once per request, not once per row".
func BenchmarkValidate(b *testing.B) {
	req := validRequest()

	for b.Loop() {
		_ = Validate(req)
	}
}

// A hand-written equivalent, for scale.
func BenchmarkValidateHandWritten(b *testing.B) {
	req := validRequest()

	for b.Loop() {
		_ = handWrittenValidate(req)
	}
}

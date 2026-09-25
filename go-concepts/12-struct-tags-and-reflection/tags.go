// Package main is lesson 12 of go-concepts: struct tags and reflection.
//
// A struct tag is a string literal after a field. The compiler stores it and
// ignores it; libraries read it at runtime through reflect.
//
//	type User struct {
//	    Email string `json:"email" db:"email" validate:"required,email"`
//	}
//
// The format is a convention, not syntax: space-separated key:"value" pairs.
// reflect.StructTag.Get and .Lookup parse it.
package main

import (
	"fmt"
	"reflect"
	"strings"
)

// TaggedUser carries tags for three different consumers, which is the normal
// case: one struct, read by the JSON encoder, a database mapper and a
// validator, none of which know about each other.
type TaggedUser struct {
	ID       int64  `json:"id" db:"user_id" validate:"required"`
	Email    string `json:"email" db:"email_address" validate:"required,email"`
	Name     string `json:"name,omitempty" db:"full_name" validate:"min=2,max=50"`
	Password string `json:"-" db:"password_hash" validate:"required,min=8"`
	Internal string // no tags at all
	hidden   string //nolint:unused // unexported: invisible to every library
}

// readTags walks a struct type and returns what each consumer would see.
//
// reflect.Type.Field(i) gives a StructField, which carries the Name, the Type,
// the Tag, and whether the field is exported.
func readTags(v any) []FieldTags {
	t := reflect.TypeOf(v)

	// Accept a pointer as well as a value, because callers pass both and the
	// difference is not interesting here.
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}

	out := make([]FieldTags, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// Get returns "" for a missing key, which is indistinguishable from a
		// key present with an empty value. Lookup reports which.
		dbTag, dbPresent := field.Tag.Lookup("db")

		out = append(out, FieldTags{
			Name:      field.Name,
			Type:      field.Type.String(),
			Exported:  field.IsExported(),
			JSON:      field.Tag.Get("json"),
			DB:        dbTag,
			DBPresent: dbPresent,
			Validate:  field.Tag.Get("validate"),
			RawTag:    string(field.Tag),
		})
	}

	return out
}

// FieldTags is what readTags reports per field.
type FieldTags struct {
	Name      string
	Type      string
	Exported  bool
	JSON      string
	DB        string
	DBPresent bool
	Validate  string
	RawTag    string
}

// getVsLookup is the distinction worth knowing. Get cannot tell "absent" from
// "present and empty", which matters when an empty value is meaningful:
// `json:""` is not the same as no json tag.
func getVsLookup() (absentGet string, absentOK bool, emptyGet string, emptyOK bool) {
	type sample struct {
		NoTag    string
		EmptyTag string `json:""`
	}

	t := reflect.TypeOf(sample{})

	noTag := t.Field(0).Tag
	emptyTag := t.Field(1).Tag

	absentGet = noTag.Get("json")
	_, absentOK = noTag.Lookup("json")

	emptyGet = emptyTag.Get("json")
	_, emptyOK = emptyTag.Lookup("json")

	return absentGet, absentOK, emptyGet, emptyOK
}

// The malformed-tag trap
// ----------------------
//
// A tag is an ordinary string literal, so the compiler accepts anything. These
// all compile and all fail silently at runtime:
//
//	`json: "email"`     a space after the colon: the key is "json" with no value
//	`json:'email'`      single quotes: not the convention, not parsed
//	"json:\"email\""    double quotes instead of backticks: works, but nobody
//	                    escapes it correctly the second time
//
// GO VET CATCHES THE FIRST TWO. `go vet` runs the `structtag` analyser, which
// reports "struct field tag not compatible with reflect.StructTag.Get". That is
// one of the better arguments for having vet in CI: the failure mode without it
// is a field that silently uses its Go name as the JSON key.

// malformedTagExamples documents what each mistake produces.
func malformedTagExamples() []string {
	return []string{
		`json: "email"   -> a space after the colon; the value is lost, vet catches it`,
		`json:'email'    -> single quotes are not the convention; vet catches it`,
		`Json:"email"    -> wrong case; encoding/json looks for "json" and finds nothing`,
		`json:"email"    -> correct`,
	}
}

// unexportedFieldsAreInvisible is the single most common "why is my JSON empty"
// cause. Reflection can SEE an unexported field's name and type, and can read
// nothing and set nothing.
func unexportedFieldsAreInvisible() (total, exported, unexported int, names []string) {
	t := reflect.TypeOf(TaggedUser{})
	total = t.NumField()

	for i := 0; i < total; i++ {
		f := t.Field(i)
		if f.IsExported() {
			exported++
			continue
		}
		unexported++
		names = append(names, f.Name)
	}

	return total, exported, unexported, names
}

// parseTagOptions splits a tag value into its name and options, which is what
// every tag consumer does first. encoding/json's own parser is this, plus
// validation of the name.
func parseTagOptions(tag string) (name string, options []string) {
	parts := strings.Split(tag, ",")

	name = parts[0]
	if len(parts) > 1 {
		options = parts[1:]
	}

	return name, options
}

// hasOption reports whether a tag carries a given option.
func hasOption(tag, option string) bool {
	_, options := parseTagOptions(tag)

	for _, o := range options {
		if o == option {
			return true
		}
	}
	return false
}

// embeddedFieldsAreWalkedToo: an embedded struct appears as one field whose
// Anonymous flag is set. Libraries that flatten embedded fields (encoding/json
// does) recurse into them.
type Timestamps struct {
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type Article struct {
	Timestamps        // embedded, Anonymous == true
	Title      string `json:"title"`
}

func embeddedFields() (anonymous []string, named []string) {
	t := reflect.TypeOf(Article{})

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			anonymous = append(anonymous, f.Name)
			continue
		}
		named = append(named, f.Name)
	}

	return anonymous, named
}

// demoTags prints tag reading.
func demoTags() {
	fmt.Println("  fields of TaggedUser, as three libraries would see them:")
	fmt.Printf("    %-9s %-8s %-6s %-16s %-16s %s\n", "FIELD", "TYPE", "EXP", "JSON", "DB", "VALIDATE")
	for _, f := range readTags(TaggedUser{}) {
		db := f.DB
		if !f.DBPresent {
			db = "(absent)"
		}
		fmt.Printf("    %-9s %-8s %-6t %-16q %-16q %q\n", f.Name, f.Type, f.Exported, f.JSON, db, f.Validate)
	}

	absentGet, absentOK, emptyGet, emptyOK := getVsLookup()
	fmt.Printf("\n  Get vs Lookup:\n")
	fmt.Printf("    no json tag:    Get=%q Lookup ok=%t\n", absentGet, absentOK)
	fmt.Printf("    `json:\"\"`:       Get=%q Lookup ok=%t   <- Get cannot tell these apart\n", emptyGet, emptyOK)

	total, exported, unexported, names := unexportedFieldsAreInvisible()
	fmt.Printf("\n  TaggedUser has %d fields: %d exported, %d unexported (%v)\n",
		total, exported, unexported, names)
	fmt.Println("    an unexported field cannot be marshalled, unmarshalled or validated")

	name, options := parseTagOptions("email,omitempty,string")
	fmt.Printf("\n  parseTagOptions(\"email,omitempty,string\") -> name=%q options=%v\n", name, options)
	fmt.Printf("    hasOption(\"name,omitempty\", \"omitempty\") = %t\n", hasOption("name,omitempty", "omitempty"))
	fmt.Printf("    hasOption(\"name\", \"omitempty\")           = %t\n", hasOption("name", "omitempty"))

	anonymous, named := embeddedFields()
	fmt.Printf("\n  Article: embedded=%v named=%v\n", anonymous, named)

	fmt.Println("\n  malformed tags:")
	for _, s := range malformedTagExamples() {
		fmt.Printf("    %s\n", s)
	}
}

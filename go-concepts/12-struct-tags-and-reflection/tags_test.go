package main

import (
	"reflect"
	"slices"
	"testing"
)

func TestReadTags(t *testing.T) {
	fields := readTags(TaggedUser{})

	if len(fields) != 6 {
		t.Fatalf("got %d fields, want 6", len(fields))
	}

	byName := make(map[string]FieldTags, len(fields))
	for _, f := range fields {
		byName[f.Name] = f
	}

	tests := []struct {
		field    string
		json     string
		db       string
		validate string
		exported bool
	}{
		{"ID", "id", "user_id", "required", true},
		{"Email", "email", "email_address", "required,email", true},
		{"Name", "name,omitempty", "full_name", "min=2,max=50", true},
		{"Password", "-", "password_hash", "required,min=8", true},
		{"Internal", "", "", "", true},
		{"hidden", "", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			f, ok := byName[tt.field]
			if !ok {
				t.Fatalf("field %q not found", tt.field)
			}
			if f.JSON != tt.json {
				t.Errorf("json = %q, want %q", f.JSON, tt.json)
			}
			if f.DB != tt.db {
				t.Errorf("db = %q, want %q", f.DB, tt.db)
			}
			if f.Validate != tt.validate {
				t.Errorf("validate = %q, want %q", f.Validate, tt.validate)
			}
			if f.Exported != tt.exported {
				t.Errorf("exported = %t, want %t", f.Exported, tt.exported)
			}
		})
	}
}

func TestReadTagsAcceptsAPointer(t *testing.T) {
	fromValue := readTags(TaggedUser{})
	fromPointer := readTags(&TaggedUser{})

	if len(fromValue) != len(fromPointer) {
		t.Errorf("value gave %d fields, pointer gave %d", len(fromValue), len(fromPointer))
	}
}

func TestReadTagsOnANonStruct(t *testing.T) {
	for _, v := range []any{42, "text", []int{1}, map[string]int{}} {
		if got := readTags(v); got != nil {
			t.Errorf("readTags(%T) = %v, want nil", v, got)
		}
	}
}

// TestGetVsLookup is the distinction Get cannot express.
func TestGetVsLookup(t *testing.T) {
	absentGet, absentOK, emptyGet, emptyOK := getVsLookup()

	if absentGet != "" || emptyGet != "" {
		t.Errorf("both Gets should be empty, got %q and %q", absentGet, emptyGet)
	}
	if absentOK {
		t.Error("a missing tag should report ok=false from Lookup")
	}
	if !emptyOK {
		t.Error("a present-but-empty tag should report ok=true from Lookup")
	}
}

func TestUnexportedFieldsAreInvisible(t *testing.T) {
	total, exported, unexported, names := unexportedFieldsAreInvisible()

	if total != 6 {
		t.Errorf("total = %d, want 6", total)
	}
	if exported != 5 {
		t.Errorf("exported = %d, want 5", exported)
	}
	if unexported != 1 {
		t.Errorf("unexported = %d, want 1", unexported)
	}
	if !slices.Equal(names, []string{"hidden"}) {
		t.Errorf("names = %v, want [hidden]", names)
	}
}

func TestParseTagOptions(t *testing.T) {
	tests := []struct {
		tag         string
		wantName    string
		wantOptions []string
	}{
		{"email", "email", nil},
		{"email,omitempty", "email", []string{"omitempty"}},
		{"email,omitempty,string", "email", []string{"omitempty", "string"}},
		{",omitempty", "", []string{"omitempty"}},
		{"-", "-", nil},
		{"", "", nil},
	}

	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			name, options := parseTagOptions(tt.tag)

			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if !slices.Equal(options, tt.wantOptions) {
				t.Errorf("options = %v, want %v", options, tt.wantOptions)
			}
		})
	}
}

func TestHasOption(t *testing.T) {
	tests := []struct {
		tag, option string
		want        bool
	}{
		{"name,omitempty", "omitempty", true},
		{"name", "omitempty", false},
		{"name,string,omitempty", "omitempty", true},
		{",omitempty", "omitempty", true},
		{"omitempty", "omitempty", false}, // it is the NAME here, not an option
	}

	for _, tt := range tests {
		if got := hasOption(tt.tag, tt.option); got != tt.want {
			t.Errorf("hasOption(%q, %q) = %t, want %t", tt.tag, tt.option, got, tt.want)
		}
	}
}

func TestEmbeddedFields(t *testing.T) {
	anonymous, named := embeddedFields()

	if !slices.Equal(anonymous, []string{"Timestamps"}) {
		t.Errorf("anonymous = %v, want [Timestamps]", anonymous)
	}
	if !slices.Equal(named, []string{"Title"}) {
		t.Errorf("named = %v, want [Title]", named)
	}
}

// TestStructTagParsingMatchesTheStdlib: the convention is documented on
// reflect.StructTag, so the stdlib parser is the reference implementation.
func TestStructTagParsingMatchesTheStdlib(t *testing.T) {
	tag := reflect.StructTag(`json:"email,omitempty" db:"email" validate:"required,email"`)

	tests := []struct {
		key  string
		want string
	}{
		{"json", "email,omitempty"},
		{"db", "email"},
		{"validate", "required,email"},
		{"missing", ""},
	}

	for _, tt := range tests {
		if got := tag.Get(tt.key); got != tt.want {
			t.Errorf("Get(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

// TestMalformedTagLosesItsValue pins the failure mode go vet exists to catch.
func TestMalformedTagLosesItsValue(t *testing.T) {
	// A space after the colon. The stdlib parser stops at the space, so the
	// key "json" is never found.
	malformed := reflect.StructTag(`json: "email"`)

	if got := malformed.Get("json"); got != "" {
		t.Errorf("Get on a malformed tag = %q, want empty", got)
	}
	if _, ok := malformed.Lookup("json"); ok {
		t.Error("Lookup should not find a key in a malformed tag")
	}

	// The correct form, for contrast.
	correct := reflect.StructTag(`json:"email"`)
	if got := correct.Get("json"); got != "email" {
		t.Errorf("Get on a correct tag = %q, want email", got)
	}
}

func TestMalformedTagExamplesAreDocumented(t *testing.T) {
	if got := malformedTagExamples(); len(got) < 3 {
		t.Errorf("expected at least 3 examples, got %d", len(got))
	}
}

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// JSON tags
// =========
//
//	json:"email"           use this key
//	json:"-"               never encode or decode this field
//	json:"-,"              use the literal key "-" (the trailing comma escapes it)
//	json:",omitempty"      omit when empty: 0, "", false, nil, len 0
//	json:",omitzero"       omit when the zero value (Go 1.24), works on structs
//	json:",string"         encode a number or bool as a JSON string
//
// omitempty and omitzero are NOT the same, and the difference is why omitzero
// was added.

// Profile shows every tag option in one struct.
type Profile struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`

	// omitempty drops "", 0, false, nil, and empty slices and maps.
	Bio      string   `json:"bio,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Follower int      `json:"followers,omitempty"`

	// A pointer plus omitempty is the classic way to make a field genuinely
	// optional: nil means absent, and &0 means "present and zero".
	Age *int `json:"age,omitempty"`

	// string encodes the number as a JSON string, which is how you survive
	// JavaScript's float64 ids (lesson 03's precision test).
	BigID int64 `json:"big_id,string"`

	// - means never. A password hash must not reach a response by accident.
	PasswordHash string `json:"-"`

	// -, means the literal key "-": the trailing comma is the escape. Rare,
	// and worth knowing exists.
	//
	// staticcheck flags this (SA5008) because `json:"-"` and `json:"-,"` differ
	// by one character and mean opposite things, and it suggests the clearer
	// `json:"'-',"` spelling. Both work; this one is the form you will meet in
	// other people's code, which is why it is here.
	Dash string `json:"-,"` //nolint:staticcheck // SA5008: demonstrating the classic spelling
}

// Event contrasts omitempty and omitzero on a time.Time, which is the case
// that motivated omitzero.
type Event struct {
	Name string `json:"name"`

	// omitempty does NOT work here. A struct is never "empty" to encoding/json,
	// so a zero time.Time marshals as "0001-01-01T00:00:00Z".
	StartsAt time.Time `json:"starts_at,omitempty"`

	// omitzero (Go 1.24) omits the zero value, and consults IsZero() when the
	// type has one. time.Time does, so this disappears when unset.
	EndsAt time.Time `json:"ends_at,omitzero"`
}

// omitemptyVsOmitzero marshals an Event with both fields unset.
func omitemptyVsOmitzero() (withBothUnset string, withBothSet string, err error) {
	unset, err := json.Marshal(Event{Name: "unset"})
	if err != nil {
		return "", "", fmt.Errorf("marshal unset: %w", err)
	}

	at := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	set, err := json.Marshal(Event{Name: "set", StartsAt: at, EndsAt: at})
	if err != nil {
		return "", "", fmt.Errorf("marshal set: %w", err)
	}

	return string(unset), string(set), nil
}

// omitzeroUsesIsZero: any type with an IsZero() bool method controls its own
// omission. This is what makes omitzero work for sql.NullString, decimal types,
// and anything else with a domain-specific notion of empty.
type Money struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

// IsZero makes Money omitzero-aware. Note that a zero AMOUNT in a real currency
// is not the same as an unset Money, and this type says so.
func (m Money) IsZero() bool { return m.Amount == 0 && m.Currency == "" }

// Invoice uses it.
type Invoice struct {
	Number string `json:"number"`
	Total  Money  `json:"total,omitzero"`
	Paid   Money  `json:"paid,omitzero"`
}

// customMarshalling: a type can control its own JSON entirely by implementing
// json.Marshaler and json.Unmarshaler. This is how time.Time produces RFC 3339
// rather than a struct dump.

// Duration wraps time.Duration to marshal as a human string ("1h30m") rather
// than as the nanosecond integer time.Duration would otherwise produce.
type Duration struct {
	time.Duration
}

// MarshalJSON implements json.Marshaler. The VALUE receiver matters: with a
// pointer receiver, marshalling a Duration (rather than a *Duration) would not
// find the method, and you would get the default encoding with no error.
// That is a genuinely nasty silent failure.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

// UnmarshalJSON implements json.Unmarshaler. This one MUST have a pointer
// receiver, because it mutates.
func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}

	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", s, err)
	}

	d.Duration = parsed
	return nil
}

// Job uses the custom type.
type Job struct {
	Name    string   `json:"name"`
	Timeout Duration `json:"timeout"`
}

// unknownFieldsAreIgnoredByDefault is a decision worth knowing about: by
// default, JSON keys with no matching field are silently dropped. For an API
// that is usually right; for a config file it hides typos.
//
// DisallowUnknownFields turns it into an error.
func unknownFieldsAreIgnoredByDefault(input string) (lenient Profile, strictErr error) {
	_ = json.Unmarshal([]byte(input), &lenient)

	var strict Profile
	dec := json.NewDecoder(strings.NewReader(input))
	dec.DisallowUnknownFields()
	strictErr = dec.Decode(&strict)

	return lenient, strictErr
}

// caseInsensitiveMatching is another default that surprises people:
// encoding/json matches field names case-insensitively when there is no exact
// match. So "USERNAME" and "UserName" both fill a field tagged "username".
func caseInsensitiveMatching() (fromExact, fromUpper, fromMixed string, err error) {
	var a, b, c Profile

	for _, pair := range []struct {
		input  string
		target *Profile
	}{
		{`{"username":"exact"}`, &a},
		{`{"USERNAME":"upper"}`, &b},
		{`{"UserName":"mixed"}`, &c},
	} {
		if uerr := json.Unmarshal([]byte(pair.input), pair.target); uerr != nil {
			return "", "", "", fmt.Errorf("unmarshal %s: %w", pair.input, uerr)
		}
	}

	return a.Username, b.Username, c.Username, nil
}

// demoJSONTags prints tag behaviour.
func demoJSONTags() {
	age := 0
	p := Profile{
		ID:           1,
		Username:     "ana",
		Age:          &age, // present and zero: a pointer distinguishes it
		BigID:        9007199254740993,
		PasswordHash: "never-send-this",
		Dash:         "literal dash key",
	}

	data, _ := json.MarshalIndent(p, "    ", "  ")
	fmt.Printf("  Profile with most fields unset:\n    %s\n", data)
	fmt.Println("    ...bio, tags and followers omitted; age present because the pointer is non-nil")
	fmt.Println("    ...big_id is a STRING, so JavaScript cannot round it")
	fmt.Println("    ...password_hash absent entirely")

	unset, set, err := omitemptyVsOmitzero()
	fmt.Printf("\n  omitempty vs omitzero on time.Time (err=%v):\n", err)
	fmt.Printf("    both unset: %s\n", unset)
	fmt.Printf("    both set:   %s\n", set)
	fmt.Println("    ...omitempty kept the zero time; omitzero dropped it")

	inv := Invoice{Number: "INV-1", Total: Money{Amount: 5000, Currency: "GBP"}}
	data, _ = json.Marshal(inv)
	fmt.Printf("\n  omitzero consulting IsZero(): %s\n", data)

	job := Job{Name: "reindex", Timeout: Duration{90 * time.Minute}}
	data, _ = json.Marshal(job)
	fmt.Printf("\n  custom MarshalJSON: %s\n", data)

	var decoded Job
	_ = json.Unmarshal([]byte(`{"name":"backup","timeout":"2h15m"}`), &decoded)
	fmt.Printf("  custom UnmarshalJSON: %s -> %v\n", "2h15m", decoded.Timeout.Duration)

	lenient, strictErr := unknownFieldsAreIgnoredByDefault(`{"username":"ana","typo_field":1}`)
	fmt.Printf("\n  unknown fields, default:               username=%q, no error\n", lenient.Username)
	fmt.Printf("  unknown fields, DisallowUnknownFields: %v\n", strictErr)

	exact, upper, mixed, _ := caseInsensitiveMatching()
	fmt.Printf("\n  field matching is case-insensitive: %q, %q, %q all filled the same field\n",
		exact, upper, mixed)
}

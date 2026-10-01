package main

import (
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"fmt"
)

// encoding/json/v2
// ================
//
// Go 1.27 ships encoding/json/v2 alongside encoding/json, and encoding/json is now implemented on top of it
// while keeping its old behaviour. The two read the same tags and disagree on what they mean in five places,
// each one a v1 default that hid a mistake:
//
//	field names      v1 matches keys case-insensitively, so "NAME" fills Name; v2 matches exactly
//	duplicate keys   v1 keeps the last one silently; v2 refuses the document
//	nil slices       v1 writes null; v2 writes [], what a client iterating it wanted
//	omitempty        v1 omits 0, false and ""; v2 omits only empty JSON (null, "", [], {}), so 0 stays.
//	                 For "leave out the zero value", the tag in both is omitzero
//	invalid UTF-8    v1 replaces it with U+FFFD; v2 refuses it
//
// Every row is a v2 refusal or a v2 change of output, so moving a service from v1 to v2 is a behaviour change
// to its API, not an import rename. The measured outputs are in TestJSONv2Differences.

// v2Doc is the shape the comparison decodes into.
type v2Doc struct {
	Name  string   `json:"name"`
	Tags  []string `json:"tags"`
	Count int      `json:"count,omitempty"`
}

// bothDecode decodes the same input with each version and reports what each did.
func bothDecode(in string) (v1 v2Doc, v1Err error, v2 v2Doc, v2Err error) {
	v1Err = jsonv1.Unmarshal([]byte(in), &v1)
	v2Err = json.Unmarshal([]byte(in), &v2)

	return v1, v1Err, v2, v2Err
}

// bothEncode encodes the same value with each version.
func bothEncode(v v2Doc) (v1, v2 string) {
	b1, _ := jsonv1.Marshal(v)
	b2, _ := json.Marshal(v)

	return string(b1), string(b2)
}

// demoJSONv2 prints each difference side by side.
func demoJSONv2() {
	for _, in := range []string{`{"NAME":"ada"}`, `{"name":"a","name":"b"}`, "{\"name\":\"\xff\"}"} {
		v1, e1, v2, e2 := bothDecode(in)
		fmt.Printf("  %-26q v1: %q err=%v\n  %-26s v2: %q err=%v\n", in, v1.Name, e1, "", v2.Name, e2)
	}

	v1, v2 := bothEncode(v2Doc{})
	fmt.Printf("  encode the zero value  v1: %s\n                         v2: %s\n", v1, v2)
}

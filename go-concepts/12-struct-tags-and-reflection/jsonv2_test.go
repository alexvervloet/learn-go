package main

import "testing"

// TestJSONv2Differences pins each place v1 and v2 disagree, with the outputs measured.
func TestJSONv2Differences(t *testing.T) {
	t.Run("field names: v1 ignores case, v2 does not", func(t *testing.T) {
		v1, e1, v2, e2 := bothDecode(`{"NAME":"ada"}`)
		if e1 != nil || e2 != nil || v1.Name != "ada" || v2.Name != "" {
			t.Errorf("v1 %q %v, v2 %q %v", v1.Name, e1, v2.Name, e2)
		}
	})

	t.Run("duplicate keys: v1 keeps the last, v2 refuses", func(t *testing.T) {
		v1, e1, _, e2 := bothDecode(`{"name":"a","name":"b"}`)
		if e1 != nil || v1.Name != "b" || e2 == nil {
			t.Errorf("v1 %q %v, v2 err %v", v1.Name, e1, e2)
		}
	})

	t.Run("invalid UTF-8: v1 replaces it, v2 refuses", func(t *testing.T) {
		v1, e1, _, e2 := bothDecode("{\"name\":\"\xff\"}")
		if e1 != nil || v1.Name != "�" || e2 == nil {
			t.Errorf("v1 %q %v, v2 err %v", v1.Name, e1, e2)
		}
	})

	t.Run("nil slice and omitempty on 0", func(t *testing.T) {
		v1, v2 := bothEncode(v2Doc{})
		if v1 != `{"name":"","tags":null}` {
			t.Errorf("v1 wrote %s", v1)
		}
		if v2 != `{"name":"","tags":[],"count":0}` {
			t.Errorf("v2 wrote %s", v2)
		}
	})
}

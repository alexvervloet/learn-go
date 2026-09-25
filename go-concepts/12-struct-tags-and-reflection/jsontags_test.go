package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestJSONTagOptions(t *testing.T) {
	age := 0
	p := Profile{
		ID:           1,
		Username:     "ana",
		Age:          &age,
		BigID:        9007199254740993,
		PasswordHash: "never-send-this",
		Dash:         "literal",
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(data)

	tests := []struct {
		name     string
		fragment string
		want     bool
	}{
		{"omitempty drops an empty string", `"bio"`, false},
		{"omitempty drops a nil slice", `"tags"`, false},
		{"omitempty drops a zero int", `"followers"`, false},
		{"a non-nil pointer survives omitempty even at zero", `"age":0`, true},
		{"the string option quotes the number", `"big_id":"9007199254740993"`, true},
		{"a dash tag removes the field entirely", `"password_hash"`, false},
		{"the password value never appears", `never-send-this`, false},
		{"dash-comma means a literal dash key", `"-":"literal"`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := strings.Contains(out, tt.fragment); got != tt.want {
				t.Errorf("contains(%q) = %t, want %t\nJSON: %s", tt.fragment, got, tt.want, out)
			}
		})
	}
}

// TestPointerDistinguishesAbsentFromZero is why optional fields are pointers.
func TestPointerDistinguishesAbsentFromZero(t *testing.T) {
	zero := 0

	withZero, err := json.Marshal(Profile{Age: &zero})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	withNil, err := json.Marshal(Profile{Age: nil})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if !strings.Contains(string(withZero), `"age":0`) {
		t.Errorf("a pointer to zero should be encoded: %s", withZero)
	}
	if strings.Contains(string(withNil), `"age"`) {
		t.Errorf("a nil pointer should be omitted: %s", withNil)
	}
}

// TestOmitemptyVsOmitzero is the distinction that motivated omitzero.
func TestOmitemptyVsOmitzero(t *testing.T) {
	unset, set, err := omitemptyVsOmitzero()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	t.Run("omitempty does NOT drop a zero struct", func(t *testing.T) {
		if !strings.Contains(unset, `"starts_at"`) {
			t.Errorf("omitempty should have kept the zero time: %s", unset)
		}
		if !strings.Contains(unset, "0001-01-01") {
			t.Errorf("expected the zero time to be rendered: %s", unset)
		}
	})

	t.Run("omitzero does", func(t *testing.T) {
		if strings.Contains(unset, `"ends_at"`) {
			t.Errorf("omitzero should have dropped the zero time: %s", unset)
		}
	})

	t.Run("both appear when set", func(t *testing.T) {
		if !strings.Contains(set, `"starts_at"`) || !strings.Contains(set, `"ends_at"`) {
			t.Errorf("both fields should be present: %s", set)
		}
	})
}

// TestOmitzeroConsultsIsZero: a type controls its own omission.
func TestOmitzeroConsultsIsZero(t *testing.T) {
	inv := Invoice{
		Number: "INV-1",
		Total:  Money{Amount: 5000, Currency: "GBP"},
		// Paid is the zero Money, and Money.IsZero reports true.
	}

	data, err := json.Marshal(inv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(data)

	if !strings.Contains(out, `"total"`) {
		t.Errorf("a non-zero Money should be present: %s", out)
	}
	if strings.Contains(out, `"paid"`) {
		t.Errorf("a zero Money should be omitted via IsZero: %s", out)
	}
}

func TestMoneyIsZero(t *testing.T) {
	tests := []struct {
		name string
		m    Money
		want bool
	}{
		{"fully zero", Money{}, true},
		{"zero amount with a currency", Money{Currency: "GBP"}, false},
		{"non-zero amount", Money{Amount: 1}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.IsZero(); got != tt.want {
				t.Errorf("IsZero() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestCustomMarshalling(t *testing.T) {
	t.Run("marshal", func(t *testing.T) {
		data, err := json.Marshal(Job{Name: "reindex", Timeout: Duration{90 * time.Minute}})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if want := `{"name":"reindex","timeout":"1h30m0s"}`; string(data) != want {
			t.Errorf("got %s, want %s", data, want)
		}
	})

	t.Run("unmarshal", func(t *testing.T) {
		var job Job
		if err := json.Unmarshal([]byte(`{"name":"backup","timeout":"2h15m"}`), &job); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if job.Timeout.Duration != 2*time.Hour+15*time.Minute {
			t.Errorf("timeout = %v, want 2h15m", job.Timeout.Duration)
		}
	})

	t.Run("round trip", func(t *testing.T) {
		original := Job{Name: "sync", Timeout: Duration{45 * time.Second}}

		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		var decoded Job
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if decoded != original {
			t.Errorf("round trip gave %+v, want %+v", decoded, original)
		}
	})

	t.Run("a bad duration reports why", func(t *testing.T) {
		var job Job
		err := json.Unmarshal([]byte(`{"timeout":"not-a-duration"}`), &job)

		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "parse duration") {
			t.Errorf("err = %q, want it to name the operation", err)
		}
	})

	t.Run("a non-string duration reports why", func(t *testing.T) {
		var job Job
		err := json.Unmarshal([]byte(`{"timeout":90}`), &job)

		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "must be a string") {
			t.Errorf("err = %q", err)
		}
	})
}

func TestUnknownFields(t *testing.T) {
	lenient, strictErr := unknownFieldsAreIgnoredByDefault(`{"username":"ana","typo_field":1}`)

	if lenient.Username != "ana" {
		t.Errorf("the known field should still decode, got %q", lenient.Username)
	}
	if strictErr == nil {
		t.Fatal("DisallowUnknownFields should have rejected the input")
	}
	if !strings.Contains(strictErr.Error(), "typo_field") {
		t.Errorf("err = %q, want it to name the unknown field", strictErr)
	}
}

// TestCaseInsensitiveMatching is a default that surprises people and is worth
// pinning: three spellings all fill the same field.
func TestCaseInsensitiveMatching(t *testing.T) {
	exact, upper, mixed, err := caseInsensitiveMatching()
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if exact != "exact" || upper != "upper" || mixed != "mixed" {
		t.Errorf("got %q, %q, %q — all three spellings should have matched", exact, upper, mixed)
	}
}

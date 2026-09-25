package main

import (
	"strings"
	"testing"
)

func TestDescribeJSON(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"null", `null`, "null"},
		{"bool", `true`, "bool(true)"},
		{"integer decodes as float64", `7`, "number(7)"},
		{"float", `1.5`, "number(1.5)"},
		{"string", `"hi"`, `string("hi")`},
		{"empty array", `[]`, "array[]"},
		{"mixed array", `[1, "a", null]`, `array[number(1), string("a"), null]`},
		{"object sorts its keys", `{"b": 2, "a": 1}`, "object{a: number(1), b: number(2)}"},
		{"nested", `{"xs": [true]}`, "object{xs: array[bool(true)]}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := parseJSON(tt.raw)
			if err != nil {
				t.Fatalf("parseJSON(%s): %v", tt.raw, err)
			}
			if got := describeJSON(v); got != tt.want {
				t.Errorf("describeJSON(%s) = %s, want %s", tt.raw, got, tt.want)
			}
		})
	}
}

// TestJSONNumbersLosePrecision is the reason the describeJSON comment warns
// about large ids. This is not a Go quirk, it is IEEE 754: float64 carries 53
// bits of mantissa, so 2^53 is the last integer with a unique representation.
// 2^53+1 rounds down to 2^53 on the way in, and the value is simply gone.
//
// This is why every API that issues 64-bit ids (Twitter's snowflake ids being
// the famous case) sends them as JSON strings. A Go service decoding into a
// typed struct with an int64 field is fine; one decoding into `any` is not.
func TestJSONNumbersLosePrecision(t *testing.T) {
	const maxExact = int64(1) << 53 // 9007199254740992
	const oneMore = maxExact + 1    // 9007199254740993, not representable

	v, err := parseJSON(`{"exact": 9007199254740992, "lost": 9007199254740993}`)
	if err != nil {
		t.Fatalf("parseJSON: %v", err)
	}
	obj := v.(map[string]any)

	if got := int64(obj["exact"].(float64)); got != maxExact {
		t.Errorf("2^53 decoded as %d, want %d — it should survive exactly", got, maxExact)
	}

	got := int64(obj["lost"].(float64))
	if got == oneMore {
		t.Error("expected 2^53+1 to lose precision decoding into float64")
	}
	if got != maxExact {
		t.Errorf("2^53+1 decoded as %d, want it rounded to %d", got, maxExact)
	}
	t.Logf("2^53+1 = %d decoded as %d", oneMore, got)
}

func TestParseJSONError(t *testing.T) {
	_, err := parseJSON(`{not json`)
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error %q should name the operation", err.Error())
	}
}

func TestAssertToAnInterface(t *testing.T) {
	tests := []struct {
		name      string
		in        any
		want      string
		wantMatch bool
	}{
		{"Loud is a Greeter", Loud("hey"), "HEY!", true},
		{"Polite is a Greeter", Polite{Name: "Ana"}, "Good day, Ana.", true},
		{"int is not", 42, "", false},
		{"nil is not", nil, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := assertToAnInterface(tt.in)
			if ok != tt.wantMatch {
				t.Fatalf("ok = %t, want %t", ok, tt.wantMatch)
			}
			if got != tt.want {
				t.Errorf("greeting = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestComparingAssertionForms(t *testing.T) {
	t.Run("wrong type: comma-ok reports, bare form panics", func(t *testing.T) {
		got, ok, panicMsg := comparingAssertionForms(42)

		if ok {
			t.Error("comma-ok should report false for an int asserted to string")
		}
		if got != "" {
			t.Errorf("value = %q, want the zero string", got)
		}
		if panicMsg == "" {
			t.Error("the bare assertion should have panicked")
		}
		if !strings.Contains(panicMsg, "int") || !strings.Contains(panicMsg, "string") {
			t.Errorf("panic message %q should name both types", panicMsg)
		}
	})

	t.Run("right type: both succeed", func(t *testing.T) {
		got, ok, panicMsg := comparingAssertionForms("hello")

		if !ok || got != "hello" {
			t.Errorf("got %q, %t; want \"hello\", true", got, ok)
		}
		if panicMsg != "" {
			t.Errorf("no panic expected, got %q", panicMsg)
		}
	})
}

func TestEmbeddingPromotesMethods(t *testing.T) {
	svc := Service{baseLogger: baseLogger{prefix: "svc"}, Name: "users"}

	// Service declares no Log method, and satisfies Logger.
	var l Logger = svc
	if got, want := l.Log("started"), "svc: started"; got != want {
		t.Errorf("promoted Log() = %q, want %q", got, want)
	}
}

func TestOuterMethodShadowsEmbedded(t *testing.T) {
	loud := LoudService{baseLogger: baseLogger{prefix: "svc"}, Name: "users"}

	if got, want := loud.Log("started"), "SVC: STARTED"; got != want {
		t.Errorf("Log() = %q, want the shadowing version %q", got, want)
	}
	if got, want := loud.baseLogger.Log("started"), "svc: started"; got != want {
		t.Errorf("baseLogger.Log() = %q, want %q — the embedded method stays reachable", got, want)
	}
}

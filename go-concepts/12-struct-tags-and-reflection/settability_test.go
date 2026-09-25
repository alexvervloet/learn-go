package main

import (
	"strings"
	"testing"
)

// TestCannotSetACopy is the rule everyone meets once.
func TestCannotSetACopy(t *testing.T) {
	canSet, panicMsg := cannotSetACopy()

	if canSet {
		t.Error("a field of a copied value must not report CanSet")
	}
	if panicMsg == "" {
		t.Fatal("setting an unaddressable value should panic")
	}
	if !strings.Contains(panicMsg, "unaddressable") {
		t.Errorf("panic = %q, want it to mention addressability", panicMsg)
	}
}

func TestCanSetThroughAPointer(t *testing.T) {
	canSet, host := canSetThroughAPointer()

	if !canSet {
		t.Error("a field reached through a pointer should be settable")
	}
	if host != "changed" {
		t.Errorf("host = %q, want changed — the write should have landed", host)
	}
}

// TestUnexportedFieldsAreNeverSettable: addressable and still not settable.
// Reflection respects export rules.
func TestUnexportedFieldsAreNeverSettable(t *testing.T) {
	addressable, settable := unexportedFieldsAreNeverSettable()

	if !addressable {
		t.Error("an unexported field of an addressable struct is addressable")
	}
	if settable {
		t.Error("an unexported field must never be settable")
	}
}

func TestSetFieldByName(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		value   string
		check   func(Settings) bool
		wantErr string
	}{
		{"string", "Host", "example.com", func(s Settings) bool { return s.Host == "example.com" }, ""},
		{"int", "Port", "8080", func(s Settings) bool { return s.Port == 8080 }, ""},
		{"bool true", "Debug", "true", func(s Settings) bool { return s.Debug }, ""},
		{"bool 1", "Debug", "1", func(s Settings) bool { return s.Debug }, ""},
		{"float", "Timeout", "2.5", func(s Settings) bool { return s.Timeout == 2.5 }, ""},
		{"negative int", "Port", "-1", func(s Settings) bool { return s.Port == -1 }, ""},

		{"bad int", "Port", "not-a-number", nil, "invalid syntax"},
		{"bad bool", "Debug", "maybe", nil, "invalid syntax"},
		{"bad float", "Timeout", "x", nil, "invalid syntax"},
		{"missing field", "Nope", "x", nil, "no field"},
		{"unexported field", "secret", "x", nil, "not settable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s Settings
			err := setFieldByName(&s, tt.field, tt.value)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("err = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.check(s) {
				t.Errorf("the field was not set correctly: %+v", s)
			}
		})
	}
}

// TestSetFieldByNameRejectsNonPointers: the error should say what to do,
// rather than failing confusingly at CanSet.
func TestSetFieldByNameRejectsNonPointers(t *testing.T) {
	tests := []struct {
		name   string
		target any
	}{
		{"a value", Settings{}},
		{"a nil pointer", (*Settings)(nil)},
		{"a pointer to a non-struct", new(int)},
		{"not a pointer at all", 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := setFieldByName(tt.target, "Host", "x")
			if err == nil {
				t.Fatal("expected an error")
			}
			t.Logf("%v", err)
		})
	}
}

func TestLoadFromMap(t *testing.T) {
	t.Run("all valid", func(t *testing.T) {
		var s Settings
		err := LoadFromMap(&s, map[string]string{
			"Host":    "example.com",
			"Port":    "8080",
			"Debug":   "true",
			"Timeout": "2.5",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := Settings{Host: "example.com", Port: 8080, Debug: true, Timeout: 2.5}
		if s != want {
			t.Errorf("got %+v, want %+v", s, want)
		}
	})

	t.Run("reports every failure, not just the first", func(t *testing.T) {
		var s Settings
		err := LoadFromMap(&s, map[string]string{
			"Port":    "not-a-number",
			"Debug":   "maybe",
			"Missing": "x",
			"secret":  "x",
		})

		if err == nil {
			t.Fatal("expected an error")
		}
		for _, want := range []string{"Port", "Debug", "Missing", "secret"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("err %q should mention %q", err, want)
			}
		}
	})

	t.Run("valid fields still land when others fail", func(t *testing.T) {
		var s Settings
		_ = LoadFromMap(&s, map[string]string{
			"Host": "example.com",
			"Port": "bad",
		})

		if s.Host != "example.com" {
			t.Errorf("Host = %q, want it set despite the other failure", s.Host)
		}
	})

	t.Run("empty map", func(t *testing.T) {
		var s Settings
		if err := LoadFromMap(&s, nil); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if s != (Settings{}) {
			t.Errorf("got %+v, want the zero value", s)
		}
	})
}

// TestLoadFromMapErrorsAreDeterministic: map iteration is random, so the
// implementation sorts keys. Without that, the message differs per run and a
// test asserting on it flakes.
func TestLoadFromMapErrorsAreDeterministic(t *testing.T) {
	values := map[string]string{
		"Port":    "bad",
		"Debug":   "bad",
		"Timeout": "bad",
	}

	var first string
	for i := 0; i < 20; i++ {
		var s Settings
		err := LoadFromMap(&s, values)
		if err == nil {
			t.Fatal("expected an error")
		}
		if i == 0 {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("message changed between runs:\n  %q\n  %q", first, err.Error())
		}
	}
}

func TestSettabilityRulesAreDocumented(t *testing.T) {
	if got := settabilityRules(); len(got) < 4 {
		t.Errorf("expected at least 4 rules, got %d", len(got))
	}
}

package stack

import "testing"

func TestIsBalanced(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", true},
		{"one pair", "()", true},
		{"nested", "([{}])", true},
		{"sequential", "()[]{}", true},
		{"deeply nested", "((((()))))", true},
		{"with other characters", "a(b[c]d)e", true},
		{"no brackets at all", "hello", true},

		{"closer with nothing open", ")", false},
		{"unclosed opener", "(", false},
		{"mismatched", "(]", false},
		{"mismatched nested", "([)]", false},
		{"extra closer", "())", false},
		{"extra opener", "(()", false},
		{"wrong order", "}{", false},

		// A realistic case: brackets inside a string literal are not tracked,
		// which is a limitation worth knowing rather than a bug.
		{"brackets in text", `print("(")`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsBalanced(tt.in); got != tt.want {
				t.Errorf("IsBalanced(%q) = %t, want %t", tt.in, got, tt.want)
			}
		})
	}
}

// TestIsBalancedHandlesMultibyteInput: ranging over a string yields runes, so
// non-ASCII input works. Indexing with s[i] would give bytes and break here.
func TestIsBalancedHandlesMultibyteInput(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"(héllo)", true},
		{"(日本語)", true},
		{"(🙂)", true},
		{"(🙂", false},
	}

	for _, tt := range tests {
		if got := IsBalanced(tt.in); got != tt.want {
			t.Errorf("IsBalanced(%q) = %t, want %t", tt.in, got, tt.want)
		}
	}
}

func TestLongestBalancedPrefix(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"fully balanced", "()", 2},
		{"balanced then garbage", "())", 2},
		{"two pairs then garbage", "()()]", 4},
		{"nothing balanced", ")(", 0},
		{"unclosed from the start", "((", 0},
		{"nested then broken", "([])(", 4},
		{"empty", "", 0},
		{"no brackets", "abc", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LongestBalancedPrefix(tt.in); got != tt.want {
				t.Errorf("LongestBalancedPrefix(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// TestLongestBalancedPrefixAgreesWithIsBalanced: when the whole string is
// balanced, the prefix must be the whole string.
func TestLongestBalancedPrefixAgreesWithIsBalanced(t *testing.T) {
	balanced := []string{"()", "([{}])", "()[]{}", "((()))"}

	for _, s := range balanced {
		if !IsBalanced(s) {
			t.Fatalf("%q should be balanced", s)
		}
		if got := LongestBalancedPrefix(s); got != len(s) {
			t.Errorf("%q: prefix = %d, want %d (the whole string)", s, got, len(s))
		}
	}
}

func BenchmarkIsBalanced(b *testing.B) {
	input := ""
	for i := 0; i < 500; i++ {
		input += "([{}])"
	}

	for b.Loop() {
		_ = IsBalanced(input)
	}
}

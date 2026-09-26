package stack

import "unicode/utf8"

// Balanced brackets
// =================
//
// The canonical stack problem, and the reason stacks are taught first: the
// nesting structure of the input IS a stack, so the algorithm writes itself.
//
//	push every opener
//	on a closer, pop and check it matches
//	the stack must be empty at the end

// pairs maps each closer to its opener. A map rather than a switch so adding a
// bracket type is one line.
var pairs = map[rune]rune{
	')': '(',
	']': '[',
	'}': '{',
}

// IsBalanced reports whether every bracket in s is closed in the right order.
//
// Characters that are not brackets are ignored, so "a(b[c]d)e" is balanced.
//
// Three ways to fail, and all three are tested:
//
//	")"    a closer with an empty stack
//	"(]"   a closer that does not match the top
//	"("    a non-empty stack at the end
func IsBalanced(s string) bool {
	var open Stack[rune]

	// range over a string yields RUNES, not bytes, so this is correct for
	// multi-byte input. Indexing with s[i] would give bytes and break on
	// anything outside ASCII. See go-concepts/01.
	for _, c := range s {
		// An opener: remember it.
		if c == '(' || c == '[' || c == '{' {
			open.Push(c)
			continue
		}

		// A closer: it must match the most recent unclosed opener.
		wanted, isCloser := pairs[c]
		if !isCloser {
			continue // not a bracket at all
		}

		top, ok := open.Pop()
		if !ok {
			return false // a closer with nothing open
		}
		if top != wanted {
			return false // closed the wrong kind
		}
	}

	// Anything left open is unbalanced.
	return open.IsEmpty()
}

// LongestBalancedPrefix returns the length of the longest prefix of s that is
// balanced, which is what an editor uses to decide where the error is.
//
// It tracks the position after each complete close, so an unbalanced tail does
// not discard the valid start.
func LongestBalancedPrefix(s string) int {
	var open Stack[rune]
	longest := 0

	// Same shape as IsBalanced, deliberately. A `switch { case c == '(' ... }`
	// reads the same and staticcheck rejects it (QF1002: a switch on a single
	// variable should be a tagged switch), so the if/continue form it is.
	for i, c := range s {
		if c == '(' || c == '[' || c == '{' {
			open.Push(c)
			continue
		}

		wanted, isCloser := pairs[c]
		if !isCloser {
			continue
		}

		top, ok := open.Pop()
		if !ok || top != wanted {
			return longest // the prefix ends here
		}

		// Everything up to and including this rune is balanced, provided
		// nothing is still open. RuneLen rather than len(string(c)), which
		// would allocate a string per closer just to measure it.
		if open.IsEmpty() {
			longest = i + utf8.RuneLen(c)
		}
	}

	return longest
}

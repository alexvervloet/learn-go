package main

import (
	"fmt"
	"strings"
)

// What panics
// ===========
//
// The runtime panics on a handful of conditions, all of which mean the program
// asked for something that does not exist:
//
//	index out of range          xs[5] on a 3-element slice
//	nil pointer dereference     p.Field where p is nil
//	nil map write               m["k"] = 1 on a nil map
//	integer divide by zero      n / 0
//	slice bounds out of range   xs[2:10]
//	interface conversion        v.(string) where v holds an int
//	close of a closed channel   see lesson 07
//	send on a closed channel    see lesson 07
//
// None of these are conditions to handle. Every one is a bug in the code that
// caused it, which is exactly why the language kills the program rather than
// returning an error nobody would check.

// panicKind names each runtime panic so the demo table can be driven by data.
type panicKind struct {
	name    string
	trigger func()
}

// runtimePanics is every runtime panic worth knowing, as a table. Each trigger
// is wrapped by capturePanic below rather than being run directly.
func runtimePanics() []panicKind {
	return []panicKind{
		{"index out of range", func() {
			xs := []int{1, 2, 3}
			i := 5 // in a variable, or the compiler would reject it outright
			_ = xs[i]
		}},
		{"nil pointer dereference", func() {
			type T struct{ Field int }
			var p *T
			_ = p.Field
		}},
		{"nil map write", func() {
			var m map[string]int
			m["key"] = 1 //nolint:staticcheck // SA5000 is right; the panic is the catalogue entry
		}},
		{"integer divide by zero", func() {
			n, d := 1, 0
			_ = n / d
		}},
		{"slice bounds out of range", func() {
			xs := []int{1, 2, 3}
			high := 10
			_ = xs[2:high]
		}},
		{"interface conversion", func() {
			var v any = 42
			_ = v.(string)
		}},
	}
}

// capturePanic runs fn and returns the panic message, or "" if fn returned
// normally. This is a test helper shape, not a production one: recovering
// around arbitrary code is only acceptable when the goal is to observe the
// panic, as here.
func capturePanic(fn func()) (message string) {
	defer func() {
		if r := recover(); r != nil {
			message = fmt.Sprint(r)
		}
	}()

	fn()
	return ""
}

// Panicking on purpose
// ====================
//
// Three shapes are idiomatic. All of them say "this state is impossible", and
// none of them can be triggered by a caller passing bad data.

// Shape is a closed enum this package fully controls.
type Shape int

// The shapes this package knows about.
const (
	ShapeCircle Shape = iota + 1
	ShapeSquare
	ShapeTriangle
)

// Area is the exhaustive-switch shape. The default branch is unreachable for
// any Shape this package produces, so panicking there is honest: reaching it
// means someone added a shape and forgot this switch.
//
// The alternative, returning 0 and nil, turns a forgotten case into a silently
// wrong number that surfaces three services away.
func Area(s Shape, size float64) float64 {
	switch s {
	case ShapeCircle:
		return 3.14159265358979 * size * size
	case ShapeSquare:
		return size * size
	case ShapeTriangle:
		return 0.5 * size * size
	default:
		panic(fmt.Sprintf("Area: unhandled shape %d", int(s)))
	}
}

// MustParseCSVHeader is the Must shape. A Must function panics instead of
// returning an error, and is reserved for input that is a literal in the source
// and therefore a typo rather than a runtime condition.
//
// regexp.MustCompile, template.Must and netip.MustParseAddr all work this way.
// The rule: a Must function is for something you wrote, never for input.
func MustParseCSVHeader(header string) []string {
	cols, err := ParseCSVHeader(header)
	if err != nil {
		panic(fmt.Sprintf("MustParseCSVHeader(%q): %v", header, err))
	}
	return cols
}

// ParseCSVHeader is the error-returning version, which is what callers handling
// real input must use.
func ParseCSVHeader(header string) ([]string, error) {
	if strings.TrimSpace(header) == "" {
		return nil, fmt.Errorf("empty header")
	}

	cols := strings.Split(header, ",")
	seen := make(map[string]struct{}, len(cols))

	for i, c := range cols {
		c = strings.TrimSpace(c)
		if c == "" {
			return nil, fmt.Errorf("column %d is empty", i)
		}
		if _, dup := seen[c]; dup {
			return nil, fmt.Errorf("duplicate column %q", c)
		}
		seen[c] = struct{}{}
		cols[i] = c
	}

	return cols, nil
}

// ring is a tiny data structure whose invariant is worth asserting. Panicking
// on a broken internal invariant is the third idiomatic shape: it means this
// package's own code is wrong, not its caller's.
type ring struct {
	items []string
	head  int
}

func newRing(size int) *ring {
	if size <= 0 {
		// A caller CAN cause this, so it is a judgement call. Panicking is
		// defensible for a constructor argument that is always a literal;
		// returning an error is defensible too. What is not defensible is
		// silently clamping to 1.
		panic(fmt.Sprintf("newRing: size must be positive, got %d", size))
	}
	return &ring{items: make([]string, size)}
}

func (r *ring) push(s string) {
	// The invariant: head always indexes into items. If this ever fails, the
	// bug is in this file, and continuing would corrupt data.
	if r.head < 0 || r.head >= len(r.items) {
		panic(fmt.Sprintf("ring invariant violated: head=%d len=%d", r.head, len(r.items)))
	}
	r.items[r.head] = s
	r.head = (r.head + 1) % len(r.items)
}

// fatalErrorsCannotBeRecovered documents what recover does NOT catch. These are
// "fatal error", not "panic", and no deferred recover sees them:
//
//	fatal error: concurrent map writes
//	fatal error: all goroutines are asleep - deadlock!
//	fatal error: stack overflow
//	runtime: out of memory
//
// The distinction is deliberate. A panic unwinds a stack that is still
// coherent. A concurrent map write means the map's internal structure is
// already corrupt, so running deferred code over it would make things worse.
//
// This function only describes them; triggering one would end the process.
func fatalErrorsCannotBeRecovered() []string {
	return []string{
		"concurrent map writes",
		"all goroutines are asleep - deadlock!",
		"stack overflow",
		"out of memory",
	}
}

// demoPanics prints every runtime panic and the deliberate ones.
func demoPanics() {
	fmt.Println("  runtime panics, captured:")
	for _, pk := range runtimePanics() {
		fmt.Printf("    %-26s %s\n", pk.name, capturePanic(pk.trigger))
	}

	fmt.Println("\n  panicking on purpose:")
	fmt.Printf("    Area(ShapeSquare, 3) = %.0f\n", Area(ShapeSquare, 3))
	fmt.Printf("    Area(Shape(99), 3)   panics: %s\n", capturePanic(func() { _ = Area(Shape(99), 3) }))

	fmt.Printf("    MustParseCSVHeader(\"id,name\") = %v\n", MustParseCSVHeader("id,name"))
	fmt.Printf("    MustParseCSVHeader(\"id,id\")   panics: %s\n",
		capturePanic(func() { _ = MustParseCSVHeader("id,id") }))
	_, err := ParseCSVHeader("id,id")
	fmt.Printf("    ParseCSVHeader(\"id,id\")       returns: %v\n", err)

	fmt.Printf("    newRing(0) panics: %s\n", capturePanic(func() { _ = newRing(0) }))

	fmt.Println("\n  NOT recoverable (fatal error, not panic):")
	for _, f := range fatalErrorsCannotBeRecovered() {
		fmt.Printf("    %s\n", f)
	}
}

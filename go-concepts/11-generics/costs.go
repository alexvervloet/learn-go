package main

import (
	"fmt"
	"strings"
)

// What generics cost
// ==================
//
// Go does NOT do what C++ templates do (one compiled copy per type, and a
// binary that grows accordingly), and does not do what Java does either
// (erase everything to Object and box the primitives).
//
// It uses GC SHAPE STENCILING. Types are grouped into "gcshapes" by memory
// layout and pointer positions, and one copy is compiled per shape:
//
//	int, int64          one shape   (8 bytes, no pointers)
//	*User, *Order, *T   ONE shape   (all pointers look alike to the GC)
//	string              one shape   (16 bytes, one pointer)
//	struct{a,b int}     one shape   (16 bytes, no pointers)
//
// Where a shape covers several types, the instantiation receives a hidden
// DICTIONARY argument holding the type-specific details: method addresses,
// type descriptors, conversion info.
//
// Three consequences:
//
//  1. No code-size explosion. This was a deliberate design goal.
//  2. Generic code over POINTER types can be slower than the concrete version,
//     because method calls go through the dictionary instead of being inlined.
//  3. Generic code over a single concrete shape (ints, say) is usually
//     identical to hand-written code.
//
// The benchmarks in costs_test.go measure all three on your machine.

// sumInts is the concrete version, for the benchmark baseline.
func sumInts(values []int) int {
	total := 0
	for _, v := range values {
		total += v
	}
	return total
}

// sumGeneric is the same loop with a type parameter.
func sumGeneric[T Number](values []T) T {
	var total T
	for _, v := range values {
		total += v
	}
	return total
}

// sumAny is the pre-generics alternative: `any` plus a type switch. It is the
// slowest of the three, and the one that fails at runtime rather than at
// compile time.
func sumAny(values []any) (int, error) {
	total := 0
	for _, v := range values {
		n, ok := v.(int)
		if !ok {
			return 0, fmt.Errorf("sumAny: element is %T, not int", v)
		}
		total += n
	}
	return total, nil
}

// Stringish is a method constraint, which is where the dictionary shows up:
// calling String() on a T goes through it rather than being a direct call.
type Stringish interface {
	String() string
}

// joinGeneric calls a method on every element, so it exercises the dictionary
// path.
func joinGeneric[T Stringish](items []T, sep string) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, item.String())
	}
	return strings.Join(parts, sep)
}

// joinInterface does the same through an ordinary interface, for comparison.
// This is the version that existed before generics, and for this shape of
// problem it is often just as fast.
func joinInterface(items []Stringish, sep string) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, item.String())
	}
	return strings.Join(parts, sep)
}

// label is a small type with a String method, for the benchmarks.
type label struct {
	id   int
	text string
}

func (l label) String() string { return fmt.Sprintf("%d:%s", l.id, l.text) }

// typesShareAShape documents the grouping, which is the part that is hard to
// observe directly from Go code. These facts come from the compiler's
// implementation of stenciling, not from anything measurable in this file.
func typesShareAShape() []string {
	return []string{
		"int and int64 on a 64-bit platform: one shape (8 bytes, no pointers)",
		"*User, *Order and every other pointer: ONE shape, plus a dictionary",
		"string: its own shape (16 bytes, one pointer at offset 0)",
		"struct{a, b int}: its own shape (16 bytes, no pointers)",
		"so the binary grows with SHAPES, not with the number of types used",
	}
}

// whenGenericsCostSomething is the practical summary, and it is written from
// the benchmark results rather than from folklore. The numbers are in the
// README; the short version is that the cost was not measurable on any of the
// workloads here.
func whenGenericsCostSomething() []string {
	return []string{
		"over one concrete shape (ints): measured identical to hand-written code",
		"a generic container vs the map it wraps: within 3%",
		"a stack of pointers vs a stack of ints: no measurable difference",
		"vs `any` plus a type switch: generics are 2x FASTER, and checked at compile time",
		"vs a plain interface: a wash, 2% either way",
		"the dictionary cost is real but needs a profiler to find; do not design around it",
		"the cost that is always real is readability, paid by every future reader",
	}
}

// demoCosts prints the cost picture.
func demoCosts() {
	values := []int{1, 2, 3, 4, 5}
	anyValues := []any{1, 2, 3, 4, 5}

	concrete := sumInts(values)
	generic := sumGeneric(values)
	viaAny, err := sumAny(anyValues)

	fmt.Printf("  sumInts    = %d\n", concrete)
	fmt.Printf("  sumGeneric = %d\n", generic)
	fmt.Printf("  sumAny     = %d (err=%v)\n", viaAny, err)

	_, err = sumAny([]any{1, "not an int", 3})
	fmt.Printf("  sumAny with a wrong type: %v   <- a runtime failure the others cannot have\n", err)

	labels := []label{{1, "alpha"}, {2, "beta"}}
	fmt.Printf("\n  joinGeneric:   %s\n", joinGeneric(labels, ", "))

	asInterfaces := make([]Stringish, len(labels))
	for i, l := range labels {
		asInterfaces[i] = l
	}
	fmt.Printf("  joinInterface: %s\n", joinInterface(asInterfaces, ", "))

	fmt.Println("\n  GC shape stenciling groups types by layout:")
	for _, s := range typesShareAShape() {
		fmt.Printf("    %s\n", s)
	}

	fmt.Println("\n  when generics cost something:")
	for _, s := range whenGenericsCostSomething() {
		fmt.Printf("    %s\n", s)
	}
	fmt.Println("    run `go test -bench . -benchmem -run '^$' ./11-generics` for the numbers")
}

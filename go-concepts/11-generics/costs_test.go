package main

import (
	"strings"
	"testing"
)

func TestSumImplementationsAgree(t *testing.T) {
	values := []int{1, 2, 3, 4, 5}
	anyValues := []any{1, 2, 3, 4, 5}

	concrete := sumInts(values)
	generic := sumGeneric(values)

	viaAny, err := sumAny(anyValues)
	if err != nil {
		t.Fatalf("sumAny: %v", err)
	}

	if concrete != 15 || generic != 15 || viaAny != 15 {
		t.Errorf("results disagree: concrete=%d generic=%d any=%d", concrete, generic, viaAny)
	}
}

// TestSumAnyFailsAtRuntime is the cost of the pre-generics approach: a type
// error that the compiler could have caught becomes a runtime failure.
func TestSumAnyFailsAtRuntime(t *testing.T) {
	_, err := sumAny([]any{1, "not an int", 3})

	if err == nil {
		t.Fatal("expected a type error")
	}
	if !strings.Contains(err.Error(), "string") {
		t.Errorf("err = %q, want it to name the offending type", err)
	}
	// The generic and concrete versions cannot be called this way at all:
	// sumGeneric([]any{...}) does not compile, because any is not in Number.
}

func TestJoinImplementationsAgree(t *testing.T) {
	labels := []label{{1, "alpha"}, {2, "beta"}}

	generic := joinGeneric(labels, ", ")

	asInterfaces := make([]Stringish, len(labels))
	for i, l := range labels {
		asInterfaces[i] = l
	}
	viaInterface := joinInterface(asInterfaces, ", ")

	if generic != viaInterface {
		t.Errorf("generic = %q, interface = %q", generic, viaInterface)
	}
	if want := "1:alpha, 2:beta"; generic != want {
		t.Errorf("got %q, want %q", generic, want)
	}
}

func TestCostDocsArePresent(t *testing.T) {
	if got := typesShareAShape(); len(got) < 4 {
		t.Errorf("expected at least 4 documented shapes, got %d", len(got))
	}
	if got := whenGenericsCostSomething(); len(got) < 4 {
		t.Errorf("expected at least 4 documented cases, got %d", len(got))
	}
}

// The benchmarks behind the README's claims about cost. Run:
//
//	go test -bench . -benchmem -run '^$' ./11-generics
//
// Three comparisons, each isolating one thing:
//
//	Sum*     generic vs concrete vs any, over ints (one gcshape, no dictionary)
//	Join*    generic vs interface, with a method call (the dictionary path)
//	Set*     a generic container vs a hand-written map

var benchValues = func() []int {
	v := make([]int, 1000)
	for i := range v {
		v[i] = i
	}
	return v
}()

var benchAnyValues = func() []any {
	v := make([]any, 1000)
	for i := range v {
		v[i] = i
	}
	return v
}()

func BenchmarkSumConcrete(b *testing.B) {
	for b.Loop() {
		_ = sumInts(benchValues)
	}
}

func BenchmarkSumGeneric(b *testing.B) {
	for b.Loop() {
		_ = sumGeneric(benchValues)
	}
}

func BenchmarkSumAny(b *testing.B) {
	for b.Loop() {
		_, _ = sumAny(benchAnyValues)
	}
}

var benchLabels = func() []label {
	l := make([]label, 100)
	for i := range l {
		l[i] = label{id: i, text: "item"}
	}
	return l
}()

var benchStringish = func() []Stringish {
	s := make([]Stringish, len(benchLabels))
	for i, l := range benchLabels {
		s[i] = l
	}
	return s
}()

func BenchmarkJoinGeneric(b *testing.B) {
	for b.Loop() {
		_ = joinGeneric(benchLabels, ",")
	}
}

func BenchmarkJoinInterface(b *testing.B) {
	for b.Loop() {
		_ = joinInterface(benchStringish, ",")
	}
}

// The container pair: a generic Set against the hand-written map it replaces.
// If generics cost anything here, this is where it would show.

func BenchmarkSetGeneric(b *testing.B) {
	for b.Loop() {
		s := make(Set[int], 1000)
		for i := 0; i < 1000; i++ {
			s.Add(i)
		}
		for i := 0; i < 1000; i++ {
			_ = s.Has(i)
		}
	}
}

func BenchmarkSetHandWritten(b *testing.B) {
	for b.Loop() {
		s := make(map[int]struct{}, 1000)
		for i := 0; i < 1000; i++ {
			s[i] = struct{}{}
		}
		for i := 0; i < 1000; i++ {
			_, _ = s[i]
		}
	}
}

// And the pointer case, where the dictionary is unavoidable: every pointer type
// shares one gcshape, so the instantiation cannot specialise.

func BenchmarkStackOfPointers(b *testing.B) {
	products := make([]*Product, 100)
	for i := range products {
		products[i] = &Product{SKU: "A", Title: "T"}
	}

	for b.Loop() {
		s := NewStack[*Product](100)
		for _, p := range products {
			s.Push(p)
		}
		for range products {
			_, _ = s.Pop()
		}
	}
}

func BenchmarkStackOfInts(b *testing.B) {
	for b.Loop() {
		s := NewStack[int](100)
		for i := 0; i < 100; i++ {
			s.Push(i)
		}
		for i := 0; i < 100; i++ {
			_, _ = s.Pop()
		}
	}
}

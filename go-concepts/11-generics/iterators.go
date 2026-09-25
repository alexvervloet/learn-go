package main

import (
	"cmp"
	"fmt"
	"iter"
	"maps"
	"slices"
)

// Iterators: range over function
// ==============================
//
// Go 1.23 added range-over-func, which is generics plus a compiler rewrite.
// Two types, both in the `iter` package:
//
//	type Seq[V any]     func(yield func(V) bool)
//	type Seq2[K, V any] func(yield func(K, V) bool)
//
// Writing `for v := range mySeq` compiles into a call to mySeq with a yield
// function the compiler builds from the loop body.
//
// THE CONTRACT: yield returns false when the consumer has stopped (a break, a
// return, a panic). The producer MUST stop calling yield and return. Ignoring
// that is the one way to get this wrong, and it turns `break` into "keep going".

// Count produces the integers [0, n). The simplest possible Seq.
func Count(n int) iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := 0; i < n; i++ {
			if !yield(i) {
				return // the consumer stopped; so do we
			}
		}
	}
}

// CountBroken ignores yield's return value, which breaks the contract.
//
// THE RUNTIME CATCHES THIS, and the behaviour is better than the obvious guess.
// Calling yield again after it has returned false panics:
//
//	panic: runtime error: range function continued iteration after
//	       function for loop body returned false
//
// So a producer that ignores the contract does not silently waste work or
// quietly keep going: it crashes at the second bad call, with a message naming
// exactly what went wrong. The compiler rewrites the loop body into a yield
// function that tracks whether the loop has exited, and the generated code
// checks on every call.
//
// That is a good trade. A panic during development beats an iterator that keeps
// reading files after its consumer has stopped.
func CountBroken(n int, work func(int)) iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := 0; i < n; i++ {
			work(i)
			yield(i) //nolint:errcheck,gocritic // ignoring the result is the bug being demonstrated
		}
	}
}

// capturePanic runs fn and returns the panic message, or "" if it returned.
func capturePanic(fn func()) (message string) {
	defer func() {
		if r := recover(); r != nil {
			message = fmt.Sprint(r)
		}
	}()

	fn()
	return ""
}

// Filter wraps a Seq, passing through only what matches. Note that it forwards
// yield's result, which is what makes a break propagate through the chain.
func Filter[T any](seq iter.Seq[T], keep func(T) bool) iter.Seq[T] {
	return func(yield func(T) bool) {
		for v := range seq {
			if !keep(v) {
				continue
			}
			if !yield(v) {
				return
			}
		}
	}
}

// MapSeq transforms each element. T and U again, so it is a function.
func MapSeq[T, U any](seq iter.Seq[T], f func(T) U) iter.Seq[U] {
	return func(yield func(U) bool) {
		for v := range seq {
			if !yield(f(v)) {
				return
			}
		}
	}
}

// Take limits a sequence to the first n elements, which is how you consume an
// infinite one safely.
func Take[T any](seq iter.Seq[T], n int) iter.Seq[T] {
	return func(yield func(T) bool) {
		if n <= 0 {
			return
		}
		count := 0
		for v := range seq {
			if !yield(v) {
				return
			}
			count++
			if count >= n {
				return
			}
		}
	}
}

// Naturals is infinite. It terminates only because the consumer stops, which is
// the property that makes the yield contract load-bearing rather than tidy.
func Naturals() iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := 1; ; i++ {
			if !yield(i) {
				return
			}
		}
	}
}

// Enumerate is a Seq2: it yields an index alongside each value, which is what
// range over a slice already does and what range over a Seq does not.
func Enumerate[T any](seq iter.Seq[T]) iter.Seq2[int, T] {
	return func(yield func(int, T) bool) {
		i := 0
		for v := range seq {
			if !yield(i, v) {
				return
			}
			i++
		}
	}
}

// SortedByValue is a Seq2 over a map, in descending value order. Iterating a
// map in a useful order is the everyday case this makes pleasant.
func SortedByValue[K comparable, V cmp.Ordered](m map[K]V) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		keys := slices.Collect(maps.Keys(m))

		slices.SortFunc(keys, func(a, b K) int {
			// Descending by value, then by key so ties are deterministic.
			if c := cmp.Compare(m[b], m[a]); c != 0 {
				return c
			}
			return 0
		})

		for _, k := range keys {
			if !yield(k, m[k]) {
				return
			}
		}
	}
}

// Pull converts a push iterator into a pull one, for when you need to advance
// two sequences in step. iter.Pull returns a next function and a stop function.
//
// The stop MUST be called, or the goroutine iter.Pull runs the producer on is
// leaked. This is the one sharp edge in the iterator API.
func Zip[A, B any](a iter.Seq[A], b iter.Seq[B]) iter.Seq2[A, B] {
	return func(yield func(A, B) bool) {
		nextA, stopA := iter.Pull(a)
		defer stopA()

		nextB, stopB := iter.Pull(b)
		defer stopB()

		for {
			va, okA := nextA()
			vb, okB := nextB()
			if !okA || !okB {
				return // stop at the shorter of the two
			}
			if !yield(va, vb) {
				return
			}
		}
	}
}

// collectSeq drains a Seq into a slice. slices.Collect does this in the
// standard library; this exists so the demo can show what it is doing.
func collectSeq[T any](seq iter.Seq[T]) []T {
	var out []T
	for v := range seq {
		out = append(out, v)
	}
	return out
}

// demoIterators prints iterator behaviour.
func demoIterators() {
	fmt.Printf("  Count(5):                    %v\n", collectSeq(Count(5)))
	fmt.Printf("  slices.Collect(Count(5)):    %v   (the stdlib version)\n", slices.Collect(Count(5)))

	evens := Filter(Count(10), func(v int) bool { return v%2 == 0 })
	fmt.Printf("  Filter(Count(10), even):     %v\n", collectSeq(evens))

	squares := MapSeq(Count(5), func(v int) int { return v * v })
	fmt.Printf("  MapSeq(Count(5), square):    %v\n", collectSeq(squares))

	fmt.Printf("  Take(Naturals(), 5):         %v   <- an infinite sequence, stopped by the consumer\n",
		collectSeq(Take(Naturals(), 5)))

	chained := Take(Filter(MapSeq(Naturals(), func(v int) int { return v * 3 }),
		func(v int) bool { return v%2 == 0 }), 4)
	fmt.Printf("  chained over an infinite seq: %v\n", collectSeq(chained))

	fmt.Println("\n  break propagates through the chain:")
	count := 0
	for v := range Filter(Count(1000), func(v int) bool { return v%7 == 0 }) {
		count++
		if v > 20 {
			break
		}
	}
	fmt.Printf("    consumed %d values out of 1000 before breaking\n", count)

	work := 0
	msg := capturePanic(func() {
		for range CountBroken(100, func(int) { work++ }) {
			break
		}
	})
	fmt.Printf("    a producer that ignores yield's result computes %d element(s), then:\n", work)
	fmt.Printf("      panic: %s\n", msg)

	fmt.Println("\n  Seq2 yields pairs:")
	for i, v := range Enumerate(MapSeq(Count(3), func(v int) string {
		return fmt.Sprintf("item-%d", v)
	})) {
		fmt.Printf("    %d -> %s\n", i, v)
	}

	scores := map[string]int{"ana": 92, "bo": 78, "cy": 95, "di": 78}
	fmt.Println("  map sorted by value, descending:")
	for name, score := range SortedByValue(scores) {
		fmt.Printf("    %-4s %d\n", name, score)
	}

	fmt.Println("  Zip of two sequences:")
	names := slices.Values([]string{"go", "rust", "zig"})
	for name, n := range Zip(names, Naturals()) {
		fmt.Printf("    %d. %s\n", n, name)
	}
}

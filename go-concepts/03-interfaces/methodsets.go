package main

import (
	"fmt"
	"strings"
)

// Method sets
// ===========
//
// A method's receiver decides which types carry it:
//
//	func (t T)  ValueMethod()    -> in the method set of BOTH T and *T
//	func (t *T) PointerMethod()  -> in the method set of *T ONLY
//
// So this compiles:      var p *T = &T{}; var i Iface = p
// And this does not:     var v T;         var i Iface = v      (if Iface needs PointerMethod)
//
// The reason is addressability. Calling a pointer method needs the address of
// the receiver. Go can take the address of a variable, which is why `v.Ptr()`
// works as a direct call (the compiler rewrites it to `(&v).Ptr()`). It cannot
// take the address of a value already copied into an interface, so the
// conversion is rejected at compile time rather than silently mutating a copy.

// Tally accumulates counts. Add must have a pointer receiver because it
// mutates; Total does not mutate and could have either.
type Tally struct {
	count int
	items []string
}

// Add has a POINTER receiver: it mutates the Tally.
func (t *Tally) Add(item string) {
	t.count++
	t.items = append(t.items, item)
}

// Total has a VALUE receiver purely to demonstrate a mixed set. In real code,
// pick one kind per type and use it for every method. Mixed receivers are a
// well-known Go smell for exactly the confusion this file is about.
func (t Tally) Total() int { return t.count }

// String has a VALUE receiver, so both Tally and *Tally are fmt.Stringers.
func (t Tally) String() string {
	return fmt.Sprintf("%d: %s", t.count, strings.Join(t.items, ","))
}

// Adder needs the pointer-receiver method, so only *Tally satisfies it.
type Adder interface {
	Add(item string)
}

// Totaler needs the value-receiver method, so both Tally and *Tally satisfy it.
type Totaler interface {
	Total() int
}

// These compile-time assertions document the rule precisely. The commented line
// is the one that does not build, with the compiler's actual message:
//
//	var _ Adder = Tally{}
//	  -> cannot use Tally{} (value of struct type Tally) as Adder value:
//	     Tally does not implement Adder (method Add has pointer receiver)
var (
	_ Adder   = (*Tally)(nil) // pointer satisfies the pointer-receiver interface
	_ Totaler = Tally{}       // value satisfies the value-receiver interface
	_ Totaler = (*Tally)(nil) // and so does the pointer
)

// directCallOnAValueWorks shows the addressability rule from the other side. A
// pointer method called directly on an addressable variable compiles fine,
// because the compiler inserts the &. Only the INTERFACE conversion is refused.
func directCallOnAValueWorks() (total int, str string) {
	var t Tally // a value, not a pointer

	t.Add("a") // compiles: rewritten to (&t).Add("a")
	t.Add("b")

	return t.Total(), t.String()
}

// valueReceiverGetsACopy is the bug mixed receivers invite. Because Total has a
// value receiver, it operates on a copy. That is harmless for reading. The same
// mistake on a mutating method silently discards the mutation, which is why a
// mutating method must never have a value receiver.
//
// brokenAdd is what that looks like.
type brokenTally struct{ count int }

// brokenAdd mutates a COPY. The caller sees nothing change.
//
// staticcheck reports SA4005 here ("ineffective assignment to field
// brokenTally.count"), which is the bug, found by a linter, for free.
//
//nolint:staticcheck // SA4005 is correct; the ineffective write is the lesson
func (t brokenTally) brokenAdd() { t.count++ }

// workingAdd mutates the original.
func (t *brokenTally) workingAdd() { t.count++ }

func valueReceiverGetsACopy() (afterBroken, afterWorking int) {
	var a brokenTally
	a.brokenAdd()
	a.brokenAdd()

	var b brokenTally
	b.workingAdd()
	b.workingAdd()

	return a.count, b.count
}

// sliceOfValuesCannotSatisfy is the practical form the rule takes. A
// []Tally cannot be converted to []Adder at all (Go never converts slice
// element types), and even one element at a time each value would be rejected.
// Store pointers when the elements need pointer-receiver methods.
func sliceOfPointersSatisfies() []Adder {
	tallies := []*Tally{{}, {}}

	out := make([]Adder, 0, len(tallies))
	for _, t := range tallies {
		out = append(out, t) // *Tally is an Adder
	}
	for i, a := range out {
		a.Add(fmt.Sprintf("item-%d", i))
	}
	return out
}

// demoMethodSets prints each rule with its result.
func demoMethodSets() {
	total, str := directCallOnAValueWorks()
	fmt.Printf("  pointer method on an addressable value: total=%d, String()=%q\n", total, str)

	broken, working := valueReceiverGetsACopy()
	fmt.Printf("  two calls to a VALUE-receiver mutator:   count=%d (both lost)\n", broken)
	fmt.Printf("  two calls to a POINTER-receiver mutator: count=%d\n", working)

	adders := sliceOfPointersSatisfies()
	fmt.Printf("  []*Tally converted element-wise to []Adder: %d adders\n", len(adders))
	for _, a := range adders {
		if t, ok := a.(*Tally); ok {
			fmt.Printf("    %v\n", t)
		}
	}

	fmt.Println("  `var _ Adder = Tally{}` does not compile: Add has a pointer receiver")
}

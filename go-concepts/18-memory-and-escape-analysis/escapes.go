// Package main is lesson 18 of go-concepts: memory and escape analysis.
//
// The compiler decides, per value, whether it lives on the stack (free,
// reclaimed on return) or the heap (allocated, later collected). The rule,
// roughly:
//
//	a value escapes when the compiler cannot prove its lifetime ends with
//	the function.
//
// See the decisions with:
//
//	go build -gcflags=-m ./18-memory-and-escape-analysis
package main

import (
	"fmt"
	"strings"
)

// User is the value moved around below. Deliberately small and pointer-free,
// so the only reason it ever escapes is how it is used.
type User struct {
	ID    int64
	Score float64
	Admin bool
}

// Case 1: returning a pointer versus a value
// ------------------------------------------

// newUserPointer ESCAPES. The caller outlives this frame, so u cannot be on
// the stack.
//
//	./escapes.go:NN:9: &User{...} escapes to heap
func newUserPointer(id int64) *User {
	return &User{ID: id, Score: float64(id)}
}

// newUserValue does NOT escape. The struct is copied into the caller's frame,
// which is a register-or-two move for something this small.
//
// Lesson 17 measured the pair: 7.9 ns and one allocation against 1.8 ns and
// none.
func newUserValue(id int64) User {
	return User{ID: id, Score: float64(id)}
}

// Case 2: a pointer that stays local
// ----------------------------------

// pointerThatStaysLocal does not escape, even though it takes an address. The
// compiler can see that u's lifetime ends here.
func pointerThatStaysLocal(id int64) float64 {
	u := User{ID: id, Score: float64(id)}

	p := &u // the address is taken, and never leaves
	p.Score *= 2

	return u.Score
}

// pointerStoredGlobally escapes: the package-level variable outlives every
// call, so anything it points at must too.
var globalUser *User

func pointerStoredGlobally(id int64) {
	u := User{ID: id}
	globalUser = &u // moved to heap: u
}

// Case 3: interfaces box their contents
// -------------------------------------
//
// Putting a value in an `any` (or any interface) needs a pointer to the data,
// so the value escapes. This is why lesson 17 measured boxing 1000 integers at
// 745 allocations, and why a generic function over a type set beats the `any`
// version.

// The rule, measured rather than repeated:
//
//	BOXING ALLOCATES WHEN THE INTERFACE VALUE ESCAPES.
//
// Not when boxing happens. Escape analysis applies to the box itself, so an
// `any` that stays inside the callee's frame is stack-allocated like anything
// else. Measured with testing.AllocsPerRun:
//
//	boxed, callee inlined and devirtualised     0 allocations
//	boxed, callee NOT inlined, box not escaping 0 allocations
//	boxed and stored in a package variable      1 allocation
//	boxed and appended to a slice               1 allocation
//
// "Passing a value to an interface allocates" is the folklore, and it is only
// true for the last two. This took four attempts to get right; see LESSONS.md.
//
// One more trap sits underneath all of it: a composite literal whose fields are
// ALL CONSTANTS, such as User{ID: 2}, can be emitted as a static value, and
// then nothing allocates in any of the four cases. Measuring boxing needs a
// value the compiler cannot fold, which is why the test derives one from a
// variable.

// passedAsAny does NOT allocate. idFromAny inlines, the compiler sees the
// concrete type at the call site, devirtualises the assertion, and the boxing
// disappears entirely.
func passedAsAny(u User) int64 {
	return idFromAny(u)
}

// idFromAny is small enough to inline.
func idFromAny(v any) int64 {
	if u, ok := v.(User); ok {
		return u.ID
	}
	return 0
}

// passedAsAnyNoInline also does NOT allocate, which is the surprising one.
// The callee is out of line, so the boxing is real, but escape analysis proves
// the interface value never leaves idFromAnyNoInline's frame, so the box lives
// on the stack.
func passedAsAnyNoInline(u User) int64 {
	return idFromAnyNoInline(u)
}

// idFromAnyNoInline is kept out of line so the call cannot be devirtualised.
// //go:noinline is a blunt instrument that belongs in benchmarks and not in
// production code; it is here because it is the only way to isolate the effect.
//
//go:noinline
func idFromAnyNoInline(v any) int64 {
	if u, ok := v.(User); ok {
		return u.ID
	}
	return 0
}

// boxedValues is where an escaping interface value ends up.
var boxedValues []any

// passedAsAnyThatEscapes DOES allocate: the interface value outlives the call,
// so the box must be on the heap.
//
// This is the case the folklore is describing, and it is narrower than the
// folklore suggests.
func passedAsAnyThatEscapes(u User) {
	storeAny(u)
}

// storeAny keeps its argument, which is what makes the box escape.
//
//go:noinline
func storeAny(v any) {
	boxedValues = append(boxedValues[:0], v)
}

// passedConcretely does not escape: the parameter is a copy in the callee's
// frame and the compiler knows its type.
func passedConcretely(u User) int64 {
	return idFromUser(u)
}

// idFromUser takes the concrete type, so there is nothing to box.
func idFromUser(u User) int64 { return u.ID }

// describeUser and describeAny are for the demo's output, and are deliberately
// NOT the benchmark pair: one builds a string with strings.Repeat and the
// other uses fmt.Sprint, so they do different work. Benchmarking them against
// each other was lesson 17's first lie, committed here before it was caught.
func describeUser(u User) string {
	return "user " + strings.Repeat("x", int(u.ID%3))
}

func describeAny(v any) string { return fmt.Sprint(v) }

// Case 4: closures
// ----------------

// closureCalledImmediately does not escape. The closure and everything it
// captures die with the frame.
func closureCalledImmediately(n int) int {
	total := 0

	add := func(v int) { total += v } // total stays on the stack
	for i := 0; i < n; i++ {
		add(i)
	}

	return total
}

// closureReturned escapes: the returned function outlives this frame, so
// everything it captures must be on the heap.
func closureReturned() func() int {
	count := 0 // moved to heap: count

	return func() int {
		count++
		return count
	}
}

// closureInAGoroutine escapes, and NOT in the way the obvious reading suggests.
//
// `-gcflags='-m -m'` says:
//
//	func literal escapes to heap in closureInAGoroutine
//	closureInAGoroutine capturing by value: value (addr=false assign=false)
//
// The FUNC LITERAL is heap-allocated, because the goroutine outlives this
// frame. `value` is captured BY VALUE, copied into the closure, and never
// moved to the heap as a variable in its own right.
//
// The distinction matters when the captured thing is large or is mutated: a
// variable the closure ASSIGNS to is captured by reference and does move.
func closureInAGoroutine(done chan<- int) {
	value := 42 // captured by value, copied into the closure

	go func() { done <- value }()
}

// closureThatMutatesCapturesByReference is the contrast. Because the closure
// assigns to counter, the variable itself moves to the heap:
//
//	moved to heap: counter
func closureThatMutatesCapturesByReference(done chan<- int) {
	counter := 0 // moved to heap: the closure assigns to it

	go func() {
		counter++
		done <- counter
	}()
}

// Case 5: slice sizing
// --------------------

// makeWithConstantSize does not escape: the compiler knows the size at compile
// time and it is small enough to fit in the frame.
func makeWithConstantSize() int {
	buf := make([]byte, 64) // stays on the stack
	for i := range buf {
		buf[i] = byte(i)
	}
	return len(buf)
}

// makeWithVariableSize is the case I got wrong before measuring it, and it is
// the most interesting one here.
//
// "make with a non-constant size escapes" is the folklore, and it is not what
// happens. Measured with testing.AllocsPerRun:
//
//	makeWithVariableSize(64)      0 allocations
//	makeWithVariableSize(100000)  1 allocation
//
// The function is small enough to inline, so after inlining the compiler can
// see the ACTUAL argument at each call site. Where it can prove the size is
// small, the slice stays in the caller's frame. Where it cannot, it escapes.
//
// So escape analysis is not a property of this function. It is a property of
// this function AT EACH CALL SITE, and inlining is what makes that possible.
// `go build -gcflags=-l` (inlining off) makes it escape unconditionally.
func makeWithVariableSize(n int) int {
	buf := make([]byte, n) // escapes only when n cannot be shown to be small
	for i := range buf {
		buf[i] = byte(i)
	}
	return len(buf)
}

// makeTooLargeForTheStack escapes even with a constant size, because the frame
// would be enormous. The threshold is an implementation detail (currently
// around 64KB for a slice) and not something to design around.
func makeTooLargeForTheStack() int {
	buf := make([]byte, 1<<20) // 1MB: escapes to heap
	return len(buf)
}

// Case 6: channels
// ----------------

// sendPointerOnChannel escapes: the receiver is another goroutine with its own
// lifetime.
func sendPointerOnChannel(ch chan<- *User, id int64) {
	u := User{ID: id} // moved to heap: u
	ch <- &u
}

// sendValueOnChannel copies into the channel's buffer, so the local does not
// escape. For a small struct this is cheaper as well as simpler.
func sendValueOnChannel(ch chan<- User, id int64) {
	u := User{ID: id}
	ch <- u
}

// escapeCauses is the checklist.
func escapeCauses() []string {
	return []string{
		"returning &x, or storing it anywhere longer-lived",
		"putting a value in an interface AND letting that interface value escape",
		"capturing a variable in a closure that is returned or started as a goroutine",
		"make with a size the compiler cannot show is small (see makeWithVariableSize)",
		"sending a pointer on a channel",
		"anything the compiler cannot see through: a call via an interface, or reflection",
	}
}

// whatStaysOnTheStack is the other half, and the more useful one.
func whatStaysOnTheStack() []string {
	return []string{
		"a local whose address is never taken",
		"a local whose address IS taken but never leaves the frame",
		"a small struct returned by value",
		"make([]T, n) where inlining lets the compiler see n is small",
		"a closure called in place, or passed to a function that does not keep it",
		"a value boxed into an interface that stays inside the callee frame",
		"a value passed to a concrete (non-interface) parameter",
	}
}

// demoEscapes prints the cases and their costs.
func demoEscapes() {
	fmt.Println("  each pair below does the same job, and allocates differently:")

	ptr := newUserPointer(1)
	val := newUserValue(1)
	fmt.Printf("    newUserPointer -> %+v (heap)\n", *ptr)
	fmt.Printf("    newUserValue   -> %+v (stack)\n", val)

	fmt.Printf("    pointerThatStaysLocal(5) = %.0f, and does NOT escape\n", pointerThatStaysLocal(5))

	pointerStoredGlobally(9)
	fmt.Printf("    pointerStoredGlobally: the global now points at id %d (heap)\n", globalUser.ID)

	fmt.Printf("    passedAsAny(id 2):         %d  inlined and devirtualised: 0 allocs\n",
		passedAsAny(User{ID: 2}))
	fmt.Printf("    passedAsAnyNoInline(id 2): %d  boxed, box does not escape: 0 allocs\n",
		passedAsAnyNoInline(User{ID: 2}))
	passedAsAnyThatEscapes(User{ID: 2})
	fmt.Printf("    passedAsAnyThatEscapes:    %d  the box escapes: 1 alloc\n",
		boxedValues[0].(User).ID)
	fmt.Printf("    passedConcretely(id 2):    %d  nothing to box: 0 allocs\n",
		passedConcretely(User{ID: 2}))
	fmt.Printf("    describeUser:  %q\n", describeUser(User{ID: 2}))
	fmt.Printf("    describeAny:   %s\n", describeAny(User{ID: 2}))

	fmt.Printf("    closureCalledImmediately(100) = %d (stack)\n", closureCalledImmediately(100))
	counter := closureReturned()
	fmt.Printf("    closureReturned: %d, %d, %d (heap)\n", counter(), counter(), counter())

	fmt.Printf("    make([]byte, 64)        -> %d bytes, stack\n", makeWithConstantSize())
	fmt.Printf("    make([]byte, n) n=64    -> %d bytes, STACK (inlined, n proven small)\n",
		makeWithVariableSize(64))
	fmt.Printf("    make([]byte, n) n=1e5   -> %d bytes, heap\n", makeWithVariableSize(100_000))
	fmt.Printf("    make([]byte, 1MB)       -> %d bytes, heap\n", makeTooLargeForTheStack())

	fmt.Println("\n  what escapes:")
	for _, s := range escapeCauses() {
		fmt.Printf("    %s\n", s)
	}

	fmt.Println("\n  what stays on the stack:")
	for _, s := range whatStaysOnTheStack() {
		fmt.Printf("    %s\n", s)
	}
}

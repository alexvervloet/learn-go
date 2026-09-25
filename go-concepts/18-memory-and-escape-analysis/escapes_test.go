package main

import (
	"strings"
	"testing"
)

// testing.AllocsPerRun is the right tool for asserting an escape. It runs the
// function the given number of times and returns the average allocations,
// which turns "does this escape?" from a reading of -gcflags=-m into a test
// that fails when someone changes it.
//
// It returns a float64 because it is an average; for a deterministic function
// it is a whole number.

func TestValueDoesNotEscapeButPointerDoes(t *testing.T) {
	ptrAllocs := testing.AllocsPerRun(100, func() {
		sinkUserPtr = newUserPointer(1)
	})
	valAllocs := testing.AllocsPerRun(100, func() {
		sinkUser = newUserValue(1)
	})

	t.Logf("newUserPointer: %.1f allocs, newUserValue: %.1f allocs", ptrAllocs, valAllocs)

	if ptrAllocs < 1 {
		t.Errorf("newUserPointer allocated %.1f times, want at least 1", ptrAllocs)
	}
	if valAllocs != 0 {
		t.Errorf("newUserValue allocated %.1f times, want 0", valAllocs)
	}
}

func TestPointerThatStaysLocalDoesNotEscape(t *testing.T) {
	allocs := testing.AllocsPerRun(100, func() {
		sinkFloat = pointerThatStaysLocal(5)
	})

	if allocs != 0 {
		t.Errorf("allocated %.1f times, want 0 — the address never leaves the frame", allocs)
	}
}

func TestPointerThatStaysLocalIsCorrect(t *testing.T) {
	if got := pointerThatStaysLocal(5); got != 10 {
		t.Errorf("got %v, want 10", got)
	}
}

// TestBoxingAllocatesOnlyWhenTheBoxEscapes is the correction this lesson took
// three attempts to get right.
//
// "Passing a value to an interface allocates" is standard advice. Measured, it
// is true only when the interface value itself escapes. Escape analysis applies
// to the box like anything else.
func TestBoxingAllocatesOnlyWhenTheBoxEscapes(t *testing.T) {
	// The value must NOT be a constant composite literal. `User{ID: 2}` has
	// only constant fields, so the compiler can emit it as a static value with
	// nothing to allocate, and every measurement below comes back as zero.
	//
	// That cost an attempt to work out. Deriving the value from a variable the
	// compiler cannot fold is what makes the comparison measure boxing.
	n := int64(2)
	if testing.Short() {
		n = 3
	}
	u := User{ID: n, Score: float64(n)}

	inlined := testing.AllocsPerRun(100, func() {
		sinkID = passedAsAny(u)
	})
	notInlined := testing.AllocsPerRun(100, func() {
		sinkID = passedAsAnyNoInline(u)
	})
	escaping := testing.AllocsPerRun(100, func() {
		passedAsAnyThatEscapes(u)
	})
	concrete := testing.AllocsPerRun(100, func() {
		sinkID = passedConcretely(u)
	})

	t.Logf("inlined: %.1f, not inlined: %.1f, escaping: %.1f, concrete: %.1f",
		inlined, notInlined, escaping, concrete)

	if inlined != 0 {
		t.Errorf("an inlined interface call allocated %.1f times, want 0 — devirtualisation removes it",
			inlined)
	}
	if notInlined != 0 {
		t.Errorf("an out-of-line interface call allocated %.1f times, want 0 — the box does not escape",
			notInlined)
	}
	if escaping < 1 {
		t.Errorf("an ESCAPING interface value allocated %.1f times, want at least 1", escaping)
	}
	if concrete != 0 {
		t.Errorf("the concrete call allocated %.1f times, want 0", concrete)
	}
}

// TestBothPathsReturnTheSameAnswer is the guard that makes the pair above a
// fair comparison: they must do the same job.
func TestBoxingPairAgrees(t *testing.T) {
	for _, id := range []int64{0, 1, 42, -7} {
		u := User{ID: id}
		if passedAsAny(u) != passedConcretely(u) || passedAsAnyNoInline(u) != passedConcretely(u) {
			t.Errorf("id %d: the three paths disagree", id)
		}

		passedAsAnyThatEscapes(u)
		if got := boxedValues[0].(User).ID; got != id {
			t.Errorf("the escaping path stored id %d, want %d", got, id)
		}
	}
}

func TestClosureEscapeBehaviour(t *testing.T) {
	immediate := testing.AllocsPerRun(100, func() {
		sinkIntValue = closureCalledImmediately(100)
	})

	if immediate != 0 {
		t.Errorf("an immediately-called closure allocated %.1f times, want 0", immediate)
	}

	returned := testing.AllocsPerRun(100, func() {
		sinkFunc = closureReturned()
	})

	if returned < 1 {
		t.Errorf("a returned closure allocated %.1f times, want at least 1", returned)
	}
}

func TestClosureReturnedKeepsItsState(t *testing.T) {
	counter := closureReturned()

	for want := 1; want <= 3; want++ {
		if got := counter(); got != want {
			t.Errorf("call %d returned %d", want, got)
		}
	}

	// A second closure has its own state.
	other := closureReturned()
	if got := other(); got != 1 {
		t.Errorf("a fresh closure started at %d, want 1", got)
	}
}

// TestMakeEscapeDependsOnTheCallSite is the correction this lesson needed.
//
// "make with a non-constant size escapes" is the folklore. What actually
// happens is that the function inlines, the compiler sees the caller's actual
// argument, and the slice stays on the stack when it can be shown to be small.
func TestMakeEscapeDependsOnTheCallSite(t *testing.T) {
	constantSize := testing.AllocsPerRun(100, func() {
		sinkIntValue = makeWithConstantSize()
	})
	smallVariable := testing.AllocsPerRun(100, func() {
		sinkIntValue = makeWithVariableSize(64)
	})
	largeVariable := testing.AllocsPerRun(100, func() {
		sinkIntValue = makeWithVariableSize(100_000)
	})
	tooLarge := testing.AllocsPerRun(10, func() {
		sinkIntValue = makeTooLargeForTheStack()
	})

	t.Logf("constant 64: %.1f, variable 64: %.1f, variable 100000: %.1f, constant 1MB: %.1f",
		constantSize, smallVariable, largeVariable, tooLarge)

	if constantSize != 0 {
		t.Errorf("make([]byte, 64) allocated %.1f times, want 0", constantSize)
	}
	if smallVariable != 0 {
		t.Errorf("make([]byte, n) with n=64 allocated %.1f times, want 0 — inlining should keep it on the stack",
			smallVariable)
	}
	if largeVariable < 1 {
		t.Errorf("make([]byte, n) with n=100000 allocated %.1f times, want at least 1", largeVariable)
	}
	if tooLarge < 1 {
		t.Errorf("make([]byte, 1MB) allocated %.1f times, want at least 1", tooLarge)
	}
}

func TestSendingOnAChannel(t *testing.T) {
	ptrCh := make(chan *User, 100)
	valCh := make(chan User, 100)

	ptrAllocs := testing.AllocsPerRun(50, func() {
		sendPointerOnChannel(ptrCh, 1)
		<-ptrCh
	})
	valAllocs := testing.AllocsPerRun(50, func() {
		sendValueOnChannel(valCh, 1)
		<-valCh
	})

	t.Logf("pointer: %.1f allocs, value: %.1f allocs", ptrAllocs, valAllocs)

	if ptrAllocs < 1 {
		t.Errorf("sending a pointer allocated %.1f times, want at least 1", ptrAllocs)
	}
	if valAllocs != 0 {
		t.Errorf("sending a value allocated %.1f times, want 0", valAllocs)
	}
}

func TestGlobalStorageEscapes(t *testing.T) {
	pointerStoredGlobally(9)

	if globalUser == nil {
		t.Fatal("the global was not set")
	}
	if globalUser.ID != 9 {
		t.Errorf("globalUser.ID = %d, want 9", globalUser.ID)
	}
}

func TestClosureCaptureModes(t *testing.T) {
	// Captured by value: the goroutine receives the copy.
	byValue := make(chan int, 1)
	closureInAGoroutine(byValue)
	if got := <-byValue; got != 42 {
		t.Errorf("got %d, want 42", got)
	}

	// Captured by reference, because the closure assigns to it.
	byReference := make(chan int, 1)
	closureThatMutatesCapturesByReference(byReference)
	if got := <-byReference; got != 1 {
		t.Errorf("got %d, want 1", got)
	}
}

func TestEscapeDocsArePresent(t *testing.T) {
	if got := escapeCauses(); len(got) < 5 {
		t.Errorf("expected at least 5 causes, got %d", len(got))
	}
	if got := whatStaysOnTheStack(); len(got) < 5 {
		t.Errorf("expected at least 5 cases, got %d", len(got))
	}
}

func TestDescribeUser(t *testing.T) {
	tests := []struct {
		id   int64
		want string
	}{
		{0, "user "},
		{1, "user x"},
		{2, "user xx"},
		{3, "user "},
	}

	for _, tt := range tests {
		if got := describeUser(User{ID: tt.id}); got != tt.want {
			t.Errorf("id %d: got %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestDescribeAny(t *testing.T) {
	if got := describeAny(User{ID: 1}); !strings.Contains(got, "1") {
		t.Errorf("got %q, want it to mention the id", got)
	}
}

// Benchmarks for the pairs. Run:
//
//	go test -bench . -benchmem -run '^$' ./18-memory-and-escape-analysis

func BenchmarkNewUserPointer(b *testing.B) {
	for b.Loop() {
		sinkUserPtr = newUserPointer(1)
	}
}

func BenchmarkNewUserValue(b *testing.B) {
	for b.Loop() {
		sinkUser = newUserValue(1)
	}
}

func BenchmarkPassedAsAny(b *testing.B) {
	u := User{ID: 2}
	for b.Loop() {
		sinkID = passedAsAny(u)
	}
}

func BenchmarkPassedAsAnyNoInline(b *testing.B) {
	u := User{ID: 2}
	for b.Loop() {
		sinkID = passedAsAnyNoInline(u)
	}
}

func BenchmarkPassedAsAnyThatEscapes(b *testing.B) {
	u := User{ID: 2}
	for b.Loop() {
		passedAsAnyThatEscapes(u)
	}
}

func BenchmarkPassedConcretely(b *testing.B) {
	u := User{ID: 2}
	for b.Loop() {
		sinkID = passedConcretely(u)
	}
}

func BenchmarkMakeSmallVariable(b *testing.B) {
	for b.Loop() {
		sinkIntValue = makeWithVariableSize(64)
	}
}

func BenchmarkMakeLargeVariable(b *testing.B) {
	for b.Loop() {
		sinkIntValue = makeWithVariableSize(100_000)
	}
}

package main

import (
	"slices"
	"strings"
	"testing"
)

// TestGrowthImplementationsAgree is lie number one's defence: the pair must
// produce identical output, or the benchmark compares nothing.
func TestGrowthImplementationsAgree(t *testing.T) {
	for _, n := range []int{0, 1, 10, 1000} {
		unbounded := growUnbounded(n)
		presized := growPresized(n)

		if !slices.Equal(unbounded, presized) {
			t.Errorf("n=%d: growUnbounded and growPresized disagree", n)
		}
	}
}

// TestExtraWorkVersionProducesTheSamePrimaryOutput is why the bug survives a
// casual check: the values match, and the work does not.
func TestExtraWorkVersionIsNotComparable(t *testing.T) {
	const n = 100

	plain := growUnbounded(n)
	withExtra, caps := growUnboundedWithExtraWork(n)

	if !slices.Equal(plain, withExtra) {
		t.Error("the primary output should be identical, which is what makes this trap work")
	}
	if len(caps) != n {
		t.Errorf("the extra version did %d additional appends, want %d", len(caps), n)
	}
	// The point: identical output, different work. A benchmark pairing these
	// two would attribute the extra appends to the growth strategy.
}

func TestPresizedNeverReallocates(t *testing.T) {
	const n = 1000

	got := growPresized(n)

	if len(got) != n {
		t.Errorf("len = %d, want %d", len(got), n)
	}
	if cap(got) != n {
		t.Errorf("cap = %d, want %d — a correctly sized make should never grow", cap(got), n)
	}
}

func TestExpensiveComputationIsDeterministic(t *testing.T) {
	// A benchmark of a function that is not deterministic measures the input,
	// not the code.
	first := expensiveComputation(1000)
	for i := 0; i < 5; i++ {
		if got := expensiveComputation(1000); got != first {
			t.Fatalf("run %d gave %d, first gave %d", i, got, first)
		}
	}
}

func TestLieDocsArePresent(t *testing.T) {
	if got := theThreeLies(); len(got) != 3 {
		t.Errorf("expected exactly 3 lies, got %d", len(got))
	}
	if got := warningSigns(); len(got) < 4 {
		t.Errorf("expected at least 4 warning signs, got %d", len(got))
	}
	if got := benchstatWorkflow(); len(got) < 5 {
		t.Errorf("expected at least 5 workflow steps, got %d", len(got))
	}
}

// BenchmarkEliminated demonstrates lie number two. Without the sink, the
// compiler can remove the call entirely, and the result is a sub-nanosecond
// number that is not a measurement of anything.
//
// Compare with BenchmarkNotEliminated below. Both use b.Loop, which is opaque
// to the optimiser, so on Go 1.24+ the difference is smaller than it used to
// be. The sink is still the habit to keep, because the old loop form is still
// common and the guarantee is easier to reason about than the optimiser.
func BenchmarkEliminated(b *testing.B) {
	for b.Loop() {
		_ = expensiveComputation(100) // the result is discarded
	}
}

func BenchmarkNotEliminated(b *testing.B) {
	for b.Loop() {
		sinkInt = expensiveComputation(100) // the result escapes to a package variable
	}
}

// The growth pair, correctly matched.
func BenchmarkGrowUnbounded(b *testing.B) {
	for b.Loop() {
		sinkSlice = growUnbounded(1000)
	}
}

func BenchmarkGrowPresized(b *testing.B) {
	for b.Loop() {
		sinkSlice = growPresized(1000)
	}
}

// BenchmarkGrowUnboundedWithExtraWork is the UNFAIR one, kept so the numbers
// can be compared with BenchmarkGrowUnbounded and the inflation seen directly.
func BenchmarkGrowUnboundedWithExtraWork(b *testing.B) {
	for b.Loop() {
		values, caps := growUnboundedWithExtraWork(1000)
		sinkSlice = values
		sinkInt = len(caps)
	}
}

func TestSinksAreUsed(t *testing.T) {
	// A guard that the sinks are genuinely package-level and writable, since
	// the benchmarks above depend on that to prevent elimination.
	sinkInt = 1
	sinkString = strings.Repeat("x", 1)
	sinkSlice = []int{1}

	if sinkInt != 1 || sinkString != "x" || len(sinkSlice) != 1 {
		t.Error("the sinks are not behaving as package-level variables")
	}
}

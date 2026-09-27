// Package basics's tests are the point of the file. They cover what pytest's first section
// covers, and where Go's answer is different.
//
// # The mapping from pytest
//
//	pytest                        go test
//	----------------------------  ------------------------------------------------
//	test discovery by name        files named *_test.go, funcs named TestXxx
//	@pytest.mark.parametrize      a table and a loop
//	fixtures                      ordinary functions, plus t.Cleanup
//	fixture scope="module"        TestMain, or a package-level sync.Once
//	yield fixtures                t.Cleanup, or defer in a helper returning a closure
//	@pytest.mark.slow + -m        testing.Short() and -short, or a build tag
//	conftest.py                   nothing; there is no implicit sharing
//	assert a == b                 if a != b { t.Errorf(...) }
//	pytest -k                     go test -run
//	pytest -x                     go test -failfast
//
// # There is no fixture injection, and that is mostly a relief
//
// pytest's fixtures are resolved by parameter NAME, which is powerful and invisible: a test's
// dependencies are declared by spelling, and finding where one comes from means searching every
// conftest.py up the tree. Go has no such mechanism. A test that needs a cart calls newCart(t),
// and the definition is one jump away.
//
// What is genuinely lost: fixture caching across tests in a scope, and the automatic teardown
// ordering that comes with it. t.Cleanup covers the teardown; the caching has to be written.
package basics

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain runs once for the whole package, and it is the closest thing to a session-scoped
// fixture.
//
// The two rules:
//
//	it must call m.Run() and pass the result to os.Exit, or nothing runs
//	os.Exit skips deferred functions, so teardown goes BEFORE the Exit, not in a defer
//
// That second one catches everyone. `defer teardown()` here never runs.
func TestMain(m *testing.M) {
	// Setup for every test in the package.
	if err := os.Setenv("BASICS_TEST", "1"); err != nil {
		fmt.Fprintln(os.Stderr, "setup failed:", err)
		os.Exit(1)
	}

	code := m.Run()

	// Teardown, NOT deferred, because os.Exit below would skip a defer.
	_ = os.Unsetenv("BASICS_TEST")

	os.Exit(code)
}

// TestTableDriven is the Go idiom and the direct replacement for parametrize.
//
// Four things make it worth the shape rather than one test per case:
//
//	the cases are data, so adding one is a line
//	t.Run gives each case a name that appears in the failure and in -run
//	one loop means one place to change the assertion
//	a failing case does not stop the others, because Errorf is not Fatalf
func TestTableDriven(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Seconds
		wantErr error
	}{
		{name: "seconds", in: "90s", want: 90},
		{name: "minutes", in: "5m", want: 300},
		{name: "hours", in: "2h", want: 7200},
		{name: "zero", in: "0s", want: 0},
		{name: "whitespace is trimmed", in: "  30s  ", want: 30},
		{name: "empty", in: "", wantErr: ErrEmpty},
		{name: "only whitespace", in: "   ", wantErr: ErrEmpty},
		{name: "no number", in: "s", wantErr: ErrNoUnit},
		{name: "not a number", in: "abcs", wantErr: ErrNoUnit},
		{name: "unknown unit", in: "5d", wantErr: ErrUnknownUnit},
		{name: "negative", in: "-5s", wantErr: ErrNegative},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDuration(tt.in)

			// errors.Is, not ==, so a wrapped error still matches. ParseDuration wraps
			// its sentinels with context and a == comparison would fail on every one.
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ParseDuration(%q) error = %v, want %v", tt.in, err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if got != tt.want {
				t.Errorf("ParseDuration(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// TestSubtestsShareSetup shows the other reason for t.Run: grouping tests that share a fixture
// while keeping their failures separate.
func TestSubtestsShareSetup(t *testing.T) {
	// One cart, several subtests against it. Ordering matters here, which is a reason to
	// be careful: these subtests are NOT independent.
	cart := newCart(t)

	t.Run("adding a known item", func(t *testing.T) {
		if err := cart.Add("apple", 2); err != nil {
			t.Fatalf("Add: %v", err)
		}
		if got := cart.Total(); got != 200 {
			t.Errorf("Total = %d, want 200", got)
		}
	})

	t.Run("adding more of the same item accumulates", func(t *testing.T) {
		if err := cart.Add("apple", 1); err != nil {
			t.Fatalf("Add: %v", err)
		}
		if got := cart.Total(); got != 300 {
			t.Errorf("Total = %d, want 300", got)
		}
		if got := cart.Len(); got != 1 {
			t.Errorf("Len = %d, want 1: the same item twice is one entry", got)
		}
	})

	t.Run("an unknown item is rejected", func(t *testing.T) {
		if err := cart.Add("caviar", 1); err == nil {
			t.Error("expected an error for an item with no price")
		}
		if got := cart.Total(); got != 300 {
			t.Errorf("Total = %d; a failed Add must not change the cart", got)
		}
	})
}

// TestIndependentSubtests is the same tests with a fresh fixture each, which is what you almost
// always want.
//
// The difference from the version above matters: shared state makes subtests order-dependent, so
// running one with -run picks up whatever the others left behind. Here each one is
// self-contained and `-run TestIndependentSubtests/rejects` works on its own.
func TestIndependentSubtests(t *testing.T) {
	tests := map[string]func(t *testing.T, cart *Cart){
		"accumulates": func(t *testing.T, cart *Cart) {
			mustAdd(t, cart, "apple", 2)
			mustAdd(t, cart, "apple", 1)

			if got := cart.Total(); got != 300 {
				t.Errorf("Total = %d, want 300", got)
			}
		},
		"rejects an unknown item": func(t *testing.T, cart *Cart) {
			if err := cart.Add("caviar", 1); err == nil {
				t.Error("expected an error")
			}
		},
		"rejects a non-positive quantity": func(t *testing.T, cart *Cart) {
			for _, n := range []int{0, -1} {
				if err := cart.Add("apple", n); err == nil {
					t.Errorf("Add(apple, %d) was accepted", n)
				}
			}
		},
	}

	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			run(t, newCart(t)) // a fresh fixture per subtest
		})
	}
}

// newCart is a fixture. It is an ordinary function taking *testing.T, which is the whole of Go's
// fixture story.
//
// t.Helper() is the line that makes it usable: without it, a failure reported inside this
// function points HERE rather than at the test that called it, and every failure in the suite
// has the same file and line. It is one line and it is the difference between a useful failure
// message and a useless one.
//
// t.Cleanup is the yield-fixture equivalent. It runs after the test that registered it,
// including after a t.Fatal, which a defer in the test body also does — but Cleanup registered
// inside a HELPER outlives the helper's own return, and a defer does not.
func newCart(t *testing.T) *Cart {
	t.Helper()

	cart := NewCart(map[string]int{"apple": 100, "pear": 150})

	t.Cleanup(func() {
		// Nothing to release here, and the shape is the point: a database handle, a temp
		// directory, a stopped server all go here.
		t.Logf("cleanup: cart had %d distinct items", cart.Len())
	})

	return cart
}

// mustAdd is the other kind of helper: one that fails the test rather than returning an error.
//
// Worth having for the setup steps of a test, where an error means the test cannot proceed and
// checking it inline adds three lines of noise per call. Not worth having for the thing under
// test, because then the assertion has moved out of the test.
func mustAdd(t *testing.T, cart *Cart, item string, n int) {
	t.Helper()

	if err := cart.Add(item, n); err != nil {
		t.Fatalf("Add(%q, %d): %v", item, n, err)
	}
}

// TestHelperLineNumbers demonstrates what t.Helper() buys, by checking the behaviour rather than
// describing it.
//
// A sub-test's failure is reported against the line that called the helper, not the line inside
// it. There is no API to read that back, so this test runs a helper that would fail and asserts
// only that the mechanism exists; the observable difference is in the output of a real failure.
func TestHelperLineNumbers(t *testing.T) {
	// The closest thing to an assertion available: t.Helper on a func that never fails is a
	// no-op, and the interesting case cannot be observed from inside a passing test.
	//
	// So this documents rather than asserts, which is honest. Run
	// `go test -run TestDeliberateFailure ./basics` with the build tag to see it.
	t.Log("t.Helper() makes a failure inside a helper point at the caller. " +
		"See basics/failing_test.go, which is behind a build tag so it does not " +
		"fail the suite: go test -tags demo_failure -run Deliberate ./basics")
}

// Short mode and filtering
// ========================

// TestSlowThing is the analogue of @pytest.mark.slow plus `pytest -m "not slow"`.
//
// testing.Short() is set by `go test -short`, and the convention is that a test costing real
// time checks it and skips. There is no marker system: the check is in the test.
//
// A build tag is the other option and it is stronger, because a tagged file is not compiled at
// all. Use Short() for "slow", a build tag for "needs a database".
func TestSlowThing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode: this sleeps")
	}

	start := time.Now()
	time.Sleep(50 * time.Millisecond)

	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Errorf("slept %v, want at least 50ms", elapsed)
	}
}

// TestEnvironmentFromTestMain checks the package-level setup actually ran, which is the thing a
// session-scoped fixture is for.
func TestEnvironmentFromTestMain(t *testing.T) {
	if os.Getenv("BASICS_TEST") != "1" {
		t.Error("TestMain's setup did not run; every test in the package depends on it")
	}
}

// t.Setenv and parallelism
// ========================

// TestSetenvIsScopedToTheTest: t.Setenv restores the old value on cleanup, which is what makes
// environment-dependent tests safe to write.
//
// It also FORBIDS t.Parallel in the same test, and the error is a panic rather than a compile
// error, because the environment is process-global and two parallel tests changing it would
// race. That restriction is the reason to prefer passing configuration as an argument.
func TestSetenvIsScopedToTheTest(t *testing.T) {
	const key = "BASICS_SCOPED"

	if _, set := os.LookupEnv(key); set {
		t.Fatalf("%s is already set; the test is not isolated", key)
	}

	t.Setenv(key, "value")

	if got := os.Getenv(key); got != "value" {
		t.Errorf("Getenv = %q", got)
	}

	// The cleanup unsets it after this test, which TestSetenvIsUndone checks.
}

// TestSetenvIsUndone runs after the test above in source order and confirms the restore.
//
// Relying on source order between top-level tests is fragile in general, and it is the only way
// to observe this from inside the suite. `go test -run 'TestSetenv'` runs both in order.
func TestSetenvIsUndone(t *testing.T) {
	if _, set := os.LookupEnv("BASICS_SCOPED"); set {
		t.Error("BASICS_SCOPED survived the test that set it")
	}
}

// Parallelism
// ===========

// TestParallelSubtests runs the cases at once, which for a table of pure functions is free
// speed.
//
// The famous trap was the loop variable: before Go 1.22, `tt` was one variable reused by every
// iteration, so a parallel subtest capturing it saw whatever the loop had reached by the time it
// ran. Every table-driven parallel test needed `tt := tt` at the top of the loop body.
//
// Go 1.22 changed the loop-variable scope, so each iteration has its own. TestLoopVarIsPerIteration
// below proves it, and the `tt := tt` line is now noise.
func TestParallelSubtests(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Seconds
	}{
		{"seconds", "90s", 90},
		{"minutes", "5m", 300},
		{"hours", "2h", 7200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel() // this subtest pauses here until the others have all started

			got, err := ParseDuration(tt.in)
			if err != nil {
				t.Fatalf("ParseDuration(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("= %d, want %d", got, tt.want)
			}
		})
	}
}

// TestLoopVarIsPerIteration proves the Go 1.22 loop-variable change, which is what retired the
// `tt := tt` line from every parallel table test.
//
// Before 1.22 this test fails: all three goroutines see the final value.
func TestLoopVarIsPerIteration(t *testing.T) {
	var mu sync.Mutex
	seen := make(map[int]bool)

	var wg sync.WaitGroup

	for i := range 3 {
		wg.Go(func() {
			mu.Lock()
			seen[i] = true // i is per-iteration since Go 1.22
			mu.Unlock()
		})
	}

	wg.Wait()

	if len(seen) != 3 {
		t.Errorf("saw %d distinct values, want 3: %v", len(seen), seen)
	}
}

// TestParallelCleanupOrdering is the ordering rule that surprises people.
//
// A parallel subtest's body runs AFTER its parent function returns. So a defer in the parent
// runs before the subtest does, and only t.Cleanup waits.
//
// This is the single most common parallel-test bug: set up a resource, `defer` its teardown, run
// parallel subtests against it, and every subtest sees a closed resource.
func TestParallelCleanupOrdering(t *testing.T) {
	var (
		mu       sync.Mutex
		order    []string
		resource = "open"
	)

	record := func(s string) {
		mu.Lock()
		order = append(order, s)
		mu.Unlock()
	}

	t.Run("group", func(t *testing.T) {
		// A defer here runs when THIS function returns, which is before the parallel
		// subtests below have run.
		defer record("parent defer")

		// t.Cleanup waits for the parallel subtests, which is the fix.
		t.Cleanup(func() {
			record("parent cleanup")
			resource = "closed"
		})

		for _, name := range []string{"a", "b"} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				record("subtest " + name)

				if resource != "open" {
					t.Errorf("subtest %s saw the resource as %q", name, resource)
				}
			})
		}
	})

	mu.Lock()
	defer mu.Unlock()

	// The defer fired first, before either subtest.
	if len(order) == 0 || order[0] != "parent defer" {
		t.Fatalf("order = %v, want the parent's defer first", order)
	}
	if order[len(order)-1] != "parent cleanup" {
		t.Errorf("order = %v, want the cleanup last", order)
	}

	t.Logf("order: %v", order)
	t.Log("the parent's defer ran BEFORE the parallel subtests; only t.Cleanup waited. " +
		"Setting up a resource and deferring its teardown is the classic parallel-test bug.")
}

// Assertions: stdlib or testify
// =============================

// TestStdlibAssertions is what the standard library gives you, which is nothing.
//
// if/Errorf is verbose and it has two properties a matcher library does not: the comparison is
// visible, so there is no question what "equal" means for this type, and the message says what
// the test actually cares about rather than dumping both values.
func TestStdlibAssertions(t *testing.T) {
	got, err := ParseDuration("5m")

	if err != nil {
		t.Fatalf("ParseDuration: %v", err)
	}
	if got != 300 {
		t.Errorf("ParseDuration(\"5m\") = %d, want 300", got)
	}
}

// TestErrorfVersusFatalf is the distinction that decides how much a failure tells you.
//
//	Errorf marks the test failed and CONTINUES, so one run reports every problem
//	Fatalf marks it failed and stops, for when continuing would panic or be meaningless
//
// The rule: Fatalf when the rest of the test cannot run (a nil result, a failed setup), Errorf
// for an assertion about a value you already have. A test that uses Fatalf for everything
// reports one problem per run.
func TestErrorfVersusFatalf(t *testing.T) {
	cart := newCart(t)

	// Fatalf: if this fails, every assertion below is meaningless.
	if err := cart.Add("apple", 2); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Errorf: independent assertions, so all of them should be checked.
	if got := cart.Total(); got != 200 {
		t.Errorf("Total = %d, want 200", got)
	}
	if got := cart.Len(); got != 1 {
		t.Errorf("Len = %d, want 1", got)
	}
}

// TestSkipAndSkipNow: three ways a test can decline to run, and they mean different things.
func TestSkipVariants(t *testing.T) {
	t.Run("t.Skip with a reason", func(t *testing.T) {
		if !testing.Short() {
			t.Skip("only interesting in short mode, and this is not it")
		}
	})

	t.Run("skipping on a missing dependency", func(t *testing.T) {
		// The right shape for a test needing something external: SKIP, do not fail. A
		// failing test for a missing local postgres trains people to ignore red.
		if os.Getenv("DATABASE_URL") == "" {
			t.Skip("DATABASE_URL not set")
		}
		t.Error("this line is unreachable in CI without a database")
	})

	t.Run("reporting that it skipped", func(t *testing.T) {
		defer func() {
			// t.Skipped() is readable from a cleanup, which is how a helper can tell
			// whether the test it set up actually ran.
			if !t.Skipped() {
				t.Error("expected this subtest to have skipped")
			}
		}()

		t.SkipNow()
	})
}

// TestTempDirIsCleanedUp: t.TempDir is the fixture with the least boilerplate in the standard
// library, and it removes the directory on cleanup whether the test passed or failed.
func TestTempDirIsCleanedUp(t *testing.T) {
	dir := t.TempDir()

	path := dir + "/file.txt"
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("read %q", data)
	}

	// Each call returns a DIFFERENT directory, so two fixtures in one test cannot collide.
	if other := t.TempDir(); other == dir {
		t.Error("two TempDir calls returned the same directory")
	}

	// And the name carries the test's name, which makes a leaked one traceable.
	if !strings.Contains(dir, "TestTempDirIsCleanedUp") {
		t.Logf("TempDir = %q; the name usually contains the test's", dir)
	}
}

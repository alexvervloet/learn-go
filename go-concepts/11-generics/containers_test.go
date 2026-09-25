package main

import (
	"errors"
	"slices"
	"sync"
	"testing"
)

func TestSet(t *testing.T) {
	s := NewSet(1, 2, 3, 2, 1)

	if s.Len() != 3 {
		t.Errorf("Len = %d, want 3 — duplicates should collapse", s.Len())
	}
	if !s.Has(1) || !s.Has(2) || !s.Has(3) {
		t.Error("all three values should be present")
	}
	if s.Has(9) {
		t.Error("9 should be absent")
	}

	s.Add(4)
	s.Add(4)
	if s.Len() != 4 {
		t.Errorf("Len = %d, want 4 — Add must be idempotent", s.Len())
	}

	s.Remove(4)
	s.Remove(99) // absent, must be a no-op
	if s.Len() != 3 {
		t.Errorf("Len = %d, want 3", s.Len())
	}
}

func TestSetOperations(t *testing.T) {
	a := NewSet(1, 2, 3, 4)
	b := NewSet(3, 4, 5, 6)

	tests := []struct {
		name string
		got  []int
		want []int
	}{
		{"union", SortedValues(a.Union(b)), []int{1, 2, 3, 4, 5, 6}},
		{"intersection", SortedValues(a.Intersection(b)), []int{3, 4}},
		{"a minus b", SortedValues(a.Difference(b)), []int{1, 2}},
		{"b minus a", SortedValues(b.Difference(a)), []int{5, 6}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !slices.Equal(tt.got, tt.want) {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}

	// The operations must not mutate their inputs.
	if a.Len() != 4 || b.Len() != 4 {
		t.Errorf("inputs were mutated: a=%d b=%d, want 4 and 4", a.Len(), b.Len())
	}
}

// TestIntersectionIteratesTheSmallerSet is a property worth pinning, because
// the optimisation is easy to lose in a refactor and the results must match
// whichever way round the arguments go.
func TestIntersectionIsSymmetric(t *testing.T) {
	small := NewSet(1, 2)
	large := NewSet(1, 2, 3, 4, 5, 6, 7, 8, 9, 10)

	forward := SortedValues(small.Intersection(large))
	backward := SortedValues(large.Intersection(small))

	if !slices.Equal(forward, backward) {
		t.Errorf("a∩b = %v but b∩a = %v", forward, backward)
	}
	if want := []int{1, 2}; !slices.Equal(forward, want) {
		t.Errorf("got %v, want %v", forward, want)
	}
}

func TestSetOfStructs(t *testing.T) {
	s := NewSet(Point{1, 2}, Point{3, 4}, Point{1, 2})

	if s.Len() != 2 {
		t.Errorf("Len = %d, want 2", s.Len())
	}
	if !s.Has(Point{1, 2}) {
		t.Error("an equal struct should be found")
	}
}

func TestEmptySet(t *testing.T) {
	s := NewSet[string]()

	if s.Len() != 0 {
		t.Errorf("Len = %d, want 0", s.Len())
	}
	if got := SortedValues(s); len(got) != 0 {
		t.Errorf("Values = %v, want empty", got)
	}
	if got := SortedValues(s.Union(NewSet("a"))); !slices.Equal(got, []string{"a"}) {
		t.Errorf("union with empty = %v", got)
	}
}

func TestResult(t *testing.T) {
	boom := errors.New("not found")

	ok := Ok(42)
	bad := Err[int](boom)

	if !ok.IsOk() {
		t.Error("Ok should report IsOk")
	}
	if bad.IsOk() {
		t.Error("Err should not report IsOk")
	}

	v, err := ok.Unwrap()
	if v != 42 || err != nil {
		t.Errorf("Unwrap = %d, %v", v, err)
	}

	_, err = bad.Unwrap()
	if !errors.Is(err, boom) {
		t.Errorf("Unwrap err = %v, want %v", err, boom)
	}

	if got := ok.ValueOr(-1); got != 42 {
		t.Errorf("ValueOr on Ok = %d, want 42", got)
	}
	if got := bad.ValueOr(-1); got != -1 {
		t.Errorf("ValueOr on Err = %d, want -1", got)
	}
}

func TestMapResult(t *testing.T) {
	boom := errors.New("boom")

	t.Run("transforms a success", func(t *testing.T) {
		got := MapResult(Ok(21), func(v int) int { return v * 2 })
		if v, _ := got.Unwrap(); v != 42 {
			t.Errorf("got %d, want 42", v)
		}
	})

	t.Run("passes a failure through without calling f", func(t *testing.T) {
		called := false
		got := MapResult(Err[int](boom), func(v int) int { called = true; return v })

		if called {
			t.Error("f should not be called on a failed Result")
		}
		if _, err := got.Unwrap(); !errors.Is(err, boom) {
			t.Errorf("err = %v, want %v", err, boom)
		}
	})
}

func TestCollectResults(t *testing.T) {
	first := errors.New("first")
	second := errors.New("second")

	t.Run("all succeed", func(t *testing.T) {
		values, err := CollectResults([]Result[int]{Ok(1), Ok(2), Ok(3)})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := []int{1, 2, 3}; !slices.Equal(values, want) {
			t.Errorf("got %v, want %v", values, want)
		}
	})

	t.Run("collects every error", func(t *testing.T) {
		values, err := CollectResults([]Result[int]{Ok(1), Err[int](first), Ok(3), Err[int](second)})

		if err == nil {
			t.Fatal("expected an error")
		}
		if !errors.Is(err, first) || !errors.Is(err, second) {
			t.Errorf("err = %v, want it to contain both failures", err)
		}
		// The successes survive.
		if want := []int{1, 3}; !slices.Equal(values, want) {
			t.Errorf("values = %v, want %v", values, want)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		values, err := CollectResults([]Result[int](nil))
		if err != nil {
			t.Errorf("err = %v, want nil", err)
		}
		if len(values) != 0 {
			t.Errorf("values = %v, want empty", values)
		}
	})
}

// TestOptionalDistinguishesZeroFromAbsent is the reason Optional exists at all.
func TestOptionalDistinguishesZeroFromAbsent(t *testing.T) {
	present := Some(0)
	absent := None[int]()

	v, ok := present.Get()
	if !ok || v != 0 {
		t.Errorf("Some(0).Get() = %d, %t; want 0, true", v, ok)
	}

	v, ok = absent.Get()
	if ok || v != 0 {
		t.Errorf("None.Get() = %d, %t; want 0, false", v, ok)
	}

	if got := present.OrElse(-1); got != 0 {
		t.Errorf("Some(0).OrElse(-1) = %d, want 0 — present and zero is not absent", got)
	}
	if got := absent.OrElse(-1); got != -1 {
		t.Errorf("None.OrElse(-1) = %d, want -1", got)
	}

	if got := present.String(); got != "Some(0)" {
		t.Errorf("String = %q", got)
	}
	if got := absent.String(); got != "None" {
		t.Errorf("String = %q", got)
	}
}

func TestCache(t *testing.T) {
	c := NewCache[string, int]()

	if _, ok := c.Get("missing"); ok {
		t.Error("an empty cache should report a miss")
	}

	c.Set("a", 1)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Errorf("Get = %d, %t", v, ok)
	}
	if c.Len() != 1 {
		t.Errorf("Len = %d, want 1", c.Len())
	}
}

// TestCacheGetOrComputeRunsOnce is lesson 09's double-check, now generic.
func TestCacheGetOrComputeRunsOnce(t *testing.T) {
	c := NewCache[string, int]()

	var (
		mu       sync.Mutex
		computed int
		wg       sync.WaitGroup
	)

	for i := 0; i < 200; i++ {
		wg.Go(func() {
			v, _ := c.GetOrCompute("key", func() int {
				mu.Lock()
				computed++
				mu.Unlock()
				return 99
			})
			if v != 99 {
				t.Errorf("got %d, want 99", v)
			}
		})
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	if computed != 1 {
		t.Errorf("computed %d times, want exactly 1", computed)
	}
	if c.Len() != 1 {
		t.Errorf("Len = %d, want 1", c.Len())
	}
}

func TestCacheWithStructValues(t *testing.T) {
	c := NewCache[int, Product]()

	c.Set(1, Product{SKU: "A1", Title: "Keyboard"})

	p, ok := c.Get(1)
	if !ok || p.Title != "Keyboard" {
		t.Errorf("Get = %+v, %t", p, ok)
	}
}

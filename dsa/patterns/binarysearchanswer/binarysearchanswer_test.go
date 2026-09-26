package binarysearchanswer

import (
	"errors"
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

func TestSearch(t *testing.T) {
	tests := []struct {
		name     string
		lo, hi   int
		feasible func(int) bool
		want     int
		err      bool
	}{
		{"boundary in the middle", 1, 100, func(x int) bool { return x >= 42 }, 42, false},
		{"everything works", 1, 10, func(int) bool { return true }, 1, false},
		{"nothing works", 1, 10, func(int) bool { return false }, 0, true},
		{"only the top works", 1, 10, func(x int) bool { return x >= 10 }, 10, false},
		{"single value", 5, 5, func(x int) bool { return x >= 5 }, 5, false},
		{"single value, infeasible", 5, 5, func(int) bool { return false }, 0, true},
		{"inverted range", 10, 5, func(int) bool { return true }, 0, true},
		{"negative bounds", -100, 100, func(x int) bool { return x >= -7 }, -7, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Search(tt.lo, tt.hi, tt.feasible)

			if tt.err {
				if !errors.Is(err, ErrImpossible) {
					t.Errorf("err = %v, want ErrImpossible (got %d)", err, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Search = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSearchMax(t *testing.T) {
	tests := []struct {
		name     string
		lo, hi   int
		feasible func(int) bool
		want     int
		err      bool
	}{
		{"boundary in the middle", 1, 100, func(x int) bool { return x <= 42 }, 42, false},
		{"everything works", 1, 10, func(int) bool { return true }, 10, false},
		{"nothing works", 1, 10, func(int) bool { return false }, 0, true},
		{"only the bottom works", 1, 10, func(x int) bool { return x <= 1 }, 1, false},
		{"single value", 5, 5, func(x int) bool { return x <= 5 }, 5, false},
		{"inverted range", 10, 5, func(int) bool { return true }, 0, true},
		{"negative bounds", -100, 100, func(x int) bool { return x <= -7 }, -7, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SearchMax(tt.lo, tt.hi, tt.feasible)

			if tt.err {
				if !errors.Is(err, ErrImpossible) {
					t.Errorf("err = %v, want ErrImpossible (got %d)", err, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("SearchMax = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestSearchMatchesLinearScan: bisection is only correct when the predicate is monotonic,
// so the oracle is the obvious linear scan over the same predicate.
func TestSearchMatchesLinearScan(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))

	for range 5000 {
		lo := r.IntN(40) - 20
		hi := lo + r.IntN(40)
		boundary := lo - 5 + r.IntN(50)

		rising := func(x int) bool { return x >= boundary }
		falling := func(x int) bool { return x <= boundary }

		// Minimum, by scanning.
		wantMin, foundMin := 0, false
		for x := lo; x <= hi; x++ {
			if rising(x) {
				wantMin, foundMin = x, true
				break
			}
		}

		gotMin, err := Search(lo, hi, rising)
		if foundMin != (err == nil) {
			t.Fatalf("lo=%d hi=%d boundary=%d: Search err = %v, scan found = %v",
				lo, hi, boundary, err, foundMin)
		}
		if foundMin && gotMin != wantMin {
			t.Fatalf("lo=%d hi=%d boundary=%d: Search = %d, want %d", lo, hi, boundary, gotMin, wantMin)
		}

		// Maximum, by scanning backwards.
		wantMax, foundMax := 0, false
		for x := hi; x >= lo; x-- {
			if falling(x) {
				wantMax, foundMax = x, true
				break
			}
		}

		gotMax, err := SearchMax(lo, hi, falling)
		if foundMax != (err == nil) {
			t.Fatalf("lo=%d hi=%d boundary=%d: SearchMax err = %v, scan found = %v",
				lo, hi, boundary, err, foundMax)
		}
		if foundMax && gotMax != wantMax {
			t.Fatalf("lo=%d hi=%d boundary=%d: SearchMax = %d, want %d", lo, hi, boundary, gotMax, wantMax)
		}
	}
}

func TestMinEatingSpeed(t *testing.T) {
	tests := []struct {
		piles []int
		hours int
		want  int
		err   bool
	}{
		{[]int{3, 6, 7, 11}, 8, 4, false},
		{[]int{30, 11, 23, 4, 20}, 5, 30, false},
		{[]int{30, 11, 23, 4, 20}, 6, 23, false},
		{[]int{1}, 1, 1, false},
		{[]int{1, 1, 1}, 2, 0, true}, // fewer hours than piles
		{nil, 5, 0, true},
		{[]int{100}, 100, 1, false},
	}

	for _, tt := range tests {
		got, err := MinEatingSpeed(tt.piles, tt.hours)

		if tt.err {
			if err == nil {
				t.Errorf("MinEatingSpeed(%v, %d) = %d, want an error", tt.piles, tt.hours, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("MinEatingSpeed(%v, %d) failed: %v", tt.piles, tt.hours, err)
			continue
		}
		if got != tt.want {
			t.Errorf("MinEatingSpeed(%v, %d) = %d, want %d", tt.piles, tt.hours, got, tt.want)
		}
	}
}

// hoursAt is the simulation, written independently so the test does not just re-run the
// implementation.
func hoursAt(piles []int, speed int) int {
	total := 0
	for _, p := range piles {
		total += int(math.Ceil(float64(p) / float64(speed)))
	}
	return total
}

// TestMinEatingSpeedIsTheBoundary checks the two properties that define a minimum: the
// answer works and the value below it does not. That is stronger than comparing against a
// number, because it verifies the boundary rather than a memorised result.
func TestMinEatingSpeedIsTheBoundary(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))

	for range 3000 {
		n := 1 + r.IntN(8)
		piles := make([]int, n)
		for i := range piles {
			piles[i] = 1 + r.IntN(30)
		}
		hours := n + r.IntN(15)

		got, err := MinEatingSpeed(piles, hours)
		if err != nil {
			t.Fatalf("MinEatingSpeed(%v, %d) failed: %v", piles, hours, err)
		}

		if hoursAt(piles, got) > hours {
			t.Fatalf("MinEatingSpeed(%v, %d) = %d, which takes %d hours",
				piles, hours, got, hoursAt(piles, got))
		}
		if got > 1 && hoursAt(piles, got-1) <= hours {
			t.Fatalf("MinEatingSpeed(%v, %d) = %d, but %d also works",
				piles, hours, got, got-1)
		}
	}
}

func TestMinShipCapacity(t *testing.T) {
	tests := []struct {
		weights []int
		days    int
		want    int
	}{
		{[]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 5, 15},
		{[]int{3, 2, 2, 4, 1, 4}, 3, 6},
		{[]int{1, 2, 3, 1, 1}, 4, 3},
		{[]int{10}, 1, 10},
		{[]int{1, 1, 1}, 3, 1},
		{[]int{1, 1, 1}, 1, 3},
	}

	for _, tt := range tests {
		got, err := MinShipCapacity(tt.weights, tt.days)
		if err != nil {
			t.Errorf("MinShipCapacity(%v, %d) failed: %v", tt.weights, tt.days, err)
			continue
		}
		if got != tt.want {
			t.Errorf("MinShipCapacity(%v, %d) = %d, want %d", tt.weights, tt.days, got, tt.want)
		}
	}

	if _, err := MinShipCapacity(nil, 1); err == nil {
		t.Error("expected an error for no packages")
	}
	if _, err := MinShipCapacity([]int{1}, 0); err == nil {
		t.Error("expected an error for zero days")
	}
}

func daysNeeded(weights []int, capacity int) int {
	used, load := 1, 0
	for _, w := range weights {
		if load+w > capacity {
			used++
			load = 0
		}
		load += w
	}
	return used
}

func TestMinShipCapacityIsTheBoundary(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 7))

	for range 3000 {
		n := 1 + r.IntN(10)
		weights := make([]int, n)
		for i := range weights {
			weights[i] = 1 + r.IntN(20)
		}
		days := 1 + r.IntN(n)

		got, err := MinShipCapacity(weights, days)
		if err != nil {
			t.Fatalf("MinShipCapacity(%v, %d) failed: %v", weights, days, err)
		}

		if daysNeeded(weights, got) > days {
			t.Fatalf("capacity %d takes %d days, want at most %d", got, daysNeeded(weights, got), days)
		}
		if got > slices.Max(weights) && daysNeeded(weights, got-1) <= days {
			t.Fatalf("capacity %d works but %d was returned", got-1, got)
		}
	}
}

// TestTheseAreAllTheSameProblem is the point of collecting them. Ship capacity, array
// splitting and workload balancing are one question with three stories.
func TestTheseAreAllTheSameProblem(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 11))

	for range 3000 {
		n := 1 + r.IntN(10)
		values := make([]int, n)
		for i := range values {
			values[i] = 1 + r.IntN(20)
		}
		k := 1 + r.IntN(n)

		ship, err1 := MinShipCapacity(values, k)
		split, err2 := SplitArrayLargestSum(values, k)
		workload, err3 := MinMaxWorkload(values, k)

		if err1 != nil || err2 != nil || err3 != nil {
			t.Fatalf("values=%v k=%d: errors %v %v %v", values, k, err1, err2, err3)
		}
		if ship != split || split != workload {
			t.Fatalf("values=%v k=%d: ship %d, split %d, workload %d", values, k, ship, split, workload)
		}
	}
}

func TestSplitArrayLargestSum(t *testing.T) {
	tests := []struct {
		nums []int
		k    int
		want int
		err  bool
	}{
		{[]int{7, 2, 5, 10, 8}, 2, 18, false},
		{[]int{1, 2, 3, 4, 5}, 2, 9, false},
		{[]int{1, 4, 4}, 3, 4, false},
		{[]int{1, 2, 3}, 1, 6, false},
		{[]int{5}, 1, 5, false},
		{[]int{1, 2}, 3, 0, true}, // more parts than elements
		{[]int{1, 2}, 0, 0, true},
		{nil, 1, 0, true},
	}

	for _, tt := range tests {
		got, err := SplitArrayLargestSum(tt.nums, tt.k)

		if tt.err {
			if err == nil {
				t.Errorf("SplitArrayLargestSum(%v, %d) = %d, want an error", tt.nums, tt.k, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("SplitArrayLargestSum(%v, %d) failed: %v", tt.nums, tt.k, err)
			continue
		}
		if got != tt.want {
			t.Errorf("SplitArrayLargestSum(%v, %d) = %d, want %d", tt.nums, tt.k, got, tt.want)
		}
	}
}

// TestSplitArrayMatchesBruteForce: the exponential version enumerates every way to cut the
// array, which is the definition of the answer.
func TestSplitArrayMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(13, 17))

	for range 2000 {
		n := 1 + r.IntN(9)
		nums := make([]int, n)
		for i := range nums {
			nums[i] = 1 + r.IntN(12)
		}
		k := 1 + r.IntN(n)

		// Every way to place k-1 cuts among n-1 gaps.
		best := math.MaxInt
		var cut func(at, partsLeft, current, largest int)
		cut = func(at, partsLeft, current, largest int) {
			if at == n {
				if partsLeft == 1 {
					best = min(best, max(largest, current))
				}
				return
			}
			// Extend the current part.
			cut(at+1, partsLeft, current+nums[at], largest)
			// Or close it and start a new one, if any are left.
			if partsLeft > 1 && current > 0 {
				cut(at+1, partsLeft-1, nums[at], max(largest, current))
			}
		}
		cut(0, k, 0, 0)

		got, err := SplitArrayLargestSum(nums, k)
		if err != nil {
			t.Fatalf("SplitArrayLargestSum(%v, %d) failed: %v", nums, k, err)
		}
		if got != best {
			t.Fatalf("SplitArrayLargestSum(%v, %d) = %d, brute force says %d", nums, k, got, best)
		}
	}
}

func TestMaxMinDistance(t *testing.T) {
	tests := []struct {
		positions []int
		count     int
		want      int
		err       bool
	}{
		{[]int{1, 2, 4, 8, 9}, 3, 3, false},
		{[]int{1, 2, 3, 4, 7}, 3, 3, false},
		{[]int{0, 10}, 2, 10, false},
		{[]int{1, 2, 3}, 3, 1, false},
		{[]int{1, 2, 3}, 4, 0, true}, // more items than positions
		{[]int{1, 2, 3}, 1, 0, true}, // one item has no closest pair
		{nil, 2, 0, true},
	}

	for _, tt := range tests {
		got, err := MaxMinDistance(tt.positions, tt.count)

		if tt.err {
			if err == nil {
				t.Errorf("MaxMinDistance(%v, %d) = %d, want an error", tt.positions, tt.count, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("MaxMinDistance(%v, %d) failed: %v", tt.positions, tt.count, err)
			continue
		}
		if got != tt.want {
			t.Errorf("MaxMinDistance(%v, %d) = %d, want %d", tt.positions, tt.count, got, tt.want)
		}
	}
}

// TestMaxMinDistanceMatchesBruteForce enumerates every placement, which is what the answer
// means.
func TestMaxMinDistanceMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(19, 23))

	for range 2000 {
		n := 2 + r.IntN(8)

		seen := map[int]bool{}
		var positions []int
		for len(positions) < n {
			p := r.IntN(25)
			if seen[p] {
				continue
			}
			seen[p] = true
			positions = append(positions, p)
		}
		slices.Sort(positions)

		count := 2 + r.IntN(n-1)

		// Every subset of the right size, and the best closest-pair distance among them.
		best := -1
		for mask := 0; mask < 1<<n; mask++ {
			var chosen []int
			for i := range positions {
				if mask&(1<<i) != 0 {
					chosen = append(chosen, positions[i])
				}
			}
			if len(chosen) != count {
				continue
			}

			closest := math.MaxInt
			for i := 1; i < len(chosen); i++ {
				closest = min(closest, chosen[i]-chosen[i-1])
			}
			best = max(best, closest)
		}

		got, err := MaxMinDistance(positions, count)
		if err != nil {
			t.Fatalf("MaxMinDistance(%v, %d) failed: %v", positions, count, err)
		}
		if got != best {
			t.Fatalf("MaxMinDistance(%v, %d) = %d, brute force says %d", positions, count, got, best)
		}
	}
}

func TestMinDaysForBouquets(t *testing.T) {
	tests := []struct {
		bloom      []int
		bouquets   int
		perBouquet int
		want       int
		err        bool
	}{
		{[]int{1, 10, 3, 10, 2}, 3, 1, 3, false},
		{[]int{1, 10, 3, 10, 2}, 3, 2, 0, true}, // needs 6 flowers, only 5 exist
		{[]int{7, 7, 7, 7, 12, 7, 7}, 2, 3, 12, false},
		{[]int{1, 10, 2, 9, 3, 8, 4, 7, 5, 6}, 4, 2, 9, false},
		{[]int{1}, 1, 1, 1, false},
		{[]int{1, 2, 3}, 0, 1, 0, true},
		{[]int{1, 2, 3}, 1, 0, 0, true},
	}

	for _, tt := range tests {
		got, err := MinDaysForBouquets(tt.bloom, tt.bouquets, tt.perBouquet)

		if tt.err {
			if err == nil {
				t.Errorf("MinDaysForBouquets(%v, %d, %d) = %d, want an error",
					tt.bloom, tt.bouquets, tt.perBouquet, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("MinDaysForBouquets(%v, %d, %d) failed: %v",
				tt.bloom, tt.bouquets, tt.perBouquet, err)
			continue
		}
		if got != tt.want {
			t.Errorf("MinDaysForBouquets(%v, %d, %d) = %d, want %d",
				tt.bloom, tt.bouquets, tt.perBouquet, got, tt.want)
		}
	}
}

func TestIntegerSquareRoot(t *testing.T) {
	for n := range 200 {
		got, err := IntegerSquareRoot(n)
		if err != nil {
			t.Fatalf("IntegerSquareRoot(%d) failed: %v", n, err)
		}

		if got*got > n {
			t.Errorf("IntegerSquareRoot(%d) = %d, whose square is too big", n, got)
		}
		if (got+1)*(got+1) <= n {
			t.Errorf("IntegerSquareRoot(%d) = %d, but %d also fits", n, got, got+1)
		}
	}

	// Large values, where x*x would overflow and x <= n/x does not.
	for _, n := range []int{1 << 40, 1<<62 - 1, math.MaxInt} {
		got, err := IntegerSquareRoot(n)
		if err != nil {
			t.Fatalf("IntegerSquareRoot(%d) failed: %v", n, err)
		}
		if got > n/got {
			t.Errorf("IntegerSquareRoot(%d) = %d, too large", n, got)
		}
		if got+1 <= n/(got+1) {
			t.Errorf("IntegerSquareRoot(%d) = %d, but %d also fits", n, got, got+1)
		}
	}

	if _, err := IntegerSquareRoot(-1); err == nil {
		t.Error("expected an error for a negative input")
	}
}

// TestSearchIsLogarithmic: counting predicate calls is exact and needs no clock. A range of
// a billion must cost about 30 calls, not a billion.
func TestSearchIsLogarithmic(t *testing.T) {
	for _, hi := range []int{100, 1_000_000, 1_000_000_000} {
		calls := 0

		got, err := Search(1, hi, func(x int) bool {
			calls++
			return x >= hi/2
		})
		if err != nil {
			t.Fatal(err)
		}
		if got != hi/2 {
			t.Errorf("Search over [1,%d] = %d, want %d", hi, got, hi/2)
		}

		limit := 2
		for 1<<limit < hi {
			limit++
		}
		limit += 2

		if calls > limit {
			t.Errorf("range of %d took %d calls, want at most %d", hi, calls, limit)
		}
		t.Logf("range of %-13d %2d predicate calls", hi, calls)
	}
}

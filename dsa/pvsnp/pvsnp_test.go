package pvsnp

import (
	"errors"
	"math/rand/v2"
	"slices"
	"testing"
)

func TestVerifySubsetSum(t *testing.T) {
	numbers := []int{3, 34, 4, 12, 5, 2}

	tests := []struct {
		name    string
		indices []int
		target  int
		want    bool
	}{
		{"valid", []int{0, 2, 4}, 12, true}, // 3+4+5
		{"empty subset, zero target", nil, 0, true},
		{"empty subset, nonzero target", nil, 9, false},
		{"wrong sum", []int{0, 1}, 9, false},
		{"duplicate index", []int{0, 0, 0}, 9, false},
		{"index out of range", []int{99}, 9, false},
		{"negative index", []int{-1}, 9, false},
		{"whole set", []int{0, 1, 2, 3, 4, 5}, 60, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VerifySubsetSum(numbers, tt.indices, tt.target); got != tt.want {
				t.Errorf("VerifySubsetSum(%v, %d) = %v, want %v", tt.indices, tt.target, got, tt.want)
			}
		})
	}
}

// TestSolversAgreeWithTheVerifier is the shape the whole package is built around:
// the solver proposes, the verifier decides. A solver test that checks the answer
// against a hardcoded subset is testing the wrong thing, because several different
// subsets can be equally correct.
func TestSolversAgreeWithTheVerifier(t *testing.T) {
	solvers := []struct {
		name  string
		solve func([]int, int) ([]int, error)
	}{
		{"brute", SubsetSumBrute},
		{"dp", SubsetSumDP},
	}

	cases := []struct {
		name     string
		numbers  []int
		target   int
		solvable bool
	}{
		{"classic", []int{3, 34, 4, 12, 5, 2}, 9, true},
		{"needs everything", []int{1, 2, 3}, 6, true},
		{"needs nothing", []int{1, 2, 3}, 0, true},
		{"impossible", []int{2, 4, 6}, 7, false},
		{"single element", []int{5}, 5, true},
		{"single element, miss", []int{5}, 4, false},
		{"empty set, zero target", nil, 0, true},
		{"empty set", nil, 5, false},
		{"target larger than the sum", []int{1, 2}, 100, false},
		{"duplicates in the input", []int{5, 5, 5}, 10, true},
		{"zeroes", []int{0, 0, 7}, 7, true},
	}

	for _, s := range solvers {
		t.Run(s.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					indices, err := s.solve(tc.numbers, tc.target)

					if !tc.solvable {
						if !errors.Is(err, ErrNoSolution) {
							t.Errorf("expected ErrNoSolution, got %v (indices %v)", err, indices)
						}
						return
					}

					if err != nil {
						t.Fatalf("unexpected error: %v", err)
					}
					if !VerifySubsetSum(tc.numbers, indices, tc.target) {
						t.Errorf("solver returned %v, which the verifier rejects", indices)
					}
				})
			}
		})
	}
}

// TestBruteAndDPAgreeRandomised: two independent implementations checking each other
// is worth more than either checked against my expectations.
func TestBruteAndDPAgreeRandomised(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))

	for range 3000 {
		n := r.IntN(12)
		numbers := make([]int, n)
		for i := range numbers {
			numbers[i] = r.IntN(20)
		}

		target := r.IntN(40)

		bruteIdx, bruteErr := SubsetSumBrute(numbers, target)
		dpIdx, dpErr := SubsetSumDP(numbers, target)

		bruteFound := bruteErr == nil
		dpFound := dpErr == nil

		if bruteFound != dpFound {
			t.Fatalf("numbers=%v target=%d: brute found=%v, dp found=%v",
				numbers, target, bruteFound, dpFound)
		}

		// The subsets may differ, and both must verify.
		if bruteFound {
			if !VerifySubsetSum(numbers, bruteIdx, target) {
				t.Fatalf("brute returned %v for %v/%d, which does not verify", bruteIdx, numbers, target)
			}
			if !VerifySubsetSum(numbers, dpIdx, target) {
				t.Fatalf("dp returned %v for %v/%d, which does not verify", dpIdx, numbers, target)
			}
		}
	}
}

// TestDPDescendingLoopMatters is the test for the one line in SubsetSumDP that has
// any content. Iterating the sums ascending lets an element be used twice, which
// turns subset sum into the unbounded-knapsack problem: a different, easier problem
// with a different answer.
func TestDPDescendingLoopMatters(t *testing.T) {
	// 3 alone cannot make 6, but 3 twice can. The descending loop must refuse.
	if _, err := SubsetSumDP([]int{3}, 6); !errors.Is(err, ErrNoSolution) {
		t.Error("SubsetSumDP([3], 6) found a solution, so an element is being reused")
	}

	// And the ascending version, for contrast: written out so the difference is one
	// character rather than a claim.
	reachableAscending := func(numbers []int, target int) bool {
		reachable := make([]bool, target+1)
		reachable[0] = true
		for _, v := range numbers {
			for s := v; s <= target; s++ { // ascending: the bug
				if reachable[s-v] {
					reachable[s] = true
				}
			}
		}
		return reachable[target]
	}

	if !reachableAscending([]int{3}, 6) {
		t.Error("the ascending version should reach 6 from a single 3, reusing it")
	}
	if !reachableAscending([]int{3}, 9) {
		t.Error("the ascending version should reach 9 from a single 3")
	}
}

func TestSubsetSumDPRejectsNegatives(t *testing.T) {
	if _, err := SubsetSumDP([]int{1, -2, 3}, 2); err == nil {
		t.Error("expected an error for negative input")
	}
	if errors.Is(err(SubsetSumDP([]int{1, -2, 3}, 2)), ErrNoSolution) {
		t.Error("a negative input is a caller error, not a missing solution")
	}

	// Brute force has no such restriction, which is worth showing: the exponential
	// algorithm is more general than the fast one.
	indices, e := SubsetSumBrute([]int{1, -2, 3}, 2)
	if e != nil {
		t.Fatalf("brute force failed on negative input: %v", e)
	}
	if !VerifySubsetSum([]int{1, -2, 3}, indices, 2) {
		t.Errorf("brute returned %v, which does not verify", indices)
	}
}

// err extracts the error from a two-value return, for the assertion above.
func err(_ []int, e error) error { return e }

func TestSubsetSumBruteRefusesHugeInput(t *testing.T) {
	if _, e := SubsetSumBrute(make([]int, 63), 1); e == nil {
		t.Error("expected an error for 63 elements, since the mask would not fit")
	}
}

func TestSubsetSumCount(t *testing.T) {
	tests := []struct {
		numbers []int
		target  int
		want    int
	}{
		{[]int{1, 2, 3}, 3, 2}, // {3} and {1,2}
		{[]int{1, 1, 1}, 2, 3}, // three ways to pick two of three
		{[]int{2, 4, 6}, 7, 0},
		{[]int{1, 2, 3}, 0, 1}, // the empty subset
		{nil, 0, 1},
		{[]int{5}, 5, 1},
		{[]int{1, 2, 3, 4, 5}, 5, 3}, // {5}, {1,4}, {2,3}
	}

	for _, tt := range tests {
		if got := SubsetSumCount(tt.numbers, tt.target); got != tt.want {
			t.Errorf("SubsetSumCount(%v, %d) = %d, want %d", tt.numbers, tt.target, got, tt.want)
		}
	}
}

// TestCountAgreesWithEnumeration: the DP counter against actually enumerating, which
// is the only independent check available.
func TestCountAgreesWithEnumeration(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))

	for range 500 {
		n := r.IntN(11)
		numbers := make([]int, n)
		for i := range numbers {
			numbers[i] = r.IntN(12)
		}
		target := r.IntN(25)

		// Enumerate every subset and count the hits.
		want := 0
		for mask := 0; mask < 1<<n; mask++ {
			sum := 0
			for i := range n {
				if mask&(1<<i) != 0 {
					sum += numbers[i]
				}
			}
			if sum == target {
				want++
			}
		}

		if got := SubsetSumCount(numbers, target); got != want {
			t.Fatalf("SubsetSumCount(%v, %d) = %d, enumeration says %d", numbers, target, got, want)
		}
	}
}

// TestPseudoPolynomialBlowup is the point of the whole subset-sum section. The DP is
// O(n * target), which looks polynomial and is not, because the input SIZE is the
// number of bits needed to write the target down.
//
// The cost IS the table size, so this needs no clock: it compares the bytes of input
// against the entries of table, and the two grow at completely different rates.
func TestPseudoPolynomialBlowup(t *testing.T) {
	const n = 20

	for _, bitsInTarget := range []int{10, 15, 20, 25} {
		target := 1<<bitsInTarget - 1

		numbers := make([]int, n)
		for i := range numbers {
			numbers[i] = target / (i + 2)
		}

		inputBytes := n*len(itoaBytes(numbers[0])) + len(itoaBytes(target))
		tableEntries := target + 1

		t.Logf("target %-12d %2d bits  input ~%3d bytes  table %11d entries (%5d MB)",
			target, bitsInTarget, inputBytes, tableEntries, tableEntries*8/1024/1024)

		// Five more bits is 32x the table for one more byte of input.
		if want := 1 << bitsInTarget; tableEntries != want {
			t.Errorf("table for a %d-bit target is %d entries, want %d", bitsInTarget, tableEntries, want)
		}

		// Run the small ones, and whatever they return has to verify.
		if bitsInTarget <= 20 {
			indices, e := SubsetSumDP(numbers, target)
			switch {
			case errors.Is(e, ErrNoSolution):
				// Fine: the question is the cost, not the answer.
			case e != nil:
				t.Errorf("unexpected error: %v", e)
			case !VerifySubsetSum(numbers, indices, target):
				t.Errorf("solver returned %v, which does not verify", indices)
			}
		}
	}

	// The comparison that makes the term "pseudo-polynomial" concrete: a target of
	// 2^40 takes 13 bytes to write down in decimal and needs an 8-terabyte table.
	huge := 1 << 40
	t.Logf("a target of %d takes %d bytes to write and a table of %d GB",
		huge, len(itoaBytes(huge)), huge*8/1024/1024/1024)

	if got := huge * 8 / 1024 / 1024 / 1024; got != 8192 {
		t.Errorf("the 2^40 table is %d GB, expected 8192", got)
	}
}

func itoaBytes(n int) []byte {
	if n == 0 {
		return []byte{'0'}
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return out
}

// TestExponentialGrowth logs the numbers from the package doc, so they are checked
// rather than asserted from memory.
func TestExponentialGrowth(t *testing.T) {
	for _, n := range []int{10, 20, 30, 50, 62, 63, 64} {
		subsets := SubsetCount(n)
		tours := TourCount(n, false)

		t.Logf("n=%-3d subsets=%-22d tours=(n-1)!=%d", n, subsets, tours)
	}

	if got := SubsetCount(10); got != 1024 {
		t.Errorf("SubsetCount(10) = %d, want 1024", got)
	}
	if got := SubsetCount(64); got != -1 {
		t.Errorf("SubsetCount(64) = %d, want -1 for overflow", got)
	}
	if got := TourCount(5, false); got != 24 {
		t.Errorf("TourCount(5) = %d, want 24", got)
	}
	if got := TourCount(5, true); got != 12 {
		t.Errorf("TourCount(5, symmetric) = %d, want 12", got)
	}
	if got := TourCount(30, false); got != -1 {
		t.Errorf("TourCount(30) = %d, want -1 for overflow", got)
	}
}

func TestIndicesOf(t *testing.T) {
	tests := []struct {
		mask uint64
		want []int
	}{
		{0b0000, nil},
		{0b0001, []int{0}},
		{0b1010, []int{1, 3}},
		{0b1111, []int{0, 1, 2, 3}},
	}

	for _, tt := range tests {
		got := indicesOf(tt.mask)
		if len(got) == 0 && len(tt.want) == 0 {
			continue
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("indicesOf(%04b) = %v, want %v", tt.mask, got, tt.want)
		}
	}
}

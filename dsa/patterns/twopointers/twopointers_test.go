package twopointers

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func TestTwoSumSorted(t *testing.T) {
	sorted := []int{1, 3, 4, 6, 8, 11}

	tests := []struct {
		target int
		i, j   int
		found  bool
	}{
		{7, 0, 3, true},  // 1+6, which the pointers reach before 3+4
		{9, 0, 4, true},  // 1+8
		{12, 0, 5, true}, // 1+11
		{19, 4, 5, true}, // 8+11
		{2, 0, 0, false}, // below everything
		{100, 0, 0, false},
		{5, 0, 2, true},  // 1+4
		{6, 0, 0, false}, // no pair sums to 6
	}

	for _, tt := range tests {
		i, j, found := TwoSumInts(sorted, tt.target)

		if found != tt.found {
			t.Errorf("TwoSumInts(%d) found = %v, want %v", tt.target, found, tt.found)
			continue
		}
		if !found {
			continue
		}
		if i != tt.i || j != tt.j {
			t.Errorf("TwoSumInts(%d) = %d, %d; want %d, %d", tt.target, i, j, tt.i, tt.j)
		}
		if sorted[i]+sorted[j] != tt.target {
			t.Errorf("TwoSumInts(%d) returned indices summing to %d", tt.target, sorted[i]+sorted[j])
		}
	}
}

func TestTwoSumSortedDegenerate(t *testing.T) {
	for _, s := range [][]int{nil, {1}} {
		if _, _, found := TwoSumInts(s, 1); found {
			t.Errorf("TwoSumInts(%v, 1) found a pair", s)
		}
	}

	// A single element must not pair with itself.
	if _, _, found := TwoSumInts([]int{4}, 8); found {
		t.Error("a single element paired with itself")
	}
}

// TestTwoSumSortedMatchesBruteForce, because the converging-pointer argument is the kind
// of thing that is convincing and can still be off by one.
func TestTwoSumSortedMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))

	for range 5000 {
		n := r.IntN(20)
		s := make([]int, n)
		for i := range s {
			s[i] = r.IntN(30)
		}
		slices.Sort(s)

		target := r.IntN(60)

		// Brute force: does any pair sum to target?
		want := false
		for i := range s {
			for j := i + 1; j < len(s); j++ {
				if s[i]+s[j] == target {
					want = true
				}
			}
		}

		i, j, found := TwoSumInts(s, target)
		if found != want {
			t.Fatalf("TwoSumInts(%v, %d) found = %v, want %v", s, target, found, want)
		}
		if found && (i >= j || s[i]+s[j] != target) {
			t.Fatalf("TwoSumInts(%v, %d) = %d, %d, which is not a valid pair", s, target, i, j)
		}
	}
}

func TestTwoSumUnsorted(t *testing.T) {
	nums := []int{2, 7, 11, 15}

	i, j, found := TwoSumUnsorted(nums, 9)
	if !found || i != 0 || j != 1 {
		t.Errorf("TwoSumUnsorted = %d, %d, %v; want 0, 1, true", i, j, found)
	}

	// Unsorted input, and the ORIGINAL indices are what comes back, which is what a
	// sort-then-two-pointers version would have destroyed.
	unsorted := []int{3, 9, 1, 5}
	i, j, found = TwoSumUnsorted(unsorted, 6)
	if !found {
		t.Fatal("expected 1+5 to be found")
	}
	if got := unsorted[i] + unsorted[j]; got != 6 {
		t.Errorf("indices %d, %d hold %d and %d, summing to %d, want 6",
			i, j, unsorted[i], unsorted[j], got)
	}

	// An element must not pair with itself, which is why the check comes before the
	// insert.
	if _, _, found := TwoSumUnsorted([]int{4}, 8); found {
		t.Error("a single element paired with itself")
	}
	if _, _, found := TwoSumUnsorted([]int{4, 4}, 8); !found {
		t.Error("two distinct 4s should pair to make 8")
	}
}

func TestThreeSum(t *testing.T) {
	tests := []struct {
		name string
		nums []int
		want [][3]int
	}{
		{
			name: "classic",
			nums: []int{-1, 0, 1, 2, -1, -4},
			want: [][3]int{{-1, -1, 2}, {-1, 0, 1}},
		},
		{"all zeros", []int{0, 0, 0, 0}, [][3]int{{0, 0, 0}}},
		{"no solution", []int{1, 2, 3}, nil},
		{"too short", []int{0, 0}, nil},
		{"empty", nil, nil},
		{"all positive", []int{1, 2, 3, 4}, nil},
		{"many duplicates", []int{-2, 0, 0, 2, 2}, [][3]int{{-2, 0, 2}}},
		{
			name: "several triples",
			nums: []int{-4, -2, -2, -2, 0, 1, 2, 2, 2, 3, 3, 4, 4, 6, 6},
			want: [][3]int{
				{-4, -2, 6}, {-4, 0, 4}, {-4, 1, 3}, {-4, 2, 2},
				{-2, -2, 4}, {-2, 0, 2},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ThreeSum(tt.nums)
			if !slices.Equal(got, tt.want) {
				t.Errorf("ThreeSum(%v) =\n  %v\nwant\n  %v", tt.nums, got, tt.want)
			}
		})
	}
}

// TestThreeSumHasNoDuplicates is the property the whole duplicate-skipping exists for,
// and a randomised check is the only way to be sure all three skips are right.
func TestThreeSumMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))

	for range 3000 {
		n := r.IntN(14)
		nums := make([]int, n)
		for i := range nums {
			nums[i] = r.IntN(9) - 4 // a small range, so duplicates are common
		}

		// Brute force: every triple of indices, deduplicated by value.
		seen := map[[3]int]bool{}
		for i := range nums {
			for j := i + 1; j < n; j++ {
				for k := j + 1; k < n; k++ {
					if nums[i]+nums[j]+nums[k] != 0 {
						continue
					}
					triple := [3]int{nums[i], nums[j], nums[k]}
					slices.Sort(triple[:])
					seen[triple] = true
				}
			}
		}

		want := make([][3]int, 0, len(seen))
		for triple := range seen {
			want = append(want, triple)
		}
		slices.SortFunc(want, func(a, b [3]int) int { return slices.Compare(a[:], b[:]) })
		if len(want) == 0 {
			want = nil
		}

		got := ThreeSum(nums)
		if !slices.Equal(got, want) {
			t.Fatalf("ThreeSum(%v) =\n  %v\nwant\n  %v", nums, got, want)
		}

		// No duplicates, stated separately because it is the actual requirement.
		unique := map[[3]int]bool{}
		for _, triple := range got {
			if unique[triple] {
				t.Fatalf("ThreeSum(%v) returned %v twice", nums, triple)
			}
			unique[triple] = true
		}
	}
}

func TestThreeSumDoesNotMutateItsInput(t *testing.T) {
	nums := []int{3, -1, 0, 1, -2}
	before := slices.Clone(nums)

	ThreeSum(nums)

	if !slices.Equal(nums, before) {
		t.Errorf("ThreeSum mutated its input: %v then %v", before, nums)
	}
}

func TestMaxWaterContainer(t *testing.T) {
	tests := []struct {
		heights []int
		want    int
	}{
		{[]int{1, 8, 6, 2, 5, 4, 8, 3, 7}, 49}, // 8 and 7, 7 apart
		{[]int{1, 1}, 1},
		{[]int{4, 3, 2, 1, 4}, 16},
		{[]int{1, 2, 1}, 2},
		{nil, 0},
		{[]int{5}, 0},
		{[]int{0, 0, 0}, 0},
		{[]int{1, 2, 3, 4, 5}, 6}, // heights 2 and 5, three apart
	}

	for _, tt := range tests {
		if got := MaxWaterContainer(tt.heights); got != tt.want {
			t.Errorf("MaxWaterContainer(%v) = %d, want %d", tt.heights, got, tt.want)
		}
	}
}

// TestMaxWaterContainerMatchesBruteForce: the "move the shorter wall" argument is exactly
// the kind of greedy step that sounds right and needs checking.
func TestMaxWaterContainerMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 7))

	for range 5000 {
		n := r.IntN(25)
		heights := make([]int, n)
		for i := range heights {
			heights[i] = r.IntN(15)
		}

		want := 0
		for i := range heights {
			for j := i + 1; j < n; j++ {
				want = max(want, (j-i)*min(heights[i], heights[j]))
			}
		}

		if got := MaxWaterContainer(heights); got != want {
			t.Fatalf("MaxWaterContainer(%v) = %d, want %d", heights, got, want)
		}
	}
}

func TestIsPalindrome(t *testing.T) {
	tests := []struct {
		s    string
		want bool
	}{
		{"A man, a plan, a canal: Panama", true},
		{"race a car", false},
		{"", true},
		{" ", true},
		{".,;", true}, // nothing but punctuation
		{"a", true},
		{"ab", false},
		{"aa", true},
		{"0P", false}, // the classic trap: '0' and 'P' are adjacent in ASCII order
		{"12321", true},
		{"No 'x' in Nixon", true},
	}

	for _, tt := range tests {
		if got := IsPalindrome(tt.s); got != tt.want {
			t.Errorf("IsPalindrome(%q) = %v, want %v", tt.s, got, tt.want)
		}
	}
}

func TestDedupe(t *testing.T) {
	tests := []struct {
		in   []int
		want []int
	}{
		{[]int{1, 1, 2}, []int{1, 2}},
		{[]int{0, 0, 1, 1, 1, 2, 2, 3, 3, 4}, []int{0, 1, 2, 3, 4}},
		{[]int{1, 2, 3}, []int{1, 2, 3}},
		{[]int{1, 1, 1}, []int{1}},
		{nil, nil},
		{[]int{5}, []int{5}},
		{[]int{1, 2, 1, 2}, []int{1, 2, 1, 2}}, // only CONSECUTIVE duplicates go
	}

	for _, tt := range tests {
		s := slices.Clone(tt.in)
		n := Dedupe(s)

		got := s[:n]
		if len(got) == 0 {
			got = nil
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("Dedupe(%v) kept %v, want %v", tt.in, got, tt.want)
		}
	}
}

// TestDedupeMatchesSlicesCompact: the stdlib has this, so the stdlib is the oracle.
func TestDedupeMatchesSlicesCompact(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 11))

	for range 5000 {
		n := r.IntN(25)
		in := make([]int, n)
		for i := range in {
			in[i] = r.IntN(4)
		}

		mine := slices.Clone(in)
		kept := Dedupe(mine)

		want := slices.Compact(slices.Clone(in))

		if !slices.Equal(mine[:kept], want) {
			t.Fatalf("Dedupe(%v) = %v, slices.Compact = %v", in, mine[:kept], want)
		}
	}
}

func TestDedupeAllowing(t *testing.T) {
	tests := []struct {
		in    []int
		limit int
		want  []int
	}{
		{[]int{1, 1, 1, 2, 2, 3}, 2, []int{1, 1, 2, 2, 3}},
		{[]int{0, 0, 1, 1, 1, 1, 2, 3, 3}, 2, []int{0, 0, 1, 1, 2, 3, 3}},
		{[]int{1, 1, 1}, 1, []int{1}},
		{[]int{1, 1, 1}, 3, []int{1, 1, 1}},
		{[]int{1, 1, 1}, 5, []int{1, 1, 1}},
		{[]int{1, 2, 3}, 1, []int{1, 2, 3}},
		{nil, 2, nil},
		{[]int{1, 1}, 0, nil}, // a limit of zero keeps nothing
	}

	for _, tt := range tests {
		s := slices.Clone(tt.in)
		n := DedupeAllowing(s, tt.limit)

		got := s[:n]
		if len(got) == 0 {
			got = nil
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("DedupeAllowing(%v, %d) kept %v, want %v", tt.in, tt.limit, got, tt.want)
		}
	}

	// limit 1 must be exactly Dedupe.
	r := rand.New(rand.NewPCG(13, 17))
	for range 2000 {
		in := make([]int, r.IntN(20))
		for i := range in {
			in[i] = r.IntN(4)
		}

		a, b := slices.Clone(in), slices.Clone(in)
		na, nb := Dedupe(a), DedupeAllowing(b, 1)

		if !slices.Equal(a[:na], b[:nb]) {
			t.Fatalf("Dedupe and DedupeAllowing(_, 1) disagree on %v: %v and %v", in, a[:na], b[:nb])
		}
	}
}

func TestMoveZerosToEnd(t *testing.T) {
	tests := []struct {
		in   []int
		want []int
	}{
		{[]int{0, 1, 0, 3, 12}, []int{1, 3, 12, 0, 0}},
		{[]int{0}, []int{0}},
		{[]int{1, 2, 3}, []int{1, 2, 3}},
		{[]int{0, 0, 0}, []int{0, 0, 0}},
		{nil, nil},
		{[]int{0, 0, 1}, []int{1, 0, 0}},
		{[]int{1, 0, 0}, []int{1, 0, 0}},
	}

	for _, tt := range tests {
		s := slices.Clone(tt.in)
		MoveZerosToEnd(s)

		if !slices.Equal(s, tt.want) {
			t.Errorf("MoveZerosToEnd(%v) = %v, want %v", tt.in, s, tt.want)
		}
	}
}

func TestPartitionByPredicate(t *testing.T) {
	s := []int{1, 2, 3, 4, 5, 6, 7, 8}
	even := func(v int) bool { return v%2 == 0 }

	n := PartitionByPredicate(s, even)

	if n != 4 {
		t.Errorf("kept %d, want 4", n)
	}
	for _, v := range s[:n] {
		if !even(v) {
			t.Errorf("odd value %d in the kept half: %v", v, s)
		}
	}
	for _, v := range s[n:] {
		if even(v) {
			t.Errorf("even value %d in the dropped half: %v", v, s)
		}
	}
	// Order within the KEPT half is preserved. The rejected half is not, which the doc
	// comment says and this pins down so nobody starts relying on it.
	if !slices.Equal(s[:n], []int{2, 4, 6, 8}) {
		t.Errorf("kept half = %v, want [2 4 6 8]", s[:n])
	}
	if slices.IsSorted(s[n:]) {
		t.Errorf("rejected half = %v; it happens to be sorted, so this test proves nothing", s[n:])
	}

	// Degenerate predicates.
	all := []int{1, 2, 3}
	if got := PartitionByPredicate(all, func(int) bool { return true }); got != 3 {
		t.Errorf("an always-true predicate kept %d, want 3", got)
	}
	if got := PartitionByPredicate(all, func(int) bool { return false }); got != 0 {
		t.Errorf("an always-false predicate kept %d, want 0", got)
	}
}

func TestSquaresOfSorted(t *testing.T) {
	tests := []struct {
		in   []int
		want []int
	}{
		{[]int{-4, -1, 0, 3, 10}, []int{0, 1, 9, 16, 100}},
		{[]int{-7, -3, 2, 3, 11}, []int{4, 9, 9, 49, 121}},
		{[]int{1, 2, 3}, []int{1, 4, 9}},
		{[]int{-3, -2, -1}, []int{1, 4, 9}},
		{nil, nil},
		{[]int{0}, []int{0}},
		{[]int{-5, 5}, []int{25, 25}},
	}

	for _, tt := range tests {
		got := SquaresOfSorted(tt.in)
		if len(got) == 0 {
			got = nil
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("SquaresOfSorted(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestSquaresOfSortedMatchesSquareThenSort(t *testing.T) {
	r := rand.New(rand.NewPCG(19, 23))

	for range 5000 {
		n := r.IntN(25)
		s := make([]int, n)
		for i := range s {
			s[i] = r.IntN(40) - 20
		}
		slices.Sort(s)

		want := make([]int, n)
		for i, v := range s {
			want[i] = v * v
		}
		slices.Sort(want)

		got := SquaresOfSorted(s)
		if !slices.Equal(got, want) {
			t.Fatalf("SquaresOfSorted(%v) = %v, want %v", s, got, want)
		}
	}
}

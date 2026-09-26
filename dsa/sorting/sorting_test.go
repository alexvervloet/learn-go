package sorting

import (
	"cmp"
	"math/rand/v2"
	"slices"
	"testing"
)

// Every algorithm, run through the same tests. A table of functions rather than
// six copies of each test, so an algorithm cannot be quietly excluded from one.
var algorithms = []struct {
	name   string
	sort   func([]int)
	stable bool
}{
	{"bubble", Bubble[int], true},
	{"insertion", Insertion[int], true},
	{"selection", Selection[int], false},
	{"merge", Merge[int], true},
	{"quick", Quick[int], false},
	{"heap", Heap[int], false},
}

// The input shapes that matter. Real data is rarely random, and three of these
// five are the shapes that break a naive implementation.
func shapes(n int) map[string][]int {
	r := rand.New(rand.NewPCG(1, 2))

	random := make([]int, n)
	for i := range random {
		random[i] = r.IntN(n * 10)
	}

	sorted := make([]int, n)
	for i := range sorted {
		sorted[i] = i
	}

	reversed := make([]int, n)
	for i := range reversed {
		reversed[i] = n - i
	}

	// Sorted except for a few elements a short distance out of place, which is
	// what "nearly sorted" means in practice: a log file with a few late arrivals.
	nearly := slices.Clone(sorted)
	for range n / 20 {
		i := r.IntN(n)
		j := min(i+r.IntN(5), n-1)
		nearly[i], nearly[j] = nearly[j], nearly[i]
	}

	// Many duplicates, which is what breaks two-way partitioning.
	duplicates := make([]int, n)
	for i := range duplicates {
		duplicates[i] = r.IntN(3)
	}

	return map[string][]int{
		"random":     random,
		"sorted":     sorted,
		"reversed":   reversed,
		"nearly":     nearly,
		"duplicates": duplicates,
	}
}

func TestSortsEverything(t *testing.T) {
	for _, alg := range algorithms {
		t.Run(alg.name, func(t *testing.T) {
			for _, n := range []int{0, 1, 2, 3, 7, 13, 100, 1000} {
				for shape, input := range shapes(n) {
					got := slices.Clone(input)
					want := slices.Clone(input)
					slices.Sort(want)

					alg.sort(got)

					if !slices.Equal(got, want) {
						t.Errorf("n=%d %s: wrong result", n, shape)
						if n <= 20 {
							t.Errorf("  input %v\n  got   %v\n  want  %v", input, got, want)
						}
					}
				}
			}
		})
	}
}

// TestPreservesEveryElement catches the bug class that a "is it sorted" check
// misses entirely: an algorithm that drops or duplicates an element can still
// return something perfectly sorted.
func TestPreservesEveryElement(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))

	for _, alg := range algorithms {
		t.Run(alg.name, func(t *testing.T) {
			for range 20 {
				n := 1 + r.IntN(200)
				input := make([]int, n)
				for i := range input {
					input[i] = r.IntN(20) // heavy duplication, on purpose
				}

				counts := map[int]int{}
				for _, v := range input {
					counts[v]++
				}

				got := slices.Clone(input)
				alg.sort(got)

				if len(got) != n {
					t.Fatalf("length changed from %d to %d", n, len(got))
				}
				if !IsSorted(got, cmp.Compare) {
					t.Fatalf("not sorted: %v", got)
				}

				after := map[int]int{}
				for _, v := range got {
					after[v]++
				}
				for v, c := range counts {
					if after[v] != c {
						t.Fatalf("value %d appeared %d times, was %d times", v, after[v], c)
					}
				}
			}
		})
	}
}

// pair is a value with a tag, so the order of equal values is observable. This is
// the only way to test stability: with plain ints, two equal elements swapping is
// invisible.
type pair struct {
	key int
	tag int // the original position
}

func byKey(a, b pair) int { return cmp.Compare(a.key, b.key) }

func TestStability(t *testing.T) {
	sorters := []struct {
		name   string
		sort   func([]pair, Compare[pair])
		stable bool
	}{
		{"bubble", BubbleFunc[pair], true},
		{"insertion", InsertionFunc[pair], true},
		{"selection", SelectionFunc[pair], false},
		{"merge", MergeFunc[pair], true},
		{"quick", QuickFunc[pair], false},
		{"heap", HeapFunc[pair], false},
	}

	for _, s := range sorters {
		t.Run(s.name, func(t *testing.T) {
			// 60 elements over 4 distinct keys, so every key has many ties and
			// there is plenty of room for an unstable sort to reorder them. Also
			// large enough that quick and merge do not fall through to their
			// insertion-sort threshold, which would make them look stable.
			const n = 60
			input := make([]pair, n)
			for i := range input {
				input[i] = pair{key: i % 4, tag: i}
			}

			got := slices.Clone(input)
			s.sort(got, byKey)

			if !IsSorted(got, byKey) {
				t.Fatalf("not sorted by key: %v", got)
			}

			// Stable means the tags are ascending within each key.
			isStable := true
			for i := 1; i < len(got); i++ {
				if got[i-1].key == got[i].key && got[i-1].tag > got[i].tag {
					isStable = false
					break
				}
			}

			if isStable != s.stable {
				t.Errorf("stability = %v, want %v", isStable, s.stable)
				t.Logf("result: %v", got[:min(16, len(got))])
			}
		})
	}
}

// TestQuickHandlesAdversarialInput covers the inputs that turn a naive quicksort
// quadratic. None of them is exotic: sorted data is the most common shape real
// data has, and a column with three distinct values is an ordinary database
// column.
func TestQuickHandlesAdversarialInput(t *testing.T) {
	const n = 50_000 // large enough that O(n^2) would not finish

	cases := map[string][]int{
		"sorted":       nil,
		"reversed":     nil,
		"all equal":    nil,
		"three values": nil,
		"organ pipe":   nil, // ascending then descending, a classic pivot killer
	}

	sorted := make([]int, n)
	for i := range sorted {
		sorted[i] = i
	}
	cases["sorted"] = sorted

	reversed := make([]int, n)
	for i := range reversed {
		reversed[i] = n - i
	}
	cases["reversed"] = reversed

	cases["all equal"] = make([]int, n) // every element is 0

	threeValues := make([]int, n)
	for i := range threeValues {
		threeValues[i] = i % 3
	}
	cases["three values"] = threeValues

	pipe := make([]int, n)
	for i := range pipe {
		if i < n/2 {
			pipe[i] = i
		} else {
			pipe[i] = n - i
		}
	}
	cases["organ pipe"] = pipe

	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			got := slices.Clone(input)
			want := slices.Clone(input)
			slices.Sort(want)

			QuickFunc(got, cmp.Compare)

			if !slices.Equal(got, want) {
				t.Error("wrong result")
			}
		})
	}
}

// TestQuickRecursesIntoTheSmallerSide: recursing into both sides makes the stack
// O(n) in the worst case, which is a crash rather than a slow sort. 10 million
// elements of the shape that provokes it.
func TestQuickRecursesIntoTheSmallerSide(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates 80 MB")
	}

	const n = 10_000_000

	s := make([]int, n)
	for i := range s {
		s[i] = i // sorted, the shape that provokes deep recursion with a bad pivot
	}

	Quick(s)

	if !slices.IsSorted(s) {
		t.Error("not sorted")
	}
}

func TestHeapBuildIsLinear(t *testing.T) {
	// Not a timing assertion, which would be flaky. The property: after the build
	// phase alone, index 0 holds the maximum.
	r := rand.New(rand.NewPCG(5, 6))

	s := make([]int, 1000)
	for i := range s {
		s[i] = r.IntN(10_000)
	}

	want := slices.Max(s)

	for parent := len(s)/2 - 1; parent >= 0; parent-- {
		siftDown(s, parent, len(s), cmp.Compare)
	}

	if s[0] != want {
		t.Errorf("after building the heap, s[0] = %d, want the maximum %d", s[0], want)
	}

	// And every parent is at least as large as its children.
	for i := range s {
		for _, child := range []int{2*i + 1, 2*i + 2} {
			if child < len(s) && s[child] > s[i] {
				t.Fatalf("heap property broken: s[%d]=%d < s[%d]=%d", i, s[i], child, s[child])
			}
		}
	}
}

func TestBubbleExitsEarlyOnSortedInput(t *testing.T) {
	// The early exit is bubble sort's only redeeming feature, and it is
	// observable through the comparison count.
	const n = 1000

	sorted := make([]int, n)
	for i := range sorted {
		sorted[i] = i
	}

	comparisons := 0
	BubbleFunc(sorted, counted(&comparisons))

	// One pass is n-1 comparisons. Anything near n^2 means the early exit is gone.
	if comparisons > n {
		t.Errorf("%d comparisons on sorted input, want at most %d (one pass)", comparisons, n)
	}
}

// counted wraps cmp.Compare and counts the calls, which is the one thing about an
// algorithm's cost that a test can observe directly and exactly. No timing, so no
// flakiness.
func counted(n *int) Compare[int] {
	return func(a, b int) int {
		*n++
		return cmp.Compare(a, b)
	}
}

// TestSelectionHasNoBestCase is selection sort's defining weakness, stated as an
// exact number. The scan for the minimum cannot be cut short, so sorted input
// costs precisely as much as reversed input: n(n-1)/2 comparisons, every time.
func TestSelectionHasNoBestCase(t *testing.T) {
	const n = 500
	want := n * (n - 1) / 2

	for shape, input := range shapes(n) {
		s := slices.Clone(input)

		comparisons := 0
		SelectionFunc(s, counted(&comparisons))

		if comparisons != want {
			t.Errorf("%s: %d comparisons, want exactly %d", shape, comparisons, want)
		}
		if !slices.IsSorted(s) {
			t.Errorf("%s: not sorted", shape)
		}
	}
}

// TestInsertionBestAndWorstCase is the opposite, and it is why insertion sort is
// inside slices.Sort. The same algorithm costs O(n) or O(n^2) depending entirely
// on the input.
func TestInsertionBestAndWorstCase(t *testing.T) {
	const n = 500
	shaped := shapes(n)

	count := func(input []int) int {
		s := slices.Clone(input)
		comparisons := 0
		InsertionFunc(s, counted(&comparisons))
		if !slices.IsSorted(s) {
			t.Fatal("not sorted")
		}
		return comparisons
	}

	sorted := count(shaped["sorted"])
	nearly := count(shaped["nearly"])
	random := count(shaped["random"])
	reversed := count(shaped["reversed"])

	t.Logf("n=%d comparisons: sorted %d, nearly sorted %d, random %d, reversed %d",
		n, sorted, nearly, random, reversed)

	// Sorted input is exactly n-1: every element stops immediately.
	if sorted != n-1 {
		t.Errorf("sorted input took %d comparisons, want exactly %d", sorted, n-1)
	}

	// Reversed input is the worst case, n(n-1)/2, because every element slides all
	// the way back.
	if want := n * (n - 1) / 2; reversed != want {
		t.Errorf("reversed input took %d comparisons, want exactly %d", reversed, want)
	}

	// Nearly sorted stays close to linear, which is the property that makes this
	// algorithm useful rather than a curiosity.
	if nearly > 4*n {
		t.Errorf("nearly sorted input took %d comparisons, want under %d", nearly, 4*n)
	}

	// And random input sits between the two, near n^2/4.
	if random < n || random > n*n/2 {
		t.Errorf("random input took %d comparisons, expected between %d and %d", random, n, n*n/2)
	}
}

// TestMergeSkipsTheMergeWhenHalvesAreOrdered: the `if compare(s[mid-1], s[mid])
// <= 0 { return }` line is what makes merge sort fast on nearly-sorted input, and
// the comparison count is how to see it.
func TestMergeSkipsTheMergeWhenHalvesAreOrdered(t *testing.T) {
	const n = 4096
	shaped := shapes(n)

	count := func(input []int) int {
		s := slices.Clone(input)
		comparisons := 0
		MergeFunc(s, counted(&comparisons))
		if !slices.IsSorted(s) {
			t.Fatal("not sorted")
		}
		return comparisons
	}

	sorted := count(shaped["sorted"])
	random := count(shaped["random"])

	t.Logf("n=%d comparisons: sorted %d, random %d", n, sorted, random)

	// On sorted input every merge is skipped, so the cost is the recursion plus
	// the insertion sort of each small block: well under n log n.
	if ceiling := n * ilog2(n) / 2; sorted > ceiling {
		t.Errorf("sorted input took %d comparisons, want well under %d", sorted, ceiling)
	}
	if sorted >= random {
		t.Errorf("sorted input took %d comparisons and random took %d; the skip is not working",
			sorted, random)
	}
}

// TestFuncAndOrderedAgree: the Ordered wrappers must be exactly the Func versions
// with cmp.Compare, or the convenience form and the tested form diverge.
func TestFuncAndOrderedAgree(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 8))

	pairs := []struct {
		name    string
		ordered func([]int)
		fn      func([]int, Compare[int])
	}{
		{"bubble", Bubble[int], BubbleFunc[int]},
		{"insertion", Insertion[int], InsertionFunc[int]},
		{"selection", Selection[int], SelectionFunc[int]},
		{"merge", Merge[int], MergeFunc[int]},
		{"quick", Quick[int], QuickFunc[int]},
		{"heap", Heap[int], HeapFunc[int]},
	}

	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			input := make([]int, 200)
			for i := range input {
				input[i] = r.IntN(100)
			}

			a, b := slices.Clone(input), slices.Clone(input)
			p.ordered(a)
			p.fn(b, cmp.Compare)

			if !slices.Equal(a, b) {
				t.Error("the Ordered wrapper and the Func version disagree")
			}
		})
	}
}

func TestIsSorted(t *testing.T) {
	tests := []struct {
		in   []int
		want bool
	}{
		{nil, true},
		{[]int{1}, true},
		{[]int{1, 2, 3}, true},
		{[]int{1, 1, 1}, true}, // equal elements are sorted
		{[]int{1, 3, 2}, false},
		{[]int{3, 2, 1}, false},
	}

	for _, tt := range tests {
		if got := IsSorted(tt.in, cmp.Compare); got != tt.want {
			t.Errorf("IsSorted(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestIlog2(t *testing.T) {
	for _, tt := range []struct{ in, want int }{
		{1, 0}, {2, 1}, {3, 1}, {4, 2}, {7, 2}, {8, 3}, {1024, 10}, {1025, 10},
	} {
		if got := ilog2(tt.in); got != tt.want {
			t.Errorf("ilog2(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

// TestQuickDoesNotFallBackOnNaturalInput is the test that would have caught the
// budget bug. The heapsort fallback exists for deliberately constructed input, and
// if it fires on ordinary data then quicksort is quietly not being used.
//
// The fallback is a parameter of quickSort precisely so it can be counted here.
func TestQuickDoesNotFallBackOnNaturalInput(t *testing.T) {
	for _, n := range []int{100, 10_000, 100_000} {
		for shape, input := range shapes(n) {
			s := slices.Clone(input)

			fallbacks := 0
			counting := func(sub []int, compare Compare[int]) {
				fallbacks++
				HeapFunc(sub, compare)
			}

			quickSort(s, cmp.Compare, budgetFor(len(s)), counting)

			if !slices.IsSorted(s) {
				t.Errorf("n=%d %s: not sorted", n, shape)
			}
			if fallbacks != 0 {
				t.Errorf("n=%d %s: the heapsort fallback fired %d times on natural input",
					n, shape, fallbacks)
			}
		}
	}
}

// TestQuickFallbackWorks exercises the other side: with no budget at all, the
// result still has to be correct, because that path is the guarantee.
func TestQuickFallbackWorks(t *testing.T) {
	for _, n := range []int{13, 100, 10_000} {
		for shape, input := range shapes(n) {
			s := slices.Clone(input)
			want := slices.Clone(input)
			slices.Sort(want)

			fallbacks := 0
			counting := func(sub []int, compare Compare[int]) {
				fallbacks++
				HeapFunc(sub, compare)
			}

			quickSort(s, cmp.Compare, 0, counting)

			if !slices.Equal(s, want) {
				t.Errorf("n=%d %s: wrong result through the fallback path", n, shape)
			}
			if fallbacks != 1 {
				t.Errorf("n=%d %s: fallback fired %d times with a budget of 0, want 1",
					n, shape, fallbacks)
			}
		}
	}
}

func TestBudgetFor(t *testing.T) {
	// Monotonic and logarithmic, which is the only contract that matters.
	for _, tt := range []struct{ n, want int }{
		{0, 8}, {1, 8}, {2, 10}, {16, 16}, {100_000, 40},
	} {
		if got := budgetFor(tt.n); got != tt.want {
			t.Errorf("budgetFor(%d) = %d, want %d", tt.n, got, tt.want)
		}
	}
}

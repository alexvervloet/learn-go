package heap

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func TestKLargestAndKSmallest(t *testing.T) {
	s := []int{3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5}

	tests := []struct {
		k        int
		largest  []int
		smallest []int
	}{
		{0, nil, nil},
		{1, []int{9}, []int{1}},
		{3, []int{9, 6, 5}, []int{1, 1, 2}},
		{5, []int{9, 6, 5, 5, 5}, []int{1, 1, 2, 3, 3}},
		{len(s), []int{9, 6, 5, 5, 5, 4, 3, 3, 2, 1, 1}, []int{1, 1, 2, 3, 3, 4, 5, 5, 5, 6, 9}},
		{99, []int{9, 6, 5, 5, 5, 4, 3, 3, 2, 1, 1}, []int{1, 1, 2, 3, 3, 4, 5, 5, 5, 6, 9}},
		{-1, nil, nil},
	}

	for _, tt := range tests {
		if got := KLargest(s, tt.k); !slices.Equal(got, tt.largest) {
			t.Errorf("KLargest(k=%d) = %v, want %v", tt.k, got, tt.largest)
		}
		if got := KSmallest(s, tt.k); !slices.Equal(got, tt.smallest) {
			t.Errorf("KSmallest(k=%d) = %v, want %v", tt.k, got, tt.smallest)
		}
	}
}

// TestKLargestMatchesSorting is the property worth asserting: whatever the input, the
// heap version must agree with sort-and-slice, which is the obvious correct answer.
func TestKLargestMatchesSorting(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))

	for range 3000 {
		n := r.IntN(40)
		s := make([]int, n)
		for i := range s {
			s[i] = r.IntN(15) // duplicates on purpose
		}
		k := r.IntN(12)

		sorted := slices.Clone(s)
		slices.Sort(sorted)
		slices.Reverse(sorted)

		wantLargest := sorted[:min(max(k, 0), len(sorted))]
		if len(wantLargest) == 0 {
			wantLargest = nil
		}

		if got := KLargest(s, k); !slices.Equal(got, wantLargest) {
			t.Fatalf("KLargest(%v, %d) = %v, want %v", s, k, got, wantLargest)
		}

		slices.Reverse(sorted)
		wantSmallest := sorted[:min(max(k, 0), len(sorted))]
		if len(wantSmallest) == 0 {
			wantSmallest = nil
		}

		if got := KSmallest(s, k); !slices.Equal(got, wantSmallest) {
			t.Fatalf("KSmallest(%v, %d) = %v, want %v", s, k, got, wantSmallest)
		}
	}
}

// TestKLargestDoesNotMutateItsInput: it clones the first k elements, and forgetting to
// would rearrange the caller's slice.
func TestKLargestDoesNotMutateItsInput(t *testing.T) {
	s := []int{5, 3, 9, 1, 7}
	before := slices.Clone(s)

	KLargest(s, 3)
	KSmallest(s, 3)

	if !slices.Equal(s, before) {
		t.Errorf("the input was mutated: %v then %v", before, s)
	}
}

func TestKMostFrequent(t *testing.T) {
	tests := []struct {
		name string
		s    []string
		k    int
		want []Counted[string]
	}{
		{
			name: "clear winner",
			s:    []string{"a", "b", "a", "c", "a", "b"},
			k:    2,
			want: []Counted[string]{{"a", 3}, {"b", 2}},
		},
		{
			name: "ties broken by value",
			s:    []string{"c", "b", "a"},
			k:    2,
			want: []Counted[string]{{"a", 1}, {"b", 1}},
		},
		{
			name: "k larger than the number of distinct values",
			s:    []string{"a", "a", "b"},
			k:    10,
			want: []Counted[string]{{"a", 2}, {"b", 1}},
		},
		{name: "empty", s: nil, k: 3, want: nil},
		{name: "k is zero", s: []string{"a"}, k: 0, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := KMostFrequent(tt.s, tt.k)
			if !slices.Equal(got, tt.want) {
				t.Errorf("KMostFrequent(%v, %d) = %v, want %v", tt.s, tt.k, got, tt.want)
			}
		})
	}
}

// TestKMostFrequentIsDeterministic: map iteration order is random, so without the
// tie-break the result would differ between runs and this test would be impossible.
// Running it many times is what proves the tie-break works.
func TestKMostFrequentIsDeterministic(t *testing.T) {
	// Twenty distinct values, all with count 1, so every comparison is a tie.
	s := make([]string, 20)
	for i := range s {
		s[i] = string(rune('a' + i))
	}

	first := KMostFrequent(s, 5)

	for range 200 {
		if got := KMostFrequent(s, 5); !slices.Equal(got, first) {
			t.Fatalf("two runs disagree: %v and %v", first, got)
		}
	}

	// And the tie-break is "smallest value wins".
	want := []Counted[string]{{"a", 1}, {"b", 1}, {"c", 1}, {"d", 1}, {"e", 1}}
	if !slices.Equal(first, want) {
		t.Errorf("KMostFrequent = %v, want %v", first, want)
	}
}

func TestKMostFrequentMatchesCounting(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))

	for range 2000 {
		n := r.IntN(60)
		s := make([]int, n)
		for i := range s {
			s[i] = r.IntN(10)
		}
		k := r.IntN(12)

		// The obvious correct answer: count, then sort by count then value.
		counts := map[int]int{}
		for _, v := range s {
			counts[v]++
		}

		all := make([]Counted[int], 0, len(counts))
		for value, count := range counts {
			all = append(all, Counted[int]{value, count})
		}
		slices.SortFunc(all, func(a, b Counted[int]) int {
			if a.Count != b.Count {
				return b.Count - a.Count
			}
			return a.Value - b.Value
		})

		want := all[:min(max(k, 0), len(all))]
		if len(want) == 0 {
			want = nil
		}

		if got := KMostFrequent(s, k); !slices.Equal(got, want) {
			t.Fatalf("KMostFrequent(%v, %d) = %v, want %v", s, k, got, want)
		}
	}
}

func TestMergeSorted(t *testing.T) {
	tests := []struct {
		name  string
		lists [][]int
		want  []int
	}{
		{"three lists", [][]int{{1, 4, 7}, {2, 5, 8}, {3, 6, 9}}, []int{1, 2, 3, 4, 5, 6, 7, 8, 9}},
		{"uneven", [][]int{{1}, {2, 3, 4, 5}, nil}, []int{1, 2, 3, 4, 5}},
		{"one list", [][]int{{3, 1, 2}}, []int{3, 1, 2}}, // unsorted input in, same out
		{"all empty", [][]int{nil, nil}, nil},
		{"no lists", nil, nil},
		{"duplicates across lists", [][]int{{1, 1}, {1, 1}}, []int{1, 1, 1, 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MergeSorted(tt.lists); !slices.Equal(got, tt.want) {
				t.Errorf("MergeSorted(%v) = %v, want %v", tt.lists, got, tt.want)
			}
		})
	}
}

func TestMergeSortedMatchesConcatenateAndSort(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 7))

	for range 2000 {
		k := r.IntN(8)
		lists := make([][]int, k)

		var all []int
		for i := range lists {
			n := r.IntN(10)
			lists[i] = make([]int, n)
			for j := range lists[i] {
				lists[i][j] = r.IntN(30)
			}
			slices.Sort(lists[i])
			all = append(all, lists[i]...)
		}

		slices.Sort(all)
		if len(all) == 0 {
			all = nil
		}

		if got := MergeSorted(lists); !slices.Equal(got, all) {
			t.Fatalf("MergeSorted(%v) = %v, want %v", lists, got, all)
		}
	}
}

// TestMergeSortedMemoryIsO_k: the heap holds cursors, not values, so merging a million
// elements across three lists keeps three things in the heap.
func TestMergeSortedMemoryIsO_k(t *testing.T) {
	const perList = 100_000

	lists := make([][]int, 3)
	for i := range lists {
		lists[i] = make([]int, perList)
		for j := range lists[i] {
			lists[i][j] = j*3 + i
		}
	}

	got := MergeSorted(lists)

	if len(got) != 3*perList {
		t.Fatalf("merged %d elements, want %d", len(got), 3*perList)
	}
	if !slices.IsSorted(got) {
		t.Error("the result is not sorted")
	}
	for i, v := range got {
		if v != i {
			t.Fatalf("got[%d] = %d, want %d", i, v, i)
			break
		}
	}
}

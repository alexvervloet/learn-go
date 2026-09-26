package intervals

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func iv(start, end int) Interval { return Interval{Start: start, End: end} }

func list(pairs ...[2]int) []Interval {
	out := make([]Interval, len(pairs))
	for i, p := range pairs {
		out[i] = iv(p[0], p[1])
	}
	return out
}

// TestHalfOpenConvention pins down the decision the whole package rests on. Touching
// intervals do not overlap, and they do touch.
func TestHalfOpenConvention(t *testing.T) {
	a, b := iv(1, 2), iv(2, 3)

	if a.Overlaps(b) {
		t.Error("[1,2) and [2,3) overlap; under half-open they should not")
	}
	if !a.Touches(b) {
		t.Error("[1,2) and [2,3) do not touch; they abut, so they should")
	}
	if a.Contains(2) {
		t.Error("[1,2) contains 2; the end is exclusive")
	}
	if !a.Contains(1) {
		t.Error("[1,2) does not contain 1; the start is inclusive")
	}

	// Genuine overlap.
	if !iv(1, 3).Overlaps(iv(2, 4)) {
		t.Error("[1,3) and [2,4) should overlap")
	}

	// Empty intervals overlap nothing, including themselves.
	empty := iv(5, 5)
	if !empty.IsEmpty() || empty.Length() != 0 {
		t.Error("[5,5) should be empty with length 0")
	}
	if empty.Overlaps(iv(0, 10)) || iv(0, 10).Overlaps(empty) {
		t.Error("an empty interval overlaps nothing")
	}
	if iv(5, 3).Length() != 0 {
		t.Error("a backwards interval should have length 0")
	}
}

func TestIntersect(t *testing.T) {
	tests := []struct {
		a, b, want Interval
	}{
		{iv(1, 5), iv(3, 8), iv(3, 5)},
		{iv(1, 5), iv(5, 8), iv(5, 5)},  // touching: empty
		{iv(1, 5), iv(6, 8), iv(6, 5)},  // disjoint: empty (End < Start)
		{iv(1, 10), iv(3, 4), iv(3, 4)}, // contained
		{iv(1, 5), iv(1, 5), iv(1, 5)},  // identical
	}

	for _, tt := range tests {
		got := tt.a.Intersect(tt.b)

		if got.IsEmpty() != tt.want.IsEmpty() {
			t.Errorf("%v.Intersect(%v) = %v, want empty = %v", tt.a, tt.b, got, tt.want.IsEmpty())
			continue
		}
		if !got.IsEmpty() && got != tt.want {
			t.Errorf("%v.Intersect(%v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestMerge(t *testing.T) {
	tests := []struct {
		name string
		in   []Interval
		want []Interval
	}{
		{
			name: "classic",
			in:   list([2]int{1, 3}, [2]int{2, 6}, [2]int{8, 10}, [2]int{15, 18}),
			want: list([2]int{1, 6}, [2]int{8, 10}, [2]int{15, 18}),
		},
		{
			name: "touching merge",
			in:   list([2]int{1, 2}, [2]int{2, 3}),
			want: list([2]int{1, 3}),
		},
		{
			name: "contained",
			in:   list([2]int{1, 10}, [2]int{3, 4}),
			want: list([2]int{1, 10}),
		},
		{
			name: "unsorted input",
			in:   list([2]int{8, 10}, [2]int{1, 3}, [2]int{2, 6}),
			want: list([2]int{1, 6}, [2]int{8, 10}),
		},
		{
			name: "identical",
			in:   list([2]int{1, 2}, [2]int{1, 2}, [2]int{1, 2}),
			want: list([2]int{1, 2}),
		},
		{
			name: "empties are dropped",
			in:   list([2]int{1, 2}, [2]int{5, 5}, [2]int{7, 3}),
			want: list([2]int{1, 2}),
		},
		{name: "nothing", in: nil, want: nil},
		{name: "all empty", in: list([2]int{5, 5}), want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Merge(tt.in)

			if !slices.Equal(got, tt.want) {
				t.Errorf("Merge(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestMergeProperties checks what merging means rather than a fixed answer: the output is
// sorted, disjoint, non-touching, and covers exactly the same points as the input.
func TestMergeProperties(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))

	for range 5000 {
		n := r.IntN(12)
		in := make([]Interval, n)
		for i := range in {
			start := r.IntN(20)
			in[i] = iv(start, start+r.IntN(6))
		}

		got := Merge(in)

		// Sorted, disjoint and not even touching.
		for i := 1; i < len(got); i++ {
			if got[i-1].End >= got[i].Start {
				t.Fatalf("Merge(%v) = %v: %v and %v touch or overlap",
					in, got, got[i-1], got[i])
			}
		}

		// The same set of covered points, checked directly over a small universe.
		for point := -1; point <= 30; point++ {
			wantCovered := false
			for _, x := range in {
				if x.Contains(point) {
					wantCovered = true
				}
			}

			gotCovered := false
			for _, x := range got {
				if x.Contains(point) {
					gotCovered = true
				}
			}

			if gotCovered != wantCovered {
				t.Fatalf("Merge(%v) = %v: point %d covered = %v, want %v",
					in, got, point, gotCovered, wantCovered)
			}
		}
	}
}

func TestMergeDoesNotMutateItsInput(t *testing.T) {
	in := list([2]int{8, 10}, [2]int{1, 3}, [2]int{2, 6})
	before := slices.Clone(in)

	Merge(in)

	if !slices.Equal(in, before) {
		t.Errorf("Merge mutated its input: %v then %v", before, in)
	}
}

func TestInsert(t *testing.T) {
	tests := []struct {
		name   string
		sorted []Interval
		add    Interval
		want   []Interval
	}{
		{
			name:   "between two",
			sorted: list([2]int{1, 3}, [2]int{6, 9}),
			add:    iv(2, 5),
			want:   list([2]int{1, 5}, [2]int{6, 9}),
		},
		{
			name:   "swallows several",
			sorted: list([2]int{1, 2}, [2]int{3, 5}, [2]int{6, 7}, [2]int{8, 10}, [2]int{12, 16}),
			add:    iv(4, 8),
			want:   list([2]int{1, 2}, [2]int{3, 10}, [2]int{12, 16}),
		},
		{
			name:   "before everything",
			sorted: list([2]int{5, 7}),
			add:    iv(1, 2),
			want:   list([2]int{1, 2}, [2]int{5, 7}),
		},
		{
			name:   "after everything",
			sorted: list([2]int{1, 2}),
			add:    iv(5, 7),
			want:   list([2]int{1, 2}, [2]int{5, 7}),
		},
		{
			name:   "into an empty list",
			sorted: nil,
			add:    iv(1, 2),
			want:   list([2]int{1, 2}),
		},
		{
			name:   "touching merges",
			sorted: list([2]int{1, 2}),
			add:    iv(2, 3),
			want:   list([2]int{1, 3}),
		},
		{
			name:   "an empty interval changes nothing",
			sorted: list([2]int{1, 2}),
			add:    iv(5, 5),
			want:   list([2]int{1, 2}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Insert(tt.sorted, tt.add)

			if !slices.Equal(got, tt.want) {
				t.Errorf("Insert(%v, %v) = %v, want %v", tt.sorted, tt.add, got, tt.want)
			}
		})
	}
}

// TestInsertMatchesMerge: the O(n) three-phase walk must always agree with the obvious
// O(n log n) append-and-merge.
func TestInsertMatchesMerge(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))

	for range 5000 {
		n := r.IntN(8)
		raw := make([]Interval, n)
		for i := range raw {
			start := r.IntN(20)
			raw[i] = iv(start, start+1+r.IntN(5))
		}
		sorted := Merge(raw)

		start := r.IntN(22)
		add := iv(start, start+r.IntN(6))

		got := Insert(sorted, add)
		want := Merge(append(slices.Clone(sorted), add))

		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !slices.Equal(got, want) {
			t.Fatalf("Insert(%v, %v) = %v, Merge says %v", sorted, add, got, want)
		}
	}
}

func TestInsertDoesNotMutateItsInput(t *testing.T) {
	sorted := list([2]int{1, 3}, [2]int{6, 9})
	before := slices.Clone(sorted)

	Insert(sorted, iv(2, 5))

	if !slices.Equal(sorted, before) {
		t.Errorf("Insert mutated its input: %v then %v", before, sorted)
	}
}

func TestAnyOverlap(t *testing.T) {
	tests := []struct {
		name string
		in   []Interval
		want bool
	}{
		{"can attend all", list([2]int{7, 10}, [2]int{2, 4}), false},
		{"cannot", list([2]int{0, 30}, [2]int{5, 10}, [2]int{15, 20}), true},
		{"back to back is fine", list([2]int{1, 2}, [2]int{2, 3}, [2]int{3, 4}), false},
		{"one meeting", list([2]int{1, 5}), false},
		{"none", nil, false},
		{"identical", list([2]int{1, 2}, [2]int{1, 2}), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b, got := AnyOverlap(tt.in)

			if got != tt.want {
				t.Errorf("AnyOverlap(%v) = %v, want %v", tt.in, got, tt.want)
			}
			if got && !a.Overlaps(b) {
				t.Errorf("AnyOverlap returned %v and %v, which do not overlap", a, b)
			}
		})
	}
}

// TestAnyOverlapMatchesAllPairs: checking only adjacent pairs after sorting is the
// optimisation, and it has to give the same answer as comparing every pair.
func TestAnyOverlapMatchesAllPairs(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 7))

	for range 5000 {
		n := r.IntN(10)
		in := make([]Interval, n)
		for i := range in {
			start := r.IntN(15)
			in[i] = iv(start, start+1+r.IntN(4))
		}

		want := false
		for i := range in {
			for j := i + 1; j < n; j++ {
				if in[i].Overlaps(in[j]) {
					want = true
				}
			}
		}

		if _, _, got := AnyOverlap(in); got != want {
			t.Fatalf("AnyOverlap(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestMinRooms(t *testing.T) {
	tests := []struct {
		name string
		in   []Interval
		want int
	}{
		{"classic", list([2]int{0, 30}, [2]int{5, 10}, [2]int{15, 20}), 2},
		{"no overlap", list([2]int{7, 10}, [2]int{2, 4}), 1},
		{"back to back needs one room", list([2]int{1, 2}, [2]int{2, 3}, [2]int{3, 4}), 1},
		{"all at once", list([2]int{1, 5}, [2]int{1, 5}, [2]int{1, 5}), 3},
		{"nested", list([2]int{1, 10}, [2]int{2, 9}, [2]int{3, 8}), 3},
		{"none", nil, 0},
		{"one", list([2]int{1, 2}), 1},
		{"empties ignored", list([2]int{5, 5}, [2]int{1, 2}), 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MinRooms(tt.in); got != tt.want {
				t.Errorf("MinRooms(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// TestMinRoomsMatchesCounting: the sweep is the clever version, and the obvious one is to
// count, for every instant, how many intervals cover it.
func TestMinRoomsMatchesCounting(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 11))

	for range 5000 {
		n := r.IntN(10)
		in := make([]Interval, n)
		for i := range in {
			start := r.IntN(15)
			in[i] = iv(start, start+r.IntN(5))
		}

		want := 0
		for point := -1; point <= 25; point++ {
			covering := 0
			for _, x := range in {
				if x.Contains(point) {
					covering++
				}
			}
			want = max(want, covering)
		}

		if got := MinRooms(in); got != want {
			t.Fatalf("MinRooms(%v) = %d, want %d", in, got, want)
		}
	}
}

func TestMaxNonOverlapping(t *testing.T) {
	tests := []struct {
		name string
		in   []Interval
		want int
	}{
		{
			// The case that defeats "sort by start": [0,10) blocks both others.
			name: "a long early interval",
			in:   list([2]int{0, 10}, [2]int{1, 2}, [2]int{3, 4}),
			want: 2,
		},
		{
			// The case that defeats "take the shortest": [4,6) blocks both others.
			name: "the shortest is not the best",
			in:   list([2]int{1, 5}, [2]int{4, 6}, [2]int{5, 9}),
			want: 2,
		},
		{"classic", list([2]int{1, 2}, [2]int{2, 3}, [2]int{3, 4}, [2]int{1, 3}), 3},
		{"all overlapping", list([2]int{1, 5}, [2]int{2, 6}, [2]int{3, 7}), 1},
		{"none overlapping", list([2]int{1, 2}, [2]int{3, 4}), 2},
		{"none", nil, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MaxNonOverlapping(tt.in)

			if len(got) != tt.want {
				t.Errorf("MaxNonOverlapping(%v) kept %v (%d), want %d", tt.in, got, len(got), tt.want)
			}

			// Whatever it keeps must genuinely not overlap.
			for i := range got {
				for j := i + 1; j < len(got); j++ {
					if got[i].Overlaps(got[j]) {
						t.Errorf("kept %v and %v, which overlap", got[i], got[j])
					}
				}
			}
		})
	}
}

// TestGreedyByEndIsOptimal is the heart of the pattern: the earliest-finishing choice is
// checked against exhaustive search, and the two plausible wrong greedies are checked to be
// wrong.
func TestGreedyByEndIsOptimal(t *testing.T) {
	r := rand.New(rand.NewPCG(13, 17))

	byStart := func(in []Interval) int {
		sorted := slices.Clone(in)
		slices.SortFunc(sorted, ByStart)

		kept, lastEnd, started := 0, 0, false
		for _, x := range sorted {
			if x.IsEmpty() || (started && x.Start < lastEnd) {
				continue
			}
			kept++
			lastEnd = x.End
			started = true
		}
		return kept
	}

	byShortest := func(in []Interval) int {
		sorted := slices.Clone(in)
		slices.SortFunc(sorted, func(a, b Interval) int { return a.Length() - b.Length() })

		var chosen []Interval
		for _, x := range sorted {
			if x.IsEmpty() {
				continue
			}
			conflicts := false
			for _, c := range chosen {
				if c.Overlaps(x) {
					conflicts = true
					break
				}
			}
			if !conflicts {
				chosen = append(chosen, x)
			}
		}
		return len(chosen)
	}

	startWorse, shortestWorse := 0, 0

	for range 3000 {
		n := r.IntN(9)
		in := make([]Interval, n)
		for i := range in {
			start := r.IntN(12)
			in[i] = iv(start, start+1+r.IntN(5))
		}

		// Exhaustive: the largest non-overlapping subset.
		best := 0
		for mask := 0; mask < 1<<n; mask++ {
			var subset []Interval
			for i := range in {
				if mask&(1<<i) != 0 {
					subset = append(subset, in[i])
				}
			}

			ok := true
			for i := range subset {
				for j := i + 1; j < len(subset); j++ {
					if subset[i].Overlaps(subset[j]) {
						ok = false
					}
				}
			}
			if ok {
				best = max(best, len(subset))
			}
		}

		if got := len(MaxNonOverlapping(in)); got != best {
			t.Fatalf("MaxNonOverlapping(%v) kept %d, the optimum is %d", in, got, best)
		}
		if got := MinRemovals(in); got != n-best {
			t.Fatalf("MinRemovals(%v) = %d, want %d", in, got, n-best)
		}

		if byStart(in) < best {
			startWorse++
		}
		if byShortest(in) < best {
			shortestWorse++
		}
	}

	t.Logf("sorting by start was suboptimal on %d of 3000 inputs", startWorse)
	t.Logf("taking the shortest first was suboptimal on %d of 3000 inputs", shortestWorse)

	if startWorse == 0 {
		t.Error("sorting by start was never worse; the test inputs are too easy to prove anything")
	}
	if shortestWorse == 0 {
		t.Error("taking the shortest was never worse; the test inputs are too easy")
	}
}

func TestIntersection(t *testing.T) {
	a := list([2]int{0, 2}, [2]int{5, 10}, [2]int{13, 23}, [2]int{24, 25})
	b := list([2]int{1, 5}, [2]int{8, 12}, [2]int{15, 24}, [2]int{25, 26})

	got := Intersection(a, b)
	want := list([2]int{1, 2}, [2]int{8, 10}, [2]int{15, 23})

	if !slices.Equal(got, want) {
		t.Errorf("Intersection = %v, want %v", got, want)
	}

	if got := Intersection(nil, b); got != nil {
		t.Errorf("Intersection(nil, b) = %v", got)
	}
	if got := Intersection(a, nil); got != nil {
		t.Errorf("Intersection(a, nil) = %v", got)
	}
}

func TestIntersectionMatchesPointwise(t *testing.T) {
	r := rand.New(rand.NewPCG(19, 23))

	for range 3000 {
		mk := func() []Interval {
			n := r.IntN(6)
			raw := make([]Interval, n)
			for i := range raw {
				start := r.IntN(20)
				raw[i] = iv(start, start+1+r.IntN(4))
			}
			return Merge(raw)
		}

		a, b := mk(), mk()
		got := Intersection(a, b)

		for point := -1; point <= 28; point++ {
			inA, inB, inGot := false, false, false
			for _, x := range a {
				inA = inA || x.Contains(point)
			}
			for _, x := range b {
				inB = inB || x.Contains(point)
			}
			for _, x := range got {
				inGot = inGot || x.Contains(point)
			}

			if inGot != (inA && inB) {
				t.Fatalf("Intersection(%v, %v) = %v: point %d in result = %v, want %v",
					a, b, got, point, inGot, inA && inB)
			}
		}
	}
}

func TestSubtract(t *testing.T) {
	tests := []struct {
		name    string
		base    Interval
		covered []Interval
		want    []Interval
	}{
		{
			name:    "a hole in the middle",
			base:    iv(0, 10),
			covered: list([2]int{3, 5}),
			want:    list([2]int{0, 3}, [2]int{5, 10}),
		},
		{
			name:    "two holes",
			base:    iv(0, 10),
			covered: list([2]int{2, 3}, [2]int{6, 8}),
			want:    list([2]int{0, 2}, [2]int{3, 6}, [2]int{8, 10}),
		},
		{
			name:    "fully covered",
			base:    iv(0, 10),
			covered: list([2]int{0, 10}),
			want:    nil,
		},
		{
			name:    "covered beyond the edges",
			base:    iv(2, 8),
			covered: list([2]int{0, 20}),
			want:    nil,
		},
		{
			name:    "nothing covered",
			base:    iv(0, 5),
			covered: nil,
			want:    list([2]int{0, 5}),
		},
		{
			name:    "covers only the start",
			base:    iv(0, 5),
			covered: list([2]int{0, 2}),
			want:    list([2]int{2, 5}),
		},
		{
			name:    "covers only the end",
			base:    iv(0, 5),
			covered: list([2]int{3, 9}),
			want:    list([2]int{0, 3}),
		},
		{
			name:    "irrelevant blocks",
			base:    iv(5, 10),
			covered: list([2]int{0, 2}, [2]int{20, 30}),
			want:    list([2]int{5, 10}),
		},
		{name: "empty base", base: iv(5, 5), covered: nil, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Subtract(tt.base, tt.covered)

			if !slices.Equal(got, tt.want) {
				t.Errorf("Subtract(%v, %v) = %v, want %v", tt.base, tt.covered, got, tt.want)
			}
		})
	}
}

func TestSubtractMatchesPointwise(t *testing.T) {
	r := rand.New(rand.NewPCG(29, 31))

	for range 5000 {
		start := r.IntN(15)
		base := iv(start, start+r.IntN(10))

		n := r.IntN(6)
		covered := make([]Interval, n)
		for i := range covered {
			s := r.IntN(25)
			covered[i] = iv(s, s+r.IntN(5))
		}

		got := Subtract(base, covered)

		for point := -1; point <= 30; point++ {
			inBase := base.Contains(point)

			inCovered := false
			for _, x := range covered {
				inCovered = inCovered || x.Contains(point)
			}

			inGot := false
			for _, x := range got {
				inGot = inGot || x.Contains(point)
			}

			if inGot != (inBase && !inCovered) {
				t.Fatalf("Subtract(%v, %v) = %v: point %d in result = %v, want %v",
					base, covered, got, point, inGot, inBase && !inCovered)
			}
		}
	}
}

func TestTotalCovered(t *testing.T) {
	tests := []struct {
		in   []Interval
		want int
	}{
		{list([2]int{1, 3}, [2]int{2, 6}), 5},
		{list([2]int{1, 2}, [2]int{3, 4}), 2},
		{list([2]int{1, 5}, [2]int{1, 5}), 4},
		{nil, 0},
		{list([2]int{5, 5}), 0},
	}

	for _, tt := range tests {
		if got := TotalCovered(tt.in); got != tt.want {
			t.Errorf("TotalCovered(%v) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

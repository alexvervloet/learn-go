// Package intervals implements the intervals pattern.
//
// # The tell
//
// A list of [start, end] pairs plus "overlap", "merge", "schedule", "how many rooms" or
// "can they all fit".
//
// # Sort first
//
// Almost every interval problem begins by sorting, and WHICH endpoint you sort by is the
// decision that solves the problem:
//
//	by START   merging, inserting, "do any overlap"
//	by END     "how many can I fit without overlap", the classic greedy scheduling result
//
// Sorting by start and then greedily keeping the first interval is WRONG for the
// scheduling problem, and it is wrong in a way that looks right on small examples. See
// MaxNonOverlapping.
//
// # Half-open or closed
//
// The other decision, and the one that causes more bugs than the algorithms do. Does
// [1,2] overlap [2,3]?
//
//	CLOSED intervals, [1,2] and [2,3] share the point 2, so yes
//	HALF-OPEN [1,2) and [2,3) touch and do not overlap, so no
//
// A meeting from 1 to 2 and one from 2 to 3 do not conflict; a fence post at 1 to 2 and
// one at 2 to 3 do share a post. This package uses HALF-OPEN throughout, which is the
// convention that matches scheduling, and every function says so. Getting it backwards
// changes answers by exactly one in the cases where it matters, which is the hardest kind
// of bug to spot.
package intervals

import (
	"cmp"
	"slices"
)

// Interval is a half-open range [Start, End). An interval with End <= Start is empty.
type Interval struct {
	Start, End int
}

// IsEmpty reports whether the interval contains no points.
func (i Interval) IsEmpty() bool { return i.End <= i.Start }

// Length returns End-Start, or 0 for an empty interval.
func (i Interval) Length() int {
	if i.IsEmpty() {
		return 0
	}
	return i.End - i.Start
}

// Contains reports whether point is in [Start, End).
func (i Interval) Contains(point int) bool { return point >= i.Start && point < i.End }

// Overlaps reports whether two intervals share at least one point.
//
// The condition is the whole half-open convention in one line: a.Start < b.End AND
// b.Start < a.End. With `<=` instead, touching intervals would count as overlapping, which
// is the closed-interval answer.
func (i Interval) Overlaps(other Interval) bool {
	if i.IsEmpty() || other.IsEmpty() {
		return false
	}
	return i.Start < other.End && other.Start < i.End
}

// Touches reports whether two intervals overlap or abut, so [1,2) touches [2,3).
//
// This is what merging uses: [1,2) and [2,3) should merge into [1,3), even though they do
// not overlap. Overlap and adjacency are different questions and both come up.
func (i Interval) Touches(other Interval) bool {
	if i.IsEmpty() || other.IsEmpty() {
		return false
	}
	return i.Start <= other.End && other.Start <= i.End
}

// Intersect returns the overlap of two intervals, which is empty when they do not overlap.
func (i Interval) Intersect(other Interval) Interval {
	return Interval{Start: max(i.Start, other.Start), End: min(i.End, other.End)}
}

// ByStart orders intervals by start, then by end.
//
// The tie-break on End is not cosmetic: without it the order of equal-start intervals
// depends on the sort's stability, so a result that happens to be right with one sort
// changes with another.
func ByStart(a, b Interval) int {
	if a.Start != b.Start {
		return cmp.Compare(a.Start, b.Start)
	}
	return cmp.Compare(a.End, b.End)
}

// ByEnd orders intervals by end, then by start.
func ByEnd(a, b Interval) int {
	if a.End != b.End {
		return cmp.Compare(a.End, b.End)
	}
	return cmp.Compare(a.Start, b.Start)
}

// Merge returns the given intervals combined so that no two overlap or touch, sorted by
// start.
//
// Sort by start, then walk: each interval either extends the current one or begins a new
// one. O(n log n) for the sort and O(n) for the walk, so the sort is the whole cost.
//
// Empty intervals are dropped, because an interval containing no points cannot merge with
// anything and keeping it would produce output that does not satisfy the postcondition.
func Merge(in []Interval) []Interval {
	sorted := make([]Interval, 0, len(in))
	for _, iv := range in {
		if !iv.IsEmpty() {
			sorted = append(sorted, iv)
		}
	}
	if len(sorted) == 0 {
		return nil
	}

	slices.SortFunc(sorted, ByStart)

	out := []Interval{sorted[0]}

	for _, iv := range sorted[1:] {
		last := &out[len(out)-1]

		// Touches, not Overlaps: [1,2) and [2,3) merge into [1,3).
		if iv.Start <= last.End {
			last.End = max(last.End, iv.End)
			continue
		}

		out = append(out, iv)
	}

	return out
}

// Insert adds one interval to an already-merged, start-sorted list and returns the result,
// still merged and sorted.
//
// O(n), because the input is already sorted. Appending and calling Merge is O(n log n) and
// the usual first answer; the three-phase walk below is the reason to know the pattern.
//
// The three phases are the shape worth remembering:
//
//  1. copy everything strictly before the new interval
//  2. absorb everything that touches it, widening as you go
//  3. copy everything strictly after
func Insert(sorted []Interval, add Interval) []Interval {
	if add.IsEmpty() {
		return slices.Clone(sorted)
	}

	out := make([]Interval, 0, len(sorted)+1)
	at := 0

	// Phase 1: entirely before, with no contact.
	for at < len(sorted) && sorted[at].End < add.Start {
		out = append(out, sorted[at])
		at++
	}

	// Phase 2: everything touching, merged into add.
	for at < len(sorted) && sorted[at].Start <= add.End {
		add.Start = min(add.Start, sorted[at].Start)
		add.End = max(add.End, sorted[at].End)
		at++
	}
	out = append(out, add)

	// Phase 3: the rest.
	out = append(out, sorted[at:]...)

	return out
}

// AnyOverlap reports whether any two of the given intervals overlap, and returns one such
// pair.
//
// "Can this person attend all these meetings". Sort by start, then only ADJACENT pairs need
// checking: if any two overlap, two adjacent ones do. That is what turns the obvious O(n^2)
// comparison of every pair into O(n log n).
func AnyOverlap(in []Interval) (Interval, Interval, bool) {
	sorted := slices.Clone(in)
	slices.SortFunc(sorted, ByStart)

	for i := 1; i < len(sorted); i++ {
		if sorted[i-1].Overlaps(sorted[i]) {
			return sorted[i-1], sorted[i], true
		}
	}

	return Interval{}, Interval{}, false
}

// MinRooms returns the largest number of intervals that overlap at any one moment.
//
// "How many meeting rooms are needed". The answer is the maximum number of simultaneous
// intervals, and the clean way to get it is a SWEEP: turn each interval into a +1 at its
// start and a -1 at its end, sort the events, and track the running total.
//
// The tie-break is the half-open convention again. At a moment where one meeting ends and
// another begins, the END must be processed first, or the room is counted twice. Sorting
// ends before starts at the same time is that rule, and getting it backwards gives an
// answer one too high on exactly the inputs where rooms are tight.
func MinRooms(in []Interval) int {
	type event struct {
		at    int
		delta int
	}

	events := make([]event, 0, 2*len(in))
	for _, iv := range in {
		if iv.IsEmpty() {
			continue
		}
		events = append(events, event{at: iv.Start, delta: +1})
		events = append(events, event{at: iv.End, delta: -1})
	}

	slices.SortFunc(events, func(a, b event) int {
		if a.at != b.at {
			return cmp.Compare(a.at, b.at)
		}
		// -1 before +1 at the same instant: a room freed at 10 is available at 10.
		return cmp.Compare(a.delta, b.delta)
	})

	current, most := 0, 0
	for _, e := range events {
		current += e.delta
		most = max(most, current)
	}

	return most
}

// MaxNonOverlapping returns the largest number of mutually non-overlapping intervals that
// can be chosen, and which ones.
//
// The classic greedy scheduling result, and the greedy choice is the point: sort by END and
// always take the interval that finishes EARLIEST among those that still fit.
//
// Sorting by start and taking the first is wrong. For [0,10), [1,2), [3,4) it keeps only
// [0,10) where three fit, because a long early interval blocks everything. Sorting by
// LENGTH and taking the shortest is also wrong, for [1,5), [4,6), [5,9): the shortest is
// [4,6) and it blocks both others, where [1,5) and [5,9) both fit.
//
// Finishing earliest is optimal because it leaves the most room for everything after it,
// and nothing is given up: any schedule can have its first interval swapped for the
// earliest-finishing one without conflict.
func MaxNonOverlapping(in []Interval) []Interval {
	sorted := make([]Interval, 0, len(in))
	for _, iv := range in {
		if !iv.IsEmpty() {
			sorted = append(sorted, iv)
		}
	}

	slices.SortFunc(sorted, ByEnd)

	var out []Interval
	lastEnd := 0
	started := false

	for _, iv := range sorted {
		if started && iv.Start < lastEnd {
			continue // overlaps the one we kept
		}

		out = append(out, iv)
		lastEnd = iv.End
		started = true
	}

	return out
}

// MinRemovals returns how many intervals must be dropped so none of the rest overlap.
//
// The same problem as MaxNonOverlapping, counted the other way round, and saying so is the
// point: "remove the fewest" and "keep the most" are one question.
func MinRemovals(in []Interval) int {
	nonEmpty := 0
	for _, iv := range in {
		if !iv.IsEmpty() {
			nonEmpty++
		}
	}
	return nonEmpty - len(MaxNonOverlapping(in))
}

// Intersection returns the intervals common to two already-merged, start-sorted lists.
//
// Two pointers over sorted lists, O(m+n). The pair with the smaller END is the one to
// advance, because it cannot intersect anything later in the other list.
func Intersection(a, b []Interval) []Interval {
	var out []Interval
	i, j := 0, 0

	for i < len(a) && j < len(b) {
		if overlap := a[i].Intersect(b[j]); !overlap.IsEmpty() {
			out = append(out, overlap)
		}

		// Advance whichever ends first: it has nothing left to offer the other list.
		if a[i].End < b[j].End {
			i++
			continue
		}
		j++
	}

	return out
}

// Subtract removes the covered ranges from base and returns what is left.
//
// "Which parts of this window are still free". The covered list is merged first, so the
// walk can assume it is sorted and disjoint.
func Subtract(base Interval, covered []Interval) []Interval {
	if base.IsEmpty() {
		return nil
	}

	var out []Interval
	at := base.Start

	for _, block := range Merge(covered) {
		if block.End <= at {
			continue // entirely before what is left
		}
		if block.Start >= base.End {
			break // entirely after, and the rest are too
		}

		if block.Start > at {
			out = append(out, Interval{Start: at, End: block.Start})
		}

		at = max(at, block.End)
		if at >= base.End {
			return out
		}
	}

	if at < base.End {
		out = append(out, Interval{Start: at, End: base.End})
	}

	return out
}

// TotalCovered returns the total length covered by the given intervals, counting overlaps
// once.
func TotalCovered(in []Interval) int {
	total := 0
	for _, iv := range Merge(in) {
		total += iv.Length()
	}
	return total
}

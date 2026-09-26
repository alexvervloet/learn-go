package heap

import "cmp"

// Running median with two heaps
// =============================
//
// "Report the median after every value in a stream" is the problem that shows what a
// heap is really for. Keeping a sorted slice costs O(n) per insertion; sorting on
// every query costs O(n log n). Two heaps cost O(log n) per insertion and O(1) per
// query.
//
// The idea is to split the values in half and point the two halves at each other:
//
//	lower: a MAX-heap of the smaller half   ->  its top is the largest small value
//	upper: a MIN-heap of the larger half    ->  its top is the smallest large value
//
//	          lower (max-heap)        upper (min-heap)
//	          [1 3 5 |7|]             [|8| 9 12 20]
//	                  ^                 ^
//	                  the median lives between these two
//
// The median is one or both of those two tops, so reading it is O(1). The whole
// difficulty is keeping the two halves balanced, which is the Add method.

// MedianStream reports the median of every value added so far.
//
// The zero value is not usable: use NewMedianStream.
type MedianStream[T cmp.Ordered] struct {
	lower *Heap[T] // max-heap: the smaller half
	upper *Heap[T] // min-heap: the larger half
}

// NewMedianStream returns an empty stream.
func NewMedianStream[T cmp.Ordered]() *MedianStream[T] {
	return &MedianStream[T]{
		lower: NewMax[T](),
		upper: NewMin[T](),
	}
}

// Len reports how many values have been added.
func (m *MedianStream[T]) Len() int { return m.lower.Len() + m.upper.Len() }

// Add inserts v, in O(log n).
//
// The two-step shape is the part worth learning. Push onto whichever side v belongs,
// then rebalance so the sizes differ by at most one. Trying to do both at once means
// a case analysis that is easy to get wrong; doing them in sequence means each step
// is obviously correct.
func (m *MedianStream[T]) Add(v T) {
	// Step one: which half does v belong in?
	top, ok := m.lower.Peek()

	if !ok || v <= top {
		m.lower.Push(v)
	} else {
		m.upper.Push(v)
	}

	// Step two: rebalance. lower is allowed to hold one more than upper, which is
	// the convention that makes the odd-count median lower.Peek().
	switch {
	case m.lower.Len() > m.upper.Len()+1:
		moved, _ := m.lower.Pop()
		m.upper.Push(moved)

	case m.upper.Len() > m.lower.Len():
		moved, _ := m.upper.Pop()
		m.lower.Push(moved)
	}
}

// Median returns the middle value, and whether there is one.
//
// For an even count it returns the LOWER of the two middle values rather than their
// mean, because T is cmp.Ordered and averaging is not defined for strings. MedianOf
// below is the numeric version.
func (m *MedianStream[T]) Median() (T, bool) {
	return m.lower.Peek()
}

// MedianPair returns the one or two values the median sits between.
//
// For an odd count both returned values are the median itself. For an even count they
// are the two middle values, which is everything a caller needs to compute a mean, a
// midpoint, or either side.
func (m *MedianStream[T]) MedianPair() (low, high T, ok bool) {
	lowVal, ok := m.lower.Peek()
	if !ok {
		var zero T
		return zero, zero, false
	}

	if m.lower.Len() > m.upper.Len() {
		return lowVal, lowVal, true
	}

	highVal, _ := m.upper.Peek()
	return lowVal, highVal, true
}

// Number is the constraint for values that can be averaged.
type Number interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~float32 | ~float64
}

// MedianFloat returns the median as a float64, averaging the two middle values when
// the count is even.
func MedianFloat[T Number](m *MedianStream[T]) (float64, bool) {
	low, high, ok := m.MedianPair()
	if !ok {
		return 0, false
	}
	return (float64(low) + float64(high)) / 2, true
}

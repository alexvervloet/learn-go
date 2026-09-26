package queue

import "testing"

// The steady state that tells the three implementations apart: a queue holding
// `window` elements, with one push and one pop per iteration. This is what a
// work queue or a request buffer actually does.
const window = 1000

func BenchmarkRingBuffer(b *testing.B) {
	q := New[int](window)
	for i := 0; i < window; i++ {
		q.Push(i)
	}

	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		q.Push(i)
		q.Pop()
	}
}

func BenchmarkNaiveReslice(b *testing.B) {
	var q naiveReslice[int]
	for i := 0; i < window; i++ {
		q.Push(i)
	}

	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		q.Push(i)
		q.Pop()
	}
}

func BenchmarkShiftDown(b *testing.B) {
	var q shiftDown[int]
	for i := 0; i < window; i++ {
		q.Push(i)
	}

	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		q.Push(i)
		q.Pop()
	}
}

// How much memory each one is holding after a long churn, which is the claim
// worth checking rather than asserting: the naive version keeps reallocating,
// so the live array is bounded, but it never stops copying.
func BenchmarkDrain(b *testing.B) {
	const n = 10_000

	b.Run("ring", func(b *testing.B) {
		for b.Loop() {
			q := New[int](8)
			for i := 0; i < n; i++ {
				q.Push(i)
			}
			for q.Len() > 0 {
				q.Pop()
			}
		}
	})

	b.Run("reslice", func(b *testing.B) {
		for b.Loop() {
			var q naiveReslice[int]
			for i := 0; i < n; i++ {
				q.Push(i)
			}
			for q.Len() > 0 {
				q.Pop()
			}
		}
	})

	b.Run("shift", func(b *testing.B) {
		for b.Loop() {
			var q shiftDown[int]
			for i := 0; i < n; i++ {
				q.Push(i)
			}
			for q.Len() > 0 {
				q.Pop()
			}
		}
	})
}

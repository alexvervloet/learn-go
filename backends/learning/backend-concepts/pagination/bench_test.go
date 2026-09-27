package pagination

import (
	"context"
	"testing"
	"time"
)

// timeFixture is a fixed instant, so the codec benchmark measures the same work every run.
var timeFixture = time.Date(2025, 6, 1, 12, 0, 0, 123456789, time.UTC)

// BenchmarkPageDepth is the shape of the problem in one table: what page 1 costs against page 2,251.
//
// The row counts in TestOffsetCostGrowsWithTheOffset are the assertion, because they hold anywhere. This is
// the same fact in milliseconds, which is what makes it feel real.
func BenchmarkPageDepth(b *testing.B) {
	db := dataset(b)
	ctx := context.Background()

	// The cursors for the same depths, computed once so the benchmark measures the query and not the
	// walk to get there.
	cursors := map[int]string{0: ""}

	for _, offset := range []int{100, 5_000, 45_000} {
		page, err := Keyset(ctx, db, offset, "")
		if err != nil {
			b.Fatal(err)
		}
		if !page.HasMore {
			b.Fatalf("only %d rows available, need more than %d", len(page.Events), offset)
		}
		cursors[offset] = page.Next
	}

	for _, offset := range []int{0, 100, 5_000, 45_000} {
		b.Run("offset/"+itoa(offset), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Offset(ctx, db, 20, offset); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("keyset/"+itoa(offset), func(b *testing.B) {
			cursor := cursors[offset]

			b.ReportAllocs()
			for b.Loop() {
				if _, err := Keyset(ctx, db, 20, cursor); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkCount is the other half: why a page number needs a total and a total is expensive.
func BenchmarkCount(b *testing.B) {
	db := dataset(b)
	ctx := context.Background()

	b.Run("count(*)", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := Count(ctx, db); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("reltuples", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := CountEstimate(ctx, db); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkCursorCodec, because a cursor is encoded and decoded on every request and it should not show up
// anywhere near the query cost.
func BenchmarkCursorCodec(b *testing.B) {
	c := Cursor{CreatedAt: timeFixture, ID: 4242}
	encoded := EncodeCursor(c)

	b.Run("encode", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = EncodeCursor(c)
		}
	})

	b.Run("decode", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := DecodeCursor(encoded); err != nil {
				b.Fatal(err)
			}
		}
	})
}

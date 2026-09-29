package redistest

import (
	"context"
	"errors"
	"testing"
)

// TestFlushRefusesADatabaseItDoesNotOwn plants a key that is not the harness's, in a database without the
// marker, and checks the key survives.
func TestFlushRefusesADatabaseItDoesNotOwn(t *testing.T) {
	c := Client(t)
	ctx := context.Background()

	Flush(t)

	// Someone else's data: drop the marker and write a key of theirs.
	if err := c.Del(ctx, MarkerKey).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(ctx, "someones:session", "keep me", 0).Err(); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		// Put the marker back so the next run can claim the database again.
		_ = c.Set(ctx, MarkerKey, "restored by the test", 0).Err()
	})

	err := flushOwned(ctx, c)

	if !errors.Is(err, ErrForeignDatabase) {
		t.Fatalf("flushOwned on a foreign database: err = %v, want ErrForeignDatabase", err)
	}

	t.Logf("refused: %v", err)

	if got, _ := c.Get(ctx, "someones:session").Result(); got != "keep me" {
		t.Errorf("the foreign key was deleted, value now %q", got)
	}

	// With the marker present again, the same database is the harness's to empty.
	if err := c.Set(ctx, MarkerKey, "x", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := flushOwned(ctx, c); err != nil {
		t.Fatalf("flushOwned with the marker: %v", err)
	}
}

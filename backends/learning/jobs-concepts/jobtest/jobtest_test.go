package jobtest

import (
	"context"
	"errors"
	"testing"

	"github.com/redis/go-redis/v9"
)

// TestFlushRefusesADatabaseItDoesNotOwn plants a key that is not the harness's, in a database without the
// marker, and checks the key survives.
func TestFlushRefusesADatabaseItDoesNotOwn(t *testing.T) {
	Flush(t)

	c := redis.NewClient(&redis.Options{Addr: Addr(), DB: database()})
	t.Cleanup(func() { _ = c.Close() })

	ctx := context.Background()

	if err := c.Del(ctx, MarkerKey).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(ctx, "someones:session", "keep me", 0).Err(); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = c.Set(ctx, MarkerKey, "restored by the test", 0).Err() })

	err := flushOwned(ctx, c)

	if !errors.Is(err, ErrForeignDatabase) {
		t.Fatalf("flushOwned on a foreign database: err = %v, want ErrForeignDatabase", err)
	}

	if got, _ := c.Get(ctx, "someones:session").Result(); got != "keep me" {
		t.Errorf("the foreign key was deleted, value now %q", got)
	}
}

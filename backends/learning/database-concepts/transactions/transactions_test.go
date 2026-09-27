package transactions

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alexvervloet/learn-go/backends/learning/database-concepts/dbtest"
)

// accounts resets the accounts table to a known state and returns the pool.
//
// Truncate rather than a rolled-back transaction, for the reason in the package doc: these tests
// commit, and a test that commits cannot be isolated by rolling back. That makes them mutually
// exclusive, which is what the absence of t.Parallel in this file means.
func accounts(t *testing.T, balances ...int64) (*pgxpool.Pool, []int64) {
	t.Helper()

	db := dbtest.Pool(t)
	ctx := context.Background()

	dbtest.Truncate(t, "accounts")

	ids := make([]int64, len(balances))

	for i, cents := range balances {
		if err := db.QueryRow(ctx,
			"INSERT INTO accounts (owner, balance_cents) VALUES ($1, $2) RETURNING id",
			"owner", cents).Scan(&ids[i]); err != nil {
			t.Fatalf("inserting account %d: %v", i, err)
		}
	}

	return db, ids
}

func TestTransferMovesMoney(t *testing.T) {
	db, ids := accounts(t, 10_000, 0)
	ctx := context.Background()

	if _, err := Retry(ctx, db, pgx.TxOptions{}, DefaultRetry(), func(tx pgx.Tx) error {
		return Transfer(ctx, tx, ids[0], ids[1], 2_500)
	}); err != nil {
		t.Fatal(err)
	}

	for i, want := range []int64{7_500, 2_500} {
		got, err := Balance(ctx, db, ids[i])
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("account %d: got %d, want %d", i, got, want)
		}
	}
}

func TestTransferRejectsAnOverdraft(t *testing.T) {
	db, ids := accounts(t, 100, 0)
	ctx := context.Background()

	_, err := Retry(ctx, db, pgx.TxOptions{}, DefaultRetry(), func(tx pgx.Tx) error {
		return Transfer(ctx, tx, ids[0], ids[1], 500)
	})

	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("expected ErrInsufficientFunds, got %v", err)
	}

	// And the transaction rolled back, so the credit did not happen either. A version that
	// returned the error after the second UPDATE would leave the money created.
	total, err := Total(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if total != 100 {
		t.Errorf("total is %d, want 100", total)
	}
}

func TestTransferDistinguishesMissingFromEmpty(t *testing.T) {
	db, ids := accounts(t, 100)
	ctx := context.Background()

	_, err := Retry(ctx, db, pgx.TxOptions{}, DefaultRetry(), func(tx pgx.Tx) error {
		return Transfer(ctx, tx, ids[0], 999_999, 50)
	})

	if !errors.Is(err, ErrNoAccount) {
		t.Fatalf("expected ErrNoAccount for the destination, got %v", err)
	}
}

// TestLostUpdate is the anomaly, demonstrated rather than described.
//
// Two goroutines each transfer 600 out of an account holding 1000. Either one alone succeeds and the
// other should fail for insufficient funds. The read-then-write version lets both through.
func TestLostUpdate(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name     string
		transfer func(context.Context, pgx.Tx, int64, int64, int64) error
		iso      pgx.TxIsoLevel
		wantSafe bool
	}{
		{"read-then-write at READ COMMITTED", TransferLostUpdate, pgx.ReadCommitted, false},
		{"read-then-write at REPEATABLE READ", TransferLostUpdate, pgx.RepeatableRead, true},
		{"SELECT FOR UPDATE at READ COMMITTED", TransferSelectForUpdate, pgx.ReadCommitted, true},
		{"the check inside the UPDATE", Transfer, pgx.ReadCommitted, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, ids := accounts(t, 1_000, 0)

			// A barrier, so both transactions have definitely read before either
			// writes. Without it the race usually does not happen and the test passes
			// for the wrong reason.
			var (
				start   = make(chan struct{})
				wg      sync.WaitGroup
				results = make([]error, 2)
			)

			for i := range 2 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start

					// No retry here: the point is to see the raw outcome.
					results[i] = WithTx(ctx, db,
						pgx.TxOptions{IsoLevel: tc.iso},
						func(tx pgx.Tx) error {
							err := tc.transfer(ctx, tx, ids[0], ids[1], 600)
							// A small pause between the read and the
							// commit widens the window the anomaly
							// needs. Real contention does not need help;
							// a test does.
							time.Sleep(20 * time.Millisecond)
							return err
						})
				}()
			}

			close(start)
			wg.Wait()

			succeeded := 0
			for _, err := range results {
				if err == nil {
					succeeded++
					continue
				}
				t.Logf("  one transaction failed with: %v", err)
			}

			from, err := Balance(ctx, db, ids[0])
			if err != nil {
				t.Fatal(err)
			}
			to, err := Balance(ctx, db, ids[1])
			if err != nil {
				t.Fatal(err)
			}

			t.Logf("  %d of 2 committed; balances %d and %d, total %d (started at 1000)",
				succeeded, from, to, from+to)

			if tc.wantSafe {
				if succeeded != 1 {
					t.Errorf("expected exactly one transfer to succeed, %d did",
						succeeded)
				}
				if from+to != 1_000 {
					t.Errorf("money was created or destroyed: total is %d", from+to)
				}
				return
			}

			// The unsafe case is expected to BREAK the invariant, so the assertion is
			// inverted. A test that demonstrates a bug has to fail when the bug is absent,
			// otherwise it is not demonstrating anything.
			if succeeded != 2 {
				t.Errorf("expected both transfers to commit and show the lost update, "+
					"%d did; the goroutines may not have overlapped", succeeded)
			}
			if from+to == 1_000 {
				t.Error("the total is still 1000, so the lost update did not happen")
			} else {
				t.Logf("  600 appeared out of nothing: both transactions read 1000, "+
					"both wrote an absolute 400, and both credited 600, so the "+
					"total is %d", from+to)
			}
		})
	}
}

// TestWriteSkew is the anomaly REPEATABLE READ does not prevent, and the reason SERIALIZABLE exists.
//
// The rule is about the PAIR, not either row: the two accounts must hold at least 600 between them.
// Both start at 500, so the pair holds 1000. Each transaction reads the pair's total, sees that
// withdrawing 300 from its own account leaves 700, and withdraws. Both are individually correct. Both
// commit. The pair now holds 400 and the rule is broken, and no row was ever modified by two
// transactions, so snapshot isolation has nothing to complain about.
//
// The first version of this test had each transaction withdraw 600 from an account holding 500, which
// is an ordinary CHECK constraint violation and fails identically at every isolation level. Write skew
// needs each individual write to be LEGAL. That is exactly what makes it dangerous, and it is easy to
// get wrong when constructing the example.
//
// This is the on-call rota bug (two doctors each check that someone else is on call, then both go
// home), the last-admin bug, and the double-booking bug. It is not exotic.
func TestWriteSkew(t *testing.T) {
	ctx := context.Background()

	// The rule the pair has to satisfy.
	const floor = 600

	for _, iso := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable} {
		t.Run(string(iso), func(t *testing.T) {
			db, ids := accounts(t, 500, 500)

			var (
				start   = make(chan struct{})
				wg      sync.WaitGroup
				results = make([]error, 2)
			)

			for i := range 2 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start

					mine := ids[i]

					results[i] = WithTx(ctx, db,
						pgx.TxOptions{IsoLevel: iso},
						func(tx pgx.Tx) error {
							// Reads BOTH rows. That is the read that
							// SERIALIZABLE tracks and REPEATABLE READ
							// does not.
							var total int64
							if err := tx.QueryRow(ctx,
								"SELECT coalesce(sum(balance_cents), 0) FROM accounts WHERE id = ANY($1)",
								ids).Scan(&total); err != nil {
								return err
							}

							if total-300 < floor {
								return errors.New("the pair would fall below the floor")
							}

							time.Sleep(20 * time.Millisecond)

							// Writes only its own row, and the write is
							// legal: 500 - 300 = 200, well above the
							// CHECK constraint's zero.
							_, err := tx.Exec(ctx,
								"UPDATE accounts SET balance_cents = balance_cents - 300 WHERE id = $1",
								mine)
							return err
						})
				}()
			}

			close(start)
			wg.Wait()

			var (
				committed int
				failures  []string
			)
			for _, err := range results {
				if err == nil {
					committed++
					continue
				}
				failures = append(failures, Code(err)+" "+err.Error())
			}

			total, err := Total(ctx, db)
			if err != nil {
				t.Fatal(err)
			}

			t.Logf("  %d committed, the pair went from 1000 to %d (the rule says >= %d)",
				committed, total, floor)
			for _, f := range failures {
				t.Logf("  failed: %s", f)
			}

			switch iso {
			case pgx.RepeatableRead:
				// Both commit and the rule is broken. Postgres is behaving exactly as
				// specified; snapshot isolation does not promise this.
				if committed != 2 {
					t.Errorf("expected both to commit at %s, %d did", iso, committed)
				}
				if total >= floor {
					t.Errorf("expected the rule to be broken, the pair holds %d", total)
				} else {
					t.Logf("  write skew: neither transaction did anything wrong " +
						"and the rule is broken anyway")
				}

			case pgx.Serializable:
				if committed != 1 {
					t.Errorf("SERIALIZABLE must let exactly one commit, %d did", committed)
				}
				if total < floor {
					t.Errorf("SERIALIZABLE let the rule break: the pair holds %d", total)
				}

				sawSerializationFailure := false
				for _, err := range results {
					if err != nil && Code(err) == SerializationFailure {
						sawSerializationFailure = true
					}
				}
				if !sawSerializationFailure {
					t.Errorf("expected a %s serialization failure, got %v",
						SerializationFailure, failures)
				}
			}
		})
	}
}

// TestRetryMakesSerializableUsable is the payoff: the same workload that returns 40001 to the caller
// succeeds when the caller retries.
func TestRetryMakesSerializableUsable(t *testing.T) {
	db, ids := accounts(t, 5_000, 5_000)
	ctx := context.Background()

	const goroutines = 8

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		attempts []Attempts
		failures []error
	)

	for i := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()

			from, to := ids[i%2], ids[1-i%2]

			a, err := Retry(ctx, db, pgx.TxOptions{IsoLevel: pgx.Serializable},
				RetryConfig{MaxAttempts: 10, BaseDelay: time.Millisecond, MaxDelay: 50 * time.Millisecond},
				func(tx pgx.Tx) error {
					return Transfer(ctx, tx, from, to, 100)
				})

			mu.Lock()
			attempts = append(attempts, a)
			if err != nil {
				failures = append(failures, err)
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	retried, total := 0, 0
	for _, a := range attempts {
		total += a.Total
		if a.Total > 1 {
			retried++
		}
	}

	t.Logf("%d transfers took %d attempts; %d needed a retry", goroutines, total, retried)

	for _, err := range failures {
		t.Errorf("a transfer failed even with retries: %v", err)
	}

	// The invariant. 8 transfers of 100 in alternating directions, so the total is unchanged and
	// each balance is back where it started.
	sum, err := Total(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if sum != 10_000 {
		t.Errorf("total is %d, want 10000", sum)
	}
}

// TestRetryDoesNotRetryDeterministicErrors, because retrying a unique violation is a slow way to fail.
func TestRetryDoesNotRetryDeterministicErrors(t *testing.T) {
	db, _ := accounts(t)
	ctx := context.Background()

	var id int64
	if err := db.QueryRow(ctx,
		"INSERT INTO accounts (owner, balance_cents) VALUES ('dup', 1) RETURNING id").
		Scan(&id); err != nil {
		t.Fatal(err)
	}

	a, err := Retry(ctx, db, pgx.TxOptions{}, DefaultRetry(), func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			"INSERT INTO accounts (id, owner, balance_cents) VALUES ($1, 'dup', 1)", id)
		return err
	})

	if err == nil {
		t.Fatal("expected a unique violation")
	}
	if Code(err) != UniqueViolation {
		t.Errorf("expected %s, got %s (%v)", UniqueViolation, Code(err), err)
	}
	if a.Total != 1 {
		t.Errorf("made %d attempts at a deterministic failure, want 1", a.Total)
	}
}

// TestRetryStopsWhenTheContextIsDone: a caller with a deadline should not wait out the backoff.
func TestRetryStopsWhenTheContextIsDone(t *testing.T) {
	db, _ := accounts(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	start := time.Now()

	// A fake serialization failure, so the test does not need real contention to exercise the
	// backoff. Postgres raises 40001 with RAISE.
	_, err := Retry(ctx, db, pgx.TxOptions{},
		RetryConfig{MaxAttempts: 20, BaseDelay: 50 * time.Millisecond, MaxDelay: time.Second},
		func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx,
				"DO $$ BEGIN RAISE EXCEPTION 'forced' USING ERRCODE = '40001'; END $$")
			return err
		})

	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a failure")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected the context deadline, got %v", err)
	}

	// 20 attempts at 50ms of backoff would be well over a second.
	if elapsed > 500*time.Millisecond {
		t.Errorf("took %v, so the backoff ignored the context", elapsed)
	}

	t.Logf("gave up after %v rather than the full backoff", elapsed.Round(time.Millisecond))
}

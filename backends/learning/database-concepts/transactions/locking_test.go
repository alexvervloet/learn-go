package transactions

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/alexvervloet/learn-go/backends/learning/database-concepts/dbtest"
)

// TestForUpdateBlocks measures how long the second transaction waits, which is the cost people do not
// account for when they reach for FOR UPDATE.
func TestForUpdateBlocks(t *testing.T) {
	db, ids := accounts(t, 1_000)
	ctx := context.Background()

	const hold = 150 * time.Millisecond

	locked := make(chan struct{})
	done := make(chan struct{})

	// The holder: locks the row and sits on it.
	go func() {
		defer close(done)

		err := WithTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
			var cents int64
			if err := tx.QueryRow(ctx,
				"SELECT balance_cents FROM accounts WHERE id = $1 FOR UPDATE", ids[0]).
				Scan(&cents); err != nil {
				return err
			}

			close(locked)
			time.Sleep(hold)

			return nil
		})
		if err != nil {
			t.Errorf("the holder failed: %v", err)
		}
	}()

	<-locked

	start := time.Now()

	err := WithTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var cents int64
		return tx.QueryRow(ctx,
			"SELECT balance_cents FROM accounts WHERE id = $1 FOR UPDATE", ids[0]).Scan(&cents)
	})

	waited := time.Since(start)
	<-done

	if err != nil {
		t.Fatalf("the waiter failed: %v", err)
	}

	t.Logf("the second FOR UPDATE waited %v for a lock held for %v",
		waited.Round(time.Millisecond), hold)

	// A generous lower bound, because the assertion worth making is "it blocked", not a
	// millisecond count.
	if waited < hold/2 {
		t.Errorf("waited only %v, so it did not block on the lock", waited)
	}
}

// TestNoWaitFailsImmediately is the alternative to waiting: fail now and tell the user.
//
// NOWAIT turns an unbounded wait into SQLSTATE 55P03. For a user-facing request that is usually what
// you want, because a request holding a connection for eight seconds waiting on a lock is worse for
// everyone than a "please try again".
func TestNoWaitFailsImmediately(t *testing.T) {
	db, ids := accounts(t, 1_000)
	ctx := context.Background()

	locked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)

		_ = WithTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
			var cents int64
			if err := tx.QueryRow(ctx,
				"SELECT balance_cents FROM accounts WHERE id = $1 FOR UPDATE", ids[0]).
				Scan(&cents); err != nil {
				return err
			}

			close(locked)
			<-release

			return nil
		})
	}()

	<-locked

	start := time.Now()

	err := WithTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var cents int64
		return tx.QueryRow(ctx,
			"SELECT balance_cents FROM accounts WHERE id = $1 FOR UPDATE NOWAIT", ids[0]).
			Scan(&cents)
	})

	elapsed := time.Since(start)

	close(release)
	<-done

	if err == nil {
		t.Fatal("expected NOWAIT to fail on a locked row")
	}

	// 55P03 is lock_not_available.
	if got := Code(err); got != "55P03" {
		t.Errorf("expected 55P03, got %s (%v)", got, err)
	}

	t.Logf("NOWAIT failed after %v with %s", elapsed.Round(time.Millisecond), Code(err))

	if elapsed > 100*time.Millisecond {
		t.Errorf("NOWAIT took %v, which is not immediate", elapsed)
	}
}

// TestSkipLockedIsAJobQueue is the single most useful locking clause in Postgres, and the reason a
// separate queue system is often unnecessary.
//
// FOR UPDATE SKIP LOCKED means "give me rows nobody else has claimed, and do not wait". N workers
// running the same query get disjoint sets of rows, with no coordination and no extra infrastructure.
// Every Postgres-backed job queue is built on this one clause.
func TestSkipLockedIsAJobQueue(t *testing.T) {
	db := dbtest.Pool(t)
	ctx := context.Background()

	dbtest.Truncate(t, "accounts")

	// The queue: 60 rows standing in for jobs.
	const jobs = 60

	if _, err := db.Exec(ctx, `
		INSERT INTO accounts (owner, balance_cents)
		SELECT 'job ' || g, 0 FROM generate_series(1, $1) g`, jobs); err != nil {
		t.Fatal(err)
	}

	const (
		workers   = 6
		batchSize = 5
	)

	var (
		mu     sync.Mutex
		claims = make(map[int64]int)
		wg     sync.WaitGroup
	)

	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for {
				var claimed []int64

				err := WithTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
					// The query every Postgres job queue runs. The
					// sub-select with SKIP LOCKED picks the rows, and the
					// UPDATE marks them so a second pass does not see them
					// again.
					rows, err := tx.Query(ctx, `
						UPDATE accounts SET balance_cents = 1
						 WHERE id IN (
						       SELECT id FROM accounts
						        WHERE balance_cents = 0
						        ORDER BY id
						        FOR UPDATE SKIP LOCKED
						        LIMIT $1
						 )
						 RETURNING id`, batchSize)
					if err != nil {
						return err
					}
					defer rows.Close()

					for rows.Next() {
						var id int64
						if err := rows.Scan(&id); err != nil {
							return err
						}
						claimed = append(claimed, id)
					}

					return rows.Err()
				})
				if err != nil {
					t.Errorf("worker %d: %v", w, err)
					return
				}

				if len(claimed) == 0 {
					return
				}

				mu.Lock()
				for _, id := range claimed {
					claims[id]++
				}
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if len(claims) != jobs {
		t.Errorf("claimed %d of %d jobs", len(claims), jobs)
	}

	doubled := 0
	for id, n := range claims {
		if n != 1 {
			doubled++
			t.Errorf("job %d was claimed %d times", id, n)
		}
	}

	if doubled == 0 {
		t.Logf("%d workers claimed all %d jobs with no overlap and no coordination",
			workers, jobs)
	}
}

// TestDeadlockAndItsFix is the one anomaly that is entirely the application's fault.
//
// Two transfers in opposite directions between the same pair lock the rows in opposite orders. Each
// holds what the other needs. Postgres notices after deadlock_timeout (1 second by default), picks a
// victim, and aborts it with 40P01.
//
// The fix is not a retry loop, although a retry loop hides it. The fix is to always acquire locks in
// the same order, and the cheapest order is by primary key.
func TestDeadlockAndItsFix(t *testing.T) {
	ctx := context.Background()

	t.Run("opposite order deadlocks", func(t *testing.T) {
		db, ids := accounts(t, 1_000, 1_000)

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

				from, to := ids[i], ids[1-i]

				results[i] = WithTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
					// Lock the first row.
					if _, err := tx.Exec(ctx,
						"UPDATE accounts SET balance_cents = balance_cents - 1 WHERE id = $1",
						from); err != nil {
						return err
					}

					// Give the other transaction time to take the lock this
					// one is about to want.
					time.Sleep(50 * time.Millisecond)

					_, err := tx.Exec(ctx,
						"UPDATE accounts SET balance_cents = balance_cents + 1 WHERE id = $1",
						to)
					return err
				})
			}()
		}

		close(start)
		wg.Wait()

		deadlocked := false
		for _, err := range results {
			if err != nil && Code(err) == Deadlock {
				deadlocked = true
				t.Logf("  Postgres aborted one transaction: %v", err)
			}
		}

		if !deadlocked {
			t.Errorf("expected a %s deadlock, got %v and %v", Deadlock, results[0], results[1])
		}
	})

	t.Run("locking in ID order does not", func(t *testing.T) {
		db, ids := accounts(t, 1_000, 1_000)

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

				from, to := ids[i], ids[1-i]

				results[i] = WithTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
					// The one added line: take both locks in a fixed order
					// before touching anything.
					if err := LockOrdered(ctx, tx, from, to); err != nil {
						return err
					}

					if _, err := tx.Exec(ctx,
						"UPDATE accounts SET balance_cents = balance_cents - 1 WHERE id = $1",
						from); err != nil {
						return err
					}

					time.Sleep(50 * time.Millisecond)

					_, err := tx.Exec(ctx,
						"UPDATE accounts SET balance_cents = balance_cents + 1 WHERE id = $1",
						to)
					return err
				})
			}()
		}

		close(start)
		wg.Wait()

		for i, err := range results {
			if err != nil {
				t.Errorf("transaction %d failed: %v", i, err)
			}
		}

		total, err := Total(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if total != 2_000 {
			t.Errorf("total is %d, want 2000", total)
		}

		t.Log("  both committed; the sort is the whole fix")
	})
}

// TestAdvisoryLockIsNotTiedToARow, for the case there is no row to lock: a cron job that must not run
// twice, or a migration guard.
//
// pg_try_advisory_xact_lock takes a lock on an arbitrary integer and releases it when the transaction
// ends, which is the version to use. The plain pg_advisory_lock holds until the SESSION ends, and a
// session is a pooled connection, so a leaked advisory lock outlives the request and blocks a later
// unrelated one.
func TestAdvisoryLockIsNotTiedToARow(t *testing.T) {
	db := dbtest.Pool(t)
	ctx := context.Background()

	const key = 424242

	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)

		_ = WithTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
			var ok bool
			if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", key).
				Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return errors.New("the first attempt could not take the lock")
			}

			close(held)
			<-release

			return nil
		})
	}()

	<-held

	var got bool
	if err := WithTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", key).Scan(&got)
	}); err != nil {
		t.Fatal(err)
	}

	if got {
		t.Error("the second transaction took a lock the first was holding")
	}

	close(release)
	<-done

	// And once the holder's transaction ends, the lock is free. No unlock call, which is the
	// point of the _xact_ variant.
	if err := WithTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", key).Scan(&got)
	}); err != nil {
		t.Fatal(err)
	}

	if !got {
		t.Error("the lock was not released when the holding transaction ended")
	}
}

package transactions

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestAnErrorPoisonsTheWholeTransaction is the Postgres behaviour that surprises everyone coming from
// MySQL or SQLite, and the reason savepoints exist.
//
// After ANY failed statement, the transaction is in an aborted state. Every subsequent statement
// returns 25P02, "current transaction is aborted, commands ignored until end of transaction block".
// There is no partial recovery. A handler that catches a unique violation and carries on inside the
// same transaction does not work.
func TestAnErrorPoisonsTheWholeTransaction(t *testing.T) {
	db, ids := accounts(t, 1_000)
	ctx := context.Background()

	err := WithTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		// A deliberate failure: the CHECK constraint forbids a negative balance.
		_, err := tx.Exec(ctx,
			"UPDATE accounts SET balance_cents = -1 WHERE id = $1", ids[0])
		if err == nil {
			return errors.New("expected the CHECK constraint to fire")
		}

		t.Logf("the first statement failed with %s, which is expected", Code(err))

		// Now carry on as if the error had been handled. This is the mistake.
		var cents int64
		err = tx.QueryRow(ctx, "SELECT balance_cents FROM accounts WHERE id = $1", ids[0]).
			Scan(&cents)

		if err == nil {
			return errors.New("the transaction was not poisoned, which contradicts " +
				"everything this test is about")
		}

		// 25P02 is in_failed_sql_transaction.
		if got := Code(err); got != "25P02" {
			return errors.New("expected 25P02, got " + got + ": " + err.Error())
		}

		t.Logf("every later statement returns %s until the transaction ends", Code(err))

		return nil
	})

	// And the sting in the tail, which I did not expect. fn returned nil, so WithTx tried to
	// COMMIT. Postgres accepts COMMIT on an aborted transaction and performs a ROLLBACK instead,
	// silently, at the protocol level. pgx notices the mismatch and returns ErrTxCommitRollback
	// rather than reporting success, which most drivers do not.
	//
	// So a handler that swallows one statement's error and commits at the end gets an error from
	// the commit. That is the best outcome available: the alternative is a function that returns
	// nil having written nothing.
	if !errors.Is(err, pgx.ErrTxCommitRollback) {
		t.Fatalf("expected pgx.ErrTxCommitRollback from committing an aborted "+
			"transaction, got %v", err)
	}

	t.Logf("committing the aborted transaction returned: %v", err)
}

// TestSavepointRecoversFromOneFailedStatement is the fix.
//
// I expected WithTx to work unchanged at both levels, since a pool and a transaction can both begin
// something. They cannot: pgx.Tx has Begin, not BeginTx, because a savepoint has no isolation level of
// its own. So WithSavepoint is a separate function with the same shape, and the difference in the
// signature is telling you something true about transactions.
func TestSavepointRecoversFromOneFailedStatement(t *testing.T) {
	db, ids := accounts(t, 1_000)
	ctx := context.Background()

	err := WithTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		inner := WithSavepoint(ctx, tx, func(sp pgx.Tx) error {
			_, err := sp.Exec(ctx,
				"UPDATE accounts SET balance_cents = -1 WHERE id = $1", ids[0])
			return err
		})

		if inner == nil {
			return errors.New("expected the CHECK constraint to fire")
		}

		t.Logf("the savepoint absorbed a %s", Code(inner))

		// The outer transaction is healthy, because the savepoint rolled back rather than
		// the whole transaction.
		var cents int64
		if err := tx.QueryRow(ctx, "SELECT balance_cents FROM accounts WHERE id = $1", ids[0]).
			Scan(&cents); err != nil {
			return err
		}

		if cents != 1_000 {
			return errors.New("the balance changed despite the rollback")
		}

		// And a real change after the failure commits.
		_, err := tx.Exec(ctx,
			"UPDATE accounts SET balance_cents = 500 WHERE id = $1", ids[0])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := Balance(ctx, db, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if got != 500 {
		t.Errorf("balance is %d, want 500", got)
	}
}

// TestSavepointNestsFurther, because the pattern composes and it is worth knowing it does.
func TestSavepointNestsFurther(t *testing.T) {
	db, ids := accounts(t, 1_000)
	ctx := context.Background()

	err := WithTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			"UPDATE accounts SET balance_cents = 900 WHERE id = $1", ids[0]); err != nil {
			return err
		}

		outer := WithSavepoint(ctx, tx, func(sp1 pgx.Tx) error {
			if _, err := sp1.Exec(ctx,
				"UPDATE accounts SET balance_cents = 800 WHERE id = $1", ids[0]); err != nil {
				return err
			}

			// A third level, which fails and is absorbed by the second.
			inner := WithSavepoint(ctx, sp1, func(sp2 pgx.Tx) error {
				if _, err := sp2.Exec(ctx,
					"UPDATE accounts SET balance_cents = 700 WHERE id = $1",
					ids[0]); err != nil {
					return err
				}
				return errors.New("changed my mind")
			})

			if inner == nil {
				return errors.New("expected the innermost level to fail")
			}

			// 800 survived, 700 did not.
			var cents int64
			if err := sp1.QueryRow(ctx,
				"SELECT balance_cents FROM accounts WHERE id = $1", ids[0]).
				Scan(&cents); err != nil {
				return err
			}
			if cents != 800 {
				return errors.New("expected 800 after the inner rollback")
			}

			return nil
		})
		if outer != nil {
			return outer
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := Balance(ctx, db, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if got != 800 {
		t.Errorf("balance is %d, want 800", got)
	}

	t.Log("three levels, one failed, the two above it committed")
}

// TestSavepointsAreNotFree measures the subtransaction cost, because "just wrap every row in a
// savepoint" is advice people give.
func TestSavepointsAreNotFree(t *testing.T) {
	db, _ := accounts(t)
	ctx := context.Background()

	if _, err := db.Exec(ctx, `
		INSERT INTO accounts (owner, balance_cents)
		SELECT 'row ' || g, g FROM generate_series(1, 2000) g`); err != nil {
		t.Fatal(err)
	}

	// One savepoint for the whole batch.
	flat, err := timeIt(func() error {
		return WithTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
			for i := 1; i <= 2000; i++ {
				if _, err := tx.Exec(ctx,
					"UPDATE accounts SET balance_cents = balance_cents + 1 WHERE owner = $1",
					"row "+itoa(i)); err != nil {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	// One savepoint per row, which is what "handle each row's errors" turns into.
	nested, err := timeIt(func() error {
		return WithTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
			for i := 1; i <= 2000; i++ {
				if err := WithSavepoint(ctx, tx, func(sp pgx.Tx) error {
					_, err := sp.Exec(ctx,
						"UPDATE accounts SET balance_cents = balance_cents + 1 WHERE owner = $1",
						"row "+itoa(i))
					return err
				}); err != nil {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("2000 updates, one transaction:          %v", flat)
	t.Logf("2000 updates, a savepoint per row:      %v (%.2fx)",
		nested, float64(nested)/float64(flat))

	if nested <= flat {
		t.Logf("the savepoints did not cost measurably here, which happens on a fast local " +
			"socket where the extra round trips dominate")
	}
}

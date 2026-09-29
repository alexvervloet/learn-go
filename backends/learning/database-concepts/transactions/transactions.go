// Package transactions covers what ACID actually guarantees, which is less than the acronym suggests,
// and the retry loop that every serializable transaction needs.
//
// # This is the one package that cannot use rollback isolation
//
// Every other package here gets a transaction from dbtest.Tx and lets it roll back. That technique
// cannot test transactions: the thing under test IS the transaction. So these tests take real
// connections from the pool, commit for real, and truncate afterwards. That is also why they cannot
// run in parallel with each other.
//
// # The four isolation levels, and what Postgres actually does
//
//	READ UNCOMMITTED  accepted and treated as READ COMMITTED. Postgres cannot do dirty reads at
//	                  all, because MVCC has no way to show an uncommitted row to another
//	                  transaction. So one of the four anomalies is simply impossible here.
//	READ COMMITTED    the default. Each STATEMENT sees a fresh snapshot, so two reads in one
//	                  transaction can disagree.
//	REPEATABLE READ   one snapshot for the whole TRANSACTION. Postgres's implementation also
//	                  prevents phantom reads, which the standard permits at this level, so it is
//	                  really snapshot isolation. It still allows write skew.
//	SERIALIZABLE      adds predicate locking (SSI) to catch write skew, at the cost of aborting
//	                  transactions with SQLSTATE 40001 that would have committed.
//
// The practical summary: Postgres is stricter than the standard requires at every level, and the only
// anomaly left at REPEATABLE READ is write skew. Write skew is also the one that causes real money
// bugs, which is why SERIALIZABLE exists.
//
// # The rule that matters more than the levels
//
// SERIALIZABLE without a retry loop is worse than READ COMMITTED, because it turns a silent wrong
// answer into a 500. Every serializable transaction must be retryable, which means it cannot have
// side effects before it commits: no email, no charge, no file write. Collect the effects, commit,
// then perform them.
package transactions

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Beginner is what a pool satisfies: something that can start a transaction with options.
//
// pgx.Tx does NOT satisfy it, and that is not an oversight in pgx. A transaction can start a nested
// one, but only through Begin with no options, because the options that matter (the isolation level,
// read-only, deferrable) are properties of the whole transaction and cannot change halfway through.
// So the nested case gets its own helper, WithSavepoint, rather than reusing this one.
type Beginner interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// SQLSTATE codes worth naming rather than writing as string literals at the call site.
const (
	// SerializationFailure is 40001. It means "this transaction would have produced a result no
	// serial ordering could produce, so it was aborted". It is not an error in the code; it is
	// the database asking for the transaction again.
	SerializationFailure = "40001"

	// Deadlock is 40P01. Two transactions each hold what the other wants. Postgres picks a
	// victim after deadlock_timeout (1s by default) and aborts it.
	Deadlock = "40P01"

	// UniqueViolation is 23505, here because the retry loop must NOT retry it: a duplicate key
	// is deterministic and retrying just fails again.
	UniqueViolation = "23505"
)

// IsRetryable reports whether an error is one the same transaction might survive on a second attempt.
//
// Only two codes qualify. Everything else, including a unique violation and a check constraint, will
// fail identically however many times you try, and retrying it turns a fast failure into a slow one.
func IsRetryable(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	return pgErr.Code == SerializationFailure || pgErr.Code == Deadlock
}

// Code returns an error's SQLSTATE, or "" if it is not a Postgres error.
//
// errors.As rather than a type assertion, because the error has been wrapped by every layer between
// pgx and here. A type assertion works until someone adds an fmt.Errorf with %w, and then it silently
// stops matching and the retry loop stops retrying.
func Code(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// WithTx runs fn in a transaction, committing if it returns nil and rolling back otherwise.
//
// The shape is worth copying exactly, because three things here are easy to get wrong.
//
// The rollback is deferred and its error is discarded, but ONLY the ErrTxClosed case: after a
// successful Commit the deferred Rollback returns ErrTxClosed, and treating that as a failure breaks
// every successful call. Any other rollback error is real and is joined onto the returned error,
// because a rollback that fails means the connection is in an unknown state.
//
// A panic in fn must not leave the transaction open. The deferred rollback handles that, and it is the
// reason for defer rather than an explicit rollback on the error path.
//
// fn gets the pgx.Tx and must use it. A closure that reaches past it to the pool runs OUTSIDE the
// transaction, on a different connection, and that is the single most common bug in code shaped like
// this. Nothing in the type system prevents it.
func WithTx(ctx context.Context, db Beginner, opts pgx.TxOptions, fn func(pgx.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("beginning: %w", err)
	}

	defer func() {
		rollbackErr := tx.Rollback(ctx)

		if rollbackErr == nil || errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return
		}

		err = errors.Join(err, fmt.Errorf("rolling back: %w", rollbackErr))
	}()

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing: %w", err)
	}

	return nil
}

// WithSavepoint runs fn inside a savepoint on an existing transaction, rolling back to the savepoint
// if fn fails and leaving the outer transaction usable.
//
// This is the answer to the Postgres behaviour that catches everyone: after ANY failed statement the
// whole transaction is aborted and every later statement returns 25P02. There is no partial recovery
// without a savepoint.
//
// pgx spells a savepoint as tx.Begin, which issues SAVEPOINT rather than BEGIN, and the returned Tx's
// Rollback issues ROLLBACK TO SAVEPOINT. So the body of this function is the same shape as WithTx and
// only the Begin call differs.
//
// The cost is real, and it has two parts. Each savepoint is two extra statements (SAVEPOINT, then
// RELEASE or ROLLBACK TO), so a savepoint per row triples the round trips. And each is a subtransaction:
// Postgres caches 64 per backend, and past that visibility checks on rows a subtransaction wrote go to
// pg_subtrans, which can mean disk. A loop taking a savepoint per row over tens of thousands of rows is a
// documented way to make a database crawl. The README's 1.41x mixes both costs.
func WithSavepoint(ctx context.Context, tx pgx.Tx, fn func(pgx.Tx) error) (err error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("creating a savepoint: %w", err)
	}

	defer func() {
		rollbackErr := sp.Rollback(ctx)

		if rollbackErr == nil || errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return
		}

		err = errors.Join(err, fmt.Errorf("rolling back to the savepoint: %w", rollbackErr))
	}()

	if err := fn(sp); err != nil {
		return err
	}

	// RELEASE SAVEPOINT, not COMMIT. pgx picks the right statement; the name is Commit because
	// it is the same interface.
	if err := sp.Commit(ctx); err != nil {
		return fmt.Errorf("releasing the savepoint: %w", err)
	}

	return nil
}

// RetryConfig controls the backoff.
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration

	// Rand makes the jitter testable. A nil Rand uses the global source.
	Rand *rand.Rand
}

// DefaultRetry is three attempts with a short exponential backoff.
//
// Three rather than ten: a serialization failure means real contention, and a tenth attempt on a
// contended row is a request holding a connection for a second while making the contention worse.
// Failing fast and letting the client retry sheds load; retrying forever inside the handler does the
// opposite.
func DefaultRetry() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   5 * time.Millisecond,
		MaxDelay:    200 * time.Millisecond,
	}
}

// Attempts is how many tries a Retry call made, returned so a test can assert a retry happened rather
// than guessing from a timing.
type Attempts struct {
	Total   int
	Retried []string
}

// Retry runs fn in a transaction and retries it on a serialization failure or a deadlock.
//
// This is the function that makes SERIALIZABLE usable. Without it, any concurrent workload on a
// serializable transaction returns 40001 to the user, and the user sees an error for something that
// simply needed doing again.
//
// The three rules the signature is trying to enforce:
//
// fn may run several times, so it must be idempotent in its effects OUTSIDE the database. Sending an
// email inside fn sends it once per attempt.
//
// The jitter is not decoration. Two transactions that deadlock, back off by the same amount and retry
// together deadlock again. The randomness is what breaks the lockstep.
//
// The context bounds the whole thing, retries included. A caller with a 100ms deadline should not wait
// 600ms because the backoff did not check.
func Retry(ctx context.Context, db Beginner, opts pgx.TxOptions, cfg RetryConfig, fn func(pgx.Tx) error) (Attempts, error) {
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 1
	}

	var attempts Attempts

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		attempts.Total = attempt

		err := WithTx(ctx, db, opts, fn)
		if err == nil {
			return attempts, nil
		}

		if !IsRetryable(err) {
			return attempts, err
		}

		attempts.Retried = append(attempts.Retried, Code(err))

		if attempt == cfg.MaxAttempts {
			return attempts, fmt.Errorf("gave up after %d attempts: %w", attempt, err)
		}

		delay := backoff(cfg, attempt)

		// A timer rather than time.Sleep, so a cancelled context stops the wait instead of
		// sleeping through it and then failing.
		select {
		case <-ctx.Done():
			return attempts, fmt.Errorf("retrying: %w", ctx.Err())
		case <-time.After(delay):
		}
	}

	// Unreachable: the loop returns on every path. Written out because a bare panic here would
	// be worse and the compiler wants a return.
	return attempts, errors.New("transactions: retry loop fell through")
}

// backoff is exponential with full jitter.
//
// Full jitter (a uniform draw from [0, delay]) rather than the delay plus a small random amount,
// because the point is to spread the retries out, and adding 10% of noise to a shared delay leaves
// them still bunched.
func backoff(cfg RetryConfig, attempt int) time.Duration {
	base := cfg.BaseDelay
	if base <= 0 {
		base = time.Millisecond
	}

	delay := base << (attempt - 1)

	if cfg.MaxDelay > 0 && delay > cfg.MaxDelay {
		delay = cfg.MaxDelay
	}

	if cfg.Rand != nil {
		return time.Duration(cfg.Rand.Int64N(int64(delay) + 1))
	}

	return time.Duration(rand.Int64N(int64(delay) + 1))
}

// Transfer moves money between two accounts inside one transaction.
//
// The textbook example, and the reason it is the textbook example is that every part of it is a trap.
//
// The balance check and the update have to happen in the same statement or under a lock, or two
// concurrent transfers both see a sufficient balance and both proceed. This version uses a single
// UPDATE with the check in its WHERE clause, which is correct at READ COMMITTED because the UPDATE
// takes a row lock and re-evaluates the WHERE against the new version.
//
// The order of the two updates matters for deadlocks: two transfers in opposite directions between
// the same pair lock the rows in opposite orders and deadlock. Ordering the updates by account ID
// removes that, and LockOrdered below does it.
func Transfer(ctx context.Context, tx pgx.Tx, from, to int64, cents int64) error {
	if cents <= 0 {
		return fmt.Errorf("transfer amount must be positive, got %d", cents)
	}

	// The check is IN the UPDATE. A SELECT followed by an UPDATE is the lost-update bug, and it
	// looks more careful.
	tag, err := tx.Exec(ctx,
		"UPDATE accounts SET balance_cents = balance_cents - $1 WHERE id = $2 AND balance_cents >= $1",
		cents, from)
	if err != nil {
		return fmt.Errorf("debiting %d: %w", from, err)
	}

	// RowsAffected of 0 means either "no such account" or "insufficient funds", and they need
	// different errors, so it takes one more query to tell them apart. Returning a vague error
	// here is how "insufficient funds" becomes a 500.
	if tag.RowsAffected() == 0 {
		var balance int64

		err := tx.QueryRow(ctx, "SELECT balance_cents FROM accounts WHERE id = $1", from).
			Scan(&balance)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: account %d", ErrNoAccount, from)
		}
		if err != nil {
			return fmt.Errorf("checking the balance of %d: %w", from, err)
		}

		return fmt.Errorf("%w: account %d has %d, needs %d",
			ErrInsufficientFunds, from, balance, cents)
	}

	tag, err = tx.Exec(ctx,
		"UPDATE accounts SET balance_cents = balance_cents + $1 WHERE id = $2", cents, to)
	if err != nil {
		return fmt.Errorf("crediting %d: %w", to, err)
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: account %d", ErrNoAccount, to)
	}

	return nil
}

// Sentinel errors, so a caller can distinguish "the user does not have the money" from "the database
// is broken" with errors.Is rather than by matching a string.
var (
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrNoAccount         = errors.New("no such account")
)

// TransferLostUpdate is the WRONG version, kept because seeing it fail is the lesson.
//
// SELECT the balance, check it in Go, then UPDATE to an absolute value. At READ COMMITTED two
// concurrent calls both read the same balance, both pass the check, and the second UPDATE overwrites
// the first. Money appears out of nothing.
//
// Nothing about this code looks wrong. It is the shape most people write first.
func TransferLostUpdate(ctx context.Context, tx pgx.Tx, from, to int64, cents int64) error {
	var balance int64

	if err := tx.QueryRow(ctx, "SELECT balance_cents FROM accounts WHERE id = $1", from).
		Scan(&balance); err != nil {
		return fmt.Errorf("reading the balance of %d: %w", from, err)
	}

	if balance < cents {
		return fmt.Errorf("%w: account %d has %d", ErrInsufficientFunds, from, balance)
	}

	// The gap between the read above and the write below is where the other transaction fits.
	if _, err := tx.Exec(ctx, "UPDATE accounts SET balance_cents = $1 WHERE id = $2",
		balance-cents, from); err != nil {
		return fmt.Errorf("debiting %d: %w", from, err)
	}

	if _, err := tx.Exec(ctx,
		"UPDATE accounts SET balance_cents = balance_cents + $1 WHERE id = $2",
		cents, to); err != nil {
		return fmt.Errorf("crediting %d: %w", to, err)
	}

	return nil
}

// TransferSelectForUpdate is the third version: keep the read-then-write shape and take the lock
// explicitly.
//
// This is correct at READ COMMITTED, and it is what to reach for when the calculation between the read
// and the write is too complicated to express in SQL. FOR UPDATE makes the second transaction wait at
// the SELECT rather than proceeding on stale data.
//
// It also serialises every transfer touching the same account, which is the cost.
func TransferSelectForUpdate(ctx context.Context, tx pgx.Tx, from, to int64, cents int64) error {
	var balance int64

	if err := tx.QueryRow(ctx,
		"SELECT balance_cents FROM accounts WHERE id = $1 FOR UPDATE", from).
		Scan(&balance); err != nil {
		return fmt.Errorf("locking %d: %w", from, err)
	}

	if balance < cents {
		return fmt.Errorf("%w: account %d has %d", ErrInsufficientFunds, from, balance)
	}

	if _, err := tx.Exec(ctx, "UPDATE accounts SET balance_cents = $1 WHERE id = $2",
		balance-cents, from); err != nil {
		return fmt.Errorf("debiting %d: %w", from, err)
	}

	if _, err := tx.Exec(ctx,
		"UPDATE accounts SET balance_cents = balance_cents + $1 WHERE id = $2",
		cents, to); err != nil {
		return fmt.Errorf("crediting %d: %w", to, err)
	}

	return nil
}

// LockOrdered locks a set of accounts in ID order, which is the cheapest deadlock prevention there is.
//
// A deadlock needs two transactions to acquire the same locks in different orders. Sorting the IDs
// before locking makes that impossible, and it costs a sort.
//
// `ORDER BY id FOR UPDATE` in one statement rather than a loop, because a loop still acquires them one
// at a time and the ordering has to be right in Go as well. One statement lets Postgres do it.
func LockOrdered(ctx context.Context, tx pgx.Tx, ids ...int64) error {
	rows, err := tx.Query(ctx,
		"SELECT id FROM accounts WHERE id = ANY($1) ORDER BY id FOR UPDATE", ids)
	if err != nil {
		return fmt.Errorf("locking %v: %w", ids, err)
	}
	defer rows.Close()

	locked := 0
	for rows.Next() {
		locked++
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("locking %v: %w", ids, err)
	}

	if locked != len(ids) {
		return fmt.Errorf("%w: locked %d of %d", ErrNoAccount, locked, len(ids))
	}

	return nil
}

// Balance reads one account's balance.
func Balance(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, id int64) (int64, error) {
	var cents int64

	if err := q.QueryRow(ctx, "SELECT balance_cents FROM accounts WHERE id = $1", id).
		Scan(&cents); err != nil {
		return 0, fmt.Errorf("reading account %d: %w", id, err)
	}

	return cents, nil
}

// Total is the invariant every test here checks: money is neither created nor destroyed.
func Total(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}) (int64, error) {
	var cents int64

	if err := q.QueryRow(ctx, "SELECT coalesce(sum(balance_cents), 0) FROM accounts").
		Scan(&cents); err != nil {
		return 0, fmt.Errorf("summing balances: %w", err)
	}

	return cents, nil
}

// Tests for package fakes, comparing three ways to stand in for a dependency:
//
//	a hand-written fake        ~15 lines, compiles, no dependency
//	a function-field stub      ~5 lines, per-test behaviour, no dependency
//	testify/mock               call assertions and argument matchers, plus a dependency
//
// All three appear here on the same service so the trade is visible rather than argued.
package fakes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// 1. The function-field stub
// ==========================
//
// The form to reach for first. Each method is a struct field holding a function, so a test sets
// only the behaviour it cares about and the rest panic if called, which is a useful default:
// an unexpected call is a bug and a nil func makes it loud.

type stubPayments struct {
	charge func(ctx context.Context, amount int, key string) (Charge, error)
}

func (s stubPayments) Charge(ctx context.Context, amount int, key string) (Charge, error) {
	return s.charge(ctx, amount, key)
}

type stubNotifier struct {
	notify func(ctx context.Context, email, subject, body string) error
}

func (s stubNotifier) Notify(ctx context.Context, email, subject, body string) error {
	if s.notify == nil {
		return nil // a sensible zero for the common case
	}
	return s.notify(ctx, email, subject, body)
}

type stubOrders struct {
	get      func(ctx context.Context, id string) (Order, error)
	markPaid func(ctx context.Context, id, chargeID string) error
}

func (s stubOrders) Get(ctx context.Context, id string) (Order, error) {
	return s.get(ctx, id)
}

func (s stubOrders) MarkPaid(ctx context.Context, id, chargeID string) error {
	if s.markPaid == nil {
		return nil
	}
	return s.markPaid(ctx, id, chargeID)
}

// fixedClock is the whole reason Clock is an interface.
type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

func TestCheckoutWithStubs(t *testing.T) {
	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	var chargedAmount int
	var chargedKey string

	checkout := NewCheckout(
		stubPayments{charge: func(_ context.Context, amount int, key string) (Charge, error) {
			chargedAmount, chargedKey = amount, key
			return Charge{ID: "ch_1", Amount: amount}, nil
		}},
		stubNotifier{},
		stubOrders{get: func(_ context.Context, id string) (Order, error) {
			return Order{ID: id, Email: "a@example.com", Amount: 2500}, nil
		}},
		fixedClock{at: at},
	)

	got, err := checkout.Run(context.Background(), "o_1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got.Charge.ID != "ch_1" {
		t.Errorf("charge = %+v", got.Charge)
	}
	if !got.Notified {
		t.Error("Notified = false, want true")
	}
	// The clock is fixed, so the assertion is exact rather than a tolerance.
	if !got.At.Equal(at) {
		t.Errorf("At = %v, want %v", got.At, at)
	}

	if chargedAmount != 2500 {
		t.Errorf("charged %d, want 2500", chargedAmount)
	}
	// The idempotency key must come from the ORDER, not the clock or a random source, or a
	// retry charges twice.
	if chargedKey != "order-o_1" {
		t.Errorf("idempotency key = %q, want it derived from the order", chargedKey)
	}
}

// TestNotificationFailureDoesNotFailTheCheckout pins down the business decision that looks like
// an oversight: the money has moved, so losing the order would be worse than losing the email.
func TestNotificationFailureDoesNotFailTheCheckout(t *testing.T) {
	checkout := NewCheckout(
		stubPayments{charge: func(context.Context, int, string) (Charge, error) {
			return Charge{ID: "ch_1", Amount: 100}, nil
		}},
		stubNotifier{notify: func(context.Context, string, string, string) error {
			return errors.New("smtp down")
		}},
		stubOrders{get: func(_ context.Context, id string) (Order, error) {
			return Order{ID: id, Amount: 100}, nil
		}},
		fixedClock{},
	)

	got, err := checkout.Run(context.Background(), "o_1")

	if err != nil {
		t.Fatalf("Run returned %v; a failed notification must not fail the checkout", err)
	}
	if got.Notified {
		t.Error("Notified = true, but the notifier failed")
	}
}

// TestFailurePathsAbort is the mirror: these two DO abort.
func TestFailurePathsAbort(t *testing.T) {
	tests := []struct {
		name    string
		orders  stubOrders
		pay     stubPayments
		wantErr error
	}{
		{
			name: "a missing order",
			orders: stubOrders{get: func(context.Context, string) (Order, error) {
				return Order{}, ErrOrderNotFound
			}},
			pay: stubPayments{charge: func(context.Context, int, string) (Charge, error) {
				t.Error("Charge was called for an order that does not exist")
				return Charge{}, nil
			}},
			wantErr: ErrOrderNotFound,
		},
		{
			name: "a declined card",
			orders: stubOrders{get: func(_ context.Context, id string) (Order, error) {
				return Order{ID: id, Amount: 100}, nil
			}},
			pay: stubPayments{charge: func(context.Context, int, string) (Charge, error) {
				return Charge{}, ErrCardDeclined
			}},
			wantErr: ErrCardDeclined,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// markPaid is nil, so a call would panic. That is the point: the stub's zero
			// value asserts "this must not be called" for free.
			marked := stubOrders{get: tt.orders.get, markPaid: func(context.Context, string, string) error {
				t.Error("MarkPaid was called on a failed checkout")
				return nil
			}}

			checkout := NewCheckout(tt.pay, stubNotifier{}, marked, fixedClock{})

			_, err := checkout.Run(context.Background(), "o_1")

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// 2. The hand-written fake
// ========================
//
// When a test needs the collaborator to REMEMBER things, a struct with state beats a closure.
// This one is a working in-memory Orders, so a test can check the state afterwards rather than
// recording calls.

type fakeOrders struct {
	mu     sync.Mutex
	orders map[string]Order
	paid   map[string]string // order ID -> charge ID
}

func newFakeOrders(orders ...Order) *fakeOrders {
	f := &fakeOrders{orders: make(map[string]Order), paid: make(map[string]string)}
	for _, o := range orders {
		f.orders[o.ID] = o
	}
	return f
}

func (f *fakeOrders) Get(_ context.Context, id string) (Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	order, ok := f.orders[id]
	if !ok {
		return Order{}, fmt.Errorf("%w: %s", ErrOrderNotFound, id)
	}
	return order, nil
}

func (f *fakeOrders) MarkPaid(_ context.Context, id, chargeID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.orders[id]; !ok {
		return fmt.Errorf("%w: %s", ErrOrderNotFound, id)
	}

	f.paid[id] = chargeID
	return nil
}

// isPaid is the query the test wants, and having it on the fake keeps the assertion readable.
func (f *fakeOrders) isPaid(id string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	charge, ok := f.paid[id]
	return charge, ok
}

// countingPayments is a fake that records how many times it was called, which is what a mock
// library's AssertNumberOfCalls does and which is four lines here.
type countingPayments struct {
	mu    sync.Mutex
	calls []string // idempotency keys, in order
}

func (p *countingPayments) Charge(_ context.Context, amount int, key string) (Charge, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls = append(p.calls, key)

	return Charge{ID: fmt.Sprintf("ch_%d", len(p.calls)), Amount: amount}, nil
}

func (p *countingPayments) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func TestCheckoutWithAStatefulFake(t *testing.T) {
	orders := newFakeOrders(Order{ID: "o_1", Email: "a@example.com", Amount: 2500})
	payments := &countingPayments{}

	checkout := NewCheckout(payments, stubNotifier{}, orders, RealClock{})

	got, err := checkout.Run(context.Background(), "o_1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The assertion is about STATE, not about calls, which is what makes a fake nicer than a
	// mock: the test says what the world looks like afterwards.
	chargeID, paid := orders.isPaid("o_1")
	if !paid {
		t.Fatal("the order was not marked paid")
	}
	if chargeID != got.Charge.ID {
		t.Errorf("marked paid with %q, charged %q", chargeID, got.Charge.ID)
	}
}

// TestIdempotencyKeyIsStableAcrossRetries is the test a stateful fake makes easy: run the same
// checkout twice and check the key did not change, because a key derived from a clock or a random
// source would charge twice.
func TestIdempotencyKeyIsStableAcrossRetries(t *testing.T) {
	orders := newFakeOrders(Order{ID: "o_1", Amount: 100})
	payments := &countingPayments{}

	checkout := NewCheckout(payments, stubNotifier{}, orders, RealClock{})

	for range 3 {
		if _, err := checkout.Run(context.Background(), "o_1"); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}

	if payments.count() != 3 {
		t.Fatalf("Charge called %d times, want 3", payments.count())
	}

	// Every call used the same key, so a real gateway would have charged once.
	for i, key := range payments.calls {
		if key != "order-o_1" {
			t.Errorf("call %d used key %q, want it stable", i, key)
		}
	}
}

// 3. testify/mock
// ===============
//
// What it adds: argument matchers, call-count assertions, and a failure when an expected call did
// not happen. What it costs: a dependency, a reflective call path, and failures that report a
// mock's expectations rather than the behaviour under test.
//
// Worth it when the ASSERTION IS ABOUT THE CALL rather than about the result: "we must not charge
// twice", "the audit log must record this". Not worth it for "the total is 300".

type mockNotifier struct {
	mock.Mock
}

func (m *mockNotifier) Notify(ctx context.Context, email, subject, body string) error {
	args := m.Called(ctx, email, subject, body)
	return args.Error(0)
}

func TestCheckoutWithTestifyMock(t *testing.T) {
	notifier := &mockNotifier{}

	// The expectation: called once, with this email, and any subject and body.
	notifier.On("Notify",
		mock.Anything,                 // the context
		"a@example.com",               // an exact match
		mock.AnythingOfType("string"), // a type match
		mock.Anything,
	).Return(nil).Once()

	checkout := NewCheckout(
		&countingPayments{},
		notifier,
		newFakeOrders(Order{ID: "o_1", Email: "a@example.com", Amount: 100}),
		RealClock{},
	)

	_, err := checkout.Run(context.Background(), "o_1")
	require.NoError(t, err)

	// This is what the library buys: a failure if Notify was NOT called, which a stub cannot
	// give you without a bool and an assertion.
	notifier.AssertExpectations(t)
}

// TestTestifyMockCatchesAMissingCall shows the failure a stub would miss, by running the
// assertion against a recording *testing.T rather than the real one.
func TestTestifyMockCatchesAMissingCall(t *testing.T) {
	notifier := &mockNotifier{}
	notifier.On("Notify", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	// Never call it.

	// mock.AssertExpectations takes a TestingT, so a fake one captures the failure instead of
	// failing this test.
	spy := &recordingT{}
	if notifier.AssertExpectations(spy) {
		t.Error("AssertExpectations passed despite the call never happening")
	}
	if !spy.failed {
		t.Error("the mock did not report a failure")
	}

	t.Logf("the mock reported: %s", strings.TrimSpace(strings.Join(spy.messages, " | ")))
}

// recordingT captures what a testify assertion reports, so a test can assert on the assertion.
type recordingT struct {
	failed   bool
	messages []string
}

func (r *recordingT) Logf(format string, args ...any) {
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

func (r *recordingT) Errorf(format string, args ...any) {
	r.failed = true
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

func (r *recordingT) FailNow() { r.failed = true }

// TestRequireVersusAssert is the testify distinction that matters most, and the one people get
// wrong.
//
//	assert.X   marks the test failed and CONTINUES, like t.Errorf
//	require.X  marks it failed and STOPS, like t.Fatalf
//
// Using assert for a nil check and then dereferencing panics, which reports a panic rather than
// the assertion that should have stopped the test.
func TestRequireVersusAssert(t *testing.T) {
	orders := newFakeOrders(Order{ID: "o_1", Amount: 100})

	order, err := orders.Get(context.Background(), "o_1")

	// require, because everything below is meaningless if this failed.
	require.NoError(t, err)
	require.Equal(t, "o_1", order.ID)

	// The stdlib equivalent is three lines and says the same thing. Which to use is a taste
	// question; mixing them in one file is not.
	if order.Amount != 100 {
		t.Errorf("Amount = %d, want 100", order.Amount)
	}
}

// 4. Fake time, properly
// ======================

// slowCheckout wraps Checkout with a retry, so there is something with real timing to test.
type slowCheckout struct {
	inner    *Checkout
	attempts int
	backoff  time.Duration
}

func (s *slowCheckout) Run(ctx context.Context, orderID string) (Result, error) {
	var lastErr error

	for attempt := range s.attempts {
		if attempt > 0 {
			select {
			case <-time.After(s.backoff << (attempt - 1)): // exponential
			case <-ctx.Done():
				return Result{}, ctx.Err()
			}
		}

		result, err := s.inner.Run(ctx, orderID)
		if err == nil {
			return result, nil
		}

		// Only a rate limit is worth retrying; a declined card will stay declined.
		if !errors.Is(err, ErrRateLimited) {
			return Result{}, err
		}
		lastErr = err
	}

	return Result{}, fmt.Errorf("giving up after %d attempts: %w", s.attempts, lastErr)
}

// TestRetryWithSynctest is the Go 1.24+ answer to testing anything with a timer in it, and it is
// the closest analogue to pytest-asyncio's event-loop control.
//
// Inside a synctest bubble the time package uses a FAKE CLOCK that advances only when every
// goroutine in the bubble is blocked. So a test of exponential backoff with 1s, 2s and 4s waits
// runs instantly and deterministically.
//
// The usual alternative is to make the backoff configurable, set it to a millisecond in tests,
// and accept that the test is timing-dependent and occasionally flaky on a loaded CI runner. Seven seconds of sleep becomes microseconds here, with no
// millisecond-tuning and no flake.
func TestRetryWithSynctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attempts int

		payments := stubPayments{charge: func(context.Context, int, string) (Charge, error) {
			attempts++
			if attempts < 3 {
				return Charge{}, ErrRateLimited
			}
			return Charge{ID: "ch_1", Amount: 100}, nil
		}}

		retrying := &slowCheckout{
			inner: NewCheckout(payments, stubNotifier{},
				newFakeOrders(Order{ID: "o_1", Amount: 100}), RealClock{}),
			attempts: 5,
			backoff:  time.Second,
		}

		start := time.Now() // always midnight UTC 2000-01-01 in a bubble

		got, err := retrying.Run(context.Background(), "o_1")
		if err != nil {
			t.Fatalf("Run: %v", err)
		}

		if attempts != 3 {
			t.Errorf("attempts = %d, want 3", attempts)
		}
		if got.Charge.ID != "ch_1" {
			t.Errorf("charge = %+v", got.Charge)
		}

		// Two backoffs: 1s then 2s. Asserted EXACTLY, which a real clock could never do.
		if elapsed := time.Since(start); elapsed != 3*time.Second {
			t.Errorf("elapsed = %v, want exactly 3s", elapsed)
		}
	})
}

// TestRetryGivesUpWithSynctest checks the exhaustion path, which on a real clock would take
// 1+2+4+8 = 15 seconds.
func TestRetryGivesUpWithSynctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		payments := stubPayments{charge: func(context.Context, int, string) (Charge, error) {
			return Charge{}, ErrRateLimited
		}}

		retrying := &slowCheckout{
			inner: NewCheckout(payments, stubNotifier{},
				newFakeOrders(Order{ID: "o_1", Amount: 100}), RealClock{}),
			attempts: 5,
			backoff:  time.Second,
		}

		start := time.Now()

		_, err := retrying.Run(context.Background(), "o_1")

		if !errors.Is(err, ErrRateLimited) {
			t.Errorf("err = %v, want it to wrap ErrRateLimited", err)
		}
		if !strings.Contains(err.Error(), "5 attempts") {
			t.Errorf("err = %v, want it to name the attempt count", err)
		}

		// 1 + 2 + 4 + 8 seconds of backoff between five attempts.
		if elapsed := time.Since(start); elapsed != 15*time.Second {
			t.Errorf("elapsed = %v, want exactly 15s", elapsed)
		}
	})
}

// TestContextCancellationDuringBackoff: the retry loop selects on ctx.Done(), and synctest makes
// the interleaving deterministic instead of a race between a timer and a cancel.
func TestContextCancellationDuringBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		payments := stubPayments{charge: func(context.Context, int, string) (Charge, error) {
			return Charge{}, ErrRateLimited
		}}

		retrying := &slowCheckout{
			inner: NewCheckout(payments, stubNotifier{},
				newFakeOrders(Order{ID: "o_1", Amount: 100}), RealClock{}),
			attempts: 10,
			backoff:  time.Second,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
		defer cancel()

		start := time.Now()

		_, err := retrying.Run(ctx, "o_1")

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want context.DeadlineExceeded", err)
		}

		// The deadline fires 2.5s in, during the 2s backoff before the third attempt.
		if elapsed := time.Since(start); elapsed != 2500*time.Millisecond {
			t.Errorf("elapsed = %v, want exactly 2.5s", elapsed)
		}
	})
}

// Package fakes is the code under test for Go's answer to unittest.mock.
//
// # The short version: define the interface at the consumer
//
// Python's mock.patch works by REPLACING an attribute on a module at runtime. It needs no
// cooperation from the code under test, which is its strength and its weakness: any function can
// be patched, and a patch that targets the wrong import path silently does nothing.
//
// Go has no equivalent and does not need one, because of one property of its interfaces:
//
//	an interface is satisfied implicitly, so the IMPLEMENTATION does not know it exists
//
// That means the consumer declares the narrow interface it wants, the real type satisfies it
// without being changed, and a fake satisfies it too. There is no patching because there is
// nothing to patch: the dependency arrives as an argument.
//
// # The rule that follows
//
//	Accept interfaces, return structs. And define the interface where it is USED, not where
//	it is implemented.
//
// A Notifier interface next to the email client is a Java habit. The interface belongs next to
// the checkout service, listing only the methods checkout calls, which is usually one or two. A
// fake for a two-method interface is fifteen lines; a fake for the email client's full surface is
// two hundred, and that difference is why people reach for a mock library.
package fakes

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Domain types
// ============

// Order is what gets checked out.
type Order struct {
	ID     string
	Email  string
	Amount int // cents
}

// Charge is the result of taking money.
type Charge struct {
	ID     string
	Amount int
}

// Errors the collaborators can return.
var (
	ErrCardDeclined  = errors.New("fakes: card declined")
	ErrRateLimited   = errors.New("fakes: rate limited")
	ErrOrderNotFound = errors.New("fakes: order not found")
)

// The interfaces the service needs, declared HERE
// ===============================================
//
// Each one lists only what Checkout calls. The real payment gateway has thirty methods; Checkout
// needs one, so the interface has one, and the fake for it is six lines.

// PaymentGateway takes money.
type PaymentGateway interface {
	Charge(ctx context.Context, amount int, idempotencyKey string) (Charge, error)
}

// Notifier tells the customer something happened.
type Notifier interface {
	Notify(ctx context.Context, email, subject, body string) error
}

// Orders reads and writes orders.
type Orders interface {
	Get(ctx context.Context, id string) (Order, error)
	MarkPaid(ctx context.Context, id, chargeID string) error
}

// Clock is the dependency people forget to abstract, and then write tests with time.Sleep in
// them.
//
// One method. Anything that reads the wall clock inside business logic makes that logic
// untestable without waiting, and a test that waits is a test that is slow and flaky at once.
type Clock interface {
	Now() time.Time
}

// RealClock is the production implementation, and it is the whole of it.
type RealClock struct{}

// Now returns the current time.
func (RealClock) Now() time.Time { return time.Now() }

// The service under test
// ======================

// Checkout charges for an order, marks it paid, and notifies the customer.
//
// Every dependency is an interface and arrives through the constructor. There is no global, no
// package-level client, and nothing to patch, which is what makes the tests in fakes_test.go
// plain.
type Checkout struct {
	payments PaymentGateway
	notifier Notifier
	orders   Orders
	clock    Clock
}

// NewCheckout wires the service up.
func NewCheckout(payments PaymentGateway, notifier Notifier, orders Orders, clock Clock) *Checkout {
	return &Checkout{payments: payments, notifier: notifier, orders: orders, clock: clock}
}

// Result is what a completed checkout reports.
type Result struct {
	Charge   Charge
	Notified bool
	At       time.Time
}

// Run performs the checkout.
//
// The interesting decisions for testing are the two failure policies:
//
//	a declined card ABORTS, because there is no point marking an unpaid order paid
//	a failed notification does NOT abort, because the money has already moved and losing
//	  the order would be worse than losing the email
//
// That second one is the kind of thing a test has to pin down, because it is a business decision
// that looks like an oversight.
func (c *Checkout) Run(ctx context.Context, orderID string) (Result, error) {
	order, err := c.orders.Get(ctx, orderID)
	if err != nil {
		return Result{}, fmt.Errorf("loading order: %w", err)
	}

	// The idempotency key is derived from the order, so a retry charges once. Deriving it
	// from the clock or a random source would defeat that, which is a thing a test can catch.
	charge, err := c.payments.Charge(ctx, order.Amount, "order-"+order.ID)
	if err != nil {
		return Result{}, fmt.Errorf("charging: %w", err)
	}

	if err := c.orders.MarkPaid(ctx, order.ID, charge.ID); err != nil {
		// The money moved and the record did not. Returning the error is right, and the
		// caller has a reconciliation problem either way.
		return Result{}, fmt.Errorf("marking paid: %w", err)
	}

	// Deliberately not fatal: the order is paid and recorded, so failing here would report
	// a failure for a checkout that succeeded.
	notified := true
	if err := c.notifier.Notify(ctx, order.Email, "Order confirmed", "Thanks for your order."); err != nil {
		notified = false
	}

	return Result{Charge: charge, Notified: notified, At: c.clock.Now()}, nil
}

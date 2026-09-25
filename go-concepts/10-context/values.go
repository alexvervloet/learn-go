package main

import (
	"context"
	"fmt"
	"time"
)

// Context values
// ==============
//
//	func WithValue(parent Context, key, val any) Context
//	func (c Context) Value(key any) any
//
// Untyped on both ends, which is why the key needs care and the accessor needs
// to be written by hand. The safe idiom, used throughout this file:
//
//  1. An UNEXPORTED key type, so no other package can collide or read it.
//  2. A setter and a getter as the only access.
//  3. The getter returns a typed value plus an ok.
//
// And the rule that matters more than the idiom: context values are for
// REQUEST-SCOPED data that crosses API boundaries. A request ID, a trace span,
// an authenticated user. Not configuration, not a database handle, not optional
// arguments. If a function needs a thing to work, that thing is a parameter.

// requestIDKey is an unexported struct type used only as a map key. Using
// struct{} rather than a string or int means:
//
//	no allocation             an empty struct is zero bytes
//	no possible collision     another package cannot name this type
//	no accidental reads       another package cannot construct the key
type requestIDKey struct{}

// userKey is a second key, distinct because the TYPES differ. Two struct{}
// types with different names are different keys even though both are empty.
type userKey struct{}

// traceKey carries a trace identifier.
type traceKey struct{}

// User is request-scoped data: it belongs to this request and no other.
type User struct {
	ID    int64
	Email string
	Admin bool
}

// WithRequestID attaches a request ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID reads it back. The ok result distinguishes "absent" from "present
// and empty", exactly as with a map.
func RequestID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(requestIDKey{}).(string)
	return id, ok
}

// WithUser attaches the authenticated user.
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

// CurrentUser reads it back.
func CurrentUser(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(userKey{}).(*User)
	return u, ok
}

// WithTrace attaches a trace ID.
func WithTrace(ctx context.Context, trace string) context.Context {
	return context.WithValue(ctx, traceKey{}, trace)
}

// Trace reads it back.
func Trace(ctx context.Context) (string, bool) {
	t, ok := ctx.Value(traceKey{}).(string)
	return t, ok
}

// valuesAreImmutableAndLayered: WithValue returns a NEW context wrapping the
// old one. Nothing is mutated, so a parent never sees a child's values, and
// "overwriting" a key just shadows it for that subtree.
func valuesAreImmutableAndLayered() (parentHas, childHas bool, parentValue, childValue string) {
	parent := WithRequestID(context.Background(), "parent-id")
	child := WithRequestID(parent, "child-id")

	parentValue, parentHas = RequestID(parent)
	childValue, childHas = RequestID(child)

	return parentHas, childHas, parentValue, childValue
}

// lookupIsLinear is the performance characteristic to know. Value walks up the
// chain comparing keys, so a context with fifty values costs fifty comparisons
// on a miss.
//
// In practice this is fine for the handful of values a request should carry,
// and it is another reason not to use a context as a general-purpose bag.
func lookupIsLinear(depth int) (found bool, missing bool) {
	ctx := context.Background()

	for i := 0; i < depth; i++ {
		// Each of these is a distinct layer that a lookup must walk past.
		ctx = context.WithValue(ctx, fmt.Sprintf("filler-%d", i), i) //nolint:staticcheck // SA1029: demonstrating the wrong key type
	}
	ctx = WithRequestID(ctx, "found-me")

	_, found = RequestID(ctx)
	_, missing = CurrentUser(ctx) // walks the whole chain and finds nothing

	return found, missing
}

// stringKeysCollide is the mistake the unexported-type idiom prevents. Two
// packages both using the string "user_id" silently overwrite each other, and
// the failure appears as the wrong user's data rather than as an error.
//
// go vet's SA1029 (via staticcheck) flags a basic type as a context key, which
// is why the calls below carry a suppression.
func stringKeysCollide() (packageAValue, packageBValue string) {
	ctx := context.Background()

	// Package A stores what it thinks is its own value.
	ctx = context.WithValue(ctx, "user_id", "package-a-value") //nolint:staticcheck // SA1029: the collision is the demonstration

	// Package B, written by someone else, picks the same obvious string.
	ctx = context.WithValue(ctx, "user_id", "package-b-value") //nolint:staticcheck // SA1029: the collision is the demonstration

	// Both read the same key. A's value is shadowed and unreachable.
	v, _ := ctx.Value("user_id").(string) //nolint:staticcheck // SA1029

	return v, v
}

// typedKeysCannotCollide: two unexported struct types never compare equal, so
// two packages using this idiom cannot interfere, even by accident.
func typedKeysCannotCollide() (requestID string, user *User, trace string) {
	ctx := context.Background()
	ctx = WithRequestID(ctx, "req-123")
	ctx = WithUser(ctx, &User{ID: 7, Email: "ana@example.com", Admin: true})
	ctx = WithTrace(ctx, "trace-abc")

	requestID, _ = RequestID(ctx)
	user, _ = CurrentUser(ctx)
	trace, _ = Trace(ctx)

	return requestID, user, trace
}

// whatDoesNotBelongInAContext is the rule, stated, because this is the single
// most abused part of the package.
func whatDoesNotBelongInAContext() []string {
	return []string{
		"configuration: it is not request-scoped, and a parameter is typed",
		"a database handle or an HTTP client: dependencies are constructor arguments",
		"optional function arguments: use a struct or functional options",
		"anything a function NEEDS to work: if it cannot proceed without it, it is a parameter",
		"large objects: contexts are copied down a call tree and live as long as the request",
	}
}

// whatBelongs is the short list.
func whatBelongs() []string {
	return []string{
		"a request ID or correlation ID",
		"an authenticated user or session, set by middleware",
		"a trace span, for distributed tracing",
		"a locale or feature-flag set decided per request",
	}
}

// valuesSurviveWithoutCancel is worth knowing when detaching work: the values
// come along, the cancellation does not.
func valuesSurviveWithoutCancel() (idBefore, idAfter string, cancelledBefore, cancelledAfter bool) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = WithRequestID(ctx, "req-999")

	idBefore, _ = RequestID(ctx)
	cancel()
	cancelledBefore = isDone(ctx)

	detached := context.WithoutCancel(ctx)
	idAfter, _ = RequestID(detached)
	cancelledAfter = isDone(detached)

	return idBefore, idAfter, cancelledBefore, cancelledAfter
}

// logWithContext is what the values are actually for: every log line in a
// request carries its identifiers without every function taking three extra
// parameters.
func logWithContext(ctx context.Context, message string) string {
	id, _ := RequestID(ctx)
	trace, _ := Trace(ctx)

	user := "anonymous"
	if u, ok := CurrentUser(ctx); ok {
		user = u.Email
	}

	deadline := "none"
	if d, ok := ctx.Deadline(); ok {
		deadline = time.Until(d).Round(time.Millisecond).String()
	}

	return fmt.Sprintf("[req=%s trace=%s user=%s budget=%s] %s", id, trace, user, deadline, message)
}

// demoValues prints value handling.
func demoValues() {
	parentHas, childHas, parentValue, childValue := valuesAreImmutableAndLayered()
	fmt.Printf("  WithValue returns a new context, it never mutates:\n")
	fmt.Printf("    parent: has=%t value=%q\n", parentHas, parentValue)
	fmt.Printf("    child:  has=%t value=%q  (shadows the parent for its subtree)\n", childHas, childValue)

	found, missing := lookupIsLinear(50)
	fmt.Printf("  lookup walks the chain: found the key=%t, a missing key walked all 50 layers=%t\n",
		found, !missing)

	a, b := stringKeysCollide()
	fmt.Printf("\n  two packages using the string key \"user_id\":\n")
	fmt.Printf("    package A reads %q\n    package B reads %q   <- A's value is gone\n", a, b)

	requestID, user, trace := typedKeysCannotCollide()
	fmt.Printf("  unexported struct keys, three values, no interference:\n")
	fmt.Printf("    request=%s user=%s trace=%s\n", requestID, user.Email, trace)

	idBefore, idAfter, cancelledBefore, cancelledAfter := valuesSurviveWithoutCancel()
	fmt.Printf("\n  WithoutCancel: id %q -> %q, cancelled %t -> %t\n",
		idBefore, idAfter, cancelledBefore, cancelledAfter)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = WithRequestID(ctx, "req-123")
	ctx = WithTrace(ctx, "trace-abc")
	ctx = WithUser(ctx, &User{ID: 7, Email: "ana@example.com"})
	fmt.Printf("\n  %s\n", logWithContext(ctx, "handling request"))

	fmt.Println("\n  what belongs in a context:")
	for _, s := range whatBelongs() {
		fmt.Printf("    %s\n", s)
	}
	fmt.Println("  what does not:")
	for _, s := range whatDoesNotBelongInAContext() {
		fmt.Printf("    %s\n", s)
	}
}

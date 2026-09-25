package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
)

// Sentinel errors
// ===============
//
// A sentinel is a package-level error value that callers compare against, to
// branch on a specific condition:
//
//	var ErrNotFound = errors.New("not found")
//
// The standard library is full of them: io.EOF, sql.ErrNoRows, fs.ErrNotExist,
// context.Canceled, context.DeadlineExceeded.
//
// Two things to understand before exporting one:
//
//  1. An exported sentinel is part of your API forever. Callers write
//     errors.Is(err, ErrNotFound) and you can never change what it means.
//  2. errors.New returns a POINTER, so two sentinels with identical text are
//     still different values. That is what makes comparison meaningful.

// The sentinels this package exports. Declared together so the set is visible,
// and documented individually because each one is a promise.
var (
	// ErrNotFound means the requested record does not exist. Callers typically
	// turn this into a 404.
	ErrNotFound = errors.New("not found")

	// ErrPermission means the caller is known but not allowed. A 403.
	ErrPermission = errors.New("permission denied")

	// ErrConflict means the write lost a race with another writer. Callers may
	// retry after re-reading.
	ErrConflict = errors.New("conflict")
)

// identicalTextStillDiffers proves sentinels compare by identity, not message.
// If errors.New returned a comparable value type instead, every "not found"
// error in the program would match every other one.
func identicalTextStillDiffers() (sameText bool, sameValue bool) {
	a := errors.New("not found")
	b := errors.New("not found")

	return a.Error() == b.Error(), errors.Is(a, b)
}

// fetchRecord is a fake repository call that returns sentinels wrapped in
// context. This is the normal shape: the sentinel says WHAT, the wrapper says
// WHERE.
func fetchRecord(id int) error {
	switch {
	case id <= 0:
		return fmt.Errorf("fetch record %d: %w", id, ErrNotFound)
	case id == 403:
		return fmt.Errorf("fetch record %d: %w", id, ErrPermission)
	case id == 409:
		return fmt.Errorf("fetch record %d: %w", id, ErrConflict)
	default:
		return nil
	}
}

// equalityBreaksOnceWrapped is the reason errors.Is exists. == compares the
// outer wrapper, which is a different value from the sentinel inside it, so it
// is always false. This looks correct in review and never fires.
func equalityBreaksOnceWrapped(id int) (withEquality bool, withErrorsIs bool) {
	err := fetchRecord(id)

	//nolint:errorlint // comparing with == is the bug being demonstrated
	return err == ErrNotFound, errors.Is(err, ErrNotFound)
}

// httpStatusFor is the realistic payoff: one function mapping every sentinel
// the package can produce onto a status code, wherever in the chain it sits.
func httpStatusFor(err error) int {
	switch {
	case err == nil:
		return 200
	case errors.Is(err, ErrNotFound):
		return 404
	case errors.Is(err, ErrPermission):
		return 403
	case errors.Is(err, ErrConflict):
		return 409
	default:
		// An error nobody anticipated. 500, and log it. Never leak the message
		// to the client: it may contain a query, a path, or a hostname.
		return 500
	}
}

// stdlibSentinels shows that errors.Is works across package boundaries, which
// is what makes the convention worth following.
func stdlibSentinels() (isEOF bool, isNotExist bool) {
	// io.EOF is the most-used sentinel in Go. It is not a failure: it is how a
	// reader reports that it finished.
	readErr := fmt.Errorf("decode body: %w", io.EOF)

	// fs.ErrNotExist is matched by os.IsNotExist and by errors.Is. The errors.Is
	// form is preferred, because it survives wrapping.
	openErr := fmt.Errorf("open /etc/nope: %w", fs.ErrNotExist)

	return errors.Is(readErr, io.EOF), errors.Is(openErr, fs.ErrNotExist)
}

// demoSentinels prints sentinel behaviour and the equality trap.
func demoSentinels() {
	sameText, sameValue := identicalTextStillDiffers()
	fmt.Printf("  two errors.New(\"not found\"): same text=%t, same value=%t\n", sameText, sameValue)

	withEq, withIs := equalityBreaksOnceWrapped(0)
	fmt.Printf("  wrapped ErrNotFound:  err == ErrNotFound -> %-5t   errors.Is -> %t\n", withEq, withIs)

	for _, id := range []int{1, 0, 403, 409} {
		err := fetchRecord(id)
		fmt.Printf("  fetchRecord(%3d) -> status %d   %v\n", id, httpStatusFor(err), err)
	}

	isEOF, isNotExist := stdlibSentinels()
	fmt.Printf("  wrapped io.EOF found: %t   wrapped fs.ErrNotExist found: %t\n", isEOF, isNotExist)
}

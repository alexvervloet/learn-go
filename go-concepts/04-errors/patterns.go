package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Everyday error patterns
// =======================
//
// The three that come up in almost every Go service: retrying on a classified
// failure, closing a resource without losing its error, and deciding when to
// panic instead.

// attemptFunc is the unit of work a retry loop drives. Returning a
// *RetryableError is how it says "this is worth trying again".
type attemptFunc func(attempt int) error

// retry runs fn until it succeeds, until fn returns something not retryable, or
// until maxAttempts is used up.
//
// Two details that matter more than the loop:
//
//  1. The decision to retry belongs to the ERROR, not to the loop. retry does
//     not inspect status codes or message text; it asks errors.As whether a
//     RetryableError is in the chain. New retryable conditions need no change
//     here.
//  2. The final error wraps the last failure, so the caller can still reach
//     the original cause with errors.Is and errors.As.
//
// sleep is injected so tests do not actually wait. A real caller passes
// time.Sleep.
func retry(maxAttempts int, sleep func(time.Duration), fn attemptFunc) error {
	if maxAttempts < 1 {
		return fmt.Errorf("retry: maxAttempts must be at least 1, got %d", maxAttempts)
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := fn(attempt)
		if err == nil {
			return nil
		}
		lastErr = err

		var rerr *RetryableError
		if !errors.As(err, &rerr) {
			// Not retryable. Returning immediately is the point: retrying a
			// validation failure just wastes the user's time.
			return fmt.Errorf("attempt %d: %w", attempt, err)
		}
		if attempt < maxAttempts {
			sleep(rerr.After)
		}
	}

	return fmt.Errorf("giving up after %d attempts: %w", maxAttempts, lastErr)
}

// writeConfig is the deferred-close pattern, which is the one place where
// getting error handling right takes real care.
//
// The trap: `defer f.Close()` discards Close's error. For a file opened for
// WRITING that is a real bug, because buffered data is flushed during Close and
// a full disk or a network filesystem failure is reported there and nowhere
// else. The write calls all succeeded; the data still did not land.
//
// The fix is a named return plus a deferred closure that assigns to it, taking
// care not to overwrite an earlier, more informative error.
func writeConfig(path string, data []byte) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}

	defer func() {
		cerr := f.Close()
		if cerr == nil {
			return
		}
		if err == nil {
			// The writes worked and Close failed. Close's error is the news.
			err = fmt.Errorf("close %s: %w", path, cerr)
			return
		}
		// Something already failed. Keep both: the first error explains the
		// cause, the Close error may explain a follow-on effect.
		err = errors.Join(err, fmt.Errorf("close %s: %w", path, cerr))
	}()

	if _, err = f.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// closeOnRead is the counterpart, and the reason people get complacent. For a
// file opened for READING, Close has nothing left to fail at, so the bare
// `defer f.Close()` really is fine. errcheck will still flag it, which is why
// the explicit discard with a reason is better than switching the linter off.
func readConfig(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// Reading only: Close cannot report a data-loss condition here.
	defer func() { _ = f.Close() }()

	// The loop is written out rather than using io.ReadAll, because the EOF
	// handling is the part worth seeing. Two rules:
	//
	//  1. io.EOF is NOT a failure. It is how a reader reports that it finished.
	//     Wrapping it in "read failed" context is a classic beginner bug.
	//  2. Process the n bytes BEFORE checking the error. Read is allowed to
	//     return n > 0 together with io.EOF, and code that checks the error
	//     first silently drops the last chunk of every file.
	data := make([]byte, 0, 64)
	buf := make([]byte, 32)
	for {
		n, rerr := f.Read(buf)
		data = append(data, buf[:n]...) // rule 2: consume n first

		if rerr != nil {
			if errors.Is(rerr, io.EOF) { // rule 1: errors.Is, never == or string comparison
				break
			}
			return nil, fmt.Errorf("read %s: %w", path, rerr)
		}
	}
	return data, nil
}

// mustCompileStyle shows when a panic is correct. A "Must" prefix is Go's
// convention for a constructor that panics rather than returning an error,
// reserved for inputs that are compile-time constants in practice.
//
// regexp.MustCompile, template.Must and netip.MustParseAddr all work this way.
// The rule: a Must function is for a literal you wrote, never for input.
func mustAbsolutePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		// Reaching this means the process has no working directory, which is
		// not a condition any caller can do anything about.
		panic(fmt.Sprintf("mustAbsolutePath(%q): %v", p, err))
	}
	return abs
}

// demoPatterns prints retry and the close-error pattern.
func demoPatterns() {
	// A transient failure that clears on the third attempt.
	noSleep := func(time.Duration) {}
	attempts := 0
	err := retry(5, noSleep, func(n int) error {
		attempts = n
		if n < 3 {
			return &RetryableError{After: 10 * time.Millisecond, Err: errors.New("connection reset")}
		}
		return nil
	})
	fmt.Printf("  retry, succeeds on attempt 3: err=%v, attempts=%d\n", err, attempts)

	// A permanent failure: retry must not loop.
	attempts = 0
	err = retry(5, noSleep, func(n int) error {
		attempts = n
		return &ValidationError{Field: fieldEmail, Value: "nope", Rule: ruleEmail}
	})
	fmt.Printf("  retry, not retryable:        attempts=%d\n", attempts)
	fmt.Printf("    %v\n", err)

	// Exhausting the budget: the last error stays reachable.
	attempts = 0
	err = retry(3, noSleep, func(n int) error {
		attempts = n
		return &RetryableError{After: time.Millisecond, Err: ErrConflict}
	})
	fmt.Printf("  retry, exhausted:            attempts=%d, errors.Is(err, ErrConflict)=%t\n",
		attempts, errors.Is(err, ErrConflict))
	fmt.Printf("    %v\n", err)

	// The close pattern, against a real file in the OS temp directory.
	path := filepath.Join(os.TempDir(), "go-concepts-04-errors.txt")
	if werr := writeConfig(path, []byte("key = value\n")); werr != nil {
		fmt.Printf("  writeConfig -> %v\n", werr)
	} else {
		data, rerr := readConfig(path)
		fmt.Printf("  writeConfig then readConfig -> %q, err=%v\n", string(data), rerr)
	}
	_ = os.Remove(path)

	fmt.Printf("  mustAbsolutePath(\".\") -> %s\n", mustAbsolutePath("."))
}

package transactions

import "time"

// timeIt runs fn and reports how long it took, so a test can log a ratio without turning into a
// benchmark. A ratio between two calls in the same process is meaningful; the absolute number is not,
// which is why these are logged and not asserted.
func timeIt(fn func() error) (time.Duration, error) {
	start := time.Now()
	err := fn()
	return time.Since(start), err
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}

	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}

	return string(digits)
}

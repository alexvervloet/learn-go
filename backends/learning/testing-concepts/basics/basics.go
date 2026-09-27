// Package basics is the code under test for the testing techniques in basics_test.go.
//
// The tests are the subject; this file exists so they have something to exercise. It is kept
// deliberately plain: a parser, a small piece of business logic, and a type with a lifecycle.
package basics

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Errors returned by ParseDuration below.
var (
	ErrEmpty       = errors.New("basics: empty input")
	ErrNoUnit      = errors.New("basics: missing unit")
	ErrUnknownUnit = errors.New("basics: unknown unit")
	ErrNegative    = errors.New("basics: negative duration")
)

// Seconds is a duration in seconds, so the parser returns something simple.
type Seconds int

// ParseDuration parses "90s", "5m", "2h" into seconds.
//
// Deliberately not time.ParseDuration: a function with several error paths and a few edge cases
// is what a table-driven test is for, and using the stdlib's version would leave nothing to
// test.
func ParseDuration(s string) (Seconds, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, ErrEmpty
	}

	unit := s[len(s)-1]
	digits := s[:len(s)-1]

	if digits == "" {
		return 0, fmt.Errorf("%w: %q has no number", ErrNoUnit, s)
	}

	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrNoUnit, s)
	}
	if n < 0 {
		return 0, fmt.Errorf("%w: %d", ErrNegative, n)
	}

	switch unit {
	case 's':
		return Seconds(n), nil
	case 'm':
		return Seconds(n * 60), nil
	case 'h':
		return Seconds(n * 3600), nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrUnknownUnit, string(unit))
	}
}

// Cart is a shopping cart, for the subtests and the fixture examples.
type Cart struct {
	items map[string]int
	rates map[string]int // price in cents
}

// NewCart returns an empty cart with the given price list.
func NewCart(rates map[string]int) *Cart {
	return &Cart{items: make(map[string]int), rates: rates}
}

// Add puts n of an item in the cart.
func (c *Cart) Add(item string, n int) error {
	if _, ok := c.rates[item]; !ok {
		return fmt.Errorf("basics: no price for %q", item)
	}
	if n <= 0 {
		return fmt.Errorf("basics: quantity must be positive, got %d", n)
	}

	c.items[item] += n
	return nil
}

// Total returns the cart's total in cents.
func (c *Cart) Total() int {
	total := 0
	for item, n := range c.items {
		total += c.rates[item] * n
	}
	return total
}

// Len returns the number of distinct items.
func (c *Cart) Len() int { return len(c.items) }

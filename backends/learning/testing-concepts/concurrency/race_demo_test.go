//go:build demo_race

// Behind a build tag, because this test is SUPPOSED to fail under -race. Run it deliberately:
//
//	go test -tags demo_race -race -run TestDeliberateRace ./concurrency
//
// The output is the thing to read: the detector names both accesses, both goroutines, and the line
// each was created on. That report is why -race is worth the 5-10x slowdown in CI.
package concurrency

import (
	"sync"
	"testing"
)

func TestDeliberateRace(t *testing.T) {
	c := &UnsafeCounter{}

	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			for range 1000 {
				c.Add(1)
			}
		})
	}
	wg.Wait()

	t.Logf("counter = %d, want 2000", c.Value())
}

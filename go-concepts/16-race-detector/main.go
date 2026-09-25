package main

import "fmt"

func section(n int, title string) {
	fmt.Printf("\n%d. %s\n%s\n", n, title, "-----------------------------------------")
}

func main() {
	fmt.Println("Lesson 16 — the race detector")

	section(1, "Five race shapes")
	demoRaces()

	section(2, "Each one fixed, and the rule that makes the fix work")
	demoFixes()

	section(3, "Code that looks racy and is not")
	demoNotARaces()

	section(4, "What -race cannot see")
	demoNotCaught()

	section(5, "Reading a report")
	demoReading()

	fmt.Println("\nRun `go test ./16-race-detector` to see the races lose updates.")
	fmt.Println("Run `go test -race ./16-race-detector` to see them skip, and everything else checked.")
}

// demoRaces prints each racy shape and what it produces.
func demoRaces() {
	const n = 2000

	if raceDetectorEnabled {
		fmt.Println("  running under -race, so the deliberate races are not executed here.")
		fmt.Println("  run without -race to see them lose updates.")
	} else {
		got := countWithRacyCounter(n)
		fmt.Printf("  an unsynchronised counter: %d of %d increments survived\n", got, n)

		appended := racyAppend(n)
		fmt.Printf("  concurrent append:         %d of %d elements survived\n", len(appended), n)

		captured := racyClosureCapture(n)
		fmt.Printf("  a captured variable:       total %d, expected %d\n", captured, n*(n-1)/2)

		cfg := racyStructAccess(1000)
		fmt.Printf("  unsynchronised struct fields: timeout=%d retries=%d\n", cfg.Timeout, cfg.Retries)
	}

	fmt.Println("\n  a concurrent map is worse than a race:")
	for _, s := range racyMapWriteDescription() {
		fmt.Printf("    %s\n", s)
	}

	fmt.Println("\n  the four conditions for a race:")
	for _, s := range raceConditions() {
		fmt.Printf("    %s\n", s)
	}

	fmt.Println("\n  why undefined behaviour matters:")
	for _, s := range whyUndefinedBehaviourMatters() {
		fmt.Printf("    %s\n", s)
	}
}

// demoFixes prints each fix producing the correct answer.
func demoFixes() {
	const n = 2000

	fmt.Printf("  mutex counter:        %d of %d\n", countWithMutex(n), n)
	fmt.Printf("  atomic counter:       %d of %d\n", countWithAtomic(n), n)
	fmt.Printf("  guarded map:          %d of %d entries\n", fillMapConcurrently(n), n)
	fmt.Printf("  pre-sized slice:      %d of %d elements, in order, no lock\n", len(fillSliceByIndex(n)), n)
	fmt.Printf("  append under a lock:  %d of %d elements, order not guaranteed\n", len(appendUnderLock(n)), n)
	fmt.Printf("  collected by channel: %d of %d elements\n", len(collectOverAChannel(n)), n)

	guarded, swapped := updateConfigConcurrently(1000)
	timeout, retries, _ := guarded.Snapshot()
	fmt.Printf("  guarded struct:       timeout=%d retries=%d (consistent)\n", timeout, retries)
	fmt.Printf("  swapped immutably:    timeout=%d retries=%d (consistent)\n", swapped.Timeout, swapped.Retries)

	fmt.Printf("  no sharing at all:    sum=%d\n", sumWithoutSharing(1000, 8))

	fmt.Println("\n  the happens-before edges that make a fix a fix:")
	for _, s := range happensBeforeEdges() {
		fmt.Printf("    %s\n", s)
	}

	fmt.Println("\n  which fix to reach for, in order:")
	for _, s := range fixPreference() {
		fmt.Printf("    %s\n", s)
	}
}

// demoNotARaces prints safe code that looks unsafe.
func demoNotARaces() {
	fmt.Printf("  distinct slice indexes:   %d elements, no lock\n", len(distinctIndexesAreSafe(100)))
	fmt.Printf("  read-only shared data:    sum=%d across 8 goroutines\n",
		readOnlySharingIsSafe([]int{1, 2, 3, 4, 5}, 8))
	fmt.Printf("  ownership over a channel: %d batches processed with no lock\n",
		len(ownershipTransferIsSafe(50)))
	fmt.Printf("  a value copied at the go statement: %d results\n",
		len(copyingBeforeTheGoStatementIsSafe(racyConfig{Timeout: 10}, 20)))

	values, distinct := onceIsSafe(100)
	fmt.Printf("  sync.Once: %d readers saw %d distinct value(s): %q\n", len(values), distinct, values[0])

	fmt.Println("\n  why each is safe:")
	reasons := whyEachIsSafe()
	for _, k := range []string{
		"distinct slice indexes", "read-only shared data", "ownership over a channel",
		"a value passed as an argument", "sync.Once",
	} {
		fmt.Printf("    %-32s %s\n", k, reasons[k])
	}
}

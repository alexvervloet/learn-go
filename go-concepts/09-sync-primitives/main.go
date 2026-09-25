package main

import "fmt"

func section(n int, title string) {
	fmt.Printf("\n%d. %s\n%s\n", n, title, "-----------------------------------------")
}

func main() {
	fmt.Println("Lesson 09 — sync primitives")

	section(1, "Mutex, RWMutex, and check-then-act")
	demoMutexes()

	section(2, "WaitGroup: the rules and the bugs")
	demoWaitGroups()

	section(3, "sync.Once and its Go 1.21 replacements")
	demoOnce()

	section(4, "Atomics: counters, CAS, and consistent snapshots")
	demoAtomics()

	section(5, "sync.Map against a RWMutex map")
	demoMaps()

	section(6, "sync.Pool: allocation reuse, not resource pooling")
	demoPools()

	section(7, "errgroup: WaitGroup plus first-error plus cancellation")
	demoErrgroups()

	fmt.Println("\nRun `go test -race ./09-sync-primitives` to see each claim verified.")
}

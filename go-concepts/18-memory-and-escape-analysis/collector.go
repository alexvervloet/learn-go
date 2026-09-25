package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// The garbage collector
// =====================
//
// Concurrent, tricolour mark-and-sweep. NON-generational and NON-compacting,
// with a write barrier.
//
// Non-compacting has visible consequences: pointers are stable, so
// unsafe.Pointer arithmetic works and cgo can hold a heap pointer for the
// duration of a call. The cost is that memory can fragment.
//
// It is tuned for LATENCY. Pauses are sub-millisecond, and the price is that it
// does more total work than a stop-the-world collector would. That is the right
// trade for a server and the wrong one for a batch job, which is what GOGC is
// for.

// MemorySnapshot is the subset of runtime.MemStats worth looking at. The full
// struct has fifty fields and most of them are for the runtime's own use.
type MemorySnapshot struct {
	HeapAllocKB    uint64 // allocated and still reachable
	HeapSysKB      uint64 // obtained from the OS
	HeapIdleKB     uint64 // in spans with no objects, returnable to the OS
	HeapReleasedKB uint64 // actually returned
	StackInuseKB   uint64
	NumGC          uint32
	PauseTotalMs   float64
	GCCPUPercent   float64
	NextGCKB       uint64 // the heap size that will trigger the next cycle
}

// Snapshot reads the current state.
//
// ReadMemStats STOPS THE WORLD briefly, so calling it in a hot loop is
// self-defeating. Once per scrape interval on a metrics endpoint is fine;
// once per request is not.
func Snapshot() MemorySnapshot {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return MemorySnapshot{
		HeapAllocKB:    m.HeapAlloc / 1024,
		HeapSysKB:      m.HeapSys / 1024,
		HeapIdleKB:     m.HeapIdle / 1024,
		HeapReleasedKB: m.HeapReleased / 1024,
		StackInuseKB:   m.StackInuse / 1024,
		NumGC:          m.NumGC,
		PauseTotalMs:   float64(m.PauseTotalNs) / 1e6,
		GCCPUPercent:   m.GCCPUFraction * 100,
		NextGCKB:       m.NextGC / 1024,
	}
}

// String renders a snapshot.
func (s MemorySnapshot) String() string {
	return fmt.Sprintf("heap %d KB (sys %d, idle %d, released %d), stack %d KB, %d GCs, %.2f ms paused, %.2f%% CPU, next at %d KB",
		s.HeapAllocKB, s.HeapSysKB, s.HeapIdleKB, s.HeapReleasedKB,
		s.StackInuseKB, s.NumGC, s.PauseTotalMs, s.GCCPUPercent, s.NextGCKB)
}

// allocateAndDiscard creates garbage on purpose, so the collector has
// something to do.
func allocateAndDiscard(rounds, size int) (collections uint32) {
	before := Snapshot()

	for i := 0; i < rounds; i++ {
		buf := make([]byte, size)
		buf[0] = byte(i)
		sinkSlice = buf
	}
	sinkSlice = nil

	return Snapshot().NumGC - before.NumGC
}

// gogcControlsWhenItRuns demonstrates the knob.
//
// GOGC=100 (the default) means "collect when the heap has grown 100% since the
// last cycle finished". GOGC=400 means wait for 400%: fewer collections, more
// memory. GOGC=off disables proportional collection entirely.
//
// debug.SetGCPercent is the programmatic form, and returns the previous value.
func gogcControlsWhenItRuns(rounds, size int) (atDefault, atHigh uint32) {
	previous := debug.SetGCPercent(100)
	defer debug.SetGCPercent(previous)

	runtime.GC()
	atDefault = allocateAndDiscard(rounds, size)

	debug.SetGCPercent(800)
	runtime.GC()
	atHigh = allocateAndDiscard(rounds, size)

	return atDefault, atHigh
}

// gomemlimitIsTheContainerAnswer.
//
// GOGC is proportional and knows nothing about the machine, so a container with
// a 512MB limit and a heap that grows in bursts gets OOM-killed while the
// collector is patiently waiting for the next doubling.
//
// GOMEMLIMIT (Go 1.19) is a SOFT limit on total memory. As the process
// approaches it, the collector runs more often, up to continuously. It does not
// prevent an OOM if live data genuinely exceeds it, but it stops the common
// case of dying with a mostly-garbage heap.
//
// The recommended configuration for a container: GOMEMLIMIT at about 90% of the
// cgroup limit, and GOGC left alone or turned off.
func gomemlimitIsTheContainerAnswer() (previous int64, current int64) {
	previous = debug.SetMemoryLimit(-1) // -1 reads without setting

	// Set a limit, then restore it. math.MaxInt64 is "no limit", which is the
	// default and what -1 reports when none is set.
	debug.SetMemoryLimit(512 << 20)
	current = debug.SetMemoryLimit(-1)

	debug.SetMemoryLimit(previous)
	return previous, current
}

// forcingACycle is occasionally right: before a heap profile (so it shows what
// is genuinely reachable), and after a large one-off allocation in a batch job.
//
// In a server it is almost always wrong, because it stops the world and the
// collector's own pacing is better informed than you are.
func forcingACycle() (beforeKB, afterKB uint64) {
	// Built inside a closure so the slice genuinely goes out of scope. An
	// earlier version set `garbage = nil` and staticcheck flagged the appends
	// as pointless (SA4010), which was fair: assigning nil does not make the
	// data unreachable if the variable is still live in the frame.
	before := func() MemorySnapshot {
		garbage := make([][]byte, 0, 1000)
		for i := 0; i < 1000; i++ {
			garbage = append(garbage, make([]byte, 1024))
		}

		snapshot := Snapshot()
		runtime.KeepAlive(garbage) // alive up to here, and dead after
		return snapshot
	}()

	runtime.GC()

	return before.HeapAllocKB, Snapshot().HeapAllocKB
}

// returningMemoryToTheOS: Go returns free memory lazily, so RSS stays high
// after a spike. debug.FreeOSMemory forces it, at the cost of a full GC and a
// stop-the-world pause.
//
// Usually the right answer is to do nothing: the memory is free for the next
// spike, and returning it means faulting the pages back in.
func returningMemoryToTheOS() (idleBeforeKB, releasedAfterKB uint64) {
	before := Snapshot()

	debug.FreeOSMemory()

	after := Snapshot()
	return before.HeapIdleKB, after.HeapReleasedKB
}

// collectorProperties is the summary.
func collectorProperties() map[string]string {
	return map[string]string{
		"algorithm":        "concurrent tricolour mark-and-sweep with a write barrier",
		"generational":     "NO: every cycle scans the whole live heap",
		"compacting":       "NO: pointers are stable, so unsafe and cgo work; memory can fragment",
		"tuned for":        "LATENCY: sub-millisecond pauses, at the cost of total work",
		"what triggers it": "heap growth past NextGC, or a 2-minute timer, or runtime.GC()",
		"what it scans":    "POINTERS, not bytes: a []int of a million is one object to scan",
	}
}

// knobs is the reference.
func knobs() map[string]string {
	return map[string]string{
		"GOGC=100":              "the default: collect when the heap has grown 100% since the last cycle",
		"GOGC=400":              "fewer collections, more memory: for a batch job",
		"GOGC=off":              "no proportional collection; pair it with GOMEMLIMIT",
		"GOMEMLIMIT=512MiB":     "a SOFT total-memory limit; the collector works harder near it",
		"debug.SetGCPercent(n)": "GOGC at runtime; returns the previous value",
		"debug.SetMemoryLimit":  "GOMEMLIMIT at runtime; -1 reads without setting",
		"debug.FreeOSMemory()":  "force a collection AND return free memory to the OS",
		"GODEBUG=gctrace=1":     "a line per collection on stderr: the cheapest observability there is",
	}
}

// demoCollector prints collector behaviour.
func demoCollector() {
	fmt.Printf("  at startup: %s\n", Snapshot())

	collections := allocateAndDiscard(20_000, 1024)
	fmt.Printf("  after allocating 20MB in 1KB pieces: %d collection(s)\n", collections)
	fmt.Printf("  now:        %s\n", Snapshot())

	atDefault, atHigh := gogcControlsWhenItRuns(20_000, 1024)
	fmt.Printf("\n  the same work at GOGC=100: %d collections\n", atDefault)
	fmt.Printf("  at GOGC=800:               %d collections\n", atHigh)
	fmt.Println("    ...fewer collections, more memory held. That is the whole trade.")

	previous, current := gomemlimitIsTheContainerAnswer()
	fmt.Printf("\n  GOMEMLIMIT: was %d (math.MaxInt64 means unset), set to %d, then restored\n",
		previous, current)

	beforeKB, afterKB := forcingACycle()
	fmt.Printf("  forcing a cycle: heap %d KB -> %d KB\n", beforeKB, afterKB)

	idleKB, releasedKB := returningMemoryToTheOS()
	fmt.Printf("  FreeOSMemory: %d KB idle before, %d KB released after\n", idleKB, releasedKB)

	fmt.Println("\n  the collector:")
	for _, k := range []string{
		"algorithm", "generational", "compacting", "tuned for", "what triggers it", "what it scans",
	} {
		fmt.Printf("    %-17s %s\n", k, collectorProperties()[k])
	}

	fmt.Println("\n  the knobs:")
	for _, k := range []string{
		"GOGC=100", "GOGC=400", "GOGC=off", "GOMEMLIMIT=512MiB",
		"debug.SetGCPercent(n)", "debug.SetMemoryLimit", "debug.FreeOSMemory()", "GODEBUG=gctrace=1",
	} {
		fmt.Printf("    %-22s %s\n", k, knobs()[k])
	}
}

// sinkSlice keeps allocations alive long enough to become garbage rather than
// being optimised away.
//
// It is written and never read, which is the entire point, and which
// staticcheck's `unused` check reports. The suppression is narrow and says why:
// removing the variable would let the compiler delete the allocations the
// collector demo needs.
//
//nolint:unused // written to defeat optimisation; never read, by design
var sinkSlice []byte

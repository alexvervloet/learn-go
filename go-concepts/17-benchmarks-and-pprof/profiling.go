package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
)

// Profiling
// =========
//
// Six profiles, all in the standard library:
//
//	CPU     where time goes                    -cpuprofile
//	heap    what is allocated AND still live   -memprofile
//	allocs  every allocation ever made         -memprofile -memprofilerate=1
//	block   time blocked on channels and locks -blockprofile
//	mutex   lock contention                    -mutexprofile
//	trace   the scheduler, GC and goroutines   -trace
//
//	go test -bench . -cpuprofile=cpu.out
//	go tool pprof -http=:8080 cpu.out

// ProfileKind describes one, and when to reach for it.
type ProfileKind struct {
	Name      string
	Shows     string
	TestFlag  string
	WhenToUse string
}

// profileKinds is the reference.
func profileKinds() []ProfileKind {
	return []ProfileKind{
		{
			Name:      "cpu",
			Shows:     "where wall-clock time is spent, sampled at 100Hz",
			TestFlag:  "-cpuprofile=cpu.out",
			WhenToUse: "the service is slow and busy",
		},
		{
			Name:      "heap",
			Shows:     "what is allocated and STILL REACHABLE",
			TestFlag:  "-memprofile=mem.out",
			WhenToUse: "memory grows and does not come back: a leak",
		},
		{
			Name:      "allocs",
			Shows:     "every allocation ever made, live or not",
			TestFlag:  "-memprofile=mem.out -memprofilerate=1",
			WhenToUse: "GC pressure is high but memory is not growing",
		},
		{
			Name:      "block",
			Shows:     "time spent blocked on channels, locks and syscalls",
			TestFlag:  "-blockprofile=block.out",
			WhenToUse: "latency is bad but CPU is idle",
		},
		{
			Name:      "mutex",
			Shows:     "contention: who is waiting for which lock",
			TestFlag:  "-mutexprofile=mutex.out",
			WhenToUse: "adding cores does not help throughput",
		},
		{
			Name:      "trace",
			Shows:     "a timeline of the scheduler, GC and every goroutine",
			TestFlag:  "-trace=trace.out",
			WhenToUse: "the behaviour is intermittent and the other profiles look fine",
		},
	}
}

// heapVsAllocs is the distinction people get wrong most often.
func heapVsAllocs() []string {
	return []string{
		"heap:   what is STILL REACHABLE when the profile is taken -> finds LEAKS",
		"allocs: every allocation ever made                        -> finds GC PRESSURE",
		"a program allocating 10GB and freeing it all has a huge allocs profile and a tiny heap",
		"a program holding 100MB forever has a small allocs profile and a heap that grows",
		"they answer different questions; taking the wrong one wastes an afternoon",
	}
}

// flatVsCum is what makes a profile readable at all.
func flatVsCum() []string {
	return []string{
		"flat: time in that function's OWN code",
		"cum:  time in it AND everything it called",
		"main always has ~100% cum and ~0% flat: that is normal and uninteresting",
		"sort by flat to find where the work happens",
		"sort by cum to find which subtree to look inside",
		"the function to FIX is often the caller of the one with high flat",
	}
}

// writeCPUProfile captures a CPU profile around fn and writes it to path.
//
// This is the from-code version. In a test, -cpuprofile does it for you, and
// in a service net/http/pprof does. Doing it by hand is worth knowing for a
// batch job or a CLI, where neither applies.
func writeCPUProfile(path string, fn func()) (err error) {
	f, err := os.Create(path) //nolint:gosec // a caller-supplied path in a demo
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", path, cerr)
		}
	}()

	if err := pprof.StartCPUProfile(f); err != nil {
		return fmt.Errorf("start cpu profile: %w", err)
	}
	defer pprof.StopCPUProfile()

	fn()
	return nil
}

// writeHeapProfile captures the heap after fn.
//
// runtime.GC() first is not optional: without it the profile includes objects
// that are already unreachable and simply have not been collected, which makes
// a leak hunt much harder.
func writeHeapProfile(path string, fn func()) (err error) {
	fn()

	runtime.GC() // so the profile shows what is genuinely still reachable

	f, err := os.Create(path) //nolint:gosec // a caller-supplied path in a demo
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", path, cerr)
		}
	}()

	if err := pprof.WriteHeapProfile(f); err != nil {
		return fmt.Errorf("write heap profile: %w", err)
	}
	return nil
}

// profileCommands are the ones worth having.
func profileCommands() map[string]string {
	return map[string]string{
		"go tool pprof -http=:8080 cpu.out":      "a flame graph and call graph in a browser: start here",
		"go tool pprof -top -nodecount=10 x.out": "the ten hottest functions, on the terminal",
		"go tool pprof -list=FuncName x.out":     "line-by-line timings inside one function",
		"go tool pprof -peek=FuncName x.out":     "who calls it, and who it calls",
		"go tool pprof -base=old.out new.out":    "the DIFFERENCE between two profiles",
		"go tool trace trace.out":                "the execution timeline in a browser",
	}
}

// interpretingACPUProfile is the procedure.
func interpretingACPUProfile() []string {
	return []string{
		"1. -top first: if one function is 40% flat, that is the answer",
		"2. if the time is spread thin, look for a COMMON CALLER with high cum",
		"3. runtime.mallocgc high in flat means allocations: take an allocs profile instead",
		"4. runtime.gcBgMarkWorker high means GC pressure: same conclusion",
		"5. syscall high means IO: a CPU profile is the wrong tool, take a block profile",
		"6. -list on the hot function to see which LINE, then decide",
	}
}

// burnCPU is the workload the demo profiles. Deliberately allocation-heavy so
// the resulting profile has something recognisable in it.
func burnCPU(iterations int) int {
	total := 0
	for i := 0; i < iterations; i++ {
		parts := make([]string, 0, 8)
		for j := 0; j < 8; j++ {
			parts = append(parts, strings.Repeat("x", j+1))
		}
		sort.Strings(parts)
		total += len(strings.Join(parts, "-"))
	}
	return total
}

// demoProfiling writes a real profile and reports on it.
func demoProfiling() {
	fmt.Println("  the six profiles:")
	for _, p := range profileKinds() {
		fmt.Printf("    %-7s %-42s %s\n", p.Name, p.Shows, p.TestFlag)
	}

	fmt.Println("\n  when to reach for each:")
	for _, p := range profileKinds() {
		fmt.Printf("    %-7s %s\n", p.Name, p.WhenToUse)
	}

	fmt.Println("\n  heap vs allocs, which are not the same profile:")
	for _, s := range heapVsAllocs() {
		fmt.Printf("    %s\n", s)
	}

	fmt.Println("\n  flat vs cum:")
	for _, s := range flatVsCum() {
		fmt.Printf("    %s\n", s)
	}

	// Write real profiles into the OS temp directory, so the demo produces
	// something you can actually open.
	dir := os.TempDir()
	cpuPath := filepath.Join(dir, "go-concepts-17-cpu.out")
	heapPath := filepath.Join(dir, "go-concepts-17-heap.out")

	cpuErr := writeCPUProfile(cpuPath, func() { sinkInt = burnCPU(20_000) })
	heapErr := writeHeapProfile(heapPath, func() { sinkSlice = growPresized(100_000) })

	fmt.Printf("\n  wrote a real CPU profile:  %s (err=%v)\n", cpuPath, cpuErr)
	fmt.Printf("  wrote a real heap profile: %s (err=%v)\n", heapPath, heapErr)
	fmt.Printf("    open it with: go tool pprof -top -nodecount=10 %s\n", cpuPath)

	fmt.Println("\n  the commands:")
	for _, k := range []string{
		"go tool pprof -http=:8080 cpu.out",
		"go tool pprof -top -nodecount=10 x.out",
		"go tool pprof -list=FuncName x.out",
		"go tool pprof -peek=FuncName x.out",
		"go tool pprof -base=old.out new.out",
		"go tool trace trace.out",
	} {
		fmt.Printf("    %-40s %s\n", k, profileCommands()[k])
	}

	fmt.Println("\n  interpreting a CPU profile:")
	for _, s := range interpretingACPUProfile() {
		fmt.Printf("    %s\n", s)
	}
}

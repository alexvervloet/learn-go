package main

import (
	"fmt"
	"sync"
	"time"
)

// Channel patterns
// ================
//
// Five shapes that cover most of what channels are used for in real code.
// Everything here uses only send, receive and close; select arrives in the
// next lesson and adds the rest.

// pingPong alternates between two goroutines using two unbuffered channels.
// It is the clearest demonstration that an unbuffered channel synchronises:
// the two goroutines take strict turns, with no locks and no shared state.
func pingPong(rounds int) []string {
	var (
		ping = make(chan int)
		pong = make(chan int)
		mu   sync.Mutex
		log  []string
		wg   sync.WaitGroup
	)

	record := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		log = append(log, s)
	}

	wg.Add(1)
	go func() { // the pong player
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			v := <-ping
			record(fmt.Sprintf("pong received %d", v))
			pong <- v + 1
		}
	}()

	for i := 0; i < rounds; i++ {
		ping <- i * 10
		v := <-pong
		record(fmt.Sprintf("ping received %d", v))
	}

	wg.Wait()
	return log
}

// semaphore limits how many goroutines do something at once. A buffered channel
// IS a counting semaphore: acquiring is a send, releasing is a receive, and the
// capacity is the permit count.
//
// This is the standard way to bound concurrency in Go. There is no pool to
// configure, no worker count to tune, and the goroutines are still one per unit
// of work.
type semaphore chan struct{}

func newSemaphore(permits int) semaphore {
	return make(semaphore, permits)
}

// acquire blocks until a permit is free.
func (s semaphore) acquire() { s <- struct{}{} }

// release returns a permit. Always deferred, so a panic cannot strand one.
func (s semaphore) release() { <-s }

// boundedConcurrency runs n tasks with at most `limit` in flight, and reports
// the highest number that were running simultaneously.
func boundedConcurrency(tasks, limit int, work time.Duration) (peak int) {
	var (
		sem     = newSemaphore(limit)
		wg      sync.WaitGroup
		mu      sync.Mutex
		running int
	)

	for i := 0; i < tasks; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			sem.acquire()
			defer sem.release()

			mu.Lock()
			running++
			if running > peak {
				peak = running
			}
			mu.Unlock()

			time.Sleep(work)

			mu.Lock()
			running--
			mu.Unlock()
		}()
	}

	wg.Wait()
	return peak
}

// doneChannel is the cancellation shape that predates context. A struct{}
// channel that is only ever closed, never sent on, so closing broadcasts to
// every goroutine watching it.
//
// context.Context wraps exactly this and adds deadlines, values and a tree of
// cancellation. Lesson 10 covers it, and in new code you should use it. This is
// here because the pattern is underneath everything and still appears in
// libraries that predate context or do not want the dependency.
func doneChannel(workers int) (stopped int) {
	var (
		done = make(chan struct{})
		wg   sync.WaitGroup
		mu   sync.Mutex
	)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			<-done // released by the close

			mu.Lock()
			defer mu.Unlock()
			stopped++
		}()
	}

	time.Sleep(5 * time.Millisecond)
	close(done) // one signal, every worker sees it

	wg.Wait()
	return stopped
}

// request carries a reply channel with it. This is how you get a response back
// from a goroutine that owns some state, without exposing the state or locking
// it.
//
// The reply channel is buffered with capacity 1 on purpose: the server can send
// the reply and move on even if the requester has already given up, so the
// server goroutine never blocks on a departed client. Making it unbuffered here
// would be a leak waiting to happen.
type request struct {
	key   string
	reply chan int
}

// counterServer owns a map and serves requests for it. No mutex anywhere: the
// map is touched by exactly one goroutine, and everyone else sends messages.
//
// This is "share memory by communicating" in its purest form. Whether it beats
// a mutex depends on the workload; for a hot counter a mutex or atomic wins
// easily, and lesson 09 measures that.
func counterServer(requests <-chan request, done <-chan struct{}) {
	counts := make(map[string]int) // owned solely by this goroutine

	for {
		select {
		case req, ok := <-requests:
			if !ok {
				return
			}
			counts[req.key]++
			req.reply <- counts[req.key]
		case <-done:
			return
		}
	}
}

// askCounter sends one request and waits for its reply.
func askCounter(requests chan<- request, key string) int {
	reply := make(chan int, 1) // buffered: the server never blocks on us

	requests <- request{key: key, reply: reply}
	return <-reply
}

// fanInSimple merges two channels into one, without select, by running a
// goroutine per input and closing the output once both finish. The select
// version in lesson 08 is shorter; this one shows the WaitGroup-plus-closer
// shape doing real work.
func fanInSimple(a, b <-chan int) <-chan int {
	out := make(chan int)

	var wg sync.WaitGroup
	forward := func(in <-chan int) {
		defer wg.Done()
		for v := range in {
			out <- v
		}
	}

	wg.Add(2)
	go forward(a)
	go forward(b)

	go func() {
		wg.Wait()
		close(out) // exactly once, after both forwarders are done
	}()

	return out
}

// demoPatterns prints each pattern.
func demoPatterns() {
	fmt.Println("  ping-pong over two unbuffered channels:")
	for _, e := range pingPong(3) {
		fmt.Printf("    %s\n", e)
	}

	peak := boundedConcurrency(20, 4, 10*time.Millisecond)
	fmt.Printf("\n  20 tasks, semaphore of 4: peak concurrent = %d\n", peak)

	fmt.Printf("  done channel: one close stopped %d workers\n", doneChannel(50))

	requests := make(chan request)
	done := make(chan struct{})
	go counterServer(requests, done)

	var counts []int
	for i := 0; i < 3; i++ {
		counts = append(counts, askCounter(requests, "hits"))
	}
	counts = append(counts, askCounter(requests, "other"))
	close(done)
	fmt.Printf("  request/reply to a state-owning goroutine: %v\n", counts)

	a := make(chan int)
	b := make(chan int)
	go func() {
		defer close(a)
		for i := 0; i < 3; i++ {
			a <- i
		}
	}()
	go func() {
		defer close(b)
		for i := 10; i < 13; i++ {
			b <- i
		}
	}()

	total := 0
	for v := range fanInSimple(a, b) {
		total += v
	}
	fmt.Printf("  fan-in of two channels, values summing to %d\n", total)
}

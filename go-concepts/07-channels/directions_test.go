package main

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProduceConsume(t *testing.T) {
	withTimeout(t, 2*time.Second, func() {
		ch := make(chan int)
		go produce(ch, 5)

		sum, count := consume(ch)

		// 0 + 1 + 4 + 9 + 16
		if want := 30; sum != want {
			t.Errorf("sum = %d, want %d", sum, want)
		}
		if count != 5 {
			t.Errorf("count = %d, want 5", count)
		}
	})
}

func TestProduceClosesItsChannel(t *testing.T) {
	withTimeout(t, 2*time.Second, func() {
		ch := make(chan int, 10)
		produce(ch, 3)

		// Draining everything, the fourth receive must report closed.
		for i := 0; i < 3; i++ {
			<-ch
		}
		if _, ok := <-ch; ok {
			t.Error("produce should close its output channel when done")
		}
	})
}

func TestGenerator(t *testing.T) {
	withTimeout(t, 2*time.Second, func() {
		var got []string
		for w := range generator([]string{"go", "channels", "ownership"}) {
			got = append(got, w)
		}

		want := []string{"GO", "CHANNELS", "OWNERSHIP"}
		if !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

func TestGeneratorOnEmptyInput(t *testing.T) {
	withTimeout(t, 2*time.Second, func() {
		count := 0
		for range generator(nil) {
			count++
		}
		if count != 0 {
			t.Errorf("got %d values from an empty generator, want 0", count)
		}
	})
}

func TestBridge(t *testing.T) {
	withTimeout(t, 2*time.Second, func() {
		nums := make(chan int)
		strs := make(chan string)

		go produce(nums, 3)
		go bridge(nums, strs)

		var got []string
		for s := range strs {
			got = append(got, s)
		}

		want := []string{"value-0", "value-1", "value-4"}
		if !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

// TestOwnershipTransfer: the receiver mutates what it was handed, with no lock,
// and -race confirms there is no race because the sender let go.
func TestOwnershipTransfer(t *testing.T) {
	withTimeout(t, 2*time.Second, func() {
		batches := make(chan *batch)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(batches)
			for i := 1; i <= 3; i++ {
				buildAndSend(batches, i, []string{"item"})
			}
		}()

		got := receiveAndOwn(batches)
		wg.Wait()

		if len(got) != 3 {
			t.Fatalf("got %d results, want 3", len(got))
		}
		for i, s := range got {
			want := "item+processed-by-" + string(rune('1'+i))
			if s != want {
				t.Errorf("result %d = %q, want %q", i, s, want)
			}
		}
	})
}

// TestBuildAndSendCopiesItsInput: the batch takes a copy, so a caller reusing
// its slice afterwards cannot corrupt what was sent. Without the copy this is a
// data race that -race would find.
func TestBuildAndSendCopiesItsInput(t *testing.T) {
	withTimeout(t, 2*time.Second, func() {
		ch := make(chan *batch, 1)
		items := []string{"original"}

		buildAndSend(ch, 1, items)
		items[0] = "mutated after the send"

		got := <-ch
		if got.Items[0] != "original" {
			t.Errorf("batch item = %q, want %q — buildAndSend should copy", got.Items[0], "original")
		}
	})
}

// TestDirectionalConversionsAreOneWay documents what the compiler enforces.
// The failing conversions are in comments because they do not compile, which
// is the point.
func TestDirectionalConversionsAreOneWay(t *testing.T) {
	bidirectional := make(chan int, 1)

	// Both conversions are implicit and legal.
	var sendOnly chan<- int = bidirectional
	var recvOnly <-chan int = bidirectional

	sendOnly <- 42
	if got := <-recvOnly; got != 42 {
		t.Errorf("got %d, want 42", got)
	}

	// These do not compile:
	//   var back chan int = sendOnly
	//     -> cannot use sendOnly (variable of type chan<- int) as chan int value
	//   <-sendOnly
	//     -> invalid operation: cannot receive from send-only channel
	//   close(recvOnly)
	//     -> invalid operation: cannot close receive-only channel
	if !strings.Contains("compile-time", "compile") {
		t.Error("unreachable")
	}
}

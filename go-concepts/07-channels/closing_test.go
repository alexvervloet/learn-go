package main

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestReceiveFromClosedReturnsZero(t *testing.T) {
	values, oks := receiveFromClosedReturnsZero()

	// Buffered values survive the close and come out first.
	if want := []int{10, 20, 0, 0}; !slices.Equal(values, want) {
		t.Errorf("values = %v, want %v", values, want)
	}
	if want := []bool{true, true, false, false}; !slices.Equal(oks, want) {
		t.Errorf("oks = %v, want %v", oks, want)
	}
}

// TestCommaOkDisambiguates is the reason the two-value receive exists: the
// value alone cannot distinguish a real zero from a closed channel.
func TestCommaOkDisambiguates(t *testing.T) {
	realZero, realZeroOK, afterClose, afterCloseOK := commaOkDisambiguates()

	if realZero != 0 || afterClose != 0 {
		t.Fatalf("both receives should yield 0, got %d and %d", realZero, afterClose)
	}
	if realZeroOK != 1 {
		t.Error("a genuinely sent zero must report ok=true")
	}
	if afterCloseOK {
		t.Error("a receive from a closed, drained channel must report ok=false")
	}
}

func TestRangeStopsOnClose(t *testing.T) {
	for _, n := range []int{0, 1, 5, 100} {
		withTimeout(t, 2*time.Second, func() {
			got := rangeStopsOnClose(n)

			if len(got) != n {
				t.Fatalf("received %d values, want %d", len(got), n)
			}
			for i, v := range got {
				if v != i {
					t.Errorf("value %d = %d, want %d — order is guaranteed on one channel", i, v, i)
				}
			}
		})
	}
}

// TestTheThreePanics pins every way close goes wrong.
func TestTheThreePanics(t *testing.T) {
	panics := theThreePanics()

	tests := []struct {
		key  string
		want string
	}{
		{"send on closed", "send on closed channel"},
		{"close of closed", "close of closed channel"},
		{"close of nil", "close of nil channel"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got, ok := panics[tt.key]
			if !ok {
				t.Fatalf("no entry for %q", tt.key)
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("panic = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

func TestMultipleSendersNeedACloser(t *testing.T) {
	tests := []struct {
		senders, perSender int
	}{
		{1, 10},
		{4, 25},
		{16, 8},
	}

	for _, tt := range tests {
		withTimeout(t, 5*time.Second, func() {
			want := tt.senders * tt.perSender
			if got := multipleSendersNeedACloser(tt.senders, tt.perSender); got != want {
				t.Errorf("%d senders x %d: received %d, want %d", tt.senders, tt.perSender, got, want)
			}
		})
	}
}

// TestCloseAsBroadcast: one close releases every waiter, however many there are.
func TestCloseAsBroadcast(t *testing.T) {
	for _, waiters := range []int{1, 10, 1000} {
		withTimeout(t, 5*time.Second, func() {
			if got := closeAsBroadcast(waiters); got != waiters {
				t.Errorf("released %d of %d waiters", got, waiters)
			}
		})
	}
}

func TestWhyThereIsNoIsClosedIsDocumented(t *testing.T) {
	if got := whyThereIsNoIsClosed(); len(got) < 3 {
		t.Errorf("expected at least 3 documented reasons, got %d", len(got))
	}
}

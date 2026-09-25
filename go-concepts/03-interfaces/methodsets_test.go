package main

import "testing"

// TestDirectCallOnAValueWorks: the addressability rule permits the direct call
// even though the interface conversion would be refused.
func TestDirectCallOnAValueWorks(t *testing.T) {
	total, str := directCallOnAValueWorks()

	if total != 2 {
		t.Errorf("Total() = %d, want 2", total)
	}
	if want := "2: a,b"; str != want {
		t.Errorf("String() = %q, want %q", str, want)
	}
}

// TestValueReceiverGetsACopy is the bug that makes "never use a value receiver
// on a mutating method" a hard rule rather than a preference.
func TestValueReceiverGetsACopy(t *testing.T) {
	broken, working := valueReceiverGetsACopy()

	if broken != 0 {
		t.Errorf("value-receiver mutator left count = %d, want 0 — it mutates a copy", broken)
	}
	if working != 2 {
		t.Errorf("pointer-receiver mutator left count = %d, want 2", working)
	}
}

func TestSliceOfPointersSatisfies(t *testing.T) {
	adders := sliceOfPointersSatisfies()

	if len(adders) != 2 {
		t.Fatalf("got %d adders, want 2", len(adders))
	}
	for i, a := range adders {
		tally, ok := a.(*Tally)
		if !ok {
			t.Fatalf("adder %d is %T, want *Tally", i, a)
		}
		if tally.Total() != 1 {
			t.Errorf("adder %d total = %d, want 1", i, tally.Total())
		}
	}
}

// TestMethodSetMembership states the rule as assertions rather than prose. The
// var declarations at the top of methodsets.go already prove the positive
// cases at compile time; this checks them at the value level too, and the
// negative case is documented there because it cannot be written at all.
func TestMethodSetMembership(t *testing.T) {
	var value Tally
	var pointer = &Tally{}

	// Both satisfy the value-receiver interface.
	var _ Totaler = value
	var _ Totaler = pointer

	// Only the pointer satisfies the pointer-receiver interface. Asserting a
	// Tally value to Adder at runtime is the closest we can get to showing the
	// compile error, and it fails as expected.
	if _, ok := any(value).(Adder); ok {
		t.Error("Tally (value) must not satisfy Adder — Add has a pointer receiver")
	}
	if _, ok := any(pointer).(Adder); !ok {
		t.Error("*Tally must satisfy Adder")
	}
}

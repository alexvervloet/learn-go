package main

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// failingStore is a second UserStore, written in six lines, existing only to
// drive the handler's error path. This is the payoff of a two-method
// consumer-defined interface: no mocking library, no code generation.
type failingStore struct{ err error }

func (f failingStore) GetUser(context.Context, int64) (*User, error) { return nil, f.err }
func (f failingStore) SaveUser(context.Context, *User) error         { return f.err }

var _ UserStore = failingStore{}

func TestHandlerRegister(t *testing.T) {
	boom := errors.New("database unavailable")

	tests := []struct {
		name    string
		store   UserStore
		email   string
		wantErr bool
		errIs   error
	}{
		{"valid email is stored", NewMemoryStore(), "ana@example.com", false, nil},
		{"invalid email never reaches the store", NewMemoryStore(), "nope", true, nil},
		{"store failure propagates", failingStore{err: boom}, "ana@example.com", true, boom},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHandler(tt.store)

			err := h.Register(context.Background(), 1, "Ana", tt.email)

			if (err != nil) != tt.wantErr {
				t.Fatalf("Register() error = %v, wantErr %t", err, tt.wantErr)
			}
			if tt.errIs != nil && !errors.Is(err, tt.errIs) {
				t.Errorf("Register() error = %v, want it to wrap %v", err, tt.errIs)
			}
		})
	}
}

// TestMemoryStore exercises the concrete type, including the Len method that
// "return structs" preserved.
func TestMemoryStore(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	if s.Len() != 0 {
		t.Errorf("new store Len() = %d, want 0", s.Len())
	}

	if err := s.SaveUser(ctx, &User{ID: 1, Name: "Ana"}); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	if s.Len() != 1 {
		t.Errorf("Len() = %d, want 1", s.Len())
	}

	if err := s.SaveUser(ctx, &User{ID: 2}); err == nil {
		t.Error("SaveUser with an empty name should fail")
	}

	got, err := s.GetUser(ctx, 1)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Name != "Ana" {
		t.Errorf("Name = %q, want %q", got.Name, "Ana")
	}

	if _, err := s.GetUser(ctx, 99); err == nil {
		t.Error("GetUser on a missing id should fail")
	}
}

// TestStructuralSatisfaction is the "no declaration anywhere" claim, checked.
// Neither Loud nor Polite mentions Greeter, and both are Greeters.
func TestStructuralSatisfaction(t *testing.T) {
	got := greetAll([]Greeter{Loud("hello"), Polite{Name: "Ana"}})

	want := []string{"HELLO!", "Good day, Ana."}
	if !slices.Equal(got, want) {
		t.Errorf("greetAll() = %v, want %v", got, want)
	}

	// A third implementation, declared inside a test, joins the set for free.
	var _ Greeter = quietGreeter{}
	got = greetAll([]Greeter{quietGreeter{}})
	if want := []string{"..."}; !slices.Equal(got, want) {
		t.Errorf("greetAll(quietGreeter) = %v, want %v", got, want)
	}
}

type quietGreeter struct{}

func (quietGreeter) Greet() string { return "..." }

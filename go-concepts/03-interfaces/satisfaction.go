// Package main is lesson 03 of go-concepts: interfaces.
//
// Go interfaces are satisfied structurally. A type never declares that it
// implements anything; it simply has the methods, and the compiler checks at
// every assignment. Two consequences follow, and they shape how Go packages are
// designed:
//
//  1. The interface belongs to the CONSUMER. The package that needs a
//     behaviour declares the shape it needs. Implementations do not import it.
//  2. Interfaces stay small, because a small one is satisfied by more types.
//
// The proverb is "accept interfaces, return structs": take the narrowest thing
// you can at a boundary, hand back the concrete type so callers keep every
// method.
package main

import (
	"context"
	"fmt"
	"strings"
)

// User is the domain type the store below moves around.
type User struct {
	ID    int64
	Name  string
	Email string
}

// UserStore is a consumer-defined interface. It lists the two methods the
// handler actually calls, not everything a database can do. A Postgres client,
// an in-memory fake, and a caching decorator all satisfy it without importing
// this file's package or knowing the interface exists.
//
// Compare with the alternative: a `Database` interface with twenty methods
// declared next to the Postgres implementation. Every fake would then need
// twenty stub methods to compile, and the handler's real dependency, which is
// two methods, would be invisible.
type UserStore interface {
	GetUser(ctx context.Context, id int64) (*User, error)
	SaveUser(ctx context.Context, u *User) error
}

// MemoryStore is a complete UserStore in twenty lines, which is the entire
// point of keeping the interface at two methods. This is what a test uses.
type MemoryStore struct {
	users map[int64]*User
}

// NewMemoryStore returns a ready store. It returns the concrete *MemoryStore
// rather than UserStore, following "return structs": a caller that wants the
// interface can assign it, and a caller that wants Len can still reach it.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{users: make(map[int64]*User)}
}

// GetUser implements UserStore.
func (s *MemoryStore) GetUser(_ context.Context, id int64) (*User, error) {
	u, ok := s.users[id]
	if !ok {
		return nil, fmt.Errorf("user %d not found", id)
	}
	return u, nil
}

// SaveUser implements UserStore.
func (s *MemoryStore) SaveUser(_ context.Context, u *User) error {
	if u.Name == "" {
		return fmt.Errorf("user %d: name is required", u.ID)
	}
	s.users[u.ID] = u
	return nil
}

// Len is not part of UserStore. It exists to show what "return structs" buys:
// NewMemoryStore's caller keeps this method, and would have lost it if the
// constructor had been declared to return UserStore.
func (s *MemoryStore) Len() int { return len(s.users) }

// Compile-time interface assertion. Costs nothing at runtime, and moves the
// failure from "some call site three packages away" to this line. Put one of
// these next to every type that is meant to satisfy an interface.
var _ UserStore = (*MemoryStore)(nil)

// Handler is the consumer. It depends on the interface, so main can pass a
// Postgres store and a test can pass a MemoryStore, with no build tag, no
// dependency injection framework, and no mocking library.
type Handler struct {
	store UserStore
}

// NewHandler accepts the interface.
func NewHandler(store UserStore) *Handler { return &Handler{store: store} }

// Register is the behaviour under test in handler_test-style code elsewhere.
func (h *Handler) Register(ctx context.Context, id int64, name, email string) error {
	if !strings.Contains(email, "@") {
		return fmt.Errorf("register %q: %q is not an email address", name, email)
	}
	return h.store.SaveUser(ctx, &User{ID: id, Name: name, Email: email})
}

// Greeter and Loud below show structural satisfaction with no coordination at
// all: Loud was not written with Greeter in mind, and satisfies it anyway.
type Greeter interface {
	Greet() string
}

// Loud is an ordinary string type that happens to have a Greet method.
type Loud string

// Greet makes Loud a Greeter without Loud ever mentioning the interface.
func (l Loud) Greet() string { return strings.ToUpper(string(l)) + "!" }

// Polite is a struct that satisfies the same interface a different way.
type Polite struct{ Name string }

// Greet makes Polite a Greeter.
func (p Polite) Greet() string { return "Good day, " + p.Name + "." }

// greetAll takes the interface, so it works on both types and on anything
// added later, including types in packages that do not exist yet.
func greetAll(gs []Greeter) []string {
	out := make([]string, 0, len(gs))
	for _, g := range gs {
		out = append(out, g.Greet())
	}
	return out
}

// demoSatisfaction prints consumer-defined interfaces and structural matching.
func demoSatisfaction() {
	ctx := context.Background()
	store := NewMemoryStore() // concrete type, so Len is available
	h := NewHandler(store)    // interface, so a fake would drop in here

	if err := h.Register(ctx, 1, "Ana", "ana@example.com"); err != nil {
		fmt.Printf("  unexpected: %v\n", err)
	}
	if err := h.Register(ctx, 2, "Bo", "not-an-email"); err != nil {
		fmt.Printf("  rejected before it reached the store: %v\n", err)
	}
	fmt.Printf("  store holds %d user(s)\n", store.Len())

	u, err := store.GetUser(ctx, 1)
	fmt.Printf("  GetUser(1) -> %+v, err=%v\n", u, err)
	_, err = store.GetUser(ctx, 99)
	fmt.Printf("  GetUser(99) -> err=%v\n", err)

	fmt.Printf("  greetAll over two unrelated types: %v\n",
		greetAll([]Greeter{Loud("hello"), Polite{Name: "Ana"}}))
}

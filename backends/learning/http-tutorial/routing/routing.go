// Package routing covers http.ServeMux, which since Go 1.22 does most of what a
// third-party router used to be needed for.
//
// # What changed in Go 1.22
//
// Before 1.22, ServeMux matched prefixes and nothing else. No method matching, no path
// variables, so every non-trivial service imported gorilla/mux or chi or gin, and the
// question "which router should I use" was the first decision in every Go HTTP tutorial.
//
// Since 1.22 a pattern can carry a method and wildcards:
//
//	"GET /items/{id}"          a method and one path variable
//	"POST /items/"             a subtree, because of the trailing slash
//	"GET /files/{path...}"     a multi-segment wildcard, which must be last
//	"GET /items/{$}"           EXACTLY /items/, not the subtree below it
//	"example.com/items"        host-specific
//
// That covers most services. What it still does not do: regular expressions, per-route
// middleware, route groups, or reverse URL generation. chi is 1,000 lines and adds all four,
// and this module uses chi's MIDDLEWARE with the stdlib's router, which is a combination
// worth knowing about.
//
// # Precedence is by specificity, not by registration order
//
// This is the rule that surprises people coming from Express or Flask, where the first
// matching route wins and order is everything.
//
//	Two patterns conflict when they match a common request and neither is more specific
//	than the other. Registering conflicting patterns PANICS at registration time.
//
// "More specific" means: pattern A is more specific than B when A matches a strict subset of
// what B matches. So "GET /items/latest" beats "GET /items/{id}" regardless of which was
// registered first, because every request the first matches, the second also matches.
//
// The panic is the good part. A router that silently picks one of two overlapping routes
// produces a bug that only shows up for certain URLs; a router that refuses to start makes
// it impossible to deploy. See TestConflictingPatternsPanic.
package routing

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
)

// Item is the thing this router serves, kept trivial so the routing is the subject.
type Item struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Owner string `json:"owner,omitempty"`
}

// Store is an in-memory item store, safe for concurrent use.
//
// It has to be. net/http serves every request on its own goroutine, so a store behind
// a server is read and written concurrently by default. The first version said "not
// safe for concurrent use" while cmd/server served it anyway, and
// TestStoreIsSafeBehindAServer found the race. A RWMutex, because reads outnumber
// writes; go-concepts/09 covers the choice.
type Store struct {
	mu    sync.RWMutex
	items map[int]Item
	next  int
}

// NewStore returns a store with a few items in it.
func NewStore() *Store {
	s := &Store{items: make(map[int]Item), next: 1}

	for _, name := range []string{"hammer", "nails", "saw"} {
		s.Add(name)
	}

	return s
}

// Add stores a new item and returns it.
func (s *Store) Add(name string) Item {
	s.mu.Lock()
	defer s.mu.Unlock()

	item := Item{ID: s.next, Name: name}
	s.items[item.ID] = item
	s.next++
	return item
}

// Get returns an item by ID.
func (s *Store) Get(id int) (Item, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	item, ok := s.items[id]
	return item, ok
}

// All returns every item, in ID order.
func (s *Store) All() []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Item, 0, len(s.items))
	for id := 1; id < s.next; id++ {
		if item, ok := s.items[id]; ok {
			out = append(out, item)
		}
	}
	return out
}

// Len returns the number of items.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.items)
}

// NewMux builds a router demonstrating every pattern form ServeMux supports.
//
// Nothing here needs a third-party router, and that is the point of the package.
func NewMux(store *Store) *http.ServeMux {
	mux := http.NewServeMux()

	// A method and an exact path. Without the method, this pattern would also match POST,
	// DELETE and everything else, which is the most common Go HTTP bug there is.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// A LITERAL segment, registered alongside a wildcard below. It wins for /items/latest
	// because it is more specific, whatever order they were registered in.
	mux.HandleFunc("GET /items/latest", func(w http.ResponseWriter, _ *http.Request) {
		all := store.All()
		if len(all) == 0 {
			http.Error(w, "no items", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, all[len(all)-1])
	})

	// A path variable, read with r.PathValue. No third-party router, no context key, no
	// type assertion.
	mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			http.Error(w, "id must be a number", http.StatusBadRequest)
			return
		}

		item, ok := store.Get(id)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		writeJSON(w, http.StatusOK, item)
	})

	// {$} means EXACTLY this path, so "GET /items/{$}" matches /items/ and nothing below
	// it. Without it, "GET /items/" is a subtree pattern matching /items/anything/at/all,
	// which is almost never what you want for a collection endpoint.
	mux.HandleFunc("GET /items/{$}", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, store.All())
	})

	mux.HandleFunc("POST /items/{$}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}

		// Capped, because an unbounded Decode is the pattern request.DecodeJSON warns
		// about: a client can make the server allocate whatever it sends. The routing
		// lesson keeps the handler short, but not at that price.
		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if body.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}

		writeJSON(w, http.StatusCreated, store.Add(body.Name))
	})

	// A multi-segment wildcard, which must be the last segment of the pattern. This is how
	// a file server or a proxy route is expressed.
	mux.HandleFunc("GET /files/{path...}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"path": r.PathValue("path")})
	})

	// A subtree WITHOUT {$}: matches /api/ and everything under it. Useful for mounting a
	// sub-router, and a trap when you meant an exact path.
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"matched": "subtree", "path": r.URL.Path})
	})

	// Two variables in one pattern.
	mux.HandleFunc("GET /users/{user}/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"user": r.PathValue("user"),
			"id":   r.PathValue("id"),
		})
	})

	return mux
}

// writeJSON is the three lines every Go service writes for itself, because the stdlib has no
// JSON response helper.
//
// The header has to be set BEFORE WriteHeader, and WriteHeader before Write. Getting that
// order wrong does not error: the header is silently dropped, because the response has
// already been committed. See response/ for the whole ordering problem.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	// The error is deliberately ignored: by the time encoding fails the status line is
	// already sent, so there is nothing useful to do. response/ has the version that logs
	// it.
	_ = json.NewEncoder(w).Encode(v)
}

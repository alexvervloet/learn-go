package queue

import (
	"fmt"
	"strings"
)

// Matchmaking
// ===========
//
// What a queue is actually for, and a reminder that a real one is rarely a
// plain FIFO. Players join the back; the matchmaker pairs the first player with
// the first COMPATIBLE player behind them, which may not be the second in line.
//
// That is why the queue has SearchAndRemove: a player at the front with nobody
// compatible must wait while pairs are matched around them, rather than
// blocking the whole queue.

// Player is someone waiting for a match.
type Player struct {
	Name string
	Rank string // "bronze", "silver", "gold"
}

// String makes Player print readably.
func (p Player) String() string { return fmt.Sprintf("%s(%s)", p.Name, p.Rank) }

// Match is a pair of compatible players.
type Match struct {
	A, B Player
}

// String renders a match.
func (m Match) String() string { return m.A.String() + " vs " + m.B.String() }

// compatible reports whether two players can be matched. Same rank here;
// a real system would use a numeric rating with a widening tolerance the
// longer someone waits.
func compatible(a, b Player) bool { return a.Rank == b.Rank }

// Matchmake pops the front player and pairs them with the first compatible
// player behind them, removing both from the queue.
//
// It reports:
//
//	the match, if one was made
//	whether one was made
//
// When the front player has no compatible partner they are put BACK at the
// front, not the back: being unmatched is not their fault and sending them to
// the back would starve them forever.
//
// That requeue-at-the-front is why this takes the queue by pointer and mutates
// it, rather than being a pure function.
func Matchmake(q *Queue[Player]) (Match, bool) {
	front, ok := q.Pop()
	if !ok {
		return Match{}, false
	}

	// Look for anyone compatible among those still waiting.
	for _, candidate := range q.Slice() {
		if !compatible(front, candidate) {
			continue
		}

		// Found one. Remove them from wherever they were.
		partner, removed := q.SearchAndRemove(candidate, func(a, b Player) bool {
			return a.Name == b.Name
		})
		if !removed {
			// Cannot happen: the candidate came from Slice() and nothing has
			// mutated the queue since. Checked because a silent mismatch here
			// would drop a player.
			continue
		}

		return Match{A: front, B: partner}, true
	}

	// Nobody compatible. Put them back at the FRONT so they keep their place.
	q.pushFront(front)
	return Match{}, false
}

// pushFront puts v back at the head, which is what a failed match needs and
// which a FIFO does not normally offer. Unexported: this is a matchmaking
// detail rather than part of the queue's contract.
func (q *Queue[T]) pushFront(v T) {
	if q.count == len(q.items) {
		q.grow()
	}

	// Step head backwards, wrapping.
	q.head = (q.head - 1 + len(q.items)) % len(q.items)
	q.items[q.head] = v
	q.count++
}

// MatchAll drains the queue as far as it can, returning every match made and
// the players left waiting.
//
// The loop has two moving parts. Matchmake handles one attempt and leaves the
// order alone. When it fails, MatchAll rotates the unmatched player to the back
// and tries the next one, because a player nobody can pair with must not block
// the players behind them.
//
// `failures < q.Len()` is the termination condition, and it is the whole
// difficulty of the function: a full pass with no match means nobody left can
// be paired, so stop. Without it a queue holding one bronze and one gold player
// rotates forever.
func MatchAll(q *Queue[Player]) (matches []Match, waiting []Player) {
	failures := 0

	for q.Len() >= 2 && failures < q.Len() {
		match, ok := Matchmake(q)
		if ok {
			matches = append(matches, match)
			failures = 0 // progress, so give everyone another chance
			continue
		}

		front, _ := q.Pop() // Matchmake put them back, so this cannot fail
		q.Push(front)
		failures++
	}

	return matches, q.Slice()
}

// Describe renders a matchmaking result, for the example.
func Describe(matches []Match, waiting []Player) string {
	var sb strings.Builder

	for _, m := range matches {
		sb.WriteString(m.String())
		sb.WriteString("\n")
	}

	if len(waiting) == 0 {
		sb.WriteString("nobody waiting")
		return sb.String()
	}

	names := make([]string, 0, len(waiting))
	for _, p := range waiting {
		names = append(names, p.String())
	}
	sb.WriteString("waiting: " + strings.Join(names, ", "))
	return sb.String()
}

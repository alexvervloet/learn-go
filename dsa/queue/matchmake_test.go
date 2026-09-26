package queue

import (
	"slices"
	"testing"
)

func players(t *testing.T, spec ...Player) *Queue[Player] {
	t.Helper()
	q := New[Player](len(spec))
	for _, p := range spec {
		q.Push(p)
	}
	return q
}

func names(ps []Player) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

func TestMatchmakeSkipsPastAnIncompatiblePlayer(t *testing.T) {
	q := players(t,
		Player{Name: "Ada", Rank: "gold"},
		Player{Name: "Bo", Rank: "bronze"},
		Player{Name: "Cy", Rank: "gold"},
	)

	match, ok := Matchmake(q)
	if !ok {
		t.Fatal("expected a match")
	}
	if match.A.Name != "Ada" || match.B.Name != "Cy" {
		t.Errorf("matched %s vs %s, want Ada vs Cy", match.A.Name, match.B.Name)
	}
	if got, want := names(q.Slice()), []string{"Bo"}; !slices.Equal(got, want) {
		t.Errorf("still waiting: %v, want %v", got, want)
	}
}

// TestUnmatchedFrontKeepsItsPlace is the fairness property. A player nobody can
// pair with must not be sent to the back by Matchmake, or on a busy server they
// would never reach the front again.
func TestUnmatchedFrontKeepsItsPlace(t *testing.T) {
	q := players(t,
		Player{Name: "Ada", Rank: "gold"},
		Player{Name: "Bo", Rank: "bronze"},
		Player{Name: "Cy", Rank: "silver"},
	)

	if _, ok := Matchmake(q); ok {
		t.Fatal("no two players share a rank, so there should be no match")
	}
	if got, want := names(q.Slice()), []string{"Ada", "Bo", "Cy"}; !slices.Equal(got, want) {
		t.Errorf("queue = %v, want the original order %v", got, want)
	}
}

func TestMatchmakeOnAnEmptyQueue(t *testing.T) {
	var q Queue[Player]
	if _, ok := Matchmake(&q); ok {
		t.Error("an empty queue should not produce a match")
	}
}

func TestMatchmakeWithOnePlayer(t *testing.T) {
	q := players(t, Player{Name: "Ada", Rank: "gold"})

	if _, ok := Matchmake(q); ok {
		t.Error("one player cannot be matched")
	}
	if q.Len() != 1 {
		t.Errorf("the lone player was dropped: Len() = %d, want 1", q.Len())
	}
}

func TestMatchAll(t *testing.T) {
	tests := []struct {
		name        string
		queue       []Player
		wantMatches []string // "A vs B" in order
		wantWaiting int
	}{
		{
			name: "everyone pairs",
			queue: []Player{
				{Name: "Ada", Rank: "gold"}, {Name: "Bo", Rank: "gold"},
				{Name: "Cy", Rank: "bronze"}, {Name: "Di", Rank: "bronze"},
			},
			wantMatches: []string{"Ada(gold) vs Bo(gold)", "Cy(bronze) vs Di(bronze)"},
		},
		{
			name: "one left over",
			queue: []Player{
				{Name: "Ada", Rank: "gold"}, {Name: "Bo", Rank: "bronze"},
				{Name: "Cy", Rank: "gold"},
			},
			wantMatches: []string{"Ada(gold) vs Cy(gold)"},
			wantWaiting: 1,
		},
		{
			name: "nobody pairs",
			queue: []Player{
				{Name: "Ada", Rank: "gold"}, {Name: "Bo", Rank: "bronze"},
			},
			wantWaiting: 2,
		},
		{
			name:        "empty",
			queue:       nil,
			wantWaiting: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := players(t, tt.queue...)

			matches, waiting := MatchAll(q)

			got := make([]string, 0, len(matches))
			for _, m := range matches {
				got = append(got, m.String())
			}
			if !slices.Equal(got, tt.wantMatches) {
				t.Errorf("matches = %v, want %v", got, tt.wantMatches)
			}
			if len(waiting) != tt.wantWaiting {
				t.Errorf("waiting = %v, want %d players", names(waiting), tt.wantWaiting)
			}

			// Nobody may be lost or duplicated.
			if total := len(matches)*2 + len(waiting); total != len(tt.queue) {
				t.Errorf("accounted for %d players, started with %d", total, len(tt.queue))
			}
		})
	}
}

// TestMatchAllTerminates is the reason the failures counter exists. Without it
// this queue rotates forever, and the test would hang rather than fail, which
// is why it is worth its own case.
func TestMatchAllTerminates(t *testing.T) {
	q := players(t,
		Player{Name: "A", Rank: "gold"},
		Player{Name: "B", Rank: "silver"},
		Player{Name: "C", Rank: "bronze"},
	)

	matches, waiting := MatchAll(q)

	if len(matches) != 0 {
		t.Errorf("matched %v, expected none", matches)
	}
	if len(waiting) != 3 {
		t.Errorf("waiting = %d, want 3", len(waiting))
	}
}

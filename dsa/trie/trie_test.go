package trie

import (
	"slices"
	"testing"
)

func build(words ...string) *Trie {
	t := New()
	for _, w := range words {
		t.Insert(w)
	}
	return t
}

func TestZeroValueIsUsable(t *testing.T) {
	var tr Trie // no New

	if tr.Len() != 0 || tr.Contains("go") || tr.HasPrefix("g") {
		t.Error("an empty trie should be empty")
	}

	if !tr.Insert("go") {
		t.Error("Insert reported the word was already there")
	}
	if !tr.Contains("go") {
		t.Error("the word did not survive insertion")
	}
}

func TestInsertReportsNewness(t *testing.T) {
	tr := New()

	if !tr.Insert("go") {
		t.Error("first Insert should report true")
	}
	if tr.Insert("go") {
		t.Error("second Insert should report false")
	}
	if tr.Len() != 1 {
		t.Errorf("Len() = %d after a duplicate insert, want 1", tr.Len())
	}
}

// TestPrefixIsNotAWord is the distinction the terminal flag exists for.
// Inserting "golang" creates a node at "go", and "go" is not a word until
// someone says so.
func TestPrefixIsNotAWord(t *testing.T) {
	tr := build("golang")

	if tr.Contains("go") {
		t.Error(`Contains("go") is true, but only "golang" was inserted`)
	}
	if !tr.HasPrefix("go") {
		t.Error(`HasPrefix("go") is false, but "golang" starts with it`)
	}

	tr.Insert("go")

	if !tr.Contains("go") || !tr.Contains("golang") {
		t.Error("inserting the prefix broke one of the two words")
	}
	if tr.Len() != 2 {
		t.Errorf("Len() = %d, want 2", tr.Len())
	}
}

func TestCount(t *testing.T) {
	tr := build("go", "goal", "golang", "to", "top")

	tests := []struct {
		prefix string
		want   int
	}{
		{"", 5},
		{"g", 3},
		{"go", 3},
		{"goa", 1},
		{"gol", 1},
		{"t", 2},
		{"z", 0},
		{"golanger", 0},
	}

	for _, tt := range tests {
		if got := tr.Count(tt.prefix); got != tt.want {
			t.Errorf("Count(%q) = %d, want %d", tt.prefix, got, tt.want)
		}
	}
}

func TestComplete(t *testing.T) {
	tr := build("go", "goal", "golang", "gopher", "to", "top")

	tests := []struct {
		prefix string
		want   []string
	}{
		{"go", []string{"go", "goal", "golang", "gopher"}},
		{"gol", []string{"golang"}},
		{"t", []string{"to", "top"}},
		{"", []string{"go", "goal", "golang", "gopher", "to", "top"}},
		{"zz", nil},
	}

	for _, tt := range tests {
		got := tr.Complete(tt.prefix)
		if !slices.Equal(got, tt.want) {
			t.Errorf("Complete(%q) = %v, want %v", tt.prefix, got, tt.want)
		}
	}
}

// TestWordsWithPrefixStopsEarly holds up the range-over-func contract through a
// recursive walk, which is where it is easiest to break: every level has to
// propagate the false from yield, or the walk finishes anyway and panics on the
// next yield.
func TestWordsWithPrefixStopsEarly(t *testing.T) {
	tr := build("a", "ab", "abc", "abcd", "abcde")

	count := 0
	for range tr.WordsWithPrefix("a") {
		count++
		if count == 2 {
			break
		}
	}

	if count != 2 {
		t.Errorf("visited %d words after breaking at 2", count)
	}
}

func TestDelete(t *testing.T) {
	tests := []struct {
		name    string
		insert  []string
		delete  string
		found   bool
		wantAll []string
	}{
		{
			name:    "a leaf",
			insert:  []string{"go", "goal"},
			delete:  "goal",
			found:   true,
			wantAll: []string{"go"},
		},
		{
			name:    "a prefix of another word",
			insert:  []string{"go", "golang"},
			delete:  "go",
			found:   true,
			wantAll: []string{"golang"},
		},
		{
			name:    "one of two branches",
			insert:  []string{"goal", "gopher"},
			delete:  "goal",
			found:   true,
			wantAll: []string{"gopher"},
		},
		{
			name:    "absent",
			insert:  []string{"go"},
			delete:  "goal",
			found:   false,
			wantAll: []string{"go"},
		},
		{
			name:    "a prefix that is not a word",
			insert:  []string{"golang"},
			delete:  "go",
			found:   false,
			wantAll: []string{"golang"},
		},
		{
			name:    "the last word",
			insert:  []string{"go"},
			delete:  "go",
			found:   true,
			wantAll: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := build(tt.insert...)

			if got := tr.Delete(tt.delete); got != tt.found {
				t.Errorf("Delete(%q) = %v, want %v", tt.delete, got, tt.found)
			}
			if got := tr.Words(); !slices.Equal(got, tt.wantAll) {
				t.Errorf("Words() = %v, want %v", got, tt.wantAll)
			}
			if tr.Len() != len(tt.wantAll) {
				t.Errorf("Len() = %d, want %d", tr.Len(), len(tt.wantAll))
			}
		})
	}
}

// countNodes walks the whole tree, which is the only way to see whether Delete
// actually unlinked anything. Len() would be happy either way.
func countNodes(n *node) int {
	if n == nil {
		return 0
	}
	total := 1
	for _, c := range n.children {
		total += countNodes(c)
	}
	return total
}

// TestDeletePrunesDeadNodes is the half of Delete that no behavioural test
// catches. A trie that only clears the terminal flag answers every query
// correctly and grows without bound.
func TestDeletePrunesDeadNodes(t *testing.T) {
	tr := build("go")
	before := countNodes(tr.root) // root + g + o

	tr.Insert("golang")
	grown := countNodes(tr.root)
	if grown <= before {
		t.Fatalf("inserting a longer word did not add nodes: %d then %d", before, grown)
	}

	tr.Delete("golang")

	if got := countNodes(tr.root); got != before {
		t.Errorf("after deleting the longer word the trie has %d nodes, want %d back", got, before)
	}
	if !tr.Contains("go") {
		t.Error("the prune took the shorter word with it")
	}
}

// TestDeleteKeepsSharedNodes: deleting one of two words that share a prefix must
// not unlink the shared part.
func TestDeleteKeepsSharedNodes(t *testing.T) {
	tr := build("goal", "gopher")

	tr.Delete("goal")

	if !tr.Contains("gopher") {
		t.Fatal("deleting a sibling removed the shared prefix")
	}
	if !tr.HasPrefix("go") {
		t.Error(`HasPrefix("go") is false after deleting a sibling`)
	}
	if tr.Count("go") != 1 {
		t.Errorf("Count(\"go\") = %d, want 1", tr.Count("go"))
	}
}

func TestDeleteThenReinsert(t *testing.T) {
	tr := build("go", "golang")

	tr.Delete("golang")
	if !tr.Insert("golang") {
		t.Error("Insert after Delete reported the word was still there")
	}
	if got, want := tr.Words(), []string{"go", "golang"}; !slices.Equal(got, want) {
		t.Errorf("Words() = %v, want %v", got, want)
	}
}

func TestMatch(t *testing.T) {
	tr := build("cat", "cot", "cut", "cart", "dog")

	tests := []struct {
		pattern string
		want    bool
	}{
		{"cat", true},
		{"c.t", true},
		{"c..t", true},   // cart
		{"...", true},    // cat, cot, cut, dog
		{"....", true},   // cart
		{".....", false}, // nothing is five long
		{"c.g", false},
		{"...s", false},
		{"", false}, // the empty string was never inserted
	}

	for _, tt := range tests {
		if got := tr.Match(tt.pattern); got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.pattern, got, tt.want)
		}
	}
}

func TestLongestPrefixOf(t *testing.T) {
	// The routing table shape this method exists for.
	routes := build("/", "/users/", "/users/admin/", "/static/")

	tests := []struct {
		path string
		want string
	}{
		{"/users/42/posts", "/users/"},
		{"/users/admin/settings", "/users/admin/"},
		{"/users/", "/users/"},
		{"/static/css/main.css", "/static/"},
		{"/anything", "/"},
	}

	for _, tt := range tests {
		got, ok := routes.LongestPrefixOf(tt.path)
		if !ok {
			t.Errorf("LongestPrefixOf(%q) found nothing", tt.path)
			continue
		}
		if got != tt.want {
			t.Errorf("LongestPrefixOf(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}

	// The empty path matches nothing, because "" was never registered. "/" is a
	// word made of one character, not the empty word.
	if _, ok := routes.LongestPrefixOf(""); ok {
		t.Error(`LongestPrefixOf("") matched, but "" was never inserted`)
	}

	// With no catch-all registered, an unmatched path finds nothing.
	noRoot := build("/users/")
	if _, ok := noRoot.LongestPrefixOf("/other"); ok {
		t.Error("expected no match without a registered root")
	}
}

// TestUnicode: the trie is keyed on runes, so a multi-byte character is one node
// and every prefix operation works. Keyed on bytes it would split a character
// across two nodes, which happens to work for Contains and breaks Complete.
func TestUnicode(t *testing.T) {
	tr := build("naïve", "naïveté", "naive")

	if !tr.Contains("naïve") {
		t.Error("lost a word with a multi-byte character")
	}
	if got, want := tr.Complete("naï"), []string{"naïve", "naïveté"}; !slices.Equal(got, want) {
		t.Errorf("Complete(\"naï\") = %v, want %v", got, want)
	}
	if tr.Count("na") != 3 {
		t.Errorf("Count(\"na\") = %d, want 3", tr.Count("na"))
	}
}

func TestEmptyStringIsAWord(t *testing.T) {
	tr := New()
	tr.Insert("")

	if !tr.Contains("") {
		t.Error("the empty string did not survive insertion")
	}
	if tr.Len() != 1 {
		t.Errorf("Len() = %d, want 1", tr.Len())
	}

	tr.Insert("go")
	if got, want := tr.Words(), []string{"", "go"}; !slices.Equal(got, want) {
		t.Errorf("Words() = %v, want %v", got, want)
	}

	if !tr.Delete("") || tr.Contains("") {
		t.Error("the empty string could not be deleted")
	}
	if !tr.Contains("go") {
		t.Error("deleting the empty string took another word with it")
	}
}

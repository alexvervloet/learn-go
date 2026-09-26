package trie

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

func TestReplaceWords(t *testing.T) {
	tests := []struct {
		name     string
		sentence string
		roots    []string
		want     string
	}{
		{
			name:     "classic",
			sentence: "the cattle was rattled by the battery",
			roots:    []string{"cat", "bat", "rat"},
			want:     "the cat was rat by the bat",
		},
		{
			name:     "shortest root wins",
			sentence: "catastrophe",
			roots:    []string{"catas", "cat", "ca"},
			want:     "ca",
		},
		{
			name:     "no match leaves the word alone",
			sentence: "hello world",
			roots:    []string{"cat"},
			want:     "hello world",
		},
		{
			name:     "a root equal to the word",
			sentence: "cat",
			roots:    []string{"cat"},
			want:     "cat",
		},
		{name: "no roots", sentence: "a b", roots: nil, want: "a b"},
		{name: "empty sentence", sentence: "", roots: []string{"a"}, want: ""},
		{
			name:     "empty roots are ignored",
			sentence: "hello",
			roots:    []string{""},
			want:     "hello",
		},
		{
			name:     "multi-byte",
			sentence: "naïveté",
			roots:    []string{"naï"},
			want:     "naï",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ReplaceWords(tt.sentence, tt.roots); got != tt.want {
				t.Errorf("ReplaceWords(%q, %v) = %q, want %q", tt.sentence, tt.roots, got, tt.want)
			}
		})
	}
}

// TestReplaceWordsMatchesBruteForce: "shortest matching root" is the part to get right, and
// the obvious version tries every root and keeps the shortest match.
func TestReplaceWordsMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))

	brute := func(sentence string, roots []string) string {
		words := strings.Fields(sentence)

		for i, w := range words {
			best := ""
			for _, root := range roots {
				if root == "" || !strings.HasPrefix(w, root) {
					continue
				}
				if best == "" || len(root) < len(best) {
					best = root
				}
			}
			if best != "" {
				words[i] = best
			}
		}

		return strings.Join(words, " ")
	}

	for range 3000 {
		var words []string
		for range 1 + r.IntN(4) {
			words = append(words, randomWord(r, 1+r.IntN(5)))
		}
		sentence := strings.Join(words, " ")

		var roots []string
		for range r.IntN(5) {
			roots = append(roots, randomWord(r, 1+r.IntN(3)))
		}

		got := ReplaceWords(sentence, roots)
		want := brute(sentence, slices.Clone(roots))

		if got != want {
			t.Fatalf("ReplaceWords(%q, %v) = %q, want %q", sentence, roots, got, want)
		}
	}
}

func randomWord(r *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + r.IntN(3))
	}
	return string(b)
}

func TestLongestCommonPrefix(t *testing.T) {
	tests := []struct {
		words []string
		want  string
	}{
		{[]string{"flower", "flow", "flight"}, "fl"},
		{[]string{"dog", "racecar", "car"}, ""},
		{[]string{"same", "same"}, "same"},
		{[]string{"one"}, "one"},
		{nil, ""},
		{[]string{""}, ""},
		{[]string{"a", ""}, ""},
		{[]string{"prefix", "prefixed", "prefixes"}, "prefix"},
		{[]string{"naïve", "naïveté"}, "naïve"},
	}

	for _, tt := range tests {
		if got := LongestCommonPrefix(tt.words); got != tt.want {
			t.Errorf("LongestCommonPrefix(%v) = %q, want %q", tt.words, got, tt.want)
		}
	}
}

func TestLongestCommonPrefixMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))

	for range 5000 {
		var words []string
		for range 1 + r.IntN(5) {
			words = append(words, randomWord(r, r.IntN(6)))
		}

		got := LongestCommonPrefix(words)

		// Every word must start with it.
		for _, w := range words {
			if !strings.HasPrefix(w, got) {
				t.Fatalf("LongestCommonPrefix(%v) = %q, which %q does not start with",
					words, got, w)
			}
		}

		// And one character more must fail for at least one word.
		if len(got) < len(words[0]) {
			longer := words[0][:len(got)+1]
			all := true
			for _, w := range words {
				if !strings.HasPrefix(w, longer) {
					all = false
				}
			}
			if all {
				t.Fatalf("LongestCommonPrefix(%v) = %q, but %q also works", words, got, longer)
			}
		}
	}
}

func TestAutocomplete(t *testing.T) {
	a := NewAutocompleter()
	a.RecordMany([]string{
		"go", "go", "go",
		"golang", "golang",
		"goal",
		"gopher",
		"rust",
	})

	if a.Len() != 5 {
		t.Errorf("Len() = %d, want 5", a.Len())
	}

	got := a.Suggest("go", 3)

	want := []Suggestion{
		{Word: "go", Count: 3},
		{Word: "golang", Count: 2},
		{Word: "goal", Count: 1}, // ties broken alphabetically, so goal before gopher
	}
	if !slices.Equal(got, want) {
		t.Errorf("Suggest(\"go\", 3) = %v, want %v", got, want)
	}

	// The limit is respected, and asking for more than exists is not an error.
	if got := a.Suggest("go", 1); len(got) != 1 || got[0].Word != "go" {
		t.Errorf("Suggest(\"go\", 1) = %v", got)
	}
	if got := a.Suggest("go", 99); len(got) != 4 {
		t.Errorf("Suggest(\"go\", 99) returned %d, want 4", len(got))
	}
	if got := a.Suggest("go", 0); got != nil {
		t.Errorf("Suggest with limit 0 = %v, want nil", got)
	}
	if got := a.Suggest("zz", 5); got != nil {
		t.Errorf("Suggest for an unknown prefix = %v, want nil", got)
	}

	// The empty prefix matches everything.
	if got := a.Suggest("", 99); len(got) != 5 {
		t.Errorf("Suggest(\"\", 99) returned %d, want 5", len(got))
	}
}

// TestAutocompleteIsDeterministic: the trie iterates in map order, so without the tie-break
// the ranking would change between runs.
func TestAutocompleteIsDeterministic(t *testing.T) {
	a := NewAutocompleter()
	for i := range 20 {
		// Twenty distinct words all with count 1, so every comparison is a tie.
		a.Record("word" + string(rune('a'+i)))
	}

	first := a.Suggest("word", 5)

	for range 200 {
		if got := a.Suggest("word", 5); !slices.Equal(got, first) {
			t.Fatalf("two calls disagree: %v and %v", first, got)
		}
	}

	// And the tie-break is alphabetical.
	for i := 1; i < len(first); i++ {
		if first[i-1].Word > first[i].Word {
			t.Errorf("ties are not alphabetical: %v", first)
		}
	}
}

func TestAutocompleteEmpty(t *testing.T) {
	a := NewAutocompleter()

	if a.Len() != 0 {
		t.Errorf("Len() = %d, want 0", a.Len())
	}
	if got := a.Suggest("a", 5); got != nil {
		t.Errorf("Suggest on an empty autocompleter = %v", got)
	}

	// An empty word is ignored rather than recorded.
	a.Record("")
	if a.Len() != 0 {
		t.Errorf("recording an empty word changed Len() to %d", a.Len())
	}
}

func grid(rows ...string) [][]rune {
	g := make([][]rune, len(rows))
	for i, row := range rows {
		g[i] = []rune(row)
	}
	return g
}

func TestWordSearchII(t *testing.T) {
	tests := []struct {
		name string
		rows []string
		dict []string
		want []string
	}{
		{
			name: "classic",
			rows: []string{"oaan", "etae", "ihkr", "iflv"},
			dict: []string{"oath", "pea", "eat", "rain"},
			want: []string{"eat", "oath"},
		},
		{
			name: "none found",
			rows: []string{"ab", "cd"},
			dict: []string{"xyz"},
			want: nil,
		},
		{
			name: "a cell cannot be reused",
			rows: []string{"ab"},
			dict: []string{"aba"},
			want: nil,
		},
		{
			name: "single cell",
			rows: []string{"a"},
			dict: []string{"a", "aa"},
			want: []string{"a"},
		},
		{
			name: "duplicates in the dictionary",
			rows: []string{"ab", "cd"},
			dict: []string{"ab", "ab", "abcd"},
			want: []string{"ab"},
		},
		{name: "empty grid", rows: nil, dict: []string{"a"}, want: nil},
		{name: "empty dictionary", rows: []string{"ab"}, dict: nil, want: nil},
		{
			name: "empty words are ignored",
			rows: []string{"ab"},
			dict: []string{""},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WordSearchII(grid(tt.rows...), tt.dict)
			if len(got) == 0 {
				got = nil
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("WordSearchII = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestWordSearchIIRestoresTheGrid: the grid doubles as the visited set, so every undo has to
// put the cell back, including on paths that failed.
func TestWordSearchIIRestoresTheGrid(t *testing.T) {
	rows := []string{"oaan", "etae", "ihkr", "iflv"}
	g := grid(rows...)

	WordSearchII(g, []string{"oath", "pea", "eat", "rain", "zzz"})

	for i, row := range rows {
		if string(g[i]) != row {
			t.Errorf("row %d is %q, want %q", i, string(g[i]), row)
		}
	}
}

// TestWordSearchIIMatchesPerWordSearch: the trie version must find exactly the words that a
// separate single-word search finds, one at a time.
func TestWordSearchIIMatchesPerWordSearch(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 7))

	// The single-word search, written out so the test does not depend on another package.
	single := func(g [][]rune, word string) bool {
		target := []rune(word)
		if len(target) == 0 {
			return true
		}

		var walk func(row, col, at int) bool
		walk = func(row, col, at int) bool {
			if row < 0 || row >= len(g) || col < 0 || col >= len(g[row]) {
				return false
			}
			if g[row][col] != target[at] {
				return false
			}
			if at == len(target)-1 {
				return true
			}

			was := g[row][col]
			g[row][col] = 0

			found := walk(row-1, col, at+1) || walk(row+1, col, at+1) ||
				walk(row, col-1, at+1) || walk(row, col+1, at+1)

			g[row][col] = was
			return found
		}

		for row := range g {
			for col := range g[row] {
				if walk(row, col, 0) {
					return true
				}
			}
		}
		return false
	}

	for range 500 {
		rows, cols := 1+r.IntN(4), 1+r.IntN(4)

		g := make([][]rune, rows)
		for i := range g {
			g[i] = make([]rune, cols)
			for j := range g[i] {
				g[i][j] = rune('a' + r.IntN(3))
			}
		}

		var dict []string
		for range 1 + r.IntN(8) {
			dict = append(dict, randomWord(r, 1+r.IntN(4)))
		}

		got := WordSearchII(g, dict)

		var want []string
		seen := map[string]bool{}
		for _, w := range dict {
			if seen[w] || !single(g, w) {
				continue
			}
			seen[w] = true
			want = append(want, w)
		}
		slices.Sort(want)

		if len(got) == 0 {
			got = nil
		}
		if !slices.Equal(got, want) {
			t.Fatalf("grid=%v dict=%v: WordSearchII = %v, per-word search says %v",
				g, dict, got, want)
		}
	}
}

func TestBitTrie(t *testing.T) {
	b := NewBitTrie(32)

	if _, ok := b.MaxXORWith(5); ok {
		t.Error("an empty trie reported a result")
	}
	if b.Len() != 0 {
		t.Errorf("Len() = %d, want 0", b.Len())
	}

	for _, v := range []int{3, 10, 5, 25, 2, 8} {
		b.Insert(v)
	}
	if b.Len() != 6 {
		t.Errorf("Len() = %d, want 6", b.Len())
	}

	// 5 XOR 25 = 28, the largest against 5.
	got, ok := b.MaxXORWith(5)
	if !ok || got != 28 {
		t.Errorf("MaxXORWith(5) = %d, %v; want 28, true", got, ok)
	}
}

func TestMaxXORPair(t *testing.T) {
	tests := []struct {
		values []int
		want   int
		ok     bool
	}{
		{[]int{3, 10, 5, 25, 2, 8}, 28, true}, // 5 XOR 25
		{[]int{0}, 0, false},                  // needs two
		{nil, 0, false},
		{[]int{0, 0}, 0, true},
		{[]int{1, 2}, 3, true},
		{[]int{14, 70, 53, 83, 49, 91, 36, 80, 92, 51, 66, 70}, 127, true},
		{[]int{8, 10, 2}, 10, true},
	}

	for _, tt := range tests {
		got, ok := MaxXORPair(tt.values)

		if ok != tt.ok {
			t.Errorf("MaxXORPair(%v) ok = %v, want %v", tt.values, ok, tt.ok)
			continue
		}
		if ok && got != tt.want {
			t.Errorf("MaxXORPair(%v) = %d, want %d", tt.values, got, tt.want)
		}
	}
}

// TestMaxXORPairMatchesEveryPair: the greedy bit walk is the whole idea and it is easy to get
// subtly wrong, so it is checked against comparing all n^2/2 pairs.
func TestMaxXORPairMatchesEveryPair(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 11))

	for range 5000 {
		n := r.IntN(20)
		values := make([]int, n)
		for i := range values {
			values[i] = r.IntN(1 << 20)
		}

		want, wantOK := 0, n >= 2
		for i := range values {
			for j := i + 1; j < n; j++ {
				want = max(want, values[i]^values[j])
			}
		}

		got, ok := MaxXORPair(values)

		if ok != wantOK {
			t.Fatalf("MaxXORPair(%v) ok = %v, want %v", values, ok, wantOK)
		}
		if ok && got != want {
			t.Fatalf("MaxXORPair(%v) = %d, every pair says %d", values, got, want)
		}
	}
}

// TestMaxXORWithMatchesLinearScan checks the single query, which is the primitive the pair
// search is built from.
func TestMaxXORWithMatchesLinearScan(t *testing.T) {
	r := rand.New(rand.NewPCG(13, 17))

	for range 5000 {
		n := 1 + r.IntN(15)
		values := make([]int, n)
		for i := range values {
			values[i] = r.IntN(1 << 16)
		}

		b := NewBitTrie(20)
		for _, v := range values {
			b.Insert(v)
		}

		x := r.IntN(1 << 16)

		want := 0
		for _, v := range values {
			want = max(want, x^v)
		}

		got, ok := b.MaxXORWith(x)
		if !ok {
			t.Fatalf("MaxXORWith reported empty with %d values", n)
		}
		if got != want {
			t.Fatalf("values=%v x=%d: MaxXORWith = %d, scan says %d", values, x, got, want)
		}
	}
}

// TestWordSearchImplementationsAgree: the node-carrying and prefix-carrying versions share no
// traversal code and must always produce the same set of words.
func TestWordSearchImplementationsAgree(t *testing.T) {
	r := rand.New(rand.NewPCG(19, 23))

	for range 2000 {
		rows, cols := 1+r.IntN(5), 1+r.IntN(5)

		g := make([][]rune, rows)
		for i := range g {
			g[i] = make([]rune, cols)
			for j := range g[i] {
				g[i][j] = rune('a' + r.IntN(3))
			}
		}

		var dict []string
		for range 1 + r.IntN(8) {
			dict = append(dict, randomWord(r, 1+r.IntN(4)))
		}

		byNode := WordSearchII(g, dict)
		byPrefix := WordSearchIIByPrefix(g, dict)

		if !slices.Equal(byNode, byPrefix) {
			t.Fatalf("grid=%v dict=%v: node version %v, prefix version %v",
				g, dict, byNode, byPrefix)
		}
	}
}

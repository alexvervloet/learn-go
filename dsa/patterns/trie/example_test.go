package trie_test

import (
	"fmt"

	ptrie "github.com/alexvervloet/learn-go/dsa/patterns/trie"
)

// Shape one: walk one string down the trie. Each word is walked once and stops at the first
// terminal, so the cost does not depend on how many roots there are.
func ExampleReplaceWords() {
	fmt.Println(ptrie.ReplaceWords(
		"the cattle was rattled by the battery",
		[]string{"cat", "bat", "rat"},
	))

	// The SHORTEST matching root wins, which is why the walk stops at the first terminal
	// rather than remembering the last.
	fmt.Println(ptrie.ReplaceWords("catastrophe", []string{"catas", "cat", "ca"}))

	// Output:
	// the cat was rat by the bat
	// ca
}

// Here to say it is NOT a trie problem. Comparing the first word against the others is the
// same O(total length), allocates nothing, and is four lines.
func ExampleLongestCommonPrefix() {
	fmt.Printf("%q\n", ptrie.LongestCommonPrefix([]string{"flower", "flow", "flight"}))
	fmt.Printf("%q\n", ptrie.LongestCommonPrefix([]string{"dog", "racecar", "car"}))

	// Output:
	// "fl"
	// ""
}

// Shape two: collect a subtree. The cost is the size of the ANSWER, not of the dictionary,
// and the ranking is what makes the problem realistic.
func ExampleAutocompleter() {
	a := ptrie.NewAutocompleter()
	a.RecordMany([]string{
		"go", "go", "go",
		"golang", "golang",
		"goal",
		"gopher",
		"rust",
	})

	for _, s := range a.Suggest("go", 3) {
		fmt.Printf("%-7s %d\n", s.Word, s.Count)
	}

	// Output:
	// go      3
	// golang  2
	// goal    1
}

// Shape three: walk the grid and the trie together, so a path that is not a prefix of any
// word is abandoned at the first character.
func ExampleWordSearchII() {
	grid := [][]rune{
		[]rune("oaan"),
		[]rune("etae"),
		[]rune("ihkr"),
		[]rune("iflv"),
	}

	fmt.Println(ptrie.WordSearchII(grid, []string{"oath", "pea", "eat", "rain"}))

	// The same word spellable by several paths is still one answer.
	fmt.Println(ptrie.WordSearchII([][]rune{[]rune("ab"), []rune("cd")},
		[]string{"ab", "ab", "abcd"}))

	// Output:
	// [eat oath]
	// [ab]
}

// Shape four: a trie over bits rather than letters. Any sequence with a small branching
// factor works, and greedy bit-by-bit choices become a walk down one.
func ExampleBitTrie() {
	b := ptrie.NewBitTrie(32)
	for _, v := range []int{3, 10, 5, 25, 2, 8} {
		b.Insert(v)
	}

	// 5 XOR 25 is 28, the largest available against 5.
	got, _ := b.MaxXORWith(5)
	fmt.Println(got)

	// Output:
	// 28
}

// O(n * bits) against O(n²) for every pair.
func ExampleMaxXORPair() {
	got, ok := ptrie.MaxXORPair([]int{3, 10, 5, 25, 2, 8})
	fmt.Println(got, ok)

	// Fewer than two values has no pair.
	_, ok = ptrie.MaxXORPair([]int{7})
	fmt.Println(ok)

	// Output:
	// 28 true
	// false
}

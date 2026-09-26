package trie_test

import (
	"fmt"

	"github.com/alexvervloet/learn-go/dsa/trie"
)

func ExampleTrie() {
	var t trie.Trie // the zero value is ready to use

	for _, w := range []string{"go", "goal", "golang", "gopher", "to", "top"} {
		t.Insert(w)
	}

	fmt.Println(t.Len(), "words")
	fmt.Println(t.Complete("go"))
	fmt.Println(t.Complete("gol"))

	// Output:
	// 6 words
	// [go goal golang gopher]
	// [golang]
}

// Contains and HasPrefix are different questions, and the terminal flag is what
// separates them. Inserting "golang" creates a node at "go" without making "go"
// a word.
func ExampleTrie_HasPrefix() {
	t := trie.New()
	t.Insert("golang")

	fmt.Println("contains go:  ", t.Contains("go"))
	fmt.Println("has prefix go:", t.HasPrefix("go"))

	// Output:
	// contains go:   false
	// has prefix go: true
}

// Count is O(len(prefix)) because every node carries the number of words in its
// subtree. Scanning a map for the same answer has to look at every key.
func ExampleTrie_Count() {
	t := trie.New()
	for _, w := range []string{"go", "goal", "golang", "to"} {
		t.Insert(w)
	}

	fmt.Println(t.Count(""))
	fmt.Println(t.Count("go"))
	fmt.Println(t.Count("goa"))
	fmt.Println(t.Count("zz"))

	// Output:
	// 4
	// 3
	// 1
	// 0
}

// Deleting a word has to leave the words that share its path alone, in both
// directions: "go" survives deleting "golang", and "golang" survives deleting
// "go".
func ExampleTrie_Delete() {
	t := trie.New()
	t.Insert("go")
	t.Insert("golang")

	fmt.Println(t.Delete("golang"), t.Words())
	fmt.Println(t.Delete("golang"), t.Words()) // already gone

	t.Insert("golang")
	fmt.Println(t.Delete("go"), t.Words())

	// Output:
	// true [go]
	// false [go]
	// true [golang]
}

// Match treats '.' as exactly one character. A hash map cannot answer this at
// any price; the trie only branches over children that exist.
func ExampleTrie_Match() {
	t := trie.New()
	for _, w := range []string{"cat", "cot", "cart", "dog"} {
		t.Insert(w)
	}

	for _, pattern := range []string{"c.t", "c..t", "...", "c.g"} {
		fmt.Printf("%-5s %v\n", pattern, t.Match(pattern))
	}

	// Output:
	// c.t   true
	// c..t  true
	// ...   true
	// c.g   false
}

// LongestPrefixOf is the least famous method here and the most used. It is how a
// router picks a handler, how a phone network picks a carrier, and how an IP
// router picks a route.
func ExampleTrie_LongestPrefixOf() {
	routes := trie.New()
	for _, r := range []string{"/", "/users/", "/users/admin/", "/static/"} {
		routes.Insert(r)
	}

	for _, path := range []string{"/users/42/posts", "/users/admin/settings", "/blog"} {
		match, _ := routes.LongestPrefixOf(path)
		fmt.Printf("%-22s -> %s\n", path, match)
	}

	// Output:
	// /users/42/posts        -> /users/
	// /users/admin/settings  -> /users/admin/
	// /blog                  -> /
}

// WordsWithPrefix is an iterator, so a break stops the walk rather than
// discarding a fully built slice. Order is map order, hence the sort in the
// Complete examples.
func ExampleTrie_WordsWithPrefix() {
	t := trie.New()
	for _, w := range []string{"a", "ab", "abc", "abcd"} {
		t.Insert(w)
	}

	count := 0
	for range t.WordsWithPrefix("a") {
		count++
		if count == 2 {
			break
		}
	}
	fmt.Println("stopped after", count)

	// Output:
	// stopped after 2
}

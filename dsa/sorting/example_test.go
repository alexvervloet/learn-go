package sorting_test

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/alexvervloet/learn-go/dsa/sorting"
)

func ExampleInsertion() {
	s := []int{5, 2, 9, 1, 7}
	sorting.Insertion(s)
	fmt.Println(s)

	// Output:
	// [1 2 5 7 9]
}

func ExampleQuick() {
	s := []string{"pear", "apple", "fig", "banana"}
	sorting.Quick(s)
	fmt.Println(s)

	// Output:
	// [apple banana fig pear]
}

// Every function takes the same comparison signature as slices.SortFunc, so a
// call site can be swapped between them without any other change.
func ExampleMergeFunc() {
	type employee struct {
		name string
		dept string
	}

	staff := []employee{
		{"ada", "eng"}, {"bo", "ops"}, {"cy", "eng"}, {"di", "ops"},
	}

	sorting.MergeFunc(staff, func(a, b employee) int {
		return cmp.Compare(a.dept, b.dept)
	})

	for _, e := range staff {
		fmt.Println(e.dept, e.name)
	}

	// Output:
	// eng ada
	// eng cy
	// ops bo
	// ops di
}

// Stability is why that example came out with ada before cy. A stable sort keeps
// equal elements in their original order, which matters whenever you sort twice.
func ExampleMergeFunc_stability() {
	type record struct {
		key int
		tag string
	}

	input := []record{{1, "first"}, {0, "a"}, {1, "second"}, {0, "b"}, {1, "third"}}
	byKey := func(a, b record) int { return cmp.Compare(a.key, b.key) }

	stable := slices.Clone(input)
	sorting.MergeFunc(stable, byKey)

	unstable := slices.Clone(input)
	sorting.SelectionFunc(unstable, byKey)

	show := func(label string, rs []record) {
		tags := make([]string, len(rs))
		for i, r := range rs {
			tags[i] = r.tag
		}
		fmt.Printf("%-10s %s\n", label, strings.Join(tags, " "))
	}

	show("merge:", stable)
	show("selection:", unstable)

	// Output:
	// merge:     a b first second third
	// selection: a b second first third
}

// Sorting twice is what stability is for: sort by one key, then stably by another,
// and the first ordering survives inside each group of the second.
func ExampleMergeFunc_twoPass() {
	type task struct {
		priority int
		name     string
	}

	tasks := []task{
		{2, "deploy"}, {1, "review"}, {2, "announce"}, {1, "build"},
	}

	// Pass one: by name.
	sorting.MergeFunc(tasks, func(a, b task) int { return cmp.Compare(a.name, b.name) })

	// Pass two: by priority, stably, so names stay alphabetical inside each.
	sorting.MergeFunc(tasks, func(a, b task) int { return cmp.Compare(a.priority, b.priority) })

	for _, t := range tasks {
		fmt.Println(t.priority, t.name)
	}

	// Output:
	// 1 build
	// 1 review
	// 2 announce
	// 2 deploy
}

// IsSorted takes the same comparison function, so a sort and its check cannot
// disagree about what order means.
func ExampleIsSorted() {
	fmt.Println(sorting.IsSorted([]int{1, 2, 2, 3}, cmp.Compare))
	fmt.Println(sorting.IsSorted([]int{1, 3, 2}, cmp.Compare))

	// Descending, by reversing the comparison.
	descending := func(a, b int) int { return cmp.Compare(b, a) }
	s := []int{1, 5, 3}
	sorting.QuickFunc(s, descending)
	fmt.Println(s, sorting.IsSorted(s, descending))

	// Output:
	// true
	// false
	// [5 3 1] true
}

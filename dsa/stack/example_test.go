package stack_test

import (
	"fmt"

	"github.com/alexvervloet/learn-go/dsa/stack"
)

func ExampleStack() {
	var s stack.Stack[int]

	s.Push(10)
	s.Push(20)
	s.Push(30)

	fmt.Println(s.String())

	top, _ := s.Pop()
	fmt.Println("popped:", top)
	fmt.Println("now:", s.String())

	// Output:
	// [10 20 30] <- top
	// popped: 30
	// now: [10 20] <- top
}

func ExampleStack_Pop_empty() {
	var s stack.Stack[int]

	// The comma-ok form, because a Stack[int] cannot use 0 to mean empty.
	v, ok := s.Pop()
	fmt.Printf("v=%d ok=%t\n", v, ok)

	// Output:
	// v=0 ok=false
}

func ExampleIsBalanced() {
	for _, input := range []string{"([{}])", "(]", "(", "a(b[c]d)e"} {
		fmt.Printf("%-10q %t\n", input, stack.IsBalanced(input))
	}

	// Output:
	// "([{}])"   true
	// "(]"       false
	// "("        false
	// "a(b[c]d)e" true
}

func ExampleLongestBalancedPrefix() {
	// Where an editor would put the error marker.
	fmt.Println(stack.LongestBalancedPrefix("()()]"))
	fmt.Println(stack.LongestBalancedPrefix(")("))

	// Output:
	// 4
	// 0
}

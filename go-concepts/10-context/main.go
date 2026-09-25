package main

import "fmt"

func section(n int, title string) {
	fmt.Printf("\n%d. %s\n%s\n", n, title, "-----------------------------------------")
}

func main() {
	fmt.Println("Lesson 10 — context")

	section(1, "Cancellation, deadlines, and the cancel contract")
	demoBasics()

	section(2, "The context tree: propagation and stricter-not-looser")
	demoTree()

	section(3, "Cause, WithoutCancel, and AfterFunc")
	demoCause()

	section(4, "Values, and the typed-key idiom")
	demoValues()

	section(5, "HTTP: request contexts, budgets across hops, shutdown")
	demoHTTP()

	section(6, "The six context mistakes")
	demoMistakes()

	fmt.Println("\nRun `go test -race ./10-context` to see each claim verified.")
}

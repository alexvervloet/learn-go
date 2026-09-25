package main

import "fmt"

func section(n int, title string) {
	fmt.Printf("\n%d. %s\n%s\n", n, title, "-----------------------------------------")
}

func main() {
	fmt.Println("Lesson 07 — channels")

	section(1, "Unbuffered handshakes and buffered capacity")
	demoBasics()

	section(2, "Closing: who, when, and the three panics")
	demoClosing()

	section(3, "Directional types and ownership transfer")
	demoDirections()

	section(4, "Patterns: ping-pong, semaphore, done, request/reply, fan-in")
	demoPatterns()

	section(5, "The six ways to deadlock")
	demoDeadlocks()

	fmt.Println("\nRun `go test -race ./07-channels` to see each claim verified.")
}

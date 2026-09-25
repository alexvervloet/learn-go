package main

import "fmt"

func section(n int, title string) {
	fmt.Printf("\n%d. %s\n%s\n", n, title, "-----------------------------------------")
}

func main() {
	fmt.Println("Lesson 15 — modules and workspaces")

	section(1, "go.mod, and semantic import versioning")
	demoGoMod()

	section(2, "Minimal version selection")
	demoSelection()

	section(3, "Workspaces, read from this repository's own go.work")
	demoWorkspace()

	section(4, "The commands, grouped by what they change")
	demoCommands()

	fmt.Println("\nRun `go test ./15-modules-and-workspaces` to see each claim verified.")
}

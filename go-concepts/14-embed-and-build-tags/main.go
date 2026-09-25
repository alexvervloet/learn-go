package main

import "fmt"

func section(n int, title string) {
	fmt.Printf("\n%d. %s\n%s\n", n, title, "-----------------------------------------")
}

func main() {
	fmt.Println("Lesson 14 — go:embed and build tags")

	section(1, "go:embed: three types, and the rules")
	demoEmbedding()

	section(2, "embed.FS is an fs.FS, so it composes")
	demoEmbedFS()

	section(3, "Build tags for platform code")
	demoPlatform()

	section(4, "A custom tag: assertions that vanish in production")
	demoDebug()

	section(5, "Cross-compilation")
	demoCrossCompile()

	fmt.Println("\nRun `go test ./14-embed-and-build-tags` to see each claim verified.")
	fmt.Println("Run `go run -tags debug ./14-embed-and-build-tags` to see the other build.")
}

// demoDebug prints which build this is and exercises the assertions.
func demoDebug() {
	for _, s := range debugModeExplanation() {
		fmt.Printf("    %s\n", s)
	}

	// In a production build these are empty functions and cost nothing.
	// In a debug build they check, and a failing one panics.
	Assert(1+1 == 2, "arithmetic still works")
	AssertFunc(func() bool { return Version() != "" }, "the embedded version is present")
	Tracef("demo reached the end of section 4, debug=%t", DebugEnabled)

	fmt.Printf("\n    DebugEnabled = %t\n", DebugEnabled)
}

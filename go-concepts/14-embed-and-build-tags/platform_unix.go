//go:build unix

package main

// This file builds on Linux, macOS, the BSDs and anything else Go considers
// Unix-like. The `unix` term was added in Go 1.19; before it this file needed
// `//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly ||
// solaris || aix`, which is exactly as pleasant as it looks.
//
// The blank line after the constraint is gofmt's, not the compiler's. With
// `//go:build` the line directly above `package` still works: tested with
// `//go:build ignore`, the file was excluded and go vet said nothing. gofmt
// inserts the blank line so the constraint can't be mistaken for the package
// doc comment. The blank line WAS required by the old `// +build` syntax,
// which is where the advice comes from.

// unixPlatform is the Unix implementation of Platform.
type unixPlatform struct{}

// currentPlatform is assigned here and in platform_windows.go. Exactly one of
// the two files is compiled, so there is never a duplicate declaration.
var currentPlatform Platform = unixPlatform{}

func (unixPlatform) Name() string { return "unix" }

func (unixPlatform) PathListSeparator() rune { return ':' }

func (unixPlatform) LineEnding() string { return "\n" }

func (unixPlatform) TempDirName() string { return "/tmp" }

// IsCaseSensitiveFS returns true, which is the Linux default and NOT the macOS
// one: APFS ships case-insensitive. The interface comment says as much; this
// is a demonstration of build tags rather than a filesystem library.
func (unixPlatform) IsCaseSensitiveFS() bool { return true }

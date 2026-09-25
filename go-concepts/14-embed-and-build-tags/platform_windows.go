//go:build windows

package main

// This file builds only on Windows. It is not compiled on any other platform,
// so it is also not type checked there, which is the standard build-tag hazard:
// a typo here would not be caught by a Linux or macOS CI job.
//
// The defence is to build every platform in CI. This repo's workflow includes
// windows-latest in its matrix for exactly that reason, and
// `GOOS=windows go build ./...` from any machine does the same check locally,
// in about a second, with no Windows required.

// windowsPlatform is the Windows implementation of Platform.
type windowsPlatform struct{}

// currentPlatform is assigned here and in platform_unix.go. Exactly one of the
// two files is compiled, so there is never a duplicate declaration.
var currentPlatform Platform = windowsPlatform{}

func (windowsPlatform) Name() string { return "windows" }

func (windowsPlatform) PathListSeparator() rune { return ';' }

func (windowsPlatform) LineEnding() string { return "\r\n" }

func (windowsPlatform) TempDirName() string { return `C:\Windows\Temp` }

// IsCaseSensitiveFS returns false: NTFS is case-insensitive by default, though
// it can be configured per directory since Windows 10.
func (windowsPlatform) IsCaseSensitiveFS() bool { return false }

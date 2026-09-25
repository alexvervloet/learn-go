//go:build !unix && !windows

package main

// The FALLBACK, and the reason it exists.
//
// platform_unix.go covers `unix` and platform_windows.go covers `windows`.
// Those two look exhaustive and are not. Go builds for targets that are
// neither:
//
//	GOOS=js      GOARCH=wasm     browsers
//	GOOS=wasip1  GOARCH=wasm     WASI runtimes
//	GOOS=plan9                   Plan 9
//
// Without this file, `GOOS=js GOARCH=wasm go build ./...` fails with:
//
//	platform.go:51:32: undefined: currentPlatform
//
// which is a genuinely confusing message, because the declaration exists in two
// files and the compiler is looking at neither.
//
// This was not hypothetical while writing the lesson: the js/wasm build broke
// exactly this way, and the fix is the rule worth taking away.
//
// THE RULE: build constraints must be EXHAUSTIVE. Either cover every target
// with a fallback like this one, or state the supported set explicitly so the
// failure is a clear message rather than an undefined symbol:
//
//	//go:build !unix && !windows
//	package main
//	func init() { panic("unsupported platform") }   // a loud, specific failure
//
// The compiler will not tell you the set is incomplete. Only building for the
// uncovered target does, which is why `go tool dist list` plus a loop in CI is
// worth the thirty seconds.

// otherPlatform is the fallback implementation of Platform.
type otherPlatform struct{}

// currentPlatform is assigned in exactly one of the three platform files.
var currentPlatform Platform = otherPlatform{}

func (otherPlatform) Name() string { return "other" }

// PathListSeparator follows the Unix convention, which is what js/wasm,
// wasip1 and plan9 all use.
func (otherPlatform) PathListSeparator() rune { return ':' }

func (otherPlatform) LineEnding() string { return "\n" }

func (otherPlatform) TempDirName() string { return "/tmp" }

func (otherPlatform) IsCaseSensitiveFS() bool { return true }

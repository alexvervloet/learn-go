package main

import (
	"fmt"
	"time"
)

// Untyped constants
// =================
//
// A Go constant without an explicit type has no type until it is used. That is
// why one declaration works in three incompatible contexts:
//
//	const factor = 3
//	var i int           = factor      // becomes int
//	var f float64       = factor      // becomes float64
//	var d time.Duration = factor      // becomes time.Duration
//
// Give it a type and all three stop compiling except the matching one. Untyped
// constants also carry arbitrary precision until assigned, so this is exact:
const (
	factor        = 3
	bigPrecision  = 1 << 62         // fine as an untyped constant
	thirdOfAThird = 1.0 / 3.0 / 3.0 // computed at full precision, then rounded once
)

// Typed constant. Assigning this to a float64 requires an explicit conversion.
const typedFactor int = 3

// Level is an enum built with iota. iota resets to 0 at each const block and
// increments once per ConstSpec line, so the values below are 1, 2, 3, 4.
type Level int

// The enum starts at iota + 1 on purpose. If Debug were 0 it would also be
// Level's zero value, and a struct field nobody set would silently mean Debug.
// Starting at 1 leaves 0 free to mean "unset", which LevelUnset names.
const (
	LevelUnset Level = iota // 0, the zero value, explicitly meaningless
	LevelDebug              // 1
	LevelInfo               // 2
	LevelWarn               // 3
	LevelError              // 4
)

// String makes Level print as a name instead of a number, everywhere: fmt, log,
// %v, %s. Implementing fmt.Stringer on an enum is close to mandatory in Go,
// because without it every log line shows "level 3".
func (l Level) String() string {
	switch l {
	case LevelUnset:
		return "UNSET"
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		// Reached when someone writes Level(99). Go enums are not closed sets:
		// any int can be converted to a Level, so this branch is reachable and
		// should say so rather than returning "".
		return fmt.Sprintf("Level(%d)", int(l))
	}
}

// Valid reports whether l is one of the declared levels. Because Go enums are
// open, a Valid method is how you actually enforce the set at a boundary such
// as JSON decoding or a CLI flag.
func (l Level) Valid() bool {
	return l >= LevelDebug && l <= LevelError
}

// ByteSize shows the other common iota pattern: the shift. Each line multiplies
// by 1024 without repeating a single literal.
type ByteSize float64

const (
	_           = iota             // skip 1<<0
	KB ByteSize = 1 << (10 * iota) // 1 << 10
	MB                             // 1 << 20
	GB                             // 1 << 30
	TB                             // 1 << 40
)

// demoConstants prints the untyped-constant trick and both iota patterns.
func demoConstants() {
	// The explicit types below are the demonstration. staticcheck QF1011/ST1023
	// would have them inferred, which would erase exactly what is being shown:
	// one untyped constant landing in three unrelated types.
	var i int = factor                         //nolint:staticcheck // explicit type is the point
	var f float64 = factor                     //nolint:staticcheck // explicit type is the point
	var d time.Duration = factor * time.Second //nolint:staticcheck // explicit type is the point
	fmt.Printf("  one untyped const `factor` used three ways: int %d, float64 %.1f, Duration %v\n", i, f, d)

	// typedFactor is an int, so reaching float64 needs a written conversion.
	var g float64 = float64(typedFactor) //nolint:staticcheck // explicit type is the point
	fmt.Printf("  typed const needs a conversion: float64(typedFactor) = %.1f\n", g)

	fmt.Printf("  Level enum: %v=%d %v=%d %v=%d %v=%d %v=%d\n",
		LevelUnset, LevelUnset, LevelDebug, LevelDebug, LevelInfo, LevelInfo,
		LevelWarn, LevelWarn, LevelError, LevelError)

	var unset Level // the zero value, deliberately not a real level
	fmt.Printf("  var l Level -> %v, Valid() = %t\n", unset, unset.Valid())
	fmt.Printf("  Level(99)   -> %v, Valid() = %t\n", Level(99), Level(99).Valid())

	fmt.Printf("  shift iota: KB=%.0f MB=%.0f GB=%.0f TB=%.0f\n",
		float64(KB), float64(MB), float64(GB), float64(TB))
}

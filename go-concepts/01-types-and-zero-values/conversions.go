package main

import (
	"fmt"
	"math"
	"strconv"
)

// Conversions, never coercions
// ============================
//
// Go converts nothing implicitly. int and int64 are different types even where
// they are the same width, and mixing them is a compile error rather than a
// silent widening. Every conversion is written as T(v) and is visible in review.
//
// That strictness is the point: the two dangerous numeric operations, truncation
// and overflow, happen at a conversion, so Go makes you write where they happen.

// truncatesTowardZero shows that float-to-int drops the fraction rather than
// rounding. -2.7 becomes -2, not -3: Go truncates toward zero, while Python's
// int() does the same but math.floor() does not.
func truncatesTowardZero(f float64) int {
	return int(f)
}

// roundsProperly is what you almost always meant. math.Round moves away from
// zero at .5, so 2.5 becomes 3 and -2.5 becomes -3.
func roundsProperly(f float64) int {
	return int(math.Round(f))
}

// overflowsSilently converts an int32-sized value into an int8. Go does not
// check range at runtime: the high bits are discarded and you get a wrapped
// number with no error and no panic.
//
//	int8 holds -128..127
//	200 as bits:      1100 1000
//	read as signed:  -56
func overflowsSilently(n int) int8 {
	return int8(n)
}

// convertsSafely is the version to reach for at a trust boundary. Go 1.17 added
// no built-in checked conversion, so the comparison is written out.
func convertsSafely(n int) (int8, error) {
	if n < math.MinInt8 || n > math.MaxInt8 {
		return 0, fmt.Errorf("convert %d to int8: out of range [%d, %d]", n, math.MinInt8, math.MaxInt8)
	}
	return int8(n), nil
}

// stringBytesRunes covers the conversion that trips up everyone arriving from
// Python 3, where str and bytes are cleanly separate.
//
// A Go string is an immutable sequence of BYTES that is conventionally UTF-8.
// It is not a sequence of characters.
//
//	len("héllo")          == 6   bytes, because é encodes as two bytes
//	utf8.RuneCountInString == 5   characters
//	s[1]                   == 0xC3, the first byte of é, not é itself
//
// Converting to []rune gives you the code points, at the cost of an allocation
// and a full scan. Ranging over a string decodes runes as it goes, for free.
func stringBytesRunes(s string) (byteLen, runeLen int, firstByte byte, firstRune rune) {
	bs := []byte(s) // copies, because strings are immutable and []byte is not
	rs := []rune(s) // decodes UTF-8 into code points
	return len(bs), len(rs), bs[0], rs[0]
}

// rangeOverStringYieldsRunes shows that the index jumps by the byte width of
// each rune, which is why `for i := 0; i < len(s); i++` is wrong for text.
func rangeOverStringYieldsRunes(s string) (indexes []int, runes []rune) {
	for i, r := range s {
		indexes = append(indexes, i)
		runes = append(runes, r)
	}
	return indexes, runes
}

// numberFromString is the conversion Go does NOT spell as a type conversion.
// int("42") is a compile error; parsing is a function in strconv that returns
// an error, because parsing can fail and conversion cannot.
func numberFromString(s string) (int, error) {
	return strconv.Atoi(s)
}

// demoConversions prints each conversion hazard with its result.
func demoConversions() {
	fmt.Printf("  int(2.7)=%d  int(-2.7)=%d   (truncates toward zero)\n",
		truncatesTowardZero(2.7), truncatesTowardZero(-2.7))
	fmt.Printf("  round(2.5)=%d round(-2.5)=%d  (math.Round, away from zero)\n",
		roundsProperly(2.5), roundsProperly(-2.5))

	fmt.Printf("  int8(200)=%d   (silent overflow, no error)\n", overflowsSilently(200))
	if _, err := convertsSafely(200); err != nil {
		fmt.Printf("  convertsSafely(200) -> %v\n", err)
	}
	v, _ := convertsSafely(100)
	fmt.Printf("  convertsSafely(100) -> %d\n", v)

	const s = "héllo"
	byteLen, runeLen, firstByte, firstRune := stringBytesRunes(s)
	fmt.Printf("  %q: len=%d bytes, %d runes, s[0]=%#x, rune[0]=%q\n",
		s, byteLen, runeLen, firstByte, firstRune)

	idx, runes := rangeOverStringYieldsRunes(s)
	fmt.Printf("  range %q -> byte indexes %v, runes %q   (index skips 2 at é)\n", s, idx, runes)

	n, err := numberFromString("42")
	fmt.Printf("  strconv.Atoi(\"42\") -> %d, %v\n", n, err)
	_, err = numberFromString("4x2")
	fmt.Printf("  strconv.Atoi(\"4x2\") -> %v\n", err)
}

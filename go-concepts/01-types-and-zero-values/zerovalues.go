// Package main is lesson 01 of go-concepts: types and zero values.
//
// Go has no None, no undefined, and no uninitialised memory. Declaring a
// variable without a value gives you that type's zero value, and the zero
// value is always a real, readable value.
//
//	var i int        // 0
//	var s string     // ""
//	var p *int       // nil
//	var xs []string  // nil, but len/range/append all work
//	var m map[string]int  // nil, reads work, WRITES PANIC
//
// The design goal is that a freshly declared value is useful without a
// constructor. bytes.Buffer, sync.Mutex and sync.WaitGroup all work this way.
package main

import (
	"fmt"
	"strings"
)

// Config shows why "every field has a zero value" is a design problem and not
// just a language rule. A Config declared with var is entirely valid Go, and
// entirely wrong as configuration: Timeout 0 means "no timeout" to most
// libraries, and Retries 0 means "never retry".
type Config struct {
	Host    string
	Port    int
	Timeout int // seconds
	Retries int
	Debug   bool
}

// demoZeroValues prints the zero value of each kind of type.
func demoZeroValues() {
	var (
		i  int
		f  float64
		b  bool
		s  string
		p  *int
		fn func()
		ch chan int
		e  error
		xs []string
		m  map[string]int
		a  [3]int
		c  Config
	)

	// %#v prints a Go-syntax representation, which is the honest way to show
	// the difference between a nil slice and an empty one.
	fmt.Printf("  int        %#v\n", i)
	fmt.Printf("  float64    %#v\n", f)
	fmt.Printf("  bool       %#v\n", b)
	fmt.Printf("  string     %#v\n", s)
	fmt.Printf("  *int       %#v\n", p)
	fmt.Printf("  func()     %v\n", fn == nil) // funcs are only comparable to nil
	fmt.Printf("  chan int   %#v\n", ch)
	fmt.Printf("  error      %#v\n", e)
	fmt.Printf("  []string   %#v   (len %d, cap %d)\n", xs, len(xs), cap(xs))
	fmt.Printf("  map        %#v   (len %d)\n", m, len(m))
	fmt.Printf("  [3]int     %#v\n", a)
	fmt.Printf("  Config     %#v\n", c)
}

// nilSliceIsUsable shows that a nil slice supports every read operation plus
// append. This is why idiomatic Go rarely writes make([]T, 0) just to have
// something to append to.
func nilSliceIsUsable() (length int, capacity int, afterAppend []string) {
	var xs []string // nil

	length, capacity = len(xs), cap(xs) // 0, 0, no panic

	for range xs { // ranges zero times, no panic
		panic("unreachable: a nil slice has no elements")
	}

	// append allocates a backing array on first use, so this works on nil.
	xs = append(xs, "first")
	return length, capacity, xs
}

// nilSliceVsEmptySlice returns the two values that behave identically in Go and
// differently the moment they cross a boundary such as JSON encoding.
//
//	var nilSlice []string    -> encoding/json writes  null
//	emptySlice := []string{}  -> encoding/json writes  []
//
// Both have len 0. Both compare == only against nil, never against each other,
// because slices are not comparable. This distinction bites when an API
// contract says "always return an array".
func nilSliceVsEmptySlice() (nilSlice, emptySlice []string) {
	var a []string
	b := []string{}
	return a, b
}

// writingToNilMapPanics performs the single most common zero-value mistake in
// Go. It is recovered here so the demo can print it rather than dying, but in
// real code the fix is to make the map, never to recover.
func writingToNilMapPanics() (recovered string) {
	defer func() {
		if r := recover(); r != nil {
			recovered = fmt.Sprint(r)
		}
	}()

	var m map[string]int

	// Reading a nil map is legal and returns the value type's zero value.
	// The comma-ok form correctly reports that the key is absent.
	_, ok := m["missing"]
	if ok {
		panic("unreachable: nothing is present in a nil map")
	}

	// staticcheck SA5000 flags this correctly: it IS a write to a nil map.
	// The suppression is the whole point of the function, so it is narrow and
	// says why rather than switching the check off repo-wide.
	m["key"] = 1 //nolint:staticcheck // SA5000 is right; the panic is the lesson
	return ""
}

// demoNilBehaviour prints the slice and map differences side by side.
func demoNilBehaviour() {
	length, capacity, appended := nilSliceIsUsable()
	fmt.Printf("  nil slice: len=%d cap=%d, after append: %#v\n", length, capacity, appended)

	nilSlice, emptySlice := nilSliceVsEmptySlice()
	fmt.Printf("  var xs []string   -> %#v  len=%d  xs == nil: %t\n", nilSlice, len(nilSlice), nilSlice == nil)
	fmt.Printf("  xs := []string{}  -> %#v  len=%d  xs == nil: %t\n", emptySlice, len(emptySlice), emptySlice == nil)

	fmt.Printf("  writing to a nil map: %s\n", writingToNilMapPanics())

	m := map[string]int{} // or make(map[string]int)
	m["key"] = 1
	fmt.Printf("  after make/literal:   %#v\n", m)
}

// usefulZeroValue is the property to aim for in your own types. A Counter
// declared with var is immediately correct, with no New function to forget.
type Counter struct {
	total int
	parts []string // nil is a fine starting point
}

// Add works on a zero Counter because append handles the nil slice.
func (c *Counter) Add(part string) {
	c.total++
	c.parts = append(c.parts, part)
}

// String makes Counter satisfy fmt.Stringer.
func (c Counter) String() string {
	return fmt.Sprintf("%d parts: %s", c.total, strings.Join(c.parts, ","))
}

// demoUsefulZeroValue shows a zero-value-ready type in action.
func demoUsefulZeroValue() {
	var c Counter // no constructor
	c.Add("alpha")
	c.Add("beta")
	fmt.Printf("  var c Counter; c.Add(...) -> %v\n", c)

	var cfg Config // valid Go, useless configuration
	fmt.Printf("  var cfg Config            -> %+v\n", cfg)
	fmt.Println("  ...Timeout 0 reads as \"no timeout\", not \"unset\". Guard it.")
}

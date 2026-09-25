package main

import (
	"cmp"
	"fmt"
	"strings"
)

// Constraints
// ===========
//
// A constraint is an interface used as a type bound. Three kinds:
//
//	method constraints   the familiar kind: fmt.Stringer, error
//	type sets            a union of concrete types: ~int | ~float64
//	both                 methods AND a type set, in one interface
//
// The standard ones:
//
//	any          no constraint
//	comparable   supports == and !=, so usable as a map key
//	cmp.Ordered  supports < <= > >=: every numeric type, plus string

// Number is a type set. The | is union, and the ~ is the part that matters.
//
//	 int   means EXACTLY int
//	~int   means any type whose UNDERLYING type is int
//
// Without the tildes, `type Celsius float64` would not satisfy this, and
// neither would time.Duration, http.StatusCode, or any other named type in
// any codebase. Almost always use ~.
type Number interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64
}

// Integer is a narrower set, for operations that only make sense on integers
// (bit shifts, modulo without surprises).
type Integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// Celsius is a named float64. It satisfies Number because of the ~.
type Celsius float64

// Bytes is a named int64, and satisfies both Number and Integer.
type Bytes int64

// Sum adds every element. This needs a type set rather than a method
// constraint, because + is an operator and Go has no operator overloading:
// there is no interface that means "supports +".
func Sum[T Number](values []T) T {
	var total T // the zero value of whatever T is
	for _, v := range values {
		total += v
	}
	return total
}

// Mean needs division, so it is in Number too. Note the conversion:
// T(len(values)) works because every type in Number is convertible from int.
func Mean[T Number](values []T) T {
	if len(values) == 0 {
		return Zero[T]()
	}
	return Sum(values) / T(len(values))
}

// IsEven only makes sense on integers, which is why Integer exists separately.
// Putting this in Number would compile until someone passed a float64 and got
// a nonsense answer.
func IsEven[T Integer](v T) bool { return v%2 == 0 }

// withoutTilde demonstrates what the ~ buys, by defining a constraint without
// it. StrictInt accepts int and nothing else.
type StrictInt interface {
	int // no tilde
}

// SumStrict compiles, and rejects every named integer type:
//
//	SumStrict([]Bytes{1, 2})
//	  -> Bytes does not satisfy StrictInt (possibly missing ~ for int in StrictInt)
//
// The compiler's error message names the fix, which is a small kindness.
func SumStrict[T StrictInt](values []T) T {
	var total T
	for _, v := range values {
		total += v
	}
	return total
}

// comparableIsNotOrdered is a distinction worth internalising.
//
//	comparable   == and !=          structs, arrays, interfaces, pointers, ...
//	cmp.Ordered  < <= > >=          numbers and strings only
//
// A struct is comparable (if its fields are) and NOT ordered. So Contains works
// on a []Point and Max does not.
type Point struct{ X, Y int }

// Deduplicate needs only equality, so comparable is the right constraint and
// it accepts structs.
func Deduplicate[T comparable](items []T) []T {
	seen := make(map[T]struct{}, len(items))
	out := make([]T, 0, len(items))

	for _, item := range items {
		if _, dup := seen[item]; dup {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}

	return out
}

// SortedUnique needs ordering as well, so its constraint is stricter and it
// will not accept a Point.
func SortedUnique[T cmp.Ordered](items []T) []T {
	unique := Deduplicate(items)

	// Insertion sort, written out rather than calling slices.Sort, to show a
	// generic algorithm using the constraint's operators directly.
	for i := 1; i < len(unique); i++ {
		for j := i; j > 0 && unique[j] < unique[j-1]; j-- {
			unique[j], unique[j-1] = unique[j-1], unique[j]
		}
	}

	return unique
}

// Method constraints
// ------------------
//
// The familiar kind. Any interface with methods works as a constraint, and the
// type parameter then has those methods available.

// Named is a method constraint.
type Named interface {
	Name() string
}

// Describe uses the method the constraint guarantees.
func Describe[T Named](items []T) string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name())
	}
	return strings.Join(names, ", ")
}

// Product satisfies Named with a value receiver, so both Product and *Product
// can be used as T (lesson 03's method sets, applied to generics).
type Product struct {
	SKU   string
	Title string
}

func (p Product) Name() string { return p.Title }

// Both a type set and methods
// ---------------------------
//
// An interface can combine them, and the type argument must satisfy both.

// StringableNumber is any numeric type that also has a String method. This is
// narrow on purpose: it shows the syntax, and it is also a good example of a
// constraint that is probably too clever for real code.
type StringableNumber interface {
	Number
	fmt.Stringer
}

// Temperature is a Celsius with a String method, satisfying both halves.
type Temperature float64

func (t Temperature) String() string { return fmt.Sprintf("%.1f°C", float64(t)) }

// FormatAll uses the method, and the type set lets it do arithmetic too.
func FormatAll[T StringableNumber](values []T) (formatted []string, total T) {
	for _, v := range values {
		formatted = append(formatted, v.String())
		total += v
	}
	return formatted, total
}

// constraintInterfacesCannotBeVariableTypes is the restriction that surprises
// people. An interface containing a type set exists only as a constraint:
//
//	var n Number = 5
//	  -> cannot use type Number outside a type constraint:
//	     interface contains type constraints
//
// Method-only interfaces are unaffected: `var s fmt.Stringer` is fine, because
// it describes behaviour rather than a set of types.
func constraintInterfacesCannotBeVariableTypes() []string {
	return []string{
		"var n Number = 5          -> interface contains type constraints",
		"var s fmt.Stringer = t    -> fine: method-only interfaces are ordinary types",
		"the rule: a type set makes an interface constraint-only",
	}
}

// demoConstraints prints constraint behaviour.
func demoConstraints() {
	fmt.Printf("  Sum([]int{1,2,3})        = %v\n", Sum([]int{1, 2, 3}))
	fmt.Printf("  Sum([]float64{1.5, 2.5}) = %v\n", Sum([]float64{1.5, 2.5}))
	fmt.Printf("  Sum([]Celsius{20, 22})   = %v   <- a named type, accepted because of ~\n",
		Sum([]Celsius{20, 22}))
	fmt.Printf("  Mean([]int{2, 4, 9})     = %v   (integer division)\n", Mean([]int{2, 4, 9}))
	fmt.Printf("  Mean([]float64{2, 4, 9}) = %.2f\n", Mean([]float64{2, 4, 9}))

	fmt.Printf("\n  IsEven(Bytes(4)) = %v   (Integer, so no floats)\n", IsEven(Bytes(4)))
	fmt.Printf("  SumStrict([]int{1,2}) = %v\n", SumStrict([]int{1, 2}))
	fmt.Println("  SumStrict([]Bytes{1,2}) does not compile: \"possibly missing ~ for int\"")

	points := []Point{{1, 2}, {1, 2}, {3, 4}}
	fmt.Printf("\n  Deduplicate on structs (comparable): %v\n", Deduplicate(points))
	fmt.Printf("  SortedUnique([]int{3,1,3,2}):        %v\n", SortedUnique([]int{3, 1, 3, 2}))
	fmt.Printf("  SortedUnique([]string{\"c\",\"a\",\"c\"}): %v\n", SortedUnique([]string{"c", "a", "c"}))
	fmt.Println("  SortedUnique(points) does not compile: Point is comparable but not ordered")

	products := []Product{{SKU: "A1", Title: "Keyboard"}, {SKU: "B2", Title: "Mouse"}}
	fmt.Printf("\n  Describe (method constraint): %s\n", Describe(products))

	formatted, total := FormatAll([]Temperature{20.5, 22.1})
	fmt.Printf("  FormatAll (type set + method): %v, total %v\n", formatted, total)

	fmt.Println("\n  constraint interfaces are constraint-only:")
	for _, s := range constraintInterfacesCannotBeVariableTypes() {
		fmt.Printf("    %s\n", s)
	}
}

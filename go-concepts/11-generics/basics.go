// Package main is lesson 11 of go-concepts: generics.
//
//	func Max[T cmp.Ordered](a, b T) T
//	     ^^^^^^^^^^^^^^^^^^
//	     type parameter list: one parameter T, constrained to ordered types
//
// A constraint is an interface used as a type bound. It can list methods, or
// concrete types (a "type set"), or both.
//
// Type arguments are usually inferred from the ARGUMENTS. They cannot be
// inferred from the return type, which is the main case where you have to write
// them out.
package main

import (
	"cmp"
	"fmt"
	"strings"
)

// Max is the canonical example. cmp.Ordered is the standard library's
// constraint for types supporting < <= > >=, which is every numeric type plus
// string.
func Max[T cmp.Ordered](a, b T) T {
	if a > b {
		return a
	}
	return b
}

// Min is its counterpart. Both exist in the standard library as of Go 1.21
// (the builtins `min` and `max`), so in real code use those; these are here to
// show the mechanics.
func Min[T cmp.Ordered](a, b T) T {
	if a < b {
		return a
	}
	return b
}

// Clamp takes three values of the same type, which is where a type parameter
// earns its place: the relationship between the parameters is what is being
// expressed, and `any` could not express it.
func Clamp[T cmp.Ordered](v, low, high T) T {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

// twoTypeParameters shows that a function can have several, and that they are
// inferred independently.
//
// Pairs is a transform: T in, U out. U cannot be inferred from the arguments
// alone, but it CAN be inferred from the function argument's signature, which
// is why callers rarely have to write it.
func Pairs[T, U any](items []T, f func(T) U) []U {
	out := make([]U, 0, len(items))
	for _, item := range items {
		out = append(out, f(item))
	}
	return out
}

// Zero returns the zero value for T. This is the case where inference is
// impossible: there are no arguments to infer from, so the caller must write
// Zero[int]().
//
// It is also genuinely useful. Inside a generic function, `var zero T` is how
// you produce a zero value without knowing what T is, and returning it is how
// a generic Get reports "not found".
func Zero[T any]() T {
	var zero T
	return zero
}

// inferenceFailsOnReturnTypeAlone documents the error, because the message is
// not obvious the first time:
//
//	var x float64 = Max(3, 5)
//	  -> cannot use Max(3, 5) (value of type int) as float64 value
//
// Go inferred T as int from the untyped constants, THEN found the assignment
// did not fit. The fix is Max[float64](3, 5), which makes the constants adopt
// float64 the way untyped constants do everywhere else (lesson 01).
func inferenceFailsOnReturnTypeAlone() (inferred int, explicit float64) {
	return Max(3, 5), Max[float64](3, 5)
}

// untypedConstantsStillWork: the constraint applies after the constants have
// adopted a type, so mixing an int literal with a float64 variable is fine.
func untypedConstantsStillWork() (a float64, b time_Duration) {
	var f float64 = 2.5 //nolint:staticcheck // the explicit type is what makes the next line's inference visible

	// 3 is untyped, so it becomes float64 to match f.
	a = Max(f, 3)

	// A named type works too, because cmp.Ordered uses ~ internally.
	var d time_Duration = 5
	b = Max(d, 10)

	return a, b
}

// time_Duration is a local named integer type, standing in for time.Duration
// without the import. It exists to show that cmp.Ordered accepts NAMED types,
// because the constraint is defined with ~int64 and not int64.
type time_Duration int64 //nolint:revive // the underscore marks it as a local stand-in

// singleUseTypeParameterIsASmell is the anti-pattern to recognise. T appears
// exactly once, so it constrains nothing and relates nothing:
//
//	func Print[T any](v T)   is   func Print(v any)   with extra steps
//
// The rule: if a type parameter appears only once in the signature, it should
// probably be `any`, or the function should not be generic at all.
func printGeneric[T any](v T) string { return fmt.Sprint(v) }

// printAny is the same function, and the honest spelling of it.
func printAny(v any) string { return fmt.Sprint(v) }

// aRealUseRelatesSeveralPositions is the contrast. T appears three times, and
// the signature now guarantees that the slice, the argument and the result all
// hold the same type. `any` could not say that.
func Contains[T comparable](haystack []T, needle T) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

// demoBasics prints type parameters and inference.
func demoBasics() {
	fmt.Printf("  Max(3, 5)        = %v   (T inferred as int)\n", Max(3, 5))
	fmt.Printf("  Max(\"a\", \"b\")    = %v   (T inferred as string)\n", Max("a", "b"))
	fmt.Printf("  Max(1.5, 2.5)    = %v (T inferred as float64)\n", Max(1.5, 2.5))
	fmt.Printf("  Min(3, 5)        = %v\n", Min(3, 5))
	fmt.Printf("  Clamp(15, 0, 10) = %v\n", Clamp(15, 0, 10))
	fmt.Printf("  Clamp(-5, 0, 10) = %v\n", Clamp(-5, 0, 10))

	lengths := Pairs([]string{"go", "generics", "type"}, func(s string) int { return len(s) })
	fmt.Printf("\n  Pairs(strings, len)  = %v   (U inferred from the function)\n", lengths)

	upper := Pairs([]string{"go", "rust"}, strings.ToUpper)
	fmt.Printf("  Pairs(strings, ToUpper) = %v\n", upper)

	inferred, explicit := inferenceFailsOnReturnTypeAlone()
	fmt.Printf("\n  Max(3, 5)          -> %v (int)\n", inferred)
	fmt.Printf("  Max[float64](3, 5) -> %v (explicit, because the return type cannot be inferred)\n", explicit)

	a, b := untypedConstantsStillWork()
	fmt.Printf("  Max(float64, untyped 3) = %v\n", a)
	fmt.Printf("  Max(named int64 type, 10) = %v   (cmp.Ordered uses ~, so named types fit)\n", b)

	fmt.Printf("\n  Zero[int]()    = %v\n", Zero[int]())
	fmt.Printf("  Zero[string]() = %q\n", Zero[string]())
	fmt.Printf("  Zero[[]int]()  = %v (nil)\n", Zero[[]int]())

	fmt.Printf("\n  printGeneric(42) = %q, printAny(42) = %q  <- identical; T appears once\n",
		printGeneric(42), printAny(42))
	fmt.Printf("  Contains([]int{1,2,3}, 2)  = %v   <- T appears three times, so it earns its place\n",
		Contains([]int{1, 2, 3}, 2))
}

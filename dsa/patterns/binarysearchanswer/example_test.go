package binarysearchanswer_test

import (
	"errors"
	"fmt"

	bsa "github.com/alexvervloet/learn-go/dsa/patterns/binarysearchanswer"
)

// The pattern in its bare form: bisect the space of possible answers, using a predicate
// that is false up to some threshold and true above it.
func ExampleSearch() {
	// The smallest x with x*x >= 200.
	got, _ := bsa.Search(1, 1000, func(x int) bool { return x*x >= 200 })
	fmt.Println(got)

	// Nothing in range works.
	_, err := bsa.Search(1, 10, func(int) bool { return false })
	fmt.Println(errors.Is(err, bsa.ErrImpossible))

	// Output:
	// 15
	// true
}

// The mirror image, obtained by bisecting the NEGATION and stepping back one. Writing a
// second loop with the comparisons flipped is where the off-by-one lives.
func ExampleSearchMax() {
	// The largest x with x*x <= 200.
	got, _ := bsa.SearchMax(0, 1000, func(x int) bool { return x*x <= 200 })
	fmt.Println(got)

	// Output:
	// 14
}

// The answer is a speed. "Can I finish in time at this speed" is monotonic, because eating
// faster never takes longer.
func ExampleMinEatingSpeed() {
	speed, _ := bsa.MinEatingSpeed([]int{3, 6, 7, 11}, 8)
	fmt.Println(speed)

	// More hours means a slower speed suffices.
	speed, _ = bsa.MinEatingSpeed([]int{30, 11, 23, 4, 20}, 5)
	fmt.Println(speed)
	speed, _ = bsa.MinEatingSpeed([]int{30, 11, 23, 4, 20}, 6)
	fmt.Println(speed)

	// Fewer hours than piles cannot work at any speed, because a partly eaten pile still
	// costs a whole hour.
	_, err := bsa.MinEatingSpeed([]int{1, 1, 1}, 2)
	fmt.Println(errors.Is(err, bsa.ErrImpossible))

	// Output:
	// 4
	// 30
	// 23
	// true
}

// Three problems, one question. Recognising that is worth more than knowing any of them.
func ExampleMinShipCapacity() {
	values := []int{7, 2, 5, 10, 8}

	ship, _ := bsa.MinShipCapacity(values, 2)
	split, _ := bsa.SplitArrayLargestSum(values, 2)
	workload, _ := bsa.MinMaxWorkload(values, 2)

	fmt.Println("ship capacity:  ", ship)
	fmt.Println("largest part:   ", split)
	fmt.Println("busiest worker: ", workload)

	// Output:
	// ship capacity:   18
	// largest part:    18
	// busiest worker:  18
}

// "Maximise the minimum", which is the one that needs SearchMax: the predicate is true for
// small gaps and false for large ones, the opposite direction from everything else here.
func ExampleMaxMinDistance() {
	got, _ := bsa.MaxMinDistance([]int{1, 2, 4, 8, 9}, 3)
	fmt.Println(got)

	got, _ = bsa.MaxMinDistance([]int{0, 10}, 2)
	fmt.Println(got)

	// Output:
	// 3
	// 10
}

func ExampleMinDaysForBouquets() {
	// One flower per bouquet, three bouquets: the third-earliest bloom.
	day, _ := bsa.MinDaysForBouquets([]int{1, 10, 3, 10, 2}, 3, 1)
	fmt.Println(day)

	// Two adjacent flowers per bouquet, and only five flowers exist, so six are needed
	// and it is impossible whatever the day.
	_, err := bsa.MinDaysForBouquets([]int{1, 10, 3, 10, 2}, 3, 2)
	fmt.Println(errors.Is(err, bsa.ErrImpossible))

	// Output:
	// 3
	// true
}

// The simplest instance, with no simulation to distract from the shape. The predicate is
// `x <= n/x` rather than `x*x <= n`, because the multiplication overflows for large n and
// the division does not.
func ExampleIntegerSquareRoot() {
	for _, n := range []int{0, 1, 8, 9, 10, 1 << 40} {
		got, _ := bsa.IntegerSquareRoot(n)
		fmt.Printf("isqrt(%d) = %d\n", n, got)
	}

	// Output:
	// isqrt(0) = 0
	// isqrt(1) = 1
	// isqrt(8) = 2
	// isqrt(9) = 3
	// isqrt(10) = 3
	// isqrt(1099511627776) = 1048576
}

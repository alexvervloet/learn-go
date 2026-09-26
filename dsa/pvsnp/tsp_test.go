package pvsnp

import (
	"errors"
	"math/rand/v2"
	"slices"
	"testing"
)

// A 5-city instance small enough to reason about and large enough for the
// heuristics to get wrong.
//
//	(0,0)  (0,3)
//	(4,0)  (4,3)
//	       (2,6)
func squareish() Distances {
	return Points([][2]int{
		{0, 0}, {4, 0}, {4, 3}, {2, 6}, {0, 3},
	})
}

func randomPoints(r *rand.Rand, n, span int) Distances {
	pts := make([][2]int, n)
	for i := range pts {
		pts[i] = [2]int{r.IntN(span), r.IntN(span)}
	}
	return Points(pts)
}

func TestTourLengthAndVerify(t *testing.T) {
	d := Points([][2]int{{0, 0}, {3, 0}, {3, 4}})

	// 3 + 4 + 5, the Pythagorean triangle.
	if got := d.TourLength([]int{0, 1, 2}); got != 12 {
		t.Errorf("TourLength = %d, want 12", got)
	}

	// A cycle, so rotating and reversing cost the same.
	for _, tour := range [][]int{{1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		if got := d.TourLength(tour); got != 12 {
			t.Errorf("TourLength(%v) = %d, want 12", tour, got)
		}
	}

	if _, ok := d.VerifyTour([]int{0, 1, 2}); !ok {
		t.Error("a valid tour was rejected")
	}

	for _, bad := range [][]int{
		{0, 1},       // too short
		{0, 1, 2, 0}, // too long
		{0, 0, 1},    // a repeat
		{0, 1, 3},    // out of range
		{0, 1, -1},   // negative
	} {
		if _, ok := d.VerifyTour(bad); ok {
			t.Errorf("VerifyTour(%v) accepted an invalid tour", bad)
		}
	}
}

func TestTourLengthDegenerate(t *testing.T) {
	d := Points([][2]int{{0, 0}, {1, 1}})

	if got := d.TourLength(nil); got != 0 {
		t.Errorf("empty tour = %d, want 0", got)
	}
	if got := d.TourLength([]int{0}); got != 0 {
		t.Errorf("one-city tour = %d, want 0", got)
	}
	// Two cities: there and back.
	if got := d.TourLength([]int{0, 1}); got != 2 {
		t.Errorf("two-city tour = %d, want 2", got)
	}
}

// TestExactSolversAgree is the strongest test available. Brute force and Held-Karp
// share no code and must always produce the same LENGTH, though not necessarily the
// same tour: ties are common on integer distances.
func TestExactSolversAgree(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))

	for range 300 {
		n := 2 + r.IntN(8) // 2 to 9 cities
		d := randomPoints(r, n, 20)

		bruteTour, bruteLen, e1 := TSPBrute(d)
		hkTour, hkLen, e2 := TSPHeldKarp(d)

		if e1 != nil || e2 != nil {
			t.Fatalf("n=%d: brute err=%v, held-karp err=%v", n, e1, e2)
		}

		if bruteLen != hkLen {
			t.Fatalf("n=%d: brute says %d, held-karp says %d", n, bruteLen, hkLen)
		}

		// Both tours must be valid and match their reported length.
		for name, tour := range map[string][]int{"brute": bruteTour, "held-karp": hkTour} {
			length, ok := d.VerifyTour(tour)
			if !ok {
				t.Fatalf("%s returned an invalid tour %v", name, tour)
			}
			if length != bruteLen {
				t.Fatalf("%s tour %v measures %d but was reported as %d", name, tour, length, bruteLen)
			}
		}
	}
}

func TestExactSolversOnTheSample(t *testing.T) {
	d := squareish()

	_, bruteLen, err := TSPBrute(d)
	if err != nil {
		t.Fatal(err)
	}
	_, hkLen, err := TSPHeldKarp(d)
	if err != nil {
		t.Fatal(err)
	}

	if bruteLen != hkLen {
		t.Errorf("brute %d, held-karp %d", bruteLen, hkLen)
	}
	t.Logf("optimal tour length: %d", bruteLen)
}

func TestExactSolversDegenerate(t *testing.T) {
	empty := Distances{}

	for name, solve := range map[string]func(Distances) ([]int, int, error){
		"brute": TSPBrute, "held-karp": TSPHeldKarp,
	} {
		tour, length, err := solve(empty)
		if err != nil || length != 0 || tour != nil {
			t.Errorf("%s on an empty instance = %v, %d, %v", name, tour, length, err)
		}
	}

	one := Points([][2]int{{0, 0}})
	tour, length, err := TSPHeldKarp(one)
	if err != nil || length != 0 || !slices.Equal(tour, []int{0}) {
		t.Errorf("held-karp on one city = %v, %d, %v", tour, length, err)
	}
}

func TestExactSolversRefuseHugeInstances(t *testing.T) {
	if _, _, e := TSPBrute(make(Distances, 13)); !errors.Is(e, ErrTooLarge) {
		t.Error("TSPBrute should refuse 13 cities")
	}
	if _, _, e := TSPHeldKarp(make(Distances, 21)); !errors.Is(e, ErrTooLarge) {
		t.Error("TSPHeldKarp should refuse 21 cities")
	}
}

// TestPermuteVisitsEveryPermutationOnce, because everything above rests on it.
func TestPermuteVisitsEveryPermutationOnce(t *testing.T) {
	for n := range 7 {
		s := make([]int, n)
		for i := range s {
			s[i] = i
		}

		seen := map[string]int{}
		count := 0

		permute(s, func(order []int) {
			count++
			// The callback gets the LIVE slice, so it has to be copied to be kept.
			// Forgetting that is the classic bug and makes every entry identical.
			seen[keyOf(order)]++
		})

		want := factorial(n)
		if count != want {
			t.Errorf("n=%d: %d permutations, want %d", n, count, want)
		}
		if len(seen) != want {
			t.Errorf("n=%d: %d DISTINCT permutations, want %d", n, len(seen), want)
		}
		for k, times := range seen {
			if times != 1 {
				t.Errorf("n=%d: permutation %s visited %d times", n, k, times)
			}
		}

		// And the caller's slice is restored.
		for i := range s {
			if s[i] != i {
				t.Errorf("n=%d: permute left the slice as %v", n, s)
				break
			}
		}
	}
}

func keyOf(s []int) string {
	b := make([]byte, 0, len(s))
	for _, v := range s {
		b = append(b, byte('a'+v))
	}
	return string(b)
}

func factorial(n int) int {
	total := 1
	for i := 2; i <= n; i++ {
		total *= i
	}
	return total
}

func TestNearestNeighbourProducesValidTours(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))

	for range 500 {
		n := 1 + r.IntN(30)
		d := randomPoints(r, n, 100)

		for start := range n {
			tour, length := TSPNearestNeighbour(d, start)

			measured, ok := d.VerifyTour(tour)
			if !ok {
				t.Fatalf("n=%d start=%d: invalid tour %v", n, start, tour)
			}
			if measured != length {
				t.Fatalf("reported %d, measured %d", length, measured)
			}
			if tour[0] != start {
				t.Fatalf("tour starts at %d, want %d", tour[0], start)
			}
		}
	}
}

// TestNearestNeighbourHasNoGuarantee uses the worst instance found by searching
// 20,000 random 5-to-8-city instances, where nearest neighbour is 1.47x optimal.
//
// The first version of this test put four cities on a LINE and expected the
// heuristic to do badly. It cannot: on a line every tour is the same out-and-back
// walk, so the ratio was exactly 1.00 in every case and the test asserted nothing
// while appearing to demonstrate something.
func TestNearestNeighbourHasNoGuarantee(t *testing.T) {
	worst := [][2]int{{4, 35}, {16, 32}, {15, 44}, {43, 8}, {7, 14}, {19, 7}, {23, 25}, {2, 37}}
	d := Points(worst)

	_, optimal, err := TSPBrute(d)
	if err != nil {
		t.Fatal(err)
	}

	_, nn := TSPNearestNeighbour(d, 0)

	ratio := float64(nn) / float64(optimal)
	t.Logf("optimal %d, nearest neighbour %d, ratio %.3f", optimal, nn, ratio)

	if nn < optimal {
		t.Fatal("the heuristic beat optimal, which is impossible")
	}
	if ratio < 1.4 {
		t.Errorf("ratio %.3f, expected at least 1.4 on this instance", ratio)
	}

	// 2-opt rescues it, which is the practical lesson.
	nnTour, _ := TSPNearestNeighbour(d, 0)
	_, improved := TwoOpt(d, nnTour)

	t.Logf("after 2-opt: %d, ratio %.3f", improved, float64(improved)/float64(optimal))

	if improved > nn {
		t.Error("2-opt made it worse")
	}
}

// TestHeuristicQualityBySize is the table in the TSPNearestNeighbour doc comment,
// so the numbers there are checked rather than remembered. Held-Karp supplies the
// exact answer, which is what limits n to 12 here.
func TestHeuristicQualityBySize(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 7))

	type row struct {
		n                  int
		nn, nnBest, twoOpt float64
	}
	var rows []row

	for _, n := range []int{5, 7, 9, 12} {
		var opt, nn, nnBest, two float64

		for range 100 {
			d := randomPoints(r, n, 1000)

			_, optimal, err := TSPHeldKarp(d)
			if err != nil || optimal == 0 {
				continue
			}

			_, a := TSPNearestNeighbour(d, 0)
			tour, b := TSPNearestNeighbourBest(d)
			_, c := TwoOpt(d, tour)

			opt += float64(optimal)
			nn += float64(a)
			nnBest += float64(b)
			two += float64(c)
		}

		pct := func(x float64) float64 { return (x/opt - 1) * 100 }
		rows = append(rows, row{n, pct(nn), pct(nnBest), pct(two)})

		t.Logf("n=%-3d nearest neighbour %+5.1f%%   best start %+5.1f%%   then 2-opt %+5.1f%%",
			n, pct(nn), pct(nnBest), pct(two))
	}

	// The ordering is the claim, and it has to hold at every size. The percentages
	// themselves depend on the seed and are logged rather than asserted.
	for _, r := range rows {
		if r.nn < r.nnBest {
			t.Errorf("n=%d: a single start beat the best of all starts", r.n)
		}
		if r.nnBest < r.twoOpt {
			t.Errorf("n=%d: 2-opt is worse than its input", r.n)
		}
		if r.twoOpt < 0 {
			t.Errorf("n=%d: 2-opt beat optimal, which is impossible", r.n)
		}
	}

	// And the gap widens with n, which is why a heuristic measured only on tiny
	// instances looks better than it is.
	if rows[len(rows)-1].nn <= rows[0].nn {
		t.Errorf("the nearest-neighbour gap did not grow with n: %+.1f%% at n=%d, %+.1f%% at n=%d",
			rows[0].nn, rows[0].n, rows[len(rows)-1].nn, rows[len(rows)-1].n)
	}
}

func TestTwoOptNeedsFourCities(t *testing.T) {
	d := Points([][2]int{{0, 0}, {1, 0}, {0, 1}})

	tour, length := TwoOpt(d, []int{0, 1, 2})
	if len(tour) != 3 {
		t.Errorf("TwoOpt changed the tour length to %d", len(tour))
	}
	if length != d.TourLength([]int{0, 1, 2}) {
		t.Error("TwoOpt changed the cost of a 3-city tour, which has only one tour")
	}
}

// TestTwoOptDoesNotMutateItsInput: it clones, because a heuristic that scribbles on
// the caller's tour makes "compare before and after" impossible.
func TestTwoOptDoesNotMutateItsInput(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 11))
	d := randomPoints(r, 12, 100)

	input := []int{0, 5, 3, 9, 1, 7, 11, 2, 8, 4, 10, 6}
	before := slices.Clone(input)

	TwoOpt(d, input)

	if !slices.Equal(input, before) {
		t.Errorf("TwoOpt mutated its input: %v then %v", before, input)
	}
}

func TestTwoOptOnLargerInstances(t *testing.T) {
	r := rand.New(rand.NewPCG(13, 17))

	for range 200 {
		n := 4 + r.IntN(40)
		d := randomPoints(r, n, 200)

		nnTour, nnLen := TSPNearestNeighbour(d, 0)
		tour, length := TwoOpt(d, nnTour)

		measured, ok := d.VerifyTour(tour)
		if !ok {
			t.Fatalf("n=%d: 2-opt produced an invalid tour %v", n, tour)
		}
		if measured != length {
			t.Fatalf("reported %d, measured %d", length, measured)
		}
		if length > nnLen {
			t.Fatalf("n=%d: 2-opt made it worse, %d then %d", n, nnLen, length)
		}
	}
}

func TestAsymmetricDistances(t *testing.T) {
	// A one-way street: going 0->1 is cheap, coming back is expensive.
	d := Distances{
		{0, 1, 9},
		{9, 0, 1},
		{1, 9, 0},
	}

	tour, length, err := TSPHeldKarp(d)
	if err != nil {
		t.Fatal(err)
	}

	// 0->1->2->0 costs 1+1+1 = 3. The reverse costs 9+9+9 = 27.
	if length != 3 {
		t.Errorf("length = %d, want 3 (tour %v)", length, tour)
	}

	_, bruteLen, err := TSPBrute(d)
	if err != nil {
		t.Fatal(err)
	}
	if bruteLen != 3 {
		t.Errorf("brute force = %d, want 3", bruteLen)
	}

	// And the reverse tour really does cost more, which is what makes this
	// asymmetric rather than merely unusual.
	if got := d.TourLength([]int{0, 2, 1}); got != 27 {
		t.Errorf("the reverse tour costs %d, want 27", got)
	}
}

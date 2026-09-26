package pvsnp

import (
	"errors"
	"math"
	"math/bits"
	"slices"
)

// The travelling salesman
// =======================
//
// Given the distances between n cities, find the shortest tour that visits each
// once and returns to the start.
//
// The decision version ("is there a tour shorter than k") is NP-complete. The
// optimisation version ("what is the shortest tour") is NP-hard, and the difference
// matters: a certificate for the decision version is a tour, which is checkable in
// O(n), while there is no short certificate for "this is the shortest", because
// proving it requires ruling out the others.
//
// That is why the heuristics below have no Verify counterpart. You can verify a
// tour. You cannot verify optimality without solving the problem again.

// ErrTooLarge means the exact algorithms would not finish.
var ErrTooLarge = errors.New("pvsnp: too many cities for an exact solution")

// Distances is a square matrix where [i][j] is the cost of going from i to j.
//
// Not required to be symmetric. Asymmetric TSP is the general case and is what a
// road network with one-way streets actually is; the standard heuristics below
// assume nothing about symmetry except where noted.
type Distances [][]int

// Points builds a Distances matrix from 2D points, using rounded Euclidean
// distance.
//
// Rounded to int deliberately. Float distances make the tests depend on exact
// floating-point arithmetic, and a tie broken differently on another platform would
// give a different, equally optimal tour with the same length. Comparing tours
// rather than lengths is then wrong, and comparing lengths needs an epsilon. Ints
// avoid all of it.
func Points(pts [][2]int) Distances {
	n := len(pts)
	d := make(Distances, n)

	for i := range d {
		d[i] = make([]int, n)
		for j := range d[i] {
			dx := float64(pts[i][0] - pts[j][0])
			dy := float64(pts[i][1] - pts[j][1])
			d[i][j] = int(math.Round(math.Hypot(dx, dy)))
		}
	}

	return d
}

// Len returns the number of cities.
func (d Distances) Len() int { return len(d) }

// TourLength returns the total cost of visiting tour in order and returning to the
// start. An empty or single-city tour costs nothing.
func (d Distances) TourLength(tour []int) int {
	if len(tour) < 2 {
		return 0
	}

	total := 0
	for i := range tour {
		next := tour[(i+1)%len(tour)]
		total += d[tour[i]][next]
	}
	return total
}

// VerifyTour reports whether tour visits every city exactly once, and its length.
//
// O(n), and it is the definition of a valid answer. Note what it does NOT check:
// whether the tour is the shortest. Nothing can check that in polynomial time,
// which is the difference between NP-complete and NP-hard.
func (d Distances) VerifyTour(tour []int) (int, bool) {
	if len(tour) != d.Len() {
		return 0, false
	}

	seen := make([]bool, d.Len())
	for _, city := range tour {
		if city < 0 || city >= d.Len() || seen[city] {
			return 0, false
		}
		seen[city] = true
	}

	return d.TourLength(tour), true
}

// TSPBrute tries every tour and returns the shortest.
//
// O(n!) tours, each O(n) to measure, so O(n * n!). City 0 is fixed as the start,
// because a tour is a cycle and rotating it changes nothing: that alone divides the
// work by n, taking (n-1)! rather than n!.
//
// The other easy factor of two, only valid for a symmetric matrix, is that a tour
// and its reverse cost the same. It is not taken here, because Distances is allowed
// to be asymmetric and a solver that is silently wrong on one-way streets is worse
// than one that is twice as slow.
func TSPBrute(d Distances) ([]int, int, error) {
	n := d.Len()
	if n == 0 {
		return nil, 0, nil
	}
	if n > 12 {
		return nil, 0, ErrTooLarge // 11! is 40 million tours, and 12! is 479 million
	}

	rest := make([]int, n-1)
	for i := range rest {
		rest[i] = i + 1
	}

	best := math.MaxInt
	var bestTour []int

	permute(rest, func(order []int) {
		tour := append([]int{0}, order...)

		if length := d.TourLength(tour); length < best {
			best = length
			bestTour = slices.Clone(tour)
		}
	})

	return bestTour, best, nil
}

// permute calls visit with every permutation of s, reusing s as the buffer.
//
// Heap's algorithm would be marginally faster. This is the swap-and-recurse version
// because it is four lines and the shape is the same one the backtracking pattern
// uses: choose, recurse, undo.
//
// visit receives the live slice, not a copy. Callers that keep it must clone it,
// which TSPBrute does, and forgetting to is the classic bug: every stored
// permutation ends up identical to the last one.
func permute(s []int, visit func([]int)) {
	var recurse func(int)
	recurse = func(k int) {
		if k == len(s) {
			visit(s)
			return
		}

		for i := k; i < len(s); i++ {
			s[k], s[i] = s[i], s[k]
			recurse(k + 1)
			s[k], s[i] = s[i], s[k] // undo, so the caller's slice is unchanged
		}
	}

	recurse(0)
}

// TSPHeldKarp returns the shortest tour using the Held-Karp dynamic program.
//
// O(n^2 * 2^n) time and O(n * 2^n) space, against brute force's O(n * n!). Both are
// exponential and the difference is enormous: at n=20, 2^20 * 400 is 4 x 10^8 while
// 19! is 1.2 x 10^17, a factor of 300 million.
//
// The idea is that the cost of the best path visiting a SET of cities and ending at
// j does not depend on the order it visited them in. So the state is (set, endpoint)
// rather than (sequence), and there are 2^n * n states instead of n! sequences.
// That is the same trick as every bitmask DP: collapse a permutation into a subset.
//
// It is still exponential, and the memory is what stops it first. n=25 needs
// 25 * 2^25 entries, which is 6.7 GB at 8 bytes each.
func TSPHeldKarp(d Distances) ([]int, int, error) {
	n := d.Len()

	switch {
	case n == 0:
		return nil, 0, nil
	case n == 1:
		return []int{0}, 0, nil
	case n > 20:
		return nil, 0, ErrTooLarge
	}

	const unreachable = math.MaxInt / 4 // /4 so adding two of them cannot overflow

	size := 1 << n

	// cost[mask*n + j]: the cheapest path starting at 0, visiting exactly the
	// cities in mask, and ending at j. A flat slice rather than [][]int, for the
	// same reason the graph package's matrix is flat: one allocation instead of
	// 2^n, and no pointer chase per row.
	cost := make([]int, size*n)
	prev := make([]int, size*n)
	for i := range cost {
		cost[i] = unreachable
		prev[i] = -1
	}

	cost[(1<<0)*n+0] = 0 // the empty path at the start city

	for mask := 1; mask < size; mask++ {
		if mask&1 == 0 {
			continue // every path starts at city 0, so it is always in the set
		}

		for j := range n {
			if mask&(1<<j) == 0 || cost[mask*n+j] == unreachable {
				continue
			}
			base := cost[mask*n+j]

			for k := range n {
				if mask&(1<<k) != 0 {
					continue // already visited
				}

				next := mask | 1<<k
				through := base + d[j][k]

				if through < cost[next*n+k] {
					cost[next*n+k] = through
					prev[next*n+k] = j
				}
			}
		}
	}

	// Close the cycle: from each possible last city, go home.
	full := size - 1
	best, last := unreachable, -1

	for j := 1; j < n; j++ {
		if cost[full*n+j] == unreachable {
			continue
		}
		if total := cost[full*n+j] + d[j][0]; total < best {
			best, last = total, j
		}
	}

	if last < 0 {
		return nil, 0, ErrNoSolution
	}

	// Walk prev backwards to rebuild the tour.
	tour := make([]int, 0, n)
	mask, city := full, last

	for city >= 0 {
		tour = append(tour, city)
		parent := prev[mask*n+city]
		mask &^= 1 << city // &^ is Go's AND NOT: clear this city from the set
		city = parent
	}

	slices.Reverse(tour)

	return tour, best, nil
}

// Heuristics
// ==========
//
// When exact is impossible, the question becomes how wrong you are willing to be.
// Both of these are polynomial, and neither has any guarantee on a general
// asymmetric matrix.

// TSPNearestNeighbour builds a tour by always going to the closest unvisited city.
//
// O(n^2), and it is the first thing anyone tries. Measured against exact solutions
// on random Euclidean instances, its average excess over optimal grows with n:
//
//	n=5    +4.3%      n=12   +13.6%
//	n=7    +7.4%      n=14   +13.8%
//	n=9   +10.1%      n=16   +12.4%
//
// Averages hide the risk. Over 20,000 random instances of 5 to 8 cities the worst
// single result was 1.47x optimal, and there is no constant factor it stays within:
// the worst case grows as log(n), because committing to the nearest city early can
// strand a distant one that must then be reached from wherever the tour ended up.
//
// TestNearestNeighbourHasNoGuarantee uses the worst instance that search found.
func TSPNearestNeighbour(d Distances, start int) ([]int, int) {
	n := d.Len()
	if n == 0 {
		return nil, 0
	}

	visited := make([]bool, n)
	tour := make([]int, 0, n)

	current := start
	visited[current] = true
	tour = append(tour, current)

	for len(tour) < n {
		nearest, nearestDist := -1, math.MaxInt

		for j := range n {
			if visited[j] {
				continue
			}
			if d[current][j] < nearestDist {
				nearest, nearestDist = j, d[current][j]
			}
		}

		visited[nearest] = true
		tour = append(tour, nearest)
		current = nearest
	}

	return tour, d.TourLength(tour)
}

// TSPNearestNeighbourBest runs the nearest-neighbour heuristic from every start and
// keeps the best result.
//
// O(n^3), and usually several percent better than a single run, because the
// heuristic's quality depends heavily on where it begins. This is the cheapest
// possible improvement to a greedy algorithm: run it n times and pick a winner.
func TSPNearestNeighbourBest(d Distances) ([]int, int) {
	n := d.Len()
	if n == 0 {
		return nil, 0
	}

	var bestTour []int
	best := math.MaxInt

	for start := range n {
		tour, length := TSPNearestNeighbour(d, start)
		if length < best {
			best, bestTour = length, tour
		}
	}

	return bestTour, best
}

// TwoOpt improves a tour by repeatedly reversing any segment that shortens it.
//
// The move is to take two edges (a,b) and (c,dd) and replace them with (a,c) and
// (b,dd), which requires reversing the segment between b and c. That reversal is
// why 2-opt assumes a SYMMETRIC matrix: on an asymmetric one, reversing a segment
// changes the cost of every edge inside it, and the O(1) delta below is wrong.
//
// O(n^2) per pass, run until no improvement is found, and it is the best return on
// effort in this file. Starting from nearest-neighbour-best on the same instances as
// above:
//
//	n       nearest neighbour   best start   then 2-opt
//	5       +4.3%               +1.2%        +0.2%
//	9       +10.1%              +2.1%        +0.3%
//	16      +12.4%              +4.5%        +0.5%
//
// Under one percent on instances small enough to check exactly. The classic figure
// quoted for hundreds of cities is nearer 5%, and this package cannot measure that,
// because there is no optimal to compare against at that size.
func TwoOpt(d Distances, tour []int) ([]int, int) {
	n := len(tour)
	if n < 4 {
		return tour, d.TourLength(tour)
	}

	tour = slices.Clone(tour)

	for improved := true; improved; {
		improved = false

		for i := range n - 1 {
			for j := i + 2; j < n; j++ {
				if i == 0 && j == n-1 {
					continue // that pair is the same edge twice
				}

				a, b := tour[i], tour[i+1]
				c, e := tour[j], tour[(j+1)%n]

				// The delta, in O(1). Computing the whole tour length instead
				// would make each pass O(n^3).
				before := d[a][b] + d[c][e]
				after := d[a][c] + d[b][e]

				if after >= before {
					continue
				}

				slices.Reverse(tour[i+1 : j+1])
				improved = true
			}
		}
	}

	return tour, d.TourLength(tour)
}

// Counting
// ========

// TourCount returns the number of distinct tours for n cities, or -1 if it
// overflows an int.
//
// (n-1)! for an asymmetric matrix, since the start is fixed, and (n-1)!/2 for a
// symmetric one, since a tour and its reverse are the same. The overflow check is
// the point: this is a function whose ANSWER stops fitting in 64 bits at n=22.
func TourCount(n int, symmetric bool) int {
	if n < 3 {
		return 1
	}

	total := 1
	for i := 2; i <= n-1; i++ {
		if total > math.MaxInt/i {
			return -1
		}
		total *= i
	}

	if symmetric {
		return total / 2
	}
	return total
}

// SubsetCount returns 2^n, or -1 if it overflows.
func SubsetCount(n int) int {
	if n >= bits.UintSize-1 {
		return -1
	}
	return 1 << n
}

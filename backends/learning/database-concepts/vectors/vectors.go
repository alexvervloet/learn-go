// Package vectors is similarity search with pgvector, and the thing that makes it unlike every other
// index in this module: the index changes the ANSWER.
//
// # The one idea
//
// An embedding is a list of floats produced by a model, arranged so that similar inputs are close
// together. Search is then "find the rows whose vector is nearest to this one", and nearest means one of
// three operators:
//
//	<->  L2 (Euclidean) distance
//	<=>  cosine distance, which is 1 - cosine similarity
//	<#>  negative inner product. Negative because pgvector only supports ASC ordering, so it
//	     negates to make "most similar" sort first, and a distance of -0.9 is a BETTER match
//	     than -0.1. This sign trips people up constantly.
//
// For vectors normalised to unit length, L2 and cosine rank identically, because ||a-b||² = 2 - 2(a·b)
// when ||a|| = ||b|| = 1. TestNormalisedVectorsMakeL2AndCosineAgree measures that rather than asserting
// it. Most embedding models return normalised vectors, so the choice of operator often does not matter
// and is presented as though it does.
//
// # The part that is genuinely different
//
// Every index elsewhere in this module is exact: `WHERE author_id = 42` returns the same rows with or
// without `idx_books_author`, only faster. A vector index is APPROXIMATE. Adding an HNSW index to a
// table changes which rows come back, and there is no error, no warning, and nothing in the plan that
// says "these results are wrong". The measure is recall, the fraction of the true nearest neighbours the
// index found, and the tests here compute it against exact search.
//
// That is the whole tradeoff and it is a product decision, not a database one. 95% recall at 20x the
// speed is excellent for "related products" and unacceptable for "find this exact document".
//
// # HNSW against IVFFlat
//
//	                  HNSW                              IVFFlat
//	build             slow, and per-row on INSERT       fast, and needs data to exist first
//	memory            large, the graph is in memory     small
//	recall            higher at the same speed          lower
//	tuning            m, ef_construction, ef_search     lists, probes
//	empty table       works                             builds a useless index
//
// The last row is the one that causes real bugs. IVFFlat clusters the existing rows, so an index built
// on an empty table has one meaningless cluster, and it keeps being used as the table fills.
// TestIVFFlatOnAnEmptyTableIsUseless measures what that costs.
//
// HNSW is the default choice now. IVFFlat is for when the index has to fit in memory alongside
// everything else.
package vectors

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pgvector/pgvector-go"
)

// Dimensions matches the column in the migration, and 384 is what all-MiniLM-L6-v2 produces.
const Dimensions = 384

// Model is the value stored in book_embeddings.model, so a later real model can coexist.
const Model = "synthetic-384"

// Querier is the subset these functions need.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Embed turns text into a deterministic unit vector.
//
// This is NOT an embedding model. It is a hashed bag of words: each word is hashed to a handful of
// dimensions and the result is normalised. That gives the two properties the tests need, which a call to
// a real model would not:
//
//	deterministic, so a test asserts a fixed answer rather than "roughly similar"
//	free and offline, so the suite does not need an API key or a 90 MB model download
//
// What it does NOT give is semantics. "dog" and "puppy" are as far apart as "dog" and "kingdom", because
// nothing here knows what a word means. So it can demonstrate every mechanical property of vector search
// (distance, recall, index behaviour, tuning) and nothing about embedding quality. Being clear about
// which half of the subject a fixture covers is the point.
func Embed(text string) pgvector.Vector {
	v := make([]float32, Dimensions)

	for _, word := range strings.Fields(strings.ToLower(text)) {
		h := fnv.New64a()
		_, _ = h.Write([]byte(word))
		sum := h.Sum64()

		// Three dimensions per word, so two texts sharing a word overlap measurably and a
		// single-dimension collision does not dominate.
		for i := range 3 {
			// A different mix per slot, otherwise all three land in the same place for
			// short words.
			mixed := sum*uint64(i*2+1) + uint64(i)*0x9e3779b97f4a7c15

			dim := mixed % Dimensions

			// The sign comes from a bit of the hash, so unrelated words cancel rather
			// than accumulate.
			if mixed&(1<<40) != 0 {
				v[dim] += 1
			} else {
				v[dim] -= 1
			}
		}
	}

	return pgvector.NewVector(normalise(v))
}

// normalise scales a vector to unit length.
//
// The zero vector is the case to handle: dividing by a zero norm gives NaN in every dimension, and a
// NaN vector makes every distance NaN, which sorts unpredictably and returns nothing useful with no
// error. Empty text is a real input, so this returns a fixed unit vector instead.
func normalise(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}

	if sum == 0 {
		v[0] = 1
		return v
	}

	norm := float32(math.Sqrt(sum))
	for i := range v {
		v[i] /= norm
	}

	return v
}

// Norm returns a vector's length, so a test can check the normalisation rather than trust it.
func Norm(v pgvector.Vector) float64 {
	var sum float64
	for _, x := range v.Slice() {
		sum += float64(x) * float64(x)
	}
	return math.Sqrt(sum)
}

// Load computes and stores an embedding for every book.
//
// A single INSERT ... SELECT with unnest rather than a row at a time, because 10,000 round trips to
// store 10,000 vectors is the N+1 from the other module wearing a different hat. CopyFrom would be
// faster still and pgvector-go's binary format needs the type registered on the connection, which is one
// more moving part than this needs.
func Load(ctx context.Context, q Querier, batchSize int) (int, error) {
	if batchSize <= 0 {
		batchSize = 500
	}

	rows, err := q.Query(ctx, "SELECT id, title FROM books ORDER BY id")
	if err != nil {
		return 0, fmt.Errorf("selecting books: %w", err)
	}

	type book struct {
		id    int64
		title string
	}

	books, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (book, error) {
		var b book
		err := r.Scan(&b.id, &b.title)
		return b, err
	})
	if err != nil {
		return 0, fmt.Errorf("scanning books: %w", err)
	}

	total := 0

	for start := 0; start < len(books); start += batchSize {
		end := min(start+batchSize, len(books))
		batch := books[start:end]

		ids := make([]int64, len(batch))

		// The vectors go as TEXT, cast to vector[] by Postgres. pgx cannot encode
		// []pgvector.Vector: pgvector-go registers the type per CONNECTION through
		// pgxvector.RegisterTypes, and even registered it has no array codec. The error is
		// "unable to encode []pgvector.Vector" followed by a dump of every float, which is
		// 636 KB of test output and does not say what to do about it.
		//
		// Sending text avoids touching the shared pool's connection setup. The cost is real
		// and worth knowing: "0.28867513" is eleven bytes where the binary format uses four,
		// so a 384-dimension vector goes from 1.5 KB to about 4 KB on the wire. For a load
		// that runs once that is the right trade; for a hot query path, register the type.
		embeddings := make([]string, len(batch))

		for i, b := range batch {
			ids[i] = b.id
			embeddings[i] = Embed(b.title).String()
		}

		tag, err := q.Exec(ctx, `
			INSERT INTO book_embeddings (book_id, embedding, model)
			SELECT id, e::vector, $3 FROM unnest($1::bigint[], $2::text[]) AS t(id, e)
			ON CONFLICT (book_id) DO UPDATE SET embedding = excluded.embedding`,
			ids, embeddings, Model)
		if err != nil {
			return total, fmt.Errorf("inserting embeddings %d..%d: %w", start, end, err)
		}

		total += int(tag.RowsAffected())
	}

	return total, nil
}

// Neighbour is one search result.
type Neighbour struct {
	BookID   int64
	Title    string
	Distance float64
}

// Operator names the three distance operators.
type Operator string

// The three operators. See the package doc for what each means.
const (
	L2           Operator = "<->"
	Cosine       Operator = "<=>"
	InnerProduct Operator = "<#>"
)

// Search finds the k nearest books to a query vector.
//
// The ORDER BY has to be exactly `column <op> $param` for an index to be usable. Wrapping it in a
// function, reversing the operands, or adding arithmetic makes it a plain sort over every row, silently,
// which is the vector-search version of applying a function to an indexed column.
func Search(ctx context.Context, q Querier, op Operator, query pgvector.Vector, k int) ([]Neighbour, error) {
	// $1::vector, and the parameter is the vector's text form. Same reason as in Load: no
	// per-connection type registration. The cast is inside the ORDER BY expression too, because
	// the expression has to match the index's exactly and `embedding <=> $1::vector` is what the
	// planner sees either way.
	sql := fmt.Sprintf(`
		SELECT e.book_id, b.title, (e.embedding %s $1::vector)::float8
		  FROM book_embeddings e
		  JOIN books b ON b.id = e.book_id
		 ORDER BY e.embedding %s $1::vector
		 LIMIT $2`, op, op)

	rows, err := q.Query(ctx, sql, query.String(), k)
	if err != nil {
		return nil, fmt.Errorf("searching with %s: %w", op, err)
	}

	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Neighbour, error) {
		var n Neighbour
		err := r.Scan(&n.BookID, &n.Title, &n.Distance)
		return n, err
	})
}

// RecallStrict is the fraction of the exact result set, by ID, that an approximate search also found.
//
// This is the definition everyone writes first and it is wrong whenever the data has ties. If 200 rows
// share the same distance and the query asks for 50, "the exact top 50" is an arbitrary 50 of those 200,
// and an approximate search that returns a different arbitrary 50 scores 0% recall while being exactly
// as good an answer.
//
// The fixture in this package has heavy ties (the seeded titles come from 8 adjectives and 8 nouns, so
// thousands of books share a vector's shape) and that is how the problem showed up: recall came out
// 20%, 74%, 56%, 100% as ef_search rose, which is not a tradeoff curve, it is noise.
func RecallStrict(exact, approximate []Neighbour) float64 {
	if len(exact) == 0 {
		return 1
	}

	want := make(map[int64]struct{}, len(exact))
	for _, n := range exact {
		want[n.BookID] = struct{}{}
	}

	found := 0
	for _, n := range approximate {
		if _, ok := want[n.BookID]; ok {
			found++
		}
	}

	return float64(found) / float64(len(exact))
}

// Recall is the tie-aware version, and the one to use.
//
// Instead of asking "did it find these exact rows", it asks "is each row it found at least as close as
// the worst row in the exact answer". That is what a user of the search experiences, and it is what the
// information-retrieval literature means by recall at k in the presence of ties.
//
// The epsilon is not optional. The exact query and the approximate query compute the same distance
// through different code paths, and float32 arithmetic is not associative, so a genuinely tied row comes
// back as 0.8660254 in one and 0.86602545 in the other. Without the tolerance those count as misses and
// the tie-aware measure has the same problem as the strict one.
func Recall(exact, approximate []Neighbour) float64 {
	if len(exact) == 0 {
		return 1
	}

	const epsilon = 1e-6

	threshold := exact[len(exact)-1].Distance + epsilon

	good := 0
	for _, n := range approximate {
		if n.Distance <= threshold {
			good++
		}
	}

	return float64(good) / float64(len(exact))
}

// DistinctDistances counts how many different distances a result set contains, which is how to tell
// whether a recall number means anything.
func DistinctDistances(ns []Neighbour) int {
	seen := make(map[float64]struct{}, len(ns))
	for _, n := range ns {
		seen[n.Distance] = struct{}{}
	}
	return len(seen)
}

// IDs pulls the book IDs out of a result set, for comparing two searches.
func IDs(ns []Neighbour) []int64 {
	out := make([]int64, len(ns))
	for i, n := range ns {
		out[i] = n.BookID
	}
	return out
}

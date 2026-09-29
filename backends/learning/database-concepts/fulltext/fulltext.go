// Package fulltext is search inside Postgres, and where it stops being enough.
//
// # The four pieces
//
//	tsvector   the document, lexemes plus positions, after stemming and stop-word removal
//	tsquery    the query, lexemes plus operators (& | ! <->)
//	@@         the match operator between them
//	GIN index  what makes @@ fast, by mapping each lexeme to the rows containing it
//
// The stemming is the part that makes this search rather than LIKE. to_tsvector('english', 'The
// Running Dogs') is 'dog':3 'run':2. So a query for "runs" matches, "dogs" matches, and "the" matches
// nothing because it is a stop word and was never stored.
//
// # Four query parsers, and one of them is unsafe for user input
//
//	to_tsquery          raw syntax. Raises 42601 on anything malformed, which for user input
//	                    means a search box that returns a 500 when someone types "c++".
//	plainto_tsquery     treats the input as words and ANDs them. Safe, and cannot express OR
//	                    or a phrase.
//	phraseto_tsquery    ANDs them with the <-> distance operator, so word order matters.
//	websearch_to_tsquery  the one to use. Understands quoted phrases, OR, and a leading -, never
//	                    raises on malformed input, and behaves like a search engine because that
//	                    is what users expect.
//
// Three of the four never raise, so "only one is safe" (as an earlier version of this heading said)
// overstates it: to_tsquery is the unsafe one, and websearch_to_tsquery is the best of the safe three.
//
// # The generated column
//
// The migration in this module declares the tsvector as a GENERATED ALWAYS AS ... STORED column, which
// is the modern answer and removes a whole class of bug. The older patterns are a trigger (which can
// be disabled, most often by a bulk load run with session_replication_role = replica or ALTER TABLE
// ... DISABLE TRIGGER, and then the index quietly holds stale data; COPY FROM on its own does fire
// row triggers, whatever an earlier version of this comment said) or computing
// to_tsvector in the query (which cannot use the index at all unless the index is on the same
// expression).
//
// # Where Postgres search stops
//
// Worth being honest about, because "just use Postgres" is as much a reflex as "just use Elastic":
//
//	no cross-field relevance tuning. A title match and a body match can be weighted with
//	  setweight, and that is the whole of the ranking model.
//	ts_rank reads only the tsvector, so ranking cannot be combined with behavioural signals
//	  without doing it in SQL by hand.
//	no fuzzy matching. A typo matches nothing. pg_trgm gives similarity, and combining trigram
//	  similarity with a tsquery match in one ranked query is possible and not pleasant.
//	no faceting, no aggregation over search results beyond what SQL gives you.
//	the GIN index is large and its write amplification is real.
//
// For a product search box over a few million rows, Postgres is the right answer and one fewer system
// to run. For search as the product, it is not.
package fulltext

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is the subset these functions need.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Hit is one search result.
type Hit struct {
	ID       int64
	Title    string
	Rank     float32
	Headline string
	Lexemes  int
}

// Parser names the four tsquery builders, so a caller picks one deliberately instead of copying
// whichever appeared first in a search result.
type Parser string

// The four parsers. See the package doc for which to use.
const (
	Raw       Parser = "to_tsquery"
	Plain     Parser = "plainto_tsquery"
	Phrase    Parser = "phraseto_tsquery"
	WebSearch Parser = "websearch_to_tsquery"
)

// Search runs a ranked search against the generated tsvector column.
//
// The parser name is interpolated into the SQL and the user's text is a parameter, which is the right
// way round: the function name is chosen from the constants above and can never come from a request,
// and the text that can never be trusted is bound.
//
// ts_rank rather than ts_rank_cd: ts_rank counts lexeme frequency, ts_rank_cd also rewards matches that
// are close together, which is better for phrases and costs more. Neither knows anything about the rest
// of the row, so "popular results first" has to be a separate term in the ORDER BY.
func Search(ctx context.Context, q Querier, parser Parser, text string, limit int) ([]Hit, error) {
	sql := fmt.Sprintf(`
		SELECT b.id,
		       b.title,
		       ts_rank(b.search, query) AS rank,
		       ts_headline('english', b.title, query,
		                   'StartSel=<b>, StopSel=</b>, MaxFragments=2'),
		       length(b.search)
		  FROM books b, %s('english', $1) AS query
		 WHERE b.search @@ query
		 ORDER BY rank DESC, b.id
		 LIMIT $2`, parser)

	rows, err := q.Query(ctx, sql, text, limit)
	if err != nil {
		return nil, fmt.Errorf("searching for %q with %s: %w", text, parser, err)
	}

	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Hit, error) {
		var h Hit
		err := r.Scan(&h.ID, &h.Title, &h.Rank, &h.Headline, &h.Lexemes)
		return h, err
	})
}

// Lexemes returns what to_tsvector made of a string, which is the only way to understand why a search
// did or did not match.
//
// Every "why doesn't my search find this" question is answered by running this on the document and on
// the query and comparing.
func Lexemes(ctx context.Context, q Querier, config, text string) (string, error) {
	var out string

	if err := q.QueryRow(ctx, "SELECT to_tsvector($1::regconfig, $2)::text", config, text).
		Scan(&out); err != nil {
		return "", fmt.Errorf("tokenising %q as %s: %w", text, config, err)
	}

	return out, nil
}

// ParsedQuery returns what a parser made of a query string.
func ParsedQuery(ctx context.Context, q Querier, parser Parser, text string) (string, error) {
	var out string

	sql := fmt.Sprintf("SELECT %s('english', $1)::text", parser)

	if err := q.QueryRow(ctx, sql, text).Scan(&out); err != nil {
		return "", fmt.Errorf("parsing %q with %s: %w", text, parser, err)
	}

	return out, nil
}

// SimilarTitles is trigram search, which is the answer to the two things a tsvector cannot do: typos
// and substrings.
//
// pg_trgm breaks a string into overlapping three-character sequences and compares the sets. "postgres"
// and "postgress" share almost all their trigrams, so a typo still matches, and a GIN index on the
// trigrams makes a leading-wildcard LIKE indexable too.
//
// The cost: the index is bigger than a tsvector's, and similarity has no notion of word boundaries or
// stemming, so it is a different tool rather than a better one. Real search boxes use both, tsquery for
// the match and trigram similarity as a fallback when the tsquery returns nothing.
func SimilarTitles(ctx context.Context, q Querier, text string, limit int) ([]Hit, error) {
	rows, err := q.Query(ctx, `
		SELECT id, title, similarity(title, $1) AS sim, title, 0
		  FROM books
		 WHERE title %> $1
		 ORDER BY sim DESC, id
		 LIMIT $2`, text, limit)
	if err != nil {
		return nil, fmt.Errorf("trigram search for %q: %w", text, err)
	}

	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Hit, error) {
		var h Hit
		err := r.Scan(&h.ID, &h.Title, &h.Rank, &h.Headline, &h.Lexemes)
		return h, err
	})
}

// Weighted is the one piece of relevance tuning Postgres offers: A, B, C and D labels on parts of a
// document, with a weight vector supplied at query time.
//
// setweight is applied when the tsvector is BUILT, so changing the weights means rebuilding the column.
// The {D,C,B,A} array at query time only changes how much each existing label counts, which is the part
// that can be tuned without a migration.
func Weighted(ctx context.Context, q Querier, text string, limit int) ([]Hit, error) {
	rows, err := q.Query(ctx, `
		WITH doc AS (
		     SELECT b.id,
		            b.title,
		            setweight(to_tsvector('english', b.title), 'A') ||
		            setweight(to_tsvector('english', coalesce(a.name, '')), 'B') AS v
		       FROM books b
		       LEFT JOIN authors a ON a.id = b.author_id
		)
		SELECT doc.id,
		       doc.title,
		       -- The weight array is {D,C,B,A}, in that order, which is backwards from how
		       -- anyone would write it and is the reason tuned weights often do the opposite
		       -- of what was intended.
		       ts_rank('{0.1, 0.2, 0.4, 1.0}', doc.v, query) AS rank,
		       doc.title,
		       length(doc.v)
		  FROM doc, websearch_to_tsquery('english', $1) AS query
		 WHERE doc.v @@ query
		 ORDER BY rank DESC, doc.id
		 LIMIT $2`, text, limit)
	if err != nil {
		return nil, fmt.Errorf("weighted search for %q: %w", text, err)
	}

	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Hit, error) {
		var h Hit
		err := r.Scan(&h.ID, &h.Title, &h.Rank, &h.Headline, &h.Lexemes)
		return h, err
	})
}

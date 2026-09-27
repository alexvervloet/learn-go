// Package normalization is the one topic here that is not about Go at all, and the tests prove the
// anomalies rather than describing them.
//
// # The forms, in the order they matter
//
//	1NF  every column holds ONE value. No comma-separated lists, no "phone1, phone2, phone3".
//	2NF  1NF, and every non-key column depends on the WHOLE key. Only bites composite keys.
//	3NF  2NF, and no non-key column depends on another non-key column.
//	BCNF 3NF, and every determinant is a candidate key. Rare, and the example below is the
//	     standard one because natural examples are hard to find.
//
// The definitions are not the useful part. The useful part is that each form exists to prevent a specific
// ANOMALY, and an anomaly is a way for the same fact to be stored twice and then disagree with itself:
//
//	update anomaly  one UPDATE has to touch N rows, and if it touches N-1 the data is now wrong
//	insert anomaly  a fact cannot be recorded without inventing an unrelated one
//	delete anomaly  removing one fact silently removes another
//
// Every test in this package triggers one of these against a badly shaped table and then shows the
// normalised version making it impossible.
//
// # What normalisation costs
//
// Joins. A fully normalised schema answers "show me this order" with five joins, and a denormalised one
// answers it with a select. That is a real cost and it is why the orders table in this module's migration
// carries a denormalised total_cents with a trigger maintaining it.
//
// The rule that follows: normalise until it hurts, denormalise until it works, and never denormalise
// without something that keeps the copy honest. A copy nothing maintains is not an optimisation, it is a
// bug with a schedule.
//
// # What Go adds to the story
//
// Not much, and that is worth saying. Go has no ORM doing lazy loading, so a normalised schema means the
// struct with the joined data is a struct you write, and the join is SQL you write. The one Go-specific
// point is that a denormalised column and the struct field holding it are two more places for the same
// fact to live, so the trigger has to be in the database rather than in Go: application code that
// maintains a denormalised column is correct until the second process writes to the table.
package normalization

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is the subset these functions need.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Unnormalised is the shape data arrives in: one row per order line, with everything repeated.
//
// It is also the shape a CSV export has, and the shape a reporting table deliberately keeps. The problem
// is never the shape, it is using it as the source of truth.
type Unnormalised struct {
	OrderID      int64
	CustomerName string
	CustomerCity string
	// Tags is the 1NF violation: several values in one column.
	Tags       string
	BookTitle  string
	BookAuthor string
	Quantity   int32
	PriceCents int64
}

// CreateUnnormalised builds the bad table and fills it.
//
// A temp table, so every test gets its own and nothing needs cleaning up. TEMP tables live for the
// session, which for a pooled connection means the transaction that created them has to be the one using
// them.
func CreateUnnormalised(ctx context.Context, q Querier) error {
	if _, err := q.Exec(ctx, `
		CREATE TEMP TABLE orders_flat (
		    order_id      bigint,
		    customer_name text,
		    customer_city text,
		    tags          text,
		    book_title    text,
		    book_author   text,
		    quantity      int,
		    price_cents   bigint
		)`); err != nil {
		return fmt.Errorf("creating orders_flat: %w", err)
	}

	// Two orders from the same customer, three lines between them, two of them the same book. So
	// the customer's city appears three times and the book's author twice, which is what makes the
	// anomalies possible.
	return Refill(ctx, q)
}

// Refill puts the fixture rows back, for a test that emptied the table.
//
// Split out of CreateUnnormalised rather than duplicated, because the fixture rows are the thing every
// assertion in the package counts and two copies of them would drift.
func Refill(ctx context.Context, q Querier) error {
	if _, err := q.Exec(ctx, `
		INSERT INTO orders_flat VALUES
		    (1, 'Ada Lovelace', 'London', 'vip,gift',  'The Quick River',  'Iris Chen', 2, 1200),
		    (1, 'Ada Lovelace', 'London', 'vip,gift',  'The Silent Garden','Omar Diaz', 1,  900),
		    (2, 'Ada Lovelace', 'London', 'vip',       'The Quick River',  'Iris Chen', 3, 1200)`); err != nil {
		return fmt.Errorf("filling orders_flat: %w", err)
	}

	return nil
}

// Tags splits the 1NF-violating column.
//
// This function is the smell. Splitting a column in application code means the database cannot index it,
// cannot constrain it, cannot join on it, and cannot stop 'vip' and 'VIP ' being different tags. Every
// query filtering on a tag becomes a LIKE with wildcards on both sides, which the indexes package showed
// cannot use a B-tree.
func Tags(s string) []string {
	if s == "" {
		return nil
	}

	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))

	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}

// CityUpdateAnomaly is what happens when the same fact lives in three rows.
//
// It updates the city for ONE of the customer's rows, which is what a query written against the wrong row
// identifier does. Afterwards the customer lives in two cities and the database is content.
func CityUpdateAnomaly(ctx context.Context, q Querier, orderID int64, city string) error {
	// A WHERE that looks specific and is not. This is the bug: order_id is not a key of this
	// table, so "the customer on order 1" is two rows and this updates both of them but not the
	// third.
	if _, err := q.Exec(ctx,
		"UPDATE orders_flat SET customer_city = $1 WHERE order_id = $2", city, orderID); err != nil {
		return fmt.Errorf("updating the city: %w", err)
	}

	return nil
}

// DistinctCities counts how many cities one customer is recorded as living in.
func DistinctCities(ctx context.Context, q Querier, name string) ([]string, error) {
	rows, err := q.Query(ctx,
		"SELECT DISTINCT customer_city FROM orders_flat WHERE customer_name = $1 ORDER BY 1", name)
	if err != nil {
		return nil, fmt.Errorf("counting cities for %s: %w", name, err)
	}

	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (string, error) {
		var city string
		err := r.Scan(&city)
		return city, err
	})
}

// Normalise creates the 3NF version of the same data.
//
// Four tables where there was one. That is the cost, stated plainly:
//
//	n_customers  one row per customer, so the city is stored once
//	n_tags       one row per (customer, tag), which is what 1NF means for a list
//	n_books      one row per book, so the author is stored once
//	n_orders     the order header
//	n_lines      one row per line, referencing a book
//
// The quantity stays on the line because it genuinely depends on (order, book), and the price is copied
// onto the line ON PURPOSE. That copy is not a normalisation failure: the price of a book today is not the
// price it was sold at, and an order that reprices itself when the catalogue changes is a much worse bug
// than a duplicated number. Knowing which duplicates are facts about a moment in time is the part the
// normal forms do not tell you.
func Normalise(ctx context.Context, q Querier) error {
	statements := []string{
		`CREATE TEMP TABLE n_customers (
		     id   bigserial PRIMARY KEY,
		     name text NOT NULL UNIQUE,
		     city text NOT NULL
		 )`,

		// The composite primary key is what makes a tag list a set: the same tag cannot be
		// attached twice, and the database enforces it rather than the application remembering
		// to.
		`CREATE TEMP TABLE n_tags (
		     customer_id bigint NOT NULL REFERENCES n_customers(id) ON DELETE CASCADE,
		     tag         text   NOT NULL,
		     PRIMARY KEY (customer_id, tag)
		 )`,

		`CREATE TEMP TABLE n_books (
		     id     bigserial PRIMARY KEY,
		     title  text NOT NULL UNIQUE,
		     author text NOT NULL
		 )`,

		`CREATE TEMP TABLE n_orders (
		     id          bigserial PRIMARY KEY,
		     customer_id bigint NOT NULL REFERENCES n_customers(id)
		 )`,

		`CREATE TEMP TABLE n_lines (
		     order_id    bigint NOT NULL REFERENCES n_orders(id) ON DELETE CASCADE,
		     book_id     bigint NOT NULL REFERENCES n_books(id),
		     quantity    int    NOT NULL CHECK (quantity > 0),
		     price_cents bigint NOT NULL,
		     PRIMARY KEY (order_id, book_id)
		 )`,

		`INSERT INTO n_customers (name, city)
		 SELECT DISTINCT customer_name, customer_city FROM orders_flat`,

		// unnest(string_to_array(...)) is the SQL for "split this column", and needing it is
		// the sign the column should never have held a list.
		`INSERT INTO n_tags (customer_id, tag)
		 SELECT DISTINCT c.id, trim(t)
		   FROM orders_flat f
		   JOIN n_customers c ON c.name = f.customer_name
		   CROSS JOIN unnest(string_to_array(f.tags, ',')) AS t
		  WHERE trim(t) <> ''`,

		`INSERT INTO n_books (title, author)
		 SELECT DISTINCT book_title, book_author FROM orders_flat`,

		`INSERT INTO n_orders (id, customer_id)
		 SELECT DISTINCT f.order_id, c.id
		   FROM orders_flat f JOIN n_customers c ON c.name = f.customer_name`,

		`INSERT INTO n_lines (order_id, book_id, quantity, price_cents)
		 SELECT f.order_id, b.id, sum(f.quantity), max(f.price_cents)
		   FROM orders_flat f JOIN n_books b ON b.title = f.book_title
		  GROUP BY f.order_id, b.id`,
	}

	for i, sql := range statements {
		if _, err := q.Exec(ctx, sql); err != nil {
			return fmt.Errorf("normalising, statement %d: %w", i+1, err)
		}
	}

	return nil
}

// SetCity updates a customer's city in the normalised schema.
//
// One row. There is no way to write this wrongly, which is the entire argument for 3NF: the anomaly is
// not prevented by discipline, it is prevented by there being only one place to write.
func SetCity(ctx context.Context, q Querier, name, city string) error {
	tag, err := q.Exec(ctx, "UPDATE n_customers SET city = $1 WHERE name = $2", city, name)
	if err != nil {
		return fmt.Errorf("setting the city for %s: %w", name, err)
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("no customer named %q", name)
	}

	return nil
}

// FlatView reassembles the original shape from the normalised tables.
//
// This is the cost: four joins to get back what one table held. Worth writing out so the trade is visible
// rather than asserted, and worth noticing that the view is now a DERIVED thing that cannot disagree with
// itself.
func FlatView(ctx context.Context, q Querier) ([]Unnormalised, error) {
	rows, err := q.Query(ctx, `
		SELECT o.id,
		       c.name,
		       c.city,
		       coalesce(string_agg(DISTINCT t.tag, ',' ORDER BY t.tag), ''),
		       b.title,
		       b.author,
		       l.quantity,
		       l.price_cents
		  FROM n_orders o
		  JOIN n_customers c ON c.id = o.customer_id
		  JOIN n_lines l     ON l.order_id = o.id
		  JOIN n_books b     ON b.id = l.book_id
		  LEFT JOIN n_tags t ON t.customer_id = c.id
		 GROUP BY o.id, c.name, c.city, b.title, b.author, l.quantity, l.price_cents
		 ORDER BY o.id, b.title`)
	if err != nil {
		return nil, fmt.Errorf("rebuilding the flat view: %w", err)
	}

	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Unnormalised, error) {
		var u Unnormalised
		err := r.Scan(&u.OrderID, &u.CustomerName, &u.CustomerCity, &u.Tags,
			&u.BookTitle, &u.BookAuthor, &u.Quantity, &u.PriceCents)
		return u, err
	})
}

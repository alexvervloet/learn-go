package normalization

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/alexvervloet/learn-go/backends/learning/database-concepts/dbtest"
)

// flat gives a transaction with the unnormalised table in it.
//
// Every test here uses TEMP tables, which belong to the session. A pooled connection means the session is
// whatever connection the transaction holds, so the table has to be created and used inside one
// transaction. That is also why these tests need no cleanup: the rollback takes the tables with it.
func flat(t *testing.T) pgx.Tx {
	t.Helper()

	tx := dbtest.Tx(t)
	ctx := context.Background()

	if err := CreateUnnormalised(ctx, tx); err != nil {
		t.Fatal(err)
	}

	return tx
}

// TestFirstNormalFormIsAboutIndexability, not tidiness.
func TestFirstNormalFormIsAboutIndexability(t *testing.T) {
	tx := flat(t)
	ctx := context.Background()

	// Finding every VIP in the flat table.
	var flatVIPs int
	if err := tx.QueryRow(ctx,
		"SELECT count(DISTINCT customer_name) FROM orders_flat WHERE tags LIKE '%vip%'").
		Scan(&flatVIPs); err != nil {
		t.Fatal(err)
	}

	t.Logf("the flat table finds %d VIP customer(s), with a leading-wildcard LIKE", flatVIPs)

	// And the reason that is not acceptable: the same query matches things it should not.
	if _, err := tx.Exec(ctx, `
		INSERT INTO orders_flat VALUES
		    (3, 'Not A VIP', 'Leeds', 'invipid', 'The Quick River', 'Iris Chen', 1, 1200)`); err != nil {
		t.Fatal(err)
	}

	var falsePositives int
	if err := tx.QueryRow(ctx,
		"SELECT count(DISTINCT customer_name) FROM orders_flat WHERE tags LIKE '%vip%'").
		Scan(&falsePositives); err != nil {
		t.Fatal(err)
	}

	t.Logf("after adding a customer tagged 'invipid', the same query finds %d", falsePositives)

	if falsePositives <= flatVIPs {
		t.Errorf("expected the substring match to pick up a false positive, got %d then %d",
			flatVIPs, falsePositives)
	}

	// Go-side splitting gets the right answer and cannot be indexed, constrained or joined.
	tags := Tags("vip,gift")
	if len(tags) != 2 || tags[0] != "vip" || tags[1] != "gift" {
		t.Errorf("Tags gave %v", tags)
	}

	if got := Tags("  vip , , gift  "); len(got) != 2 {
		t.Errorf("Tags did not trim and drop empties: %q", got)
	}
	if got := Tags(""); got != nil {
		t.Errorf("Tags on an empty string should be nil, got %v", got)
	}

	// The normalised version: one row per tag, a primary key that makes it a set, and an exact
	// match instead of a wildcard.
	if err := Normalise(ctx, tx); err != nil {
		t.Fatal(err)
	}

	var exactVIPs int
	if err := tx.QueryRow(ctx,
		"SELECT count(*) FROM n_tags WHERE tag = 'vip'").Scan(&exactVIPs); err != nil {
		t.Fatal(err)
	}

	t.Logf("n_tags finds %d with tag = 'vip', an equality that an index can serve", exactVIPs)

	// And the same tag cannot be attached twice.
	var customerID int64
	if err := tx.QueryRow(ctx,
		"SELECT id FROM n_customers WHERE name = 'Ada Lovelace'").Scan(&customerID); err != nil {
		t.Fatal(err)
	}

	_, err := tx.Exec(ctx, "INSERT INTO n_tags (customer_id, tag) VALUES ($1, 'vip')", customerID)
	if err == nil {
		t.Error("n_tags accepted a duplicate tag, so the primary key is not doing its job")
	} else {
		t.Logf("a duplicate tag is rejected by the schema: %v", err)
	}
}

// TestUpdateAnomaly is the one that costs money in real systems.
func TestUpdateAnomaly(t *testing.T) {
	tx := flat(t)
	ctx := context.Background()

	before, err := DistinctCities(ctx, tx, "Ada Lovelace")
	if err != nil {
		t.Fatal(err)
	}

	if len(before) != 1 {
		t.Fatalf("the fixture should start with one city, got %v", before)
	}

	t.Logf("before: the customer lives in %v", before)

	// The update a developer writes when they think order_id identifies a customer.
	if err := CityUpdateAnomaly(ctx, tx, 1, "Manchester"); err != nil {
		t.Fatal(err)
	}

	after, err := DistinctCities(ctx, tx, "Ada Lovelace")
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("after updating order 1's rows: the customer lives in %v", after)

	if len(after) != 2 {
		t.Errorf("expected two cities after the anomaly, got %v", after)
	}

	// Nothing complained. The table has no constraint that could have complained, because the
	// constraint it needs ("one customer, one city") is not expressible on this shape.
	var rowsWrong int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM orders_flat
		 WHERE customer_name = 'Ada Lovelace' AND customer_city <> 'Manchester'`).
		Scan(&rowsWrong); err != nil {
		t.Fatal(err)
	}

	t.Logf("%d row(s) still say the old city, and the database is content", rowsWrong)

	// The normalised version makes the same mistake impossible: there is one row to write. That is
	// TestNormalisedDataRoundTrips.
	//
	// Normalising THIS data is not possible at all, because n_customers.name is UNIQUE and the
	// table now holds two cities for one name. That failure is the next test, and it is the more
	// useful half of the lesson.
}

// TestNormalisingInconsistentDataFails is the point above, isolated.
//
// This is the most practically useful thing in the package: the migration that adds the constraint is the
// thing that tells you the data was already wrong, and it fails in production at 2am rather than in
// review.
func TestNormalisingInconsistentDataFails(t *testing.T) {
	tx := flat(t)
	ctx := context.Background()

	// Break it first.
	if err := CityUpdateAnomaly(ctx, tx, 1, "Manchester"); err != nil {
		t.Fatal(err)
	}

	// Then try to normalise with the UNIQUE constraint in place. SELECT DISTINCT
	// (customer_name, customer_city) now yields two rows for one name.
	err := Normalise(ctx, tx)

	if err == nil {
		t.Fatal("normalising inconsistent data succeeded, which means the UNIQUE constraint " +
			"on n_customers.name is missing")
	}

	if !strings.Contains(err.Error(), "duplicate key") {
		t.Errorf("expected a unique violation, got %v", err)
	}

	t.Logf("the migration fails rather than silently picking a city: %v", err)
	t.Log("which is the right outcome, and is why adding a constraint to an existing table is " +
		"a data-cleaning job before it is a schema change")
}

// TestNormalisedDataRoundTrips: the flat shape is still available, it is just derived now.
func TestNormalisedDataRoundTrips(t *testing.T) {
	tx := flat(t)
	ctx := context.Background()

	if err := Normalise(ctx, tx); err != nil {
		t.Fatal(err)
	}

	rebuilt, err := FlatView(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("%d rows rebuilt from four tables", len(rebuilt))
	for _, u := range rebuilt {
		t.Logf("  order %d  %-14s %-10s %-10s %-18s %-10s q=%d %d",
			u.OrderID, u.CustomerName, u.CustomerCity, u.Tags,
			u.BookTitle, u.BookAuthor, u.Quantity, u.PriceCents)
	}

	// The fixture has three lines, two of them the same book in different orders, so the
	// normalised form has three lines too.
	if len(rebuilt) != 3 {
		t.Errorf("expected 3 lines, got %d", len(rebuilt))
	}

	for _, u := range rebuilt {
		if u.CustomerCity != "London" {
			t.Errorf("order %d has city %q", u.OrderID, u.CustomerCity)
		}
		if u.Tags == "" {
			t.Errorf("order %d lost its tags", u.OrderID)
		}
	}

	// And the round trip is NOT lossless, which the test found and I had not planned for. In the
	// flat table order 1 carries 'vip,gift' and order 2 carries 'vip'. The normalised schema hangs
	// tags off the CUSTOMER, so both orders come back with 'gift,vip'.
	//
	// That is not a bug in the normalisation. It is the normalisation asking a question the flat
	// shape let everyone avoid: does a tag describe the customer or the order? The flat table
	// stored an answer to neither, and any choice here changes what the data means.
	//
	// It is the most useful thing normalising does. Deciding what each fact is ABOUT is the work,
	// and the normal forms are just the notation for having done it.
	for _, u := range rebuilt {
		if u.OrderID == 2 && u.Tags != "gift,vip" {
			t.Errorf("order 2 came back with tags %q; the fixture gave it 'vip' alone, "+
				"and tags now belong to the customer", u.Tags)
		}
	}

	t.Log("order 2 came back tagged gift,vip where the flat table said vip: tags are now a " +
		"fact about the customer, which is a decision the flat shape let us skip")

	// Now the single-row update, and every rebuilt row agrees.
	if err := SetCity(ctx, tx, "Ada Lovelace", "Manchester"); err != nil {
		t.Fatal(err)
	}

	after, err := FlatView(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}

	for _, u := range after {
		if u.CustomerCity != "Manchester" {
			t.Errorf("order %d still says %q after one UPDATE", u.OrderID, u.CustomerCity)
		}
	}

	t.Log("one UPDATE, three rows changed, no way to change only some of them")

	// SetCity on a name that does not exist is an error rather than a silent no-op, which is the
	// difference between a 404 and a bug report six weeks later.
	if err := SetCity(ctx, tx, "Nobody", "Leeds"); err == nil {
		t.Error("SetCity silently did nothing for an unknown customer")
	}
}

// TestDeleteAnomaly: removing one fact should not remove another.
func TestDeleteAnomaly(t *testing.T) {
	tx := flat(t)
	ctx := context.Background()

	// The only record that 'Omar Diaz' wrote 'The Silent Garden' is a row of an order. Cancel the
	// order and the book's author is gone from the database.
	var authorsBefore int
	if err := tx.QueryRow(ctx,
		"SELECT count(DISTINCT book_author) FROM orders_flat").Scan(&authorsBefore); err != nil {
		t.Fatal(err)
	}

	if _, err := tx.Exec(ctx,
		"DELETE FROM orders_flat WHERE order_id = 1 AND book_title = 'The Silent Garden'"); err != nil {
		t.Fatal(err)
	}

	var authorsAfter int
	if err := tx.QueryRow(ctx,
		"SELECT count(DISTINCT book_author) FROM orders_flat").Scan(&authorsAfter); err != nil {
		t.Fatal(err)
	}

	t.Logf("deleting one order line took the database from %d known authors to %d",
		authorsBefore, authorsAfter)

	if authorsAfter >= authorsBefore {
		t.Errorf("expected to lose an author, went from %d to %d", authorsBefore, authorsAfter)
	}

	// In the normalised schema the book outlives the order line, because ON DELETE CASCADE runs
	// from orders to lines and stops there. n_lines references n_books with no CASCADE, so
	// deleting a book that has been ordered is REFUSED, which is the other half of the guarantee.
	if _, err := tx.Exec(ctx, "DELETE FROM orders_flat"); err != nil {
		t.Fatal(err)
	}
	if err := Refill(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err := Normalise(ctx, tx); err != nil {
		t.Fatal(err)
	}

	if _, err := tx.Exec(ctx, "DELETE FROM n_orders WHERE id = 1"); err != nil {
		t.Fatal(err)
	}

	var books, lines int
	if err := tx.QueryRow(ctx,
		"SELECT (SELECT count(*) FROM n_books), (SELECT count(*) FROM n_lines)").
		Scan(&books, &lines); err != nil {
		t.Fatal(err)
	}

	t.Logf("after deleting order 1: %d lines left, and all %d books still known", lines, books)

	if books != 2 {
		t.Errorf("expected both books to survive, got %d", books)
	}

	// And a book that has been ordered cannot be deleted, so history stays intact.
	_, err := tx.Exec(ctx, "DELETE FROM n_books WHERE title = 'The Quick River'")
	if err == nil {
		t.Error("deleted a book that an order line references")
	} else {
		t.Logf("deleting a referenced book is refused: %v", err)
	}
}

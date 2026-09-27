-- +goose Up
-- +goose StatementBegin

-- Every index here exists because a query in this module needs it, and the indexes/ package
-- measures what each one is worth. An index nobody can name a query for is a write
-- amplification with no upside.

-- A foreign key does NOT create an index on the referencing side. Postgres indexes the
-- referenced primary key and nothing else, so "find every book by this author" is a
-- sequential scan until you add this. It is the single most common missing index in any
-- schema.
CREATE INDEX idx_books_author ON books (author_id);
CREATE INDEX idx_orders_customer ON orders (customer_id);
CREATE INDEX idx_order_items_book ON order_items (book_id);

-- A COMPOSITE index, and the column order is the whole decision. This serves
-- "recent orders for a customer" because customer_id is an equality filter and placed_at
-- is the range. Reversed, it cannot serve that query at all: the leading column has to be
-- the one you filter on exactly.
CREATE INDEX idx_orders_customer_placed ON orders (customer_id, placed_at DESC);

-- A PARTIAL index. Most orders end up paid or shipped, so an index covering only the
-- pending ones is a fraction of the size and serves the query the dashboard actually runs.
CREATE INDEX idx_orders_pending ON orders (placed_at DESC) WHERE status = 'pending';

-- A GIN index for the generated tsvector column. GIN rather than GiST: GIN is slower to
-- build and update and much faster to search, which is the right trade for a corpus that
-- is read far more than written.
CREATE INDEX idx_books_search ON books USING gin (search);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_books_search;
DROP INDEX IF EXISTS idx_orders_pending;
DROP INDEX IF EXISTS idx_orders_customer_placed;
DROP INDEX IF EXISTS idx_order_items_book;
DROP INDEX IF EXISTS idx_orders_customer;
DROP INDEX IF EXISTS idx_books_author;
-- +goose StatementEnd

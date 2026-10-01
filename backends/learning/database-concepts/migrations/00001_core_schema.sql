-- +goose Up
-- +goose StatementBegin

-- Extensions first. citext gives a case-insensitive text type, which is the right answer for
-- an email column: lower(email) with a functional unique index also works and forces every
-- query to remember the lower().
CREATE EXTENSION IF NOT EXISTS citext;

-- The normalised schema every other package in this module queries.
--
-- Normalisation, briefly:
--
--   1NF  every column holds one value. No comma-separated lists, no array standing in for
--        a relationship.
--   2NF  every non-key column depends on the WHOLE key. Splitting order_items out of
--        orders is this: a line's quantity depends on (order, book), not on the order.
--   3NF  no non-key column depends on another non-key column. Storing a line's total next
--        to its quantity and unit price breaks it, because total is derivable.
--
-- Where this schema deliberately breaks 3NF: orders.total_cents. The trigger below keeps it
-- correct, and a denormalised column with no mechanism to keep it correct is a bug waiting
-- for a deploy. The justification is read cost: an order's total without a SUM over its lines.

CREATE TABLE authors (
    id          bigserial PRIMARY KEY,
    name        text        NOT NULL,
    country     text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE books (
    id          bigserial PRIMARY KEY,
    author_id   bigint      NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
    title       text        NOT NULL,
    isbn        text        NOT NULL UNIQUE,
    published   date        NOT NULL,
    price_cents integer     NOT NULL CHECK (price_cents >= 0),
    -- A GENERATED column rather than a trigger-maintained one: Postgres keeps it correct and
    -- it cannot drift. Available since 12, and the right choice whenever the expression is
    -- immutable. to_tsvector('english', ...) is immutable; to_tsvector(title) without an
    -- explicit configuration is not, and Postgres rejects it here, which is a useful error.
    search      tsvector    GENERATED ALWAYS AS (to_tsvector('english', title)) STORED,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE customers (
    id          bigserial PRIMARY KEY,
    email       citext      NOT NULL UNIQUE,
    name        text        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE orders (
    id           bigserial PRIMARY KEY,
    customer_id  bigint      NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    placed_at    timestamptz NOT NULL DEFAULT now(),
    status       text        NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled')),
    total_cents  integer     NOT NULL DEFAULT 0
);

COMMENT ON COLUMN orders.total_cents IS
    'Denormalised sum of the order lines, maintained by trg_order_items_total. Exists so an '
    'order list can be read without joining every line; see the module README for the '
    'measurement.';

CREATE TABLE order_items (
    order_id    bigint  NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    book_id     bigint  NOT NULL REFERENCES books(id),
    quantity    integer NOT NULL CHECK (quantity > 0),
    unit_cents  integer NOT NULL CHECK (unit_cents >= 0),
    -- A composite primary key, which is what 2NF asks for: the same book twice in one order
    -- is one row with a quantity, not two rows.
    PRIMARY KEY (order_id, book_id)
);

-- Money, for the transactions package.
CREATE TABLE accounts (
    id            bigserial PRIMARY KEY,
    owner         text   NOT NULL,
    balance_cents bigint NOT NULL CHECK (balance_cents >= 0)
);

-- +goose StatementEnd

-- +goose StatementBegin
-- The trigger that keeps the denormalised total honest.
--
-- Recomputing from scratch rather than adding a delta: a delta is faster and gets the
-- UPDATE case wrong, because OLD and NEW both matter and the arithmetic has to handle a
-- quantity change, a price change, or both. A full recompute of one order's lines is a few
-- index reads and cannot drift.
CREATE OR REPLACE FUNCTION recompute_order_total() RETURNS trigger AS $$
DECLARE
    target bigint;
BEGIN
    -- On DELETE there is no NEW row, so the order id comes from OLD.
    target := COALESCE(NEW.order_id, OLD.order_id);

    UPDATE orders
       SET total_cents = COALESCE((
               SELECT SUM(quantity * unit_cents)
                 FROM order_items
                WHERE order_id = target
           ), 0)
     WHERE id = target;

    RETURN NULL; -- an AFTER trigger's return value is ignored
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_order_items_total
    AFTER INSERT OR UPDATE OR DELETE ON order_items
    FOR EACH ROW EXECUTE FUNCTION recompute_order_total();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_order_items_total ON order_items;
DROP FUNCTION IF EXISTS recompute_order_total();
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS order_items;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS customers;
DROP TABLE IF EXISTS books;
DROP TABLE IF EXISTS authors;
-- +goose StatementEnd

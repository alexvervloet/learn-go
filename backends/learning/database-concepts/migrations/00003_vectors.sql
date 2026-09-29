-- +goose Up
-- +goose StatementBegin

-- The vector extension, which is pgvector. Separate migration from the core schema because it is the
-- one thing here that a managed Postgres may not have: RDS and Cloud SQL both ship it now, but a
-- self-hosted 15 without the contrib package does not, and a migration that fails halfway is worse than
-- one that fails on its first statement.
CREATE EXTENSION IF NOT EXISTS vector;

-- +goose StatementEnd

-- +goose StatementBegin

-- Embeddings for the books, one row per book.
--
-- A separate table rather than a column on books, and the reason is ROW WIDTH. A vector(384) is 384 * 4 +
-- 8 = 1544 bytes, larger than the whole rest of a book row. It is under the ~2 KB TOAST threshold, so it
-- is stored inline (pg_column_size says 1544, and the TOAST table stays empty), which means a books row
-- carrying it would be several times wider: fewer rows per 8 KB page, and every scan of books reads that
-- many more pages. In their own table, "list the books" never reads a vector. (An earlier version of this
-- comment gave TOAST as the reason. At 384 dimensions TOAST never happens; at 1536, it would.)
--
-- 384 dimensions because that is what all-MiniLM-L6-v2 produces, which is the model most people reach
-- for first. OpenAI's text-embedding-3-small is 1536, and the arithmetic scales: 10,000 rows at 1536
-- dimensions is about 62 MB (59 MiB) of vectors before any index.
CREATE TABLE book_embeddings (
    book_id   bigint PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE,
    embedding vector(384) NOT NULL,

    -- Which model produced it. Not decoration: embeddings from different models are not
    -- comparable, and a table mixing them returns nonsense with no error. A model column plus a
    -- filter is the cheapest guard there is.
    model text NOT NULL DEFAULT 'synthetic-384'
);

-- +goose StatementEnd

-- +goose StatementBegin

-- No index here on purpose.
--
-- An index on a vector column is APPROXIMATE, which makes it different from every other index in this
-- schema: adding it changes the ANSWER, not just the speed. So the vectors package builds its indexes
-- inside its tests, measures the recall it loses, and this migration leaves the table exact.
COMMENT ON TABLE book_embeddings IS
    'Vectors with no index: exact search. The vectors package builds HNSW and IVFFlat indexes in its tests and measures the recall each one costs.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS book_embeddings;
-- +goose StatementEnd

-- +goose StatementBegin
-- The extension is deliberately NOT dropped. Another schema in the same database may depend on it, and
-- a down migration that removes a shared extension is how a rollback breaks something unrelated.
-- +goose StatementEnd

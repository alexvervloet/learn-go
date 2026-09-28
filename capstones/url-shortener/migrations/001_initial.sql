-- +goose Up
-- +goose StatementBegin

-- Users.
--
-- The email is CITEXT rather than TEXT with a lower() index. Email addresses are case-insensitive in the part
-- that matters, and making the column do it means no query can forget: a lookup for Alex@example.com finds the
-- row stored as alex@example.com without the caller lowercasing first.
--
-- The alternative, a unique index on lower(email), works and puts the rule in one place that every query has to
-- know about. This puts it in the type.
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    email         CITEXT NOT NULL UNIQUE,

    -- The bcrypt hash, which is 60 ASCII characters and carries its own salt and cost inside it. There is no
    -- separate salt column for that reason, and no length constraint, because a future move to argon2 produces
    -- a longer string and a CHECK here would block the migration.
    password_hash TEXT NOT NULL,

    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- URLs.
CREATE TABLE urls (
    id          BIGSERIAL PRIMARY KEY,

    -- The slug is its own column rather than being derived from the id at read time.
    --
    -- That costs an index and buys two things: a custom slug lives in the same column as a generated one, so
    -- the lookup path is identical; and the id-to-slug mapping can change without breaking every link ever
    -- issued.
    slug        TEXT NOT NULL UNIQUE,

    target      TEXT NOT NULL,

    -- ON DELETE CASCADE, so deleting a user deletes their URLs.
    --
    -- The alternative, ON DELETE RESTRICT, means a user cannot be deleted until their URLs are, which is the
    -- right default for anything financial and the wrong one here. Choosing is mandatory: the default is NO
    -- ACTION, which fails the delete with an error at the end of the statement and surprises everyone.
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ,

    -- A denormalised counter, updated by the worker.
    --
    -- The alternative is COUNT(*) over the clicks table on every read, which is correct and gets slower as the
    -- table grows. This is the trade every analytics feature makes, and the cost is that the two can disagree
    -- if an update is lost. The clicks table is the source of truth and this is a cache of it.
    click_count BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX urls_user_id_created_at_idx ON urls (user_id, created_at DESC);

-- Partial, because the only query that uses it asks for the ones that expire.
-- A full index would carry a row for every URL that never expires, which is most of them.
CREATE INDEX urls_expires_at_idx ON urls (expires_at) WHERE expires_at IS NOT NULL;

-- Clicks.
CREATE TABLE clicks (
    id         BIGSERIAL PRIMARY KEY,
    url_id     BIGINT NOT NULL REFERENCES urls(id) ON DELETE CASCADE,
    clicked_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Deliberately not the full IP.
    --
    -- A URL shortener does not need to know who clicked, and storing an IP makes the table personal data with
    -- everything that follows. The referrer and user agent are kept because they answer "where is this link
    -- being shared", which is the actual question.
    referrer   TEXT,
    user_agent TEXT
);

CREATE INDEX clicks_url_id_clicked_at_idx ON clicks (url_id, clicked_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE clicks;
DROP TABLE urls;
DROP TABLE users;
-- +goose StatementEnd

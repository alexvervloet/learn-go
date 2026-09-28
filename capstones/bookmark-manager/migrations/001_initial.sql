-- +goose Up
-- +goose StatementBegin

CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    email         CITEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Refresh tokens, stored.
--
-- # Why these are in the database when the access token is not
--
-- An access token is stateless on purpose: no lookup per request. The price is that it cannot be revoked, so
-- it is short-lived and you live with being wrong for a few minutes.
--
-- A refresh token lives for weeks. Being unable to revoke one for weeks is not a trade anybody wants, so it is
-- a row, and logging out is a DELETE.
--
-- # The hash, not the token
--
-- A stolen database of refresh tokens is a stolen set of sessions. Storing the SHA-256 means the database holds
-- something that cannot be replayed. SHA-256 rather than bcrypt because the token is 32 bytes of CSPRNG output
-- rather than a human password: there is nothing to brute force, and a lookup on every refresh should not cost
-- 250ms.
CREATE TABLE refresh_tokens (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL UNIQUE,
    issued_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,

    -- The rotation chain. When a token is used, it is marked used and a new one is issued whose parent is this
    -- one. A SECOND use of an already-used token means the token was stolen, because a legitimate client threw
    -- its copy away, and the correct response is to revoke the whole chain.
    used_at    TIMESTAMPTZ,
    parent_id  BIGINT REFERENCES refresh_tokens(id) ON DELETE SET NULL,

    -- The family is the whole chain from one login. Revoking a family logs that session out everywhere without
    -- touching the user's other sessions.
    family     UUID NOT NULL,
    revoked_at TIMESTAMPTZ
);

CREATE INDEX refresh_tokens_family_idx ON refresh_tokens (family);
CREATE INDEX refresh_tokens_user_id_idx ON refresh_tokens (user_id);

-- Partial: the sweep only cares about the ones that have expired and not been cleaned up.
CREATE INDEX refresh_tokens_expires_at_idx ON refresh_tokens (expires_at) WHERE revoked_at IS NULL;

CREATE TABLE categories (
    id      BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name    TEXT NOT NULL,

    -- Unique PER USER, not globally. Two people can both have a "Reading" category, and the composite unique
    -- is what says so. A global UNIQUE (name) would be a first-come-first-served land grab.
    UNIQUE (user_id, name)
);

CREATE TABLE tags (
    id      BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name    CITEXT NOT NULL,

    UNIQUE (user_id, name)
);

CREATE TABLE bookmarks (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- ON DELETE SET NULL, not CASCADE.
    --
    -- Deleting a category must not delete the bookmarks in it. That is the kind of mistake that is discovered
    -- by a support ticket, and the cascade that felt tidy in the migration is the reason.
    category_id BIGINT REFERENCES categories(id) ON DELETE SET NULL,

    url         TEXT NOT NULL,
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- The search vector, as a GENERATED column.
    --
    -- # Why generated and not a trigger
    --
    -- A trigger is the classic answer and it is code that can be forgotten on an INSERT path, disabled by a
    -- bulk load, or written to use a different text search configuration from the query. A generated column is
    -- maintained by the storage engine and cannot drift.
    --
    -- STORED, not VIRTUAL: Postgres only has STORED, and a GIN index needs the value on disk anyway.
    --
    -- # The weights
    --
    -- setweight marks which field each lexeme came from, A through D, and ts_rank uses them. A match in the
    -- title should outrank a match in the description, and without weights it does not.
    --
    -- # coalesce is not optional
    --
    -- to_tsvector(NULL) is NULL, and NULL || anything is NULL, so one NULL field makes the whole vector NULL
    -- and the row becomes unsearchable. description has a NOT NULL default for this reason and the coalesce is
    -- belt and braces.
    search_vector TSVECTOR GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(description, '')), 'B')
    ) STORED,

    -- One user cannot save the same URL twice. A different user can.
    UNIQUE (user_id, url)
);

CREATE INDEX bookmarks_user_id_created_at_idx ON bookmarks (user_id, created_at DESC, id DESC);
CREATE INDEX bookmarks_category_id_idx ON bookmarks (category_id);

-- GIN on (user_id, search_vector), not on search_vector alone.
--
-- # Why GIN and not GiST
--
-- For a tsvector, GIN is larger and slower to update and much faster to search, which is the right trade for
-- data that is read far more than it is written. GiST is lossy and rechecks.
--
-- # Why user_id is in the index, and why that needs an extension
--
-- Every search is `WHERE user_id = $1 AND search_vector @@ ...`. A GIN index cannot normally contain a scalar
-- column, because there is no GIN operator class for bigint; btree_gin supplies one, so one index can cover
-- both halves of the predicate instead of the planner having to pick an index for one half and filter with the
-- other.
--
-- # What the planner actually does, measured
--
-- It depends on how many rows the user has, and the test in internal/store measures it rather than assuming.
-- With 500 bookmarks for the user, Postgres reads the btree on (user_id, created_at, id), fetches all 500 and
-- filters by the tsvector. That is the CHEAPER plan and the planner is right: 500 rows is nothing.
--
-- With several thousand, it switches to this index and filters by user_id afterwards. That is the plan this
-- index exists for, and it is the case that matters, because a user with 500 bookmarks does not need search.
--
-- The lesson is that "is the index used" has no answer without a row count. A test asserting the plan on a
-- fixture of 500 rows asserts something true about 500 rows and nothing about the schema.
CREATE EXTENSION IF NOT EXISTS btree_gin;

CREATE INDEX bookmarks_search_idx ON bookmarks USING GIN (user_id, search_vector);

-- The join table.
CREATE TABLE bookmark_tags (
    bookmark_id BIGINT NOT NULL REFERENCES bookmarks(id) ON DELETE CASCADE,
    tag_id      BIGINT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,

    -- The composite primary key IS the uniqueness constraint, so there is no surrogate id. A join table with
    -- its own BIGSERIAL id and a separate unique index is a column and an index nothing reads.
    --
    -- The column ORDER matters: this primary key's index serves "the tags of this bookmark" and not "the
    -- bookmarks with this tag", which is why there is a second index below.
    PRIMARY KEY (bookmark_id, tag_id)
);

CREATE INDEX bookmark_tags_tag_id_idx ON bookmark_tags (tag_id, bookmark_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE bookmark_tags;
DROP TABLE bookmarks;
DROP TABLE tags;
DROP TABLE categories;
DROP TABLE refresh_tokens;
DROP TABLE users;
-- +goose StatementEnd

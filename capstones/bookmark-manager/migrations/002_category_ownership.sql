-- +goose Up
-- +goose StatementBegin

-- A bookmark's category must belong to the bookmark's owner.
--
-- # What 001 got wrong
--
-- 001 declared `category_id BIGINT REFERENCES categories(id)`. That proves the category EXISTS, and nothing
-- about who owns it. A user who guessed another user's category id could file bookmarks under it, and could
-- enumerate other users' category ids by watching for 201 versus 400. Listing was scoped by user_id, so every
-- read test passed; the hole was on the write path.
--
-- # The fix is a composite key, not a check in Go
--
-- A `SELECT ... WHERE user_id = $1` before the INSERT would work until someone adds a second write path (an
-- import, an update endpoint) and forgets it. The database checks every path, including ones not written yet.
--
-- The foreign key becomes (user_id, category_id) -> categories(user_id, id). A foreign key must point at a
-- unique constraint, and id alone being unique does not make (user_id, id) one as far as Postgres is concerned,
-- so the categories side needs that constraint first. It is redundant as data (id is already unique) and
-- necessary as a target.
--
-- # ON DELETE SET NULL (category_id), with the column list
--
-- A plain ON DELETE SET NULL on a composite key nulls EVERY referencing column, which here includes the
-- bookmark's user_id. That column is NOT NULL, so deleting a category would fail. Postgres 15 added the column
-- list: only category_id is nulled, and the bookmark keeps its owner.
--
-- # Why a new migration and not an edit to 001
--
-- 001 has already run on every database that exists. Editing it changes nothing for them, because goose
-- records it as applied. Fixes go forward.
--
-- # Existing bad rows come first
--
-- ADD CONSTRAINT validates every existing row and refuses the whole migration if one violates it. A database
-- that was running 001 may already hold bookmarks filed under someone else's category, because that is what
-- the bug allowed. The first version of this migration skipped this step and failed on exactly such a row,
-- left behind by the test that proves the bug. Unfile them: the bookmark stays, the foreign category goes.

UPDATE bookmarks AS b
SET    category_id = NULL
FROM   categories AS c
WHERE  b.category_id = c.id
AND    c.user_id <> b.user_id;

ALTER TABLE categories
    ADD CONSTRAINT categories_user_id_id_key UNIQUE (user_id, id);

ALTER TABLE bookmarks
    DROP CONSTRAINT bookmarks_category_id_fkey;

ALTER TABLE bookmarks
    ADD CONSTRAINT bookmarks_category_owner_fkey
    FOREIGN KEY (user_id, category_id) REFERENCES categories (user_id, id)
    ON DELETE SET NULL (category_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE bookmarks
    DROP CONSTRAINT bookmarks_category_owner_fkey;

ALTER TABLE bookmarks
    ADD CONSTRAINT bookmarks_category_id_fkey
    FOREIGN KEY (category_id) REFERENCES categories (id)
    ON DELETE SET NULL;

ALTER TABLE categories
    DROP CONSTRAINT categories_user_id_id_key;

-- +goose StatementEnd

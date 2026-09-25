-- Embedded migrations are the clearest win for go:embed: the schema travels
-- with the binary, so a deploy cannot pick up the wrong version.
CREATE TABLE users (
    id    BIGSERIAL PRIMARY KEY,
    email TEXT NOT NULL UNIQUE
);

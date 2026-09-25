module github.com/example/service/v2

go 1.27

toolchain go1.27.1

require (
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.7.1
	golang.org/x/sync v0.8.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.27.0 // indirect
	golang.org/x/text v0.18.0 // indirect
)

// During development, point at a local checkout. This MUST be removed before
// publishing: a replace with a filesystem path makes the module unbuildable
// for anyone else.
replace github.com/example/internal-lib => ../internal-lib

// Refuse a specific version. Rare, and usually because it was published
// broken and not retracted.
exclude github.com/some/dependency v1.2.3

// Declare our OWN published versions unusable. Consumers running `go get` see
// the reason and skip them.
retract (
	v2.0.0 // published with a broken migration; use v2.0.1
	[v2.1.0, v2.1.2] // panics on an empty result set
)

// Package model holds the domain types the schema binds to.
//
// # Why these are hand-written
//
// gqlgen will generate a struct per type, and those structs are fine for a toy and wrong for a service: they
// have no methods, no validation, and every schema change rewrites them. Binding the schema to types that live
// here means the domain type is the domain type, and gqlgen's job is to map the schema onto it rather than to
// own it.
//
// The cost is that a field the schema declares and this struct does not have becomes a RESOLVER, which is often
// what you want (see Author.Books) and is occasionally a surprise.
package model

// Author is a writer.
type Author struct {
	ID   string
	Name string

	// Books and BookCount are deliberately absent. The schema declares them, so gqlgen generates a resolver
	// interface for each, which is what makes the N+1 visible: resolving a field is a function call, and a
	// function call per author is a query per author unless something batches them.
}

// Book is a book.
type Book struct {
	ID         string
	Title      string
	PriceCents int32

	// AuthorID is here and Author is not, for the same reason: the schema's `author: Author!` becomes a
	// resolver, and that resolver is where the batching happens.
	AuthorID string
}

package resolvers

// The hand-written half of the resolvers package.
//
// This file exists because of where gqlgen draws its line. It owns `{name}.resolvers.go`, and "owns" means it
// REWRITES the file on every generate: it keeps the method bodies whose signatures still match the schema and
// discards everything else in the file. Plain functions sitting at the bottom of schema.resolvers.go are
// everything else, so a regenerate deleted them and the build that gqlgen runs to validate its own output failed
// with `undefined: toBooks`.
//
// Anything that is not a resolver method goes here, in a file gqlgen never opens.

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/alexvervloet/learn-go/backends/learning/graphql-concepts/graph/model"
	"github.com/alexvervloet/learn-go/backends/learning/graphql-concepts/store"
)

func toAuthor(a store.Author) *model.Author {
	return &model.Author{ID: a.ID, Name: a.Name}
}

func toBook(b store.Book) *model.Book {
	return &model.Book{
		ID:         b.ID,
		Title:      b.Title,
		PriceCents: b.PriceCents,
		AuthorID:   b.AuthorID,
	}
}

func toBooks(books []store.Book) []model.Book {
	out := make([]model.Book, 0, len(books))

	for _, b := range books {
		out = append(out, model.Book{
			ID:         b.ID,
			Title:      b.Title,
			PriceCents: b.PriceCents,
			AuthorID:   b.AuthorID,
		})
	}

	return out
}

func strPtr(s string) *string { return &s }

// encodeCursor turns an id into an opaque cursor.
//
// # Why base64 and why opaque
//
// The Relay spec says a cursor is an opaque String. Opaque is the operative word: a client that can read it will
// parse it, and then the cursor format is a public API that cannot change. Base64 does not prevent that, it makes
// it obviously a bad idea.
//
// The prefix is there for the same reason a database has typed ids: a cursor from the books connection handed to
// the authors connection is a client bug, and decoding it into a plausible author id is the worst possible
// outcome. With a prefix it is a clear error.
func encodeCursor(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte("book:" + id))
}

// decodeCursor parses one.
func decodeCursor(cursor string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", fmt.Errorf("invalid cursor: not base64: %w", err)
	}

	id, ok := strings.CutPrefix(string(raw), "book:")
	if !ok {
		return "", errors.New("invalid cursor: not a book cursor")
	}

	return id, nil
}

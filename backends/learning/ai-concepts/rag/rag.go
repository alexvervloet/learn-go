// Package rag is retrieval: splitting documents and finding the relevant pieces.
//
// # What RAG is, without the acronym
//
// The model knows what was in its training data. It does not know your documents, and a context window is not
// a place to put all of them. So: split the documents into pieces, find the few pieces relevant to a question,
// and put those in the prompt.
//
// That is it. The retrieval half is an ordinary search problem and the generation half is an ordinary prompt.
// Most of what makes a RAG system good or bad happens in the retrieval, which is why this package is about
// chunking and ranking and has nothing to do with an API.
//
// # Why chunking is the part that decides everything
//
// A chunk is the unit of retrieval. Too large and every hit drags in paragraphs of irrelevance, which costs
// tokens and dilutes the answer. Too small and a chunk loses the context that made it meaningful: a sentence
// saying "it costs $40 a month" is useless without the sentence naming the product.
//
// The overlap between chunks exists for exactly that: a sentence spanning a boundary is otherwise cut in half
// and neither half retrieves.
//
// This package's vectors are a toy. Real embeddings come from a model and this module does not spend money to
// demonstrate cosine similarity, which is the same arithmetic either way. What is real here is the chunking,
// the ranking and the assembly, and those are where the bugs live.
package rag

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

// Chunk is a piece of a document.
type Chunk struct {
	DocID string
	Index int
	Text  string

	// Start and End are rune offsets into the source, so a citation can point at the original.
	Start int
	End   int
}

// ChunkOptions controls the split.
type ChunkOptions struct {
	// Size is the target in RUNES, not bytes and not tokens.
	//
	// Runes because a byte split can cut a multi-byte character in half and produce invalid UTF-8. Not tokens
	// because counting tokens needs the model's tokenizer, and the rule of thumb (about four characters per
	// token for English, far fewer for other scripts) is close enough for sizing and wrong enough that you
	// should not budget a context window with it.
	Size int

	// Overlap is how many runes each chunk repeats from the one before.
	Overlap int
}

// ErrBadChunkOptions is returned for a configuration that cannot terminate.
var ErrBadChunkOptions = errors.New("rag: overlap must be smaller than size")

// Fixed splits text into fixed-size overlapping chunks.
//
// # The simplest thing, and what it gets wrong
//
// It cuts mid-word and mid-sentence. That is genuinely bad for retrieval, and it is here as the baseline the
// next function is measured against, because "split every N characters" is what everybody writes first.
//
// The guard on overlap is not defensive programming. An overlap equal to the size means the window never
// advances, and the loop does not terminate.
func Fixed(docID, text string, opts ChunkOptions) ([]Chunk, error) {
	if opts.Size <= 0 {
		return nil, fmt.Errorf("%w: size must be positive", ErrBadChunkOptions)
	}

	if opts.Overlap >= opts.Size {
		return nil, fmt.Errorf("%w: size %d, overlap %d, so the window never advances",
			ErrBadChunkOptions, opts.Size, opts.Overlap)
	}

	runes := []rune(text)
	if len(runes) == 0 {
		return nil, nil
	}

	var chunks []Chunk

	step := opts.Size - opts.Overlap

	for start, i := 0, 0; start < len(runes); start, i = start+step, i+1 {
		end := min(start+opts.Size, len(runes))

		chunks = append(chunks, Chunk{
			DocID: docID,
			Index: i,
			Text:  string(runes[start:end]),
			Start: start,
			End:   end,
		})

		if end == len(runes) {
			break
		}
	}

	return chunks, nil
}

// Sentences splits on sentence boundaries, packing sentences into chunks up to Size.
//
// # Why this beats a fixed split
//
// A chunk that ends mid-sentence retrieves badly, because the embedding of half a sentence is not the
// embedding of the idea. Packing whole sentences up to a budget keeps each chunk a complete thought and costs
// nothing except a variable chunk size.
//
// The overlap here is measured in SENTENCES rather than runes, which is the same idea applied to the same unit.
// It is capped at one less than the sentences in the chunk, so every chunk starts at least a sentence later than
// the one before.
//
// The sentence splitter is deliberately naive: a period, question mark or exclamation followed by a space. It
// gets "Dr. Smith" and "e.g." wrong, and a real system uses a proper segmenter. Saying so is better than
// pretending a regex handles English.
func Sentences(docID, text string, opts ChunkOptions) ([]Chunk, error) {
	if opts.Size <= 0 {
		return nil, fmt.Errorf("%w: size must be positive", ErrBadChunkOptions)
	}

	sentences := sentenceSpans(text)
	if len(sentences) == 0 {
		return nil, nil
	}

	var (
		chunks  []Chunk
		current []sentence
		length  int
		index   int
	)

	// Start and End come from the splitter, which knows where each sentence was. Searching the text for the
	// sentence afterwards gives a byte offset where a rune offset was promised, and points every repeat of a
	// sentence ("Yes.", a boilerplate disclaimer) at its first appearance.
	flush := func() {
		if len(current) == 0 {
			return
		}

		texts := make([]string, len(current))
		for i, c := range current {
			texts[i] = c.text
		}

		chunks = append(chunks, Chunk{
			DocID: docID,
			Index: index,
			Text:  strings.Join(texts, " "),
			Start: current[0].start,
			End:   current[len(current)-1].end,
		})

		index++
	}

	for _, s := range sentences {
		runes := s.end - s.start

		// A single sentence longer than the budget goes in alone rather than being dropped or cut. A chunk
		// slightly over budget is better than a chunk that is half a sentence.
		if length+runes > opts.Size && len(current) > 0 {
			flush()

			// Carry the last `Overlap` sentences into the next chunk, but never all of them. Carrying the whole
			// chunk means the next one starts where this one did, and each chunk after that is the previous
			// chunk plus a sentence: the output grows with the square of the document.
			keep := min(opts.Overlap, len(current)-1)
			current = append([]sentence(nil), current[len(current)-keep:]...)

			length = 0
			for _, c := range current {
				length += c.end - c.start
			}
		}

		current = append(current, s)
		length += runes
	}

	flush()

	return chunks, nil
}

// sentence is one sentence and where it sits in the source, in runes, with surrounding whitespace excluded.
type sentence struct {
	text       string
	start, end int
}

// splitSentences is the naive segmenter described above.
func splitSentences(text string) []string {
	spans := sentenceSpans(text)

	out := make([]string, len(spans))
	for i, s := range spans {
		out[i] = s.text
	}

	return out
}

// sentenceSpans does the splitting and keeps each sentence's position.
func sentenceSpans(text string) []sentence {
	var (
		out   []sentence
		start int
	)

	runes := []rune(text)

	emit := func(from, to int) {
		for from < to && unicode.IsSpace(runes[from]) {
			from++
		}

		for to > from && unicode.IsSpace(runes[to-1]) {
			to--
		}

		if from < to {
			out = append(out, sentence{text: string(runes[from:to]), start: from, end: to})
		}
	}

	for i, r := range runes {
		if r != '.' && r != '!' && r != '?' {
			continue
		}

		// A terminator is only a boundary when whitespace or the end of the text follows it. Without this,
		// "3.14" is two sentences.
		if i+1 < len(runes) && !unicode.IsSpace(runes[i+1]) {
			continue
		}

		emit(start, i+1)

		start = i + 1
	}

	emit(start, len(runes))

	return out
}

// Vector is an embedding.
type Vector []float64

// Cosine is the similarity between two vectors.
//
// # Why cosine and not euclidean distance
//
// Cosine measures the ANGLE and ignores the magnitude, so a long document and a short one about the same thing
// score the same. Euclidean distance would rank by length as much as by meaning.
//
// Two facts worth holding. On normalised vectors, which is what every embedding API returns, cosine similarity
// and the dot product are the same number, so a production system does the cheaper one. And the range is -1 to
// 1 for arbitrary vectors but 0 to 1 in practice, because embedding models do not produce opposites.
func Cosine(a, b Vector) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("rag: cannot compare a %d-dimensional vector with a %d-dimensional one", len(a), len(b))
	}

	if len(a) == 0 {
		return 0, errors.New("rag: cannot compare empty vectors")
	}

	var dot, normA, normB float64

	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0, errors.New("rag: a zero vector has no direction, so no angle")
	}

	return dot / (math.Sqrt(normA) * math.Sqrt(normB)), nil
}

// Document is a chunk with its embedding.
type Document struct {
	Chunk  Chunk
	Vector Vector
}

// Hit is a retrieved chunk and its score.
type Hit struct {
	Document Document
	Score    float64
}

// Store is an in-memory index.
//
// # Why a linear scan is the right first answer
//
// It is O(n) per query, and for a few thousand chunks that is microseconds. A vector database is worth adding
// when the linear scan stops being fast enough, which is later than people expect, and the module in this repo
// that covers the alternative is database-concepts with pgvector.
//
// The important part is that the ranking is identical either way. An approximate index trades recall for speed;
// it does not change what "relevant" means.
type Store struct {
	docs []Document
}

// Add indexes a document.
func (s *Store) Add(docs ...Document) {
	s.docs = append(s.docs, docs...)
}

// Len is how many chunks are indexed.
func (s *Store) Len() int { return len(s.docs) }

// Search returns the k best matches above a threshold.
//
// # The threshold is the part people leave out
//
// Without one, a query about something the corpus does not cover still returns k chunks, with low scores that
// nothing looks at. Those chunks go in the prompt, and the model, asked a question with irrelevant context
// attached, answers from the irrelevant context. "I do not know" is a better answer and the threshold is how
// you get it.
func (s *Store) Search(query Vector, k int, threshold float64) ([]Hit, error) {
	if k <= 0 {
		return nil, errors.New("rag: k must be positive")
	}

	var hits []Hit

	for _, doc := range s.docs {
		score, err := Cosine(query, doc.Vector)
		if err != nil {
			return nil, err
		}

		if score < threshold {
			continue
		}

		hits = append(hits, Hit{Document: doc, Score: score})
	}

	// Sort by score, then by a stable tiebreaker. Without the tiebreaker two equally-scoring chunks come back
	// in an order that depends on insertion, which makes a test flaky the day someone reorders the corpus.
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}

		if hits[i].Document.Chunk.DocID != hits[j].Document.Chunk.DocID {
			return hits[i].Document.Chunk.DocID < hits[j].Document.Chunk.DocID
		}

		return hits[i].Document.Chunk.Index < hits[j].Document.Chunk.Index
	})

	if len(hits) > k {
		hits = hits[:k]
	}

	return hits, nil
}

// Prompt assembles retrieved chunks into context for a question.
//
// # Three things this does that matter
//
//   - It labels each chunk with its source, so the model can cite and a reader can check. A RAG answer with no
//     citation is an answer nobody can verify, which defeats the purpose of grounding it in documents.
//   - It puts the retrieved context BEFORE the question. Models attend to the start and the end of a prompt
//     more reliably than the middle, and the question is what should be last.
//   - It says what to do when the context does not answer the question. Without that instruction the model
//     fills the gap, which is the failure mode RAG was supposed to fix.
func Prompt(question string, hits []Hit) string {
	if len(hits) == 0 {
		return "No relevant documents were found.\n\nQuestion: " + question +
			"\n\nSay that you do not have the information to answer."
	}

	var b strings.Builder

	b.WriteString("Answer the question using only the context below.\n")
	b.WriteString("If the context does not contain the answer, say so. Do not use other knowledge.\n")
	b.WriteString("Cite the source of each claim as [source].\n\n")

	for _, hit := range hits {
		fmt.Fprintf(&b, "[%s#%d] %s\n\n", hit.Document.Chunk.DocID, hit.Document.Chunk.Index, hit.Document.Chunk.Text)
	}

	b.WriteString("Question: ")
	b.WriteString(question)

	return b.String()
}

// BagOfWords is a toy embedding, so the retrieval tests need no API.
//
// It builds a vector over a fixed vocabulary by counting words. That is a 1970s information retrieval model and
// it has the property this package needs to demonstrate: similar texts point in similar directions.
//
// What it does NOT have is any notion of meaning. "car" and "automobile" are orthogonal here and nearly
// parallel in a real embedding, which is the entire reason embeddings replaced this. The test for that is in
// database-concepts, against pgvector, with real vectors.
func BagOfWords(vocabulary []string, text string) Vector {
	index := make(map[string]int, len(vocabulary))
	for i, word := range vocabulary {
		index[word] = i
	}

	v := make(Vector, len(vocabulary))

	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		if i, ok := index[word]; ok {
			v[i]++
		}
	}

	return v
}

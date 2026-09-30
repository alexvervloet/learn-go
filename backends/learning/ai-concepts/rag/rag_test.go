package rag

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFixedChunkingOverlaps is the baseline.
func TestFixedChunkingOverlaps(t *testing.T) {
	text := strings.Repeat("abcdefghij", 5) // 50 runes

	chunks, err := Fixed("doc", text, ChunkOptions{Size: 20, Overlap: 5})
	require.NoError(t, err)

	// Step is size minus overlap, so 15. Starts are 0, 15, 30, and the third reaches the end at 50.
	require.Len(t, chunks, 3)
	require.Equal(t, 0, chunks[0].Start)
	require.Equal(t, 20, chunks[0].End)
	require.Equal(t, 15, chunks[1].Start)
	require.Equal(t, 30, chunks[2].Start)
	require.Equal(t, 50, chunks[2].End)

	// The chunk count is ceil((n - overlap) / step), not n / size. Getting that wrong is how a chunker ends
	// up either dropping the tail or emitting an empty last chunk.
	require.Len(t, []rune(chunks[2].Text), 20)

	// The overlap is the point: the last 5 runes of one chunk are the first 5 of the next, so a phrase
	// spanning the boundary survives in one piece somewhere.
	for i := 1; i < len(chunks); i++ {
		prev := []rune(chunks[i-1].Text)
		curr := []rune(chunks[i].Text)

		require.Equal(t, string(prev[len(prev)-5:]), string(curr[:5]), "chunk %d", i)
	}
}

// TestOverlapThatCannotAdvanceIsRejected is the infinite loop, prevented.
func TestOverlapThatCannotAdvanceIsRejected(t *testing.T) {
	for _, opts := range []ChunkOptions{
		{Size: 10, Overlap: 10},
		{Size: 10, Overlap: 11},
		{Size: 0, Overlap: 0},
		{Size: 3, Overlap: -2},
	} {
		_, err := Fixed("doc", "some text", opts)
		require.ErrorIs(t, err, ErrBadChunkOptions, "%+v", opts)
	}

	// Sentences measures overlap in sentences, so only size and sign are wrong in every case. A negative
	// overlap there used to be a slice out of range.
	for _, opts := range []ChunkOptions{{Size: 0}, {Size: 5, Overlap: -1}} {
		_, err := Sentences("doc", "A a. B b. C c.", opts)
		require.ErrorIs(t, err, ErrBadChunkOptions, "%+v", opts)
	}
}

// TestFixedChunkingDoesNotSplitRunes is why the unit is runes and not bytes.
func TestFixedChunkingDoesNotSplitRunes(t *testing.T) {
	// Each of these is 3 bytes and 1 rune. A byte-based split at 10 would land inside one and produce
	// invalid UTF-8, which then fails to embed, fails to display, and fails quietly.
	text := strings.Repeat("日", 25)

	chunks, err := Fixed("doc", text, ChunkOptions{Size: 10, Overlap: 2})
	require.NoError(t, err)

	for _, c := range chunks {
		require.True(t, isValidUTF8(c.Text), "chunk %d is not valid UTF-8", c.Index)
		require.LessOrEqual(t, len([]rune(c.Text)), 10)
	}

	// 75 bytes, 25 runes: a byte-based splitter would have produced a different number of chunks and broken
	// characters in every one.
	require.Equal(t, 75, len(text))
	require.Equal(t, 25, len([]rune(text)))
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}

	return true
}

// TestSentenceChunkingKeepsSentencesWhole is the improvement over Fixed.
func TestSentenceChunkingKeepsSentencesWhole(t *testing.T) {
	text := "The server listens on port 8080. It uses TLS. " +
		"Requests are logged to stdout. Errors go to stderr. " +
		"The config file is read at startup."

	chunks, err := Sentences("doc", text, ChunkOptions{Size: 60, Overlap: 1})
	require.NoError(t, err)
	require.Greater(t, len(chunks), 1, "the text is longer than one chunk")

	for _, c := range chunks {
		require.True(t, strings.HasSuffix(c.Text, ".") || strings.HasSuffix(c.Text, "?") ||
			strings.HasSuffix(c.Text, "!"), "chunk %d ends mid-sentence: %q", c.Index, c.Text)
	}

	// And the overlap is a whole sentence, so a fact that needs its neighbour keeps it.
	for i := 1; i < len(chunks); i++ {
		prev := splitSentences(chunks[i-1].Text)
		curr := splitSentences(chunks[i].Text)

		require.Equal(t, prev[len(prev)-1], curr[0], "chunk %d does not carry the previous sentence", i)
	}
}

// TestTheSentenceSplitterIsNaive is honest about the limits.
//
// Asserting a known wrong answer is better than leaving it unsaid. This is the line in the chunker's doc
// comment, made checkable, and it is what would have to change if a real segmenter were dropped in.
func TestTheSentenceSplitterIsNaive(t *testing.T) {
	require.Equal(t, []string{"Pi is 3.14 exactly."}, splitSentences("Pi is 3.14 exactly."),
		"a decimal point is not followed by a space, so it is not treated as a boundary")

	require.Equal(t, []string{"Dr.", "Smith arrived."}, splitSentences("Dr. Smith arrived."),
		"an abbreviation IS followed by a space, so the naive rule gets it wrong")
}

// TestCosineIgnoresMagnitude is why cosine and not euclidean.
func TestCosineIgnoresMagnitude(t *testing.T) {
	short := Vector{1, 2, 3}
	long := Vector{10, 20, 30} // the same direction, ten times the length

	score, err := Cosine(short, long)
	require.NoError(t, err)
	require.InDelta(t, 1.0, score, 1e-9, "same direction is a perfect score whatever the magnitude")

	orthogonal, err := Cosine(Vector{1, 0}, Vector{0, 1})
	require.NoError(t, err)
	require.InDelta(t, 0.0, orthogonal, 1e-9, "nothing in common is zero")

	opposite, err := Cosine(Vector{1, 0}, Vector{-1, 0})
	require.NoError(t, err)
	require.InDelta(t, -1.0, opposite, 1e-9,
		"the range is -1 to 1 in arithmetic and 0 to 1 in practice, because embeddings have no opposites")
}

// TestCosineRejectsWhatItCannotCompare covers the two error cases.
func TestCosineRejectsWhatItCannotCompare(t *testing.T) {
	_, err := Cosine(Vector{1, 2}, Vector{1, 2, 3})
	require.ErrorContains(t, err, "2-dimensional")

	_, err = Cosine(Vector{0, 0}, Vector{1, 1})
	require.ErrorContains(t, err, "zero vector", "a zero vector has no direction, so no angle")
}

// corpus builds a tiny indexed store for the retrieval tests.
func corpus(t *testing.T) (*Store, []string) {
	t.Helper()

	vocab := []string{"postgres", "index", "query", "docker", "image", "container", "kafka", "topic", "partition"}

	docs := map[string]string{
		"db":     "Postgres uses an index to make a query fast. Without an index the query scans.",
		"docker": "A docker image is a filesystem. A container is a running image.",
		"kafka":  "A kafka topic is split into a partition. Each partition is ordered.",
	}

	store := &Store{}

	for id, text := range docs {
		chunks, err := Sentences(id, text, ChunkOptions{Size: 200, Overlap: 0})
		require.NoError(t, err)

		for _, c := range chunks {
			store.Add(Document{Chunk: c, Vector: BagOfWords(vocab, c.Text)})
		}
	}

	require.Equal(t, 3, store.Len())

	return store, vocab
}

// TestSearchRanksByRelevance is the retrieval half.
func TestSearchRanksByRelevance(t *testing.T) {
	store, vocab := corpus(t)

	hits, err := store.Search(BagOfWords(vocab, "how do I make a postgres query fast"), 3, 0)
	require.NoError(t, err)
	require.NotEmpty(t, hits)

	require.Equal(t, "db", hits[0].Document.Chunk.DocID, "the database chunk is first: %v", hits)

	// Scores descend. This is the ordering contract every caller relies on and the one a careless sort breaks.
	for i := 1; i < len(hits); i++ {
		require.GreaterOrEqual(t, hits[i-1].Score, hits[i].Score)
	}
}

// TestTheThresholdIsWhatMakesIDoNotKnowPossible is the rule people leave out.
func TestTheThresholdIsWhatMakesIDoNotKnowPossible(t *testing.T) {
	store, vocab := corpus(t)

	// A question the corpus knows nothing about. No vocabulary word appears, so the query vector is all
	// zeroes, and a zero vector has no direction. Search reports that rather than scoring everything at zero
	// and returning three irrelevant chunks, which is the failure this test is about.
	unrelated := BagOfWords(vocab, "what is the airspeed velocity of an unladen swallow")
	require.Equal(t, make(Vector, len(vocab)), unrelated)

	_, err := store.Search(unrelated, 3, 0)
	require.ErrorContains(t, err, "zero vector")

	// The realistic version: a query that matches SOMETHING, weakly.
	weak := BagOfWords(vocab, "kafka")

	// A threshold of 0 is not a threshold: a chunk sharing NO words scores exactly 0, and 0 is not less than
	// 0, so all three come back. Two of them are known irrelevant and they would go straight into the prompt.
	all, err := store.Search(weak, 3, 0)
	require.NoError(t, err)
	require.Len(t, all, 3, "zero lets a zero score through")
	require.Equal(t, "kafka", all[0].Document.Chunk.DocID)
	require.Zero(t, all[1].Score)
	require.Zero(t, all[2].Score)

	// Any threshold above zero drops them.
	relevant, err := store.Search(weak, 3, 0.01)
	require.NoError(t, err)
	require.Len(t, relevant, 1, "only the kafka chunk shares a word")
	require.Less(t, relevant[0].Score, 0.9, "and even that is a weak match: %f", relevant[0].Score)

	none, err := store.Search(weak, 3, 0.9)
	require.NoError(t, err)
	require.Empty(t, none, "a high threshold returns nothing rather than the best of a bad set")

	// Which is what Prompt turns into an instruction to say so, rather than sending low-scoring chunks the
	// model will then answer from.
	require.Contains(t, Prompt("what is a kafka topic", none), "do not have the information")
}

// TestPromptPutsContextFirstAndCitesIt covers the three assembly rules.
func TestPromptPutsContextFirstAndCitesIt(t *testing.T) {
	store, vocab := corpus(t)

	hits, err := store.Search(BagOfWords(vocab, "docker image container"), 2, 0.1)
	require.NoError(t, err)
	require.NotEmpty(t, hits)

	prompt := Prompt("what is a container?", hits)

	// The question is last, because that is where a model attends most reliably.
	require.True(t, strings.HasSuffix(prompt, "Question: what is a container?"))

	// The context is labelled, so the answer can cite and a reader can check.
	require.Contains(t, prompt, "[docker#0]")

	// And the instruction that stops the model filling a gap it was supposed to have filled from documents.
	require.Contains(t, prompt, "If the context does not contain the answer, say so")
	require.Contains(t, prompt, "Do not use other knowledge")

	// Context before question.
	require.Less(t, strings.Index(prompt, "[docker#0]"), strings.Index(prompt, "Question:"))
}

// TestSearchKMustBePositive is the last guard.
func TestSearchKMustBePositive(t *testing.T) {
	store, vocab := corpus(t)

	_, err := store.Search(BagOfWords(vocab, "postgres"), 0, 0)
	require.ErrorContains(t, err, "k must be positive")
}

// TestBagOfWordsHasNoNotionOfMeaning is the honest limit of the toy.
func TestBagOfWordsHasNoNotionOfMeaning(t *testing.T) {
	vocab := []string{"car", "automobile", "road"}

	car := BagOfWords(vocab, "the car is on the road")
	auto := BagOfWords(vocab, "the automobile is on the road")

	score, err := Cosine(car, auto)
	require.NoError(t, err)

	// They share "road" and nothing else, so the score is 0.5 rather than the near-1 a real embedding would
	// give. This is precisely why embeddings replaced word counting, and why this package's vectors are a
	// stand-in for arithmetic and not a model.
	require.InDelta(t, 0.5, score, 1e-9)
}

// TestSentenceOffsetsPointAtTheSource is the citation: Start and End must slice the original text back out,
// in runes, for every chunk, including a sentence that appears twice.
func TestSentenceOffsetsPointAtTheSource(t *testing.T) {
	text := "Café é bom.  Yes. Café é bom. Yes."

	chunks, err := Sentences("doc", text, ChunkOptions{Size: 12})
	require.NoError(t, err)
	require.Len(t, chunks, 4)

	runes := []rune(text)

	for _, c := range chunks {
		require.Equal(t, c.Text, string(runes[c.Start:c.End]), "chunk %d", c.Index)
	}

	require.Equal(t, 18, chunks[2].Start, "the second \"Café é bom.\" is not the first one")
	require.Equal(t, 30, chunks[3].Start)
}

// TestSentenceOverlapStillAdvances is the overlap larger than a chunk. Without the cap each chunk is the one before
// plus a sentence, and 5 sentences come out as 15.
func TestSentenceOverlapStillAdvances(t *testing.T) {
	chunks, err := Sentences("doc", "A a. B b. C c. D d. E e.", ChunkOptions{Size: 5, Overlap: 10})
	require.NoError(t, err)

	var texts []string
	for _, c := range chunks {
		texts = append(texts, c.Text)
	}

	require.Equal(t, []string{"A a.", "B b.", "C c.", "D d.", "E e."}, texts)
}

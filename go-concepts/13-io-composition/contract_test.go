package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

// TestReadContractLosesTheLastChunk is the headline bug of this lesson,
// asserted. The broken loop must stay broken, or the example stops teaching.
func TestReadContractLosesTheLastChunk(t *testing.T) {
	const payload = "hello, contract"

	broken := readBroken(&eofWithDataReader{data: []byte(payload)})
	correct, err := readCorrect(&eofWithDataReader{data: []byte(payload)})

	if err != nil {
		t.Fatalf("the correct loop returned an error: %v", err)
	}
	if correct != payload {
		t.Errorf("correct loop = %q, want %q", correct, payload)
	}
	if broken == payload {
		t.Error("the error-first loop should have lost data — the example is no longer wrong")
	}
	if len(broken) >= len(payload) {
		t.Errorf("broken = %q (%d bytes), want fewer than %d", broken, len(broken), len(payload))
	}

	t.Logf("error-first loop kept %d of %d bytes", len(broken), len(payload))
}

// TestEOFWithDataOnlyOnTheLastChunk: the reader returns (n, nil) for earlier
// chunks, which is exactly why the bug survives testing on short inputs.
func TestEOFWithDataOnlyOnTheLastChunk(t *testing.T) {
	r := &eofWithDataReader{data: []byte("0123456789")}
	buf := make([]byte, 4)

	n, err := r.Read(buf)
	if n != 4 || err != nil {
		t.Errorf("first read = %d, %v; want 4, nil", n, err)
	}

	n, err = r.Read(buf)
	if n != 4 || err != nil {
		t.Errorf("second read = %d, %v; want 4, nil", n, err)
	}

	n, err = r.Read(buf)
	if n != 2 || !errors.Is(err, io.EOF) {
		t.Errorf("final read = %d, %v; want 2, io.EOF together", n, err)
	}

	n, err = r.Read(buf)
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Errorf("read past the end = %d, %v; want 0, io.EOF", n, err)
	}
}

// TestCorrectLoopMatchesReadAll is the strongest check available: the
// hand-written loop must agree with the standard library on every input.
func TestCorrectLoopMatchesReadAll(t *testing.T) {
	inputs := []string{
		"",
		"a",
		"exactly8",
		"nine char",
		strings.Repeat("x", 1000),
		"multi\nline\ninput\n",
	}

	for _, in := range inputs {
		t.Run(in[:min(10, len(in))], func(t *testing.T) {
			got, err := readCorrect(&eofWithDataReader{data: []byte(in)})
			if err != nil {
				t.Fatalf("readCorrect: %v", err)
			}

			want, err := io.ReadAll(&eofWithDataReader{data: []byte(in)})
			if err != nil {
				t.Fatalf("io.ReadAll: %v", err)
			}

			if got != string(want) {
				t.Errorf("readCorrect = %q, io.ReadAll = %q", got, want)
			}
		})
	}
}

// TestHandlesZeroNil: (0, nil) is legal and means "nothing this time".
func TestHandlesZeroNil(t *testing.T) {
	data, reads, err := handlesZeroNil(&zeroNilReader{stalls: 3, data: "eventually"})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data != "eventually" {
		t.Errorf("data = %q, want %q", data, "eventually")
	}
	if reads < 5 {
		t.Errorf("reads = %d, want at least 5 (3 stalls, 1 data, 1 EOF)", reads)
	}
}

func TestShortWriterBreaksTheContract(t *testing.T) {
	correctBytes, shortErr, correctErr := copyDetectsShortWrites()

	if shortErr == nil {
		t.Fatal("io.Copy should reject a writer that short-writes with a nil error")
	}
	if !errors.Is(shortErr, io.ErrShortWrite) {
		t.Errorf("err = %v, want io.ErrShortWrite", shortErr)
	}

	if correctErr != nil {
		t.Errorf("a correct writer produced %v", correctErr)
	}
	if correctBytes != 11 {
		t.Errorf("copied %d bytes, want 11", correctBytes)
	}
}

func TestCorrectWriterReportsItsLimit(t *testing.T) {
	w := &correctWriter{capacity: 5}

	n, err := w.Write([]byte("hello"))
	if n != 5 || err != nil {
		t.Fatalf("first write = %d, %v; want 5, nil", n, err)
	}

	n, err = w.Write([]byte(" world"))
	if err == nil {
		t.Fatal("writing past the capacity should fail")
	}
	if !errors.Is(err, io.ErrShortWrite) {
		t.Errorf("err = %v, want io.ErrShortWrite", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0 — the writer was already full", n)
	}
}

func TestEOFIsNotAnError(t *testing.T) {
	wrong, right := eofIsNotAnError()

	if !strings.Contains(wrong, "failed") {
		t.Errorf("the bad example should look like a failure, got %q", wrong)
	}
	if strings.Contains(right, "failed") {
		t.Errorf("the good example should not look like a failure, got %q", right)
	}
	if !strings.Contains(right, "completed") {
		t.Errorf("the good example should report completion, got %q", right)
	}
}

// TestWhichReadersReturnDataWithEOF checks the lesson's claim against real readers rather than the one written
// for it. gzip's Reader hands back its last bytes together with io.EOF, so the broken loop loses them; the readers
// people usually test with do not, so the broken loop passes on them. iotest.DataErrReader turns any reader into
// the gzip kind, which is how to test a read loop without writing a reader.
func TestWhichReadersReturnDataWithEOF(t *testing.T) {
	const payload = "hello, contract"

	gzipped := func() io.Reader {
		var buf bytes.Buffer

		w := gzip.NewWriter(&buf)
		_, _ = w.Write([]byte(payload))
		_ = w.Close()

		r, err := gzip.NewReader(&buf)
		if err != nil {
			t.Fatal(err)
		}

		return r
	}

	for _, tc := range []struct {
		name      string
		reader    func() io.Reader
		losesData bool
	}{
		{"strings.Reader", func() io.Reader { return strings.NewReader(payload) }, false},
		{"bytes.Reader", func() io.Reader { return bytes.NewReader([]byte(payload)) }, false},
		{"gzip.Reader", gzipped, true},
		{"iotest.DataErrReader", func() io.Reader { return iotest.DataErrReader(strings.NewReader(payload)) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := readBroken(tc.reader())

			if lost := got != payload; lost != tc.losesData {
				t.Errorf("the broken loop read %q; losing data = %t, want %t", got, lost, tc.losesData)
			}

			correct, err := readCorrect(tc.reader())
			if err != nil || correct != payload {
				t.Errorf("the correct loop read %q, %v", correct, err)
			}
		})
	}
}

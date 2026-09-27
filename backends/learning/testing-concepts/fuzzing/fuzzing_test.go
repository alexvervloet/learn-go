package fuzzing

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestDecodeRejectsTheFuzzerCrashers is the regression test for what the fuzzer found.
//
// A crasher belongs in an ordinary table test as well as in the fuzz corpus. The corpus makes the
// fuzzer re-check it; the table makes it visible, named, and runnable without -fuzz. A corpus file
// called 66498f377f38b53e tells a reader nothing.
func TestDecodeRejectsTheFuzzerCrashers(t *testing.T) {
	tests := []struct {
		name    string
		in      []byte
		wantErr error
	}{
		{
			// The input the fuzzer minimised to six bytes in 0.04 seconds. 0x3030 is
			// 12336, so it claims 12,336 records from a four-byte remainder.
			name:    "six ASCII zeros, the original crasher",
			in:      []byte("000000"),
			wantErr: ErrTooManyRecs,
		},
		{
			// One record claiming 0xFFFFFFFF bytes. This is the bug the crasher above
			// was a variant of: data[:length] with length far past the end.
			name:    "a length claiming four billion bytes",
			in:      []byte{0, 1, 0xFF, 0xFF, 0xFF, 0xFF},
			wantErr: ErrTooLong,
		},
		{name: "empty", in: nil, wantErr: ErrTruncated},
		{name: "one byte", in: []byte{0}, wantErr: ErrTruncated},
		{
			name:    "a record with no length prefix",
			in:      []byte{0, 1, 0, 0},
			wantErr: ErrTruncated,
		},
		{
			name:    "trailing bytes",
			in:      []byte{0, 0, 'x'},
			wantErr: ErrTrailing,
		},
		{
			name:    "a count above the cap",
			in:      []byte{0xFF, 0xFF},
			wantErr: ErrTooManyRecs,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(tt.in)

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Decode(%v) = %v, want %v", tt.in, err, tt.wantErr)
			}
		})
	}
}

func TestEncodeDecode(t *testing.T) {
	tests := []struct {
		name string
		in   []string
	}{
		{"empty", nil},
		{"one record", []string{"hello"}},
		{"several", []string{"a", "bb", "ccc"}},
		{"an empty record", []string{""}},
		{"empty records among others", []string{"a", "", "b"}},
		{"multi-byte", []string{"naïve", "日本語"}},
		{"a record containing the delimiter bytes", []string{"\x00\x01\xFF"}},
		{"a long record", []string{strings.Repeat("x", 70000)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := Encode(tt.in)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}

			got, err := Decode(encoded)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}

			// slices.Equal treats nil and empty as equal, which is right here: Encode
			// of nil and of []string{} produce the same bytes, so Decode cannot tell
			// them apart and should not pretend to.
			if !slices.Equal(got, tt.in) {
				t.Errorf("round trip gave %q, want %q", got, tt.in)
			}
		})
	}
}

func TestEncodeRejectsTooManyRecords(t *testing.T) {
	if _, err := Encode(make([]string, maxRecords+1)); !errors.Is(err, ErrTooManyRecs) {
		t.Errorf("err = %v, want ErrTooManyRecs", err)
	}
	if _, err := Encode(make([]string, maxRecords)); err != nil {
		t.Errorf("exactly at the cap was rejected: %v", err)
	}
}

// FuzzDecodeDoesNotPanic is the first fuzz target anyone should write for a parser: feed it
// arbitrary bytes and require that it returns rather than panicking.
//
// It asserts almost nothing about correctness, and that is the point. A parser that never panics on
// hostile input is most of what a parser at a trust boundary needs to promise, and it is a property
// a fuzzer can check without a model of what the right answer is.
//
// Run it with:
//
//	go test -fuzz FuzzDecodeDoesNotPanic -fuzztime 30s ./fuzzing
//
// Without -fuzz it runs only the seed corpus, which is what CI does: the seeds become ordinary test
// cases and cost microseconds.
func FuzzDecodeDoesNotPanic(f *testing.F) {
	// The seeds matter more than people expect. A fuzzer mutates what it is given, so a corpus
	// of valid inputs explores the valid paths and a corpus of near-misses explores the error
	// paths. These are deliberately both.
	f.Add([]byte(nil))
	f.Add([]byte{0, 0})
	f.Add([]byte{0, 1, 0, 0, 0, 5, 'h', 'e', 'l', 'l', 'o'})
	f.Add([]byte("000000"))                     // the original crasher
	f.Add([]byte{0, 1, 0xFF, 0xFF, 0xFF, 0xFF}) // an absurd length
	f.Add([]byte{0xFF, 0xFF})                   // an absurd count
	f.Add([]byte{0, 2, 0, 0, 0, 1, 'a', 0, 0, 0, 1, 'b'})

	f.Fuzz(func(t *testing.T, data []byte) {
		records, err := Decode(data)

		// An error is a perfectly good outcome. What is not acceptable: returning records
		// AND an error, which would leave a caller with a half-decoded value it might use.
		if err != nil && records != nil {
			t.Errorf("Decode returned %d records alongside an error %v", len(records), err)
		}

		if err != nil {
			return
		}

		// On success, the result must re-encode to exactly the input. That is a real
		// correctness property and it catches the ambiguity bug: if Decode accepted
		// trailing bytes, re-encoding would drop them and this would fail.
		reencoded, err := Encode(records)
		if err != nil {
			t.Fatalf("Encode of a successfully decoded value failed: %v", err)
		}
		if !slices.Equal(reencoded, data) {
			t.Errorf("Decode then Encode changed the bytes\n input: %v\noutput: %v",
				data, reencoded)
		}
	})
}

// FuzzEncodeDecodeRoundTrip goes the other way, and it is the stronger property: for ANY slice of
// strings, encoding and decoding gives the slice back.
//
// The fuzzer cannot generate a []string directly. It generates the corpus types it supports, so the
// trick is to take a string and split it, which is how most structured fuzzing in Go is done.
func FuzzEncodeDecodeRoundTrip(f *testing.F) {
	f.Add("")
	f.Add("a")
	f.Add("a\x00b\x00c")
	f.Add("naïve\x00日本語")
	f.Add(strings.Repeat("x", 1000))

	f.Fuzz(func(t *testing.T, joined string) {
		records := strings.Split(joined, "\x00")

		// The cap is a real limit, not something to fuzz around.
		if len(records) > maxRecords {
			t.Skip("more records than the format allows")
		}

		encoded, err := Encode(records)
		if err != nil {
			t.Fatalf("Encode(%q): %v", records, err)
		}

		got, err := Decode(encoded)
		if err != nil {
			t.Fatalf("Decode of our own output failed: %v", err)
		}

		if !slices.Equal(got, records) {
			t.Errorf("round trip changed the records\n in: %q\nout: %q", records, got)
		}
	})
}

// FuzzDecodeStrictAgreesWithDecode pins down the one place the two decoders legitimately differ, so
// the difference is a property rather than a surprise.
//
// This is the shape worth copying: when two functions are meant to agree except in stated cases,
// fuzz the agreement and state the exception. It found nothing here, which is the outcome you want
// from a property you already believed.
func FuzzDecodeStrictAgreesWithDecode(f *testing.F) {
	f.Add([]byte{0, 1, 0, 0, 0, 5, 'h', 'e', 'l', 'l', 'o'})
	f.Add([]byte{0, 1, 0, 0, 0, 1, 0xFF}) // 0xFF alone is not valid UTF-8
	f.Add([]byte("000000"))

	f.Fuzz(func(t *testing.T, data []byte) {
		lax, laxErr := Decode(data)
		strict, strictErr := DecodeStrict(data)

		// Whenever Decode fails, DecodeStrict must fail the same way: it is Decode plus a
		// check, so it cannot succeed where Decode did not.
		if laxErr != nil {
			if strictErr == nil {
				t.Errorf("DecodeStrict succeeded where Decode failed with %v", laxErr)
			}
			return
		}

		// Decode succeeded. DecodeStrict may fail, and only for the stated reason.
		if strictErr != nil {
			if !errors.Is(strictErr, ErrInvalidUTF8) {
				t.Errorf("DecodeStrict failed with %v, want only ErrInvalidUTF8", strictErr)
			}
			return
		}

		if !slices.Equal(lax, strict) {
			t.Errorf("the two decoders disagree\n   lax: %q\nstrict: %q", lax, strict)
		}
	})
}

// TestDecodeStrictRejectsInvalidUTF8, as an ordinary test, because the fuzz target above only
// checks the two AGREE and never that DecodeStrict rejects anything at all.
//
// That is a real gap in property testing generally: "these two functions agree except when X" is
// satisfied by a DecodeStrict that is identical to Decode. The property needs a companion example.
func TestDecodeStrictRejectsInvalidUTF8(t *testing.T) {
	// 0xFF is not a valid UTF-8 byte in any position.
	encoded, err := Encode([]string{"\xFF"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Decode(encoded); err != nil {
		t.Errorf("Decode should accept arbitrary bytes: %v", err)
	}
	if _, err := DecodeStrict(encoded); !errors.Is(err, ErrInvalidUTF8) {
		t.Errorf("DecodeStrict err = %v, want ErrInvalidUTF8", err)
	}

	// And valid UTF-8 passes both.
	valid, err := Encode([]string{"naïve", "日本語"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeStrict(valid); err != nil {
		t.Errorf("DecodeStrict rejected valid UTF-8: %v", err)
	}
}

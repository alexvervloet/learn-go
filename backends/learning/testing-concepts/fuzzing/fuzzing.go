// Package fuzzing is the code under test for go test -fuzz.
//
// A length-prefixed record codec: the kind of small binary format that looks obviously correct and
// is not. Every bug the fuzzer found in the first version is recorded in the test file.
package fuzzing

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"
)

// Errors returned by Decode.
var (
	ErrTruncated   = errors.New("fuzzing: truncated input")
	ErrTooLong     = errors.New("fuzzing: length exceeds remaining input")
	ErrTrailing    = errors.New("fuzzing: trailing bytes after the last record")
	ErrTooManyRecs = errors.New("fuzzing: too many records")
	ErrInvalidUTF8 = errors.New("fuzzing: record is not valid UTF-8")
)

// maxRecords caps how many records Decode will allocate for.
//
// Not arbitrary defensiveness: a two-byte count field can claim 65,535 records, and a decoder that
// preallocates that many from a four-byte input is a denial of service with a very small request.
// The fuzzer does not find this one, because it is not a crash; it was found by reading the
// allocation after the fuzzer found the others.
const maxRecords = 1024

// Encode writes records as a count followed by length-prefixed payloads.
//
//	uint16  record count
//	then, per record:
//	uint32  payload length
//	bytes   payload
//
// Big-endian, because network byte order is the convention for a wire format and picking the
// other one is a decision nobody wants to revisit.
func Encode(records []string) ([]byte, error) {
	if len(records) > maxRecords {
		return nil, fmt.Errorf("%w: %d", ErrTooManyRecs, len(records))
	}

	// Size the buffer up front: 2 for the count, then 4 + len per record.
	size := 2
	for _, r := range records {
		size += 4 + len(r)
	}

	out := make([]byte, 0, size)
	out = binary.BigEndian.AppendUint16(out, uint16(len(records)))

	for _, r := range records {
		out = binary.BigEndian.AppendUint32(out, uint32(len(r)))
		out = append(out, r...)
	}

	return out, nil
}

// Decode reads what Encode wrote.
//
// Every bounds check here exists because the fuzzer found its absence. The first version was
// twelve lines and looked fine; see FuzzDecodeDoesNotPanic for what it did.
func Decode(data []byte) ([]string, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("%w: need 2 bytes for the count, have %d", ErrTruncated, len(data))
	}

	count := int(binary.BigEndian.Uint16(data))
	data = data[2:]

	if count > maxRecords {
		return nil, fmt.Errorf("%w: claimed %d", ErrTooManyRecs, count)
	}

	// The allocation is capped by maxRecords above, so a claimed count cannot be used to
	// force an arbitrary allocation. Sizing it from the claim rather than growing is still
	// worth it for the common case.
	out := make([]string, 0, count)

	for i := range count {
		if len(data) < 4 {
			return nil, fmt.Errorf("%w: record %d has no length prefix", ErrTruncated, i)
		}

		length := int(binary.BigEndian.Uint32(data))
		data = data[4:]

		// The check the first version did not have. A four-byte length can claim four
		// billion bytes from a six-byte input, and data[:length] then panics.
		if length > len(data) {
			return nil, fmt.Errorf("%w: record %d claims %d bytes, %d remain",
				ErrTooLong, i, length, len(data))
		}

		out = append(out, string(data[:length]))
		data = data[length:]
	}

	// Trailing bytes mean the input was not produced by Encode, and accepting them silently
	// makes the format ambiguous: two different inputs would decode to the same records.
	if len(data) > 0 {
		return nil, fmt.Errorf("%w: %d bytes", ErrTrailing, len(data))
	}

	return out, nil
}

// DecodeStrict is Decode plus a UTF-8 check.
//
// Separate because the two are genuinely different contracts. Decode round-trips arbitrary bytes,
// which is what a wire format should do; DecodeStrict is for when the records are meant to be text
// and invalid UTF-8 should be rejected at the boundary rather than producing a string that cannot
// be logged or stored.
//
// The distinction matters for the round-trip property: Decode(Encode(x)) == x holds for any
// []string, because a Go string is arbitrary bytes. DecodeStrict(Encode(x)) == x only holds when
// every element is valid UTF-8, and the fuzzer finds the difference immediately.
func DecodeStrict(data []byte) ([]string, error) {
	records, err := Decode(data)
	if err != nil {
		return nil, err
	}

	for i, r := range records {
		if !utf8.ValidString(r) {
			return nil, fmt.Errorf("%w: record %d", ErrInvalidUTF8, i)
		}
	}

	return records, nil
}

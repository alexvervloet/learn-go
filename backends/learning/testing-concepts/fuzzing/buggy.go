package fuzzing

import "encoding/binary"

// buggyDecode is the first version of Decode, kept so the fuzzer has something to find.
//
// It is what a careful person writes in five minutes, and it has three bugs. Run
//
//	go test -tags demo_fuzz -fuzz FuzzBuggyDecode ./fuzzing
//
// and the fuzzer finds the first of them in well under a second. The three:
//
//  1. no check that the claimed length fits the remaining input, so data[:length] panics
//  2. no check on the record count, so a two-byte input can preallocate 65,535 strings
//  3. trailing bytes are ignored, so the format is ambiguous
//
// Only the first is a crash, which is the limit of what a fuzzer finds on its own. The other two
// needed a property to check against, and that is what FuzzEncodeDecodeRoundTrip is for.
//
// than inside that file so a reader browsing the package sees the before-and-after side by side.
//
//nolint:unused // its only caller is behind the demo_fuzz build tag, and it is kept here rather
func buggyDecode(data []byte) ([]string, error) {
	if len(data) < 2 {
		return nil, ErrTruncated
	}

	count := int(binary.BigEndian.Uint16(data))
	data = data[2:]

	out := make([]string, 0, count)

	for range count {
		if len(data) < 4 {
			return nil, ErrTruncated
		}

		length := int(binary.BigEndian.Uint32(data))
		data = data[4:]

		// Bug 1: length is unchecked against len(data).
		out = append(out, string(data[:length]))
		data = data[length:]
	}

	return out, nil
}

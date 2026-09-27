//go:build demo_fuzz

// Behind a build tag, because this fuzz target is SUPPOSED to fail. Run it deliberately:
//
//	go test -tags demo_fuzz -fuzz FuzzBuggyDecode -fuzztime 30s ./fuzzing
package fuzzing

import "testing"

func FuzzBuggyDecode(f *testing.F) {
	f.Add([]byte{0, 1, 0, 0, 0, 5, 'h', 'e', 'l', 'l', 'o'})
	f.Add([]byte{0, 0})

	f.Fuzz(func(_ *testing.T, data []byte) {
		// Any panic is a failure; the fuzzer reports one and writes the input to
		// testdata/fuzz/FuzzBuggyDecode/.
		_, _ = buggyDecode(data)
	})
}

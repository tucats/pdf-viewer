package jpx

import "testing"

// FuzzParseHeader checks that ParseHeader never panics or hangs on
// arbitrary bytes, however malformed - it is expected to return an error
// constantly on random input (a random byte string essentially never
// happens to look like a valid codestream or JP2 file), so this fuzz
// target's only job is catching a panic, not checking any particular
// result. Seeds come from this package's own test-only encoder
// (testutil_test.go), covering both the bare-codestream and JP2-wrapped
// forms - the same "seed with a real encoder's own output" approach
// internal/filter's JBIG2 fuzz seeds use, for the same reason: starting
// from valid input lets the fuzzer's mutations discover interesting
// near-valid cases much faster than starting from nothing.
func FuzzParseHeader(f *testing.F) {
	cs := buildCodestream(16, 12, 2, 2, defaultTileConfig, []byte{0x01, 0x02, 0x03, 0x04})
	f.Add(cs)
	f.Add(buildJP2File(cs, 16, 12, 1, 7, EnumCSGreyscale))
	f.Add([]byte(nil))
	f.Add([]byte{0xFF, 0x4F})

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseHeader(data)
	})
}

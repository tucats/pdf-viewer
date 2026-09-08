package content

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
)

// FuzzParseAndInterpret is this package's Phase 2 fuzz target, matching
// the pattern internal/syntax and internal/parser established in Phase
// 1 (see their own fuzz_test.go files): a page's content stream is
// entirely attacker-controlled bytes (it comes straight from the PDF
// file, filter-decoded but otherwise unvalidated), and this package is
// exactly where those bytes are tokenized into operators and replayed
// against a graphics state machine - Parse and Interpret must never
// panic and must always return, regardless of how malformed or
// adversarial the input is.
//
// The seed corpus includes a representative sample of every operator
// category this package recognizes, plus a few pathological shapes
// (deeply nested arrays, an operator with no operands, unmatched
// q/Q) intended to steer the fuzzer toward this package's edge cases
// faster than starting from nothing.
func FuzzParseAndInterpret(f *testing.F) {
	seeds := []string{
		"",
		"q 1 0 0 1 10 10 cm 0 0 5 5 re f Q",
		"1 0 0 rg 0 0 1 RG 0 0 0 1 k 0.5 g",
		"0 0 m 10 0 l 10 10 c 5 5 10 10 v 0 0 10 y h S",
		"0 0 10 10 re W n",
		"0 0 10 10 re W* n",
		"q q q Q Q Q Q",
		"5 w 1 J 1 j 4 M [] 0 d 0 i 0 ri",
		"1 scn 1 0 0 scn 0 0 0 1 scn /Pattern1 scn",
		"(text) Tj BT ET",
		"/Im1 Do",
		"[[[[1]]]] op",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		ops, err := Parse(data)
		if err != nil {
			return
		}
		_, _ = Interpret(ops, graphics.Identity())
	})
}

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
		// Phase 5: shading and patterns - with resources nil (see this
		// fuzz target's own doc comment on why), both "sh" and a pattern
		// name given to "scn"/"SCN" always exercise the "no /Resources"
		// tolerance/error path rather than reaching internal/graphics's
		// shading math; that math is instead covered directly by
		// shading_test.go and internal/graphics/shading_test.go.
		"/Sh1 sh",
		"/Pattern cs /P1 scn 0 0 5 5 re f",
		// Phase 5: Form XObjects - with resources nil, "Do" naming a Form
		// always hits the "no /Resources" tolerance path (lookupXObject),
		// never reaching form.go's real recursion/BBox/Matrix logic; that
		// is instead covered directly by form_test.go.
		"q 1 0 0 1 10 10 cm /Fm1 Do Q",
		// Phase 5: ExtGState alpha/blend mode - with resources nil, "gs"
		// always hits the "no /Resources" tolerance path; real coverage
		// is in extgstate_test.go.
		"/GS1 gs 0 0 5 5 re f",
		"(text) Tj BT ET",
		"/Im1 Do",
		"[[[[1]]]] op",
		// Phase 3: inline images, including a dictionary value that is
		// itself an (illegal, but syntactically parseable) indirect
		// reference - a nil resolver (this fuzz target never supplies
		// one; see the Interpret call below) must handle this without a
		// nil-interface method call panic.
		"BI /W 4 /H 4 /BPC 8 /CS /RGB ID \x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00 EI",
		"BI /W 1 /H 1 /IM true ID \x00 EI",
		"BI /W 1 /H 1 /CS 5 0 R ID \x00 EI",
		"BI /W 1 /H 1 /F /Fl ID notreallyflatedata EI",
		// Phase 4: text operators, including malformed/edge-case shapes
		// (an unresolvable font name, a text-showing operator before any
		// "Tf", mismatched operand counts, a deeply nested TJ array
		// element) - a nil-resources content stream can never reach a
		// real internal/fonts.Font (see this fuzz target's own doc
		// comment on why "Do"/"Tf" stay unresolvable here), so this
		// mainly steers the fuzzer toward this package's own operand
		// parsing and text-matrix bookkeeping robustness, not
		// internal/fonts itself (which has its own FuzzParseSfnt).
		"BT /F1 12 Tf 0 0 Td (Hello, world!) Tj ET",
		"BT 10 TL 2 Tc 3 Tw 50 Tz 1 Ts 2 Tr /F1 12 Tf (A) ' 0 0 (B) \" ET",
		"BT /F1 12 Tf [(A) -100 (B) 200 (C)] TJ ET",
		"(no BT) Tj",
		"BT Tj TJ Tf Td TD Tm T* ET",
		"BT /F1 Tf ET",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		ops, err := Parse(data)
		if err != nil {
			return
		}
		// A real (if empty-backed) resolver, rather than nil, is used
		// here specifically so a fuzzer-mutated inline image ("BI") -
		// which needs no /Resources dictionary to be reachable, unlike
		// "Do" - actually flows all the way into internal/image.Decode
		// instead of being skipped by the nil-resolver guard in
		// doInlineImage; see TestInterpretDoAndInlineImageWithNilResolverDoesNotPanic
		// in image_test.go for the dedicated (non-fuzz) coverage of that
		// guard itself. resources stays nil, since a fuzzer mutating raw
		// bytes has no realistic way to construct a matching /XObject
		// entry for "Do" to find - "Do" is covered by the unit tests in
		// image_test.go instead.
		_, _ = Interpret(ops, graphics.Identity(), nil, &fakeResolver{})
	})
}

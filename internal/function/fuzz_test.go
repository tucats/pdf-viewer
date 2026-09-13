package function

import "testing"

// FuzzType4Program feeds arbitrary byte slices into the Type 4 (PostScript
// calculator) interpreter's own parse-then-evaluate pipeline
// (parseType4Program followed by Eval), matching this project's
// established pattern of a fuzz target alongside any parser that reads
// hostile or corrupted embedded content - see, for example,
// internal/fonts/fuzz_test.go's FuzzParseSfnt for a TrueType font
// program, or internal/filter's own FuzzDecode. A Type 4 function's
// stream is exactly this kind of input: arbitrary bytes from a PDF file
// this project does not control, interpreted as a tiny embedded program
// (see this package's type4.go doc comment) rather than looked up or
// computed by a fixed formula the way Type 0/2/3 are - the one function
// type in this package where "the input itself is code" makes a fuzz
// target worth having.
//
// The property under test is "never panics, always terminates promptly" -
// not any particular numeric answer. A malformed or adversarial program
// is documented (see parseType4Program's and (*type4).Eval's own doc
// comments) to fail closed at parse time with an error, or otherwise to
// produce some finite (if meaningless) output at evaluation time, never
// to crash or hang; a hang would be caught by the fuzzer's own
// per-execution timeout, and a panic fails the fuzz run directly.
func FuzzType4Program(f *testing.F) {
	seeds := []string{
		"{ 1 exch sub dup dup dup }",
		"{ dup 0 gt { 1 } { -1 } ifelse }",
		"{ dup 0 gt { pop 1 } { dup 0 lt { pop -1 } { pop 0 } ifelse } ifelse }",
		"{ 3 1 roll 2 copy 1 index exch }",
		"{ % a comment\n 2 mul add }",
		"{ true false and or not }",
		"{ if }",
		"{ { 1 } if }",
		"{",
		"",
		"{ NaN Inf }",
		"{{{{{{{{{{{{{{{{{{{{}}}}}}}}}}}}}}}}}}}}",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		prog, err := parseType4Program(data)
		if err != nil {
			return
		}
		// A successful parse should be safely evaluable regardless of
		// what it actually computes - build a 1-input, 4-output function
		// around it (wide enough to observe several stack underflow/
		// overflow shapes without itself being the thing under test) and
		// run it at a couple of representative inputs.
		fn := &type4{
			domain:   []float64{0, 1},
			rangeArr: []float64{-1e6, 1e6, -1e6, 1e6, -1e6, 1e6, -1e6, 1e6},
			prog:     prog,
		}
		for _, x := range []float64{0, 0.5, 1} {
			_, _ = fn.Eval([]float64{x})
		}
	})
}

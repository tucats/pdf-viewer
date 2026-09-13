package function

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// mustParseType4 builds a Type 4 function dictionary+stream out of domain,
// rangeArr, and src (the program's literal PostScript-calculator source
// text, braces included - e.g. "{ 1 exch sub }"), and parses it,
// failing the test immediately if parsing itself fails. Every test in
// this file that only cares about *evaluating* an already-valid program
// goes through this helper so the parsing boilerplate is not repeated at
// every call site - the same role fakeResolver plays for every other
// _test.go file in this package.
func mustParseType4(t *testing.T, domain, rangeArr []float64, src string) Function {
	t.Helper()
	dict := syntax.Dictionary{
		"FunctionType": syntax.Integer(4),
		"Domain":       numArray(domain...),
		"Range":        numArray(rangeArr...),
	}
	stream := syntax.Stream{Dict: dict, Raw: []byte(src)}
	fn, err := parseType4(&fakeResolver{}, dict, stream)
	if err != nil {
		t.Fatalf("parseType4(%q): %v", src, err)
	}
	return fn
}

// evalType4 evaluates fn at inputs, failing the test if Eval itself
// returns an error (which, per the Function interface's doc comment,
// only ever happens for a wrong input count - never for the program's
// own content, however malformed).
func evalType4(t *testing.T, fn Function, inputs ...float64) []float64 {
	t.Helper()
	out, err := fn.Eval(inputs)
	if err != nil {
		t.Fatalf("Eval(%v): %v", inputs, err)
	}
	return out
}

// wideRange is a Range wide enough that none of this file's arithmetic
// test cases ever accidentally hits Eval's own output clipping - tests
// that specifically want to exercise clipping build their own narrow
// Range instead (see TestType4OutputIsClippedToRange).
var wideRange = []float64{-1e6, 1e6}

// rangeOfWidth builds an n-pair Range, each pair wide enough to never
// clip - used by TestType4StackOperators, where each subtest needs
// Eval to hand back a different number of final stack values (a stack
// operator's whole point is changing how many values end up on the
// stack), unlike this file's other tests, which mostly stick to one
// input and one output.
func rangeOfWidth(n int) []float64 {
	r := make([]float64, 0, 2*n)
	for i := 0; i < n; i++ {
		r = append(r, -1e6, 1e6)
	}
	return r
}

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// --- One-instruction-at-a-time coverage of every operator in Annex B's ---
// --- allowed set (see allowedType4Operators), grouped the same way   ---
// --- applyType4Operator's own comments group them.                  ---

func TestType4Arithmetic(t *testing.T) {
	cases := []struct {
		name string
		src  string
		in   float64
		want float64
	}{
		{"abs positive", "{ abs }", 3, 3},
		{"abs negative", "{ abs }", -3, 3},
		{"add", "{ 2 add }", 5, 7},
		{"atan quadrant 1", "{ 1 atan }", 1, 45}, // "1 1 atan" -> num=1, den=1
		{"ceiling", "{ ceiling }", 1.2, 2},
		{"ceiling negative", "{ ceiling }", -1.8, -1},
		{"cos 0", "{ cos }", 0, 1},
		{"cos 180", "{ cos }", 180, -1},
		{"cvi truncates toward zero", "{ cvi }", 2.9, 2},
		{"cvi truncates negative toward zero", "{ cvi }", -2.9, -2},
		{"cvr is a no-op", "{ cvr }", 2.5, 2.5},
		{"div", "{ 2 div }", 5, 2.5},
		{"div by zero is sanitized to 0", "{ 0 div }", 5, 0},
		{"exp", "{ 3 exp }", 2, 8}, // 2^3
		{"floor", "{ floor }", 1.8, 1},
		{"floor negative", "{ floor }", -1.2, -2},
		{"idiv truncates", "{ 2 idiv }", 7, 3},
		{"idiv by zero is 0, not a panic", "{ 0 idiv }", 7, 0},
		{"ln", "{ ln }", math.E, 1},
		{"ln of a negative number is sanitized to 0", "{ ln }", -1, 0},
		{"log base 10", "{ log }", 100, 2},
		{"mod", "{ 3 mod }", 7, 1},
		{"mod by zero is 0, not a panic", "{ 0 mod }", 7, 0},
		{"mul", "{ 3 mul }", 4, 12},
		{"neg", "{ neg }", 4, -4},
		{"neg of negative", "{ neg }", -4, 4},
		{"round half rounds toward +Inf", "{ round }", 2.5, 3},
		{"round half rounds toward +Inf (negative)", "{ round }", -2.5, -2},
		{"sin 0", "{ sin }", 0, 0},
		{"sin 90", "{ sin }", 90, 1},
		{"sqrt", "{ sqrt }", 9, 3},
		{"sqrt of a negative number is sanitized to 0", "{ sqrt }", -1, 0},
		{"sub", "{ 3 sub }", 10, 7},
		{"truncate drops fraction", "{ truncate }", 2.9, 2},
		{"truncate toward zero (negative)", "{ truncate }", -2.9, -2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fn := mustParseType4(t, []float64{-1e6, 1e6}, wideRange, c.src)
			out := evalType4(t, fn, c.in)
			if len(out) != 1 || !approxEqual(out[0], c.want) {
				t.Fatalf("%s Eval(%v) = %v, want [%v]", c.src, c.in, out, c.want)
			}
		})
	}
}

func TestType4RelationalBooleanAndBitwise(t *testing.T) {
	cases := []struct {
		name string
		src  string
		in   float64
		want float64
	}{
		{"eq true", "{ 5 eq }", 5, 1},
		{"eq false", "{ 5 eq }", 4, 0},
		{"ne true", "{ 5 ne }", 4, 1},
		{"gt true", "{ 5 gt }", 6, 1},
		{"gt false", "{ 5 gt }", 5, 0},
		{"ge true (equal)", "{ 5 ge }", 5, 1},
		{"lt true", "{ 5 lt }", 4, 1},
		{"le true (equal)", "{ 5 le }", 5, 1},
		{"true pushes 1", "{ pop true }", 0, 1},
		{"false pushes 0", "{ pop false }", 0, 0},
		{"and of booleans is logical", "{ 1 and }", 1, 1},
		{"and of booleans, one false", "{ 0 and }", 1, 0},
		{"and of integers is bitwise", "{ 5 and }", 3, 1}, // 3 & 5 = 1
		{"or of booleans is logical", "{ 0 or }", 0, 0},
		{"or of integers is bitwise", "{ 5 or }", 3, 7}, // 3 | 5 = 7
		{"xor of booleans is logical", "{ 1 xor }", 1, 0},
		{"xor of integers is bitwise", "{ 5 xor }", 3, 6}, // 3 ^ 5 = 6
		{"not of a boolean flips it", "{ not }", 1, 0},
		{"not of a boolean flips it (0)", "{ not }", 0, 1},
		{"not of an integer is bitwise complement", "{ not }", 5, -6}, // ^5 == -6
		{"bitshift left", "{ 2 bitshift }", 1, 4},                     // 1 << 2
		{"bitshift right", "{ -1 bitshift }", 8, 4},                   // 8 >> 1
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fn := mustParseType4(t, []float64{-1e6, 1e6}, wideRange, c.src)
			out := evalType4(t, fn, c.in)
			if len(out) != 1 || !approxEqual(out[0], c.want) {
				t.Fatalf("%s Eval(%v) = %v, want [%v]", c.src, c.in, out, c.want)
			}
		})
	}
}

// TestType4StackOperators exercises pop/exch/dup/copy/index/roll by
// starting from a known 3-value stack (pushed via the function's own
// three inputs) and checking every value that remains, in stack order
// (bottom to top - see (*type4).Eval's doc comment on why no reversal is
// needed), against each operator's documented stack effect.
func TestType4StackOperators(t *testing.T) {
	threeInputDomain := []float64{-1e6, 1e6, -1e6, 1e6, -1e6, 1e6}

	t.Run("pop discards the top", func(t *testing.T) {
		fn := mustParseType4(t, threeInputDomain, []float64{-1e6, 1e6, -1e6, 1e6}, "{ pop }")
		out := evalType4(t, fn, 1, 2, 3)
		if got := []float64{1, 2}; len(out) != 2 || !approxEqual(out[0], got[0]) || !approxEqual(out[1], got[1]) {
			t.Fatalf("Eval(1,2,3) after pop = %v, want %v", out, got)
		}
	})

	t.Run("exch swaps the top two", func(t *testing.T) {
		fn := mustParseType4(t, threeInputDomain, rangeOfWidth(3), "{ exch }")
		out := evalType4(t, fn, 1, 2, 3)
		want := []float64{1, 3, 2}
		for i := range want {
			if !approxEqual(out[i], want[i]) {
				t.Fatalf("Eval(1,2,3) after exch = %v, want %v", out, want)
			}
		}
	})

	t.Run("dup duplicates the top", func(t *testing.T) {
		fn := mustParseType4(t, threeInputDomain, rangeOfWidth(4), "{ dup }")
		out := evalType4(t, fn, 1, 2, 3)
		want := []float64{1, 2, 3, 3}
		for i := range want {
			if !approxEqual(out[i], want[i]) {
				t.Fatalf("Eval(1,2,3) after dup = %v, want %v", out, want)
			}
		}
	})

	t.Run("copy duplicates the top n in order", func(t *testing.T) {
		fn := mustParseType4(t, threeInputDomain, rangeOfWidth(5), "{ 2 copy }")
		out := evalType4(t, fn, 1, 2, 3)
		want := []float64{1, 2, 3, 2, 3}
		for i := range want {
			if !approxEqual(out[i], want[i]) {
				t.Fatalf("Eval(1,2,3) after 2 copy = %v, want %v", out, want)
			}
		}
	})

	t.Run("index copies the nth-from-top element", func(t *testing.T) {
		// "2 index" on (1 2 3) reaches two below the top (3 is 0, 2 is 1,
		// 1 is 2) - i.e. it copies the bottom value, 1.
		fn := mustParseType4(t, threeInputDomain, rangeOfWidth(4), "{ 2 index }")
		out := evalType4(t, fn, 1, 2, 3)
		want := []float64{1, 2, 3, 1}
		for i := range want {
			if !approxEqual(out[i], want[i]) {
				t.Fatalf("Eval(1,2,3) after 2 index = %v, want %v", out, want)
			}
		}
	})

	t.Run("0 index is the same as dup", func(t *testing.T) {
		fn := mustParseType4(t, threeInputDomain, rangeOfWidth(4), "{ 0 index }")
		out := evalType4(t, fn, 1, 2, 3)
		want := []float64{1, 2, 3, 3}
		for i := range want {
			if !approxEqual(out[i], want[i]) {
				t.Fatalf("Eval(1,2,3) after 0 index = %v, want %v", out, want)
			}
		}
	})

	// The PostScript Language Reference's own worked example for roll:
	// starting from (1 2 3) [a b c], "3 1 roll" leaves (3 1 2) [c a b].
	t.Run("roll performs a circular shift", func(t *testing.T) {
		fn := mustParseType4(t, threeInputDomain, rangeOfWidth(3), "{ 3 1 roll }")
		out := evalType4(t, fn, 1, 2, 3)
		want := []float64{3, 1, 2}
		for i := range want {
			if !approxEqual(out[i], want[i]) {
				t.Fatalf("Eval(1,2,3) after 3 1 roll = %v, want %v", out, want)
			}
		}
	})

	t.Run("roll with a negative j shifts the other way", func(t *testing.T) {
		// "3 -1 roll" is equivalent to "3 2 roll" (mod 3): (1 2 3) -> (2 3 1).
		fn := mustParseType4(t, threeInputDomain, rangeOfWidth(3), "{ 3 -1 roll }")
		out := evalType4(t, fn, 1, 2, 3)
		want := []float64{2, 3, 1}
		for i := range want {
			if !approxEqual(out[i], want[i]) {
				t.Fatalf("Eval(1,2,3) after 3 -1 roll = %v, want %v", out, want)
			}
		}
	})

	t.Run("roll with j a multiple of n is a no-op", func(t *testing.T) {
		fn := mustParseType4(t, threeInputDomain, rangeOfWidth(3), "{ 3 3 roll }")
		out := evalType4(t, fn, 1, 2, 3)
		want := []float64{1, 2, 3}
		for i := range want {
			if !approxEqual(out[i], want[i]) {
				t.Fatalf("Eval(1,2,3) after 3 3 roll = %v, want %v", out, want)
			}
		}
	})
}

// --- if/ifelse conditionals, including nesting ---------------------------

func TestType4If(t *testing.T) {
	fn := mustParseType4(t, []float64{-1e6, 1e6}, wideRange, "{ dup 0 gt { 100 add } if }")
	if out := evalType4(t, fn, 5); len(out) != 1 || !approxEqual(out[0], 105) {
		t.Fatalf("Eval(5) = %v, want [105] (condition true, block runs)", out)
	}
	if out := evalType4(t, fn, -5); len(out) != 1 || !approxEqual(out[0], -5) {
		t.Fatalf("Eval(-5) = %v, want [-5] (condition false, block skipped)", out)
	}
}

func TestType4IfElse(t *testing.T) {
	fn := mustParseType4(t, []float64{-1e6, 1e6}, wideRange, "{ 0 gt { 1 } { -1 } ifelse }")
	if out := evalType4(t, fn, 5); len(out) != 1 || out[0] != 1 {
		t.Fatalf("Eval(5) = %v, want [1]", out)
	}
	if out := evalType4(t, fn, -5); len(out) != 1 || out[0] != -1 {
		t.Fatalf("Eval(-5) = %v, want [-1]", out)
	}
}

// TestType4NestedIfElseComputesSign builds a three-way sign(x) function
// entirely out of nested ifelse blocks, confirming that a block parsed
// inside another block's own "then"/"else" branch (rather than at the
// program's top level) is handled identically - the case that most
// directly exercises parseType4Block's own recursion and
// execType4Block's matching recursive evaluation.
func TestType4NestedIfElseComputesSign(t *testing.T) {
	src := "{ dup 0 gt { pop 1 } { dup 0 lt { pop -1 } { pop 0 } ifelse } ifelse }"
	fn := mustParseType4(t, []float64{-1e6, 1e6}, wideRange, src)
	for in, want := range map[float64]float64{5: 1, -5: -1, 0: 0} {
		if out := evalType4(t, fn, in); len(out) != 1 || out[0] != want {
			t.Fatalf("Eval(%v) = %v, want [%v]", in, out, want)
		}
	}
}

// TestType4RichBlackTintTransform exercises the exact real-world pattern
// that motivated this phase (see docs/PLAN3.md's Phase 19): a single
// spot-color tint value complemented into four identical CMYK components
// via "1 exch sub" followed by three "dup"s, matching a DeviceN color
// space's tint-transform function.
func TestType4RichBlackTintTransform(t *testing.T) {
	fn := mustParseType4(t, []float64{0, 1}, []float64{0, 1, 0, 1, 0, 1, 0, 1}, "{ 1 exch sub dup dup dup }")
	out := evalType4(t, fn, 0.3)
	want := []float64{0.7, 0.7, 0.7, 0.7}
	for i := range want {
		if !approxEqual(out[i], want[i]) {
			t.Fatalf("Eval(0.3) = %v, want %v", out, want)
		}
	}
}

// --- Domain/Range handling -------------------------------------------------

func TestType4ClipsInputToDomain(t *testing.T) {
	fn := mustParseType4(t, []float64{0, 1}, wideRange, "{}")
	out := evalType4(t, fn, 5)
	if len(out) != 1 || out[0] != 1 {
		t.Fatalf("Eval(5) with Domain [0,1] and an empty program = %v, want [1] (input clipped, then passed through as the sole output)", out)
	}
}

func TestType4OutputIsClippedToRange(t *testing.T) {
	fn := mustParseType4(t, []float64{-1e6, 1e6}, []float64{0, 1}, "{ 100 mul }")
	out := evalType4(t, fn, 1) // raw output 100, clipped to Range [0,1]
	if len(out) != 1 || out[0] != 1 {
		t.Fatalf("Eval(1) = %v, want [1] (clipped to Range)", out)
	}
}

func TestType4MultipleInputs(t *testing.T) {
	fn := mustParseType4(t, []float64{-1e6, 1e6, -1e6, 1e6}, wideRange, "{ add }")
	out := evalType4(t, fn, 2, 3)
	if len(out) != 1 || out[0] != 5 {
		t.Fatalf("Eval(2,3) = %v, want [5]", out)
	}
}

func TestType4WrongInputCountIsMalformed(t *testing.T) {
	fn := mustParseType4(t, []float64{0, 1}, wideRange, "{}")
	if _, err := fn.Eval([]float64{0, 1}); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Eval with 2 inputs against a 1-input function: got %v, want an error wrapping ErrMalformed", err)
	}
}

// --- Parse-time validation --------------------------------------------------

func TestType4ParseRequiresDomain(t *testing.T) {
	dict := syntax.Dictionary{"FunctionType": syntax.Integer(4), "Range": numArray(0, 1)}
	stream := syntax.Stream{Dict: dict, Raw: []byte("{}")}
	if _, err := parseType4(&fakeResolver{}, dict, stream); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("parseType4 with no /Domain: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestType4ParseRequiresRange(t *testing.T) {
	// Unlike Type 2/3, /Range is required for Type 4 (see parseType4's
	// doc comment): there is no other way to learn the program's output
	// arity.
	dict := syntax.Dictionary{"FunctionType": syntax.Integer(4), "Domain": numArray(0, 1)}
	stream := syntax.Stream{Dict: dict, Raw: []byte("{}")}
	if _, err := parseType4(&fakeResolver{}, dict, stream); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("parseType4 with no /Range: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestType4ProgramMustStartWithBrace(t *testing.T) {
	if _, err := parseType4Program([]byte("1 2 add")); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("parseType4Program with no leading brace: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestType4UnterminatedBlockIsMalformed(t *testing.T) {
	if _, err := parseType4Program([]byte("{ 1 2 add")); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("parseType4Program with an unterminated block: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestType4TrailingContentAfterCloseIsMalformed(t *testing.T) {
	if _, err := parseType4Program([]byte("{ 1 2 add } garbage")); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("parseType4Program with trailing content: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestType4UnknownOperatorIsMalformed(t *testing.T) {
	if _, err := parseType4Program([]byte("{ frobnicate }")); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("parseType4Program with an unknown operator: got %v, want an error wrapping ErrMalformed", err)
	}
}

// TestType4NaNAndInfLiteralsAreRejected confirms parseType4Number's
// explicit refusal to treat the bare words "NaN"/"Inf" as numbers (see
// its own doc comment): they instead fall through to being treated as
// operator names, which are then rejected as unknown - a clean parse-time
// error rather than a live NaN/Inf ever reaching the operand stack.
func TestType4NaNAndInfLiteralsAreRejected(t *testing.T) {
	for _, word := range []string{"NaN", "Inf", "+Inf", "-Inf"} {
		if _, err := parseType4Program([]byte("{ " + word + " }")); !errors.Is(err, pdferror.ErrMalformed) {
			t.Fatalf("parseType4Program with literal %q: got %v, want an error wrapping ErrMalformed", word, err)
		}
	}
}

func TestType4DanglingIfIsMalformed(t *testing.T) {
	if _, err := parseType4Program([]byte("{ if }")); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("parseType4Program with a dangling \"if\": got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestType4DanglingIfElseIsMalformed(t *testing.T) {
	if _, err := parseType4Program([]byte("{ ifelse }")); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("parseType4Program with a dangling \"ifelse\": got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestType4BlockNotFollowedByIfOrIfElseIsMalformed(t *testing.T) {
	if _, err := parseType4Program([]byte("{ { 1 } add }")); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("parseType4Program with a stray block: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestType4TwoBlocksNotFollowedByIfElseIsMalformed(t *testing.T) {
	if _, err := parseType4Program([]byte("{ { 1 } { 2 } add }")); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("parseType4Program with two blocks not followed by ifelse: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestType4CommentsAreIgnored(t *testing.T) {
	fn := mustParseType4(t, []float64{-1e6, 1e6}, wideRange, "{ % this adds two\n2 add % end\n}")
	out := evalType4(t, fn, 3)
	if len(out) != 1 || out[0] != 5 {
		t.Fatalf("Eval(3) with a commented program = %v, want [5]", out)
	}
}

// --- Safety bounds ----------------------------------------------------------

// TestType4TooDeeplyNestedBlocksIsMalformed builds a program with more
// nested "{" blocks than maxType4ParseDepth permits (each one immediately
// closed and consumed by "if" so the overall program stays syntactically
// valid otherwise) and confirms the parser rejects it with a clean error
// rather than overflowing its own call stack - see maxType4ParseDepth's
// doc comment for why a deeply-nested input is otherwise dangerous for a
// recursive-descent parser like this one.
func TestType4TooDeeplyNestedBlocksIsMalformed(t *testing.T) {
	src := "true "
	for i := 0; i < maxType4ParseDepth+10; i++ {
		src += "{ true "
	}
	src += "1"
	for i := 0; i < maxType4ParseDepth+10; i++ {
		src += " } if"
	}
	if _, err := parseType4Program([]byte("{ " + src + " }")); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("parseType4Program with %d nested blocks: got %v, want an error wrapping ErrMalformed", maxType4ParseDepth+10, err)
	}
}

// TestType4TooManyInstructionsIsMalformed confirms maxType4Instructions
// bounds a program's total flat size (a pathologically long, but not
// nested, program - the other shape of hostile input from
// TestType4TooDeeplyNestedBlocksIsMalformed's deeply-*nested* one).
func TestType4TooManyInstructionsIsMalformed(t *testing.T) {
	src := "{"
	for i := 0; i < maxType4Instructions+10; i++ {
		src += " pop true"
	}
	src += " }"
	if _, err := parseType4Program([]byte(src)); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("parseType4Program with more than %d instructions: got %v, want an error wrapping ErrMalformed", maxType4Instructions, err)
	}
}

// TestType4StackUnderflowDoesNotPanic confirms that an operator run
// against fewer operands than it needs degrades to using 0 for the
// missing ones (popType4's documented behavior) instead of panicking -
// the eval-time counterpart to every parse-time malformed-input test
// above.
func TestType4StackUnderflowDoesNotPanic(t *testing.T) {
	cases := []string{
		"{ add }", "{ sub }", "{ mul }", "{ div }", "{ exch }", "{ dup }",
		"{ pop }", "{ copy }", "{ index }", "{ roll }", "{ not }",
	}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			fn := mustParseType4(t, []float64{0, 1}, wideRange, src)
			// The only assertion is "this does not panic" - popType4's
			// documented 0-fallback means there is no single universally
			// "correct" numeric answer to check here, unlike every other
			// test in this file.
			evalType4(t, fn, 0)
		})
	}
}

// TestType4EvalTerminatesEvenWithAnExcessiveStepBudget is a white-box
// test (same package, so it can build a t4Instr tree directly rather than
// going through the parser) confirming maxType4Steps's own enforcement in
// execType4Block actually stops execution - a case parseType4Block's own
// maxType4Instructions bound makes unreachable through any program the
// public parseType4Program/parseType4 entry points can ever produce (see
// maxType4Steps's doc comment: with no loop construct in the language,
// total executed instructions can never exceed the parsed program's own
// size), but still worth exercising directly as insurance against a bug
// in that reasoning, or in a future change to this file.
func TestType4EvalTerminatesEvenWithAnExcessiveStepBudget(t *testing.T) {
	prog := make([]t4Instr, maxType4Steps+1000)
	for i := range prog {
		prog[i] = t4Instr{kind: t4InstrOp, name: "pop"}
	}
	stack := []float64{1}
	steps := 0
	done := make(chan struct{})
	go func() {
		execType4Block(prog, &stack, &steps)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("execType4Block did not terminate within 5s against an oversized instruction list")
	}
	if steps > maxType4Steps+1 {
		t.Fatalf("execType4Block executed %d steps, want at most %d (+1 for the check itself)", steps, maxType4Steps)
	}
}

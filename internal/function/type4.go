package function

import (
	"math"
	"strconv"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements a PDF Type 4 ("PostScript calculator") function
// (ISO 32000-1 7.10.5 and Annex B): a function whose stream content is
// not a lookup table (Type 0) or a small closed-form formula (Type 2/3)
// but a tiny embedded *program*, written in a stripped-down subset of the
// PostScript programming language. A real one, as it appears verbatim
// inside a PDF file, looks like this:
//
//	{ 1 exch sub dup dup dup }
//
// which takes one input (a "tint" value between 0 and 1, say, for a
// single spot-color ink) and produces four outputs: it computes 1-input,
// then duplicates that result three more times, leaving four copies on
// the stack - a "rich black" style Cyan/Magenta/Yellow/Black complement
// of a single ink amount, one of the two motivating real-world uses this
// project found for Type 4 (see docs/PLAN-V3.md's Phase 19).
//
// # If you are new to Go: what kind of program this file is
//
// This file is a small *interpreter*: a program that reads another
// program (the bytes of a Type 4 function's stream, plain ASCII text
// using PostScript's syntax) and carries out what it describes, one step
// at a time. Writing an interpreter always splits naturally into two
// separate jobs, and this file keeps them in two separate passes, exactly
// as this package's own doc comment says Type 0/2/3 already do ("parsed
// once, evaluated many times"):
//
//  1. Parsing (parseType4Program, parseType4Block, and the small
//     lexer/tokenizer type below): turn the raw bytes into a tree of Go
//     values (t4Instr) that are easy and fast to walk repeatedly. This
//     happens exactly once, when the PDF's Function object is first
//     parsed.
//  2. Evaluation (execType4Block and applyType4Operator): walk that
//     already-built tree, maintaining a small stack of numbers exactly
//     the way the PostScript language itself does, to compute actual
//     output values. This happens every time the function is called
//     (Eval) - for a shading, potentially once per pixel, which is why
//     doing the parsing work again on every call would be wasteful.

// maxType4ParseDepth bounds how many "{ ... }" procedure blocks may nest
// inside one another while parsing one Type 4 function's program.
//
// This package's parser is a classic "recursive descent" parser (see
// parseType4Block): every time it encounters a "{" that opens a nested
// block, it calls itself one level deeper to parse whatever is inside
// that block, and that nested call itself calls deeper still for any
// block nested inside *that*, and so on. Each of those nested calls uses
// up a small amount of the running program's own call stack - a
// finite resource - so a Type 4 function stream deliberately crafted
// with, say, a million consecutive "{" characters and no matching "}"
// could otherwise exhaust that stack and crash the whole program with a
// stack overflow, rather than cleanly failing to parse. Rejecting
// anything nested this deep is this package's usual defense against that
// class of hostile input (see maxType0Inputs's doc comment for another
// example of the same general policy) - no real Type 4 function nests
// more than a handful of if/ifelse levels deep, so this bound is never
// hit by legitimate content.
const maxType4ParseDepth = 64

// maxType4Instructions bounds the total number of instructions (numeric
// literals, operator calls, and if/ifelse conditionals, counted across
// every nested block combined) one Type 4 function's parsed program may
// contain. Real tint-transform and shading functions are a few dozen
// instructions at most; this is generous headroom above that - not a
// realistic value - existing only to keep a pathologically large function
// stream's parse time and memory use bounded.
const maxType4Instructions = 10000

// maxType4Steps bounds the total number of instructions one single call
// to (*type4).Eval may execute. The Type 4 language, unlike a general
// programming language, has no loop construct at all - no "for",
// "repeat", or "loop" (7.10.5 restricts a Type 4 program to exactly the
// operator set parseType4Block's allowedType4Operators enforces) - so
// simply running the parsed program from top to bottom, taking exactly
// one branch at every if/ifelse, can never visit more instructions than
// the program actually contains, which is itself already bounded by
// maxType4Instructions above. This counter is nonetheless kept as a
// second, independent safety net at evaluation time (rather than trusting
// that reasoning alone) - the same "belt and suspenders" caution this
// project applies elsewhere to interpreters over untrusted embedded
// content (see, for example, internal/fonts/type1.go's maxType1Steps for
// the Type 1 Charstring interpreter this package's own doc comment
// compares Type 4 against).
const maxType4Steps = 1 << 16

// t4InstrKind tags which of t4Instr's fields are meaningful for a given
// instruction - Go has no built-in "one of several shapes" (a "sum type",
// in language-design terms) the way some other languages do, so the
// idiomatic way to represent one is a plain struct with an explicit kind
// field like this one, where the kind says which of the struct's other
// fields actually apply. (The alternative - a Go interface with several
// implementing types - is this package's choice for the four *function
// types* themselves, see the Function interface in function.go; a plain
// tagged struct is a better fit here because these four instruction kinds
// are only ever consumed by this file's own execType4Block switch, never
// by unrelated code that only knows the general shape, which is the case
// where an interface earns its keep.)
type t4InstrKind int

const (
	// t4InstrNumber pushes a constant onto the operand stack - t4Instr's
	// num field holds the value.
	t4InstrNumber t4InstrKind = iota
	// t4InstrOp calls one named operator (anything in this file's
	// allowedType4Operators set except "if"/"ifelse") - t4Instr's name
	// field holds the operator's name.
	t4InstrOp
	// t4InstrIf represents a parsed "{ ... } if": t4Instr's thenProc
	// field holds the block to run when the condition popped off the
	// stack at evaluation time is true (nonzero).
	t4InstrIf
	// t4InstrIfElse represents a parsed "{ ... } { ... } ifelse":
	// t4Instr's thenProc and elseProc fields hold the two blocks, exactly
	// one of which runs depending on the popped condition.
	t4InstrIfElse
)

// t4Instr is one parsed instruction of a Type 4 function's program - see
// t4InstrKind's doc comment for why this is a tagged struct rather than
// an interface, and the four t4InstrXxx constants for which fields apply
// to which kind.
type t4Instr struct {
	kind     t4InstrKind
	num      float64
	name     string
	thenProc []t4Instr
	elseProc []t4Instr
}

// allowedType4Operators is the exact operator vocabulary 7.10.5/Annex B
// permits inside a Type 4 function's program, deliberately much smaller
// than the full PostScript language it is a dialect of: no named
// procedures or variables, no loops, no string/array/dictionary
// operators, and no I/O - a Type 4 function only ever computes some
// numbers from some other numbers. "if" and "ifelse" are the two
// exceptions this set intentionally omits: they are recognized
// structurally by parseType4Block instead (see t4InstrIf/t4InstrIfElse),
// because - unlike every operator in this set - they each take a
// procedure block as one of their operands, which cannot be represented
// as a plain number sitting on the calculator's own operand stack the way
// every other operator's operands are.
//
// Rejecting anything outside this set at parse time (rather than only at
// evaluation time, or not at all) matches this package's usual policy of
// treating a malformed or non-conformant input as a clear, early error:
// per 7.10.5, a stream using an operator outside this list is not valid
// Type 4 function content at all, so there is no reasonable "silently
// ignore it" behavior to fall back on the way there sometimes is for a
// stray unknown key in a dictionary.
var allowedType4Operators = map[string]bool{
	// Arithmetic.
	"abs": true, "add": true, "atan": true, "ceiling": true, "cos": true,
	"cvi": true, "cvr": true, "div": true, "exp": true, "floor": true,
	"idiv": true, "ln": true, "log": true, "mod": true, "mul": true,
	"neg": true, "round": true, "sin": true, "sqrt": true, "sub": true,
	"truncate": true,
	// Relational, boolean, and bitwise.
	"and": true, "or": true, "xor": true, "not": true, "bitshift": true,
	"eq": true, "ne": true, "gt": true, "ge": true, "lt": true, "le": true,
	"true": true, "false": true,
	// Stack manipulation.
	"pop": true, "exch": true, "dup": true, "copy": true, "index": true,
	"roll": true,
}

// type4 implements a PDF Type 4 (PostScript calculator) function
// (7.10.5): prog is the already-parsed program (see parseType4Program);
// Eval runs it once per call, starting from a stack pre-loaded with this
// call's (clipped) inputs.
type type4 struct {
	domain   []float64 // 2*m entries, m = NumInputs()
	rangeArr []float64 // 2*n entries; required for Type 4, unlike Type 2/3
	prog     []t4Instr
}

// parseType4 reads a Type 4 function's dictionary and decodes its
// stream's content as a PostScript calculator program. Like Type 0 (see
// parseType0), a Type 4 function is always a stream, never a plain
// dictionary - its program text *is* the stream's data - so stream is
// required to be non-nil, which parseSingle (function.go) already
// guarantees before calling this.
func parseType4(r Resolver, dict syntax.Dictionary, stream syntax.Stream) (Function, error) {
	domain, err := requiredNumberArray(r, dict, "Domain")
	if err != nil {
		return nil, err
	}
	if len(domain) == 0 || len(domain)%2 != 0 {
		return nil, pdferror.Malformedf("Type 4 function /Domain must have a positive even number of entries, has %d", len(domain))
	}

	// Unlike Type 2/3 (single input only), a Type 4 function may declare
	// any number of inputs - most real ones declare exactly one (a tint
	// or a shading's parametric position), matching the trigger file's
	// own case, but the specification does not restrict it, so this
	// package does not either.
	//
	// /Range is required for Type 4 specifically (7.10.5's function
	// dictionary table), for the same reason it is required for Type 0:
	// a Type 4 program's own output arity is exactly len(Range)/2, with
	// no other way to determine it (unlike Type 2/3, whose output arity
	// instead comes from len(C0)/len(C1) or a subfunction's own arity).
	rangeArr, err := requiredNumberArray(r, dict, "Range")
	if err != nil {
		return nil, err
	}
	if len(rangeArr) == 0 || len(rangeArr)%2 != 0 {
		return nil, pdferror.Malformedf("Type 4 function /Range must have a positive even number of entries, has %d", len(rangeArr))
	}

	data, err := r.DecodeStream(stream)
	if err != nil {
		return nil, err
	}

	prog, err := parseType4Program(data)
	if err != nil {
		return nil, err
	}

	return &type4{domain: domain, rangeArr: rangeArr, prog: prog}, nil
}

func (f *type4) NumInputs() int  { return len(f.domain) / 2 }
func (f *type4) NumOutputs() int { return len(f.rangeArr) / 2 }

// Eval runs f's parsed program once, starting from a stack holding
// exactly this call's (domain-clipped) inputs, bottom-to-top in argument
// order, and reads the outputs back off the top of the stack once
// execution finishes.
//
// Per 7.10.5, "the output values shall be the n numbers that remain on
// the stack after the function's program has executed" (n = NumOutputs,
// i.e. len(Range)/2), in order: the bottommost of those n remaining
// values is output 0, and the topmost is output n-1. This implementation
// pushes with append and pops from the end of a Go slice (see
// popType4/pushType4 below), so the stack's slice order already matches
// that: index 0 is the bottom of the stack, and the last index is the
// top - no reversal is ever needed to go from "the stack" to "the output
// array" or back.
//
// A conformant, well-behaved program leaves exactly n values on the
// stack. This package tolerates a non-conformant one instead of erroring
// out (see the Function interface's doc comment: Eval only ever returns
// an error for a wrong *input* count, never for a malformed program body)
// by taking whatever is actually there: the top n values if there are at
// least n, or every available value aligned to the end (leaving the
// unknown, missing leading outputs as 0) if there are fewer - there is no
// way to know which specific outputs a broken program intended to
// produce, so this is a best-effort fallback, not a claim of
// correctness, consistent with this package's "a bad function can only
// ever produce a wrong-looking finite color" policy (see safeFloat).
func (f *type4) Eval(inputs []float64) ([]float64, error) {
	m := f.NumInputs()
	if len(inputs) != m {
		return nil, pdferror.Malformedf("Type 4 function expects %d input(s), got %d", m, len(inputs))
	}

	stack := make([]float64, 0, m+8)
	for i, x := range inputs {
		stack = append(stack, clip(x, f.domain[2*i], f.domain[2*i+1]))
	}

	steps := 0
	execType4Block(f.prog, &stack, &steps)

	n := f.NumOutputs()
	out := make([]float64, n)
	if len(stack) >= n {
		copy(out, stack[len(stack)-n:])
	} else {
		copy(out[n-len(stack):], stack)
	}
	for i := range out {
		out[i] = safeFloat(out[i])
	}
	return clipToRange(out, f.rangeArr), nil
}

// execType4Block runs prog's instructions in order against *stack,
// mutating it in place - the evaluation half of this file's interpreter
// (see the file's top doc comment). steps is a pointer shared across the
// whole call tree of nested execType4Block calls (one for the top-level
// program, and one more each time an if/ifelse enters whichever branch
// it takes), so that maxType4Steps bounds the *total* work done by one
// Eval call, not just one block's own instruction count.
//
// if/ifelse's own nesting depth needs no separate counter here: it is
// already bounded at parse time by maxType4ParseDepth (parseType4Block
// refuses to build a program nested deeper than that in the first
// place), and execType4Block's own recursion exactly mirrors that same
// nesting - so a program the parser accepted can never recurse deeper
// here than parsing already proved it does not.
func execType4Block(prog []t4Instr, stack *[]float64, steps *int) {
	for _, instr := range prog {
		*steps++
		if *steps > maxType4Steps {
			return
		}

		switch instr.kind {
		case t4InstrNumber:
			pushType4(stack, instr.num)
		case t4InstrOp:
			applyType4Operator(instr.name, stack)
		case t4InstrIf:
			if popType4(stack) != 0 {
				execType4Block(instr.thenProc, stack, steps)
			}
		case t4InstrIfElse:
			if popType4(stack) != 0 {
				execType4Block(instr.thenProc, stack, steps)
			} else {
				execType4Block(instr.elseProc, stack, steps)
			}
		}
	}
}

// pushType4 appends v to *stack. Go slices grow dynamically (append
// allocates a bigger backing array behind the scenes whenever the
// existing one is full), but the *variable* holding a slice - here, the
// caller's stack - does not automatically see a new backing array
// through a plain assignment; that is exactly why every function in this
// file that changes the stack takes a *[]float64 (a pointer to the slice
// variable, letting it write back a possibly-reallocated slice) rather
// than a plain []float64.
//
// v is routed through safeFloat here (rather than at each individual
// operator, or only once at the very end in Eval) so that every value
// that ever lands on the stack - a literal from the program text, or
// any operator's computed result - is already a finite number before
// anything else (a later operator, or Eval's own final output read)
// gets a chance to use it, exactly mirroring how type2.go and type3.go
// sanitize their own arithmetic.
func pushType4(stack *[]float64, v float64) {
	*stack = append(*stack, safeFloat(v))
}

// popType4 removes and returns the top value of *stack, or 0 if the
// stack is already empty. Returning a quiet 0 rather than panicking (Go's
// own reaction to indexing an empty slice) or returning an error is this
// package's deliberate choice for a malformed program that pops more
// values than it ever pushed - the same "a bad function can only ever
// produce a wrong-looking finite color, never crash" policy every other
// Eval method in this package already follows for its own kind of bad
// input.
func popType4(stack *[]float64) float64 {
	s := *stack
	if len(s) == 0 {
		return 0
	}
	v := s[len(s)-1]
	*stack = s[:len(s)-1]
	return v
}

// type4Int converts v to an int64 for the handful of Type 4 operators
// (idiv, mod, and/or/xor/not, bitshift, and copy/index/roll's own operand
// counts) that the specification defines in terms of integers rather than
// general real numbers. A plain Go int64(v) conversion is used nowhere
// else in this file's operator implementations because the Go language
// specification calls the result of converting a float64 outside int64's
// representable range "implementation-defined" - not a panic, but not a
// predictable value either - which is exactly the kind of "technically
// not undefined behavior, but not safe to reason about" situation this
// project avoids on principle for input it does not control. Clamping
// first keeps the conversion's result always exactly what it looks like
// it should be.
func type4Int(v float64) int64 {
	switch {
	case math.IsNaN(v):
		return 0
	case v >= math.MaxInt64:
		return math.MaxInt64
	case v <= math.MinInt64:
		return math.MinInt64
	default:
		return int64(v)
	}
}

// isType4Bool reports whether v looks like a PostScript boolean (exactly
// 0 or 1) rather than a general integer. Real PostScript keeps booleans
// and numbers as distinct value types; this package represents every
// Type 4 value uniformly as a plain float64 instead (simpler, and every
// one of this package's other function types already works in plain
// float64 throughout), so "and"/"or"/"xor"/"not" - which the
// specification defines to behave differently for the two cases (a
// logical operation on booleans, a bitwise one on integers) - use this
// 0-or-1 test as a practical stand-in for "is this actually a boolean
// here". Every place this package's own parsed programs produce a true
// boolean (true, false, eq, ne, gt, ge, lt, le) produces exactly 0 or 1,
// so this test is exact for every value this interpreter itself ever
// computes; a hand-crafted adversarial input could still contund a
// literal 1 with a genuine boolean, but the two operations agree exactly
// at 0 and 1 anyway (bitwise AND of 0/1 values equals their logical AND),
// so there is no case where this distinction actually changes the
// answer.
func isType4Bool(v float64) bool {
	return v == 0 || v == 1
}

// boolFloat converts a Go bool into this package's 0.0/1.0 encoding of a
// PostScript boolean (see isType4Bool).
func boolFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// applyType4Operator carries out one named operator (anything in
// allowedType4Operators except "if"/"ifelse", which execType4Block
// handles directly) against *stack, popping whatever operands it needs
// and pushing its result(s). Every operand pop below uses popType4 (never
// a raw slice index), so a malformed program that runs an operator
// without enough values already on the stack degrades to using 0 for the
// missing operand(s) rather than panicking - see popType4's doc comment.
//
// Each PDF operator's stack-effect comment below (for example, "num1
// num2 add sum") follows the exact notation the PostScript Language
// Reference and 7.10.5/Annex B themselves use: values left of the
// operator's own name are consumed (read in top-of-stack-last order, so
// the rightmost of them was pushed most recently), values to its right
// are what get pushed as a result.
func applyType4Operator(name string, stack *[]float64) {
	switch name {

	// --- Arithmetic -----------------------------------------------------

	case "abs": // num1 abs num2
		pushType4(stack, math.Abs(popType4(stack)))
	case "add": // num1 num2 add sum
		b, a := popType4(stack), popType4(stack)
		pushType4(stack, a+b)
	case "atan": // num den atan angle (degrees, 0-360)
		den, num := popType4(stack), popType4(stack)
		angle := math.Atan2(num, den) * 180 / math.Pi
		if angle < 0 {
			angle += 360
		}
		pushType4(stack, angle)
	case "ceiling": // num1 ceiling num2
		pushType4(stack, math.Ceil(popType4(stack)))
	case "cos": // angle cos real (angle in degrees)
		pushType4(stack, math.Cos(popType4(stack)*math.Pi/180))
	case "cvi": // num cvi int (truncate toward 0)
		pushType4(stack, math.Trunc(popType4(stack)))
	case "cvr": // num cvr real
		// Every value in this interpreter is already a float64 ("real"),
		// so converting "to real" is a true no-op: leave the operand
		// exactly where it already is on the stack.
	case "div": // num1 num2 div quotient (real division)
		b, a := popType4(stack), popType4(stack)
		// b == 0 produces +/-Inf or NaN here, which pushType4's safeFloat
		// call quietly turns into 0 - no explicit zero-check needed,
		// unlike idiv/mod below (Go's *integer* division by zero panics,
		// where float64 division does not).
		pushType4(stack, a/b)
	case "exp": // base exponent exp real (base ^ exponent)
		exponent, base := popType4(stack), popType4(stack)
		pushType4(stack, math.Pow(base, exponent))
	case "floor": // num1 floor num2
		pushType4(stack, math.Floor(popType4(stack)))
	case "idiv": // int1 int2 idiv quotient (integer division, truncated)
		b, a := popType4(stack), popType4(stack)
		bi, ai := type4Int(b), type4Int(a)
		if bi == 0 {
			pushType4(stack, 0)
			return
		}
		pushType4(stack, float64(ai/bi))
	case "ln": // num ln real (natural log)
		pushType4(stack, math.Log(popType4(stack)))
	case "log": // num log real (base-10 log)
		pushType4(stack, math.Log10(popType4(stack)))
	case "mod": // int1 int2 mod remainder
		b, a := popType4(stack), popType4(stack)
		bi, ai := type4Int(b), type4Int(a)
		if bi == 0 {
			pushType4(stack, 0)
			return
		}
		pushType4(stack, float64(ai%bi))
	case "mul": // num1 num2 mul product
		b, a := popType4(stack), popType4(stack)
		pushType4(stack, a*b)
	case "neg": // num1 neg num2
		pushType4(stack, -popType4(stack))
	case "round": // num1 round num2 (nearest integer, ties toward +Inf)
		pushType4(stack, math.Floor(popType4(stack)+0.5))
	case "sin": // angle sin real (angle in degrees)
		pushType4(stack, math.Sin(popType4(stack)*math.Pi/180))
	case "sqrt": // num sqrt real
		pushType4(stack, math.Sqrt(popType4(stack)))
	case "sub": // num1 num2 sub difference
		b, a := popType4(stack), popType4(stack)
		pushType4(stack, a-b)
	case "truncate": // num1 truncate num2 (drop the fractional part)
		pushType4(stack, math.Trunc(popType4(stack)))

	// --- Relational, boolean, and bitwise --------------------------------

	case "and": // bool1 bool2 and bool3 -or- int1 int2 and int3
		b, a := popType4(stack), popType4(stack)
		if isType4Bool(a) && isType4Bool(b) {
			pushType4(stack, boolFloat(a != 0 && b != 0))
			return
		}
		pushType4(stack, float64(type4Int(a)&type4Int(b)))
	case "or": // bool1 bool2 or bool3 -or- int1 int2 or int3
		b, a := popType4(stack), popType4(stack)
		if isType4Bool(a) && isType4Bool(b) {
			pushType4(stack, boolFloat(a != 0 || b != 0))
			return
		}
		pushType4(stack, float64(type4Int(a)|type4Int(b)))
	case "xor": // bool1 bool2 xor bool3 -or- int1 int2 xor int3
		b, a := popType4(stack), popType4(stack)
		if isType4Bool(a) && isType4Bool(b) {
			pushType4(stack, boolFloat((a != 0) != (b != 0)))
			return
		}
		pushType4(stack, float64(type4Int(a)^type4Int(b)))
	case "not": // bool1 not bool2 -or- int1 not int2
		a := popType4(stack)
		if isType4Bool(a) {
			pushType4(stack, 1-a)
			return
		}
		pushType4(stack, float64(^type4Int(a)))
	case "bitshift": // int1 shift bitshift int2
		shift, v := popType4(stack), popType4(stack)
		vi, s := type4Int(v), type4Int(shift)
		// Go itself never panics or overflows-unsafely on a shift count
		// larger than the value's bit width (the result is simply all
		// zero bits, or a fully sign-extended value for a negative
		// right-shifted one) - unlike C, there is no undefined behavior
		// here to guard against, so an out-of-range shift count needs no
		// special-case beyond what type4Int already gives us.
		if s >= 0 {
			if s > 63 {
				s = 63
			}
			pushType4(stack, float64(vi<<uint(s)))
			return
		}
		s = -s
		if s > 63 {
			s = 63
		}
		pushType4(stack, float64(vi>>uint(s)))
	case "eq": // num1 num2 eq bool
		b, a := popType4(stack), popType4(stack)
		pushType4(stack, boolFloat(a == b))
	case "ne": // num1 num2 ne bool
		b, a := popType4(stack), popType4(stack)
		pushType4(stack, boolFloat(a != b))
	case "gt": // num1 num2 gt bool
		b, a := popType4(stack), popType4(stack)
		pushType4(stack, boolFloat(a > b))
	case "ge": // num1 num2 ge bool
		b, a := popType4(stack), popType4(stack)
		pushType4(stack, boolFloat(a >= b))
	case "lt": // num1 num2 lt bool
		b, a := popType4(stack), popType4(stack)
		pushType4(stack, boolFloat(a < b))
	case "le": // num1 num2 le bool
		b, a := popType4(stack), popType4(stack)
		pushType4(stack, boolFloat(a <= b))
	case "true": // -- true bool
		pushType4(stack, 1)
	case "false": // -- false bool
		pushType4(stack, 0)

	// --- Stack manipulation ----------------------------------------------

	case "pop": // any pop --
		popType4(stack)
	case "exch": // any1 any2 exch any2 any1
		s := *stack
		if len(s) < 2 {
			// Not enough operands to swap - leave the stack exactly as it
			// is rather than guessing at a missing operand's value.
			return
		}
		s[len(s)-1], s[len(s)-2] = s[len(s)-2], s[len(s)-1]
	case "dup": // any dup any any
		s := *stack
		if len(s) == 0 {
			pushType4(stack, 0)
			return
		}
		pushType4(stack, s[len(s)-1])
	case "copy": // any(n-1) ... any0 n copy any(n-1) ... any0 any(n-1) ... any0
		n := type4Int(popType4(stack))
		s := *stack
		if n <= 0 {
			return
		}
		if n > int64(len(s)) {
			// A malformed program asking to copy more values than exist -
			// copy everything there is rather than reading out of bounds.
			n = int64(len(s))
		}
		copied := append([]float64(nil), s[int64(len(s))-n:]...)
		*stack = append(s, copied...)
	case "index": // anyn ... any0 n index anyn ... any0 anyn
		n := type4Int(popType4(stack))
		s := *stack
		if n < 0 || n >= int64(len(s)) {
			// An out-of-range index has no well-defined value to copy -
			// push 0 rather than reading out of bounds.
			pushType4(stack, 0)
			return
		}
		pushType4(stack, s[int64(len(s))-1-n])
	case "roll": // any(n-1) ... any0 n j roll <top n elements circularly shifted by j>
		j := type4Int(popType4(stack))
		n := type4Int(popType4(stack))
		s := *stack
		if n <= 0 || n > int64(len(s)) {
			// n must name an actual range of existing stack elements;
			// anything else is a no-op rather than an out-of-bounds
			// access.
			return
		}
		// roll is defined as a *circular* shift, so only j's remainder
		// modulo n ever matters - reducing it first keeps the rest of
		// this working set small regardless of how large the program's
		// own j literal was. Go's % can return a negative result for a
		// negative j (unlike, say, Python's), so the "+n) % n" below
		// brings that back into [0, n) before use.
		j = ((j % n) + n) % n
		if j == 0 {
			return
		}
		top := s[int64(len(s))-n:]
		rolled := make([]float64, n)
		for i, v := range top {
			rolled[(int64(i)+j)%n] = v
		}
		copy(top, rolled)

	default:
		// Unreachable for any program parseType4Block accepted: it
		// checks every operator name against allowedType4Operators
		// before ever building a t4InstrOp instruction, so a name this
		// switch does not recognize could only reach here through a bug
		// in that check, not through any Type 4 function stream. Doing
		// nothing here (rather than panicking) keeps that hypothetical
		// bug's failure mode the same "wrong-looking but finite output"
		// this whole interpreter aims for, rather than a crash.
	}
}

// --- Parsing: turning a Type 4 function's raw stream bytes into a t4Instr
// tree (see this file's top doc comment for why this is a separate pass
// from evaluation) ---------------------------------------------------------

// t4TokenKind identifies which of a t4Token's fields are meaningful - the
// same "tagged struct" pattern t4InstrKind uses, for the same reason (see
// t4InstrKind's doc comment).
type t4TokenKind int

const (
	t4TokNumber t4TokenKind = iota
	t4TokName
	t4TokLBrace
	t4TokRBrace
	t4TokEOF
)

// t4Token is one lexical token of a Type 4 function's program text: a
// number, a bare operator name, a "{" or "}" delimiter, or the special
// "no more input" end-of-stream marker.
type t4Token struct {
	kind t4TokenKind
	num  float64
	name string
}

// t4Lexer turns a Type 4 function's raw program bytes into a stream of
// t4Token values, one at a time. "Lexer" (short for "lexical analyzer",
// also sometimes called a "tokenizer" or "scanner") is the standard name
// for this kind of low-level "turn characters into meaningful chunks"
// component of a parser - it deliberately knows nothing about the
// *structure* of a Type 4 program (which tokens are allowed to follow
// which), only about where one token ends and the next begins; enforcing
// structure is parseType4Block's job instead.
type t4Lexer struct {
	data []byte
	pos  int

	// peeked holds a token already read out of data but not yet consumed
	// by a call to next - Some(t4Token) in spirit, but since Go has no
	// built-in "optional value" type, a nil *t4Token plays that role here
	// instead: nil means "nothing buffered yet, read from data next
	// time", matching how this package's other pointer fields already
	// use nil to mean "absent" throughout.
	peeked *t4Token
}

// isType4Space reports whether b is PostScript/PDF whitespace - the same
// five bytes (space, tab, carriage return, line feed, form feed) plus NUL
// that the PDF specification itself treats as whitespace between tokens,
// which this project's own internal/syntax tokenizer for the surrounding
// PDF file syntax also recognizes; Type 4 function content, though a
// different (PostScript) syntax layered inside a PDF stream, uses the
// same whitespace conventions.
func isType4Space(b byte) bool {
	switch b {
	case 0x00, '\t', '\n', '\f', '\r', ' ':
		return true
	default:
		return false
	}
}

// next returns the next token from lx, consuming it (a second call to
// next will not return the same token again) - the lexer's main entry
// point.
func (lx *t4Lexer) next() t4Token {
	if lx.peeked != nil {
		tok := *lx.peeked
		lx.peeked = nil
		return tok
	}
	return lx.scan()
}

// peek returns the next token from lx without consuming it: the token
// peek returns is exactly the one the next call to next will also
// return. parseType4Block needs this one-token lookahead to decide, after
// parsing a "{ ... }" block, whether what immediately follows is "if"
// (this block is a conditional's body), another "{" (this block is
// ifelse's first branch, with a second block still to come), or neither
// (a malformed program) - see parseType4Block.
func (lx *t4Lexer) peek() t4Token {
	if lx.peeked == nil {
		tok := lx.scan()
		lx.peeked = &tok
	}
	return *lx.peeked
}

// scan reads and returns exactly one token starting at lx.pos, advancing
// lx.pos past it. This is the lexer's only method that actually looks at
// lx.data; next and peek both funnel through it via lx.peeked's small
// one-token buffer.
func (lx *t4Lexer) scan() t4Token {
	lx.skipSpaceAndComments()
	if lx.pos >= len(lx.data) {
		return t4Token{kind: t4TokEOF}
	}

	b := lx.data[lx.pos]
	if b == '{' {
		lx.pos++
		return t4Token{kind: t4TokLBrace}
	}
	if b == '}' {
		lx.pos++
		return t4Token{kind: t4TokRBrace}
	}

	// Anything else is a "word": a run of bytes up to the next
	// whitespace, brace, or comment - either a number (7.10.5's programs
	// use ordinary PostScript number syntax: an optional sign, digits,
	// an optional decimal point and/or exponent) or an operator name like
	// "add" or "dup".
	start := lx.pos
	for lx.pos < len(lx.data) {
		c := lx.data[lx.pos]
		if isType4Space(c) || c == '{' || c == '}' || c == '%' {
			break
		}
		lx.pos++
	}
	word := string(lx.data[start:lx.pos])
	if v, ok := parseType4Number(word); ok {
		return t4Token{kind: t4TokNumber, num: v}
	}
	return t4Token{kind: t4TokName, name: word}
}

// skipSpaceAndComments advances lx.pos past any run of whitespace and
// "%"-introduced comments (PostScript's comment syntax: "%" through the
// end of its line is ignored) sitting at the current position, leaving
// lx.pos at the start of the next real token (or at len(lx.data), if
// nothing but whitespace/comments remains).
func (lx *t4Lexer) skipSpaceAndComments() {
	for lx.pos < len(lx.data) {
		b := lx.data[lx.pos]
		if b == '%' {
			for lx.pos < len(lx.data) && lx.data[lx.pos] != '\n' && lx.data[lx.pos] != '\r' {
				lx.pos++
			}
			continue
		}
		if isType4Space(b) {
			lx.pos++
			continue
		}
		break
	}
}

// parseType4Number parses word as a Type 4 numeric literal using Go's own
// standard-library float parser, rejecting anything that is not a plain
// finite number.
//
// strconv.ParseFloat happily accepts the literal text "NaN", "Inf",
// "+Inf", and "-Inf" as valid floating-point values - useful for many Go
// programs, but not for this one: nothing in 7.10.5's grammar permits
// such a token inside a Type 4 program, and letting one slip through as a
// number would defeat this package's whole "every value that reaches the
// stack is already a safe, finite number" discipline (see pushType4)
// before it even gets a chance to run. Explicitly rejecting them here
// means a hostile stream containing the literal word "NaN" instead falls
// through to being treated as an *operator name* - which
// parseType4Block then rejects as "unknown operator" at parse time, a
// clean, early error, rather than a live NaN quietly entering evaluation.
func parseType4Number(word string) (float64, bool) {
	v, err := strconv.ParseFloat(word, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// parseType4Program parses data (a Type 4 function's already
// filter-decoded stream content) into a t4Instr tree ready for repeated
// evaluation by execType4Block.
//
// Per 7.10.5, a Type 4 function's entire program is always exactly one
// top-level "{ ... }" procedure - so parseType4Program's own job is just
// to consume that one opening brace, delegate everything inside it to
// parseType4Block, and confirm nothing but whitespace/comments follows
// the matching closing brace.
func parseType4Program(data []byte) ([]t4Instr, error) {
	lx := &t4Lexer{data: data}

	open := lx.next()
	if open.kind != t4TokLBrace {
		return nil, pdferror.Malformedf("Type 4 function program does not begin with a { ... } procedure")
	}

	instrCount := 0
	prog, err := parseType4Block(lx, 0, &instrCount)
	if err != nil {
		return nil, err
	}

	if trailing := lx.next(); trailing.kind != t4TokEOF {
		return nil, pdferror.Malformedf("Type 4 function program has content after its closing \"}\"")
	}

	return prog, nil
}

// parseType4Block parses one "{ ... }" block's contents - everything
// after an opening brace already consumed by the caller, up to (and
// including) its matching closing brace - into a flat []t4Instr,
// recursing into itself for each nested block it encounters along the
// way. depth counts how many blocks deep this call is nested (0 for the
// outermost, per parseType4Program) and is checked against
// maxType4ParseDepth on every recursive call to keep a hostile,
// deeply-nested input from overflowing this package's own call stack
// (see maxType4ParseDepth's doc comment). instrCount is a running total
// shared across the *entire* parse (every nested call included),
// incremented and bounds-checked each time an instruction is appended, so
// that maxType4Instructions bounds the parsed program's total size, not
// just one block's.
func parseType4Block(lx *t4Lexer, depth int, instrCount *int) ([]t4Instr, error) {
	if depth > maxType4ParseDepth {
		return nil, pdferror.Malformedf("Type 4 function nests { ... } procedure blocks more than %d levels deep", maxType4ParseDepth)
	}

	var instrs []t4Instr
	for {
		tok := lx.next()
		switch tok.kind {

		case t4TokEOF:
			return nil, pdferror.Malformedf("Type 4 function has an unterminated { ... } procedure block")

		case t4TokRBrace:
			return instrs, nil

		case t4TokNumber:
			if err := appendType4Instr(&instrs, t4Instr{kind: t4InstrNumber, num: tok.num}, instrCount); err != nil {
				return nil, err
			}

		case t4TokName:
			// "if" and "ifelse" only ever appear immediately after the
			// procedure block(s) they belong to (see the t4TokLBrace case
			// below, which is the only place that consumes them) - seeing
			// one here means it showed up on its own, with no preceding
			// block, which 7.10.5's grammar does not permit.
			if tok.name == "if" || tok.name == "ifelse" {
				return nil, pdferror.Malformedf("Type 4 function uses %q without a preceding { ... } procedure block", tok.name)
			}
			if !allowedType4Operators[tok.name] {
				return nil, pdferror.Malformedf("Type 4 function uses unknown operator %q", tok.name)
			}
			if err := appendType4Instr(&instrs, t4Instr{kind: t4InstrOp, name: tok.name}, instrCount); err != nil {
				return nil, err
			}

		case t4TokLBrace:
			thenProc, err := parseType4Block(lx, depth+1, instrCount)
			if err != nil {
				return nil, err
			}

			switch next := lx.peek(); {
			case next.kind == t4TokName && next.name == "if":
				lx.next() // consume "if"
				if err := appendType4Instr(&instrs, t4Instr{kind: t4InstrIf, thenProc: thenProc}, instrCount); err != nil {
					return nil, err
				}

			case next.kind == t4TokLBrace:
				lx.next() // consume the second block's opening "{"
				elseProc, err := parseType4Block(lx, depth+1, instrCount)
				if err != nil {
					return nil, err
				}
				after := lx.next()
				if after.kind != t4TokName || after.name != "ifelse" {
					return nil, pdferror.Malformedf("Type 4 function has two adjacent { ... } procedure blocks not followed by \"ifelse\"")
				}
				if err := appendType4Instr(&instrs, t4Instr{kind: t4InstrIfElse, thenProc: thenProc, elseProc: elseProc}, instrCount); err != nil {
					return nil, err
				}

			default:
				return nil, pdferror.Malformedf("Type 4 function has a { ... } procedure block not followed by \"if\" or \"ifelse\"")
			}
		}
	}
}

// appendType4Instr appends instr to *instrs, first incrementing
// *instrCount and rejecting the program once that running total exceeds
// maxType4Instructions - the one choke point every instruction added
// anywhere in parseType4Block (including every nested block) passes
// through, which is what lets a single shared counter bound the parsed
// program's total size regardless of how deeply nested the instruction
// that pushed it over the limit happens to be.
func appendType4Instr(instrs *[]t4Instr, instr t4Instr, instrCount *int) error {
	*instrCount++
	if *instrCount > maxType4Instructions {
		return pdferror.Malformedf("Type 4 function program has more than %d instructions", maxType4Instructions)
	}
	*instrs = append(*instrs, instr)
	return nil
}

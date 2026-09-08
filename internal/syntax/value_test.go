package syntax

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/tucats/pdf-viewer/internal/pdferror"
)

// parse is a small test helper that runs ParseValue over the given PDF
// source text and returns the resulting Object.
func parse(t *testing.T, src string) Object {
	t.Helper()
	lex := NewLexer(strings.NewReader(src))
	obj, err := ParseValue(lex, 0)
	if err != nil {
		t.Fatalf("ParseValue(%q): %v", src, err)
	}
	return obj
}

func TestParseValuePrimitives(t *testing.T) {
	tests := []struct {
		src  string
		want Object
	}{
		{"true", Boolean(true)},
		{"false", Boolean(false)},
		{"null", Null{}},
		{"123", Integer(123)},
		{"-17", Integer(-17)},
		{"+42", Integer(42)},
		{"3.14", Real(3.14)},
		{"-.5", Real(-0.5)},
		{"4.", Real(4)},
		{"/Type", Name("Type")},
		{"/A#42", Name("AB")},
		{"/", Name("")},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			got := parse(t, tt.src)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseValue(%q) = %#v, want %#v", tt.src, got, tt.want)
			}
		})
	}
}

func TestParseValueLiteralString(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{`(Hello, World!)`, "Hello, World!"},
		{`(He said \(hi\).)`, "He said (hi)."},
		{`(nested (parens) are fine)`, "nested (parens) are fine"},
		{"(line\\\ncontinuation)", "linecontinuation"},
		{`(tab\there)`, "tab\there"},
		{`(newline\nhere)`, "newline\nhere"},
		{`(octal \101\102\103)`, "octal ABC"},
		{`(unknown escape \q)`, "unknown escape q"},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			got := parse(t, tt.src)
			s, ok := got.(String)
			if !ok {
				t.Fatalf("ParseValue(%q) = %#v (%T), want String", tt.src, got, got)
			}
			if string(s) != tt.want {
				t.Errorf("ParseValue(%q) = %q, want %q", tt.src, s, tt.want)
			}
		})
	}
}

func TestParseValueHexString(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{"<48656C6C6F>", "Hello"},
		{"<48 65 6C 6C 6F>", "Hello"}, // whitespace between digits is legal
		{"<901FA3>", "\x90\x1f\xa3"},
		{"<48656C6C6F1>", "Hello\x10"}, // odd digit count: pad with trailing 0
		{"<>", ""},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			got := parse(t, tt.src)
			s, ok := got.(String)
			if !ok {
				t.Fatalf("ParseValue(%q) = %#v (%T), want String", tt.src, got, got)
			}
			if string(s) != tt.want {
				t.Errorf("ParseValue(%q) = %q, want %q", tt.src, s, tt.want)
			}
		})
	}
}

func TestParseValueArray(t *testing.T) {
	got := parse(t, "[1 2.5 (str) /Name true false null [1 2]]")
	want := Array{
		Integer(1),
		Real(2.5),
		String("str"),
		Name("Name"),
		Boolean(true),
		Boolean(false),
		Null{},
		Array{Integer(1), Integer(2)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestParseValueEmptyArray(t *testing.T) {
	got := parse(t, "[]")
	if arr, ok := got.(Array); !ok || len(arr) != 0 {
		t.Errorf("ParseValue(\"[]\") = %#v, want empty Array", got)
	}
}

func TestParseValueDictionary(t *testing.T) {
	got := parse(t, "<< /Type /Catalog /Count 3 /Nested << /A 1 >> >>")
	want := Dictionary{
		"Type":  Name("Catalog"),
		"Count": Integer(3),
		"Nested": Dictionary{
			"A": Integer(1),
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestParseValueDictionaryRepeatedKeyLastWins(t *testing.T) {
	got := parse(t, "<< /A 1 /A 2 >>")
	want := Dictionary{"A": Integer(2)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestParseValueReference(t *testing.T) {
	got := parse(t, "5 0 R")
	want := Reference{Number: 5, Generation: 0}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if got, want := want.String(), "5 0 R"; got != want {
		t.Errorf("Reference.String() = %q, want %q", got, want)
	}
}

func TestParseValueReferenceInArray(t *testing.T) {
	got := parse(t, "[3 0 R 4 0 R]")
	want := Array{
		Reference{Number: 3, Generation: 0},
		Reference{Number: 4, Generation: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

// TestParseValueTwoIntegersNotAReference guards the lookahead logic in
// parseNumberOrReference: "5 0" not followed by "R" must be parsed as
// two independent Integer values (in whatever context calls ParseValue
// twice), not misidentified as a reference or have tokens lost.
func TestParseValueTwoIntegersNotAReference(t *testing.T) {
	lex := NewLexer(strings.NewReader("5 0 obj"))

	first, err := ParseValue(lex, 0)
	if err != nil {
		t.Fatalf("first ParseValue: %v", err)
	}
	if first != Integer(5) {
		t.Fatalf("first value = %#v, want Integer(5)", first)
	}

	second, err := ParseValue(lex, 0)
	if err != nil {
		t.Fatalf("second ParseValue: %v", err)
	}
	if second != Integer(0) {
		t.Fatalf("second value = %#v, want Integer(0)", second)
	}

	// The "obj" keyword must still be available afterward, proving no
	// tokens were consumed or lost by the failed reference lookahead.
	tok, err := lex.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if tok.Kind != KindKeyword || tok.Text != "obj" {
		t.Fatalf("final Token = %#v, want keyword \"obj\"", tok)
	}
}

func TestParseValueDictionaryWithReferences(t *testing.T) {
	got := parse(t, "<< /Parent 2 0 R /Length 10 >>")
	want := Dictionary{
		"Parent": Reference{Number: 2, Generation: 0},
		"Length": Integer(10),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestParseValueStreamWithDirectLength(t *testing.T) {
	src := "<< /Length 11 >>\nstream\nHello World\nendstream"
	got := parse(t, src)
	stream, ok := got.(Stream)
	if !ok {
		t.Fatalf("got %#v (%T), want Stream", got, got)
	}
	if want := "Hello World"; string(stream.Raw) != want {
		t.Errorf("stream.Raw = %q, want %q", stream.Raw, want)
	}
	if want := (Dictionary{"Length": Integer(11)}); !reflect.DeepEqual(stream.Dict, want) {
		t.Errorf("stream.Dict = %#v, want %#v", stream.Dict, want)
	}
}

func TestParseValueStreamWithEmptyBody(t *testing.T) {
	src := "<< /Length 0 >>\nstream\n\nendstream"
	got := parse(t, src)
	stream, ok := got.(Stream)
	if !ok {
		t.Fatalf("got %#v (%T), want Stream", got, got)
	}
	if len(stream.Raw) != 0 {
		t.Errorf("stream.Raw = %q, want empty", stream.Raw)
	}
}

// TestParseValueStreamWithoutDirectLength exercises the fallback path
// used when /Length is missing or is an indirect reference this package
// cannot resolve on its own: scanning forward for a literal "endstream"
// keyword. This is what lets internal/parser hand this package a stream
// whose /Length is "9 0 R" without first having to resolve object 9
// itself.
func TestParseValueStreamWithoutDirectLength(t *testing.T) {
	src := "<< /Length 9 0 R >>\nstream\nHello World\nendstream"
	got := parse(t, src)
	stream, ok := got.(Stream)
	if !ok {
		t.Fatalf("got %#v (%T), want Stream", got, got)
	}
	if want := "Hello World"; string(stream.Raw) != want {
		t.Errorf("stream.Raw = %q, want %q", stream.Raw, want)
	}
}

// TestParseValueStreamWithDirectLengthConsumesEndstream is a regression
// test for a bug where reading a stream's raw bytes via a direct
// /Length (readExactly) stopped exactly at the end of that data without
// consuming the mandatory "endstream" keyword that follows - leaving it
// sitting in the token stream for whatever the caller reads next
// (typically "endobj") to trip over instead. This went unnoticed through
// Phase 1 because nothing in that phase ever resolved a stream object
// all the way through internal/parser.Document.Resolve (Page.Render
// always returned ErrUnsupported without reading /Contents); Phase 2's
// filter decoding and content-stream rendering both resolve stream
// objects directly, which is what surfaced it. See value.go's
// expectEndstream.
func TestParseValueStreamWithDirectLengthConsumesEndstream(t *testing.T) {
	lex := NewLexer(strings.NewReader("<< /Length 5 >>\nstream\nHello\nendstream\nendobj"))
	got, err := ParseValue(lex, 0)
	if err != nil {
		t.Fatalf("ParseValue: %v", err)
	}
	if _, ok := got.(Stream); !ok {
		t.Fatalf("got %#v (%T), want Stream", got, got)
	}

	tok, err := lex.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if tok.Kind != KindKeyword || tok.Text != "endobj" {
		t.Fatalf("Token after the stream = %#v, want keyword \"endobj\" (the \"endstream\" keyword was not consumed)", tok)
	}
}

// TestParseValueStreamMissingEndstreamKeywordIsMalformed confirms that a
// direct-/Length stream whose data is *not* actually followed by
// "endstream" - i.e. /Length itself is wrong - is reported as malformed
// rather than silently accepted or silently misreading whatever comes
// next as the stream's own content.
func TestParseValueStreamMissingEndstreamKeywordIsMalformed(t *testing.T) {
	lex := NewLexer(strings.NewReader("<< /Length 5 >>\nstream\nHello\nNOT-ENDSTREAM"))
	if _, err := ParseValue(lex, 0); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("error = %v, want ErrMalformed", err)
	}
}

func TestParseValueStreamCRLFAfterKeyword(t *testing.T) {
	src := "<< /Length 5 >>\nstream\r\nHello\r\nendstream"
	got := parse(t, src)
	stream, ok := got.(Stream)
	if !ok {
		t.Fatalf("got %#v (%T), want Stream", got, got)
	}
	if want := "Hello"; string(stream.Raw) != want {
		t.Errorf("stream.Raw = %q, want %q", stream.Raw, want)
	}
}

func TestParseValueUnterminatedArrayIsMalformed(t *testing.T) {
	lex := NewLexer(strings.NewReader("[1 2 3"))
	if _, err := ParseValue(lex, 0); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("error = %v, want ErrMalformed", err)
	}
}

func TestParseValueUnterminatedDictionaryIsMalformed(t *testing.T) {
	lex := NewLexer(strings.NewReader("<< /A 1"))
	if _, err := ParseValue(lex, 0); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("error = %v, want ErrMalformed", err)
	}
}

func TestParseValueDictionaryNonNameKeyIsMalformed(t *testing.T) {
	lex := NewLexer(strings.NewReader("<< 1 2 >>"))
	if _, err := ParseValue(lex, 0); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("error = %v, want ErrMalformed", err)
	}
}

func TestParseValueEmptyInputIsMalformed(t *testing.T) {
	lex := NewLexer(strings.NewReader(""))
	if _, err := ParseValue(lex, 0); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("error = %v, want ErrMalformed", err)
	}
}

// TestParseValueDeeplyNestedArrayIsRejected confirms the recursion
// guard: an absurdly deeply nested array must return ErrMalformed
// instead of overflowing the goroutine stack. See maxNestingDepth's doc
// comment.
func TestParseValueDeeplyNestedArrayIsRejected(t *testing.T) {
	src := strings.Repeat("[", maxNestingDepth+10) + strings.Repeat("]", maxNestingDepth+10)
	lex := NewLexer(strings.NewReader(src))
	if _, err := ParseValue(lex, 0); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("error = %v, want ErrMalformed", err)
	}
}

func TestParseValueReasonableNestingIsAccepted(t *testing.T) {
	src := strings.Repeat("[", 10) + "1" + strings.Repeat("]", 10)
	lex := NewLexer(strings.NewReader(src))
	if _, err := ParseValue(lex, 0); err != nil {
		t.Fatalf("ParseValue: %v", err)
	}
}

// TestParseValueSkipsCommentsAndWhitespace confirms comments (which run
// to end of line) and arbitrary whitespace between tokens are ignored,
// as the PDF specification requires.
func TestParseValueSkipsCommentsAndWhitespace(t *testing.T) {
	src := "  % this is a comment\n<<\n  /A 1 % inline comment\n  /B 2\n>>"
	got := parse(t, src)
	want := Dictionary{"A": Integer(1), "B": Integer(2)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestParseValueFromBytesReader(t *testing.T) {
	// Sanity check that ParseValue works the same over an io.Reader
	// backed by raw bytes (as it will be in production, reading from
	// internal/source.Reader.SectionFrom) and not just over strings.
	lex := NewLexer(bytes.NewReader([]byte("42")))
	got, err := ParseValue(lex, 0)
	if err != nil {
		t.Fatalf("ParseValue: %v", err)
	}
	if got != Integer(42) {
		t.Errorf("got %#v, want Integer(42)", got)
	}
}

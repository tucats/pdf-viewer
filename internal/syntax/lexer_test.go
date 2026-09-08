package syntax

import (
	"strings"
	"testing"
)

// tokens runs the Lexer to exhaustion over src and returns every Token
// it produced, stopping at (and not including) the terminating KindEOF.
func tokens(t *testing.T, src string) []Token {
	t.Helper()
	lex := NewLexer(strings.NewReader(src))
	var out []Token
	for {
		tok, err := lex.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if tok.Kind == KindEOF {
			return out
		}
		out = append(out, tok)
	}
}

func TestLexerSkipsWhitespaceAndComments(t *testing.T) {
	got := tokens(t, "  \t\n % a comment\r\n /A")
	if len(got) != 1 || got[0].Kind != KindName || string(got[0].Bytes) != "A" {
		t.Fatalf("tokens = %#v, want single Name(A) Token", got)
	}
}

func TestLexerDelimiters(t *testing.T) {
	got := tokens(t, "[ ] << >>")
	wantKinds := []Kind{KindArrayStart, KindArrayEnd, KindDictStart, KindDictEnd}
	if len(got) != len(wantKinds) {
		t.Fatalf("got %d tokens, want %d: %#v", len(got), len(wantKinds), got)
	}
	for i, k := range wantKinds {
		if got[i].Kind != k {
			t.Errorf("Token %d kind = %v, want %v", i, got[i].Kind, k)
		}
	}
}

// TestLexerPushBackOrder confirms PushBack restores tokens in
// last-in-first-out order, matching how ParseValue's lookahead pushes
// back up to two tokens (the generation number and "R" keyword, or
// whatever they turned out to be) and expects to see them again in
// their original order on the next two calls to Next.
func TestLexerPushBackOrder(t *testing.T) {
	lex := NewLexer(strings.NewReader("1 2 3"))

	a, err := lex.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	b, err := lex.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}

	// Push back in the order a caller doing two-Token lookahead would:
	// push the more-recently-read Token first, then the earlier one, so
	// that draining PushBack again yields them in original order.
	lex.PushBack(b)
	lex.PushBack(a)

	got1, err := lex.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	got2, err := lex.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	got3, err := lex.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}

	if got1.Text != "1" || got2.Text != "2" || got3.Text != "3" {
		t.Fatalf("got %q, %q, %q; want \"1\", \"2\", \"3\"", got1.Text, got2.Text, got3.Text)
	}
}

func TestLexerNameHexEscape(t *testing.T) {
	got := tokens(t, "/Name#20With#20Spaces")
	if len(got) != 1 || got[0].Kind != KindName {
		t.Fatalf("tokens = %#v, want single Name Token", got)
	}
	if want := "Name With Spaces"; string(got[0].Bytes) != want {
		t.Errorf("name = %q, want %q", got[0].Bytes, want)
	}
}

func TestLexerNameWithMalformedEscapeKeepsLiteralHash(t *testing.T) {
	got := tokens(t, "/A#ZZ")
	if len(got) != 1 || got[0].Kind != KindName {
		t.Fatalf("tokens = %#v, want single Name Token", got)
	}
	// "#ZZ" is not a valid two-hex-digit escape, so it is kept literally.
	if want := "A#ZZ"; string(got[0].Bytes) != want {
		t.Errorf("name = %q, want %q", got[0].Bytes, want)
	}
}

func TestLexerKeywords(t *testing.T) {
	got := tokens(t, "true false null obj endobj stream endstream xref trailer startxref R")
	want := []string{"true", "false", "null", "obj", "endobj", "stream", "endstream", "xref", "trailer", "startxref", "R"}
	if len(got) != len(want) {
		t.Fatalf("got %d tokens, want %d: %#v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Kind != KindKeyword || got[i].Text != w {
			t.Errorf("Token %d = %#v, want keyword %q", i, got[i], w)
		}
	}
}

// TestLexerToleratesStrayDelimiter confirms the lexer's documented
// tolerance policy (see the doc comment on Lexer.Next): an unexpected
// stray delimiter such as a lone ')' is skipped rather than causing an
// error, and tokenizing continues normally afterward.
func TestLexerToleratesStrayDelimiter(t *testing.T) {
	got := tokens(t, ") /A")
	if len(got) != 1 || got[0].Kind != KindName || string(got[0].Bytes) != "A" {
		t.Fatalf("tokens = %#v, want single Name(A) Token", got)
	}
}

// TestLexerPosTracksBytesConsumed exercises the property internal/parser
// relies on for its cross-reference recovery scan (see internal/parser's
// doc comment): Pos must report how many bytes of the underlying stream
// have actually been consumed at the moment a token was returned, so a
// caller can record "object 3 starts here" while scanning through a
// whole file token by token.
func TestLexerPosTracksBytesConsumed(t *testing.T) {
	lex := NewLexer(strings.NewReader("12 0 obj"))

	if got, want := lex.Pos(), int64(0); got != want {
		t.Fatalf("Pos() before reading = %d, want %d", got, want)
	}

	if _, err := lex.Next(); err != nil { // "12"
		t.Fatalf("Next: %v", err)
	}
	if got, want := lex.Pos(), int64(2); got != want {
		t.Fatalf("Pos() after \"12\" = %d, want %d", got, want)
	}

	posBeforeObj := lex.Pos()
	if _, err := lex.Next(); err != nil { // "0"
		t.Fatalf("Next: %v", err)
	}
	tok, err := lex.Next() // "obj"
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if tok.Kind != KindKeyword || tok.Text != "obj" {
		t.Fatalf("token = %#v, want keyword \"obj\"", tok)
	}
	if got := lex.Pos(); got <= posBeforeObj {
		t.Fatalf("Pos() did not advance while reading \"0 obj\": before=%d, after=%d", posBeforeObj, got)
	}
	if got, want := lex.Pos(), int64(len("12 0 obj")); got != want {
		t.Fatalf("Pos() at end of input = %d, want %d", got, want)
	}
}

func TestLexerEmptyInputYieldsNoTokens(t *testing.T) {
	got := tokens(t, "")
	if len(got) != 0 {
		t.Fatalf("tokens = %#v, want none", got)
	}
}

// TestLexerNeverHangsOnUnterminatedConstructs is a fast, deterministic
// smoke test for the same property internal/syntax's fuzz target
// (fuzz_test.go) checks more broadly: tokenizing must always terminate
// (by reaching EOF) even for inputs missing their closing delimiter,
// since a lexer that blocked forever on such input would let a
// malformed file hang any code that tries to open it.
func TestLexerNeverHangsOnUnterminatedConstructs(t *testing.T) {
	inputs := []string{
		"(unterminated string",
		"<unterminated hex string",
		"<<unterminated dict",
		"/unterminated#",
		"\\",
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			// tokens() itself will call t.Fatalf if Next ever returns a
			// non-EOF error, and will simply return once it observes
			// KindEOF - so this test "hanging" (rather than failing
			// quickly) is exactly the failure mode it exists to catch.
			_ = tokens(t, in)
		})
	}
}

// --- Inline image support (Phase 3) ----------------------------------
//
// internal/content's inline-image parsing (BI/ID/EI) drives these three
// Lexer methods directly, rather than through Next/ParseValue, because
// an inline image's raw sample data is not PDF object-grammar syntax at
// all - see each method's doc comment in lexer.go.

func TestLexerReadRawBytes(t *testing.T) {
	lex := NewLexer(strings.NewReader("HELLOtail"))
	got, err := lex.ReadRawBytes(5)
	if err != nil {
		t.Fatalf("ReadRawBytes: %v", err)
	}
	if string(got) != "HELLO" {
		t.Fatalf("ReadRawBytes = %q, want %q", got, "HELLO")
	}
	if lex.Pos() != 5 {
		t.Fatalf("Pos() = %d, want 5", lex.Pos())
	}
	// The rest of the stream should still read normally afterward.
	rest, err := lex.ReadRawBytes(4)
	if err != nil || string(rest) != "tail" {
		t.Fatalf("ReadRawBytes(remaining) = %q, %v, want %q, nil", rest, err, "tail")
	}
}

func TestLexerReadRawBytesPastEOFIsMalformed(t *testing.T) {
	lex := NewLexer(strings.NewReader("ab"))
	if _, err := lex.ReadRawBytes(10); err == nil {
		t.Fatal("ReadRawBytes past EOF: want error, got nil")
	}
}

func TestLexerSkipOneWhitespaceByte(t *testing.T) {
	lex := NewLexer(strings.NewReader(" X"))
	if err := lex.SkipOneWhitespaceByte(); err != nil {
		t.Fatalf("SkipOneWhitespaceByte: %v", err)
	}
	got, err := lex.ReadRawBytes(1)
	if err != nil || string(got) != "X" {
		t.Fatalf("after skip, ReadRawBytes(1) = %q, %v, want %q, nil", got, err, "X")
	}

	// A non-whitespace byte is left in place rather than consumed.
	lex2 := NewLexer(strings.NewReader("Y"))
	if err := lex2.SkipOneWhitespaceByte(); err != nil {
		t.Fatalf("SkipOneWhitespaceByte: %v", err)
	}
	got2, err := lex2.ReadRawBytes(1)
	if err != nil || string(got2) != "Y" {
		t.Fatalf("after no-op skip, ReadRawBytes(1) = %q, %v, want %q, nil", got2, err, "Y")
	}
}

func TestLexerScanForInlineImageEnd(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "simple", input: "rawbytes EI", want: "rawbytes"},
		{name: "trailing delimiter after EI", input: "rawbytes EI/Next", want: "rawbytes"},
		{name: "EI-like text inside data is not a false match without leading whitespace", input: "abcEIdef EI", want: "abcEIdef"},
		{name: "empty data", input: " EI", want: ""},
		{name: "unterminated", input: "no terminator here", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lex := NewLexer(strings.NewReader(tt.input))
			got, err := lex.ScanForInlineImageEnd()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ScanForInlineImageEnd(%q): want error, got none (result %q)", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ScanForInlineImageEnd(%q): %v", tt.input, err)
			}
			if string(got) != tt.want {
				t.Fatalf("ScanForInlineImageEnd(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

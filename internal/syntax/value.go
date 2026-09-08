package syntax

import (
	"strconv"
	"strings"

	"github.com/tucats/pdf-viewer/internal/pdferror"
)

// This file implements ParseValue, which sits on top of the Lexer
// (lexer.go) and assembles tokens into the Object values defined in
// object.go: numbers, names, strings, arrays, dictionaries, streams,
// references, booleans, and null.
//
// maxNestingDepth bounds how deeply arrays and dictionaries may nest
// within a single value. Without a limit, a maliciously crafted file
// containing something like "[[[[[[[[...]]]]]]]]" thousands of levels
// deep could exhaust the goroutine's stack through unbounded recursion
// (ParseValue calls itself once per nesting level) before any size or
// time limit elsewhere in the module had a chance to reject the file.
// This is exactly the kind of "bounded work" the repository README's
// "Dependency and safety policy" section requires. 64 levels is far
// deeper than any legitimate PDF content this project's fixture corpus
// or the wild is expected to need - real documents rarely nest more
// than a handful of levels - while still comfortably fitting in a
// single goroutine stack.
const maxNestingDepth = 64

// ParseValue reads and returns the next complete Object from lex,
// following PDF's object grammar. depth tracks the current nesting
// level for the maxNestingDepth guard described above; callers parsing
// a top-level value (not already nested inside an array or dictionary)
// should pass depth 0.
//
// Indirect references ("N G R") require up to two tokens of lookahead
// beyond a plain integer to recognize: ParseValue reads an Integer,
// then peeks at the next one or two tokens to see whether they form
// "<integer> R"; if not, the peeked tokens are pushed back onto lex so
// they are seen again by whatever reads next.
//
// Streams are recognized by seeing the "stream" keyword immediately
// following a dictionary value. This function reads the stream's raw
// bytes (still filter-encoded; see the Stream type's doc comment in
// object.go) directly from lex's underlying reader: if the dictionary's
// /Length entry is a direct Integer, exactly that many bytes are read;
// otherwise (missing, or an indirect reference this package cannot
// resolve without the cross-reference table - see internal/parser) this
// function instead scans forward for the literal "endstream" keyword
// and treats everything before it as the stream's raw content. The
// fallback path is deliberately conservative: a stream containing the
// literal bytes "endstream" within its own encoded data as unlikely
// coincidence, rather than as a real occurrence of that keyword, is not
// something this function attempts to distinguish, since doing so
// correctly requires the resolved /Length in the first place.
func ParseValue(lex *Lexer, depth int) (Object, error) {
	tok, err := lex.Next()
	if err != nil {
		return nil, err
	}
	return parseValueFromToken(lex, tok, depth)
}

// parseValueFromToken does the actual work of ParseValue given a Token
// that has already been read. It is a separate function (rather than
// ParseValue reading the Token itself and then continuing inline)
// because parseArray and parseDictOrStream below need to parse each of
// their elements/values starting from a Token they have already peeked
// at, without re-entering ParseValue and its lex.Next() call.
//
// The maxNestingDepth guard lives here, at the entry point every
// recursive descent into a nested value actually goes through
// (parseArray and parseDictOrStream both call this function directly,
// not ParseValue), rather than only in ParseValue above - putting it
// only in ParseValue would check depth on the outermost call but not on
// any of the recursive ones, which is exactly the case this guard
// exists to catch.
func parseValueFromToken(lex *Lexer, tok Token, depth int) (Object, error) {
	if depth > maxNestingDepth {
		return nil, pdferror.Malformedf("object nesting exceeds limit of %d levels", maxNestingDepth)
	}

	switch tok.Kind {
	case KindEOF:
		return nil, pdferror.Malformedf("unexpected end of input while parsing a value")

	case KindNumber:
		return parseNumberOrReference(lex, tok)

	case KindName:
		return Name(tok.Bytes), nil

	case KindLiteralString, KindHexString:
		return String(tok.Bytes), nil

	case KindArrayStart:
		return parseArray(lex, depth)

	case KindDictStart:
		return parseDictOrStream(lex, depth)

	case KindKeyword:
		switch tok.Text {
		case "true":
			return Boolean(true), nil
		case "false":
			return Boolean(false), nil
		case "null":
			return Null{}, nil
		default:
			return nil, pdferror.Malformedf("unexpected keyword %q where a value was expected", tok.Text)
		}

	case KindArrayEnd, KindDictEnd:
		return nil, pdferror.Malformedf("unexpected closing delimiter where a value was expected")

	default:
		// Unreachable as long as every Kind above is handled; kept
		// as an explicit error rather than a panic in case a future
		// Token kind is added to lexer.go without updating this switch.
		return nil, pdferror.Malformedf("internal error: unhandled Token kind %d", tok.Kind)
	}
}

// parseNumberOrReference has already consumed a KindNumber (first) and
// decides whether it is a plain Integer/Real or the first part of an
// "N G R" indirect reference, using up to two tokens of lookahead.
func parseNumberOrReference(lex *Lexer, first Token) (Object, error) {
	n, isInt, err := parseNumberText(first.Text)
	if err != nil {
		return nil, err
	}

	// A reference's object number must be a non-negative integer; if
	// this Token isn't even an integer, it can't start a reference, so
	// there is no point in looking ahead.
	if !isInt || n < 0 {
		if isInt {
			return Integer(n), nil
		}
		f, err := strconv.ParseFloat(first.Text, 64)
		if err != nil {
			return nil, pdferror.Malformedf("invalid number %q", first.Text)
		}
		return Real(f), nil
	}

	second, err := lex.Next()
	if err != nil {
		return nil, err
	}
	genVal, genIsInt, genErr := int64(0), false, error(nil)
	if second.Kind == KindNumber {
		genVal, genIsInt, genErr = parseNumberText(second.Text)
	}
	if second.Kind != KindNumber || genErr != nil || !genIsInt || genVal < 0 {
		lex.PushBack(second)
		return Integer(n), nil
	}

	third, err := lex.Next()
	if err != nil {
		return nil, err
	}
	if third.Kind == KindKeyword && third.Text == "R" {
		return Reference{Number: int(n), Generation: int(genVal)}, nil
	}
	lex.PushBack(third)
	lex.PushBack(second)
	return Integer(n), nil
}

// parseNumberText interprets the raw text of a KindNumber as either an
// integer (isInt true) or, failing that, leaves integer parsing to the
// caller so it can fall back to float parsing for real numbers such as
// "3.14". It exists mainly to share integer-vs-real detection between
// parseNumberOrReference's "is this plausibly an object number" check
// and its final Integer/Real decision.
func parseNumberText(text string) (n int64, isInt bool, err error) {
	// A number containing '.' is never a valid integer in PDF syntax
	// (object and generation numbers in particular are always plain
	// digit sequences), so route it straight to real-number handling by
	// the caller rather than attempting (and always failing)
	// strconv.ParseInt first.
	if strings.ContainsRune(text, '.') {
		return 0, false, nil
	}
	v, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, false, nil
	}
	return v, true, nil
}

func parseArray(lex *Lexer, depth int) (Object, error) {
	var arr Array
	for {
		tok, err := lex.Next()
		if err != nil {
			return nil, err
		}
		if tok.Kind == KindArrayEnd {
			return arr, nil
		}
		if tok.Kind == KindEOF {
			return nil, pdferror.Malformedf("unterminated array: reached end of input before ']'")
		}
		val, err := parseValueFromToken(lex, tok, depth+1)
		if err != nil {
			return nil, err
		}
		arr = append(arr, val)
	}
}

func parseDictOrStream(lex *Lexer, depth int) (Object, error) {
	dict := Dictionary{}
	for {
		tok, err := lex.Next()
		if err != nil {
			return nil, err
		}
		if tok.Kind == KindDictEnd {
			break
		}
		if tok.Kind == KindEOF {
			return nil, pdferror.Malformedf("unterminated dictionary: reached end of input before '>>'")
		}
		if tok.Kind != KindName {
			return nil, pdferror.Malformedf("expected a name as a dictionary key, found %v", tok)
		}
		key := Name(tok.Bytes)
		val, err := ParseValue(lex, depth+1)
		if err != nil {
			return nil, err
		}
		dict[key] = val
	}

	// A dictionary immediately followed by the "stream" keyword is a
	// stream object rather than a plain dictionary; peek one Token to
	// check.
	next, err := lex.Next()
	if err != nil {
		return nil, err
	}
	if next.Kind != KindKeyword || next.Text != "stream" {
		lex.PushBack(next)
		return dict, nil
	}

	raw, err := readStreamBody(lex, dict)
	if err != nil {
		return nil, err
	}
	return Stream{Dict: dict, Raw: raw}, nil
}

// readStreamBody reads a stream's raw byte content, having already
// consumed the "stream" keyword that introduces it. Per the PDF
// specification, "stream" is followed by exactly a CRLF or a bare LF
// (a bare CR alone is not permitted precisely because it is
// ambiguous - though this reader accepts it anyway for the same
// tolerate-minor-corruption reasons documented on Lexer.Next) and then
// the raw bytes begin.
func readStreamBody(lex *Lexer, dict Dictionary) ([]byte, error) {
	if err := skipStreamKeywordEOL(lex); err != nil {
		return nil, err
	}

	if length, ok := directLength(dict); ok {
		return readExactly(lex, length)
	}
	return readUntilEndstream(lex)
}

// skipStreamKeywordEOL consumes the single end-of-line sequence
// (CRLF or LF) mandated immediately after the "stream" keyword. Because
// the Lexer's own Token reading (used for everything before this point)
// already treats whitespace as insignificant and skips over it, by the
// time readStreamBody runs, the *first* byte of the actual line ending
// may already have been consumed as ordinary skipped whitespace while
// looking for the "stream" keyword's own Token boundary - to stay
// correct regardless of exactly how much of the line ending the lexer's
// internal buffering already consumed, this reads directly from the
// lexer's raw byte source via its exported byte-level helper.
func skipStreamKeywordEOL(lex *Lexer) error {
	return lex.skipOneEOL()
}

// directLength reports the stream length and true if the dictionary's
// /Length entry is present and is a direct (non-reference) Integer.
func directLength(dict Dictionary) (int64, bool) {
	v, ok := dict["Length"]
	if !ok {
		return 0, false
	}
	n, ok := v.(Integer)
	if !ok || n < 0 {
		return 0, false
	}
	return int64(n), true
}

// maxStreamScanLength bounds how far readUntilEndstream will search for
// the "endstream" keyword when a stream's /Length cannot be used
// directly. Without a bound, a malformed file that never contains
// "endstream" at all would force scanning all the way to end of file on
// every such stream, which - combined with a file containing many such
// streams - is exactly the kind of unbounded work the repository
// README's "Dependency and safety policy" section asks parsers to avoid.
// 64 MiB comfortably covers any legitimate embedded resource (image,
// font program, or content stream) this project's phased plan
// anticipates handling, while still capping the cost of a hostile file.
const maxStreamScanLength = 64 << 20

// readExactly reads n raw bytes through lex's position-tracked
// readByte/unreadByte (rather than lex.r directly), so that lex.Pos()
// stays accurate for any code reading a value that contains a stream -
// see the doc comment on Lexer.Pos for why that matters.
func readExactly(lex *Lexer, n int64) ([]byte, error) {
	buf := make([]byte, n)
	for i := range buf {
		b, err := lex.readByte()
		if err != nil {
			return nil, pdferror.Malformedf("reading %d-byte stream: %v", n, err)
		}
		buf[i] = b
	}
	return buf, nil
}

func readUntilEndstream(lex *Lexer) ([]byte, error) {
	const marker = "endstream"
	var out []byte
	for {
		if len(out) > maxStreamScanLength {
			return nil, pdferror.Malformedf("stream exceeds %d bytes without a resolvable /Length or an \"endstream\" keyword", maxStreamScanLength)
		}
		b, err := lex.readByte()
		if err != nil {
			return nil, pdferror.Malformedf("stream has no resolvable /Length and no \"endstream\" keyword was found before end of input")
		}
		out = append(out, b)
		if len(out) >= len(marker) && string(out[len(out)-len(marker):]) == marker {
			trimmed := out[:len(out)-len(marker)]
			// The specification calls for an end-of-line sequence
			// before "endstream" that is not counted as part of the
			// stream's data; trim at most one such sequence.
			trimmed = trimTrailingEOL(trimmed)
			return trimmed, nil
		}
	}
}

func trimTrailingEOL(b []byte) []byte {
	if len(b) >= 2 && b[len(b)-2] == '\r' && b[len(b)-1] == '\n' {
		return b[:len(b)-2]
	}
	if len(b) >= 1 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		return b[:len(b)-1]
	}
	return b
}

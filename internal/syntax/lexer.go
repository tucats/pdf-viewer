package syntax

import (
	"bufio"
	"io"
)

// This file implements the Lexer, the lowest level of this package: it
// turns a raw byte stream into a sequence of tokens (numbers, names,
// strings, delimiters, and bare keywords), following the character
// classification rules from the PDF specification (whitespace,
// delimiters, and regular characters). It knows nothing about what the
// tokens *mean* - that is ParseValue's job, in value.go - only about
// where one Token ends and the next begins.
//
// Splitting tokenizing from value parsing this way mirrors how the PDF
// specification itself describes the grammar (as a lexical layer
// followed by a syntax layer), and it means the trickiest, most
// bit-fiddly code (string escape sequences, name hex-escapes, deciding
// where a bare number or keyword ends) lives in one place and can be
// tested in isolation from array/dictionary/stream structure.

// Kind identifies which of PDF's small set of lexical categories a
// Token belongs to.
type Kind int

const (
	KindEOF Kind = iota
	KindNumber
	KindName
	KindLiteralString
	KindHexString
	KindArrayStart // [
	KindArrayEnd   // ]
	KindDictStart  // <<
	KindDictEnd    // >>
	KindKeyword    // a bare run of regular characters: true, false, null, obj, endobj, stream, endstream, xref, trailer, startxref, R, or any other identifier-like Token
)

// Token is one lexical unit produced by the Lexer. All three fields are
// exported because internal/parser needs to drive the Lexer directly
// (rather than only through ParseValue) to recognize PDF's structural
// keywords - "xref", "trailer", "startxref", "obj", and "endobj" - which
// are not values in PDF's object grammar and so are deliberately not
// handled by ParseValue at all.
type Token struct {
	Kind Kind
	// Text holds the Token's textual form for KindNumber (e.g. "3.14")
	// and KindKeyword (e.g. "obj"). It is unused for other kinds.
	Text string
	// Bytes holds the decoded byte content for KindName, KindLiteralString,
	// and KindHexString. For KindName this is the name with its leading
	// slash removed and any #xx escapes already resolved. It is unused
	// for other kinds.
	Bytes []byte
}

// Lexer reads PDF Token syntax from an io.Reader. It is not safe for
// concurrent use.
type Lexer struct {
	r *bufio.Reader
	// pos counts bytes consumed from r via readByte/unreadByte, giving
	// Pos its answer. It intentionally does not account for tokens
	// currently sitting in pending (see PushBack): Pos always reflects
	// how far the underlying byte stream has actually been read, not
	// how far a caller has logically "gotten to" if it has pushed
	// tokens back.
	pos int64
	// pending holds tokens that have been read and then handed back via
	// PushBack, most-recently-pushed-back first, so Next always drains
	// pending before reading new bytes. This is what gives ParseValue
	// the lookahead it needs to distinguish, for example, a plain
	// Integer from the first two numbers of an "N G R" reference.
	pending []Token
}

// NewLexer returns a Lexer reading from r.
func NewLexer(r io.Reader) *Lexer {
	return &Lexer{r: bufio.NewReader(r)}
}

// PushBack returns tok to the front of the Token stream, so the next
// call to Next returns it again. Multiple pushed-back tokens are
// returned in last-pushed-first-out order, matching how callers
// naturally unwind lookahead (peek Token A, then Token B, decide neither
// applies, push back B then A so A is seen again first).
func (l *Lexer) PushBack(tok Token) {
	l.pending = append(l.pending, tok)
}

// Pos reports how many bytes have been consumed so far from the
// io.Reader this Lexer was constructed with. internal/parser uses this
// while scanning a whole file for "N G obj" markers (its fallback
// recovery path for a corrupted cross-reference table; see
// internal/parser's doc comment) to record where each object actually
// starts. Ordinary tokenizing and value parsing never need this - it
// exists specifically to support that recovery scan.
func (l *Lexer) Pos() int64 {
	return l.pos
}

// readByte and unreadByte wrap the underlying bufio.Reader's ReadByte
// and UnreadByte, keeping pos in sync. Every other method in this file
// reads bytes through these two methods rather than calling l.r
// directly, specifically so that Pos stays accurate no matter which
// lexing path bytes happen to flow through.
func (l *Lexer) readByte() (byte, error) {
	b, err := l.r.ReadByte()
	if err == nil {
		l.pos++
	}
	return b, err
}

func (l *Lexer) unreadByte() error {
	if err := l.r.UnreadByte(); err != nil {
		return err
	}
	l.pos--
	return nil
}

// Next returns the next Token in the stream, or a KindEOF Token when the
// underlying reader is exhausted. It returns an error only for I/O
// failures from the underlying reader (never merely for encountering
// unexpected bytes - unrecognized bytes are skipped, since a lexer that
// stopped dead on the first stray byte would make every higher-level
// parser far more fragile against minor real-world file corruption than
// it needs to be; structural validity is checked at the parsing layer,
// which is in a position to decide whether a given stray byte actually
// matters).
func (l *Lexer) Next() (Token, error) {
	if n := len(l.pending); n > 0 {
		tok := l.pending[n-1]
		l.pending = l.pending[:n-1]
		return tok, nil
	}
	return l.next()
}

// skipOneEOL consumes a single end-of-line sequence - CRLF, a bare LF,
// or (tolerantly, per the general policy documented on Next) a bare CR
// - from the raw byte stream, if one is present; if the next byte is
// not part of an end-of-line sequence at all, it is left unconsumed.
// value.go's readStreamBody uses this to skip the single mandatory
// end-of-line that separates the "stream" keyword from a stream's raw
// data.
func (l *Lexer) skipOneEOL() error {
	b, err := l.readByte()
	if err != nil {
		if err == io.EOF {
			return nil
		}
		return err
	}
	switch b {
	case '\r':
		next, err := l.readByte()
		if err == nil && next != '\n' {
			return l.unreadByte()
		}
		return nil
	case '\n':
		return nil
	default:
		return l.unreadByte()
	}
}

func (l *Lexer) next() (Token, error) {
	if err := l.skipWhitespaceAndComments(); err != nil {
		if err == io.EOF {
			return Token{Kind: KindEOF}, nil
		}
		return Token{}, err
	}

	b, err := l.readByte()
	if err == io.EOF {
		return Token{Kind: KindEOF}, nil
	}
	if err != nil {
		return Token{}, err
	}

	switch {
	case b == '/':
		return l.readName()
	case b == '(':
		return l.readLiteralString()
	case b == '<':
		return l.readAngleBracketToken()
	case b == '>':
		return l.readDictEnd()
	case b == '[':
		return Token{Kind: KindArrayStart}, nil
	case b == ']':
		return Token{Kind: KindArrayEnd}, nil
	case b == '{', b == '}':
		// PostScript-calculator-function braces. This module does not
		// yet interpret PostScript functions (planned for whenever
		// Separation/DeviceN or type 4 shading functions are
		// implemented, per docs/capability-matrix.md); treating each
		// brace as its own single-character keyword Token lets the
		// lexer keep working on files that contain them elsewhere
		// without needing to understand their contents.
		return Token{Kind: KindKeyword, Text: string(b)}, nil
	case isNumberStart(b):
		return l.readNumber(b)
	case isRegularChar(b):
		return l.readKeyword(b)
	default:
		// An unexpected delimiter such as a stray ')' with no matching
		// '(' before it. Skip it and continue - see the doc comment on
		// Next for why the lexer does not treat this as fatal.
		return l.next()
	}
}

// skipWhitespaceAndComments consumes PDF whitespace characters and "%"
// comments (which run to the next end-of-line), leaving the reader
// positioned at the first byte of the next Token. It returns io.EOF if
// the stream ends while skipping.
func (l *Lexer) skipWhitespaceAndComments() error {
	for {
		b, err := l.readByte()
		if err != nil {
			return err
		}
		switch {
		case isWhitespace(b):
			continue
		case b == '%':
			if err := l.skipToEndOfLine(); err != nil {
				return err
			}
		default:
			return l.unreadByte()
		}
	}
}

func (l *Lexer) skipToEndOfLine() error {
	for {
		b, err := l.readByte()
		if err != nil {
			return err
		}
		if b == '\n' || b == '\r' {
			return nil
		}
	}
}

// readName reads a name Token, having already consumed the leading '/'.
// Names may contain "#xx" hex escapes (two hex digits after '#'), used
// to represent characters that would otherwise be whitespace or
// delimiters within a name; a malformed escape (missing or non-hex
// digits) is left as-is (the literal '#' and following bytes are kept)
// rather than treated as an error, consistent with this lexer's general
// policy of tolerating minor corruption rather than aborting - see the
// doc comment on Next.
func (l *Lexer) readName() (Token, error) {
	var out []byte
	for {
		b, err := l.readByte()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Token{}, err
		}
		if !isRegularChar(b) {
			if err := l.unreadByte(); err != nil {
				return Token{}, err
			}
			break
		}
		if b == '#' {
			if hi, lo, ok := l.peekTwoHexDigits(); ok {
				out = append(out, hi<<4|lo)
				continue
			}
		}
		out = append(out, b)
	}
	return Token{Kind: KindName, Bytes: out}, nil
}

// peekTwoHexDigits consumes exactly two bytes if both are valid
// hexadecimal digits, returning their combined 4-bit values and true;
// otherwise it puts back whatever it consumed and returns false, false,
// false so the caller can treat the '#' as a literal character instead.
func (l *Lexer) peekTwoHexDigits() (hi, lo byte, ok bool) {
	b1, err := l.readByte()
	if err != nil {
		return 0, 0, false
	}
	v1, ok1 := hexVal(b1)
	if !ok1 {
		_ = l.unreadByte()
		return 0, 0, false
	}
	b2, err := l.readByte()
	if err != nil {
		// Only one byte was consumed and it was a valid hex digit;
		// there is no second byte to unread, and bufio.Reader only
		// supports a single-byte UnreadByte, so the '#' + one hex
		// digit is simply dropped here. This only happens at the very
		// end of a truncated file, which is already malformed for
		// other reasons.
		return 0, 0, false
	}
	v2, ok2 := hexVal(b2)
	if !ok2 {
		// Two bytes were consumed but only the first was a valid hex
		// digit; bufio.Reader can only unread one byte, so the second
		// (non-hex) byte is lost. As above, this is a rare edge case
		// against already-malformed input and the fallback (treat '#'
		// literally) is a reasonable degradation, not a correctness
		// issue for well-formed files.
		return 0, 0, false
	}
	return v1, v2, true
}

func hexVal(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}

// readLiteralString reads a "(...)"-delimited string, having already
// consumed the leading '('. It implements the escape sequences and
// balanced-parenthesis nesting rules from the PDF specification:
// unescaped parentheses nest (so "(a(b)c)" is the single string
// "a(b)c"), a backslash followed by an end-of-line is a line
// continuation (both are dropped from the decoded output), backslash
// followed by one of a small set of letters denotes a control character,
// and backslash followed by one to three octal digits denotes that
// character's byte value.
func (l *Lexer) readLiteralString() (Token, error) {
	var out []byte
	depth := 1
	for {
		b, err := l.readByte()
		if err != nil {
			// Reached EOF (or another I/O error) before the closing
			// ')'. Return what was decoded so far rather than losing it
			// - the caller (ParseValue) is in a better position to
			// decide whether an unterminated string should be a hard
			// parse error for the document as a whole.
			if err == io.EOF {
				return Token{Kind: KindLiteralString, Bytes: out}, nil
			}
			return Token{}, err
		}
		switch b {
		case '(':
			depth++
			out = append(out, b)
		case ')':
			depth--
			if depth == 0 {
				return Token{Kind: KindLiteralString, Bytes: out}, nil
			}
			out = append(out, b)
		case '\\':
			decoded, ok, err := l.readStringEscape()
			if err != nil {
				return Token{}, err
			}
			if ok {
				out = append(out, decoded)
			}
			// !ok means the escape was a line continuation
			// (backslash+EOL): both bytes are simply dropped from the
			// decoded output, per the PDF specification.
		default:
			out = append(out, b)
		}
	}
}

// readStringEscape reads one escape sequence within a literal string,
// having already consumed the leading backslash. It returns the decoded
// byte and ok=true for a normal escape, or ok=false for a line
// continuation (backslash immediately followed by CR, LF, or CRLF, which
// contributes no byte to the decoded string at all).
func (l *Lexer) readStringEscape() (decoded byte, ok bool, err error) {
	b, err := l.readByte()
	if err != nil {
		if err == io.EOF {
			// A trailing backslash with nothing after it, right at
			// EOF. Treat it as contributing nothing, matching the
			// line-continuation case, since there is no correct byte
			// value to invent for it.
			return 0, false, nil
		}
		return 0, false, err
	}
	switch b {
	case 'n':
		return '\n', true, nil
	case 'r':
		return '\r', true, nil
	case 't':
		return '\t', true, nil
	case 'b':
		return '\b', true, nil
	case 'f':
		return '\f', true, nil
	case '(', ')', '\\':
		return b, true, nil
	case '\r':
		// CRLF line continuation: also consume a following '\n' if
		// present.
		if next, err := l.readByte(); err == nil && next != '\n' {
			_ = l.unreadByte()
		}
		return 0, false, nil
	case '\n':
		return 0, false, nil
	default:
		if b >= '0' && b <= '7' {
			return l.readOctalEscape(b)
		}
		// PDF specifies that a backslash before any other character is
		// simply that character, with the backslash itself ignored -
		// e.g. "\%" means "%". This also gracefully handles any escape
		// letter this lexer does not explicitly recognize above.
		return b, true, nil
	}
}

// readOctalEscape reads a "\ddd" octal escape (one to three octal
// digits), having already consumed the first digit as first. Per the
// PDF specification, if the resulting value would not fit in a byte,
// the high-order overflow bits are simply dropped rather than treated as
// an error.
func (l *Lexer) readOctalEscape(first byte) (byte, bool, error) {
	val := first - '0'
	for i := 0; i < 2; i++ {
		b, err := l.readByte()
		if err != nil {
			if err == io.EOF {
				break
			}
			return 0, false, err
		}
		if b < '0' || b > '7' {
			_ = l.unreadByte()
			break
		}
		val = val*8 + (b - '0')
	}
	return val, true, nil
}

// readAngleBracketToken reads a Token starting with '<', having already
// consumed that first '<'. This is either the start of a dictionary
// ("<<") or a hexadecimal string ("<48656C6C6F>") - PDF distinguishes
// the two solely by whether a second '<' immediately follows.
func (l *Lexer) readAngleBracketToken() (Token, error) {
	b, err := l.readByte()
	if err == io.EOF {
		// A lone '<' at EOF is malformed either way; report it as an
		// (empty) hex string, which ParseValue will reject in a context
		// where a value was expected, or which will simply be a
		// harmless empty result if encountered while skipping.
		return Token{Kind: KindHexString}, nil
	}
	if err != nil {
		return Token{}, err
	}
	if b == '<' {
		return Token{Kind: KindDictStart}, nil
	}
	if err := l.unreadByte(); err != nil {
		return Token{}, err
	}
	return l.readHexString()
}

// readHexString reads a "<...>"-delimited hex string. Whitespace between
// hex digits is ignored (this is explicitly permitted by the PDF
// specification, and real files use it to keep long hex strings
// readable across multiple lines). If the string contains an odd number
// of hex digits, the final digit is treated as if followed by an
// implicit 0, per the specification. Any byte that is not a hex digit or
// whitespace is skipped rather than treated as an error, consistent with
// this lexer's general tolerance policy (see the doc comment on Next).
func (l *Lexer) readHexString() (Token, error) {
	var digits []byte
	for {
		b, err := l.readByte()
		if err != nil {
			if err == io.EOF {
				break
			}
			return Token{}, err
		}
		if b == '>' {
			break
		}
		if v, ok := hexVal(b); ok {
			digits = append(digits, v)
		}
		// Non-hex, non-'>' bytes (including whitespace) are silently
		// skipped.
	}
	if len(digits)%2 == 1 {
		digits = append(digits, 0)
	}
	out := make([]byte, len(digits)/2)
	for i := range out {
		out[i] = digits[2*i]<<4 | digits[2*i+1]
	}
	return Token{Kind: KindHexString, Bytes: out}, nil
}

// readDictEnd reads a Token starting with '>', having already consumed
// that first '>'. A lone '>' is not valid PDF syntax on its own (dict
// end is always "»", i.e. two characters), but this lexer tolerates it
// as a dict-end Token anyway: a parser expecting "<<...>>" that only
// sees one '>' before running out of matching context will fail with a
// clear structural error at the parsing layer regardless, and treating
// stray '>' bytes as significant (rather than silently discarding them,
// which could shift the rest of the Token stream in confusing ways) is
// the more predictable behavior.
func (l *Lexer) readDictEnd() (Token, error) {
	b, err := l.readByte()
	if err == io.EOF {
		return Token{Kind: KindDictEnd}, nil
	}
	if err != nil {
		return Token{}, err
	}
	if b == '>' {
		return Token{Kind: KindDictEnd}, nil
	}
	if err := l.unreadByte(); err != nil {
		return Token{}, err
	}
	return Token{Kind: KindDictEnd}, nil
}

// readNumber reads a numeric Token, having already consumed its first
// byte as first. PDF numbers are a run of digits, an optional single
// leading sign, and an optional single decimal point - this reads the
// widest run of characters that could plausibly be part of such a
// number and lets the caller (ParseValue, via strconv) decide whether
// the result parses as a valid integer or real; a malformed number like
// "1.2.3" is passed through as text and reported as a parse error at
// that point; caller code should never assume readNumber's output is
// guaranteed valid.
func (l *Lexer) readNumber(first byte) (Token, error) {
	buf := []byte{first}
	for {
		b, err := l.readByte()
		if err != nil {
			if err == io.EOF {
				break
			}
			return Token{}, err
		}
		if b == '+' || b == '-' || b == '.' || (b >= '0' && b <= '9') {
			buf = append(buf, b)
			continue
		}
		if err := l.unreadByte(); err != nil {
			return Token{}, err
		}
		break
	}
	return Token{Kind: KindNumber, Text: string(buf)}, nil
}

// readKeyword reads a bare run of regular characters, having already
// consumed its first byte as first: this covers PDF's small set of
// reserved keywords (true, false, null, obj, endobj, stream, endstream,
// xref, trailer, startxref, R) as well as any other identifier-like text
// that might appear where this lexer is used more leniently (for
// example, this same lexer is reused to scan for structural keywords
// while recovering from a corrupted cross-reference table; see
// internal/parser).
func (l *Lexer) readKeyword(first byte) (Token, error) {
	buf := []byte{first}
	for {
		b, err := l.readByte()
		if err != nil {
			if err == io.EOF {
				break
			}
			return Token{}, err
		}
		if !isRegularChar(b) {
			if err := l.unreadByte(); err != nil {
				return Token{}, err
			}
			break
		}
		buf = append(buf, b)
	}
	return Token{Kind: KindKeyword, Text: string(buf)}, nil
}

// --- PDF character classification -----------------------------------
//
// These helpers implement the three-way character classification PDF's
// grammar is built on: whitespace, delimiters, and "regular" characters
// (everything else, which makes up names and keywords). See ISO
// 32000-1/2, "Lexical Conventions".

func isWhitespace(b byte) bool {
	switch b {
	case 0x00, 0x09, 0x0A, 0x0C, 0x0D, 0x20:
		return true
	default:
		return false
	}
}

func isDelimiter(b byte) bool {
	switch b {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	default:
		return false
	}
}

func isRegularChar(b byte) bool {
	return !isWhitespace(b) && !isDelimiter(b)
}

func isNumberStart(b byte) bool {
	return b == '+' || b == '-' || b == '.' || (b >= '0' && b <= '9')
}

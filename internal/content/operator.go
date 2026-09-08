package content

import (
	"bytes"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements Parse, which tokenizes a page's decoded content
// stream bytes into a sequence of Operators. A content stream's grammar
// reuses PDF's ordinary object syntax for operands (numbers, names,
// strings, arrays, and dictionaries - see internal/syntax) but has no
// "N G obj"/indirect-reference wrapping at all, and introduces
// operators: bare keywords (like "m", "re", "f", "Tj") that consume
// whatever operands precede them on the "operand stack" and are not
// values in their own right - internal/syntax's ParseValue does not (and
// should not) know about this, so this package drives internal/syntax's
// lower-level Lexer directly instead, exactly as internal/parser does
// for the structural keywords "xref"/"trailer"/"obj"/"endobj".

// Operator is one content stream operator together with the operands
// that preceded it - e.g. "10 10 80 80 re" becomes
// Operator{Name: "re", Operands: []syntax.Object{Integer(10), ...}}.
//
// InlineImage is non-nil only for an operator named "BI": an inline
// image has no ordinary numeric/name operands at all (see Parse's doc
// comment), so its data is carried in this separate field instead of
// Operands.
type Operator struct {
	Name        string
	Operands    []syntax.Object
	InlineImage *InlineImage
}

// InlineImage is the parsed form of a "BI...ID...EI" inline image: Dict
// is its image dictionary (with every abbreviated key - see
// inlineKeyAliases - normalized to the corresponding full name an
// ordinary XObject image dictionary would use, e.g. "BPC" becomes
// "BitsPerComponent"; this normalization is exactly what lets
// internal/content's "Do" and "BI" handling in interpret.go, and
// internal/image's Decode beneath it, share a single code path instead
// of two nearly-identical ones), and Raw is the inline image's raw
// sample data exactly as it appeared in the content stream, still
// encoded by whatever filter (if any) Dict's /Filter entry names - the
// same "not yet filter-decoded" contract syntax.Stream.Raw documents for
// an ordinary stream object.
type InlineImage struct {
	Dict syntax.Dictionary
	Raw  []byte
}

// maxOperandsPerOperator bounds how many operands Parse will accumulate
// before the next operator keyword, so a maliciously crafted content
// stream consisting of millions of bare numbers with no operator at all
// cannot force unbounded memory growth before Parse ever gets a chance
// to reject it; see the repository README's "Dependency and safety
// policy". No real operator (the widest is a color-space operator like
// "scn" with a handful of components, or the text-showing array
// operator "TJ") takes anywhere near this many operands.
const maxOperandsPerOperator = 64

// Parse tokenizes data (a page's already fully filter-decoded content
// stream bytes - see internal/parser.Document.DecodeStream) into a
// sequence of Operators, in the order they appear.
//
// Two operators receive special handling because they are not simple
// "operands then keyword" forms:
//
//   - "BI" (begin inline image) introduces inline image data with its
//     own dictionary-like key/value syntax followed by raw, still
//     filter-encoded image bytes terminated by "EI" - none of which is
//     valid content stream *operator* syntax, so it cannot be parsed the
//     way every other operator is. parseInlineImage (below) handles this
//     directly against the Lexer, producing an InlineImage carried on the
//     resulting Operator instead of ordinary Operands; see Interpret for
//     how it is actually decoded and painted (Phase 3).
//   - "true"/"false"/"null" are valid operand values (via
//     syntax.ParseValue), not operators, even though a bare content
//     stream keyword otherwise means "this is an operator": Parse relies
//     on syntax.ParseValue to recognize them as values before ever
//     treating a keyword as an operator name.
func Parse(data []byte) ([]Operator, error) {
	lex := syntax.NewLexer(bytes.NewReader(data))

	var ops []Operator
	var operands []syntax.Object

	for {
		tok, err := lex.Next()
		if err != nil {
			return nil, err
		}
		if tok.Kind == syntax.KindEOF {
			return ops, nil
		}

		if tok.Kind == syntax.KindKeyword {
			switch tok.Text {
			case "true":
				operands = append(operands, syntax.Boolean(true))
				continue
			case "false":
				operands = append(operands, syntax.Boolean(false))
				continue
			case "null":
				operands = append(operands, syntax.Null{})
				continue
			case "BI":
				img, err := parseInlineImage(lex)
				if err != nil {
					return nil, err
				}
				ops = append(ops, Operator{Name: "BI", InlineImage: img})
				operands = nil
				continue
			default:
				ops = append(ops, Operator{Name: tok.Text, Operands: operands})
				operands = nil
				continue
			}
		}

		// Not a keyword at all (a number, name, string, array, or
		// dictionary): push the already-read token back so
		// syntax.ParseValue - which always starts by reading its own
		// first token - sees it again, rather than duplicating
		// ParseValue's per-Kind dispatch logic in this package.
		lex.PushBack(tok)
		val, err := syntax.ParseValue(lex, 0)
		if err != nil {
			return nil, err
		}
		if len(operands) >= maxOperandsPerOperator {
			return nil, pdferror.Malformedf("content stream operator has more than %d operands", maxOperandsPerOperator)
		}
		operands = append(operands, val)
	}
}

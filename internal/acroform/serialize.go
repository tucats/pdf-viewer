package acroform

import (
	"fmt"
	"strings"

	"github.com/tucats/pdf-viewer/internal/content"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements the small amount of content-stream *writing*
// this package needs: re-emitting a field's /DA operators other than
// "Tf" (see nonFontOperatorsSource's doc comment for why "Tf" itself is
// excluded) and escaping arbitrary bytes into a valid PDF literal
// string. Everything else this package's generated appearances contain
// (numbers, operator keywords) is simple enough to build with fmt.
// Sprintf directly in textfield.go/checkbox.go.

// nonFontOperatorsSource re-parses da (already known to parse, since
// ParseDA succeeded, but re-parsed here rather than threading the
// operator list through as well, since only this one caller needs it)
// and returns the source text of every operator *except* "Tf" - almost
// always just a single color operator ("g"/"rg"/"k").
//
// "Tf" is excluded because appearance.go always re-derives font size
// itself (needed to resolve /DA's "auto-size" 0 into a real size - see
// autoFontSize - even when the producer's own size was already
// nonzero) and therefore always emits its own "Tf" operator rather than
// trusting /DA's; splicing both would set the font twice, harmlessly
// but pointlessly, so the original is dropped instead. An operator this
// function does not know how to re-serialize (see writeOperator) is
// simply skipped, the same "one bad field, not a bad appearance"
// tolerance this project applies elsewhere - a generated appearance
// missing an unusual /DA operator still shows correctly colored,
// correctly sized text in the overwhelmingly common case (/DA
// containing only "Tf" and one color operator).
func nonFontOperatorsSource(da []byte) []byte {
	ops, err := content.Parse(da)
	if err != nil {
		return nil
	}
	var b strings.Builder
	for _, op := range ops {
		if op.Name == "Tf" {
			continue
		}
		if s, ok := writeOperator(op); ok {
			b.WriteString(s)
			b.WriteByte('\n')
		}
	}
	return []byte(b.String())
}

// writeOperator serializes op back into content-stream source text
// ("operands, then keyword"), reporting ok=false for an operand type
// /DA legitimately never contains (an Array or Dictionary operand -
// no operator valid in a /DA string takes one).
func writeOperator(op content.Operator) (string, bool) {
	var b strings.Builder
	for _, operand := range op.Operands {
		s, ok := writeOperand(operand)
		if !ok {
			return "", false
		}
		b.WriteString(s)
		b.WriteByte(' ')
	}
	b.WriteString(op.Name)
	return b.String(), true
}

// writeOperand serializes a single operand value.
func writeOperand(obj syntax.Object) (string, bool) {
	switch v := obj.(type) {
	case syntax.Integer:
		return fmt.Sprintf("%d", int64(v)), true
	case syntax.Real:
		return formatNumber(float64(v)), true
	case syntax.Name:
		return "/" + string(v), true
	default:
		return "", false
	}
}

// formatNumber formats v the way this package's own generated content
// streams need numbers written: a plain decimal with no exponent form
// (which content-stream number syntax does not accept) and no more
// precision than a rendered PDF page can ever visibly distinguish.
func formatNumber(v float64) string {
	s := fmt.Sprintf("%.4f", v)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

// escapeLiteral returns b as the contents of a valid PDF literal string
// (the bytes that belong between "(" and ")"), escaping the three bytes
// that are syntactically significant inside one (backslash, and both
// parentheses) and any byte outside printable ASCII as a three-digit
// octal escape - safe, simple, and unambiguous for internal/syntax's
// lexer to read back exactly, regardless of what a font's own byte
// encoding (see encodeForFont) happens to produce for a given piece of
// text (which is not necessarily printable ASCII at all - only "means
// something to this one font's /Encoding").
func escapeLiteral(b []byte) []byte {
	out := make([]byte, 0, len(b)+8)
	for _, c := range b {
		switch c {
		case '(', ')', '\\':
			out = append(out, '\\', c)
		default:
			if c < 0x20 || c >= 0x7F {
				out = append(out, '\\', '0'+(c>>6)&7, '0'+(c>>3)&7, '0'+c&7)
			} else {
				out = append(out, c)
			}
		}
	}
	return out
}

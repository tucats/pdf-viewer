package content

import (
	"errors"
	"reflect"
	"testing"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

func TestParseSimpleOperators(t *testing.T) {
	ops, err := Parse([]byte("1 0 0 rg\n10 10 80 80 re\nf\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Operator{
		{Name: "rg", Operands: []syntax.Object{syntax.Integer(1), syntax.Integer(0), syntax.Integer(0)}},
		{Name: "re", Operands: []syntax.Object{syntax.Integer(10), syntax.Integer(10), syntax.Integer(80), syntax.Integer(80)}},
		{Name: "f", Operands: nil},
	}
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("Parse = %#v, want %#v", ops, want)
	}
}

func TestParseEmptyContentStream(t *testing.T) {
	ops, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("Parse(nil) = %#v, want empty", ops)
	}
}

func TestParseBooleanAndNullOperands(t *testing.T) {
	ops, err := Parse([]byte("true false null vset"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Operator{{
		Name:     "vset",
		Operands: []syntax.Object{syntax.Boolean(true), syntax.Boolean(false), syntax.Null{}},
	}}
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("Parse = %#v, want %#v", ops, want)
	}
}

func TestParseArrayAndDictOperands(t *testing.T) {
	ops, err := Parse([]byte("[1 2 3] << /A 1 >> op"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(ops) != 1 || ops[0].Name != "op" || len(ops[0].Operands) != 2 {
		t.Fatalf("Parse = %#v, want one \"op\" operator with 2 operands", ops)
	}
	if _, ok := ops[0].Operands[0].(syntax.Array); !ok {
		t.Errorf("first operand = %#v, want syntax.Array", ops[0].Operands[0])
	}
	if _, ok := ops[0].Operands[1].(syntax.Dictionary); !ok {
		t.Errorf("second operand = %#v, want syntax.Dictionary", ops[0].Operands[1])
	}
}

// TestParseInlineImage confirms Parse produces a "BI" Operator carrying
// a parsed InlineImage rather than treating "BI" as an ordinary
// operator - see inlineimage_test.go for more thorough coverage of
// parseInlineImage's own length-determination logic (explicit /L,
// computed from dimensions, and the EI-scanning fallback).
func TestParseInlineImage(t *testing.T) {
	ops, err := Parse([]byte("q BI /W 1 /H 1 /BPC 8 /CS /G ID \x2a EI Q"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(ops) != 3 || ops[0].Name != "q" || ops[1].Name != "BI" || ops[2].Name != "Q" {
		t.Fatalf("Parse = %#v, want [q, BI, Q]", ops)
	}
	img := ops[1].InlineImage
	if img == nil {
		t.Fatal("BI operator's InlineImage is nil")
	}
	if img.Dict["Width"] != syntax.Integer(1) || img.Dict["Height"] != syntax.Integer(1) {
		t.Errorf("InlineImage.Dict = %#v, want normalized /Width and /Height", img.Dict)
	}
	if img.Dict["BitsPerComponent"] != syntax.Integer(8) {
		t.Errorf("InlineImage.Dict[\"BitsPerComponent\"] (from /BPC) = %#v, want Integer(8)", img.Dict["BitsPerComponent"])
	}
	if img.Dict["ColorSpace"] != syntax.Name("G") {
		t.Errorf("InlineImage.Dict[\"ColorSpace\"] (from /CS) = %#v, want Name(\"G\")", img.Dict["ColorSpace"])
	}
	if len(img.Raw) != 1 || img.Raw[0] != 0x2a {
		t.Errorf("InlineImage.Raw = %v, want [0x2a]", img.Raw)
	}
}

func TestParseTooManyOperandsIsMalformed(t *testing.T) {
	data := make([]byte, 0, 4*(maxOperandsPerOperator+5))
	for i := 0; i < maxOperandsPerOperator+5; i++ {
		data = append(data, []byte("1 ")...)
	}
	data = append(data, 'x')
	if _, err := Parse(data); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Parse with too many operands: error = %v, want ErrMalformed", err)
	}
}

func TestParseMultipleOperatorsResetOperands(t *testing.T) {
	ops, err := Parse([]byte("1 w\n2 w\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("len(ops) = %d, want 2", len(ops))
	}
	if len(ops[0].Operands) != 1 || len(ops[1].Operands) != 1 {
		t.Fatalf("operands leaked across operators: %#v", ops)
	}
}

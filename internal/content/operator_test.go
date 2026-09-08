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

func TestParseInlineImageIsUnsupported(t *testing.T) {
	_, err := Parse([]byte("q BI /W 1 /H 1 ID \x00 EI Q"))
	if !errors.Is(err, pdferror.ErrUnsupported) {
		t.Fatalf("Parse with BI: error = %v, want ErrUnsupported", err)
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

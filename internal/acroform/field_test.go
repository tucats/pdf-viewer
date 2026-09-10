package acroform

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

// fakeResolver is a minimal in-memory Resolver for tests: objects maps
// an object number to the value Resolve should return for it - the same
// small test double every other internal package in this module defines
// its own copy of (see, for example, internal/annotation/resolver_test.go).
type fakeResolver struct {
	objects map[int]syntax.Object
}

func (f *fakeResolver) Resolve(num int) (syntax.Object, error) {
	if obj, ok := f.objects[num]; ok {
		return obj, nil
	}
	return syntax.Null{}, nil
}

func (f *fakeResolver) ResolveDictionary(dict syntax.Dictionary) (syntax.Dictionary, error) {
	out := make(syntax.Dictionary, len(dict))
	for k, v := range dict {
		rv, err := resolveIfRef(f, v)
		if err != nil {
			return nil, err
		}
		out[k] = rv
	}
	return out, nil
}

func (f *fakeResolver) DecodeStream(s syntax.Stream) ([]byte, error) {
	return s.Raw, nil
}

// TestResolveOwnAttributesNeedNoParent confirms a merged field/widget
// (the common case: one field, one widget, no separate /Parent object at
// all) resolves entirely from its own dictionary.
func TestResolveOwnAttributesNeedNoParent(t *testing.T) {
	widget := syntax.Dictionary{
		"FT": syntax.Name("Tx"),
		"Ff": syntax.Integer(0),
		"V":  syntax.String("hello"),
		"DA": syntax.String("/Helv 12 Tf 0 g"),
		"Q":  syntax.Integer(1),
	}
	f := Resolve(&fakeResolver{}, widget)
	if f.FT != "Tx" || f.Q != 1 || string(f.DA) != "/Helv 12 Tf 0 g" {
		t.Fatalf("Resolve = %+v", f)
	}
	if s, ok := f.V.(syntax.String); !ok || string(s) != "hello" {
		t.Fatalf("V = %#v, want String(\"hello\")", f.V)
	}
}

// TestResolveInheritsFromParent exercises the split field/widget shape:
// a widget with only /Rect-shaped, presentation-specific entries (here,
// none at all beyond /Parent) inherits every field attribute from its
// /Parent, exactly as 12.7.3.2 requires.
func TestResolveInheritsFromParent(t *testing.T) {
	parent := syntax.Dictionary{
		"FT": syntax.Name("Tx"),
		"V":  syntax.String("inherited"),
		"DA": syntax.String("/Helv 10 Tf 0 g"),
	}
	widget := syntax.Dictionary{"Parent": syntax.Reference{Number: 1}}
	r := &fakeResolver{objects: map[int]syntax.Object{1: parent}}

	f := Resolve(r, widget)
	if f.FT != "Tx" {
		t.Fatalf("FT = %q, want Tx", f.FT)
	}
	if s, ok := f.V.(syntax.String); !ok || string(s) != "inherited" {
		t.Fatalf("V = %#v, want String(\"inherited\")", f.V)
	}
	if string(f.DA) != "/Helv 10 Tf 0 g" {
		t.Fatalf("DA = %q", f.DA)
	}
}

// TestResolveOwnValueOverridesParent confirms a widget's own entry wins
// over an inherited one when both are present - the field tree's
// "closest wins" rule, not "first found from the root".
func TestResolveOwnValueOverridesParent(t *testing.T) {
	parent := syntax.Dictionary{"V": syntax.String("parent value")}
	widget := syntax.Dictionary{
		"Parent": syntax.Reference{Number: 1},
		"V":      syntax.String("own value"),
	}
	r := &fakeResolver{objects: map[int]syntax.Object{1: parent}}

	f := Resolve(r, widget)
	if s, ok := f.V.(syntax.String); !ok || string(s) != "own value" {
		t.Fatalf("V = %#v, want String(\"own value\")", f.V)
	}
}

// TestResolveCyclicParentDoesNotHang confirms a /Parent cycle (a
// malformed but not impossible-to-encounter file) terminates via
// maxFieldTreeDepth rather than looping forever.
func TestResolveCyclicParentDoesNotHang(t *testing.T) {
	a := syntax.Dictionary{"Parent": syntax.Reference{Number: 2}}
	b := syntax.Dictionary{"Parent": syntax.Reference{Number: 1}}
	r := &fakeResolver{objects: map[int]syntax.Object{1: a, 2: b}}

	// The assertion is simply that this call returns at all - an
	// infinite loop here would fail the test via Go's own test-binary
	// timeout rather than any explicit check.
	_ = Resolve(r, a)
}

// TestResolveWithAcroFormFillsDefaultDA confirms a field with no /DA
// anywhere in its own ancestry falls back to the AcroForm dictionary's
// own /DA, per 12.7.3.3's documented default-of-last-resort, and that DR
// is always taken from the AcroForm dictionary regardless of whether the
// field tree itself supplied a /DA.
func TestResolveWithAcroFormFillsDefaultDA(t *testing.T) {
	widget := syntax.Dictionary{"FT": syntax.Name("Tx")}
	acroForm := syntax.Dictionary{
		"DA": syntax.String("/Helv 0 Tf 0 g"),
		"DR": syntax.Dictionary{"Font": syntax.Dictionary{"Helv": syntax.Integer(0)}},
	}
	f := ResolveWithAcroForm(&fakeResolver{}, widget, acroForm)
	if string(f.DA) != "/Helv 0 Tf 0 g" {
		t.Fatalf("DA = %q, want AcroForm default", f.DA)
	}
	if f.DR == nil {
		t.Fatalf("DR = nil, want AcroForm's /DR")
	}
}

// TestResolveWithAcroFormFieldDATakesPrecedence confirms a field-tree /DA
// is preferred over the AcroForm-level default when both are present.
func TestResolveWithAcroFormFieldDATakesPrecedence(t *testing.T) {
	widget := syntax.Dictionary{"FT": syntax.Name("Tx"), "DA": syntax.String("/Helv 20 Tf 0 g")}
	acroForm := syntax.Dictionary{"DA": syntax.String("/Helv 0 Tf 0 g")}
	f := ResolveWithAcroForm(&fakeResolver{}, widget, acroForm)
	if string(f.DA) != "/Helv 20 Tf 0 g" {
		t.Fatalf("DA = %q, want the field's own", f.DA)
	}
}

// TestResolveWithAcroFormNilAcroFormIsHarmless confirms passing a nil
// AcroForm dictionary (a document with no /AcroForm at all) degrades to
// plain Resolve rather than panicking.
func TestResolveWithAcroFormNilAcroFormIsHarmless(t *testing.T) {
	widget := syntax.Dictionary{"FT": syntax.Name("Tx"), "DA": syntax.String("/Helv 12 Tf 0 g")}
	f := ResolveWithAcroForm(&fakeResolver{}, widget, nil)
	if string(f.DA) != "/Helv 12 Tf 0 g" {
		t.Fatalf("DA = %q", f.DA)
	}
}

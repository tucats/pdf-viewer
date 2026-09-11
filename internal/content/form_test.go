package content

import (
	"errors"
	"testing"
	"time"

	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// formXObjectResources builds a /Resources dictionary with a single
// /Form XObject "Fm0" whose content is formContent and whose dictionary
// is extended with extra (e.g. /Matrix, /BBox, /Resources) - the shared
// setup every test in this file starts from.
func formXObjectResources(formContent string, extra syntax.Dictionary) syntax.Dictionary {
	dict := syntax.Dictionary{"Subtype": syntax.Name("Form")}
	for k, v := range extra {
		dict[k] = v
	}
	stream := syntax.Stream{Dict: dict, Raw: []byte(formContent)}
	return syntax.Dictionary{"XObject": syntax.Dictionary{"Fm0": stream}}
}

func TestDoFormPaintsNestedContent(t *testing.T) {
	resources := formXObjectResources("1 0 0 rg\n0 0 10 10 re\nf\n", nil)
	ops, err := Parse([]byte("/Fm0 Do"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	if list[0].Color != (graphics.Color{R: 1}) {
		t.Fatalf("Color = %+v, want red", list[0].Color)
	}
	minX, minY, maxX, maxY, ok := list[0].Path.Bounds()
	if !ok || minX != 0 || minY != 0 || maxX != 10 || maxY != 10 {
		t.Fatalf("bounds = (%v,%v,%v,%v ok=%v), want (0,0,10,10 true)", minX, minY, maxX, maxY, ok)
	}
}

// TestDoFormAppliesMatrixAndCurrentCTM confirms a form's /Matrix is
// concatenated onto the CTM active where "Do" runs (not
// Interpret's initialCTM - unlike a pattern's own /Matrix, see
// shading_test.go's TestScnShadingPatternUsesInitialCTMNotCurrent for
// the deliberate contrast): translating by (1000,0) via "cm" before
// "Do", on top of the form's own (50,50) /Matrix translation, should
// move its content to device (1050,50).
func TestDoFormAppliesMatrixAndCurrentCTM(t *testing.T) {
	resources := formXObjectResources("0 0 10 10 re\nf\n", syntax.Dictionary{
		"Matrix": syntax.Array{syntax.Real(1), syntax.Real(0), syntax.Real(0), syntax.Real(1), syntax.Real(50), syntax.Real(50)},
	})
	ops, err := Parse([]byte("q 1 0 0 1 1000 0 cm /Fm0 Do Q"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	minX, minY, maxX, maxY, ok := list[0].Path.Bounds()
	if !ok || minX != 1050 || minY != 50 || maxX != 1060 || maxY != 60 {
		t.Fatalf("bounds = (%v,%v,%v,%v ok=%v), want (1050,50,1060,60 true)", minX, minY, maxX, maxY, ok)
	}
}

// TestDoFormBBoxAddsClip confirms a form's /BBox becomes an additional
// clip on every DrawOp its content produces, on top of whatever clip
// was already active in the caller.
func TestDoFormBBoxAddsClip(t *testing.T) {
	resources := formXObjectResources("0 0 100 100 re\nf\n", syntax.Dictionary{
		"BBox": syntax.Array{syntax.Real(0), syntax.Real(0), syntax.Real(5), syntax.Real(5)},
	})
	ops, err := Parse([]byte("/Fm0 Do"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	if len(list[0].Clips) != 1 {
		t.Fatalf("len(Clips) = %d, want 1 (the /BBox clip)", len(list[0].Clips))
	}
	minX, minY, maxX, maxY, ok := list[0].Clips[0].Path.Bounds()
	if !ok || minX != 0 || minY != 0 || maxX != 5 || maxY != 5 {
		t.Fatalf("BBox clip bounds = (%v,%v,%v,%v ok=%v), want (0,0,5,5 true)", minX, minY, maxX, maxY, ok)
	}
}

// TestDoFormMergesOuterClipWithBBox confirms a clip already active where
// "Do" is invoked survives alongside the form's own /BBox clip - a
// caller's clip must still apply to a form's content, not be discarded.
func TestDoFormMergesOuterClipWithBBox(t *testing.T) {
	resources := formXObjectResources("0 0 100 100 re\nf\n", syntax.Dictionary{
		"BBox": syntax.Array{syntax.Real(0), syntax.Real(0), syntax.Real(5), syntax.Real(5)},
	})
	ops, err := Parse([]byte("0 0 20 20 re W n /Fm0 Do"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if len(list[0].Clips) != 2 {
		t.Fatalf("len(Clips) = %d, want 2 (the outer \"W\" clip plus the form's own /BBox)", len(list[0].Clips))
	}
}

// TestDoFormResourcesFallBackToParent confirms a form with no /Resources
// entry of its own can still resolve a named XObject from the *calling*
// content stream's /Resources - per the specification's inheritance
// rule.
func TestDoFormResourcesFallBackToParent(t *testing.T) {
	imgDict := syntax.Dictionary{
		"Subtype": syntax.Name("Image"), "Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8), "ColorSpace": syntax.Name("DeviceGray"),
	}
	formDict := syntax.Dictionary{"Subtype": syntax.Name("Form")}
	formStream := syntax.Stream{Dict: formDict, Raw: []byte("q 1 0 0 1 0 0 cm /Im0 Do Q\n")}
	resources := syntax.Dictionary{
		"XObject": syntax.Dictionary{
			"Fm0": formStream,
			"Im0": syntax.Stream{Dict: imgDict, Raw: []byte{200}},
		},
	}
	ops, err := Parse([]byte("/Fm0 Do"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if len(list) != 1 || list[0].Image == nil {
		t.Fatalf("Interpret = %#v, want a single image DrawOp resolved via the parent's /Resources", list)
	}
}

// TestDoFormOwnResourcesOverrideParent confirms a form's own /Resources,
// when present, is what its content resolves names against - not the
// caller's - by giving the form a /Resources /XObject entry the parent
// does not have at all.
func TestDoFormOwnResourcesOverrideParent(t *testing.T) {
	imgDict := syntax.Dictionary{
		"Subtype": syntax.Name("Image"), "Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8), "ColorSpace": syntax.Name("DeviceGray"),
	}
	formOwnResources := syntax.Dictionary{"XObject": syntax.Dictionary{"Im0": syntax.Stream{Dict: imgDict, Raw: []byte{50}}}}
	resources := formXObjectResources("q 1 0 0 1 0 0 cm /Im0 Do Q\n", syntax.Dictionary{"Resources": formOwnResources})

	ops, err := Parse([]byte("/Fm0 Do"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if len(list) != 1 || list[0].Image == nil {
		t.Fatalf("Interpret = %#v, want a single image DrawOp resolved via the form's own /Resources", list)
	}
}

// TestDoFormIsolatedGroupUsesOuterAlphaNotInnerReset is the regression
// test for the real-world bug that motivated doIsolatedGroupForm: a
// producer-generated PDF (a DJI product's quick start guide) set
// ExtGState ca=0 before "Do"-ing a decorative /Group /S /Transparency
// form, expecting it invisible - but the form's own content stream
// immediately set its *own*, differently-numbered local "GS0" resource
// (ca=1) before painting a solid fill, since 11.4.7.2 guarantees that
// reset stays local to the group's own internal painting and can never
// leak out to affect how the finished group is composited into the
// backdrop. Before doIsolatedGroupForm existed, doForm always flattened
// a form's DrawOps directly, so each flattened fill only remembered the
// *nested* interpreter's own FillAlpha (1, after the inner reset) -
// never the outer ca=0 - and the "invisible" decoration rendered fully
// opaque.
func TestDoFormIsolatedGroupUsesOuterAlphaNotInnerReset(t *testing.T) {
	groupDict := syntax.Dictionary{
		"Subtype": syntax.Name("Form"),
		"BBox":    syntax.Array{syntax.Real(0), syntax.Real(0), syntax.Real(10), syntax.Real(10)},
		"Group":   syntax.Dictionary{"Type": syntax.Name("Group"), "S": syntax.Name("Transparency")},
		// The group's own /Resources /ExtGState /GS0 is a *different*
		// object than the outer content stream's - same resource name,
		// unrelated dictionary, exactly the object-number collision the
		// real DJI file has between a page's own /GS0 and a nested
		// form's /GS0.
		"Resources": syntax.Dictionary{
			"ExtGState": syntax.Dictionary{"GS0": syntax.Dictionary{"ca": syntax.Real(1.0)}},
		},
	}
	groupStream := syntax.Stream{Dict: groupDict, Raw: []byte("/GS0 gs\n1 0 0 rg\n0 0 10 10 re\nf\n")}
	resources := syntax.Dictionary{
		"ExtGState": syntax.Dictionary{"GS0": syntax.Dictionary{"ca": syntax.Real(0.0)}},
		"XObject":   syntax.Dictionary{"Fm0": groupStream},
	}

	ops, err := Parse([]byte("/GS0 gs\n/Fm0 Do"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	if list[0].Image == nil {
		t.Fatalf("DrawOp has no Image - want the isolated-group buffer path, not a flattened fill")
	}
	// The outer ca=0 (not the group's own internal ca=1 reset) must
	// govern the group's own compositing back into the page.
	if list[0].Alpha != 0 {
		t.Fatalf("Alpha = %v, want 0 (the outer ca at the point \"Do\" ran)", list[0].Alpha)
	}
	// The group's own internal content still painted fully opaque red
	// into its own offscreen buffer - the reset only changes how that
	// finished buffer is composited afterward, not what is inside it.
	r, g, b, a := list[0].Image.At(list[0].Image.Width/2, list[0].Image.Height/2)
	if r != 1 || g != 0 || b != 0 || a != 1 {
		t.Fatalf("group buffer center = (%v,%v,%v,%v), want opaque red (1,0,0,1)", r, g, b, a)
	}
}

// TestDoFormCyclicSelfReferenceIsBoundedNotInfinite confirms a form
// whose own content invokes itself again (directly - the simplest cycle
// shape) is rejected past maxFormDepth rather than recursing forever.
// Run with a timeout, matching internal/image's identically-motivated
// mask-recursion regression test, so a future regression that breaks
// this bound fails the test suite (hangs, then times out) rather than
// hanging an entire CI run indefinitely.
func TestDoFormCyclicSelfReferenceIsBoundedNotInfinite(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		formDict := syntax.Dictionary{"Subtype": syntax.Name("Form")}
		stream := syntax.Stream{Dict: formDict, Raw: []byte("/Fm0 Do\n")}
		resources := syntax.Dictionary{"XObject": syntax.Dictionary{"Fm0": stream}}

		ops, err := Parse([]byte("/Fm0 Do"))
		if err != nil {
			done <- err
			return
		}
		_, err = Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, pdferror.ErrMalformed) {
			t.Fatalf("Interpret with a self-referential Form: got %v, want an error wrapping ErrMalformed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Interpret with a self-referential Form did not terminate within 5s - maxFormDepth guard appears broken")
	}
}

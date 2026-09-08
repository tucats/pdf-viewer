package content

import (
	"errors"
	"testing"

	"github.com/tucats/pdf-viewer/internal/filter"
	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// fakeResolver is a minimal, in-memory pdfimage.Resolver for these
// tests: object numbers live in a plain map rather than requiring a real
// parsed PDF file. It mirrors internal/image's own test-only fakeResolver
// (each package defines its own copy rather than sharing one - there is
// no dependency between the two packages' test files to share it
// through, and it is a handful of lines).
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
	dict, err := f.ResolveDictionary(s.Dict)
	if err != nil {
		return nil, err
	}
	return filter.Decode(dict, s.Raw)
}

// TestImageSpaceToDeviceFlipsRowOrder confirms imageSpaceToDevice's
// vertical flip: for a page's ordinary CTM (device y increasing
// downward, matching pageDeviceGeometry in the root package's page.go),
// image space's row 0 (v=0, an image's topmost stored row - see
// graphics.Image's doc comment) must land at the *top* of the painted
// region in device space (small device y), and row height-1 (v close to
// 1) at the bottom (large device y) - i.e. an image's stored top row
// visually ends up on top, not upside down. This is a regression test
// for exactly the bug this function exists to fix: using a content
// stream's CTM directly as DrawOp.ImageToDevice (without this flip)
// renders every image vertically mirrored.
func TestImageSpaceToDeviceFlipsRowOrder(t *testing.T) {
	// The CTM in effect when a "Do" operator runs always already maps
	// the unit square [0,1]x[0,1] onto the region an image should cover
	// (typically via a preceding "cm", e.g. "100 0 0 100 0 0 cm" to fill
	// a 100x100 page) - never a page's raw, unscaled user-space CTM by
	// itself. This is exactly that combination for an unrotated 100x100
	// page: baseCTM ({A:1,D:-1,F:100}, see pageDeviceGeometry in the
	// root package's page.go) composed with a "100 0 0 100 0 0 cm".
	ctm := graphics.Matrix{A: 100, D: -100, F: 100}
	m := imageSpaceToDevice(ctm)

	// Image space (0,0) - the top-left sample - should land near device
	// y=0 (the top of the rendered image), not near y=100.
	_, topY := m.Apply(0, 0)
	if topY > 1 {
		t.Errorf("imageSpaceToDevice: image row 0 mapped to device y=%v, want ~0 (the top of the image)", topY)
	}
	// Image space (0,1) - the bottom-left sample - should land near
	// device y=100 (the bottom).
	_, bottomY := m.Apply(0, 1)
	if bottomY < 99 {
		t.Errorf("imageSpaceToDevice: image row bottom mapped to device y=%v, want ~100 (the bottom of the image)", bottomY)
	}
}

// TestInterpretDoPaintsReferencedImage confirms "Do" looks up an image
// XObject through /Resources /XObject, decodes it via internal/image,
// and appends an image DrawOp covering the CTM-mapped unit square.
func TestInterpretDoPaintsReferencedImage(t *testing.T) {
	imgDict := syntax.Dictionary{
		"Subtype": syntax.Name("Image"),
		"Width":   syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("DeviceRGB"),
	}
	imgStream := syntax.Stream{Dict: imgDict, Raw: []byte{10, 20, 30}}

	resources := syntax.Dictionary{
		"XObject": syntax.Dictionary{"Im0": syntax.Reference{Number: 5}},
	}
	resolver := &fakeResolver{objects: map[int]syntax.Object{5: imgStream}}

	ops, err := Parse([]byte("q 100 0 0 100 0 0 cm /Im0 Do Q"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, resolver)
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	op := list[0]
	if op.Image == nil {
		t.Fatal("DrawOp.Image is nil, want a decoded image")
	}
	if op.Image.Width != 1 || op.Image.Height != 1 {
		t.Errorf("decoded image dimensions = %dx%d, want 1x1", op.Image.Width, op.Image.Height)
	}
	if r, g, b, a := op.Image.At(0, 0); r != 10.0/255 || g != 20.0/255 || b != 30.0/255 || a != 1 {
		t.Errorf("decoded pixel = (%v,%v,%v,%v), want (%v,%v,%v,1)", r, g, b, a, 10.0/255, 20.0/255, 30.0/255)
	}
	// The unit square [0,1]x[0,1] mapped by "100 0 0 100 0 0 cm" covers
	// device rectangle [0,100]x[0,100].
	minX, minY, maxX, maxY, ok := op.Path.Bounds()
	if !ok || minX != 0 || minY != 0 || maxX != 100 || maxY != 100 {
		t.Errorf("image quad bounds = (%v,%v,%v,%v), want (0,0,100,100)", minX, minY, maxX, maxY)
	}
}

// TestInterpretDoWithEmptyFormXObjectAddsNothing confirms a "Do" naming
// a /Form XObject with no content bytes of its own contributes no
// DrawOps (as opposed to erroring, or somehow producing an image
// DrawOp) - see form_test.go for real (non-empty) Form XObject coverage,
// now that Form XObjects are implemented (form.go's doForm).
func TestInterpretDoWithEmptyFormXObjectAddsNothing(t *testing.T) {
	formStream := syntax.Stream{Dict: syntax.Dictionary{"Subtype": syntax.Name("Form")}, Raw: nil}
	resources := syntax.Dictionary{"XObject": syntax.Dictionary{"Fm0": formStream}}

	ops, err := Parse([]byte("/Fm0 Do\n0 0 1 1 re f"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if len(list) != 1 || list[0].Image != nil {
		t.Fatalf("Interpret with a Form XObject = %#v, want only the following fill (no image DrawOp)", list)
	}
}

// TestInterpretDoWithMissingXObjectIsSkipped confirms a "Do" naming a
// resource that is not present at all is tolerated exactly like an
// unresolvable named color space resource elsewhere in this package -
// not every malformed-resource situation is treated as fatal.
func TestInterpretDoWithMissingXObjectIsSkipped(t *testing.T) {
	ops, err := Parse([]byte("/NotThere Do"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), nil, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret with a missing XObject resource: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("len(list) = %d, want 0", len(list))
	}
}

func TestInterpretDoWithUnsupportedImageFeaturePropagatesError(t *testing.T) {
	// /Pattern is not a legal image /ColorSpace at all (patterns are only
	// meaningful for "scn"/"SCN" fill/stroke colors) - still an
	// unsupported-color-space case now that /Lab itself (this test's
	// former example - see docs/capability-matrix.md) is implemented.
	imgDict := syntax.Dictionary{
		"Subtype": syntax.Name("Image"),
		"Width":   syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Array{syntax.Name("Pattern")},
	}
	resources := syntax.Dictionary{"XObject": syntax.Dictionary{"Im0": syntax.Stream{Dict: imgDict, Raw: []byte{0, 0, 0}}}}

	ops, err := Parse([]byte("/Im0 Do"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if !errors.Is(err, pdferror.ErrUnsupported) {
		t.Fatalf("Interpret with an unsupported image color space: got %v, want an error wrapping ErrUnsupported", err)
	}
}

// TestInterpretInlineImagePaintsImage confirms "BI" decodes and paints
// exactly like "Do" does for a referenced image, using an /ImageMask
// inline image (the common real-world use of inline images: small
// stencil masks and glyph-like shapes) so this also exercises
// FillColor being threaded through to internal/image.Decode from the
// current graphics state.
func TestInterpretInlineImagePaintsImage(t *testing.T) {
	ops, err := Parse([]byte("q 1 0 0 rg 10 0 0 10 0 0 cm BI /W 1 /H 1 /IM true ID \x00 EI Q"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), nil, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if len(list) != 1 || list[0].Image == nil {
		t.Fatalf("Interpret(inline image) = %#v, want a single image DrawOp", list)
	}
	r, g, b, a := list[0].Image.At(0, 0)
	if r != 1 || g != 0 || b != 0 || a != 1 {
		t.Errorf("inline /ImageMask pixel = (%v,%v,%v,%v), want (1,0,0,1) (red fill color, painted)", r, g, b, a)
	}
}

// TestInterpretDoAndInlineImageWithNilResolverDoesNotPanic confirms that
// a nil resolver (which FuzzParseAndInterpret deliberately passes - see
// that fuzz target) makes "Do" and "BI" no-ops rather than reaching a
// nil-interface method call. In particular, an inline image dictionary
// value that is itself a syntax.Reference - forbidden by the
// specification but not rejected by this package's Parse - would
// otherwise reach resolveIfRef(nilResolver, ref) and panic trying to
// call Resolve on a nil interface.
func TestInterpretDoAndInlineImageWithNilResolverDoesNotPanic(t *testing.T) {
	resources := syntax.Dictionary{"XObject": syntax.Dictionary{"Im0": syntax.Integer(1)}}
	ops, err := Parse([]byte("/Im0 Do\nBI /W 1 /H 1 /CS 5 0 R ID \x00 EI"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := Interpret(ops, graphics.Identity(), resources, nil); err != nil {
		t.Fatalf("Interpret with a nil resolver: %v (want no error, since both operators should be silently skipped)", err)
	}
}

func TestInterpretInlineImageMalformedIsError(t *testing.T) {
	// /W 0 is not a valid image width.
	ops, err := Parse([]byte("BI /W 0 /H 1 /BPC 8 /CS /G ID  EI"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = Interpret(ops, graphics.Identity(), nil, &fakeResolver{})
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Interpret with an invalid inline image: got %v, want an error wrapping ErrMalformed", err)
	}
}

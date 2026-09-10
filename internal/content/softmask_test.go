package content

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// softMaskGroupStream builds a transparency-group Form XObject stream
// (the /G a soft-mask dictionary points at) with the given /BBox and
// content - the shared setup every test in this file starts from,
// mirroring form_test.go's formXObjectResources for an ordinary Form
// XObject.
func softMaskGroupStream(bbox [4]float64, content string) syntax.Stream {
	dict := syntax.Dictionary{
		"Subtype": syntax.Name("Form"),
		"BBox": syntax.Array{
			syntax.Real(bbox[0]), syntax.Real(bbox[1]), syntax.Real(bbox[2]), syntax.Real(bbox[3]),
		},
	}
	return syntax.Stream{Dict: dict, Raw: []byte(content)}
}

// smaskExtGState builds a /Resources dictionary with a single ExtGState
// "GS0" whose /SMask is smaskDict.
func smaskExtGState(smaskDict syntax.Object) syntax.Dictionary {
	return syntax.Dictionary{
		"ExtGState": syntax.Dictionary{"GS0": syntax.Dictionary{"SMask": smaskDict}},
	}
}

// allBytesEqual reports whether every entry of values equals want -
// used to check a rendered mask is uniform, e.g. entirely white or
// entirely black, without needing to hand-check every pixel individually.
func allBytesEqual(t *testing.T, values []byte, want byte) {
	t.Helper()
	for i, v := range values {
		if v != want {
			t.Fatalf("Values[%d] = %d, want %d (mask not uniform)", i, v, want)
		}
	}
}

// TestGsBuildsLuminosityMaskFromWhiteGroup confirms a /Luminosity soft
// mask whose group paints solid white across its whole /BBox produces a
// fully-unmasked (255) mask of the expected pixel dimensions - the
// simplest possible non-degenerate case, exercising the whole build
// pipeline: resolving /S and /G, rendering the group offscreen, and
// reducing white (luminosity 1) to a mask value of 255.
func TestGsBuildsLuminosityMaskFromWhiteGroup(t *testing.T) {
	group := softMaskGroupStream([4]float64{0, 0, 10, 10}, "1 1 1 rg\n0 0 10 10 re\nf\n")
	smask := syntax.Dictionary{"S": syntax.Name("Luminosity"), "G": group}
	resources := smaskExtGState(smask)

	ops, err := Parse([]byte("/GS0 gs\n0 0 10 10 re\nf\n"))
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
	mask := list[0].SoftMask
	if mask == nil {
		t.Fatal("SoftMask = nil, want a built mask")
	}
	if mask.Width != 10 || mask.Height != 10 {
		t.Fatalf("mask dimensions = %dx%d, want 10x10 (matching the group's /BBox mapped through an identity CTM)", mask.Width, mask.Height)
	}
	allBytesEqual(t, mask.Values, 255)
}

// TestGsBuildsAlphaMaskFromGroupCoverage confirms a /Alpha soft mask
// reads the group's own rendered alpha channel directly, ignoring color
// entirely: a group that paints nothing produces an all-0 (fully masked
// out) mask, and one that fully covers its /BBox with an opaque fill -
// any color, since /Alpha does not look at color - produces an all-255
// mask, regardless of how dark that fill color is (unlike /Luminosity,
// where a black fill would produce an all-0 mask for the opposite
// reason - this is exactly the distinction the two /S values exist to
// make).
func TestGsBuildsAlphaMaskFromGroupCoverage(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    byte
	}{
		{"empty group", "", 0},
		{"fully covered with black", "0 0 10 10 re\nf\n", 255},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group := softMaskGroupStream([4]float64{0, 0, 10, 10}, tt.content)
			smask := syntax.Dictionary{"S": syntax.Name("Alpha"), "G": group}
			resources := smaskExtGState(smask)

			ops, err := Parse([]byte("/GS0 gs\n0 0 10 10 re\nf\n"))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
			if err != nil {
				t.Fatalf("Interpret: %v", err)
			}
			mask := list[0].SoftMask
			if mask == nil {
				t.Fatal("SoftMask = nil, want a built mask")
			}
			allBytesEqual(t, mask.Values, tt.want)
		})
	}
}

// TestGsSoftMaskNoneClearsAnActiveMask confirms /SMask /None (the
// specification's own way to say "stop masking") clears a
// previously-set soft mask, not just leaves a fresh one unset.
func TestGsSoftMaskNoneClearsAnActiveMask(t *testing.T) {
	group := softMaskGroupStream([4]float64{0, 0, 10, 10}, "1 1 1 rg\n0 0 10 10 re\nf\n")
	resources := syntax.Dictionary{
		"ExtGState": syntax.Dictionary{
			"GS0": syntax.Dictionary{"SMask": syntax.Dictionary{"S": syntax.Name("Luminosity"), "G": group}},
			"GS1": syntax.Dictionary{"SMask": syntax.Name("None")},
		},
	}
	ops, err := Parse([]byte("/GS0 gs /GS1 gs\n0 0 10 10 re\nf\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if list[0].SoftMask != nil {
		t.Fatalf("SoftMask after /SMask /None = %+v, want nil", list[0].SoftMask)
	}
}

// TestGsSoftMaskSavedAndRestoredByQQ confirms SoftMask is part of the
// graphics state proper - saved by "q" and restored by "Q" - exactly
// like FillAlpha or BlendMode already are (extgstate_test.go's
// TestGsAlphaAndBlendModeAreSavedAndRestoredByQQ).
func TestGsSoftMaskSavedAndRestoredByQQ(t *testing.T) {
	group := softMaskGroupStream([4]float64{0, 0, 10, 10}, "1 1 1 rg\n0 0 10 10 re\nf\n")
	smask := syntax.Dictionary{"S": syntax.Name("Luminosity"), "G": group}
	resources := smaskExtGState(smask)

	ops, err := Parse([]byte("q /GS0 gs Q\n0 0 10 10 re\nf\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if list[0].SoftMask != nil {
		t.Fatalf("SoftMask after Q = %+v, want nil (restored to the pre-q default)", list[0].SoftMask)
	}
}

// TestGsSoftMaskMalformedDictionaryIsTolerated confirms a handful of
// structurally-present-but-unusable /SMask dictionaries are each
// tolerated (no error, and any previously active mask is left exactly
// as it was) rather than aborting the render - this package's general
// "missing/malformed resource" policy, applied here the same way
// extgstate_test.go's TestGsMissingResourceIsTolerated already checks
// it for /ca.
func TestGsSoftMaskMalformedDictionaryIsTolerated(t *testing.T) {
	validGroup := softMaskGroupStream([4]float64{0, 0, 10, 10}, "1 1 1 rg\n0 0 10 10 re\nf\n")

	tests := []struct {
		name  string
		smask syntax.Dictionary
	}{
		{"missing /S", syntax.Dictionary{"G": validGroup}},
		{"unrecognized /S", syntax.Dictionary{"S": syntax.Name("Bogus"), "G": validGroup}},
		{"missing /G", syntax.Dictionary{"S": syntax.Name("Luminosity")}},
		{"/G not a stream", syntax.Dictionary{"S": syntax.Name("Luminosity"), "G": syntax.Integer(5)}},
		{"group with no /BBox", syntax.Dictionary{
			"S": syntax.Name("Luminosity"),
			"G": syntax.Stream{Dict: syntax.Dictionary{"Subtype": syntax.Name("Form")}, Raw: nil},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources := syntax.Dictionary{
				"ExtGState": syntax.Dictionary{
					// GS0 sets a real mask first, so this test can also
					// confirm a later malformed /SMask leaves it alone
					// rather than clearing it.
					"GS0": syntax.Dictionary{"SMask": syntax.Dictionary{"S": syntax.Name("Luminosity"), "G": validGroup}},
					"GS1": syntax.Dictionary{"SMask": tt.smask},
				},
			}
			ops, err := Parse([]byte("/GS0 gs /GS1 gs\n0 0 10 10 re\nf\n"))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
			if err != nil {
				t.Fatalf("Interpret with a malformed /SMask (%s): want no error, got %v", tt.name, err)
			}
			if list[0].SoftMask == nil {
				t.Fatalf("SoftMask = nil after a malformed /SMask (%s); want GS0's earlier mask left in place", tt.name)
			}
		})
	}
}

// TestGsSoftMaskNameOtherThanNoneIsTolerated confirms a bare Name value
// other than /None (illegal per the specification, which only permits
// /None or a dictionary) is tolerated exactly like any other malformed
// /SMask value, rather than being misread as some other meaningful
// operand.
func TestGsSoftMaskNameOtherThanNoneIsTolerated(t *testing.T) {
	resources := smaskExtGState(syntax.Name("Bogus"))
	ops, err := Parse([]byte("/GS0 gs\n0 0 10 10 re\nf\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if list[0].SoftMask != nil {
		t.Fatalf("SoftMask = %+v, want nil (no mask was ever active, and this malformed value should not create one)", list[0].SoftMask)
	}
}

// TestBuildSoftMaskNestingDepthGuard confirms buildSoftMask itself
// refuses to build a mask once formDepth already reached maxFormDepth -
// the same guard doForm and buildTilingPattern already have against
// unbounded recursion, exercised directly here (rather than via a
// genuinely self-referential fixture, which would need maxFormDepth
// levels of nested "Do"/"gs" to actually trigger) since this test file
// is part of the content package and can construct an interpreter
// in-progress-at-depth directly.
func TestBuildSoftMaskNestingDepthGuard(t *testing.T) {
	group := softMaskGroupStream([4]float64{0, 0, 10, 10}, "1 1 1 rg\n0 0 10 10 re\nf\n")
	smask := syntax.Dictionary{"S": syntax.Name("Luminosity"), "G": group}

	in := &interpreter{resolver: &fakeResolver{}, formDepth: maxFormDepth}
	if _, err := in.buildSoftMask(smask, graphics.Identity()); err == nil {
		t.Fatal("buildSoftMask at maxFormDepth: want an error, got nil")
	}
}

// TestLuminosityValueCompositesOverBlackBackdrop confirms
// luminosityValue treats an under-covered (alpha < 1) source pixel as
// partially blended with the (default, black) backdrop before computing
// brightness - not as if the source's own painted color were the whole
// story regardless of coverage. A 50%-alpha white pixel over the default
// black backdrop should read at roughly half brightness, not full white.
func TestLuminosityValueCompositesOverBlackBackdrop(t *testing.T) {
	full := luminosityValue(1, 1, 1, 1, graphics.Color{})
	if full < 0.999 || full > 1 {
		// 0.3+0.59+0.11 does not land on exactly 1.0 in floating point -
		// a documented near-miss, not a bug, so this allows for it rather
		// than asserting bit-exact equality.
		t.Errorf("fully opaque white over black = %v, want ~1", full)
	}
	half := luminosityValue(1, 1, 1, 0.5, graphics.Color{})
	if half < 0.45 || half > 0.55 {
		t.Errorf("50%% alpha white over black = %v, want ~0.5", half)
	}
	transparent := luminosityValue(1, 1, 1, 0, graphics.Color{})
	if transparent != 0 {
		t.Errorf("fully transparent (nothing painted) over black = %v, want 0", transparent)
	}
}

// TestBackdropColorFromComponents confirms /BC's component-count-based
// interpretation (1 -> DeviceGray, 3 -> DeviceRGB, anything else ->
// black, matching this project's colorFromComponents fallback used
// elsewhere).
func TestBackdropColorFromComponents(t *testing.T) {
	if got := backdropColorFromComponents([]float64{0.5}); got != (graphics.Color{R: 0.5, G: 0.5, B: 0.5}) {
		t.Errorf("1 component = %+v, want gray 0.5", got)
	}
	if got := backdropColorFromComponents([]float64{1, 0, 0}); got != (graphics.Color{R: 1}) {
		t.Errorf("3 components = %+v, want red", got)
	}
	if got := backdropColorFromComponents(nil); got != (graphics.Color{}) {
		t.Errorf("0 components = %+v, want black (the default)", got)
	}
	if got := backdropColorFromComponents([]float64{1, 2, 3, 4}); got != (graphics.Color{}) {
		t.Errorf("4 components = %+v, want black (unrecognized count falls back to the default)", got)
	}
}

package model

import "testing"

// TestDocument_FontSubstitutionSourceDefaultsToNil confirms a Document
// that never has SetFontSource called on it - the case for every
// Document opened without the root package's WithFontSubstitution option
// - reports no font source at all, matching internal/fonts'
// substitutionSourceFor's expectation that a nil return means
// "substitution not configured" (see that function's own doc comment).
func TestDocument_FontSubstitutionSourceDefaultsToNil(t *testing.T) {
	d := openFixture(t, "minimal-blank-page.pdf")
	if got := d.FontSubstitutionSource(); got != nil {
		t.Errorf("FontSubstitutionSource() = %v, want nil for a Document with SetFontSource never called", got)
	}
}

// TestDocument_SetFontSourceRoundTrips confirms SetFontSource/
// FontSubstitutionSource is a plain round-trip through the `any`-typed
// field - see SetFontSource's own doc comment for why this package
// deliberately does not import internal/fonts just to type this more
// specifically.
func TestDocument_SetFontSourceRoundTrips(t *testing.T) {
	d := openFixture(t, "minimal-blank-page.pdf")

	// A trivial stand-in for a real internal/fonts.FontSource - this
	// package has no reason to know or care what shape the value
	// actually is, only that it comes back unchanged.
	type fakeSource struct{ name string }
	want := &fakeSource{name: "test-source"}

	d.SetFontSource(want)
	got, ok := d.FontSubstitutionSource().(*fakeSource)
	if !ok || got != want {
		t.Errorf("FontSubstitutionSource() = %#v, want the exact value passed to SetFontSource (%#v)", d.FontSubstitutionSource(), want)
	}
}

// TestDocument_SetFontSourceNilDetaches confirms SetFontSource(nil)
// restores the "no font source" default, mirroring SetDiagnostics(nil)'s
// existing documented behavior.
func TestDocument_SetFontSourceNilDetaches(t *testing.T) {
	d := openFixture(t, "minimal-blank-page.pdf")
	d.SetFontSource("anything")
	d.SetFontSource(nil)
	if got := d.FontSubstitutionSource(); got != nil {
		t.Errorf("FontSubstitutionSource() = %v after SetFontSource(nil), want nil", got)
	}
}

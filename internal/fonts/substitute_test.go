package fonts

import "testing"

// face is a small helper for building a synthetic FontFace in tests
// without needing a real font file - see this file's tests below and
// docs/FONTS.md's "Testing" section, which specifically calls for the
// matching algorithm to be testable against "small in-memory fake
// FontSource implementations" rather than real installed fonts.
func face(family string, bold, italic, serif, fixedPitch bool, hasOutlines bool) FontFace {
	return FontFace{
		Characteristics: FontCharacteristics{
			Family:     family,
			Bold:       bold,
			Italic:     italic,
			Serif:      serif,
			FixedPitch: fixedPitch,
		},
		HasOutlines: hasOutlines,
	}
}

func chars(family string, bold, italic, serif, fixedPitch bool) FontCharacteristics {
	return FontCharacteristics{Family: family, Bold: bold, Italic: italic, Serif: serif, FixedPitch: fixedPitch}
}

func TestMatchFace_ExactFamilyMatch(t *testing.T) {
	candidates := []FontFace{
		face("Verdana", false, false, false, false, true),
		face("Arial", false, false, false, false, true),
		face("Arial", true, false, false, false, true),
	}
	query := chars("Arial", true, false, false, false)

	got, ok := matchFace(query, candidates)
	if !ok {
		t.Fatal("expected a match")
	}
	if got.Characteristics.Family != "Arial" || !got.Characteristics.Bold {
		t.Fatalf("expected bold Arial, got %+v", got.Characteristics)
	}
}

func TestMatchFace_FamilyMatchIsCaseInsensitive(t *testing.T) {
	candidates := []FontFace{face("ARIAL", false, false, false, false, true)}
	query := chars("arial", false, false, false, false)

	got, ok := matchFace(query, candidates)
	if !ok || got.Characteristics.Family != "ARIAL" {
		t.Fatalf("expected a case-insensitive family match, got %+v ok=%v", got, ok)
	}
}

func TestMatchFace_FamilyMatchPrefersClosestStyle(t *testing.T) {
	// None of these three exactly matches "bold, not italic", but the
	// second (bold, italic - one trait right) should still beat the
	// first (neither trait right) and tie with the third (also one
	// trait right) - tie broken by order, so the second should win.
	candidates := []FontFace{
		face("Georgia", false, true, false, false, true),
		face("Georgia", true, true, false, false, true),
		face("Georgia", false, false, false, false, true),
	}
	query := chars("Georgia", true, false, false, false)

	got, ok := matchFace(query, candidates)
	if !ok {
		t.Fatal("expected a match")
	}
	if !got.Characteristics.Bold || !got.Characteristics.Italic {
		t.Fatalf("expected the bold+italic candidate (one matching trait, first in order) to win, got %+v", got.Characteristics)
	}
}

func TestMatchFace_NoFamilyMatchFallsBackToStandard14Category(t *testing.T) {
	// The PDF asks for "Times New Roman" (a standard-14 name), and no
	// candidate is named that - but "Liberation Serif" is present and
	// (per its own OS/2-derived characteristics) a serif face, so it
	// should be chosen via the standard-14 category fallback.
	candidates := []FontFace{
		face("Liberation Sans", false, false, false, false, true),
		face("Liberation Serif", false, false, true, false, true),
		face("Liberation Mono", false, false, false, true, true),
	}
	query := chars("Times New Roman", false, false, false, false)

	got, ok := matchFace(query, candidates)
	if !ok || got.Characteristics.Family != "Liberation Serif" {
		t.Fatalf("expected Liberation Serif via category fallback, got %+v ok=%v", got, ok)
	}
}

func TestMatchFace_NoFamilyMatchStandard14MonospaceCategory(t *testing.T) {
	candidates := []FontFace{
		face("Liberation Sans", false, false, false, false, true),
		face("Liberation Mono", false, false, false, true, true),
	}
	query := chars("Courier", false, false, false, false)

	got, ok := matchFace(query, candidates)
	if !ok || got.Characteristics.Family != "Liberation Mono" {
		t.Fatalf("expected Liberation Mono via monospace category fallback, got %+v ok=%v", got, ok)
	}
}

func TestMatchFace_SymbolAndZapfDingbatsHaveNoCategoryFallback(t *testing.T) {
	// "Symbol" and "ZapfDingbats" are deliberately absent from
	// standard14Categories (see that table's doc comment) - even with a
	// plausible-looking sans-serif candidate present, no match should be
	// found, since substituting ordinary Latin-text glyphs for a symbol
	// font's dingbats would be actively wrong, not just approximate.
	candidates := []FontFace{face("Liberation Sans", false, false, false, false, true)}

	for _, family := range []string{"Symbol", "ZapfDingbats"} {
		query := chars(family, false, false, false, false)
		if _, ok := matchFace(query, candidates); ok {
			t.Errorf("expected no match for %q, since it has no standard-14 category", family)
		}
	}
}

func TestMatchFace_DescriptorFlagsFallback(t *testing.T) {
	// query.Family is empty (or otherwise unrecognized) and not a
	// standard-14 name, but the PDF's own /FontDescriptor said Serif -
	// step 3 of matchFace should still find a serif candidate.
	candidates := []FontFace{
		face("Liberation Sans", false, false, false, false, true),
		face("Liberation Serif", false, false, true, false, true),
	}
	query := chars("SomeUnknownFontXYZ", false, false, true, false)

	got, ok := matchFace(query, candidates)
	if !ok || got.Characteristics.Family != "Liberation Serif" {
		t.Fatalf("expected Liberation Serif via descriptor Serif flag fallback, got %+v ok=%v", got, ok)
	}
}

func TestMatchFace_DescriptorFixedPitchFallback(t *testing.T) {
	candidates := []FontFace{
		face("Liberation Sans", false, false, false, false, true),
		face("Liberation Mono", false, false, false, true, true),
	}
	query := chars("SomeUnknownFontXYZ", false, false, false, true)

	got, ok := matchFace(query, candidates)
	if !ok || got.Characteristics.Family != "Liberation Mono" {
		t.Fatalf("expected Liberation Mono via descriptor FixedPitch flag fallback, got %+v ok=%v", got, ok)
	}
}

func TestMatchFace_NothingUsableFoundReportsNotOK(t *testing.T) {
	candidates := []FontFace{face("Liberation Sans", false, false, false, false, true)}
	query := chars("SomeUnknownFontXYZ", false, false, false, false)

	if _, ok := matchFace(query, candidates); ok {
		t.Fatal("expected no match when nothing is a family, category, or flags match")
	}
}

func TestMatchFace_EmptyCandidatesReportsNotOK(t *testing.T) {
	if _, ok := matchFace(chars("Arial", false, false, false, false), nil); ok {
		t.Fatal("expected no match against an empty candidate list")
	}
}

func TestMatchFace_SkipsCandidatesWithoutOutlines(t *testing.T) {
	// An exact family match with HasOutlines=false must not be chosen -
	// it should be treated as if absent, falling through every remaining
	// step and ultimately reporting no match at all here since no other
	// candidate exists.
	candidates := []FontFace{face("Arial", false, false, false, false, false)}
	query := chars("Arial", false, false, false, false)

	if _, ok := matchFace(query, candidates); ok {
		t.Fatal("expected an outline-less candidate to be skipped, not selected")
	}
}

func TestMatchFace_CategoryFallbackSkipsCandidatesWithoutOutlines(t *testing.T) {
	candidates := []FontFace{
		face("Liberation Serif", false, false, true, false, false), // no outlines
	}
	query := chars("Times New Roman", false, false, false, false)

	if _, ok := matchFace(query, candidates); ok {
		t.Fatal("expected an outline-less category candidate to be skipped, not selected")
	}
}

func TestCategoryOf(t *testing.T) {
	tests := []struct {
		name string
		fc   FontCharacteristics
		want fontCategory
	}{
		{"standard-14 name wins over flags", FontCharacteristics{Family: "Arial", Serif: true}, categorySansSerif},
		{"fixed-pitch flag", FontCharacteristics{Family: "Some Mono Face", FixedPitch: true}, categoryMonospace},
		{"serif flag", FontCharacteristics{Family: "Some Serif Face", Serif: true}, categorySerif},
		{"no signal defaults to sans-serif", FontCharacteristics{Family: "Nondescript"}, categorySansSerif},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := categoryOf(tt.fc); got != tt.want {
				t.Errorf("categoryOf(%+v) = %v, want %v", tt.fc, got, tt.want)
			}
		})
	}
}

// fakeFontSource is the minimal FontSource implementation used to prove
// matchFace works through the interface, not just as a bare function -
// mirroring how a later phase's real DirectorySource will be consumed.
type fakeFontSource struct{ faces []FontFace }

func (s fakeFontSource) Candidates() []FontFace { return s.faces }

// fakeSubstitutionProviderResolver is a minimal Resolver that also
// implements SubstitutionProvider, standing in for
// *internal/model.Document in tests without this package needing to
// import that (higher-level) package - see substitute.go's
// SubstitutionProvider doc comment for why the interface is shaped this
// way (FontSubstitutionSource returning `any`).
type fakeSubstitutionProviderResolver struct {
	fakeResolver
	source any
}

func (r fakeSubstitutionProviderResolver) FontSubstitutionSource() any { return r.source }

func TestSubstitutionSourceFor_NotAProvider(t *testing.T) {
	if _, ok := substitutionSourceFor(fakeResolver{}); ok {
		t.Fatal("expected ok=false for a Resolver that does not implement SubstitutionProvider at all")
	}
}

func TestSubstitutionSourceFor_ProviderWithNoSourceAttached(t *testing.T) {
	r := fakeSubstitutionProviderResolver{source: nil}
	if _, ok := substitutionSourceFor(r); ok {
		t.Fatal("expected ok=false when FontSubstitutionSource returns nil")
	}
}

func TestSubstitutionSourceFor_ProviderWithSourceAttached(t *testing.T) {
	want := fakeFontSource{faces: []FontFace{face("Arial", false, false, false, false, true)}}
	r := fakeSubstitutionProviderResolver{source: want}

	src, ok := substitutionSourceFor(r)
	if !ok {
		t.Fatal("expected ok=true when a real FontSource was attached")
	}
	got, ok := matchFace(chars("Arial", false, false, false, false), src.Candidates())
	if !ok || got.Characteristics.Family != "Arial" {
		t.Fatalf("expected the resolved FontSource to behave like the original, got %+v ok=%v", got, ok)
	}
}

func TestSubstitutionSourceFor_ProviderReturningNonFontSource(t *testing.T) {
	r := fakeSubstitutionProviderResolver{source: "not a FontSource"}
	if _, ok := substitutionSourceFor(r); ok {
		t.Fatal("expected ok=false when FontSubstitutionSource returns something that isn't a FontSource")
	}
}

func TestFontSource_Interface(t *testing.T) {
	var src FontSource = fakeFontSource{faces: []FontFace{face("Arial", false, false, false, false, true)}}
	got, ok := matchFace(chars("Arial", false, false, false, false), src.Candidates())
	if !ok || got.Characteristics.Family != "Arial" {
		t.Fatalf("expected a match through the FontSource interface, got %+v ok=%v", got, ok)
	}
}

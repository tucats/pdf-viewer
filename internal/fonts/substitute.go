package fonts

import "strings"

// This file implements the matching half of Phase 4 of the
// font-substitution work described in docs/FONTS.md: given a
// FontCharacteristics describing what a PDF font *should* look like
// (built by Phase 1's Characterize, from a font dictionary this package
// could not extract a real outline for), pick the best available
// candidate FontFace (Phase 2's probe.go type) out of a FontSource's own
// list of known candidates - or report that nothing usable was found, in
// which case the caller (simple.go's loadSimpleFont) falls back to
// notdefGlyph exactly as it does today. Nothing in this file touches a
// filesystem; directory_source.go (a later sub-phase) is what actually
// builds a real FontSource by scanning disk. Keeping the two separate is
// what makes the algorithm in this file testable with small, fully
// synthetic candidate lists (see substitute_test.go) rather than
// depending on whatever fonts happen to be installed on the machine
// running `go test`.
//
// # If you are new to Go: why this file defines an interface (FontSource)
//
// FontSource below is a Go "interface type": it lists method signatures,
// not fields or a concrete implementation. Any type that happens to
// define a Candidates method with this exact signature automatically
// "satisfies" the interface - there is no explicit "implements
// FontSource" declaration to write, unlike some other languages. That is
// what lets substitute_test.go define its own tiny fake FontSource (a
// slice of FontFace values with a Candidates method) without this
// package needing to know tests exist at all, and it is what will let
// directory_source.go's real, disk-scanning FontSource slot into exactly
// the same matching code later - see resolver.go's doc comment for the
// same pattern already used elsewhere in this package.

// FontSource supplies the candidate font faces matchFace chooses among
// when this package needs a substitute outline for a font it could not
// extract one from directly. directory_source.go's DirectorySource is
// the only implementation this package ships (a directory scan using
// Phase 2's ProbeFontFile), but FontSource is its own interface - rather
// than matchFace simply taking a []FontFace directly - so that a real
// disk-backed source can build its candidate index lazily and cache it
// (see DirectorySource's own doc comment), without every call to
// matchFace needing to know or care whether that work has already
// happened.
type FontSource interface {
	// Candidates returns every known candidate face, in priority order:
	// when two candidates are otherwise an equally good match (see
	// matchFace), the one appearing earlier in this slice wins. For
	// DirectorySource this means most-local-first (a font a specific
	// user installed for themselves beats a same-named one shipped by
	// the operating system) - see docs/FONTS.md's "Configuration"
	// section for the full rationale. A candidate whose HasOutlines is
	// false may still appear here (Phase 2's ProbeFontFile
	// characterizes every face it can read the "name"/"OS/2" tables of,
	// regardless of outline support - see FontFace's own doc comment);
	// matchFace is responsible for skipping those, not Candidates.
	Candidates() []FontFace
}

// fontCategory is a coarse classification matchFace falls back to when
// no candidate carries the exact family name a PDF font asked for (see
// matchFace's second and third fallback steps below). This is
// deliberately a small, closed set - just enough to tell "a serif
// substitute is closer to what was wanted than a sans-serif one," not a
// fine-grained design taxonomy.
type fontCategory int

const (
	// categoryUnknown means "no category could be determined" - never a
	// valid target category to search for (see bestByCategory, which is
	// never called with it), but the zero value a lookup miss returns.
	categoryUnknown fontCategory = iota
	categorySansSerif
	categorySerif
	categoryMonospace
)

// standard14Categories maps a normalized (see normalizeFamily) standard
// PostScript base font family name - the "standard 14" fonts every PDF
// consumer is traditionally expected to have available without
// embedding, PDF specification section 9.6.2.2 - to the broad category
// matchFace searches installed candidates in in its second and third
// fallback steps (see matchFace's own doc comment). "Symbol" and
// "ZapfDingbats" are deliberately not listed here: both are symbol fonts
// whose glyphs are drawing shapes/dingbats rather than Latin letterforms
// at standard Unicode code points, so substituting *any* sans-serif or
// serif text font for one would produce visibly wrong (not just
// differently-shaped) glyphs - falling all the way through to
// notdefGlyph is the more honest outcome for those two names, matching
// this package's general "never silently produce a plausible-looking
// wrong answer" preference.
var standard14Categories = map[string]fontCategory{
	"helvetica":       categorySansSerif,
	"arial":           categorySansSerif,
	"times":           categorySerif,
	"times new roman": categorySerif,
	"times roman":     categorySerif,
	"courier":         categoryMonospace,
	"courier new":     categoryMonospace,
}

// normalizeFamily lowercases and trims name, giving a form suitable for
// case-insensitive family-name comparison (both exact matching in
// matchFace's first step and standard14Categories lookups in its
// second). Both sides of every comparison this file makes go through
// this same function, so differences in case alone (e.g. a PDF's
// "Arial" against a font file's own "ARIAL") never prevent a match.
func normalizeFamily(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// matchFace picks the single best candidate in candidates for query,
// or ok=false if nothing in candidates is usable at all. It tries four
// steps in order, stopping at the first that finds anything, per
// docs/FONTS.md's "Matching algorithm" section:
//
//  1. Exact, case-insensitive family-name match: every candidate whose
//     own Characteristics.Family equals query.Family, narrowed by
//     bold/italic (see bestByStyle).
//  2. No family match: if query.Family is itself a recognized
//     standard-14 name (see standard14Categories), search every
//     candidate in that name's broad category (sans-serif, serif, or
//     monospace) instead.
//  3. Still nothing: fall back to query's own descriptor-derived
//     Serif/FixedPitch flags (see FontCharacteristics' doc comment),
//     searching candidates in that category the same way - this is a
//     weaker signal than step 2 (a raw /Flags bit rather than a
//     recognized name), which is why it is tried last.
//  4. Nothing found by any step: ok=false, and the caller falls back to
//     notdefGlyph exactly as if substitution were disabled.
//
// A candidate with HasOutlines false (Phase 2 could characterize it -
// read its name/OS/2 tables - but this package cannot extract its
// glyphs, e.g. an OTTO face with no "CFF " table) is never selected by
// any step: bestByStyle and its callers below all filter on HasOutlines
// first, so a candidate that could never actually produce a glyph is as
// if it were not there at all, rather than "winning" a match it cannot
// honor.
func matchFace(query FontCharacteristics, candidates []FontFace) (FontFace, bool) {
	queryFamily := normalizeFamily(query.Family)

	if queryFamily != "" {
		var familyMatches []FontFace
		for _, c := range candidates {
			if c.HasOutlines && normalizeFamily(c.Characteristics.Family) == queryFamily {
				familyMatches = append(familyMatches, c)
			}
		}
		if face, ok := bestByStyle(query, familyMatches); ok {
			return face, true
		}
	}

	if cat, ok := standard14Categories[queryFamily]; ok {
		if face, ok := bestByCategory(cat, query, candidates); ok {
			return face, true
		}
	}

	switch {
	case query.FixedPitch:
		if face, ok := bestByCategory(categoryMonospace, query, candidates); ok {
			return face, true
		}
	case query.Serif:
		if face, ok := bestByCategory(categorySerif, query, candidates); ok {
			return face, true
		}
	}

	return FontFace{}, false
}

// bestByCategory filters candidates down to those with usable outlines
// whose own categoryOf classification is cat, then picks the best
// bold/italic match among them - the shared second half of matchFace's
// steps 2 and 3 above, which differ only in how they decide which
// category to search.
func bestByCategory(cat fontCategory, query FontCharacteristics, candidates []FontFace) (FontFace, bool) {
	var matches []FontFace
	for _, c := range candidates {
		if c.HasOutlines && categoryOf(c.Characteristics) == cat {
			matches = append(matches, c)
		}
	}
	return bestByStyle(query, matches)
}

// categoryOf classifies a candidate face's own FontCharacteristics into
// a fontCategory, for comparison against the target category matchFace's
// steps 2/3 are searching for. A candidate's own family name is checked
// against standard14Categories first (a real system font is very
// commonly itself named "Arial" or "Courier New" - reusing the same
// table both directions keeps the classification consistent with what a
// PDF's own /BaseFont would resolve to), then its Serif/FixedPitch flags
// (see FontCharacteristics' doc comment - for a candidate probed by
// probe.go's characterizeFace, Serif comes from the font's own "OS/2"
// sFamilyClass when present; FixedPitch is not currently populated for a
// probed candidate at all - see probe.go's Phase 2 non-goals - so this
// branch only ever fires for FixedPitch when categorizing a query built
// from a PDF's own /FontDescriptor, never for a candidate). A candidate
// matching neither is assumed sans-serif, the most common case for a
// proportional font with no other signal either way.
func categoryOf(fc FontCharacteristics) fontCategory {
	if cat, ok := standard14Categories[normalizeFamily(fc.Family)]; ok {
		return cat
	}
	if fc.FixedPitch {
		return categoryMonospace
	}
	if fc.Serif {
		return categorySerif
	}
	return categorySansSerif
}

// bestByStyle picks the candidate in candidates whose Bold/Italic best
// match query's, scoring each candidate by how many of the two traits
// agree (0, 1, or 2) and keeping the first candidate to reach a new best
// score. Iterating in candidates' own order and only replacing the
// current best on a strictly greater score (never a tie) is what
// implements FontSource.Candidates' documented tie-breaking rule
// ("earlier wins") - see that method's doc comment - without this
// function needing to know anything about *why* candidates came in that
// order (most-local-first, for DirectorySource) at all.
//
// ok is false only when candidates is empty; a non-empty candidates
// slice always has *some* best-scoring entry, even a 0-scoring one (a
// candidate matching neither Bold nor Italic is still a better outcome
// than notdefGlyph's placeholder box, per docs/FONTS.md's overall
// design).
func bestByStyle(query FontCharacteristics, candidates []FontFace) (FontFace, bool) {
	bestScore := -1
	var best FontFace
	for _, c := range candidates {
		score := 0
		if c.Characteristics.Bold == query.Bold {
			score++
		}
		if c.Characteristics.Italic == query.Italic {
			score++
		}
		if score > bestScore {
			bestScore = score
			best = c
		}
	}
	return best, bestScore >= 0
}

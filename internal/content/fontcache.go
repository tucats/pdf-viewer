package content

import "github.com/tucats/pdf-viewer/internal/fonts"

// This file implements FontCache, the piece of Phase 6's "resource
// caching" work that applies to internal/content: remembering an
// already-loaded *fonts.Font across more than one call to Interpret (or
// InterpretCached - see interpret.go), rather than only within the one
// call that first loaded it.
//
// # Why this was deferred until now
//
// Before this sub-phase, text.go's lookupFont already cached a loaded
// font - but only in textInterpreterState.fontCache, a plain map keyed
// by the font's /Resources /Font resource *name* and created fresh by
// every interpreter (see interpretAtDepth). That map is thrown away the
// moment Interpret returns, so re-rendering the same page (Render, then
// Thumbnail of the same page - see the root package's page.go, which
// does exactly this) - or rendering two different pages that both use
// the same embedded font program, extremely common in real documents -
// re-parsed that font's embedded program (a real cost for a
// several-hundred-glyph TrueType program - see internal/fonts' package
// doc comment) every single time.
//
// The repository README's Phase 5g entry deliberately deferred fixing
// this to Phase 6, because designing a cache that survives across
// Interpret calls first needed Phase 6's own "decide concurrency
// guarantees" question settled - a cache shared across calls only
// behaves correctly under a known concurrency contract. Phase 6a
// decided that: a *Document (and everything reachable from it,
// including this cache) is not safe for concurrent use by multiple
// goroutines - see the root package's Document type doc comment. That
// is exactly why FontCache below carries no locking of its own.

// FontCache remembers every *fonts.Font this package has already built
// via internal/fonts.Load, keyed by the font dictionary's own indirect
// object number (not by resource name, which is only meaningful within
// one content stream's own /Resources - see lookupFont in text.go for
// where both keys are actually used together). A caller that shares one
// FontCache across every Interpret call it makes against the same
// document (the root package's Document does exactly this - see
// document.go) gets each embedded font program parsed at most once for
// the lifetime of that cache, no matter how many pages reference it or
// how many times any one page is rendered.
//
// A nil *FontCache is valid and behaves as "no cross-call caching at
// all" (every method below is nil-receiver-safe) - this is what plain
// Interpret (as opposed to InterpretCached) passes down, keeping every
// existing caller and test that does not care about cross-call caching
// unchanged.
//
// Per this project's Phase 6 concurrency decision (see the root
// package's Document doc comment), a FontCache - like every other
// per-Document cache in this project - is not safe for concurrent use
// by multiple goroutines.
type FontCache struct {
	byRef map[int]*fonts.Font
}

// NewFontCache returns an empty FontCache ready for use.
func NewFontCache() *FontCache {
	return &FontCache{byRef: make(map[int]*fonts.Font)}
}

// get returns the font previously stored under object number ref, if
// any. Safe to call on a nil *FontCache (always a miss).
func (c *FontCache) get(ref int) (*fonts.Font, bool) {
	if c == nil {
		return nil, false
	}
	f, ok := c.byRef[ref]
	return f, ok
}

// put stores f under object number ref for later get calls. A no-op on
// a nil *FontCache.
func (c *FontCache) put(ref int, f *fonts.Font) {
	if c == nil {
		return
	}
	c.byRef[ref] = f
}

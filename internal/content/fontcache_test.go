package content

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// TestInterpretCachedReusesFontAcrossCalls confirms the actual point of
// FontCache (see its doc comment): loading the same font resource -
// object 5, here, exactly like fakeFontResources/nonEmbeddedSimpleFontDict
// already set up for text.go's other tests - across two separate
// InterpretCached calls sharing one *FontCache resolves and loads it
// only once, not twice. It uses fakeResolver's resolveCalls counter
// (image_test.go) as the observable proxy for "was this font's
// dictionary looked at again", since internal/fonts.Load itself has no
// instrumentation of its own to hook into from this package's tests.
func TestInterpretCachedReusesFontAcrossCalls(t *testing.T) {
	const fontObjNum = 5
	resolver := &fakeResolver{objects: map[int]syntax.Object{fontObjNum: nonEmbeddedSimpleFontDict}}
	resources := fakeFontResources(fontObjNum)
	cache := NewFontCache()

	src := "BT\n/F1 10 Tf\n0 0 Td\n(A) Tj\nET\n"
	ops, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}

	if _, err := InterpretCached(ops, graphics.Identity(), resources, resolver, cache); err != nil {
		t.Fatalf("first InterpretCached: %v", err)
	}
	firstCalls := resolver.resolveCalls[fontObjNum]
	if firstCalls == 0 {
		t.Fatalf("resolveCalls[%d] = 0 after first InterpretCached, want at least 1 (the font dictionary was never resolved at all)", fontObjNum)
	}

	if _, err := InterpretCached(ops, graphics.Identity(), resources, resolver, cache); err != nil {
		t.Fatalf("second InterpretCached: %v", err)
	}
	secondCalls := resolver.resolveCalls[fontObjNum]
	if secondCalls != firstCalls {
		t.Errorf("resolveCalls[%d] went from %d to %d across a second InterpretCached call sharing the same *FontCache; want unchanged (cache hit)", fontObjNum, firstCalls, secondCalls)
	}
}

// TestInterpretCachedWithoutSharedCacheReloadsFont is
// TestInterpretCachedReusesFontAcrossCalls' control case: two
// InterpretCached calls that do *not* share a *FontCache (nil, exactly
// like plain Interpret) must each resolve the font dictionary again -
// confirming the previous test's assertion is actually exercising the
// cache, not some other, unrelated reason resolveCalls stopped
// increasing.
func TestInterpretCachedWithoutSharedCacheReloadsFont(t *testing.T) {
	const fontObjNum = 5
	resolver := &fakeResolver{objects: map[int]syntax.Object{fontObjNum: nonEmbeddedSimpleFontDict}}
	resources := fakeFontResources(fontObjNum)

	src := "BT\n/F1 10 Tf\n0 0 Td\n(A) Tj\nET\n"
	ops, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}

	if _, err := InterpretCached(ops, graphics.Identity(), resources, resolver, nil); err != nil {
		t.Fatalf("first InterpretCached: %v", err)
	}
	firstCalls := resolver.resolveCalls[fontObjNum]

	if _, err := InterpretCached(ops, graphics.Identity(), resources, resolver, nil); err != nil {
		t.Fatalf("second InterpretCached: %v", err)
	}
	secondCalls := resolver.resolveCalls[fontObjNum]
	if secondCalls <= firstCalls {
		t.Errorf("resolveCalls[%d] went from %d to %d across a second InterpretCached call with no shared cache; want it to increase (no caching)", fontObjNum, firstCalls, secondCalls)
	}
}

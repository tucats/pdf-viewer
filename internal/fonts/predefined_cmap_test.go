package fonts

import "testing"

// fakeCMapSource is a minimal CMapSource, mirroring substitute_test.go's
// fakeFontSource exactly - see this file's other fakes for the same
// "prove the interface actually works, not just the concrete function"
// rationale.
type fakeCMapSource struct{ data map[string][]byte }

func (s fakeCMapSource) CMapData(name string) ([]byte, bool) {
	d, ok := s.data[name]
	return d, ok
}

// fakeCMapSourceProviderResolver is a minimal Resolver that also
// implements CMapSourceProvider, standing in for *internal/model.Document
// in tests without this package needing to import that (higher-level)
// package - see predefined_cmap.go's CMapSourceProvider doc comment for
// why the interface is shaped this way (PredefinedCMapSource returning
// `any`), mirroring substitute_test.go's fakeSubstitutionProviderResolver.
type fakeCMapSourceProviderResolver struct {
	fakeResolver
	source any
}

func (r fakeCMapSourceProviderResolver) PredefinedCMapSource() any { return r.source }

func TestCMapSourceFor_NotAProvider(t *testing.T) {
	if _, ok := cmapSourceFor(fakeResolver{}); ok {
		t.Fatal("expected ok=false for a Resolver that does not implement CMapSourceProvider at all")
	}
}

func TestCMapSourceFor_ProviderWithNoSourceAttached(t *testing.T) {
	r := fakeCMapSourceProviderResolver{source: nil}
	if _, ok := cmapSourceFor(r); ok {
		t.Fatal("expected ok=false when PredefinedCMapSource returns nil")
	}
}

func TestCMapSourceFor_ProviderReturningNonCMapSource(t *testing.T) {
	r := fakeCMapSourceProviderResolver{source: "not a CMapSource"}
	if _, ok := cmapSourceFor(r); ok {
		t.Fatal("expected ok=false when PredefinedCMapSource returns something that isn't a CMapSource")
	}
}

// TestPredefinedCMapFor_NoSourceConfigured confirms the overwhelmingly
// common case - no pdfviewer.WithPredefinedCMaps option, so resolver
// doesn't implement CMapSourceProvider at all - costs nothing beyond one
// failed type assertion and returns ok=false, matching
// loadType0Encoding's fallback to "still usable, just imprecise" for a
// predefined name it cannot resolve.
func TestPredefinedCMapFor_NoSourceConfigured(t *testing.T) {
	if _, ok := predefinedCMapFor(fakeResolver{}, "UniGB-UCS2-H"); ok {
		t.Fatal("expected ok=false with no CMapSource configured at all")
	}
}

// TestPredefinedCMapFor_UnknownName confirms a configured source that
// simply doesn't have data for the requested name behaves the same as
// no source at all, rather than panicking on a nil/empty result.
func TestPredefinedCMapFor_UnknownName(t *testing.T) {
	r := fakeCMapSourceProviderResolver{source: fakeCMapSource{data: map[string][]byte{}}}
	if _, ok := predefinedCMapFor(r, "Some-Unknown-Encoding"); ok {
		t.Fatal("expected ok=false for a name the configured CMapSource has no data for")
	}
}

// TestPredefinedCMapFor_ResolvesRealData confirms a configured
// CMapSource's data is actually parsed and usable, exactly like an
// embedded CMap stream would be.
func TestPredefinedCMapFor_ResolvesRealData(t *testing.T) {
	r := fakeCMapSourceProviderResolver{source: fakeCMapSource{data: map[string][]byte{
		"Test-Predefined": []byte(`
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
1 begincidrange
<0041> <005A> 1
endcidrange
endcmap
`),
	}}}

	cm, ok := predefinedCMapFor(r, "Test-Predefined")
	if !ok {
		t.Fatal("expected ok=true for a name the configured CMapSource has data for")
	}
	if cid, ok := cm.CIDForCode(0x0041); !ok || cid != 1 {
		t.Errorf("CIDForCode(0x41) = (%d,%v), want (1,true)", cid, ok)
	}
}

// TestPredefinedCMapFor_UseCMapChainsThroughSameSource confirms a
// predefined CMap that itself declares "usecmap" (a real-world pattern:
// several of Adobe's own predefined encodings build on a simpler base)
// resolves that reference against the same CMapSource, not just an
// embedded stream's own usecmap.
func TestPredefinedCMapFor_UseCMapChainsThroughSameSource(t *testing.T) {
	r := fakeCMapSourceProviderResolver{source: fakeCMapSource{data: map[string][]byte{
		"Base": []byte(`
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
1 begincidrange
<0000> <FFFF> 0
endcidrange
endcmap
`),
		"Derived": []byte(`
/Base usecmap
1 begincidchar
<0041> 999
endcidchar
endcmap
`),
	}}}

	cm, ok := predefinedCMapFor(r, "Derived")
	if !ok {
		t.Fatal("expected ok=true resolving Derived")
	}
	if cid, ok := cm.CIDForCode(0x0041); !ok || cid != 999 {
		t.Errorf("CIDForCode(0x41) = (%d,%v), want (999,true) - Derived's own entry", cid, ok)
	}
	if cid, ok := cm.CIDForCode(0x0042); !ok || cid != 0x0042 {
		t.Errorf("CIDForCode(0x42) = (%d,%v), want (66,true) via the inherited Base", cid, ok)
	}
}

// TestNewPredefinedCMapResolver_CycleGuardTerminates confirms that two
// predefined names usecmap-ing each other (a resolver misconfiguration,
// or corrupted/hostile CMap resource data - not something a well-formed
// Adobe resource set should ever do, but not this package's to trust
// blindly either) does not recurse forever: resolving either name must
// return promptly, and the resulting CMap must still expose whatever
// entries were reachable before the cycle was cut off.
func TestNewPredefinedCMapResolver_CycleGuardTerminates(t *testing.T) {
	r := fakeCMapSourceProviderResolver{source: cyclicCMapSource{}}

	cm, ok := predefinedCMapFor(r, "A")
	if !ok {
		t.Fatal("expected ok=true resolving A despite the A<->B cycle")
	}
	// A's own direct entry must still be visible...
	if cid, ok := cm.CIDForCode(0x0001); !ok || cid != 1 {
		t.Errorf("CIDForCode(0x1) = (%d,%v), want (1,true) - A's own entry", cid, ok)
	}
}

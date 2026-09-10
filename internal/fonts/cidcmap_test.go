package fonts

import "testing"

// This file tests cidcmap.go's CMap parsing in isolation, before cid.go
// (a later sub-phase) ever wires it into a real Font - see that file's
// own doc comment for why CMap is designed to be tested this way (a
// plain []byte in, a *CMap with plain methods out, no PDF document
// structure involved at all).

// TestParseCMap_CodespaceAndCIDRange confirms the common real-world
// shape: one 2-byte codespace range plus a couple of cidrange entries,
// matching what most embedded CMaps and predefined CJK encodings
// actually look like.
func TestParseCMap_CodespaceAndCIDRange(t *testing.T) {
	t.Parallel()
	data := []byte(`
/CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> def
/CMapName /Test-Encoding def
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
2 begincidrange
<0000> <00FF> 0
<0100> <01FF> 256
endcidrange
endcmap
`)
	cm := parseCMap(data, nil)

	codes := cm.decode([]byte{0x00, 0x41, 0x01, 0x02})
	if len(codes) != 2 {
		t.Fatalf("decode returned %d codes, want 2", len(codes))
	}
	if codes[0] != (DecodedCode{Code: 0x0041, Bytes: 2}) {
		t.Errorf("codes[0] = %+v, want {0x41 2}", codes[0])
	}
	if codes[1] != (DecodedCode{Code: 0x0102, Bytes: 2}) {
		t.Errorf("codes[1] = %+v, want {0x102 2}", codes[1])
	}

	if cid, ok := cm.CIDForCode(0x0041); !ok || cid != 0x41 {
		t.Errorf("CIDForCode(0x41) = (%d,%v), want (65,true)", cid, ok)
	}
	if cid, ok := cm.CIDForCode(0x0102); !ok || cid != 256+2 {
		t.Errorf("CIDForCode(0x102) = (%d,%v), want (258,true)", cid, ok)
	}
	if _, ok := cm.CIDForCode(0xFFFF); ok {
		t.Errorf("CIDForCode(0xFFFF) reported found, want not found (outside every declared range)")
	}
}

// TestParseCMap_CIDChar confirms an isolated begincidchar entry is
// found and takes priority over a cidrange that also happens to cover
// the same code (a conforming CMap should never declare both for one
// code, but CIDForCode's own doc comment promises this order regardless).
func TestParseCMap_CIDChar(t *testing.T) {
	t.Parallel()
	data := []byte(`
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
1 begincidrange
<0000> <FFFF> 0
endcidrange
1 begincidchar
<0041> 999
endcidchar
endcmap
`)
	cm := parseCMap(data, nil)

	if cid, ok := cm.CIDForCode(0x0041); !ok || cid != 999 {
		t.Errorf("CIDForCode(0x41) = (%d,%v), want (999,true) - cidchar should win over cidrange", cid, ok)
	}
	if cid, ok := cm.CIDForCode(0x0042); !ok || cid != 0x42 {
		t.Errorf("CIDForCode(0x42) = (%d,%v), want (66,true) via the cidrange", cid, ok)
	}
}

// TestParseCMap_MixedByteLengthCodespace confirms decode's general
// shortest-length-first matching algorithm against a codespace mixing
// 1-byte and 2-byte codes - a rarer real-world shape (some legacy CJK
// encodings reserve the 1-byte codespace for ASCII), but one the
// specification's own algorithm explicitly describes.
func TestParseCMap_MixedByteLengthCodespace(t *testing.T) {
	t.Parallel()
	data := []byte(`
2 begincodespacerange
<00> <7F>
<8140> <FCFC>
endcodespacerange
1 begincidrange
<00> <7F> 0
endcidrange
1 begincidrange
<8140> <8140> 1000
endcidrange
endcmap
`)
	cm := parseCMap(data, nil)

	// "A" (0x41, a 1-byte code, since 0x41 <= 0x7F) followed by the
	// 2-byte code 0x8140 (0x81 alone is not a valid 1-byte code - 0x81 >
	// 0x7F - so decode must consume both bytes together).
	codes := cm.decode([]byte{0x41, 0x81, 0x40})
	if len(codes) != 2 {
		t.Fatalf("decode returned %d codes, want 2: %+v", len(codes), codes)
	}
	if codes[0] != (DecodedCode{Code: 0x41, Bytes: 1}) {
		t.Errorf("codes[0] = %+v, want {0x41 1}", codes[0])
	}
	if codes[1] != (DecodedCode{Code: 0x8140, Bytes: 2}) {
		t.Errorf("codes[1] = %+v, want {0x8140 2}", codes[1])
	}

	if cid, ok := cm.CIDForCode(0x41); !ok || cid != 0x41 {
		t.Errorf("CIDForCode(0x41) = (%d,%v), want (65,true)", cid, ok)
	}
	if cid, ok := cm.CIDForCode(0x8140); !ok || cid != 1000 {
		t.Errorf("CIDForCode(0x8140) = (%d,%v), want (1000,true)", cid, ok)
	}
}

// TestParseCMap_UseCMap confirms a "usecmap" operator chains to a
// resolver-supplied parent CMap, inheriting both its codespace (when
// this CMap declares none of its own) and its CID mappings for any code
// this CMap's own tables don't cover - see CMap.parent's doc comment for
// the "supplements, doesn't replace" semantics being tested here.
func TestParseCMap_UseCMap(t *testing.T) {
	t.Parallel()
	parentData := []byte(`
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
1 begincidrange
<0000> <FFFF> 0
endcidrange
endcmap
`)
	parent := parseCMap(parentData, nil)

	childData := []byte(`
/ParentCMap usecmap
1 begincidchar
<0041> 999
endcidchar
endcmap
`)
	resolve := func(name string) (*CMap, bool) {
		if name == "ParentCMap" {
			return parent, true
		}
		return nil, false
	}
	child := parseCMap(childData, resolve)

	// The child declares no codespacerange of its own, so it must fall
	// back to the parent's - otherwise decode would have nothing to
	// split codes by at all.
	codes := child.decode([]byte{0x00, 0x41})
	if len(codes) != 1 || codes[0] != (DecodedCode{Code: 0x0041, Bytes: 2}) {
		t.Fatalf("decode = %+v, want one 2-byte code 0x41 (inherited codespace)", codes)
	}

	// The child's own cidchar entry overrides the parent's cidrange for
	// the same code...
	if cid, ok := child.CIDForCode(0x0041); !ok || cid != 999 {
		t.Errorf("CIDForCode(0x41) = (%d,%v), want (999,true) - child's own entry", cid, ok)
	}
	// ...but a code the child says nothing about at all still resolves
	// via the inherited parent.
	if cid, ok := child.CIDForCode(0x0042); !ok || cid != 0x0042 {
		t.Errorf("CIDForCode(0x42) = (%d,%v), want (66,true) via inherited parent", cid, ok)
	}
}

// TestParseCMap_UnresolvedUseCMapIsHarmless confirms that a "usecmap"
// operator naming something the caller's resolver cannot find (or, as
// here, no resolver at all - the nil case loadType0Encoding uses before
// Phase 9c's predefined-CMap support exists) simply leaves the CMap
// with no parent, rather than panicking or corrupting later parsing.
func TestParseCMap_UnresolvedUseCMapIsHarmless(t *testing.T) {
	t.Parallel()
	data := []byte(`
/SomePredefinedName usecmap
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
1 begincidchar
<0041> 65
endcidchar
endcmap
`)
	cm := parseCMap(data, nil)
	if cm.parent != nil {
		t.Errorf("parent = %+v, want nil (no resolver supplied)", cm.parent)
	}
	if cid, ok := cm.CIDForCode(0x0041); !ok || cid != 65 {
		t.Errorf("CIDForCode(0x41) = (%d,%v), want (65,true) - own entries still work", cid, ok)
	}
	if _, ok := cm.CIDForCode(0x0099); ok {
		t.Errorf("CIDForCode(0x99) reported found, want not found (no parent, no matching entry)")
	}
}

// TestParseCMap_MalformedInputTolerated confirms that truncated,
// out-of-order, or otherwise malformed CMap content never panics and
// never blocks parsing of whatever comes after it - matching this
// project's general "one bad field doesn't fail everything" policy
// (see, for example, cid.go's parseCIDWidths doc comment).
func TestParseCMap_MalformedInputTolerated(t *testing.T) {
	t.Parallel()
	cases := [][]byte{
		nil,
		[]byte(""),
		[]byte("this is not a cmap at all"),
		[]byte("1 begincodespacerange <00 endcodespacerange"), // truncated hex string
		[]byte("1 begincidrange <0000> <00FF>\nendcidrange"),  // missing cid value
		[]byte("1 begincidrange <0000> <00FF> notanumber\nendcidrange"),
		[]byte("1 begincidchar <0041>\nendcidchar"),                     // missing cid value
		[]byte("1 begincodespacerange\n<0000> <FF>\nendcodespacerange"), // mismatched lengths
		[]byte("1 begincidrange\n<0000> <FFFFFFFFFFFF> 0\nendcidrange"), // oversized hex
		[]byte("begincodespacerange"),                                   // never closed
		[]byte("begincidrange <0000> <FFFF> 0"),                         // never closed
	}
	for i, data := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("case %d: parseCMap panicked: %v", i, r)
				}
			}()
			cm := parseCMap(data, nil)
			if cm == nil {
				t.Errorf("case %d: parseCMap returned nil, want a usable zero-ish *CMap", i)
			}
			// Every malformed case above should simply mean "no CID
			// found" for an arbitrary code, not a crash.
			cm.CIDForCode(0x41)
			cm.decode([]byte{0x00, 0x41})
		}()
	}
}

// TestParseCMap_NoCodespaceFallsBackByteAtATime confirms decode's last
// resort (no codespace declared at all) matches simple fonts' own
// one-byte-per-code behavior, rather than returning nothing.
func TestParseCMap_NoCodespaceFallsBackByteAtATime(t *testing.T) {
	t.Parallel()
	cm := parseCMap([]byte("endcmap"), nil)
	codes := cm.decode([]byte{0x41, 0x42})
	if len(codes) != 2 || codes[0] != (DecodedCode{Code: 0x41, Bytes: 1}) || codes[1] != (DecodedCode{Code: 0x42, Bytes: 1}) {
		t.Errorf("decode (no codespace) = %+v, want one byte per code", codes)
	}
}

// TestParseCMap_UnmatchedPrefixFallsBackToFirstCodespaceLength
// confirms that a byte sequence outside every declared codespace range
// still gets split into codes (using the first declared range's byte
// length) rather than being silently dropped - matching decode's own
// documented fallback for malformed/out-of-codespace content.
func TestParseCMap_UnmatchedPrefixFallsBackToFirstCodespaceLength(t *testing.T) {
	t.Parallel()
	data := []byte(`
1 begincodespacerange
<0100> <01FF>
endcodespacerange
endcmap
`)
	cm := parseCMap(data, nil)
	// 0x0041 falls outside the declared <0100>-<01FF> range entirely,
	// but the codespace still declares a 2-byte length, so decode should
	// still consume 2 bytes at a time rather than falling back to 1.
	codes := cm.decode([]byte{0x00, 0x41, 0x01, 0x50})
	if len(codes) != 2 {
		t.Fatalf("decode = %+v, want 2 codes", codes)
	}
	if codes[0].Bytes != 2 || codes[1].Bytes != 2 {
		t.Errorf("decode = %+v, want both codes to be 2 bytes (the declared codespace length)", codes)
	}
}

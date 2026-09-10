package jpx

import "testing"

// This file tests the per-tile COD/COC/QCD/QCC override threading this
// sub-phase (14b) adds: 14a parsed these tile-part-scoped marker
// segments but deliberately discarded them (see markers.go's
// parseTilePartHeader doc comment, pre-14b) - Header.effectiveCoding/
// effectiveQuant (siz.go) are what 14b's packet parsing (packet.go)
// actually consults instead of the codestream-wide DefaultCoding/
// DefaultQuant, and this is their own dedicated coverage (packet_test.go
// and packet_roundtrip_test.go only ever exercise the no-override path).

// buildTilePartWithOverrides assembles one tile-part - SOT, then any of
// segments (already-built marker segments, e.g. via segment(markerCOD,
// ...)), then SOD and data - patching Psot exactly as buildTilePart
// does (testutil_test.go), letting a test insert tile-part-scoped
// COD/COC/QCD/QCC segments that helper has no way to express.
func buildTilePartWithOverrides(tileIndex uint16, partIndex, partCount byte, segments [][]byte, data []byte) []byte {
	sot := segment(markerSOT, codeSOT(tileIndex, partIndex, partCount))
	tp := append([]byte{}, sot...)
	for _, s := range segments {
		tp = append(tp, s...)
	}
	tp = append(tp, bareMarker(markerSOD)...)
	tp = append(tp, data...)

	const psotOffset = 6
	binary := tp[psotOffset : psotOffset+4]
	length := uint32(len(tp))
	binary[0] = byte(length >> 24)
	binary[1] = byte(length >> 16)
	binary[2] = byte(length >> 8)
	binary[3] = byte(length)
	return tp
}

func TestTilePartCODCOCQCDQCCOverrides(t *testing.T) {
	// Codestream-wide default: no wavelet decomposition, quantStyle
	// none. Two components, two tiles side by side.
	def := defaultTileConfig
	// Tile 1's own override: 1 decomposition level (different subband
	// count than the default), a different code-block size, quantStyle
	// expounded.
	override := tileConfig{decompLevels: 1, cbWidthExp: 2, cbHeightExp: 2, transform: 0, quantStyle: 2, guardBits: 3}
	componentOverride := tileConfig{decompLevels: 1, cbWidthExp: 3, cbHeightExp: 3, transform: 0, quantStyle: 2, guardBits: 1}

	cs := bareMarker(markerSOC)
	cs = append(cs, segment(markerSIZ, codeSIZ(64, 32, 32, 32, 2, 8, false))...)
	cs = append(cs, segment(markerCOD, codeCOD(def, 1))...)
	cs = append(cs, segment(markerQCD, codeQCD(def, subbandsFor(def)))...)

	// Tile 0: no overrides at all.
	cs = append(cs, buildTilePartWithOverrides(0, 0, 1, nil, []byte{0xAA})...)

	// Tile 1: its own COD/QCD (tile-wide override) plus a COC/QCC for
	// component 1 specifically (more specific than the tile's own COD).
	tile1Segments := [][]byte{
		segment(markerCOD, codeCOD(override, 1)),
		segment(markerQCD, codeQCD(override, subbandsFor(override))),
		segment(markerCOC, codeCOC(1, componentOverride)),
		segment(markerQCC, codeQCC(1, componentOverride, subbandsFor(componentOverride))),
	}
	cs = append(cs, buildTilePartWithOverrides(1, 0, 1, tile1Segments, []byte{0xBB})...)
	cs = append(cs, bareMarker(markerEOC)...)

	h, err := ParseHeader(cs)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}

	// Tile 0, either component: no override anywhere, falls back to the
	// codestream-wide default.
	for c := 0; c < 2; c++ {
		got := h.effectiveCoding(0, c)
		if got.DecompositionLevels != def.decompLevels {
			t.Errorf("tile 0 component %d: DecompositionLevels = %d, want %d (codestream default)", c, got.DecompositionLevels, def.decompLevels)
		}
		if qs := h.effectiveQuant(0, c); qs.Style != QuantStyle(def.quantStyle) {
			t.Errorf("tile 0 component %d: quant style = %v, want %v", c, qs.Style, def.quantStyle)
		}
	}

	// Tile 1, component 0: no COC/QCC of its own, so it should pick up
	// the *tile's own* COD/QCD override, not the codestream default.
	got0 := h.effectiveCoding(1, 0)
	if got0.DecompositionLevels != override.decompLevels {
		t.Errorf("tile 1 component 0: DecompositionLevels = %d, want %d (tile override)", got0.DecompositionLevels, override.decompLevels)
	}
	if got0.CodeBlockWidth != 1<<(override.cbWidthExp+2) {
		t.Errorf("tile 1 component 0: CodeBlockWidth = %d, want %d", got0.CodeBlockWidth, 1<<(override.cbWidthExp+2))
	}
	if qs0 := h.effectiveQuant(1, 0); qs0.Style != QuantStyle(override.quantStyle) || qs0.GuardBits != int(override.guardBits) {
		t.Errorf("tile 1 component 0: quant = %+v, want style=%v guardBits=%d", qs0, override.quantStyle, override.guardBits)
	}

	// Tile 1, component 1: has its own COC/QCC, which must win over both
	// the tile's own COD/QCD and the codestream default.
	got1 := h.effectiveCoding(1, 1)
	if got1.DecompositionLevels != componentOverride.decompLevels {
		t.Errorf("tile 1 component 1: DecompositionLevels = %d, want %d (component override)", got1.DecompositionLevels, componentOverride.decompLevels)
	}
	if got1.CodeBlockWidth != 1<<(componentOverride.cbWidthExp+2) {
		t.Errorf("tile 1 component 1: CodeBlockWidth = %d, want %d (component override, not tile's 2+2)", got1.CodeBlockWidth, 1<<(componentOverride.cbWidthExp+2))
	}
	if qs1 := h.effectiveQuant(1, 1); qs1.GuardBits != int(componentOverride.guardBits) {
		t.Errorf("tile 1 component 1: quant guardBits = %d, want %d (component override)", qs1.GuardBits, componentOverride.guardBits)
	}

	// effectiveTileDefaultCoding must reflect tile 1's own COD (its
	// progression order etc.), not the codestream default nor any COC.
	tileDefault := h.effectiveTileDefaultCoding(1)
	if tileDefault.DecompositionLevels != override.decompLevels {
		t.Errorf("effectiveTileDefaultCoding(1) = %+v, want DecompositionLevels=%d", tileDefault, override.decompLevels)
	}
	if d0 := h.effectiveTileDefaultCoding(0); d0.DecompositionLevels != def.decompLevels {
		t.Errorf("effectiveTileDefaultCoding(0) = %+v, want DecompositionLevels=%d", d0, def.decompLevels)
	}

	// And the TilePart records themselves should carry the parsed
	// overrides directly (not just via the Header-level helpers).
	var tp1 *TilePart
	for i := range h.TileParts {
		if h.TileParts[i].TileIndex == 1 {
			tp1 = &h.TileParts[i]
		}
	}
	if tp1 == nil {
		t.Fatalf("no tile-part found for tile 1")
	}
	if tp1.TileCoding == nil {
		t.Fatalf("tile 1's TilePart.TileCoding is nil, want the parsed COD override")
	}
	if tp1.TileComponentCoding == nil || len(tp1.TileComponentCoding) != 1 {
		t.Fatalf("tile 1's TilePart.TileComponentCoding = %+v, want exactly one entry (component 1)", tp1.TileComponentCoding)
	}
}

package jpx

import (
	"bytes"
	"errors"
	"testing"
)

func TestParseHeaderSingleTile(t *testing.T) {
	data := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05}
	cs := buildCodestream(100, 80, 1, 1, defaultTileConfig, data)

	h, err := ParseHeader(cs)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}

	if h.Width != 100 || h.Height != 80 {
		t.Errorf("Width/Height = %d/%d, want 100/80", h.Width, h.Height)
	}
	if h.NumTilesX != 1 || h.NumTilesY != 1 {
		t.Errorf("NumTilesX/Y = %d/%d, want 1/1", h.NumTilesX, h.NumTilesY)
	}
	if len(h.Components) != 1 {
		t.Fatalf("len(Components) = %d, want 1", len(h.Components))
	}
	if c := h.Components[0]; c.BitDepth != 8 || c.Signed {
		t.Errorf("Components[0] = %+v, want BitDepth=8 Signed=false", c)
	}

	dc := h.DefaultCoding
	if dc.DecompositionLevels != 0 || dc.CodeBlockWidth != 64 || dc.CodeBlockHeight != 64 {
		t.Errorf("DefaultCoding = %+v, want decomp=0 64x64 code-blocks", dc)
	}
	if dc.Transform != Transform5x3 {
		t.Errorf("DefaultCoding.Transform = %v, want Transform5x3", dc.Transform)
	}
	if dc.ProgressionOrder != ProgressionLRCP || dc.NumLayers != 1 {
		t.Errorf("DefaultCoding progression/layers = %v/%d, want LRCP/1", dc.ProgressionOrder, dc.NumLayers)
	}

	dq := h.DefaultQuant
	if dq.Style != QuantNone || dq.GuardBits != 2 || len(dq.StepSizes) != 1 {
		t.Errorf("DefaultQuant = %+v, want style=none guardBits=2 1 step size", dq)
	}

	if len(h.TileParts) != 1 {
		t.Fatalf("len(TileParts) = %d, want 1", len(h.TileParts))
	}
	tp := h.TileParts[0]
	if tp.TileIndex != 0 || tp.PartIndex != 0 || tp.PartCount != 1 {
		t.Errorf("TileParts[0] = %+v, want TileIndex=0 PartIndex=0 PartCount=1", tp)
	}
	if tp.DataLength != len(data) {
		t.Fatalf("DataLength = %d, want %d", tp.DataLength, len(data))
	}
	got := cs[tp.DataStart : tp.DataStart+tp.DataLength]
	if !bytes.Equal(got, data) {
		t.Errorf("tile-part data = %x, want %x", got, data)
	}
}

func TestParseHeaderMultiTile(t *testing.T) {
	data := []byte{0xAA, 0xBB, 0xCC}
	cs := buildCodestream(64, 64, 2, 2, defaultTileConfig, data)

	h, err := ParseHeader(cs)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if h.NumTilesX != 2 || h.NumTilesY != 2 {
		t.Fatalf("NumTilesX/Y = %d/%d, want 2/2", h.NumTilesX, h.NumTilesY)
	}
	if len(h.TileParts) != 4 {
		t.Fatalf("len(TileParts) = %d, want 4", len(h.TileParts))
	}
	for i, tp := range h.TileParts {
		if tp.TileIndex != i {
			t.Errorf("TileParts[%d].TileIndex = %d, want %d", i, tp.TileIndex, i)
		}
		got := cs[tp.DataStart : tp.DataStart+tp.DataLength]
		if !bytes.Equal(got, data) {
			t.Errorf("TileParts[%d] data = %x, want %x", i, got, data)
		}
	}
	// Every tile-part's byte range must be disjoint from every other's.
	for i := range h.TileParts {
		for j := range h.TileParts {
			if i == j {
				continue
			}
			a, b := h.TileParts[i], h.TileParts[j]
			if a.DataStart < b.DataStart+b.DataLength && b.DataStart < a.DataStart+a.DataLength {
				t.Errorf("TileParts[%d] and [%d] overlap: %+v / %+v", i, j, a, b)
			}
		}
	}
}

func TestParseHeaderCOCAndQCCOverride(t *testing.T) {
	baseTC := defaultTileConfig
	overrideTC := tileConfig{decompLevels: 1, cbWidthExp: 2, cbHeightExp: 2, transform: 0, quantStyle: 2, guardBits: 1}

	cs := bareMarker(markerSOC)
	cs = append(cs, segment(markerSIZ, codeSIZ(32, 32, 32, 32, 2, 8, false))...)
	cs = append(cs, segment(markerCOD, codeCOD(baseTC, 1))...)
	cs = append(cs, segment(markerCOC, codeCOC(1, overrideTC))...)
	cs = append(cs, segment(markerQCD, codeQCD(baseTC, subbandsFor(baseTC)))...)
	cs = append(cs, segment(markerQCC, codeQCC(1, overrideTC, subbandsFor(overrideTC)))...)
	cs = append(cs, buildTilePart(0, 0, 1, []byte{0x01})...)
	cs = append(cs, bareMarker(markerEOC)...)

	h, err := ParseHeader(cs)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}

	if _, ok := h.ComponentCoding[0]; ok {
		t.Errorf("component 0 has an unexpected COC override")
	}
	cc, ok := h.ComponentCoding[1]
	if !ok {
		t.Fatalf("component 1 has no COC override")
	}
	if cc.DecompositionLevels != 1 || cc.Transform != Transform9x7 {
		t.Errorf("component 1 override = %+v, want decomp=1 Transform9x7", cc)
	}

	qc, ok := h.ComponentQuant[1]
	if !ok {
		t.Fatalf("component 1 has no QCC override")
	}
	if qc.Style != QuantScalarExpounded || qc.GuardBits != 1 {
		t.Errorf("component 1 quant override = %+v, want style=expounded guardBits=1", qc)
	}

	// codingStyleFor/quantStyleFor: overridden component uses the
	// override, every other component falls back to the default.
	if got := h.codingStyleFor(0); got.Transform != baseTC.transformAsWaveletTransform() {
		t.Errorf("codingStyleFor(0).Transform = %v, want the codestream default", got.Transform)
	}
	if got := h.codingStyleFor(1); got.Transform != Transform9x7 {
		t.Errorf("codingStyleFor(1).Transform = %v, want Transform9x7 (overridden)", got.Transform)
	}
}

// transformAsWaveletTransform is a tiny test-only convenience so
// TestParseHeaderCOCAndQCCOverride can compare against defaultTileConfig
// without hand-converting its raw byte field.
func (tc tileConfig) transformAsWaveletTransform() WaveletTransform {
	if tc.transform == 0 {
		return Transform9x7
	}
	return Transform5x3
}

func TestParseHeaderJP2Container(t *testing.T) {
	data := []byte{0x10, 0x20}
	cs := buildCodestream(16, 16, 1, 1, defaultTileConfig, data)
	jp2 := buildJP2File(cs, 16, 16, 1, 7, EnumCSGreyscale)

	h, err := ParseHeader(jp2)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if h.Width != 16 || h.Height != 16 {
		t.Errorf("Width/Height = %d/%d, want 16/16", h.Width, h.Height)
	}
	if len(h.TileParts) != 1 {
		t.Fatalf("len(TileParts) = %d, want 1", len(h.TileParts))
	}
}

func TestParseHeaderMalformed(t *testing.T) {
	validCS := buildCodestream(8, 8, 1, 1, defaultTileConfig, []byte{0x00})

	tests := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"truncated SOC", []byte{0xFF}},
		{"not SOC", []byte{0xFF, 0x00}},
		{"SIZ missing after SOC", append(bareMarker(markerSOC), bareMarker(markerEOC)...)},
		{"truncated codestream", validCS[:len(validCS)-5]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseHeader(tt.data); err == nil {
				t.Errorf("ParseHeader(%q): got nil error, want one", tt.name)
			}
		})
	}
}

func TestParseHeaderRejectsBadCsiz(t *testing.T) {
	cs := bareMarker(markerSOC)
	siz := codeSIZ(8, 8, 8, 8, 1, 8, false)
	// Corrupt Csiz (bytes 34:36 of the SIZ content) to 0, which is
	// outside the valid 1-16384 range.
	siz[34], siz[35] = 0, 0
	cs = append(cs, segment(markerSIZ, siz)...)

	if _, err := ParseHeader(cs); err == nil || !errors.Is(err, ErrMalformed) {
		t.Errorf("ParseHeader with Csiz=0: err = %v, want ErrMalformed", err)
	}
}

func TestParseHeaderRejectsUnsupportedRGN(t *testing.T) {
	cs := bareMarker(markerSOC)
	cs = append(cs, segment(markerSIZ, codeSIZ(8, 8, 8, 8, 1, 8, false))...)
	cs = append(cs, segment(markerCOD, codeCOD(defaultTileConfig, 1))...)
	cs = append(cs, segment(markerQCD, codeQCD(defaultTileConfig, subbandsFor(defaultTileConfig)))...)
	cs = append(cs, segment(markerRGN, []byte{0x00, 0x00, 0x07})...)
	cs = append(cs, buildTilePart(0, 0, 1, []byte{0x00})...)
	cs = append(cs, bareMarker(markerEOC)...)

	_, err := ParseHeader(cs)
	if err == nil || !errors.Is(err, ErrUnsupported) {
		t.Errorf("ParseHeader with RGN: err = %v, want ErrUnsupported", err)
	}
}

func TestParseHeaderPsotZeroScansForNextMarker(t *testing.T) {
	// Build a single-tile-part codestream, then zero out its Psot field
	// so tilePartData must fall back to scanning for the next marker.
	cs := buildCodestream(8, 8, 1, 1, defaultTileConfig, []byte{0x01, 0x02, 0x03})

	sotMarkerOffset := bytes.Index(cs, []byte{0xFF, 0x90})
	if sotMarkerOffset < 0 {
		t.Fatalf("test setup: SOT marker not found")
	}
	psotOffset := sotMarkerOffset + 2 + 2 + 2 // marker + length + Isot
	for i := 0; i < 4; i++ {
		cs[psotOffset+i] = 0
	}

	h, err := ParseHeader(cs)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if len(h.TileParts) != 1 {
		t.Fatalf("len(TileParts) = %d, want 1", len(h.TileParts))
	}
	tp := h.TileParts[0]
	if !tp.LengthUnknown {
		t.Errorf("LengthUnknown = false, want true")
	}
	if tp.DataLength != 3 {
		t.Errorf("DataLength = %d, want 3 (scanned up to EOC)", tp.DataLength)
	}
}

func TestParseHeaderToleratesMissingTrailingEOC(t *testing.T) {
	cs := buildCodestream(8, 8, 1, 1, defaultTileConfig, []byte{0x01})
	cs = cs[:len(cs)-2] // drop the trailing EOC marker

	h, err := ParseHeader(cs)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if len(h.TileParts) != 1 {
		t.Errorf("len(TileParts) = %d, want 1", len(h.TileParts))
	}
}

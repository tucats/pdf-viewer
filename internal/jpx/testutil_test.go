package jpx

// This file has no JPEG 2000 sample files to draw on (see doc.go's
// Provenance section on why this package builds its own fixtures rather
// than relying on real-world ones the way most of this project's other
// filters eventually could), so every test in this package instead
// assembles a codestream by hand, byte by byte, exactly matching the
// marker layouts markers.go/siz.go/coding.go/quant.go/box.go parse. These
// small helpers keep that assembly readable instead of a wall of
// opaque hex literals in every test.

// u16, u32, segment, bareMarker, codeSIZ and buildTilePart (which every
// test in this file also uses) now live in encode.go, promoted there by
// 14g so tools/genfixtures - an ordinary non-test build - can reach them
// too. See that file's own doc comment.

// tileConfig describes one tile's worth of coding/quantization
// parameters for buildCodestream, letting a test build codestreams that
// exercise SPcod/SPqcd variations without duplicating the whole
// SIZ/COD/QCD assembly each time.
type tileConfig struct {
	decompLevels int
	cbWidthExp   int // stored value (real exponent minus 2), 0-8
	cbHeightExp  int
	transform    byte // 0 = 9-7, 1 = 5-3
	quantStyle   byte // 0 = none, 1 = derived, 2 = expounded
	guardBits    byte
}

// defaultTileConfig is deliberately the simplest legal configuration: no
// wavelet decomposition at all (one resolution level, one subband),
// which keeps every other test's byte-counting arithmetic (how many
// SPqcd entries QCD needs, how many precinct-size bytes COD needs) as
// simple as possible unless a test specifically wants to vary it.
var defaultTileConfig = tileConfig{
	decompLevels: 0,
	cbWidthExp:   4, // 2^(4+2) = 64
	cbHeightExp:  4,
	transform:    1, // 5-3 reversible
	quantStyle:   0, // none
	guardBits:    2,
}

// codeCOD builds a COD marker segment's content for tc, with no
// explicit precinct sizes (Scod bit0 clear - see CodingStyle.
// PrecinctsDefined).
func codeCOD(tc tileConfig, numLayers uint16) []byte {
	c := []byte{0x00} // Scod: no precincts, no SOP/EPH
	c = append(c, 0)  // progression order: LRCP
	c = u16(c, numLayers)
	c = append(c, 0) // MCT: off
	c = append(c, byte(tc.decompLevels), byte(tc.cbWidthExp), byte(tc.cbHeightExp), 0, tc.transform)
	return c
}

// codeQCD builds a QCD marker segment's content for tc. subbandCount
// must match 3*tc.decompLevels+1 for quantStyle none/expounded, or be 1
// for quantStyle derived - callers are expected to pass the right value
// (subbandsFor(tc) computes it) rather than this function silently
// coping with a mismatch, since a mismatch would indicate a test bug.
func codeQCD(tc tileConfig, subbandCount int) []byte {
	sqcd := (tc.guardBits << 5) | tc.quantStyle
	c := []byte{sqcd}
	entrySize := 2
	if tc.quantStyle == 0 {
		entrySize = 1
	}
	for i := 0; i < subbandCount; i++ {
		exponent := byte(8 + i%4) // arbitrary but valid (0-31) exponents
		if entrySize == 1 {
			c = append(c, exponent<<3)
		} else {
			c = u16(c, uint16(exponent)<<11)
		}
	}
	return c
}

func subbandsFor(tc tileConfig) int {
	if tc.quantStyle == 1 {
		return 1
	}
	return 3*tc.decompLevels + 1
}

// codeSPcodOnly builds just the SPcod/SPcoc field group (no SGcod, no
// Scod/Scoc byte) - the part COD's and COC's encodings share exactly,
// per parseSPcod's own doc comment - with no explicit precinct sizes.
func codeSPcodOnly(tc tileConfig) []byte {
	return []byte{byte(tc.decompLevels), byte(tc.cbWidthExp), byte(tc.cbHeightExp), 0, tc.transform}
}

// codeCOC builds a COC marker segment's content overriding component
// componentIndex (assumed < 256, so a single-byte component index).
func codeCOC(componentIndex byte, tc tileConfig) []byte {
	c := []byte{componentIndex, 0x00} // Scoc: no explicit precincts
	return append(c, codeSPcodOnly(tc)...)
}

// codeQCC builds a QCC marker segment's content overriding component
// componentIndex (assumed < 256).
func codeQCC(componentIndex byte, tc tileConfig, subbandCount int) []byte {
	c := []byte{componentIndex}
	return append(c, codeQCD(tc, subbandCount)...)
}

// buildCodestream assembles a complete, minimal-but-valid bare
// codestream (SOC through EOC) for one or more tiles, each built from
// dataPerTile (arbitrary placeholder bytes standing in for compressed
// tile data - 14a never interprets tile-part data, only locates it).
func buildCodestream(width, height, numTilesX, numTilesY int, tc tileConfig, dataPerTile []byte) []byte {
	tileW := (width + numTilesX - 1) / numTilesX
	tileH := (height + numTilesY - 1) / numTilesY

	out := bareMarker(markerSOC)
	out = append(out, segment(markerSIZ, codeSIZ(uint32(width), uint32(height), uint32(tileW), uint32(tileH), 1, 8, false))...)
	out = append(out, segment(markerCOD, codeCOD(tc, 1))...)
	out = append(out, segment(markerQCD, codeQCD(tc, subbandsFor(tc)))...)

	idx := 0
	for ty := 0; ty < numTilesY; ty++ {
		for tx := 0; tx < numTilesX; tx++ {
			out = append(out, buildTilePart(uint16(idx), 0, 1, dataPerTile)...)
			idx++
		}
	}
	out = append(out, bareMarker(markerEOC)...)
	return out
}

// jp2Box builds one JP2 box: a 4-byte length, a 4-character tag, and
// content.
func jp2Box(tag string, content []byte) []byte {
	out := u32(nil, uint32(8+len(content)))
	out = append(out, tag...)
	return append(out, content...)
}

// jp2SuperBox builds a box whose content is itself a sequence of
// already-built child boxes (used for "jp2h").
func jp2SuperBox(tag string, children ...[]byte) []byte {
	var content []byte
	for _, c := range children {
		content = append(content, c...)
	}
	return jp2Box(tag, content)
}

// buildIhdr builds an Image Header ("ihdr") box's content.
func buildIhdr(height, width uint32, numComponents int, bpc byte) []byte {
	c := u32(nil, height)
	c = u32(c, width)
	c = u16(c, uint16(numComponents))
	c = append(c, bpc, 7, 0, 0) // BPC, C=7 (JP2), UnkC=0, IPR=0
	return c
}

// buildColrEnumerated builds a Colour Specification ("colr") box's
// content for an enumerated color space (METH=1).
func buildColrEnumerated(enumCS uint32) []byte {
	c := []byte{1, 0, 0} // METH=1, PREC=0, APPROX=0
	return u32(c, enumCS)
}

// buildJP2File wraps codestream in a minimal-but-complete JP2 container:
// signature box, a bare-minimum "ftyp" box, "jp2h" (with "ihdr" and
// "colr"), and "jp2c".
func buildJP2File(codestream []byte, height, width uint32, numComponents int, bpc byte, enumCS uint32) []byte {
	var out []byte
	out = append(out, jp2SignatureBox[:]...)
	out = append(out, jp2Box("ftyp", []byte("jp2 \x00\x00\x00\x00jp2 "))...)
	out = append(out, jp2SuperBox("jp2h",
		jp2Box("ihdr", buildIhdr(height, width, numComponents, bpc)),
		jp2Box("colr", buildColrEnumerated(enumCS)),
	)...)
	out = append(out, jp2Box("jp2c", codestream)...)
	return out
}

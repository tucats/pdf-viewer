package jpx

import (
	"math/rand"
	"testing"
)

func TestFinalizeSampleUnsignedLevelShift(t *testing.T) {
	// Unsigned bitDepth 8: the encoder centred samples around zero by
	// subtracting 128, so a zero-centred v of -128 (the darkest possible
	// sample) must come back as 0, and +127 (the brightest) as 255... 254
	// really, since [-128,127] is the coded range - check the midpoint too.
	cases := []struct {
		v    float64
		want int32
	}{
		{-128, 0},
		{0, 128},
		{127, 255},
	}
	for _, c := range cases {
		if got := finalizeSample(c.v, 8, false); got != c.want {
			t.Errorf("finalizeSample(%v, 8, unsigned): got %d, want %d", c.v, got, c.want)
		}
	}
}

func TestFinalizeSampleSignedNoLevelShift(t *testing.T) {
	cases := []struct {
		v    float64
		want int32
	}{
		{-128, -128},
		{0, 0},
		{127, 127},
	}
	for _, c := range cases {
		if got := finalizeSample(c.v, 8, true); got != c.want {
			t.Errorf("finalizeSample(%v, 8, signed): got %d, want %d", c.v, got, c.want)
		}
	}
}

func TestFinalizeSampleRounds(t *testing.T) {
	if got := finalizeSample(0.5, 8, true); got != 1 {
		t.Errorf("got %d, want 1 (round half away from zero via math.Round)", got)
	}
	if got := finalizeSample(-0.5, 8, true); got != -1 {
		t.Errorf("got %d, want -1", got)
	}
}

func TestFinalizeSampleClamps(t *testing.T) {
	// A corrupt/hostile codestream's dequantized value can fall outside
	// its own declared bit depth's range; finalizeSample must clamp
	// rather than wrap or panic.
	if got := finalizeSample(1000, 8, false); got != 255 {
		t.Errorf("unsigned overflow: got %d, want 255", got)
	}
	if got := finalizeSample(-1000, 8, false); got != 0 {
		t.Errorf("unsigned underflow: got %d, want 0", got)
	}
	if got := finalizeSample(1000, 8, true); got != 127 {
		t.Errorf("signed overflow: got %d, want 127", got)
	}
	if got := finalizeSample(-1000, 8, true); got != -128 {
		t.Errorf("signed underflow: got %d, want -128", got)
	}
}

func TestNewImageRejectsExcessiveBitDepth(t *testing.T) {
	h := &Header{Width: 1, Height: 1, Components: []ComponentInfo{{BitDepth: maxSupportedBitDepth + 1}}}
	if _, err := newImage(h); err == nil {
		t.Fatal("expected an error for a bit depth beyond this package's supported range, got nil")
	}
}

func TestCompositeTileHonorsImageAreaOffset(t *testing.T) {
	// A tile-component's left/top (tileComponentBounds) are absolute
	// reference-grid coordinates; compositeTile must subtract the image
	// area's own origin (XOsiz/YOsiz) back out, not assume it is zero.
	h := &Header{
		Width: 2, Height: 2, XOsiz: 3, YOsiz: 5,
		Components: []ComponentInfo{{BitDepth: 8, Signed: true}},
	}
	img, err := newImage(h)
	if err != nil {
		t.Fatalf("newImage: %v", err)
	}
	rc := &reconstructedComponent{left: 3, top: 5, width: 2, height: 2, items: []float64{1, 2, 3, 4}}
	compositeTile(h, img, []*reconstructedComponent{rc})

	want := []int32{1, 2, 3, 4}
	for i, w := range want {
		if got := img.Components[0].Samples[i]; got != w {
			t.Errorf("sample %d: got %d, want %d", i, got, w)
		}
	}
}

// buildTier1LLComponent builds a componentDecode whose single (decomp
// level 0, LL-only) resolution's code-block is real tier1-encoded data
// (encodeCodeBlockTier1/decodeCodeBlockTier1 - 14c) for values, bypassing
// tier-2 packet parsing (14b) exactly as idwt_test.go's
// TestReconstructTileComponentTier1RoundTrip does; reversible transform
// and QuantNone with an exponent chosen so dequantizeComponent's mb
// exactly matches numBitPlanes (no scaling, no truncation), same
// reasoning that test's own comment gives.
func buildTier1LLComponent(t *testing.T, size, bitDepth, numBitPlanes int, values []float64) *componentDecode {
	t.Helper()
	magnitude := make([]uint32, len(values))
	sign := make([]uint8, len(values))
	for i, v := range values {
		iv := int64(v)
		if iv < 0 {
			sign[i] = 1
			iv = -iv
		}
		magnitude[i] = uint32(iv)
	}
	data, numPasses := encodeCodeBlockTier1(size, size, magnitude, sign, subbandLL, numBitPlanes, 0)
	cb := &codeBlockInfo{
		tbx0: 0, tby0: 0, tbx1: size, tby1: size,
		contributions: []codeBlockContribution{{numPasses: numPasses, data: data}},
	}
	samples, err := decodeCodeBlockTier1(cb, size, size, subbandLL, 0)
	if err != nil {
		t.Fatalf("decodeCodeBlockTier1: %v", err)
	}
	cb.samples = samples

	sb := &subbandInfo{kind: subbandLL, tbx0: 0, tby0: 0, tbx1: size, tby1: size, codeBlocks: []*codeBlockInfo{cb}}
	res := &resolutionInfo{level: 0, trx0: 0, try0: 0, trx1: size, try1: size, subbands: []*subbandInfo{sb}}
	return &componentDecode{
		coding:      CodingStyle{Transform: Transform5x3, DecompositionLevels: 0},
		quant:       QuantizationStyle{Style: QuantNone, GuardBits: 0, StepSizes: []QuantStep{{Exponent: numBitPlanes + 1}}},
		bitDepth:    bitDepth,
		resolutions: []*resolutionInfo{res},
	}
}

// TestMCTAndLevelShiftEndToEnd chains real 14c/14d decoding
// (buildTier1LLComponent) into mct.go's inverse RCT and this file's DC
// level shifting/clamping/compositing, over three components built from
// deliberately forward-transformed (level-shifted, then RCT'd) ground
// truth - i.e. exactly what a real encoder would have produced upstream
// of tier-1 coding - and checks the original R/G/B pixel values come
// back out exactly. Unlike idwt_test.go's own tier-1 round trip (a
// single component, no colour transform), this is 14e's own new logic:
// mct.go's inverseRCT and image.go's finalizeSample/compositeTile,
// exercised together against genuinely tier-1-decoded (not hand-injected)
// coefficients.
func TestMCTAndLevelShiftEndToEnd(t *testing.T) {
	const size = 4
	const bitDepth = 8
	const numBitPlanes = 8 // covers Y/Cb/Cr's worst-case +-255 range

	rng := rand.New(rand.NewSource(4))
	r := make([]float64, size*size)
	g := make([]float64, size*size)
	b := make([]float64, size*size)
	for i := range r {
		r[i] = float64(rng.Intn(256))
		g[i] = float64(rng.Intn(256))
		b[i] = float64(rng.Intn(256))
	}

	// Ground truth an encoder would feed tier-1: DC-level-shift each
	// (unsigned, bitDepth 8: subtract 128) then forward-RCT - see
	// forwardRCT's own doc comment (mct_test.go). No wavelet transform to
	// undo here since DecompositionLevels 0 means the LL subband's
	// dequantized samples already are the final tile-component values
	// (idwt.go's reconstructTileComponent).
	y := make([]float64, len(r))
	cb := make([]float64, len(r))
	cr := make([]float64, len(r))
	for i := range r {
		y[i], cb[i], cr[i] = r[i]-128, g[i]-128, b[i]-128
	}
	forwardRCT(y, cb, cr)

	comps := []*componentDecode{
		buildTier1LLComponent(t, size, bitDepth, numBitPlanes, y),
		buildTier1LLComponent(t, size, bitDepth, numBitPlanes, cb),
		buildTier1LLComponent(t, size, bitDepth, numBitPlanes, cr),
	}
	reconstructed, err := reconstructTile(comps)
	if err != nil {
		t.Fatalf("reconstructTile: %v", err)
	}

	h := &Header{
		Width: size, Height: size,
		Components: []ComponentInfo{
			{BitDepth: bitDepth, Signed: false},
			{BitDepth: bitDepth, Signed: false},
			{BitDepth: bitDepth, Signed: false},
		},
		DefaultCoding: CodingStyle{MultipleComponentTransform: true, Transform: Transform5x3},
	}
	if err := applyMultipleComponentTransform(h, 0, reconstructed); err != nil {
		t.Fatalf("applyMultipleComponentTransform: %v", err)
	}

	img, err := newImage(h)
	if err != nil {
		t.Fatalf("newImage: %v", err)
	}
	compositeTile(h, img, reconstructed)

	check := func(name string, got []int32, want []float64) {
		for i, w := range want {
			if got[i] != int32(w) {
				t.Errorf("%s sample %d: got %d, want %v", name, i, got[i], w)
			}
		}
	}
	check("R", img.Components[0].Samples, r)
	check("G", img.Components[1].Samples, g)
	check("B", img.Components[2].Samples, b)
}

// TestDecodeSingleTileGrayscale exercises Decode's own marker-level
// wiring (ParseHeader through the tile loop, packet.go/tier1.go's real
// tier-2/tier-1 decoding, and this file's DC level shifting) for the
// simplest real codestream: one tile, one grayscale (no MCT) component,
// one code-block covering the whole tile. TestMCTAndLevelShiftEndToEnd
// above covers mct.go/image.go's own logic in more depth by building
// componentDecode geometry directly; this test instead checks that a
// byte-accurate codestream survives the whole public Decode entry point.
func TestDecodeSingleTileGrayscale(t *testing.T) {
	const size = 4
	const bitDepth = 8
	const numBitPlanes = 8

	rng := rand.New(rand.NewSource(5))
	want := make([]float64, size*size)
	for i := range want {
		want[i] = float64(rng.Intn(256))
	}
	shifted := make([]float64, len(want))
	for i, v := range want {
		shifted[i] = v - 128
	}
	magnitude := make([]uint32, len(shifted))
	sign := make([]uint8, len(shifted))
	for i, v := range shifted {
		iv := int64(v)
		if iv < 0 {
			sign[i] = 1
			iv = -iv
		}
		magnitude[i] = uint32(iv)
	}
	data, numPasses := encodeCodeBlockTier1(size, size, magnitude, sign, subbandLL, numBitPlanes, 0)

	tc := tileConfig{decompLevels: 0, cbWidthExp: 2, cbHeightExp: 2, transform: 1, quantStyle: 0, guardBits: 0} // 16x16 code-block, covers the whole 4x4 tile
	cs := CodingStyle{
		ProgressionOrder: ProgressionLRCP, NumLayers: 1,
		DecompositionLevels: 0, CodeBlockWidth: 16, CodeBlockHeight: 16,
		Transform:               Transform5x3,
		PrecinctWidthExponents:  []int{15},
		PrecinctHeightExponents: []int{15},
	}
	components := []*componentDecode{{coding: cs, resolutions: buildResolutions(0, 0, size, size, cs)}}
	truth := map[[2]int]*codeBlockTruth{
		{0, 0}: {includedAtLayer: []bool{true}, numPasses: []int{numPasses}, data: [][]byte{data}, zeroBitPlanes: 0},
	}
	tilePartBody := encodeSyntheticTile(t, cs, components, truth)

	// QCD, quantStyle none, single LL subband: Sqcd = guardBits(0)<<5 |
	// style(0), one SPqcd byte whose exponent (top 5 bits) matches
	// numBitPlanes+1 so dequantizeComponent's mb exactly equals
	// numBitPlanes (buildTier1LLComponent's own doc comment explains why).
	qcd := []byte{0, byte(numBitPlanes+1) << 3}

	codestream := bareMarker(markerSOC)
	codestream = append(codestream, segment(markerSIZ, codeSIZ(size, size, size, size, 1, bitDepth, false))...)
	codestream = append(codestream, segment(markerCOD, codeCODCustom(ProgressionLRCP, 1, tc, false, false, nil))...)
	codestream = append(codestream, segment(markerQCD, qcd)...)
	codestream = append(codestream, buildTilePart(0, 0, 1, tilePartBody)...)
	codestream = append(codestream, bareMarker(markerEOC)...)

	h, err := ParseHeader(codestream)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	img, err := Decode(h, codestream)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if img.Width != size || img.Height != size || len(img.Components) != 1 {
		t.Fatalf("unexpected image shape: %dx%d, %d component(s)", img.Width, img.Height, len(img.Components))
	}
	for i, w := range want {
		if got := img.Components[0].Samples[i]; got != int32(w) {
			t.Errorf("sample %d: got %d, want %v", i, got, w)
		}
	}
}

package jpx

// This file is 14f's one-call convenience entry point: while ParseHeader
// (markers.go) and Decode (image.go) are this package's two lower-level
// entry points - deliberately kept separate since a caller that only
// needs geometry (14a's original motivation) should not be forced to
// also pay for a full pixel decode - DecodeStream below is what
// internal/filter's adapter actually uses: unwrap any JP2 container,
// parse the header, and decode every tile, all in one step, additionally
// reporting whatever color space information only a JP2 container (never
// a bare codestream) carries - see ColorInfo.

// ColorInfo is whatever color space information a JP2-boxed file's own
// "colr" box declares - an exported mirror of box.go's own (unexported)
// containerInfo, trimmed to the fields a caller outside this package
// needs. Present is false for a bare codestream (no JP2 wrapper at all);
// every other field is meaningless when Present is false.
type ColorInfo struct {
	// Present is true only when data (DecodeStream's argument) was a
	// JP2-boxed file, i.e. looksLikeJP2(data) - a bare codestream carries
	// no color space information of its own for this package to report.
	Present bool

	// Method is the "colr" box's own METH field: 1 means
	// EnumeratedColorSpace is meaningful, 2 means ICCProfile is
	// meaningful (a restricted-form embedded ICC profile). Method 3/4
	// (Part 2's "any ICC profile" / vendor color space) are recorded here
	// too, but this package does not extract their payload - see
	// box.go's containerInfo.colorSpaceMethod doc comment.
	Method int
	// EnumeratedColorSpace is one of the EnumCS* constants (box.go) when
	// Method is 1.
	EnumeratedColorSpace int
	// ICCProfile is the raw embedded ICC profile bytes, present only
	// when Method is 2.
	ICCProfile []byte
}

// DecodeStream runs this package's whole pipeline over data - a bare
// JPEG 2000 codestream or a JP2-boxed file, exactly like ParseHeader
// accepts (see that function's doc comment) - parsing its header and
// decoding every tile into one final Image, per Decode's own doc
// comment. It additionally reports any JP2 container color space
// information found along the way, which ParseHeader's own contract
// deliberately does not surface (see ParseHeader's doc comment) since a
// caller only wanting geometry has no use for it - internal/filter's
// adapter uses this to implement JPXDecode's own /ColorSpace fallback
// (ISO 32000-1 7.4.9) when a PDF image dictionary omits /ColorSpace.
func DecodeStream(data []byte) (*Image, ColorInfo, error) {
	codestream, colorInfo, err := unwrapContainer(data)
	if err != nil {
		return nil, ColorInfo{}, err
	}
	h, err := parseCodestream(codestream)
	if err != nil {
		return nil, ColorInfo{}, err
	}
	img, err := Decode(h, codestream)
	if err != nil {
		return nil, ColorInfo{}, err
	}
	return img, colorInfo, nil
}

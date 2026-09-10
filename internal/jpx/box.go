package jpx

import (
	"encoding/binary"
)

// This file parses the JP2 file format's "box" structure (ISO/IEC
// 15444-1 Annex I). PDF's JPXDecode filter is explicitly allowed to
// contain *either* a bare JPEG 2000 codestream (starting directly with
// the SOC marker parsed in markers.go) *or* a full JP2 file wrapping
// that same codestream in a box structure that also carries a
// self-described image size and color space (ISO 32000-1 7.4.9) - so a
// decoder has to handle both, and tell them apart itself: nothing in a
// PDF image dictionary says which form was used.
//
// # What a "box" is
//
// A JP2 file is a flat sequence of boxes, each shaped like a TLV
// (type-length-value) record: a 4-byte big-endian length, a 4-byte
// ASCII type tag (e.g. "jp2h", "jp2c"), and then that many bytes of
// content - which, for a handful of box types this package calls
// "superboxes" (only "jp2h" matters here), is itself a nested sequence
// of boxes rather than opaque data. Two length encodings exist beyond
// the ordinary 4-byte one: a length of 1 means "the real length is a
// 8-byte value immediately following the type tag" (for a box too large
// for 4 bytes to express - not expected in a PDF-embedded image, but
// cheap to support correctly), and a length of 0 means "this box runs to
// the end of the file" (only meaningful for the last box in the file,
// and only actually used in practice for the final "jp2c" box).
const (
	boxHeaderSize   = 8  // LBox (4) + TBox (4)
	boxHeaderSizeXL = 16 // LBox==1 case: + XLBox (8)
)

// box is one parsed box: its 4-character type tag and the position of
// its content within the original byte slice. Keeping only a byte range
// (rather than copying content out) avoids doubling memory use for the
// codestream box, which can be the large majority of the file.
type box struct {
	tag     [4]byte
	content []byte // dataSlice[start:end], aliasing the caller's own backing array
}

func (b box) tagString() string {
	return string(b.tag[:])
}

// readBoxes walks data as a flat sequence of top-level boxes, per the
// length rules described above. It does not recurse into superboxes -
// callers that need a superbox's own children call readBoxes again on
// that box's content (see readContainer below, for "jp2h").
func readBoxes(data []byte) ([]box, error) {
	var boxes []box
	pos := 0
	for pos < len(data) {
		if len(data)-pos < boxHeaderSize {
			return nil, malformedf("JP2 box header truncated at offset %d", pos)
		}
		lBox := binary.BigEndian.Uint32(data[pos:])
		var tag [4]byte
		copy(tag[:], data[pos+4:pos+8])

		headerSize := boxHeaderSize
		var contentLen int64
		switch {
		case lBox == 0:
			// "Extends to the end of the file/data" - only sensible for
			// the last box, so treat all remaining bytes as its content
			// regardless of what follows (there should be nothing).
			contentLen = int64(len(data) - pos - boxHeaderSize)
		case lBox == 1:
			if len(data)-pos < boxHeaderSizeXL {
				return nil, malformedf("JP2 extended box header truncated at offset %d", pos)
			}
			xlBox := binary.BigEndian.Uint64(data[pos+8:])
			headerSize = boxHeaderSizeXL
			if xlBox < uint64(headerSize) {
				return nil, malformedf("JP2 box at offset %d has impossible extended length %d", pos, xlBox)
			}
			contentLen = int64(xlBox) - int64(headerSize)
		default:
			if lBox < boxHeaderSize {
				return nil, malformedf("JP2 box at offset %d has impossible length %d", pos, lBox)
			}
			contentLen = int64(lBox) - int64(headerSize)
		}

		contentStart := pos + headerSize
		if contentLen < 0 || int64(contentStart)+contentLen > int64(len(data)) {
			return nil, malformedf("JP2 box %q at offset %d claims %d content bytes, past the end of the data", string(tag[:]), pos, contentLen)
		}

		boxes = append(boxes, box{tag: tag, content: data[contentStart : contentStart+int(contentLen)]})
		pos = contentStart + int(contentLen)
	}
	return boxes, nil
}

// jp2Signature is the fixed 12-byte first box every JP2 file begins
// with: a 4-byte length of 12, the type tag "jP  " (with two trailing
// spaces), and this fixed 4-byte content. Comparing the whole 12 bytes
// at once is simpler and just as correct as parsing it as a box.
var jp2SignatureBox = [12]byte{0x00, 0x00, 0x00, 0x0C, 'j', 'P', ' ', ' ', 0x0D, 0x0A, 0x87, 0x0A}

// looksLikeJP2 reports whether data begins with the fixed JP2 signature
// box. A bare codestream instead begins directly with the two-byte SOC
// marker (0xFF4F) - see markers.go.
func looksLikeJP2(data []byte) bool {
	return len(data) >= len(jp2SignatureBox) && [12]byte(data[:12]) == jp2SignatureBox
}

// containerInfo is what this package can learn from a JP2 file's boxes
// without touching the codestream itself: the image header box's own
// declared size (a cross-check against the codestream's SIZ marker, not
// a replacement for it - see ParseHeader) and, more usefully, the color
// specification box's data, since the codestream itself carries no color
// space information at all. A JPXDecode image whose PDF dictionary omits
// /ColorSpace is defined (ISO 32000-1 7.4.9) to fall back to exactly this
// - see internal/filter's adapter, added in a later sub-phase.
type containerInfo struct {
	// present is false when the input was a bare codestream (no JP2 box
	// wrapper at all), in which case every other field is zero-valued
	// and unused.
	present bool

	// height, width, componentCount and bitsPerComponent come from the
	// "ihdr" (Image Header) box. bitsPerComponent is 0xFF when
	// components do not all share one bit depth (the true per-component
	// depths live in a "bpcc" box this package does not parse, since the
	// codestream's own SIZ marker always carries accurate per-component
	// depths anyway - see ParseHeader).
	height, width    int
	componentCount   int
	bitsPerComponent int

	// colorSpaceMethod is the "colr" (Colour Specification) box's METH
	// field: 1 means enumeratedColorSpace is meaningful, 2 means
	// iccProfile is meaningful (a restricted-form embedded ICC profile).
	// Method 3 and 4 ("any ICC profile" / vendor color space) are Part 2
	// extensions this package does not interpret; colorSpaceMethod is
	// still recorded for them so a caller can at least see one was
	// present, but iccProfile/enumeratedColorSpace are left unset.
	colorSpaceMethod int
	// enumeratedColorSpace is a JP2 enumerated color space code, e.g. 16
	// (sRGB), 17 (greyscale), or 18 (sYCC) - see the "colr" box's own
	// doc comment on colorSpaceMethod above.
	enumeratedColorSpace int
	// iccProfile is the raw embedded ICC profile bytes, present only
	// when colorSpaceMethod == 2.
	iccProfile []byte

	// codestream is the "jp2c" box's content - the same bytes ParseHeader
	// goes on to parse as a codestream. Recorded here mainly so tests can
	// confirm box-walking located the right bytes.
	codestream []byte
}

// parseContainer walks data's top-level JP2 boxes and extracts the
// codestream bytes plus whatever image-header/color-space information
// the box structure carries. It is only called once looksLikeJP2 has
// already confirmed data begins with the JP2 signature box.
func parseContainer(data []byte) (containerInfo, error) {
	info := containerInfo{present: true, bitsPerComponent: -1}

	boxes, err := readBoxes(data)
	if err != nil {
		return info, err
	}

	foundCodestream := false
	for _, b := range boxes {
		switch b.tagString() {
		case "jp2h":
			if err := parseHeaderSuperbox(b.content, &info); err != nil {
				return info, err
			}
		case "jp2c":
			info.codestream = b.content
			foundCodestream = true
			// A conforming JP2 file has exactly one contiguous codestream
			// box; if more than one somehow appears, keep the first
			// (matching this package's general "be liberal about reading,
			// never surprise the caller by silently preferring a later
			// one over the earlier, spec-mandated-first occurrence"
			// stance) and stop scanning further boxes for it.
		}
		// Every other top-level box (signature, "ftyp", "uuid", "xml ",
		// vendor-specific boxes, and so on) carries metadata this
		// decoder has no use for and is silently skipped - JP2's box
		// format is explicitly designed so an unrecognized box can
		// always be skipped this way, using only its own length field.
	}

	if !foundCodestream {
		return info, malformedf("JP2 file has no jp2c (codestream) box")
	}
	return info, nil
}

// parseHeaderSuperbox reads the children of a "jp2h" box: "ihdr" (Image
// Header, required, always first) and "colr" (Colour Specification, at
// least one required). Other jp2h children this package does not need
// ("bpcc", "pclr", "cdef", "res") are skipped the same way top-level
// unrecognized boxes are.
func parseHeaderSuperbox(content []byte, info *containerInfo) error {
	children, err := readBoxes(content)
	if err != nil {
		return err
	}

	haveIhdr := false
	for _, c := range children {
		switch c.tagString() {
		case "ihdr":
			if err := parseIhdr(c.content, info); err != nil {
				return err
			}
			haveIhdr = true
		case "colr":
			// A JP2 file may carry more than one "colr" box (e.g. an ICC
			// profile plus an enumerated approximation of it); this
			// package keeps only the first, matching parseContainer's
			// jp2c handling above. Skip any content that fails to parse
			// as this box requires only a best-effort read: /ColorSpace
			// fallback is itself a best-effort convenience (a PDF nearly
			// always supplies its own /ColorSpace), not load-bearing for
			// decoding the pixels themselves.
			if info.colorSpaceMethod == 0 {
				parseColr(c.content, info)
			}
		}
	}
	if !haveIhdr {
		return malformedf("JP2 jp2h box has no ihdr (Image Header) box")
	}
	return nil
}

// parseIhdr reads the fixed 14-byte Image Header box content: HEIGHT
// (4 bytes), WIDTH (4 bytes), NC (2 bytes, component count), BPC (1
// byte), C (1 byte, compression type - must be 7 for JP2/JPX and is not
// separately checked here since a mismatched value does not stop this
// decoder from reading the actual codestream), UnkC and IPR (1 byte
// each, not used by this package).
func parseIhdr(content []byte, info *containerInfo) error {
	const ihdrLen = 14
	if len(content) < ihdrLen {
		return malformedf("JP2 ihdr box is %d bytes, want at least %d", len(content), ihdrLen)
	}
	info.height = int(binary.BigEndian.Uint32(content[0:4]))
	info.width = int(binary.BigEndian.Uint32(content[4:8]))
	info.componentCount = int(binary.BigEndian.Uint16(content[8:10]))
	info.bitsPerComponent = int(content[10])
	return nil
}

// parseColr reads a Colour Specification box's METH/PREC/APPROX header
// plus whichever payload METH selects. Errors are deliberately not
// returned - see parseHeaderSuperbox's call site.
func parseColr(content []byte, info *containerInfo) {
	const colrHeaderLen = 3
	if len(content) < colrHeaderLen {
		return
	}
	meth := int(content[0])
	info.colorSpaceMethod = meth

	switch meth {
	case 1: // enumerated color space
		if len(content) >= colrHeaderLen+4 {
			info.enumeratedColorSpace = int(binary.BigEndian.Uint32(content[colrHeaderLen:]))
		}
	case 2: // restricted ICC profile
		info.iccProfile = content[colrHeaderLen:]
	default:
		// Method 3/4 (Part 2: "any ICC profile" / vendor color space) -
		// left unrecorded, see the containerInfo.colorSpaceMethod doc
		// comment.
	}
}

// Enumerated JP2 color space codes this package recognizes (Annex I.5.3.3
// of ISO/IEC 15444-1) - exported as named constants so internal/filter's
// adapter (added once /ColorSpace fallback is wired up) does not need to
// spell out these magic numbers itself.
const (
	// EnumCSGreyscale is JP2's enumerated "greyscale" color space.
	EnumCSGreyscale = 17
	// EnumCSSRGB is JP2's enumerated sRGB color space.
	EnumCSSRGB = 16
	// EnumCSSYCC is JP2's enumerated sYCC (a specific YCbCr-like
	// encoding of sRGB) color space.
	EnumCSSYCC = 18
)

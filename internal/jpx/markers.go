package jpx

import "encoding/binary"

// This file is this sub-phase's top-level entry point: ParseHeader walks
// a JPEG 2000 codestream's markers from the very beginning through to
// its last tile-part, building a Header (siz.go) that describes the
// image's geometry and coding parameters and a list of every tile-part's
// byte range (TilePart, below) - everything later decoding sub-phases
// (14b onward) need to know *where* the compressed data is, without this
// file itself decoding a single sample.
//
// # Markers and marker segments
//
// Outside of the compressed tile-part data itself, a JPEG 2000
// codestream is a sequence of two-byte, big-endian "markers" (always
// 0xFFxx, with xx never 0x00 or 0x30-0x3F - a range the standard
// reserves so a marker byte can never be confused with a compressed
// data byte inside packet data - see the tilePartData function's own
// doc comment on why that matters). Most markers are immediately
// followed by a two-byte length field and that many bytes of
// marker-specific content; this combination is a "marker segment". A
// handful of markers - SOC, SOD, EOC, and (inside packet data, not
// relevant to this file) EPH - stand alone with no length field or
// content at all.
const (
	markerSOC = 0xFF4F // Start Of Codestream
	markerSIZ = 0xFF51 // Image and tile size
	markerCOD = 0xFF52 // Coding style Default
	markerCOC = 0xFF53 // Coding style Component
	markerTLM = 0xFF55 // Tile-part Lengths, Main header
	markerPLM = 0xFF57 // Packet Length, Main header
	markerPLT = 0xFF58 // Packet Length, Tile-part header
	markerQCD = 0xFF5C // Quantization Default
	markerQCC = 0xFF5D // Quantization Component
	markerRGN = 0xFF5E // Region of interest
	markerPOC = 0xFF5F // Progression Order Change
	markerPPM = 0xFF60 // Packed Packet headers, Main header
	markerPPT = 0xFF61 // Packed Packet headers, Tile-part header
	markerCOM = 0xFF64 // Comment
	markerSOT = 0xFF90 // Start Of Tile-part
	markerSOD = 0xFF93 // Start Of Data
	markerEOC = 0xFFD9 // End Of Codestream
)

// TilePart records where one tile-part's header markers and compressed
// data live within the codestream byte slice ParseHeader was given, and
// the bookkeeping fields (TileIndex, PartIndex, PartCount) a later
// decoding sub-phase needs to know how many tile-parts make up each
// tile and in what order to apply them - a tile is allowed to be split
// across more than one tile-part (e.g. so an encoder can interleave
// tiles for progressive transmission), and nothing about a tile-part's
// position in the codestream guarantees its TileIndex matches its
// position in TilePart (tile 3's parts can appear before tile 1's).
type TilePart struct {
	TileIndex int // Isot: which tile this is a part of
	PartIndex int // TPsot: this part's index within that tile (0-based)
	// PartCount is TNsot: how many tile-parts make up this tile, or 0 if
	// the codestream does not say (rare; a later decoding sub-phase must
	// then discover the count by observing how many parts with this
	// TileIndex actually appear before EOC).
	PartCount int

	// DataStart, DataLength give this tile-part's compressed data - the
	// bytes strictly after its SOD marker - as a byte range within the
	// slice ParseHeader was given. HeaderStart, HeaderEnd similarly
	// bound this tile-part's own header (from its SOT marker through its
	// SOD marker inclusive), which a later sub-phase needs to re-walk to
	// pick up any COD/COC/QCD/QCC/RGN overrides scoped to this tile
	// alone - see parseTilePartHeader's doc comment on why 14a does not
	// already apply them.
	HeaderStart, HeaderEnd int
	DataStart, DataLength  int
	// LengthUnknown is true when this tile-part's SOT declared Psot=0
	// ("extends to the next SOT or EOC, length not given directly") -
	// see tilePartData's doc comment. DataLength is still filled in
	// (computed by scanning forward for the next marker), but a caller
	// that cares about the distinction can check this flag.
	LengthUnknown bool

	// TileCoding and TileComponentCoding, TileQuant and
	// TileComponentQuant mirror Header's own DefaultCoding/
	// ComponentCoding and DefaultQuant/ComponentQuant, but scoped to
	// this one tile-part's own COD/COC/QCD/QCC marker segments (ISO/IEC
	// 15444-1 A.4.2 permits a non-first tile-part to repeat any of these
	// to override the main header's values for that tile alone). nil/
	// unset fields mean this tile-part carried no such override; see
	// Header.effectiveCoding and Header.effectiveQuant, which combine
	// every tile-part sharing a TileIndex with the codestream-wide
	// defaults to get one tile's actual effective coding/quantization
	// parameters - the two functions 14b's packet parsing (packet.go)
	// uses instead of ever reading DefaultCoding/DefaultQuant directly.
	TileCoding          *CodingStyle
	TileComponentCoding map[int]CodingStyle
	TileQuant           *QuantizationStyle
	TileComponentQuant  map[int]QuantizationStyle
}

// Data returns this tile-part's compressed data as a byte range within
// codestream, which must be the same byte slice (the bare codestream
// ParseHeader was ultimately given, after unwrapping any JP2 container -
// see ParseHeader's own doc comment) that produced the Header this
// TilePart came from; DataStart/DataLength are meaningless against any
// other slice.
func (tp *TilePart) Data(codestream []byte) []byte {
	return codestream[tp.DataStart : tp.DataStart+tp.DataLength]
}

// ParseHeader parses data (either a bare JPEG 2000 codestream, or a JP2
// file wrapping one - see box.go) far enough to describe the whole
// image's geometry and coding parameters and to locate every tile-part's
// byte range, without decoding any compressed sample data.
func ParseHeader(data []byte) (*Header, error) {
	codestream := data
	if looksLikeJP2(data) {
		info, err := parseContainer(data)
		if err != nil {
			return nil, err
		}
		codestream = info.codestream
		// info's ihdr/colr fields (image size cross-check, embedded
		// color space) are consumed by internal/filter's adapter once
		// it exists (14f) - ParseHeader's own contract is codestream
		// geometry via SIZ, which is authoritative regardless of what a
		// wrapping JP2 file's ihdr box happens to also claim.
	}
	return parseCodestream(codestream)
}

// parseCodestream implements ParseHeader once any JP2 container wrapper
// has already been stripped away, leaving a bare codestream starting
// with SOC.
func parseCodestream(data []byte) (*Header, error) {
	pos := 0

	marker, pos, err := readMarker(data, pos)
	if err != nil {
		return nil, err
	}
	if marker != markerSOC {
		return nil, malformedf("codestream does not begin with SOC (found marker 0x%04X)", marker)
	}

	marker, content, pos, err := readMarkerSegment(data, pos)
	if err != nil {
		return nil, err
	}
	if marker != markerSIZ {
		return nil, malformedf("codestream's first marker segment after SOC is 0x%04X, want SIZ (0xFF51)", marker)
	}
	h, err := parseSIZ(content)
	if err != nil {
		return nil, err
	}
	h.ComponentCoding = make(map[int]CodingStyle)
	h.ComponentQuant = make(map[int]QuantizationStyle)

	haveCOD, haveQCD := false, false
	// Read every remaining main-header marker segment until SOT (the
	// first tile-part) is reached.
	for {
		if pos+2 > len(data) {
			return nil, malformedf("codestream main header ends before any SOT (Start Of Tile-part) marker")
		}
		marker, err = peekMarker(data, pos)
		if err != nil {
			return nil, err
		}
		if marker == markerSOT {
			break
		}

		marker, content, pos, err = readMarkerSegment(data, pos)
		if err != nil {
			return nil, err
		}
		switch marker {
		case markerCOD:
			h.DefaultCoding, err = parseCOD(content)
			haveCOD = true
		case markerCOC:
			var idx int
			var cs CodingStyle
			idx, cs, err = parseCOC(content, len(h.Components))
			h.ComponentCoding[idx] = cs
		case markerQCD:
			h.DefaultQuant, err = parseQCD(content)
			haveQCD = true
		case markerQCC:
			var idx int
			var qs QuantizationStyle
			idx, qs, err = parseQCC(content, len(h.Components))
			h.ComponentQuant[idx] = qs
		case markerCOM, markerTLM, markerPLM, markerPPM:
			// Recognized, deliberately skipped - see doc.go's Scope
			// section. readMarkerSegment already consumed exactly this
			// segment's bytes, so there is nothing further to do.
		case markerPOC, markerRGN:
			err = unsupportedf("JPEG 2000 feature marker 0x%04X (%s) is not implemented", marker, markerName(marker))
		default:
			err = malformedf("unexpected marker 0x%04X in codestream main header", marker)
		}
		if err != nil {
			return nil, err
		}
	}

	if !haveCOD {
		return nil, malformedf("codestream main header has no COD (Coding style Default) marker segment")
	}
	if !haveQCD {
		return nil, malformedf("codestream main header has no QCD (Quantization Default) marker segment")
	}

	tileParts, err := parseTileParts(data, pos, len(h.Components))
	if err != nil {
		return nil, err
	}
	h.TileParts = tileParts
	return h, nil
}

// parseTileParts walks every tile-part from pos (the first SOT marker)
// through EOC, recording each one's byte range. It also parses (and
// currently discards) each tile-part header's own marker segments -
// see parseTilePartHeader's doc comment for why discarding them, rather
// than merging their overrides into Header, is this sub-phase's
// deliberate scope boundary rather than an oversight.
func parseTileParts(data []byte, pos int, componentCount int) ([]TilePart, error) {
	var parts []TilePart
	for {
		marker, err := peekMarker(data, pos)
		if err != nil {
			return nil, err
		}
		if marker == markerEOC {
			return parts, nil
		}

		tp, nextPos, err := parseOneTilePart(data, pos, componentCount)
		if err != nil {
			return nil, err
		}
		parts = append(parts, tp)
		pos = nextPos

		if pos >= len(data) {
			// A well-formed codestream always ends with EOC; running out
			// of bytes right after a tile-part's data (rather than
			// finding EOC next) is unusual but not fatal - some
			// producers omit the trailing EOC entirely. Treat "no more
			// bytes" the same as "found EOC".
			return parts, nil
		}
	}
}

// parseOneTilePart reads one SOT marker segment, its tile-part header
// (any COD/COC/QCD/QCC/RGN/COM/PLT/PPT marker segments up to SOD), and
// then locates - without copying - that tile-part's compressed data,
// per Psot. It returns the resulting TilePart and the position
// immediately after this tile-part's data, ready for the next SOT or
// EOC.
func parseOneTilePart(data []byte, pos int, componentCount int) (TilePart, int, error) {
	headerStart := pos
	marker, content, pos, err := readMarkerSegment(data, pos)
	if err != nil {
		return TilePart{}, 0, err
	}
	if marker != markerSOT {
		return TilePart{}, 0, malformedf("expected SOT (Start Of Tile-part) marker, found 0x%04X", marker)
	}
	const sotContentLen = 8
	if len(content) != sotContentLen {
		return TilePart{}, 0, malformedf("SOT marker segment is %d bytes, want %d", len(content), sotContentLen)
	}
	tp := TilePart{
		TileIndex: int(binary.BigEndian.Uint16(content[0:2])),
		PartIndex: int(content[6]),
		PartCount: int(content[7]),
	}
	psot := int(binary.BigEndian.Uint32(content[2:6]))

	pos, err = parseTilePartHeader(data, pos, componentCount, &tp)
	if err != nil {
		return TilePart{}, 0, err
	}
	tp.HeaderStart, tp.HeaderEnd = headerStart, pos

	dataStart, dataLength, unknown, err := tilePartData(data, headerStart, pos, psot)
	if err != nil {
		return TilePart{}, 0, err
	}
	tp.DataStart, tp.DataLength, tp.LengthUnknown = dataStart, dataLength, unknown

	return tp, dataStart + dataLength, nil
}

// parseTilePartHeader consumes every marker segment between an SOT and
// its matching SOD, returning the position immediately after SOD (where
// this tile-part's compressed data begins). Any COD/COC/QCD/QCC marker
// segments found are parsed and recorded on tp (see TilePart's own doc
// comment on those fields) - ISO/IEC 15444-1 A.4.2 permits a non-first
// tile-part to repeat any of these to override the main header's values
// for that tile alone, and 14b's packet parsing (packet.go) needs the
// tile's true effective coding/quantization style, not just the
// codestream-wide default 14a alone produced.
func parseTilePartHeader(data []byte, pos int, componentCount int, tp *TilePart) (int, error) {
	for {
		marker, err := peekMarker(data, pos)
		if err != nil {
			return 0, err
		}
		if marker == markerSOD {
			_, pos, err = readMarker(data, pos)
			return pos, err
		}

		var content []byte
		marker, content, pos, err = readMarkerSegment(data, pos)
		if err != nil {
			return 0, err
		}
		switch marker {
		case markerCOD:
			var cs CodingStyle
			cs, err = parseCOD(content)
			tp.TileCoding = &cs
		case markerCOC:
			var idx int
			var cs CodingStyle
			idx, cs, err = parseCOC(content, componentCount)
			if err == nil {
				if tp.TileComponentCoding == nil {
					tp.TileComponentCoding = make(map[int]CodingStyle)
				}
				tp.TileComponentCoding[idx] = cs
			}
		case markerQCD:
			var qs QuantizationStyle
			qs, err = parseQCD(content)
			tp.TileQuant = &qs
		case markerQCC:
			var idx int
			var qs QuantizationStyle
			idx, qs, err = parseQCC(content, componentCount)
			if err == nil {
				if tp.TileComponentQuant == nil {
					tp.TileComponentQuant = make(map[int]QuantizationStyle)
				}
				tp.TileComponentQuant[idx] = qs
			}
		case markerCOM, markerPLT, markerPPT:
			// Recognized, deliberately skipped - see doc.go's Scope
			// section (PLT/PPT: this decoder reads packet headers
			// inline rather than needing them pre-measured or
			// separated out; COM: a free-text comment).
		case markerRGN:
			return 0, unsupportedf("JPEG 2000 feature marker 0x%04X (%s) is not implemented", marker, markerName(marker))
		default:
			return 0, malformedf("unexpected marker 0x%04X in tile-part header", marker)
		}
		if err != nil {
			return 0, err
		}
	}
}

// tilePartData locates a tile-part's compressed data - the bytes
// strictly after its SOD marker - given psot (Psot: the tile-part's
// total length, counted from the first byte of its SOT marker segment
// through the end of its data, or 0 meaning "not given, extends to the
// next SOT or to EOC").
//
// A Psot of 0 needs this function to search forward for the next
// marker rather than being told the length outright. That search is
// safe (cannot mistake a compressed data byte for a marker) because of
// a guarantee the MQ arithmetic coder's own output procedure upholds -
// the same guarantee internal/filter/jbig2mq.go's carry-propagating
// byteOut implements for JBIG2's use of the same coder (see that file's
// doc comment): the coder never emits a 0xFF byte followed by a byte
// with its top bit set, so any 0xFF byte in tile-part data that *is*
// followed by such a byte must be a genuine marker, not coded data that
// merely looks like one.
func tilePartData(data []byte, headerStart, sodEnd, psot int) (dataStart, dataLength int, lengthUnknown bool, err error) {
	dataStart = sodEnd
	if psot != 0 {
		dataLength = psot - (sodEnd - headerStart)
		if dataLength < 0 || dataStart+dataLength > len(data) {
			return 0, 0, false, malformedf("SOT declares Psot=%d, too short to cover its own header (or past the end of the data)", psot)
		}
		return dataStart, dataLength, false, nil
	}

	// Psot == 0: scan forward for the next marker whose value indicates
	// a genuine marker rather than coded data - see this function's doc
	// comment. Any 0xFF byte immediately followed by a byte at or above
	// 0x90 is such a marker (the lowest marker value that can follow SOD
	// is SOT itself, 0xFF90; EOC, 0xFFD9, is the only other marker that
	// can end a tile-part's data, and also falls in this range).
	i := dataStart
	for i+1 < len(data) {
		if data[i] == 0xFF && data[i+1] >= 0x90 {
			return dataStart, i - dataStart, true, nil
		}
		i++
	}
	// No further marker found: the data runs to the end of the slice
	// (this tile-part is the last one, and the codestream has no
	// trailing EOC - see parseTileParts's own tolerance of that case).
	return dataStart, len(data) - dataStart, true, nil
}

// peekMarker reads the two-byte marker at pos without advancing past it
// - used wherever the next marker's identity determines which parsing
// function to hand off to.
func peekMarker(data []byte, pos int) (uint16, error) {
	marker, _, err := readMarker(data, pos)
	return marker, err
}

// readMarker reads a bare two-byte marker (used for SOC, SOD, EOC, and
// to peek at whatever marker comes next before deciding how to parse
// it), returning the position immediately after it.
func readMarker(data []byte, pos int) (marker uint16, next int, err error) {
	if pos+2 > len(data) {
		return 0, 0, malformedf("expected a marker at offset %d, but only %d byte(s) remain", pos, len(data)-pos)
	}
	marker = binary.BigEndian.Uint16(data[pos:])
	if marker&0xFF00 != 0xFF00 {
		return 0, 0, malformedf("expected a marker (0xFFxx) at offset %d, found 0x%04X", pos, marker)
	}
	return marker, pos + 2, nil
}

// readMarkerSegment reads a marker followed by its two-byte length field
// and that many bytes of content (the length field's own two bytes are
// included in the count, per the standard, so content is length-2
// bytes), returning the position immediately after the segment.
func readMarkerSegment(data []byte, pos int) (marker uint16, content []byte, next int, err error) {
	marker, pos, err = readMarker(data, pos)
	if err != nil {
		return 0, nil, 0, err
	}
	if pos+2 > len(data) {
		return 0, nil, 0, malformedf("marker 0x%04X at offset %d has a truncated length field", marker, pos-2)
	}
	length := int(binary.BigEndian.Uint16(data[pos:]))
	if length < 2 {
		return 0, nil, 0, malformedf("marker 0x%04X declares segment length %d, which cannot even cover the length field itself", marker, length)
	}
	if pos+length > len(data) {
		return 0, nil, 0, malformedf("marker 0x%04X declares segment length %d, extending past the end of the data", marker, length)
	}
	content = data[pos+2 : pos+length]
	return marker, content, pos + length, nil
}

// markerName returns a short human-readable name for the markers this
// package's error messages mention by name, or "unknown" for any other
// value - used only to make an error message more informative, never to
// change parsing behavior.
func markerName(marker uint16) string {
	switch marker {
	case markerPOC:
		return "POC, Progression Order Change"
	case markerRGN:
		return "RGN, Region of interest"
	default:
		return "unknown"
	}
}

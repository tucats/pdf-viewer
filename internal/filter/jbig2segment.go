package filter

import (
	"encoding/binary"

	"github.com/tucats/pdf-viewer/internal/pdferror"
)

// This file implements JBIG2 segment header parsing (ITU-T T.88 clause
// 7.2) - see jbig2.go's doc comment for the broader picture of how a
// PDF-embedded JBIG2 stream is structured as a sequence of these
// headers, each followed immediately by that many bytes of segment-
// type-specific data.

// Segment type numbers this package cares about (T.88 Table 3, "Segment
// types"). Only a handful of the roughly 20 defined types are named
// here: the ones jbig2.go's segment-processing switch needs to either
// handle (the three generic region variants) or explicitly reject as
// unsupported (everything symbol/text/halftone/refinement-shaped) rather
// than silently ignore. Every other type (page info, end-of-page,
// end-of-stripe, end-of-file, tables, extensions, and any type number
// this project has never seen) falls through jbig2.go's switch
// unhandled, which is safe precisely because this package never needs
// to interpret their contents - it only needs to skip over them, which
// happens unconditionally based on the segment header's own data-length
// field before the switch ever runs.
const (
	segTypeSymbolDictionary = 0

	segTypeTextRegionIntermediate      = 4
	segTypeTextRegionImmediate         = 6
	segTypeTextRegionImmediateLossless = 7

	segTypePatternDictionary = 16

	segTypeHalftoneRegionIntermediate      = 20
	segTypeHalftoneRegionImmediate         = 22
	segTypeHalftoneRegionImmediateLossless = 23

	segTypeIntermediateGenericRegion      = 36
	segTypeImmediateGenericRegion         = 38
	segTypeImmediateLosslessGenericRegion = 39

	segTypeRefinementRegionIntermediate      = 40
	segTypeRefinementRegionImmediate         = 42
	segTypeRefinementRegionImmediateLossless = 43
)

// unknownSegmentLength is the sentinel data-length value (T.88
// 7.2.7) marking a segment whose length is not known up front, only
// discoverable by scanning its data for a terminating marker. This
// package treats such a segment as unsupported (see jbig2.go) rather
// than implementing that scan, since it is only legal for a subset of
// generic region encodings this package does not expect to encounter in
// practice (real-world PDF producers give every segment a known
// length).
const unknownSegmentLength = 0xFFFFFFFF

// segmentHeader is one parsed JBIG2 segment header (T.88 7.2) - just the
// handful of fields jbig2.go actually needs, not a full transcription of
// every header field (in particular, the referred-to segment numbers
// themselves are parsed only to know how many bytes to skip past, since
// generic region decoding never needs to look another segment up by
// number - only symbol/text region decoding would, and this package does
// not implement those).
type segmentHeader struct {
	number      uint32
	segmentType int
	dataLength  uint64
}

// parseSegmentHeader parses the single segment header at the start of
// data, returning it along with how many bytes that header itself
// occupied (so the caller knows where the segment's data - dataLength
// bytes - begins).
func parseSegmentHeader(data []byte) (segmentHeader, int, error) {
	const fixedPrefix = 4 + 1 // segment number (4 bytes) + header flags (1 byte).
	if len(data) < fixedPrefix {
		return segmentHeader{}, 0, pdferror.Malformedf("JBIG2Decode: truncated segment header")
	}

	number := binary.BigEndian.Uint32(data[0:4])
	flags := data[4]
	segType := int(flags & 0x3F)
	pageAssocIs4Bytes := flags&0x40 != 0

	pos := fixedPrefix

	// Referred-to segment count and retention flags (7.2.4). The top 3
	// bits of the next byte say how many segments this one refers to -
	// unless they are all 1 (value 7), which instead signals the "long
	// form" where the count is a full 29-bit field spread across 4
	// bytes.
	if len(data) < pos+1 {
		return segmentHeader{}, 0, pdferror.Malformedf("JBIG2Decode: truncated segment header (referred-to count)")
	}
	countField := data[pos] >> 5
	var refCount int
	if countField != 7 {
		// Short form: the whole field is this one byte (3-bit count,
		// packed with a 5-bit retention-flags field this package has no
		// use for).
		refCount = int(countField)
		pos++
	} else {
		// Long form: a 4-byte count (top 3 bits are the "7" marker
		// itself, so the real count is the low 29 bits), followed by
		// ceil((count+1)/8) bytes of per-referred-segment retention
		// flags this package also has no use for.
		if len(data) < pos+4 {
			return segmentHeader{}, 0, pdferror.Malformedf("JBIG2Decode: truncated segment header (long-form referred-to count)")
		}
		refCount = int(binary.BigEndian.Uint32(data[pos:pos+4]) & 0x1FFFFFFF)
		pos += 4
		// The count is a 29-bit field, so it can claim up to ~537 million
		// referred-to segments - enough that the byte counts derived from
		// it below could overflow int on a 32-bit platform before any
		// bounds check got to look at them. A segment cannot legitimately
		// refer to more segments than the stream could possibly contain
		// (every referred-to number costs at least one byte here, on top of
		// the referring segment's own header), so anything beyond the data
		// length is malformed regardless of platform.
		if refCount > len(data) {
			return segmentHeader{}, 0, pdferror.Malformedf("JBIG2Decode: segment header claims %d referred-to segments, more than the %d bytes of remaining data could hold", refCount, len(data))
		}
		retentionBytes := (refCount + 8) / 8 // ceil((refCount+1)/8)
		pos += retentionBytes
	}

	// Referred-to segment numbers (7.2.5): each is 1, 2, or 4 bytes,
	// sized by comparing *this* segment's own number against the
	// thresholds below - not by the referred-to numbers' own
	// magnitudes.
	var refSize int
	switch {
	case number <= 256:
		refSize = 1
	case number <= 65536:
		refSize = 2
	default:
		refSize = 4
	}
	pos += refCount * refSize

	// Segment page association (7.2.6): 1 or 4 bytes per the header
	// flags bit checked above.
	pageAssocSize := 1
	if pageAssocIs4Bytes {
		pageAssocSize = 4
	}
	pos += pageAssocSize

	// Segment data length (7.2.7): always 4 bytes.
	if len(data) < pos+4 {
		return segmentHeader{}, 0, pdferror.Malformedf("JBIG2Decode: truncated segment header (data length)")
	}
	dataLength := binary.BigEndian.Uint32(data[pos : pos+4])
	pos += 4

	return segmentHeader{number: number, segmentType: segType, dataLength: uint64(dataLength)}, pos, nil
}

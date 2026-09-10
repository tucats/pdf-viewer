package jpx

// This file implements the bit-level reader a packet header's own
// content (packetheader.go) is coded with - NOT the MQ arithmetic coder
// (mq.go), which only ever codes a code-block's actual sample data
// (tier-1, 14c). A packet header is plain, MSB-first bits, packed with
// one quirk (ISO/IEC 15444-1 B.10.1): whenever a packed byte's value is
// 0xFF, the very next byte only carries 7 significant bits (its would-be
// top bit is a stuffed 0) - the same marker-avoidance convention the MQ
// coder's own byte packing uses, and for the same reason: it guarantees
// a 0xFF byte inside packet data is never immediately followed by a byte
// whose top bit is set, so nothing packed this way can ever be mistaken
// for the start of a two-byte marker (0xFFxx) by code scanning for one -
// see markers.go's tilePartData, which relies on that same guarantee.
//
// Ported from Mozilla's pdf.js (jpx.js's parseTilePackets, its readBits/
// skipMarkerIfEqual/alignToByte closures - Apache License 2.0), per this
// package's usual "write from the spec, cross-check against a proven
// implementation" approach (doc.go's Provenance section) - restructured
// into an explicit Go type rather than JavaScript closures capturing
// mutable locals.

// packetBitReader reads packet header bits from one tile-part's
// compressed data, tracking position in whole bytes (pos) so a caller
// can byte-align (alignToByte) once a packet header ends, matching how
// packet bodies (the actual code-block data readBits skips past once
// header decoding hands off to it) always start on a byte boundary.
type packetBitReader struct {
	data []byte
	// pos is the next unread byte's offset within data.
	pos int
	// buf accumulates whole bytes not yet fully consumed by readBits;
	// bufBits counts how many low bits of buf are still unread.
	buf     uint32
	bufBits int
	// stuffNext is true when the most recently buffered byte was 0xFF,
	// meaning the next byte pulled into buf only contributes 7 bits (its
	// stuffed top bit is skipped) rather than 8.
	stuffNext bool
}

func newPacketBitReader(data []byte) *packetBitReader {
	return &packetBitReader{data: data}
}

// readBits reads the next count (1-31) bits, MSB first, applying B.10.1
// bit-stuffing as bytes are pulled in. Reading past the end of data
// yields zero bits rather than an error, matching this package's
// "bounded, not panicking" tolerance for truncated input elsewhere
// (mqDecoder.byteAt); a caller working from a well-formed codestream
// never needs that padding.
func (r *packetBitReader) readBits(count int) int {
	for r.bufBits < count {
		var b byte
		if r.pos < len(r.data) {
			b = r.data[r.pos]
		}
		r.pos++
		if r.stuffNext {
			r.buf = (r.buf << 7) | uint32(b)
			r.bufBits += 7
			r.stuffNext = false
		} else {
			r.buf = (r.buf << 8) | uint32(b)
			r.bufBits += 8
		}
		if b == 0xFF {
			r.stuffNext = true
		}
	}
	r.bufBits -= count
	return int(r.buf>>uint(r.bufBits)) & ((1 << uint(count)) - 1)
}

// readBit is the count==1 case of readBits, used everywhere a single
// tag-tree or flag bit is read (which is most call sites).
func (r *packetBitReader) readBit() int {
	return r.readBits(1)
}

// alignToByte discards any partially-read byte's remaining bits, moving
// pos to the start of the next whole byte - called once a packet
// header's bits are exhausted (its own length is never stored directly;
// the standard defines it as running until every code-block it
// describes has been accounted for) so the code-block data that follows
// it in the tile-part's byte stream can be located.
func (r *packetBitReader) alignToByte() {
	r.bufBits = 0
	r.buf = 0
	if r.stuffNext {
		// The last whole byte consumed was 0xFF, so byte-aligning must
		// still skip the stuffed byte after it even though readBits
		// never actually pulled any of its bits into buf.
		r.pos++
		r.stuffNext = false
	}
}

// skipSOPIfPresent recognizes and skips a Start-Of-Packet marker (0xFF91)
// at the reader's current byte position, plus its fixed 4-byte segment
// length and packet-sequence-number content - present at the start of
// every packet's header when COD/COC's Scod bit 1 (UseSOPMarkers) is
// set. r must currently be byte-aligned (true at the point every caller
// invokes this: either at the very start of a tile-part's packet data,
// or right after a previous packet's alignToByte).
func (r *packetBitReader) skipSOPIfPresent() {
	if r.pos+1 < len(r.data) && r.data[r.pos] == 0xFF && r.data[r.pos+1] == 0x91 {
		r.pos += 2 + 4
	}
}

// skipEPHIfPresent recognizes and skips a End-of-Packet-Header marker
// (0xFF92) at the reader's current byte position - present right after a
// packet's header (before its code-block data) when COD's Scod bit 2
// (UseEPHMarkers) is set. Like skipSOPIfPresent, called only when r is
// byte-aligned.
func (r *packetBitReader) skipEPHIfPresent() {
	if r.pos+1 < len(r.data) && r.data[r.pos] == 0xFF && r.data[r.pos+1] == 0x92 {
		r.pos += 2
	}
}

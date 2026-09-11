// Package jpx implements a JPEG 2000 ("JPX") image decoder, for PDF's
// JPXDecode stream filter (ISO/IEC 15444-1, also published as ITU-T
// T.800). See docs/PLAN2.md's Phase 14 entry for the project-level
// design decisions summarized here.
//
// # Why this package exists, and why it is separate from internal/filter
//
// Every other PDF stream filter this project supports either has a
// decoder in Go's standard library (DCTDecode/JPEG, via image/jpeg) or
// is small enough to live directly in internal/filter as one or two
// files (CCITTFaxDecode, JBIG2Decode). JPEG 2000 is neither: nothing in
// the standard library reads it, and a real decoder - container parsing,
// arithmetic entropy coding, a wavelet transform, and quantization, each
// with their own moving parts - is far larger than either of those.
//
// So JPEG 2000 decoding lives in its own package, and deliberately knows
// nothing about PDF at all: it does not import internal/syntax (PDF's
// object model) or internal/pdferror (this module's shared error
// values), and its only public entry points take and return plain Go
// types (byte slices, ints, a small Image result type). internal/filter
// contains the thin adapter file (jpx.go, mirroring how dct.go adapts
// Go's standard image/jpeg to this project's filter pipeline) that
// translates this package's own error type into pdferror-wrapped errors
// and reshapes its output into the raw sample bytes internal/image
// expects. Keeping the boundary this clean means this package could, in
// principle, be pulled out into its own standalone Go module later
// (hence the name "jpx" rather than something PDF-specific) without
// touching a single line of its own code - only the adapter would move.
//
// # Development plan (see docs/PLAN2.md Phase 14 for the authoritative,
// living version of this list)
//
//   - 14a (done): package skeleton, JP2 container ("box") parsing, and
//     codestream main-header marker segment parsing (SIZ, COD, COC,
//     QCD, QCC) plus a structural walk locating every tile-part's byte
//     range. No entropy decoding, no pixel output yet.
//   - 14b (this sub-phase): the MQ arithmetic coder (mq.go - a second,
//     independent implementation from internal/filter's JBIG2 one - see
//     below), plus tier-2 packet header parsing (geometry.go's
//     resolution/subband/precinct/code-block structure, progression.go's
//     five packet-iteration orders, tagtree.go's inclusion and
//     zero-bit-plane tag trees, bitreader.go's bit-stuffed packet header
//     reader, and packet.go's top-level decodeTilePackets). Also resolves
//     14a's one deferred item: per-tile COD/COC/QCD/QCC overrides are now
//     parsed and threaded through (TilePart's new fields, Header's
//     effectiveCoding/effectiveQuant/effectiveTileDefaultCoding in
//     siz.go) rather than discarded. Still no pixel output - this
//     sub-phase locates every code-block's compressed-data byte ranges
//     and pass counts; nothing yet runs the MQ coder over them.
//   - 14c (done): tier-1 coding (tier1.go): the EBCOT bit-plane coding
//     passes (significance propagation, magnitude refinement, cleanup -
//     including the cleanup pass's run-length optimization) that turn a
//     code-block's compressed bytes into per-sample magnitude/sign/
//     bitsDecoded data - not yet a final dequantized coefficient value;
//     see tier1.go's own doc comment on why that split matches the
//     standard's own Annex D/Annex E boundary. Supports only the
//     default code-block style plus codeBlockResetContext and
//     codeBlockSegmentationSymbols; the other four style bits
//     (selective bypass, per-pass MQ termination, vertically-causal
//     context, predictable termination) report ErrUnsupported - the
//     same scope cut pdf.js's own tier-1 decoder makes, confirmed by
//     checking this sub-phase's context tables and pass logic directly
//     against that project's BitModel class (an exception to this
//     package's usual from-the-specification-alone approach, made
//     because those 75-entry lookup tables are unusually easy to get
//     silently wrong - see tier1.go's Provenance section).
//   - 14d (done): dequantization (dequantize.go - Annex E.1's inverse
//     quantization procedure, including the "insert a reconstruction bit
//     for any bit-planes a code-block's contributions truncated" rule)
//     and the inverse discrete wavelet transform (idwt.go - both the 5/3
//     reversible integer filter and the 9/7 irreversible filter, applied
//     row-then-column per resolution level per §F.3's own recursion),
//     reassembling one tile-component's full sample array from its
//     subbands. Still not reachable from internal/filter - the result
//     (reconstructedComponent) is real-valued, not yet DC-level-shifted,
//     clamped, or passed through any multiple component transform, all
//     three of which are 14e's job.
//   - 14e (done): the multiple component transform (mct.go - reversible
//     RCT / irreversible ICT, both decoder-direction only, applied across
//     a tile's first three reconstructed components), DC level shifting,
//     rounding/clamping, and tile compositing into a final image
//     (image.go - Image/ImageComponent, and Decode, this package's first
//     public whole-image entry point, tying together every earlier
//     sub-phase's per-tile-component pipeline plus this one's own
//     per-component finalization and placement). Still not reachable from
//     internal/filter, and Image carries no colour-space or soft-mask
//     concept yet - both are 14f's job.
//   - 14f (done): internal/filter wiring (jpx.go there - the JPXDecode
//     case in filter.go's decodeOne, plus DecodeImage, the richer entry
//     point internal/content uses for an actual image XObject or inline
//     image) and the PDF-specific behaviors ISO 32000-1 7.4.9 documents
//     for this filter specifically: a /ColorSpace-absent image falls back
//     to a Device family of the right arity (DeviceGray/RGB/CMYK by
//     decoded component count - the same "component count, not the
//     embedded profile itself" approximation this project's own
//     /ICCBased handling already uses, per internal/image/colorspace.go's
//     resolveICCBased), and a nonzero /SMaskInData splits the *last*
//     decoded component off as a per-pixel alpha channel rather than
//     parsing the JP2 container's own "cdef" (Channel Definition) box to
//     find it precisely - see DecodeImage's doc comment for why the
//     trailing-component convention is enough in practice.
//   - 14g (done): a from-scratch codestream *encoder* (encode.go - a
//     single, fixed configuration: one tile, zero decomposition levels,
//     5/3 reversible only, single layer, LRCP - see that file's own
//     Scope section for why), tools/genfixtures fixtures built with it
//     (image-jpx.pdf, image-jpx-rgb.pdf), end-to-end render tests, and
//     documentation (docs/capability-matrix.md, FIXTURES.md). Also fixed
//     a real bug this sub-phase's own FuzzDecode seeds immediately
//     found: siz.go's validateGeometry was missing the standard's own
//     "XTOsiz+XTsiz > XOsiz" (and Y) requirement, letting a malformed
//     SIZ describe a tile grid whose first column/row does not reach the
//     image area's own origin - tileGridBounds then handed a negative
//     width/height down to dequantizeComponent's slice allocation.
//
// # Provenance: an independent, from-scratch implementation
//
// Unlike internal/filter/ccitt.go (a deliberate, documented port of a
// long-proven reference decoder's algorithm), this package is written
// directly from the ITU-T T.800 specification's own algorithmic
// description, the same approach internal/filter took for the harder
// pieces of JBIG2 (its arithmetic integer decoding procedures, T.88
// Annex A) where no reference implementation this project could legally
// and mechanically port was in hand. Two independent reasons make that
// the right call here too: first, a mechanical port needs a specific,
// checkable reference source to compare against line by line, which
// this project does not have for JPEG 2000; second, this package's
// stated goal (a clean, standalone-extractable implementation with no
// inherited license entanglements) is best served by writing the
// algorithm out fresh from the standard's own description.
//
// This project has no independently-produced real-world JPX sample to
// validate against (the same situation JBIG2 started in - see
// internal/filter/jbig2mq.go's doc comment). So, following that same
// precedent, this package also builds its own from-scratch *encoder*
// (test- and fixture-only; never reachable from real PDF decoding),
// letting a known bitmap round-trip through encode-then-decode as the
// strongest verification available. Writing the two directions from
// opposite ends of the standard's own description - rather than deriving
// one mechanically from the other - is what makes a round trip real
// evidence both are right, instead of evidence they merely share an
// assumption.
//
// # Scope
//
// A full JPEG 2000 Part 1 decoder handles a very large surface area;
// this package deliberately implements the common, real-world subset a
// PDF's JPXDecode stream actually needs, and reports every deliberately
// unimplemented feature by name via ErrUnsupported rather than
// mis-decoding it silently:
//
//   - Both defined wavelet filters (5/3 reversible, 9/7 irreversible)
//     and both defined multiple component transforms (RCT, ICT) are
//     supported - a decoder cannot choose which one a given file uses,
//     so these are not optional scope, unlike the items below.
//   - All five progression orders (LRCP, RLCP, RPCL, PCRL, CPRL) are
//     supported for the same reason: progression order only changes the
//     *sequence* packets appear in within the codestream, which a
//     general decoder must follow regardless of which order an encoder
//     chose.
//   - Multiple tiles, and multiple tile-parts per tile, are supported -
//     large geospatial and medical-imaging exports (this filter's most
//     common real-world source per docs/PLAN2.md's Phase 14 rationale)
//     routinely tile a large image rather than encoding it as one piece.
//   - Both the raw codestream and the full JP2 container ("box") file
//     format are accepted, matching ISO 32000-1 7.4.9's own allowance of
//     either form inside a JPXDecode stream.
//   - Region-of-interest coding (the RGN marker) is explicitly not
//     implemented - a real but narrow feature (selectively higher
//     quality for one image region) with no evidence of real-world use
//     in PDF-embedded JPX specifically; a stream naming it fails with
//     ErrUnsupported rather than silently ignoring the region weighting.
//   - JPEG 2000 Part 2 extensions (custom multiple-component transforms
//     via the MCT/MCC/MCO markers, arbitrary wavelet kernels, and so on)
//     are not implemented - these are rare extensions even outside PDF,
//     and PDF's own JPXDecode filter is defined against Part 1.
//   - Of the six code-block style bits COD/COC can set (coding.go's
//     codeBlock* constants), only the default and codeBlockResetContext/
//     codeBlockSegmentationSymbols are decoded (tier1.go, 14c); the
//     other four (selective arithmetic coding bypass, termination on
//     each coding pass, vertically-causal context formation,
//     predictable termination) report ErrUnsupported by name - the same
//     scope cut pdf.js's own tier-1 decoder makes, which this package
//     takes as real-world evidence PDF-embedded JPX rarely needs them.
//   - The packed-packet-headers markers (PPM in the main header, PPT in
//     a tile-part header) and the packet/tile-part length markers
//     (PLM, PLT, TLM) are recognized and skipped where they carry no
//     information this decoder needs to already have (this decoder reads
//     packet headers inline rather than needing them separated out or
//     pre-measured) - see markers.go.
package jpx

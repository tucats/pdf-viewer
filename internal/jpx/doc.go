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
//   - 14a (this sub-phase): package skeleton, JP2 container ("box")
//     parsing, and codestream main-header marker segment parsing (SIZ,
//     COD, COC, QCD, QCC) plus a structural walk locating every
//     tile-part's byte range. No entropy decoding, no pixel output yet -
//     see Header and ParseHeader below.
//   - 14b: the MQ arithmetic coder (a second, independent implementation
//     from internal/filter's JBIG2 one - see below) and tier-2 packet
//     header parsing (tag trees, inclusion and zero-bit-plane
//     information, layer/precinct/resolution/component packet
//     iteration for every progression order).
//   - 14c: tier-1 coding: the EBCOT bit-plane coding passes (significance
//     propagation, magnitude refinement, cleanup) that turn a
//     code-block's compressed bytes into quantized wavelet coefficients.
//   - 14d: dequantization and the inverse discrete wavelet transform (both
//     the 5/3 reversible integer filter and the 9/7 irreversible filter),
//     reassembling one tile-component's samples from its subbands.
//   - 14e: the multiple component transform (reversible RCT / irreversible
//     ICT), DC level shifting, and tile compositing into a final image.
//   - 14f: internal/filter wiring (the JPXDecode case in filter.go) and
//     the PDF-specific behaviors ISO 32000-1 7.4.9 documents for this
//     filter specifically (a /ColorSpace-absent image falls back to the
//     JPX data's own embedded color space; /SMaskInData controls whether
//     an embedded opacity channel is used as this image's soft mask).
//   - 14g: fixtures (built with this package's own from-scratch encoder,
//     for the round-trip-testing reason given below), end-to-end render
//     tests, and documentation.
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
//   - The packed-packet-headers markers (PPM in the main header, PPT in
//     a tile-part header) and the packet/tile-part length markers
//     (PLM, PLT, TLM) are recognized and skipped where they carry no
//     information this decoder needs to already have (this decoder reads
//     packet headers inline rather than needing them separated out or
//     pre-measured) - see markers.go.
package jpx

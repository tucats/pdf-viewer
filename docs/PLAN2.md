# PLAN2: Post-v1 capability expansion

`docs/PLAN.md`'s Phase 0-6 plan is complete (see its Progress Log). Every
row in `docs/capability-matrix.md` that was "Not started" or "Not
scheduled" at that point was a deliberate, documented deferral, not an
oversight - see each phase's own "What's carried forward" note and the
Phase 6 closeout's summary list. This document is the living plan for
attacking those deferrals (plus a few gaps the matrix always knew about
but never scheduled at all), so that "what's next" has the same kind of
single authoritative answer PLAN.md gave Phase 0-6.

**How phases here are ordered.** Not by implementation difficulty, but
by an estimate of how often a real-world PDF actually encountered by an
embedding application would hit the gap, weighted by how badly it fails
when it does:

- **Hard failure** (the document does not open at all, or a page throws
    `ErrUnsupported` instead of rendering anything) ranks above
- **Visible defect** (the page renders, but specific content is wrong or
    missing - a `notdefGlyph` box instead of real text, a missing
    shading region, a blank form field) which ranks above
- **Cosmetic/fidelity gap** (the page renders correctly in substance but
    not in exact fine detail - stroke join geometry, non-color-managed
    ICC, clip-intersection approximation).

Within similar severity, more common document types (scanned office
documents, forms, non-Latin text) rank above rarer ones (prepress
color-managed workflows, mesh-gradient vector art, geospatial imagery).
This is an estimate, not measured telemetry from real usage - revise the
ordering if a concrete corpus or user report contradicts it.

Each phase below should update `docs/capability-matrix.md`'s
corresponding row(s) in the same change that changes them, exactly as
PLAN.md's phases did, and land as independently-committable sub-phases
per the project's established workflow (implement, test/fuzz, commit,
push - not one giant commit at the end).

**Status legend:** Not started / In progress / Done, same as the
capability matrix.

## Phase 7: Standard security handler (encryption)

**Status: 7a and 7b done.**

Currently *any* `/Encrypt`-declaring trailer is rejected immediately
(`ErrEncrypted`), including the very common case of a PDF whose owner
password sets permissions (no printing, no copying, ...) but whose user
password is empty - meaning the file is meant to open freely in any
reader. This is a **hard failure**: today, that whole broad class of
real-world "protected" PDFs (many bank statements, invoices, e-signed
contracts, and print-to-PDF outputs from tools that default to
permissions-only protection) cannot be opened at all, not even
degraded. This is likely the single highest-impact gap in the matrix
precisely because it is a hard failure rather than a partial one.

- **7a: Empty user password (RC4 and AES, revisions 2-6).** Implement
    the Standard Security Handler's key-derivation algorithm (ISO
    32000-1 7.6.3/7.6.4, including AESV2/AESV3 for newer revisions) and
    RC4/AES-CBC stream and string decryption, using only
    `crypto/rc4`, `crypto/aes`, `crypto/cipher`, and `crypto/md5`/`sha256`
    from the standard library - no CGO, matching this project's existing
    dependency policy. Scope this sub-phase to the empty-user-password
    case only, since the key is derivable with no caller-supplied input
    and `Open`/`OpenFile`'s signatures do not need to change.
- **7b: Non-empty user password.** Requires the password-input API
    decision PLAN.md's Draft Public API section flagged and deferred
    (an `OpenOption`, most likely - e.g. `WithPassword(string)`) plus
    wiring incorrect-password detection into a clear error distinct from
    "this file is encrypted at all."
- **Explicitly not in this phase's scope:** public-key security handlers
    (no known demand; see the matrix) and the `Crypt` stream filter
    (only meaningful once decryption exists at all - defer to whichever
    sub-phase turns out to need it, likely folded into 7a).

**Exit criteria:** a PDF encrypted with an empty user password opens and
renders identically to its unencrypted equivalent; a PDF with a
non-empty user password opens given the correct password via the new
option and fails with a distinguishable error given a wrong one or none.

## Phase 8: JBIG2Decode

**Status: Done (8a through 8h).** Every arithmetic-coded JBIG2 mode -
generic regions, symbol dictionaries, text regions and refinement - plus
`/JBIG2Globals`. Huffman-coded symbol dictionaries and text regions,
halftone regions and pattern dictionaries, and MMR-coded generic regions
remain unimplemented and report `ErrUnsupported` naming the feature; see
`docs/capability-matrix.md`'s JBIG2Decode row for the exact list.

JBIG2 is a common compression choice for black-and-white scanned pages
specifically because it beats CCITT Group 4 (already supported) on
text-heavy scans - many enterprise scan-to-PDF/copier defaults and OCR
pipelines emit it. Any page whose image uses this filter currently fails
outright (`ErrUnsupported` propagated from `internal/filter`), a **hard
failure** for what is likely the most common *remaining* filter gap in
practice (ahead of JPEG 2000 - see Phase 14).

- **8a: the MQ arithmetic coder** (`internal/filter/jbig2mq.go`), both
    directions - the decoder JBIG2 needs and a test/fixture-only encoder
    that exists so the two can be round-trip tested against each other
    (this project has no independently-produced real-world JBIG2 sample
    to check against; see that file's doc comments).
- **8b: segment parsing and generic-region decoding** - the common case
    for scanned text. JBIG2's symbol/text-region dictionary-based
    compression (used for the highest compression ratios on large text
    scans) can follow as a further sub-phase if the generic-region path
    proves insufficient against real fixtures. *It did:* the first
    real-world sample this project was given (a Xerox copier scan) used
    symbol mode and failed outright, which is what prompted 8d-8h below.
- **8d: the arithmetic integer decoding procedures** (T.88 Annex A) -
    reading whole numbers, rather than single pixels, out of the same
    MQ-coded stream. A prerequisite for everything after it.
- **8e: symbol dictionary and text region decoding** - the mode that
    prompted this continuation, and the one most real scan-to-PDF and
    OCR output actually uses.
- **8f: `/JBIG2Globals`** - resolving the second stream a shared symbol
    dictionary lives in, which needs a cross-reference table and so a
    resolver interface `internal/parser` satisfies.
- **8g: generic refinement regions** (T.88 6.3) - how lossy encoders
    correct an individual symbol instance, and how a dictionary defines
    one symbol as a refinement or aggregate of others.
- **8h: fixtures, real-world sample and documentation.**
- **8c: fixtures, end-to-end render test and documentation** - a
    `tools/genfixtures` JBIG2 builder, a render test proving a
    JBIG2-encoded page image decodes and draws, and the
    `docs/capability-matrix.md` row update.
- No standard-library or already-permitted dependency exists for this
    (same situation CCITT was in - see `internal/filter/ccitt.go`'s
    provenance-documented from-scratch port); plan for a similarly
    from-scratch implementation with clear license/provenance
    documentation if porting a known-good reference decoder's algorithm,
    per this project's existing precedent.

**Exit criteria:** a representative scanned-document fixture using
JBIG2 generic-region encoding decodes and renders correctly; the matrix
row moves from "Not scheduled" to "Done" or "Partial" with any remaining
JBIG2 feature (e.g. refinement regions) explicitly noted. For 8d-8h,
additionally: a real scanner's symbol-mode page decodes and renders
correctly end to end.

## Phase 9: Non-Identity Type 0/CID encodings (CJK support)

**Status: Done (9a, 9b, 9c).**

Today, any Type 0/CID font not using `Identity-H`/`Identity-V` - that
is, any predefined CJK encoding (`UniGB-UCS2-H`, `UniCNS-UCS2-H`,
`90ms-RKSC-H`, and similar) or an embedded CMap stream - falls back to
`notdefGlyph` for every single character, a **visible defect** that in
practice means *all* text is replaced with hollow boxes on a large
fraction of real-world Chinese/Japanese/Korean PDFs (Identity-H is
common only when the producer chose to embed a CID-keyed font
one-to-one; many CJK-authoring tool chains instead select a predefined
encoding). This is a structural prerequisite the matrix already flagged
(`internal/fonts/cid.go`'s doc comment) rather than a newly discovered
gap.

- Implement CMap parsing (`internal/fonts`, likely a new file/small
    package) covering both the embedded-CMap-stream form (a PostScript-like
    `begincidrange`/`endcidrange` structure) and, for predefined names,
    either a small bundled table for the common predefined CJK encodings
    or (preferred, avoiding bundling licensed Adobe CMap data) parsing
    the same embedded-stream grammar applied to whichever predefined
    resource is actually reachable - resolve this design question at the
    start of this phase rather than assuming either answer.
- This is the natural place to build shared code/byte-sequence-to-CID
    parsing machinery that Phase 10 (below) reuses for `/ToUnicode`,
    since both are CMap-shaped structures per ISO 32000-1 9.7.5.

**Exit criteria:** a fixture using a predefined CJK encoding (or an
embedded CMap) renders real glyph outlines (given an embedded or
substitute CID-keyed font) instead of `notdefGlyph` boxes for every
code.

## Phase 10: Text extraction (`/ToUnicode` CMap parsing)

**Status: Done.**

Not a rendering-correctness gap (pages already render without this),
but the single most commonly expected capability of "a PDF viewer" that
this project does not yet expose at all: recovering searchable/copyable
Unicode text from a page. It also directly unblocks a capability the
matrix explicitly flagged as scope-limited for exactly this reason -
font substitution for Type 0/CID fonts (`docs/capability-matrix.md`'s
Fonts section: "a Type0 font's CIDs have no known Unicode meaning
without a `/ToUnicode` CMap, which this package does not parse").

- Parse `/ToUnicode` CMap streams (the same CMap grammar Phase 9 already
    needs to build for embedded encoding CMaps - sequence this phase
    right after Phase 9 specifically to reuse that parser rather than
    duplicating it).
- Define the extraction API surface (likely `Page.Text` or similar,
    returning positioned runs) as a new, separate capability per
    PLAN.md's original Phase 4 bullet ("Keep text extraction as a
    separate capability from text painting") - this is a new public API
    decision, not just an internal one, and may warrant its own small
    design note before implementation.
- Once `/ToUnicode` parsing exists, revisit whether Type 0/CID font
    substitution (deferred in Phase 4, see `docs/FONTS.md` and this
    project's font-substitution status) is now unblocked as a follow-on,
    small sub-phase - it depends on exactly this CMap.

**Exit criteria:** given a fixture with known text content (both a
simple-font and a Type0/CID fixture), the new extraction API returns the
correct Unicode string and a defensible per-glyph position.

## Phase 11: Type 1 embedded font outline extraction

**Status: Done.**

Type 1's own charstring format (distinct from CFF's Type 2 charstrings,
already fully implemented) is unimplemented; a Type 1 font falls back to
`notdefGlyph` unless font substitution (opt-in, `WithFontSubstitution`)
finds a same-family system font - which, when enabled, already
mitigates this gap somewhat for simple fonts (Type 1 is always a simple
font). Ranked below the CJK and text-extraction phases above because
(a) substitution already provides a usable, if inexact, fallback when
enabled, and (b) most *current* PDF-producing tool chains (Word,
browsers, InDesign, Illustrator, LaTeX's more recent output modes)
default to TrueType or CFF/OpenType rather than Type 1. It remains worth
doing because Type 1 is still common in older and print/publishing-origin
PDFs (classic LaTeX/dvips output, legacy scanned-and-OCRed archives,
many academic-paper repositories) where exact glyph shape matters more
than in casual documents. This is a **visible defect** (wrong/placeholder
glyphs) when substitution is off, and an accuracy gap even when it is on.

- Implement the Type 1 charstring interpreter (a different operator set
    and encryption scheme - `eexec`/charstring decryption per the Type 1
    Font Format spec - from Type 2, but the same general "stack-based
    outline-construction interpreter" shape `cff.go` already established;
    that file's structure is a reasonable template).
- Wire into `simple.go`'s embedded-font path (`/FontFile`) alongside the
    existing `/FontFile2` (TrueType) and `/FontFile3` (CFF) paths.

**Exit criteria:** a fixture with an embedded Type 1 font renders its
real glyph outlines, matching this project's existing TrueType/CFF
fixture-testing rigor.

## Phase 12: AcroForm field appearance regeneration

**Status: Done.**

Widget annotations with an *existing* appearance stream already render
correctly (Phase 5). Fields filled by tooling that does not generate an
appearance stream itself (common with programmatic form-filling
libraries, older or minimal PDF producers, and some non-Adobe scanning/
OCR-plus-form pipelines) currently show as blank - a **visible defect**
scoped to forms specifically, not general rendering.

- Regenerate a text/checkbox/radio/choice field's appearance from its
    `/V` (value) and `/DA` (default appearance string, providing font
    and color) per ISO 32000-1 12.7, reusing this project's existing
    text-painting and font machinery rather than building a second one.
- Stay within this project's stated scope as a *renderer*: this is
    "paint what the field's current value says," not interactivity,
    scripting, or field editing - the matrix's existing non-goal for
    interactivity is unaffected.

**Exit criteria:** a fixture with an unfilled-appearance but
value-populated form field renders the value, matching Acrobat's
appearance-generation rules closely enough for common field types (text,
checkbox).

## Phase 13: Mesh and function-based shadings (Types 1, 4-7)

**Status: 13a-13c done; 13d-13e (Types 6-7, Coons/tensor patch meshes) in progress.**

Axial and radial shadings (Types 2-3, the large majority of real-world
gradients) are done. Function-based (Type 1) and mesh shadings (Types
4-7, free-form/lattice-form Gouraud triangles and Coons/tensor patches)
are real but rarer - typically produced by higher-end vector-illustration
tools (Illustrator gradient meshes) for complex color blends a simple
axial/radial gradient cannot express. A page using one currently fails
that specific `sh`/shading-pattern operation with `ErrUnsupported`
rather than silently omitting it - a **visible defect** confined to
whatever shape used the shading.

- Type 1 (function-based) shadings are the simpler addition: evaluate
    the shading's 2-in/N-out function (`internal/function`, already
    built) over its domain, reusing the existing `Shading` abstraction's
    `ColorAt` closure pattern from Types 2/3.
- Mesh shadings (Types 4-7) are substantially more work: parsing the
    shading stream's own packed vertex/patch binary format (distinct per
    subtype) and rasterizing triangle/patch interpolation rather than a
    simple 1D gradient parameter. Consider splitting into its own
    sub-phase (13b) separate from Type 1 (13a) given the size difference.

**Exit criteria:** a Type 1 shading fixture renders correctly; mesh
shading support (13b) is either delivered or explicitly re-scoped with
its own documented rationale if the implementation cost turns out to
outweigh realistic benefit.

## Phase 14: JPXDecode (JPEG 2000)

**Status: Not started.**

Same class of gap as JBIG2 (Phase 8) - a page using this filter fails
outright - but JPEG 2000 in practice appears mostly in narrower
verticals (geospatial/GIS exports, medical imaging (DICOM-to-PDF),
archival scanning standards like PDF/A with JPX for high-fidelity
images) rather than general office/consumer documents, so it is ranked
below JBIG2 despite being the same severity of failure.

- No standard-library JPEG 2000 decoder exists. Evaluate scope
    carefully before committing: a full JPEG 2000 (wavelet-based, richly
    featured) decoder is a substantially larger undertaking than CCITT or
    even JBIG2's generic-region mode. Consider whether a minimal
    subset (e.g. only the common lossy/lossless codestream profiles
    actually seen in PDF-embedded JPX, not the full standard) is
    sufficient before treating this as full-format work.

**Exit criteria:** a representative JPX-encoded image fixture decodes
and renders correctly, with any deliberately-unsupported JPEG 2000
feature documented the same way CCITT/JBIG2's own gaps are.

## Phase 15: Stroke join geometry and exact clip intersection

**Status: Not started.**

Both are **cosmetic/fidelity** gaps only - no page fails or shows wrong
content, but two specific things are approximated: every stroke join
renders round regardless of the content stream's requested join style
(no true miter or bevel geometry), and multiple simultaneously active
clips combine via coverage-multiplication rather than true polygon
Boolean intersection (exact only for opaque-interior clips). Ranked
below every phase above because nothing in the document fails or goes
missing - the output is subtly different from a reference renderer, not
wrong in a way most viewing use cases would notice, but it is the kind
of gap that shows up in pixel-diff-based regression testing against
other renderers and in high-fidelity print-proofing use cases.

- **15a: Miter/bevel stroke joins.** Extend `internal/graphics/stroke.go`'s
    `StrokeToFill` with real miter-limit-aware and bevel join geometry
    alongside the existing round-join path.
- **15b: Exact clip intersection.** Replace (or supplement, for a
    performance-sensitive fast path) coverage-multiplication with true
    polygon-boolean intersection where more than one clip is active and
    at least one has a non-trivial (non-opaque-interior) coverage
    profile.

**Exit criteria:** stroke fixtures with explicit miter/bevel joins match
a hand-derived reference geometry; a multi-clip fixture with
partially-transparent-edge clips (e.g. two overlapping anti-aliased
circles) matches true intersection rather than the multiplied
approximation.

## Phase 16: Remaining page boxes and PDF 2.0 verification

**Status: Not started.**

Both are low real-world impact for a general-purpose viewer:

- **BleedBox/TrimBox/ArtBox** matter almost exclusively to
    prepress/print-production workflows, a narrow slice of documents
    this project is likely to encounter; `MediaBox`/`CropBox` (already
    done) cover ordinary viewing correctly. Implementation is mostly
    mechanical (same inheritance/clipping pattern as `CropBox`, exposed
    via `RenderOptions`' page-box selection already sketched in
    PLAN.md's Draft Public API).
- **PDF 2.0 verification** is not "missing support" so much as
    "unverified against a real corpus" - PLAN.md's own assessment is that
    most 1.7-targeting code already handles a 2.0 file's shared
    structure. This phase is mostly about obtaining or constructing a
    real 2.0 test corpus and confirming that assessment, plus scoping
    whatever genuinely-2.0-specific features (new encryption revisions,
    `/AF` associated files) turn out to matter once real files are in
    hand.

**Exit criteria:** `RenderOptions` can select any of the four page
boxes and renders accordingly; a PDF 2.0 corpus renders correctly or
each specific failure is triaged into its own follow-up item.

## Backlog: revisit only on concrete demand

These are real, known gaps - already documented in
`docs/capability-matrix.md` - that this plan deliberately does not
schedule into the numbered phases above, either because real-world
frequency appears very low, because a design decision blocks starting
at all, or because the matrix already treats them as a considered
non-goal for now. Move an item out of this section and into a numbered
phase above if a concrete user report or corpus finding changes the
frequency estimate.

- **Bundled last-resort fallback font** (so font substitution has a
    candidate even with zero system fonts available/configured) - blocked
    on the user's own license/bundling decision, see
    `docs/FONTS.md`'s Phase 5 and this project's font-substitution-status
    memory. Do not start without asking first.
- **Type 0/CID font substitution** - naturally follows Phase 10 above
    once `/ToUnicode` parsing exists; not separately numbered since it is
    expected to be a small follow-on to Phase 10, not its own phase.
- **Public-key security handler** - no known demand.
- **Uncolored tiling patterns** (`/PaintType 2`) - narrow real-world
    usage relative to colored tiling patterns (already done).
- **Non-separable blend modes** (Hue, Saturation, Color, Luminosity) and
    the four separable modes this project chose not to implement
    (ColorDodge, ColorBurn, HardLight, SoftLight, Overlay) - all fall back
    to Normal per spec, which is a defined, non-broken behavior, not a
    failure; revisit if a corpus shows these are common enough to matter
    visually.
- **Transparency groups** (isolated/knockout compositing for a `/Group`
    Form XObject) - a real gap, but content generally still looks
    reasonable without group isolation except in specific overlapping-
    transparency compositions; revisit if real-world pages show visibly
    wrong compositing.
- **ExtGState-level soft masks** (`/SMask` deriving a mask from a whole
    transparency group) - narrower usage than per-image `/SMask`
    (already done).
- **Type 3 fonts** (glyphs as content streams) - rare in practice.
- **LZWDecode `/EarlyChange 0`** - rare; the standard library offers no
    direct way to select this variant, so support would require a
    from-scratch LZW decoder specifically for this one flag.
- **Hybrid-reference files** (`/XRefStm` alongside a classic xref table)
    - rare, and fails soft (a missing object resolves as null) rather
    than hard, unlike every hard-failure item scheduled above.
- **True ICC profile application, CalGray/CalRGB white point and gamma**
    - would require an actual color-management engine; this project's
    existing by-component-count/aliasing approximation is a deliberate,
    documented non-color-managed policy, revisited only if genuinely
    color-critical use cases appear.
- **Marked content operators** (`BMC`/`BDC`/`EMC`/`MP`/`DP`) - metadata/
    structure only, no rendering effect in this project's scope.
- **Annotation/form interactivity, JavaScript actions, PDF creation or
    editing** - standing non-goals, not deferred work; see
    `docs/PLAN.md`'s "Non-goals for the Initial Release" and
    `docs/capability-matrix.md`'s "Explicit non-goals" section. Listed
    here only so this document is a complete answer to "what's not
    planned and why," not because they are candidates to schedule later.

## How to use this document

Treat it like `docs/PLAN.md`: update a phase's status and add a Progress
Log entry (mirroring PLAN.md's own format) as work actually lands, and
update `docs/capability-matrix.md`'s corresponding rows in the same
change - a code change not reflected in the matrix is incomplete, per
that document's own stated rule. Re-order phases here if a concrete
real-world finding (a user-reported file that fails, a corpus survey)
contradicts the frequency estimates above; the ordering is a starting
estimate, not a commitment independent of evidence.

## Progress Log

This section mirrors `docs/PLAN.md`'s own Progress Log: updated at the
end of each phase (or sub-phase) with what was actually built, appended
in order, not rewritten later except to fix mistakes.

### Phase 7a: Standard security handler, empty user password — done (2026-09-09)

- **`internal/crypt` (new package).** Implements the Standard Security
    Handler's key derivation and decryption, using only `crypto/rc4`,
    `crypto/aes`, `crypto/cipher`, `crypto/md5`, `crypto/sha256`, and
    `crypto/sha512` from the standard library (no CGO, no third-party
    dependency, matching this project's Dependency and safety policy):
    - `standard.go`: Algorithm 2 (`ComputeFileKey`) and Algorithms 3-5
        (`ComputeOwnerHash`, `ComputeUserHash`) for revisions 2-4 (RC4 and
        AES-128/"AESV2").
    - `hash56.go`: the AES-256 ("V5") revision 5/6 construction -
        revision 6's hardened hash (Algorithm 2.B, `hashR6`, mixing
        SHA-256/384/512 with AES-128 rounds) and Algorithm 2.A's file-key
        unwrapping (`ComputeFileKeyR56`), plus the forward direction
        (`ComputeAES256UserStrings`/`ComputeAES256OwnerStrings`) used only
        by this package's own tests and `tools/genfixtures`.
    - `key.go`: Algorithm 1 (`ObjectKey`), the per-object key mixing
        revisions 2-4 need and revision 5-6 deliberately skips.
    - `cipher.go`: RC4 and PKCS#7-padded AES-CBC encrypt/decrypt wrappers.
    - `handler.go`: `Handler`, built by `New` from an already-resolved
        `/Encrypt` dictionary and the trailer `/ID`, always attempting an
        empty password and returning `ErrWrongPassword` if that does not
        validate (Algorithm 6 for revisions 2-4, the validation-salt hash
        comparison for revisions 5-6) - the one case Phase 7a leaves for
        Phase 7b. `Handler.DecryptObject` recursively decrypts every
        `syntax.String` and `syntax.Stream` found inside an already-parsed
        object, correctly leaving `/Metadata` streams alone when
        `/EncryptMetadata` is false.
- **`internal/parser` wiring.** `Open` no longer rejects every `/Encrypt`
    trailer unconditionally: `setupEncryption` (new file, `encrypt.go`)
    resolves the `/Encrypt` dictionary and trailer `/ID` (before any
    decryption is possible, so neither is ever itself decrypted) and
    builds a `crypt.Handler`, stored on `Document.crypt`.
    `Document.Resolve` now decrypts every ordinary (non-object-stream)
    object it reads via `Handler.DecryptObject`, keyed by that object's
    own number and generation - an object packed inside an object stream
    is correctly left alone a second time, since the containing object
    stream was already decrypted as an ordinary stream. Any failure
    building the handler (malformed `/Encrypt` dictionary, unsupported
    revision or security handler, or a real password requirement) is
    still reported as `ErrEncrypted`, preserving this package's existing
    error-classification behavior for every case Phase 7a does not cover.
- **Fixtures.** `tools/genfixtures` gained
    `buildEncryptedRC4_40bit`/`buildEncryptedAES128`/`buildEncryptedAES256`,
    real byte-accurate encrypted PDFs (built using `internal/crypt`'s own
    exported forward-direction functions, so the fixture generator and
    the decryption code cannot silently drift apart) covering revisions
    2, 4, and 6 respectively, each encrypting the same content
    `filled-rect.pdf` draws plus an `/Info /Title` string. The
    pre-existing `encrypted.pdf` fixture (placeholder, non-byte-accurate
    `/O`/`/U`) now exercises the "password required" rejection path
    instead of "encryption unimplemented at all".
- **Tests.** `internal/crypt/handler_test.go` round-trips every
    supported revision/cipher combination (encrypt with this package's
    own forward functions, decrypt with `Handler`) plus wrong-password,
    unsupported-revision/filter, `/Identity` crypt-filter passthrough,
    dictionary/array/stream recursion, and `/EncryptMetadata false`
    cases. `internal/parser/parser_test.go` and
    `pdfviewer_render_test.go` add end-to-end coverage confirming a real
    encrypted fixture opens, decrypts its content stream and `/Info`
    string correctly, and renders pixel-identically to its unencrypted
    equivalent. The existing `FuzzOpenAndResolveAll` target picks up the
    new fixtures as seeds automatically (no code change needed) and ran
    clean (8M+ executions, no panics) against the new decryption code
    paths.
- **What's carried forward.** Phase 7b (a non-empty, caller-supplied
    password) is unimplemented, as scoped - see this document's Phase 7b
    bullet and `docs/capability-matrix.md`'s Encryption section. The
    `/Crypt` stream filter and public-key security handlers remain
    explicitly out of this phase's scope, as originally planned.

### Phase 7b: Standard security handler, non-empty user password — done (2026-09-09)

- **Password-input API.** Added `WithPassword(string)` to the root
    package's `OpenOption`s (options.go), matching the shape
    `docs/PLAN.md`'s Draft Public API section had sketched. It threads
    down through a new, parallel `internal/parser.OpenOption`/
    `internal/parser.WithPassword` (parser.go) - `internal/parser.Open`
    gained a `opts ...OpenOption` parameter, backward compatible with
    every existing call site since it is variadic - to
    `internal/crypt.New`'s new `password string` parameter.
- **`internal/crypt` password support.** `New`'s key-derivation calls
    (`ComputeFileKey` for revisions 2-4, `ComputeFileKeyR56` for 5-6)
    already accepted an arbitrary password byte slice - Phase 7a simply
    always passed nil/empty. The only genuinely new code is
    `password.go`'s `encodePassword`, converting a caller's Go string
    into the bytes each revision's algorithm expects: revisions 2-4 use
    an approximation of PDFDocEncoding (exact for ASCII/Latin-1
    passwords, substituting `?` for anything outside that range, a
    documented simplification - see that function's doc comment);
    revisions 5-6 use the password's own UTF-8 bytes truncated to 127
    bytes per ISO 32000-2 §7.6.4.3.4, without the full SASLprep (RFC
    4013) normalization the specification technically also calls for
    (exact for any password SASLprep would not itself alter, which
    covers ordinary ASCII text - another documented simplification, the
    same kind this project makes elsewhere, e.g. LZWDecode's
    `/EarlyChange 0` gap). `ComputeAES256UserStrings` (hash56.go) gained
    a `password` parameter for the same reason its revision 2-4
    counterparts already had one - so tools/genfixtures and this
    package's own tests could build a real, correctly-passworded
    fixture.
    - **Deliberately out of scope:** recovering a user password from a
        supplied *owner* password (ISO 32000-1 Algorithm 7) - this phase
        only tries a caller-supplied password as the document's *user*
        password, matching the phase's own title. See `New`'s doc
        comment.
- **Error message, not a new sentinel.** `internal/parser.setupEncryption`
    now distinguishes "no password was supplied" from "the supplied
    password was wrong" only in the wrapped error's message text - both
    still classify as `ErrEncrypted` via `errors.Is`, keeping this
    project's four-sentinel error taxonomy (root package's errors.go)
    intact rather than adding a fifth category for a distinction a
    message string already conveys.
- **Fixtures.** `tools/genfixtures` gained
    `buildEncryptedPasswordAES128`/`buildEncryptedPasswordAES256`,
    byte-for-byte identical to Phase 7a's `buildEncryptedAES128`/
    `buildEncryptedAES256` except for a shared non-empty
    `encryptedFixturePassword` constant used as the user password -
    `buildEncryptedFilledRect` and the AES-256 fixture builder were both
    refactored to take a `password` parameter (defaulting to `""` for
    the pre-existing Phase 7a callers) rather than duplicating the whole
    fixture-building function a second time.
- **Tests.** `internal/crypt/handler_test.go` gained
    `TestNewR234WithNonEmptyPassword`/`TestNewR56WithNonEmptyPassword`
    (correct password opens and decrypts; empty or wrong password each
    fail with `ErrWrongPassword`) and `password_test.go` covers
    `encodePassword`'s ASCII/non-ASCII/truncation behavior directly.
    `internal/parser/parser_test.go`'s
    `TestOpenEncryptedDocumentNonEmptyPasswordDecrypts` and the root
    package's `TestOpenFileWithPasswordSucceedsOrFails` (plus
    `pdfviewer_render_test.go`'s
    `TestRenderEncryptedWithPasswordMatchesPlainContent`) add the same
    "correct password succeeds; wrong or missing password fails with
    `ErrEncrypted`" coverage at the `Document.Resolve`, `Open`/
    `OpenFile`, and full-page-rendering layers respectively. Full test
    suite, `go vet`, the race detector, and a 20s/7M+-execution run of
    the existing `FuzzOpenAndResolveAll` target (which picks up the new
    fixtures as seeds automatically) all pass clean.
- **What's carried forward.** Owner-password recovery of a user
    password (Algorithm 7) remains out of scope, as does the `/Crypt`
    stream filter and public-key security handlers - see this phase's
    "Deliberately out of scope" bullet above and
    `docs/capability-matrix.md`'s Encryption section. With 7a and 7b
    both done, Phase 7 as a whole is complete.

### Phase 8a: JBIG2 MQ arithmetic coder — done (2026-09-09)

- **`internal/filter/jbig2mq.go` (new).** A from-scratch Go
    implementation of the MQ-coder, the binary arithmetic coder ITU-T
    T.88's JBIG2 arithmetic decoding procedures are built on (the same
    coder T.82 and, under another name, JPEG 2000 use - but this project
    needs it only for JBIG2, so it lives in `internal/filter` rather than
    a shared location):
    - `qeTable`, T.88 Table E.1's 47-state probability-estimation state
        machine, and `mqContext`, one adaptive probability estimator
        (whose Go zero value is deliberately the correct initial state,
        so a freshly-made slice of them needs no explicit setup).
    - `mqDecoder` (`newMQDecoder`/`decodeBit`/`byteIn`/`renormalize`),
        a direct translation of Annex E's INITDEC, DECODE (with
        MPS_EXCHANGE/LPS_EXCHANGE inlined), BYTEIN and RENORMD
        procedures, including the "pad a short stream with an endless run
        of 0xFF" convention that makes truncated input decode to garbage
        rather than panic.
    - `mqEncoder` (`newMQEncoder`/`encodeBit`/`renormalize`/`byteOut`/
        `flush`) plus the `encodeMQSequence` convenience wrapper: the
        forward direction, from the same annex's CODEMPS, CODELPS,
        RENORME, BYTEOUT and FLUSH procedures. It is test- and
        fixture-only (never reachable from real PDF decoding) and exists
        for the same reason `internal/crypt/hash56.go`'s forward AES-256
        functions do - this project has no independently-produced
        real-world JBIG2 sample to validate the decoder against, so
        encoding a known bitmap and decoding it back is the strongest
        verification available. Writing the two directions from opposite
        ends of the standard's own description, rather than deriving one
        from the other, is what makes a round trip real evidence both are
        right instead of evidence they share an assumption.
- **Tests (`internal/filter/jbig2mq_test.go`, new).** Round-trip
    coverage: all-zeros, all-ones and alternating decisions through a
    single context; every decision against its own untouched context;
    skewed random bits through 1/2/5/16 shared contexts; empty and
    single-decision sequences; and the ctxIndex/bit length-mismatch
    panic. `TestMQRoundTripManyShapes` adds a 200-seed randomized sweep
    over lengths, 0/1 balances and context counts specifically to
    exercise `byteOut`'s carry-propagation and 0xFF bit-stuffing paths,
    and asserts that a healthy share of the streams it produces actually
    contain an 0xFF byte, so those paths cannot silently stop being
    covered. `TestMQRoundTripHighlyCompressible` round-trips 200,000
    identical decisions and checks they encode into a near-constant
    handful of bytes (they compress to 2), confirming the coder really
    does reach qeTable's confident states rather than merely being
    lossless. `go vet` and the full `internal/filter` suite pass clean.
- **What's carried forward.** An earlier attempt built the encoder as a
    search - choose each output byte by asking the decoder whether the
    bytes so far already reproduce every decision they determine - on the
    theory that decoding's strictly-forward nature made backtracking
    unnecessary. That reasoning was wrong (a locally-consistent prefix
    need not be extensible to a full solution) and even with real
    backtracking added the search still dead-ended within a few bytes on
    sequences a correct coder handles easily. It was replaced wholesale
    by the standard's own carry-propagating BYTEOUT construction above,
    which is both correct and O(n) rather than O(256n) per byte - the
    latter mattering because 8c's fixtures encode thousands of pixels.

### Phase 8b: JBIG2 segment parsing and generic-region decoding — done (2026-09-09)

- **`internal/filter/jbig2segment.go` (new).** Segment header parsing
    (T.88 clause 7.2): segment number, type, and data length, correctly
    skipping the variable-width parts a generic-region decoder never
    needs to interpret (short- and long-form referred-to segment counts
    and their retention flags, the referred-to segment numbers - whose
    width depends on the *referring* segment's own number - and the 1- or
    4-byte page association). A long-form count is a 29-bit field, so it
    is bounded against the remaining data length before any byte count is
    derived from it, which also keeps the arithmetic from overflowing
    `int` on a 32-bit platform.
- **`internal/filter/jbig2generic.go` (new).** The generic region
    decoding procedure (T.88 6.2):
    - `codingTemplates`/`defaultATPixels`/`genericContextTemplate` build
        each GBTEMPLATE's context-pixel layout. The bit ordering is
        produced by sorting the fixed and adaptive template pixels into
        raster order, which yields exactly the numbering T.88's own
        template figures assign whenever the adaptive pixels sit at their
        default positions - the only arrangement this decoder accepts.
    - `parseRegionInfo` (T.88 7.4.1) reads each region's size, position
        and combination operator, bounding size *and* position per axis so
        that neither an absurd dimension nor an absurd position can drive
        an oversized allocation.
    - `decodeGenericRegionSegment` (7.4.6) parses the segment's own flags
        and AT pixels, rejecting MMR coding and non-default AT positions
        as `ErrUnsupported` rather than mis-decoding them, and
        `decodeGenericBitmap` runs the per-pixel arithmetic decode
        including TPGDON typical prediction (a row identical to the one
        above coded as a single bit).
    - `EncodeJBIG2GenericRegion`/`encodeGenericRegionSegment`/
        `buildGenericRegionSegment`/`buildSegmentHeader`: the matching
        encode side, test- and fixture-only for the reasons Phase 8a's
        entry gives, covering both typical-prediction settings and
        arbitrary region positions and combination operators.
- **`internal/filter/jbig2.go` (new).** `decodeJBIG2`, the filter entry
    point: walks the segment stream (PDF's "embedded organization", T.88
    Annex D.3), composites every generic region onto a page bitmap sized
    to what the regions actually painted, ignores the segment types a
    single-page generic-region bitmap does not need (page info,
    end-of-page, end-of-stripe, end-of-file, tables, extensions), and
    reports symbol dictionary, text, halftone and refinement region
    segments as `ErrUnsupported` naming the type. `jbig2Bitmap` keeps one
    byte per pixel while decoding (random access matters more than
    compactness there) and `packInverted` produces the bit-packed,
    MSB-first output the image pipeline expects - inverting as it goes,
    since JBIG2 defines 1 as black while a `/BitsPerComponent 1`
    DeviceGray sample's 0 is black.
- **`internal/filter/filter.go` wiring.** `decodeOne` now dispatches
    `"JBIG2Decode"` to `decodeJBIG2`; `TestDecodeUnsupportedFilter`
    switched to `JPXDecode` as its still-unsupported example.
- **Tests (`internal/filter/jbig2_test.go`, new).** Round trips across
    five bitmap sizes (including 1x1, and widths that are not multiples
    of 8) times six patterns times both typical-prediction settings;
    typical prediction actually compressing an image of repeated rows;
    the round trip through the public `Decode` entry point, confirming
    the filter dispatch wiring; three stacked region segments
    compositing into one page; ignorable segment types being skipped;
    thirteen malformed or unsupported streams each asserted to produce
    the right error class; a region positioned past the page limit; and
    truncated coded data neither panicking nor hanging.
    `TestJBIG2ContextTemplatesMatchSpecifiedBitLayout` pins all four
    templates' exact context bit ordering against the specification's
    figures - deliberately, because that is the one property round-trip
    testing structurally cannot check: encoder and decoder share
    `genericContextTemplate`, so a permuted bit order would round-trip
    perfectly here while decoding every real-world JBIG2 stream to
    noise. `FuzzDecode` gained two JBIG2 seed streams (without them a
    random byte string essentially never parses as a valid segment
    header, so the fuzzer would never reach the arithmetic decoder);
    a 45-second, 14M-execution run found no panic or hang. Full test
    suite, `go vet` and the race detector pass clean.
- **What's carried forward.** Symbol dictionary and text region
    segments (JBIG2's highest-compression mode for large text scans),
    halftone and refinement regions, MMR-coded generic regions,
    non-default AT pixel positions, and unknown-length segments all
    report `ErrUnsupported` naming the specific feature. `/JBIG2Globals`
    is consequently not consulted either: a stream that needs it
    necessarily uses symbol/text regions, which stop this decoder
    first. Phase 8c still owes the `tools/genfixtures` builder, an
    end-to-end render test, and the `docs/capability-matrix.md` update.

### Phase 8c: JBIG2 fixture, render test and documentation — done (2026-09-09)

- **`tools/genfixtures` (`buildImageJBIG2`, new fixture
    `image-jbig2.pdf`).** A 100x100-point page painting a referenced
    image XObject with `/Filter /JBIG2Decode`: a 32x32 bilevel bitmap
    whose top-left quadrant is black and whose other three quadrants are
    white, inside a one-pixel black border, encoded with typical
    prediction enabled. The shape is deliberately asymmetric in both
    axes *and* in black versus white, so a rendering test against it
    fails under a horizontal flip, a vertical flip, or an inverted
    black/white mapping - the three mistakes a bilevel image pipeline
    most easily makes. The JBIG2 bytes come from `internal/filter`'s own
    encoder at fixture-build time, so unlike `image-jpeg.pdf` (whose
    bytes depend on the standard library's JPEG encoder) this fixture is
    fully reproducible from this project's own code.
- **End-to-end tests.** `TestRenderJBIG2Image`
    (`pdfviewer_image_test.go`) renders the fixture through the public
    API and asserts each quadrant's color and three border points;
    `image-jbig2.pdf` was added to
    `TestRenderMatchesReferenceImages`'s list, with the golden PNG
    checked in at `testdata/renderrefs/image-jbig2.png`. Together these
    exercise the whole path - cross-reference and object resolution,
    `internal/filter`'s JBIG2 decoding, `internal/image`'s 1-bit sample
    handling, and rasterization - rather than only `internal/filter`'s
    own unit tests. `internal/filter`'s `FuzzDecode` gained two JBIG2
    seed streams (14M executions clean); the root package's
    `FuzzOpenAndRender` picks the new fixture up as a seed
    automatically (10M executions clean). Full test suite, `go vet` and
    the race detector all pass.
- **Documentation.** `docs/capability-matrix.md`'s JBIG2Decode filter
    row moved from "Not scheduled / Not started" to "Phase 8 / Partial",
    listing what is implemented and naming every deliberately
    unimplemented JBIG2 feature; its images row was split so JBIG2 and
    JPX no longer share one entry, and the JPXDecode rows now point at
    Phase 14 rather than saying "same rationale as JBIG2Decode" (a
    rationale that no longer applies to JBIG2). `FIXTURES.md` documents
    the new fixture.
- **What's carried forward.** Phase 8 is complete for generic-region
    coding. Symbol dictionary and text regions remain the one JBIG2
    feature likely to matter in practice (they are how the highest
    compression ratios on large text scans are achieved); a stream using
    them fails cleanly with `ErrUnsupported` naming the segment type, so
    the gap is visible rather than silent, and a follow-on phase can add
    them if real-world files turn out to need it.

### Phase 8d: JBIG2 arithmetic integer decoding procedures — done (2026-09-09)

- **What prompted the continuation.** 8c's carried-forward note said a
    follow-on phase could add symbol/text regions "if real-world files
    turn out to need it". The first real-world JBIG2 file this project
    was handed - a Xerox WorkCentre 5030 scan - needed it: a symbol
    dictionary in `/JBIG2Globals` and two text regions, failing outright
    with the `ErrUnsupported` 8b had deliberately put there. That is the
    scope decision 8d-8h implement.
- **`internal/filter/jbig2arith.go`.** T.88 Annex A's integer decoding
    procedure (A.2 - a sign bit, a prefix selecting one of six magnitude
    ranges, then that range's value bits, every decision against a
    context selected by the bits of the same integer decoded so far) and
    symbol-ID procedure (A.3). Both directions, for the same round-trip
    reason 8a's MQ coder has both, though the encoding direction is not
    in the standard at all - it is derived here as the decoder's exact
    inverse. Magnitudes are bounded well below the 2^32 the largest
    range could spell out, since anything near that neither fits an
    `int` on a 32-bit platform nor describes any real image.
- **Tests.** Round trips over every Table A.1 range boundary, the "OOB"
    out-of-band sentinel mixed with real zeros (it is spelled as
    negative zero, so confusing the two is the obvious failure), and
    2000 random values through one shared, adapting context set. One
    test instead drives the decoder with a bit sequence written out
    literally from the standard's table, so a shared misreading of the
    table itself would not pass unnoticed.

### JBIG2 adaptive-template pixel positions — done (2026-09-09)

- **Why here.** 8b accepted only the default adaptive-template (AT)
    pixel positions, because it derived each template's context bit
    layout by sorting the combined fixed and adaptive points into raster
    order - which reproduces T.88's own bit numbering exactly when the
    AT pixels sit where the template figures draw them, and not
    otherwise. Symbol dictionaries in real files do move them, so this
    had to be fixed before 8e rather than after.
- **What changed.** `codingTemplates` now spells each template's bit
    layout out as an ordered list of slots, each either a fixed neighbor
    or an AT index, so a moved AT pixel keeps its assigned bit position
    while reading from its new location. The generic bitmap coding loops
    also split out from the segment-level code in both directions, so a
    decoder and its context set can be passed in - which 8e needs, since
    a symbol dictionary decodes many small bitmaps from one continuing
    stream with one continuously-adapting context set.
- **Tests.** All four templates round-trip with both default and moved
    AT positions. The existing bit-layout test can only describe the
    default case (a moved pixel has no simple "read it off the figure"
    expected form), and now says so explicitly rather than appearing to
    cover more than it does.

### Phase 8e: JBIG2 symbol dictionary and text region decoding — done (2026-09-09)

- **`internal/filter/jbig2symbol.go`** (T.88 7.4.3 and 6.5). Symbols are
    grouped into height classes and coded as differences between
    consecutive heights and widths, with every symbol bitmap decoded
    from one continuing stream through one continuing context set - that
    sharing is most of why a dictionary of similar glyphs compresses so
    well. Also the export-flag runs (6.5.10) deciding which of the
    imported and newly-decoded symbols a dictionary passes on.
- **`internal/filter/jbig2text.go`** (T.88 7.4.4 and 6.4). The strip
    walk placing symbol instances by index and positional delta, all
    four reference corners, and transposed regions. The one subtlety
    worth recording: untransposed, a symbol's left edge sits at S
    regardless of which corner is the reference, because for a
    right-hand corner T.88 advances S past the symbol *before* placing
    it rather than after - so only the vertical placement actually
    differs between corners.
- **Segment registry.** Segment headers now record their referred-to
    segment numbers (8b parsed them only to know how many bytes to skip
    past), and `decodeJBIG2` keeps a registry keyed by segment number,
    since a text region names the dictionaries whose symbols it draws
    with and a dictionary may import another's.
- **Tests.** The expected page is built by plain compositing, sharing no
    code with the text region decoder, so a misread position, symbol ID
    or strip boundary is caught even though the encoder and decoder do
    share the coding tables. Covers all four GBTEMPLATEs, dictionary
    chaining, every reference corner, transposed placement, and multi-row
    strips.

### Phase 8f: JBIG2 `/JBIG2Globals` — done (2026-09-09)

- **The dependency problem.** A shared symbol dictionary lives in a
    separate stream the image's `/DecodeParms` names by indirect
    reference, and `internal/filter` cannot follow one - that needs a
    cross-reference table, which is `internal/parser`'s job. So the
    filter package declares a `StreamResolver` interface and a
    `DecodeWith` entry point, and `parser.Document` satisfies it,
    keeping the dependency pointing the way it already did. `Decode`
    keeps its signature for the callers with no document to resolve
    against.
- **Recursion.** `Document.DecodeReferencedStream` deliberately decodes
    the second stream *without* passing itself along again, so a file
    whose globals stream names itself cannot recurse without end.
    Nothing legitimate needs the chain: a globals stream is
    Flate-compressed or raw, never JBIG2-coded itself.

### Phase 8g: JBIG2 generic refinement regions — done (2026-09-09)

- **`internal/filter/jbig2refine.go`** (T.88 6.3 and 7.4.7). Both
    GRTEMPLATEs, their adaptive pixels, and TPGRON typical prediction
    (where a pixel whose 3x3 reference neighborhood is uniform is not
    coded at all). Wired into all three of its users: standalone
    refinement region segments, a text region's per-instance SBREFINE,
    and a symbol dictionary's SDREFAGG - including the aggregate form,
    where a new symbol is a collage of existing ones coded as a
    miniature text region sharing the dictionary's own stream and
    contexts. That sharing is why the text region's contexts became a
    named group passed in rather than a dozen locals.
- **The test that matters most.** Round trips prove the two directions
    agree, but would not notice a decoder that ignored the *reference*
    half of the context entirely, since both directions would ignore it
    together. So one test asserts that refining against a
    near-identical reference really is smaller than coding the same
    bitmap from scratch - which is only true if the reference is being
    consulted.

### Phase 8h: JBIG2 fixtures, real-world sample and documentation — done (2026-09-09)

- **`image-jbig2-text.pdf`** (`tools/genfixtures`, `buildImageJBIG2Text`).
    A 40x40 symbol-mode image whose two-symbol dictionary lives in a
    separate `/JBIG2Globals` stream: a solid square in one quadrant and
    a hollow one in two others. The shapes differ in both size and
    interior, so swapping symbol IDs, misplacing an instance, dropping
    the dictionary's second height class, or inverting black and white
    each change a point `TestRenderJBIG2SymbolTextImage` asserts. As with
    `image-jbig2.pdf`, the bytes come from this project's own encoder, so
    the fixture stays reproducible from this project's own code.
- **The first real-world fixture** (`testdata/fixtures/real-world/`,
    a directory `FIXTURES.md` had reserved since Phase 0 and which was
    empty until now). `pdf-with-jbig2.pdf` is a Xerox WorkCentre 5030
    scan: a 55-symbol dictionary in `/JBIG2Globals`, a second 114-symbol
    dictionary in the image stream, and two text regions placing 1025
    instances from both dictionaries at once, with 4- and 8-row strips, a
    non-zero `SBDSOFFSET`, and `/Rotate 270`. `TestRenderRealWorldJBIG2Sample`
    renders it against a golden image.
    
    This is worth more than its size suggests. Every JBIG2 fixture
    before it was built by this package's own encoder, and a round trip
    between an encoder and decoder written by the same project proves
    they agree with each other, not that either matches T.88. This is
    the first test here that could catch that class of error. Its
    provenance and license are recorded in `FIXTURES.md` as Phase 0
    requires.
- **Bounded work, found by fuzzing.** `FuzzOpenAndRender` produced a
    failure whose saved input did not reproduce on its own, which is the
    signature of resource exhaustion rather than a crash: symbol mode's
    first limits were too loose. A stream of a few dozen bytes could
    declare thousands of symbols and simply end, leaving the arithmetic
    decoder to invent their dimensions from its end-of-data padding -
    about a second of work and tens of megabytes, multiplied by every
    parallel fuzz worker. Three bounds were tightened or added: a
    dictionary's total symbol area (a glyph is not a page, so the
    whole-page limit was the wrong scale), a text region's total
    composited area (the instance count alone does not bound work, since
    each instance costs its symbol's area), and the number of height
    classes a dictionary may spend without accounting for its declared
    symbols (a class need not contain any symbols, so the loop could not
    be required to make progress otherwise). Worst-case time for a
    hostile input dropped about fourfold, and the fuzzer's throughput
    stopped collapsing on slow inputs - 12M executions on
    `internal/filter`'s target and 95M on the root package's, both clean.
    `TestJBIG2SymbolModeBoundsHostileInput` pins each bound.
- **Documentation.** `docs/capability-matrix.md`'s JBIG2 rows list
    everything 8d-8h added and name every remaining unimplemented
    feature; `FIXTURES.md` documents both new fixtures and the
    `real-world/` section's rules; `.gitattributes` marks the new
    directory binary, for a stronger reason than the generated corpus -
    those files cannot be regenerated at all.
- **What's carried forward.** Huffman-coded symbol dictionaries and text
    regions (including the custom table segments and MMR collective
    bitmaps they imply), halftone regions and pattern dictionaries,
    MMR-coded generic regions, symbol dictionaries importing another
    segment's arithmetic contexts, and unknown-length segments all still
    report `ErrUnsupported` naming the specific feature. Huffman coding
    is the most plausible of these to meet in the wild, and would be the
    natural next sub-phase if a real file ever needs it - the same
    "wait for a real file" judgment 8c made about symbol mode, which
    turned out to be needed within a day.

### Phase 10: Text extraction (`/ToUnicode` CMap parsing) — done (2026-09-09)

- **`internal/fonts/tounicode.go` (new).** Parses a font's `/ToUnicode`
    CMap stream (ISO 32000-1 9.10.3): `beginbfchar` (a single code to a
    UTF-16BE destination string, decoded via `unicode/utf16` - possibly
    more than one Unicode character, the common way a ligature glyph such
    as "ffi" is given a `/ToUnicode` entry) and both `beginbfrange`
    shapes - `<lo> <hi> <dst>` ("increment a single destination value" by
    reinterpreting `dst` as a big-endian integer and adding each code's
    offset, the common real-world shape for a contiguous run of ordinary
    characters) and `<lo> <hi> [<d0> <d1> ...]` ("explicit destination per
    code", no arithmetic at all). Reuses `cidcmap.go`'s lexer-based
    parsing loop shape and its `beValue`/bound constants, but is otherwise
    a self-contained parser: unlike Phase 9's CID CMaps, a `/ToUnicode`
    CMap's own `usecmap` operator is deliberately left unimplemented (rare
    in practice for `/ToUnicode` specifically - see that file's own doc
    comment for the reasoning), so no `resolveUseCMap`-style callback
    machinery was needed here. `ToUnicodeMap.TextForCode` is keyed by the
    font's *raw character code*, never a CID - a Type0 font's `/Encoding`
    CMap (code -> CID) and `/ToUnicode` CMap (code -> text) are two
    independent mappings that both start from the same raw code, a
    distinction that file's package doc comment calls out explicitly
    since it is easy to get backwards.
- **`internal/fonts/font.go`: `Font.TextForCode` (new).** This package's
    one text-extraction-facing entry point, deliberately separate from
    `Glyph`/`Width` (painting/measuring) per this package's own doc
    comment's "text extraction is a separate capability" section (updated
    to describe what actually shipped). Tries the font's `toUnicode` field
    first (set by `loadToUnicode`, called from both `simple.go`'s
    `loadSimpleFont` and `cid.go`'s `loadType0Font` - `/ToUnicode` is a
    feature of any font dictionary, not just Type0's), then - for a
    simple font only - falls back to a new `simpleEncoding` field (the
    same `runeTable` `BuildSimpleEncoding` already computed for
    `spaceCodes`, now also kept on `Font` itself), covering the common
    case of a simple font using a standard encoding with no separate
    `/ToUnicode` stream at all. A Type0 font with neither answers
    ok=false, honestly, rather than guessing.
- **`internal/content/text_extract.go` (new): `ExtractText`.** A
    genuinely separate, much smaller interpreter from `interpret.go`'s
    text-*painting* one - not a mode flag threaded through it - per
    `docs/PLAN.md`'s original Phase 4 plan to keep extraction separate
    from painting. It understands only `q`/`Q`/`cm` (for the CTM,
    reusing `graphics.Stack`/`graphics.State` directly for free push/pop
    of the seven text-state fields already living there) and the
    text-object/state/positioning/showing operators; every other operator
    (paths, painting, images, shadings, `gs`, `Do`, marked content) is
    silently skipped. This is a deliberate, useful difference from
    `Interpret`: a content stream mixing perfectly extractable text with
    some entirely unrelated unsupported feature (an unsupported image
    color space, say) still extracts its text cleanly, where `Interpret`
    itself would fail outright - `TestExtractText_UnsupportedContentDoesNotError`
    pins exactly this contrast using the same fixture
    `TestInterpretDoWithUnsupportedImageFeaturePropagatesError` uses to
    prove `Interpret` *does* fail on it. Font resolution (`resolveFont`,
    factored out of `text.go`'s `lookupFont` so both files call the same
    code) and the text-positioning formula (`textRenderingMatrix`,
    `text.go`'s existing package-level function, called directly on
    glyph-space (0,0) to get each glyph's baseline origin) are the two
    things this file deliberately does *not* duplicate, since painting
    and extraction must agree on both exactly.
- **Public API: `Page.Text`.** New root-package file `text.go` defines
    `TextGlyph` (`Text`, `X`/`Y`, `Width`, `FontSize`); `page.go`'s new
    `Page.Text` method calls `content.ExtractText` with
    `graphics.Identity()` as its initial CTM (unlike `Render`'s
    device-pixel CTM - extraction has no inherent target resolution), so
    every glyph's position comes back in the same page-default-user-space
    PDF points `Page.Bounds` reports.
- **Fixtures.** `tools/genfixtures/text.go` gained
    `buildTextToUnicodeSimple` (`text-tounicode-simple.pdf`: "ABC" in a
    simple TrueType font, `/ToUnicode` exercising both a plain `beginbfchar`
    entry and a `beginbfrange` array-form entry) and
    `buildTextToUnicodeType0` (`text-tounicode-type0.pdf`: structurally
    `text-type0-identity.pdf` plus a `/ToUnicode` CMap mapping code 1 to
    U+5B57 "字" - deliberately non-ASCII and non-coincidental, so a
    passing test can only mean the CMap was actually parsed and
    consulted). Both were added to `TestRenderMatchesReferenceImages`
    with checked-in golden PNGs, even though `/ToUnicode` has zero effect
    on rendering, matching this project's established per-fixture
    regression-coverage convention.
- **Tests.** `internal/fonts/tounicode_test.go` covers `parseToUnicodeCMap`
    in isolation (bfchar, both bfrange shapes, ligature and surrogate-pair
    destinations, bfchar-over-bfrange precedence, malformed-input
    tolerance, nil-receiver safety); `font_test.go` gained
    `TestLoad_SimpleFontToUnicode`/`TestLoad_SimpleFontToUnicodeFallsBackToEncoding`/
    `TestLoad_Type0ToUnicode` confirming `Font.TextForCode` end to end
    through `Load`. `internal/content/text_extract_test.go` covers
    `ExtractText`'s operator handling in isolation (positions, widths,
    `cm`/`q`/`Q` CTM tracking, `TJ` numeric adjustments not emitting a
    glyph, no-font-selected tolerance, the unsupported-content contrast
    with `Interpret` above, and malformed-`Tf`-still-errors). Root-package
    `pdfviewer_text_test.go` gained `TestPageTextSimpleFont`/
    `TestPageTextType0Font` (Phase 10's own stated exit criteria) and
    `TestPageTextType0PredefinedEncodingFallsBackToNoText` (a Type0 font
    with no `/ToUnicode` at all still yields a correctly positioned glyph
    with empty text, not an error or a wrong guess). Full test suite,
    `go vet`, and the race detector all pass clean; regenerating every
    `tools/genfixtures` fixture reproduced every existing file
    byte-for-byte except the two new fixtures added.
- **What's carried forward.** `ExtractText` does not recurse into a Form
    XObject's own content stream - text painted only inside a form is not
    extracted (a documented scope limitation, not an oversight; see
    `text_extract.go`'s own doc comment). A `/ToUnicode` CMap's `usecmap`
    operator is not resolved (see `tounicode.go`'s doc comment). Type0/CID
    font substitution (this document's Backlog section) is the natural,
    still-unscheduled follow-on Phase 10 unblocks: `Font.TextForCode` can
    now answer "what Unicode rune does this Type0 code mean" for a font
    with a `/ToUnicode` CMap, but `cid.go`'s `loadType0Font` does not yet
    use that answer to attempt substitution the way `simple.go` does for
    simple fonts.

### Phase 9a: CMap parsing core — done (2026-09-09)

- **`internal/fonts/cidcmap.go` (new).** A from-scratch parser for PDF's
    CMap grammar (ISO 32000-1 9.7.5, a small PostScript-like subset)
    built on `internal/syntax`'s existing token `Lexer` - the same lexer
    `internal/content`'s own content-stream `Parse` drives directly,
    reused here for an unrelated grammar built from the same tokens
    (hex strings, names, integers, bare keywords). Covers
    `begincodespacerange`/`begincidrange`/`begincidchar` and `usecmap`
    chaining (a `parent` `*CMap`, checked only after this CMap's own
    tables miss - matching the specification's "supplements, does not
    replace" wording), plus `CMap.decode`, splitting a shown string into
    codes per the codespace's own (possibly mixed) byte lengths rather
    than a single fixed width. Every parse failure - truncated input, a
    missing operand, an oversized or malformed hex string, an unresolved
    `usecmap` name - is tolerated (that entry, or the whole CMap, is
    simply absent from the result) rather than surfaced as an error, the
    same policy this package already applies to every other malformed
    PDF field it reads.
- **Not yet wired into any `Font`** - this sub-phase is the parser and
    its own unit tests (`cidcmap_test.go`) only, deliberately kept
    independently testable (a `[]byte` in, a `*CMap` with plain methods
    out, no PDF document structure involved) before 9b makes `cid.go`
    depend on it.

### Phase 9b: wire embedded CMap `/Encoding` streams into Type0 fonts — done (2026-09-09)

- **`cid.go`'s `loadType0Encoding` (new).** Resolves a Type0 font's
    `/Encoding` entry for real, replacing the previous unconditional
    "assume Identity-H/V" behavior: the names `Identity-H`/`Identity-V`
    still take the existing fast path (no CMap needed, code == CID by
    definition), and a `Stream` value - an embedded CMap - is now
    filter-decoded and parsed via 9a's `parseCMap`, attached to the
    `Font` as its `cmap` field. A predefined CJK encoding *name* (e.g.
    `UniGB-UCS2-H`) is handed to `predefinedCMapFor`
    (`predefined_cmap.go`, new - see below), which returns `ok=false`
    for every caller until a further sub-phase exposes a way to actually
    configure one, so this case's behavior is unchanged from before this
    phase: 2-byte codes assumed, `notdefGlyph` for every code.
- **`font.go`: `Font.DecodeCodes` and `Font.cidFor` (new).** Splitting a
    shown string into codes (previously `internal/content/text.go`'s own
    `decodeCodes`, driven by the public `TwoByteCodes` bool) moved onto
    `Font` itself as `DecodeCodes`, returning a `[]DecodedCode` pairing
    each code with how many raw bytes it consumed - needed because a
    CMap's codespace can mix byte widths, and because PDF's word-spacing
    rule cares specifically about a *single-byte* code 32, not just any
    code whose value happens to be 32. `cidFor` is the identity function
    for every font except one with a real `cmap` attached, for which it
    resolves a raw code to its actual CID (falling back to CID 0/.notdef
    for a code the CMap says nothing about) before `Width`/`Glyph` index
    their CID-keyed data - so Identity-H/V and simple fonts are
    completely unaffected by this refactor.
- **`internal/content/text.go`** updated to call `font.DecodeCodes`
    instead of its own removed `decodeCodes`, and to gate word spacing on
    `DecodedCode.Bytes == 1` instead of `!font.TwoByteCodes`.
- **`predefined_cmap.go` (new, `internal/fonts`).** Defines the
    extension point a later sub-phase will expose publicly:
    `CMapSource` (one method, `CMapData(name) ([]byte, bool)`) and
    `CMapSourceProvider`, mirroring `substitute.go`'s
    `FontSource`/`SubstitutionProvider` pattern exactly (a `Resolver`
    that also structurally implements `CMapSourceProvider` - in
    practice, `*internal/model.Document` once wired - is asked for a
    configured source via `PredefinedCMapSource() any`, type-asserted
    back to `CMapSource` on this package's side). `newPredefinedCMapResolver`
    bounds how many chained `usecmap` resolutions it will follow
    (`maxUseCMapDepth`, 8) before giving up, since a cycle here spans
    multiple independent `parseCMap` calls that `parseCMap` itself has
    no way to detect - the reason this guard could not simply live
    inside `cidcmap.go`'s own (single-call, therefore cycle-free by
    construction) parsing loop.
- **Fixtures.** `tools/genfixtures` gained `buildTextType0EmbeddedCMap`
    (`text-type0-embedded-cmap.pdf`): structurally identical to the
    existing `text-type0-identity.pdf` (same embedded TrueType program,
    same rendered square, same size/position) except its `/Encoding` is
    an embedded CMap stream mapping the arbitrary code `0x1234` to CID 1
    instead of the literal name `Identity-H` - the two fixtures are
    asserted to render pixel-for-pixel identically
    (`TestRenderType0EmbeddedCMapTextMatchesIdentity`,
    `pdfviewer_text_test.go`) despite going through entirely different
    `cid.go` code paths, plus a checked-in golden PNG
    (`testdata/renderrefs/text-type0-embedded-cmap.png`).
- **Tests.** `cidcmap_test.go` (9a) covers codespace/cidrange/cidchar
    parsing, mixed-byte-length codespaces, `usecmap` chaining and its
    "supplements, doesn't replace" precedence, an unresolved `usecmap`,
    and a battery of malformed/truncated inputs. `font_test.go` gained
    `TestLoad_Type0EmbeddedCMap` (a full `Load` through an embedded CMap
    stream, confirming `DecodeCodes`/`Width`/`Glyph` all correctly
    translate a raw code through it, including the CID-0 fallback for an
    unmapped code). `predefined_cmap_test.go` covers the
    `CMapSourceProvider` wiring (mirroring `substitute_test.go`'s own
    coverage of `SubstitutionProvider`) and a deliberately cyclic
    `CMapSource` (`fuzz_test.go`'s new `cyclicCMapSource`) proving the
    `usecmap` depth guard actually terminates rather than recursing
    forever. `fuzz_test.go` gained `FuzzParseCMap`, exercised for both
    an ordinary CMap grammar and (via that same cyclic source, routed
    through the real depth-bounded resolver) the `usecmap` cycle case;
    an 8M-execution run found no panic or hang. Full test suite, `go
    vet`, and the race detector all pass clean; regenerating every
    `tools/genfixtures` fixture reproduced every existing file
    byte-for-byte except the one new fixture added.
- **What's carried forward.** Predefined CJK encoding names remain
    unresolvable until a further sub-phase exposes `CMapSource`
    publicly (a `pdfviewer.WithPredefinedCMaps` option, following
    `WithFontSubstitution`'s precedent) with a real, disk-backed
    `CMapSource` implementation and end-to-end fixtures/tests - see
    Phase 9's own remaining scope.

### Phase 9c: public API for predefined CJK CMap resolution — done (2026-09-09)

- **`internal/fonts/directory_cmap_source.go` (new).** `DirectoryCMapSource`,
    this package's only shipped `CMapSource`: recursively scans a fixed
    list of directories, indexing every regular file found by its own
    base name (predefined CMap resource files - Adobe's own, or the
    equivalent files inside a Ghostscript/poppler/TeX installation - have
    no extension and are conventionally named exactly after the CMap
    they contain, e.g. a file literally named "UniGB-UCS2-H"), reading a
    file's bytes only once actually requested via `CMapData`. Unlike
    `DirectorySource` (font substitution), there is no GOOS-gated default
    directory list - CMap resources are not part of any operating
    system's normal resource stack - so this is opt-in-with-explicit-
    directories only, and, like `DirectorySource`, scans the filesystem
    at most once per instance (cached thereafter).
- **`options.go`: `WithPredefinedCMaps`/`PredefinedCMaps` (new).**
    Mirrors `WithFontSubstitution`/`FontSubstitution` exactly (a single
    `Directories []string` field, no `DisableSystemDefaults` equivalent
    since there are no defaults to disable). `document.go`'s `Open`
    wires a configured `PredefinedCMaps` into a new
    `internal/fonts.NewDirectoryCMapSource`, attached via
    `internal/model.Document`'s new `SetCMapSource` - the exact
    `SetFontSource` counterpart, added alongside a `PredefinedCMapSource`
    method implementing `internal/fonts.CMapSourceProvider` structurally
    (mirroring `FontSubstitutionSource`/`SubstitutionProvider`).
- **No `cid.go`/`predefined_cmap.go` changes were needed** - 9b already
    built `predefinedCMapFor`/`CMapSourceProvider` anticipating exactly
    this wiring, so this sub-phase is purely the public-facing plumbing
    (`internal/model`, `options.go`, `document.go`) plus the concrete,
    disk-backed `CMapSource` implementation.
- **Fixtures.** `tools/genfixtures` gained `buildTextType0PredefinedEncoding`
    (`text-type0-predefined-encoding.pdf`): structurally identical to
    `text-type0-identity.pdf`/`text-type0-embedded-cmap.pdf`, but its
    `/Encoding` is the bare name `UniGB-UCS2-H` (a real predefined CJK
    encoding name; this project bundles none of Adobe's actual data for
    it). Used two ways: rendered with no option at all, it falls back to
    `notdefGlyph` exactly like `text-notdef-fallback.pdf`
    (`TestRenderType0PredefinedEncodingFallsBackToNotdefByDefault`,
    `pdfviewer_text_test.go`, plus a checked-in golden PNG showing that
    fallback box); rendered through the new public API with
    `WithPredefinedCMaps` pointed at a temporary directory containing a
    small, entirely project-owned file literally named "UniGB-UCS2-H"
    (never any of Adobe's real licensed data - the same
    fixture-independence policy this project's other tests already
    follow), it renders the real square glyph pixel-for-pixel identically
    to the Identity-H fixture
    (`TestRenderType0PredefinedEncodingWithConfiguredCMapSource`,
    `pdfviewer_predefinedcmap_test.go`, new).
- **Tests.** `directory_cmap_source_test.go` (new) covers exact-name
    matching, Adobe's own one-level-nested resource layout, a missing
    directory, and the one-time-scan caching behavior.
    `predefined_cmap_test.go` (from 9b, already covered the
    `CMapSourceProvider` wiring and `usecmap` cycle guard using a fake
    `CMapSource` - unchanged by this sub-phase). `pdfviewer_predefinedcmap_test.go`
    (new) adds the same three-shape end-to-end coverage
    `pdfviewer_fontsubstitution_test.go` established for
    `WithFontSubstitution`: opting in has no effect on documents that
    don't need it, a zero-value option is a documented no-op (no default
    directory list, unlike `FontSubstitution`'s useful zero value), and a
    correctly configured source actually changes rendering. Full test
    suite, `go vet`, and the race detector all pass clean; regenerating
    every `tools/genfixtures` fixture reproduced every existing file
    byte-for-byte except the one new fixture added.
- **What's carried forward.** With 9a-9c done, Phase 9 is complete for
    both embedded-CMap and (opt-in) predefined-name encodings. This
    package's own CMap grammar parser (`internal/fonts/cidcmap.go`) is
    built to be reused, unchanged, by Phase 10's `/ToUnicode` parsing
    (a different "begin...end" vocabulary over the same lexical grammar
    and `usecmap`/codespace machinery) - see that phase's own plan entry.

### Phase 11: Type 1 embedded font outline extraction — done (2026-09-09)

- **The sample turned out not to test this phase.** This phase's own
    user-supplied sample fixture (`sample-font--type-1.pdf`, kept only
    for local development per its licensing - see `FIXTURES.md`'s
    `real-world/` rules) has ten `/Subtype /Type1` font dictionaries,
    but every one of them embeds its program via `/FontFile3` as a
    CFF/Type1C program (already handled by Phase 4's `cff.go`), not a
    single genuine `/FontFile` Type 1 charstring program - a PDF font
    dictionary's `/Subtype /Type1` names the font *interface*, not which
    embedded program format backs it. This project therefore has no
    independently produced, freely redistributable real-world Type 1
    `/FontFile` sample at all; `internal/fonts/type1_encode.go`'s
    from-scratch forward encoder (the same "round-trip against an
    encoder this project also owns" strategy `internal/filter/jbig2mq.go`
    and `cff_test.go` already use) is this phase's only way to validate
    `type1.go`'s parser against real, structurally-correct input.
- **`internal/fonts/type1.go` (new).** eexec/charstring decryption
    (`decryptType1`, both keys per the Type 1 Font Format specification
    section 7.3), a PFA-style hex-armored eexec section detector and
    decoder (some real-world producers embed a font this way even inside
    a PDF, despite the PDF specification's own preference for raw
    binary), a small PostScript-source tokenizer (`type1Scanner`) that
    walks the decrypted Private dictionary's `/Subrs` and `/CharStrings`
    structure without needing a general PostScript interpreter, and a
    full Type 1 Charstring interpreter (`type1Interp`) - all
    path-construction operators, `hsbw`/`sbw` (Type 1's explicit
    width/sidebearing operators, which mean this format has none of
    Type 2's "maybe the first operand is actually a width" ambiguity),
    local subroutine calls with no index bias (unlike CFF), and the
    `callothersubr`/`pop` OtherSubrs idiom real font-creation tools
    emit for the flex feature (two very flat curves, encoded as seven
    `rmoveto` calls instead of ordinary curve operators - drawn as two
    real curves here, since this project never applies hinting) and
    hint replacement. `seac` is recognized and reported as one
    unavailable glyph, deliberately not composed - the same scope
    decision `cff.go` already made for its own implicit-`seac` `endchar`
    form, for the same reason (no StandardEncoding-code-to-name table
    this package has any other use for).
- **`internal/fonts/type1_encode.go` (new).** `EncodeType1FontProgram`,
    exported test/fixture-only forward encoder (mirroring
    `internal/filter`'s exported `EncodeJBIG2GenericRegion` for the same
    reason - `tools/genfixtures`, a separate module, needs it too) that
    assembles a complete, loadable Type 1 font program from a list of
    named, already op-encoded charstrings plus optional local
    subroutines, returning the `/Length1`/`/Length2` values a real
    `/FontFile` stream dictionary needs alongside it.
- **`simple.go` wiring.** `loadEmbeddedType1` (new) and
    `readType1FontFileStream` (new, resolving `/Length1`/`/Length2`
    - not read by `readFontFileStream`, which the self-describing
    `/FontFile2`/`/FontFile3` formats never needed) plug `/FontFile`
    into `loadSimpleFont`'s existing TrueType-then-CFF fallback chain as
    a third, final embedded-program attempt before font substitution;
    glyph lookup goes through the same `simpleRuneGlyphLookup` (name via
    `/Encoding`, then rune) CFF already uses, since Type 1's own
    built-in `/Encoding` table is out of scope for the same reason CFF's
    is (see `type1.go`'s doc comment).
- **Fixtures.** `tools/genfixtures` gained `buildTestType1FontProgram`
    (`type1.go`, new) and `buildTextSimpleType1` (`text.go`), producing
    `text-simple-type1.pdf` - structurally identical to
    `text-simple-truetype.pdf` (same page, size, position, and
    hand-derived expected device-space rectangle) but backed by a
    synthetic Type 1 program instead of a TrueType one, letting
    `TestRenderSimpleType1Text` (`pdfviewer_text_test.go`, new) assert
    the exact same pixels; also added to
    `TestRenderMatchesReferenceImages` with a checked-in golden PNG.
- **Tests.** `internal/fonts/type1_test.go` (new) covers a simple square
    glyph's exact bounds, `callsubr`/`return`, the flex idiom (asserting
    two real 16-segment flattened curves were drawn, not a straight-line
    shortcut, landing at the exact expected final point), `seac`
    reporting the glyph unavailable rather than composing or
    misinterpreting its operands, a PFA-style hex-armored eexec section,
    missing `/Length1`/`/Length2` (falls back to searching for the
    literal `eexec` keyword), a custom `/FontMatrix`, and
    `decodeType1Number`'s every encoded-integer form including its
    truncated-input failure cases. `FuzzParseType1Font`
    (`fuzz_test.go`) covers the same "never panics, always terminates"
    property `FuzzParseCFFFont` established for Type 2 charstrings,
    seeded with a plain square glyph and a flex-plus-`callsubr` glyph;
    21s/4M+ executions found no panic or hang. Full test suite, `go
    vet`, and `gofmt` all pass clean; regenerating every
    `tools/genfixtures` fixture reproduced every existing file
    byte-for-byte except the one new fixture added.
- **What's carried forward.** A Type 1 font's own built-in `/Encoding`
    array and `seac` composition remain unimplemented, matching CFF's
    identical, already-documented gaps - see `docs/capability-matrix.md`'s
    updated Type 1 row. A `/FontFile` stream binding its "read N binary
    bytes" private procedure to a name other than the canonical `RD`/`-|`
    (essentially unseen in real-world output, per `isType1RDToken`'s doc
    comment) fails closed to `notdefGlyph` for that font rather than
    misparsing it.

### Phase 12: AcroForm field appearance regeneration — done (2026-09-10)

- **`internal/acroform` (new package).** Given a widget annotation
    `internal/annotation` already reported has no usable existing
    appearance, generates one from the field's current value:
    - `field.go`: `Resolve`/`ResolveWithAcroForm` walk a widget's
        `/Parent` chain merging inheritable AcroForm attributes (`/FT`,
        `/Ff`, `/V`, `/DA`, `/Q`, 12.7.3.2), falling back to the AcroForm
        dictionary's own `/DA`/`/DR` (12.7.3.3's documented default) -
        this half shipped as Phase 12a.
    - `da.go`'s `ParseDA` (Phase 12b) reuses `internal/content.Parse`
        directly against a `/DA` string, since its grammar *is* ordinary
        content-stream operator syntax - reading out just the `"Tf"`
        operator's font name and size, needed to resolve `/DA`'s
        "auto-size" `0` into a real size even when the producer's own
        size was already nonzero.
    - `textfield.go`/`measure.go`/`textstring.go`/`serialize.go` (Phase
        12c) build a generated Form XObject's content stream for a text
        field (`/FT /Tx` - single-line, with `shrinkToFitWidth`'s exact
        (width scales linearly with size) horizontal fit correction, and
        basic word-wrapped, height-truncated multiline) or a choice
        field shown as plain text (`/FT /Ch`, always the field's first
        selection for a multi-select list, never through `/Opt`):
        `encodeForFont` converts a field's decoded `/V` into the
        specific `/DA` font's own byte encoding (inverting
        `internal/fonts.BuildSimpleEncoding`'s code-to-rune table) so the
        generated `"Tj"` operand means what it says once
        `internal/content`'s own unmodified text-showing code reads it
        back; `nonFontOperatorsSource` splices `/DA`'s own color
        operator through verbatim while this package re-derives its own
        `"Tf"`. Password fields (`/Ff` bit 14) never get a generated
        appearance at all, matching Acrobat's own refusal to paint a
        password's plaintext.
    - `checkbox.go` (folded into 12c, since the dispatcher needed both
        paths anyway) generates a solid inset square for a checked
        non-pushbutton button field (`/FT /Btn`), plus an `/MK`-driven
        border/background - deliberately not the specific dingbat
        character Acrobat itself would draw, and deliberately not
        distinguishing a checkbox from a radio button visually; see that
        file's doc comment for the full reasoning. A push button
        (`/Ff` bit 17, icon/caption-driven rather than value-driven) and
        a signature field (`/FT /Sig`, no generic "current value" at
        all) are both skipped entirely, exactly as before this package
        existed.
    - `appearance.go`'s `GenerateAppearance` is the single entry point,
        dispatching on `/FT` and returning a ready-to-paint Form XObject
        stream plus the widget's own `/Rect`.
- **`internal/annotation` refactor.** `resolveOne` is now exported as
    `ResolveOne`, its `/Annots`-array-walking loop factored out into
    `ResolveAnnots` (used by both `Resolve` and the root package's new
    form-field pass), and its BBox-to-Rect mapping math factored out into
    `FromStream` - so a generated appearance is placed on the page via
    the *exact* same algorithm a real `/AP` stream already goes through,
    rather than a second copy of it.
- **`internal/model.Document` gained `AcroForm()`**, resolving the
    document catalog's own `/AcroForm` entry (the catalog dictionary
    itself is now kept on `Document`, which had never previously needed
    it for anything beyond finding `/Pages`).
- **Wiring.** `page.go`'s `renderAtScale` calls a new `formFieldDrawOps`
    (`annotations.go`) alongside the existing `annotationDrawOps`: for
    every widget `internal/annotation.ResolveOne` could not already
    resolve an appearance for, it hands the widget to
    `internal/acroform.GenerateAppearance` and, on success, paints the
    result exactly like any other annotation appearance (reusing
    `internal/content`'s Form XObject machinery, same as
    `annotationDrawOps` already does).
- **Fixtures.** `tools/genfixtures/acroform.go`'s
    `buildFormFilledNoAppearance` builds `form-filled-no-appearance.pdf`:
    a text field (`/V (A)`, red, size exactly matching its own field
    height) and a checked/unchecked pair of checkboxes, all three with
    `/V` but no `/AP` at all, reusing the same synthetic square-glyph
    TrueType program (`text.go`'s `buildTestFontProgram`) the existing
    text fixtures embed - so, as with those, the fixture is fully
    reproducible from this project's own code and every expected pixel
    is derivable by hand from this package's own documented layout
    formulas (see that function's doc comment for the worked-out
    geometry).
- **Tests.** Each `internal/acroform` file has its own table-driven unit
    tests (field-tree inheritance and cycle guarding, `/DA` parsing
    including malformed/ambiguous cases, text-string decoding for both
    PDFDocEncoding-as-Latin-1 and UTF-16BE, font-byte encoding and its
    `'?'` fallback, content-stream serialization/escaping, word-wrapping,
    checkbox state resolution, and the top-level dispatcher's every
    "nothing to generate" case) exercising the pure logic at the Go-value
    level, without needing a PDF file at all. `pdfviewer_acroform_test.go`
    adds one end-to-end test per field kind rendering the new fixture and
    asserting the exact hand-derived device-space pixels; the fixture was
    also added to `TestRenderMatchesReferenceImages`'s golden-image list.
    Full test suite, `go vet`, and the race detector all pass clean; a
    45-second, 17M-execution run of `FuzzOpenAndRender` (which picks up
    the new fixture as a seed automatically) found no panic or hang.
- **What's carried forward.** Every deliberate scope limit is documented
    on `internal/acroform`'s own package doc comment and cross-referenced
    from `docs/capability-matrix.md`'s AcroForm field rendering row:
    comb fields render as ordinary non-comb text, a choice field's
    `/Opt` export-value table is never consulted, a multi-select list
    box always shows only its first selection, and font auto-sizing/
    quadding use a documented heuristic rather than Acrobat's own
    metrics-driven algorithm. None of these are silent - each one simply
    continues to render exactly as it did before this phase (unpainted,
    or a slightly different but still-legible layout), never incorrectly.

### Phase 13a: Function-based (Type 1) shadings — done (2026-09-10)

- **`internal/graphics/shading.go`.** `ShadingKind` gained
    `FunctionBasedShading` (Type 1) alongside the existing
    `AxialShading`/`RadialShading`, plus the four mesh kinds
    (`FreeFormTriangleMesh` through `TensorProductPatchMesh`) declared
    now so Phase 13b onward has named constants to build against, even
    though nothing produces them yet. `Shading` gained `Domain2`
    (the Type 1 rectangular input domain), `Matrix` (Type 1's own
    domain-to-shading-space transform, layered underneath
    `ShadingToDevice`), `ColorAt2` (the 2-input color closure), and
    `Triangles`/`MeshTriangle` (mesh shadings' representation, unused
    until Phase 13b). `At` dispatches `FunctionBasedShading` to a new
    `atFunctionBased`, which inverts `Matrix` to recover the domain-space
    (x, y) a shading-space point corresponds to and reports the point
    uncovered if it falls outside `Domain2` - unlike axial/radial, Type 1
    has no `/Extend`-style "paint the edge value beyond the domain" rule.
    `MeshTriangle.colorAt` (barycentric interpolation) and `meshColorAt`
    (linear scan over a triangle list) are added now, ready for Phase
    13b's mesh types to populate `Triangles`, but not yet reachable from
    `internal/content` - see "What's carried forward" below.
- **`internal/content/shading.go` refactor.** `buildShading` used to take
    an already-dictionary-shaped shading (via `lookupShadingDict`
    /`dictionaryOrStreamDict`, which discarded a stream's raw bytes) -
    fine while only axial/radial (always plain dictionaries) existed, but
    mesh shadings' packed vertex/patch data lives in the stream's own
    bytes. `buildShading` now takes the resolved-but-unsplit object
    (dictionary or stream) and does that split itself, so every shading
    type's own builder can assume whichever shape it actually needs.
    `lookupShadingDict` was renamed `lookupShadingObject` and no longer
    reduces a stream to its dictionary; `resolvePatternPaint`'s shading-
    pattern case was updated the same way. The old axial/radial-only body
    of `buildShading` moved unchanged into a new `buildAxialOrRadialShading`;
    a new `buildFunctionBasedShading` parses Type 1's differently-shaped
    `/Domain` (4 numbers, not axial/radial's 2), its own `/Matrix`
    (default identity), and its `/Function` (validated to take exactly 2
    inputs, unlike axial/radial's 1), building a `Shading` via `ColorAt2`.
    Mesh shading types (4-7) reach a new, dedicated `ErrUnsupported`
    branch (previously folded into the same "not axial or radial" error
    axial/radial's own type-switch produced).
- **Tests.** `internal/graphics/shading_test.go` gained
    `FunctionBasedShading`/`MeshTriangle` coverage: domain containment,
    `/Matrix` inversion (a mistranslated or un-inverted matrix would
    silently compute a wrong domain coordinate), a nil `ColorAt2` safe
    default, barycentric corner/centroid/outside/degenerate cases for
    `MeshTriangle`, and `At`'s mesh-kind dispatch (covered vs. uncovered,
    and that `ShadingToDevice` is honored). `internal/content/shading_test.go`
    gained `functionBasedShadingDict` (a 2x2-grid Type 0 sampled function,
    the only function type this project implements that takes 2 inputs -
    Type 2/3 are both 1-input-only) and `TestDoShadingFunctionBasedPaintsFromXY`,
    plus updated `TestDoShadingUnsupportedTypeIsError` to exercise a mesh
    type (4) instead of Type 1, now that Type 1 is supported.
    `tools/genfixtures`'s `buildFunctionBasedShading` builds
    `function-based-shading.pdf`: a 2x2 Type 0 function producing black/
    red/green/yellow at its four domain corners, deliberately varying
    differently along each axis (red grows with x, green with y) so a
    bug that swapped or dropped an axis would fail. `pdfviewer_shading_test.go`'s
    `TestRenderFunctionBasedShading` asserts all four device-space
    corners (worked out by hand, accounting for `page.go`'s PDF-to-device
    y-axis flip - see that test's doc comment), and the fixture was added
    to `TestRenderMatchesReferenceImages`'s golden-image list. Full test
    suite, `go build`, and `go vet` pass clean.
- **What's carried forward.** Types 4-7 (mesh shadings) remain
    `ErrUnsupported`, tracked as Phase 13b onward in this document. Every
    shading type's optional `/Background` and `/BBox` entries remain
    unimplemented - a pre-existing simplification predating this phase
    (axial/radial never read them either), now called out explicitly in
    `docs/capability-matrix.md` since mesh shadings will make `/BBox`
    more commonly relevant once Phase 13b lands.

### Phase 13b: Mesh shading infrastructure and Type 4 (free-form triangle mesh) — done (2026-09-10)

- **Why 4 first.** All four mesh types (4-7) ultimately reduce to the
    same on-screen representation - a flat list of Gouraud-shaded
    triangles - but get there via completely different packed-stream
    formats (Type 4's per-vertex edge flags, Type 5's implicit grid
    adjacency, Types 6/7's Bezier patch control points). Type 4 is the
    simplest of the four and was built first specifically to stand up the
    shared plumbing (the bit reader, `/BitsPerCoordinate`/
    `/BitsPerComponent`/`/Decode` parsing, the `/Function`-or-raw-
    components color path, and `graphics.MeshTriangle`'s own rendering)
    that Types 5-7 (later sub-phases) will reuse rather than duplicate.
- **Getting the bit-packing rules right.** Neither this document's
    authors nor a from-memory reading of the specification text alone
    was trusted for one specific, easy-to-get-wrong detail: which mesh
    types byte-align each vertex/patch record and which pack bits
    continuously with no padding at all. This was cross-checked against
    Mozilla's pdf.js (`src/core/pattern.js`'s `MeshStreamReader`/
    `MeshShading` - a mature, spec-conformant, MPL-2.0-licensed
    implementation) rather than assumed: Type 4 pads every vertex to a
    byte boundary (`align()` called after each one); Types 5, 6, and 7
    (later sub-phases) do not pad at all. Getting this wrong would
    produce a mesh that decodes without any error at all, just complete
    visual garbage - exactly the class of mistake this project's own
    round-trip self-testing (encode with this project's own code, decode
    with this project's own code) structurally cannot catch, since there
    is no production PDF-*writing* encoder here to round-trip against in
    the first place (see 8a's JBIG2 entry above for the same concern in a
    different codec). Only the bit-layout/control-point-ordering rules
    were consulted from pdf.js's source; nothing was copied, and this
    project's Go implementation, types, and structure are its own - see
    `internal/content/meshshading.go`'s own doc comment for the full
    provenance note.
- **`internal/content/meshshading.go` (new).** `bitReader`
    (MSB-first unsigned-integer reads, `hasData`/`align`) is this file's
    own small duplicate of the same bit-reading shape
    `internal/image/decode.go` and `internal/function/type0.go` each
    already have their own copy of, matching this project's established
    precedent of a few duplicated lines over a shared micro-package.
    `meshParams` bundles what every mesh type's per-vertex reads need
    (bit widths, `/Decode`, component count, optional `/Function`, color
    space); `readCoordinate`/`readColor` map raw bits through `/Decode`;
    `meshColor` applies the optional `/Function` then the color space
    (mirroring `buildAxialOrRadialShading`'s `ColorAt` closure, including
    its same defensive black-on-error fallback); `latticeToTriangles`
    triangulates any regular vertex grid (used directly by Type 5 and, by
    patch subdivision, Types 6/7 - all later sub-phases) into
    `graphics.MeshTriangle`s. `buildMeshShading` parses everything the
    four types share (`/BitsPerCoordinate`, `/BitsPerComponent`, the
    optional `/Function`, `/ColorSpace`, `/Decode` - validating its length
    against how many raw color components a vertex actually carries, 1 if
    a `/Function` exists, else the color space's own component count,
    exactly mirroring the axial/radial-shading rule) before dispatching
    to whichever per-type decoder does the actual bitstream walk -
    currently only `decodeType4Mesh`; Types 5-7 fall through to a
    dedicated `ErrUnsupported` branch there.
- **`decodeType4Mesh`** implements 8.7.4.5.5's triangle-strip-like
    encoding: edge flag 0 starts an independent triangle (needing 2 more
    vertices); flag 1 shares the *previous* triangle's own 2nd and 3rd
    vertices as the new triangle's 1st and 2nd (needing 1 more vertex);
    flag 2 shares the previous triangle's 1st and 3rd the same way - built
    as a flat, growing index list rather than reading three vertices at a
    time, since a flag-1/2 triangle only ever supplies one new vertex of
    its own. A truncated trailing partial triangle is dropped rather than
    erroring, this project's usual tolerance for malformed trailing data.
- **`internal/content/shading.go` wiring.** `buildShading`'s mesh-type
    case now calls `buildMeshShading` instead of returning
    `ErrUnsupported` unconditionally; `TestDoShadingUnsupportedTypeIsError`
    was updated to exercise Type 5 (still genuinely unsupported) instead
    of Type 4.
- **Tests.** `internal/content/meshshading_test.go` covers `bitReader`
    (MSB-first reads across a byte boundary, past-end zero-fill, `align`,
    `hasData`), `readCoordinate`/`readColor`'s `/Decode` mapping,
    `meshColor`, `latticeToTriangles`, and `decodeType4Mesh` end to end at
    the Go-value level: a single fresh triangle; edge flag 1 sharing
    *exactly* the previous triangle's 2nd/3rd vertices (not some other
    plausible-looking pairing, which would still look like a mesh, just a
    geometrically wrong one); edge flag 2's equivalent case; an invalid
    flag and a flag-1-with-no-prior-triangle both erroring; and a
    truncated trailing triangle being dropped rather than fabricated. A
    `meshBitWriter` test helper (this file's own small forward-direction
    inverse of `bitReader`, needed because this project has no production
    mesh-shading encoder to build test input with otherwise) packs the
    synthetic streams these tests exercise. `tools/genfixtures` gained its
    own identically-shaped `meshBitWriter` and `buildFreeFormTriangleMeshShading`,
    building `mesh-shading-type4.pdf`: one real triangle (red/green/blue
    corners) through an actual PDF stream and content-stream `sh` operator,
    not just this package's own unit tests. `pdfviewer_shading_test.go`'s
    `TestRenderFreeFormTriangleMeshShading` checks device-space "dominant
    color near each vertex" (a fixed-tolerance exact-color check does not
    work this close to a vertex of a ~100-unit triangle under linear
    Gouraud interpolation - see that test's own comment) plus one point
    genuinely outside the triangle, confirming a mesh shading really does
    leave part of the page unpainted rather than defaulting to some
    background color the way axial/radial's `/Extend` might suggest. The
    fixture was added to `TestRenderMatchesReferenceImages`'s golden-image
    list. Full test suite, `go build`, `go vet`, and the race detector all
    pass clean; 20+-second fuzz runs of both the root package's
    `FuzzOpenAndRender` and `internal/content`'s `FuzzParseAndInterpret`
    (which pick up the new fixture/mesh-shading code paths automatically)
    found no panic or hang.
- **What's carried forward.** Types 5 (lattice-form triangle mesh), 6
    (Coons patch mesh), and 7 (tensor-product patch mesh) remain
    `ErrUnsupported`, tracked as Phase 13c-13e. `latticeToTriangles`
    already exists ready for Type 5 to call directly and for Types 6/7 to
    call after subdividing a patch's bicubic surface into a fine grid.

### Phase 13c: Type 5 (lattice-form triangle mesh) — done (2026-09-10)

- **`decodeType5Mesh`** (`internal/content/meshshading.go`) implements
    8.7.4.5.6: unlike Type 4, a Type 5 stream has no edge flags at all -
    every vertex is simply `(x, y, r, g, b...)`, and the mesh's structure
    is an implicit `/VerticesPerRow`-wide grid read one row at a time,
    triangulated purely by adjacency via `latticeToTriangles` (already
    built in 13b, unused until now). The other half of 13b's "why Type 4
    first" reasoning pays off here: this sub-phase added one ~15-line
    function, no new shared infrastructure at all.
- **The one genuinely new risk.** Type 5 vertices are packed with *no*
    per-vertex byte alignment, unlike Type 4's - confirmed against pdf.js
    the same way 13b's entry describes. `decodeType5Mesh` deliberately has
    no `br.align()` call. `TestDecodeType5MeshNoByteAlignmentBetweenVertices`
    (a bit width that does not divide evenly into a byte, so any
    accidentally-inserted alignment would desync every field after the
    first vertex) is this sub-phase's regression guard for that specific
    fact - verified to actually fail if `align()` is added back in, not
    just written to look like it would.
- **`internal/content/shading.go` wiring.** `buildMeshShading`'s type
    switch gained a `case 5` parsing `/VerticesPerRow` (required, >= 2)
    before calling `decodeType5Mesh`; `TestDoShadingUnsupportedTypeIsError`
    moved from Type 5 to Type 6 (still genuinely unsupported).
- **Tests.** `internal/content/meshshading_test.go` gained a 2x2-grid
    round trip, a truncated-trailing-row case (a partial final row is
    dropped, not fabricated - the same tolerance policy Type 4's
    truncated-triangle handling uses), and the byte-alignment regression
    test above. `tools/genfixtures`'s `buildLatticeFormTriangleMeshShading`
    builds `mesh-shading-type5.pdf`: a 2x2 grid covering the *entire*
    page (red/green/blue/yellow at its four corners), a deliberate
    complement to 13b's Type 4 fixture, which instead leaves half its
    page unpainted - together the two fixtures cover both "a mesh that
    covers everything" and "a mesh that leaves part of the clip
    unpainted". `pdfviewer_shading_test.go`'s
    `TestRenderLatticeFormTriangleMeshShading` checks dominant color near
    each of the four corners (yellow needs its own two-channel check,
    unlike the other three single-channel-dominant corners), and the
    fixture was added to `TestRenderMatchesReferenceImages`'s golden-image
    list. Full test suite, `go build`, `go vet`, and the race detector all
    pass clean; a 15+-second `FuzzOpenAndRender` run (picking up the new
    fixture automatically) found no panic or hang.
- **What's carried forward.** Types 6 (Coons patch mesh) and 7
    (tensor-product patch mesh) remain `ErrUnsupported`, tracked as Phase
    13d-13e - substantially more work than 13b/13c, since a patch's
    bicubic Bezier surface has to be evaluated and subdivided into a fine
    triangle grid before `latticeToTriangles` can take over, rather than
    reading the mesh's final triangle shape directly off the stream the
    way Types 4 and 5 do.

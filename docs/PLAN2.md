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

**Status: 8a done; 8b and 8c in progress.**

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
    proves insufficient against real fixtures.
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
JBIG2 feature (e.g. refinement regions) explicitly noted.

## Phase 9: Non-Identity Type 0/CID encodings (CJK support)

**Status: Not started.**

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

**Status: Not started.**

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

**Status: Not started.**

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

**Status: Not started.**

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

**Status: Not started.**

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

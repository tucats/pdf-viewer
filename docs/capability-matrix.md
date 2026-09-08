# PDF capability matrix

This is the Phase 0 capability matrix called for in the repository
README's phased plan: a single place recording, for each area of PDF
functionality, what this project targets supporting and what phase is
expected to deliver it. It exists so that "does pdf-viewer support X?"
has one authoritative, checkable answer instead of requiring an
archaeology of commit history or source comments, and so that a decision
to leave something permanently unsupported is recorded as a decision
rather than discovered by accident.

**Status legend**

| Status | Meaning |
| --- | --- |
| Not started | No design decision or code yet. |
| Planned | Targeted for a specific phase below, no code yet. |
| Partial | Some code exists; coverage is incomplete. See notes. |
| Done | Implemented and covered by tests. |
| Non-goal | Deliberately out of scope; see the README's "Non-goals for the Initial Release" section. |

This table should be updated in the same change that changes a
capability's support level — treat a code change that isn't reflected
here as incomplete. See [README.md](../README.md) for the phased plan
and its Progress Log for what was actually built in each phase.

## PDF versions

| Capability | Target phase | Status | Notes |
| --- | --- | --- | --- |
| PDF 1.4–1.7 core structure (classic xref tables, classic trailers, incremental updates via /Prev, recovery scan for a corrupted xref table) | Phase 1 | Partial | Implemented in `internal/parser`; see its package doc comment. "Partial" because broader real-world compatibility is still growing, not because the mechanism itself is incomplete. |
| PDF 1.5+ cross-reference streams and object streams | Phase 2 (moved from Phase 1) | Done | Implemented in `internal/parser` (`xrefstream.go`, `objstream.go`), now that Flate decoding exists (`internal/filter`). A document may freely mix classic and stream-based cross-reference sections across its `/Prev` chain. Hybrid-reference files (a classic table plus a supplementary `/XRefStm` for stream-unaware readers) are not specially handled - objects only reachable via `/XRefStm` are not found - but this is rare in practice and does not cause an error, only a missing object (resolved as null). |
| PDF 2.0 (ISO 32000-2) structural changes | Phase 6 | Not started | Revisited during API stabilization once the 1.x corpus is solid. |
| Linearized ("fast web view") files | Not scheduled | Not started | Linearization is an optimization hint or convention layered on top of standard structure, not the file's ground truth; a linearized file must still be readable using vanilla xref/trailer parsing, so this project treats it as automatically handled rather than as a scheduled feature. |

## Encryption

| Capability | Target phase | Status | Notes |
| --- | --- | --- | --- |
| Unencrypted documents | Phase 1 | Not started | Baseline. |
| Standard security handler, empty user password ("owner-password-only" protected files) | Not yet scheduled | Not started | Common in practice (permissions-only protection); revisit once Phase 1-3 are solid. |
| Standard security handler, non-empty user password | Not yet scheduled | Not started | Requires a password-input API decision; see the README's Draft Public API open questions. |
| Public-key security handler | Not scheduled | Not started | No known demand driving this; revisit only if a real use case appears. |

## Filters (stream and string decoding)

| Capability | Target phase | Status | Notes |
| --- | --- | --- | --- |
| ASCII85Decode, ASCIIHexDecode | Phase 2 | Done | Implemented in `internal/filter` (`ascii85.go`, `asciihex.go`). |
| FlateDecode (with predictors) | Phase 2 | Done | Implemented in `internal/filter` (`flate.go`, `predictor.go`). Both PNG (predictor 10-15) and TIFF (predictor 2) predictors are supported for `/BitsPerComponent` 1, 2, 4, 8, and 16. |
| RunLengthDecode | Phase 2 | Done | Implemented in `internal/filter` (`runlength.go`). |
| LZWDecode | Phase 2 | Partial | Implemented in `internal/filter` (`lzw.go`) via the standard library's `compress/lzw`, whose `MSB` order is explicitly documented as PDF-compatible. "Partial" because only the default `/EarlyChange 1` is supported - `/EarlyChange 0` returns an error wrapping `ErrUnsupported`, since the standard library offers no way to select that variant. PNG/TIFF predictors are supported here too, sharing `predictor.go` with FlateDecode. |
| DCTDecode (JPEG) | Phase 3 | Not started | Gated on standard-library `image/jpeg` sufficing; see README "Images, color, and thumbnails". |
| CCITTFaxDecode | Not yet scheduled | Not started | Common in scanned/fax-derived PDFs; no standard-library decoder exists, so this needs its own design decision before scheduling. |
| JBIG2Decode | Not scheduled | Not started | Encumbered, complex format with narrow real-world benefit relative to implementation cost; revisit only on concrete demand. |
| JPXDecode (JPEG 2000) | Not scheduled | Not started | Same rationale as JBIG2Decode. |
| Crypt filter | Not scheduled | Not started | Depends on encryption support above. |

## Content streams and graphics

| Capability | Target phase | Status | Notes |
| --- | --- | --- | --- |
| Content stream operator parsing | Phase 2 | Done | Implemented in `internal/content` (`operator.go`); reuses `internal/syntax` for operand syntax. Inline images (`BI`/`ID`/`EI`) are explicitly detected and rejected as unsupported (Phase 3) rather than silently misparsed. |
| Graphics state stack (`q`/`Q`), coordinate transforms (`cm`) | Phase 2 | Done | Implemented in `internal/graphics` (`state.go`, `matrix.go`). |
| Path construction (`m`/`l`/`c`/`v`/`y`/`h`/`re`) | Phase 2 | Done | Implemented in `internal/graphics` (`path.go`). Bézier curves are flattened into a fixed number of line segments for deterministic rasterization. |
| Path painting: fill, nonzero and even-odd (`f`/`F`/`f*`/`B`/`B*`/`b`/`b*`) | Phase 2 | Done | Implemented in `internal/raster`'s scanline coverage rasterizer (`scanline.go`). |
| Path painting: stroke (`S`/`s`), line width/cap/join | Phase 2 | Partial | Implemented via `internal/graphics.StrokeToFill` (`stroke.go`): a stroke is converted to its filled outline before rasterization. Line width and caps (butt/round/square) are exact; joins are always rendered as round joins regardless of the requested join style - true miter (with miter-limit fallback to bevel) and bevel join geometry are not yet implemented. Dash patterns (`d`) are parsed and ignored; every stroke is solid. |
| Clipping (`W`/`W*`) | Phase 2 | Partial | Implemented in `internal/graphics` (`state.go`'s `Clips`) and `internal/raster` (coverage-multiplication intersection). Multiple simultaneously active clips are supported, but combined via multiplying each clip's independently anti-aliased coverage rather than true polygon-boolean intersection - an approximation, exact for non-anti-aliased (opaque-interior) clip regions. |
| Solid color: DeviceGray/RGB/CMYK (`g`/`G`/`rg`/`RG`/`k`/`K`) | Phase 2 | Done | Implemented in `internal/content` (`interpret.go`). CMYK uses the PDF specification's own baseline (non-color-managed) conversion formula. |
| `sc`/`SC`/`scn`/`SCN` with a resolved `/ColorSpace` resource | Phase 2/3 | Partial | `internal/content` infers DeviceGray/RGB/CMYK directly from the operand count (1, 3, or 4 numeric components) without resolving `/Resources`/`/ColorSpace` at all; this covers the common case but not a named color space with a different component count. `cs`/`CS` are accepted and otherwise ignored. A pattern name operand (`scn`/`SCN` with `/Pattern`) is explicitly detected and rejected as unsupported (Phase 5). |
| Form XObjects (`Do` with a `/Form` XObject) | Phase 3/5 (not yet scheduled precisely) | Not started | `Do` is currently silently skipped, along with every other unrecognized operator - see `internal/content`'s package doc comment. |
| Text showing/positioning operators | Phase 4 | Not started | Silently skipped; see the row above. |
| Marked content (`BMC`/`BDC`/`EMC`/`MP`/`DP`) | Not scheduled | Not started | Silently skipped. No rendering-relevant effect for this project's scope (marked content is a metadata/structure mechanism, not a paint operation). |

## Color spaces

| Capability | Target phase | Status | Notes |
| --- | --- | --- | --- |
| DeviceGray, DeviceRGB, DeviceCMYK | Phase 2–3 | Partial | Solid-color operators (`g`/`rg`/`k`, `sc`/`scn` by component count) are implemented - see "Content streams and graphics" above. Use as an image color space (decoding pixel samples) is Phase 3 work. |
| Indexed | Phase 3 | Not started | |
| CalGray, CalRGB, Lab | Phase 3 | Not started | |
| ICCBased | Phase 3 | Not started | Target: use the ICC profile's declared alternate/component count for correct rendering without implementing a full color management engine. |
| Separation, DeviceN | Phase 5 | Not started | Needed for tint-transform-driven spot colors, grouped with Phase 5's shading/pattern work since they share the "evaluate a PDF function" dependency. |
| Pattern color space (tiling and shading patterns) | Phase 5 | Not started | |

## Fonts

| Capability | Target phase | Status | Notes |
| --- | --- | --- | --- |
| Simple fonts: Type 1 (embedded) | Phase 4 | Not started | |
| Simple fonts: TrueType (embedded) | Phase 4 | Not started | |
| Composite fonts: Type 0 / CID (embedded, Identity-H/V) | Phase 4 | Not started | |
| Non-embedded font fallback | Phase 4 | Not started | Must be an explicit, documented in-package policy; must not query the host OS for installed fonts (see `internal/fonts` package doc). |
| Type 3 fonts (glyphs defined as content streams) | Not yet scheduled | Not started | Rare in practice; revisit after Phase 4's core font support is stable. |
| OpenType/CFF (Type1C, CIDFontType0C) | Phase 4 | Not started | Grouped with Phase 4 since these are just embedded-font-program variants, not a separate capability. |

## Transparency and advanced graphics

| Capability | Target phase | Status | Notes |
| --- | --- | --- | --- |
| Alpha constants and basic transparency (`ca`/`CA`) | Phase 5 | Not started | |
| Transparency groups and blend modes | Phase 5 | Not started | |
| Soft masks | Phase 5 | Not started | |
| Tiling patterns | Phase 5 | Not started | |
| Shading patterns (axial, radial; other types as needed) | Phase 5 | Not started | |

## Images

| Capability | Target phase | Status | Notes |
| --- | --- | --- | --- |
| Referenced XObject images | Phase 3 | Not started | |
| Inline images (`BI`/`ID`/`EI` operators) | Phase 3 | Not started | |
| Image masks (stencil masking, `/ImageMask true`) | Phase 3 | Not started | |
| Soft masks on images (`/SMask`) | Phase 3 (basic), Phase 5 (full transparency interaction) | Not started | Basic per-image soft mask support targeted alongside other Phase 3 image work; full interaction with transparency groups is a Phase 5 concern. |
| Decode arrays | Phase 3 | Not started | |

## Annotations and forms

| Capability | Target phase | Status | Notes |
| --- | --- | --- | --- |
| Annotation appearance streams (rendering an annotation's existing visual appearance) | Phase 5 | Not started | Deliberately scheduled after ordinary page content is stable, per the README's Phase 5 plan. |
| AcroForm field rendering | Phase 5 (at earliest) | Not started | Rendering only — see "PDF creation or editing" non-goal below for what is explicitly out of scope. |
| Annotation/form *interactivity* (filling in fields, running actions/JavaScript) | Not scheduled | Non-goal | This project is a renderer, not a form-filling or scripting engine. |

## Page boxes

| Capability | Target phase | Status | Notes |
| --- | --- | --- | --- |
| MediaBox | Phase 1 | Done | Implemented in `internal/model`, including inheritance from an ancestor Pages node when a page does not specify its own; exposed publicly via `Page.Bounds`. |
| CropBox | Phase 1–2 | Not started | Falls back to MediaBox when absent, per spec. |
| BleedBox, TrimBox, ArtBox | Phase 2 | Not started | Exposed via `RenderOptions`' page-box selection (see README Draft Public API). |
| Page rotation (`/Rotate`) | Phase 2 | Done | Implemented in `internal/model` (inherited like `/MediaBox`/`/Resources`, normalized to 0/90/180/270 with an invalid value falling back to the inherited default) and applied in `Page.Render`'s device geometry (root package `page.go`). |

## Explicit non-goals

These are recorded here (duplicating the README's "Non-goals for the
Initial Release" section) so this matrix is a complete answer to "is X
supported", including the things this project does not intend to
support:

- PDF creation or editing (this project reads and renders; it does not
	write PDF files).
- Annotation/form interactivity, JavaScript actions, or any other
	scripting behavior embedded in a PDF.
- Guaranteed recovery of every malformed or hostile file; only bounded,
	non-panicking, classified-error behavior is guaranteed (see the
	"Dependency and safety policy" section of the README).

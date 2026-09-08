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
| DCTDecode (JPEG) | Phase 3 | Done | Implemented in `internal/filter` (`dct.go`) via the standard library's `image/jpeg`: baseline and progressive JPEG, grayscale/YCbCr/CMYK (Adobe) component layouts all supported since Go's decoder already handles each. Bounded via `jpeg.DecodeConfig` (reads only the header) before committing to a full decode, so a maliciously huge declared image size is rejected before an oversized allocation. |
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
| `sc`/`SC`/`scn`/`SCN` with a resolved `/ColorSpace` resource | Phase 2/3/5 | Partial | `internal/content` (`colorspace.go`) now resolves `cs`/`CS`'s named color space via `internal/image.ResolveColorSpace` - the same logic an image's own `/ColorSpace` uses - so `sc`/`scn`/`SC`/`SCN` convert correctly through a resolved Separation/DeviceN/Lab/Indexed/ICCBased/CalGray/CalRGB space, not just the three Device families. When no color space was selected, the selected one could not be resolved, or its component count does not match the operands given, this falls back to inferring DeviceGray/RGB/CMYK directly from operand count (1, 3, or 4), exactly as before. "Partial" because Pattern is still not usable as a fill/stroke paint source (a pattern name operand, `scn`/`SCN` with `/Pattern`, is still explicitly detected and rejected as unsupported - remaining Phase 5 work). |
| Image XObjects (`Do` with an `/Image` XObject) | Phase 3 | Done | Implemented in `internal/content` (`image.go`'s `doXObject`), decoding through `internal/image.Decode` and painting via `internal/raster`'s `Canvas.DrawImage` - see the "Images" section below for color-space/mask/decode-array coverage. |
| Inline images (`BI`/`ID`/`EI` operators) | Phase 3 | Done | Implemented in `internal/content` (`operator.go`'s `Parse` calls `inlineimage.go`'s `parseInlineImage`; `image.go`'s `doInlineImage` decodes and paints, sharing all of `internal/image`'s color-space/mask logic with a referenced XObject image). An inline image's raw data length is read via a non-standard `/L` key when present, computed exactly from `/W`/`/H`/`/BPC`/`/CS` for an unfiltered image, or found by scanning for a whitespace-delimited `EI` otherwise - see `parseInlineImage`'s doc comment for the tradeoffs of each. |
| Form XObjects (`Do` with a `/Form` XObject) | Phase 5 | Done | Implemented in `internal/content` (`form.go`'s `doForm`): the form's `/Matrix` (default identity) is concatenated onto the CTM active where `Do` is invoked; its `/BBox` becomes an additional clip; its `/Resources`, when present, replaces the caller's for the form's own content (falling back to the caller's `/Resources` when absent, per the specification). A form's content is recursively interpreted and its resulting `DrawOp`s are flattened directly into the caller's own display list (no intermediate offscreen buffer - see the package's own form.go doc comment for why this is sufficient, unlike a tiling pattern), bounded by `maxFormDepth` (8 levels) against a self-referential or cyclic form. A form's own `/Group` entry (marking it as a transparency group, with isolated/knockout semantics) is not inspected - see "Transparency groups and blend modes" below. |
| Text showing/positioning operators (`BT`/`ET`, `Tc`/`Tw`/`Tz`/`TL`/`Tf`/`Tr`/`Ts`, `Td`/`TD`/`Tm`/`T*`, `Tj`/`TJ`/`'`/`"`) | Phase 4 | Done | Implemented in `internal/content` (`text.go`). Each glyph becomes an ordinary Fill `DrawOp` (`showGlyph`), sharing `internal/raster`'s one rasterization path with vector fills. Text render modes are simplified to two cases: modes 3 and 7 (invisible / clip-only) paint nothing, every other mode (0-2, 4-6) is treated as an ordinary fill - stroked glyph outlines and accumulating a text clip path are not implemented. Only horizontal writing is implemented; a vertical-mode Type0 font (`Identity-V`) still shows text and advances correctly using the horizontal formula rather than vertically. |
| Shading (`sh` operator) | Phase 5 | Partial | Implemented in `internal/content` (`shading.go`'s `doShading`) for axial (`/ShadingType 2`) and radial (`/ShadingType 3`) shadings - see `internal/graphics`'s `Shading` type (`shading.go`) for the gradient geometry/math and the "Transparency and advanced graphics" table below for the pattern-fill counterpart. Function-based (Type 1) and mesh (Types 4-7) shadings are not implemented and return an error wrapping `ErrUnsupported` naming the type. |
| Marked content (`BMC`/`BDC`/`EMC`/`MP`/`DP`) | Not scheduled | Not started | Silently skipped. No rendering-relevant effect for this project's scope (marked content is a metadata/structure mechanism, not a paint operation). |

## Color spaces

| Capability | Target phase | Status | Notes |
| --- | --- | --- | --- |
| DeviceGray, DeviceRGB, DeviceCMYK | Phase 2–3 | Done | Solid-color operators (`g`/`rg`/`k`, `sc`/`scn` by component count) were Phase 2 - see "Content streams and graphics" above. Use as an image color space (decoding pixel samples, including the inline-image abbreviations `/G`/`/RGB`/`/CMYK`) is implemented in `internal/image` (`colorspace.go`). |
| Indexed | Phase 3 | Done | Implemented in `internal/image` (`colorspace.go`'s `resolveIndexed`) over any supported base color space; the lookup table may be a string or a stream (filter-decoded via the same `Resolver.DecodeStream` path as any other stream). An `/Indexed` color space whose base is itself `/Indexed` is rejected as unsupported (not meaningful, and not something real producers emit). |
| ICCBased | Phase 3 | Partial | Implemented in `internal/image` (`colorspace.go`'s `resolveICCBased`) by component count only (`/N` = 1, 3, or 4, aliased to DeviceGray/RGB/CMYK respectively) - this is the documented target behavior ("use the ICC profile's declared alternate/component count... without implementing a full color management engine"), so "Partial" reflects that no actual ICC profile is ever parsed or applied, by design. |
| CalGray, CalRGB | Phase 3 | Partial | Implemented in `internal/image` (`colorspace.go`) as aliases for DeviceGray/DeviceRGB - white point, gamma, and (for CalRGB) matrix are not applied, matching this project's existing non-color-managed CMYK conversion precedent. |
| Lab | Phase 5 | Done | Implemented in `internal/image` (`colorspace.go`'s `resolveLab`/`labToRGB`): standard CIELAB -> CIEXYZ -> linear sRGB -> gamma-encoded sRGB, using the color space's own `/WhitePoint` for the CIEXYZ step but the D65-calibrated standard XYZ->sRGB matrix regardless (a documented approximation - a `/WhitePoint` other than D65, e.g. the D50 common in prepress PDFs, introduces a color cast this project does not chromatically adapt away; full ICC-grade color management is out of scope, matching the existing ICCBased/CalRGB/CalGray precedent). `/Range` (default `[-100 100 -100 100]`) and the fixed `L*` domain `[0 100]` are both honored, including as the color space's own default `/Decode` range for an *image* using `/Lab` (`decode.go`'s `colorSpace.decodeDefault`). |
| Separation, DeviceN | Phase 5 | Done | Implemented in `internal/image` (`colorspace.go`'s `resolveSeparationOrDeviceN`) by evaluating the color space's tint-transform function (`internal/function` - Type 0/2/3; Type 4 PostScript-calculator functions are unsupported, see that package's doc comment) and converting the result through the alternate color space. Available both as an image's `/ColorSpace` and - new in Phase 5 - as a content stream's `cs`/`CS`-selected fill/stroke color space (see the "Content streams and graphics" table's `sc`/`SC`/`scn`/`SCN` row). The `/DeviceN` `/Attributes` dictionary (colorant metadata, `/Process`, overprint simulation) is not inspected. |
| Pattern color space (tiling and shading patterns) | Phase 5 | Partial | `cs`/`CS Pattern` is recognized and recorded (`internal/content/colorspace.go`). `scn`/`SCN` with a pattern name resolving to a shading pattern (`/PatternType 2`) now paints correctly - see the "Transparency and advanced graphics" table's Shading patterns row; one resolving to a tiling pattern (`/PatternType 1`) is still explicitly rejected as unsupported. |

## Fonts

| Capability | Target phase | Status | Notes |
| --- | --- | --- | --- |
| Simple fonts: Type 1 (embedded, `/FontFile`) | Phase 4 | Not started | Font dictionary parsing (encoding, widths) is implemented and usable, but Type 1 charstring outline extraction is not - falls back to `notdefGlyph` (see "Non-embedded font fallback" below). Deferred: Type 1 charstrings are a substantially different, separately complex format from TrueType's `glyf` outlines. |
| Simple fonts: TrueType (embedded, `/FontFile2`) | Phase 4 | Done | Implemented in `internal/fonts` (`truetype.go`: sfnt table directory, `head`/`maxp`/`loca`/`glyf` parsing, simple and composite glyph outlines; `cmap.go`: format 0/4/6 subtable parsing). Encoding resolution (`encoding.go`) covers StandardEncoding/WinAnsiEncoding/MacRomanEncoding (ASCII range exact; StandardEncoding's upper range is a documented gap - see that file's doc comment) and `/Differences` arrays (via a subset of the Adobe Glyph List plus the `uniXXXX` convention). |
| Composite fonts: Type 0 / CID (embedded, Identity-H/V) | Phase 4 | Partial | Implemented in `internal/fonts/cid.go`: `/DescendantFonts`, `/W`/`/DW` widths, `/CIDToGIDMap` (`/Identity` or an explicit stream), and CIDFontType2 (TrueType-based) outline extraction, restricted to the `Identity-H`/`Identity-V` encodings (a code is always 2 bytes, equal to its CID). Any other `/Encoding` (a predefined CJK encoding name, or an embedded CMap stream) still produces a usable font (assuming 2-byte codes) but with no way to know real CIDs from raw codes, so it falls back to a generic default width and `notdefGlyph` for every code. CIDFontType0 (CFF-based) descendant fonts are not extracted (no outlines - same fallback as Type 1 above). Vertical writing (`Identity-V`) advances horizontally rather than vertically - a documented simplification, not a crash or misrender. |
| Non-embedded font fallback | Phase 4 | Done | Implemented in `internal/fonts` (`font.go`'s `notdefGlyph`): a small hollow placeholder box sized to the glyph's own advance width, painted for any code this package cannot resolve a real outline for - except a code positively identified as whitespace (a simple font's resolved encoding says so), which paints nothing instead. No system font service is ever queried (see the package doc comment); a non-embedded font's width still comes from `/Widths`/`/W` when present, or a generic default (`internal/fonts/simple.go`'s `defaultMissingWidth`, 500/1000 em) otherwise - this project ships no standard-14 (Helvetica, Times, ...) AFM metrics table. |
| Type 3 fonts (glyphs defined as content streams) | Not yet scheduled | Not started | A Type 3 font dictionary is accepted (via the same "simple font" code path as Type 1/TrueType) but its glyphs are never executed as content streams - it always falls back to `notdefGlyph`, using whatever `/Widths` it declares. Its own `/FontMatrix` (which can differ from the standard 1/1000 scale every other font type uses) is not applied to width interpretation, since no real outline is ever painted for one. Rare in practice; revisit if real-world demand appears. |
| OpenType/CFF (Type1C, CIDFontType0C) | Phase 4 | Not started | `/FontFile3` (a bare CFF/Type1C program, for a simple font, or a CIDFontType0's own program) is never parsed - falls back to `notdefGlyph` like Type 1. Grouped with Phase 4 since these are just embedded-font-program variants, not a separate capability. |
| Text extraction (recovering Unicode text from a page, as opposed to painting it) | Not yet scheduled | Not started | Deliberately kept separate from text *painting* per the README's Phase 4 plan, so it can be added later without changing how rendering works; `internal/fonts` parses no `/ToUnicode` CMap and exposes no rune-level API. |

## Transparency and advanced graphics

| Capability | Target phase | Status | Notes |
| --- | --- | --- | --- |
| Alpha constants and basic transparency (`ca`/`CA`) | Phase 5 | Done | Implemented via `gs` (`internal/content/extgstate.go`'s `applyExtGState`): `/ca`/`/CA` set `graphics.State.FillAlpha`/`StrokeAlpha` (both part of the graphics state, saved/restored by `q`/`Q`), which attenuate every fill, stroke, image, and shading `DrawOp` (`internal/raster.Canvas`'s compositing formula, `blendChannel`). No transparency *groups* exist (see the next row), so alpha compositing is against the single, always-opaque page canvas rather than an isolated group buffer - correct for how PDF renders in the common case of un-grouped content, but not equivalent to a full group-aware compositor for content that specifically relies on group isolation or a non-`/Isolated` group's backdrop semantics. |
| Transparency groups and blend modes | Phase 5 | Partial | Blend modes: `gs`'s `/BM` sets `graphics.State.BlendMode`, applied identically for fill/stroke/image/shading painting. Only PDF's six *separable* blend modes are implemented (Multiply, Screen, Darken, Lighten, Difference, Exclusion - `internal/graphics/blend.go`'s `Blend`); the four non-separable modes (Hue, Saturation, Color, Luminosity) and the remaining separable ones this project chose not to implement (ColorDodge, ColorBurn, HardLight, SoftLight, Overlay) resolve to Normal, per the specification's own documented fallback for an unsupported blend mode. Transparency *groups* themselves (`/Group` on a Form XObject, isolated/knockout compositing semantics) are not implemented - a Form XObject's content flattens directly onto the page canvas (see "Form XObjects" below) regardless of any `/Group` entry it declares. |
| Soft masks | Phase 5 | Not started | Per-image `/SMask` (Phase 3) is implemented - see "Images" below. An ExtGState-level `/SMask` (deriving a soft mask from an entire separate transparency group's luminosity or alpha, applied to everything painted afterward) is not: `gs` silently ignores a `/SMask` entry. |
| Tiling patterns | Phase 5 | Not started | `scn`/`SCN` with a pattern name resolving to `/PatternType 1` returns an error wrapping `ErrUnsupported` naming it explicitly (`internal/content/shading.go`'s `resolvePatternPaint`) rather than silently skipping the fill/stroke or misrendering it as a solid color. |
| Shading patterns (axial, radial; other types as needed) | Phase 5 | Done | Implemented in `internal/content` (`shading.go`'s `resolvePatternPaint`) for `/PatternType 2` over an axial or radial shading: the pattern's own `/Matrix` is combined with the *default* (not current) coordinate system of the content stream that defined it, per the specification, so the pattern looks identical regardless of what CTM happens to be active wherever it is later selected or used to paint. A shading pattern paints only the shape it fills or strokes (`internal/raster`'s `Canvas.FillShading`), unlike the `sh` operator's whole-clip painting (see the "Content streams and graphics" table's new Shading row). |

## Images

| Capability | Target phase | Status | Notes |
| --- | --- | --- | --- |
| Referenced XObject images | Phase 3 | Done | See "Content streams and graphics" above (`Do` with an `/Image` XObject) and `internal/image`'s package doc comment. |
| Inline images (`BI`/`ID`/`EI` operators) | Phase 3 | Done | See "Content streams and graphics" above. |
| Image masks (stencil masking, `/ImageMask true`) | Phase 3 | Done | Implemented in `internal/image` (`decode.go`): paints using the current graphics-state fill color, honoring `/Decode` reversal (`[1 0]` swaps which sample value paints versus masks out). |
| `/Mask` (stencil, referencing another `/ImageMask` image) | Phase 3 | Done | Implemented in `internal/image` (`mask.go`'s `maskAlphaOrColorKey`), decoded exactly like an ordinary `/ImageMask` image and resampled (nearest-neighbor) to the base image's dimensions if they differ. |
| `/Mask` (color-key masking, an array of raw sample ranges) | Phase 3 | Done | Implemented in `internal/image` (`mask.go`'s `parseColorKeyRanges`/`colorKeyMasked`): a pixel is fully transparent only if every raw (pre-`/Decode`) component sample falls within its own named range. |
| Soft masks on images (`/SMask`) | Phase 3 (basic), Phase 5 (full transparency interaction) | Partial | Basic per-image soft mask support is implemented in `internal/image` (`mask.go`'s `smaskAlphaFn`), including resampling a soft mask of different pixel dimensions than its base image; per the specification, `/SMask` takes priority over `/Mask` when both are present. "Partial" because full interaction with transparency groups and blend modes remains Phase 5. A malicious or malformed pair of images whose `/SMask` entries reference each other is rejected past a bounded recursion depth (`maxMaskRecursionDepth` in `internal/image/decode.go`) rather than recursing indefinitely. |
| Decode arrays | Phase 3 | Done | Implemented in `internal/image` (`decode.go`'s `decodeArray`/`decodeSample`) for every supported color space, `/ImageMask`, and `/Indexed`'s index range, each with the specification's documented default when `/Decode` is absent. |
| `/BitsPerComponent` 1, 2, 4, 8, 16 | Phase 3 | Done | Implemented in `internal/image` (`decode.go`'s `bitReader`): most-significant-bit-first packing, each image row starting on a fresh byte boundary, per the specification. |
| DCTDecode (JPEG) images | Phase 3 | Done | See the Filters table above. |
| CCITTFaxDecode / JBIG2Decode / JPXDecode images | Not yet scheduled | Not started | An image using one of these filters fails with an error wrapping `ErrUnsupported` (propagated from `internal/filter`) rather than being silently skipped or misrendered - see `internal/content`'s "Do"/"BI" handling. |

## Annotations and forms

| Capability | Target phase | Status | Notes |
| --- | --- | --- | --- |
| Annotation appearance streams (rendering an annotation's existing visual appearance) | Phase 5 | Done | Implemented in `internal/annotation` (page-level `/Annots` resolution, `/F` Hidden/NoView flag filtering, `/AP` `/N` single-stream or `/AS`-selected multi-state lookup, and the BBox-to-Rect mapping algorithm, 12.5.5) plus the root package's `annotations.go`, which executes each resolved appearance by reusing `internal/content`'s Form XObject machinery directly (an appearance stream *is* a Form XObject). Painted after the page's own content, on by default; `RenderOptions`/`ThumbnailOptions`' new `HideAnnotations` field opts out. A single malformed annotation dictionary or appearance content stream is skipped rather than failing the whole page's render, consistent with this project's "missing/malformed resource" tolerance elsewhere - see `internal/annotation`'s package doc comment. AcroForm field *value* rendering (regenerating an appearance from a field's current value, rather than painting whatever appearance already exists) is not implemented - see the next row. |
| AcroForm field rendering | Phase 5 (at earliest) | Partial | A form field widget annotation's *existing* appearance stream renders exactly like any other annotation's (see the row above); this project never regenerates one from a field's current value (rendering only, and only of what is already recorded - see "PDF creation or editing" non-goal below for what is explicitly out of scope). |
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

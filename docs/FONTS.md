# Font substitution: audit and plan

This is the working document for adding font-substitution support to
this project: when a PDF references a font this package cannot extract
real glyph outlines for (no embedded program, an unsupported embedded
program format, or a non-embedded font expected to be supplied by
whatever renders the page), today's only behavior is a placeholder
"tofu" box per glyph. The goal explored here is finding and using a
reasonable substitute outline instead - either an explicitly configured
font file, or a system-installed font discovered by matching the PDF
font's declared characteristics (family, weight, italic, serif/sans,
fixed-pitch).

This document has two parts: an audit of what exists today (so the plan
is grounded in the actual code, not assumptions), and a proposed design.
Nothing here has been implemented yet - see "Open questions" for the
decisions that should be settled before writing code, especially the
policy question in the first bullet below.

## Audit: what works today

### Font loading pipeline

`internal/fonts.Load` (`font.go:203`) dispatches on a font dictionary's
`/Subtype`: `Type0` composite fonts go through `cid.go`
(`loadType0Font`), everything else (`Type1`, `TrueType`, `MMType1`,
`Type3`, and anything unrecognized) goes through `simple.go`
(`loadSimpleFont`). Both converge on the same `Font` type
(`font.go:36`), so `internal/content`'s text-showing code
(`internal/content/text.go`) never needs to know which kind of font
dictionary it started from.

`Font` exposes two methods that are documented to *never fail*:

- `Width(code int) float64` (`font.go:86`) - falls back to a default
  width (`/MissingWidth`, or a generic 500/1000-em constant,
  `defaultMissingWidth` in `simple.go:30`) when a code has no `/Widths`
  or `/W` entry.
- `Glyph(code int) *graphics.Path` (`font.go:101`) - falls back to
  `notdefGlyph` (`font.go:153`), a small hollow placeholder box sized to
  the glyph's own advance width, for any code it cannot resolve a real
  outline for. A code positively identified as whitespace paints nothing
  instead of a box.

### What actually produces a real outline today

Only one path exists: an embedded TrueType/OpenType-with-TrueType-outlines
program (`/FontFile2`), parsed by `truetype.go`'s from-scratch sfnt
reader (table directory, `head`/`maxp`/`loca`/`glyf`, both simple and
composite glyphs) and looked up via `cmap.go` (subtable formats 0, 4, 6).
`simple.go:124`'s `loadEmbeddedTrueType` reads it from a simple font's
`/FontDescriptor`; `cid.go` reads it from a Type0 font's descendant
CIDFontType2's `/FontDescriptor`.

Every other case - Type 1 (`/FontFile`), bare CFF/OpenType
(`/FontFile3`), CIDFontType0 (CFF-keyed composite), Type 3, or **no
embedded font program at all** - produces a fully usable `Font` (per its
documented never-fail contract) but one whose `Glyph` always returns
`notdefGlyph`'s box. This last case - no `/FontFile*` entry at all,
meaning the PDF was authored assuming the *viewer* supplies the font
(the standard-14 names like Helvetica/Times/Arial, or any other
non-embedded font) - is exactly the case in the reported preview
(`2017 Pond Inspecttion.pdf`): named faces the file expects to be
available locally, that this package currently has no way to locate or
substitute. Running the reported repro command confirms this -
`-diagnostics` reports five distinct non-embedded `/TrueType` fonts, all
named with Windows's `...MT`/`...PSMT` PostScript-name convention rather
than a subset-tagged embedded name:

```text
font TimesNewRomanPSMT (TrueType) has no usable embedded TrueType outline data ...
font Arial-BoldMT (TrueType) has no usable embedded TrueType outline data ...
font TimesNewRomanPS-BoldMT (TrueType) has no usable embedded TrueType outline data ...
font ArialMT (TrueType) has no usable embedded TrueType outline data ...
font Arial-BoldItalicMT (TrueType) has no usable embedded TrueType outline data ...
```

These are a clean test case for `/BaseFont` parsing (see "What the audit
implies for a design" below): `TimesNewRomanPS-BoldMT` and
`Arial-BoldItalicMT` both need the `-Bold`/`-BoldItalic` infix
recognized and stripped, alongside the `PSMT`/`MT` suffix, to recover
the plain family names "Times New Roman" and "Arial" plus bold/italic
flags.

### The explicit "no system font service" policy

`docs/PLAN.md`'s "Dependency and safety policy" section
(`docs/PLAN.md:50`) states the core "must not use CGO, `os/exec`,
subprocess workers, or runtime loading of a system PDF library." The
`internal/fonts` package doc comment (`doc.go:37`) sharpens this
specifically for fonts:

> This package must never assume a system font is available and must
> never silently invoke a platform font service (fontconfig, Core Text,
> DirectWrite, and so on), because doing so would make rendering output
> depend on what happens to be installed on the machine running the
> code.

This is referenced again from `docs/PLAN.md:1059` as "a hard
requirement, not a convenience," and `docs/capability-matrix.md`'s
"Non-embedded font fallback" row states "No system font service is ever
queried." **Any font-substitution design has to be reconciled with this
policy rather than silently overridden** - see "Open questions" below
for how this document proposes doing that (the operative word in the
existing policy is "silently"; an explicit, opt-in, caller-controlled
mechanism is a different thing than an automatic background dependency).

### Font descriptor metadata this package already parses vs. ignores

`isSymbolic` (`simple.go:112`) reads `/FontDescriptor /Flags` for only
the symbolic bit (bit 3). No other descriptor field that would help
*match* a substitute font is read anywhere today:
`/FontFamily`, `/FontWeight`, `/ItalicAngle`, `/StemV`,
`/Flags`' serif (bit 2) and fixed-pitch (bit 1) bits,
`/Ascent`/`/Descent`/`/CapHeight`/`/FontBBox` are all present in the PDF
spec's FontDescriptor but unused by this package. `/BaseFont` (the
PostScript name, e.g. `Arial-BoldMT` or `ABCDEE+Verdana` with a subset
tag) is only ever read for diagnostic messages (`simple.go:60`,
`cid.go:51,63`), never parsed for family/weight/style hints.

### Diagnostics path

`internal/diag` (`diag.go`) is the existing, opt-in mechanism for
surfacing exactly this kind of "tolerated rather than rejected" event.
`simple.go:60` and `cid.go:51,63` already call `diag.Note` when a font
falls back to placeholder glyphs, naming the font's `/BaseFont` and
`/Subtype`. `pdfviewer.WithDiagnostics` (`options.go:65`) attaches a
`*Diagnostics` to a `Document`; `cmd/pdfpreview`'s `-diagnostics` flag
(referenced in the bug report) prints these to stdout. This is the
mechanism that surfaced the reported "font specifications ... were not
found, and were substituted for placeholder box characters" messages,
and it is the natural place to also report *which* substitute font (if
any) a new mechanism chose for a given fallback.

### Options pattern

`OpenOption` (`options.go:39`, mutating an internal `openConfig`) is the
existing extension point for `Open`/`OpenFile` and is where
`WithDiagnostics` lives. It's the natural home for whatever new option
enables font substitution (see "Configuration" below) - additive,
consistent with how diagnostics was introduced, and opt-in by
construction (a `Document` opened without it behaves exactly as today).

### No standard-14 metrics table

`simple.go:74` notes this package "ships no standard-14 (Helvetica,
Times, ...) AFM metrics table" - a non-embedded standard font's width
comes from `/Widths` when the PDF happens to include them (uncommon for
a non-embedded font, since the point of not embedding is relying on the
viewer), or the generic 500/1000-em default otherwise. This is a
related but separate gap from glyph *shape* substitution: even with a
perfect substitute font chosen, per-glyph *advance width* would still
use the substitute font's own metrics only if the substitution mechanism
also reads them (see "Widths vs. outlines" below) - today's width
fallback path is completely independent of `Glyph`.

## What the audit implies for a design

1. Font substitution is purely an extension of the existing fallback
   path: everywhere `notdefGlyph` is reached today because there is no
   usable `glyphSource`, a substitution mechanism would instead try to
   supply one. `Font`'s never-fail contract should be preserved -
   substitution failing (no candidate found, candidate fails to parse)
   should still fall back to `notdefGlyph`, not become a new error path.
2. The existing sfnt/TrueType parser (`truetype.go`) already does
   everything needed to extract outlines from a **TrueType** substitute
   font file. A substitute that happens to be OpenType-with-CFF-outlines
   (`OTTO`) would hit the same "not accepted" gap `truetype.go:142`
   documents for embedded fonts today - worth knowing since many system
   fonts (especially on macOS) are `.ttc`/OpenType-CFF, not plain
   TrueType.
3. Matching needs descriptor metadata this package currently discards
   (`/FontFamily`, `/Flags` serif/fixed-pitch bits, `/ItalicAngle`,
   `/FontWeight`/`/StemV`) plus parsing `/BaseFont` for family and
   style hints (e.g. stripping a subset tag, splitting "Arial-BoldMT"
   into family "Arial" + bold + not-italic). None of this parsing exists
   yet.
4. Locating candidate font files needs a new capability this project
   has none of today: enumerating and reading files from platform font
   directories, or from an explicitly configured directory/file list.
   This is the part that interacts with the "no system font service"
   policy.

## Proposed design

### Scope decision: directory scanning, not a font-service API

"No system font service" is best read as "no dependency on an external
process or platform API whose behavior this project can't control or
reproduce" (CGO bindings to Core Text/DirectWrite/fontconfig, `os/exec`
calls to `fc-match`, etc.) rather than "must never read a `.ttf` file
from disk." Reading files from a directory with `os.ReadDir`/`os.Open`
is exactly the kind of standard-library-only, portable operation the
rest of this project already does (parsing the PDF file itself, for
instance). The proposal is therefore:

- Substitution is driven by **directory scanning and this package's own
  `truetype.go` parser** - no CGO, no subprocess, no platform font API.
- Where those directories are is either **explicitly configured by the
  caller** or, if the caller opts in, **assumed from well-known
  per-platform default locations** (a plain list of paths, `GOOS`-gated,
  no API call to discover them). Both are opt-in via a new `OpenOption`
  (see below) - a `Document` opened without it does exactly what it does
  today, satisfying the "never silently" half of the existing policy.
- This keeps the mechanism 100% reproducible given the same directories
  and files (matching the project's existing "bounded, deterministic"
  ethos - see `docs/PLAN.md`'s cancellation/limits discussion), while
  still being non-portable *in outcome* (which fonts are actually found
  depends on what's on disk) - which is unavoidable for any real
  substitution feature and exactly why it must stay opt-in.

This scope decision is the crux of the whole design and should be
confirmed with the user/maintainer before anything else here is acted
on - see "Open questions."

### Pipeline sketch

```text
FontDescriptor / BaseFont
        |
        v
 descriptor.go (new): extract a FontCharacteristics
   { family string, bold, italic, serif, fixedPitch bool,
     weight int (from /FontWeight or bold flag), ... }
        |
        v
 substitution.go (new): given FontCharacteristics, ask a FontSource
 for the closest candidate font file
        |
        v
 candidate *.ttf/*.ttc path
        |
        v
 truetype.go's existing parseSfnt (already handles plain TrueType;
 .ttc collections and OTTO/CFF-outline OpenType need new handling -
 see "Format coverage" below)
        |
        v
 Font.glyphSource / Font.lookupGID, exactly like an embedded font today
```

### New package: `internal/fonts/substitute` (or a new file in
`internal/fonts`)

- `FontCharacteristics`: parsed from `/FontDescriptor` (`/FontFamily`,
  `/Flags`, `/ItalicAngle`, `/FontWeight`/`/StemV`) and `/BaseFont` as a
  fallback when `/FontDescriptor` is absent or sparse (common for
  standard-14 fonts, which often have no `/FontDescriptor` at all).
  `/BaseFont` parsing needs to strip a subset tag (`ABCDEF+`) and
  recognize common suffix conventions (`-Bold`, `-Italic`, `-BoldOblique`,
  `,Bold`, `MT`, `PS` naming, etc.) - real-world files are inconsistent
  here, so this should degrade gracefully (unrecognized suffix -> just
  use the name as the family).
- `FontSource` interface: something that can list candidate font files
  and their own characteristics (parsed once from each file's `name`
  table - already something `truetype.go` would need to read a bit more
  of) and hand back a parsed `sfntFont` for a chosen candidate.
  - `DirectorySource`: scans one or more configured directories
    (non-recursive vs. recursive - platform font directories are
    usually flat or one level deep) for `.ttf`/`.ttc`/`.otf` files.
  - Composed/cached: parsing every font's `name` table up front for
    every render would be wasteful; a source should build its candidate
    index once (lazily, on first substitution request) and reuse it for
    the life of a `Document`, mirroring how `FontCache`
    (`internal/content/fontcache.go`) already caches parsed embedded
    fonts per-`Document`.
- Matching algorithm: start simple and explicit rather than a fuzzy
  scoring system that's hard to reason about -
  1. Exact family-name match (case-insensitive) among candidates, then
     pick the one whose bold/italic flags match.
  2. No family match: fall back to a small built-in table mapping
     standard-14 names (Helvetica, Times, Courier, Symbol, ZapfDingbats
     and their bold/italic/oblique variants) to generic family
     categories (sans-serif, serif, monospace), then pick any installed
     candidate in that category matching bold/italic.
  3. No category match either: use the descriptor's serif/fixed-pitch
     flags (if present) the same way.
  4. Nothing usable found: unchanged fallback to `notdefGlyph` (never a
     hard error - preserves `Font`'s existing contract).
  - Every step should be simple enough to unit test with a handful of
    fake candidate fonts, without needing real system font files present
    in CI (see "Testing" below).

### Format coverage

Substitute candidates found on a real system will very often be `.ttc`
(TrueType Collection, common on macOS/Windows for CJK and family
bundles) or OpenType with CFF outlines (`OTTO`) rather than plain
TrueType `glyf` outlines - `truetype.go:142`'s doc comment already notes
`OTTO` is deliberately rejected today. **Decided** (see "Open
questions"): both are in scope, not skipped -

- `.ttc` support (picking one font out of a collection's table
  directory) is a bounded, mechanical addition to `truetype.go` -
  parsing a `ttcf` header to get to the individual `sfnt` offsets it
  already knows how to read from there. Planned for Phase 2 (see
  "Implementation" below).
- CFF-outline (`OTTO`) support is a much bigger undertaking (a full CFF
  Type2 charstring interpreter) - the same scope this project has
  already deferred for *embedded* Type1C/CIDFontType0C fonts (see
  `docs/capability-matrix.md`'s "OpenType/CFF" row). Explicitly in
  scope here because it is important for macOS, whose system font
  directories lean heavily on CFF-outline `.ttc`/`.otf` files - but
  large enough to warrant its own phase (Phase 3) rather than being
  folded into directory scanning itself. Once built, it also closes the
  long-standing embedded-CFF gap in `docs/capability-matrix.md` for
  free, since the same charstring interpreter serves both a
  `/FontFile3` embedded program and a candidate file found on disk.

### Widths vs. outlines

`Font.Width` and `Font.Glyph` are independent today (`font.go:86` vs.
`font.go:101`) - a substituted glyph outline does not, by itself, change
what `Width` returns. Two options, not mutually exclusive:

- Leave `Width` exactly as-is (uses `/Widths`/`/W` or the generic
  default) - simplest, and correct whenever the PDF *does* supply
  `/Widths` (very common even for non-embedded fonts, since layout was
  computed against *some* real font at authoring time).
- When `/Widths` is absent *and* a substitute was found, optionally use
  the substitute font's own `hmtx` advance widths (already parsed by
  `truetype.go` for embedded fonts) instead of the generic 500/1000-em
  default - a strict improvement for that specific gap, but changes
  positioning versus today's output, so it should be its own clearly
  logged decision (a diagnostic note) rather than a silent change.

### Configuration (`OpenOption` additions)

Mirroring `WithDiagnostics`'s shape:

```go
func WithFontSubstitution(cfg FontSubstitution) OpenOption
```

**Decided** (see "Open questions" #2): opting into substitution at all
turns on platform-default directory scanning too, by default - the
"never silently" policy is satisfied by `WithFontSubstitution` itself
being opt-in (a `Document` opened without it behaves exactly as today),
not by making the caller separately opt into *where* it looks. To get
"on by default" from Go's own zero-value semantics (so a caller who
writes `FontSubstitution{}` gets the useful default rather than a
silently-empty configuration), the field is phrased as an opt-*out*:

```go
type FontSubstitution struct {
    // Directories are explicit paths to scan, checked before any
    // platform-default directory (highest priority - an explicit
    // caller-supplied font should always win over one this package
    // merely guessed at from the OS).
    Directories []string

    // DisableSystemDefaults, if true, turns off scanning this
    // package's built-in, GOOS-gated list of common platform font
    // directories (see below), leaving only Directories. The zero
    // value (false) means platform defaults ARE scanned - this is
    // the field's whole reason for being named as a negative: Go
    // gives every unset bool field false, so FontSubstitution{} (or
    // a struct literal that only sets Directories) still gets
    // system-default scanning without the caller needing to say so
    // twice.
}
```

Default per-platform lists to ship (subject to verification per
platform - see "Open questions"), **ordered most-local to least-local**
per the user's decision (Open questions #2): a font a specific user
installed for themselves should be found, and preferred over same-named
matches, before one installed machine-wide or shipped by the OS. Within
`DirectorySource` (Phase 4), this ordering is what breaks ties when more
than one candidate matches a `FontCharacteristics` equally well - the
first-scanned directory's candidate wins.

- macOS: `~/Library/Fonts` (per-user), then `/Library/Fonts`
  (machine-wide, admin-installed), then `/System/Library/Fonts`
  (Apple-shipped).
- Linux: `~/.local/share/fonts` (per-user, recursive - distros nest by
  family), then `/usr/local/share/fonts` (machine-wide, locally
  installed), then `/usr/share/fonts` (distro-packaged).
- Windows: no real per-user vs. system distinction in the common case -
  `%WINDIR%\Fonts` (typically `C:\Windows\Fonts`); a per-user
  `%LOCALAPPDATA%\Microsoft\Windows\Fonts` also exists on modern Windows
  (fonts installed without admin rights) and should be scanned first
  when present, mirroring the same most-local-first principle.

### Diagnostics integration

Extend the existing `diag.Note` call sites (`simple.go:60`, `cid.go:63`)
so that when substitution is enabled, the message reflects the actual
outcome: which substitute (family/file) was chosen, or that none was
found and the placeholder box is being used - giving a caller using
`-diagnostics` (as in the reported bug) direct visibility into *why* a
given glyph looks the way it does, matching this package's existing
"explicit, in-package, documented decision" philosophy
(`doc.go:45`).

### Caching

A `FontSource`'s directory scan and per-candidate `name`-table parse
should be built once and reused for a `Document`'s lifetime (like
`FontCache` today), and matching results per distinct
`FontCharacteristics` are cheap enough to not need their own cache.
Given the existing "a `*Document` is not safe for concurrent use"
decision (`docs/PLAN.md`'s Phase 6a), a `FontSource`'s index can follow
`FontCache`'s existing precedent of no internal locking.

## Testing

- Descriptor/BaseFont parsing (`FontCharacteristics` extraction): pure
  unit tests, no real fonts needed.
- Matching algorithm: unit tests against small in-memory fake
  `FontSource` implementations (a handful of synthetic candidates with
  known characteristics) - deterministic, no dependency on what's
  actually installed on the machine running `go test`, consistent with
  how `truetype_test.go`/`fuzz_test.go` already avoid depending on real
  font files.
- `.ttc` parsing: a small synthetic collection fixture (following
  `truetype_test.go`'s existing pattern of hand-built minimal sfnt
  fixtures) rather than checking in a real system font.
- End-to-end (a document with a non-embedded font, substitution
  enabled, real placeholder-box count drops to zero / outlines appear):
  worth one integration-style test, but it needs a real `.ttf` fixture
  checked into `testdata` (a small permissively-licensed font, e.g. one
  of the existing Liberation/DejaVu-style metric-compatible fonts, or a
  minimal hand-built one) rather than depending on the CI machine having
  any particular font installed - **do not** write a test that depends
  on `UseSystemDefaults` finding something real on the CI runner.

## Open questions (resolved)

All six were resolved with the user on 2026-09-09; kept here (rather than
deleted) as a record of the decision and its rationale, since later
phases depend on knowing *why*, not just *what*.

1. **Does directory-scanning-for-local-files count as violating "no
   system font service"?** **Resolved: no.** The user agreed this is an
   acceptable feature, confirming this document's reading (see "Scope
   decision" above) - no CGO, no subprocess, no platform font API, and
   opt-in via `WithFontSubstitution`. `docs/PLAN.md`'s "Dependency and
   safety policy" wording should still be revisited when Phase 4 lands,
   to make the distinction explicit there rather than only in this doc.
2. **Should system-default directories be scanned automatically once
   substitution is opted into?** **Resolved: yes, on by default.** Also
   resolved: search order must go most-local to least-local (a
   per-user-installed font should win over a same-named system one) -
   see the revised "Configuration" section above for the concrete
   `DisableSystemDefaults`-phrased field and the per-platform ordering.
3. **Is CFF-outline (`OTTO`) substitute-font support in scope?**
   **Resolved: yes, explicitly in scope**, specifically because it
   matters for macOS's default font directories. Given its size, it is
   its own phase (Phase 3) rather than bundled into directory scanning -
   see "Format coverage" above and "Implementation" below.
4. **Ship a bundled fallback font?** **Resolved: not for now.** The user
   would rather not bundle one, mainly due to uncertainty about finding
   a truly compatible license, but asked to be told about a suitable
   candidate if one is known. There is one worth naming: Google's
   **Arimo**, **Tinos**, and **Cousine** (the "Chrome OS core fonts",
   descended from the earlier Ascender/Liberation metric-compatible
   family) are licensed under the **Apache License 2.0** and were
   purpose-built as metric-compatible substitutes for exactly
   Arial/Helvetica, Times New Roman, and Courier New respectively - the
   same three families this project's own standard-14 fallback table
   already needs to categorize (see "Matching algorithm" above). Apache
   2.0 is not textually MIT - it is also permissive and does not require
   derivative works to be relicensed (no copyleft), but it does require
   preserving the license text/attribution (and, technically, carries an
   explicit patent grant clause MIT does not have) - a real distinction
   worth the user's own explicit sign-off before any font binary is
   committed to this repository, not something to decide unilaterally.
   Recorded as **Phase 5, deferred** below: not started, blocked on that
   explicit decision (which specific font(s), and how the license file
   is carried alongside this project's own `LICENSE`).
5. **Exact matching precedence when signals conflict?** **Resolved: as
   proposed** - family-name match wins over an inferred category, which
   wins over raw descriptor flags. Confirming this against real sample
   files (including `2017 Pond Inspecttion.pdf`'s own font dictionaries)
   remains a good early task within Phase 4's testing, not a blocker to
   starting implementation.
6. **Width-from-substitute behavior in scope?** **Resolved: yes**,
   included as part of Phase 4 (see "Widths vs. outlines" above).

## Implementation

This section is the authoritative, living record of the phase plan and
each phase's actual status - kept current as work happens, so that
picking this project back up after a gap only requires reading this
section (plus, for a phase in progress, the "Resume notes" left in it)
rather than reconstructing context from scratch. Each phase has its own
subsection below with the same shape: **Goal**, **Deliverables**,
**Depends on**, **Non-goals** (explicitly deferred to a later phase, so
scope doesn't creep during implementation), and **Status**.

The five phases exist because the full feature is too large and too
risky to land as one change: Phase 3 (CFF outlines) in particular is a
substantial, independently useful piece of work - implementing a Type2
charstring interpreter - that would otherwise dominate and obscure
review of everything else. Splitting also means Phases 1-2 produce
real, independently testable, mergeable value (better font
*characterization*) before any file-system scanning or matching
behavior exists at all.

### Phase 1 - Font characteristics extraction

**Goal.** Turn a font dictionary's `/FontDescriptor` and `/BaseFont`
into a small, structured `FontCharacteristics` value (family name,
bold, italic, fixed-pitch, serif, numeric weight) that later phases can
compare against candidate font files. Pure data transformation - no
directory scanning, no file I/O, no change to `Load`'s existing
behavior or `Font`'s fallback path.

**Deliverables.**

- `internal/fonts/descriptor.go`: the `FontCharacteristics` type, a
  `Characterize(dict syntax.Dictionary, resolver Resolver)
  FontCharacteristics` function that reads `/FontDescriptor` (`/Flags`
  bits for fixed-pitch/serif/italic/force-bold, `/FontWeight`,
  `/ItalicAngle`) and falls back to parsing `/BaseFont` when the
  descriptor is absent or silent on a given trait (common for
  standard-14 fonts, which often carry no `/FontDescriptor` at all -
  confirmed by the reported repro file's own diagnostics, whose five
  fonts are exactly this case).
- A `ParsePostScriptName(name string) (family string, bold, italic
  bool)` helper: strips a subset tag (`ABCDEF+`), recognizes common
  PostScript style infixes/suffixes (`-Bold`, `-Italic`,
  `-BoldItalic`, `-Oblique`, `-BoldOblique`, trailing `MT`/`PSMT`/`PS`
  foundry markers), and applies a best-effort camelCase-to-spaced-words
  transform so `"TimesNewRoman"` reads as `"Times New Roman"` (matching
  how real installed font family names are usually spelled, which
  matters once Phase 4 compares against them).
- `internal/fonts/descriptor_test.go`: table-driven unit tests,
  including the five real `/BaseFont` names from the reported repro
  file (`TimesNewRomanPSMT`, `Arial-BoldMT`, `TimesNewRomanPS-BoldMT`,
  `ArialMT`, `Arial-BoldItalicMT`) as concrete cases, plus synthetic
  `/FontDescriptor` dictionaries exercising each `/Flags` bit and
  `/FontWeight`/`/ItalicAngle`.

**Depends on.** Nothing new - only the existing `syntax.Dictionary` /
`Resolver` / `numberValue` / `dictValue` machinery already in
`internal/fonts`.

**Non-goals (deferred).** Guessing *serif-ness* from a family name
alone (e.g. inferring "Georgia" is serif from the name) is explicitly
out of scope here - Phase 1 only reads the descriptor's own Serif flag
when present. Serif/category inference for a name with no reliable
descriptor bit is Phase 4's job, via the standard-14 category table
("Matching algorithm" above), which already needs to solve exactly this
problem for well-known names. Nothing in this phase reads a file from
disk or changes what `loadSimpleFont`/`loadType0Font` do.

**Status.** Done (this session). See `internal/fonts/descriptor.go` and
`internal/fonts/descriptor_test.go`.

### Phase 2 - sfnt format extensions for candidate font files

**Goal.** Extend this package's own sfnt reader (`truetype.go`) so it
can (a) describe an arbitrary font *file* found on disk the same way
Phase 1 describes a PDF font dictionary, and (b) handle TrueType
Collection (`.ttc`) containers, which bundle several faces in one file
and are extremely common among real system fonts (especially on
macOS/Windows).

**Deliverables.**

- Parse the sfnt `name` table (platform/encoding-aware, reusing the
  existing platform-3-preferred-else-platform-1 selection precedent
  `cmap.go`'s `selectCmapSubtable` already established) far enough to
  read name IDs 1 (family), 2 (subfamily), 4 (full name), and 6
  (PostScript name).
- Parse the sfnt `OS/2` table (`usWeightClass`, `fsSelection` bold/
  italic bits, `sFamilyClass`) as a more reliable characterization
  source for a real font *file* than guessing from its filename -
  converges on the same `FontCharacteristics` shape Phase 1 defined, so
  Phase 4's matcher compares like with like.
- Parse the `ttcf` collection header so a single `.ttc` file's N
  component faces (each its own ordinary table directory, per the
  existing `parseSfnt` entry point) can be enumerated and independently
  selected by offset/index, rather than this package only ever
  understanding a bare single-face sfnt file as it does today.
- A candidate-facing entry point, something like
  `ProbeFontFile(data []byte) ([]FontFace, error)`, returning one
  `FontFace{Characteristics FontCharacteristics, ...}` per face in the
  file (one element for a plain `.ttf`/`.otf`, N for a `.ttc`), each
  carrying enough to later fully parse that specific face's outlines on
  demand (Phase 4 should not need to re-probe every candidate's full
  glyph tables just to decide *whether* to use it).
- Unit tests using small, hand-built synthetic sfnt/`.ttc` fixtures
  (following `truetype_test.go`'s existing precedent), not real
  checked-in system fonts.

**Depends on.** Phase 1's `FontCharacteristics` type (reused as the
output shape here).

**Non-goals (deferred).** No CFF/`OTTO` outline extraction yet - an
`OTTO`-flavored sfnt's `name`/`OS/2` tables can still be read by this
phase's work (those tables are identical in layout regardless of
whether the file's outlines are `glyf` or `CFF`), so an `OTTO` file can
be *characterized* in Phase 2 even though it can't have its glyph
outlines *extracted* until Phase 3 lands - `ProbeFontFile` should
therefore not reject `OTTO` files outright, just mark that face as
"outline extraction not yet available" so Phase 4 can still match
against it and Phase 3 can later fill in the gap without reshaping this
phase's API. No directory scanning yet (this phase operates on `[]byte`
already read into memory by its caller/tests).

**Status.** Done (this session). See `internal/fonts/probe.go`,
`internal/fonts/probe_test.go`, and the supporting refactor of
`internal/fonts/truetype.go`'s `parseTableDirectory`/`parseSfnt` (split
into a directory-offset-aware `parseTableDirectory`/`parseSfntAt` pair so
a `.ttc` face's table directory, which does not start at byte 0 of the
file, can be parsed with the same code an ordinary single-face file
uses). `ProbeFontFile` returns one `FontFace` per face (handling both a
plain file and a `.ttc` collection), each with a `FontCharacteristics`
derived from that face's `name`/`OS/2` tables (falling back to
`ParsePostScriptName`/`detectStyleTokens`, reusing Phase 1's helpers,
when a table is absent) and a `HasOutlines` flag that is `false` for an
`OTTO` (CFF-outline) face per this phase's non-goal. A `FuzzProbeFontFile`
target was added alongside the existing `FuzzParseSfnt`, covering the new
`.ttc`/`name`/`OS/2` parsing surface.

### Phase 3 - CFF / OpenType-CFF (OTTO) outline support

**Goal.** Implement enough of the CFF format (INDEX/DICT structures,
charset, and the Type2 charstring instruction set) to extract real glyph
outlines from a bare CFF program and from an `OTTO`-flavored sfnt's
`CFF` table (the table tag itself is four bytes, `"CFF "`, but is
referred to here without the padding space for readability) -
unlocking both embedded `/FontFile3` fonts
(`docs/capability-matrix.md`'s long-standing "OpenType/CFF: Not
started" row) and CFF-outline candidate files found on disk (the
common case for several macOS system fonts).

**Deliverables.**

- `internal/fonts/cff.go` (or similar): CFF INDEX/DICT/charset parsing
  and a Type2 charstring interpreter producing the same
  `graphics.Path`-shaped glyph outlines `truetype.go`'s `glyf` parsing
  already produces, so the rest of the pipeline (`scaleGlyph`, `Font`'s
  fallback logic) needs no changes to consume either source.
- Wired into `simple.go`'s embedded-font loading for `/FontFile3`
  (bare CFF/Type1C) and into `cid.go` for a `CIDFontType0` descendant's
  own CFF program - closing the existing embedded-CFF gap, independent
  of font substitution.
- Wired into Phase 2's `ProbeFontFile`/`FontFace` so a `CFF`-table sfnt
  or a bare `.otf` candidate file can have its outlines extracted too,
  not just characterized.
- A charstring interpreter fuzz test mirroring `fuzz_test.go`'s
  existing `FuzzParseSfnt` coverage (malformed/truncated CFF data must
  fail closed, never panic or hang - `maxCompositeDepth`'s existing
  precedent for bounding recursive/self-referential structures applies
  equally to CFF's own subroutine call mechanism).
- `docs/capability-matrix.md` update: flip "OpenType/CFF (Type1C,
  CIDFontType0C)" from "Not started" to "Done" (or "Partial", if some
  sub-feature - e.g. CFF2 variable fonts - is explicitly deferred
  further within this same phase).

**Depends on.** Phase 2 (for the candidate-file wiring half of this
phase; the embedded-`/FontFile3` half depends on nothing but existing
`internal/fonts` machinery and could technically be built first/
independently if useful to de-risk).

**Non-goals (deferred).** CFF2 (the variable-font-flavored successor
format, distinct from CFF/"CFF " table) is not required - no font this
project needs to *read* as a substitute is expected to be
variable-only. Hinting (CFF's own hint operators, `hstem`/`vstem`/
hint-replacement) is parsed only as far as needed to correctly skip
those operators' operands when interpreting a charstring - actual
hinting/grid-fitting is out of scope, matching this project's existing
"no hinting" precedent for TrueType outlines.

**Status.** Not started.

### Phase 4 - Font source, matching, and wiring

**Goal.** The "make it real" phase: turn Phases 1-3's building blocks
into an actual opt-in substitution feature a caller can enable, wired
into the existing font-loading fallback path.

**Deliverables.**

- `FontSource` interface and a `DirectorySource` implementation
  (`os.ReadDir`/`os.Open` only - no CGO, no subprocess, no platform font
  API, per the resolved policy question) that scans configured
  directories, builds a candidate index via Phase 2's `ProbeFontFile`
  (lazily, once per `Document`, cached like `FontCache` already is -
  see "Caching" above), and hands back a chosen `FontFace` for a given
  `FontCharacteristics` query.
- The `WithFontSubstitution` `OpenOption` and `FontSubstitution` struct
  exactly as specified in the revised "Configuration" section above
  (`Directories []string`, `DisableSystemDefaults bool` with
  scan-by-default zero-value semantics), plus the `GOOS`-gated,
  most-local-first default directory lists for darwin/linux/windows
  also specified there.
- The matching algorithm from "Matching algorithm" above (exact family
  match, then the standard-14 category table, then raw descriptor
  flags), including the small built-in standard-14-name-to-category
  table itself.
- Wiring into `simple.go`'s `loadSimpleFont` and `cid.go`'s
  `loadType0Font`: when no embedded outline was found *and*
  substitution is enabled for the current `Document`, attempt a
  substitute before falling back to `notdefGlyph`. This needs a way for
  `internal/fonts.Load`'s call sites to reach the configured
  `FontSource` - likely threaded the same way `diag.Note` reaches a
  `Resolver` today (a small interface a `Document`/resolver
  optionally implements), rather than growing `Load`'s own parameter
  list.
- Diagnostics updates at the existing `diag.Note` call sites
  (`simple.go:60`, `cid.go:51,63`) reporting which substitute
  (family/file/face) was chosen, or that substitution was enabled but
  found nothing.
- Width-from-substitute: when `/Widths`/`/W` is absent and a substitute
  face was used, prefer that face's own `hmtx` advances over the
  generic `defaultMissingWidth` constant, logged as its own diagnostic
  (see "Widths vs. outlines" above).
- Integration tests using synthetic `testdata` fixtures (per "Testing"
  above) - explicitly never a test that depends on `DisableSystemDefaults`
  finding something real on the CI runner - plus, as a manual sanity
  check (not a committed test, since it depends on the local machine's
  installed fonts), re-running the reported repro command
  (`go run ./cmd/pdfpreview -scale 4 -diagnostics -out ...
  '2017 Pond Inspecttion.pdf'`) with substitution enabled to confirm
  the five Arial/Times fonts it names actually resolve to real outlines
  on a real macOS machine.
- `docs/capability-matrix.md` update: a new "Font substitution" row (or
  small set of rows) under Fonts.

**Depends on.** Phases 1-3 (uses `FontCharacteristics` from Phase 1,
`ProbeFontFile`/`FontFace` from Phase 2, and CFF outline extraction
from Phase 3 for the many real-world candidates that are CFF-outline
files).

**Non-goals (deferred).** Phase 5's bundled last-resort font (see
below) - Phase 4's `FontSource` design should not preclude adding one
later (an in-memory/embedded `FontSource` implementation should fit the
same interface as `DirectorySource`), but does not include one itself.

**Status.** Not started.

### Phase 5 - Bundled last-resort font (deferred, not started)

**Goal.** An always-available, zero-configuration last-resort candidate
(via `go:embed`) for when no configured or system-discovered font
matches at all - see "Open questions" #4 above for the full discussion
and the Arimo/Tinos/Cousine (Apache License 2.0) recommendation.

**Status.** Explicitly **not started and not scheduled** - blocked on
the user making an explicit, separate decision on (a) which specific
font(s) to bundle and (b) how that font's license is carried alongside
this project's own `LICENSE` file, per the resolution of open question 4
above. Do not begin this phase without that sign-off, even if Phase 4 is
otherwise complete and this would be a natural-feeling next step.

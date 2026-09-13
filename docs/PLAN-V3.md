# PLAN3: Post-v0.3.1 capability expansion

`docs/PLAN2.md`'s Phase 7-18 plan is complete (see its own Progress
Log); every deferral it scheduled has landed or been explicitly moved
to its Backlog section. This document picks up from there, the same
way PLAN2 picked up from PLAN.md: a living plan for gaps found *after*
that point, so "what's next" keeps having one authoritative answer
rather than scattering across ad-hoc notes.

Unlike PLAN2 (which started from a systematic sweep of
`docs/CAPABILITY-MATRIX.md`'s "Not started"/"Not scheduled" rows), this
document currently exists because of a single concrete trigger: a
real-world PDF a user is actually working with
(`2014ElectionManual-stripped.pdf`, a New York State Board of Elections
document) hit a gap PLAN2 knew about but never scheduled - Type 4
(PostScript calculator) functions, called out as unimplemented in
`internal/function/doc.go` since that package was first written and
never revisited because it was assumed rare. It is not rare enough:
see Phase 19 below. Add further phases here the same way if more
post-v0.3.1 gaps turn up, following the same severity-ordering rule.

**How phases here are ordered.** Same rule PLAN2 used:

- **Hard failure** (the document does not open at all, or a page
    throws `ErrUnsupported`/aborts rendering instead of finishing)
    ranks above
- **Visible defect** (the page renders, but specific content is wrong
    or missing) which ranks above
- **Cosmetic/fidelity gap** (the page renders correctly in substance
    but not exact fine detail).

Within similar severity, more common document types rank above rarer
ones. This is an estimate, revised when concrete evidence (a user's
own file, as with Phase 19) contradicts it - which is exactly how
Phase 19 came to be the first entry here despite PLAN2's Backlog
section never mentioning it at all.

Each phase should update `docs/CAPABILITY-MATRIX.md`'s corresponding
row(s) in the same change that changes them, and land as
independently-committable sub-phases per the project's established
workflow (implement, test/fuzz, commit, push).

**Status legend:** Not started / In progress / Done, same as PLAN2 and
the capability matrix.

## Phase 19: Type 4 (PostScript calculator) functions

**Status: Done (19a-19d complete).**

PDF Functions (ISO 32000-1 7.10) have four representations; this
project's `internal/function` package (built for PLAN.md's Phase 5)
implements three of them - Type 0 (sampled), Type 2 (exponential
interpolation), Type 3 (stitching) - and explicitly declines the
fourth, Type 4 (a small embedded PostScript-like calculator language,
7.10.5/Annex B), on the stated assumption that it is "materially rarer
in real-world content than the other three combined." `Parse` returns
`pdferror.ErrUnsupported` naming it explicitly rather than
miscomputing or panicking - this project's usual policy for a
recognized-but-unimplemented feature - so today's failure mode is
already "clean" in the sense of not corrupting output, but it is not
uniform: this phase's own investigation (see below) found the
severity actually depends on *which* of the two real call sites hits
it, because only one of them has a fallback at all.

- **Confirmed real-world occurrence.** The trigger file's `CS1`
    resource (used for body text and headers via `cs`/`scn`) resolves
    to `[/DeviceN [/Black] /DeviceCMYK <tint-transform> <attrs>]` - a
    single-colorant ("Black") spot-color space whose tint transform is
    a Type 4 function (`/Domain [0 1] /Range [0 1 0 1 0 1 0 1]`,
    computing a four-way complement of its one input via `roll`/`sub`
    to produce CMYK). This is a completely ordinary prepress/InDesign-
    style pattern for a single named ink, not an exotic one - any PDF
    produced this way (spot-color or "rich black" workflows are common
    in print-origin documents: government forms, newsletters,
    packaging proofs) will hit this same gap.
- **Severity differs by call site - both existing callers of
    `function.Parse` need checking, not just the one the diagnostic
    happened to surface:**
    - **`cs`/`CS`/`sc`/`scn`/`SC`/`SCN` via a content stream's
        `/Resources`/`ColorSpace` entry** (`internal/content/
        colorspace.go`'s `setColorSpace`) - **visible defect only**,
        because this path already catches the resolution error, logs
        the `diag.Note` the user saw, and falls back to guessing
        DeviceGray/RGB/CMYK from the operand count
        (`colorFromComponents`). This is the case the reported
        diagnostic came from, and text/fills painted this way still
        render, just with the wrong (guessed, not transformed) color.
    - **An image's own `/ColorSpace`** being Separation/DeviceN with a
        Type 4 tint transform (`internal/image/colorspace.go`'s
        `resolveSeparationOrDeviceN`, via `function.Parse` at line 476)
        - **hard failure**: the error propagates out of color space
            resolution with no fallback, which fails decoding the
            image entirely (unlike the content-stream case, nothing
            here catches it).
    - **A shading's `/Function` entry** (`internal/content/
        shading.go`'s `doShading` and `resolvePatternPaint`, via
        `function.Parse` at line 246/337) - **hard failure**: also
        uncaught, propagating up through `sh` or a shading-pattern
        fill and aborting that operator (and, per this project's
        existing render-error surfacing, potentially the whole page).
        Type 4 is a common choice specifically for shadings that need
        a custom (non-linear, non-power-curve) color ramp a Type 2/3
        function's fixed shapes cannot express - exactly the kind of
        content Type 0/2/3 already cover the *simple* cases of.
- **Not a hard failure for the document as a whole** in the trigger
    file's own case (the content-stream fallback means the page still
    renders, just with wrong colors in one spot-color channel) - hence
    ranked as a **visible defect** overall, but with a **hard-failure**
    tail risk for any file that instead hits it through an image or a
    shading, which is why this phase fixes the underlying gap
    (implementing Type 4 itself) rather than just extending the
    existing fallback to more call sites.

- **19a: the PostScript calculator language interpreter**
    (`internal/function/type4.go`, new). Scope is exactly Annex B's
    operator set - deliberately much smaller than a general PostScript
    interpreter, and smaller than this project's own existing Type 1
    charstring interpreter (Phase 11) despite the superficial "stack-
    based interpreter over embedded bytecode" similarity:
    - **Arithmetic:** `abs add atan ceiling cos cvi cvr div exp floor
        idiv ln log mod mul neg round sin sqrt sub truncate`.
    - **Relational/boolean/bitwise:** `and or xor not bitshift eq ne
        gt ge lt le true false`.
    - **Stack:** `pop exch dup copy index roll`.
    - **Conditional:** `if ifelse` (the *only* control-flow forms the
        language has - per 7.10.5, "these are the only two operators
        that are not idempotent" - implemented as nested `{ ... }`
        procedure blocks, parsed once into a small block-structured
        program rather than re-tokenized on every `Eval` call, matching
        this package's existing "parse once, evaluate many times" split
        stated in the package doc comment).
    - **Explicitly not needed:** the full PostScript language's named
        procedures, variables, loops (`for`/`repeat`/`loop`), string/
        array/dictionary operators, and I/O - 7.10.5 restricts a
        Type 4 function's content to exactly the operator subset above,
        so nothing else is reachable from a conformant function stream
        and nothing else needs implementing (a non-conformant stream
        using a disallowed operator is a malformed-input error, not a
        silently-ignored one).
    - **Safety, since this executes embedded content from an untrusted
        file:** bound total executed-operator count and `if`/`ifelse`
        nesting depth against a hostile or degenerate program (this
        project's established policy for any interpreter over
        attacker-influenced bytecode - see `internal/content/
        interpret.go`'s form-recursion depth limit and the JBIG2/JPX
        packages' own decode-bound checks); guard `copy`/`roll`/`index`
        against out-of-range or absurd counts against the operand
        stack rather than panicking or unbounded-allocating; route every
        arithmetic result through this package's existing `safeFloat`
        (NaN/Inf-to-0) exactly as Type 2/3 already do, so a malformed
        function can only ever produce a wrong-looking finite color.
    - Parse's Type 4 entry: `/Domain` (required, like every function
        type) and `/Range` (required for Type 4 specifically, unlike
        Type 2/3 - 7.10.5's function-dictionary table - since a Type 4
        program's own output arity is exactly `len(Range)/2` with no
        other way to know it, the same reason Type 0 already requires
        it).
- **19b: wire into `internal/function.Parse`.** Replace
    `parseSingle`'s `case 4:` `pdferror.Unsupportedf` branch with a
    call to the new `parseType4`; no changes needed at either call site
    (`internal/image/colorspace.go`, `internal/content/shading.go`)
    since both already go through `function.Parse` uniformly and treat
    whatever `Function` comes back identically regardless of type -
    the whole point of the `Function` interface. Update
    `internal/function/doc.go`'s package comment (currently states
    Type 4 "is not implemented" as a deliberate scope cut) and
    `internal/content/colorspace.go`'s `setColorSpace` doc comment
    (currently names "a Type 4 tint transform function" as its example
    of an unresolvable color space).
- **19c: fixtures and real-world validation.** A `tools/genfixtures`
    Type 4 tint-transform fixture (a DeviceN/Separation color space
    whose function is a small hand-written calculator program,
    mirroring the trigger file's own complement-style transform) and a
    Type 4 axial-shading fixture (a custom non-power-curve color ramp
    Type 2/3 cannot express, proving the shading call site as well as
    the color-space one); both added to
    `TestRenderMatchesReferenceImages` with checked-in golden PNGs.
    Unit tests in `internal/function/type4_test.go` covering every
    operator in 19a's list individually, `if`/`ifelse` (including
    nested blocks), a stack-underflow/malformed-program case per
    operator class, and the safety bounds (an adversarially long or
    deeply-nested program still returns promptly with a bounded-error
    result rather than hanging or panicking - a fuzz target,
    `FuzzType4Eval` or similar, alongside this package's existing
    `function_test.go` coverage). Additionally, re-render the actual
    trigger file (`2014ElectionManual-stripped.pdf`, kept local per
    this project's existing policy of not committing user-supplied
    non-fixture PDFs) before and after, confirming the `CS1` diagnostic
    no longer appears and the previously-guessed spot-color text/fills
    now render with the correct transformed color.
- **19d: documentation.** `docs/CAPABILITY-MATRIX.md`'s Separation/
    DeviceN row (currently notes "Type 4 PostScript-calculator functions
    are unsupported") and Shading row updated to reflect full Function
    Type 0/2/3/4 support; `FIXTURES.md` documents the two new fixtures.

**Exit criteria:** a Type 4 tint-transform color space (Separation,
DeviceN, or an image's `/ColorSpace`) and a Type 4 shading `/Function`
both evaluate correctly rather than falling back or failing; the
trigger file's `CS1` resolves without a diagnostic and its
previously-guessed colors match the spot-color complement transform;
`internal/function`'s full existing test suite plus 19a/19c's new
coverage, `go vet`, `gofmt`, and fuzzing all pass clean.

## Backlog

No new backlog items yet - this document currently has exactly one
phase, seeded by one concrete report. See `docs/PLAN2.md`'s own
Backlog section for every other known, deliberately-unscheduled gap;
it continues to apply unchanged (this document does not duplicate it).
Add an item here, rather than there, only once it is specifically a
*post*-PLAN2 finding.

## How to use this document

Same as PLAN2: update a phase's status and add a Progress Log entry
(mirroring PLAN.md/PLAN2.md's own format) as work actually lands, and
update `docs/CAPABILITY-MATRIX.md`'s corresponding rows in the same
change. Add new phases here, in severity order, as further post-v0.3.1
gaps are found - do not restart a fourth document until this one is
similarly complete.

## Progress Log

This section mirrors `docs/PLAN2.md`'s own Progress Log: updated at
the end of each phase (or sub-phase) with what was actually built,
appended in order, not rewritten later except to fix mistakes.

### Phase 19a: the PostScript calculator language interpreter — done (2026-09-13)

- **`internal/function/type4.go` (new).** A from-scratch interpreter for
    the Annex B/7.10.5 operator subset, split into the same two-pass
    "parse once, evaluate many times" shape this package's doc comment
    describes for Type 0/2/3:
    - **Parsing** (`parseType4Program`, `parseType4Block`, `t4Lexer`): a
        small hand-written lexer over the stream's raw bytes (numbers via
        `strconv.ParseFloat`, explicitly rejecting the literal words
        "NaN"/"Inf"/"+Inf"/"-Inf" so they fall through to a clean
        "unknown operator" parse error instead of ever reaching the
        stack) feeding a recursive-descent parser that turns `{ ... }`
        procedure blocks into a `t4Instr` tree, structurally recognizing
        `if`/`ifelse` (the only operators that take a procedure as an
        operand) rather than treating them as ordinary named operators.
        Every other operator name is checked against
        `allowedType4Operators` (the exact arithmetic/relational-boolean-
        bitwise/stack set Annex B permits) at parse time, so a
        non-conformant stream fails with a clear `pdferror.Malformedf`
        before evaluation ever runs.
    - **Evaluation** (`execType4Block`, `applyType4Operator`): a plain
        `[]float64` operand stack (PostScript booleans represented as
        0.0/1.0 - see `isType4Bool`'s doc comment for why that is exact
        for every boolean value this interpreter itself ever produces).
        Every pushed value is routed through the package's existing
        `safeFloat`, matching Type 2/3's own NaN/Inf discipline; a
        malformed program that pops more values than it ever pushed
        degrades to a quiet 0 (`popType4`) rather than panicking, and a
        float outside int64's representable range is clamped
        (`type4Int`) before any of the integer-typed operators (idiv,
        mod, and/or/xor/not, bitshift, copy/index/roll's own counts) use
        it, avoiding Go's "implementation-defined" float-to-int64
        conversion behavior on out-of-range input.
    - **Safety bounds**, per this project's established policy for any
        interpreter over untrusted embedded content: `maxType4ParseDepth`
        (64) bounds `{ ... }` nesting so a pathological brace-only stream
        cannot overflow the parser's own call stack;
        `maxType4Instructions` (10000) bounds a parsed program's total
        flat size; `maxType4Steps` (1<<16) bounds one `Eval` call's total
        executed instructions - provably redundant, since the language
        has no loop construct so total execution can never exceed the
        already-bounded program size, but kept as a second independent
        check per this project's usual "belt and suspenders" habit for
        this class of interpreter (see `internal/fonts/type1.go`'s
        `maxType1Steps`). `copy`/`index`/`roll` each clamp their operand
        count against the stack's actual size rather than indexing out of
        bounds.
    - `parseType4` reads `/Domain` (required, any number of inputs
        unlike Type 2/3's fixed one) and `/Range` (required specifically
        for Type 4, since a program's own output arity is otherwise
        unknowable - the same reason Type 0 requires it) but is not yet
        wired into `parseSingle`'s dispatch (`case 4:` still returns
        `pdferror.Unsupportedf` there) - that wiring, plus the doc-comment
        updates it implies, is 19b.
    - Every exported-from-this-file identifier carries comments aimed at
        a reader new to Go as well as to PDF (per this project's phased-
        commit convention): why a tagged struct rather than an interface
        represents `t4Instr`/`t4Token`, why stack-mutating helpers take a
        `*[]float64` rather than `[]float64`, what a recursive-descent
        parser's lexer `peek`/`next` split buys, and so on.
- **`internal/function/type4_test.go` (new).** Table-driven coverage of
    every operator in `allowedType4Operators` individually (arithmetic;
    relational/boolean/bitwise, including the boolean-vs-integer dual
    behavior of and/or/xor/not; stack manipulation, including the
    PostScript Language Reference's own worked `roll` example); `if` and
    nested `ifelse` (a three-way sign function driven entirely by nested
    blocks); the trigger file's own rich-black tint-transform pattern
    (`{ 1 exch sub dup dup dup }`) as an end-to-end sanity check; Domain
    clipping and Range clipping; multiple-input functions; and a parse-
    time-malformed case for every distinct rejection
    `parseType4Program`/`parseType4Block` can produce (missing leading
    brace, unterminated block, trailing content, unknown operator,
    NaN/Inf literals, a dangling `if`/`ifelse`, a block not consumed by
    `if`/`ifelse`, over-deep nesting, and an oversized flat instruction
    count). `TestType4StackUnderflowDoesNotPanic` and a white-box
    `TestType4EvalTerminatesEvenWithAnExcessiveStepBudget` (built by
    constructing a `t4Instr` slice directly, since no parseable program
    can actually reach `maxType4Steps`) exercise the safety bounds
    directly.
- **`internal/function/fuzz_test.go` (new).** `FuzzType4Program` feeds
    arbitrary bytes through `parseType4Program` and, for anything that
    parses, `Eval` at several inputs - the "never panics, always
    terminates" property this project's other embedded-content
    interpreters (Type 1 charstrings, TrueType `sfnt` parsing) already
    each get their own fuzz target for. Ran clean for ~15 million
    executions with no crashes found during this phase's own validation.
- **Deliberately deferred to 19b-19d** (unchanged from this phase's own
    plan above): wiring `parseType4` into `function.Parse`'s dispatch and
    updating the two doc comments that currently describe Type 4 as
    unimplemented (19b); `tools/genfixtures` fixtures with checked-in
    golden PNGs and re-validation against the trigger file,
    `2014ElectionManual-stripped.pdf` (19c); `docs/CAPABILITY-MATRIX.md`
    and `FIXTURES.md` updates (19d).

### Phase 19b: wire into `internal/function.Parse` — done (2026-09-13)

- **`internal/function/function.go`.** `parseSingle`'s `case 4:` no
    longer returns `pdferror.Unsupportedf`; it now mirrors Type 0's own
    stream handling exactly (a Type 4 function's program text is its
    stream data, just as Type 0's samples are): if `stream` is nil (the
    caller was handed a bare dictionary, not a `syntax.Stream`) it
    returns a `pdferror.Malformedf` naming the missing stream, otherwise
    it calls the already-existing `parseType4(r, dict, *stream)` from
    19a. No changes were needed at either real call site
    (`internal/image/colorspace.go`'s `resolveSeparationOrDeviceN` or
    `internal/content/shading.go`'s `doShading`/`resolvePatternPaint`) -
    both already go through `function.Parse` uniformly and treat
    whatever `Function` comes back identically regardless of concrete
    type, confirming the `Function` interface abstraction from Phase 5
    did its job.
- **Doc comments updated** to stop describing Type 4 as unimplemented:
    `internal/function/doc.go`'s package comment now lists Type 4
    alongside Types 0/2/3 as implemented (with a short description of
    its operator scope and typical real-world use) and explains that an
    out-of-range `/FunctionType` is malformed rather than merely
    unsupported; `internal/content/colorspace.go`'s `setColorSpace` doc
    comment's example of an "unresolvable" color space feature was
    swapped from "a Type 4 tint transform function" (no longer true) to
    "an /Indexed color space whose base is itself /Indexed" (still an
    actual `pdferror.Unsupportedf` case, in `internal/image/
    colorspace.go`).
- **Tests.** `internal/function/function_test.go`'s
    `TestParseType4IsUnsupported` (asserted the old, now-wrong,
    behavior) was replaced with two tests exercising `Parse`'s own
    top-level dispatch rather than `parseType4` directly (which
    `type4_test.go`'s existing `mustParseType4` helper already covers
    heavily): `TestParseDispatchesType4ThroughStream` builds a Type 4
    `syntax.Stream` (domain `[0 1]`, range `[0 1]`, program
    `{ 1 exch sub }`) and confirms `Parse` reaches a working `Function`
    end to end (`Eval([0.25])` returns `[0.75]`); `TestParseType4RequiresAStream`
    mirrors the existing `TestParseType0RequiresAStream` pattern,
    confirming a bare Type 4 dictionary (no stream) is rejected as
    malformed rather than panicking on a nil-pointer dereference. Full
    existing suite (`go test ./...`), `go vet ./...`, and `gofmt -l`
    all pass clean; no fixtures changed in this sub-phase (that is
    19c).
- **Deferred to 19c-19d** (unchanged): fixtures/golden-PNGs and
    trigger-file re-validation (19c); capability-matrix/FIXTURES.md
    updates (19d).

### Phase 19c: fixtures and real-world validation — done (2026-09-13)

- **`tools/genfixtures/main.go`.** Two new hand-authored fixtures, added
    next to the existing fixture each one most directly parallels so the
    two are easy to compare side by side:
    - **`buildType4TintTransformFill`** (`type4-tint-transform-fill.pdf`,
        next to `buildSeparationFill`): a `/DeviceN [/Black] /DeviceCMYK`
        color space whose tint transform is a Type 4 function stream
        object using the exact same program text as 19a's own
        `TestType4RichBlackTintTransform` and the real trigger file's own
        pattern, `{ 1 exch sub dup dup dup }`. Two rectangles are filled at
        different tints (0.75 and 0.2) chosen specifically so a naive
        "guess DeviceGray from the operand count" fallback (the behavior
        this call site had *before* Phase 19) would produce a visibly
        different, wrong color at both - proving the fixture can only pass
        if the real Type 4 program actually ran, not merely if some
        plausible-looking gray came out.
    - **`buildType4AxialShading`** (`type4-axial-shading.pdf`, next to
        `buildAxialShading`): the same named-shading-via-`sh"` structure as
        `buildAxialShading`, but with a Type 4 `/Function`
        (`{ 180 mul sin dup dup }`) computing a black-white-black sine
        "hump" - a curve shape no Type 2 (always monotonic between its two
        endpoint colors) or Type 3 (stitched Type 2 pieces, not used by
        either existing shading fixture) function could produce, so this
        fixture specifically proves the shading call site
        (`internal/content/shading.go`'s `doShading`), which had *no*
        fallback at all before Phase 19 and would have aborted the whole
        `sh` operator.
    - Both fixtures' doc comments work out expected pixel colors by hand
      (including that `Page.Render` samples each device pixel's *center*,
      i.e. device column x corresponds to input t=(x+0.5)/100, not
      t=x/100 - a detail that mattered for getting the shading fixture's
      sampled points right) so a reader can check the Go source against
      the checked-in golden PNG without re-deriving anything themselves.
    - `tools/genfixtures/main_test.go`'s `TestGeneratedFixturesMatchCheckedInFiles`
      and `TestFixtureSetIsComplete` both updated with the two new
      filenames, keeping this package's existing "checked-in fixtures
      always match what the generator currently produces" guarantee
      intact.
- **`pdfviewer_render_test.go`.** `TestRenderType4TintTransformFill` and
    `TestRenderType4AxialShading` - direct-pixel-sampling tests following
    this file's own established pattern (see `TestRenderFilledRect` and
    friends): each independently re-derives, in its own comment, the exact
    expected RGB at a handful of sampled points, rather than just trusting
    the fixture's doc comment. Both fixtures were also added to
    `TestRenderMatchesReferenceImages`'s fixture list, with their golden
    PNGs generated via `go test . -run TestRenderMatchesReferenceImages
    -update` and checked in under `testdata/renderrefs`, giving this pair
    both kinds of regression coverage every other vector/shading fixture
    in this project gets.
- **Real-world re-validation.** The trigger file
    (`2014ElectionManual-stripped.pdf`, kept local, not committed - see
    this project's existing non-fixture-PDF policy) was opened with
    `WithDiagnostics` and every one of its 80 pages rendered: zero
    diagnostic messages were recorded at all, confirming the `CS1`
    "color space could not be resolved" note the original report saw is
    gone now that Type 4 is implemented, and that no other page in the
    same document regressed in the process.
- **Verification.** Full existing suite (`go test ./...`), `go vet ./...`,
    and `gofmt -l` all pass clean; no changes needed to
    `internal/function/type4_test.go` or `fuzz_test.go` (19a's coverage of
    every operator, `if`/`ifelse`, and the safety bounds already satisfies
    this sub-phase's own unit-test scope from the plan above).
- **Deferred to 19d** (unchanged): `docs/CAPABILITY-MATRIX.md`'s
    Separation/DeviceN and Shading rows, and `testdata/fixtures/FIXTURES.md`'s
    entries for the two new fixtures.

### Phase 19d: documentation — done (2026-09-13)

- **`docs/CAPABILITY-MATRIX.md`.** Added a new row, "PDF Functions
    (`/FunctionType` 0, 2, 3, 4)", to the "Color spaces" table - the
    matrix previously had no single place recording that all four
    function types are implemented at all; that fact was only ever
    stated as a parenthetical inside the Separation/DeviceN and Shading
    rows' own Notes, in two different words, which is exactly the kind
    of scattered-answer problem this document's own opening paragraph
    says it exists to avoid. The new row is the one place documenting
    the full Type 0/2/3/4 breakdown (mirroring `internal/function/
    doc.go`'s package comment), the safety bounds Type 4 specifically
    adds, and the fact that - since the specification defines exactly
    four function types and this package now implements all of them -
    there is no further function type left to add; any future Type
    4-related work would extend how thoroughly it is exercised (more
    real-world fixtures), not its Annex B operator coverage, which is
    already complete. The Separation/DeviceN and Shading rows' own Notes
    were trimmed to cross-reference the new row instead of re-stating
    the same four-type breakdown a third and fourth time, while keeping
    each row's own specific detail (the real-world "rich black"
    motivation and `type4-tint-transform-fill.pdf` for Separation/
    DeviceN; the non-monotonic-curve rationale and
    `type4-axial-shading.pdf` for Shading). Both rows' Target phase
    columns already read "Phase 5, 19a-19c" from 19c's own commit.
- **`testdata/fixtures/FIXTURES.md`.** Added table rows for
    `type4-tint-transform-fill.pdf` (immediately after `separation-fill.pdf`,
    the fixture it most directly parallels) and `type4-axial-shading.pdf`
    (immediately after `axial-shading.pdf`), each following this file's
    existing one-row-per-fixture convention: what the fixture contains,
    which Type 4 call site it exercises, and why its specific numbers
    were chosen (the fallback-distinguishing tints for the tint-transform
    fixture; the no-fallback-at-all shading call site for the other).
    `testdata/renderrefs/`'s own "Rendered reference images" section
    needed no changes - it already describes the golden-PNG directory
    generically rather than enumerating fixtures by name.
- **Verification.** `go build ./...`, `go vet ./...`, `gofmt -l .`, and
    `go test ./...` all still pass clean (docs-only change; no Go source
    touched in this sub-phase).

**Phase 19 is now complete (19a-19d).** Exit criteria met: Type 4 tint
transforms and shading functions both evaluate correctly rather than
falling back or failing (19a-19b, confirmed end to end by 19c's
fixtures); the trigger file's `CS1` resolves without a diagnostic, and
all 80 of its pages render with zero diagnostics recorded (19c); the
full test suite, `go vet`, `gofmt`, and fuzzing all pass clean; and every
capability-matrix/fixture-documentation obligation the phase took on is
now recorded (19d).

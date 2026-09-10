package content

import (
	"github.com/tucats/pdf-viewer/internal/diag"
	"github.com/tucats/pdf-viewer/internal/fonts"
	"github.com/tucats/pdf-viewer/internal/graphics"
	pdfimage "github.com/tucats/pdf-viewer/internal/image"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements Phase 10's text extraction: ExtractText walks a
// content stream's already-parsed Operators (the very same []Operator
// Parse produces for Interpret - see interpret.go's own doc comment) and
// recovers every glyph a text-showing operator ("Tj", "'", "\"", or one
// string element of a "TJ" array) paints, as a TextGlyph carrying its
// decoded Unicode text (fonts.Font.TextForCode, Phase 10's own addition
// to internal/fonts) and its device-space baseline position - instead of
// building the paintable graphics.DisplayList Interpret produces.
//
// # Why this is a separate walker, not a mode flag on interpreter
//
// docs/PLAN.md's original Phase 4 plan says to "keep text extraction as
// a separate capability from text painting, so it can be added without
// changing page rendering ownership." Threading an "extraction mode"
// through interpret.go's exec method would satisfy that in letter (
// Render would still call it exactly as before) but not in spirit: it
// would make every future change to path/image/shading handling in that
// file something a text-extraction bug could also come from, and it
// would make Interpret's own error behavior (which correctly *does* fail
// on, say, an unsupported image color space - see Interpret's doc
// comment) leak into extraction, where it does not belong at all - a
// content stream mixing perfectly extractable text with some entirely
// unrelated unsupported feature should still extract its text cleanly.
//
// So extractor below is a genuinely separate, much smaller interpreter
// that only understands the operators that can affect what text exists
// or where it sits: "q"/"Q"/"cm" (for the CTM), "BT"/"ET" and the seven
// text-state operators, the four text-positioning operators, and the
// four text-showing operators. Every other operator - path construction,
// painting, images, shadings, "gs", "Do" (including a Form XObject - see
// this file's own "What's carried forward" note below), marked content -
// is simply skipped, exactly like Interpret already skips operators it
// does not recognize at all, except here that list is deliberately much
// longer.
//
// The two things extraction and painting must still agree on exactly -
// font resolution (which *fonts.Font a given /Resources /Font name means
// right now) and the text-positioning formula (where a glyph's baseline
// actually lands) - are shared, not duplicated: resolveFont below is
// text.go's lookupFont's own logic, factored out so both this file and
// that one call it; and showText (this file's) calls text.go's own
// package-level textRenderingMatrix function directly, the exact same
// code Render's showGlyph uses to place a glyph outline.
//
// # What's carried forward
//
// This first version does not recurse into Form XObjects ("Do" naming a
// /Form, as form.go's doForm does for Render) - text painted only inside
// a form's own content stream is not extracted. This is a real,
// documented gap (some producers wrap a whole page's content in one
// outer form, or place repeated/watermark text via one), not an
// oversight: Phase 10's own exit criteria (docs/PLAN2.md) only calls for
// correct extraction from a page's own content stream, and form
// recursion needs its own resource-scoping, matrix-composition, and
// recursion-depth-guard machinery (see form.go's maxFormDepth) that adds
// real complexity for a case outside that scope. A later phase can add
// it the same way form.go added it to Interpret, following that file's
// own precedent, if real-world use shows it is needed.

// TextGlyph is one glyph ExtractText recovered from a content stream:
// its decoded Unicode text and its device-space baseline position/
// advance - see ExtractText's own doc comment for the coordinate space
// these are expressed in (governed entirely by the initialCTM the caller
// passes in, exactly like Interpret's own DisplayList output).
type TextGlyph struct {
	// Text is this glyph's decoded Unicode text, from the font's own
	// /ToUnicode CMap (fonts.Font.TextForCode) or, for a simple font with
	// no /ToUnicode entry, its resolved /Encoding - see TextForCode's own
	// doc comment for exactly which of those two answers this can be.
	// Empty when neither source has any defined text for this glyph's
	// code (most commonly a Type0/CID font with no /ToUnicode CMap at
	// all) - this is a real, honest "unknown", not an error, so the
	// glyph still appears in ExtractText's result with a correct
	// position and width, just no text.
	Text string

	// X, Y is this glyph's baseline origin - textRenderingMatrix applied
	// to glyph-space (0,0), the same point showGlyph (text.go) anchors a
	// painted outline to - in whatever space initialCTM maps user space
	// into (see ExtractText's doc comment).
	X, Y float64

	// Width is this glyph's advance width, in the same space as X/Y:
	// showText computes it with the exact formula text.go's own showText
	// uses to advance the text matrix for painting (PDF specification
	// 9.4.3), so summing consecutive glyphs' Width values reproduces the
	// same line layout Render would have painted.
	Width float64

	// FontSize is the font size ("Tf"'s second operand, in unscaled text
	// space units) active when this glyph was shown.
	FontSize float64
}

// resolveFont looks up name in resources's /Font dictionary and returns
// a *fonts.Font for it, building (and caching) one via fonts.Load on
// first use. This is the resource-lookup/caching logic both
// interpreter.lookupFont (text.go, Phase 4's text painting) and this
// file's extractor.lookupFont (Phase 10's text extraction) need
// identically - both are asking exactly the same question ("what font
// does /Resources /Font /F1 mean right now"), just for different reasons
// once they have the answer (paint a glyph outline, or recover its
// Unicode text).
//
// crossCallCache is FontCache's cross-Interpret-call cache (nil-safe,
// see that type's own doc comment); localCache is the caller's own
// by-name cache for this one call, which must already be a non-nil map
// (both callers below allocate theirs up front) since this function
// writes into it directly.
func resolveFont(resources syntax.Dictionary, resolver pdfimage.Resolver, crossCallCache *FontCache, localCache map[syntax.Name]*fonts.Font, name syntax.Name) (*fonts.Font, bool) {
	if f, ok := localCache[name]; ok {
		return f, true
	}
	if resources == nil || resolver == nil {
		return nil, false
	}
	fontsEntry, ok := resources["Font"]
	if !ok {
		return nil, false
	}
	resolved, err := resolveIfRef(resolver, fontsEntry)
	if err != nil {
		return nil, false
	}
	fontsDict, ok := resolved.(syntax.Dictionary)
	if !ok {
		return nil, false
	}
	entry, ok := fontsDict[name]
	if !ok {
		diag.Note(resolver, "font %q is not in /Resources /Font; text using it will not be shown", name)
		return nil, false
	}

	// If entry is itself an indirect reference, its object number is a
	// stable identity for this font dictionary across the whole
	// document - unlike name, which is only meaningful within this one
	// content stream's own /Resources /Font dictionary. Check the
	// cross-call cache before paying to resolve and load anything.
	ref, hasRef := entry.(syntax.Reference)
	if hasRef {
		if f, ok := crossCallCache.get(ref.Number); ok {
			localCache[name] = f
			return f, true
		}
	}

	resolvedEntry, err := resolveIfRef(resolver, entry)
	if err != nil {
		return nil, false
	}
	dict, ok := resolvedEntry.(syntax.Dictionary)
	if !ok {
		return nil, false
	}

	f, err := fonts.Load(dict, resolver)
	if err != nil || f == nil {
		diag.Note(resolver, "font %q could not be loaded (%v); text using it will not be shown", name, err)
		return nil, false
	}
	localCache[name] = f
	if hasRef {
		crossCallCache.put(ref.Number, f)
	}
	return f, true
}

// ExtractText walks ops and recovers every glyph shown by a text-showing
// operator, decoded to Unicode via each glyph's own font - see this
// file's own doc comment for the general design and scope.
//
// initialCTM plays exactly the role it plays for Interpret: every
// TextGlyph's X/Y/Width comes out already transformed by it (composed
// with whatever "cm" operators the content stream itself applies) - the
// root package's Page.Text calls this with graphics.Identity(), so a
// glyph's position is reported in the page's own default user space (the
// same PDF-point coordinate system Page.Bounds reports), but a caller
// with a different need (say, device-space coordinates matching a
// specific Render call) can pass that Render call's own CTM instead.
//
// resources/resolver/fontCache mean exactly what they mean for
// InterpretCached (resources is nil-safe, resolver may be nil only if
// resources is too, fontCache may be nil for "no cross-call caching").
//
// Unlike Interpret, ExtractText never fails on account of an unsupported
// image, color space, shading, or pattern - none of that can change what
// text exists on a page or where it sits (see this file's own doc
// comment). It does still return an error for genuinely malformed
// content among the operators it does interpret (wrong operand count or
// type for "cm", "Tf", or any text-positioning/showing operator) -
// Interpret's own "malformed content is not tolerated, merely
// unsupported features are" distinction, applied to this file's smaller
// operator set.
func ExtractText(ops []Operator, initialCTM graphics.Matrix, resources syntax.Dictionary, resolver pdfimage.Resolver, fontCache *FontCache) ([]TextGlyph, error) {
	ex := &extractor{
		stack:          graphics.NewStack(graphics.NewState(initialCTM)),
		resources:      resources,
		resolver:       resolver,
		fontCache:      fontCache,
		localFontCache: make(map[syntax.Name]*fonts.Font),
	}
	for _, op := range ops {
		if err := ex.exec(op); err != nil {
			return nil, err
		}
	}
	return ex.glyphs, nil
}

// extractor is ExtractText's own small interpreter - see this file's
// doc comment for why it is not interpret.go's interpreter running in a
// different mode. It reuses graphics.Stack/graphics.State for CTM and
// text-state (CharSpace, WordSpace, Hscale, Leading, FontSize, Rise,
// Font) tracking, exactly the fields Render's own interpreter keeps
// there for the same reason (see graphics.State's own doc comment on
// why those seven fields live in the *graphics* state rather than
// alongside the text/line matrices below) - "q"/"Q" push and pop them
// correctly with zero extra code here. The text and line matrices
// (Tm/Tlm) are not part of graphics.State (per the specification - see
// text.go's textInterpreterState doc comment for why) so extractor keeps
// its own, exactly mirroring textInterpreterState's tm/tlm fields.
type extractor struct {
	stack   *graphics.Stack
	tm, tlm graphics.Matrix

	resources      syntax.Dictionary
	resolver       pdfimage.Resolver
	fontCache      *FontCache
	localFontCache map[syntax.Name]*fonts.Font

	glyphs []TextGlyph
}

// lookupFont is extractor's counterpart to interpreter.lookupFont
// (text.go) - see resolveFont's own doc comment for the shared logic
// both delegate to.
func (ex *extractor) lookupFont(name syntax.Name) (*fonts.Font, bool) {
	return resolveFont(ex.resources, ex.resolver, ex.fontCache, ex.localFontCache, name)
}

// exec interprets one operator against ex - see this file's own doc
// comment for exactly which operators are handled and why every other
// one is silently skipped in the default case below.
func (ex *extractor) exec(op Operator) error {
	st := ex.stack.Current()

	switch op.Name {
	case "q":
		ex.stack.Push()
	case "Q":
		// Tolerate a stack underflow exactly like interpret.go's own "Q"
		// handling does - malformed content should not abort extraction
		// of whatever text follows.
		_ = ex.stack.Pop()

	case "cm":
		vals, err := requireFloats(op.Operands, 6)
		if err != nil {
			return err
		}
		m := graphics.Matrix{A: vals[0], B: vals[1], C: vals[2], D: vals[3], E: vals[4], F: vals[5]}
		st.CTM = m.Mul(st.CTM)

	case "BT":
		ex.tm = graphics.Identity()
		ex.tlm = graphics.Identity()
	case "ET":
		// Nothing to do - see text.go's own "ET" case for why.

	case "Tc":
		vals, err := requireFloats(op.Operands, 1)
		if err != nil {
			return err
		}
		st.CharSpace = vals[0]
	case "Tw":
		vals, err := requireFloats(op.Operands, 1)
		if err != nil {
			return err
		}
		st.WordSpace = vals[0]
	case "Tz":
		vals, err := requireFloats(op.Operands, 1)
		if err != nil {
			return err
		}
		st.Hscale = vals[0]
	case "TL":
		vals, err := requireFloats(op.Operands, 1)
		if err != nil {
			return err
		}
		st.Leading = vals[0]
	case "Ts":
		vals, err := requireFloats(op.Operands, 1)
		if err != nil {
			return err
		}
		st.Rise = vals[0]
	case "Tr":
		// Tracked for completeness (a "q" might save/restore it) but
		// never consulted: unlike Render's paintsGlyphs, extraction
		// recovers text regardless of render mode, including mode 3
		// ("invisible") - the common way an OCR text layer is placed
		// over a scanned page image, which is exactly the kind of text a
		// caller of Page.Text most wants back.
		vals, err := requireFloats(op.Operands, 1)
		if err != nil {
			return err
		}
		st.RenderMode = int(vals[0])
	case "Tf":
		return ex.setFont(st, op.Operands)

	case "Td":
		vals, err := requireFloats(op.Operands, 2)
		if err != nil {
			return err
		}
		ex.moveTextLine(vals[0], vals[1])
	case "TD":
		vals, err := requireFloats(op.Operands, 2)
		if err != nil {
			return err
		}
		st.Leading = -vals[1]
		ex.moveTextLine(vals[0], vals[1])
	case "Tm":
		vals, err := requireFloats(op.Operands, 6)
		if err != nil {
			return err
		}
		m := graphics.Matrix{A: vals[0], B: vals[1], C: vals[2], D: vals[3], E: vals[4], F: vals[5]}
		ex.tm = m
		ex.tlm = m
	case "T*":
		ex.moveTextLine(0, -st.Leading)

	case "Tj":
		s, err := requireString(op.Operands)
		if err != nil {
			return err
		}
		ex.showText(st, s)
	case "'":
		s, err := requireString(op.Operands)
		if err != nil {
			return err
		}
		ex.moveTextLine(0, -st.Leading)
		ex.showText(st, s)
	case "\"":
		if len(op.Operands) != 3 {
			return pdferror.Malformedf("\"\\\"\" expects 3 operands, got %d", len(op.Operands))
		}
		vals, err := requireFloats(op.Operands[:2], 2)
		if err != nil {
			return err
		}
		s, ok := op.Operands[2].(syntax.String)
		if !ok {
			return pdferror.Malformedf("\"\\\"\" third operand must be a string, found %T", op.Operands[2])
		}
		st.WordSpace, st.CharSpace = vals[0], vals[1]
		ex.moveTextLine(0, -st.Leading)
		ex.showText(st, []byte(s))
	case "TJ":
		arr, ok := singleArrayOperand(op.Operands)
		if !ok {
			return pdferror.Malformedf("\"TJ\" expects a single array operand, got %v", op.Operands)
		}
		for _, elem := range arr {
			switch v := elem.(type) {
			case syntax.String:
				ex.showText(st, []byte(v))
			case syntax.Integer:
				ex.showAdjustment(st, float64(v))
			case syntax.Real:
				ex.showAdjustment(st, float64(v))
			}
		}

	default:
		// Path construction, painting, images, shadings, "gs", "Do" (see
		// this file's own doc comment on Form XObjects), marked content,
		// and anything else - none of it can change what text exists or
		// where it sits, so extraction simply ignores it rather than
		// interpreting (or failing on) it the way Interpret must.
	}
	return nil
}

// setFont implements "Tf" for extraction - text.go's own setFont, but
// resolving through extractor.lookupFont instead of interpreter.lookupFont.
func (ex *extractor) setFont(st *graphics.State, operands []syntax.Object) error {
	if len(operands) != 2 {
		return pdferror.Malformedf("\"Tf\" expects 2 operands, got %d", len(operands))
	}
	name, ok := operands[0].(syntax.Name)
	if !ok {
		return pdferror.Malformedf("\"Tf\" first operand must be a name, found %T", operands[0])
	}
	size, ok := numberValue(operands[1])
	if !ok {
		return pdferror.Malformedf("\"Tf\" second operand must be a number, found %T", operands[1])
	}
	st.FontSize = size
	if f, ok := ex.lookupFont(name); ok {
		st.Font = f
	}
	return nil
}

// moveTextLine is text.go's moveTextLine, operating on ex's own tm/tlm
// instead of an interpreter's in.text fields - see that function's doc
// comment for the "Td"/"TD" semantics this implements.
func (ex *extractor) moveTextLine(tx, ty float64) {
	ex.tlm = graphics.Translate(tx, ty).Mul(ex.tlm)
	ex.tm = ex.tlm
}

// showAdjustment is text.go's showAdjustment, operating on ex's own tm -
// see that function's doc comment for the "TJ" numeric-adjustment
// semantics this implements.
func (ex *extractor) showAdjustment(st *graphics.State, amount float64) {
	tx := -(amount / 1000) * st.FontSize * (st.Hscale / 100)
	ex.tm = graphics.Translate(tx, 0).Mul(ex.tm)
}

// showText is ExtractText's actual text-recovery step: decode s into
// character codes per the current font's own encoding (fonts.Font.
// DecodeCodes, exactly as text.go's showText does for painting), and for
// each code, record a TextGlyph at the text matrix's current position
// (via textRenderingMatrix, text.go's own package-level function - the
// identical math Render uses to place a painted glyph outline, just
// applied to glyph-space (0,0) instead of an outline's own points) before
// advancing the text matrix by that glyph's width, using the exact same
// formula (PDF specification 9.4.3) text.go's showText uses:
//
//	tx = ((w0 - Tj/1000) * Tfs + Tc + Tw) * Th
//
// A code with no selected font (st.Font not a *fonts.Font - "Tf" was
// never successfully called) is silently skipped, matching text.go's own
// "no font selected" tolerance.
func (ex *extractor) showText(st *graphics.State, s []byte) {
	font, ok := st.Font.(*fonts.Font)
	if !ok || font == nil {
		return
	}
	for _, dc := range font.DecodeCodes(s) {
		trm := textRenderingMatrix(st, ex.tm)
		x, y := trm.Apply(0, 0)
		text, _ := font.TextForCode(dc.Code)

		w0 := font.Width(dc.Code) / 1000
		tw := 0.0
		if dc.Bytes == 1 && dc.Code == 32 {
			tw = st.WordSpace
		}
		tx := (w0*st.FontSize + st.CharSpace + tw) * (st.Hscale / 100)

		ex.glyphs = append(ex.glyphs, TextGlyph{
			Text:     text,
			X:        x,
			Y:        y,
			Width:    tx,
			FontSize: st.FontSize,
		})
		ex.tm = graphics.Translate(tx, 0).Mul(ex.tm)
	}
}

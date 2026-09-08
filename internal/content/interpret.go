package content

import (
	"math"

	"github.com/tucats/pdf-viewer/internal/graphics"
	pdfimage "github.com/tucats/pdf-viewer/internal/image"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements Interpret, which replays a sequence of Operators
// (from Parse) against a graphics.Stack, producing the graphics.DisplayList
// internal/raster consumes. See the package doc comment for operator
// coverage.

// Interpret replays ops against a fresh graphics.Stack seeded with
// initialCTM - the mapping from the page's default user space to device
// (pixel) space, established by the caller before any content stream
// operator runs (see the root package's Page.Render) - and returns the
// resulting DisplayList: every fill, stroke, and (Phase 3) image the
// content actually painted, already flattened into device-space
// geometry with resolved colors and clips.
//
// resources is the page's (or, once forms are supported, a form
// XObject's) /Resources dictionary, consulted only by "Do" (to look up a
// named XObject) and, beneath it, internal/image (to resolve a named
// /ColorSpace) - it may be nil for content that uses neither. resolver
// reaches back into the document for anything an image needs beyond its
// own dictionary and raw bytes (see pdfimage.Resolver); it may be nil
// only if resources is also nil (a nil resolver used for image work
// would panic, but a page with no /Resources at all cannot legally paint
// an XObject image in the first place, since there would be nothing for
// "Do" to name).
//
// Interpret does not abort on an operator it does not recognize (Form
// XObject painting via "Do", shading via "sh", marked content, and so on
// - none of which is implemented yet; see the package doc comment) -
// those are silently skipped, so a page mixing supported
// content with unsupported features still renders whatever this package
// can handle rather than failing the whole page. It does return an error
// for content that is itself malformed (wrong operand count or type for
// a recognized operator, or a malformed inline/referenced image) or that
// names an explicitly unsupported feature this package can positively
// detect (pattern color spaces, an image using an unsupported color
// space or filter) rather than merely not recognizing.
func Interpret(ops []Operator, initialCTM graphics.Matrix, resources syntax.Dictionary, resolver pdfimage.Resolver) (graphics.DisplayList, error) {
	in := &interpreter{
		stack:      graphics.NewStack(graphics.NewState(initialCTM)),
		resources:  resources,
		resolver:   resolver,
		initialCTM: initialCTM,
	}
	for _, op := range ops {
		if err := in.exec(op); err != nil {
			return nil, err
		}
	}
	return in.list, nil
}

type interpreter struct {
	stack *graphics.Stack

	// path is the path currently under construction - the sequence of
	// "m"/"l"/"c"/"v"/"y"/"re" operators since the last painting
	// operator ("S", "f", "n", ...) reset it. Per the PDF specification,
	// the current path is not part of the graphics state a "q" saves -
	// see graphics.State's doc comment - so it lives here instead, on
	// the interpreter, rather than inside graphics.State.
	path graphics.Path

	// pendingClip and pendingClipRule record a "W"/"W*" operator seen
	// since the current path was last reset: per the specification, the
	// clipping path it names does not take effect until *after* the next
	// painting operator finishes painting - see endPath.
	pendingClip     *graphics.Path
	pendingClipRule graphics.FillRule
	hasPendingClip  bool

	// resources and resolver back "Do" (referenced XObject images), "BI"
	// (inline images), (Phase 4) "Tf" (looking up and loading a named
	// font), and (Phase 5) "sh" and a shading pattern's /Shading
	// dictionary - see Interpret's doc comment.
	resources syntax.Dictionary
	resolver  pdfimage.Resolver

	// initialCTM is the CTM Interpret was originally called with - the
	// mapping from this content stream's *default* user space to device
	// space, before any "cm" has run. Unlike graphics.Stack's current
	// state (which "cm"/"q"/"Q" freely mutate), this never changes for
	// the lifetime of one Interpret call: it is exactly what a pattern's
	// own /Matrix is defined relative to (PDF patterns are deliberately
	// independent of whatever CTM happens to be active when they are
	// later selected or used to paint - see shading.go's
	// resolvePatternPaint), which the live, mutable CTM on graphics.State
	// cannot supply once any "cm" has run.
	initialCTM graphics.Matrix

	// text holds Phase 4's text-object-local state (the text/line
	// matrices and the font cache) - see textInterpreterState's doc
	// comment in text.go for why these live here rather than on
	// graphics.State.
	text textInterpreterState

	list graphics.DisplayList
}

func (in *interpreter) exec(op Operator) error {
	st := in.stack.Current()

	switch op.Name {
	case "q":
		in.stack.Push()
	case "Q":
		// A stack underflow (more "Q" than "q") is tolerated rather than
		// aborting the render: it is malformed content, but the current
		// state simply stays as it is, which lets the rest of the page
		// keep rendering - consistent with this package's general
		// "skip, don't abort" tolerance for problems that do not prevent
		// interpreting what follows.
		_ = in.stack.Pop()

	case "cm":
		vals, err := requireFloats(op.Operands, 6)
		if err != nil {
			return err
		}
		m := graphics.Matrix{A: vals[0], B: vals[1], C: vals[2], D: vals[3], E: vals[4], F: vals[5]}
		st.CTM = m.Mul(st.CTM)

	case "w":
		vals, err := requireFloats(op.Operands, 1)
		if err != nil {
			return err
		}
		st.LineWidth = vals[0]
	case "J":
		vals, err := requireFloats(op.Operands, 1)
		if err != nil {
			return err
		}
		st.LineCap = graphics.LineCap(int(vals[0]))
	case "j":
		vals, err := requireFloats(op.Operands, 1)
		if err != nil {
			return err
		}
		st.LineJoin = graphics.LineJoin(int(vals[0]))
	case "M":
		vals, err := requireFloats(op.Operands, 1)
		if err != nil {
			return err
		}
		st.MiterLimit = vals[0]
	case "d", "ri", "i", "gs":
		// Accepted and ignored: dash patterns, rendering intent,
		// flatness tolerance, and ExtGState parameters (transparency,
		// blend modes, ...) are not yet implemented - see
		// docs/capability-matrix.md. Every stroke is painted solid and
		// fully opaque regardless of these.

	case "m":
		pt, err := in.point(st, op.Operands)
		if err != nil {
			return err
		}
		in.path.MoveTo(pt)
	case "l":
		pt, err := in.point(st, op.Operands)
		if err != nil {
			return err
		}
		in.path.LineTo(pt)
	case "c":
		pts, err := in.points(st, op.Operands, 3)
		if err != nil {
			return err
		}
		in.path.CurveTo(pts[0], pts[1], pts[2])
	case "v":
		pts, err := in.points(st, op.Operands, 2)
		if err != nil {
			return err
		}
		c1, ok := in.path.Current()
		if !ok {
			c1 = pts[0]
		}
		in.path.CurveTo(c1, pts[0], pts[1])
	case "y":
		pts, err := in.points(st, op.Operands, 2)
		if err != nil {
			return err
		}
		in.path.CurveTo(pts[0], pts[1], pts[1])
	case "h":
		in.path.Close()
	case "re":
		if err := in.appendRect(st, op.Operands); err != nil {
			return err
		}

	case "S":
		in.strokeCurrentPath(st)
		in.endPath(st)
	case "s":
		in.path.Close()
		in.strokeCurrentPath(st)
		in.endPath(st)
	case "f", "F":
		in.fillCurrentPath(st, graphics.NonZero)
		in.endPath(st)
	case "f*":
		in.fillCurrentPath(st, graphics.EvenOdd)
		in.endPath(st)
	case "B":
		in.fillCurrentPath(st, graphics.NonZero)
		in.strokeCurrentPath(st)
		in.endPath(st)
	case "B*":
		in.fillCurrentPath(st, graphics.EvenOdd)
		in.strokeCurrentPath(st)
		in.endPath(st)
	case "b":
		in.path.Close()
		in.fillCurrentPath(st, graphics.NonZero)
		in.strokeCurrentPath(st)
		in.endPath(st)
	case "b*":
		in.path.Close()
		in.fillCurrentPath(st, graphics.EvenOdd)
		in.strokeCurrentPath(st)
		in.endPath(st)
	case "n":
		in.endPath(st)

	case "W":
		in.markPendingClip(graphics.NonZero)
	case "W*":
		in.markPendingClip(graphics.EvenOdd)

	case "g":
		vals, err := requireFloats(op.Operands, 1)
		if err != nil {
			return err
		}
		st.FillColor = grayColor(vals[0])
		st.FillShading = nil
	case "G":
		vals, err := requireFloats(op.Operands, 1)
		if err != nil {
			return err
		}
		st.StrokeColor = grayColor(vals[0])
		st.StrokeShading = nil
	case "rg":
		vals, err := requireFloats(op.Operands, 3)
		if err != nil {
			return err
		}
		st.FillColor = graphics.Color{R: vals[0], G: vals[1], B: vals[2]}
		st.FillShading = nil
	case "RG":
		vals, err := requireFloats(op.Operands, 3)
		if err != nil {
			return err
		}
		st.StrokeColor = graphics.Color{R: vals[0], G: vals[1], B: vals[2]}
		st.StrokeShading = nil
	case "k":
		vals, err := requireFloats(op.Operands, 4)
		if err != nil {
			return err
		}
		st.FillColor = cmykColor(vals[0], vals[1], vals[2], vals[3])
		st.FillShading = nil
	case "K":
		vals, err := requireFloats(op.Operands, 4)
		if err != nil {
			return err
		}
		st.StrokeColor = cmykColor(vals[0], vals[1], vals[2], vals[3])
		st.StrokeShading = nil

	case "cs":
		if err := in.setColorSpace(st, op.Operands, true); err != nil {
			return err
		}
	case "CS":
		if err := in.setColorSpace(st, op.Operands, false); err != nil {
			return err
		}
	case "sc", "scn":
		if err := in.setPaintColor(st, op.Operands, true); err != nil {
			return err
		}
	case "SC", "SCN":
		if err := in.setPaintColor(st, op.Operands, false); err != nil {
			return err
		}

	case "sh":
		if err := in.doShading(st, op.Operands); err != nil {
			return err
		}

	case "Do":
		return in.doXObject(st, op.Operands)
	case "BI":
		return in.doInlineImage(st, op.InlineImage)

	// --- Text (Phase 4) -------------------------------------------
	case "BT":
		in.beginText()
	case "ET":
		// Nothing to do: text state parameters live on graphics.State
		// (restored, if at all, only by a "Q" - see graphics.State's doc
		// comment), and the text/line matrices are simply left as they
		// are until the next "BT" resets them - the specification does
		// not require "ET" to do anything to either.
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
		vals, err := requireFloats(op.Operands, 1)
		if err != nil {
			return err
		}
		st.RenderMode = int(vals[0])
	case "Tf":
		return in.setFont(st, op.Operands)
	case "Td":
		vals, err := requireFloats(op.Operands, 2)
		if err != nil {
			return err
		}
		in.moveTextLine(vals[0], vals[1])
	case "TD":
		vals, err := requireFloats(op.Operands, 2)
		if err != nil {
			return err
		}
		st.Leading = -vals[1]
		in.moveTextLine(vals[0], vals[1])
	case "Tm":
		vals, err := requireFloats(op.Operands, 6)
		if err != nil {
			return err
		}
		m := graphics.Matrix{A: vals[0], B: vals[1], C: vals[2], D: vals[3], E: vals[4], F: vals[5]}
		in.text.tm = m
		in.text.tlm = m
	case "T*":
		in.nextLine(st)
	case "Tj":
		s, err := requireString(op.Operands)
		if err != nil {
			return err
		}
		in.showText(st, s)
	case "'":
		s, err := requireString(op.Operands)
		if err != nil {
			return err
		}
		in.nextLine(st)
		in.showText(st, s)
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
		in.nextLine(st)
		in.showText(st, []byte(s))
	case "TJ":
		arr, ok := singleArrayOperand(op.Operands)
		if !ok {
			return pdferror.Malformedf("\"TJ\" expects a single array operand, got %v", op.Operands)
		}
		for _, elem := range arr {
			switch v := elem.(type) {
			case syntax.String:
				in.showText(st, []byte(v))
			case syntax.Integer:
				in.showAdjustment(st, float64(v))
			case syntax.Real:
				in.showAdjustment(st, float64(v))
			}
		}

	default:
		// Every other operator - Form XObject painting (a "Do" naming a
		// /Form rather than an /Image XObject - see doXObject), shading
		// ("sh"), marked content ("BMC"/"BDC"/"EMC"/"MP"/"DP"), and
		// anything else this package does not recognize - is silently
		// skipped; see Interpret's doc comment for why.
	}
	return nil
}

// point interprets operands as exactly one user-space (x, y) pair and
// transforms it by st.CTM into device space.
func (in *interpreter) point(st *graphics.State, operands []syntax.Object) (graphics.Point, error) {
	vals, err := requireFloats(operands, 2)
	if err != nil {
		return graphics.Point{}, err
	}
	x, y := st.CTM.Apply(vals[0], vals[1])
	return graphics.Point{X: x, Y: y}, nil
}

// points interprets operands as exactly n user-space (x, y) pairs,
// transforming each into device space - used by the curve operators
// ("c": n=3, "v"/"y": n=2).
func (in *interpreter) points(st *graphics.State, operands []syntax.Object, n int) ([]graphics.Point, error) {
	vals, err := requireFloats(operands, n*2)
	if err != nil {
		return nil, err
	}
	pts := make([]graphics.Point, n)
	for i := 0; i < n; i++ {
		x, y := st.CTM.Apply(vals[2*i], vals[2*i+1])
		pts[i] = graphics.Point{X: x, Y: y}
	}
	return pts, nil
}

// appendRect implements "re": append a complete closed rectangular
// subpath, its four corners transformed individually by st.CTM (which
// may not be axis-aligned, e.g. under a rotation - see Path.AppendRect's
// doc comment).
func (in *interpreter) appendRect(st *graphics.State, operands []syntax.Object) error {
	vals, err := requireFloats(operands, 4)
	if err != nil {
		return err
	}
	x, y, w, h := vals[0], vals[1], vals[2], vals[3]
	userCorners := [4][2]float64{{x, y}, {x + w, y}, {x + w, y + h}, {x, y + h}}
	var corners [4]graphics.Point
	for i, c := range userCorners {
		dx, dy := st.CTM.Apply(c[0], c[1])
		corners[i] = graphics.Point{X: dx, Y: dy}
	}
	in.path.AppendRect(corners)
	return nil
}

// fillCurrentPath appends a Fill (or, when a shading pattern is the
// current fill paint - see graphics.State.FillShading's doc comment - a
// Shading) DrawOp for the current path, if it is non-empty, under rule,
// using st's current fill color/shading and clip stack. It does not
// reset the current path - painting operators like "B" fill and stroke
// the very same path, so resetting is endPath's job, called once after
// every painting operator regardless of which combination of fill/stroke
// it performed.
func (in *interpreter) fillCurrentPath(st *graphics.State, rule graphics.FillRule) {
	if len(in.path.Subpaths) == 0 {
		return
	}
	op := graphics.DrawOp{Path: clonePath(&in.path), Rule: rule, Clips: st.Clips}
	if st.FillShading != nil {
		op.Shading = st.FillShading
	} else {
		op.Color = st.FillColor
	}
	in.list = append(in.list, op)
}

// strokeCurrentPath appends a Stroke DrawOp - converted up front to its
// filled outline via graphics.StrokeToFill, since internal/raster only
// ever fills - for the current path, if it is non-empty.
//
// st.LineWidth is a user-space length; it is scaled here by
// approximate device-space scale factor of st.CTM (the square root of
// its linear part's determinant, i.e. the square root of the area
// scale factor) before being handed to StrokeToFill, since the path's
// own points are already in device space by the time they reach here.
// This is an approximation for a CTM with anisotropic (non-uniform x
// vs y) scaling - PDF's own model strokes with the CTM-transformed
// shape of a circular pen, which is only exactly a scaled circle under
// uniform scaling - but is exact for the common case (uniform scale
// and/or rotation) and reasonable otherwise; see StrokeToFill's own
// doc comment for its further (join-shape) simplifications.
func (in *interpreter) strokeCurrentPath(st *graphics.State) {
	if len(in.path.Subpaths) == 0 {
		return
	}
	deviceWidth := st.LineWidth * ctmScale(st.CTM)
	outline := graphics.StrokeToFill(&in.path, deviceWidth, st.LineCap, st.LineJoin, st.MiterLimit)
	op := graphics.DrawOp{Path: outline, Rule: graphics.NonZero, Clips: st.Clips}
	if st.StrokeShading != nil {
		op.Shading = st.StrokeShading
	} else {
		op.Color = st.StrokeColor
	}
	in.list = append(in.list, op)
}

// ctmScale returns the approximate device-space scale factor of m's
// linear part, used to convert a user-space line width into device
// space - see strokeCurrentPath's doc comment.
func ctmScale(m graphics.Matrix) float64 {
	det := m.A*m.D - m.B*m.C
	if det < 0 {
		det = -det
	}
	if det == 0 {
		return 1
	}
	return math.Sqrt(det)
}

// markPendingClip records that the current path should become an
// additional clip once the next painting operator finishes - see
// endPath.
func (in *interpreter) markPendingClip(rule graphics.FillRule) {
	if len(in.path.Subpaths) == 0 {
		return
	}
	in.pendingClip = clonePath(&in.path)
	in.pendingClipRule = rule
	in.hasPendingClip = true
}

// endPath applies any pending clip (see markPendingClip) to st and
// resets the current path, ready for the next run of path construction
// operators. It is called after every painting operator ("S", "f", "n",
// ...), matching the specification's rule that a path (and any
// "W"/"W*" clip it carries) only ever affects one painting operation.
func (in *interpreter) endPath(st *graphics.State) {
	if in.hasPendingClip {
		st.Clips = st.WithClip(in.pendingClip, in.pendingClipRule).Clips
		in.pendingClip = nil
		in.hasPendingClip = false
	}
	in.path = graphics.Path{}
}

// clonePath returns an independent deep copy of p, so a DrawOp captured
// from the interpreter's in-progress path is never later mutated (or
// have its backing arrays shared and stomped on) by subsequent path
// construction.
func clonePath(p *graphics.Path) *graphics.Path {
	clone := &graphics.Path{Subpaths: make([]graphics.Subpath, len(p.Subpaths))}
	for i, sp := range p.Subpaths {
		pts := make([]graphics.Point, len(sp.Points))
		copy(pts, sp.Points)
		clone.Subpaths[i] = graphics.Subpath{Points: pts, Closed: sp.Closed}
	}
	return clone
}

// requireFloats requires operands to contain exactly n numeric
// (Integer or Real) values, returning them as float64s in order.
func requireFloats(operands []syntax.Object, n int) ([]float64, error) {
	if len(operands) != n {
		return nil, pdferror.Malformedf("operator expects %d numeric operand(s), got %d", n, len(operands))
	}
	out := make([]float64, n)
	for i, o := range operands {
		v, ok := numberValue(o)
		if !ok {
			return nil, pdferror.Malformedf("operand %d (%#v) is not a number", i, o)
		}
		out[i] = v
	}
	return out, nil
}

func numberValue(o syntax.Object) (float64, bool) {
	switch v := o.(type) {
	case syntax.Integer:
		return float64(v), true
	case syntax.Real:
		return float64(v), true
	default:
		return 0, false
	}
}

// grayColor converts a single DeviceGray component to Color.
func grayColor(g float64) graphics.Color {
	return graphics.Color{R: g, G: g, B: g}
}

// cmykColor converts DeviceCMYK components to Color using the PDF
// specification's own baseline (non-color-managed) DeviceCMYK ->
// DeviceRGB transform (section 8.6.5.3): r = 1 - min(1, c+k), and
// likewise for g and b. This is not print-accurate, but is exactly the
// specification's documented default behavior in the absence of an ICC
// profile or other color management, and is what this project targets
// before Phase 3's broader color-space work.
func cmykColor(c, m, y, k float64) graphics.Color {
	return graphics.Color{
		R: 1 - clamp01(c+k),
		G: 1 - clamp01(m+k),
		B: 1 - clamp01(y+k),
	}
}

func clamp01(v float64) float64 {
	if v > 1 {
		return 1
	}
	if v < 0 {
		return 0
	}
	return v
}

// colorFromComponents implements sc/SC/scn/SCN's fallback behavior for
// when the active color space has not been resolved (see
// colorspace.go's colorForOperandsWithSpace, which calls this whenever
// "cs"/"CS" was never invoked, named something setColorSpace could not
// resolve, or the resolved space's component count does not match
// operands): it infers DeviceGray/DeviceRGB/DeviceCMYK directly from
// how many numeric operands are given (1, 3, or 4, respectively) - the
// number of components those three color spaces need is unambiguous, so
// this works correctly whenever the content stream's /ColorSpace
// resource actually was one of those three, without this package needing
// to resolve /Resources to know which. A trailing Name operand (scn/SCN
// with a pattern) is explicitly detected and rejected as unsupported
// rather than misread as a numeric component.
func colorFromComponents(operands []syntax.Object) (graphics.Color, error) {
	if len(operands) > 0 {
		if _, isName := operands[len(operands)-1].(syntax.Name); isName {
			return graphics.Color{}, pdferror.Unsupportedf("pattern color space (scn/SCN with a pattern name)")
		}
	}
	vals := make([]float64, len(operands))
	for i, o := range operands {
		v, ok := numberValue(o)
		if !ok {
			return graphics.Color{}, pdferror.Malformedf("sc/scn operand %d is not a number", i)
		}
		vals[i] = v
	}
	switch len(vals) {
	case 1:
		return grayColor(vals[0]), nil
	case 3:
		return graphics.Color{R: vals[0], G: vals[1], B: vals[2]}, nil
	case 4:
		return cmykColor(vals[0], vals[1], vals[2], vals[3]), nil
	default:
		return graphics.Color{}, pdferror.Unsupportedf("color space with %d components (without resolving /Resources /ColorSpace, only DeviceGray/RGB/CMYK - 1, 3, or 4 components - are recognized)", len(vals))
	}
}

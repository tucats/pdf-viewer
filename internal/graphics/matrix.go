package graphics

// Matrix is a PDF-style 2D affine transform, written as the six numbers
// [a b c d e f] PDF itself uses (in a content stream's "cm" operator, a
// page's own /Matrix entries, and so on), representing the 3x3 matrix
//
//	[ a b 0 ]
//	[ c d 0 ]
//	[ e f 1 ]
//
// applied to a point as a row vector: [x y 1] * M = [a*x+c*y+e,
// b*x+d*y+f, 1]. This is exactly the convention section 8.3.3 of the PDF
// specification ("Coordinate Systems for Text and Graphics States")
// defines, chosen here to match it precisely rather than a more
// "textbook" column-vector convention, so that translating the
// specification's own matrix formulas into code stays direct.
type Matrix struct {
	A, B, C, D, E, F float64
}

// Identity returns the matrix that leaves every point unchanged.
func Identity() Matrix {
	return Matrix{A: 1, D: 1}
}

// Translate returns a matrix that translates by (tx, ty).
func Translate(tx, ty float64) Matrix {
	return Matrix{A: 1, D: 1, E: tx, F: ty}
}

// Scale returns a matrix that scales by (sx, sy) about the origin.
func Scale(sx, sy float64) Matrix {
	return Matrix{A: sx, D: sy}
}

// Mul returns the combined matrix that applies m first and then n -
// exactly the operation PDF's "cm" operator performs when concatenating
// a new matrix onto the current transformation matrix: per the
// specification, "the specified matrix shall be concatenated with the
// current transformation matrix", computed as CTM′ = m × CTM, which in
// this package's row-vector convention is m.Mul(ctm). A point in the
// space m.Mul(n) describes is therefore reached by applying m's mapping
// first (from the newest, innermost space) and n's mapping second
// (toward device space).
func (m Matrix) Mul(n Matrix) Matrix {
	return Matrix{
		A: m.A*n.A + m.B*n.C,
		B: m.A*n.B + m.B*n.D,
		C: m.C*n.A + m.D*n.C,
		D: m.C*n.B + m.D*n.D,
		E: m.E*n.A + m.F*n.C + n.E,
		F: m.E*n.B + m.F*n.D + n.F,
	}
}

// Apply transforms the point (x, y) by m, returning the transformed
// coordinates.
func (m Matrix) Apply(x, y float64) (float64, float64) {
	return m.A*x + m.C*y + m.E, m.B*x + m.D*y + m.F
}

// ApplyVector transforms the vector (dx, dy) by m's linear part only
// (A, B, C, D), ignoring translation (E, F) - the correct transform for
// a direction or a length (such as a line width) rather than a point,
// since translating a direction makes no sense.
func (m Matrix) ApplyVector(dx, dy float64) (float64, float64) {
	return m.A*dx + m.C*dy, m.B*dx + m.D*dy
}

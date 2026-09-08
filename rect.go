package pdfviewer

// Rect is an axis-aligned rectangle expressed in PDF points (1/72 inch),
// the unit PDF itself uses for page geometry - see the README's
// "Pagination and resource ownership" section. It is what Page.Bounds
// returns.
//
// The field names follow PDF's own convention for its four-number box
// arrays (MediaBox, CropBox, and so on): LLX/LLY is the lower-left
// corner and URX/URY is the upper-right corner. The PDF specification
// does not actually require LLX < URX or LLY < URY - a producer is free
// to list a box's corners in either order - so this type does not
// assume or enforce that either; a caller that specifically needs a
// normalized (min, min)-(max, max) rectangle should compute it from
// these four values itself.
type Rect struct {
	LLX, LLY, URX, URY float64
}

// Width returns URX - LLX. Because PDF does not guarantee LLX <= URX
// (see the type's doc comment), this can be negative; callers that need
// a non-negative width should take its absolute value themselves.
func (r Rect) Width() float64 {
	return r.URX - r.LLX
}

// Height returns URY - LLY. See Width for why this can be negative.
func (r Rect) Height() float64 {
	return r.URY - r.LLY
}

package raster

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
)

func rectPath(x0, y0, x1, y1 float64) *graphics.Path {
	var p graphics.Path
	p.AppendRect([4]graphics.Point{{X: x0, Y: y0}, {X: x1, Y: y0}, {X: x1, Y: y1}, {X: x0, Y: y1}})
	return &p
}

// TestRasterizeCoverageFullyCoveredInterior confirms a pixel comfortably
// inside a large rectangle gets full (1.0) coverage.
func TestRasterizeCoverageFullyCoveredInterior(t *testing.T) {
	path := rectPath(0, 0, 10, 10)
	cov := rasterizeCoverage(path, graphics.NonZero, 0, 0, 10, 10)
	// Pixel (5,5) is comfortably interior.
	got := cov[5*10+5]
	if got < 0.999 {
		t.Errorf("interior pixel coverage = %v, want ~1.0", got)
	}
}

// TestRasterizeCoverageFullyOutside confirms a pixel entirely outside
// the shape gets zero coverage.
func TestRasterizeCoverageFullyOutside(t *testing.T) {
	path := rectPath(2, 2, 4, 4)
	cov := rasterizeCoverage(path, graphics.NonZero, 0, 0, 10, 10)
	got := cov[8*10+8]
	if got != 0 {
		t.Errorf("exterior pixel coverage = %v, want 0", got)
	}
}

// TestRasterizeCoverageHalfCoveredPixelIsAntiAliased confirms a
// rectangle edge that lands exactly halfway through a pixel column
// produces roughly 0.5 coverage there - the horizontal anti-aliasing
// addSpanCoverage implements.
func TestRasterizeCoverageHalfCoveredPixelIsAntiAliased(t *testing.T) {
	path := rectPath(0, 0, 2.5, 4) // right edge at x=2.5, cutting column 2 in half
	cov := rasterizeCoverage(path, graphics.NonZero, 0, 0, 4, 4)
	got := cov[1*4+2] // row 1, column 2
	if got < 0.45 || got > 0.55 {
		t.Errorf("half-covered pixel coverage = %v, want ~0.5", got)
	}
	// Column 0 and 1 should be fully covered; column 3 fully empty.
	if got := cov[1*4+0]; got < 0.999 {
		t.Errorf("fully covered pixel (col 0) coverage = %v, want ~1.0", got)
	}
	if got := cov[1*4+3]; got != 0 {
		t.Errorf("fully uncovered pixel (col 3) coverage = %v, want 0", got)
	}
}

// TestRasterizeCoverageEvenOddVsNonZero constructs two overlapping,
// same-direction squares (a classic case where the two fill rules
// disagree): under nonzero winding the overlap region is still filled
// (winding number 2), but under even-odd it is treated as "outside"
// (crossed twice, an even number of times).
func TestRasterizeCoverageEvenOddVsNonZero(t *testing.T) {
	var p graphics.Path
	p.AppendRect([4]graphics.Point{{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}, {X: 0, Y: 10}})
	p.AppendRect([4]graphics.Point{{X: 3, Y: 3}, {X: 13, Y: 3}, {X: 13, Y: 13}, {X: 3, Y: 13}})

	// (5,5) is inside both squares - the overlap region.
	nz := rasterizeCoverage(&p, graphics.NonZero, 0, 0, 16, 16)
	if got := nz[5*16+5]; got < 0.999 {
		t.Errorf("nonzero overlap coverage = %v, want ~1.0", got)
	}

	eo := rasterizeCoverage(&p, graphics.EvenOdd, 0, 0, 16, 16)
	if got := eo[5*16+5]; got != 0 {
		t.Errorf("even-odd overlap coverage = %v, want 0", got)
	}
	// But a point inside only one square (e.g. (1,1)) is filled under
	// both rules.
	if got := nz[1*16+1]; got < 0.999 {
		t.Errorf("nonzero single-cover coverage = %v, want ~1.0", got)
	}
	if got := eo[1*16+1]; got < 0.999 {
		t.Errorf("even-odd single-cover coverage = %v, want ~1.0", got)
	}
}

// TestRasterizeCoverageOppositeWindingCancelsUnderNonZero confirms
// nonzero winding actually tracks direction, not just a presence count:
// a hole cut by winding the inner square the opposite way should not be
// filled under NonZero even though EvenOdd (which ignores direction)
// treats it identically to the previous test's overlap case.
func TestRasterizeCoverageOppositeWindingCancelsUnderNonZero(t *testing.T) {
	var p graphics.Path
	// Outer square, clockwise in device (y-down) space.
	p.AppendRect([4]graphics.Point{{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}, {X: 0, Y: 10}})
	// Inner square, wound the opposite direction (counter-clockwise),
	// carving a hole under the nonzero rule.
	var hole graphics.Subpath
	hole.Points = []graphics.Point{{X: 3, Y: 3}, {X: 3, Y: 7}, {X: 7, Y: 7}, {X: 7, Y: 3}}
	hole.Closed = true
	p.Subpaths = append(p.Subpaths, hole)

	nz := rasterizeCoverage(&p, graphics.NonZero, 0, 0, 10, 10)
	if got := nz[5*10+5]; got != 0 {
		t.Errorf("nonzero hole coverage = %v, want 0 (opposite winding should cancel)", got)
	}
	if got := nz[1*10+1]; got < 0.999 {
		t.Errorf("nonzero outer-only coverage = %v, want ~1.0", got)
	}
}

func TestRasterizeCoverageEmptyPathReturnsNil(t *testing.T) {
	var p graphics.Path
	cov := rasterizeCoverage(&p, graphics.NonZero, 0, 0, 10, 10)
	for _, v := range cov {
		if v != 0 {
			t.Fatalf("empty path produced nonzero coverage")
		}
	}
}

func TestRasterizeCoverageZeroSizeWindow(t *testing.T) {
	path := rectPath(0, 0, 10, 10)
	if cov := rasterizeCoverage(path, graphics.NonZero, 5, 5, 5, 5); cov != nil {
		t.Errorf("zero-size window: got %v, want nil", cov)
	}
}

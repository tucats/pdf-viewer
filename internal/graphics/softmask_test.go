package graphics

import "testing"

// identityMask builds a 2x2 SoftMask whose DeviceToMask is the identity
// matrix (so device coordinates and mask pixel coordinates are the same
// numbers), with four distinct values so each quadrant is individually
// checkable: top-left 0, top-right 85, bottom-left 170, bottom-right 255
// ("black", "dark gray", "light gray", "white" on the usual 0-255 scale).
func identityMask() *SoftMask {
	return &SoftMask{
		Width:        2,
		Height:       2,
		Values:       []byte{0, 85, 170, 255},
		DeviceToMask: Matrix{A: 1, D: 1},
	}
}

func TestSoftMaskAtSamplesEachPixel(t *testing.T) {
	m := identityMask()
	tests := []struct {
		name string
		x, y float64
		want float64
	}{
		{"top-left", 0.5, 0.5, 0},
		{"top-right", 1.5, 0.5, 85.0 / 255},
		{"bottom-left", 0.5, 1.5, 170.0 / 255},
		{"bottom-right", 1.5, 1.5, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := m.At(tt.x, tt.y); got != tt.want {
				t.Errorf("At(%v, %v) = %v, want %v", tt.x, tt.y, got, tt.want)
			}
		})
	}
}

func TestSoftMaskAtOutsideGridReturnsZero(t *testing.T) {
	m := identityMask()
	for _, pt := range [][2]float64{
		{-0.5, 0.5}, // left of the grid
		{2.5, 0.5},  // right of the grid
		{0.5, -0.5}, // above the grid
		{0.5, 2.5},  // below the grid
	} {
		if got := m.At(pt[0], pt[1]); got != 0 {
			t.Errorf("At(%v, %v) = %v, want 0 (outside the mask's own grid should read as fully masked out, per the specification's black-backdrop default)", pt[0], pt[1], got)
		}
	}
}

func TestSoftMaskAtHonorsDeviceToMaskMapping(t *testing.T) {
	// A mask whose DeviceToMask scales device space down by half and
	// shifts it - checking that At actually applies the matrix, not
	// just indexing device coordinates directly into Values.
	m := &SoftMask{
		Width:  2,
		Height: 1,
		Values: []byte{0, 255},
		// device x=10 -> mask x=0 (into the first, black, pixel);
		// device x=12 -> mask x=1 (into the second, white, pixel).
		DeviceToMask: Matrix{A: 0.5, D: 1, E: -5},
	}
	if got := m.At(10, 0.5); got != 0 {
		t.Errorf("At(10, 0.5) = %v, want 0", got)
	}
	if got := m.At(12, 0.5); got != 1 {
		t.Errorf("At(12, 0.5) = %v, want 1", got)
	}
}

func TestSoftMaskAtDegenerateCases(t *testing.T) {
	var nilMask *SoftMask
	if got := nilMask.At(0, 0); got != 0 {
		t.Errorf("nil *SoftMask.At(0, 0) = %v, want 0", got)
	}

	empty := &SoftMask{Width: 0, Height: 0}
	if got := empty.At(0, 0); got != 0 {
		t.Errorf("empty SoftMask.At(0, 0) = %v, want 0", got)
	}
}

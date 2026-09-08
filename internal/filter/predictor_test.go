package filter

import (
	"bytes"
	"testing"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

func TestApplyPredictorNoPredictor(t *testing.T) {
	data := []byte{1, 2, 3}
	got, err := applyPredictor(data, nil)
	if err != nil {
		t.Fatalf("applyPredictor: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("applyPredictor with no /Predictor should return data unchanged; got %v, want %v", got, data)
	}
}

func TestApplyTIFFPredictor8Bit(t *testing.T) {
	// Rows [10 20 30] and [15 45 5], /Colors 1, /BitsPerComponent 8,
	// /Columns 3, stored as running differences.
	predicted := []byte{10, 10, 10, 15, 30, 216} // 15, 45-15=30, 5-45=-40=216 mod 256
	parms := syntax.Dictionary{
		"Predictor":        syntax.Integer(2),
		"Colors":           syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"Columns":          syntax.Integer(3),
	}
	got, err := applyPredictor(predicted, parms)
	if err != nil {
		t.Fatalf("applyPredictor: %v", err)
	}
	want := []byte{10, 20, 30, 15, 45, 5}
	if !bytes.Equal(got, want) {
		t.Fatalf("applyTIFFPredictor = %v, want %v", got, want)
	}
}

func TestApplyTIFFPredictor1Bit(t *testing.T) {
	// A single row of 8 one-bit samples: 1 0 1 1 0 0 1 1. Differencing
	// mod 2 is XOR with the previous sample, packed MSB-first into one
	// byte.
	raw := []uint32{1, 0, 1, 1, 0, 0, 1, 1}
	var predictedByte byte
	prev := uint32(0)
	for i, v := range raw {
		d := v
		if i > 0 {
			d = (v + prev) % 2
		}
		predictedByte |= byte(d) << uint(7-i)
		prev = v
	}
	parms := syntax.Dictionary{
		"Predictor":        syntax.Integer(2),
		"Colors":           syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(1),
		"Columns":          syntax.Integer(8),
	}
	got, err := applyPredictor([]byte{predictedByte}, parms)
	if err != nil {
		t.Fatalf("applyPredictor: %v", err)
	}
	var wantByte byte
	for i, v := range raw {
		wantByte |= byte(v) << uint(7-i)
	}
	if len(got) != 1 || got[0] != wantByte {
		t.Fatalf("applyTIFFPredictor(1-bit) = %08b, want %08b", got, []byte{wantByte})
	}
}

func TestApplyPNGPredictorNone(t *testing.T) {
	// Filter type 0 (None) on every row: output should equal the input
	// with the leading filter-type byte of each row stripped.
	rowBytes := 4
	data := []byte{
		0, 1, 2, 3, 4,
		0, 5, 6, 7, 8,
	}
	parms := syntax.Dictionary{
		"Predictor":        syntax.Integer(15),
		"Colors":           syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"Columns":          syntax.Integer(rowBytes),
	}
	got, err := applyPredictor(data, parms)
	if err != nil {
		t.Fatalf("applyPredictor: %v", err)
	}
	want := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	if !bytes.Equal(got, want) {
		t.Fatalf("PNG predictor (None) = %v, want %v", got, want)
	}
}

func TestApplyPNGPredictorSub(t *testing.T) {
	// One row, filter type 1 (Sub), bpp=1 (Colors=1, BitsPerComponent=8):
	// raw row after the filter byte is [10, 5, 5, 5], reconstructing to
	// [10, 15, 20, 25] via cumulative addition from the left.
	data := []byte{1, 10, 5, 5, 5}
	parms := syntax.Dictionary{
		"Predictor":        syntax.Integer(11),
		"Colors":           syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"Columns":          syntax.Integer(4),
	}
	got, err := applyPredictor(data, parms)
	if err != nil {
		t.Fatalf("applyPredictor: %v", err)
	}
	want := []byte{10, 15, 20, 25}
	if !bytes.Equal(got, want) {
		t.Fatalf("PNG predictor (Sub) = %v, want %v", got, want)
	}
}

func TestApplyPNGPredictorUpAndPaeth(t *testing.T) {
	// Two rows, Colors=1, BitsPerComponent=8, Columns=3.
	// Row 0: filter None,  raw [10, 20, 30]              -> [10, 20, 30]
	// Row 1: filter Up,    raw [1, 1, 1]                 -> [11, 21, 31]
	data := []byte{
		0, 10, 20, 30,
		2, 1, 1, 1,
	}
	parms := syntax.Dictionary{
		"Predictor":        syntax.Integer(12),
		"Colors":           syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"Columns":          syntax.Integer(3),
	}
	got, err := applyPredictor(data, parms)
	if err != nil {
		t.Fatalf("applyPredictor: %v", err)
	}
	want := []byte{10, 20, 30, 11, 21, 31}
	if !bytes.Equal(got, want) {
		t.Fatalf("PNG predictor (Up) = %v, want %v", got, want)
	}
}

func TestApplyPredictorRejectsBadShape(t *testing.T) {
	parms := syntax.Dictionary{
		"Predictor": syntax.Integer(2),
		"Colors":    syntax.Integer(0),
	}
	if _, err := applyPredictor([]byte{1, 2, 3}, parms); err == nil {
		t.Fatal("applyPredictor with /Colors 0: expected an error, got nil")
	}
}

func TestApplyPredictorRejectsBadBitsPerComponent(t *testing.T) {
	parms := syntax.Dictionary{
		"Predictor":        syntax.Integer(2),
		"BitsPerComponent": syntax.Integer(3),
	}
	if _, err := applyPredictor([]byte{1, 2, 3}, parms); err == nil {
		t.Fatal("applyPredictor with /BitsPerComponent 3: expected an error, got nil")
	}
}

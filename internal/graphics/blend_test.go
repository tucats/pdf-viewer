package graphics

import "testing"

func TestBlendNormalIgnoresBackdrop(t *testing.T) {
	if got := Blend(BlendNormal, 0.9, 0.2); got != 0.2 {
		t.Fatalf("Blend(Normal, 0.9, 0.2) = %v, want 0.2 (source, unchanged)", got)
	}
}

func TestBlendMultiplyDarkensTowardBlack(t *testing.T) {
	if got := Blend(BlendMultiply, 0.5, 0.5); got != 0.25 {
		t.Fatalf("Blend(Multiply, 0.5, 0.5) = %v, want 0.25", got)
	}
	if got := Blend(BlendMultiply, 1, 1); got != 1 {
		t.Fatalf("Blend(Multiply, 1, 1) = %v, want 1 (white x white = white)", got)
	}
	if got := Blend(BlendMultiply, 0, 1); got != 0 {
		t.Fatalf("Blend(Multiply, 0, 1) = %v, want 0 (black x anything = black)", got)
	}
}

func TestBlendScreenLightensTowardWhite(t *testing.T) {
	if got := Blend(BlendScreen, 0, 0); got != 0 {
		t.Fatalf("Blend(Screen, 0, 0) = %v, want 0", got)
	}
	if got := Blend(BlendScreen, 1, 0.3); got != 1 {
		t.Fatalf("Blend(Screen, 1, 0.3) = %v, want 1 (white screened with anything = white)", got)
	}
}

func TestBlendDarkenAndLighten(t *testing.T) {
	if got := Blend(BlendDarken, 0.3, 0.7); got != 0.3 {
		t.Fatalf("Blend(Darken, 0.3, 0.7) = %v, want 0.3", got)
	}
	if got := Blend(BlendLighten, 0.3, 0.7); got != 0.7 {
		t.Fatalf("Blend(Lighten, 0.3, 0.7) = %v, want 0.7", got)
	}
}

func TestBlendDifferenceAndExclusion(t *testing.T) {
	if got := Blend(BlendDifference, 0.8, 0.3); got != 0.5 {
		t.Fatalf("Blend(Difference, 0.8, 0.3) = %v, want 0.5", got)
	}
	if got := Blend(BlendDifference, 0.4, 0.4); got != 0 {
		t.Fatalf("Blend(Difference, 0.4, 0.4) = %v, want 0 (identical channels cancel)", got)
	}
	if got := Blend(BlendExclusion, 0, 0); got != 0 {
		t.Fatalf("Blend(Exclusion, 0, 0) = %v, want 0", got)
	}
	if got := Blend(BlendExclusion, 1, 1); got != 0 {
		t.Fatalf("Blend(Exclusion, 1, 1) = %v, want 0", got)
	}
}

func TestBlendUnrecognizedModeFallsBackToNormal(t *testing.T) {
	if got := Blend(BlendMode(999), 0.9, 0.2); got != 0.2 {
		t.Fatalf("Blend(<unrecognized>, 0.9, 0.2) = %v, want 0.2 (Normal fallback)", got)
	}
}

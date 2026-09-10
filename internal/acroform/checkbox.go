package acroform

import (
	"fmt"
	"strings"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file builds a generated appearance for a non-pushbutton button
// field (/FT /Btn - a checkbox, or one widget of a radio button group):
// an optional /MK-driven background fill and border, and - when the
// widget's current state is not "Off" - a solid inset square standing
// in for a checkmark.
//
// # Why a solid square, not an actual checkmark glyph
//
// A real Acrobat-generated checkbox appearance draws a specific
// character (almost always ZapfDingbats' "4", a checkmark, or "8", an
// "x") at the field's own /DA size and color - the same text-painting
// path textfield.go already uses. This package deliberately does not:
// building on that path here also needs to make ZapfDingbats
// available (a font this project has no built-in metrics or reason to
// treat differently from any other standard-14 name substitution
// already handles), and - more fundamentally - a checkbox with *no*
// existing appearance at all has no recorded on/off glyph choice to
// reproduce, unlike a checkbox merely missing one specific state (which
// this package never even reaches, since internal/annotation.ResolveOne
// already renders whatever state stream *does* exist). A solid inset
// square is simpler, needs no font at all, and unambiguously
// communicates "this box is checked" - a documented simplification, not
// an attempt at pixel-for-pixel Acrobat fidelity (see this package's
// doc comment).
//
// Radio buttons are drawn through this exact same code path as
// checkboxes, with no visual distinction between the two (no attempt at
// a round, rather than square, mark) - also a deliberate simplification;
// see this package's doc comment.

// checkboxMarkMarginFraction is the inset (as a fraction of the box's
// shorter side) kept between a checked mark's own edges and the
// field's /Rect - the same kind of simple, fixed-fraction layout choice
// textPadding makes for text fields.
const checkboxMarkMarginFraction = 0.2

// checkboxBorderWidth is the fixed stroke width used for a /MK /BC
// border - the specification's own /BS (border style) dictionary could
// specify a different width, but is not consulted here; this project's
// annotation appearance code (internal/annotation) does not read /BS
// for an *existing* appearance's own drawing either; a generated
// appearance's border is new content this project is producing itself,
// so it is free to pick a simple, always-reasonable value instead.
const checkboxBorderWidth = 1.0

// buildCheckboxAppearance returns the content-stream bytes for a
// generated Btn field appearance sized to width x height, or ok=false if
// there is nothing to paint: a push button (see this package's doc
// comment - never value-driven), or a field whose checked/unchecked
// state cannot be determined at all (no widget-level /AS and no
// field-level /V, so this package has no basis to decide even the
// binary on/off question, let alone which of several radio-group
// widgets is selected - see checkboxChecked).
func buildCheckboxAppearance(r Resolver, widgetDict syntax.Dictionary, f Field, width, height float64) ([]byte, syntax.Dictionary, bool) {
	if f.Ff&ffPushbutton != 0 {
		return nil, nil, false
	}
	checked, ok := checkboxChecked(r, widgetDict, f)
	if !ok {
		return nil, nil, false
	}

	var b strings.Builder
	b.WriteString("q\n")

	mk, _ := dictEntry(r, widgetDict, "MK")
	if mk != nil {
		if bg, ok := floatArrayEntry(r, mk, "BG"); ok {
			if op, ok := colorOperator(bg, false); ok {
				fmt.Fprintf(&b, "%s\n0 0 %s %s re f\n", op, formatNumber(width), formatNumber(height))
			}
		}
		if bc, ok := floatArrayEntry(r, mk, "BC"); ok {
			if op, ok := colorOperator(bc, true); ok {
				half := checkboxBorderWidth / 2
				fmt.Fprintf(&b, "%s\n%s w\n%s %s %s %s re S\n", op, formatNumber(checkboxBorderWidth),
					formatNumber(half), formatNumber(half),
					formatNumber(width-checkboxBorderWidth), formatNumber(height-checkboxBorderWidth))
			}
		}
	}

	if checked {
		if extra := nonFontOperatorsSource(f.DA); len(extra) > 0 {
			b.Write(extra)
		} else {
			b.WriteString("0 g\n")
		}
		margin := clampFloat(min(width, height)*checkboxMarkMarginFraction, 0, min(width, height)/2)
		fmt.Fprintf(&b, "%s %s %s %s re f\n",
			formatNumber(margin), formatNumber(margin),
			formatNumber(width-2*margin), formatNumber(height-2*margin))
	}

	b.WriteString("Q\n")
	return []byte(b.String()), syntax.Dictionary{}, true
}

// checkboxChecked decides whether widgetDict's button is currently in a
// non-"Off" state: preferring the widget's own /AS (appearance state -
// what a real viewer would use to pick which /AP /N substream to show,
// were one present), and falling back to the field-level /V (correct
// for an ordinary checkbox, whose /V and /AS are always kept in sync
// by any well-formed producer, but not reliably meaningful for one
// widget of a radio button group sharing a single field-level /V - see
// this package's doc comment on why radio buttons receive no more
// precise treatment than this). ok=false when neither is present at
// all, or /AS/V is present but not a syntax.Name (malformed) - "cannot
// tell" is treated as "paint nothing" here, exactly like every other
// unresolvable-input case in this package.
func checkboxChecked(r Resolver, widgetDict syntax.Dictionary, f Field) (checked bool, ok bool) {
	if asObj, present := widgetDict["AS"]; present {
		if resolved, err := resolveIfRef(r, asObj); err == nil {
			if name, ok := resolved.(syntax.Name); ok {
				return name != "Off", true
			}
		}
	}
	if name, ok := f.V.(syntax.Name); ok {
		return name != "Off", true
	}
	return false, false
}

// colorOperator formats vals (an /MK /BC or /BG color array, 0/1/3/4
// components meaning "no color"/DeviceGray/DeviceRGB/DeviceCMYK per
// 12.5.6.19 - the same component-count convention internal/content's
// colorFromComponents uses for sc/scn) as a content-stream color-setting
// operator, using the uppercase (stroking) operator name when stroke is
// true. ok=false for a component count that means neither "no color"
// nor one of the three device color spaces (0, 1, 3, or 4).
func colorOperator(vals []float64, stroke bool) (string, bool) {
	var op string
	switch len(vals) {
	case 0:
		return "", false
	case 1:
		op = "g"
	case 3:
		op = "rg"
	case 4:
		op = "k"
	default:
		return "", false
	}
	if stroke {
		op = strings.ToUpper(op)
	}
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = formatNumber(v)
	}
	return strings.Join(parts, " ") + " " + op, true
}

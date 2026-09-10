package acroform

import "github.com/tucats/pdf-viewer/internal/syntax"

// Field flag bit values (ISO 32000-1 Table 221, "Field flags common to
// all field types", and Table 226/227, the per-field-type additions),
// numbered the way the specification itself numbers bits: bit 1 is the
// least significant. Only the flags this package's own generation logic
// actually branches on are named here - the specification defines
// several more (e.g. NoExport, DoNotSpellCheck) that affect form-filling
// behavior this project, a renderer with no interactivity, never needs
// to know about.
const (
	// ffMultiline is bit 13 of a text field's /Ff: the field's value may
	// span more than one line, and should be word-wrapped to fit its
	// /Rect rather than clipped or overflowing - see textfield.go.
	ffMultiline = 1 << 12

	// ffPassword is bit 14 of a text field's /Ff: the field's value is a
	// password and must never be displayed in the clear - this package
	// deliberately generates no appearance at all for such a field (see
	// this package's doc comment), so nothing else ever inspects this
	// bit except the one check that skips generation entirely.
	ffPassword = 1 << 13

	// ffRadio is bit 16 of a button field's /Ff: the field is a radio
	// button (part of a mutually exclusive group) rather than an
	// ordinary checkbox. This package renders both identically (see
	// checkbox.go's doc comment), so this bit is currently unused beyond
	// documenting the distinction exists; it is defined here so that
	// distinction can be added later without re-deriving the bit value.
	ffRadio = 1 << 15

	// ffPushbutton is bit 17 of a button field's /Ff: the field is a
	// push button (an action trigger with an icon/caption, not a value a
	// user selects) rather than a checkbox or radio button. Push buttons
	// carry no meaningful /V to paint as a check mark, so this package
	// skips generation for them entirely - see checkbox.go.
	ffPushbutton = 1 << 16

	// ffComb is bit 25 of a text field's /Ff: the field's value should be
	// laid out in MaxLen evenly spaced character cells rather than as
	// ordinary flowing text. Not implemented (see this package's doc
	// comment) - a comb field's /Ff is never actually tested against
	// this constant; it is defined so that omission is a documented,
	// deliberate choice rather than a silently absent feature.
	ffComb = 1 << 24
)

// maxFieldTreeDepth bounds how many /Parent links this package will
// follow while resolving a widget's inherited field attributes - the
// same kind of small, generous recursion guard internal/model's
// maxPageTreeDepth applies to the page tree, for the same reason (a
// cyclic or absurdly deep /Parent chain in a hostile or corrupted file
// must not recurse without bound; see the repository README's
// "Dependency and safety policy"). Real AcroForm field hierarchies are
// rarely more than two or three levels deep even for a radio button
// group's widgets, so this bound is never reached by a legitimate file.
const maxFieldTreeDepth = 64

// Field is one widget annotation's fully resolved (inheritance-applied)
// set of AcroForm field attributes - everything appearance.go needs to
// decide what, if anything, to paint for a widget that has no usable
// existing appearance stream.
type Field struct {
	// FT is the field type: "Tx" (text), "Ch" (choice), "Btn" (button -
	// checkbox, radio button, or push button, distinguished by Ff below),
	// or "Sig" (signature). Empty if no /FT was found anywhere in the
	// widget's own dictionary or its /Parent ancestry, which this
	// package treats as "nothing to generate" rather than guessing.
	FT syntax.Name

	// Ff is the field flags integer (0 if absent anywhere in the
	// ancestry) - see the ff* constants above for the bits this package
	// actually inspects.
	Ff int

	// V is the field's current value, exactly as found (a syntax.Name
	// for a checkbox/radio button's on/off state, a syntax.String for a
	// text field or a single-selection choice field, a syntax.Array of
	// strings for a multi-select list box, or absent/syntax.Null for a
	// field with no value set at all).
	V syntax.Object

	// DA is the default appearance string (raw content-stream-operator
	// bytes, e.g. "/Helv 12 Tf 0 g") that governs the font, size, and
	// color a generated appearance should use - see da.go. Nil if no /DA
	// was found on the widget, anywhere in its /Parent ancestry, or on
	// the AcroForm dictionary itself (the specification's own fallback
	// of last resort, 12.7.3.3).
	DA []byte

	// Q is the field's quadding (text justification): 0 (left, the
	// default when absent), 1 (center), or 2 (right) - 12.7.3.3.
	Q int

	// DR is the AcroForm dictionary's /DR (default resources) entry, the
	// resource dictionary a /DA font name is looked up in - unlike FT/Ff/
	// V/DA/Q, /DR is not documented as inheritable through the field
	// tree; this package reads it once, directly from the AcroForm
	// dictionary, matching where real producers always place it.
	DR syntax.Dictionary
}

// Resolve walks widgetDict's /Parent chain (if any), merging inherited
// AcroForm field attributes with widgetDict's own (widgetDict's own
// entries always win - the specification's inheritance rule is "use the
// value at this level if present, otherwise ask the parent", exactly
// like internal/model's page-tree attribute inheritance), then falls
// back to acroForm's own /DA for a field whose ancestry specifies none
// at all (12.7.3.3's documented last-resort default).
//
// acroForm may be nil (a document with no /AcroForm dictionary at all,
// or one internal/model.Document.AcroForm could not resolve) - every
// field this package's own generation logic looks at needs at least
// /DA/DR to do anything useful, so a nil acroForm simply means every
// widget in the document resolves to a Field with no usable DA/DR,
// which appearance.go already treats as "nothing to generate".
func Resolve(r Resolver, widgetDict syntax.Dictionary) Field {
	var f Field
	haveFT, haveFf, haveV, haveDA, haveQ := false, false, false, false, false

	dict := widgetDict
	for depth := 0; dict != nil && depth < maxFieldTreeDepth; depth++ {
		if !haveFT {
			if ft, ok := dict["FT"].(syntax.Name); ok {
				f.FT = ft
				haveFT = true
			}
		}
		if !haveFf {
			if ff, ok := numericEntry(r, dict, "Ff"); ok {
				f.Ff = int(ff)
				haveFf = true
			}
		}
		if !haveV {
			if v, ok := dict["V"]; ok {
				resolved, err := resolveIfRef(r, v)
				if err == nil {
					f.V = resolved
					haveV = true
				}
			}
		}
		if !haveDA {
			if da, ok := stringEntry(r, dict, "DA"); ok {
				f.DA = da
				haveDA = true
			}
		}
		if !haveQ {
			if q, ok := numericEntry(r, dict, "Q"); ok {
				f.Q = int(q)
				haveQ = true
			}
		}

		parentObj, ok := dict["Parent"]
		if !ok {
			break
		}
		resolved, err := resolveIfRef(r, parentObj)
		if err != nil {
			break
		}
		dict, _ = resolved.(syntax.Dictionary)
	}

	return f
}

// ResolveWithAcroForm is Resolve, followed by filling in DA (if the
// field tree itself specified none) and DR from acroForm - split out as
// its own entry point (rather than folded into Resolve) so that
// field_test.go can exercise the field-tree walk in isolation, without
// needing to construct an AcroForm dictionary just to test inheritance.
func ResolveWithAcroForm(r Resolver, widgetDict syntax.Dictionary, acroForm syntax.Dictionary) Field {
	f := Resolve(r, widgetDict)
	if acroForm == nil {
		return f
	}
	if dr, ok := dictEntry(r, acroForm, "DR"); ok {
		f.DR = dr
	}
	if f.DA == nil {
		if da, ok := stringEntry(r, acroForm, "DA"); ok {
			f.DA = da
		}
	}
	return f
}

// numericEntry resolves dict[key] (following a top-level reference) and
// reports it as a float64 if it is a number.
func numericEntry(r Resolver, dict syntax.Dictionary, key syntax.Name) (float64, bool) {
	obj, ok := dict[key]
	if !ok {
		return 0, false
	}
	resolved, err := resolveIfRef(r, obj)
	if err != nil {
		return 0, false
	}
	return numberValue(resolved)
}

// stringEntry resolves dict[key] (following a top-level reference) and
// reports its raw bytes if it is a syntax.String - used for /DA, which
// the specification defines as a string holding content-stream-operator
// syntax (see da.go), not ordinary text.
func stringEntry(r Resolver, dict syntax.Dictionary, key syntax.Name) ([]byte, bool) {
	obj, ok := dict[key]
	if !ok {
		return nil, false
	}
	resolved, err := resolveIfRef(r, obj)
	if err != nil {
		return nil, false
	}
	s, ok := resolved.(syntax.String)
	if !ok {
		return nil, false
	}
	return []byte(s), true
}

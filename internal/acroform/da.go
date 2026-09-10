package acroform

import (
	"github.com/tucats/pdf-viewer/internal/content"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements reading the two pieces of a field's /DA (default
// appearance) string that appearance.go needs to know explicitly - which
// font resource name and size to use - without needing to reimplement
// content-stream tokenizing to find them.
//
// # Why /DA can be handed to internal/content.Parse directly
//
// A /DA string's content is exactly a content-stream operator sequence
// (12.7.3.3): normally just a "Tf" (select font and size) followed by a
// color operator ("g"/"rg"/"k" - DeviceGray/RGB/CMYK, never "cs"/"scn"),
// e.g. "/Helv 12 Tf 0 g". That is precisely the grammar internal/content.
// Parse already tokenizes page and Form XObject content streams with -
// so this package reuses it rather than writing a second, smaller
// tokenizer for what is structurally the identical syntax. This also
// means appearance.go can copy /DA's raw bytes verbatim into a generated
// appearance's own content stream to set up the font and color exactly
// as the field's producer specified, needing to actually interpret only
// the "Tf" operator's operands for itself (to know what to measure text
// width with) - see appearance.go.

// DefaultAppearance is the "Tf" operator's operands from a field's /DA
// string - everything appearance.go's font/size-dependent logic
// (measuring text width, choosing a baseline position, auto-sizing)
// needs to know without re-parsing /DA itself.
type DefaultAppearance struct {
	// FontName is the resource name a generated appearance's own "Tf"
	// operator should select - looked up in the AcroForm dictionary's
	// /DR /Font subdictionary (see appearance.go).
	FontName syntax.Name

	// FontSize is the requested point size, or 0 for "auto-size": the
	// specification's own convention (12.7.3.3) for "choose a size that
	// makes the value fit the field's /Rect", which appearance.go
	// implements as a documented heuristic (see its own doc comment).
	FontSize float64
}

// ParseDA parses da (a field's raw /DA bytes) looking for its "Tf"
// operator, returning ok=false if da does not tokenize as valid
// content-stream syntax at all, or contains no "Tf" operator (both
// meaning "this field's /DA cannot tell us what font to use" - a
// malformed or absent /DA that appearance.go's caller must treat as
// "nothing to generate", the same tolerance this project extends to
// every other malformed-but-optional field rather than failing outright).
//
// A /DA with more than one "Tf" (not valid per the specification, but
// not this package's job to reject) uses the *last* one, matching how a
// real content-stream interpreter would apply each operator in sequence
// and simply end up with whatever the final "Tf" set.
func ParseDA(da []byte) (DefaultAppearance, bool) {
	ops, err := content.Parse(da)
	if err != nil {
		return DefaultAppearance{}, false
	}

	var result DefaultAppearance
	found := false
	for _, op := range ops {
		if op.Name != "Tf" || len(op.Operands) != 2 {
			continue
		}
		name, ok := op.Operands[0].(syntax.Name)
		if !ok {
			continue
		}
		size, ok := numberValue(op.Operands[1])
		if !ok {
			continue
		}
		result = DefaultAppearance{FontName: name, FontSize: size}
		found = true
	}
	return result, found
}

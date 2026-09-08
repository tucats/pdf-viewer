package parser

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

// TestResolveDictionaryResolvesTopLevelReferences confirms
// ResolveDictionary resolves an indirect-reference entry to its actual
// value while leaving a direct value untouched - the behavior
// DecodeStream depends on for a stream dictionary's /Filter or
// /DecodeParms entry, and the behavior Phase 3's internal/image package
// depends on for an image XObject's dictionary entries (/ColorSpace,
// /SMask, and so on), neither of which has its own cross-reference table
// to resolve a reference with.
func TestResolveDictionaryResolvesTopLevelReferences(t *testing.T) {
	d := openFixture(t, "minimal-blank-page.pdf")

	dict := syntax.Dictionary{
		"Direct":   syntax.Integer(42),
		"Indirect": syntax.Reference{Number: 2}, // object 2 is the /Pages dictionary.
	}

	resolved, err := d.ResolveDictionary(dict)
	if err != nil {
		t.Fatalf("ResolveDictionary: %v", err)
	}

	if got, ok := resolved["Direct"].(syntax.Integer); !ok || got != 42 {
		t.Errorf("resolved[\"Direct\"] = %#v, want Integer(42) unchanged", resolved["Direct"])
	}

	pages, ok := resolved["Indirect"].(syntax.Dictionary)
	if !ok {
		t.Fatalf("resolved[\"Indirect\"] = %#v (%T), want a resolved syntax.Dictionary", resolved["Indirect"], resolved["Indirect"])
	}
	if pages["Type"] != syntax.Name("Pages") {
		t.Errorf("resolved[\"Indirect\"][\"Type\"] = %#v, want Name(\"Pages\")", pages["Type"])
	}
}

func TestResolveDictionaryPropagatesResolveErrors(t *testing.T) {
	d := openFixture(t, "minimal-blank-page.pdf")

	// Object number 999 does not exist in this fixture. Per Resolve's own
	// documented behavior, resolving a nonexistent object number yields
	// syntax.Null and no error (matching the PDF specification's rule
	// that a dangling reference behaves like null), so this exercises
	// that ResolveDictionary passes that behavior through rather than
	// treating "the referenced object doesn't exist" as a failure of its
	// own.
	dict := syntax.Dictionary{"Missing": syntax.Reference{Number: 999}}
	resolved, err := d.ResolveDictionary(dict)
	if err != nil {
		t.Fatalf("ResolveDictionary: %v", err)
	}
	if _, ok := resolved["Missing"].(syntax.Null); !ok {
		t.Errorf("resolved[\"Missing\"] = %#v, want syntax.Null{}", resolved["Missing"])
	}
}

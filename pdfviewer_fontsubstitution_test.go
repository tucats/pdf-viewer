package pdfviewer_test

import (
	"context"
	"testing"

	pdfviewer "github.com/tucats/pdf-viewer"
)

// This file tests the plumbing half of WithFontSubstitution
// (docs/FONTS.md's Phase 4c): that the option can be attached to a
// Document and does not change anything about opening or rendering a
// document whose fonts are all otherwise resolvable, and that its
// DisableSystemDefaults zero-value semantics behave as documented. The
// substitution mechanism actually changing a rendered glyph (once
// wired into internal/fonts' fallback path) is exercised separately,
// once that wiring exists.

// TestRenderWithFontSubstitutionOptionDoesNotBreakOrdinaryRendering
// confirms that simply opting into font substitution - even pointed at a
// directory with nothing useful in it - does not change the outcome for
// a document whose fonts are all embedded and already resolvable, and
// does not error or panic.
func TestRenderWithFontSubstitutionOptionDoesNotBreakOrdinaryRendering(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("two-pages.pdf"), pdfviewer.WithFontSubstitution(pdfviewer.FontSubstitution{
		Directories:           []string{t.TempDir()},
		DisableSystemDefaults: true,
	}))
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()

	page, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}
	if _, err := page.Render(context.Background(), pdfviewer.RenderOptions{}); err != nil {
		t.Fatalf("Render: %v", err)
	}
}

// TestFontSubstitutionZeroValueScansSystemDefaults confirms the
// documented zero-value behavior of FontSubstitution.
// DisableSystemDefaults: an unset (false) value means platform defaults
// ARE scanned, so FontSubstitution{} (setting nothing at all) is a
// useful, non-empty configuration rather than silently doing nothing -
// this only proves opening succeeds and the option is accepted, since a
// CI machine's actual font directories are unpredictable (see
// docs/FONTS.md's Testing section on not depending on real installed
// fonts).
func TestFontSubstitutionZeroValueScansSystemDefaults(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("minimal-blank-page.pdf"), pdfviewer.WithFontSubstitution(pdfviewer.FontSubstitution{}))
	if err != nil {
		t.Fatalf("OpenFile with a zero-value FontSubstitution: %v", err)
	}
	defer doc.Close()
}

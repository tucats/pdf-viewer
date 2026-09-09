package pdfviewer_test

import (
	"context"
	"strings"
	"testing"

	pdfviewer "github.com/tucats/pdf-viewer"
)

// This file tests the optional Diagnostics mechanism (options.go's
// WithDiagnostics/Diagnostics, backed by internal/diag): opening or
// rendering a document with no Diagnostics attached must behave exactly
// as if the feature did not exist, and attaching one must surface at
// least the one case this project's own test corpus already has a
// fixture for - a font with no usable embedded outline data, falling
// back to notdefGlyph (see pdfviewer_text_test.go's
// TestRenderNotdefFallback, which exercises the same fixture's visual
// result without looking at diagnostics at all).

// TestRenderWithoutDiagnosticsOptionRecordsNothing confirms the default,
// no-option behavior: rendering a fixture that does trigger a
// diagnostic (see TestRenderNotdefFallbackIsRecordedAsADiagnostic below)
// without ever calling WithDiagnostics does not panic and does not
// require a caller to do anything differently - there is simply nothing
// to consult, matching this feature's "off by default" design.
func TestRenderWithoutDiagnosticsOptionRecordsNothing(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("text-notdef-fallback.pdf"))
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
	// Nothing further to check: with no Diagnostics attached, there is
	// no handle to ask "what happened" through at all, which is exactly
	// the point.
}

// TestRenderNotdefFallbackIsRecordedAsADiagnostic confirms that
// rendering the same fixture *with* a Diagnostics attached (via
// WithDiagnostics) records a message about the font's missing embedded
// outline data - the exact scenario the fixture exists to exercise (see
// pdfviewer_text_test.go's TestRenderNotdefFallback for the
// corresponding visual assertion).
func TestRenderNotdefFallbackIsRecordedAsADiagnostic(t *testing.T) {
	diags := &pdfviewer.Diagnostics{}
	doc, err := pdfviewer.OpenFile(fixturePath("text-notdef-fallback.pdf"), pdfviewer.WithDiagnostics(diags))
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

	messages := diags.Messages()
	if len(messages) == 0 {
		t.Fatal("Messages() is empty, want at least one diagnostic about the fixture's font having no usable embedded outline data")
	}
	found := false
	for _, msg := range messages {
		if strings.Contains(msg, "placeholder boxes") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Messages() = %v, want one mentioning the notdef placeholder-box fallback", messages)
	}
}

// TestDiagnosticsMessagesEmptyBeforeRender confirms Messages is safe to
// call on a freshly attached Diagnostics before anything has been
// rendered through it, returning an empty (not nil-panicking) slice.
func TestDiagnosticsMessagesEmptyBeforeRender(t *testing.T) {
	diags := &pdfviewer.Diagnostics{}
	if got := diags.Messages(); len(got) != 0 {
		t.Errorf("Messages() on a fresh Diagnostics = %v, want empty", got)
	}
}

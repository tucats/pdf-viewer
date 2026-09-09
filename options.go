package pdfviewer

import (
	"image/color"

	"github.com/tucats/pdf-viewer/internal/diag"
)

// This file declares the option types sketched in the README's "Draft
// Public API" section: OpenOption for Open and OpenFile, and
// RenderOptions/ThumbnailOptions for Page.Render and Page.Thumbnail.
//
// Open and OpenFile still have nothing to configure (see OpenOption
// below) - that extension point exists purely so this package's
// exported function *signatures* already match the shape described in
// the README's Draft Public API and will not need to change (only grow)
// once there is something to put in it, since adding a field to an
// existing options struct is backward compatible in Go, but adding a
// previously-absent parameter to an existing function is not.
//
// RenderOptions carries the minimal set of fields Render's implementation
// (see page.go) actually uses: Scale and Background. Per the README's
// Draft Public API notes, a fuller RenderOptions is expected to also
// cover an explicit pixel size, page-box selection beyond MediaBox
// (CropBox/BleedBox/TrimBox/ArtBox - Phase 2/3 per
// docs/capability-matrix.md), and color mode; those are not implemented
// yet and are left for a later change now that there is a real Render to
// extend. ThumbnailOptions (Phase 3) mirrors it with MaxDimension in
// place of Scale, matching the README's own description of Thumbnail as
// "a convenience for a bounded maximum dimension" sharing full
// rendering's page interpretation.

// OpenOption configures how Open or OpenFile parses a document. Most of
// this extension point remains unused for now (a future limit on how
// large a file the cross-reference recovery scan documented in
// internal/parser will attempt, for example, exposed for callers who
// want stricter or looser bounds than this module's current internal
// defaults) - WithDiagnostics, below, is its first real use.
type OpenOption func(*openConfig)

// openConfig holds the settings OpenOption values mutate.
type openConfig struct {
	diagnostics      *Diagnostics
	fontSubstitution *FontSubstitution
}

// WithDiagnostics attaches d to the Document being opened, so that
// afterward - as pages are rendered - anything this package tolerates
// rather than rejects (an unsupported font program, an unresolvable
// resource name, a malformed field it fell back on a default for, and
// so on) is recorded into d as one human-readable message, in the order
// each was produced, for a caller to inspect via d.Messages() once
// rendering finishes.
//
// Without this option (the default for every Document opened with no
// options, or with options that do not include it), none of that
// bookkeeping happens at all: this package behaves exactly as it always
// has, silently tolerating the same things it always tolerated, at
// whatever the cost of "nothing to record into" makes free (see
// internal/diag.Recorder's doc comment on why a nil Recorder is cheap).
// Diagnostics exists purely as an opt-in debugging aid - it is not a
// substitute for Render's own error return, which remains exactly as
// strict as it always was for content this package cannot tolerate at
// all (see Page.Render's doc comment on when it returns an error rather
// than continuing).
func WithDiagnostics(d *Diagnostics) OpenOption {
	return func(c *openConfig) { c.diagnostics = d }
}

// Diagnostics collects the optional messages WithDiagnostics enables -
// see that option's doc comment for what ends up in it and why nothing
// does by default. Its zero value is ready to use: create one with
// &Diagnostics{}, pass it to Open or OpenFile via WithDiagnostics, and
// call Messages after rendering to see what (if anything) was recorded.
//
// A single Diagnostics may be attached to only one Document at a time
// (attaching it to a second Document does not clear whatever the first
// already recorded into it, and both will go on appending to the same
// underlying collection) - the common case of one Diagnostics per
// Document, created alongside it, avoids ever needing to think about
// this.
type Diagnostics struct {
	recorder diag.Recorder
}

// Messages returns a copy of every diagnostic message recorded so far,
// in the order they were produced. It is safe to call at any time,
// including before any page has been rendered (in which case it returns
// an empty slice, not nil - see internal/diag.Recorder.Messages).
func (d *Diagnostics) Messages() []string {
	return d.recorder.Messages()
}

// WithFontSubstitution opts a Document into font substitution
// (docs/FONTS.md's Phase 4): when a PDF font has no usable embedded
// glyph program at all (no /FontFile2 or /FontFile3, or one that fails
// to parse - see internal/fonts' package doc comment for the full list
// of cases this covers), the Document will try to find a real substitute
// outline from a font file on disk, matched against the PDF font's own
// declared family/weight/style (via internal/fonts.Characterize),
// instead of always falling back to notdefGlyph's placeholder box.
//
// Substitution only ever happens for a font this package could not
// otherwise extract an outline from - a font with a usable embedded
// program is completely unaffected, matching this option is purely
// additive.
//
// Without this option (the default for every Document opened with no
// options, or with options that do not include it), this package
// behaves exactly as it always has: it never reads any directory or
// file outside of the PDF being opened itself. This matters because
// internal/fonts' own package doc comment documents a hard constraint -
// "this package must never assume a system font is available and must
// never silently invoke a platform font service" - and this option is
// exactly the explicit, opt-in mechanism that constraint's own wording
// ("never silently") anticipates: FontSubstitution never calls a
// platform font API (Core Text, DirectWrite, fontconfig, or similar);
// it only ever reads ordinary files from ordinary directories via
// os.ReadDir/os.ReadFile, the same standard-library-only approach this
// project already uses to read the PDF file itself. See
// docs/FONTS.md's "Scope decision" section for the full rationale this
// option's design follows.
func WithFontSubstitution(cfg FontSubstitution) OpenOption {
	return func(c *openConfig) { c.fontSubstitution = &cfg }
}

// FontSubstitution configures WithFontSubstitution.
type FontSubstitution struct {
	// Directories are explicit paths to scan for candidate font files
	// (".ttf", ".ttc", ".otf"), checked before any platform-default
	// directory (highest priority - a directory the caller specifically
	// asked for should always win over one this package merely guessed
	// at from the operating system). Each directory is scanned
	// non-recursively (its own files only, not any subdirectory's) -
	// pass every directory that should actually be searched if
	// candidate fonts are nested.
	Directories []string

	// DisableSystemDefaults, if true, turns off scanning this package's
	// own built-in, GOOS-gated list of common per-platform font
	// directories (see internal/fonts' DirectorySource for the exact
	// list, per operating system), leaving only Directories to search.
	//
	// The zero value (false) means platform defaults ARE scanned - this
	// field is deliberately phrased as an opt-*out* (rather than, say,
	// an "IncludeSystemDefaults" opt-in) so that a caller who writes
	// FontSubstitution{} - setting only Directories, or nothing at all -
	// still gets the generally useful "also look in the usual places"
	// behavior without needing to say so twice: Go gives every unset
	// bool field false, and false is what this option needs to mean
	// "scan the defaults" for that zero-value case to be the useful
	// default rather than a silently empty configuration. See
	// docs/FONTS.md's "Configuration" section for the full rationale.
	DisableSystemDefaults bool
}

// RenderOptions configures Page.Render.
type RenderOptions struct {
	// Scale is the number of device pixels per PDF point (1/72 inch).
	// The zero value means the default, 1.0 - a page's rendered pixel
	// dimensions then exactly match its MediaBox dimensions in points
	// (before accounting for Rotate; see Page.Render). A caller wanting
	// a specific DPI can compute Scale as dpi/72.
	Scale float64

	// Background is the solid color painted behind the page before any
	// content is drawn, visible wherever the page's own content does not
	// fully cover it - most pages do not paint their own full-page
	// background. A nil Background means the default, opaque white,
	// matching how most real-world PDF viewers render a page with no
	// explicit background. Only opacity implied by full coverage exists;
	// Background's own alpha channel (if any) is otherwise ignored - the
	// rendered image is always fully opaque, since this project does not
	// yet track a page's transparency beyond what is opaquely painted
	// (see docs/capability-matrix.md's transparency row).
	Background color.Color

	// HideAnnotations, when true, suppresses painting annotation
	// appearance streams (a page's /Annots - see internal/annotation)
	// that Render would otherwise paint on top of the page's own
	// content. The zero value (false) matches how most real-world PDF
	// viewers render a page by default: form field widgets, highlights,
	// stamps, and similar existing visual appearances are shown, not
	// just the page's own content stream. This only ever paints an
	// annotation's already-existing appearance exactly as recorded in
	// the file - it never generates one from a field's value or
	// otherwise interprets form/annotation *interactivity* (see the
	// README's non-goals).
	HideAnnotations bool
}

// ThumbnailOptions configures Page.Thumbnail.
type ThumbnailOptions struct {
	// MaxDimension bounds the longer of the thumbnail's two pixel
	// dimensions; the other dimension is scaled to preserve the page's
	// own aspect ratio (after accounting for /Rotate - see
	// pageImpl.thumbnailScale in page.go). The zero value means the
	// default, 256 pixels (see defaultThumbnailMaxDimension in page.go).
	MaxDimension int

	// Background is exactly RenderOptions.Background: the solid color
	// painted behind the page before any content is drawn. A nil
	// Background means the default, opaque white - see RenderOptions.
	// Background's doc comment for the full rationale, which applies
	// here unchanged.
	Background color.Color

	// HideAnnotations is exactly RenderOptions.HideAnnotations, applied
	// to a thumbnail's rendering the same way it applies to a full
	// render - see that field's doc comment.
	HideAnnotations bool
}

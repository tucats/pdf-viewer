// Command pdfpreview is a small, runnable example of embedding the
// pdf-viewer package for the simplest possible viewer feature: showing
// one page of a PDF as an image, the way a file browser's "preview
// pane" or a single-page viewer window would. See the repository
// README's Phase 6 entry ("Add examples for a page preview...") for why
// this program exists - it is meant to be read, not just run, so every
// step below is commented as if explaining Go itself to someone new to
// the language, not just explaining this package's API.
//
// # Usage
//
//	go run ./cmd/pdfpreview [-page N] [-scale S] [-diagnostics] [-substitute-fonts] -out preview.png input.pdf
//
// Example: render the first page of report.pdf at twice the default
// resolution to preview.png:
//
//	go run ./cmd/pdfpreview -scale 2 -out preview.png report.pdf
//
// Adding -diagnostics prints, to stdout, any messages pdfviewer.
// Diagnostics recorded while opening and rendering the page - one line
// per unsupported feature, unresolvable resource name, or malformed
// field this package tolerated rather than rejected outright (see
// pdfviewer.WithDiagnostics). This is meant as a debugging aid for
// exactly the situation that motivated it: a page that renders
// (Render itself returns no error) but looks wrong or unexpectedly
// blank, where the diagnostics buffer is the tool for finding out what,
// if anything, was silently skipped along the way.
//
// Adding -substitute-fonts opts the document into docs/FONTS.md's Phase
// 4 font substitution (pdfviewer.WithFontSubstitution, with its
// zero-value FontSubstitution - see that type's own doc comment for why
// the zero value already means "scan this machine's usual platform font
// directories"): a PDF font this package cannot extract a real embedded
// outline from (most commonly a non-embedded standard font like Arial or
// Times New Roman, expecting whatever renders the page to supply it) is
// matched against real font files found on disk instead of always
// falling back to a placeholder box. Combined with -diagnostics, this is
// the manual, not-committed-as-a-test sanity check docs/FONTS.md's Phase
// 4 "Status" section describes: confirm on a real machine that a
// previously-reported "font ... was not found, and was substituted for
// placeholder box characters" case now resolves to a real outline.
package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"

	pdfviewer "github.com/tucats/pdf-viewer"
)

// main is intentionally thin: it only parses command-line flags and
// reports success or failure. All of the actual work happens in
// renderPreviewToFile below, which takes plain Go values (strings, an
// int, a float64) as parameters instead of reading directly from
// os.Args or flag variables. Splitting the code this way means
// renderPreviewToFile can be called directly from a test (see
// main_test.go) without needing to spawn this program as a real
// subprocess and without needing to fake out command-line arguments -
// a pattern worth reusing any time you write a Go command-line tool.
func main() {
	// flag.Int, flag.Float64, and flag.String each register one
	// command-line flag and return a pointer to where its value will be
	// stored once flag.Parse (below) runs. The name in quotes is what a
	// caller types on the command line (e.g. "-page 2"); the next
	// argument is the default value used when that flag is not given at
	// all; the last argument is the help text shown by "-h".
	page := flag.Int("page", 0, "zero-based page index to render")
	scale := flag.Float64("scale", 1.0, "device pixels per PDF point (1.0 = 72 DPI; e.g. 2.0 for a sharper image)")
	out := flag.String("out", "preview.png", "path to write the rendered PNG to")
	diagnostics := flag.Bool("diagnostics", false, "print to stdout any diagnostic messages recorded while opening/rendering (unsupported features, missing resources, ...)")
	substituteFonts := flag.Bool("substitute-fonts", false, "find a real substitute outline (via this machine's installed fonts) for a font this package cannot extract an embedded outline from, instead of always falling back to a placeholder box")
	flag.Parse()

	// Everything flag.Parse did not recognize as a "-flag value" pair is
	// left over as a plain positional argument, available through
	// flag.Args() - here, that is expected to be exactly one thing: the
	// path to the PDF file to open. Go's slices are just a pointer,
	// length, and capacity under the hood, so checking len(args) != 1 is
	// the idiomatic way to say "I expect exactly one input file."
	args := flag.Args()
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "usage: pdfpreview [-page N] [-scale S] [-diagnostics] [-substitute-fonts] -out preview.png input.pdf\n")
		os.Exit(2)
	}

	if err := renderPreviewToFile(args[0], *page, *scale, *out, *diagnostics, *substituteFonts); err != nil {
		// Printing to os.Stderr (rather than os.Stdout) is the Go
		// convention for error output, so that a shell pipeline
		// redirecting stdout to a file still shows errors on the
		// terminal. os.Exit(1) reports failure to whatever invoked this
		// program (a shell, a Makefile, CI, ...) via the process's exit
		// code; by convention 0 means success and any nonzero value
		// means failure.
		fmt.Fprintf(os.Stderr, "pdfpreview: %v\n", err)
		os.Exit(1)
	}
}

// renderPreviewToFile opens inputPath as a PDF document, renders the
// page at the given zero-based index at the given scale (device pixels
// per PDF point - see pdfviewer.RenderOptions.Scale), and writes the
// result to outPath as a PNG file. It returns an error instead of
// exiting the process directly, which is what lets main_test.go call it
// directly and check the error value itself.
//
// When showDiagnostics is true, a pdfviewer.Diagnostics is attached to
// the document via pdfviewer.WithDiagnostics, and whatever messages it
// collects are printed to stdout - one per line - before this function
// returns, whether or not opening/rendering ultimately succeeded (a
// diagnostic recorded before a later, unrelated failure can still be
// useful context for debugging that failure).
//
// When substituteFonts is true, the document is opened with
// pdfviewer.WithFontSubstitution(pdfviewer.FontSubstitution{}) - the
// zero-value configuration, which (per that type's own doc comment)
// already means "scan this machine's usual platform font directories,"
// exactly what a command-line preview tool run interactively on a real
// machine wants, as opposed to a caller embedding this package in a
// server or other environment that should decide its own font
// directories explicitly.
func renderPreviewToFile(inputPath string, pageIndex int, scale float64, outPath string, showDiagnostics, substituteFonts bool) error {
	var opts []pdfviewer.OpenOption
	var diagnostics *pdfviewer.Diagnostics
	if showDiagnostics {
		diagnostics = &pdfviewer.Diagnostics{}
		opts = append(opts, pdfviewer.WithDiagnostics(diagnostics))
	}
	if substituteFonts {
		opts = append(opts, pdfviewer.WithFontSubstitution(pdfviewer.FontSubstitution{}))
	}

	// pdfviewer.OpenFile is the convenience entry point for reading
	// directly from a named file on disk - see the package's Draft
	// Public API notes (README.md) for why pdfviewer.Open (which accepts
	// any io.ReaderAt) is the more general one; a command-line tool
	// reading a named file is exactly OpenFile's intended use.
	doc, err := pdfviewer.OpenFile(inputPath, opts...)
	if err != nil {
		return fmt.Errorf("opening %s: %w", inputPath, err)
	}
	// "defer" schedules doc.Close() to run when renderPreviewToFile
	// returns, regardless of which return statement is hit or whether a
	// panic unwinds the stack - the idiomatic Go way to guarantee cleanup
	// code runs exactly once without repeating it before every return.
	defer doc.Close()

	if diagnostics != nil {
		// Deferred (rather than called at the bottom of this function)
		// so that these still print even when a later step returns an
		// error - see this function's own doc comment on why that
		// matters.
		defer printDiagnostics(diagnostics)
	}

	page, err := doc.Page(pageIndex)
	if err != nil {
		return fmt.Errorf("page %d of %s: %w", pageIndex, inputPath, err)
	}

	// context.Background() is Go's "no cancellation, no deadline" root
	// context - appropriate here since this program runs to completion
	// with nothing else that could ask it to stop early. A longer-lived
	// program (a server handling many requests, or a GUI viewer that
	// lets the user navigate away mid-render) would instead pass a
	// context tied to the request or the user's navigation, so that
	// Render can stop promptly - see pdfviewer.Page.Render's own doc
	// comment on the cancellation checkpoints it honors.
	img, err := page.Render(context.Background(), pdfviewer.RenderOptions{Scale: scale})
	if err != nil {
		return fmt.Errorf("rendering page %d of %s: %w", pageIndex, inputPath, err)
	}

	return writePNG(outPath, img)
}

// printDiagnostics writes each message d has collected so far to
// stdout, one per line - stdout, rather than stderr, since (unlike an
// actual error) these are not this program failing; they are exactly
// the same kind of informational output as the PNG this program
// otherwise writes, just routed to the terminal instead of a file.
// Printing nothing when there is nothing to print (a document that
// triggered no diagnostics at all - the common case) keeps
// -diagnostics quiet on an unremarkable file.
func printDiagnostics(d *pdfviewer.Diagnostics) {
	for _, msg := range d.Messages() {
		fmt.Println(msg)
	}
}

// writePNG encodes img as a PNG file at path, using only the standard
// library's image/png package - exactly what the README's Phase 6 entry
// means by "standard-library image encoders": pdfviewer.Page.Render
// returns a plain image.Image (specifically an *image.RGBA - see
// internal/raster.Render), so any encoder from Go's standard library or
// the wider ecosystem works without pdfviewer needing to know about
// image file formats at all.
func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	// Track the first error from either Encode or Close, but always
	// attempt the Close - a failed Close (for example, a disk-full
	// condition on a buffered filesystem) can be the only sign the
	// written file is actually incomplete, so it must not be silently
	// swallowed just because Encode itself reported success.
	encErr := png.Encode(f, img)
	closeErr := f.Close()
	if encErr != nil {
		return fmt.Errorf("encoding PNG to %s: %w", path, encErr)
	}
	if closeErr != nil {
		return fmt.Errorf("closing %s: %w", path, closeErr)
	}
	return nil
}

// Command pdfexport is a small, runnable example of embedding the
// pdf-viewer package for a third common viewer feature: exporting every
// page of a document as a full-resolution raster image file, using only
// the standard library's own image encoders - see the repository
// README's Phase 6 entry ("full-page export using standard-library image
// encoders") for why this program exists. Unlike cmd/pdfthumbnails, this
// program renders sequentially, one page at a time, from a single
// *pdfviewer.Document - a perfectly good, simpler choice when (as here)
// there is no need for the extra complexity of a worker pool; see that
// command's own doc comment for the concurrent alternative and when it
// is worth reaching for instead.
//
// # Usage
//
//	go run ./cmd/pdfexport [-scale S] [-format png|jpeg] -out outdir input.pdf
//
// Each page is written to outdir as page-0000.<ext>, page-0001.<ext>,
// and so on, where <ext> matches -format.
package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"

	pdfviewer "github.com/tucats/pdf-viewer"
)

func main() {
	scale := flag.Float64("scale", 1.0, "device pixels per PDF point (1.0 = 72 DPI)")
	format := flag.String("format", "png", `output image format: "png" or "jpeg"`)
	out := flag.String("out", "", "directory to write page-NNNN.<ext> files into (required)")
	flag.Parse()

	args := flag.Args()
	if len(args) != 1 || *out == "" {
		fmt.Fprintf(os.Stderr, "usage: pdfexport [-scale S] [-format png|jpeg] -out outdir input.pdf\n")
		os.Exit(2)
	}

	if err := exportPages(args[0], *out, *format, *scale); err != nil {
		fmt.Fprintf(os.Stderr, "pdfexport: %v\n", err)
		os.Exit(1)
	}
}

// encodeFunc is the shape both image/png's Encode and image/jpeg's
// Encode already have in common (aside from image/jpeg's extra options
// argument, absorbed by the small wrapper closure in encoderFor below,
// and png.Encode's io.Writer parameter type, which *os.File already
// satisfies) - declaring it as a named type lets exportPages call
// whichever encoder was chosen without needing an if/else (or switch) at
// the point where it is actually used, which is the point of gathering
// the choice into one small helper (encoderFor) instead.
type encodeFunc func(w io.Writer, img image.Image) error

// encoderFor returns the encodeFunc and file extension for the named
// output format ("png" or "jpeg"), or an error for anything else. This
// is the only place in this program that knows about specific image
// encoders - exportPages itself just calls the function it gets back,
// which is what makes it easy to add a third format later (for example,
// image/gif's Encode has this same shape) without touching
// exportPages's own logic at all.
func encoderFor(format string) (encode encodeFunc, ext string, err error) {
	switch format {
	case "png":
		return png.Encode, "png", nil
	case "jpeg", "jpg":
		// image/jpeg.Encode takes a third argument (quality options,
		// which may be nil for the library's default); wrapping it in a
		// closure here is what makes it fit the plain encodeFunc shape
		// png.Encode already has.
		return func(w io.Writer, img image.Image) error {
			return jpeg.Encode(w, img, nil)
		}, "jpg", nil
	default:
		return nil, "", fmt.Errorf("unknown -format %q (want \"png\" or \"jpeg\")", format)
	}
}

// exportPages renders every page of inputPath at the given scale
// (device pixels per PDF point - see pdfviewer.RenderOptions.Scale) and
// writes each one to outDir as page-NNNN.<ext>, encoded with the named
// format ("png" or "jpeg").
func exportPages(inputPath, outDir, format string, scale float64) error {
	encode, ext, err := encoderFor(format)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating output directory %s: %w", outDir, err)
	}

	doc, err := pdfviewer.OpenFile(inputPath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", inputPath, err)
	}
	defer doc.Close()

	for i := 0; i < doc.PageCount(); i++ {
		if err := exportOnePage(doc, i, outDir, ext, scale, encode); err != nil {
			return err
		}
	}
	return nil
}

func exportOnePage(doc *pdfviewer.Document, pageIndex int, outDir, ext string, scale float64, encode encodeFunc) error {
	page, err := doc.Page(pageIndex)
	if err != nil {
		return fmt.Errorf("page %d: %w", pageIndex, err)
	}

	img, err := page.Render(context.Background(), pdfviewer.RenderOptions{Scale: scale})
	if err != nil {
		return fmt.Errorf("rendering page %d: %w", pageIndex, err)
	}

	outPath := filepath.Join(outDir, fmt.Sprintf("page-%04d.%s", pageIndex, ext))
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("creating %s: %w", outPath, err)
	}
	encErr := encode(f, img)
	closeErr := f.Close()
	if encErr != nil {
		return fmt.Errorf("encoding page %d to %s: %w", pageIndex, outPath, encErr)
	}
	if closeErr != nil {
		return fmt.Errorf("closing %s: %w", outPath, closeErr)
	}
	return nil
}

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestGeneratedFixturesMatchCheckedInFiles regenerates every fixture in
// memory (by calling the same buildXxx functions main() calls) and
// compares the result byte-for-byte against what is actually checked
// into testdata/fixtures/handmade.
//
// Why this test exists: the whole point of generating fixtures with Go
// code (see the package doc comment in main.go) instead of hand-editing
// PDF bytes is that the checked-in files are supposed to always be
// exactly what the generator produces. Without this test, someone could
// change a buildXxx function without re-running `go run ./tools/genfixtures`,
// and the checked-in .pdf files would silently drift out of sync with
// the source that supposedly produces them - defeating the entire
// reproducibility point. This test fails loudly in that situation and
// tells the developer to re-run the generator.
func TestGeneratedFixturesMatchCheckedInFiles(t *testing.T) {
	fixtures := map[string][]byte{
		"minimal-blank-page.pdf":             buildMinimalBlankPage(),
		"two-pages.pdf":                      buildTwoPages(),
		"incremental-update.pdf":             buildIncrementalUpdate(),
		"malformed-bad-xref-offset.pdf":      buildMalformedBadXrefOffset(),
		"truncated.pdf":                      buildTruncated(),
		"encrypted.pdf":                      buildEncrypted(),
		"xref-stream.pdf":                    buildXrefStream(),
		"object-stream.pdf":                  buildObjectStream(),
		"filled-rect.pdf":                    buildFilledRect(),
		"stroked-line.pdf":                   buildStrokedLine(),
		"stroke-joins.pdf":                   buildStrokeJoins(),
		"clipped-rect.pdf":                   buildClippedRect(),
		"transformed-rect.pdf":               buildTransformedRect(),
		"flate-content-rect.pdf":             buildFlateContentRect(),
		"image-rgb.pdf":                      buildImageRGB(),
		"image-mask.pdf":                     buildImageMask(),
		"image-smask.pdf":                    buildImageSMask(),
		"image-jpeg.pdf":                     buildImageJPEG(),
		"image-jbig2.pdf":                    buildImageJBIG2(),
		"image-jbig2-text.pdf":               buildImageJBIG2Text(),
		"image-jpx.pdf":                      buildImageJPX(),
		"image-jpx-rgb.pdf":                  buildImageJPXRGB(),
		"inline-image.pdf":                   buildInlineImage(),
		"rotated-page.pdf":                   buildRotatedPage(),
		"page-boxes.pdf":                     buildPageBoxes(),
		"text-simple-truetype.pdf":           buildTextSimpleTrueType(),
		"text-simple-type1.pdf":              buildTextSimpleType1(),
		"text-scaled.pdf":                    buildTextScaled(),
		"text-type0-identity.pdf":            buildTextType0Identity(),
		"text-type0-embedded-cmap.pdf":       buildTextType0EmbeddedCMap(),
		"text-type0-predefined-encoding.pdf": buildTextType0PredefinedEncoding(),
		"text-notdef-fallback.pdf":           buildTextNotdefFallback(),
		"text-rotated-page.pdf":              buildTextRotatedPage(),
		"text-tounicode-simple.pdf":          buildTextToUnicodeSimple(),
		"text-tounicode-type0.pdf":           buildTextToUnicodeType0(),
		"separation-fill.pdf":                buildSeparationFill(),
		"lab-fill.pdf":                       buildLabFill(),
		"axial-shading.pdf":                  buildAxialShading(),
		"radial-shading.pdf":                 buildRadialShading(),
		"shading-pattern-fill.pdf":           buildShadingPatternFill(),
		"function-based-shading.pdf":         buildFunctionBasedShading(),
		"mesh-shading-type4.pdf":             buildFreeFormTriangleMeshShading(),
		"mesh-shading-type5.pdf":             buildLatticeFormTriangleMeshShading(),
		"mesh-shading-type6.pdf":             buildCoonsPatchMeshShading(),
		"mesh-shading-type7.pdf":             buildTensorProductPatchMeshShading(),
		"form-xobject.pdf":                   buildFormXObject(),
		"annotation-appearance.pdf":          buildAnnotationAppearance(),
		"annotation-hidden.pdf":              buildAnnotationHidden(),
		"alpha-fill.pdf":                     buildAlphaFill(),
		"blend-multiply.pdf":                 buildBlendMultiply(),
		"tiling-pattern-fill.pdf":            buildTilingPatternFill(),
		"softmask-luminosity.pdf":            buildSoftMaskLuminosity(),
		"softmask-alpha.pdf":                 buildSoftMaskAlpha(),
		"encrypted-rc4-40bit.pdf":            buildEncryptedRC4_40bit(),
		"encrypted-aes128.pdf":               buildEncryptedAES128(),
		"encrypted-aes256.pdf":               buildEncryptedAES256(),
		"encrypted-password-aes128.pdf":      buildEncryptedPasswordAES128(),
		"encrypted-password-aes256.pdf":      buildEncryptedPasswordAES256(),
		"form-filled-no-appearance.pdf":      buildFormFilledNoAppearance(),
		"pdf20-classic-xref.pdf":             buildPDF20ClassicXref(),
		"pdf20-xref-stream.pdf":              buildPDF20XrefStream(),
		"pdf20-encrypted-aes256.pdf":         buildPDF20EncryptedAES256(),
	}

	for name, want := range fixtures {
		name, want := name, want
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(outputDir, name)
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading checked-in fixture %s: %v (did you forget to run `go run ./tools/genfixtures`?)", path, err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("checked-in %s does not match what the generator currently produces; run `go run ./tools/genfixtures` and commit the result", path)
			}
		})
	}
}

// TestFixtureSetIsComplete makes sure every file actually present in
// testdata/fixtures/handmade is one this test (and therefore main.go's
// fixtures list) knows about, catching the opposite drift: a fixture
// file added or renamed without updating main.go and this test to match.
func TestFixtureSetIsComplete(t *testing.T) {
	known := map[string]bool{
		"minimal-blank-page.pdf":             true,
		"two-pages.pdf":                      true,
		"incremental-update.pdf":             true,
		"malformed-bad-xref-offset.pdf":      true,
		"truncated.pdf":                      true,
		"encrypted.pdf":                      true,
		"xref-stream.pdf":                    true,
		"object-stream.pdf":                  true,
		"filled-rect.pdf":                    true,
		"stroked-line.pdf":                   true,
		"stroke-joins.pdf":                   true,
		"clipped-rect.pdf":                   true,
		"transformed-rect.pdf":               true,
		"flate-content-rect.pdf":             true,
		"image-rgb.pdf":                      true,
		"image-mask.pdf":                     true,
		"image-smask.pdf":                    true,
		"image-jpeg.pdf":                     true,
		"image-jbig2.pdf":                    true,
		"image-jbig2-text.pdf":               true,
		"image-jpx.pdf":                      true,
		"image-jpx-rgb.pdf":                  true,
		"inline-image.pdf":                   true,
		"rotated-page.pdf":                   true,
		"page-boxes.pdf":                     true,
		"text-simple-truetype.pdf":           true,
		"text-simple-type1.pdf":              true,
		"text-scaled.pdf":                    true,
		"text-type0-identity.pdf":            true,
		"text-type0-embedded-cmap.pdf":       true,
		"text-type0-predefined-encoding.pdf": true,
		"text-notdef-fallback.pdf":           true,
		"text-rotated-page.pdf":              true,
		"text-tounicode-simple.pdf":          true,
		"text-tounicode-type0.pdf":           true,
		"separation-fill.pdf":                true,
		"lab-fill.pdf":                       true,
		"axial-shading.pdf":                  true,
		"radial-shading.pdf":                 true,
		"shading-pattern-fill.pdf":           true,
		"function-based-shading.pdf":         true,
		"mesh-shading-type4.pdf":             true,
		"mesh-shading-type5.pdf":             true,
		"mesh-shading-type6.pdf":             true,
		"mesh-shading-type7.pdf":             true,
		"form-xobject.pdf":                   true,
		"annotation-appearance.pdf":          true,
		"annotation-hidden.pdf":              true,
		"alpha-fill.pdf":                     true,
		"blend-multiply.pdf":                 true,
		"tiling-pattern-fill.pdf":            true,
		"softmask-luminosity.pdf":            true,
		"softmask-alpha.pdf":                 true,
		"encrypted-rc4-40bit.pdf":            true,
		"encrypted-aes128.pdf":               true,
		"encrypted-aes256.pdf":               true,
		"encrypted-password-aes128.pdf":      true,
		"encrypted-password-aes256.pdf":      true,
		"form-filled-no-appearance.pdf":      true,
		"pdf20-classic-xref.pdf":             true,
		"pdf20-xref-stream.pdf":              true,
		"pdf20-encrypted-aes256.pdf":         true,
	}

	entries, err := os.ReadDir(outputDir)
	if err != nil {
		t.Fatalf("reading %s: %v", outputDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".pdf" {
			continue
		}
		if !known[e.Name()] {
			t.Errorf("found fixture file %s with no corresponding entry in this test (and likely none in main.go's fixtures list)", e.Name())
		}
	}
}

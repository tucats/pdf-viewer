// Command genfixtures writes the hand-authored PDF files used as test
// fixtures under testdata/fixtures/handmade.
//
// # Why a generator instead of PDFs checked in from elsewhere
//
// The project's Phase 0 exit criteria (see the "Phased Plan" section of
// the repository README) call for "small hand-authored PDFs for each
// feature under test, plus permitted real-world fixtures with recorded
// licenses and provenance." A hand-authored PDF still needs byte-exact
// cross-reference offsets to be a *valid* PDF (the xref table records
// exactly where in the file each object starts), and computing those
// offsets by hand is tedious and error-prone. This program builds each
// fixture's bytes programmatically — tracking the offset of every object
// as it is written — so the offsets are always correct by construction,
// and so the fixture corpus is reproducible: anyone can regenerate the
// exact same files by running `go run ./tools/genfixtures`.
//
// The *content* of every fixture is still authored by this project
// (there is no PDF content copied from anywhere else), which is what
// "hand-authored" refers to in the README — the generation is just a
// mechanical way of assembling and byte-counting that content correctly.
//
// # How to run it
//
//	go run ./tools/genfixtures
//
// This overwrites every file in testdata/fixtures/handmade. Run it again
// after editing this file whenever a fixture's content needs to change;
// do not hand-edit the generated .pdf files, since any edit will
// desynchronize their xref offsets from their actual byte layout and
// produce a fixture that is invalid for reasons unrelated to what it is
// supposed to be testing.
package main

import (
	"bytes"
	"compress/zlib"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tucats/pdf-viewer/internal/crypt"
	"github.com/tucats/pdf-viewer/internal/filter"
)

// outputDir is where every generated fixture is written: the
// testdata/fixtures/handmade directory in the repository, computed as an
// absolute path from this source file's own location rather than from
// the process's current working directory. That matters because this
// package is entered two different ways that have two different working
// directories - `go run ./tools/genfixtures` runs with whatever
// directory the developer happened to be in (typically the repository
// root), while `go test` always runs with the package's own directory
// (tools/genfixtures) as the working directory - and outputDir needs to
// resolve to the same place either way.
//
// runtime.Caller(0) returns the absolute path of *this* source file as
// recorded by the compiler, which is stable regardless of the process's
// working directory; walking up two directories from
// tools/genfixtures/main.go reaches the repository root.
var outputDir = func() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("genfixtures: runtime.Caller failed to report this file's own path")
	}
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile))) // tools/genfixtures -> tools -> repo root
	return filepath.Join(repoRoot, "testdata", "fixtures", "handmade")
}()

func main() {
	fixtures := []struct {
		name string
		data []byte
	}{
		{"minimal-blank-page.pdf", buildMinimalBlankPage()},
		{"two-pages.pdf", buildTwoPages()},
		{"incremental-update.pdf", buildIncrementalUpdate()},
		{"malformed-bad-xref-offset.pdf", buildMalformedBadXrefOffset()},
		{"truncated.pdf", buildTruncated()},
		{"encrypted.pdf", buildEncrypted()},
		{"xref-stream.pdf", buildXrefStream()},
		{"object-stream.pdf", buildObjectStream()},
		{"filled-rect.pdf", buildFilledRect()},
		{"stroked-line.pdf", buildStrokedLine()},
		{"stroke-joins.pdf", buildStrokeJoins()},
		{"clipped-rect.pdf", buildClippedRect()},
		{"transformed-rect.pdf", buildTransformedRect()},
		{"flate-content-rect.pdf", buildFlateContentRect()},
		{"image-rgb.pdf", buildImageRGB()},
		{"image-mask.pdf", buildImageMask()},
		{"image-smask.pdf", buildImageSMask()},
		{"image-jpeg.pdf", buildImageJPEG()},
		{"image-jbig2.pdf", buildImageJBIG2()},
		{"image-jbig2-text.pdf", buildImageJBIG2Text()},
		{"inline-image.pdf", buildInlineImage()},
		{"rotated-page.pdf", buildRotatedPage()},
		{"page-boxes.pdf", buildPageBoxes()},
		{"text-simple-truetype.pdf", buildTextSimpleTrueType()},
		{"text-simple-type1.pdf", buildTextSimpleType1()},
		{"text-scaled.pdf", buildTextScaled()},
		{"text-type0-identity.pdf", buildTextType0Identity()},
		{"text-type0-embedded-cmap.pdf", buildTextType0EmbeddedCMap()},
		{"text-type0-predefined-encoding.pdf", buildTextType0PredefinedEncoding()},
		{"text-notdef-fallback.pdf", buildTextNotdefFallback()},
		{"text-rotated-page.pdf", buildTextRotatedPage()},
		{"text-tounicode-simple.pdf", buildTextToUnicodeSimple()},
		{"text-tounicode-type0.pdf", buildTextToUnicodeType0()},
		{"separation-fill.pdf", buildSeparationFill()},
		{"lab-fill.pdf", buildLabFill()},
		{"axial-shading.pdf", buildAxialShading()},
		{"radial-shading.pdf", buildRadialShading()},
		{"shading-pattern-fill.pdf", buildShadingPatternFill()},
		{"function-based-shading.pdf", buildFunctionBasedShading()},
		{"mesh-shading-type4.pdf", buildFreeFormTriangleMeshShading()},
		{"mesh-shading-type5.pdf", buildLatticeFormTriangleMeshShading()},
		{"mesh-shading-type6.pdf", buildCoonsPatchMeshShading()},
		{"mesh-shading-type7.pdf", buildTensorProductPatchMeshShading()},
		{"form-xobject.pdf", buildFormXObject()},
		{"annotation-appearance.pdf", buildAnnotationAppearance()},
		{"annotation-hidden.pdf", buildAnnotationHidden()},
		{"alpha-fill.pdf", buildAlphaFill()},
		{"blend-multiply.pdf", buildBlendMultiply()},
		{"tiling-pattern-fill.pdf", buildTilingPatternFill()},
		{"encrypted-rc4-40bit.pdf", buildEncryptedRC4_40bit()},
		{"encrypted-aes128.pdf", buildEncryptedAES128()},
		{"encrypted-aes256.pdf", buildEncryptedAES256()},
		{"encrypted-password-aes128.pdf", buildEncryptedPasswordAES128()},
		{"encrypted-password-aes256.pdf", buildEncryptedPasswordAES256()},
		{"form-filled-no-appearance.pdf", buildFormFilledNoAppearance()},
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "genfixtures: %v\n", err)
		os.Exit(1)
	}

	for _, f := range fixtures {
		path := filepath.Join(outputDir, f.name)
		if err := os.WriteFile(path, f.data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "genfixtures: writing %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s (%d bytes)\n", path, len(f.data))
	}
}

// --- Fixture bodies -------------------------------------------------
//
// Each buildXxx function below describes, in comments, exactly what PDF
// structural feature the fixture exists to exercise. Keep that pattern
// for any fixture added later: a fixture whose purpose isn't written
// down decays into "some PDF file" that nobody dares delete or change.

// buildMinimalBlankPage returns the simplest possible valid PDF this
// project should be able to open: one page, no content, a classic
// (non-compressed) cross-reference table, and a single trailer. This is
// the baseline "does the parser work at all" fixture.
func buildMinimalBlankPage() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>", nil)
	b.addObject(4, 0, "<< /Length 0 >>", []byte{})
	return b.finish(1)
}

// buildTwoPages returns a valid PDF whose page tree has two page objects
// under one Pages node, each with distinct MediaBox dimensions so a test
// can assert that page attributes are read per-page rather than
// accidentally shared or swapped. This exercises Kids-array traversal
// and the Count field, both required before Document.PageCount and
// Document.Page(index) can be trusted.
func buildTwoPages() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 200] /Resources << >> /Contents 5 0 R >>", nil)
	b.addObject(4, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 400] /Resources << >> /Contents 6 0 R >>", nil)
	b.addObject(5, 0, "<< /Length 0 >>", []byte{})
	b.addObject(6, 0, "<< /Length 0 >>", []byte{})
	return b.finish(1)
}

// buildIncrementalUpdate returns a PDF that has been "incrementally
// saved" once: the original file (a single blank page, objects 1-4) is
// followed by an appended update that adds a second page (objects 5-6)
// and rewrites the Pages object (object 2) to reference both pages,
// closing with a *second* trailer whose /Prev entry points back at the
// first trailer's xref table. Real-world PDF editors produce files like
// this constantly (every incremental save appends rather than rewriting
// the file), so a parser that only ever reads the last trailer while
// ignoring /Prev will see a Pages object listing a page whose object
// definition it never read. This fixture is the regression test for
// that: a correct reader must end up with two pages, both renderable,
// with object 2's *final* (updated) definition winning over its
// original one.
func buildIncrementalUpdate() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>", nil)
	b.addObject(4, 0, "<< /Length 0 >>", []byte{})
	firstXrefOffset := b.writeXrefAndTrailer(1, "")

	// The appended update reuses object number 2 (superseding the
	// original Pages object) and introduces new object numbers 5 and 6
	// for the second page and its content stream. Only the *changed and
	// new* objects need to appear in this update; object 1, 3, and 4 are
	// untouched and are inherited from the previous revision via /Prev.
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R 5 0 R] /Count 2 >>", nil)
	b.addObject(5, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 250 350] /Resources << >> /Contents 6 0 R >>", nil)
	b.addObject(6, 0, "<< /Length 0 >>", []byte{})
	b.writeXrefAndTrailer(1, fmt.Sprintf(" /Prev %d", firstXrefOffset))

	return b.buf.Bytes()
}

// buildMalformedBadXrefOffset returns a file that is byte-for-byte
// identical to buildMinimalBlankPage, except the xref entry for object 3
// (the Page object) has been deliberately corrupted to point at the
// wrong offset. This exercises the error-classification requirement from
// Phase 1's exit criteria: a reader must report this as ErrMalformed
// (the file's own bookkeeping is inconsistent) rather than panicking or
// silently returning wrong data, and ideally should still be able to
// recover by falling back to a linear scan for "N G obj" markers, which
// real-world PDF readers commonly do for exactly this situation.
func buildMalformedBadXrefOffset() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>", nil)
	b.addObject(4, 0, "<< /Length 0 >>", []byte{})

	// Corrupt the recorded offset for object 3 by adding a bogus amount
	// so it no longer points at "3 0 obj". The offset is still a
	// plausible-looking in-range number, which is the more interesting
	// (and more realistic) failure mode compared to an offset that is
	// obviously out of bounds.
	b.offsets[3] += 12345

	return b.finish(1)
}

// buildTruncated returns a minimal-blank-page PDF that has been cut off
// partway through the final content stream object, before its endstream
// / endobj / xref / trailer machinery. This simulates a partial download
// or a copy that was interrupted mid-write. A reader must report this as
// ErrMalformed rather than panicking or blocking forever waiting for
// bytes that will never arrive.
// buildEncrypted returns a file that is structurally identical to
// buildMinimalBlankPage, except its trailer declares an /Encrypt entry
// pointing at a (fake, never actually used to decrypt anything) Standard
// security handler dictionary. This exercises Phase 6's "password
// handling" decision: internal/parser.Open must reject any file whose
// trailer names an /Encrypt dictionary at open time, with a clear error
// wrapping pdfviewer.ErrEncrypted, rather than only failing later and
// confusingly (as a garbled filter or syntax error) the first time some
// still-encrypted stream or string is actually read. The /Encrypt
// dictionary's contents are deliberately not a byte-accurate Standard
// security handler encoding (real /O and /U entries are 32-byte binary
// hashes, not readable placeholder text) - this fixture only needs the
// *trailer* to name an /Encrypt entry, since that is all Open inspects
// before attempting (and, because /O and /U are fake, failing) to
// validate an empty password against it. Since Phase 7a (see
// docs/PLAN2.md) added real decryption, this fixture now exercises a
// *different* case than it originally did: not "encryption is entirely
// unimplemented", but "this document's /Encrypt dictionary does not
// validate under an empty password" - still, correctly, rejected with
// an error wrapping ErrEncrypted either way. See
// buildEncryptedRC4_40bit, buildEncryptedAES128, and
// buildEncryptedAES256 below for fixtures with byte-accurate,
// successfully-decryptable encryption.
func buildEncrypted() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>", nil)
	b.addObject(4, 0, "<< /Length 0 >>", []byte{})
	b.addObject(5, 0, "<< /Filter /Standard /V 1 /R 2 /O (placeholder-owner-hash) /U (placeholder-user-hash) /P -3904 >>", nil)
	b.writeXrefAndTrailer(1, " /Encrypt 5 0 R")
	return b.buf.Bytes()
}

// encryptedFixtureID is the fixed, 16-byte trailer /ID this package's
// real (non-placeholder) encrypted fixtures below all use as both
// elements of the /ID array - a real PDF-writing application derives
// this identifier from file contents and creation time, but this
// project's fixtures need fully reproducible bytes across
// regenerations (see the package doc comment), so a fixed, readable
// value is used instead. It doubles as the "first element of /ID" input
// the Standard Security Handler's key-derivation algorithms require -
// see internal/crypt's ComputeFileKey and ComputeUserHash.
var encryptedFixtureID = []byte("pdf-viewer-fixtr") // exactly 16 bytes

// hexString formats b as a PDF hexadecimal string literal, e.g.
// hexString([]byte("AB")) == "<4142>". The Standard Security Handler's
// /O, /U, /OE, and /UE dictionary entries (and this package's /ID
// entries) are arbitrary binary bytes that would need error-prone
// backslash-escaping to write as a literal-string "(...)" PDF token;
// hex notation sidesteps that entirely, at the cost of being twice as
// long, which does not matter for these small, fixed-size fields.
func hexString(b []byte) string {
	return "<" + hex.EncodeToString(b) + ">"
}

// buildEncryptedFilledRect returns a real, spec-correct Standard
// Security Handler-encrypted PDF - decryptable by internal/crypt
// (Phase 7a; see docs/PLAN2.md) using an empty user password - built
// around the same 100x100 filled-rectangle content buildFilledRect
// uses, so a test can decode this fixture and compare its rendered
// output against that unencrypted fixture's to confirm decryption
// recovers the exact original content, plus an /Info /Title string to
// exercise string (not just stream) decryption.
//
// v and r are the /Encrypt dictionary's /V and /R entries (see
// internal/crypt's package doc comment for what those mean); method is
// the cipher actually used for both streams and strings, and cfmName is
// the /CFM name that selects it in a revision-4 document's /CF
// dictionary (unused, and left empty, for revision 2-3 documents, which
// have no /CF concept at all - RC4 is simply the only option). password
// is the document's user password ("" for Phase 7a's original
// "permissions-only, opens freely" fixtures; a real password for Phase
// 7b's - see buildEncryptedPasswordAES128, below); it is used as-is,
// without internal/crypt's own PDFDocEncoding-approximating encoding
// step (see that package's encodePassword, unexported and therefore
// unavailable here), which is exact for any plain-ASCII password - the
// only kind this package's fixtures use.
func buildEncryptedFilledRect(v, r, keyLenBytes int, method crypt.Method, cfmName, password string) []byte {
	const (
		pageContentObj = 4
		infoObj        = 5
		encryptObj     = 6
	)
	title := []byte("Encrypted Fixture")
	content := []byte("1 0 0 rg\n10 10 80 80 re\nf\n") // identical to buildFilledRect's content
	iv := bytes.Repeat([]byte{0xA5}, 16)               // arbitrary, fixed - see EncryptForFixture's doc comment
	var p int32 = -3904                                // arbitrary permissions bitmask; this project does not enforce permissions on read, only decrypts (see internal/crypt's package doc comment)
	passwordBytes := []byte(password)                  // owner password stays empty regardless - see ComputeOwnerHash's doc comment

	o := crypt.ComputeOwnerHash(nil, passwordBytes, r, keyLenBytes)
	fileKey := crypt.ComputeFileKey(passwordBytes, o, p, encryptedFixtureID, r, keyLenBytes, true)
	u := crypt.ComputeUserHash(fileKey, r, encryptedFixtureID)

	encContent, err := crypt.EncryptForFixture(fileKey, method, pageContentObj, 0, iv, content)
	if err != nil {
		panic("genfixtures: encrypting fixture content: " + err.Error())
	}
	encTitle, err := crypt.EncryptForFixture(fileKey, method, infoObj, 0, iv, title)
	if err != nil {
		panic("genfixtures: encrypting fixture title: " + err.Error())
	}

	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Resources << >> /Contents 4 0 R >>", nil)
	b.addObject(pageContentObj, 0, fmt.Sprintf("<< /Length %d >>", len(encContent)), encContent)
	b.addObject(infoObj, 0, fmt.Sprintf("<< /Title %s >>", hexString(encTitle)), nil)

	encryptDict := fmt.Sprintf("<< /Filter /Standard /V %d /R %d /O %s /U %s /P %d /Length %d",
		v, r, hexString(o), hexString(u), p, keyLenBytes*8)
	if v == 4 {
		// Revision 4 names its cipher indirectly, through a crypt filter
		// dictionary (/CF) that /StmF and /StrF both point at - see
		// internal/crypt's resolveCryptFilters.
		encryptDict += fmt.Sprintf(" /CF << /StdCF << /CFM /%s /AuthEvent /DocOpen /Length %d >> >> /StmF /StdCF /StrF /StdCF",
			cfmName, keyLenBytes)
	}
	encryptDict += " >>"
	b.addObject(encryptObj, 0, encryptDict, nil)

	trailerExtra := fmt.Sprintf(" /Encrypt %d 0 R /ID [%s %s] /Info %d 0 R",
		encryptObj, hexString(encryptedFixtureID), hexString(encryptedFixtureID), infoObj)
	b.writeXrefAndTrailer(1, trailerExtra)
	return b.buf.Bytes()
}

// buildEncryptedRC4_40bit returns an encrypted fixture using the
// oldest, simplest configuration: /V 1 /R 2, 40-bit (5-byte) RC4 -
// revision 2 always implies RC4 with no /CF dictionary at all. Its user
// password is empty (Phase 7a's "opens freely" case).
func buildEncryptedRC4_40bit() []byte {
	return buildEncryptedFilledRect(1, 2, 5, crypt.MethodRC4, "", "")
}

// buildEncryptedAES128 returns an encrypted fixture using /V 4 /R 4,
// 128-bit AES (the "/AESV2" crypt filter method) - the common
// configuration for a document that opts into AES over RC4 while
// remaining on the "classic" (non-AES-256) key derivation algorithm.
// Its user password is empty (Phase 7a's "opens freely" case); see
// buildEncryptedPasswordAES128 for the Phase 7b (non-empty password)
// counterpart.
func buildEncryptedAES128() []byte {
	return buildEncryptedFilledRect(4, 4, 16, crypt.MethodAESV2, "AESV2", "")
}

// encryptedFixturePassword is the non-empty user password this
// package's Phase 7b fixtures (buildEncryptedPasswordAES128 and
// buildEncryptedPasswordAES256) use - a document that genuinely will
// not open without a caller supplying this exact string via the root
// package's WithPassword option (see pdfviewer_test.go and
// internal/parser/parser_test.go's Phase 7b tests, which use this same
// constant to open them).
const encryptedFixturePassword = "correct horse battery staple"

// buildEncryptedPasswordAES128 is buildEncryptedAES128's Phase 7b
// counterpart: byte-for-byte identical except its user password is
// encryptedFixturePassword, not empty - so unlike every other encrypted
// fixture in this file, opening it successfully requires
// pdfviewer.WithPassword(encryptedFixturePassword) (or the equivalent
// internal/parser.WithPassword).
func buildEncryptedPasswordAES128() []byte {
	return buildEncryptedFilledRect(4, 4, 16, crypt.MethodAESV2, "AESV2", encryptedFixturePassword)
}

// buildEncryptedAES256 returns an encrypted fixture using /V 5 /R 6,
// 256-bit AES (the "/AESV3" crypt filter method) - the newest Standard
// Security Handler revision, whose file encryption key is not derived
// from the password at all but independently chosen and merely wrapped
// (via /UE) using a password-derived key - see internal/crypt/hash56.go
// and ComputeAES256UserStrings' doc comments. Unlike
// buildEncryptedFilledRect's revision 2-4 fixtures, the file key here is
// simply an arbitrary fixed 32-byte value rather than one derived by
// ComputeFileKey, since revision 5-6 never derives it from anything. Its
// user password is empty (Phase 7a's "opens freely" case); see
// buildEncryptedPasswordAES256 for the Phase 7b (non-empty password)
// counterpart.
func buildEncryptedAES256() []byte {
	return buildEncryptedAES256Fixture("")
}

// buildEncryptedPasswordAES256 is buildEncryptedAES256's Phase 7b
// counterpart: byte-for-byte identical except its user password is
// encryptedFixturePassword, not empty - see
// buildEncryptedPasswordAES128's doc comment for the parallel revision
// 2-4 fixture.
func buildEncryptedPasswordAES256() []byte {
	return buildEncryptedAES256Fixture(encryptedFixturePassword)
}

// buildEncryptedAES256Fixture is the shared implementation behind
// buildEncryptedAES256 and buildEncryptedPasswordAES256 - see
// buildEncryptedAES256's doc comment for what it builds and why the
// file key is an arbitrary fixed value rather than one ComputeFileKey
// derives. password is used as-is (see buildEncryptedFilledRect's doc
// comment on why that is exact for the plain-ASCII passwords this
// package's fixtures use).
func buildEncryptedAES256Fixture(password string) []byte {
	const (
		pageContentObj = 4
		infoObj        = 5
		encryptObj     = 6
	)
	title := []byte("Encrypted Fixture")
	content := []byte("1 0 0 rg\n10 10 80 80 re\nf\n")
	iv := bytes.Repeat([]byte{0xA5}, 16)
	var p int32 = -3904
	passwordBytes := []byte(password)

	fileKey := bytes.Repeat([]byte{0x5A}, 32) // arbitrary, fixed 256-bit file key
	validationSalt := bytes.Repeat([]byte{0x11}, 8)
	keySalt := bytes.Repeat([]byte{0x22}, 8)

	const r = 6
	u, ue, err := crypt.ComputeAES256UserStrings(fileKey, r, passwordBytes, validationSalt, keySalt)
	if err != nil {
		panic("genfixtures: computing AES-256 /U and /UE: " + err.Error())
	}
	o, oe, err := crypt.ComputeAES256OwnerStrings(fileKey, r, u, validationSalt, keySalt)
	if err != nil {
		panic("genfixtures: computing AES-256 /O and /OE: " + err.Error())
	}

	encContent, err := crypt.EncryptForFixture(fileKey, crypt.MethodAESV3, pageContentObj, 0, iv, content)
	if err != nil {
		panic("genfixtures: encrypting fixture content: " + err.Error())
	}
	encTitle, err := crypt.EncryptForFixture(fileKey, crypt.MethodAESV3, infoObj, 0, iv, title)
	if err != nil {
		panic("genfixtures: encrypting fixture title: " + err.Error())
	}

	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Resources << >> /Contents 4 0 R >>", nil)
	b.addObject(pageContentObj, 0, fmt.Sprintf("<< /Length %d >>", len(encContent)), encContent)
	b.addObject(infoObj, 0, fmt.Sprintf("<< /Title %s >>", hexString(encTitle)), nil)

	encryptDict := fmt.Sprintf(
		"<< /Filter /Standard /V 5 /R 6 /O %s /U %s /OE %s /UE %s /P %d /Length 256 /CF << /StdCF << /CFM /AESV3 /AuthEvent /DocOpen /Length 32 >> >> /StmF /StdCF /StrF /StdCF >>",
		hexString(o), hexString(u), hexString(oe), hexString(ue), p)
	b.addObject(encryptObj, 0, encryptDict, nil)

	trailerExtra := fmt.Sprintf(" /Encrypt %d 0 R /ID [%s %s] /Info %d 0 R",
		encryptObj, hexString(encryptedFixtureID), hexString(encryptedFixtureID), infoObj)
	b.writeXrefAndTrailer(1, trailerExtra)
	return b.buf.Bytes()
}

func buildTruncated() []byte {
	full := buildMinimalBlankPage()

	// Cut the file in half. Because buildMinimalBlankPage's earlier
	// objects and the xref/trailer machinery all live in the back half
	// of the file, this reliably removes the xref table, trailer, and
	// startxref entirely - a reader cannot even locate where object
	// data starts, which is deliberately the harshest truncation case.
	return full[:len(full)/2]
}

// buildXrefStream returns a PDF whose page tree is structurally
// identical to buildMinimalBlankPage's, but whose cross-reference
// section is a PDF 1.5+ cross-reference stream (Flate-compressed, per
// how real producers almost always write one) rather than a classic
// "xref" table - the Phase 2 counterpart exercising
// internal/parser.Document.loadXrefStream. The chosen field widths,
// /W [1 4 2], are deliberately not the "natural" 4-byte-everything
// choice some implementations default to, so that a reader which
// silently assumed a fixed record layout instead of reading /W would
// fail on this fixture.
func buildXrefStream() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>", nil)
	b.addObject(4, 0, "<< /Length 0 >>", []byte{})

	const xrefObjNum = 5
	size := xrefObjNum + 1
	// The xref stream object's own "5 0 obj" is about to begin at the
	// buffer's current length; its own entry (below) describes exactly
	// that offset, which is how a real cross-reference stream always
	// includes itself - the table is written as part of the very object
	// it describes.
	xrefOffset := b.buf.Len()

	var raw bytes.Buffer
	writeXrefStreamRecord(&raw, 0, 0, 65535) // object 0: free list head
	for n := 1; n < xrefObjNum; n++ {
		writeXrefStreamRecord(&raw, 1, b.offsets[n], 0)
	}
	writeXrefStreamRecord(&raw, 1, xrefOffset, 0)

	compressed := deflate(raw.Bytes())
	dict := fmt.Sprintf("<< /Type /XRef /Size %d /W [1 4 2] /Root 1 0 R /Filter /FlateDecode /Length %d >>", size, len(compressed))
	b.addObject(xrefObjNum, 0, dict, compressed)

	fmt.Fprintf(&b.buf, "startxref\n%d\n%%%%EOF\n", xrefOffset)
	return b.buf.Bytes()
}

// buildObjectStream returns a PDF whose Pages and Page dictionaries
// (object numbers 2 and 3) are packed together inside a single PDF 1.5+
// object stream (object 5) instead of each having its own "N G obj ...
// endobj" definition, described via a cross-reference stream (object 6)
// whose entries for 2 and 3 are type-2 ("compressed") records - the
// Phase 2 counterpart exercising internal/parser's
// Document.loadObjectStream and resolveCompressed. The content stream
// (object 4) and the catalog (object 1) remain ordinary top-level
// objects: the PDF specification forbids storing a stream inside an
// object stream, and there is no reason to compress the catalog itself,
// so this fixture exercises a document that mixes both storage forms -
// exactly what real-world PDF 1.5+ producers do (a document's very first
// objects are often left uncompressed for a "fast web view" preview).
func buildObjectStream() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(4, 0, "<< /Length 0 >>", []byte{})

	packed := []struct {
		num  int
		dict string
	}{
		{2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"},
		{3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>"},
	}
	var header, body strings.Builder
	for _, p := range packed {
		fmt.Fprintf(&header, "%d %d ", p.num, body.Len())
		body.WriteString(p.dict)
		body.WriteString("\n")
	}
	first := header.Len()
	compressed := deflate([]byte(header.String() + body.String()))

	const objStmNum = 5
	objStmDict := fmt.Sprintf("<< /Type /ObjStm /N %d /First %d /Filter /FlateDecode /Length %d >>", len(packed), first, len(compressed))
	b.addObject(objStmNum, 0, objStmDict, compressed)

	const xrefObjNum = 6
	size := xrefObjNum + 1
	xrefOffset := b.buf.Len()

	var raw bytes.Buffer
	writeXrefStreamRecord(&raw, 0, 0, 65535)                // 0: free list head
	writeXrefStreamRecord(&raw, 1, b.offsets[1], 0)         // 1: Catalog
	writeXrefStreamRecord(&raw, 2, objStmNum, 0)            // 2: Pages, packed at index 0
	writeXrefStreamRecord(&raw, 2, objStmNum, 1)            // 3: Page, packed at index 1
	writeXrefStreamRecord(&raw, 1, b.offsets[4], 0)         // 4: content stream
	writeXrefStreamRecord(&raw, 1, b.offsets[objStmNum], 0) // 5: the object stream itself
	writeXrefStreamRecord(&raw, 1, xrefOffset, 0)           // 6: the xref stream itself

	compressedXref := deflate(raw.Bytes())
	xrefDict := fmt.Sprintf("<< /Type /XRef /Size %d /W [1 4 2] /Root 1 0 R /Filter /FlateDecode /Length %d >>", size, len(compressedXref))
	b.addObject(xrefObjNum, 0, xrefDict, compressedXref)

	fmt.Fprintf(&b.buf, "startxref\n%d\n%%%%EOF\n", xrefOffset)
	return b.buf.Bytes()
}

// writeXrefStreamRecord appends one fixed-width cross-reference stream
// record matching the /W [1 4 2] layout buildXrefStream and
// buildObjectStream both use: a 1-byte type, a 4-byte big-endian
// field2, and a 2-byte big-endian field3. See
// internal/parser/xrefstream.go's parseXrefStreamRecord for what each
// type/field2/field3 combination means.
func writeXrefStreamRecord(buf *bytes.Buffer, typ byte, field2, field3 int) {
	buf.WriteByte(typ)
	buf.WriteByte(byte(field2 >> 24))
	buf.WriteByte(byte(field2 >> 16))
	buf.WriteByte(byte(field2 >> 8))
	buf.WriteByte(byte(field2))
	buf.WriteByte(byte(field3 >> 8))
	buf.WriteByte(byte(field3))
}

// deflate zlib-compresses data (the wrapping PDF's FlateDecode filter
// expects - see internal/filter's decodeFlate), for use by any fixture
// whose stream declares /Filter /FlateDecode.
func deflate(data []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		panic(fmt.Sprintf("genfixtures: deflate: %v", err))
	}
	if err := w.Close(); err != nil {
		panic(fmt.Sprintf("genfixtures: deflate: %v", err))
	}
	return buf.Bytes()
}

// buildFilledRect returns a single 100x100-point page whose content
// stream fills an 80x80 red square with an 10-point margin on every
// side, using plain (unfiltered) content stream bytes. This is the
// baseline Phase 2 rendering fixture: solid color, axis-aligned path
// construction ("re"), and nonzero-winding fill ("f") with no transform
// beyond the page's own device mapping.
func buildFilledRect() []byte {
	return buildSinglePageContent(100, 100, "1 0 0 rg\n10 10 80 80 re\nf\n")
}

// buildStrokedLine returns a single 100x100-point page whose content
// stream strokes a diagonal blue line, corner to corner, with a 5-point
// line width - exercising path construction via "m"/"l" and the "S"
// stroke operator together with a non-default line width, distinct from
// buildFilledRect's fill-only content.
func buildStrokedLine() []byte {
	return buildSinglePageContent(100, 100, "0 0 1 RG\n5 w\n10 10 m\n90 90 l\nS\n")
}

// buildStrokeJoins returns a single 100x100-point page whose content
// stream strokes the same 120-degree, two-segment corner shape twice
// side by side, wide (16-point line width, so the join geometry is
// clearly visible at this page's resolution) - once with an explicit
// miter join ("0 j", the default per the specification, but set
// explicitly here for clarity) and once, 40 points to the right, with a
// bevel join ("2 j") - exercising Phase 15a's real join geometry
// (internal/graphics's addJoin) end to end: content-stream "j" operator
// parsing, State.LineJoin, and StrokeToFill together, not just the
// internal/graphics-level unit tests addJoin and miterTip already have.
//
// Both corners turn the same way (from heading in the +X direction to
// heading 120 degrees from that), the same angle
// internal/graphics/stroke_test.go's a120CornerPath uses, chosen there
// because it produces a clean, exactly-computable miter ratio of 2 half-
// widths (well within the default miterLimit of 10, so no /M operator is
// needed here) - see that file's TestStrokeToFillMiterJoinReachesComputedTip
// for the hand-derived geometry this fixture's own render test checks
// against.
func buildStrokeJoins() []byte {
	return buildSinglePageContent(100, 100,
		"0 0 1 RG\n16 w\n"+
			"0 j\n10 50 m\n30 50 l\n20 67.3205 l\nS\n"+
			"2 j\n50 50 m\n70 50 l\n60 67.3205 l\nS\n")
}

// buildClippedRect returns a single 100x100-point page whose content
// stream clips to a 40x40 square in the page's center ("re W n") and
// then fills the entire page green - only the clipped square should
// actually end up green in the rendered output, exercising clipping
// together with fill.
func buildClippedRect() []byte {
	return buildSinglePageContent(100, 100, "30 30 40 40 re\nW\nn\n0 1 0 rg\n0 0 100 100 re\nf\n")
}

// buildTransformedRect returns a single 100x100-point page whose content
// stream translates to the page center and rotates 45 degrees ("cm")
// before filling an axis-aligned (in its own, now-rotated, user space)
// orange square - exercising the current transformation matrix, saved
// and restored with "q"/"Q" around the temporary transform so it does
// not leak into anything painted afterward.
func buildTransformedRect() []byte {
	return buildSinglePageContent(100, 100,
		"q\n1 0 0 1 50 50 cm\n0.70710678 0.70710678 -0.70710678 0.70710678 0 0 cm\n"+
			"1 0.5 0 rg\n-20 -20 40 40 re\nf\nQ\n")
}

// buildFlateContentRect returns a single 100x100-point page identical in
// appearance to buildFilledRect (a red 80x80 square with a 10-point
// margin), but whose content stream is Flate-compressed - exercising
// filter decoding applied to a *page content* stream specifically,
// distinct from buildXrefStream/buildObjectStream's use of Flate for
// structural (cross-reference/object-stream) data. This is what actually
// closes the loop on Phase 2's "decode ... Flate ... streams as needed
// by the fixture corpus" exit criterion for the content-stream pipeline
// (internal/model.PageContentBytes -> internal/parser.DecodeStream ->
// internal/filter.Decode), not just the parser's own bootstrapping use
// of it.
func buildFlateContentRect() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Resources << >> /Contents 4 0 R >>", nil)

	content := deflate([]byte("1 0 0 rg\n10 10 80 80 re\nf\n"))
	dict := fmt.Sprintf("<< /Length %d /Filter /FlateDecode >>", len(content))
	b.addObject(4, 0, dict, content)
	return b.finish(1)
}

// buildImageRGB returns a single 100x100-point page whose content stream
// paints a referenced image XObject ("Do") across the entire page: a 2x2
// DeviceRGB image (red, green / blue, yellow, one solid color per
// quadrant), uncompressed. This is the baseline Phase 3 rendering
// fixture for referenced images - exercising /Resources /XObject
// lookup, image dictionary resolution, and DeviceRGB sample decoding -
// the image counterpart to buildFilledRect's role for Phase 2's vector
// fills.
func buildImageRGB() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /XObject << /Im0 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("q\n100 0 0 100 0 0 cm\n/Im0 Do\nQ\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	pixels := []byte{
		255, 0, 0, 0, 255, 0, // row 0: red, green
		0, 0, 255, 255, 255, 0, // row 1: blue, yellow
	}
	imgDict := fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 2 /Height 2 "+
		"/ColorSpace /DeviceRGB /BitsPerComponent 8 /Length %d >>", len(pixels))
	b.addObject(5, 0, imgDict, pixels)

	return b.finish(1)
}

// buildImageMask returns a single 100x100-point page that sets the fill
// color to red and then paints a referenced /ImageMask stencil image
// across the entire page: a 2x1, 1-bit-per-pixel mask whose left pixel
// is painted (sample 0) and whose right pixel is masked out (sample 1),
// per the default /Decode for image masks (see internal/image's package
// doc comment). The rendered result should be a red left half and a
// white (background, left untouched) right half - exercising
// /ImageMask's "paint using the current fill color" behavior end to end,
// including internal/content threading the graphics state's FillColor
// through to internal/image.Decode.
func buildImageMask() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /XObject << /Im0 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("1 0 0 rg\nq\n100 0 0 100 0 0 cm\n/Im0 Do\nQ\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	// Two 1-bit samples packed high-bit-first into a single byte:
	// 0 (paint), then 1 (mask out), then six padding bits - 0b01000000.
	maskData := []byte{0x40}
	maskDict := fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 2 /Height 1 "+
		"/ImageMask true /Length %d >>", len(maskData))
	b.addObject(5, 0, maskDict, maskData)

	return b.finish(1)
}

// buildImageSMask returns a single 100x100-point page that paints a 1x1
// solid red image XObject with an /SMask giving it 50% alpha, across the
// entire page - exercising resolving and decoding a *second* image
// object (the soft mask) referenced from within the first one's own
// dictionary, and internal/image's per-pixel alpha blending. Over this
// project's opaque white page background, the expected rendered color is
// (approximately) red blended at ~50% opacity onto white: full red
// channel (already 255 in both layers), green and blue roughly halved
// from white toward the mask's own gray value.
func buildImageSMask() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /XObject << /Im0 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("q\n100 0 0 100 0 0 cm\n/Im0 Do\nQ\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	smaskData := []byte{128}
	smaskDict := fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 1 /Height 1 "+
		"/ColorSpace /DeviceGray /BitsPerComponent 8 /Length %d >>", len(smaskData))
	b.addObject(6, 0, smaskDict, smaskData)

	imgData := []byte{255, 0, 0}
	imgDict := fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 1 /Height 1 "+
		"/ColorSpace /DeviceRGB /BitsPerComponent 8 /SMask 6 0 R /Length %d >>", len(imgData))
	b.addObject(5, 0, imgDict, imgData)

	return b.finish(1)
}

// buildImageJPEG returns a single 100x100-point page that paints a
// referenced image XObject encoded with DCTDecode (JPEG) - a small,
// solid dark-blue 4x4 image, generated at fixture-build time with the
// standard library's own image/jpeg encoder (quality 100, to keep lossy
// compression artifacts on a flat color small enough for an exact-match
// pixel test with a modest tolerance) rather than any externally sourced
// JPEG file, keeping this fixture's provenance identical to every other
// hand-authored one in this package (see FIXTURES.md). This exercises
// internal/filter's DCTDecode support end to end, through the real
// cross-reference/object-resolution pipeline rather than only
// internal/filter's own unit tests.
func buildImageJPEG() []byte {
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			src.Set(x, y, color.RGBA{R: 20, G: 40, B: 200, A: 255})
		}
	}
	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, src, &jpeg.Options{Quality: 100}); err != nil {
		panic(fmt.Sprintf("genfixtures: encoding test JPEG: %v", err))
	}
	jpegData := jpegBuf.Bytes()

	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /XObject << /Im0 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("q\n100 0 0 100 0 0 cm\n/Im0 Do\nQ\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	imgDict := fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 4 /Height 4 "+
		"/ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>", len(jpegData))
	b.addObject(5, 0, imgDict, jpegData)

	return b.finish(1)
}

// buildImageJBIG2 returns a single 100x100-point page that paints a
// referenced image XObject encoded with JBIG2Decode - the filter scanned
// black-and-white pages commonly use (see docs/PLAN2.md's Phase 8). The
// image is a 32x32 bilevel bitmap whose top-left quadrant is black and
// whose remaining three quadrants are white, inside a one-pixel black
// border.
//
// That shape is deliberately asymmetric in both axes: it renders
// differently from itself under a horizontal flip, a vertical flip, or an
// inversion of black and white, so a rendered-pixel test against it
// catches the three mistakes easiest to make in a bilevel image pipeline
// (JBIG2 defines 1 as black, the opposite of a 1-bit DeviceGray sample -
// see jbig2.go's packInverted). The border additionally puts foreground
// pixels along every edge, where the arithmetic coder's context template
// reads neighbors from outside the bitmap.
//
// The JBIG2 bytes are produced at fixture-build time by
// internal/filter's own encoder (see jbig2mq.go's mqEncoder doc comment
// for why that encoder exists), which keeps this fixture's provenance
// identical to every other hand-authored one here - nothing is sourced
// externally. Unlike buildImageJPEG, the output is fully determined by
// this project's own code rather than by a standard-library encoder, so
// it is reproducible in exactly the way the rest of this package is.
func buildImageJBIG2() []byte {
	const dim = 32
	pix := make([]byte, dim*dim)
	for y := 0; y < dim; y++ {
		for x := 0; x < dim; x++ {
			border := x == 0 || y == 0 || x == dim-1 || y == dim-1
			topLeftQuadrant := x < dim/2 && y < dim/2
			if border || topLeftQuadrant {
				pix[y*dim+x] = 1 // JBIG2 foreground, i.e. black.
			}
		}
	}
	jbig2Data := filter.EncodeJBIG2GenericRegion(dim, dim, pix, true)

	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /XObject << /Im0 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("q\n100 0 0 100 0 0 cm\n/Im0 Do\nQ\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	imgDict := fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d "+
		"/ColorSpace /DeviceGray /BitsPerComponent 1 /Filter /JBIG2Decode /Length %d >>",
		dim, dim, len(jbig2Data))
	b.addObject(5, 0, imgDict, jbig2Data)

	return b.finish(1)
}

// buildImageJBIG2Text returns a single 100x100-point page that paints a
// referenced image XObject encoded with JBIG2Decode in *symbol mode* -
// the form scan-to-PDF and OCR pipelines actually emit, and the one
// docs/PLAN2.md's Phase 8e added support for. Where buildImageJBIG2's
// fixture codes its pixels directly (a generic region), this one codes a
// symbol dictionary of two glyph shapes and a text region placing
// instances of them, with the dictionary living in a separate
// /JBIG2Globals stream exactly as a real producer's shared dictionary
// does.
//
// The image is a 40x40 bilevel bitmap holding three placements of two
// distinguishable shapes:
//
//   - a solid 16x16 square in the top-left quadrant, and
//   - a hollow 18x18 square (a 4-pixel border around a white centre) in
//     each of the top-right and bottom-left quadrants,
//
// leaving the bottom-right quadrant blank. The two shapes differ in both
// size and interior, so a rendering test can tell which symbol was drawn
// where: swapping the two symbol IDs, misplacing an instance, dropping
// the second height class, or inverting black and white each change a
// point this fixture's test asserts. The two shapes also have different
// heights, so the dictionary needs two height classes rather than one -
// the structure T.88's symbol coding is built around.
//
// As with buildImageJBIG2, the JBIG2 bytes come from internal/filter's
// own encoder at fixture-build time, so this fixture stays fully
// reproducible from this project's own code.
func buildImageJBIG2Text() []byte {
	const dim = 40

	solid := filter.JBIG2Symbol{Width: 16, Height: 16, Pix: make([]byte, 16*16)}
	for i := range solid.Pix {
		solid.Pix[i] = 1
	}

	const hollowDim, hollowBorder = 18, 4
	hollow := filter.JBIG2Symbol{Width: hollowDim, Height: hollowDim, Pix: make([]byte, hollowDim*hollowDim)}
	for y := 0; y < hollowDim; y++ {
		for x := 0; x < hollowDim; x++ {
			if x < hollowBorder || y < hollowBorder || x >= hollowDim-hollowBorder || y >= hollowDim-hollowBorder {
				hollow.Pix[y*hollowDim+x] = 1
			}
		}
	}

	// Symbols ordered by non-decreasing height, instances by Y then X -
	// both required by the encoder, and both the order a real encoder
	// produces anyway.
	symbols := []filter.JBIG2Symbol{solid, hollow}
	instances := []filter.JBIG2Instance{
		{Symbol: 1, X: 21, Y: 1}, // Hollow, top-right quadrant.
		{Symbol: 0, X: 2, Y: 2},  // Solid, top-left quadrant.
		{Symbol: 1, X: 1, Y: 21}, // Hollow, bottom-left quadrant.
	}
	globals, page := filter.EncodeJBIG2SymbolText(dim, dim, symbols, instances)

	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /XObject << /Im0 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("q\n100 0 0 100 0 0 cm\n/Im0 Do\nQ\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	imgDict := fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d "+
		"/ColorSpace /DeviceGray /BitsPerComponent 1 /Filter /JBIG2Decode "+
		"/DecodeParms << /JBIG2Globals 6 0 R >> /Length %d >>", dim, dim, len(page))
	b.addObject(5, 0, imgDict, page)

	b.addObject(6, 0, fmt.Sprintf("<< /Length %d >>", len(globals)), globals)

	return b.finish(1)
}

// buildInlineImage returns a single 100x100-point page whose content
// stream paints an inline ("BI"/"ID"/"EI") image directly, with no
// /Resources /XObject entry at all: a 2x1 DeviceRGB image (red, green),
// uncompressed, scaled to cover the whole page. This is the Phase 3
// counterpart to buildImageRGB for the *other* way a PDF can embed image
// data - exercising internal/content's inline-image parsing
// (operator.go/inlineimage.go) end to end, including its computed-exact-
// length read path (this image has no /Filter, so its raw byte count is
// computed from /W, /H, /BPC, and /CS rather than needing an /L key or a
// scan for "EI" - see inlineimage.go's readInlineImageData).
func buildInlineImage() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Resources << >> /Contents 4 0 R >>", nil)

	var content bytes.Buffer
	content.WriteString("q\n100 0 0 100 0 0 cm\nBI /W 2 /H 1 /BPC 8 /CS /RGB ID ")
	content.Write([]byte{255, 0, 0, 0, 255, 0}) // red, green
	content.WriteString(" EI\nQ\n")

	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", content.Len()), content.Bytes())
	return b.finish(1)
}

// buildRotatedPage returns a page whose MediaBox is 100 (wide) x 200
// (tall) points but which declares /Rotate 90, with a 20x20-point red
// square filled at the origin of its own (unrotated) user space - the
// Phase 3 regression fixture for Page.Render's and Page.Thumbnail's
// /Rotate handling, which previously had no end-to-end rendering test at
// all (only internal/model's inheritance/normalization logic was
// covered - see model_test.go's TestRotateIsInheritedAndNormalized).
//
// Working through pageDeviceGeometry's own math (root package page.go)
// by hand: a 90-degree rotation swaps the rendered image's pixel
// dimensions to 200x100, and maps this fixture's user-space square
// (0,0)-(20,20) to device rectangle (0,0)-(20,20) in the *rotated*
// canvas - i.e. the red square should land at the top-left corner of a
// 200x100 rendered image. A test asserting exactly that (rather than
// only checking the output's pixel dimensions) catches a rotation
// direction or sign error that a dimensions-only check would miss.
func buildRotatedPage() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 200] /Rotate 90 "+
		"/Resources << >> /Contents 4 0 R >>", nil)

	content := []byte("1 0 0 rg\n0 0 20 20 re\nf\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	return b.finish(1)
}

// buildPageBoxes returns a single page declaring all five PDF page
// boxes, each nested strictly inside the one before it by a 10-point
// margin on every side:
//
//	MediaBox [0   0   200 200]  (the whole 200x200 physical sheet)
//	CropBox  [10  10  190 190]
//	BleedBox [20  20  180 180]
//	TrimBox  [30  30  170 170]
//	ArtBox   [40  40  160 160]  (innermost, 120x120)
//
// The content stream paints five solid, nested squares matching those
// same five rectangles, largest first so each later (smaller) square
// paints on top of the ones before it - gray for the MediaBox-sized
// square, then red/green/blue/yellow for CropBox/BleedBox/TrimBox/
// ArtBox in turn. Because each square is strictly smaller than, and
// painted after, the one before it, the color actually visible at any
// point is always the *smallest* box's color that contains that point:
// a 10-point-wide ring of gray between MediaBox and CropBox, a
// 10-point-wide ring of red between CropBox and BleedBox, and so on
// inward, with solid yellow filling all of ArtBox.
//
// This is Phase 16's page-box-selection fixture (docs/PLAN2.md): a test
// rendering this fixture with RenderOptions.Box set to each of
// CropBoxPage (the default)/MediaBoxPage/BleedBoxPage/TrimBoxPage/
// ArtBoxPage in turn can confirm both that the rendered image's pixel
// dimensions match the selected box's own size and that a point sampled
// a few points inside that box's own edge (safely within its own
// 10-point ring, away from any rounding at the exact boundary) shows
// that ring's distinct color - proof the selected box, and not some
// other one, actually became the rendered viewport. See
// pdfviewer_pagebox_test.go for exactly that derivation, box by box.
func buildPageBoxes() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R "+
		"/MediaBox [0 0 200 200] /CropBox [10 10 190 190] "+
		"/BleedBox [20 20 180 180] /TrimBox [30 30 170 170] /ArtBox [40 40 160 160] "+
		"/Resources << >> /Contents 4 0 R >>", nil)

	content := []byte("" +
		"0.6 0.6 0.6 rg\n0 0 200 200 re\nf\n" + // MediaBox-sized square: gray
		"1 0 0 rg\n10 10 180 180 re\nf\n" + // CropBox-sized square: red
		"0 1 0 rg\n20 20 160 160 re\nf\n" + // BleedBox-sized square: green
		"0 0 1 rg\n30 30 140 140 re\nf\n" + // TrimBox-sized square: blue
		"1 1 0 rg\n40 40 120 120 re\nf\n") // ArtBox-sized square: yellow
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	return b.finish(1)
}

// buildSeparationFill returns a single 100x100-point page whose
// /Resources /ColorSpace declares one named color space, /CS0: a
// [/Separation /Spot1 /DeviceRGB tintFn] color space whose tint
// transform (an inline Type 2 exponential-interpolation function
// dictionary - no separate indirect object needed, since a function
// object may be written directly rather than referenced) maps tint 0 to
// white and tint 1 to (0, 0.5, 1) - a distinctive blue-ish tone chosen so
// a rendering test can tell "the tint transform actually ran" apart from
// either endpoint of a plausible wrong guess (plain gray from a
// component-count fallback, for instance). The content stream selects
// /CS0 via "cs" and paints an 80x80 square at tint 1 via "scn" - the
// Phase 5 counterpart to buildFilledRect for a resolved, non-Device
// color space used directly as a fill color (as opposed to only as an
// image's /ColorSpace, which internal/image already supported before
// Phase 5 - see docs/capability-matrix.md).
func buildSeparationFill() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /ColorSpace << /CS0 [/Separation /Spot1 /DeviceRGB "+
		"<< /FunctionType 2 /Domain [0 1] /C0 [1 1 1] /C1 [0 0.5 1] /N 1 >> ] >> >> "+
		"/Contents 4 0 R >>", nil)

	content := []byte("/CS0 cs\n1 scn\n10 10 80 80 re\nf\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)
	return b.finish(1)
}

// buildLabFill returns a single 100x100-point page whose /Resources
// /ColorSpace declares /CS0 as a [/Lab dict] color space with an empty
// parameter dictionary (so resolveLab's documented defaults apply: a D65
// /WhitePoint and a*/b* /Range of [-100 100 -100 100]). The content
// stream selects /CS0 and paints two 50x100 bands: the left at
// L*=0,a*=0,b*=0 (black) and the right at L*=100,a*=0,b*=0 (white) - the
// two points every plausible CIELAB->sRGB conversion must agree on
// exactly (see internal/image's TestDecodeLabColorSpace, which uses the
// same two points for the same reason), making this a robust end-to-end
// rendering check independent of exactly which documented approximation
// labToRGB makes for a non-neutral color.
func buildLabFill() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /ColorSpace << /CS0 [/Lab << >>] >> >> /Contents 4 0 R >>", nil)

	content := []byte("/CS0 cs\n0 0 0 scn\n0 0 50 100 re\nf\n100 0 0 scn\n50 0 50 100 re\nf\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)
	return b.finish(1)
}

// buildAxialShading returns a single 100x100-point page whose content
// stream clips to the whole page and then paints a named axial
// (/ShadingType 2) shading directly via the "sh" operator: a black
// (x=0) to white (x=100) gradient, via a Type 2 (exponential
// interpolation) function over DeviceRGB - the baseline Phase 5 fixture
// for gradient shadings and the "sh" operator, distinct from a shading
// *pattern* (see buildShadingPatternFill) in that "sh" paints across the
// entire current clip (here, deliberately, the whole page) rather than a
// specific filled shape.
func buildAxialShading() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /Shading << /Sh0 << /ShadingType 2 /ColorSpace /DeviceRGB "+
		"/Coords [0 0 100 0] /Function << /FunctionType 2 /Domain [0 1] "+
		"/C0 [0 0 0] /C1 [1 1 1] /N 1 >> >> >> >> /Contents 4 0 R >>", nil)

	content := []byte("q\n0 0 100 100 re\nW\nn\n/Sh0 sh\nQ\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)
	return b.finish(1)
}

// buildRadialShading returns a single 100x100-point page whose content
// stream paints a named radial (/ShadingType 3) shading, unclipped, via
// "sh": two concentric circles centered at the page's own center (50,50)
// - r0=0 (black) growing to r1=50 (blue) - with /Extend [false true], so
// beyond the outer circle (the square page's corners, at distance
// ~70.7 from center, lie outside a radius-50 circle) the shading's t=1
// edge color (blue) is used rather than leaving the corners as the
// default white background. Blue (rather than white) is deliberately
// chosen as the outer color specifically so a rendering test can tell
// "the /Extend region actually painted" apart from "nothing painted,
// background showing through" - both would otherwise look identical.
func buildRadialShading() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /Shading << /Sh0 << /ShadingType 3 /ColorSpace /DeviceRGB "+
		"/Coords [50 50 0 50 50 50] /Extend [false true] "+
		"/Function << /FunctionType 2 /Domain [0 1] /C0 [0 0 0] /C1 [0 0 1] /N 1 >> >> >> >> "+
		"/Contents 4 0 R >>", nil)

	content := []byte("/Sh0 sh\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)
	return b.finish(1)
}

// buildShadingPatternFill returns a single 100x100-point page whose
// content stream selects a shading pattern (/PatternType 2, wrapping the
// same black-to-white axial gradient buildAxialShading uses, spanning
// x=10 to x=90 to line up with the filled square below) via "cs
// Pattern"/"scn", then fills an 80x80 square with it - the Phase 5
// counterpart to buildAxialShading/buildSeparationFill demonstrating a
// shading used as an ordinary fill *paint source* (bounded to the
// filled shape's own geometry) rather than "sh"'s whole-clip painting.
func buildShadingPatternFill() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /Pattern << /P0 << /PatternType 2 /Shading "+
		"<< /ShadingType 2 /ColorSpace /DeviceRGB /Coords [10 0 90 0] "+
		"/Function << /FunctionType 2 /Domain [0 1] /C0 [0 0 0] /C1 [1 1 1] /N 1 >> >> "+
		">> >> >> /Contents 4 0 R >>", nil)

	content := []byte("/Pattern cs\n/P0 scn\n10 10 80 80 re\nf\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)
	return b.finish(1)
}

// buildFunctionBasedShading returns a single 100x100-point page whose
// content stream paints a named function-based (/ShadingType 1) shading
// directly via "sh": color comes straight from a 2-input (x, y) /Function
// rather than any line/circle geometry, so this fixture deliberately
// makes the two input axes produce *different* colors (red grows with x,
// green grows with y, blue stays 0) - a bug that swapped or ignored
// either axis would still pass a fixture whose two axes did the same
// thing, but not this one.
//
// The function itself is a Type 0 (sampled) function - the only function
// type this project implements that takes 2 inputs at all (Type 2's
// exponential interpolation and Type 3's stitching are both 1-input-only
// - see internal/function's package doc comment) - over a 2x2 grid:
// (x=0,y=0) black, (x=1,y=0) red, (x=0,y=1) green, (x=1,y=1) yellow.
// /Matrix scales the shading's own [0,1]x[0,1] /Domain up to the page's
// full 100x100 user-space extent, and "sh" (like buildAxialShading) is
// run inside a clip to the whole page.
//
// Working out expected device pixel colors requires remembering that
// Page.Render's initial CTM flips the y axis (PDF user space has y
// increasing upward; device rows increase downward - see page.go's
// pageDeviceGeometry) - so PDF-space (0,0), this shading's black corner,
// ends up at the device image's *bottom*-left, not top-left. Device
// pixel (0,0) [top-left] therefore samples close to domain (0,1) -
// green - and (99,99) [bottom-right] samples close to domain (1,0) - red.
func buildFunctionBasedShading() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /Shading << /Sh0 << /ShadingType 1 /ColorSpace /DeviceRGB "+
		"/Domain [0 1 0 1] /Matrix [100 0 0 100 0 0] /Function 5 0 R >> >> >> "+
		"/Contents 4 0 R >>", nil)

	content := []byte("q\n0 0 100 100 re\nW\nn\n/Sh0 sh\nQ\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	// Sample bytes, dimension 0 (x) varying fastest per 7.10.2, 3 output
	// components (R,G,B) stored consecutively per grid point:
	// (x=0,y=0)=black, (x=1,y=0)=red, (x=0,y=1)=green, (x=1,y=1)=yellow.
	samples := []byte{
		0x00, 0x00, 0x00,
		0xFF, 0x00, 0x00,
		0x00, 0xFF, 0x00,
		0xFF, 0xFF, 0x00,
	}
	fnDict := fmt.Sprintf("<< /FunctionType 0 /Domain [0 1 0 1] /Range [0 1 0 1 0 1] "+
		"/Size [2 2] /BitsPerSample 8 /Length %d >>", len(samples))
	b.addObject(5, 0, fnDict, samples)

	return b.finish(1)
}

// meshBitWriter packs successive most-significant-bit-first fields into a
// byte slice - the forward direction of the same bit-packing convention
// internal/content/meshshading.go's bitReader reads, letting this file
// hand-author a real Type 4 mesh shading's packed vertex stream the same
// way every other fixture here hand-authors its content stream. This
// project has no production PDF-*writing* code at all (see the package
// doc comment's "hand-authored" note), so - exactly like
// internal/content/meshshading_test.go's own identical-in-shape helper -
// this exists purely to build test/fixture input, not because anything
// in the library itself ever needs to write a mesh shading stream.
type meshBitWriter struct {
	data   []byte
	bitPos int
}

func (w *meshBitWriter) write(value uint32, bits int) {
	for i := bits - 1; i >= 0; i-- {
		byteIdx := w.bitPos / 8
		for byteIdx >= len(w.data) {
			w.data = append(w.data, 0)
		}
		if (value>>uint(i))&1 != 0 {
			w.data[byteIdx] |= 1 << uint(7-w.bitPos%8)
		}
		w.bitPos++
	}
}

// align pads to the next byte boundary - Type 4's per-vertex rule (see
// buildFreeFormTriangleMeshShading below); harmless to call when already
// aligned.
func (w *meshBitWriter) align() {
	if rem := w.bitPos % 8; rem != 0 {
		w.bitPos += 8 - rem
	}
}

// buildFreeFormTriangleMeshShading returns a single 100x100-point page
// whose content stream paints a named free-form Gouraud-shaded triangle
// mesh (/ShadingType 4) directly via "sh": one triangle, red at PDF-space
// (0,0), green at (100,0), blue at (0,100) - the right triangle covering
// the half of the page on the origin side of that diagonal, left
// unpainted (background white) on the other side.
//
// /BitsPerCoordinate and /BitsPerComponent are both 8, with /Decode
// mapping the coordinate byte range [0,255] onto the page's own [0,100]
// extent and the component byte range onto color range [0,1] - chosen
// specifically so this fixture's three vertices (all at coordinate 0 or
// 255) map to *exactly* 0 or 100 with no rounding at all, unlike an
// interior point would; working out expected pixel colors only needs the
// triangle's exact geometry, not any bit-quantization rounding.
//
// As with buildFunctionBasedShading, remember Page.Render's PDF-to-device
// y-axis flip: PDF-space (0,0) [red] ends up at the device image's
// *bottom*-left, (100,0) [green] at bottom-right, and (0,100) [blue] at
// top-left - so the painted (lower-left) half of the page, in device
// terms, is where device x <= device y.
func buildFreeFormTriangleMeshShading() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /Shading << /Sh0 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("q\n0 0 100 100 re\nW\nn\n/Sh0 sh\nQ\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	w := &meshBitWriter{}
	writeMeshVertex := func(flag, x, y, r, g, bl byte) {
		w.write(uint32(flag), 8)
		w.write(uint32(x), 8)
		w.write(uint32(y), 8)
		w.write(uint32(r), 8)
		w.write(uint32(g), 8)
		w.write(uint32(bl), 8)
		w.align() // a no-op here (48 bits is already a whole number of bytes), but every Type 4 vertex must do this
	}
	writeMeshVertex(0, 0, 0, 255, 0, 0)   // red at (0,0)
	writeMeshVertex(0, 255, 0, 0, 255, 0) // green at (100,0)
	writeMeshVertex(0, 0, 255, 0, 0, 255) // blue at (0,100)

	shDict := fmt.Sprintf("<< /ShadingType 4 /ColorSpace /DeviceRGB "+
		"/BitsPerCoordinate 8 /BitsPerComponent 8 /BitsPerFlag 8 "+
		"/Decode [0 100 0 100 0 1 0 1 0 1] /Length %d >>", len(w.data))
	b.addObject(5, 0, shDict, w.data)

	return b.finish(1)
}

// buildLatticeFormTriangleMeshShading returns a single 100x100-point page
// whose content stream paints a named lattice-form Gouraud-shaded
// triangle mesh (/ShadingType 5) directly via "sh": a 2x2 grid of
// vertices (/VerticesPerRow 2) covering the *entire* page - red at
// PDF-space (0,0), green at (100,0), blue at (0,100), yellow at
// (100,100) - unlike buildFreeFormTriangleMeshShading's Type 4 fixture,
// which deliberately leaves half the page unpainted, this one is a
// complementary check that a lattice mesh's implicit (flag-less) row/
// column adjacency triangulates and covers a full quad correctly. There
// are no edge flags in a Type 5 stream at all - each vertex is simply
// (x, y, r, g, b) - and, unlike Type 4, vertices are packed with no
// per-vertex byte padding (see internal/content/meshshading.go's own doc
// comment on why the two types differ here; this fixture's 5 fields x 8
// bits = 40 bits per vertex happens to land on a byte boundary anyway, so
// it does not itself exercise that distinction - internal/content's own
// unit tests do, with a deliberately non-byte-friendly bit width).
//
// As with the Type 4 fixture, remember Page.Render's PDF-to-device
// y-axis flip when reasoning about expected pixel colors: PDF-space
// (0,0) [red] ends up at the device image's bottom-left, (100,0) [green]
// at bottom-right, (0,100) [blue] at top-left, and (100,100) [yellow] at
// top-right.
func buildLatticeFormTriangleMeshShading() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /Shading << /Sh0 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("q\n0 0 100 100 re\nW\nn\n/Sh0 sh\nQ\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	w := &meshBitWriter{}
	writeLatticeVertex := func(x, y, r, g, bl byte) {
		w.write(uint32(x), 8)
		w.write(uint32(y), 8)
		w.write(uint32(r), 8)
		w.write(uint32(g), 8)
		w.write(uint32(bl), 8)
	}
	writeLatticeVertex(0, 0, 255, 0, 0)       // row 0: red at (0,0)
	writeLatticeVertex(255, 0, 0, 255, 0)     // row 0: green at (100,0)
	writeLatticeVertex(0, 255, 0, 0, 255)     // row 1: blue at (0,100)
	writeLatticeVertex(255, 255, 255, 255, 0) // row 1: yellow at (100,100)

	shDict := fmt.Sprintf("<< /ShadingType 5 /ColorSpace /DeviceRGB "+
		"/BitsPerCoordinate 8 /BitsPerComponent 8 /VerticesPerRow 2 "+
		"/Decode [0 100 0 100 0 1 0 1 0 1] /Length %d >>", len(w.data))
	b.addObject(5, 0, shDict, w.data)

	return b.finish(1)
}

// buildCoonsPatchMeshShading returns a single 100x100-point page whose
// content stream paints a named Coons patch mesh (/ShadingType 6)
// directly via "sh": one patch, its 12 boundary control points tracing a
// perfectly flat (straight-edged) rectangle covering the entire page -
// red at PDF-space (0,0), green at (100,0), blue at (0,100), yellow at
// (100,100), the same four corners and colors as
// buildLatticeFormTriangleMeshShading's Type 5 fixture, deliberately: a
// flat Coons patch's interior is bilinear, identical in principle to a
// lattice mesh's own interpolation, so this fixture's expected pixel
// colors are (up to the coarseness of internal/content's fixed
// subdivision count) the same as that fixture's - a useful cross-check
// that Type 6 patch subdivision reproduces ordinary bilinear shading
// correctly before ever exercising a genuinely curved patch.
//
// The boundary point order matches 8.7.4.5.7's own traversal (see
// internal/content/meshshading.go's applyPatchBoundary doc comment):
// starting at the red corner, up the left edge, across the top, down the
// right edge, and back across the bottom - with each edge's two
// non-corner control points evenly spaced (85, 170 out of 255) so an
// 8-bit-per-coordinate stream can represent them exactly, keeping this
// patch's edges perfectly straight (a curved edge would need points that
// are not evenly spaced).
func buildCoonsPatchMeshShading() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /Shading << /Sh0 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("q\n0 0 100 100 re\nW\nn\n/Sh0 sh\nQ\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	w := &meshBitWriter{}
	w.write(0, 8) // edge flag: 0, an independent patch
	boundary := [][2]byte{
		{0, 0}, {0, 85}, {0, 170}, {0, 255}, {85, 255}, {170, 255},
		{255, 255}, {255, 170}, {255, 85}, {255, 0}, {170, 0}, {85, 0},
	}
	for _, p := range boundary {
		w.write(uint32(p[0]), 8)
		w.write(uint32(p[1]), 8)
	}
	colors := [][3]byte{{255, 0, 0}, {0, 0, 255}, {255, 255, 0}, {0, 255, 0}} // red, blue, yellow, green
	for _, c := range colors {
		w.write(uint32(c[0]), 8)
		w.write(uint32(c[1]), 8)
		w.write(uint32(c[2]), 8)
	}

	shDict := fmt.Sprintf("<< /ShadingType 6 /ColorSpace /DeviceRGB "+
		"/BitsPerCoordinate 8 /BitsPerComponent 8 /BitsPerFlag 8 "+
		"/Decode [0 100 0 100 0 1 0 1 0 1] /Length %d >>", len(w.data))
	b.addObject(5, 0, shDict, w.data)

	return b.finish(1)
}

// buildTensorProductPatchMeshShading returns a single 100x100-point page
// whose content stream paints a named tensor-product patch mesh
// (/ShadingType 7) directly via "sh": the same boundary and corner
// colors as buildCoonsPatchMeshShading's flat Coons patch (red/green/
// blue/yellow at PDF-space (0,0)/(100,0)/(0,100)/(100,100)), but with its
// 4 internal control points read directly from the stream (Type 7's own
// defining difference from Type 6 - see
// internal/content/meshshading.go's decodeType7Mesh) and deliberately
// pulled toward the red corner (roughly (10,10) rather than the flat
// case's bilinear (33,33)/(67,33)/(67,67)/(33,67)), producing a visibly
// curved surface pinched toward that corner rather than a flat gradient -
// a fixture that only a genuinely curved Type 7 patch (not one that
// silently fell back to treating it as flat) can render correctly.
//
// This fixture only asserts each corner's own color (still exact for any
// Bezier surface regardless of internal-point placement - see
// TestPatchToTrianglesCornersMatchControlPoints), leaving the curved
// interior to the checked-in golden reference image.
func buildTensorProductPatchMeshShading() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /Shading << /Sh0 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("q\n0 0 100 100 re\nW\nn\n/Sh0 sh\nQ\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	w := &meshBitWriter{}
	w.write(0, 8) // edge flag: 0, an independent patch
	boundary := [][2]byte{
		{0, 0}, {0, 85}, {0, 170}, {0, 255}, {85, 255}, {170, 255},
		{255, 255}, {255, 170}, {255, 85}, {255, 0}, {170, 0}, {85, 0},
	}
	// The 4 internal points, in pts[5]/pts[9]/pts[10]/pts[6] order, all
	// pulled toward the red corner (0,0) instead of sitting at the flat
	// case's own bilinear thirds.
	internal := [][2]byte{{26, 26}, {26, 26}, {26, 26}, {26, 26}}
	for _, p := range append(append([][2]byte{}, boundary...), internal...) {
		w.write(uint32(p[0]), 8)
		w.write(uint32(p[1]), 8)
	}
	colors := [][3]byte{{255, 0, 0}, {0, 0, 255}, {255, 255, 0}, {0, 255, 0}} // red, blue, yellow, green
	for _, c := range colors {
		w.write(uint32(c[0]), 8)
		w.write(uint32(c[1]), 8)
		w.write(uint32(c[2]), 8)
	}

	shDict := fmt.Sprintf("<< /ShadingType 7 /ColorSpace /DeviceRGB "+
		"/BitsPerCoordinate 8 /BitsPerComponent 8 /BitsPerFlag 8 "+
		"/Decode [0 100 0 100 0 1 0 1 0 1] /Length %d >>", len(w.data))
	b.addObject(5, 0, shDict, w.data)

	return b.finish(1)
}

// buildFormXObject returns a single 100x100-point page that invokes a
// Form XObject ("Fm0") translated by (30,30) via "cm": the form's own
// content deliberately fills the *entire* page red (`0 0 100 100 re f`),
// overflowing its own declared /BBox ([0 0 40 40] in form space) - so
// only the resulting device-space intersection of that BBox with the
// translation actually ends up painted. Worked out by hand: form space
// (0,0)-(40,40), translated by (30,30) in user space before the page's
// own y-flip, lands at device (30,30)-(70,70) - a 40x40 red square
// centered on the page, with the rest of the page left as the untouched
// white background. This is the baseline Phase 5 fixture for Form
// XObjects: it fails outright (painting the whole page red) if either
// the form's /Matrix-then-current-CTM composition or its /BBox clipping
// is wrong, rather than only partially.
func buildFormXObject() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /XObject << /Fm0 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("q\n1 0 0 1 30 30 cm\n/Fm0 Do\nQ\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	formContent := []byte("1 0 0 rg\n0 0 100 100 re\nf\n")
	formDict := fmt.Sprintf("<< /Type /XObject /Subtype /Form /BBox [0 0 40 40] /Length %d >>", len(formContent))
	b.addObject(5, 0, formDict, formContent)

	return b.finish(1)
}

// buildAnnotationAppearance returns a single 100x100-point page with no
// content of its own, but one annotation (/Rect [20 20 80 80]) whose
// normal appearance (/AP /N) is a Form XObject filling its own
// [0 0 60 60] /BBox solid green - since the appearance's /BBox is
// already exactly the same size as /Rect (60x60), the resulting
// appearance-to-Rect mapping (internal/annotation's Appearance.Matrix)
// is a pure translation, landing the green square at device
// (20,20)-(80,80) after the page's own y-flip (worked out by hand: PDF
// y in [20,80] maps to device y in [20,80] too, since 100-80=20 and
// 100-20=80 - a happy symmetry of this particular Rect, not a general
// property of the mapping). The baseline Phase 5 fixture for annotation
// appearance streams.
func buildAnnotationAppearance() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << >> /Contents 4 0 R /Annots [5 0 R] >>", nil)
	b.addObject(4, 0, "<< /Length 0 >>", []byte{})

	apContent := []byte("0 1 0 rg\n0 0 60 60 re\nf\n")
	apDict := fmt.Sprintf("<< /Type /XObject /Subtype /Form /BBox [0 0 60 60] /Length %d >>", len(apContent))
	b.addObject(6, 0, apDict, apContent)

	b.addObject(5, 0, "<< /Type /Annot /Subtype /Square /Rect [20 20 80 80] "+
		"/AP << /N 6 0 R >> >>", nil)

	return b.finish(1)
}

// buildAnnotationHidden returns a single 100x100-point page with no
// content of its own and one annotation whose /F (flags) entry sets bit
// 2 (Hidden, value 2): its appearance - a Form XObject filling the
// entire page red - must never be painted, by default or otherwise
// (unlike RenderOptions.HideAnnotations, which is a caller's opt-out;
// /F Hidden is the file's own, unconditional instruction that this
// annotation must never be displayed at all). Distinct from
// buildAnnotationAppearance so a rendering test can confirm the *page
// stays blank* rather than merely that some other appearance still
// renders correctly.
func buildAnnotationHidden() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << >> /Contents 4 0 R /Annots [5 0 R] >>", nil)
	b.addObject(4, 0, "<< /Length 0 >>", []byte{})

	apContent := []byte("1 0 0 rg\n0 0 100 100 re\nf\n")
	apDict := fmt.Sprintf("<< /Type /XObject /Subtype /Form /BBox [0 0 100 100] /Length %d >>", len(apContent))
	b.addObject(6, 0, apDict, apContent)

	b.addObject(5, 0, "<< /Type /Annot /Subtype /Square /Rect [0 0 100 100] /F 2 "+
		"/AP << /N 6 0 R >> >>", nil)

	return b.finish(1)
}

// buildAlphaFill returns a single 100x100-point page with a white
// background that selects an ExtGState (/GS0) setting the non-stroking
// constant alpha (/ca) to 0.5, then fills the entire page black - the
// baseline Phase 5 fixture for "gs"/`ca`: the result should be
// approximately 50% gray (black blended at half strength over white),
// not solid black, which is what this fixture would show if "gs"'s
// /ca were still silently ignored (as it was before Phase 5's
// transparency work).
func buildAlphaFill() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /ExtGState << /GS0 << /ca 0.5 >> >> >> /Contents 4 0 R >>", nil)

	content := []byte("/GS0 gs\n0 0 0 rg\n0 0 100 100 re\nf\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)
	return b.finish(1)
}

// buildBlendMultiply returns a single 100x100-point page that fills the
// entire page 50% gray, then - after selecting an ExtGState (/GS0)
// setting /BM to /Multiply - fills the right half of the page with
// another 50% gray: Multiply(0.5,0.5)=0.25, a darker gray than either
// input alone, so the right half should end up visibly darker than the
// (untouched, still 50% gray) left half - a result BlendNormal (an
// ordinary replace) could never produce, since replacing 50% gray with
// 50% gray leaves it exactly as it was.
func buildBlendMultiply() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /ExtGState << /GS0 << /BM /Multiply >> >> >> /Contents 4 0 R >>", nil)

	content := []byte("0.5 0.5 0.5 rg\n0 0 100 100 re\nf\n" +
		"/GS0 gs\n0.5 0.5 0.5 rg\n50 0 50 100 re\nf\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)
	return b.finish(1)
}

// buildTilingPatternFill returns a single 100x100-point page whose
// content stream selects a colored tiling pattern (/PatternType 1: a
// 20x20-unit cell, /BBox and /XStep/YStep all matching so cells tile
// with no gaps or overlap, painting a 10x10 red square at the cell's
// own origin and leaving the rest transparent) via "cs Pattern"/"scn",
// then fills an 80x80 square with it - the baseline Phase 5 fixture for
// tiling patterns, worked out by hand: the cell spanning pattern-space
// (60,60)-(80,80) places its own red square at pattern-space
// (60,60)-(70,70), which the page's standard y-flip (unrotated, scale 1)
// maps to device (60,30)-(70,40) - and the *rest* of that same cell
// (for example pattern-space (78,62), well inside the cell but outside
// its 10x10 red square) must stay the untouched white page background,
// confirming the pattern's own transparent regions are not painted as
// solid color.
func buildTilingPatternFill() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /Pattern << /P0 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("/Pattern cs\n/P0 scn\n10 10 80 80 re\nf\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	patternContent := []byte("1 0 0 rg\n0 0 10 10 re\nf\n")
	patternDict := fmt.Sprintf("<< /Type /Pattern /PatternType 1 /PaintType 1 /TilingType 1 "+
		"/BBox [0 0 20 20] /XStep 20 /YStep 20 /Resources << >> /Length %d >>", len(patternContent))
	b.addObject(5, 0, patternDict, patternContent)

	return b.finish(1)
}

// buildSinglePageContent is the shared skeleton behind the vector
// rendering fixtures above: one page of the given size, one content
// stream holding contentOps verbatim (unfiltered - internal/filter is
// exercised separately by buildXrefStream/buildObjectStream's Flate
// streams, so these rendering fixtures keep their content streams
// plain text for easy reading in a hex/text dump while debugging a
// rendering test failure).
func buildSinglePageContent(width, height int, contentOps string) []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << >> /Contents 4 0 R >>", width, height), nil)
	content := []byte(contentOps)
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)
	return b.finish(1)
}

// --- Low-level PDF byte assembly ------------------------------------

// builder accumulates PDF file bytes while tracking the byte offset at
// which each indirect object's "N G obj" line begins, which is exactly
// the information a cross-reference table needs to record. It is not a
// general-purpose PDF writer - it only supports the small set of
// constructs these fixtures need.
type builder struct {
	buf     bytes.Buffer
	offsets map[int]int // object number -> byte offset of "N G obj"
	maxObj  int
}

func newBuilder() *builder {
	b := &builder{offsets: make(map[int]int)}
	b.buf.WriteString("%PDF-1.7\n")
	// A conventional four-byte binary comment marking the file as
	// containing binary data, as recommended by the PDF specification so
	// that naive text-mode file transfers don't mangle it. It has no
	// structural meaning to a parser beyond being a comment line (a line
	// starting with '%').
	b.buf.Write([]byte{'%', 0xE2, 0xE3, 0xCF, 0xD3, '\n'})
	return b
}

// addObject records the current buffer length as the object's offset,
// then writes "N G obj\n<dict>\n[stream\n<data>\nendstream\n]endobj\n". If
// data is nil, the object has no stream and dict is written as-is
// (typically a dictionary or array). If data is non-nil (even if empty),
// dict must be a dictionary whose /Length entry matches len(data)
// exactly - the caller is responsible for that, since only the caller
// knows the intended dictionary contents.
func (b *builder) addObject(num, gen int, dict string, data []byte) {
	b.offsets[num] = b.buf.Len()
	if num > b.maxObj {
		b.maxObj = num
	}
	fmt.Fprintf(&b.buf, "%d %d obj\n%s\n", num, gen, dict)
	if data != nil {
		b.buf.WriteString("stream\n")
		b.buf.Write(data)
		b.buf.WriteString("\nendstream\n")
	}
	b.buf.WriteString("endobj\n")
}

// writeXrefAndTrailer appends a classic (non-stream) cross-reference
// table covering every object number from 0 through the highest object
// number seen so far, followed by a trailer whose dictionary is
// "<< /Size <n> /Root <rootObj> 0 R<trailerExtra> >>", and finally the
// startxref/%%EOF footer. It returns the byte offset at which the "xref"
// keyword was written, which a later incremental update's trailer needs
// to record as its /Prev value.
//
// trailerExtra is inserted verbatim just before the trailer dictionary's
// closing ">>" and is expected to already include its own leading space
// (e.g. " /Prev 123"), or to be empty for a file's first (and possibly
// only) trailer.
func (b *builder) writeXrefAndTrailer(rootObj int, trailerExtra string) int {
	xrefOffset := b.buf.Len()

	size := b.maxObj + 1
	fmt.Fprintf(&b.buf, "xref\n0 %d\n", size)

	// Object 0 is always the head of the free-object linked list, always
	// generation 65535, always type 'f'. This project never reuses freed
	// object numbers in its fixtures, so no other object needs a real
	// "next free" chain; object 0 points at itself (offset 0), which is
	// how a PDF with no free objects conventionally represents the end
	// of that chain.
	b.writeXrefEntry(0, 65535, 'f')
	for n := 1; n < size; n++ {
		offset, ok := b.offsets[n]
		if !ok {
			// No object was ever registered for this number in this
			// revision; treat it as free. None of the fixtures above
			// currently exercise this path, but it keeps the table
			// well-formed if a future fixture has a gap in object
			// numbers.
			b.writeXrefEntry(0, 65535, 'f')
			continue
		}
		b.writeXrefEntry(offset, 0, 'n')
	}

	fmt.Fprintf(&b.buf, "trailer\n<< /Size %d /Root %d 0 R%s >>\n", size, rootObj, trailerExtra)
	fmt.Fprintf(&b.buf, "startxref\n%d\n%%%%EOF\n", xrefOffset)

	return xrefOffset
}

// writeXrefEntry writes exactly one 20-byte cross-reference table entry,
// the fixed width the PDF specification requires: a 10-digit byte
// offset, a space, a 5-digit generation number, a space, the single
// letter 'n' (in use) or 'f' (free), and a mandatory 2-character
// end-of-line sequence. Using a fixed byte count matters here: some
// real-world readers seek by entry index (offset + 20*n) rather than
// scanning line by line, so an entry of the wrong length would silently
// desynchronize every entry after it.
func (b *builder) writeXrefEntry(offset, gen int, kind byte) {
	fmt.Fprintf(&b.buf, "%010d %05d %c \n", offset, gen, kind)
}

// finish writes the xref table and trailer for a single-revision file
// (no /Prev) and returns the completed file bytes.
func (b *builder) finish(rootObj int) []byte {
	b.writeXrefAndTrailer(rootObj, "")
	return b.buf.Bytes()
}

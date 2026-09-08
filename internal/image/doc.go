// Package image decodes a PDF image object - either a referenced image
// XObject (a stream whose dictionary has /Subtype /Image) or an inline
// image (a "BI...ID...EI" content stream construct) - into a plain
// graphics.Image: a grid of already-final RGBA pixels with no PDF-
// specific meaning left in it at all. This is Phase 3 work per the
// repository README's phased plan ("Images, color, and thumbnails");
// internal/content drives this package from its "Do" and "BI" operator
// handling (see that package), and internal/raster paints the resulting
// graphics.Image (see that package's Canvas.DrawImage).
//
// # What this package does not do
//
// It does not know how to read a stream's raw bytes off disk, follow an
// indirect reference through a cross-reference table, or decode a
// stream's /Filter chain - all of that is internal/parser's job. Instead
// this package is handed already-resolved, already-filter-decoded
// inputs (a dictionary and raw sample bytes) by its caller, plus a small
// Resolver interface (resolver.go) for the few cases where *this*
// package itself needs to reach back into the document for something a
// caller could not have resolved ahead of time - a /ColorSpace named in
// the page's /Resources, an /Indexed color space's lookup table stream,
// an /ICCBased color space's stream, or a separate /SMask or /Mask image
// stream.
//
// # Color spaces (colorspace.go)
//
// DeviceGray, DeviceRGB, and DeviceCMYK (and their inline-image
// abbreviations /G, /RGB, /CMYK) are supported directly. /ICCBased is
// supported by component count only (1, 3, or 4 - treated as an alias
// for the matching Device space, per this project's documented,
// deliberately non-color-managed policy - see docs/capability-matrix.md)
// rather than by actually applying the embedded ICC profile. /Indexed is
// supported over any of those base spaces. /CalGray and /CalRGB are
// treated as DeviceGray/DeviceRGB (ignoring their white point and gamma
// - another documented approximation, in the same spirit as this
// project's existing DeviceCMYK conversion formula in internal/content).
// /Lab, /Separation, /DeviceN, and /Pattern are not implemented and
// return an error wrapping pdferror.ErrUnsupported.
//
// # Sample decoding (decode.go)
//
// Decode reads /BitsPerComponent-sized samples (1, 2, 4, 8, or 16 bits)
// packed most-significant-bit-first with each image row starting on a
// fresh byte boundary, per the PDF specification, and remaps each
// through the image's /Decode array (or that color space's default
// range if none is given) before converting to RGB.
//
// # Masking (mask.go)
//
// /ImageMask true images paint using the caller-supplied current fill
// color, with each sample selecting paint-or-skip (Decode.go's Options.
// FillColor). /SMask provides a per-pixel soft (continuously variable)
// alpha from a separate grayscale image; /Mask provides either the same
// kind of stencil masking as /ImageMask (when it names another image
// stream) or "color-key" masking (when it is an array of raw sample
// ranges - a pixel whose every raw component sample falls within its
// named range is fully transparent). Per the specification, /SMask takes
// priority over /Mask when both are present. An /SMask or /Mask image
// may have different pixel dimensions than the base image; this package
// resamples it with nearest-neighbor sampling - a documented
// simplification consistent with internal/raster's own image-sampling
// approach - rather than a smoother resampling filter.
package image

// Package function evaluates PDF Function objects (ISO 32000-1 section
// 7.10): a general mechanism for mapping some number of input numbers to
// some number of output numbers, used throughout the PDF specification
// wherever a value needs to be computed from another rather than looked
// up directly. This project's Phase 5 work needs functions in two
// places: a shading's color-per-position mapping (an axial or radial
// shading dictionary's /Function entry - see internal/graphics's
// Shading type and internal/content's shading support) and a Separation
// or DeviceN color space's "tint transform" (the function that turns one
// or more tint/colorant components into an underlying color space's own
// components - see internal/image/colorspace.go).
//
// # If you are new to Go or to PDF functions
//
// A PDF function is not a Go function - it is a small piece of data
// (a dictionary, or a dictionary attached to a stream of pre-computed
// values) that describes *how* to compute outputs from inputs, in one of
// four ways the specification calls "function types". This package
// parses that data (Parse) into a Go value satisfying the Function
// interface, whose Eval method is the actual "call it like a function"
// operation - the parsing step and the evaluation step are deliberately
// separate so that a function used many times (every pixel of a
// gradient fill, for example) only has to be parsed once.
//
// This package implements the three function types that occur
// overwhelmingly in real-world PDF content:
//
//   - Type 0 (sampled): a lookup table of pre-computed output samples
//     over a regular input grid, with multilinear interpolation between
//     grid points - see type0.go.
//   - Type 2 (exponential interpolation): a single-input power-curve
//     interpolation between two output value vectors - the common case
//     for a Separation color space's tint transform and for a shading's
//     simple two-color gradient - see type2.go.
//   - Type 3 (stitching): a single-input function built by concatenating
//     several subfunctions end to end over disjoint subdomains, letting a
//     shading or tint transform describe a multi-stop gradient - see
//     type3.go.
//
// Type 4 (a small PostScript-like calculator language embedded directly
// in a function stream, with its own arithmetic/stack/conditional
// operators) is not implemented: it would need an actual expression
// interpreter that shares essentially no code with the other three
// types, and it is materially rarer in real-world content than the other
// three combined. Parse returns an error wrapping pdferror.ErrUnsupported
// that names it explicitly - this project's usual policy of
// distinguishing "malformed" from "recognized but not implemented" -
// rather than silently miscomputing an output or panicking.
//
// Every Eval implementation in this package is defensive about the
// arithmetic a hostile or malformed function dictionary can trigger
// (division by a zero-width domain, a non-integer exponent applied to a
// negative base, and so on - see safeFloat in common.go): the result is
// always a finite float64, never NaN or +/-Inf, so a bad function can
// only produce a wrong-looking color, never propagate a non-number into
// internal/raster's pixel arithmetic.
package function

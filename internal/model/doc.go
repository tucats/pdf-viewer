// Package model will define the document-level view of a PDF built on
// top of internal/parser's resolved objects: pages and the page tree,
// inherited page attributes (resources, boxes, and rotation can each be
// set on an ancestor in the page tree and inherited by descendant
// pages), resources (fonts, images, color spaces, and so on scoped to a
// page or an ancestor), page boxes (MediaBox, CropBox, and friends), and
// document-level metadata.
//
// This is the layer where PDF's tree-shaped, reference-heavy dictionary
// structure gets turned into something the rest of the codebase (and
// eventually the public pdfviewer API) can use without every caller
// having to understand PDF's object model directly. The public
// pdfviewer.Document and pdfviewer.Page types described in the README's
// Draft Public API are expected to be thin wrappers around this
// package's types, not reimplementations of it.
//
// This package is planned for Phase 1 ("File structure and safe object
// model") of the project's phased plan, with page-tree traversal as its
// first concern; later phases (fonts, images, transparency) will extend
// it rather than replace it. It intentionally contains no code yet —
// Phase 0 only establishes the package skeleton and its place in the
// pipeline described in the README's "Proposed Internal Layout" section.
package model

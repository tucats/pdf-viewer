// Package acroform implements Phase 12: regenerating a "reasonable"
// appearance stream for an AcroForm form field widget that has a
// current value (its /V entry) but no usable existing appearance stream
// to paint - the situation internal/annotation's Resolve already
// tolerates by simply not painting anything for that one annotation
// (see that package's doc comment). This is common for PDFs whose form
// fields were filled by tooling that only writes /V (many programmatic
// form-filling libraries, some non-Adobe scanning/OCR-plus-form
// pipelines) rather than also regenerating /AP, which real PDF viewers
// (Acrobat foremost) always do automatically.
//
// # If you are new to Go: why this package exists at all
//
// Every other rendering feature in this module answers "given bytes
// already present in the file, what does this look like drawn?" This
// package instead has to answer "given no drawable bytes at all, what
// *should* this look like?" - which requires implementing a small,
// deliberately approximate slice of ISO 32000-1 12.7.3.3 ("Variable
// Text"), not just reading data someone else already computed. Every
// simplification this package makes relative to what Acrobat itself
// does is called out explicitly in the function that makes it, in the
// same spirit as this project's other documented approximations (e.g.
// internal/crypt's PDFDocEncoding-as-Latin-1 substitute, or
// internal/fonts' "vertical writing mode advances horizontally"
// simplification).
//
// # The field/widget/AcroForm relationship, briefly
//
// A PDF's interactive form fields live in a tree, reachable from the
// document catalog's /AcroForm dictionary's /Fields array (12.7.2). Each
// field dictionary may itself *be* a widget annotation (the common case:
// one field, one on-screen widget, merged into a single object with both
// a field's entries - /FT, /V, ... - and a widget's - /Rect, /AP, ... -
// together) or may have one or more separate widget annotations as /Kids
// naming it via their own /Parent entry (a field with several on-screen
// representations, or - for a radio button group - one visual widget per
// possible choice). Several field attributes - notably /FT (field type),
// /Ff (field flags), /DA (default appearance string), /Q (quadding/
// justification), and /V (value) - are inheritable up this tree exactly
// like a page tree node's /Resources or /MediaBox (see internal/model's
// own doc comment on that unrelated but structurally identical
// mechanism): a widget missing one of these looks to its /Parent, and so
// on, up to the field tree's root. field.go implements this walk.
//
// # Scope
//
// Implemented: single-line and (word-wrapped, height-truncated) basic
// multiline text fields (/FT /Tx), choice fields shown as plain text of
// their current selection (/FT /Ch - combo and list boxes alike, always
// showing only the first selected value for a multi-select list), and a
// generic checked/unchecked mark for non-pushbutton button fields (/FT
// /Btn - checkboxes and radio buttons are drawn identically, a
// deliberate simplification; see checkbox.go).
//
// Deliberately not implemented, matching this project's general "narrow
// but honest" scope: comb fields (/Ff bit 25) render as ordinary
// non-comb text rather than evenly spaced per-character cells; password
// fields (/Ff bit 14) are never given a generated appearance at all,
// since painting a password's plaintext into a rendered image would
// defeat the field's entire purpose; push buttons (/Ff bit 17) are
// icon/caption-driven, not value-driven, and are skipped entirely;
// signature fields (/FT /Sig) have no generic "current value" text to
// paint at all and are skipped; a choice field's /Opt export-value
// table is not consulted, so /V is always shown exactly as written
// rather than mapped through it. None of these produce incorrect output
// - each one simply continues to render as "no appearance", exactly as
// it did before this package existed, which is always this project's
// fallback whenever a real Acrobat-equivalent algorithm is out of scope
// (see, for example, internal/shading's Type 1/4-7 gap in
// docs/PLAN2.md's Phase 13).
package acroform

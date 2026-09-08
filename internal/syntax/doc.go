// Package syntax will implement the lowest-level PDF lexical grammar:
// tokenizing raw bytes (as read via internal/source) into the small set
// of primitive value types PDF is built from — booleans, numbers,
// strings (both literal "(...)" and hexadecimal "<...>" forms), names
// ("/Name"), arrays, dictionaries ("<< ... >>"), and streams.
//
// This is deliberately a separate layer from internal/parser: this
// package only needs to understand "what shape of token comes next in
// these bytes", not "what does object 7 mean" or "how do cross-reference
// tables chain together". Keeping tokenizing separate from object
// resolution makes both halves easier to test and easier to reason about
// when a file is malformed — a tokenizing error and a structural error
// are different failure modes and should be diagnosable independently.
//
// This package is planned for Phase 1 ("File structure and safe object
// model") of the project's phased plan; see the repository README for
// the full plan. It intentionally contains no code yet — Phase 0 only
// establishes the package skeleton and its place in the pipeline
// described in the README's "Proposed Internal Layout" section.
package syntax

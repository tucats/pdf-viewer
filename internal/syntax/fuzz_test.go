package syntax

import (
	"bytes"
	"testing"
)

// This file adds the Phase 1 fuzz targets promised in the README's
// Progress Log entry for Phase 0 (which deferred them because, at
// Phase 0, there was no parser yet to fuzz). Go's built-in fuzzing
// support (the "go test -fuzz" flag) repeatedly mutates the seed inputs
// below, looking for byte sequences that make the function under test
// panic, or - relevant here - is used with a short deadline to also
// catch inputs that never return. See
// https://go.dev/security/fuzz/ for how Go fuzzing works if you have
// not used it before.
//
// Run with, for example:
//
//	go test ./internal/syntax/ -fuzz=FuzzParseValue -fuzztime=60s
//
// A plain `go test ./...` (including in CI) only runs the seed corpus
// below as ordinary test cases; it does not mutate them. Continuous
// fuzzing (letting these run for extended periods looking for new
// crashers) is a good candidate for a dedicated, separate CI job later
// in this project's development, once there is more surface area worth
// fuzzing continuously - see docs/capability-matrix.md and the README's
// phased plan for what is still to come.

// FuzzParseValue feeds arbitrary bytes to ParseValue, the entry point
// this whole package exists to expose. ParseValue must never panic and
// must always eventually return (it must not loop forever), no matter
// how malformed its input is - both properties this project's
// "Dependency and safety policy" (see the repository README) requires
// of every parser, since a PDF file is always untrusted input.
func FuzzParseValue(f *testing.F) {
	seeds := []string{
		"",
		"true",
		"false",
		"null",
		"123",
		"-123",
		"+123",
		"3.14",
		"-.5",
		"4.",
		"/Name",
		"/A#42#20B",
		"(literal string)",
		`(escapes \n\r\t\b\f\(\)\\\101)`,
		"(unterminated",
		"<48656C6C6F>",
		"<48 65 6C 6C 6F>",
		"<unterminated hex",
		"[1 2 3]",
		"[[[[[[[[[[1]]]]]]]]]]",
		"[",
		"]",
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<<",
		">>",
		"<< /A",
		"5 0 R",
		"5 0 obj",
		"<< /Length 5 >>\nstream\nHello\nendstream",
		"<< /Length 9 0 R >>\nstream\nHello\nendstream",
		"<< /Length -1 >>\nstream\nHello\nendstream",
		"stream\nendstream",
		"%comment\n123",
		"\x00\x01\x02\xff\xfe",
		strDeepNesting(),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, src string) {
		lex := NewLexer(bytes.NewReader([]byte(src)))
		// The return value is deliberately not inspected: for arbitrary
		// fuzzer-generated input, an error is a perfectly normal and
		// expected result. What this test actually checks is only what
		// `go test -fuzz` checks automatically for every call: that
		// ParseValue does not panic. A run with a fuzztime budget (see
		// the package doc comment above) additionally catches inputs
		// that never return at all, since the fuzzing engine treats a
		// hung worker process as a failure too.
		_, _ = ParseValue(lex, 0)
	})
}

// FuzzLexer is the tokenizer-only counterpart to FuzzParseValue: it
// drives the Lexer directly to end of input (or up to a generous Token
// count, as a backstop against a hypothetical future bug that made the
// lexer stop advancing on some input without actually reaching EOF),
// checking only that it never panics and always terminates.
func FuzzLexer(f *testing.F) {
	seeds := []string{
		"", "()", "((()))", "(\\", "<>", "<<>>", "/", "#", "#zz",
		"true false null 123 /Name (str) <68657820> [ ] << >>",
		"\x00\x01\x02\xff\xfe\xfd",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, src string) {
		lex := NewLexer(bytes.NewReader([]byte(src)))
		// A generous upper bound on Token count: real fixtures need at
		// most a few hundred tokens, so hitting this cap indicates the
		// lexer is stuck rather than legitimately still consuming a
		// huge input, and the test should fail loudly instead of
		// hanging the fuzzer.
		const maxTokens = 1_000_000
		for i := 0; ; i++ {
			if i > maxTokens {
				t.Fatalf("lexer did not reach EOF within %d tokens on input %q", maxTokens, src)
			}
			tok, err := lex.Next()
			if err != nil {
				return
			}
			if tok.Kind == KindEOF {
				return
			}
		}
	})
}

func strDeepNesting() string {
	depth := maxNestingDepth * 2
	buf := make([]byte, 0, depth*2)
	for i := 0; i < depth; i++ {
		buf = append(buf, '[')
	}
	for i := 0; i < depth; i++ {
		buf = append(buf, ']')
	}
	return string(buf)
}

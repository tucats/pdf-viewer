package model

import "testing"

func TestPageContentBytesSingleStream(t *testing.T) {
	data := buildTestDocument(t, `
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 10 10] >> endobj
3 0 obj << /Type /Page /Parent 2 0 R /Contents 4 0 R >> endobj
4 0 obj << /Length 11 >>
stream
Hello World
endstream endobj
`)
	d := openTestDocument(t, data)
	got, err := d.PageContentBytes(d.Page(0))
	if err != nil {
		t.Fatalf("PageContentBytes: %v", err)
	}
	if string(got) != "Hello World" {
		t.Errorf("PageContentBytes = %q, want %q", got, "Hello World")
	}
}

// TestPageContentBytesArrayIsConcatenated confirms an array of content
// streams is concatenated with a separating whitespace byte between
// each pair, per the specification - see PageContentBytes's doc
// comment.
func TestPageContentBytesArrayIsConcatenated(t *testing.T) {
	data := buildTestDocument(t, `
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 10 10] >> endobj
3 0 obj << /Type /Page /Parent 2 0 R /Contents [4 0 R 5 0 R] >> endobj
4 0 obj << /Length 5 >>
stream
1 0 0
endstream endobj
5 0 obj << /Length 5 >>
stream
2 0 0
endstream endobj
`)
	d := openTestDocument(t, data)
	got, err := d.PageContentBytes(d.Page(0))
	if err != nil {
		t.Fatalf("PageContentBytes: %v", err)
	}
	want := "1 0 0\n2 0 0"
	if string(got) != want {
		t.Errorf("PageContentBytes = %q, want %q", got, want)
	}
}

func TestPageContentBytesNoContentsIsEmpty(t *testing.T) {
	data := buildTestDocument(t, `
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 10 10] >> endobj
3 0 obj << /Type /Page /Parent 2 0 R >> endobj
`)
	d := openTestDocument(t, data)
	got, err := d.PageContentBytes(d.Page(0))
	if err != nil {
		t.Fatalf("PageContentBytes: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("PageContentBytes = %q, want empty", got)
	}
}

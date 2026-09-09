package diag

import "testing"

func TestNilRecorderRecordAndMessagesAreNoOps(t *testing.T) {
	var r *Recorder
	r.Record("this should never panic or be stored: %d", 42)
	if got := r.Messages(); got != nil {
		t.Errorf("Messages() on a nil *Recorder = %v, want nil", got)
	}
}

func TestRecordAndMessagesOrderAndFormatting(t *testing.T) {
	r := New()
	r.Record("first %s", "message")
	r.Record("second message, code %d", 2)

	got := r.Messages()
	want := []string{"first message", "second message, code 2"}
	if len(got) != len(want) {
		t.Fatalf("Messages() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Messages()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMessagesReturnsACopy(t *testing.T) {
	r := New()
	r.Record("one")
	got := r.Messages()
	got[0] = "mutated"
	if again := r.Messages(); again[0] != "one" {
		t.Errorf("mutating a Messages() result affected the Recorder's own state: got %q", again[0])
	}
}

// fakeResolver satisfies Recordable so Note has something to type-assert
// against, mirroring how internal/model.Document does it in practice
// (see that package's RecordDiagnostic).
type fakeResolver struct {
	recorder *Recorder
}

func (f *fakeResolver) RecordDiagnostic(format string, args ...any) {
	f.recorder.Record(format, args...)
}

func TestNoteRecordsThroughRecordable(t *testing.T) {
	r := New()
	Note(&fakeResolver{recorder: r}, "hello %s", "world")
	got := r.Messages()
	if len(got) != 1 || got[0] != "hello world" {
		t.Errorf("Messages() = %v, want [\"hello world\"]", got)
	}
}

// plainResolver does not implement Recordable at all - the common case
// for a resolver with no Diagnostics ever attached, or a test fake in
// another package that only implements that package's own Resolver
// interface.
type plainResolver struct{}

func TestNoteOnNonRecordableTargetDoesNothing(t *testing.T) {
	// Must not panic, and there is nothing further to observe - this is
	// exactly the "capability not present" case Note is documented to
	// tolerate silently.
	Note(plainResolver{}, "should go nowhere")
	Note(nil, "should also go nowhere")
}

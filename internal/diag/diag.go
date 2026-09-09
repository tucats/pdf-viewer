// Package diag implements an optional, low-overhead mechanism for
// recording human-readable notes about content this project tolerates
// rather than rejects: an unsupported feature, a malformed field left at
// a fallback value, a resource name that could not be resolved.
// internal/content, internal/fonts, and internal/image each already
// tolerate exactly this kind of thing throughout (see, for example,
// internal/content/interpret.go's own doc comment on "skip, don't
// abort") - what none of them previously offered was any way for a
// caller to find out it happened at all short of noticing the rendered
// result looks wrong. This package is the plumbing behind
// pdfviewer.WithDiagnostics, which is where the full story (including
// why "off by default" matters) is told.
package diag

import (
	"fmt"
	"sync"
)

// Recorder collects diagnostic messages. Its zero value is not directly
// usable as a *Recorder (see New), but a nil *Recorder is always safe to
// call Record or Messages on - both simply do nothing, matching the "no
// diagnostics requested" case this whole package exists to make free, so
// that a call site recording a diagnostic never needs a nil check of its
// own before doing so.
type Recorder struct {
	mu       sync.Mutex
	messages []string
}

// New returns a ready-to-use Recorder collecting no messages yet.
func New() *Recorder {
	return &Recorder{}
}

// Record appends one formatted message. A nil receiver (diagnostics not
// requested for this document) does nothing - checked first, before
// format's arguments are ever evaluated into a string - so recording a
// diagnostic nobody asked for costs one nil check, not a fmt.Sprintf
// call.
func (r *Recorder) Record(format string, args ...any) {
	if r == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	r.mu.Lock()
	r.messages = append(r.messages, msg)
	r.mu.Unlock()
}

// Messages returns a copy of every message recorded so far, in the order
// Record produced them. A nil receiver returns nil, matching Record's
// own no-op behavior for the disabled case.
func (r *Recorder) Messages() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.messages))
	copy(out, r.messages)
	return out
}

// Recordable is satisfied by anything that can record a diagnostic on
// somebody else's behalf - in practice, *internal/model.Document, once a
// *Recorder has been attached to it via that package's SetDiagnostics.
// Note (below) type-asserts against this interface rather than any
// package depending on internal/model directly, exactly the same
// "accept interfaces, structurally satisfied" pattern internal/content
// and internal/fonts already use for their own Resolver interfaces (see,
// for example, internal/fonts/resolver.go's doc comment) - so a resolver
// value already being passed around for unrelated reasons (looking up a
// font, an image, a color space) can double as the diagnostics sink with
// no new parameter threaded through every call site that might want to
// record something.
type Recordable interface {
	RecordDiagnostic(format string, args ...any)
}

// Note records a diagnostic against target if it implements Recordable,
// and does nothing otherwise - including when target is nil, or is a
// resolver-shaped value that simply never had a Recorder attached (the
// default, and by far the most common, case). This mirrors the "quietly
// do nothing when a capability is not present" tolerance
// internal/content and internal/fonts already apply throughout to
// unresolvable resources - see, for example, internal/content/
// colorspace.go's setColorSpace, one of Note's own callers.
func Note(target any, format string, args ...any) {
	if r, ok := target.(Recordable); ok {
		r.RecordDiagnostic(format, args...)
	}
}

package graphics

import "testing"

func TestNewStateDefaults(t *testing.T) {
	s := NewState(Identity())
	if s.FillColor != Black || s.StrokeColor != Black {
		t.Errorf("default colors = fill %v stroke %v, want Black", s.FillColor, s.StrokeColor)
	}
	if s.LineWidth != 1 {
		t.Errorf("default LineWidth = %v, want 1", s.LineWidth)
	}
	if s.MiterLimit != 10 {
		t.Errorf("default MiterLimit = %v, want 10", s.MiterLimit)
	}
	if len(s.Clips) != 0 {
		t.Errorf("default Clips = %v, want empty", s.Clips)
	}
}

// TestStackPushPopRestoresMutations confirms the core q/Q contract:
// mutating the current state after Push, then Pop, reverts exactly to
// what was saved - the same behavior a "q ... (mutating operators) ...
// Q" sequence in a content stream must produce.
func TestStackPushPopRestoresMutations(t *testing.T) {
	stack := NewStack(NewState(Identity()))
	stack.Push()

	stack.Current().CTM = Translate(5, 5)
	stack.Current().FillColor = Color{R: 1}
	stack.Current().LineWidth = 3

	if err := stack.Pop(); err != nil {
		t.Fatalf("Pop: %v", err)
	}
	got := stack.Current()
	if got.CTM != Identity() {
		t.Errorf("CTM after Pop = %#v, want Identity()", got.CTM)
	}
	if got.FillColor != Black {
		t.Errorf("FillColor after Pop = %v, want Black", got.FillColor)
	}
	if got.LineWidth != 1 {
		t.Errorf("LineWidth after Pop = %v, want 1", got.LineWidth)
	}
}

func TestStackPopUnderflow(t *testing.T) {
	stack := NewStack(NewState(Identity()))
	if err := stack.Pop(); err == nil {
		t.Fatal("Pop on an empty stack: expected ErrStackUnderflow, got nil")
	}
}

// TestStackNestedPushPop confirms more than one level of q/Q nesting
// restores state in the correct (LIFO) order.
func TestStackNestedPushPop(t *testing.T) {
	stack := NewStack(NewState(Identity()))
	stack.Current().LineWidth = 1

	stack.Push()
	stack.Current().LineWidth = 2
	stack.Push()
	stack.Current().LineWidth = 3

	if got := stack.Current().LineWidth; got != 3 {
		t.Fatalf("LineWidth = %v, want 3", got)
	}
	stack.Pop()
	if got := stack.Current().LineWidth; got != 2 {
		t.Fatalf("LineWidth after first Pop = %v, want 2", got)
	}
	stack.Pop()
	if got := stack.Current().LineWidth; got != 1 {
		t.Fatalf("LineWidth after second Pop = %v, want 1", got)
	}
}

// TestWithClipAccumulatesAndDoesNotAliasSiblings confirms clip
// intersection accumulates across nested WithClip calls, and that two
// States cloned from a shared ancestor do not see each other's later
// clip additions - the slice-aliasing hazard WithClip's doc comment
// describes.
func TestWithClipAccumulatesAndDoesNotAliasSiblings(t *testing.T) {
	base := NewState(Identity())
	clipA := &Path{}
	clipA.AppendRect([4]Point{{0, 0}, {10, 0}, {10, 10}, {0, 10}})

	withA := base.WithClip(clipA, NonZero)
	if len(withA.Clips) != 1 {
		t.Fatalf("len(Clips) after one WithClip = %d, want 1", len(withA.Clips))
	}

	clipB := &Path{}
	clipB.AppendRect([4]Point{{2, 2}, {8, 2}, {8, 8}, {2, 8}})
	withAB := withA.WithClip(clipB, EvenOdd)
	if len(withAB.Clips) != 2 {
		t.Fatalf("len(Clips) after two WithClip calls = %d, want 2", len(withAB.Clips))
	}

	// A sibling built from the same base+clipA state must still see only
	// one clip, even though withAB was derived from it afterward.
	clipC := &Path{}
	clipC.AppendRect([4]Point{{1, 1}, {2, 2}, {3, 3}, {4, 4}})
	sibling := withA.WithClip(clipC, NonZero)
	if len(sibling.Clips) != 2 {
		t.Fatalf("len(sibling.Clips) = %d, want 2", len(sibling.Clips))
	}
	if len(withA.Clips) != 1 {
		t.Fatalf("withA.Clips mutated by sibling's WithClip: len = %d, want 1", len(withA.Clips))
	}
	if sibling.Clips[1].Path != clipC {
		t.Error("sibling's second clip is not clipC")
	}
	if withAB.Clips[1].Path != clipB {
		t.Error("withAB's second clip is not clipB")
	}
}

func TestCloneIsIndependent(t *testing.T) {
	s := NewState(Identity())
	clone := s.Clone()
	clone.LineWidth = 99
	if s.LineWidth == 99 {
		t.Error("mutating a clone affected the original")
	}
}

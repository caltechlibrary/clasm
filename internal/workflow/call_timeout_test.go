package workflow

import (
	"context"
	"testing"
	"time"
)

func TestWithCallTimeout_BoundsTheContext(t *testing.T) {
	ctx, cancel := withCallTimeout(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected the returned context to have a deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > DefaultAWSCallTimeout {
		t.Errorf("deadline %v from now, want within (0, %v]", remaining, DefaultAWSCallTimeout)
	}
}

func TestWithCallTimeout_RespectsParentCancellation(t *testing.T) {
	parent, parentCancel := context.WithCancel(context.Background())
	ctx, cancel := withCallTimeout(parent)
	defer cancel()

	parentCancel()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("child context did not observe parent cancellation")
	}
}

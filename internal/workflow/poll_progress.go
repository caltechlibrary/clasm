package workflow

import (
	"context"
	"fmt"
	"io"
	"time"
)

// pollWithProgress calls check immediately, then once every pollInterval,
// until check reports done, returns an error, or timeout elapses --
// printing progress to w along the way so a real multi-minute wait
// doesn't read as a hung prompt (PLAN.md Phase 20.53, DECISIONS.md,
// "Poll-loop progress output: fix PollSnapshotUntilComplete ahead of
// Phase 20.51's sibling poller"). check returning a non-nil error ends
// polling immediately, with no further tick printed -- the caller's own
// error already explains what happened. Shared by
// PollSnapshotUntilComplete and (once built) Phase 20.51's
// PollRestoreUntilComplete, so both inherit the same behavior from one
// place rather than each needing its own fix.
func pollWithProgress(ctx context.Context, w io.Writer, label string, timeout, pollInterval time.Duration, check func(ctx context.Context) (done bool, err error)) error {
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	fmt.Fprintf(w, "waiting for %s to complete -- this can take several minutes for a large index set\n", label)
	start := time.Now()
	for {
		done, err := check(deadline)
		if err != nil {
			return err
		}
		if done {
			return nil
		}

		select {
		case <-deadline.Done():
			return fmt.Errorf("timed out waiting for %s to complete", label)
		case <-time.After(pollInterval):
			fmt.Fprintf(w, "... %s elapsed\n", time.Since(start).Round(time.Second))
		}
	}
}

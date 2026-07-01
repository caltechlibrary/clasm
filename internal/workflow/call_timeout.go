package workflow

import (
	"context"
	"time"
)

// DefaultAWSCallTimeout bounds a single (non-polling) AWS API call, as
// an extra safety net beyond the SDK's own retry/timeout behavior (see
// DESIGN.md, "Error Handling Strategy"). Polling functions (WaitUntil*,
// WaitForSSMOnline, RunShellCommand, WaitForAMIAvailable) already bound
// themselves via their own timeout parameter and don't use this.
const DefaultAWSCallTimeout = 30 * time.Second

func withCallTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, DefaultAWSCallTimeout)
}

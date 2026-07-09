package workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// buildDeleteCommand builds a shell script that removes exactly the
// given paths -- the tool-verified list, never a re-derived one (see
// DESIGN.md, Feature 11: "the instance does not re-derive its own
// 'what's stale' list, avoiding a time-of-check/time-of-use gap").
func buildDeleteCommand(paths []string) string {
	var sb strings.Builder
	for _, p := range paths {
		fmt.Fprintf(&sb, "rm -f %s\n", shellQuote(p))
	}
	return sb.String()
}

// DeleteVerifiedFiles runs buildDeleteCommand's script via a second,
// separate SSM command -- distinct from the upload command -- deleting
// only files the tool has already independently verified in S3. A nil
// or empty paths list is a no-op; nothing is sent.
func DeleteVerifiedFiles(ctx context.Context, client awsclient.SSMAPI, instanceID string, paths []string, timeout, pollInterval time.Duration) error {
	if len(paths) == 0 {
		return nil
	}
	_, status, err := RunShellCommand(ctx, client, instanceID, buildDeleteCommand(paths), timeout, pollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return fmt.Errorf("delete command on %s failed (status: %s)", instanceID, status)
	}
	return nil
}

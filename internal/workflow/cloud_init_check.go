package workflow

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// Default timeouts for CreateInstanceFromAMI's post-launch cloud-init
// completion check. Unlike Phase 10's unbounded AMI-creation poll, this
// runs at launch time and should finish in a bounded, predictable
// window (see DECISIONS.md, "Enhance Create Instance from AMI: cloud-init
// file input + completion check").
const (
	DefaultSSMOnlineTimeout = 2 * time.Minute
	DefaultCloudInitTimeout = 10 * time.Minute
)

// CloudInitCheckResult reports the outcome of waiting for a freshly
// launched instance's cloud-init run to finish. Skipped is true when SSM
// never came online -- not every AMI has SSM configured, so that's a
// clean skip, not an error. Status is "done", "degraded" (cloud-init exit 2:
// finished, with only non-fatal recoverable_errors) or "error". Detail is
// cloud-init's own errors/recoverable_errors text, set for the last two.
type CloudInitCheckResult struct {
	Skipped bool
	Status  string
	Detail  string
}

// checkCloudInitCompletion waits for SSM to report the instance Online,
// then runs `cloud-init status --wait --long` via SSM and classifies the
// result, reporting cloud-init's actual completion status rather than
// only the EC2-level running state (see DESIGN.md, Feature 2, step 6).
// The `status:` line of the output decides, not the invocation status:
// "degraded" exits 2, which SSM reports as Failed.
func checkCloudInitCompletion(ctx context.Context, client awsclient.SSMAPI, instanceID string, onlineTimeout, commandTimeout, pollInterval time.Duration) (CloudInitCheckResult, error) {
	online, err := WaitForSSMOnline(ctx, client, instanceID, onlineTimeout, pollInterval)
	if err != nil {
		return CloudInitCheckResult{}, err
	}
	if !online {
		return CloudInitCheckResult{Skipped: true}, nil
	}

	stdout, status, err := RunShellCommand(ctx, client, instanceID, "cloud-init status --wait --long", commandTimeout, pollInterval)
	if err != nil {
		return CloudInitCheckResult{}, err
	}
	detail := cloudInitErrorText(stdout)
	switch {
	case status == types.CommandInvocationStatusSuccess && cloudInitStatusLine(stdout) == "done":
		return CloudInitCheckResult{Status: "done"}, nil
	case cloudInitStatusLine(stdout) == "degraded" && !cloudInitHasErrors(stdout):
		return CloudInitCheckResult{Status: "degraded", Detail: detail}, nil
	}
	return CloudInitCheckResult{Status: "error", Detail: detail}, nil
}

// cloudInitStatusLine returns the value of the `status:` line of cloud-init's
// output, or "" if there is none.
func cloudInitStatusLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, "status:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// cloudInitHasErrors reports whether the `errors:` field holds anything: an
// inline value other than an empty list/map, or an indented block below it.
func cloudInitHasErrors(out string) bool {
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		v, ok := strings.CutPrefix(line, "errors:")
		if !ok {
			continue
		}
		if v = strings.TrimSpace(v); v != "" {
			return v != "[]" && v != "{}"
		}
		return i+1 < len(lines) && (strings.HasPrefix(lines[i+1], "\t") || strings.HasPrefix(lines[i+1], " ") || strings.HasPrefix(lines[i+1], "-"))
	}
	return false
}

// cloudInitErrorText returns the part of cloud-init's output from the `errors:`
// (or `recoverable_errors:`) field on, which is where its own explanation is,
// or all of out if neither is present.
func cloudInitErrorText(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "errors:") || strings.HasPrefix(line, "recoverable_errors:") {
			out = strings.Join(lines[i:], "\n")
			break
		}
	}
	out = strings.TrimSpace(out)
	return out
}

// reportCloudInit prints the operator-facing line(s) for a completion check.
func reportCloudInit(w io.Writer, r CloudInitCheckResult) {
	switch {
	case r.Skipped:
		fmt.Fprintln(w, "SSM never came online; skipping the cloud-init completion check.")
	case r.Status == "done":
		fmt.Fprintln(w, "cloud-init completed successfully.")
	case r.Status == "degraded":
		fmt.Fprintln(w, "cloud-init completed with warnings (degraded) -- not a failure; cloud-init reported:")
		fmt.Fprintln(w, indentLines(r.Detail))
	default:
		fmt.Fprintln(w, "cloud-init reported an error:")
		if r.Detail != "" {
			fmt.Fprintln(w, indentLines(r.Detail))
		}
		fmt.Fprintln(w, "Check the instance before using it.")
	}
}

func indentLines(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n  ")
}

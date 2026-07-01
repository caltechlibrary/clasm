package workflow

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/awstools/internal/awsclient"
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
// clean skip, not an error.
type CloudInitCheckResult struct {
	Skipped bool
	Status  string // "done" or "error"
}

// checkCloudInitCompletion waits for SSM to report the instance Online,
// then runs `cloud-init status --wait` via SSM and classifies the
// result, reporting cloud-init's actual completion status rather than
// only the EC2-level running state (see DESIGN.md, Feature 2, step 6).
func checkCloudInitCompletion(ctx context.Context, client awsclient.SSMAPI, instanceID string, onlineTimeout, commandTimeout, pollInterval time.Duration) (CloudInitCheckResult, error) {
	online, err := WaitForSSMOnline(ctx, client, instanceID, onlineTimeout, pollInterval)
	if err != nil {
		return CloudInitCheckResult{}, err
	}
	if !online {
		return CloudInitCheckResult{Skipped: true}, nil
	}

	stdout, status, err := RunShellCommand(ctx, client, instanceID, "cloud-init status --wait", commandTimeout, pollInterval)
	if err != nil {
		return CloudInitCheckResult{}, err
	}
	if status != types.CommandInvocationStatusSuccess || strings.Contains(stdout, "status: error") {
		return CloudInitCheckResult{Status: "error"}, nil
	}
	return CloudInitCheckResult{Status: "done"}, nil
}

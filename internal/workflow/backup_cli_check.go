package workflow

import (
	"context"
	"fmt"
	"time"

	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// CheckAWSCLIAvailable runs a quick `command -v aws` preflight check on
// instanceID via SSM -- before Backup Archive & Trim's directory/age/
// bucket prompts and the potentially large dry-run list -- so a missing
// AWS CLI (this project's most common real-AWS failure so far) surfaces
// as one clear, immediate error instead of every subsequent upload
// silently reporting FAIL with the real cause buried in -debug output
// (see DECISIONS.md, "Preflight check: AWS CLI availability before
// Backup Archive & Trim").
func CheckAWSCLIAvailable(ctx context.Context, client awsclient.SSMAPI, instanceID string, timeout, pollInterval time.Duration) error {
	_, status, err := RunShellCommand(ctx, client, instanceID, "command -v aws", timeout, pollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return fmt.Errorf("AWS CLI not found on instance %s -- install it before running Backup Archive & Trim", instanceID)
	}
	return nil
}

package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// CloudInitExtractionInstanceType is the smallest generally-available
// instance type used for the disposable extraction instance.
const CloudInitExtractionInstanceType = "t3.micro"

// DefaultCloudInitExtractionTimeout bounds every wait in
// ExtractCloudInitFromAMI -- this is a diagnostic side-operation, not
// core AMI creation, so it must fail cleanly rather than poll
// unboundedly like Phase 10's WaitForAMIAvailable (see DESIGN.md,
// Feature 10).
const DefaultCloudInitExtractionTimeout = 3 * time.Minute

func launchDisposableInstance(ctx context.Context, client awsclient.EC2API, imageID string) (string, error) {
	input := &ec2.RunInstancesInput{
		ImageId:      aws.String(imageID),
		InstanceType: types.InstanceType(CloudInitExtractionInstanceType),
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		TagSpecifications: []types.TagSpecification{buildTagSpecification(types.ResourceTypeInstance, map[string]string{
			"Name":    "cloud-init-extraction-temp",
			"Purpose": "cloud-init-extraction",
		})},
	}
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.RunInstances(ctx, input)
	if err != nil {
		return "", err
	}
	if len(out.Instances) == 0 {
		return "", errors.New("RunInstances returned no instances")
	}
	return aws.ToString(out.Instances[0].InstanceId), nil
}

// ExtractCloudInitFromAMI launches a temporary, disposable instance from
// imageID, waits for it to reach running and for SSM to report Online
// (both bounded by timeout), reads /var/lib/cloud/instance/user-data.txt
// via SSM, and always terminates the temporary instance afterward --
// including when SSM never comes online or the command fails. Cleanup
// runs via defer against a cleanup-scoped context, decoupled from ctx,
// so it isn't skipped by an early return or by ctx itself being
// cancelled (see DESIGN.md, Feature 10, and Security Considerations).
func ExtractCloudInitFromAMI(ctx context.Context, ec2Client awsclient.EC2API, ssmClient awsclient.SSMAPI, imageID string, timeout, pollInterval time.Duration) (string, error) {
	instanceID, err := launchDisposableInstance(ctx, ec2Client, imageID)
	if err != nil {
		return "", err
	}

	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = TerminateInstance(cleanupCtx, ec2Client, instanceID)
	}()

	if _, err := WaitUntilRunning(ctx, ec2Client, instanceID, timeout, pollInterval); err != nil {
		return "", err
	}

	online, err := WaitForSSMOnline(ctx, ssmClient, instanceID, timeout, pollInterval)
	if err != nil {
		return "", err
	}
	if !online {
		return "", fmt.Errorf("SSM never came online on temporary instance %s", instanceID)
	}

	stdout, status, err := RunShellCommand(ctx, ssmClient, instanceID, "cat /var/lib/cloud/instance/user-data.txt", timeout, pollInterval)
	if err != nil {
		return "", err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return "", fmt.Errorf("reading user-data from %s failed (status: %s)", instanceID, status)
	}
	return stdout, nil
}

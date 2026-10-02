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
	"github.com/aws/smithy-go"

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

func launchDisposableInstance(ctx context.Context, client awsclient.EC2API, imageID, securityGroupID string) (string, error) {
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
	if securityGroupID != "" {
		input.SecurityGroupIds = []string{securityGroupID}
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

// amiUserDataCommand prints the user-data of the instance the AMI was taken from.
// It is plain POSIX sh -- SSM's AWS-RunShellScript runs commands under /bin/sh.
//
// Not /var/lib/cloud/instance/user-data.txt: on boot cloud-init points
// /var/lib/cloud/instance at the NEW instance's directory, so that file is the
// temporary instance's own user-data, which is empty (confirmed on a real
// AMI-launched instance, 2026-09-30). The AMI's source is the most recently
// modified directory under instances/ other than the current one. Its file is
// the raw user-data as given, which for clasm-made templates is gzip (first
// bytes 1f 8b), so it is decompressed when it is. Finding nothing -- no other
// instance directory, no file, an empty file -- prints nothing and succeeds; the
// caller reports "no user-data found". An older ancestor's user-data is never
// substituted for a source that had none.
const amiUserDataCommand = `cur=$(basename "$(readlink /var/lib/cloud/instance)")
src=$(ls -1t /var/lib/cloud/instances | grep -v -x "$cur" | head -n 1)
f=/var/lib/cloud/instances/$src/user-data.txt
if [ -n "$src" ] && [ -s "$f" ]; then
  if [ "$(od -An -tx1 -N2 "$f" | tr -d ' \n')" = 1f8b ]; then gzip -dc "$f"; else cat "$f"; fi
fi
exit 0`

// ExtractCloudInitFromAMI launches a temporary, disposable instance from
// imageID, waits for it to reach running and for SSM to report Online
// (both bounded by timeout), reads the source instance's user-data
// (amiUserDataCommand) via SSM, and always terminates the temporary instance afterward --
// including when SSM never comes online or the command fails. Cleanup
// runs via defer against a cleanup-scoped context, decoupled from ctx,
// so it isn't skipped by an early return or by ctx itself being
// cancelled (see DESIGN.md, Feature 10, and Security Considerations).
func ExtractCloudInitFromAMI(ctx context.Context, ec2Client awsclient.EC2API, ssmClient awsclient.SSMAPI, imageID, securityGroupID string, timeout, pollInterval time.Duration) (string, error) {
	// Refuse before launching anything billable if the instance could not reach
	// SSM (found live 2026-09-30: a default group with no outbound rules made
	// every extraction time out after three minutes).
	if err := checkExtractionSecurityGroup(ctx, ec2Client, securityGroupID); err != nil {
		return "", err
	}
	instanceID, err := launchDisposableInstance(ctx, ec2Client, imageID, securityGroupID)
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
		return "", fmt.Errorf("SSM never came online on temporary instance %s; it was launched into %s, whose outbound rules must allow HTTPS to the SSM endpoints", instanceID, describeExtractionGroup(securityGroupID))
	}

	stdout, status, err := RunShellCommand(ctx, ssmClient, instanceID, amiUserDataCommand, timeout, pollInterval)
	if err != nil {
		return "", err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return "", fmt.Errorf("reading user-data from %s failed (status: %s)", instanceID, status)
	}
	return stdout, nil
}

// describeExtractionGroup names the group for messages.
func describeExtractionGroup(securityGroupID string) string {
	if securityGroupID == "" {
		return "the VPC's default security group"
	}
	return "security group " + securityGroupID
}

// extractionGroupAdvice is the fix, repeated wherever the group is at fault.
const extractionGroupAdvice = "name an existing group that allows outbound HTTPS: set this region's cloud_init_extraction_security_groups entry in ~/.clasm, or pass -security-group <sg-id>"

// checkExtractionSecurityGroup refuses, as a *UsageError, a group that would
// leave the disposable instance unable to reach the SSM endpoints: one with no
// outbound rule allowing HTTPS (port 443). With securityGroupID empty the group
// is the default VPC's default group, which is what RunInstances picks; a named
// group must exist. It blocks only on positive evidence: when the default VPC or
// its group cannot be found, or a lookup fails, the launch proceeds as it always
// did, since it cannot be shown to fail.
func checkExtractionSecurityGroup(ctx context.Context, client awsclient.EC2API, securityGroupID string) error {
	if securityGroupID != "" {
		ctx, cancel := withCallTimeout(ctx)
		defer cancel()
		out, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: []string{securityGroupID}})
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "InvalidGroup.NotFound" || apiErr.ErrorCode() == "InvalidGroupId.Malformed") {
				return &UsageError{Msg: fmt.Sprintf("security group %s for the temporary instance was not found in this region", securityGroupID)}
			}
			return err // a failed lookup is not the caller's mistake
		}
		for _, g := range out.SecurityGroups {
			if aws.ToString(g.GroupId) != securityGroupID {
				continue
			}
			if !allowsHTTPSEgress(g) {
				return &UsageError{Msg: fmt.Sprintf("security group %s has no outbound rule allowing HTTPS (port 443), so the temporary instance's SSM agent could not register; %s", securityGroupID, extractionGroupAdvice)}
			}
			return nil
		}
		return &UsageError{Msg: fmt.Sprintf("security group %s for the temporary instance was not found in this region", securityGroupID)}
	}

	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	subnets, err := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []types.Filter{{Name: aws.String("default-for-az"), Values: []string{"true"}}},
	})
	if err != nil {
		return nil
	}
	vpcID := ""
	for _, s := range subnets.Subnets {
		if aws.ToBool(s.DefaultForAz) && aws.ToString(s.VpcId) != "" {
			vpcID = aws.ToString(s.VpcId)
			break
		}
	}
	if vpcID == "" {
		return nil
	}
	groups, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
			{Name: aws.String("group-name"), Values: []string{"default"}},
		},
	})
	if err != nil {
		return nil
	}
	for _, g := range groups.SecurityGroups {
		if aws.ToString(g.GroupName) != "default" || aws.ToString(g.VpcId) != vpcID {
			continue
		}
		if !allowsHTTPSEgress(g) {
			return &UsageError{Msg: fmt.Sprintf("the VPC's default security group %s has no outbound rule allowing HTTPS (port 443), so the temporary instance's SSM agent could not register and the extraction would time out; %s", aws.ToString(g.GroupId), extractionGroupAdvice)}
		}
		return nil
	}
	return nil
}

// allowsHTTPSEgress reports whether g has an outbound rule covering TCP 443: all
// traffic, or TCP with a port range that includes it. The destination is not
// examined: a group may send HTTPS only to a VPC endpoint.
func allowsHTTPSEgress(g types.SecurityGroup) bool {
	for _, p := range g.IpPermissionsEgress {
		switch aws.ToString(p.IpProtocol) {
		case "-1":
			return true
		case "tcp", "6":
			if aws.ToInt32(p.FromPort) <= 443 && 443 <= aws.ToInt32(p.ToPort) {
				return true
			}
		}
	}
	return false
}

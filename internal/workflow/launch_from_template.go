package workflow

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// CreateInstanceFromLaunchTemplate runs the Create EC2 Instance from
// Launch Template workflow (DESIGN.md, "Launch Templates"): pick a
// template, pick a version (pre-filled to $Default, editable), and
// launch -- a third peer entry alongside Create EC2 Instance from AMI/
// Cloud-Init YAML, not a hybrid that also lets the operator override
// individual template fields. Collects nothing else; the template
// supplies every other launch parameter.
func CreateInstanceFromLaunchTemplate(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, templates []inventory.LaunchTemplate) error {
	if len(templates) == 0 {
		fmt.Fprintln(w, "No launch templates found.")
		return nil
	}
	lt, err := pickLaunchTemplate(ctx, "Select a launch template", "", templates)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return createInstanceFromLaunchTemplate(ctx, w, clients, lt, nil, nil)
}

// createInstanceFromLaunchTemplate is
// CreateInstanceFromLaunchTemplate's testable core, once a template is
// resolved -- template selection runs a real bubbletea Program
// (tui.RunPicker) that can't be pipe-tested, same limitation as every
// other Picker-tier conversion in this package.
func createInstanceFromLaunchTemplate(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, lt inventory.LaunchTemplate, input io.Reader, output io.Writer) error {
	client, err := resolveEC2(clients, lt.Region)
	if err != nil {
		return err
	}

	version, err := promptLaunchTemplateVersion(input, output)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "\nAbout to launch an instance from template %s (%s), version %s\n", lt.TemplateID, lt.Name, version)
	ok, err := Confirm("Launch this instance?", WithConfirmIO(input, output))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	instanceID, err := launchFromTemplate(ctx, client, lt.TemplateID, version)
	if err != nil {
		return fmt.Errorf("launching instance from template: %w", err)
	}

	fmt.Fprintf(w, "Launched %s, waiting for it to reach running...\n", instanceID)
	inst, err := WaitUntilRunning(ctx, client, instanceID, DefaultLaunchTimeout, DefaultLaunchPollInterval)
	if err != nil {
		return err
	}
	displayConnectionInfo(w, instanceID, inst)
	return nil
}

// launchFromTemplate calls ec2:RunInstances scoped to a single launch
// template/version -- everything else (AMI, instance type, key pair,
// security groups, subnet, IAM profile, tags, user-data, IMDSv2) comes
// from the template itself. Scopes its own withCallTimeout internally
// (matching Launch's own pattern in launch_execute.go) rather than
// leaving it to the caller -- a caller-scoped timeout context that gets
// its cancel() called and is then reused for the much longer
// WaitUntilRunning poll is exactly the bug reported 2026-07-20: every
// DescribeInstances call failed instantly with "context canceled"
// because the timeout context had already been canceled before
// WaitUntilRunning ever ran.
func launchFromTemplate(ctx context.Context, client awsclient.EC2API, templateID, version string) (string, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		LaunchTemplate: &types.LaunchTemplateSpecification{
			LaunchTemplateId: aws.String(templateID),
			Version:          aws.String(version),
		},
		MinCount: aws.Int32(1),
		MaxCount: aws.Int32(1),
	})
	if err != nil {
		return "", err
	}
	if len(out.Instances) == 0 {
		return "", fmt.Errorf("RunInstances returned no instances")
	}
	return aws.ToString(out.Instances[0].InstanceId), nil
}

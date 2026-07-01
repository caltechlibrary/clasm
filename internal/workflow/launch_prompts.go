package workflow

import (
	"context"
	"fmt"
	"strconv"

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/ui"
)

func subnetLabel(s SubnetInfo) string {
	return fmt.Sprintf("%s (%s, %s, %s)", s.SubnetID, s.VpcID, s.AvailabilityZone, s.CIDR)
}

// promptSubnetID lists subnets available in client's region and lets
// the user pick one (DESIGN.md, Feature 2: "Subnet ID (list available
// subnets)"). Falls back to a free-text prompt if the list can't be
// fetched or is empty.
func promptSubnetID(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, client awsclient.EC2API) (string, error) {
	subnets, err := listSubnets(ctx, client)
	if err != nil || len(subnets) == 0 {
		return ui.Prompt(t, le, "Subnet ID", ui.WithValidator(requireNonEmpty))
	}
	picked, err := ui.PickList(t, le, subnets, subnetLabel, "Select a subnet")
	if err != nil {
		return "", err
	}
	return picked.SubnetID, nil
}

func securityGroupLabel(g SecurityGroupInfo) string {
	if g.Description == "" {
		return fmt.Sprintf("%s (%s)", g.GroupID, g.GroupName)
	}
	return fmt.Sprintf("%s (%s) - %s", g.GroupID, g.GroupName, g.Description)
}

// promptSecurityGroupIDs lists security groups available in client's
// region (DESIGN.md, Feature 2: "Security group IDs (list available
// security groups)") and accepts a comma-separated mix of numbers
// (referencing the displayed list) and/or raw IDs, resolving numbers to
// their real sg-xxxx IDs -- security groups allow multiple selections,
// unlike the single-select key pair/subnet prompts, so this can't just
// be a PickList call. Typing a security group *name* instead of its ID
// here (the mistake this list exists to prevent) fails later at
// ec2:RunInstances with AWS's own "groupName cannot be used with
// subnet" error, not a validation error here -- this function only
// validates that a referenced number is in range, not that a raw string
// is a real ID. Falls back to the original free-text prompt if the list
// can't be fetched or is empty.
func promptSecurityGroupIDs(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, client awsclient.EC2API) ([]string, error) {
	groups, err := listSecurityGroups(ctx, client)
	if err != nil || len(groups) == 0 {
		raw, err := ui.Prompt(t, le, "Security group IDs (comma-separated)", ui.WithValidator(requireAtLeastOneSecurityGroup))
		if err != nil {
			return nil, err
		}
		return splitCSV(raw), nil
	}

	for i, g := range groups {
		t.Printf("%3d) %s\n", i+1, securityGroupLabel(g))
	}
	t.Refresh()

	validate := func(s string) error {
		tokens := splitCSV(s)
		if len(tokens) == 0 {
			return fmt.Errorf("at least one security group is required")
		}
		for _, tok := range tokens {
			if n, convErr := strconv.Atoi(tok); convErr == nil {
				if n < 1 || n > len(groups) {
					return fmt.Errorf("invalid selection %d: choose 1-%d", n, len(groups))
				}
			}
		}
		return nil
	}

	raw, err := ui.Prompt(t, le, "Security group IDs (numbers above and/or raw IDs, comma-separated)", ui.WithValidator(validate))
	if err != nil {
		return nil, err
	}

	tokens := splitCSV(raw)
	ids := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if n, convErr := strconv.Atoi(tok); convErr == nil {
			ids = append(ids, groups[n-1].GroupID)
			continue
		}
		ids = append(ids, tok)
	}
	return ids, nil
}

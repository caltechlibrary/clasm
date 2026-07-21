package workflow

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/tui"
	"github.com/caltechlibrary/clasm/internal/ui"
)

func subnetLabel(s SubnetInfo) string {
	return fmt.Sprintf("%s (%s, %s, %s)", s.SubnetID, s.VpcID, s.AvailabilityZone, s.CIDR)
}

// pickSubnet runs a Picker-tier tui.RunPicker (DESIGN.md's full
// conversion punch list) over subnets and returns the chosen one. Like
// pickInstance/pickImage, this drives a real bubbletea Program that
// can't be pipe-tested -- promptSubnetID's own list-path tests were
// retired for this reason; filterSubnetsByInstanceTypeAZ's own tests
// (instance_type_az_check_test.go) cover the pre-picker filtering logic
// directly.
func pickSubnet(ctx context.Context, title string, subnets []SubnetInfo) (SubnetInfo, error) {
	rows := make([]string, len(subnets))
	for i, s := range subnets {
		rows[i] = subnetLabel(s)
	}

	idx, err := tui.RunPicker(ctx, tui.PickerConfig{
		Title:        title,
		Description:  "The subnet's Availability Zone determines which instance types are available to this instance.",
		Rows:         rows,
		ColorEnabled: ui.ColorEnabled(),
	})
	if err != nil {
		return SubnetInfo{}, err
	}
	return subnets[idx], nil
}

// promptSubnetID lists subnets available in client's region, narrowed to
// those whose Availability Zone actually supports instanceType (DESIGN.md,
// Feature 2: "Subnet ID (list available subnets)"; see DECISIONS.md,
// "Filter the subnet picker by instance-type Availability Zone support"
// -- instance type is chosen earlier in the flow, so this list can be
// pre-filtered instead of discovering an incompatibility only after the
// fact). Falls back to a free-text prompt if the list can't be fetched
// or is empty -- in which case the returned SubnetInfo's AvailabilityZone
// is empty, signaling "unknown" to ensureInstanceTypeSupportedInSubnet
// (it skips its check rather than treating an unknown AZ as an
// incompatibility). That reactive check remains as a safety net for
// cases this filtering can't cover (e.g. the AZ-offerings lookup itself
// fails, or the free-text fallback was used).
func promptSubnetID(ctx context.Context, w io.Writer, client awsclient.EC2API, instanceType string, input io.Reader, output io.Writer) (SubnetInfo, error) {
	subnets, err := listSubnets(ctx, client)
	if err != nil || len(subnets) == 0 {
		id, err := ui.Prompt("Subnet ID", ui.WithValidator(requireNonEmpty), ui.WithIO(input, output))
		if err != nil {
			return SubnetInfo{}, err
		}
		return SubnetInfo{SubnetID: id}, nil
	}

	subnets = filterSubnetsByInstanceTypeAZ(ctx, client, instanceType, subnets)

	return pickSubnet(ctx, "Select a subnet", subnets)
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
func promptSecurityGroupIDs(ctx context.Context, w io.Writer, client awsclient.EC2API, input io.Reader, output io.Writer) ([]string, error) {
	groups, err := listSecurityGroups(ctx, client)
	if err != nil || len(groups) == 0 {
		raw, err := ui.Prompt("Security group IDs (comma-separated)", ui.WithValidator(requireAtLeastOneSecurityGroup), ui.WithIO(input, output))
		if err != nil {
			return nil, err
		}
		return splitCSV(raw), nil
	}

	for i, g := range groups {
		fmt.Fprintf(w, "%3d) %s\n", i+1, securityGroupLabel(g))
	}

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

	raw, err := ui.Prompt("Security group IDs (numbers above and/or raw IDs, comma-separated)", ui.WithValidator(validate), ui.WithIO(input, output))
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

// instanceTypeChoice is one entry in promptInstanceType's pick list;
// value is empty for the "Other" entry.
type instanceTypeChoice struct {
	value string
	label string
}

// curatedInstanceTypes is a short, hand-picked list of instance types
// relevant to this team's actual usage (Invenio RDM deployments and
// general EC2 ops) -- not an exhaustive AWS catalog. AWS offers 600+
// instance types per region; listing them all would reproduce (at a
// much larger scale) the "flat list is noise, not help" problem already
// found with key pairs (DECISIONS.md, "Support creating a new key pair
// from within awsops"). "Other" always stays available as an escape
// hatch to any value not listed here. See DECISIONS.md, "Instance type
// pick list: curated shortlist, not the full AWS catalog".
//
// t3/m5/c5/r5 are all Nitro-based and require Enhanced Networking (ENA)
// -- every one of them fails ensureInstanceTypeENACompatible against an
// AMI that isn't ENA-enabled, which real-world use surfaced as a launch
// blocked entirely for a legacy, non-ENA-enabled AMI with no curated
// type that could ever work with it. t2.micro/t2.medium are included
// specifically as non-Nitro, no-ENA-required options for that case (see
// DECISIONS.md, "Add non-ENA-required options to the curated instance
// type list").
var curatedInstanceTypes = []instanceTypeChoice{
	{value: "t3.micro", label: "t3.micro (2 vCPU, 1 GiB) -- low-cost testing (requires ENA)"},
	{value: "t3.small", label: "t3.small (2 vCPU, 2 GiB) (requires ENA)"},
	{value: "t3.medium", label: "t3.medium (2 vCPU, 4 GiB) (requires ENA)"},
	{value: "t3.large", label: "t3.large (2 vCPU, 8 GiB) -- typical small Invenio RDM instance (requires ENA)"},
	{value: "t3.xlarge", label: "t3.xlarge (4 vCPU, 16 GiB) (requires ENA)"},
	{value: "m5.large", label: "m5.large (2 vCPU, 8 GiB) -- steady-state, non-burstable (requires ENA)"},
	{value: "m5.xlarge", label: "m5.xlarge (4 vCPU, 16 GiB) (requires ENA)"},
	{value: "c5.large", label: "c5.large (2 vCPU, 4 GiB) -- compute-optimized (requires ENA)"},
	{value: "r5.large", label: "r5.large (2 vCPU, 16 GiB) -- memory-optimized (requires ENA)"},
	{value: "t2.micro", label: "t2.micro (1 vCPU, 1 GiB) -- no ENA required, works with older/legacy AMIs"},
	{value: "t2.medium", label: "t2.medium (2 vCPU, 4 GiB) -- no ENA required, works with older/legacy AMIs"},
}

// promptInstanceType offers curatedInstanceTypes as a huh.Select, plus
// "Other" to type any instance type not listed (DESIGN.md, Feature 2:
// "Instance type"; DESIGN.md's full conversion punch list). No AWS call
// is made here -- the list is static; the instance-type-vs-subnet-
// Availability-Zone pre-flight check (instance_type_az_check.go) is what
// actually validates the chosen value against AWS. input/output are nil
// in production (interactive, real terminal) and supplied by tests for
// the accessible-mode pipe path.
func promptInstanceType(w io.Writer, input io.Reader, output io.Writer) (string, error) {
	choices := make([]instanceTypeChoice, 0, len(curatedInstanceTypes)+1)
	choices = append(choices, curatedInstanceTypes...)
	choices = append(choices, instanceTypeChoice{label: "Other (type a custom instance type)"})

	picked, err := pickComparable(w, "Select an instance type", "Pick a curated size, or Other to type any instance type by name.", hintCancel, choices, func(c instanceTypeChoice) string { return c.label }, input, output)
	if err != nil {
		return "", err
	}
	if picked.value != "" {
		return picked.value, nil
	}
	return ui.Prompt("Instance type", ui.WithValidator(requireNonEmpty), ui.WithIO(input, output))
}

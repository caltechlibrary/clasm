package workflow

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/ui"
)

// instanceTypeOfferedInAZ reports whether instanceType can be launched
// in az, via ec2:DescribeInstanceTypeOfferings -- this is the pre-flight
// check for AWS's own "Unsupported: ... instance type ... is not
// supported in ... Availability Zone" RunInstances error (see
// DECISIONS.md, "Pre-flight check: instance type vs. subnet
// Availability Zone").
func instanceTypeOfferedInAZ(ctx context.Context, client awsclient.EC2API, instanceType, az string) (bool, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DescribeInstanceTypeOfferings(ctx, &ec2.DescribeInstanceTypeOfferingsInput{
		LocationType: types.LocationTypeAvailabilityZone,
		Filters: []types.Filter{
			{Name: aws.String("location"), Values: []string{az}},
			{Name: aws.String("instance-type"), Values: []string{instanceType}},
		},
	})
	if err != nil {
		return false, err
	}
	return len(out.InstanceTypeOfferings) > 0, nil
}

// instanceTypeOfferedAZs lists the Availability Zones (in the region
// client is scoped to) where instanceType is offered, best-effort, for
// a helpful message when instanceTypeOfferedInAZ reports false.
func instanceTypeOfferedAZs(ctx context.Context, client awsclient.EC2API, instanceType string) ([]string, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DescribeInstanceTypeOfferings(ctx, &ec2.DescribeInstanceTypeOfferingsInput{
		LocationType: types.LocationTypeAvailabilityZone,
		Filters: []types.Filter{
			{Name: aws.String("instance-type"), Values: []string{instanceType}},
		},
	})
	if err != nil {
		return nil, err
	}
	azs := make([]string, 0, len(out.InstanceTypeOfferings))
	for _, o := range out.InstanceTypeOfferings {
		azs = append(azs, aws.ToString(o.Location))
	}
	return azs, nil
}

// filterSubnetsByInstanceTypeAZ narrows subnets to those whose
// Availability Zone actually offers instanceType, so promptSubnetID's
// pick list never shows an incompatible subnet in the first place --
// the common case then never needs ensureInstanceTypeSupportedInSubnet's
// reactive recovery flow at all (see DECISIONS.md, "Filter the subnet
// picker by instance-type Availability Zone support"). Best-effort:
// returns subnets unfiltered if the AZ-offerings lookup errors, or if
// filtering would leave nothing to pick from -- either way,
// ensureInstanceTypeSupportedInSubnet remains the safety net, since an
// empty or lookup-failed picker is worse than an unfiltered one.
func filterSubnetsByInstanceTypeAZ(ctx context.Context, client awsclient.EC2API, instanceType string, subnets []SubnetInfo) []SubnetInfo {
	azs, err := instanceTypeOfferedAZs(ctx, client, instanceType)
	if err != nil || len(azs) == 0 {
		return subnets
	}

	offered := make(map[string]bool, len(azs))
	for _, az := range azs {
		offered[az] = true
	}

	filtered := make([]SubnetInfo, 0, len(subnets))
	for _, s := range subnets {
		if offered[s.AvailabilityZone] {
			filtered = append(filtered, s)
		}
	}
	if len(filtered) == 0 {
		return subnets
	}
	return filtered
}

// incompatibilityChoice is one option offered by
// ensureInstanceTypeSupportedInSubnet when it finds an incompatible
// instance-type/Availability-Zone pairing.
type incompatibilityChoice struct {
	label string
	kind  incompatibilityChoiceKind
}

type incompatibilityChoiceKind int

const (
	incompatibilityChangeInstanceType incompatibilityChoiceKind = iota
	incompatibilityChangeSubnet
	incompatibilityAbort
)

func incompatibilityChoiceLabel(c incompatibilityChoice) string { return c.label }

var instanceTypeAZIncompatibilityChoices = []incompatibilityChoice{
	{label: "Change instance type", kind: incompatibilityChangeInstanceType},
	{label: "Pick a different subnet", kind: incompatibilityChangeSubnet},
	{label: "Abort this launch", kind: incompatibilityAbort},
}

// ensureInstanceTypeSupportedInSubnet checks instanceType against the
// picked subnet's Availability Zone and, if AWS wouldn't actually accept
// that pairing, offers a pick list to correct it -- change instance
// type, pick a different subnet, or abort -- rather than either
// blocking with a dead-end error or silently sending a doomed
// RunInstances call (see DECISIONS.md, "Pre-flight check: instance type
// vs. subnet Availability Zone"). Returns the (possibly updated)
// instance type and subnet, or ui.ErrCancelled if the operator chooses
// to abort (handled the same way as every other cancelled confirmation
// in this tool). Skips the check entirely once the subnet's AZ is
// unknown (e.g. promptSubnetID's free-text fallback) or the check
// itself errors -- consistent with this tool's other best-effort
// diagnostics (e.g. SSM-unavailable fallbacks) that never block the
// whole flow over a check that couldn't be performed.
func ensureInstanceTypeSupportedInSubnet(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, client awsclient.EC2API, instanceType string, subnet SubnetInfo) (string, SubnetInfo, error) {
	for subnet.AvailabilityZone != "" {
		ok, err := instanceTypeOfferedInAZ(ctx, client, instanceType, subnet.AvailabilityZone)
		if err != nil || ok {
			return instanceType, subnet, nil
		}

		t.Printf("Instance type %q is not offered in %s (subnet %q's Availability Zone).\n", instanceType, subnet.AvailabilityZone, subnet.SubnetID)
		if azs, azErr := instanceTypeOfferedAZs(ctx, client, instanceType); azErr == nil && len(azs) > 0 {
			t.Printf("It is offered in: %s\n", strings.Join(azs, ", "))
		}
		t.Refresh()

		choice, err := ui.PickList(t, le, instanceTypeAZIncompatibilityChoices, incompatibilityChoiceLabel, "How would you like to proceed?")
		if err != nil {
			return "", SubnetInfo{}, err
		}

		switch choice.kind {
		case incompatibilityChangeInstanceType:
			newType, err := promptInstanceType(t, le)
			if err != nil {
				return "", SubnetInfo{}, err
			}
			instanceType = newType
		case incompatibilityChangeSubnet:
			newSubnet, err := promptSubnetID(ctx, t, le, client, instanceType)
			if err != nil {
				return "", SubnetInfo{}, err
			}
			subnet = newSubnet
		case incompatibilityAbort:
			return "", SubnetInfo{}, ui.ErrCancelled
		}
	}
	return instanceType, subnet, nil
}

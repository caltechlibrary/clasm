package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/caltechlibrary/clasm/internal/ui"
)

func TestInstanceTypeOfferedInAZ_True(t *testing.T) {
	fake := &fakeEC2Client{instanceTypeOfferings: map[string][]string{
		"t3.micro": {"us-west-2a", "us-west-2b"},
	}}
	ok, err := instanceTypeOfferedInAZ(context.Background(), fake, "t3.micro", "us-west-2a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("got false, want true")
	}
}

func TestInstanceTypeOfferedInAZ_False(t *testing.T) {
	fake := &fakeEC2Client{instanceTypeOfferings: map[string][]string{
		"t2.micro": {"us-west-2a", "us-west-2b", "us-west-2c"},
	}}
	ok, err := instanceTypeOfferedInAZ(context.Background(), fake, "t2.micro", "us-west-2d")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("got true, want false")
	}
}

func TestInstanceTypeOfferedInAZ_PropagatesError(t *testing.T) {
	fake := &fakeEC2Client{describeInstanceTypeOfferingsErr: errors.New("boom")}
	_, err := instanceTypeOfferedInAZ(context.Background(), fake, "t2.micro", "us-west-2d")
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestInstanceTypeOfferedAZs_ListsAZs(t *testing.T) {
	fake := &fakeEC2Client{instanceTypeOfferings: map[string][]string{
		"t2.micro": {"us-west-2a", "us-west-2b", "us-west-2c"},
	}}
	got, err := instanceTypeOfferedAZs(context.Background(), fake, "t2.micro")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %v, want 3 AZs", got)
	}
}

func TestInstanceTypeOfferedAZs_PropagatesError(t *testing.T) {
	fake := &fakeEC2Client{describeInstanceTypeOfferingsErr: errors.New("boom")}
	_, err := instanceTypeOfferedAZs(context.Background(), fake, "t2.micro")
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestFilterSubnetsByInstanceTypeAZ_NarrowsToCompatibleAZs(t *testing.T) {
	fake := &fakeEC2Client{instanceTypeOfferings: map[string][]string{
		"t2.medium": {"us-west-2a", "us-west-2b"},
	}}
	subnets := []SubnetInfo{
		{SubnetID: "subnet-a", AvailabilityZone: "us-west-2a"},
		{SubnetID: "subnet-d", AvailabilityZone: "us-west-2d"},
		{SubnetID: "subnet-b", AvailabilityZone: "us-west-2b"},
	}

	got := filterSubnetsByInstanceTypeAZ(context.Background(), fake, "t2.medium", subnets)
	if len(got) != 2 || got[0].SubnetID != "subnet-a" || got[1].SubnetID != "subnet-b" {
		t.Errorf("got %+v, want [subnet-a subnet-b]", got)
	}
}

func TestFilterSubnetsByInstanceTypeAZ_ReturnsUnfilteredOnLookupError(t *testing.T) {
	fake := &fakeEC2Client{describeInstanceTypeOfferingsErr: errors.New("boom")}
	subnets := []SubnetInfo{{SubnetID: "subnet-a", AvailabilityZone: "us-west-2d"}}

	got := filterSubnetsByInstanceTypeAZ(context.Background(), fake, "t2.medium", subnets)
	if len(got) != 1 || got[0].SubnetID != "subnet-a" {
		t.Errorf("got %+v, want the original unfiltered slice", got)
	}
}

func TestFilterSubnetsByInstanceTypeAZ_ReturnsUnfilteredWhenNoneMatch(t *testing.T) {
	fake := &fakeEC2Client{instanceTypeOfferings: map[string][]string{
		"t2.medium": {"us-west-2a"},
	}}
	subnets := []SubnetInfo{{SubnetID: "subnet-d", AvailabilityZone: "us-west-2d"}}

	got := filterSubnetsByInstanceTypeAZ(context.Background(), fake, "t2.medium", subnets)
	if len(got) != 1 || got[0].SubnetID != "subnet-d" {
		t.Errorf("got %+v, want the original unfiltered slice (filtering to zero is a dead end, not a narrowing)", got)
	}
}

// The "how would you like to proceed?" incompatibility-remediation
// picker (and any nested instance-type picker) converted to huh.Select
// (DESIGN.md's full conversion punch list): their selections are fed via
// a separate newHuhAccessibleInput reader (menuInput), not le, which
// still feeds promptSubnetID's free-text fallback.

func TestEnsureInstanceTypeSupportedInSubnet_CompatibleReturnsImmediately(t *testing.T) {
	fake := &fakeEC2Client{instanceTypeOfferings: map[string][]string{
		"t3.micro": {"us-west-2a"},
	}}
	term, le, buf := newPipeEditor(t, "") // no input needed -- must not prompt

	subnet := SubnetInfo{SubnetID: "subnet-1", AvailabilityZone: "us-west-2a"}
	gotType, gotSubnet, err := ensureInstanceTypeSupportedInSubnet(context.Background(), term, le, fake, "t3.micro", subnet, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotType != "t3.micro" || gotSubnet.SubnetID != "subnet-1" {
		t.Errorf("got type=%q subnet=%q, want unchanged", gotType, gotSubnet.SubnetID)
	}
	if buf.String() != "" {
		t.Errorf("expected no output for a compatible pair, got:\n%s", buf.String())
	}
}

func TestEnsureInstanceTypeSupportedInSubnet_UnknownAZSkipsCheck(t *testing.T) {
	fake := &fakeEC2Client{describeInstanceTypeOfferingsErr: errors.New("should not be called")}
	term, le, _ := newPipeEditor(t, "") // no input needed -- must not prompt

	subnet := SubnetInfo{SubnetID: "subnet-manual"} // AvailabilityZone unknown (free-text fallback)
	gotType, gotSubnet, err := ensureInstanceTypeSupportedInSubnet(context.Background(), term, le, fake, "t2.micro", subnet, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotType != "t2.micro" || gotSubnet.SubnetID != "subnet-manual" {
		t.Errorf("got type=%q subnet=%q, want unchanged", gotType, gotSubnet.SubnetID)
	}
}

func TestEnsureInstanceTypeSupportedInSubnet_CheckErrorSkipsGracefully(t *testing.T) {
	fake := &fakeEC2Client{describeInstanceTypeOfferingsErr: errors.New("access denied")}
	term, le, buf := newPipeEditor(t, "") // no input needed -- must not prompt

	subnet := SubnetInfo{SubnetID: "subnet-1", AvailabilityZone: "us-west-2d"}
	gotType, gotSubnet, err := ensureInstanceTypeSupportedInSubnet(context.Background(), term, le, fake, "t2.micro", subnet, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotType != "t2.micro" || gotSubnet.SubnetID != "subnet-1" {
		t.Errorf("got type=%q subnet=%q, want unchanged when the check itself errors", gotType, gotSubnet.SubnetID)
	}
	if strings.Contains(buf.String(), "is not offered") {
		t.Errorf("should not claim incompatibility when the check itself failed, got:\n%s", buf.String())
	}
}

func TestEnsureInstanceTypeSupportedInSubnet_ChangeInstanceTypeToACompatibleOne(t *testing.T) {
	fake := &fakeEC2Client{instanceTypeOfferings: map[string][]string{
		"t2.micro": {"us-west-2a", "us-west-2b", "us-west-2c"},
		"t3.micro": {"us-west-2a", "us-west-2b", "us-west-2c", "us-west-2d"},
	}}
	term, le, buf := newPipeEditor(t, "")

	subnet := SubnetInfo{SubnetID: "subnet-1", AvailabilityZone: "us-west-2d"}
	gotType, gotSubnet, err := ensureInstanceTypeSupportedInSubnet(context.Background(), term, le, fake, "t2.micro", subnet, newHuhAccessibleInput("1\n1\n"), buf) // 1) Change instance type -> pick t3.micro from the curated list
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotType != "t3.micro" {
		t.Errorf("gotType = %q, want %q", gotType, "t3.micro")
	}
	if gotSubnet.SubnetID != "subnet-1" {
		t.Errorf("gotSubnet.SubnetID = %q, want unchanged %q", gotSubnet.SubnetID, "subnet-1")
	}
	if !strings.Contains(buf.String(), "us-west-2a, us-west-2b, us-west-2c") {
		t.Errorf("expected the list of supported AZs in output, got:\n%s", buf.String())
	}
}

func TestEnsureInstanceTypeSupportedInSubnet_PickADifferentCompatibleSubnet(t *testing.T) {
	fake := &fakeEC2Client{
		instanceTypeOfferings: map[string][]string{
			"t2.micro": {"us-west-2a", "us-west-2b", "us-west-2c"},
		},
	}
	term, le, buf := newPipeEditor(t, "subnet-good\n") // free-text fallback (no subnets listed)

	subnet := SubnetInfo{SubnetID: "subnet-bad", AvailabilityZone: "us-west-2d"}
	gotType, gotSubnet, err := ensureInstanceTypeSupportedInSubnet(context.Background(), term, le, fake, "t2.micro", subnet, newHuhAccessibleInput("2\n"), buf) // 2) Pick a different subnet
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotType != "t2.micro" {
		t.Errorf("gotType = %q, want unchanged %q", gotType, "t2.micro")
	}
	// The re-picked subnet came back via promptSubnetID's free-text
	// fallback (fake has no subnets configured), so its AZ is unknown --
	// the loop must stop checking rather than looping forever.
	if gotSubnet.SubnetID != "subnet-good" {
		t.Errorf("gotSubnet.SubnetID = %q, want %q", gotSubnet.SubnetID, "subnet-good")
	}
}

func TestEnsureInstanceTypeSupportedInSubnet_AbortReturnsErrCancelled(t *testing.T) {
	fake := &fakeEC2Client{instanceTypeOfferings: map[string][]string{
		"t2.micro": {"us-west-2a"},
	}}
	term, le, buf := newPipeEditor(t, "")

	subnet := SubnetInfo{SubnetID: "subnet-bad", AvailabilityZone: "us-west-2d"}
	_, _, err := ensureInstanceTypeSupportedInSubnet(context.Background(), term, le, fake, "t2.micro", subnet, newHuhAccessibleInput("3\n"), buf) // 3) Abort this launch
	if !errors.Is(err, ui.ErrCancelled) {
		t.Fatalf("expected ui.ErrCancelled, got: %v", err)
	}
}

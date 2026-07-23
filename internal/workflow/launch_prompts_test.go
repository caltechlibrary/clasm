package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// The subnet list picker converted to tui.RunPicker (DESIGN.md's full
// conversion punch list, Picker tier): a real bubbletea Program that
// can't be pipe-tested, so promptSubnetID's list-path tests (picking
// from a non-empty, possibly-filtered list) are retired --
// filterSubnetsByInstanceTypeAZ's own tests
// (instance_type_az_check_test.go) already cover the pre-picker
// filtering logic directly, and the picker step itself is covered only
// by manual/interactive verification, the same accepted limitation this
// session's other Picker-tier conversions already have. The free-text
// fallback path (zero subnets) never reaches the picker, so it's still
// fully testable below.

func TestPromptSubnetID_FallsBackToFreeTextWhenEmpty(t *testing.T) {
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor("subnet-manual\n")

	got, err := promptSubnetID(context.Background(), term, fake, "t3.micro", le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.SubnetID != "subnet-manual" {
		t.Errorf("got %q, want %q", got.SubnetID, "subnet-manual")
	}
	if got.AvailabilityZone != "" {
		t.Errorf("AvailabilityZone = %q, want empty (unknown via free-text fallback)", got.AvailabilityZone)
	}
}

func TestPromptSecurityGroupIDs_ResolvesNumbersFromList(t *testing.T) {
	fake := &fakeEC2Client{securityGroups: []types.SecurityGroup{
		{GroupId: aws.String("sg-1"), GroupName: aws.String("web"), Description: aws.String("web tier")},
		{GroupId: aws.String("sg-2"), GroupName: aws.String("db"), Description: aws.String("db tier")},
	}}
	term, le, buf := newPipeEditor("1,2\n")

	got, err := promptSecurityGroupIDs(context.Background(), term, fake, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "sg-1" || got[1] != "sg-2" {
		t.Errorf("got %v, want [sg-1 sg-2]", got)
	}
	if !strings.Contains(buf.String(), "web tier") {
		t.Errorf("expected the security group list to be displayed, got:\n%s", buf.String())
	}
}

func TestPromptSecurityGroupIDs_AcceptsRawIDsAlongsideNumbers(t *testing.T) {
	fake := &fakeEC2Client{securityGroups: []types.SecurityGroup{
		{GroupId: aws.String("sg-1"), GroupName: aws.String("web")},
	}}
	term, le, buf := newPipeEditor("1,sg-999\n")

	got, err := promptSecurityGroupIDs(context.Background(), term, fake, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "sg-1" || got[1] != "sg-999" {
		t.Errorf("got %v, want [sg-1 sg-999]", got)
	}
}

func TestPromptSecurityGroupIDs_RejectsOutOfRangeNumberThenRetries(t *testing.T) {
	fake := &fakeEC2Client{securityGroups: []types.SecurityGroup{
		{GroupId: aws.String("sg-1"), GroupName: aws.String("web")},
	}}
	term, le, buf := newPipeEditor("99\n1\n")

	got, err := promptSecurityGroupIDs(context.Background(), term, fake, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "sg-1" {
		t.Errorf("got %v, want [sg-1]", got)
	}
	if !strings.Contains(buf.String(), "invalid input") {
		t.Errorf("expected a validation error message, got:\n%s", buf.String())
	}
}

func TestPromptSecurityGroupIDs_FallsBackToFreeTextWhenEmpty(t *testing.T) {
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor("sg-manual\n")

	got, err := promptSecurityGroupIDs(context.Background(), term, fake, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "sg-manual" {
		t.Errorf("got %v, want [sg-manual]", got)
	}
}

// The curated-instance-type/"Other" picker (huh.Select) and the
// "Other" free-text fallback prompt now share one accessible-mode
// reader, read in sequence one line at a time -- the menu choice first,
// then (only for "Other") the typed value.

func TestPromptInstanceType_PicksFromCuratedList(t *testing.T) {
	term, input, buf := newPipeEditor("1\n")

	got, err := promptInstanceType(term, "", input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "t3.micro" {
		t.Errorf("got %q, want %q", got, "t3.micro")
	}
	if !strings.Contains(buf.String(), "vCPU") {
		t.Errorf("expected vCPU/memory info in the listing, got:\n%s", buf.String())
	}
}

func TestPromptInstanceType_PicksALaterCuratedEntry(t *testing.T) {
	term, input, buf := newPipeEditor("4\n")

	got, err := promptInstanceType(term, "", input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "t3.large" {
		t.Errorf("got %q, want %q", got, "t3.large")
	}
}

func TestPromptInstanceType_IncludesNonENARequiredEntries(t *testing.T) {
	// t2.micro/t2.medium are the curated list's only non-Nitro types --
	// every other entry requires ENA (DECISIONS.md, "Add non-ENA-
	// required options to the curated instance type list"), so an AMI
	// without ENA support needs one of these to launch at all.
	term, input, buf := newPipeEditor("10\n")

	got, err := promptInstanceType(term, "", input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "t2.micro" {
		t.Errorf("got %q, want %q", got, "t2.micro")
	}
	if !strings.Contains(buf.String(), "no ENA required") {
		t.Errorf("expected the listing to call out non-ENA-required entries, got:\n%s", buf.String())
	}
}

func TestPromptInstanceType_OtherFallsBackToFreeText(t *testing.T) {
	term, input, buf := newPipeEditor("21\nc6g.medium\n") // 21) Other, unfiltered (20 curated entries + Other)

	got, err := promptInstanceType(term, "", input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "c6g.medium" {
		t.Errorf("got %q, want %q", got, "c6g.medium")
	}
}

func TestPromptInstanceType_OtherRejectsBlank(t *testing.T) {
	term, input, buf := newPipeEditor("21\n\nt4g.nano\n") // 21) Other, blank (rejected), retry

	got, err := promptInstanceType(term, "", input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "t4g.nano" {
		t.Errorf("got %q, want %q", got, "t4g.nano")
	}
	if !strings.Contains(buf.String(), "invalid input") {
		t.Errorf("expected a validation error message, got:\n%s", buf.String())
	}
}

// TestPromptInstanceType_FiltersToArm64 / _FiltersToX8664 -- DESIGN.md,
// "ARM64 (Graviton) Support + Ubuntu 26.04 LTS": filtering by the
// already-picked AMI's architecture, not a post-hoc reject-and-retry
// check (DECISIONS.md, "ARM64/Ubuntu 26.04: filter the instance-type
// list by AMI architecture, no new pre-flight check").

func TestPromptInstanceType_FiltersToArm64(t *testing.T) {
	term, input, buf := newPipeEditor("1\n")

	got, err := promptInstanceType(term, "arm64", input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "t4g.micro" {
		t.Errorf("got %q, want %q (first arm64 entry)", got, "t4g.micro")
	}
	if strings.Contains(buf.String(), "t3.micro") {
		t.Errorf("expected x86_64 entries to be filtered out, got:\n%s", buf.String())
	}
}

func TestPromptInstanceType_FiltersToX8664(t *testing.T) {
	term, input, buf := newPipeEditor("1\n")

	got, err := promptInstanceType(term, "x86_64", input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "t3.micro" {
		t.Errorf("got %q, want %q (first x86_64 entry)", got, "t3.micro")
	}
	if strings.Contains(buf.String(), "t4g.micro") {
		t.Errorf("expected arm64 entries to be filtered out, got:\n%s", buf.String())
	}
}

func TestPromptInstanceType_OtherAlwaysAvailableWhenFiltered(t *testing.T) {
	// 9 arm64 curated entries + "Other" -- confirmed by picking the last
	// (10th) row when filtered to arm64.
	term, input, buf := newPipeEditor("10\nc6g.medium\n")

	got, err := promptInstanceType(term, "arm64", input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "c6g.medium" {
		t.Errorf("got %q, want %q", got, "c6g.medium")
	}
}

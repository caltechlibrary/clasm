package inventory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
)

// fakeIAMClient is a fake awsclient.IAMAPI for this package's IAM
// discovery tests -- embeds the real interface so any unimplemented
// method panics loudly if a test exercises a path that needs it,
// matching fakeBucketsS3Client's style (buckets_test.go).
type fakeIAMClient struct {
	awsclient.IAMAPI

	roles            []iamtypes.Role
	listRolesErr     error
	instanceProfiles []iamtypes.InstanceProfile
	listInstProfsErr error
	policies         []iamtypes.Policy
	listPoliciesErr  error
}

func (f *fakeIAMClient) ListRoles(ctx context.Context, params *iam.ListRolesInput, optFns ...func(*iam.Options)) (*iam.ListRolesOutput, error) {
	if f.listRolesErr != nil {
		return nil, f.listRolesErr
	}
	return &iam.ListRolesOutput{Roles: f.roles}, nil
}

func (f *fakeIAMClient) ListInstanceProfiles(ctx context.Context, params *iam.ListInstanceProfilesInput, optFns ...func(*iam.Options)) (*iam.ListInstanceProfilesOutput, error) {
	if f.listInstProfsErr != nil {
		return nil, f.listInstProfsErr
	}
	return &iam.ListInstanceProfilesOutput{InstanceProfiles: f.instanceProfiles}, nil
}

func (f *fakeIAMClient) ListPolicies(ctx context.Context, params *iam.ListPoliciesInput, optFns ...func(*iam.Options)) (*iam.ListPoliciesOutput, error) {
	if f.listPoliciesErr != nil {
		return nil, f.listPoliciesErr
	}
	return &iam.ListPoliciesOutput{Policies: f.policies}, nil
}

func iamFixtureTime(t *testing.T, s string) *time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parsing fixture time %q: %v", s, err)
	}
	return &tm
}

func TestResolveOrigin_ReturnsTagValueWhenPresent(t *testing.T) {
	tags := map[string]string{"Origin": "DLD"}
	if got := ResolveOrigin(tags, "Origin"); got != "DLD" {
		t.Errorf("got %q, want %q", got, "DLD")
	}
}

func TestResolveOrigin_ReturnsUnsetWhenAbsent(t *testing.T) {
	tags := map[string]string{"Project": "newauthors"}
	if got := ResolveOrigin(tags, "Origin"); got != OriginUnset {
		t.Errorf("got %q, want %q", got, OriginUnset)
	}
}

func TestResolveOrigin_ReturnsUnsetWhenValueEmpty(t *testing.T) {
	tags := map[string]string{"Origin": ""}
	if got := ResolveOrigin(tags, "Origin"); got != OriginUnset {
		t.Errorf("got %q, want %q", got, OriginUnset)
	}
}

func TestIsDLDOwned_TrueWhenTagMatchesConfiguredValue(t *testing.T) {
	tags := map[string]string{"Origin": "DLD"}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}
	if !IsDLDOwned(tags, originTag) {
		t.Error("expected true when the tag matches the configured DLD value")
	}
}

func TestIsDLDOwned_FalseWhenTagDoesNotMatch(t *testing.T) {
	tags := map[string]string{"Origin": "IMSS"}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}
	if IsDLDOwned(tags, originTag) {
		t.Error("expected false when the tag doesn't match the configured DLD value")
	}
}

func TestIsDLDOwned_FalseWhenTagAbsent(t *testing.T) {
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}
	if IsDLDOwned(map[string]string{}, originTag) {
		t.Error("expected false when the resource has no Origin tag at all")
	}
}

func TestIsDLDOwned_FalseWhenDLDValueUnconfigured(t *testing.T) {
	tags := map[string]string{"Origin": "DLD"}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: ""}
	if IsDLDOwned(tags, originTag) {
		t.Error("expected false when the config's DLDValue is unset, regardless of the tag's own value")
	}
}

func TestRequireDLDOwned_NilWhenOwned(t *testing.T) {
	tags := map[string]string{"Origin": "DLD"}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}
	if err := RequireDLDOwned(tags, originTag, "role", "ec2-granian-test-role"); err != nil {
		t.Errorf("expected nil for a DLD-owned resource, got: %v", err)
	}
}

func TestRequireDLDOwned_ErrorWhenNotOwned(t *testing.T) {
	tags := map[string]string{"Origin": "IMSS"}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}
	err := RequireDLDOwned(tags, originTag, "role", "imss-crowdstrike-agent-role")
	if err == nil {
		t.Fatal("expected an error for a resource not recognized as DLD-owned")
	}
	if !errors.Is(err, ErrNotDLDOwned) {
		t.Errorf("expected errors.Is(err, ErrNotDLDOwned), got: %v", err)
	}
	for _, want := range []string{"role", "imss-crowdstrike-agent-role"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message %q missing %q", err.Error(), want)
		}
	}
}

func TestRequireDLDOwned_ErrorWhenDLDValueUnconfigured(t *testing.T) {
	// Even a resource explicitly tagged Origin=DLD is refused until the
	// operator actually configures which value means "DLD-owned" -- no
	// hardcoded fallback, matching IsDLDOwned's own behavior.
	tags := map[string]string{"Origin": "DLD"}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: ""}
	if err := RequireDLDOwned(tags, originTag, "policy", "some-policy"); err == nil {
		t.Error("expected an error when the config's DLDValue is unset")
	}
}

func TestListIAMRoleSummaries_ResolvesOriginAndSortsByCreateDateDescending(t *testing.T) {
	fake := &fakeIAMClient{
		roles: []iamtypes.Role{
			{
				RoleName:   aws.String("older-role"),
				CreateDate: iamFixtureTime(t, "2026-01-01T00:00:00Z"),
				Tags:       []iamtypes.Tag{{Key: aws.String("Origin"), Value: aws.String("DLD")}},
			},
			{
				RoleName:   aws.String("newer-role"),
				CreateDate: iamFixtureTime(t, "2026-06-01T00:00:00Z"),
			},
		},
	}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	got, err := ListIAMRoleSummaries(context.Background(), fake, originTag)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].Name != "newer-role" || got[1].Name != "older-role" {
		t.Fatalf("got %+v, want newer-role then older-role", got)
	}
	if got[1].Origin != "DLD" || !got[1].DLDOwned {
		t.Errorf("older-role: Origin=%q DLDOwned=%v, want Origin=DLD DLDOwned=true", got[1].Origin, got[1].DLDOwned)
	}
	if got[0].Origin != OriginUnset || got[0].DLDOwned {
		t.Errorf("newer-role: Origin=%q DLDOwned=%v, want Origin=%q DLDOwned=false", got[0].Origin, got[0].DLDOwned, OriginUnset)
	}
}

func TestListIAMRoleSummaries_PropagatesListRolesError(t *testing.T) {
	fake := &fakeIAMClient{listRolesErr: errors.New("boom")}
	_, err := ListIAMRoleSummaries(context.Background(), fake, config.OriginTagConfig{Key: "Origin"})
	if err == nil {
		t.Fatal("expected an error to propagate from ListRoles")
	}
}

func TestListIAMInstanceProfileSummaries_ResolvesOriginAndSortsByCreateDateDescending(t *testing.T) {
	fake := &fakeIAMClient{
		instanceProfiles: []iamtypes.InstanceProfile{
			{
				InstanceProfileName: aws.String("older-profile"),
				CreateDate:          iamFixtureTime(t, "2026-01-01T00:00:00Z"),
				Tags:                []iamtypes.Tag{{Key: aws.String("Origin"), Value: aws.String("IMSS")}},
			},
			{
				InstanceProfileName: aws.String("newer-profile"),
				CreateDate:          iamFixtureTime(t, "2026-06-01T00:00:00Z"),
			},
		},
	}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	got, err := ListIAMInstanceProfileSummaries(context.Background(), fake, originTag)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].Name != "newer-profile" || got[1].Name != "older-profile" {
		t.Fatalf("got %+v, want newer-profile then older-profile", got)
	}
	if got[1].Origin != "IMSS" || got[1].DLDOwned {
		t.Errorf("older-profile: Origin=%q DLDOwned=%v, want Origin=IMSS DLDOwned=false", got[1].Origin, got[1].DLDOwned)
	}
}

func TestListIAMInstanceProfileSummaries_PropagatesListInstanceProfilesError(t *testing.T) {
	fake := &fakeIAMClient{listInstProfsErr: errors.New("boom")}
	_, err := ListIAMInstanceProfileSummaries(context.Background(), fake, config.OriginTagConfig{Key: "Origin"})
	if err == nil {
		t.Fatal("expected an error to propagate from ListInstanceProfiles")
	}
}

func TestListIAMPolicySummaries_ResolvesOriginAndSortsByCreateDateDescending(t *testing.T) {
	fake := &fakeIAMClient{
		policies: []iamtypes.Policy{
			{
				PolicyName: aws.String("older-policy"),
				Arn:        aws.String("arn:aws:iam::123456789012:policy/older-policy"),
				CreateDate: iamFixtureTime(t, "2026-01-01T00:00:00Z"),
				Tags:       []iamtypes.Tag{{Key: aws.String("Origin"), Value: aws.String("DLD")}},
			},
			{
				PolicyName: aws.String("newer-policy"),
				Arn:        aws.String("arn:aws:iam::123456789012:policy/newer-policy"),
				CreateDate: iamFixtureTime(t, "2026-06-01T00:00:00Z"),
			},
		},
	}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	got, err := ListIAMPolicySummaries(context.Background(), fake, originTag)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].Name != "newer-policy" || got[1].Name != "older-policy" {
		t.Fatalf("got %+v, want newer-policy then older-policy", got)
	}
	if !got[1].DLDOwned {
		t.Error("older-policy should be recognized as DLD-owned")
	}
}

func TestListIAMPolicySummaries_PropagatesListPoliciesError(t *testing.T) {
	fake := &fakeIAMClient{listPoliciesErr: errors.New("boom")}
	_, err := ListIAMPolicySummaries(context.Background(), fake, config.OriginTagConfig{Key: "Origin"})
	if err == nil {
		t.Fatal("expected an error to propagate from ListPolicies")
	}
}

func TestListIAMPolicySummaries_ScopedToLocalByDefault(t *testing.T) {
	var captured *iam.ListPoliciesInput
	fake := &fakeIAMClient{}
	// Wrap ListPolicies to capture the request, since fakeIAMClient's
	// own method doesn't track its last input (unlike some of this
	// project's other fakes) -- a thin closure-based spy is simplest
	// here rather than adding a field only this one test needs.
	spy := &spyListPoliciesClient{fakeIAMClient: fake, captured: &captured}

	_, err := ListIAMPolicySummaries(context.Background(), spy, config.OriginTagConfig{Key: "Origin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured == nil {
		t.Fatal("expected ListPolicies to be called")
	}
	if captured.Scope != iamtypes.PolicyScopeTypeLocal {
		t.Errorf("Scope = %v, want %v", captured.Scope, iamtypes.PolicyScopeTypeLocal)
	}
}

type spyListPoliciesClient struct {
	*fakeIAMClient
	captured **iam.ListPoliciesInput
}

func (s *spyListPoliciesClient) ListPolicies(ctx context.Context, params *iam.ListPoliciesInput, optFns ...func(*iam.Options)) (*iam.ListPoliciesOutput, error) {
	*s.captured = params
	return s.fakeIAMClient.ListPolicies(ctx, params, optFns...)
}

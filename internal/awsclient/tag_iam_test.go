package awsclient

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// Everything clasm creates in IAM carries the Origin tag (DR-0189): the tag is how
// clasm decides what to show and manage, and how a report finds what it made. A
// wrapper on the IAM client adds it to every create, so no workflow can forget it
// and a creator added later is covered without plumbing the config to it.

type recordingIAM struct {
	IAMAPI
	role    *iam.CreateRoleInput
	policy  *iam.CreatePolicyInput
	profile *iam.CreateInstanceProfileInput
	getRole int
	err     error
}

func (r *recordingIAM) CreateRole(_ context.Context, p *iam.CreateRoleInput, _ ...func(*iam.Options)) (*iam.CreateRoleOutput, error) {
	r.role = p
	return &iam.CreateRoleOutput{}, r.err
}

func (r *recordingIAM) CreatePolicy(_ context.Context, p *iam.CreatePolicyInput, _ ...func(*iam.Options)) (*iam.CreatePolicyOutput, error) {
	r.policy = p
	return &iam.CreatePolicyOutput{}, r.err
}

func (r *recordingIAM) CreateInstanceProfile(_ context.Context, p *iam.CreateInstanceProfileInput, _ ...func(*iam.Options)) (*iam.CreateInstanceProfileOutput, error) {
	r.profile = p
	return &iam.CreateInstanceProfileOutput{}, r.err
}

func (r *recordingIAM) GetRole(_ context.Context, _ *iam.GetRoleInput, _ ...func(*iam.Options)) (*iam.GetRoleOutput, error) {
	r.getRole++
	return &iam.GetRoleOutput{}, nil
}

func dld() (string, string) { return "origin", "dld" }

func hasTag(tags []iamtypes.Tag, key, value string) int {
	n := 0
	for _, t := range tags {
		if aws.ToString(t.Key) == key && aws.ToString(t.Value) == value {
			n++
		}
	}
	return n
}

func TestTagIAMCreations_EveryCreateGetsTheOriginTag(t *testing.T) {
	rec := &recordingIAM{}
	c := TagIAMCreations(rec, dld)
	ctx := context.Background()
	if _, err := c.CreateRole(ctx, &iam.CreateRoleInput{RoleName: aws.String("r")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreatePolicy(ctx, &iam.CreatePolicyInput{PolicyName: aws.String("p")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{InstanceProfileName: aws.String("ip")}); err != nil {
		t.Fatal(err)
	}
	if hasTag(rec.role.Tags, "origin", "dld") != 1 || hasTag(rec.policy.Tags, "origin", "dld") != 1 || hasTag(rec.profile.Tags, "origin", "dld") != 1 {
		t.Errorf("role=%+v policy=%+v profile=%+v", rec.role.Tags, rec.policy.Tags, rec.profile.Tags)
	}
	if aws.ToString(rec.role.RoleName) != "r" || aws.ToString(rec.policy.PolicyName) != "p" || aws.ToString(rec.profile.InstanceProfileName) != "ip" {
		t.Errorf("the rest of each request must pass through unchanged")
	}
}

func TestTagIAMCreations_OtherTagsAreKept(t *testing.T) {
	rec := &recordingIAM{}
	_, _ = TagIAMCreations(rec, dld).CreateInstanceProfile(context.Background(), &iam.CreateInstanceProfileInput{
		Tags: []iamtypes.Tag{{Key: aws.String("project"), Value: aws.String("authors")}},
	})
	if hasTag(rec.profile.Tags, "project", "authors") != 1 || hasTag(rec.profile.Tags, "origin", "dld") != 1 || len(rec.profile.Tags) != 2 {
		t.Errorf("tags = %+v", rec.profile.Tags)
	}
}

// A request that already names the tag keeps its own value, and is not tagged twice
// (AWS rejects duplicate keys).
func TestTagIAMCreations_AnExplicitOriginTagWinsAndIsNeverDuplicated(t *testing.T) {
	rec := &recordingIAM{}
	c := TagIAMCreations(rec, dld)
	ctx := context.Background()
	_, _ = c.CreateRole(ctx, &iam.CreateRoleInput{Tags: []iamtypes.Tag{{Key: aws.String("origin"), Value: aws.String("dld")}}})
	if len(rec.role.Tags) != 1 {
		t.Errorf("an already-tagged request must not gain a second origin tag: %+v", rec.role.Tags)
	}
	_, _ = c.CreatePolicy(ctx, &iam.CreatePolicyInput{Tags: []iamtypes.Tag{{Key: aws.String("origin"), Value: aws.String("someone-else")}}})
	if hasTag(rec.policy.Tags, "origin", "someone-else") != 1 || len(rec.policy.Tags) != 1 {
		t.Errorf("an explicit value must win: %+v", rec.policy.Tags)
	}
}

func TestTagIAMCreations_NothingConfiguredMeansNothingAdded(t *testing.T) {
	for name, get := range map[string]func() (string, string){
		"no value": func() (string, string) { return "origin", "" },
		"no key":   func() (string, string) { return "", "dld" },
	} {
		rec := &recordingIAM{}
		_, _ = TagIAMCreations(rec, get).CreateRole(context.Background(), &iam.CreateRoleInput{})
		if len(rec.role.Tags) != 0 {
			t.Errorf("%s: tags = %+v", name, rec.role.Tags)
		}
	}
}

// The configuration can change while clasm runs (Configure > Edit Origin tag), so the
// tag is read at each create, not once at start-up.
func TestTagIAMCreations_TheConfigurationIsReadAtEachCall(t *testing.T) {
	rec := &recordingIAM{}
	value := "dld"
	c := TagIAMCreations(rec, func() (string, string) { return "origin", value })
	_, _ = c.CreateRole(context.Background(), &iam.CreateRoleInput{})
	value = "other"
	_, _ = c.CreateRole(context.Background(), &iam.CreateRoleInput{})
	if hasTag(rec.role.Tags, "origin", "other") != 1 {
		t.Errorf("the second create must carry the new value: %+v", rec.role.Tags)
	}
}

func TestTagIAMCreations_TheCallersRequestIsNotMutated(t *testing.T) {
	rec := &recordingIAM{}
	in := &iam.CreateRoleInput{RoleName: aws.String("r")}
	_, _ = TagIAMCreations(rec, dld).CreateRole(context.Background(), in)
	if len(in.Tags) != 0 {
		t.Errorf("the caller's input must be left alone: %+v", in.Tags)
	}
}

func TestTagIAMCreations_ErrorsAndOtherCallsPassThrough(t *testing.T) {
	rec := &recordingIAM{err: errors.New("EntityAlreadyExists")}
	c := TagIAMCreations(rec, dld)
	if _, err := c.CreateInstanceProfile(context.Background(), &iam.CreateInstanceProfileInput{}); err == nil || err.Error() != "EntityAlreadyExists" {
		t.Errorf("the inner error must come back as it is: %v", err)
	}
	if _, err := c.GetRole(context.Background(), &iam.GetRoleInput{}); err != nil || rec.getRole != 1 {
		t.Errorf("a call that is not a create must reach the inner client: %v %d", err, rec.getRole)
	}
}

package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

func TestFetchIAMRoleTags_ReturnsCurrentTags(t *testing.T) {
	fake := &fakeIAMClient{
		roleTags: map[string][]iamtypes.Tag{
			"air-sampling": {{Key: aws.String("origin"), Value: aws.String("dld")}},
		},
	}
	got, err := fetchIAMRoleTags(context.Background(), fake, "air-sampling")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["origin"] != "dld" {
		t.Errorf("got %v, want origin=dld", got)
	}
}

func TestFetchIAMRoleTags_PropagatesError(t *testing.T) {
	fake := &fakeIAMClient{listRoleTagsErr: errors.New("boom")}
	_, err := fetchIAMRoleTags(context.Background(), fake, "air-sampling")
	if err == nil {
		t.Fatal("expected an error to propagate")
	}
}

func TestApplyIAMRoleTagChange_AddCallsTagRole(t *testing.T) {
	fake := &fakeIAMClient{}
	err := applyIAMRoleTagChange(context.Background(), fake, TagChangeParams{ResourceID: "air-sampling", Action: "add", Key: "origin", Value: "dld"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastTagRoleInput == nil {
		t.Fatal("expected TagRole to be called")
	}
	if aws.ToString(fake.lastTagRoleInput.RoleName) != "air-sampling" {
		t.Errorf("RoleName = %q, want air-sampling", aws.ToString(fake.lastTagRoleInput.RoleName))
	}
	if len(fake.lastTagRoleInput.Tags) != 1 || aws.ToString(fake.lastTagRoleInput.Tags[0].Key) != "origin" || aws.ToString(fake.lastTagRoleInput.Tags[0].Value) != "dld" {
		t.Errorf("Tags = %+v, want [{origin dld}]", fake.lastTagRoleInput.Tags)
	}
	if fake.lastUntagRoleInput != nil {
		t.Error("did not expect UntagRole to be called for an add")
	}
}

func TestApplyIAMRoleTagChange_UpdateCallsTagRole(t *testing.T) {
	fake := &fakeIAMClient{}
	err := applyIAMRoleTagChange(context.Background(), fake, TagChangeParams{ResourceID: "air-sampling", Action: "update", Key: "origin", Value: "dld"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastTagRoleInput == nil {
		t.Fatal("expected TagRole to be called for update, same as add")
	}
}

func TestApplyIAMRoleTagChange_RemoveCallsUntagRole(t *testing.T) {
	fake := &fakeIAMClient{}
	err := applyIAMRoleTagChange(context.Background(), fake, TagChangeParams{ResourceID: "air-sampling", Action: "remove", Key: "origin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastUntagRoleInput == nil {
		t.Fatal("expected UntagRole to be called")
	}
	if aws.ToString(fake.lastUntagRoleInput.RoleName) != "air-sampling" {
		t.Errorf("RoleName = %q, want air-sampling", aws.ToString(fake.lastUntagRoleInput.RoleName))
	}
	if len(fake.lastUntagRoleInput.TagKeys) != 1 || fake.lastUntagRoleInput.TagKeys[0] != "origin" {
		t.Errorf("TagKeys = %v, want [origin]", fake.lastUntagRoleInput.TagKeys)
	}
	if fake.lastTagRoleInput != nil {
		t.Error("did not expect TagRole to be called for a remove")
	}
}

func TestApplyIAMRoleTagChange_PropagatesTagRoleError(t *testing.T) {
	fake := &fakeIAMClient{tagRoleErr: errors.New("boom")}
	err := applyIAMRoleTagChange(context.Background(), fake, TagChangeParams{ResourceID: "air-sampling", Action: "add", Key: "origin", Value: "dld"})
	if err == nil {
		t.Fatal("expected an error to propagate")
	}
}

func TestApplyIAMRoleTagChange_PropagatesUntagRoleError(t *testing.T) {
	fake := &fakeIAMClient{untagRoleErr: errors.New("boom")}
	err := applyIAMRoleTagChange(context.Background(), fake, TagChangeParams{ResourceID: "air-sampling", Action: "remove", Key: "origin"})
	if err == nil {
		t.Fatal("expected an error to propagate")
	}
}

func TestFetchIAMInstanceProfileTags_ReturnsCurrentTags(t *testing.T) {
	fake := &fakeIAMClient{
		instanceProfileTags: map[string][]iamtypes.Tag{
			"air-sampling-profile": {{Key: aws.String("origin"), Value: aws.String("dld")}},
		},
	}
	got, err := fetchIAMInstanceProfileTags(context.Background(), fake, "air-sampling-profile")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["origin"] != "dld" {
		t.Errorf("got %v, want origin=dld", got)
	}
}

func TestFetchIAMInstanceProfileTags_PropagatesError(t *testing.T) {
	fake := &fakeIAMClient{listInstProfTagsErr: errors.New("boom")}
	_, err := fetchIAMInstanceProfileTags(context.Background(), fake, "air-sampling-profile")
	if err == nil {
		t.Fatal("expected an error to propagate")
	}
}

func TestApplyIAMInstanceProfileTagChange_AddCallsTagInstanceProfile(t *testing.T) {
	fake := &fakeIAMClient{}
	err := applyIAMInstanceProfileTagChange(context.Background(), fake, TagChangeParams{ResourceID: "air-sampling-profile", Action: "add", Key: "origin", Value: "dld"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastTagInstanceProfileInput == nil {
		t.Fatal("expected TagInstanceProfile to be called")
	}
	if aws.ToString(fake.lastTagInstanceProfileInput.InstanceProfileName) != "air-sampling-profile" {
		t.Errorf("InstanceProfileName = %q, want air-sampling-profile", aws.ToString(fake.lastTagInstanceProfileInput.InstanceProfileName))
	}
}

func TestApplyIAMInstanceProfileTagChange_RemoveCallsUntagInstanceProfile(t *testing.T) {
	fake := &fakeIAMClient{}
	err := applyIAMInstanceProfileTagChange(context.Background(), fake, TagChangeParams{ResourceID: "air-sampling-profile", Action: "remove", Key: "origin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastUntagInstanceProfileInput == nil {
		t.Fatal("expected UntagInstanceProfile to be called")
	}
	if len(fake.lastUntagInstanceProfileInput.TagKeys) != 1 || fake.lastUntagInstanceProfileInput.TagKeys[0] != "origin" {
		t.Errorf("TagKeys = %v, want [origin]", fake.lastUntagInstanceProfileInput.TagKeys)
	}
}

func TestApplyIAMInstanceProfileTagChange_PropagatesErrors(t *testing.T) {
	addFake := &fakeIAMClient{tagInstanceProfileErr: errors.New("boom")}
	if err := applyIAMInstanceProfileTagChange(context.Background(), addFake, TagChangeParams{ResourceID: "p", Action: "add", Key: "k", Value: "v"}); err == nil {
		t.Error("expected TagInstanceProfile's error to propagate")
	}
	removeFake := &fakeIAMClient{untagInstanceProfileErr: errors.New("boom")}
	if err := applyIAMInstanceProfileTagChange(context.Background(), removeFake, TagChangeParams{ResourceID: "p", Action: "remove", Key: "k"}); err == nil {
		t.Error("expected UntagInstanceProfile's error to propagate")
	}
}

func TestFetchIAMPolicyTags_ReturnsCurrentTags(t *testing.T) {
	arn := "arn:aws:iam::123456789012:policy/s3-backup-access"
	fake := &fakeIAMClient{
		policyTags: map[string][]iamtypes.Tag{
			arn: {{Key: aws.String("origin"), Value: aws.String("dld")}},
		},
	}
	got, err := fetchIAMPolicyTags(context.Background(), fake, arn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["origin"] != "dld" {
		t.Errorf("got %v, want origin=dld", got)
	}
}

func TestFetchIAMPolicyTags_PropagatesError(t *testing.T) {
	fake := &fakeIAMClient{listPolicyTagsErr: errors.New("boom")}
	_, err := fetchIAMPolicyTags(context.Background(), fake, "arn:aws:iam::123456789012:policy/s3-backup-access")
	if err == nil {
		t.Fatal("expected an error to propagate")
	}
}

func TestApplyIAMPolicyTagChange_AddCallsTagPolicyWithARN(t *testing.T) {
	arn := "arn:aws:iam::123456789012:policy/s3-backup-access"
	fake := &fakeIAMClient{}
	err := applyIAMPolicyTagChange(context.Background(), fake, TagChangeParams{ResourceID: arn, Action: "add", Key: "origin", Value: "dld"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastTagPolicyInput == nil {
		t.Fatal("expected TagPolicy to be called")
	}
	if aws.ToString(fake.lastTagPolicyInput.PolicyArn) != arn {
		t.Errorf("PolicyArn = %q, want %q", aws.ToString(fake.lastTagPolicyInput.PolicyArn), arn)
	}
}

func TestApplyIAMPolicyTagChange_RemoveCallsUntagPolicyWithARN(t *testing.T) {
	arn := "arn:aws:iam::123456789012:policy/s3-backup-access"
	fake := &fakeIAMClient{}
	err := applyIAMPolicyTagChange(context.Background(), fake, TagChangeParams{ResourceID: arn, Action: "remove", Key: "origin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastUntagPolicyInput == nil {
		t.Fatal("expected UntagPolicy to be called")
	}
	if aws.ToString(fake.lastUntagPolicyInput.PolicyArn) != arn {
		t.Errorf("PolicyArn = %q, want %q", aws.ToString(fake.lastUntagPolicyInput.PolicyArn), arn)
	}
	if len(fake.lastUntagPolicyInput.TagKeys) != 1 || fake.lastUntagPolicyInput.TagKeys[0] != "origin" {
		t.Errorf("TagKeys = %v, want [origin]", fake.lastUntagPolicyInput.TagKeys)
	}
}

func TestApplyIAMPolicyTagChange_PropagatesErrors(t *testing.T) {
	addFake := &fakeIAMClient{tagPolicyErr: errors.New("boom")}
	if err := applyIAMPolicyTagChange(context.Background(), addFake, TagChangeParams{ResourceID: "arn", Action: "add", Key: "k", Value: "v"}); err == nil {
		t.Error("expected TagPolicy's error to propagate")
	}
	removeFake := &fakeIAMClient{untagPolicyErr: errors.New("boom")}
	if err := applyIAMPolicyTagChange(context.Background(), removeFake, TagChangeParams{ResourceID: "arn", Action: "remove", Key: "k"}); err == nil {
		t.Error("expected UntagPolicy's error to propagate")
	}
}

package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

func TestIsAWSManagedPolicyArn(t *testing.T) {
	if !isAWSManagedPolicyArn("arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore") {
		t.Error("expected the AWS-managed SSM policy ARN to be recognized as AWS-managed")
	}
	if isAWSManagedPolicyArn("arn:aws:iam::123456789012:policy/my-custom-policy") {
		t.Error("did not expect a customer-managed policy ARN to be recognized as AWS-managed")
	}
}

func TestDeleteIAMRole_RefusesWhenReferencedByProfiles(t *testing.T) {
	fake := &fakeIAMClient{}
	detail := IAMRoleDetail{Name: "air-sampling", ReferencedByProfiles: []string{"air-sampling-profile"}}

	err := deleteIAMRole(context.Background(), fake, detail)
	if err == nil {
		t.Fatal("expected an error when the role is still referenced by an instance profile")
	}
	if !strings.Contains(err.Error(), "air-sampling-profile") {
		t.Errorf("expected the error to name the referencing profile, got: %v", err)
	}
	if fake.lastDeleteRoleInput != nil {
		t.Error("did not expect DeleteRole to be called")
	}
}

func TestDeleteIAMRole_DeletesInlinePoliciesDetachesManagedAndDeletesRole(t *testing.T) {
	fake := &fakeIAMClient{
		entitiesByPolicyArn: map[string]iam.ListEntitiesForPolicyOutput{
			"arn:aws:iam::123456789012:policy/test-role-policy": {}, // unused elsewhere
		},
	}
	detail := IAMRoleDetail{
		Name:              "test-role",
		InlinePolicyNames: []string{"InlinePolicyA"},
		AttachedPolicies: []IAMPolicyRef{
			{Name: "AmazonSSMManagedInstanceCore", ARN: ssmManagedInstanceCorePolicyArn},
			{Name: "test-role-policy", ARN: "arn:aws:iam::123456789012:policy/test-role-policy"},
		},
	}

	err := deleteIAMRole(context.Background(), fake, detail)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fake.lastDeleteRolePolicyInputs) != 1 || aws.ToString(fake.lastDeleteRolePolicyInputs[0].PolicyName) != "InlinePolicyA" {
		t.Errorf("expected InlinePolicyA to be deleted, got: %+v", fake.lastDeleteRolePolicyInputs)
	}
	if len(fake.lastDetachRolePolicyInputs) != 2 {
		t.Fatalf("expected both attached policies to be detached, got %d", len(fake.lastDetachRolePolicyInputs))
	}
	if fake.lastDeleteRoleInput == nil || aws.ToString(fake.lastDeleteRoleInput.RoleName) != "test-role" {
		t.Error("expected DeleteRole to be called with the role's name")
	}
	if len(fake.lastDeletePolicyInputs) != 1 || aws.ToString(fake.lastDeletePolicyInputs[0].PolicyArn) != "arn:aws:iam::123456789012:policy/test-role-policy" {
		t.Errorf("expected the dedicated customer-managed policy to be deleted, got: %+v", fake.lastDeletePolicyInputs)
	}
}

func TestDeleteIAMRole_NeverDeletesAWSManagedPolicy(t *testing.T) {
	fake := &fakeIAMClient{}
	detail := IAMRoleDetail{
		Name: "test-role",
		AttachedPolicies: []IAMPolicyRef{
			{Name: "test-role-policy", ARN: ssmManagedInstanceCorePolicyArn}, // pathological: named like the dedicated policy but is AWS-managed
		},
	}

	err := deleteIAMRole(context.Background(), fake, detail)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.lastDeletePolicyInputs) != 0 {
		t.Error("did not expect an AWS-managed policy to ever be deleted")
	}
}

func TestDeleteIAMRole_LeavesDedicatedPolicyIfStillUsedElsewhere(t *testing.T) {
	fake := &fakeIAMClient{
		entitiesByPolicyArn: map[string]iam.ListEntitiesForPolicyOutput{
			"arn:aws:iam::123456789012:policy/test-role-policy": {
				PolicyRoles: []iamtypes.PolicyRole{{RoleName: aws.String("some-other-role")}},
			},
		},
	}
	detail := IAMRoleDetail{
		Name: "test-role",
		AttachedPolicies: []IAMPolicyRef{
			{Name: "test-role-policy", ARN: "arn:aws:iam::123456789012:policy/test-role-policy"},
		},
	}

	err := deleteIAMRole(context.Background(), fake, detail)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.lastDeletePolicyInputs) != 0 {
		t.Error("did not expect the dedicated policy to be deleted while still attached elsewhere")
	}
}

func TestDeleteIAMRole_DeletesNonDefaultPolicyVersionsFirst(t *testing.T) {
	arn := "arn:aws:iam::123456789012:policy/test-role-policy"
	fake := &fakeIAMClient{
		entitiesByPolicyArn: map[string]iam.ListEntitiesForPolicyOutput{arn: {}},
		policyVersionsByArn: map[string][]iamtypes.PolicyVersion{
			arn: {
				{VersionId: aws.String("v1"), IsDefaultVersion: false},
				{VersionId: aws.String("v2"), IsDefaultVersion: true},
			},
		},
	}
	detail := IAMRoleDetail{
		Name:             "test-role",
		AttachedPolicies: []IAMPolicyRef{{Name: "test-role-policy", ARN: arn}},
	}

	err := deleteIAMRole(context.Background(), fake, detail)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.lastDeletePolicyVersionInputs) != 1 || aws.ToString(fake.lastDeletePolicyVersionInputs[0].VersionId) != "v1" {
		t.Errorf("expected only the non-default version v1 to be deleted, got: %+v", fake.lastDeletePolicyVersionInputs)
	}
	if len(fake.lastDeletePolicyInputs) != 1 {
		t.Error("expected the policy itself to be deleted after clearing non-default versions")
	}
}

func TestDeleteIAMRole_PropagatesErrorsAtEachStep(t *testing.T) {
	base := IAMRoleDetail{
		Name:              "test-role",
		InlinePolicyNames: []string{"InlinePolicyA"},
		AttachedPolicies:  []IAMPolicyRef{{Name: "SomePolicy", ARN: "arn:aws:iam::123456789012:policy/SomePolicy"}},
	}

	t.Run("DeleteRolePolicy", func(t *testing.T) {
		fake := &fakeIAMClient{deleteRolePolicyErr: errors.New("boom")}
		if err := deleteIAMRole(context.Background(), fake, base); err == nil {
			t.Error("expected an error")
		}
	})
	t.Run("DetachRolePolicy", func(t *testing.T) {
		fake := &fakeIAMClient{detachRolePolicyErr: errors.New("boom")}
		if err := deleteIAMRole(context.Background(), fake, base); err == nil {
			t.Error("expected an error")
		}
	})
	t.Run("DeleteRole", func(t *testing.T) {
		fake := &fakeIAMClient{
			deleteRoleErr:       errors.New("boom"),
			entitiesByPolicyArn: map[string]iam.ListEntitiesForPolicyOutput{"arn:aws:iam::123456789012:policy/SomePolicy": {}},
		}
		if err := deleteIAMRole(context.Background(), fake, base); err == nil {
			t.Error("expected an error")
		}
	})
}

func TestFilterDLDOwnedRoles(t *testing.T) {
	roles := []inventory.IAMRoleSummary{
		{Name: "dld-role", DLDOwned: true},
		{Name: "imss-role", DLDOwned: false},
	}
	got := filterDLDOwnedRoles(roles)
	if len(got) != 1 || got[0].Name != "dld-role" {
		t.Errorf("got %+v, want only dld-role", got)
	}
}

func TestDeleteIAMRoleWorkflow_NoDLDOwnedRolesFound(t *testing.T) {
	var buf strings.Builder
	fake := &fakeIAMClient{
		roles: []iamtypes.Role{{RoleName: aws.String("imss-role")}}, // untagged -> not DLD-owned
	}
	err := deleteIAMRoleWorkflow(context.Background(), &buf, fake, config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No DLD-owned IAM roles found") {
		t.Errorf("expected a no-DLD-owned-roles message, got:\n%s", buf.String())
	}
}

func TestDeleteIAMRoleConfirmed_DeletesWhenConfirmed(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{}
	detail := IAMRoleDetail{Name: "test-role", Tags: map[string]string{"Origin": "DLD"}}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	err := deleteIAMRoleConfirmed(context.Background(), term, fake, originTag, detail, newHuhAccessibleInput("test-role\n"), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastDeleteRoleInput == nil {
		t.Fatal("expected the role to be deleted after a matching type-to-confirm")
	}
	if !strings.Contains(buf.String(), "Deleted role test-role") {
		t.Errorf("expected a success message, got:\n%s", buf.String())
	}
}

func TestDeleteIAMRoleConfirmed_RefusesWhenReferencedByProfilesBeforeConfirming(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{}
	detail := IAMRoleDetail{
		Name:                 "test-role",
		Tags:                 map[string]string{"Origin": "DLD"},
		ReferencedByProfiles: []string{"test-role-profile"},
	}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	// No confirm input at all -- if the flow tried to confirm before
	// checking ReferencedByProfiles, this would hang/error on empty
	// input instead of refusing cleanly first.
	err := deleteIAMRoleConfirmed(context.Background(), term, fake, originTag, detail, newHuhAccessibleInput(""), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastDeleteRoleInput != nil {
		t.Error("did not expect DeleteRole to be called")
	}
	if !strings.Contains(buf.String(), "test-role-profile") {
		t.Errorf("expected the error message to name the referencing profile, got:\n%s", buf.String())
	}
}

func TestDeleteIAMRoleConfirmed_DeclinedConfirmationSkipsDeletion(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{}
	detail := IAMRoleDetail{Name: "test-role", Tags: map[string]string{"Origin": "DLD"}}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	err := deleteIAMRoleConfirmed(context.Background(), term, fake, originTag, detail, newHuhAccessibleInput("wrong-name\n"), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastDeleteRoleInput != nil {
		t.Error("did not expect DeleteRole to be called after a mismatched confirmation")
	}
}

func TestDeleteIAMRoleConfirmed_RequireDLDOwnedBlocksNonDLDRole(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{}
	detail := IAMRoleDetail{Name: "imss-role", Tags: map[string]string{"Origin": "IMSS"}}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	err := deleteIAMRoleConfirmed(context.Background(), term, fake, originTag, detail, newHuhAccessibleInput("imss-role\n"), buf)
	if err == nil {
		t.Fatal("expected RequireDLDOwned to refuse a non-DLD-owned role")
	}
	if fake.lastDeleteRoleInput != nil {
		t.Error("did not expect DeleteRole to be called")
	}
}

func TestDeleteIAMRoleConfirmed_PropagatesDeleteError(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{deleteRoleErr: errors.New("boom")}
	detail := IAMRoleDetail{Name: "test-role", Tags: map[string]string{"Origin": "DLD"}}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	err := deleteIAMRoleConfirmed(context.Background(), term, fake, originTag, detail, newHuhAccessibleInput("test-role\n"), buf)
	if err == nil {
		t.Fatal("expected the delete error to propagate")
	}
}

func TestAttachPolicyToRoleWorkflow_NoDLDOwnedRolesFound(t *testing.T) {
	var buf strings.Builder
	fake := &fakeIAMClient{
		roles: []iamtypes.Role{{RoleName: aws.String("imss-role")}}, // untagged -> not DLD-owned
	}
	err := attachPolicyToRoleWorkflow(context.Background(), &buf, fake, config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No DLD-owned IAM roles found") {
		t.Errorf("expected a no-DLD-owned-roles message, got:\n%s", buf.String())
	}
}

func TestAttachPolicyToRoleConfirmed_AttachesWhenConfirmed(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{}
	role := inventory.IAMRoleSummary{Name: "test-role", Tags: map[string]string{"Origin": "DLD"}}
	policy := inventory.IAMPolicySummary{Name: "test-policy", ARN: "arn:aws:iam::123456789012:policy/test-policy"}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	err := attachPolicyToRoleConfirmed(context.Background(), term, fake, originTag, role, policy, newHuhAccessibleInput("y\n"), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.lastAttachRolePolicyInputs) != 1 {
		t.Fatalf("expected exactly one AttachRolePolicy call, got %d", len(fake.lastAttachRolePolicyInputs))
	}
	got := fake.lastAttachRolePolicyInputs[0]
	if aws.ToString(got.RoleName) != "test-role" || aws.ToString(got.PolicyArn) != policy.ARN {
		t.Errorf("unexpected AttachRolePolicy input: %+v", got)
	}
	if !strings.Contains(buf.String(), "Attached policy test-policy to role test-role") {
		t.Errorf("expected a success message, got:\n%s", buf.String())
	}
}

func TestAttachPolicyToRoleConfirmed_DeclinedConfirmationSkipsAttach(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{}
	role := inventory.IAMRoleSummary{Name: "test-role", Tags: map[string]string{"Origin": "DLD"}}
	policy := inventory.IAMPolicySummary{Name: "test-policy", ARN: "arn:aws:iam::123456789012:policy/test-policy"}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	err := attachPolicyToRoleConfirmed(context.Background(), term, fake, originTag, role, policy, newHuhAccessibleInput("n\n"), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.lastAttachRolePolicyInputs) != 0 {
		t.Error("did not expect AttachRolePolicy to be called after a declined confirmation")
	}
}

func TestAttachPolicyToRoleConfirmed_RequireDLDOwnedBlocksNonDLDRole(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{}
	role := inventory.IAMRoleSummary{Name: "imss-role", Tags: map[string]string{"Origin": "IMSS"}}
	policy := inventory.IAMPolicySummary{Name: "test-policy", ARN: "arn:aws:iam::123456789012:policy/test-policy"}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	err := attachPolicyToRoleConfirmed(context.Background(), term, fake, originTag, role, policy, newHuhAccessibleInput("y\n"), buf)
	if err == nil {
		t.Fatal("expected RequireDLDOwned to refuse a non-DLD-owned role")
	}
	if len(fake.lastAttachRolePolicyInputs) != 0 {
		t.Error("did not expect AttachRolePolicy to be called")
	}
}

func TestAttachPolicyToRoleConfirmed_PropagatesAttachError(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{attachRolePolicyErr: errors.New("boom")}
	role := inventory.IAMRoleSummary{Name: "test-role", Tags: map[string]string{"Origin": "DLD"}}
	policy := inventory.IAMPolicySummary{Name: "test-policy", ARN: "arn:aws:iam::123456789012:policy/test-policy"}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	err := attachPolicyToRoleConfirmed(context.Background(), term, fake, originTag, role, policy, newHuhAccessibleInput("y\n"), buf)
	if err == nil {
		t.Fatal("expected the attach error to propagate")
	}
}

func TestDetachPolicyFromRoleWorkflow_NoDLDOwnedRolesFound(t *testing.T) {
	var buf strings.Builder
	fake := &fakeIAMClient{
		roles: []iamtypes.Role{{RoleName: aws.String("imss-role")}}, // untagged -> not DLD-owned
	}
	err := detachPolicyFromRoleWorkflow(context.Background(), &buf, fake, config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No DLD-owned IAM roles found") {
		t.Errorf("expected a no-DLD-owned-roles message, got:\n%s", buf.String())
	}
}

func TestDetachPolicyFromRoleConfirmed_NoAttachedPolicies(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{}
	detail := IAMRoleDetail{Name: "test-role", Tags: map[string]string{"Origin": "DLD"}}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	// No menu input at all -- if the flow tried to pick a policy before
	// checking AttachedPolicies is empty, this would hang/error instead
	// of refusing cleanly first.
	err := detachPolicyFromRoleConfirmed(context.Background(), term, fake, originTag, detail, newHuhAccessibleInput(""), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "no attached managed policies") {
		t.Errorf("expected a no-attached-policies message, got:\n%s", buf.String())
	}
}

func TestDetachPolicyFromRoleConfirmed_DetachesWhenConfirmed(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{}
	detail := IAMRoleDetail{
		Name: "test-role",
		Tags: map[string]string{"Origin": "DLD"},
		AttachedPolicies: []IAMPolicyRef{
			{Name: "test-policy", ARN: "arn:aws:iam::123456789012:policy/test-policy"},
		},
	}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	// First line picks the (only) policy from the Select, second
	// confirms the Confirm prompt.
	err := detachPolicyFromRoleConfirmed(context.Background(), term, fake, originTag, detail, newHuhAccessibleInput("\ny\n"), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.lastDetachRolePolicyInputs) != 1 {
		t.Fatalf("expected exactly one DetachRolePolicy call, got %d", len(fake.lastDetachRolePolicyInputs))
	}
	got := fake.lastDetachRolePolicyInputs[0]
	if aws.ToString(got.RoleName) != "test-role" || aws.ToString(got.PolicyArn) != "arn:aws:iam::123456789012:policy/test-policy" {
		t.Errorf("unexpected DetachRolePolicy input: %+v", got)
	}
	if !strings.Contains(buf.String(), "Detached policy test-policy from role test-role") {
		t.Errorf("expected a success message, got:\n%s", buf.String())
	}
}

func TestDetachPolicyFromRoleConfirmed_RequireDLDOwnedBlocksNonDLDRole(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{}
	detail := IAMRoleDetail{
		Name: "imss-role",
		Tags: map[string]string{"Origin": "IMSS"},
		AttachedPolicies: []IAMPolicyRef{
			{Name: "test-policy", ARN: "arn:aws:iam::123456789012:policy/test-policy"},
		},
	}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	err := detachPolicyFromRoleConfirmed(context.Background(), term, fake, originTag, detail, newHuhAccessibleInput("\ny\n"), buf)
	if err == nil {
		t.Fatal("expected RequireDLDOwned to refuse a non-DLD-owned role")
	}
	if len(fake.lastDetachRolePolicyInputs) != 0 {
		t.Error("did not expect DetachRolePolicy to be called")
	}
}

func TestDetachPolicyFromRoleConfirmed_PropagatesDetachError(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{detachRolePolicyErr: errors.New("boom")}
	detail := IAMRoleDetail{
		Name: "test-role",
		Tags: map[string]string{"Origin": "DLD"},
		AttachedPolicies: []IAMPolicyRef{
			{Name: "test-policy", ARN: "arn:aws:iam::123456789012:policy/test-policy"},
		},
	}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	err := detachPolicyFromRoleConfirmed(context.Background(), term, fake, originTag, detail, newHuhAccessibleInput("\ny\n"), buf)
	if err == nil {
		t.Fatal("expected the detach error to propagate")
	}
}

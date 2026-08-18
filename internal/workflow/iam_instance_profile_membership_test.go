package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

func TestRemoveRoleFromInstanceProfileWorkflow_NoDLDOwnedRolesFound(t *testing.T) {
	var buf strings.Builder
	fake := &fakeIAMClient{
		roles: []iamtypes.Role{{RoleName: aws.String("imss-role")}}, // untagged -> not DLD-owned
	}
	err := removeRoleFromInstanceProfileWorkflow(context.Background(), &buf, fake, config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No DLD-owned IAM roles found") {
		t.Errorf("expected a no-DLD-owned-roles message, got:\n%s", buf.String())
	}
}

func TestRemoveRoleFromInstanceProfileConfirmed_NotAMemberOfAnyProfile(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{}
	detail := IAMRoleDetail{Name: "test-role", Tags: map[string]string{"Origin": "DLD"}}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	// No menu input at all -- if the flow tried to pick a profile before
	// checking ReferencedByProfiles is empty, this would hang/error
	// instead of refusing cleanly first.
	err := removeRoleFromInstanceProfileConfirmed(context.Background(), term, fake, originTag, detail, newHuhAccessibleInput(""), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "not a member of any instance profile") {
		t.Errorf("expected a not-a-member message, got:\n%s", buf.String())
	}
}

func TestRemoveRoleFromInstanceProfileConfirmed_RemovesWhenConfirmed(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{}
	detail := IAMRoleDetail{
		Name:                 "test-role",
		Tags:                 map[string]string{"Origin": "DLD"},
		ReferencedByProfiles: []string{"test-role-profile"},
	}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	// First line picks the (only) profile from the Select, second
	// confirms the Confirm prompt.
	err := removeRoleFromInstanceProfileConfirmed(context.Background(), term, fake, originTag, detail, newHuhAccessibleInput("\ny\n"), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.lastRemoveRoleFromInstanceProfileInputs) != 1 {
		t.Fatalf("expected exactly one RemoveRoleFromInstanceProfile call, got %d", len(fake.lastRemoveRoleFromInstanceProfileInputs))
	}
	got := fake.lastRemoveRoleFromInstanceProfileInputs[0]
	if aws.ToString(got.RoleName) != "test-role" || aws.ToString(got.InstanceProfileName) != "test-role-profile" {
		t.Errorf("unexpected RemoveRoleFromInstanceProfile input: %+v", got)
	}
	if !strings.Contains(buf.String(), "Removed role test-role from instance profile test-role-profile") {
		t.Errorf("expected a success message, got:\n%s", buf.String())
	}
}

func TestRemoveRoleFromInstanceProfileConfirmed_DeclinedConfirmationSkipsRemoval(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{}
	detail := IAMRoleDetail{
		Name:                 "test-role",
		Tags:                 map[string]string{"Origin": "DLD"},
		ReferencedByProfiles: []string{"test-role-profile"},
	}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	err := removeRoleFromInstanceProfileConfirmed(context.Background(), term, fake, originTag, detail, newHuhAccessibleInput("\nn\n"), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.lastRemoveRoleFromInstanceProfileInputs) != 0 {
		t.Error("did not expect RemoveRoleFromInstanceProfile to be called after a declined confirmation")
	}
}

func TestRemoveRoleFromInstanceProfileConfirmed_RequireDLDOwnedBlocksNonDLDRole(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{}
	detail := IAMRoleDetail{
		Name:                 "imss-role",
		Tags:                 map[string]string{"Origin": "IMSS"},
		ReferencedByProfiles: []string{"imss-role-profile"},
	}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	err := removeRoleFromInstanceProfileConfirmed(context.Background(), term, fake, originTag, detail, newHuhAccessibleInput("\ny\n"), buf)
	if err == nil {
		t.Fatal("expected RequireDLDOwned to refuse a non-DLD-owned role")
	}
	if len(fake.lastRemoveRoleFromInstanceProfileInputs) != 0 {
		t.Error("did not expect RemoveRoleFromInstanceProfile to be called")
	}
}

func TestRemoveRoleFromInstanceProfileConfirmed_PropagatesRemoveError(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{removeRoleFromInstanceProfileErr: errors.New("boom")}
	detail := IAMRoleDetail{
		Name:                 "test-role",
		Tags:                 map[string]string{"Origin": "DLD"},
		ReferencedByProfiles: []string{"test-role-profile"},
	}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	err := removeRoleFromInstanceProfileConfirmed(context.Background(), term, fake, originTag, detail, newHuhAccessibleInput("\ny\n"), buf)
	if err == nil {
		t.Fatal("expected the remove error to propagate")
	}
}

func TestDeleteInstanceProfileWorkflow_NoDLDOwnedInstanceProfilesFound(t *testing.T) {
	var buf strings.Builder
	fake := &fakeIAMClient{
		instanceProfiles: []iamtypes.InstanceProfile{{InstanceProfileName: aws.String("imss-profile")}}, // untagged -> not DLD-owned
	}
	err := deleteInstanceProfileWorkflow(context.Background(), &buf, fake, config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No DLD-owned IAM instance profiles found") {
		t.Errorf("expected a no-DLD-owned-instance-profiles message, got:\n%s", buf.String())
	}
}

func TestDeleteInstanceProfileConfirmed_DeletesWhenConfirmed(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{}
	profile := inventory.IAMInstanceProfileSummary{Name: "test-role-profile", Tags: map[string]string{"Origin": "DLD"}}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	err := deleteInstanceProfileConfirmed(context.Background(), term, fake, originTag, profile, newHuhAccessibleInput("y\n"), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.lastDeleteInstanceProfileInputs) != 1 {
		t.Fatalf("expected exactly one DeleteInstanceProfile call, got %d", len(fake.lastDeleteInstanceProfileInputs))
	}
	if aws.ToString(fake.lastDeleteInstanceProfileInputs[0].InstanceProfileName) != "test-role-profile" {
		t.Errorf("unexpected DeleteInstanceProfile input: %+v", fake.lastDeleteInstanceProfileInputs[0])
	}
	if !strings.Contains(buf.String(), "Deleted instance profile test-role-profile") {
		t.Errorf("expected a success message, got:\n%s", buf.String())
	}
}

func TestDeleteInstanceProfileConfirmed_DeclinedConfirmationSkipsDeletion(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{}
	profile := inventory.IAMInstanceProfileSummary{Name: "test-role-profile", Tags: map[string]string{"Origin": "DLD"}}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	err := deleteInstanceProfileConfirmed(context.Background(), term, fake, originTag, profile, newHuhAccessibleInput("n\n"), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.lastDeleteInstanceProfileInputs) != 0 {
		t.Error("did not expect DeleteInstanceProfile to be called after a declined confirmation")
	}
}

func TestDeleteInstanceProfileConfirmed_RequireDLDOwnedBlocksNonDLDProfile(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{}
	profile := inventory.IAMInstanceProfileSummary{Name: "imss-profile", Tags: map[string]string{"Origin": "IMSS"}}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	err := deleteInstanceProfileConfirmed(context.Background(), term, fake, originTag, profile, newHuhAccessibleInput("y\n"), buf)
	if err == nil {
		t.Fatal("expected RequireDLDOwned to refuse a non-DLD-owned instance profile")
	}
	if len(fake.lastDeleteInstanceProfileInputs) != 0 {
		t.Error("did not expect DeleteInstanceProfile to be called")
	}
}

// TestDeleteInstanceProfileConfirmed_PropagatesDeleteError confirms AWS's
// own "still has a role attached" precondition (DeleteConflict) surfaces
// verbatim -- this is deliberately not pre-checked client-side
// (DECISIONS.md, "Delete Role: correct the wrong-remedy message, add the
// missing instance-profile-membership actions").
func TestDeleteInstanceProfileConfirmed_PropagatesDeleteError(t *testing.T) {
	term, buf := newTermOnly()
	fake := &fakeIAMClient{deleteInstanceProfileErr: errors.New("DeleteConflict: role still attached")}
	profile := inventory.IAMInstanceProfileSummary{Name: "test-role-profile", Tags: map[string]string{"Origin": "DLD"}}
	originTag := config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}

	err := deleteInstanceProfileConfirmed(context.Background(), term, fake, originTag, profile, newHuhAccessibleInput("y\n"), buf)
	if err == nil {
		t.Fatal("expected the delete error to propagate")
	}
	if !strings.Contains(err.Error(), "DeleteConflict") {
		t.Errorf("expected AWS's own precondition error to surface verbatim, got: %v", err)
	}
}

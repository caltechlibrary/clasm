package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

func TestResolveCurrentInstanceProfileAssociation_NoneFound(t *testing.T) {
	fake := &fakeEC2Client{}
	id, found, err := resolveCurrentInstanceProfileAssociation(context.Background(), fake, "i-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found || id != "" {
		t.Errorf("got id=%q found=%v, want empty/false", id, found)
	}
}

func TestResolveCurrentInstanceProfileAssociation_Found(t *testing.T) {
	fake := &fakeEC2Client{currentInstanceProfileAssociationID: "iip-assoc-1"}
	id, found, err := resolveCurrentInstanceProfileAssociation(context.Background(), fake, "i-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || id != "iip-assoc-1" {
		t.Errorf("got id=%q found=%v, want iip-assoc-1/true", id, found)
	}
}

func TestResolveCurrentInstanceProfileAssociation_PropagatesError(t *testing.T) {
	fake := &fakeEC2Client{describeIamInstanceProfileAssociationsErr: errors.New("boom")}
	if _, _, err := resolveCurrentInstanceProfileAssociation(context.Background(), fake, "i-1"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestAssociateOrReplaceInstanceProfile_AssociatesWhenNoCurrentAssociation(t *testing.T) {
	ec2Fake := &fakeEC2Client{}
	iamFake := &fakeIAMClient{
		instanceProfiles: nil, // empty list, no free-text fallback since ListInstanceProfiles succeeds
	}
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": ec2Fake}
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", Region: "us-east-1"}

	term, le, buf := newPipeEditor("manual-profile\n")
	_ = buf
	// Force the free-text fallback path so this test doesn't need the
	// Picker-tier UI: promptIAMInstanceProfileOrCreate falls back to a
	// plain prompt only when listing profiles itself fails.
	iamFake.listInstanceProfilesErr = errors.New("access denied")

	err := associateOrReplaceInstanceProfile(context.Background(), term, ec2Clients, iamFake, inst, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ec2Fake.lastAssociateIamInstanceProfileInput == nil {
		t.Fatal("AssociateIamInstanceProfile was not called")
	}
	if aws.ToString(ec2Fake.lastAssociateIamInstanceProfileInput.InstanceId) != "i-1" {
		t.Errorf("InstanceId = %q, want i-1", aws.ToString(ec2Fake.lastAssociateIamInstanceProfileInput.InstanceId))
	}
	if aws.ToString(ec2Fake.lastAssociateIamInstanceProfileInput.IamInstanceProfile.Name) != "manual-profile" {
		t.Errorf("IamInstanceProfile.Name = %q, want manual-profile", aws.ToString(ec2Fake.lastAssociateIamInstanceProfileInput.IamInstanceProfile.Name))
	}
	if ec2Fake.lastReplaceIamInstanceProfileAssociationInput != nil {
		t.Error("ReplaceIamInstanceProfileAssociation should not be called when there's no current association")
	}
}

func TestAssociateOrReplaceInstanceProfile_ReplacesWhenAlreadyAssociated(t *testing.T) {
	ec2Fake := &fakeEC2Client{currentInstanceProfileAssociationID: "iip-assoc-1"}
	iamFake := &fakeIAMClient{listInstanceProfilesErr: errors.New("access denied")}
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": ec2Fake}
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", Region: "us-east-1"}

	term, le, buf := newPipeEditor("manual-profile\n")

	err := associateOrReplaceInstanceProfile(context.Background(), term, ec2Clients, iamFake, inst, le, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ec2Fake.lastReplaceIamInstanceProfileAssociationInput == nil {
		t.Fatal("ReplaceIamInstanceProfileAssociation was not called")
	}
	if aws.ToString(ec2Fake.lastReplaceIamInstanceProfileAssociationInput.AssociationId) != "iip-assoc-1" {
		t.Errorf("AssociationId = %q, want iip-assoc-1", aws.ToString(ec2Fake.lastReplaceIamInstanceProfileAssociationInput.AssociationId))
	}
	if aws.ToString(ec2Fake.lastReplaceIamInstanceProfileAssociationInput.IamInstanceProfile.Name) != "manual-profile" {
		t.Errorf("IamInstanceProfile.Name = %q, want manual-profile", aws.ToString(ec2Fake.lastReplaceIamInstanceProfileAssociationInput.IamInstanceProfile.Name))
	}
	if ec2Fake.lastAssociateIamInstanceProfileInput != nil {
		t.Error("AssociateIamInstanceProfile should not be called when there's already a current association")
	}
}

func TestAssociateOrReplaceInstanceProfile_PropagatesResolveError(t *testing.T) {
	ec2Fake := &fakeEC2Client{describeIamInstanceProfileAssociationsErr: errors.New("boom")}
	iamFake := &fakeIAMClient{}
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": ec2Fake}
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", Region: "us-east-1"}

	term, le, buf := newPipeEditor("")
	if err := associateOrReplaceInstanceProfile(context.Background(), term, ec2Clients, iamFake, inst, le, buf); err == nil {
		t.Fatal("expected an error")
	}
}

func TestAssociateOrReplaceInstanceProfile_PropagatesAssociateError(t *testing.T) {
	ec2Fake := &fakeEC2Client{associateIamInstanceProfileErr: errors.New("boom")}
	iamFake := &fakeIAMClient{listInstanceProfilesErr: errors.New("access denied")}
	ec2Clients := map[string]awsclient.EC2API{"us-east-1": ec2Fake}
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", Region: "us-east-1"}

	term, le, buf := newPipeEditor("manual-profile\n")
	if err := associateOrReplaceInstanceProfile(context.Background(), term, ec2Clients, iamFake, inst, le, buf); err == nil {
		t.Fatal("expected an error")
	}
}

func TestAssociateOrReplaceInstanceProfile_NoRegionClientErrors(t *testing.T) {
	ec2Clients := map[string]awsclient.EC2API{}
	iamFake := &fakeIAMClient{}
	inst := inventory.Instance{InstanceID: "i-1", Name: "web", Region: "us-west-2"}

	term, le, buf := newPipeEditor("")
	if err := associateOrReplaceInstanceProfile(context.Background(), term, ec2Clients, iamFake, inst, le, buf); err == nil {
		t.Fatal("expected an error for a missing region client")
	}
}

func TestAssociateOrReplaceInstanceProfile_NoInstancesReturnsWithoutError(t *testing.T) {
	term, buf := newTermOnly()
	err := AssociateOrReplaceInstanceProfile(context.Background(), term, nil, &fakeIAMClient{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.String() == "" {
		t.Error("expected a \"no instances\" message")
	}
}

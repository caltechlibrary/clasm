package workflow

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

func TestPromoteLaunchTemplateVersion_HappyPath(t *testing.T) {
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Region: "us-east-1"}

	input := "3\n" + "y\n"
	var buf bytes.Buffer
	err := promoteLaunchTemplateVersion(context.Background(), &buf, clients, lt, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	in := fake.lastModifyLaunchTemplateInput
	if in == nil {
		t.Fatal("ModifyLaunchTemplate was never called")
	}
	if aws.ToString(in.LaunchTemplateId) != "lt-1" {
		t.Errorf("LaunchTemplateId = %q, want lt-1", aws.ToString(in.LaunchTemplateId))
	}
	if aws.ToString(in.DefaultVersion) != "3" {
		t.Errorf("DefaultVersion = %q, want 3", aws.ToString(in.DefaultVersion))
	}
}

func TestPromoteLaunchTemplateVersion_DeclinedConfirmationDoesNotModify(t *testing.T) {
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Region: "us-east-1"}

	input := "3\n" + "n\n"
	var buf bytes.Buffer
	err := promoteLaunchTemplateVersion(context.Background(), &buf, clients, lt, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastModifyLaunchTemplateInput != nil {
		t.Error("ModifyLaunchTemplate was called despite a declined confirmation")
	}
}

func TestDeleteLaunchTemplateVersions_HappyPath(t *testing.T) {
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1"}

	input := "2, 3\n" + // versions to delete
		"lt-1\n" // type-to-confirm
	var buf bytes.Buffer
	err := deleteLaunchTemplateVersions(context.Background(), &buf, clients, lt, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	in := fake.lastDeleteLaunchTemplateVersionsInput
	if in == nil {
		t.Fatal("DeleteLaunchTemplateVersions was never called")
	}
	if len(in.Versions) != 2 || in.Versions[0] != "2" || in.Versions[1] != "3" {
		t.Errorf("Versions = %v, want [2 3]", in.Versions)
	}
}

func TestDeleteLaunchTemplateVersions_WrongIdentifierCancels(t *testing.T) {
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1"}

	input := "2\n" + "wrong-identifier\n"
	var buf bytes.Buffer
	err := deleteLaunchTemplateVersions(context.Background(), &buf, clients, lt, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastDeleteLaunchTemplateVersionsInput != nil {
		t.Error("DeleteLaunchTemplateVersions was called despite a failed type-to-confirm")
	}
}

func TestDeleteLaunchTemplateVersions_ProductionWarning(t *testing.T) {
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1", Environment: "production"}

	input := "2\n" + "lt-1\n"
	var buf bytes.Buffer
	if err := deleteLaunchTemplateVersions(context.Background(), &buf, clients, lt, newHuhAccessibleInput(input), &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "production") {
		t.Errorf("expected a production warning, got:\n%s", buf.String())
	}
}

func TestDeleteLaunchTemplateVersions_ReportsPartialFailure(t *testing.T) {
	fake := &fakeEC2Client{
		deleteLaunchTemplateVersionsUnsuccessful: []types.DeleteLaunchTemplateVersionsResponseErrorItem{
			{
				VersionNumber: aws.Int64(3),
				ResponseError: &types.ResponseError{Code: types.LaunchTemplateErrorCodeLaunchTemplateVersionDoesNotExist},
			},
		},
	}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1"}

	input := "2, 3\n" + "lt-1\n"
	var buf bytes.Buffer
	err := deleteLaunchTemplateVersions(context.Background(), &buf, clients, lt, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Deleted 1 version") {
		t.Errorf("expected exactly 1 successful deletion reported, got:\n%s", out)
	}
	if !strings.Contains(out, "FAILED to delete version 3") {
		t.Errorf("expected the failed version to be reported, got:\n%s", out)
	}
}

func TestDeleteLaunchTemplate_HappyPath(t *testing.T) {
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1"}

	input := "lt-1\n"
	var buf bytes.Buffer
	err := deleteLaunchTemplate(context.Background(), &buf, clients, lt, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	in := fake.lastDeleteLaunchTemplateInput
	if in == nil || aws.ToString(in.LaunchTemplateId) != "lt-1" {
		t.Errorf("DeleteLaunchTemplate called with %+v, want LaunchTemplateId=lt-1", in)
	}
}

func TestDeleteLaunchTemplate_WrongIdentifierCancels(t *testing.T) {
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1"}

	input := "wrong\n"
	var buf bytes.Buffer
	err := deleteLaunchTemplate(context.Background(), &buf, clients, lt, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastDeleteLaunchTemplateInput != nil {
		t.Error("DeleteLaunchTemplate was called despite a failed type-to-confirm")
	}
}

func TestDeleteLaunchTemplate_ProductionWarning(t *testing.T) {
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1", Environment: "production"}

	input := "lt-1\n"
	var buf bytes.Buffer
	if err := deleteLaunchTemplate(context.Background(), &buf, clients, lt, newHuhAccessibleInput(input), &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "production") {
		t.Errorf("expected a production warning, got:\n%s", buf.String())
	}
}

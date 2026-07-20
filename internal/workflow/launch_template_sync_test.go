package workflow

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

func TestDisplayDiff_AccessibleModeFallsBackToPlainDump(t *testing.T) {
	var buf bytes.Buffer
	// input non-nil signals accessible/test mode -- no real bubbletea
	// loop exists to drive a List-tier screen, so this must fall back
	// to a plain fmt dump rather than calling tui.RunListView (which
	// would hang waiting for a real terminal).
	err := displayDiff(context.Background(), &buf, "Diff title", "-old line\n+new line\n", strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "-old line") || !strings.Contains(out, "+new line") {
		t.Errorf("output missing diff content:\n%s", out)
	}
}

func launchTemplateVersionWithUserData(versionNumber int64, userData string) types.LaunchTemplateVersion {
	return types.LaunchTemplateVersion{
		LaunchTemplateId: aws.String("lt-1"),
		VersionNumber:    aws.Int64(versionNumber),
		DefaultVersion:   aws.Bool(true),
		LaunchTemplateData: &types.ResponseLaunchTemplateData{
			ImageId:  aws.String("ami-1"),
			UserData: aws.String(base64.StdEncoding.EncodeToString([]byte(userData))),
		},
	}
}

func TestCreateLaunchTemplateVersion_SetsSourceVersionAndUserData(t *testing.T) {
	fake := &fakeEC2Client{createLaunchTemplateVersionNumber: 5}
	got, err := createLaunchTemplateVersion(context.Background(), fake, "lt-1", "2", "#cloud-config\nnew content")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 5 {
		t.Errorf("got version %d, want 5", got)
	}
	in := fake.lastCreateLaunchTemplateVersionInput
	if aws.ToString(in.LaunchTemplateId) != "lt-1" {
		t.Errorf("LaunchTemplateId = %q, want lt-1", aws.ToString(in.LaunchTemplateId))
	}
	if aws.ToString(in.SourceVersion) != "2" {
		t.Errorf("SourceVersion = %q, want 2", aws.ToString(in.SourceVersion))
	}
	wantUserData := base64.StdEncoding.EncodeToString([]byte("#cloud-config\nnew content"))
	if aws.ToString(in.LaunchTemplateData.UserData) != wantUserData {
		t.Errorf("UserData = %q, want %q", aws.ToString(in.LaunchTemplateData.UserData), wantUserData)
	}
	if in.LaunchTemplateData.ImageId != nil {
		t.Error("expected ImageId to be left unset -- only UserData should be overridden, everything else inherited via SourceVersion")
	}
}

func TestSyncLaunchTemplate_IdenticalContentIsNoOp(t *testing.T) {
	path := writeCloudInitFixture(t, "#cloud-config\nsame content\n")
	fake := &fakeEC2Client{launchTemplateVersions: []types.LaunchTemplateVersion{
		launchTemplateVersionWithUserData(1, "#cloud-config\nsame content\n"),
	}}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Region: "us-east-1"}

	input := "\n" + // accept pre-filled $Default
		path + "\n"
	var buf bytes.Buffer
	err := syncLaunchTemplate(context.Background(), &buf, clients, lt, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No changes") {
		t.Errorf("expected a no-op message, got:\n%s", buf.String())
	}
	if fake.lastCreateLaunchTemplateVersionInput != nil {
		t.Error("CreateLaunchTemplateVersion was called despite identical content")
	}
}

func TestSyncLaunchTemplate_DifferentContentShowsDiffThenCreatesVersion(t *testing.T) {
	path := writeCloudInitFixture(t, "#cloud-config\nnew line\n")
	fake := &fakeEC2Client{
		launchTemplateVersions:            []types.LaunchTemplateVersion{launchTemplateVersionWithUserData(1, "#cloud-config\nold line\n")},
		createLaunchTemplateVersionNumber: 2,
	}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Region: "us-east-1"}

	input := "\n" + // accept pre-filled $Default
		path + "\n" +
		"y\n" // confirm creating a new version
	var buf bytes.Buffer
	err := syncLaunchTemplate(context.Background(), &buf, clients, lt, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "old line") || !strings.Contains(out, "new line") {
		t.Errorf("expected the diff to show both old and new content, got:\n%s", out)
	}
	in := fake.lastCreateLaunchTemplateVersionInput
	if in == nil {
		t.Fatal("CreateLaunchTemplateVersion was never called")
	}
	if aws.ToString(in.SourceVersion) != "1" {
		t.Errorf("SourceVersion = %q, want 1 (the resolved version, not the $Default selector)", aws.ToString(in.SourceVersion))
	}
	if !strings.Contains(out, "NOT the default version") {
		t.Errorf("expected a reminder that the new version isn't promoted, got:\n%s", out)
	}
}

func TestSyncLaunchTemplate_DeclinedConfirmationDoesNotCreateVersion(t *testing.T) {
	path := writeCloudInitFixture(t, "#cloud-config\nnew line\n")
	fake := &fakeEC2Client{
		launchTemplateVersions: []types.LaunchTemplateVersion{launchTemplateVersionWithUserData(1, "#cloud-config\nold line\n")},
	}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}
	lt := inventory.LaunchTemplate{TemplateID: "lt-1", Region: "us-east-1"}

	input := "\n" + path + "\n" + "n\n"
	var buf bytes.Buffer
	err := syncLaunchTemplate(context.Background(), &buf, clients, lt, newHuhAccessibleInput(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastCreateLaunchTemplateVersionInput != nil {
		t.Error("CreateLaunchTemplateVersion was called despite a declined confirmation")
	}
}

package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestDefaultAMIName(t *testing.T) {
	got := defaultAMIName("newauthors", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	want := "newauthors-copy-2026-07-01"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCreateAMI_Success(t *testing.T) {
	fake := &fakeEC2Client{createImageID: "ami-abc123"}
	params := CreateAMIParams{
		InstanceID:  "i-1",
		Name:        "newauthors-copy-2026-07-01",
		Description: "manual copy",
		NoReboot:    true,
		Tags:        map[string]string{"Name": "newauthors-copy", "Project": "caltechauthors", "Environment": "test"},
	}

	gotID, err := CreateAMI(context.Background(), fake, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotID != "ami-abc123" {
		t.Errorf("got %q, want %q", gotID, "ami-abc123")
	}

	in := fake.lastCreateImageInput
	if in == nil {
		t.Fatal("CreateImage was never called")
	}
	if aws.ToString(in.InstanceId) != "i-1" {
		t.Errorf("InstanceId = %q, want %q", aws.ToString(in.InstanceId), "i-1")
	}
	if aws.ToString(in.Name) != "newauthors-copy-2026-07-01" {
		t.Errorf("Name = %q, want %q", aws.ToString(in.Name), "newauthors-copy-2026-07-01")
	}
	if aws.ToString(in.Description) != "manual copy" {
		t.Errorf("Description = %q, want %q", aws.ToString(in.Description), "manual copy")
	}
	if !aws.ToBool(in.NoReboot) {
		t.Error("NoReboot = false, want true")
	}
	if len(in.TagSpecifications) != 1 || in.TagSpecifications[0].ResourceType != types.ResourceTypeImage {
		t.Fatalf("TagSpecifications = %+v, want one image-scoped spec", in.TagSpecifications)
	}
	if len(in.TagSpecifications[0].Tags) != 3 {
		t.Errorf("Tags = %+v, want 3 entries", in.TagSpecifications[0].Tags)
	}
}

func TestCreateAMI_PropagatesError(t *testing.T) {
	fake := &fakeEC2Client{createImageErr: errors.New("boom")}
	_, err := CreateAMI(context.Background(), fake, CreateAMIParams{InstanceID: "i-1", Name: "x"})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestWaitForAMIAvailable_BecomesAvailableAfterPolling(t *testing.T) {
	fake := &fakeEC2Client{imageAvailableAfterCall: 3}
	state, err := WaitForAMIAvailable(context.Background(), fake, "ami-1", testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != types.ImageStateAvailable {
		t.Errorf("state = %v, want Available", state)
	}
	if fake.describeImagesCalls < 3 {
		t.Errorf("describeImagesCalls = %d, want at least 3", fake.describeImagesCalls)
	}
}

func TestWaitForAMIAvailable_ReportsFailed(t *testing.T) {
	fake := &fakeEC2Client{imageFailedAfterCall: 2}
	state, err := WaitForAMIAvailable(context.Background(), fake, "ami-1", testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != types.ImageStateFailed {
		t.Errorf("state = %v, want Failed", state)
	}
}

func TestWaitForAMIAvailable_RespectsContextCancellation(t *testing.T) {
	fake := &fakeEC2Client{} // never available or failed
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := WaitForAMIAvailable(ctx, fake, "ami-1", testPollInterval)
	if err == nil {
		t.Fatal("expected the context cancellation to surface as an error")
	}
}

func TestWaitForAMIAvailable_PropagatesError(t *testing.T) {
	fake := &fakeEC2Client{describeImagesErr: errors.New("boom")}
	_, err := WaitForAMIAvailable(context.Background(), fake, "ami-1", testPollInterval)
	if err == nil {
		t.Fatal("expected an error")
	}
}

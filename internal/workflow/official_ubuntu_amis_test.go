package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

const nobleNamePattern = "ubuntu/images/hvm-ssd*/ubuntu-noble-24.04-amd64-server-*"

func TestLatestUbuntuAMI_ReturnsMostRecent(t *testing.T) {
	fake := &fakeEC2Client{officialUbuntuImages: map[string][]types.Image{
		nobleNamePattern: {
			{ImageId: aws.String("ami-old"), CreationDate: aws.String("2026-01-01T00:00:00.000Z")},
			{ImageId: aws.String("ami-new"), CreationDate: aws.String("2026-06-01T00:00:00.000Z")},
		},
	}}

	got, err := latestUbuntuAMI(context.Background(), fake, nobleNamePattern)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || aws.ToString(got.ImageId) != "ami-new" {
		t.Errorf("got %+v, want ami-new", got)
	}
}

func TestLatestUbuntuAMI_ReturnsNilWhenNoMatch(t *testing.T) {
	fake := &fakeEC2Client{officialUbuntuImages: map[string][]types.Image{}}

	got, err := latestUbuntuAMI(context.Background(), fake, nobleNamePattern)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestLatestUbuntuAMI_PropagatesError(t *testing.T) {
	fake := &fakeEC2Client{describeUbuntuImagesErr: errors.New("boom")}

	_, err := latestUbuntuAMI(context.Background(), fake, nobleNamePattern)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestListOfficialUbuntuAMIsInRegion_SkipsReleasesWithNoMatch(t *testing.T) {
	fake := &fakeEC2Client{officialUbuntuImages: map[string][]types.Image{
		nobleNamePattern: {
			{ImageId: aws.String("ami-noble"), CreationDate: aws.String("2026-06-01T00:00:00.000Z"), EnaSupport: aws.Bool(true)},
		},
		// jammy pattern intentionally not configured -- should be skipped, not an error
	}}

	got, err := listOfficialUbuntuAMIsInRegion(context.Background(), fake, "us-west-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d images, want 1 (only Noble matched)", len(got))
	}
	if got[0].ImageID != "ami-noble" || got[0].Region != "us-west-2" {
		t.Errorf("got %+v", got[0])
	}
	if !got[0].EnaSupport {
		t.Error("EnaSupport = false, want true (must carry through so the ENA pre-flight check doesn't false-positive on a modern official AMI)")
	}
}

func TestListOfficialUbuntuAMIsInRegion_PropagatesError(t *testing.T) {
	fake := &fakeEC2Client{describeUbuntuImagesErr: errors.New("boom")}

	_, err := listOfficialUbuntuAMIsInRegion(context.Background(), fake, "us-west-2")
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestListOfficialUbuntuAMIs_AggregatesAcrossRegions(t *testing.T) {
	clients := map[string]awsclient.EC2API{
		"us-east-1": &fakeEC2Client{officialUbuntuImages: map[string][]types.Image{
			nobleNamePattern: {{ImageId: aws.String("ami-east"), CreationDate: aws.String("2026-06-01T00:00:00.000Z")}},
		}},
		"us-west-2": &fakeEC2Client{officialUbuntuImages: map[string][]types.Image{
			nobleNamePattern: {{ImageId: aws.String("ami-west"), CreationDate: aws.String("2026-06-01T00:00:00.000Z")}},
		}},
	}

	got, err := listOfficialUbuntuAMIs(context.Background(), clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d images, want 2 (one per region)", len(got))
	}
}

func TestImagesWithOfficialUbuntu_AppendsToOwnedImages(t *testing.T) {
	owned := []inventory.Image{{ImageID: "ami-owned", Region: "us-west-2"}}
	clients := map[string]awsclient.EC2API{
		"us-west-2": &fakeEC2Client{officialUbuntuImages: map[string][]types.Image{
			nobleNamePattern: {{ImageId: aws.String("ami-noble"), CreationDate: aws.String("2026-06-01T00:00:00.000Z")}},
		}},
	}

	got := imagesWithOfficialUbuntu(context.Background(), clients, owned)
	if len(got) != 2 {
		t.Fatalf("got %d images, want 2 (1 owned + 1 official)", len(got))
	}
}

func TestImagesWithOfficialUbuntu_FallsBackOnError(t *testing.T) {
	owned := []inventory.Image{{ImageID: "ami-owned", Region: "us-west-2"}}
	clients := map[string]awsclient.EC2API{
		"us-west-2": &fakeEC2Client{describeUbuntuImagesErr: errors.New("access denied")},
	}

	got := imagesWithOfficialUbuntu(context.Background(), clients, owned)
	if len(got) != 1 || got[0].ImageID != "ami-owned" {
		t.Errorf("got %+v, want just the owned image unchanged when the lookup fails", got)
	}
}

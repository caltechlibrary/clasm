package inventory

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

func sdkImage(id, name, creationDate, state, project, environment string) types.Image {
	var tags []types.Tag
	if name != "" {
		tags = append(tags, types.Tag{Key: aws.String("Name"), Value: aws.String(name)})
	}
	if project != "" {
		tags = append(tags, types.Tag{Key: aws.String("Project"), Value: aws.String(project)})
	}
	if environment != "" {
		tags = append(tags, types.Tag{Key: aws.String("Environment"), Value: aws.String(environment)})
	}
	return types.Image{
		ImageId:      aws.String(id),
		Name:         aws.String(name),
		CreationDate: aws.String(creationDate),
		State:        types.ImageState(state),
		Tags:         tags,
	}
}

func sortImages(images []Image) {
	sort.Slice(images, func(i, j int) bool { return images[i].ImageID < images[j].ImageID })
}

func TestListImages_AggregatesAcrossRegions(t *testing.T) {
	clients := map[string]awsclient.EC2API{
		"us-east-1": &fakeEC2Client{images: []types.Image{
			sdkImage("ami-1", "base-ubuntu", "2026-01-15", "available", "caltechauthors", "production"),
		}},
		"us-west-2": &fakeEC2Client{images: []types.Image{
			sdkImage("ami-2", "app-server-v2", "2026-02-20", "available", "caltechdata", "development"),
		}},
	}

	got, err := ListImages(context.Background(), clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sortImages(got)

	want := []Image{
		{ImageID: "ami-1", Name: "base-ubuntu", CreationDate: "2026-01-15", Region: "us-east-1", Project: "caltechauthors", Environment: "production",
			Tags: map[string]string{"Name": "base-ubuntu", "Project": "caltechauthors", "Environment": "production"}},
		{ImageID: "ami-2", Name: "app-server-v2", CreationDate: "2026-02-20", Region: "us-west-2", Project: "caltechdata", Environment: "development",
			Tags: map[string]string{"Name": "app-server-v2", "Project": "caltechdata", "Environment": "development"}},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d images, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("got[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestListImages_FiltersToAvailable(t *testing.T) {
	clients := map[string]awsclient.EC2API{
		"us-east-1": &fakeEC2Client{images: []types.Image{
			sdkImage("ami-1", "ready", "2026-01-15", "available", "", ""),
			sdkImage("ami-2", "still-copying", "2026-01-16", "pending", "", ""),
			sdkImage("ami-3", "broken", "2026-01-17", "failed", "", ""),
		}},
	}

	got, err := ListImages(context.Background(), clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ImageID != "ami-1" {
		t.Fatalf("got %+v, want only ami-1", got)
	}
}

func TestListImages_ScopesToOwnedByAccount(t *testing.T) {
	fake := &fakeEC2Client{images: nil}
	clients := map[string]awsclient.EC2API{"us-east-1": fake}

	if _, err := ListImages(context.Background(), clients); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastDescribeImagesInput == nil {
		t.Fatal("DescribeImages was never called")
	}
	if len(fake.lastDescribeImagesInput.Owners) != 1 || fake.lastDescribeImagesInput.Owners[0] != "self" {
		t.Errorf("Owners = %v, want [self]", fake.lastDescribeImagesInput.Owners)
	}
}

func TestListImages_EmptyRegion(t *testing.T) {
	clients := map[string]awsclient.EC2API{
		"us-east-1": &fakeEC2Client{images: nil},
	}
	got, err := ListImages(context.Background(), clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d images, want 0", len(got))
	}
}

func TestListImages_UntaggedResourceHasEmptyFields(t *testing.T) {
	clients := map[string]awsclient.EC2API{
		"us-east-1": &fakeEC2Client{images: []types.Image{
			{ImageId: aws.String("ami-1"), Name: aws.String("custom-ami"), CreationDate: aws.String("2026-03-10"), State: types.ImageStateAvailable},
		}},
	}
	got, err := ListImages(context.Background(), clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d images, want 1", len(got))
	}
	if got[0].Project != "" || got[0].Environment != "" {
		t.Errorf("got %+v, want empty Project/Environment", got[0])
	}
	if len(got[0].Tags) != 0 {
		t.Errorf("Tags = %+v, want empty", got[0].Tags)
	}
}

func TestImageFromSDK_CarriesFullTagMap(t *testing.T) {
	img := imageFromSDK(types.Image{
		ImageId: aws.String("ami-1"),
		Tags: []types.Tag{
			{Key: aws.String("Project"), Value: aws.String("caltechauthors")},
			{Key: aws.String("CostCenter"), Value: aws.String("1234")},
		},
	}, "us-east-1")

	want := map[string]string{"Project": "caltechauthors", "CostCenter": "1234"}
	if !reflect.DeepEqual(img.Tags, want) {
		t.Errorf("Tags = %+v, want %+v (a key outside the Name/Project/Environment convention must still appear)", img.Tags, want)
	}
}

func TestListImages_PropagatesError(t *testing.T) {
	clients := map[string]awsclient.EC2API{
		"us-east-1": &fakeEC2Client{err: errors.New("boom")},
	}
	_, err := ListImages(context.Background(), clients)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestImageFromSDK_CarriesEnaSupport(t *testing.T) {
	enaEnabled := imageFromSDK(types.Image{
		ImageId:    aws.String("ami-1"),
		EnaSupport: aws.Bool(true),
	}, "us-east-1")
	if !enaEnabled.EnaSupport {
		t.Error("EnaSupport = false, want true")
	}

	enaDisabled := imageFromSDK(types.Image{
		ImageId:    aws.String("ami-2"),
		EnaSupport: aws.Bool(false),
	}, "us-east-1")
	if enaDisabled.EnaSupport {
		t.Error("EnaSupport = true, want false")
	}

	enaUnset := imageFromSDK(types.Image{
		ImageId: aws.String("ami-3"),
	}, "us-east-1")
	if enaUnset.EnaSupport {
		t.Error("EnaSupport = true, want false when the SDK field is nil")
	}
}

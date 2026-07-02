package inventory

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/awstools/internal/awsclient"
)

// fakeEC2Client embeds the (nil) EC2API interface so it satisfies
// awsclient.EC2API without stubbing every method -- only the methods a
// given test actually exercises are overridden below.
type fakeEC2Client struct {
	awsclient.EC2API
	reservations []types.Reservation
	images       []types.Image
	err          error

	lastDescribeImagesInput *ec2.DescribeImagesInput
}

func (f *fakeEC2Client) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &ec2.DescribeInstancesOutput{Reservations: f.reservations}, nil
}

func (f *fakeEC2Client) DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	f.lastDescribeImagesInput = params
	if f.err != nil {
		return nil, f.err
	}
	return &ec2.DescribeImagesOutput{Images: f.images}, nil
}

func sdkInstance(id, name, state, imageID, project, environment string) types.Instance {
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
	return types.Instance{
		InstanceId: aws.String(id),
		ImageId:    aws.String(imageID),
		State:      &types.InstanceState{Name: types.InstanceStateName(state)},
		Tags:       tags,
	}
}

func sortInstances(instances []Instance) {
	sort.Slice(instances, func(i, j int) bool { return instances[i].InstanceID < instances[j].InstanceID })
}

func TestListInstances_AggregatesAcrossRegions(t *testing.T) {
	clients := map[string]awsclient.EC2API{
		"us-east-1": &fakeEC2Client{reservations: []types.Reservation{
			{Instances: []types.Instance{sdkInstance("i-1", "web", "running", "ami-1", "caltechauthors", "production")}},
		}},
		"us-west-2": &fakeEC2Client{reservations: []types.Reservation{
			{Instances: []types.Instance{sdkInstance("i-2", "db", "stopped", "ami-2", "caltechdata", "development")}},
		}},
	}

	got, err := ListInstances(context.Background(), clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sortInstances(got)

	want := []Instance{
		{InstanceID: "i-1", Name: "web", State: "running", ImageID: "ami-1", Region: "us-east-1", Project: "caltechauthors", Environment: "production"},
		{InstanceID: "i-2", Name: "db", State: "stopped", ImageID: "ami-2", Region: "us-west-2", Project: "caltechdata", Environment: "development"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d instances, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestListInstances_ExcludesTerminated(t *testing.T) {
	clients := map[string]awsclient.EC2API{
		"us-east-1": &fakeEC2Client{reservations: []types.Reservation{
			{Instances: []types.Instance{
				sdkInstance("i-1", "keep", "running", "ami-1", "", ""),
				sdkInstance("i-2", "gone", "terminated", "ami-1", "", ""),
			}},
		}},
	}

	got, err := ListInstances(context.Background(), clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].InstanceID != "i-1" {
		t.Fatalf("got %+v, want only i-1", got)
	}
}

func TestListInstances_EmptyRegion(t *testing.T) {
	clients := map[string]awsclient.EC2API{
		"us-east-1": &fakeEC2Client{reservations: nil},
	}
	got, err := ListInstances(context.Background(), clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d instances, want 0", len(got))
	}
}

func TestListInstances_UntaggedResourceHasEmptyFields(t *testing.T) {
	clients := map[string]awsclient.EC2API{
		"us-east-1": &fakeEC2Client{reservations: []types.Reservation{
			{Instances: []types.Instance{sdkInstance("i-1", "", "running", "ami-1", "", "")}},
		}},
	}
	got, err := ListInstances(context.Background(), clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1", len(got))
	}
	if got[0].Name != "" || got[0].Project != "" || got[0].Environment != "" {
		t.Errorf("got %+v, want empty Name/Project/Environment", got[0])
	}
}

func TestListInstances_IncludesPublicAndPrivateIP(t *testing.T) {
	inst := sdkInstance("i-1", "web", "running", "ami-1", "", "")
	inst.PublicIpAddress = aws.String("203.0.113.25")
	inst.PrivateIpAddress = aws.String("10.0.1.25")
	clients := map[string]awsclient.EC2API{
		"us-east-1": &fakeEC2Client{reservations: []types.Reservation{{Instances: []types.Instance{inst}}}},
	}

	got, err := ListInstances(context.Background(), clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1", len(got))
	}
	if got[0].PublicIP != "203.0.113.25" || got[0].PrivateIP != "10.0.1.25" {
		t.Errorf("got %+v, want PublicIP=203.0.113.25 PrivateIP=10.0.1.25", got[0])
	}
}

func TestListInstances_NoPublicIPIsEmptyNotNil(t *testing.T) {
	clients := map[string]awsclient.EC2API{
		"us-east-1": &fakeEC2Client{reservations: []types.Reservation{
			{Instances: []types.Instance{sdkInstance("i-1", "web", "stopped", "ami-1", "", "")}},
		}},
	}

	got, err := ListInstances(context.Background(), clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].PublicIP != "" || got[0].PrivateIP != "" {
		t.Errorf("got %+v, want empty PublicIP/PrivateIP for a stopped instance with none assigned", got)
	}
}

func TestListInstances_PropagatesError(t *testing.T) {
	clients := map[string]awsclient.EC2API{
		"us-east-1": &fakeEC2Client{err: errors.New("boom")},
	}
	_, err := ListInstances(context.Background(), clients)
	if err == nil {
		t.Fatal("expected an error")
	}
}

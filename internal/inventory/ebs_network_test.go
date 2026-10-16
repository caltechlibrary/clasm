package inventory

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// EBS volumes, EBS snapshots and network interfaces are Tag Management kinds
// (DR-0192): every one belonging to an instance clasm did not launch itself is
// untagged, so they have to be listed to be tagged. Like the other listings they
// are aggregated across regions and follow NextToken; unlike them, what the
// account does not own, or does not manage, is left out.

// page is the slice of items for the page a token names, and the next token.
func page[T any](items []T, size int, token *string) ([]T, *string) {
	if size <= 0 {
		return items, nil
	}
	start := 0
	if token != nil {
		start, _ = strconv.Atoi(*token)
	}
	end := start + size
	if end >= len(items) {
		return items[start:], nil
	}
	return items[start:end], aws.String(strconv.Itoa(end))
}

func (f *fakeEC2Client) DescribeVolumes(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	f.describeCalls++
	if f.err != nil {
		return nil, f.err
	}
	items, next := page(f.volumes, f.pageSize, params.NextToken)
	return &ec2.DescribeVolumesOutput{Volumes: items, NextToken: next}, nil
}

func (f *fakeEC2Client) DescribeSnapshots(ctx context.Context, params *ec2.DescribeSnapshotsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error) {
	f.describeCalls++
	f.lastDescribeSnapshotsInput = params
	if f.err != nil {
		return nil, f.err
	}
	items, next := page(f.snapshots, f.pageSize, params.NextToken)
	return &ec2.DescribeSnapshotsOutput{Snapshots: items, NextToken: next}, nil
}

func (f *fakeEC2Client) DescribeNetworkInterfaces(ctx context.Context, params *ec2.DescribeNetworkInterfacesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error) {
	f.describeCalls++
	if f.err != nil {
		return nil, f.err
	}
	items, next := page(f.networkInterfaces, f.pageSize, params.NextToken)
	return &ec2.DescribeNetworkInterfacesOutput{NetworkInterfaces: items, NextToken: next}, nil
}

func tagList(kv ...string) []types.Tag {
	var tags []types.Tag
	for i := 0; i+1 < len(kv); i += 2 {
		tags = append(tags, types.Tag{Key: aws.String(kv[i]), Value: aws.String(kv[i+1])})
	}
	return tags
}

func sdkVolume(id string, size int32, state types.VolumeState, attachedTo string, tags ...string) types.Volume {
	v := types.Volume{
		VolumeId: aws.String(id), Size: aws.Int32(size), State: state, VolumeType: types.VolumeTypeGp3,
		CreateTime: aws.Time(time.Date(2026, 5, 21, 17, 39, 24, 0, time.UTC)), Tags: tagList(tags...),
	}
	if attachedTo != "" {
		v.Attachments = []types.VolumeAttachment{{InstanceId: aws.String(attachedTo)}}
	}
	return v
}

func TestListVolumes_AggregatesRegionsAndCarriesWhatTaggingNeeds(t *testing.T) {
	clients := map[string]awsclient.EC2API{
		"us-west-1": &fakeEC2Client{volumes: []types.Volume{sdkVolume("vol-1", 8, types.VolumeStateAvailable, "")}},
		"us-west-2": &fakeEC2Client{volumes: []types.Volume{sdkVolume("vol-2", 250, types.VolumeStateInUse, "i-9", "Name", "root", "origin", "dld")}},
	}
	got, err := ListVolumes(context.Background(), clients)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].VolumeID < got[j].VolumeID })
	want := []Volume{
		{VolumeID: "vol-1", Region: "us-west-1", SizeGiB: 8, State: "available", VolumeType: "gp3", Created: "2026-05-21", Tags: map[string]string{}},
		{VolumeID: "vol-2", Region: "us-west-2", SizeGiB: 250, State: "in-use", VolumeType: "gp3", AttachedTo: "i-9", Created: "2026-05-21", Tags: map[string]string{"Name": "root", "origin": "dld"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestListVolumes_FollowsEveryPage(t *testing.T) {
	var vols []types.Volume
	for i := 0; i < 5; i++ {
		vols = append(vols, sdkVolume("vol-"+strconv.Itoa(i), 8, types.VolumeStateInUse, ""))
	}
	fake := &fakeEC2Client{volumes: vols, pageSize: 2}
	got, err := ListVolumes(context.Background(), map[string]awsclient.EC2API{"us-west-2": fake})
	if err != nil || len(got) != 5 || fake.describeCalls != 3 {
		t.Errorf("got %d volumes in %d calls, err %v; want 5 in 3", len(got), fake.describeCalls, err)
	}
}

func TestListSnapshots_AsksOnlyForTheAccountsOwnAndFollowsEveryPage(t *testing.T) {
	var snaps []types.Snapshot
	for i := 0; i < 3; i++ {
		snaps = append(snaps, types.Snapshot{
			SnapshotId: aws.String("snap-" + strconv.Itoa(i)), VolumeSize: aws.Int32(100), State: types.SnapshotStateCompleted,
			Description: aws.String("Created by CreateImage(i-1) for ami-1"), StartTime: aws.Time(time.Date(2026, 8, 28, 21, 5, 59, 0, time.UTC)),
			Tags: tagList("origin", "dld"),
		})
	}
	fake := &fakeEC2Client{snapshots: snaps, pageSize: 2}
	got, err := ListSnapshots(context.Background(), map[string]awsclient.EC2API{"us-west-2": fake})
	if err != nil || len(got) != 3 || fake.describeCalls != 2 {
		t.Fatalf("got %d snapshots in %d calls, err %v; want 3 in 2", len(got), fake.describeCalls, err)
	}
	if !reflect.DeepEqual(fake.lastDescribeSnapshotsInput.OwnerIds, []string{"self"}) {
		t.Errorf("snapshots owned by others must not be listed; OwnerIds = %v", fake.lastDescribeSnapshotsInput.OwnerIds)
	}
	want := Snapshot{SnapshotID: "snap-0", Region: "us-west-2", VolumeSizeGiB: 100, State: "completed",
		Description: "Created by CreateImage(i-1) for ami-1", Started: "2026-08-28", Tags: map[string]string{"origin": "dld"}}
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("got  %+v\nwant %+v", got[0], want)
	}
}

func TestListNetworkInterfaces_LeavesOutWhatAnAWSServiceManagesAndFollowsEveryPage(t *testing.T) {
	enis := []types.NetworkInterface{
		{NetworkInterfaceId: aws.String("eni-1"), Status: types.NetworkInterfaceStatusInUse, Description: aws.String("Primary network interface"),
			PrivateIpAddress: aws.String("10.0.0.5"), Attachment: &types.NetworkInterfaceAttachment{InstanceId: aws.String("i-9")}, TagSet: tagList("Name", "web")},
		{NetworkInterfaceId: aws.String("eni-elb"), RequesterManaged: aws.Bool(true), Description: aws.String("ELB app/x")},
		{NetworkInterfaceId: aws.String("eni-2"), Status: types.NetworkInterfaceStatusAvailable},
	}
	fake := &fakeEC2Client{networkInterfaces: enis, pageSize: 2}
	got, err := ListNetworkInterfaces(context.Background(), map[string]awsclient.EC2API{"us-west-2": fake})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "eni-1" || got[1].ID != "eni-2" {
		t.Fatalf("the requester-managed interface must be left out: %+v", got)
	}
	want := NetworkInterface{ID: "eni-1", Region: "us-west-2", Status: "in-use", Description: "Primary network interface",
		PrivateIP: "10.0.0.5", AttachedTo: "i-9", Tags: map[string]string{"Name": "web"}}
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("got  %+v\nwant %+v", got[0], want)
	}
	if fake.describeCalls != 2 {
		t.Errorf("every page must be read: %d calls", fake.describeCalls)
	}
}

func TestEBSAndInterfaceListings_NameTheRegionThatFailed(t *testing.T) {
	clients := map[string]awsclient.EC2API{"us-west-2": &fakeEC2Client{err: errors.New("UnauthorizedOperation")}}
	for name, call := range map[string]func() error{
		"volumes":    func() error { _, err := ListVolumes(context.Background(), clients); return err },
		"snapshots":  func() error { _, err := ListSnapshots(context.Background(), clients); return err },
		"interfaces": func() error { _, err := ListNetworkInterfaces(context.Background(), clients); return err },
	} {
		if err := call(); err == nil || err.Error() != "us-west-2: UnauthorizedOperation" {
			t.Errorf("%s: want the region named, got %v", name, err)
		}
	}
}

func TestEBSAndInterfaceListings_AnEmptyRegionIsNotAnError(t *testing.T) {
	clients := map[string]awsclient.EC2API{"us-west-1": &fakeEC2Client{}}
	if v, err := ListVolumes(context.Background(), clients); err != nil || len(v) != 0 {
		t.Errorf("volumes: %v %v", v, err)
	}
	if s, err := ListSnapshots(context.Background(), clients); err != nil || len(s) != 0 {
		t.Errorf("snapshots: %v %v", s, err)
	}
	if n, err := ListNetworkInterfaces(context.Background(), clients); err != nil || len(n) != 0 {
		t.Errorf("interfaces: %v %v", n, err)
	}
}

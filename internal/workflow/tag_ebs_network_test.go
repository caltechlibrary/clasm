package workflow

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// EBS volumes, EBS snapshots and network interfaces are Tag Management kinds
// (DR-0192), so the menu, "Show all tags" and its command-line form cover eleven
// kinds, and the three new ones can be tagged like the rest.

func ebsFixtureClients() map[string]awsclient.EC2API {
	return map[string]awsclient.EC2API{
		"us-west-2": &fakeEC2Client{
			describeVolumesOutput: []types.Volume{{
				VolumeId: aws.String("vol-1"), Size: aws.Int32(250), State: types.VolumeStateInUse, VolumeType: types.VolumeTypeGp3,
				Attachments: []types.VolumeAttachment{{InstanceId: aws.String("i-9")}},
				Tags:        []types.Tag{{Key: aws.String("origin"), Value: aws.String("dld")}},
			}},
			describeSnapshotsOutput: []types.Snapshot{{
				SnapshotId: aws.String("snap-1"), VolumeSize: aws.Int32(100), State: types.SnapshotStateCompleted,
				Description: aws.String("Created by CreateImage for ami-1"),
			}},
			describeNetworkInterfacesOutput: []types.NetworkInterface{{
				NetworkInterfaceId: aws.String("eni-1"), Status: types.NetworkInterfaceStatusInUse, PrivateIpAddress: aws.String("10.0.0.5"),
				TagSet: []types.Tag{{Key: aws.String("Name"), Value: aws.String("web")}},
			}},
		},
	}
}

func TestTagManagementKinds_TheThreeNewOnesAreAppendedSoNumbersDoNotMove(t *testing.T) {
	want := []string{"Instance", "AMI", "Launch Template", "Key Pair", "S3 Bucket", "IAM Role", "IAM Instance Profile", "IAM Policy",
		"EBS Volume", "EBS Snapshot", "Network Interface"}
	if !reflect.DeepEqual(tagManagementKinds, want) {
		t.Errorf("kinds = %v", tagManagementKinds)
	}
	slugs := TagKindSlugs()
	if got := slugs[len(slugs)-3:]; !reflect.DeepEqual(got, []string{"ebs-volume", "ebs-snapshot", "network-interface"}) {
		t.Errorf("the new slugs must follow the old ones in picker order: %v", slugs)
	}
}

func TestTaggedResourcesForKind_ListsVolumesSnapshotsAndInterfacesWithTheirTags(t *testing.T) {
	src := TagSources{EC2Clients: ebsFixtureClients()}
	for kind, tc := range map[string]struct {
		title, id string
		tags      map[string]string
	}{
		"EBS Volume":        {"EBS Volumes -- All Tags", "vol-1", map[string]string{"origin": "dld"}},
		"EBS Snapshot":      {"EBS Snapshots -- All Tags", "snap-1", map[string]string{}},
		"Network Interface": {"Network Interfaces -- All Tags", "eni-1", map[string]string{"Name": "web"}},
	} {
		title, rows, err := taggedResourcesForKind(context.Background(), kind, src)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if title != tc.title || len(rows) != 1 || rows[0].ID != tc.id || !strings.Contains(rows[0].Label, tc.id) || !reflect.DeepEqual(rows[0].Tags, tc.tags) {
			t.Errorf("%s: title %q rows %+v", kind, title, rows)
		}
	}
}

func TestTaggedResourcesForKind_AFailedRegionIsNamed(t *testing.T) {
	src := TagSources{EC2Clients: map[string]awsclient.EC2API{"us-west-2": &fakeEC2Client{
		describeVolumesErr: errors.New("UnauthorizedOperation"), describeSnapshotsErr: errors.New("UnauthorizedOperation"), describeNetworkInterfacesErr: errors.New("UnauthorizedOperation"),
	}}}
	for _, kind := range []string{"EBS Volume", "EBS Snapshot", "Network Interface"} {
		if _, _, err := taggedResourcesForKind(context.Background(), kind, src); err == nil || !strings.Contains(err.Error(), "us-west-2") {
			t.Errorf("%s: want an error naming the region, got %v", kind, err)
		}
	}
}

func TestVolumeSnapshotAndInterfaceLabels_NameWhatYouAreAboutToTag(t *testing.T) {
	src := TagSources{EC2Clients: ebsFixtureClients()}
	_, vols, _ := taggedResourcesForKind(context.Background(), "EBS Volume", src)
	_, snaps, _ := taggedResourcesForKind(context.Background(), "EBS Snapshot", src)
	_, enis, _ := taggedResourcesForKind(context.Background(), "Network Interface", src)
	for what, want := range map[string][]string{
		vols[0].Label:  {"vol-1", "250 GiB", "in-use", "i-9", "us-west-2"},
		snaps[0].Label: {"snap-1", "100 GiB", "CreateImage", "us-west-2"},
		enis[0].Label:  {"eni-1", "10.0.0.5", "us-west-2"},
	} {
		for _, w := range want {
			if !strings.Contains(what, w) {
				t.Errorf("label %q should mention %q", what, w)
			}
		}
	}
}

func TestRunShowAllTagsCLI_TheNewKindSlugsWork(t *testing.T) {
	src := TagSources{EC2Clients: ebsFixtureClients()}
	for slug, want := range map[string]string{"ebs-volume": "vol-1", "ebs-snapshot": "snap-1", "network-interface": "eni-1"} {
		var out bytes.Buffer
		if err := RunShowAllTagsCLI(context.Background(), &out, src, slug, ui.FormatJSON); err != nil {
			t.Fatalf("%s: %v", slug, err)
		}
		if !strings.Contains(out.String(), want) {
			t.Errorf("%s: want %q in:\n%s", slug, want, out.String())
		}
	}
}

func TestFetchTagsForTheNewKinds(t *testing.T) {
	client := ebsFixtureClients()["us-west-2"]
	if got, err := fetchVolumeTags(context.Background(), client, "vol-1"); err != nil || !reflect.DeepEqual(got, map[string]string{"origin": "dld"}) {
		t.Errorf("volume: %v %v", got, err)
	}
	if got, err := fetchSnapshotTags(context.Background(), client, "snap-1"); err != nil || len(got) != 0 {
		t.Errorf("snapshot: %v %v", got, err)
	}
	if got, err := fetchNetworkInterfaceTags(context.Background(), client, "eni-1"); err != nil || !reflect.DeepEqual(got, map[string]string{"Name": "web"}) {
		t.Errorf("interface: %v %v", got, err)
	}
	empty := &fakeEC2Client{}
	if _, err := fetchVolumeTags(context.Background(), empty, "vol-x"); err == nil || !strings.Contains(err.Error(), "vol-x") {
		t.Errorf("a missing volume is an error naming it: %v", err)
	}
	if _, err := fetchSnapshotTags(context.Background(), empty, "snap-x"); err == nil || !strings.Contains(err.Error(), "snap-x") {
		t.Errorf("a missing snapshot is an error naming it: %v", err)
	}
	if _, err := fetchNetworkInterfaceTags(context.Background(), empty, "eni-x"); err == nil || !strings.Contains(err.Error(), "eni-x") {
		t.Errorf("a missing interface is an error naming it: %v", err)
	}
}

// These three are addressed by their resource IDs, so the same CreateTags and
// DeleteTags every EC2 kind already uses applies to them.
func TestApplyTagChange_WorksOnAVolumeSnapshotAndInterfaceID(t *testing.T) {
	for _, id := range []string{"vol-1", "snap-1", "eni-1"} {
		fake := &fakeEC2Client{}
		if err := ApplyTagChange(context.Background(), fake, TagChangeParams{ResourceID: id, Action: "add", Key: "origin", Value: "dld"}); err != nil {
			t.Fatal(err)
		}
		if fake.lastCreateTagsInput == nil || fake.lastCreateTagsInput.Resources[0] != id {
			t.Errorf("%s: CreateTags input = %+v", id, fake.lastCreateTagsInput)
		}
	}
}

// The read-backs find the one resource asked for among what AWS returns, not just the
// first of it.
func TestFetchTagsForTheNewKinds_PickTheRequestedResourceAmongSeveral(t *testing.T) {
	client := &fakeEC2Client{
		describeVolumesOutput: []types.Volume{
			{VolumeId: aws.String("vol-a"), Tags: []types.Tag{{Key: aws.String("which"), Value: aws.String("a")}}},
			{VolumeId: aws.String("vol-b"), Tags: []types.Tag{{Key: aws.String("which"), Value: aws.String("b")}}},
		},
		describeSnapshotsOutput: []types.Snapshot{
			{SnapshotId: aws.String("snap-a"), Tags: []types.Tag{{Key: aws.String("which"), Value: aws.String("a")}}},
			{SnapshotId: aws.String("snap-b"), Tags: []types.Tag{{Key: aws.String("which"), Value: aws.String("b")}}},
		},
		describeNetworkInterfacesOutput: []types.NetworkInterface{
			{NetworkInterfaceId: aws.String("eni-a"), TagSet: []types.Tag{{Key: aws.String("which"), Value: aws.String("a")}}},
			{NetworkInterfaceId: aws.String("eni-b"), TagSet: []types.Tag{{Key: aws.String("which"), Value: aws.String("b")}}},
		},
	}
	if got, err := fetchVolumeTags(context.Background(), client, "vol-b"); err != nil || got["which"] != "b" {
		t.Errorf("volume: %v %v", got, err)
	}
	if got, err := fetchSnapshotTags(context.Background(), client, "snap-b"); err != nil || got["which"] != "b" {
		t.Errorf("snapshot: %v %v", got, err)
	}
	if got, err := fetchNetworkInterfaceTags(context.Background(), client, "eni-b"); err != nil || got["which"] != "b" {
		t.Errorf("interface: %v %v", got, err)
	}
	if _, err := fetchVolumeTags(context.Background(), client, "vol-z"); err == nil {
		t.Errorf("an ID AWS did not return must be an error, not the first resource's tags")
	}
}

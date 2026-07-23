package workflow

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// manageResourceTags mixes a pipe-testable Menu-tier kind picker
// (pickString) with a not-pipe-testable Picker-tier resource picker
// (pickInstance/pickImage/pickLaunchTemplate/pickKeyPair) once a kind
// with resources is chosen -- the same accepted limitation manageTags
// itself has (see manage_tags_test.go's own comment). These tests only
// exercise the paths reachable before any Picker-tier call: an empty
// resource list for the chosen kind returns immediately.

func TestManageResourceTags_NoInstancesFound(t *testing.T) {
	term, menuInput, buf := newPipeEditor("1\n") // kind = Instance
	err := manageResourceTags(context.Background(), term, nil, nil, nil, config.OriginTagConfig{}, nil, nil, nil, nil, nil, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No instances found") {
		t.Errorf("expected a no-instances message, got:\n%s", buf.String())
	}
}

func TestManageResourceTags_NoAMIsFound(t *testing.T) {
	term, menuInput, buf := newPipeEditor("2\n") // kind = AMI
	err := manageResourceTags(context.Background(), term, nil, nil, nil, config.OriginTagConfig{}, nil, nil, nil, nil, nil, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No AMIs found") {
		t.Errorf("expected a no-AMIs message, got:\n%s", buf.String())
	}
}

func TestManageResourceTags_NoLaunchTemplatesFound(t *testing.T) {
	term, menuInput, buf := newPipeEditor("3\n") // kind = Launch Template
	err := manageResourceTags(context.Background(), term, nil, nil, nil, config.OriginTagConfig{}, nil, nil, nil, nil, nil, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No launch templates found") {
		t.Errorf("expected a no-launch-templates message, got:\n%s", buf.String())
	}
}

func TestManageResourceTags_NoKeyPairsFound(t *testing.T) {
	term, menuInput, buf := newPipeEditor("4\n") // kind = Key Pair
	err := manageResourceTags(context.Background(), term, nil, nil, nil, config.OriginTagConfig{}, nil, nil, nil, nil, nil, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No key pairs found") {
		t.Errorf("expected a no-key-pairs message, got:\n%s", buf.String())
	}
}

func TestManageResourceTags_NoBucketsFound(t *testing.T) {
	term, menuInput, buf := newPipeEditor("5\n") // kind = S3 Bucket
	err := manageResourceTags(context.Background(), term, nil, nil, nil, config.OriginTagConfig{}, nil, nil, nil, nil, nil, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No buckets found") {
		t.Errorf("expected a no-buckets message, got:\n%s", buf.String())
	}
}

func TestManageResourceTags_NoIAMRolesFound(t *testing.T) {
	term, menuInput, buf := newPipeEditor("6\n") // kind = IAM Role
	err := manageResourceTags(context.Background(), term, nil, nil, &fakeIAMClient{}, config.OriginTagConfig{Key: "Origin"}, nil, nil, nil, nil, nil, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No IAM roles found") {
		t.Errorf("expected a no-IAM-roles message, got:\n%s", buf.String())
	}
}

func TestManageResourceTags_NoIAMInstanceProfilesFound(t *testing.T) {
	term, menuInput, buf := newPipeEditor("7\n") // kind = IAM Instance Profile
	err := manageResourceTags(context.Background(), term, nil, nil, &fakeIAMClient{}, config.OriginTagConfig{Key: "Origin"}, nil, nil, nil, nil, nil, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No IAM instance profiles found") {
		t.Errorf("expected a no-IAM-instance-profiles message, got:\n%s", buf.String())
	}
}

func TestManageResourceTags_NoIAMPoliciesFound(t *testing.T) {
	term, menuInput, buf := newPipeEditor("8\n") // kind = IAM Policy
	err := manageResourceTags(context.Background(), term, nil, nil, &fakeIAMClient{}, config.OriginTagConfig{Key: "Origin"}, nil, nil, nil, nil, nil, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No IAM policies found") {
		t.Errorf("expected a no-IAM-policies message, got:\n%s", buf.String())
	}
}

// instanceTaggedResources/imageTaggedResources/launchTemplateTaggedResources/
// keyPairTaggedResources are pure data transforms feeding "Show all
// tags" -- unit-testable without driving tui.RunListView's interactive
// loop (showAllTags itself, below, isn't fully drivable end-to-end for
// the same reason DisplayInstances/DisplayImages/etc aren't).

func TestInstanceTaggedResources(t *testing.T) {
	instances := []inventory.Instance{
		{InstanceID: "i-1", Name: "web", Region: "us-east-1", Tags: map[string]string{"Name": "web", "Owner": "dld"}},
	}
	got := instanceTaggedResources(instances)
	want := []ui.TaggedResource{
		{ID: "i-1", Label: instanceLabel(instances[0]), Tags: map[string]string{"Name": "web", "Owner": "dld"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestImageTaggedResources(t *testing.T) {
	images := []inventory.Image{
		{ImageID: "ami-1", Name: "base", Region: "us-east-1", Tags: map[string]string{"Project": "caltechauthors"}},
	}
	got := imageTaggedResources(images)
	want := []ui.TaggedResource{
		{ID: "ami-1", Label: imageLabel(images[0]), Tags: map[string]string{"Project": "caltechauthors"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestLaunchTemplateTaggedResources(t *testing.T) {
	templates := []inventory.LaunchTemplate{
		{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1", Tags: map[string]string{"Team": "dld"}},
	}
	got := launchTemplateTaggedResources(templates)
	want := []ui.TaggedResource{
		{ID: "lt-1", Label: launchTemplateLabel(templates[0]), Tags: map[string]string{"Team": "dld"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestKeyPairTaggedResources(t *testing.T) {
	keyPairs := []inventory.KeyPair{
		{KeyPairID: "key-1", KeyName: "my-laptop-key", Region: "us-east-1", Tags: map[string]string{"Owner": "rsdoiel"}},
	}
	got := keyPairTaggedResources(keyPairs)
	want := []ui.TaggedResource{
		{ID: "key-1", Label: keyPairLabel(keyPairs[0]), Tags: map[string]string{"Owner": "rsdoiel"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// iamRoleTaggedResources/iamInstanceProfileTaggedResources/
// iamPolicyTaggedResources are also pure data transforms -- no extra
// API call needed, unlike bucketTaggedResources below, since each
// summary's Tags field is already populated by inventory.ListIAM*Summaries.
func TestIAMRoleTaggedResources(t *testing.T) {
	roles := []inventory.IAMRoleSummary{
		{Name: "air-sampling", Origin: "DLD", Tags: map[string]string{"origin": "dld"}},
	}
	got := iamRoleTaggedResources(roles)
	want := []ui.TaggedResource{
		{ID: "air-sampling", Label: iamRoleLabel(roles[0]), Tags: map[string]string{"origin": "dld"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestIAMInstanceProfileTaggedResources(t *testing.T) {
	profiles := []inventory.IAMInstanceProfileSummary{
		{Name: "air-sampling-profile", Origin: inventory.OriginUnset, Tags: map[string]string{}},
	}
	got := iamInstanceProfileTaggedResources(profiles)
	want := []ui.TaggedResource{
		{ID: "air-sampling-profile", Label: iamInstanceProfileLabel(profiles[0]), Tags: map[string]string{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestIAMPolicyTaggedResources(t *testing.T) {
	policies := []inventory.IAMPolicySummary{
		{Name: "s3-backup-access", ARN: "arn:aws:iam::123456789012:policy/s3-backup-access", Origin: "DLD", Tags: map[string]string{"origin": "dld"}},
	}
	got := iamPolicyTaggedResources(policies)
	want := []ui.TaggedResource{
		{ID: "arn:aws:iam::123456789012:policy/s3-backup-access", Label: iamPolicyLabel(policies[0]), Tags: map[string]string{"origin": "dld"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// bucketTaggedResources is not a pure data transform like its four
// EC2-backed siblings above -- it makes a real (fake, here)
// s3:GetBucketTagging call per bucket, since inventory.Bucket doesn't
// carry a full tag map the way Instance/Image/LaunchTemplate/KeyPair
// now do (DECISIONS.md, "Tag Management: a fourth domain...", "Show
// all tags" design). Still unit-testable via a fake newS3Client.
func TestBucketTaggedResources(t *testing.T) {
	fake := &fakeS3Client{tagSet: []types.Tag{{Key: aws.String("Purpose"), Value: aws.String("backup")}}}
	newS3Client := newRegionS3Client(map[string]awsclient.S3API{"us-west-2": fake})
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}}

	got, err := bucketTaggedResources(context.Background(), newS3Client, buckets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []ui.TaggedResource{
		{ID: "my-bucket", Label: bucketLabel(buckets[0]), Tags: map[string]string{"Purpose": "backup"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestBucketTaggedResources_PropagatesClientError(t *testing.T) {
	newS3Client := newRegionS3Client(map[string]awsclient.S3API{})
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-east-1"}}

	_, err := bucketTaggedResources(context.Background(), newS3Client, buckets)
	if err == nil {
		t.Fatal("expected an error")
	}
}

// showAllTags's kind picker itself is pipe-testable, but every branch
// dispatches into ui.DisplayAllTags (tui.RunListView, a real bubbletea
// Program) that can't be pipe-tested -- and, per the huh-EOF gotcha
// documented in manage_tags.go (isCancellation/manageTagsForResource's
// doc comment), an exhausted accessible-mode input on a
// PointerAccessor-backed huh.Select doesn't surface as an error either;
// it silently defaults to option 1 and would drive that real Program.
// So unlike manageResourceTags (which has "no resources of this kind"
// early-return branches reachable before any Picker/List-tier call),
// showAllTags has no branch reachable via pipe input alone that stops
// short of the interactive loop -- only its row-building conversion
// functions above and tagsListViewConfig (internal/ui) are unit-tested
// directly.

package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// tag-resource (DR-0192): add a tag to one resource of any of the eleven kinds, from a script.
// It adds what is missing, treats the same value as done, and will not silently change an
// existing different value: that needs --replace. A refusal changes nothing at all.

func parseTR(t *testing.T, args ...string) TagResourceOptions {
	t.Helper()
	o, err := ParseTagResourceArgs(args)
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return o
}

func TestParseTagResourceArgs_OptionsMayComeBeforeOrAfterTheKindAndName(t *testing.T) {
	for _, args := range [][]string{
		{"--tag", "origin=dld", "instance", "new-data"},
		{"instance", "new-data", "--tag", "origin=dld"},
		{"instance", "--tag=origin=dld", "new-data"},
	} {
		o := parseTR(t, args...)
		if o.KindSlug != "instance" || o.Kind != "Instance" || o.Name != "new-data" || len(o.Tags) != 1 || o.Tags[0] != (TagKV{"origin", "dld"}) || o.Format != ui.FormatText || o.Replace {
			t.Errorf("%v: got %+v", args, o)
		}
	}
	o := parseTR(t, "-json", "--replace", "ebs-volume", "vol-1", "--tag", "origin=dld", "--tag", "Name=root", "--tag", "note=")
	if o.Format != ui.FormatJSON || !o.Replace || len(o.Tags) != 3 || o.Tags[1] != (TagKV{"Name", "root"}) || o.Tags[2] != (TagKV{"note", ""}) {
		t.Errorf("got %+v", o)
	}
	if o := parseTR(t, "--tag", "a=b=c", "s3-bucket", "b"); o.Tags[0] != (TagKV{"a", "b=c"}) {
		t.Errorf("only the first = splits key from value: %+v", o.Tags)
	}
}

func TestParseTagResourceArgs_UsageErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"no arguments":         {},
		"no tag":               {"instance", "new-data"},
		"a tag with no =":      {"--tag", "origin", "instance", "n"},
		"an empty key":         {"--tag", "=dld", "instance", "n"},
		"a reserved aws: key":  {"--tag", "aws:cloudformation:stack-name=x", "instance", "n"},
		"the same key twice":   {"--tag", "origin=dld", "--tag", "origin=imss", "instance", "n"},
		"only a kind":          {"--tag", "origin=dld", "instance"},
		"too many words":       {"--tag", "origin=dld", "instance", "a", "b"},
		"an unknown kind":      {"--tag", "origin=dld", "no-such-kind", "n"},
		"both formats":         {"-text", "-json", "--tag", "origin=dld", "instance", "n"},
		"a blank name":         {"--tag", "origin=dld", "instance", "  "},
		"a bad Environment":    {"--tag", "Environment=prod", "instance", "n"},
		"an unknown option":    {"--nope", "--tag", "origin=dld", "instance", "n"},
		"jsonl is not offered": {"-jsonl", "--tag", "origin=dld", "instance", "n"},
	} {
		_, err := ParseTagResourceArgs(args)
		var ue *UsageError
		if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "usage: clasm tag-management tag-resource") {
			t.Errorf("%s: want a usage error carrying the usage, got %v", name, err)
		}
	}
	_, err := ParseTagResourceArgs([]string{"--tag", "origin=dld", "no-such-kind", "n"})
	if err == nil || !strings.Contains(err.Error(), "ebs-volume") || !strings.Contains(err.Error(), "no-such-kind") {
		t.Errorf("an unknown kind lists the kinds: %v", err)
	}
	_, err = ParseTagResourceArgs([]string{"--help"})
	var h *HelpRequested
	if !errors.As(err, &h) || !strings.Contains(h.Usage, "--replace") || !strings.Contains(h.Usage, "refus") {
		t.Errorf("want help explaining --replace, got %v", err)
	}
	if o, err := ParseTagResourceArgs([]string{"--tag", "Environment=test", "instance", "n"}); err != nil || o.Tags[0].Value != "test" {
		t.Errorf("a valid Environment passes: %v %v", o, err)
	}
}

// --- the fixture: one resource of each kind, and fakes that record what is sent ---

type trFixture struct {
	src  TagSources
	ec2  *fakeEC2Client // us-west-2
	ec2b *fakeEC2Client // us-west-1
	iam  *fakeIAMClient
	s3   *fakeS3Client
}

func newTRFixture() trFixture {
	f := trFixture{
		ec2:  &fakeEC2Client{},
		ec2b: &fakeEC2Client{},
		s3:   &fakeS3Client{tagSet: []s3types.Tag{{Key: aws.String("Purpose"), Value: aws.String("website")}}},
		iam: &fakeIAMClient{
			roleTags:   map[string][]iamtypes.Tag{"rdm-backups": {{Key: aws.String("team"), Value: aws.String("dld")}}},
			policies:   []iamtypes.Policy{{PolicyName: aws.String("rdm-backups-policy"), Arn: aws.String("arn:aws:iam::1:policy/rdm-backups-policy")}},
			policyTags: map[string][]iamtypes.Tag{},
		},
	}
	f.ec2b.describeVolumesOutput = []types.Volume{{VolumeId: aws.String("vol-west1"), Tags: []types.Tag{{Key: aws.String("Name"), Value: aws.String("old")}}}}
	f.ec2.describeVolumesOutput = []types.Volume{{VolumeId: aws.String("vol-west2")}}
	f.ec2.launchTemplates = []types.LaunchTemplate{{LaunchTemplateId: aws.String("lt-1")}}
	f.ec2.keyPairs = []types.KeyPairInfo{{KeyPairId: aws.String("key-1")}}
	f.ec2.describeSnapshotsOutput = []types.Snapshot{{SnapshotId: aws.String("snap-1")}}
	f.ec2.describeNetworkInterfacesOutput = []types.NetworkInterface{{NetworkInterfaceId: aws.String("eni-1")}}
	f.src = TagSources{
		EC2Clients:  map[string]awsclient.EC2API{"us-west-2": f.ec2, "us-west-1": f.ec2b},
		NewS3Client: func(ctx context.Context, region string) (awsclient.S3API, error) { return f.s3, nil },
		IAMClient:   f.iam,
		OriginTag:   untaggedOrigin,
		Instances: []inventory.Instance{
			{InstanceID: "i-new", Name: "new-data", Region: "us-west-2", Tags: map[string]string{"Name": "new-data"}},
			{InstanceID: "i-dup1", Name: "twin", Region: "us-west-2"}, {InstanceID: "i-dup2", Name: "twin", Region: "us-west-2"},
		},
		Images:          []inventory.Image{{ImageID: "ami-1", Name: "old-ami", Region: "us-west-2"}},
		LaunchTemplates: []inventory.LaunchTemplate{{TemplateID: "lt-1", Name: "tmpl", Region: "us-west-2"}},
		KeyPairs:        []inventory.KeyPair{{KeyName: "test-clasm", KeyPairID: "key-1", Region: "us-west-2"}},
		Buckets:         []inventory.Bucket{{Name: "plain-bucket", Region: "us-west-2"}},
	}
	return f
}

func (f trFixture) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := RunTagResourceCLI(context.Background(), &out, f.src, parseTR(t, args...))
	return out.String(), err
}

func createdTags(f *fakeEC2Client) map[string]string {
	m := map[string]string{}
	for _, in := range f.createTagsInputs {
		for _, tg := range in.Tags {
			m[aws.ToString(tg.Key)] = aws.ToString(tg.Value)
		}
	}
	return m
}

func TestRunTagResourceCLI_AddsAMissingTagToEachEC2Kind(t *testing.T) {
	for kind, tc := range map[string]struct{ name, id string }{
		"instance": {"new-data", "i-new"}, "ami": {"old-ami", "ami-1"}, "launch-template": {"tmpl", "lt-1"}, "key-pair": {"test-clasm", "key-1"},
		"ebs-volume": {"vol-west2", "vol-west2"}, "ebs-snapshot": {"snap-1", "snap-1"}, "network-interface": {"eni-1", "eni-1"},
	} {
		f := newTRFixture()
		out, err := f.run(t, "--tag", "origin=dld", kind, tc.name)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if len(f.ec2.createTagsInputs) != 1 || f.ec2.createTagsInputs[0].Resources[0] != tc.id || createdTags(f.ec2)["origin"] != "dld" {
			t.Errorf("%s: CreateTags inputs = %+v", kind, f.ec2.createTagsInputs)
		}
		if !strings.Contains(out, "added origin=dld") {
			t.Errorf("%s: output = %q", kind, out)
		}
	}
}

func TestRunTagResourceCLI_AVolumeIsFoundInWhicheverRegionHasIt(t *testing.T) {
	f := newTRFixture()
	if _, err := f.run(t, "--tag", "origin=dld", "ebs-volume", "vol-west1"); err != nil {
		t.Fatal(err)
	}
	if len(f.ec2b.createTagsInputs) != 1 || len(f.ec2.createTagsInputs) != 0 {
		t.Errorf("the tag must be sent to the region that has the volume: west1=%d west2=%d", len(f.ec2b.createTagsInputs), len(f.ec2.createTagsInputs))
	}
}

func TestRunTagResourceCLI_ABucketKeepsItsExistingTags(t *testing.T) {
	f := newTRFixture()
	if _, err := f.run(t, "--tag", "origin=dld", "s3-bucket", "plain-bucket"); err != nil {
		t.Fatal(err)
	}
	if len(f.s3.putBucketTaggingCalls) != 1 {
		t.Fatalf("want one PutBucketTagging, got %d", len(f.s3.putBucketTaggingCalls))
	}
	got := map[string]string{}
	for _, tg := range f.s3.putBucketTaggingCalls[0].Tagging.TagSet {
		got[aws.ToString(tg.Key)] = aws.ToString(tg.Value)
	}
	if got["Purpose"] != "website" || got["origin"] != "dld" || len(got) != 2 {
		t.Errorf("PutBucketTagging replaces the set, so the existing tags must be written back: %v", got)
	}
}

func TestRunTagResourceCLI_IAMKindsAreTaggedByNameAndAPolicyByNameOrARN(t *testing.T) {
	f := newTRFixture()
	if _, err := f.run(t, "--tag", "origin=dld", "iam-role", "rdm-backups"); err != nil {
		t.Fatal(err)
	}
	if f.iam.lastTagRoleInput == nil || aws.ToString(f.iam.lastTagRoleInput.RoleName) != "rdm-backups" || aws.ToString(f.iam.lastTagRoleInput.Tags[0].Key) != "origin" {
		t.Errorf("TagRole input = %+v", f.iam.lastTagRoleInput)
	}
	if _, err := f.run(t, "--tag", "origin=dld", "iam-instance-profile", "rdm-backups"); err != nil || aws.ToString(f.iam.lastTagInstanceProfileInput.InstanceProfileName) != "rdm-backups" {
		t.Errorf("profile: %v %+v", err, f.iam.lastTagInstanceProfileInput)
	}
	for _, arg := range []string{"rdm-backups-policy", "arn:aws:iam::1:policy/rdm-backups-policy"} {
		f = newTRFixture()
		if _, err := f.run(t, "--tag", "origin=dld", "iam-policy", arg); err != nil || aws.ToString(f.iam.lastTagPolicyInput.PolicyArn) != "arn:aws:iam::1:policy/rdm-backups-policy" {
			t.Errorf("policy %q: %v %+v", arg, err, f.iam.lastTagPolicyInput)
		}
	}
}

// Tagging is always allowed on IAM, DLD-owned or not: it is how ownership is recorded.
func TestRunTagResourceCLI_ARoleThatIsNotDLDOwnedCanStillBeTagged(t *testing.T) {
	f := newTRFixture()
	if _, err := f.run(t, "--tag", "origin=dld", "iam-role", "rdm-backups"); err != nil || f.iam.lastTagRoleInput == nil {
		t.Fatalf("a role with no origin tag must be taggable: %v", err)
	}
}

func TestRunTagResourceCLI_TheSameValueIsDoneAndSendsNothing(t *testing.T) {
	f := newTRFixture()
	f.ec2.instanceTags = ec2TagList(map[string]string{"origin": "dld"})
	out, err := f.run(t, "--tag", "origin=dld", "instance", "new-data")
	if err != nil || CLIExitCode(err) != 0 || len(f.ec2.createTagsInputs) != 0 {
		t.Fatalf("a repeat is a success that sends nothing: %v %v", err, f.ec2.createTagsInputs)
	}
	if !strings.Contains(out, "already") || !strings.Contains(out, "origin=dld") {
		t.Errorf("output = %q", out)
	}
}

// The tags come from AWS at the time, not from the listing the command started with: a
// tag added a moment ago by someone else is seen.
func TestRunTagResourceCLI_ComparesAgainstTheLiveTagsNotTheListing(t *testing.T) {
	f := newTRFixture()
	f.src.Instances[0].Tags = map[string]string{"Name": "new-data"} // the stale listing says no origin
	f.ec2.instanceTags = ec2TagList(map[string]string{"origin": "dld", "Name": "new-data"})
	if _, err := f.run(t, "--tag", "origin=dld", "instance", "new-data"); err != nil || len(f.ec2.createTagsInputs) != 0 {
		t.Errorf("the live tags already have it: %v %v", err, f.ec2.createTagsInputs)
	}
}

func TestRunTagResourceCLI_ADifferentValueIsRefusedWithExitOneAndNothingChanges(t *testing.T) {
	f := newTRFixture()
	f.src.Instances[0].Tags = map[string]string{"origin": "imss"}
	f.ec2.instanceTags = ec2TagList(map[string]string{"origin": "imss"})
	out, err := f.run(t, "--tag", "origin=dld", "--tag", "project=authors", "instance", "new-data")
	if err == nil || CLIExitCode(err) != 1 {
		t.Fatalf("want an exit-1 refusal, got %v", err)
	}
	for _, want := range []string{"origin", "imss", "dld", "--replace"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should mention %q: %v", want, err)
		}
	}
	if len(f.ec2.createTagsInputs) != 0 || out != "" {
		t.Errorf("a refusal changes nothing, not even the tags that were fine: sent %v", f.ec2.createTagsInputs)
	}
}

func TestRunTagResourceCLI_ReplaceChangesTheValueAndSaysSo(t *testing.T) {
	f := newTRFixture()
	f.ec2.instanceTags = ec2TagList(map[string]string{"origin": "imss"})
	out, err := f.run(t, "--replace", "--tag", "origin=dld", "instance", "new-data")
	if err != nil || createdTags(f.ec2)["origin"] != "dld" {
		t.Fatalf("%v %v", err, f.ec2.createTagsInputs)
	}
	if !strings.Contains(out, "replaced origin") || !strings.Contains(out, "imss") {
		t.Errorf("output = %q", out)
	}
}

func TestRunTagResourceCLI_SeveralTagsAreAddedAndWhatIsAlreadyThereIsLeft(t *testing.T) {
	f := newTRFixture()
	f.ec2.instanceTags = ec2TagList(map[string]string{"project": "authors"})
	out, err := f.run(t, "--tag", "origin=dld", "--tag", "project=authors", "--tag", "Environment=test", "instance", "new-data")
	if err != nil {
		t.Fatal(err)
	}
	got := createdTags(f.ec2)
	if got["origin"] != "dld" || got["Environment"] != "test" || len(got) != 2 {
		t.Errorf("only the missing two are sent: %v", got)
	}
	if !strings.Contains(out, "added origin=dld") || !strings.Contains(out, "already") {
		t.Errorf("output = %q", out)
	}
}

func TestRunTagResourceCLI_JSONReportsWhatHappened(t *testing.T) {
	f := newTRFixture()
	f.ec2.instanceTags = ec2TagList(map[string]string{"project": "authors", "origin": "imss"})
	var out bytes.Buffer
	o := parseTR(t, "-json", "--replace", "--tag", "origin=dld", "--tag", "project=authors", "--tag", "Environment=test", "instance", "new-data")
	if err := RunTagResourceCLI(context.Background(), &out, f.src, o); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"kind": "instance"`, `"id": "i-new"`, `"name": "new-data"`, `"added"`, `"Environment": "test"`, `"replaced"`, `"from": "imss"`, `"to": "dld"`, `"unchanged"`, `"project": "authors"`, `"tags"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("JSON missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunTagResourceCLI_AResourceThatDoesNotExistIsAUsageErrorForEveryKind(t *testing.T) {
	for _, kind := range []string{"instance", "ami", "launch-template", "key-pair", "s3-bucket", "iam-policy", "ebs-volume", "ebs-snapshot", "network-interface"} {
		f := newTRFixture()
		_, err := f.run(t, "--tag", "origin=dld", kind, "ghost")
		var ue *UsageError
		if !errors.As(err, &ue) || CLIExitCode(err) != 2 {
			t.Errorf("%s: want a usage error, got %v", kind, err)
		}
		if len(f.ec2.createTagsInputs)+len(f.s3.putBucketTaggingCalls) != 0 || f.iam.lastTagPolicyInput != nil {
			t.Errorf("%s: nothing may be sent for a resource that does not exist", kind)
		}
	}
	for kind, setup := range map[string]func(*fakeIAMClient){
		"iam-role":             func(f *fakeIAMClient) { f.listRoleTagsErr = &iamtypes.NoSuchEntityException{} },
		"iam-instance-profile": func(f *fakeIAMClient) { f.listInstProfTagsErr = &iamtypes.NoSuchEntityException{} },
	} {
		f := newTRFixture()
		setup(f.iam)
		_, err := f.run(t, "--tag", "origin=dld", kind, "ghost")
		var ue *UsageError
		if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "ghost") {
			t.Errorf("%s: want a usage error naming it, got %v", kind, err)
		}
	}
}

func TestRunTagResourceCLI_AnAmbiguousNameIsAUsageErrorListingTheMatches(t *testing.T) {
	f := newTRFixture()
	_, err := f.run(t, "--tag", "origin=dld", "instance", "twin")
	var ue *UsageError
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "i-dup1") || !strings.Contains(ue.Msg, "i-dup2") {
		t.Errorf("got %v", err)
	}
	if len(f.ec2.createTagsInputs) != 0 {
		t.Errorf("nothing may be tagged on a guess")
	}
}

func TestRunTagResourceCLI_AnAWSNotFoundForAnIDIsAUsageErrorButOtherErrorsAreNot(t *testing.T) {
	f := newTRFixture()
	f.ec2.describeVolumesErr = &smithy.GenericAPIError{Code: "InvalidVolume.NotFound", Message: "no such volume"}
	f.ec2b.describeVolumesErr = &smithy.GenericAPIError{Code: "InvalidVolume.NotFound", Message: "no such volume"}
	var ue *UsageError
	if _, err := f.run(t, "--tag", "origin=dld", "ebs-volume", "vol-gone"); !errors.As(err, &ue) {
		t.Errorf("a NotFound in every region is a usage error: %v", err)
	}
	f = newTRFixture()
	f.ec2.describeVolumesErr = &smithy.GenericAPIError{Code: "UnauthorizedOperation", Message: "no"}
	_, err := f.run(t, "--tag", "origin=dld", "ebs-volume", "vol-1")
	if err == nil || errors.As(err, &ue) || CLIExitCode(err) != 1 {
		t.Errorf("any other AWS error is exit 1: %v", err)
	}
}

func TestRunTagResourceCLI_AFailedWriteIsExitOneAndNamesTheResource(t *testing.T) {
	f := newTRFixture()
	f.ec2.createTagsErr = errors.New("TagLimitExceeded")
	_, err := f.run(t, "--tag", "origin=dld", "instance", "new-data")
	if err == nil || CLIExitCode(err) != 1 || !strings.Contains(err.Error(), "TagLimitExceeded") || !strings.Contains(err.Error(), "i-new") {
		t.Errorf("got %v", err)
	}
}

// ec2TagList is a tag map as the SDK's tag list, for the fake's live DescribeInstances answer.
func ec2TagList(m map[string]string) []types.Tag {
	var tags []types.Tag
	for k, v := range m {
		tags = append(tags, types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return tags
}

func TestParseTagResourceArgs_SpacesAroundTheKeyAreTrimmedButTheValueIsKept(t *testing.T) {
	o := parseTR(t, "--tag", "  origin =dld ", "instance", "n")
	if o.Tags[0] != (TagKV{"origin", "dld "}) {
		t.Errorf("tags = %+v", o.Tags)
	}
}

func TestRunTagResourceCLI_JSONTagsAreTheResourcesTagsAfterTheChange(t *testing.T) {
	f := newTRFixture()
	f.ec2.instanceTags = ec2TagList(map[string]string{"project": "authors", "origin": "imss", "Name": "new-data"})
	var out bytes.Buffer
	o := parseTR(t, "-json", "--replace", "--tag", "origin=dld", "--tag", "Environment=test", "instance", "new-data")
	if err := RunTagResourceCLI(context.Background(), &out, f.src, o); err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Tags map[string]string `json:"tags"`
	}
	if err := json.Unmarshal(out.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"project": "authors", "origin": "dld", "Name": "new-data", "Environment": "test"}
	if !reflect.DeepEqual(rec.Tags, want) {
		t.Errorf("tags = %v, want %v", rec.Tags, want)
	}
}

func TestRunTagResourceCLI_AMalformedIDInEveryRegionIsAUsageError(t *testing.T) {
	f := newTRFixture()
	bad := &smithy.GenericAPIError{Code: "InvalidVolumeID.Malformed", Message: "bad id"}
	f.ec2.describeVolumesErr, f.ec2b.describeVolumesErr = bad, bad
	_, err := f.run(t, "--tag", "origin=dld", "ebs-volume", "not-a-volume")
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Errorf("a malformed ID is the caller's mistake: %v", err)
	}
}

func TestRunTagResourceCLI_RegionsAreTriedInNameOrderSoTheResultDoesNotDependOnMapOrder(t *testing.T) {
	for i := 0; i < 20; i++ {
		f := newTRFixture()
		both := []types.Volume{{VolumeId: aws.String("vol-both")}}
		f.ec2.describeVolumesOutput, f.ec2b.describeVolumesOutput = both, both
		if _, err := f.run(t, "--tag", "origin=dld", "ebs-volume", "vol-both"); err != nil {
			t.Fatal(err)
		}
		if len(f.ec2b.createTagsInputs) != 1 || len(f.ec2.createTagsInputs) != 0 {
			t.Fatalf("us-west-1 sorts before us-west-2: west1=%d west2=%d", len(f.ec2b.createTagsInputs), len(f.ec2.createTagsInputs))
		}
	}
}

func TestRunTagResourceCLI_AVolumesNameIsItsNameTag(t *testing.T) {
	f := newTRFixture()
	var out bytes.Buffer
	if err := RunTagResourceCLI(context.Background(), &out, f.src, parseTR(t, "-json", "--tag", "origin=dld", "ebs-volume", "vol-west1")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"name": "old"`) || !strings.Contains(out.String(), `"id": "vol-west1"`) {
		t.Errorf("JSON = %s", out.String())
	}
}

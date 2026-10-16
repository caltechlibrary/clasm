package workflow

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// The untagged-resource report (DR-0192): what lacks the configured Origin tag, and what
// has it with another value, across the eleven Tag Management kinds. Read-only.

var untaggedOrigin = config.OriginTagConfig{Key: "origin", DLDValue: "dld"}

func untaggedFixture() (TagSources, *fakeEC2Client, *fakeIAMClient) {
	ec2Fake := &fakeEC2Client{
		describeVolumesOutput: []types.Volume{
			{VolumeId: aws.String("vol-tagged"), Tags: []types.Tag{{Key: aws.String("origin"), Value: aws.String("dld")}}},
			{VolumeId: aws.String("vol-bare"), Tags: []types.Tag{{Key: aws.String("Name"), Value: aws.String("root of new-data")}}},
		},
		describeSnapshotsOutput:         []types.Snapshot{{SnapshotId: aws.String("snap-bare")}},
		describeNetworkInterfacesOutput: []types.NetworkInterface{{NetworkInterfaceId: aws.String("eni-bare")}},
	}
	iamFake := &fakeIAMClient{
		roles: []iamtypes.Role{
			{RoleName: aws.String("AWSServiceRoleForSupport"), Path: aws.String("/aws-service-role/support.amazonaws.com/")},
			{RoleName: aws.String("lambda-role"), Path: aws.String("/service-role/")},
			{RoleName: aws.String("rdm-backups")},
			{RoleName: aws.String("dld-role")},
		},
		roleTags:         map[string][]iamtypes.Tag{"dld-role": {{Key: aws.String("origin"), Value: aws.String("dld")}}},
		instanceProfiles: []iamtypes.InstanceProfile{{InstanceProfileName: aws.String("rdm-backups")}},
		policies:         []iamtypes.Policy{{PolicyName: aws.String("rdm-backups-policy"), Arn: aws.String("arn:aws:iam::1:policy/rdm-backups-policy")}},
	}
	s3Fake := &fakeS3Client{}
	src := TagSources{
		EC2Clients: map[string]awsclient.EC2API{"us-west-2": ec2Fake},
		NewS3Client: func(ctx context.Context, region string) (awsclient.S3API, error) {
			return s3Fake, nil
		},
		IAMClient: iamFake,
		OriginTag: untaggedOrigin,
		Instances: []inventory.Instance{
			{InstanceID: "i-tagged", Name: "tagged-box", Region: "us-west-2", Created: "2026-08-28", Tags: map[string]string{"origin": "dld"}},
			{InstanceID: "i-bare", Name: "new-data", Region: "us-west-2", Created: "2026-05-21", Tags: map[string]string{"Name": "new-data"}},
			{InstanceID: "i-imss", Name: "imss-box", Region: "us-west-2", Tags: map[string]string{"origin": "imss"}},
		},
		Images:          []inventory.Image{{ImageID: "ami-bare", Name: "old-ami", Region: "us-west-2", CreationDate: "2025-01-02T03:04:05.000Z"}},
		LaunchTemplates: []inventory.LaunchTemplate{{TemplateID: "lt-tagged", Name: "tagged-lt", Tags: map[string]string{"origin": "dld"}}},
		KeyPairs:        []inventory.KeyPair{{KeyName: "test-clasm", KeyPairID: "key-1", Region: "us-west-2", Created: "2026-03-04"}},
		Buckets:         []inventory.Bucket{{Name: "plain-bucket", Region: "us-west-2", Created: "2020-02-05"}},
	}
	return src, ec2Fake, iamFake
}

func idsOf(items []UntaggedItem) []string {
	var out []string
	for _, i := range items {
		out = append(out, i.Kind+":"+i.ID)
	}
	return out
}

func TestBuildUntaggedReport_ListsWhatLacksTheTagAndSeparatesAnotherValue(t *testing.T) {
	src, _, _ := untaggedFixture()
	report, err := BuildUntaggedReport(context.Background(), src, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(idsOf(report.Missing), " ")
	for _, want := range []string{"instance:i-bare", "ami:ami-bare", "key-pair:key-1", "s3-bucket:plain-bucket", "iam-role:lambda-role", "iam-role:rdm-backups",
		"iam-instance-profile:rdm-backups", "iam-policy:arn:aws:iam::1:policy/rdm-backups-policy", "ebs-volume:vol-bare", "ebs-snapshot:snap-bare", "network-interface:eni-bare"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing list lacks %s:\n%s", want, got)
		}
	}
	for _, not := range []string{"i-tagged", "lt-tagged", "dld-role", "vol-tagged", "i-imss", "AWSServiceRole"} {
		if strings.Contains(got, not) {
			t.Errorf("%s carries the tag (or is excluded) and must not be listed as missing:\n%s", not, got)
		}
	}
	if other := idsOf(report.OtherValue); len(other) != 1 || other[0] != "instance:i-imss" {
		t.Errorf("a resource with another value goes in its own group: %v", other)
	}
	if report.OtherValue[0].OriginValue != "imss" {
		t.Errorf("the value it has must be reported: %+v", report.OtherValue[0])
	}
}

func TestBuildUntaggedReport_OrdersByKindThenNameAndCarriesWhatTheAuthorNeedsToDecide(t *testing.T) {
	src, _, _ := untaggedFixture()
	report, _ := BuildUntaggedReport(context.Background(), src, nil)
	order := []string{"instance", "ami", "key-pair", "s3-bucket", "iam-role", "iam-instance-profile", "iam-policy", "ebs-volume", "ebs-snapshot", "network-interface"}
	pos := map[string]int{}
	for i, k := range order {
		pos[k] = i
	}
	for i := 1; i < len(report.Missing); i++ {
		if pos[report.Missing[i-1].Kind] > pos[report.Missing[i].Kind] {
			t.Errorf("kinds out of picker order at %d: %v", i, idsOf(report.Missing))
		}
	}
	by := map[string]UntaggedItem{}
	for _, it := range report.Missing {
		by[it.ID] = it
	}
	if it := by["i-bare"]; it.Name != "new-data" || it.Region != "us-west-2" || it.Created != "2026-05-21" || it.Tags["Name"] != "new-data" {
		t.Errorf("instance fields: %+v", it)
	}
	if it := by["ami-bare"]; it.Created != "2025-01-02" || it.Name != "old-ami" {
		t.Errorf("an AMI's creation timestamp is cut to its date: %+v", it)
	}
	if it := by["plain-bucket"]; it.Created != "2020-02-05" || it.Region != "us-west-2" {
		t.Errorf("bucket fields: %+v", it)
	}
	if it := by["vol-bare"]; it.Name != "root of new-data" || it.Region != "us-west-2" {
		t.Errorf("a volume has no name of its own, so its Name tag stands in: %+v", it)
	}
}

func TestBuildUntaggedReport_LeavesOutServiceLinkedRolesAndCountsThem(t *testing.T) {
	src, _, _ := untaggedFixture()
	report, _ := BuildUntaggedReport(context.Background(), src, []string{"IAM Role"})
	got := strings.Join(idsOf(report.Missing), " ")
	if strings.Contains(got, "AWSServiceRole") {
		t.Errorf("an AWS service-linked role is not the account's to tag: %s", got)
	}
	if !strings.Contains(got, "lambda-role") {
		t.Errorf("a role under /service-role/ is the account's own and stays: %s", got)
	}
	if report.ServiceLinkedRoles != 1 {
		t.Errorf("ServiceLinkedRoles = %d, want 1", report.ServiceLinkedRoles)
	}
}

// Naming kinds narrows the report to them, and only their data is fetched: nothing else
// is touched (a nil client would panic).
func TestBuildUntaggedReport_KindsNarrowTheReportAndTheCalls(t *testing.T) {
	src, _, _ := untaggedFixture()
	only := TagSources{Instances: src.Instances, OriginTag: src.OriginTag} // no IAM, S3 or EC2 clients
	report, err := BuildUntaggedReport(context.Background(), only, []string{"Instance"})
	if err != nil {
		t.Fatal(err)
	}
	if got := idsOf(report.Missing); len(got) != 1 || got[0] != "instance:i-bare" {
		t.Errorf("got %v", got)
	}
}

func TestBuildUntaggedReport_WithNoDLDValueConfiguredAnyValueIsAnotherValue(t *testing.T) {
	src, _, _ := untaggedFixture()
	src.OriginTag = config.OriginTagConfig{Key: "origin"}
	report, _ := BuildUntaggedReport(context.Background(), src, []string{"Instance"})
	if got := idsOf(report.OtherValue); len(got) != 2 {
		t.Errorf("with no DLD value recognised yet, i-tagged and i-imss both carry some other value: %v", got)
	}
}

func TestBuildUntaggedReport_ARegionThatFailsIsNamed(t *testing.T) {
	src := TagSources{OriginTag: untaggedOrigin, EC2Clients: map[string]awsclient.EC2API{"us-west-2": &fakeEC2Client{describeVolumesErr: errors.New("UnauthorizedOperation")}}}
	if _, err := BuildUntaggedReport(context.Background(), src, []string{"EBS Volume"}); err == nil || !strings.Contains(err.Error(), "us-west-2") {
		t.Errorf("want an error naming the region, got %v", err)
	}
}

func TestShowUntagged_IsReadOnly(t *testing.T) {
	src, ec2Fake, iamFake := untaggedFixture()
	var out bytes.Buffer
	if err := RunShowUntaggedCLI(context.Background(), &out, src, nil, ui.FormatText); err != nil {
		t.Fatal(err)
	}
	if ec2Fake.lastCreateTagsInput != nil || iamFake.lastTagRoleInput != nil || iamFake.lastTagInstanceProfileInput != nil || iamFake.lastTagPolicyInput != nil {
		t.Errorf("the report must send only reads")
	}
}

func TestRunShowUntaggedCLI_TextJSONAndTheNote(t *testing.T) {
	src, _, _ := untaggedFixture()
	var text bytes.Buffer
	if err := RunShowUntaggedCLI(context.Background(), &text, src, nil, ui.FormatText); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"i-bare", "missing", "other: imss", "1 AWS service-linked IAM role", "never listed"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("text missing %q:\n%s", want, text.String())
		}
	}
	var js bytes.Buffer
	if err := RunShowUntaggedCLI(context.Background(), &js, src, []string{"instance"}, ui.FormatJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js.String(), `"origin_status": "missing"`) || !strings.Contains(js.String(), `"origin_status": "other-value"`) || strings.Contains(js.String(), "never listed") {
		t.Errorf("JSON = %s", js.String())
	}
}

func TestRunShowUntaggedCLI_NothingMissingIsASuccess(t *testing.T) {
	src := TagSources{OriginTag: untaggedOrigin, Instances: []inventory.Instance{{InstanceID: "i-1", Tags: map[string]string{"origin": "dld"}}}}
	var out bytes.Buffer
	if err := RunShowUntaggedCLI(context.Background(), &out, src, []string{"instance"}, ui.FormatText); err != nil || CLIExitCode(err) != 0 {
		t.Fatalf("a report that finds nothing is exit 0: %v", err)
	}
	if !strings.Contains(out.String(), "Nothing is missing the Origin tag") {
		t.Errorf("output = %q", out.String())
	}
}

func TestRunShowUntaggedCLI_AnUnknownKindIsAUsageErrorListingTheKindsBeforeAnyCall(t *testing.T) {
	err := RunShowUntaggedCLI(context.Background(), &bytes.Buffer{}, TagSources{}, []string{"instance", "no-such-kind"}, ui.FormatText)
	var ue *UsageError
	if !errors.As(err, &ue) || CLIExitCode(err) != 2 {
		t.Fatalf("want a usage error, got %v", err)
	}
	for _, want := range []string{"no-such-kind", "ebs-volume", "network-interface", "iam-role"} {
		if !strings.Contains(ue.Msg, want) {
			t.Errorf("the error should mention %q: %s", want, ue.Msg)
		}
	}
}

func TestRunShowUntaggedCLI_AKindGivenTwiceIsReportedOnce(t *testing.T) {
	src, _, _ := untaggedFixture()
	var out bytes.Buffer
	if err := RunShowUntaggedCLI(context.Background(), &out, src, []string{"instance", "instance"}, ui.FormatJSONL); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out.String(), "i-bare"); n != 1 {
		t.Errorf("i-bare listed %d times", n)
	}
}

func TestBuildUntaggedReport_ResourcesOfOneKindAreInNameOrder(t *testing.T) {
	src := TagSources{OriginTag: untaggedOrigin, Instances: []inventory.Instance{
		{InstanceID: "i-3", Name: "zulu"}, {InstanceID: "i-2", Name: "alpha"}, {InstanceID: "i-1", Name: "alpha"}, {InstanceID: "i-4", Name: "mike"},
	}}
	report, _ := BuildUntaggedReport(context.Background(), src, []string{"Instance"})
	if got := strings.Join(idsOf(report.Missing), " "); got != "instance:i-1 instance:i-2 instance:i-4 instance:i-3" {
		t.Errorf("want name order, then ID: %s", got)
	}
}

func TestBuildUntaggedReport_IAMCreationDatesAreReportedWhenThereAreOnes(t *testing.T) {
	src, _, iamFake := untaggedFixture()
	iamFake.roles = []iamtypes.Role{
		{RoleName: aws.String("dated"), CreateDate: aws.Time(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))},
		{RoleName: aws.String("undated")},
	}
	report, _ := BuildUntaggedReport(context.Background(), src, []string{"IAM Role"})
	got := map[string]string{}
	for _, it := range report.Missing {
		got[it.ID] = it.Created
	}
	if got["dated"] != "2026-01-02" || got["undated"] != "" {
		t.Errorf("created dates = %v", got)
	}
}

// With no DLD-owned value recognised yet, an Origin tag that is present is not "tagged",
// whatever it holds, even nothing.
func TestBuildUntaggedReport_AnEmptyOriginValueWithNoDLDValueIsAnotherValueNotTagged(t *testing.T) {
	src := TagSources{OriginTag: config.OriginTagConfig{Key: "origin"}, Instances: []inventory.Instance{
		{InstanceID: "i-empty", Tags: map[string]string{"origin": ""}},
	}}
	report, _ := BuildUntaggedReport(context.Background(), src, []string{"Instance"})
	if len(report.Missing) != 0 || len(report.OtherValue) != 1 {
		t.Errorf("missing=%v other=%v", idsOf(report.Missing), idsOf(report.OtherValue))
	}
}

func TestRunShowUntaggedCLI_ListsWhatIsMissingBeforeWhatHasAnotherValue(t *testing.T) {
	src, _, _ := untaggedFixture()
	var out bytes.Buffer
	if err := RunShowUntaggedCLI(context.Background(), &out, src, []string{"instance", "ebs-volume"}, ui.FormatText); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !(strings.Index(s, "i-bare") < strings.Index(s, "vol-bare") && strings.Index(s, "vol-bare") < strings.Index(s, "i-imss")) {
		t.Errorf("every missing resource comes before any with another value:\n%s", s)
	}
}

func TestRunShowUntaggedCLI_TheNoteOnlyMentionsServiceLinkedRolesWhenSomeWereLeftOut(t *testing.T) {
	src := TagSources{OriginTag: untaggedOrigin, Instances: []inventory.Instance{{InstanceID: "i-1"}}}
	var out bytes.Buffer
	_ = RunShowUntaggedCLI(context.Background(), &out, src, []string{"instance"}, ui.FormatText)
	if strings.Contains(out.String(), "Left out") || !strings.Contains(out.String(), "never listed") {
		t.Errorf("output = %q", out.String())
	}
}

func TestShowUntagged_TheKindChoiceNarrowsWhatIsRead(t *testing.T) {
	// "Instance" is the second entry (after "All kinds"). Only instances are supplied, so
	// reading any other kind would call a nil client.
	src := TagSources{OriginTag: untaggedOrigin, Instances: []inventory.Instance{{InstanceID: "i-ok", Tags: map[string]string{"origin": "dld"}}}}
	term, in, buf := newPipeEditor("2\n")
	if err := showUntagged(context.Background(), term, src, in, buf); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "Nothing is missing the Origin tag") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestShowUntagged_AllKindsReadsEveryKind(t *testing.T) {
	src, _, iamFake := untaggedFixture()
	iamFake.roles, iamFake.instanceProfiles, iamFake.policies = nil, nil, nil
	src.Instances, src.Images, src.KeyPairs, src.Buckets, src.LaunchTemplates = nil, nil, nil, nil, nil
	src.EC2Clients = map[string]awsclient.EC2API{"us-west-2": &fakeEC2Client{}}
	term, in, buf := newPipeEditor("1\n")
	if err := showUntagged(context.Background(), term, src, in, buf); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "Nothing is missing the Origin tag") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestKindsForChoice(t *testing.T) {
	if got := kindsForChoice("All kinds"); len(got) != 0 {
		t.Errorf("All kinds means no narrowing, got %v", got)
	}
	if got := kindsForChoice("EBS Volume"); len(got) != 1 || got[0] != "EBS Volume" {
		t.Errorf("one kind stands for itself, got %v", got)
	}
}

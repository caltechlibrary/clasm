package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// Every kind the interactive picker offers has exactly one CLI kind slug, and a
// kind added to the picker without a slug fails here rather than silently being
// unreachable from the command line.
func TestTagKindSlugs_CoverEveryInteractiveKind(t *testing.T) {
	shape := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	seen := map[string]bool{}
	for _, kind := range tagManagementKinds {
		slug, ok := tagKindSlugs[kind]
		if !ok {
			t.Errorf("kind %q has no CLI slug", kind)
			continue
		}
		if !shape.MatchString(slug) || seen[slug] {
			t.Errorf("kind %q: slug %q is malformed or used twice", kind, slug)
		}
		seen[slug] = true
	}
	if len(tagKindSlugs) != len(tagManagementKinds) {
		t.Errorf("%d slugs for %d kinds", len(tagKindSlugs), len(tagManagementKinds))
	}
}

func tagSources() TagSources {
	return TagSources{
		Instances:       []inventory.Instance{{InstanceID: "i-1", Name: "web-1", State: "running", Tags: map[string]string{"Name": "web-1", "Owner": "dld"}}},
		Images:          []inventory.Image{{ImageID: "ami-1", Name: "img", Tags: map[string]string{"Name": "img"}}},
		LaunchTemplates: []inventory.LaunchTemplate{{TemplateID: "lt-1", Name: "tmpl"}},
		KeyPairs:        []inventory.KeyPair{{KeyName: "k", KeyPairID: "key-1", Tags: map[string]string{"Owner": "dld"}}},
		Buckets:         []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2"}},
		NewS3Client: newRegionS3Client(map[string]awsclient.S3API{
			"us-west-2": &fakeS3Client{tagSet: []types.Tag{{Key: aws.String("Purpose"), Value: aws.String("backup")}}},
		}),
		IAMClient: iamListFake(),
		OriginTag: testOriginTag,
	}
}

func TestRunShowAllTagsCLI_EveryKind(t *testing.T) {
	for _, kind := range []string{"instance", "ami", "launch-template", "key-pair", "s3-bucket", "iam-role", "iam-instance-profile", "iam-policy"} {
		var b bytes.Buffer
		if err := RunShowAllTagsCLI(context.Background(), &b, tagSources(), kind, ui.FormatJSON); err != nil {
			t.Errorf("%s: %v", kind, err)
			continue
		}
		var got []map[string]any
		if err := json.Unmarshal(b.Bytes(), &got); err != nil || len(got) == 0 {
			t.Errorf("%s: %v\n%s", kind, err, b.String())
			continue
		}
		if got[0]["kind"] != kind {
			t.Errorf("%s: record kind = %v", kind, got[0]["kind"])
		}
		if id, _ := got[0]["id"].(string); id == "" {
			t.Errorf("%s: empty id in %v", kind, got[0])
		}
	}
}

func TestRunShowAllTagsCLI_TagsAreComplete(t *testing.T) {
	var b bytes.Buffer
	if err := RunShowAllTagsCLI(context.Background(), &b, tagSources(), "instance", ui.FormatJSON); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	_ = json.Unmarshal(b.Bytes(), &got)
	if tags, _ := got[0]["tags"].(map[string]any); tags["Owner"] != "dld" || tags["Name"] != "web-1" {
		t.Errorf("want every tag, got %v", got[0]["tags"])
	}
	b.Reset()
	if err := RunShowAllTagsCLI(context.Background(), &b, tagSources(), "s3-bucket", ui.FormatText); err != nil || !strings.Contains(b.String(), "Purpose=backup") {
		t.Errorf("bucket tags come from GetBucketTagging: %q err=%v", b.String(), err)
	}
}

func TestRunShowAllTagsCLI_UnknownKindIsAUsageErrorListingTheKinds(t *testing.T) {
	var b bytes.Buffer
	err := RunShowAllTagsCLI(context.Background(), &b, TagSources{}, "volume", ui.FormatText)
	usageErr(t, err, "volume")
	for _, slug := range tagKindSlugs {
		if !strings.Contains(err.Error(), slug) {
			t.Errorf("the error should list %q: %v", slug, err)
		}
	}
	if b.Len() != 0 {
		t.Errorf("nothing may be written, got %q", b.String())
	}
}

func TestRunShowAllTagsCLI_AWSFailureIsNotAUsageError(t *testing.T) {
	src := tagSources()
	src.IAMClient = &fakeIAMClient{listRolesErr: errors.New("boom")}
	err := RunShowAllTagsCLI(context.Background(), &bytes.Buffer{}, src, "iam-role", ui.FormatText)
	if err == nil || CLIExitCode(err) != 1 {
		t.Errorf("want exit 1, got %v", err)
	}
}

// Show all tags must send reads only, for every kind. The IAM fake records its
// mutating calls; the S3 fake embeds a nil interface, so any write to it would
// panic this test.
func TestRunShowAllTagsCLI_SendsNoMutatingCall(t *testing.T) {
	src := tagSources()
	iamFake := iamListFake()
	src.IAMClient = iamFake
	var w bytes.Buffer
	for _, kind := range TagKindSlugs() {
		for _, f := range []ui.Format{ui.FormatText, ui.FormatJSON} {
			if err := RunShowAllTagsCLI(context.Background(), &w, src, kind, f); err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
		}
	}
	if w.Len() == 0 {
		t.Fatal("the guard is vacuous: nothing was written")
	}
	if iamFake.lastTagRoleInput != nil || iamFake.lastUntagRoleInput != nil || iamFake.lastTagInstanceProfileInput != nil ||
		iamFake.lastUntagInstanceProfileInput != nil || iamFake.lastTagPolicyInput != nil || iamFake.lastUntagPolicyInput != nil {
		t.Error("Show all tags sent a tagging call")
	}
}

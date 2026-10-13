package workflow

import (
	"bytes"
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
)

// A bucket is tagged with Origin next to its Purpose (DR-0190). It is merged into the
// one tag set this workflow writes, not added by a wrapper on PutBucketTagging, which
// replaces the whole set and would also rewrite every Tag Management edit.

func createdBucketTags(t *testing.T, origin config.OriginTagConfig) map[string]string {
	t.Helper()
	fake := &fakeS3Client{}
	var buf bytes.Buffer
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }
	if err := createBucket(context.Background(), &buf, newClient, []string{"us-west-2"}, origin, newHuhAccessibleInput("my-bucket\n1\n1\n"), &buf); err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, buf.String())
	}
	if len(fake.putBucketTaggingCalls) != 1 {
		t.Fatalf("want one PutBucketTagging, got %d", len(fake.putBucketTaggingCalls))
	}
	tags := map[string]string{}
	for _, tg := range fake.putBucketTaggingCalls[0].Tagging.TagSet {
		if _, dup := tags[aws.ToString(tg.Key)]; dup {
			t.Errorf("tag %q is in the set twice", aws.ToString(tg.Key))
		}
		tags[aws.ToString(tg.Key)] = aws.ToString(tg.Value)
	}
	return tags
}

func TestCreateBucket_IsTaggedWithOriginNextToPurpose(t *testing.T) {
	tags := createdBucketTags(t, config.OriginTagConfig{Key: "origin", DLDValue: "dld"})
	if tags["origin"] != "dld" || tags["Purpose"] == "" || len(tags) != 2 {
		t.Errorf("want Purpose and origin=dld, got %v", tags)
	}
}

func TestCreateBucket_NoConfiguredOriginMeansPurposeOnly(t *testing.T) {
	for name, origin := range map[string]config.OriginTagConfig{
		"no value": {Key: "origin"},
		"no key":   {DLDValue: "dld"},
	} {
		tags := createdBucketTags(t, origin)
		if len(tags) != 1 || tags["Purpose"] == "" {
			t.Errorf("%s: want Purpose only, got %v", name, tags)
		}
	}
}

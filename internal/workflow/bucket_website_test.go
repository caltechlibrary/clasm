package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// Bucket selection (PLAN.md Phase 20.4) now runs a real bubbletea
// Program (tui.RunPicker), which can't be driven by a test's pipe
// input -- see internal/tui/picker_test.go for that component's own
// thorough test suite. Tests below exercise everything once a bucket
// is already resolved via the unexported configureBucketWebsite;
// ConfigureBucketWebsite's own picker-selection step is covered only by
// manual/interactive verification, the same accepted limitation
// object_browser.go's huh-based bucket pre-flight already has.

func TestConfigureBucketWebsite_NoBucketsFound(t *testing.T) {
	term, _, buf := newPipeEditor("")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return nil, nil }

	if err := ConfigureBucketWebsite(context.Background(), term, newClient, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No buckets found") {
		t.Errorf("expected a no-buckets message, got:\n%s", buf.String())
	}
}

func TestConfigureBucketWebsite_DefaultsAppliedViaEnter(t *testing.T) {
	bucket := inventory.Bucket{Name: "my-site", Region: "us-west-2"}
	fake := &fakeS3Client{}
	input := "\n" + // accept default index document
		"\n" // accept default error document

	term, le, buf := newPipeEditor(input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := configureBucketWebsite(context.Background(), term, newClient, bucket, le, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fake.putBucketWebsiteCalls) != 1 {
		t.Fatalf("putBucketWebsiteCalls = %d, want 1", len(fake.putBucketWebsiteCalls))
	}
	cfg := fake.putBucketWebsiteCalls[0].WebsiteConfiguration
	if aws.ToString(cfg.IndexDocument.Suffix) != "index.html" {
		t.Errorf("index document = %q, want default index.html", aws.ToString(cfg.IndexDocument.Suffix))
	}
	if aws.ToString(cfg.ErrorDocument.Key) != "error.html" {
		t.Errorf("error document = %q, want default error.html", aws.ToString(cfg.ErrorDocument.Key))
	}
}

func TestConfigureBucketWebsite_SuccessPathWithCustomDocuments(t *testing.T) {
	bucket := inventory.Bucket{Name: "my-site", Region: "us-west-2"}
	fake := &fakeS3Client{}
	input := "home.html\n" +
		"oops.html\n"

	term, le, buf := newPipeEditor(input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := configureBucketWebsite(context.Background(), term, newClient, bucket, le, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if aws.ToString(fake.putBucketWebsiteCalls[0].Bucket) != "my-site" {
		t.Errorf("bucket = %q, want my-site", aws.ToString(fake.putBucketWebsiteCalls[0].Bucket))
	}
	cfg := fake.putBucketWebsiteCalls[0].WebsiteConfiguration
	if aws.ToString(cfg.IndexDocument.Suffix) != "home.html" || aws.ToString(cfg.ErrorDocument.Key) != "oops.html" {
		t.Errorf("website config = %+v, want home.html/oops.html", cfg)
	}
	if !strings.Contains(buf.String(), "CloudFront") {
		t.Errorf("expected the CloudFront-not-implemented note, got:\n%s", buf.String())
	}
}

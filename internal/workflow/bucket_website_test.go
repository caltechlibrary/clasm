package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
)

func TestConfigureBucketWebsite_NoBucketsFound(t *testing.T) {
	term, le, buf := newPipeEditor(t, "")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return nil, nil }

	if err := ConfigureBucketWebsite(context.Background(), term, le, newClient, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No buckets found") {
		t.Errorf("expected a no-buckets message, got:\n%s", buf.String())
	}
}

func TestConfigureBucketWebsite_DefaultsAppliedViaEnter(t *testing.T) {
	buckets := []inventory.Bucket{{Name: "my-site", Region: "us-west-2"}}
	fake := &fakeS3Client{}
	input := "1\n" + // pick the bucket
		"\n" + // accept default index document
		"\n" // accept default error document

	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := ConfigureBucketWebsite(context.Background(), term, le, newClient, buckets); err != nil {
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
	buckets := []inventory.Bucket{{Name: "my-site", Region: "us-west-2"}}
	fake := &fakeS3Client{}
	input := "1\n" +
		"home.html\n" +
		"oops.html\n"

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := ConfigureBucketWebsite(context.Background(), term, le, newClient, buckets); err != nil {
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

func TestConfigureBucketWebsite_CancellationAbortsCleanly(t *testing.T) {
	buckets := []inventory.Bucket{{Name: "my-site", Region: "us-west-2"}}
	fake := &fakeS3Client{}
	term, le, _ := newPipeEditor(t, "0\n")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	err := ConfigureBucketWebsite(context.Background(), term, le, newClient, buckets)
	if err != nil {
		t.Fatalf("expected a clean cancellation (nil error), got: %v", err)
	}
	if len(fake.putBucketWebsiteCalls) != 0 {
		t.Errorf("putBucketWebsiteCalls = %d, want 0 after cancelling", len(fake.putBucketWebsiteCalls))
	}
}

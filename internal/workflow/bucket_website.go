package workflow

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
	"github.com/caltechlibrary/awstools/internal/ui"
)

func bucketLabel(b inventory.Bucket) string {
	website := "no"
	if b.StaticWebsite {
		website = "yes"
	}
	purpose := b.Purpose
	if purpose == "" {
		purpose = "untagged"
	}
	return fmt.Sprintf("%s (%s, website: %s, purpose: %s)", b.Name, b.Region, website, purpose)
}

// ConfigureBucketWebsite runs the S3 domain's "Configure Static Website
// Hosting" workflow (DESIGN.md, Feature 19), default path only -- the
// public-read bucket policy opt-out is deferred until CloudFront exists to
// hand off to (see PLAN.md's Phase 20 plan): pick a bucket, prompt index
// and error documents (defaulted to index.html/error.html), s3:
// PutBucketWebsite, then print a note that CloudFront isn't implemented
// yet instead of DESIGN.md's literal "offer to return to Feature 24".
func ConfigureBucketWebsite(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), buckets []inventory.Bucket) error {
	if len(buckets) == 0 {
		t.Println("No buckets found.")
		t.Refresh()
		return nil
	}

	bucket, err := ui.PickList(t, le, buckets, bucketLabel, "Select a bucket")
	if err != nil {
		return cancelledIsNil(t, err)
	}

	client, err := newS3Client(ctx, bucket.Region)
	if err != nil {
		return err
	}

	indexDoc, err := ui.Prompt(t, le, "Index document", ui.WithDefault("index.html"), ui.WithValidator(requireNonEmpty))
	if err != nil {
		return err
	}
	errorDoc, err := ui.Prompt(t, le, "Error document", ui.WithDefault("error.html"), ui.WithValidator(requireNonEmpty))
	if err != nil {
		return err
	}

	if _, err := client.PutBucketWebsite(ctx, &s3.PutBucketWebsiteInput{
		Bucket: aws.String(bucket.Name),
		WebsiteConfiguration: &types.WebsiteConfiguration{
			IndexDocument: &types.IndexDocument{Suffix: aws.String(indexDoc)},
			ErrorDocument: &types.ErrorDocument{Key: aws.String(errorDoc)},
		},
	}); err != nil {
		return fmt.Errorf("configuring static website hosting on bucket %s: %w", bucket.Name, err)
	}

	t.Printf("Configured static website hosting on bucket %s (index: %s, error: %s).\n", bucket.Name, indexDoc, errorDoc)
	t.Println("CloudFront support isn't implemented yet (Phase 21) -- once it is, front this bucket with a CloudFront distribution rather than making it public.")
	t.Refresh()
	return nil
}

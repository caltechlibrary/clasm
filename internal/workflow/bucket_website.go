package workflow

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/tui"
	"github.com/caltechlibrary/clasm/internal/ui"
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

// pickBucket runs a Picker-tier screen (DESIGN.md, "Terminal UI
// Architecture...," "Picker tier"; PLAN.md Phase 20.4) over buckets,
// reusing the shared bucketLabel format, and returns the chosen bucket.
// Callers map tui.ErrCancelled through cancelledIsNil at their own
// return point, the same convention every other pick-list-shaped call
// site already uses.
func pickBucket(ctx context.Context, title string, buckets []inventory.Bucket) (inventory.Bucket, error) {
	rows := make([]string, len(buckets))
	for i, b := range buckets {
		rows[i] = bucketLabel(b)
	}

	idx, err := tui.RunPicker(ctx, tui.PickerConfig{
		Title:        title,
		Rows:         rows,
		ColorEnabled: ui.ColorEnabled(),
	})
	if err != nil {
		return inventory.Bucket{}, err
	}
	return buckets[idx], nil
}

// ConfigureBucketWebsite runs the S3 domain's "Configure Static Website
// Hosting" workflow (DESIGN.md, Feature 19), default path only -- the
// public-read bucket policy opt-out is deferred until CloudFront exists to
// hand off to (see PLAN.md's Phase 20 plan): pick a bucket, prompt index
// and error documents (defaulted to index.html/error.html), s3:
// PutBucketWebsite, then print a note that CloudFront isn't implemented
// yet instead of DESIGN.md's literal "offer to return to Feature 24".
//
// Bucket selection runs a real bubbletea Program (tui.RunPicker) that
// can't be driven by a test's pipe input, so the rest of the workflow --
// everything once a bucket is resolved -- lives in the unexported,
// directly-testable configureBucketWebsite.
func ConfigureBucketWebsite(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), buckets []inventory.Bucket) error {
	if len(buckets) == 0 {
		t.Println("No buckets found.")
		t.Refresh()
		return nil
	}

	bucket, err := pickBucket(ctx, "Select a bucket", buckets)
	if err != nil {
		return cancelledIsNil(t, err)
	}

	return configureBucketWebsite(ctx, t, le, newS3Client, bucket)
}

func configureBucketWebsite(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), bucket inventory.Bucket) error {
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

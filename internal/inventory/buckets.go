package inventory

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"github.com/caltechlibrary/awstools/internal/awsclient"
)

// Bucket is an S3 bucket as displayed/managed by awsops (DESIGN.md,
// Feature 17: "List Buckets"). Unlike Compute-domain resources, buckets
// are account-wide, not per-region.
type Bucket struct {
	Name          string
	Region        string
	StaticWebsite bool
	Purpose       string // "website", "backup", "internal", or "" (untagged)
}

// ListBuckets calls s3:ListBuckets once on client (a control-plane call,
// safe from any region), then fans out one goroutine per bucket to
// resolve its region (via s3:GetBucketLocation on client, also
// control-plane-safe) and enrich it with its static-website and Purpose-
// tag state. Those last two are bucket-content calls, so each goroutine
// builds its own region-scoped client via newClient first -- calling them
// against the wrong region's client 301s with no useful detail (see
// DECISIONS.md, "Resolve a bucket's actual region before Backup Archive &
// Trim's access check"; the same risk applies here). A
// NoSuchWebsiteConfiguration error and a missing Purpose tag
// (NoSuchTagSet, or a tag set with no Purpose key) are not fan-out
// errors -- they just mean StaticWebsite: false / Purpose: "".
func ListBuckets(ctx context.Context, client awsclient.S3API, newClient func(ctx context.Context, region string) (awsclient.S3API, error)) ([]Bucket, error) {
	out, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("listing buckets: %w", err)
	}

	type result struct {
		bucket Bucket
		err    error
	}

	results := make(chan result, len(out.Buckets))
	var wg sync.WaitGroup
	for _, b := range out.Buckets {
		name := aws.ToString(b.Name)
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			bucket, err := enrichBucket(ctx, client, newClient, name)
			results <- result{bucket: bucket, err: err}
		}(name)
	}
	wg.Wait()
	close(results)

	var all []Bucket
	for r := range results {
		if r.err != nil {
			return nil, r.err
		}
		all = append(all, r.bucket)
	}
	return all, nil
}

func enrichBucket(ctx context.Context, client awsclient.S3API, newClient func(ctx context.Context, region string) (awsclient.S3API, error), name string) (Bucket, error) {
	region, err := bucketRegion(ctx, client, name)
	if err != nil {
		return Bucket{}, fmt.Errorf("%s: determining region: %w", name, err)
	}

	regionClient, err := newClient(ctx, region)
	if err != nil {
		return Bucket{}, fmt.Errorf("%s: building client for region %s: %w", name, region, err)
	}

	staticWebsite, err := bucketHasStaticWebsite(ctx, regionClient, name)
	if err != nil {
		return Bucket{}, fmt.Errorf("%s: checking static website configuration: %w", name, err)
	}

	purpose, err := bucketPurpose(ctx, regionClient, name)
	if err != nil {
		return Bucket{}, fmt.Errorf("%s: reading Purpose tag: %w", name, err)
	}

	return Bucket{Name: name, Region: region, StaticWebsite: staticWebsite, Purpose: purpose}, nil
}

// bucketRegion mirrors workflow.BucketRegion's LocationConstraint
// decoding -- duplicated rather than shared, since inventory cannot
// import workflow (workflow already imports inventory).
func bucketRegion(ctx context.Context, client awsclient.S3API, name string) (string, error) {
	out, err := client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: aws.String(name)})
	if err != nil {
		return "", err
	}
	switch out.LocationConstraint {
	case "":
		return "us-east-1", nil
	case "EU":
		return "eu-west-1", nil
	default:
		return string(out.LocationConstraint), nil
	}
}

func bucketHasStaticWebsite(ctx context.Context, client awsclient.S3API, name string) (bool, error) {
	_, err := client.GetBucketWebsite(ctx, &s3.GetBucketWebsiteInput{Bucket: aws.String(name)})
	if err != nil {
		if isS3ErrorCode(err, "NoSuchWebsiteConfiguration") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func bucketPurpose(ctx context.Context, client awsclient.S3API, name string) (string, error) {
	out, err := client.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: aws.String(name)})
	if err != nil {
		if isS3ErrorCode(err, "NoSuchTagSet") {
			return "", nil
		}
		return "", err
	}
	for _, tag := range out.TagSet {
		if aws.ToString(tag.Key) == "Purpose" {
			return aws.ToString(tag.Value), nil
		}
	}
	return "", nil
}

func isS3ErrorCode(err error, code string) bool {
	apiErr, ok := errors.AsType[smithy.APIError](err)
	return ok && apiErr.ErrorCode() == code
}

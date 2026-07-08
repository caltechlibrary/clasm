package inventory

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/caltechlibrary/awstools/internal/awsclient"
)

// fakeBucketsS3Client is a fake S3API for ListBuckets' tests. One
// instance plays the control-plane client (ListBuckets/GetBucketLocation);
// newBucketsClient below hands out one per-region instance per call,
// carrying that region's canned GetBucketWebsite/GetBucketTagging
// responses, so tests can confirm the region-scoped client (not the
// control-plane one) is what those two calls land on.
type fakeBucketsS3Client struct {
	awsclient.S3API

	mu sync.Mutex

	buckets        []types.Bucket
	listBucketsErr error

	locationByBucket map[string]types.BucketLocationConstraint
	locationErr      error

	region                string // "" for the control-plane client
	websiteConfigured     map[string]bool
	websiteErrByBucket    map[string]error
	purposeByBucket       map[string]string
	taggingErrByBucket    map[string]error
	getBucketWebsiteCalls []string
	getBucketTaggingCalls []string
}

func (f *fakeBucketsS3Client) ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	if f.listBucketsErr != nil {
		return nil, f.listBucketsErr
	}
	return &s3.ListBucketsOutput{Buckets: f.buckets}, nil
}

func (f *fakeBucketsS3Client) GetBucketLocation(ctx context.Context, params *s3.GetBucketLocationInput, optFns ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error) {
	if f.locationErr != nil {
		return nil, f.locationErr
	}
	return &s3.GetBucketLocationOutput{LocationConstraint: f.locationByBucket[aws.ToString(params.Bucket)]}, nil
}

func (f *fakeBucketsS3Client) GetBucketWebsite(ctx context.Context, params *s3.GetBucketWebsiteInput, optFns ...func(*s3.Options)) (*s3.GetBucketWebsiteOutput, error) {
	name := aws.ToString(params.Bucket)
	f.mu.Lock()
	f.getBucketWebsiteCalls = append(f.getBucketWebsiteCalls, name)
	f.mu.Unlock()
	if err, ok := f.websiteErrByBucket[name]; ok {
		return nil, err
	}
	if f.websiteConfigured[name] {
		return &s3.GetBucketWebsiteOutput{IndexDocument: &types.IndexDocument{Suffix: aws.String("index.html")}}, nil
	}
	return nil, awsAPIError("NoSuchWebsiteConfiguration")
}

func (f *fakeBucketsS3Client) GetBucketTagging(ctx context.Context, params *s3.GetBucketTaggingInput, optFns ...func(*s3.Options)) (*s3.GetBucketTaggingOutput, error) {
	name := aws.ToString(params.Bucket)
	f.mu.Lock()
	f.getBucketTaggingCalls = append(f.getBucketTaggingCalls, name)
	f.mu.Unlock()
	if err, ok := f.taggingErrByBucket[name]; ok {
		return nil, err
	}
	purpose, ok := f.purposeByBucket[name]
	if !ok {
		return nil, awsAPIError("NoSuchTagSet")
	}
	return &s3.GetBucketTaggingOutput{TagSet: []types.Tag{{Key: aws.String("Purpose"), Value: aws.String(purpose)}}}, nil
}

func awsAPIError(code string) error {
	return &smithy.GenericAPIError{Code: code, Message: code}
}

// newBucketsClient returns a newClient factory that hands every region
// its own *fakeBucketsS3Client, sharing controlPlane's canned website/
// tagging data by region-independent bucket name (fakes don't actually
// filter by region) but recording which region each call went through.
func newBucketsClient(perRegion map[string]*fakeBucketsS3Client) func(context.Context, string) (awsclient.S3API, error) {
	return func(_ context.Context, region string) (awsclient.S3API, error) {
		c, ok := perRegion[region]
		if !ok {
			return nil, errors.New("no client configured for region " + region)
		}
		return c, nil
	}
}

func sortBuckets(buckets []Bucket) {
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].Name < buckets[j].Name })
}

func TestListBuckets_AggregatesAndEnriches(t *testing.T) {
	controlPlane := &fakeBucketsS3Client{
		buckets: []types.Bucket{{Name: aws.String("website-bucket")}, {Name: aws.String("backup-bucket")}},
		locationByBucket: map[string]types.BucketLocationConstraint{
			"website-bucket": "us-west-2",
			"backup-bucket":  "", // empty LocationConstraint means us-east-1
		},
	}
	usWest2 := &fakeBucketsS3Client{
		websiteConfigured: map[string]bool{"website-bucket": true},
		purposeByBucket:   map[string]string{"website-bucket": "website"},
	}
	usEast1 := &fakeBucketsS3Client{
		websiteConfigured: map[string]bool{},
		purposeByBucket:   map[string]string{"backup-bucket": "backup"},
	}

	got, err := ListBuckets(context.Background(), controlPlane, newBucketsClient(map[string]*fakeBucketsS3Client{
		"us-west-2": usWest2,
		"us-east-1": usEast1,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sortBuckets(got)

	want := []Bucket{
		{Name: "backup-bucket", Region: "us-east-1", StaticWebsite: false, Purpose: "backup"},
		{Name: "website-bucket", Region: "us-west-2", StaticWebsite: true, Purpose: "website"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d buckets, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	if len(usWest2.getBucketWebsiteCalls) != 1 || usWest2.getBucketWebsiteCalls[0] != "website-bucket" {
		t.Errorf("expected GetBucketWebsite for website-bucket to go through the us-west-2 client, got calls: %v", usWest2.getBucketWebsiteCalls)
	}
	if len(usEast1.getBucketTaggingCalls) != 1 || usEast1.getBucketTaggingCalls[0] != "backup-bucket" {
		t.Errorf("expected GetBucketTagging for backup-bucket to go through the us-east-1 client, got calls: %v", usEast1.getBucketTaggingCalls)
	}
}

func TestListBuckets_NoSuchWebsiteConfigurationIsNotAnError(t *testing.T) {
	controlPlane := &fakeBucketsS3Client{
		buckets:          []types.Bucket{{Name: aws.String("plain-bucket")}},
		locationByBucket: map[string]types.BucketLocationConstraint{"plain-bucket": "us-east-1"},
	}
	regionClient := &fakeBucketsS3Client{purposeByBucket: map[string]string{}}

	got, err := ListBuckets(context.Background(), controlPlane, newBucketsClient(map[string]*fakeBucketsS3Client{"us-east-1": regionClient}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].StaticWebsite {
		t.Fatalf("got %+v, want one bucket with StaticWebsite: false", got)
	}
}

func TestListBuckets_MissingPurposeTagIsNotAnError(t *testing.T) {
	controlPlane := &fakeBucketsS3Client{
		buckets:          []types.Bucket{{Name: aws.String("untagged-bucket")}},
		locationByBucket: map[string]types.BucketLocationConstraint{"untagged-bucket": "us-east-1"},
	}
	regionClient := &fakeBucketsS3Client{}

	got, err := ListBuckets(context.Background(), controlPlane, newBucketsClient(map[string]*fakeBucketsS3Client{"us-east-1": regionClient}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Purpose != "" {
		t.Fatalf("got %+v, want one bucket with Purpose: \"\"", got)
	}
}

func TestListBuckets_EmptyAccount(t *testing.T) {
	controlPlane := &fakeBucketsS3Client{}
	got, err := ListBuckets(context.Background(), controlPlane, newBucketsClient(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d buckets, want 0", len(got))
	}
}

func TestListBuckets_PropagatesListBucketsError(t *testing.T) {
	controlPlane := &fakeBucketsS3Client{listBucketsErr: errors.New("boom")}
	_, err := ListBuckets(context.Background(), controlPlane, newBucketsClient(nil))
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestListBuckets_PropagatesGetBucketLocationError(t *testing.T) {
	controlPlane := &fakeBucketsS3Client{
		buckets:     []types.Bucket{{Name: aws.String("b")}},
		locationErr: errors.New("boom"),
	}
	_, err := ListBuckets(context.Background(), controlPlane, newBucketsClient(nil))
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestListBuckets_PropagatesNewClientError(t *testing.T) {
	controlPlane := &fakeBucketsS3Client{
		buckets:          []types.Bucket{{Name: aws.String("b")}},
		locationByBucket: map[string]types.BucketLocationConstraint{"b": "us-east-1"},
	}
	_, err := ListBuckets(context.Background(), controlPlane, newBucketsClient(nil))
	if err == nil {
		t.Fatal("expected an error")
	}
}

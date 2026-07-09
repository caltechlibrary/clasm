package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// fakeS3Client embeds the (nil) S3API interface so it satisfies
// awsclient.S3API without stubbing every method.
type fakeS3Client struct {
	awsclient.S3API

	// objects maps key -> actual size in the fake bucket; a missing key
	// simulates HeadObject returning a not-found error.
	objects         map[string]int64
	headErr         error // if set, every HeadObject call fails with this error
	headObjectCalls int

	headBucketErr   error // if set, HeadBucket fails with this error
	headBucketCalls int

	bucketLocation       types.BucketLocationConstraint // GetBucketLocation's canned response
	getBucketLocationErr error

	// Phase 20 (S3 domain) additions -- see bucket_fakes_test.go for the
	// methods using these fields. Kept on this same fake rather than a
	// second type, per this project's practice of extending/reusing one
	// fake S3API per package instead of duplicating it.
	createBucketErr   error
	createBucketCalls []s3.CreateBucketInput

	putPublicAccessBlockErr   error
	putPublicAccessBlockCalls []s3.PutPublicAccessBlockInput

	putBucketTaggingErr   error
	putBucketTaggingCalls []s3.PutBucketTaggingInput

	getBucketTaggingErr error
	tagSet              []types.Tag // GetBucketTagging's canned response

	getBucketWebsiteErr error
	websiteIndexSuffix  string // "" simulates NoSuchWebsiteConfiguration
	websiteErrorKey     string

	putBucketWebsiteErr   error
	putBucketWebsiteCalls []s3.PutBucketWebsiteInput

	listBucketsErr error
	buckets        []types.Bucket

	putObjectErr   error
	putObjectCalls []s3.PutObjectInput

	deleteObjectErr   error
	deleteObjectCalls []s3.DeleteObjectInput

	listObjectsV2Err    error
	allObjects          []types.Object // full object list; paginated by listObjectsPageSize
	listObjectsPageSize int            // 0 = return everything in one page
	listObjectsV2Calls  []s3.ListObjectsV2Input

	getBucketLifecycleErr error
	lifecycleRules        []types.LifecycleRule

	putBucketLifecycleErr   error
	putBucketLifecycleCalls []s3.PutBucketLifecycleConfigurationInput

	deleteBucketLifecycleErr   error
	deleteBucketLifecycleCalls []s3.DeleteBucketLifecycleInput

	deleteBucketErr   error
	deleteBucketCalls []s3.DeleteBucketInput

	// Phase 20.1 (file manager) additions -- see bucket_fakes_test.go.
	getObjectErr     error
	getObjectContent map[string][]byte // key -> body GetObject returns
	getObjectCalls   []s3.GetObjectInput
}

func (f *fakeS3Client) HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	f.headObjectCalls++
	if f.headErr != nil {
		return nil, f.headErr
	}
	size, ok := f.objects[aws.ToString(params.Key)]
	if !ok {
		return nil, errors.New("NotFound: key does not exist")
	}
	return &s3.HeadObjectOutput{ContentLength: aws.Int64(size)}, nil
}

func (f *fakeS3Client) HeadBucket(ctx context.Context, params *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	f.headBucketCalls++
	if f.headBucketErr != nil {
		return nil, f.headBucketErr
	}
	return &s3.HeadBucketOutput{}, nil
}

func (f *fakeS3Client) GetBucketLocation(ctx context.Context, params *s3.GetBucketLocationInput, optFns ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error) {
	if f.getBucketLocationErr != nil {
		return nil, f.getBucketLocationErr
	}
	return &s3.GetBucketLocationOutput{LocationConstraint: f.bucketLocation}, nil
}

func TestBucketRegion_MapsEmptyToUSEast1(t *testing.T) {
	fake := &fakeS3Client{bucketLocation: ""}
	got, err := BucketRegion(context.Background(), fake, "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "us-east-1" {
		t.Errorf("got %q, want %q", got, "us-east-1")
	}
}

func TestBucketRegion_MapsEUToEUWest1(t *testing.T) {
	fake := &fakeS3Client{bucketLocation: "EU"}
	got, err := BucketRegion(context.Background(), fake, "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "eu-west-1" {
		t.Errorf("got %q, want %q", got, "eu-west-1")
	}
}

func TestBucketRegion_ReturnsRegionAsIs(t *testing.T) {
	fake := &fakeS3Client{bucketLocation: "us-west-2"}
	got, err := BucketRegion(context.Background(), fake, "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "us-west-2" {
		t.Errorf("got %q, want %q", got, "us-west-2")
	}
}

func TestBucketRegion_ReturnsErrorOnFailure(t *testing.T) {
	fake := &fakeS3Client{getBucketLocationErr: errors.New("NoSuchBucket")}
	_, err := BucketRegion(context.Background(), fake, "my-bucket")
	if err == nil {
		t.Fatal("expected an error when GetBucketLocation fails")
	}
	if !strings.Contains(err.Error(), "my-bucket") {
		t.Errorf("expected the bucket name in the error, got: %v", err)
	}
}

func TestCheckS3BucketAccess_Success(t *testing.T) {
	fake := &fakeS3Client{}
	if err := CheckS3BucketAccess(context.Background(), fake, "my-bucket"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.headBucketCalls != 1 {
		t.Errorf("headBucketCalls = %d, want 1", fake.headBucketCalls)
	}
}

func TestCheckS3BucketAccess_ReturnsErrorOnFailure(t *testing.T) {
	fake := &fakeS3Client{headBucketErr: errors.New("Forbidden")}
	err := CheckS3BucketAccess(context.Background(), fake, "my-bucket")
	if err == nil {
		t.Fatal("expected an error when the bucket is inaccessible")
	}
	if !strings.Contains(err.Error(), "my-bucket") {
		t.Errorf("expected the bucket name in the error, got: %v", err)
	}
}

func TestVerifyUploads_VerifiesMatchingSize(t *testing.T) {
	fake := &fakeS3Client{objects: map[string]int64{"foo.sql.gz": 1024}}
	uploads := []UploadResult{{Key: "foo.sql.gz", SizeBytes: 1024, OK: true}}

	got := VerifyUploads(context.Background(), fake, "my-bucket", uploads)
	if len(got) != 1 || !got[0].Verified {
		t.Errorf("got %+v, want Verified=true", got)
	}
}

func TestVerifyUploads_SizeMismatchFailsVerification(t *testing.T) {
	fake := &fakeS3Client{objects: map[string]int64{"foo.sql.gz": 512}} // wrong size
	uploads := []UploadResult{{Key: "foo.sql.gz", SizeBytes: 1024, OK: true}}

	got := VerifyUploads(context.Background(), fake, "my-bucket", uploads)
	if len(got) != 1 || got[0].Verified {
		t.Errorf("got %+v, want Verified=false on a size mismatch", got)
	}
}

func TestVerifyUploads_MissingObjectFailsVerification(t *testing.T) {
	fake := &fakeS3Client{objects: map[string]int64{}}
	uploads := []UploadResult{{Key: "foo.sql.gz", SizeBytes: 1024, OK: true}}

	got := VerifyUploads(context.Background(), fake, "my-bucket", uploads)
	if len(got) != 1 || got[0].Verified {
		t.Errorf("got %+v, want Verified=false for a missing object", got)
	}
}

func TestVerifyUploads_SkipsHeadObjectForFailedUpload(t *testing.T) {
	fake := &fakeS3Client{headErr: errors.New("should not be called")}
	uploads := []UploadResult{{Key: "foo.sql.gz", SizeBytes: 0, OK: false}}

	got := VerifyUploads(context.Background(), fake, "my-bucket", uploads)
	if len(got) != 1 || got[0].Verified {
		t.Errorf("got %+v, want Verified=false", got)
	}
	if fake.headObjectCalls != 0 {
		t.Errorf("headObjectCalls = %d, want 0 (a failed upload shouldn't be checked)", fake.headObjectCalls)
	}
}

package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/caltechlibrary/awstools/internal/awsclient"
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

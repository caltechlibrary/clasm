package s3diff

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

func TestWithCallTimeout_BoundsToDefaultCallTimeout(t *testing.T) {
	before := time.Now()
	ctx, cancel := WithCallTimeout(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("WithCallTimeout's context has no deadline")
	}
	got := deadline.Sub(before)
	if got < DefaultCallTimeout-time.Second || got > DefaultCallTimeout+time.Second {
		t.Errorf("deadline ~%v from now, want ~%v", got, DefaultCallTimeout)
	}
}

func TestWithTransferTimeout_BoundsToTransferCallTimeout(t *testing.T) {
	before := time.Now()
	ctx, cancel := WithTransferTimeout(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("WithTransferTimeout's context has no deadline")
	}
	got := deadline.Sub(before)
	if got < TransferCallTimeout-time.Second || got > TransferCallTimeout+time.Second {
		t.Errorf("deadline ~%v from now, want ~%v", got, TransferCallTimeout)
	}
}

// stalledS3 simulates a connection that never responds -- every method
// blocks until its context is done, then returns ctx.Err(), the same
// way a real stalled net/http round trip behaves once its request
// context expires.
type stalledS3 struct{ awsclient.S3API }

func (stalledS3) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (stalledS3) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestListAllBucketObjects_RecoversFromAStalledCall is a regression
// test: without a per-call timeout, a stalled connection (one that
// never responds and never errors on its own) hangs the calling
// goroutine forever -- this is what made the file manager "appear
// hung" after an upload was investigated. Shrinks DefaultCallTimeout so
// the test doesn't have to wait out the real 30s default.
func TestListAllBucketObjects_RecoversFromAStalledCall(t *testing.T) {
	orig := DefaultCallTimeout
	DefaultCallTimeout = 20 * time.Millisecond
	defer func() { DefaultCallTimeout = orig }()

	_, err := ListAllBucketObjects(context.Background(), stalledS3{}, "bucket")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ListAllBucketObjects against a stalled connection = %v, want context.DeadlineExceeded (recovered via timeout, not hung)", err)
	}
}

// TestUploadFile_RecoversFromAStalledCall mirrors the above for the
// longer transfer timeout.
func TestUploadFile_RecoversFromAStalledCall(t *testing.T) {
	orig := TransferCallTimeout
	TransferCallTimeout = 20 * time.Millisecond
	defer func() { TransferCallTimeout = orig }()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := UploadFile(context.Background(), stalledS3{}, "bucket", "a.txt", path)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("UploadFile against a stalled connection = %v, want context.DeadlineExceeded (recovered via timeout, not hung)", err)
	}
}

func TestCompute(t *testing.T) {
	local := map[string]int64{"a.txt": 5, "b.txt": 10}
	remote := map[string]int64{"a.txt": 5, "c.txt": 3}

	d := Compute(local, remote)
	if !reflect.DeepEqual(d.Upload, []string{"b.txt"}) {
		t.Errorf("Upload = %v, want [b.txt] (a.txt unchanged)", d.Upload)
	}
	if !reflect.DeepEqual(d.Delete, []string{"c.txt"}) {
		t.Errorf("Delete = %v, want [c.txt]", d.Delete)
	}
}

func TestCompute_SizeMismatchIsAnUploadCandidate(t *testing.T) {
	local := map[string]int64{"a.txt": 99}
	remote := map[string]int64{"a.txt": 5}

	d := Compute(local, remote)
	if !reflect.DeepEqual(d.Upload, []string{"a.txt"}) {
		t.Errorf("Upload = %v, want [a.txt] (size differs)", d.Upload)
	}
}

func TestValidateLocalDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := ValidateLocalDirectory(dir); err != nil {
		t.Errorf("ValidateLocalDirectory(%q) = %v, want nil", dir, err)
	}
	if err := ValidateLocalDirectory(filepath.Join(dir, "missing")); err == nil {
		t.Error("want error for a missing path")
	}
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateLocalDirectory(file); err == nil {
		t.Error("want error for a plain file")
	}
}

func TestWalkLocalTree(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := WalkLocalTree(dir)
	if err != nil {
		t.Fatalf("WalkLocalTree: %v", err)
	}
	want := map[string]int64{"a.txt": 5, "sub/b.txt": 10}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("WalkLocalTree = %v, want %v", got, want)
	}
}

func TestContentTypeFor(t *testing.T) {
	if got := ContentTypeFor("a.txt"); got != "text/plain; charset=utf-8" {
		t.Errorf("ContentTypeFor(a.txt) = %q", got)
	}
	if got := ContentTypeFor("a.unknownext"); got != "application/octet-stream" {
		t.Errorf("ContentTypeFor(a.unknownext) = %q, want application/octet-stream", got)
	}
}

// fakeS3 is a minimal S3API double covering only ListObjectsV2/PutObject,
// paginated in pages of pageSize -- enough to exercise
// ListAllBucketObjects' ContinuationToken-following loop and UploadFile.
type fakeS3 struct {
	awsclient.S3API
	allObjects     []types.Object
	pageSize       int
	listCalls      int
	putObjectCalls []s3.PutObjectInput
}

func (f *fakeS3) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	f.listCalls++
	pageSize := f.pageSize
	if pageSize <= 0 {
		pageSize = len(f.allObjects)
	}
	start := 0
	if tok := aws.ToString(params.ContinuationToken); tok != "" {
		start, _ = strconv.Atoi(tok)
	}
	end := min(start+pageSize, len(f.allObjects))
	out := &s3.ListObjectsV2Output{Contents: f.allObjects[start:end]}
	if end < len(f.allObjects) {
		out.IsTruncated = aws.Bool(true)
		out.NextContinuationToken = aws.String(strconv.Itoa(end))
	}
	return out, nil
}

func (f *fakeS3) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.putObjectCalls = append(f.putObjectCalls, *params)
	return &s3.PutObjectOutput{}, nil
}

func TestListAllBucketObjects_FollowsPagination(t *testing.T) {
	fake := &fakeS3{
		allObjects: []types.Object{
			{Key: aws.String("a"), Size: aws.Int64(1)},
			{Key: aws.String("b"), Size: aws.Int64(2)},
			{Key: aws.String("c"), Size: aws.Int64(3)},
		},
		pageSize: 1,
	}
	got, err := ListAllBucketObjects(context.Background(), fake, "bucket")
	if err != nil {
		t.Fatalf("ListAllBucketObjects: %v", err)
	}
	want := map[string]int64{"a": 1, "b": 2, "c": 3}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListAllBucketObjects = %v, want %v", got, want)
	}
	if fake.listCalls != 3 {
		t.Errorf("listCalls = %d, want 3 (one per page)", fake.listCalls)
	}
}

func TestUploadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &fakeS3{}
	if err := UploadFile(context.Background(), fake, "bucket", "a.txt", path); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if len(fake.putObjectCalls) != 1 || aws.ToString(fake.putObjectCalls[0].Key) != "a.txt" {
		t.Fatalf("putObjectCalls = %+v, want one call for a.txt", fake.putObjectCalls)
	}
}

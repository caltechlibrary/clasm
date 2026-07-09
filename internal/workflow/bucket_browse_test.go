package workflow

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func TestListBucketObjectsWithPrefix_FollowsListObjectsV2Pagination(t *testing.T) {
	fake := &fakeS3Client{
		allObjects: []types.Object{
			{Key: aws.String("a"), Size: aws.Int64(1)},
			{Key: aws.String("b"), Size: aws.Int64(1)},
			{Key: aws.String("c"), Size: aws.Int64(1)},
		},
		listObjectsPageSize: 1,
	}

	objects, err := listBucketObjectsWithPrefix(context.Background(), fake, "my-bucket", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objects) != 3 {
		t.Errorf("got %d objects, want 3", len(objects))
	}
	if len(fake.listObjectsV2Calls) != 3 {
		t.Errorf("listObjectsV2Calls = %d, want 3 (one per page)", len(fake.listObjectsV2Calls))
	}
}

func TestListBucketObjectsWithPrefix_FiltersByPrefix(t *testing.T) {
	fake := &fakeS3Client{allObjects: []types.Object{
		{Key: aws.String("a/1.txt"), Size: aws.Int64(1)},
		{Key: aws.String("b/1.txt"), Size: aws.Int64(1)},
	}}

	objects, err := listBucketObjectsWithPrefix(context.Background(), fake, "my-bucket", "a/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.listObjectsV2Calls) == 0 || aws.ToString(fake.listObjectsV2Calls[0].Prefix) != "a/" {
		t.Fatalf("expected ListObjectsV2 to be called with Prefix=a/, got calls: %+v", fake.listObjectsV2Calls)
	}
	if len(objects) != 1 || aws.ToString(objects[0].Key) != "a/1.txt" {
		t.Fatalf("objects = %+v, want exactly a/1.txt", objects)
	}
}

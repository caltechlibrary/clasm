package workflow

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// listBucketObjectsWithPrefix lists every object under prefix in bucket
// via s3:ListObjectsV2, following ContinuationToken to page through the
// full result. Feature 21's original single-object browse/metadata/
// delete wizard (that used to live in this file) is retired -- the
// interactive file manager (internal/filemanager, DESIGN.md 21.2-21.8)
// now covers that case -- but Delete Bucket (bucket_delete.go) still
// needs this helper to confirm a bucket is empty before deleting it.
func listBucketObjectsWithPrefix(ctx context.Context, client awsclient.S3API, bucket, prefix string) ([]types.Object, error) {
	var all []types.Object
	var token *string
	for {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(prefix), ContinuationToken: token})
		if err != nil {
			return nil, err
		}
		all = append(all, out.Contents...)
		if !aws.ToBool(out.IsTruncated) || out.NextContinuationToken == nil {
			break
		}
		token = out.NextContinuationToken
	}
	return all, nil
}

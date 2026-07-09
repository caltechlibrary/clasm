package workflow

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// VerifiedFile is one file's outcome from the tool's own independent
// s3:HeadObject check -- this, not the instance's self-reported OK, is
// what authorizes the delete phase (see DESIGN.md, Feature 11, and
// Security Considerations: "the tool's independent s3:HeadObject
// verification is the actual authorization for the delete step, not a
// redundant nice-to-have").
type VerifiedFile struct {
	Key       string
	SizeBytes int64
	Verified  bool
}

// VerifyUploads calls s3:HeadObject, using the operator's own
// credentials (not the instance's), on every upload the instance
// reported OK, confirming each object exists with the expected size. A
// failed upload is recorded as unverified without calling HeadObject --
// there is nothing to check.
func VerifyUploads(ctx context.Context, client awsclient.S3API, bucket string, uploads []UploadResult) []VerifiedFile {
	verified := make([]VerifiedFile, 0, len(uploads))
	for _, u := range uploads {
		if !u.OK {
			verified = append(verified, VerifiedFile{Key: u.Key, SizeBytes: u.SizeBytes, Verified: false})
			continue
		}
		headCtx, cancel := withCallTimeout(ctx)
		out, err := client.HeadObject(headCtx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(u.Key)})
		cancel()
		ok := err == nil && out.ContentLength != nil && *out.ContentLength == u.SizeBytes
		verified = append(verified, VerifiedFile{Key: u.Key, SizeBytes: u.SizeBytes, Verified: ok})
	}
	return verified
}

// BucketRegion discovers which AWS region a bucket actually lives in,
// via s3:GetBucketLocation -- callable from a client scoped to any
// region, unlike HeadBucket/HeadObject, which 301 (MovedPermanently)
// with no useful detail when the client's region doesn't match the
// bucket's (see DECISIONS.md, "Resolve a bucket's actual region before
// Backup Archive & Trim's access check"). An empty LocationConstraint
// means us-east-1; "EU" (a legacy value) means eu-west-1; anything else
// is returned as-is.
func BucketRegion(ctx context.Context, client awsclient.S3API, bucket string) (string, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: aws.String(bucket)})
	if err != nil {
		return "", fmt.Errorf("determining region for S3 bucket %q: %w", bucket, err)
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

// CheckS3BucketAccess calls s3:HeadBucket, using the operator's own
// credentials, right after the bucket name is entered -- a preflight
// check so a missing bucket or an access problem aborts immediately
// with a clear reason, rather than only surfacing once every upload has
// already failed independent verification much later in the run (see
// DECISIONS.md, "Preflight check: S3 bucket access before Backup
// Archive & Trim's dry-run list").
func CheckS3BucketAccess(ctx context.Context, client awsclient.S3API, bucket string) error {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	if _, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)}); err != nil {
		return fmt.Errorf("no access to S3 bucket %q -- check the bucket name and that your AWS credentials have s3:ListBucket on it: %w", bucket, err)
	}
	return nil
}

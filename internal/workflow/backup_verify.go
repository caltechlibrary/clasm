package workflow

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/caltechlibrary/awstools/internal/awsclient"
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
		out, err := client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(u.Key)})
		ok := err == nil && out.ContentLength != nil && *out.ContentLength == u.SizeBytes
		verified = append(verified, VerifiedFile{Key: u.Key, SizeBytes: u.SizeBytes, Verified: ok})
	}
	return verified
}

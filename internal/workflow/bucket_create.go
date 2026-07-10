package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// bucketPurposes are the three easy-to-create bucket "types" this tool
// supports (DESIGN.md, Feature 21.1): a Create Bucket wizard tags a
// bucket's Purpose with one of these, and Manage Bucket Lifecycle
// Policies later reads that tag back to decide which UX to show.
var bucketPurposes = []string{"website", "backup", "internal"}

func promptS3Region(t *termlib.Terminal, regions []string, input io.Reader, output io.Writer) (string, error) {
	return pickString(t, "Select a region", "(q to cancel)", regions, input, output)
}

// validateBucketName checks a bucket name against S3's naming rules
// locally (DESIGN.md, Feature 18), rejecting with a clear message before
// ever calling AWS: 3-63 characters, lowercase letters/digits/hyphens/
// dots only, no leading or trailing hyphen.
func validateBucketName(name string) error {
	if len(name) < 3 || len(name) > 63 {
		return errors.New("bucket name must be 3-63 characters long")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '.':
		default:
			return fmt.Errorf("bucket name must contain only lowercase letters, digits, hyphens, and dots (found %q)", r)
		}
	}
	if name[0] == '-' || name[len(name)-1] == '-' {
		return errors.New("bucket name must not start or end with a hyphen")
	}
	return nil
}

// CreateBucket runs the S3 domain's "Create Bucket" workflow (DESIGN.md,
// Feature 18): prompt bucket name (validated locally), region, and
// purpose, then s3:CreateBucket, s3:PutPublicAccessBlock with all four
// block settings on (Security Consideration #10 -- never a public bucket
// by omission), and s3:PutBucketTagging recording the chosen Purpose.
// newS3Client builds a client scoped to the chosen region -- unlike
// Backup Archive & Trim, there's no existing bucket to discover a region
// from via BucketRegion, since this bucket doesn't exist yet.
func CreateBucket(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), regions []string) error {
	return createBucket(ctx, t, le, newS3Client, regions, nil, nil)
}

// createBucket is CreateBucket's testable core: menuInput/menuOutput are
// nil in production (the region and purpose huh.Selects run
// interactively on the real terminal, DESIGN.md's full conversion punch
// list) and are supplied by tests to drive them through their
// accessible-mode pipe path instead, separate from le, which still feeds
// the bucket-name prompt. Both huh.Selects share one reader/writer pair,
// read in sequence one line at a time.
func createBucket(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), regions []string, menuInput io.Reader, menuOutput io.Writer) error {
	name, err := ui.Prompt(t, le, "Bucket name", ui.WithValidator(validateBucketName))
	if err != nil {
		return err
	}

	region, err := promptS3Region(t, regions, menuInput, menuOutput)
	if err != nil {
		return cancelledIsNil(t, err)
	}

	purpose, err := pickString(t, "Select the bucket's purpose", "(q to cancel)", bucketPurposes, menuInput, menuOutput)
	if err != nil {
		return cancelledIsNil(t, err)
	}

	client, err := newS3Client(ctx, region)
	if err != nil {
		return err
	}

	createInput := &s3.CreateBucketInput{Bucket: aws.String(name)}
	// us-east-1 is S3's implicit default region -- CreateBucket rejects an
	// explicit LocationConstraint of "us-east-1" with InvalidLocationConstraint,
	// so it must be omitted there and only set for every other region.
	if region != "us-east-1" {
		createInput.CreateBucketConfiguration = &types.CreateBucketConfiguration{
			LocationConstraint: types.BucketLocationConstraint(region),
		}
	}
	if _, err := client.CreateBucket(ctx, createInput); err != nil {
		return fmt.Errorf("creating bucket %s: %w", name, err)
	}

	if _, err := client.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
		Bucket: aws.String(name),
		PublicAccessBlockConfiguration: &types.PublicAccessBlockConfiguration{
			BlockPublicAcls:       aws.Bool(true),
			BlockPublicPolicy:     aws.Bool(true),
			IgnorePublicAcls:      aws.Bool(true),
			RestrictPublicBuckets: aws.Bool(true),
		},
	}); err != nil {
		return fmt.Errorf("blocking public access on bucket %s: %w", name, err)
	}

	if _, err := client.PutBucketTagging(ctx, &s3.PutBucketTaggingInput{
		Bucket:  aws.String(name),
		Tagging: &types.Tagging{TagSet: []types.Tag{{Key: aws.String("Purpose"), Value: aws.String(purpose)}}},
	}); err != nil {
		return fmt.Errorf("tagging bucket %s: %w", name, err)
	}

	t.Printf("Created bucket %s in %s (purpose: %s), public access blocked.\n", name, region, purpose)
	t.Refresh()
	return nil
}

package workflow

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/awstools/internal/awsclient"
)

// amiNamePattern matches CreateImageInput's Name constraint: 3-128
// alphanumeric characters, parentheses, square brackets, spaces,
// periods, slashes, dashes, single quotes, at-signs, or underscores.
var amiNamePattern = regexp.MustCompile(`^[A-Za-z0-9()\[\]. /'@_-]{3,128}$`)

func validateAMIName(s string) error {
	if !amiNamePattern.MatchString(s) {
		return fmt.Errorf("must be 3-128 characters: letters, numbers, and ()[]. /'@_- only")
	}
	return nil
}

// DefaultAMIPollInterval is the production poll interval for
// WaitForAMIAvailable's unbounded poll.
const DefaultAMIPollInterval = 15 * time.Second

// CreateAMIParams is the resolved parameter set for creating an AMI from
// an instance.
type CreateAMIParams struct {
	InstanceID  string
	Name        string
	Description string
	NoReboot    bool
	Tags        map[string]string
}

// defaultAMIName suggests "<instance-name-or-id>-copy-<date>" (DESIGN.md,
// Feature 8) -- the user may override it. now is passed in rather than
// read from time.Now() internally so callers control the date shown.
func defaultAMIName(instanceNameOrID string, now time.Time) string {
	return instanceNameOrID + "-copy-" + now.Format("2006-01-02")
}

// CreateAMI calls ec2:CreateImage, building TagSpecifications as a typed
// SDK struct rather than a hand-built string -- the exact bug class
// (malformed AWS CLI tag-specification shorthand) that broke the Bash
// version in real use and motivated retargeting this project to Go (see
// DECISIONS.md, "Retarget implementation from Bash to Go").
func CreateAMI(ctx context.Context, client awsclient.EC2API, params CreateAMIParams) (string, error) {
	input := &ec2.CreateImageInput{
		InstanceId:        aws.String(params.InstanceID),
		Name:              aws.String(params.Name),
		NoReboot:          aws.Bool(params.NoReboot),
		TagSpecifications: []types.TagSpecification{buildTagSpecification(types.ResourceTypeImage, params.Tags)},
	}
	if params.Description != "" {
		input.Description = aws.String(params.Description)
	}

	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.CreateImage(ctx, input)
	if err != nil {
		return "", err
	}
	return aws.ToString(out.ImageId), nil
}

// WaitForAMIAvailable polls ec2:DescribeImages until imageID reaches
// available or failed. Deliberately unbounded (no internal timeout) --
// large Invenio RDM volumes can take 20-60+ minutes, and Phase 4's fixed
// 600-second timeout for this same operation was a correctness bug in
// the Bash version (see DECISIONS.md, 2026-06-30 "AMI-from-instance:
// fold ami_copy.bash capabilities into Phase 5"). The caller's ctx is
// still honored for cancellation (e.g. Ctrl+C).
func WaitForAMIAvailable(ctx context.Context, client awsclient.EC2API, imageID string, pollInterval time.Duration) (types.ImageState, error) {
	input := &ec2.DescribeImagesInput{ImageIds: []string{imageID}}
	for {
		out, err := client.DescribeImages(ctx, input)
		if err != nil {
			return "", err
		}
		if len(out.Images) > 0 {
			state := out.Images[0].State
			if state == types.ImageStateAvailable || state == types.ImageStateFailed {
				return state, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

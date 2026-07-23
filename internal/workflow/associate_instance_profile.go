package workflow

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// resolveCurrentInstanceProfileAssociation finds instanceID's current
// active IAM instance profile association (state "associating" or
// "associated"), if any -- ec2:DescribeIamInstanceProfileAssociations
// filtered by instance-id, so associateOrReplaceInstanceProfile can
// decide AssociateIamInstanceProfile vs ReplaceIamInstanceProfileAssociation
// (AWS allows only one active association per instance; calling
// Associate again on an already-associated instance errors).
func resolveCurrentInstanceProfileAssociation(ctx context.Context, client awsclient.EC2API, instanceID string) (associationID string, found bool, err error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DescribeIamInstanceProfileAssociations(ctx, &ec2.DescribeIamInstanceProfileAssociationsInput{
		Filters: []types.Filter{
			{Name: aws.String("instance-id"), Values: []string{instanceID}},
			{Name: aws.String("state"), Values: []string{"associating", "associated"}},
		},
	})
	if err != nil {
		return "", false, err
	}
	if len(out.IamInstanceProfileAssociations) == 0 {
		return "", false, nil
	}
	return aws.ToString(out.IamInstanceProfileAssociations[0].AssociationId), true, nil
}

// AssociateOrReplaceInstanceProfile runs the "Associate/replace IAM
// instance profile" workflow (DESIGN.md, "SSM-Capable Instance Profile
// Enforcement + Retrofit", Part 3): pick a running instance, pick/create
// an instance profile via the same promptIAMInstanceProfileOrCreate flow
// used at launch (SSM-capability shown, not gated -- general-purpose,
// per the 2026-07-22 scoping decision: a running instance might need a
// profile for something other than SSM, e.g. S3 access), then associate
// it if the instance has no current association, or replace the
// existing one. Returns nil (not an error) on cancellation or when
// there are no instances to pick from.
func AssociateOrReplaceInstanceProfile(ctx context.Context, w io.Writer, ec2Clients map[string]awsclient.EC2API, iamClient awsclient.IAMAPI, instances []inventory.Instance) error {
	if len(instances) == 0 {
		fmt.Fprintln(w, "No instances found.")
		return nil
	}

	inst, err := pickInstance(ctx, "Select an instance to associate/replace its IAM instance profile", "", instances)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return associateOrReplaceInstanceProfile(ctx, w, ec2Clients, iamClient, inst, nil, nil)
}

// associateOrReplaceInstanceProfile is AssociateOrReplaceInstanceProfile's
// testable core, once an instance is resolved -- instance selection runs
// a real bubbletea Program (tui.RunPicker) that can't be pipe-tested,
// same limitation as every other Picker-tier conversion. input/output
// are nil in production and supplied by tests to drive
// promptIAMInstanceProfileOrCreate's free-text fallback path.
func associateOrReplaceInstanceProfile(ctx context.Context, w io.Writer, ec2Clients map[string]awsclient.EC2API, iamClient awsclient.IAMAPI, inst inventory.Instance, input io.Reader, output io.Writer) error {
	ec2Client, err := resolveEC2(ec2Clients, inst.Region)
	if err != nil {
		return err
	}

	associationID, found, err := resolveCurrentInstanceProfileAssociation(ctx, ec2Client, inst.InstanceID)
	if err != nil {
		return err
	}

	profileName, err := promptIAMInstanceProfileOrCreate(ctx, w, iamClient, input, output)
	if err != nil {
		return err
	}

	callCtx, cancel := withCallTimeout(ctx)
	defer cancel()

	if found {
		if _, err := ec2Client.ReplaceIamInstanceProfileAssociation(callCtx, &ec2.ReplaceIamInstanceProfileAssociationInput{
			AssociationId:      aws.String(associationID),
			IamInstanceProfile: &types.IamInstanceProfileSpecification{Name: aws.String(profileName)},
		}); err != nil {
			return fmt.Errorf("replacing instance profile association for %s: %w", inst.InstanceID, err)
		}
		fmt.Fprintf(w, "Replaced the instance profile on %s (%s) with %q.\n", inst.InstanceID, inst.Name, profileName)
		return nil
	}

	if _, err := ec2Client.AssociateIamInstanceProfile(callCtx, &ec2.AssociateIamInstanceProfileInput{
		InstanceId:         aws.String(inst.InstanceID),
		IamInstanceProfile: &types.IamInstanceProfileSpecification{Name: aws.String(profileName)},
	}); err != nil {
		return fmt.Errorf("associating instance profile with %s: %w", inst.InstanceID, err)
	}
	fmt.Fprintf(w, "Associated instance profile %q with %s (%s).\n", profileName, inst.InstanceID, inst.Name)
	return nil
}

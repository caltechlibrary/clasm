package workflow

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// ssmManagedInstanceCorePolicyArn is AWS's own managed policy granting
// the permissions the SSM agent needs to register and be managed
// (DESIGN.md, "SSM-Capable Instance Profile Enforcement + Retrofit").
const ssmManagedInstanceCorePolicyArn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"

// roleHasSSMPermissions reports whether roleName has
// AmazonSSMManagedInstanceCore attached. Deliberately does not inspect
// inline policies for functionally-equivalent custom permissions --
// see DESIGN.md's "known, deliberate limitation" note: interpreting
// arbitrary IAM policy JSON to decide whether it grants "enough" SSM
// access is exactly the kind of guessing this project's "fail loud,
// don't guess" convention (Phase 20.31's growRootFilesystem) argues
// against.
func roleHasSSMPermissions(ctx context.Context, client awsclient.IAMAPI, roleName string) (bool, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{RoleName: aws.String(roleName)})
	if err != nil {
		return false, err
	}
	for _, p := range out.AttachedPolicies {
		if aws.ToString(p.PolicyArn) == ssmManagedInstanceCorePolicyArn {
			return true, nil
		}
	}
	return false, nil
}

// instanceProfileIsSSMCapable reports whether any role attached to
// profile has SSM permissions -- an instance profile conventionally
// holds exactly one role, but InstanceProfileInfo.Roles is a slice, so
// this checks all of them and treats the profile as capable if any one
// grants SSM access.
func instanceProfileIsSSMCapable(ctx context.Context, client awsclient.IAMAPI, profile InstanceProfileInfo) (bool, error) {
	for _, roleName := range profile.Roles {
		ok, err := roleHasSSMPermissions(ctx, client, roleName)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

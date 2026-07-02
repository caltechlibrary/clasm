package awsclient

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// IAMAPI covers the IAM SDK methods awsops uses for the "IAM instance
// profile" launch parameter (DESIGN.md, Feature 2): listing existing
// instance profiles and roles to pick from, and creating a new instance
// profile attached to an existing role for operators who don't have one
// yet (see DECISIONS.md, "Support picking or creating an IAM instance
// profile from within awsops"). IAM's control plane is a global service,
// like STS -- one client suffices for the whole account.
type IAMAPI interface {
	ListInstanceProfiles(ctx context.Context, params *iam.ListInstanceProfilesInput, optFns ...func(*iam.Options)) (*iam.ListInstanceProfilesOutput, error)
	ListRoles(ctx context.Context, params *iam.ListRolesInput, optFns ...func(*iam.Options)) (*iam.ListRolesOutput, error)
	CreateInstanceProfile(ctx context.Context, params *iam.CreateInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.CreateInstanceProfileOutput, error)
	AddRoleToInstanceProfile(ctx context.Context, params *iam.AddRoleToInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.AddRoleToInstanceProfileOutput, error)
}

// NewIAMClient constructs an IAM client from the SDK's default credential
// chain. region only selects the signing endpoint -- IAM's data is
// account-wide, not region-scoped.
func NewIAMClient(ctx context.Context, region string) (IAMAPI, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return iam.NewFromConfig(cfg), nil
}

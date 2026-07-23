package workflow

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// iamTagsToMap converts IAM's tag shape to a plain map, mirroring
// manage_tags.go's tagsToMap (EC2-typed) and bucket_tags.go's
// s3TagsToMap (S3-typed) -- kept separate per this project's established
// convention of one small conversion helper per distinct SDK Tag type,
// rather than a shared generic (DECISIONS.md precedent: s3TagsToMap's
// own doc comment).
func iamTagsToMap(tags []iamtypes.Tag) map[string]string {
	m := make(map[string]string, len(tags))
	for _, tag := range tags {
		m[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	return m
}

// fetchIAMRoleTags fetches roleName's current full tag set, for the Tag
// Management domain's Manage tags/Show all tags actions on IAM Role
// (PLAN.md Phase 20.37). ListRoles itself doesn't return tags inline
// (DECISIONS.md, "ListRoles/ListInstanceProfiles/ListPolicies don't
// return tags inline"), so this is a dedicated per-resource call, same
// as inventory.ListIAMRoleSummaries' own per-role fetch.
func fetchIAMRoleTags(ctx context.Context, client awsclient.IAMAPI, roleName string) (map[string]string, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.ListRoleTags(ctx, &iam.ListRoleTagsInput{RoleName: aws.String(roleName)})
	if err != nil {
		return nil, err
	}
	return iamTagsToMap(out.Tags), nil
}

// applyIAMRoleTagChange is the IAM Role apply closure (tagApplyFunc,
// manage_tags.go): iam:TagRole (add/update) or iam:UntagRole (remove) --
// the same fine-grained, one-tag-at-a-time shape as EC2's CreateTags/
// DeleteTags (ApplyTagChange), not a whole-set replace like S3's
// PutBucketTagging (applyBucketTagChange). params.ResourceID is the
// role's name.
func applyIAMRoleTagChange(ctx context.Context, client awsclient.IAMAPI, params TagChangeParams) error {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	if params.Action == "remove" {
		_, err := client.UntagRole(ctx, &iam.UntagRoleInput{
			RoleName: aws.String(params.ResourceID),
			TagKeys:  []string{params.Key},
		})
		return err
	}
	_, err := client.TagRole(ctx, &iam.TagRoleInput{
		RoleName: aws.String(params.ResourceID),
		Tags:     []iamtypes.Tag{{Key: aws.String(params.Key), Value: aws.String(params.Value)}},
	})
	return err
}

// fetchIAMInstanceProfileTags fetches profileName's current full tag
// set -- same shape as fetchIAMRoleTags.
func fetchIAMInstanceProfileTags(ctx context.Context, client awsclient.IAMAPI, profileName string) (map[string]string, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.ListInstanceProfileTags(ctx, &iam.ListInstanceProfileTagsInput{InstanceProfileName: aws.String(profileName)})
	if err != nil {
		return nil, err
	}
	return iamTagsToMap(out.Tags), nil
}

// applyIAMInstanceProfileTagChange is the IAM Instance Profile apply
// closure -- same shape as applyIAMRoleTagChange. params.ResourceID is
// the profile's name.
func applyIAMInstanceProfileTagChange(ctx context.Context, client awsclient.IAMAPI, params TagChangeParams) error {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	if params.Action == "remove" {
		_, err := client.UntagInstanceProfile(ctx, &iam.UntagInstanceProfileInput{
			InstanceProfileName: aws.String(params.ResourceID),
			TagKeys:             []string{params.Key},
		})
		return err
	}
	_, err := client.TagInstanceProfile(ctx, &iam.TagInstanceProfileInput{
		InstanceProfileName: aws.String(params.ResourceID),
		Tags:                []iamtypes.Tag{{Key: aws.String(params.Key), Value: aws.String(params.Value)}},
	})
	return err
}

// fetchIAMPolicyTags fetches policyARN's current full tag set -- same
// shape as fetchIAMRoleTags, addressed by ARN rather than name (IAM
// policies are identified by ARN in Tag/Untag/List*Tags, not by
// PolicyName).
func fetchIAMPolicyTags(ctx context.Context, client awsclient.IAMAPI, policyARN string) (map[string]string, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.ListPolicyTags(ctx, &iam.ListPolicyTagsInput{PolicyArn: aws.String(policyARN)})
	if err != nil {
		return nil, err
	}
	return iamTagsToMap(out.Tags), nil
}

// applyIAMPolicyTagChange is the IAM Policy apply closure -- same shape
// as applyIAMRoleTagChange. params.ResourceID is the policy's ARN.
func applyIAMPolicyTagChange(ctx context.Context, client awsclient.IAMAPI, params TagChangeParams) error {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	if params.Action == "remove" {
		_, err := client.UntagPolicy(ctx, &iam.UntagPolicyInput{
			PolicyArn: aws.String(params.ResourceID),
			TagKeys:   []string{params.Key},
		})
		return err
	}
	_, err := client.TagPolicy(ctx, &iam.TagPolicyInput{
		PolicyArn: aws.String(params.ResourceID),
		Tags:      []iamtypes.Tag{{Key: aws.String(params.Key), Value: aws.String(params.Value)}},
	})
	return err
}

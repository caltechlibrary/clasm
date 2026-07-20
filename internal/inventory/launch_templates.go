package inventory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// LaunchTemplate is an EC2 launch template as displayed/managed by
// clasm, aggregated across regions (DESIGN.md, "Launch Templates").
// Project and Environment are empty if the template isn't tagged that
// way -- see Instance's doc comment. These are the template resource's
// own tags (CreateLaunchTemplate's TagSpecifications, ResourceType:
// launch-template), distinct from LaunchTemplateVersionDetail.Tags
// below (the tags a launched instance would receive).
type LaunchTemplate struct {
	TemplateID     string
	Name           string
	DefaultVersion int64
	LatestVersion  int64
	Region         string
	Project        string
	Environment    string
}

// ListLaunchTemplates queries ec2:DescribeLaunchTemplates in each
// region concurrently and aggregates the results -- same shape as
// ListImages/ListInstances.
func ListLaunchTemplates(ctx context.Context, clients map[string]awsclient.EC2API) ([]LaunchTemplate, error) {
	type result struct {
		region    string
		templates []LaunchTemplate
		err       error
	}

	results := make(chan result, len(clients))
	var wg sync.WaitGroup
	for region, client := range clients {
		wg.Add(1)
		go func(region string, client awsclient.EC2API) {
			defer wg.Done()
			templates, err := listLaunchTemplatesInRegion(ctx, client, region)
			results <- result{region: region, templates: templates, err: err}
		}(region, client)
	}
	wg.Wait()
	close(results)

	var all []LaunchTemplate
	for r := range results {
		if r.err != nil {
			return nil, fmt.Errorf("%s: %w", r.region, r.err)
		}
		all = append(all, r.templates...)
	}
	return all, nil
}

func listLaunchTemplatesInRegion(ctx context.Context, client awsclient.EC2API, region string) ([]LaunchTemplate, error) {
	var templates []LaunchTemplate
	input := &ec2.DescribeLaunchTemplatesInput{}
	for {
		out, err := client.DescribeLaunchTemplates(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, lt := range out.LaunchTemplates {
			templates = append(templates, launchTemplateFromSDK(lt, region))
		}
		if out.NextToken == nil {
			break
		}
		input.NextToken = out.NextToken
	}
	return templates, nil
}

func launchTemplateFromSDK(lt types.LaunchTemplate, region string) LaunchTemplate {
	_, project, environment := tagValues(lt.Tags)
	return LaunchTemplate{
		TemplateID:     aws.ToString(lt.LaunchTemplateId),
		Name:           aws.ToString(lt.LaunchTemplateName),
		DefaultVersion: aws.ToInt64(lt.DefaultVersionNumber),
		LatestVersion:  aws.ToInt64(lt.LatestVersionNumber),
		Region:         region,
		Project:        project,
		Environment:    environment,
	}
}

// LaunchTemplateVersionDetail is one version of a launch template's
// curated detail fields -- deliberately not the full
// ResponseLaunchTemplateData surface (block device mappings, capacity
// reservations, CPU options, ...), matching Image/Instance's own
// restraint (DESIGN.md, "Launch Templates"). UserData is left
// base64-encoded, as AWS returns it -- callers decode it (see Sync's
// diff mechanism, which needs the raw encoded form to detect identical
// content, and Show, which decodes for display).
type LaunchTemplateVersionDetail struct {
	TemplateID       string
	VersionNumber    int64
	IsDefaultVersion bool
	CreateTime       string
	ImageID          string
	InstanceType     string
	KeyName          string
	// IAMInstanceProfile is the instance profile's name, if set --
	// LaunchTemplateIamInstanceProfileSpecification also carries an ARN,
	// but every other clasm workflow that collects/displays an IAM
	// instance profile already keys on name (see launch_prompts.go).
	IAMInstanceProfile string
	SecurityGroupIDs   []string
	SubnetID           string
	UserData           string
	// IMDSv2Required reports whether MetadataOptions.HttpTokens is
	// "required" -- false (flagged by Show Launch Template) if the
	// version predates clasm's own enforcement or was created outside
	// clasm (DESIGN.md, "IMDSv2 enforcement").
	IMDSv2Required bool
	Project        string
	Environment    string
}

// DescribeLaunchTemplateVersion fetches one version's curated detail.
// version is a literal version number, "$Default", or "$Latest" (AWS's
// own convention) -- client must already be scoped to the template's
// own region, matching every other region-scoped detail fetch in this
// package.
func DescribeLaunchTemplateVersion(ctx context.Context, client awsclient.EC2API, templateID, version string) (LaunchTemplateVersionDetail, error) {
	out, err := client.DescribeLaunchTemplateVersions(ctx, &ec2.DescribeLaunchTemplateVersionsInput{
		LaunchTemplateId: aws.String(templateID),
		Versions:         []string{version},
	})
	if err != nil {
		return LaunchTemplateVersionDetail{}, err
	}
	if len(out.LaunchTemplateVersions) == 0 {
		return LaunchTemplateVersionDetail{}, fmt.Errorf("launch template %s version %s not found", templateID, version)
	}
	return launchTemplateVersionFromSDK(out.LaunchTemplateVersions[0]), nil
}

// LaunchTemplateVersionSummary is one version's identifying metadata,
// for listing every version of a template (DESIGN.md, "Launch
// Templates" version-history addendum, 2026-07-20) -- deliberately
// lighter than LaunchTemplateVersionDetail (no AMI/instance-type/tags),
// since a version list is about "which versions exist and when," not
// full per-version detail.
type LaunchTemplateVersionSummary struct {
	VersionNumber    int64
	CreateTime       string
	IsDefaultVersion bool
}

// ListLaunchTemplateVersions fetches every version of templateID --
// client must already be scoped to the template's own region, matching
// DescribeLaunchTemplateVersion. AWS's own ordering isn't guaranteed;
// callers sort if a specific order matters.
func ListLaunchTemplateVersions(ctx context.Context, client awsclient.EC2API, templateID string) ([]LaunchTemplateVersionSummary, error) {
	var summaries []LaunchTemplateVersionSummary
	input := &ec2.DescribeLaunchTemplateVersionsInput{LaunchTemplateId: aws.String(templateID)}
	for {
		out, err := client.DescribeLaunchTemplateVersions(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, v := range out.LaunchTemplateVersions {
			summaries = append(summaries, LaunchTemplateVersionSummary{
				VersionNumber:    aws.ToInt64(v.VersionNumber),
				CreateTime:       formatTime(v.CreateTime),
				IsDefaultVersion: aws.ToBool(v.DefaultVersion),
			})
		}
		if out.NextToken == nil {
			break
		}
		input.NextToken = out.NextToken
	}
	return summaries, nil
}

func launchTemplateVersionFromSDK(v types.LaunchTemplateVersion) LaunchTemplateVersionDetail {
	detail := LaunchTemplateVersionDetail{
		TemplateID:       aws.ToString(v.LaunchTemplateId),
		VersionNumber:    aws.ToInt64(v.VersionNumber),
		IsDefaultVersion: aws.ToBool(v.DefaultVersion),
		CreateTime:       formatTime(v.CreateTime),
	}
	data := v.LaunchTemplateData
	if data == nil {
		return detail
	}

	detail.ImageID = aws.ToString(data.ImageId)
	detail.InstanceType = string(data.InstanceType)
	detail.KeyName = aws.ToString(data.KeyName)
	detail.UserData = aws.ToString(data.UserData)
	if data.IamInstanceProfile != nil {
		detail.IAMInstanceProfile = aws.ToString(data.IamInstanceProfile.Name)
	}
	if data.MetadataOptions != nil {
		detail.IMDSv2Required = data.MetadataOptions.HttpTokens == types.LaunchTemplateHttpTokensStateRequired
	}
	detail.SecurityGroupIDs = data.SecurityGroupIds
	if len(data.NetworkInterfaces) > 0 {
		ni := data.NetworkInterfaces[0]
		detail.SubnetID = aws.ToString(ni.SubnetId)
		if len(detail.SecurityGroupIDs) == 0 {
			detail.SecurityGroupIDs = ni.Groups
		}
	}
	_, detail.Project, detail.Environment = tagSpecificationValues(data.TagSpecifications)
	return detail
}

// tagSpecificationValues extracts Name/Project/Environment from a
// launch template version's own TagSpecifications -- these are tags
// applied to resources launched *from* the template (RunInstances-time
// tags), a different field from the template resource's own Tags
// (LaunchTemplate.Tags, decoded via tagValues) -- see DECISIONS.md,
// "Launch templates: build directly from cloud-init YAML...".
func tagSpecificationValues(specs []types.LaunchTemplateTagSpecification) (name, project, environment string) {
	for _, spec := range specs {
		if spec.ResourceType != types.ResourceTypeInstance {
			continue
		}
		n, p, e := tagValues(spec.Tags)
		if n != "" {
			name = n
		}
		if p != "" {
			project = p
		}
		if e != "" {
			environment = e
		}
	}
	return name, project, environment
}

func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

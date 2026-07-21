package workflow

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// buildRequestLaunchTemplateData converts a LaunchInstanceParams (the
// exact parameter set Feature 3's cloud-init wizard already collects)
// into the launch-template request shape, curated the same way as
// LaunchTemplateVersionDetail (DESIGN.md, "Launch Templates"). IMDSv2
// is forced to required unconditionally -- not an operator choice
// (DECISIONS.md, "Launch templates: build directly from cloud-init
// YAML, diff-then-new-version sync, fold in IMDSv2"). Subnet placement
// must go through NetworkInterfaces -- unlike RunInstancesInput,
// RequestLaunchTemplateData has no flat SubnetId field -- and once
// NetworkInterfaces is used, security groups must move into that same
// entry rather than the top-level SecurityGroupIds field, per AWS's own
// documented constraint on that field (confirmed by reading the SDK's
// field comments, not assumed).
func buildRequestLaunchTemplateData(params LaunchInstanceParams) *types.RequestLaunchTemplateData {
	data := &types.RequestLaunchTemplateData{
		ImageId:      aws.String(params.ImageID),
		InstanceType: types.InstanceType(params.InstanceType),
		KeyName:      aws.String(params.KeyName),
		NetworkInterfaces: []types.LaunchTemplateInstanceNetworkInterfaceSpecificationRequest{
			{
				DeviceIndex: aws.Int32(0),
				SubnetId:    aws.String(params.SubnetID),
				Groups:      params.SecurityGroupIDs,
			},
		},
		MetadataOptions: &types.LaunchTemplateInstanceMetadataOptionsRequest{HttpTokens: types.LaunchTemplateHttpTokensStateRequired},
	}
	if params.RootVolumeSizeGB > 0 {
		data.BlockDeviceMappings = []types.LaunchTemplateBlockDeviceMappingRequest{{
			DeviceName: aws.String(params.RootDeviceName),
			Ebs:        &types.LaunchTemplateEbsBlockDeviceRequest{VolumeSize: aws.Int32(params.RootVolumeSizeGB)},
		}}
	}
	if params.UserData != "" {
		data.UserData = aws.String(base64.StdEncoding.EncodeToString([]byte(params.UserData)))
	}
	if params.IAMInstanceProfile != "" {
		data.IamInstanceProfile = &types.LaunchTemplateIamInstanceProfileSpecificationRequest{Name: aws.String(params.IAMInstanceProfile)}
	}
	if len(params.Tags) > 0 {
		spec := buildTagSpecification(types.ResourceTypeInstance, params.Tags)
		data.TagSpecifications = []types.LaunchTemplateTagSpecificationRequest{
			{ResourceType: spec.ResourceType, Tags: spec.Tags},
		}
	}
	return data
}

// CreateLaunchTemplateFromCloudInit runs the Create Launch Template
// from Cloud-Init YAML workflow (DESIGN.md, "Launch Templates"): reuse
// Feature 3's cloud-init-YAML-then-AMI-then-remaining-params prompt
// sequence unchanged (promptCloudInitYAMLFile, pickImage,
// collectLaunchInstanceParamsFromCloudInit), then prompt for the
// template's own name, confirm, and create it as version 1 -- a third
// peer entry alongside Create EC2 Instance from AMI/Cloud-Init YAML,
// not a variant of either.
func CreateLaunchTemplateFromCloudInit(ctx context.Context, w io.Writer, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, iamClient awsclient.IAMAPI, images []inventory.Image) error {
	userData, err := promptCloudInitYAMLFile(w, nil, nil)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	image, err := pickImage(ctx, "Select a base AMI", "Includes AMIs owned by this account and official Ubuntu LTS images; the cloud-init YAML you just gave runs on top of whichever you pick.", imagesWithOfficialUbuntu(ctx, ec2Clients, images))
	if err != nil {
		return cancelledIsNil(w, err)
	}

	return createLaunchTemplateFromCloudInit(ctx, w, ec2Clients, ssmClients, iamClient, userData, image, nil, nil)
}

// createLaunchTemplateFromCloudInit is
// CreateLaunchTemplateFromCloudInit's testable core, once the
// cloud-init YAML is read and an AMI is resolved -- same limitation as
// createInstanceFromCloudInit: AMI selection runs a real bubbletea
// Program that can't be pipe-tested. menuInput/menuOutput are nil in
// production and supplied by tests to drive every prompt (including the
// remaining-params collection this shares with Feature 3) through its
// accessible-mode pipe path.
func createLaunchTemplateFromCloudInit(ctx context.Context, w io.Writer, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, iamClient awsclient.IAMAPI, userData string, image inventory.Image, menuInput io.Reader, menuOutput io.Writer) error {
	params, ec2Client, _, err := collectLaunchInstanceParamsFromCloudInit(ctx, w, ec2Clients, ssmClients, iamClient, userData, image, menuInput, menuOutput)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return createLaunchTemplate(ctx, w, ec2Client, params, menuInput, menuOutput)
}

// createLaunchTemplate prompts for the template's own name, confirms,
// and creates it as version 1 -- shared testable core, independent of
// how params was collected.
func createLaunchTemplate(ctx context.Context, w io.Writer, client awsclient.EC2API, params LaunchInstanceParams, input io.Reader, output io.Writer) error {
	name, err := ui.Prompt("Launch template name", ui.WithValidator(requireNonEmpty), ui.WithIO(input, output))
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "\nAbout to create launch template %q: image=%s type=%s key=%s subnet=%s tags=%v\n",
		name, params.ImageID, params.InstanceType, params.KeyName, params.SubnetID, params.Tags)
	ok, err := Confirm("Create this launch template?", WithConfirmIO(input, output))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
		LaunchTemplateName: aws.String(name),
		LaunchTemplateData: buildRequestLaunchTemplateData(params),
		TagSpecifications:  []types.TagSpecification{buildTagSpecification(types.ResourceTypeLaunchTemplate, params.Tags)},
	})
	if err != nil {
		return fmt.Errorf("creating launch template: %w", err)
	}

	fmt.Fprintf(w, "Created launch template %s (%s), version 1.\n", aws.ToString(out.LaunchTemplate.LaunchTemplateId), name)
	return nil
}

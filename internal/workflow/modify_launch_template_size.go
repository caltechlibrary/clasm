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

// imagesForRegionAndArchitecture filters images to those in region with
// the given arch -- a launch template's AMI must exist in the
// template's own region, and the new AMI must actually support the
// newly-picked instance type's architecture (DESIGN.md, "Modify Launch
// Template Size").
func imagesForRegionAndArchitecture(images []inventory.Image, region, arch string) []inventory.Image {
	var filtered []inventory.Image
	for _, img := range images {
		if img.Region == region && img.Architecture == arch {
			filtered = append(filtered, img)
		}
	}
	return filtered
}

// pickNewLaunchTemplateAMIFunc indirects modifyLaunchTemplateSize's
// conditional "pick a new base AMI" step (only needed when the newly
// chosen instance type's architecture doesn't match the template's
// current AMI) through a package-level var -- pickImage runs a real
// bubbletea Program (tui.RunPicker) that can't be driven by a test's
// pipe input, the same limitation as every other Picker-tier call in
// this package, but unlike those, this one is invoked conditionally in
// the middle of an otherwise fully pipe-testable prompt sequence, so it
// needs its own substitutable seam (same shape as
// backup_archive.go's promptBackupBucketFunc) rather than being hoisted
// out to the untestable entry point the way every other Picker-tier
// call in this package already is.
var pickNewLaunchTemplateAMIFunc = pickImage

// modifyLaunchTemplateVersion creates a new version of templateID,
// based on sourceVersion, overriding ImageId/InstanceType/
// BlockDeviceMappings only -- everything else (including UserData) is
// inherited via SourceVersion, mirroring createLaunchTemplateVersion's
// shape for the reverse case (UserData-only override). ImageId is
// always set explicitly, even when unchanged from the source version --
// harmless in that case, and what actually makes an AMI swap take
// effect when it did change (DESIGN.md, "Modify Launch Template Size").
func modifyLaunchTemplateVersion(ctx context.Context, client awsclient.EC2API, templateID, sourceVersion, instanceType, imageID, rootDeviceName string, rootVolumeSizeGB int32) (int64, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.CreateLaunchTemplateVersion(ctx, &ec2.CreateLaunchTemplateVersionInput{
		LaunchTemplateId: aws.String(templateID),
		SourceVersion:    aws.String(sourceVersion),
		LaunchTemplateData: &types.RequestLaunchTemplateData{
			ImageId:      aws.String(imageID),
			InstanceType: types.InstanceType(instanceType),
			BlockDeviceMappings: []types.LaunchTemplateBlockDeviceMappingRequest{{
				DeviceName: aws.String(rootDeviceName),
				Ebs:        &types.LaunchTemplateEbsBlockDeviceRequest{VolumeSize: aws.Int32(rootVolumeSizeGB)},
			}},
		},
	})
	if err != nil {
		return 0, err
	}
	return aws.ToInt64(out.LaunchTemplateVersion.VersionNumber), nil
}

// ModifyLaunchTemplateSize runs the Modify Launch Template's Instance
// Type / EBS Root Volume Size workflow (DESIGN.md, "Modify Launch
// Template Size"): pick a template, pick a source version, change
// instance type and/or root volume size (and, if the new instance
// type's architecture doesn't match the current AMI, the AMI too), and
// create a new version -- never promotes it to default, matching
// Sync's own precedent.
func ModifyLaunchTemplateSize(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, templates []inventory.LaunchTemplate, images []inventory.Image) error {
	if len(templates) == 0 {
		fmt.Fprintln(w, "No launch templates found.")
		return nil
	}
	lt, err := pickLaunchTemplate(ctx, "Select a launch template to modify", "", templates)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return cancelledIsNil(w, modifyLaunchTemplateSize(ctx, w, clients, lt, images, nil, nil))
}

// modifyLaunchTemplateSize is ModifyLaunchTemplateSize's testable core,
// once a template is resolved -- same limitation as every other
// Picker-tier conversion in this package, except for the conditional
// AMI re-pick, which goes through pickNewLaunchTemplateAMIFunc instead
// of being hoisted out here (see that var's own doc comment).
func modifyLaunchTemplateSize(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, lt inventory.LaunchTemplate, images []inventory.Image, input io.Reader, output io.Writer) error {
	client, err := resolveEC2(clients, lt.Region)
	if err != nil {
		return err
	}

	version, err := promptLaunchTemplateVersion(input, output)
	if err != nil {
		return err
	}

	detail, err := inventory.DescribeLaunchTemplateVersion(ctx, client, lt.TemplateID, version)
	if err != nil {
		return err
	}

	rootDeviceName, amiDefaultGB, amiArch, err := describeImageRootVolume(ctx, client, detail.ImageID)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "Current: instance type=%s, AMI=%s (%s), root volume=%s\n", detail.InstanceType, detail.ImageID, amiArch, rootVolumeSizeDisplay(detail.RootVolumeSizeGB))

	instanceType, err := promptInstanceType(w, "", input, output)
	if err != nil {
		return err
	}

	newImageID := detail.ImageID
	newRootDeviceName := rootDeviceName
	newAMIDefaultGB := amiDefaultGB

	targetArch, err := instanceTypeArchitecture(ctx, client, instanceType)
	if err == nil && targetArch != "" && targetArch != amiArch {
		fmt.Fprintf(w, "Instance type %q requires %s, but the current AMI (%s) is %s -- pick a new base AMI.\n", instanceType, targetArch, detail.ImageID, amiArch)
		candidates := imagesForRegionAndArchitecture(imagesWithOfficialUbuntu(ctx, clients, images), lt.Region, targetArch)
		if len(candidates) == 0 {
			return fmt.Errorf("no %s AMI available in %s -- create or import one first", targetArch, lt.Region)
		}
		image, pickErr := pickNewLaunchTemplateAMIFunc(ctx, "Select a new base AMI", fmt.Sprintf("%s requires a %s AMI; the current AMI doesn't match.", instanceType, targetArch), candidates)
		if pickErr != nil {
			return pickErr
		}
		newImageID = image.ImageID
		newRootDeviceName, newAMIDefaultGB, _, err = describeImageRootVolume(ctx, client, newImageID)
		if err != nil {
			return err
		}
	}

	currentOrDefaultGB := detail.RootVolumeSizeGB
	if currentOrDefaultGB == 0 {
		currentOrDefaultGB = newAMIDefaultGB
	}
	rootVolumeSizeGB, err := promptRootVolumeSizeGB(currentOrDefaultGB, newAMIDefaultGB, input, output)
	if err != nil {
		return err
	}

	if instanceType == detail.InstanceType && rootVolumeSizeGB == detail.RootVolumeSizeGB && newImageID == detail.ImageID {
		fmt.Fprintln(w, "No changes -- nothing to modify.")
		return nil
	}

	ok, err := Confirm(fmt.Sprintf("Create a new version of %s: instance type=%s, AMI=%s, root volume=%d GB?", lt.TemplateID, instanceType, newImageID, rootVolumeSizeGB), WithConfirmIO(input, output))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	sourceVersion := fmt.Sprintf("%d", detail.VersionNumber)
	newVersion, err := modifyLaunchTemplateVersion(ctx, client, lt.TemplateID, sourceVersion, instanceType, newImageID, newRootDeviceName, rootVolumeSizeGB)
	if err != nil {
		return fmt.Errorf("creating new launch template version: %w", err)
	}

	fmt.Fprintf(w, "Created version %d of %s. It is NOT the default version yet -- use Promote Launch Template Version to Default when ready.\n", newVersion, lt.TemplateID)
	return nil
}

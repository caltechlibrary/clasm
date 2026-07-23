package awsclient

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/caltechlibrary/clasm/internal/debuglog"
)

type loggingEC2Client struct {
	inner  EC2API
	dl     *debuglog.DebugLog
	region string
}

// WrapEC2 returns an EC2API that logs every call to dl (see
// logAWSCall) before delegating to client. A nil dl returns client
// unchanged, so -debug=false costs nothing beyond one nil check per
// EC2Clients map entry at startup.
func WrapEC2(client EC2API, dl *debuglog.DebugLog, region string) EC2API {
	if dl == nil {
		return client
	}
	return &loggingEC2Client{inner: client, dl: dl, region: region}
}

func (w *loggingEC2Client) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return logAWSCall(w.dl, "EC2.DescribeInstances", w.region, params, func() (*ec2.DescribeInstancesOutput, error) {
		return w.inner.DescribeInstances(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	return logAWSCall(w.dl, "EC2.DescribeImages", w.region, params, func() (*ec2.DescribeImagesOutput, error) {
		return w.inner.DescribeImages(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) DescribeKeyPairs(ctx context.Context, params *ec2.DescribeKeyPairsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeKeyPairsOutput, error) {
	return logAWSCall(w.dl, "EC2.DescribeKeyPairs", w.region, params, func() (*ec2.DescribeKeyPairsOutput, error) {
		return w.inner.DescribeKeyPairs(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) ImportKeyPair(ctx context.Context, params *ec2.ImportKeyPairInput, optFns ...func(*ec2.Options)) (*ec2.ImportKeyPairOutput, error) {
	return logAWSCall(w.dl, "EC2.ImportKeyPair", w.region, params, func() (*ec2.ImportKeyPairOutput, error) {
		return w.inner.ImportKeyPair(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) DeleteKeyPair(ctx context.Context, params *ec2.DeleteKeyPairInput, optFns ...func(*ec2.Options)) (*ec2.DeleteKeyPairOutput, error) {
	return logAWSCall(w.dl, "EC2.DeleteKeyPair", w.region, params, func() (*ec2.DeleteKeyPairOutput, error) {
		return w.inner.DeleteKeyPair(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) DescribeSecurityGroups(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	return logAWSCall(w.dl, "EC2.DescribeSecurityGroups", w.region, params, func() (*ec2.DescribeSecurityGroupsOutput, error) {
		return w.inner.DescribeSecurityGroups(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return logAWSCall(w.dl, "EC2.DescribeSubnets", w.region, params, func() (*ec2.DescribeSubnetsOutput, error) {
		return w.inner.DescribeSubnets(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) DescribeInstanceTypeOfferings(ctx context.Context, params *ec2.DescribeInstanceTypeOfferingsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypeOfferingsOutput, error) {
	return logAWSCall(w.dl, "EC2.DescribeInstanceTypeOfferings", w.region, params, func() (*ec2.DescribeInstanceTypeOfferingsOutput, error) {
		return w.inner.DescribeInstanceTypeOfferings(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) DescribeInstanceTypes(ctx context.Context, params *ec2.DescribeInstanceTypesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error) {
	return logAWSCall(w.dl, "EC2.DescribeInstanceTypes", w.region, params, func() (*ec2.DescribeInstanceTypesOutput, error) {
		return w.inner.DescribeInstanceTypes(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) DescribeVpcs(ctx context.Context, params *ec2.DescribeVpcsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	return logAWSCall(w.dl, "EC2.DescribeVpcs", w.region, params, func() (*ec2.DescribeVpcsOutput, error) {
		return w.inner.DescribeVpcs(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) DescribeIamInstanceProfileAssociations(ctx context.Context, params *ec2.DescribeIamInstanceProfileAssociationsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeIamInstanceProfileAssociationsOutput, error) {
	return logAWSCall(w.dl, "EC2.DescribeIamInstanceProfileAssociations", w.region, params, func() (*ec2.DescribeIamInstanceProfileAssociationsOutput, error) {
		return w.inner.DescribeIamInstanceProfileAssociations(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) RunInstances(ctx context.Context, params *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	return logAWSCall(w.dl, "EC2.RunInstances", w.region, params, func() (*ec2.RunInstancesOutput, error) {
		return w.inner.RunInstances(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) StartInstances(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	return logAWSCall(w.dl, "EC2.StartInstances", w.region, params, func() (*ec2.StartInstancesOutput, error) {
		return w.inner.StartInstances(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) StopInstances(ctx context.Context, params *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
	return logAWSCall(w.dl, "EC2.StopInstances", w.region, params, func() (*ec2.StopInstancesOutput, error) {
		return w.inner.StopInstances(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) TerminateInstances(ctx context.Context, params *ec2.TerminateInstancesInput, optFns ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	return logAWSCall(w.dl, "EC2.TerminateInstances", w.region, params, func() (*ec2.TerminateInstancesOutput, error) {
		return w.inner.TerminateInstances(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) CreateImage(ctx context.Context, params *ec2.CreateImageInput, optFns ...func(*ec2.Options)) (*ec2.CreateImageOutput, error) {
	return logAWSCall(w.dl, "EC2.CreateImage", w.region, params, func() (*ec2.CreateImageOutput, error) {
		return w.inner.CreateImage(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) DeregisterImage(ctx context.Context, params *ec2.DeregisterImageInput, optFns ...func(*ec2.Options)) (*ec2.DeregisterImageOutput, error) {
	return logAWSCall(w.dl, "EC2.DeregisterImage", w.region, params, func() (*ec2.DeregisterImageOutput, error) {
		return w.inner.DeregisterImage(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	return logAWSCall(w.dl, "EC2.CreateTags", w.region, params, func() (*ec2.CreateTagsOutput, error) {
		return w.inner.CreateTags(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) DeleteTags(ctx context.Context, params *ec2.DeleteTagsInput, optFns ...func(*ec2.Options)) (*ec2.DeleteTagsOutput, error) {
	return logAWSCall(w.dl, "EC2.DeleteTags", w.region, params, func() (*ec2.DeleteTagsOutput, error) {
		return w.inner.DeleteTags(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) DescribeTags(ctx context.Context, params *ec2.DescribeTagsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeTagsOutput, error) {
	return logAWSCall(w.dl, "EC2.DescribeTags", w.region, params, func() (*ec2.DescribeTagsOutput, error) {
		return w.inner.DescribeTags(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) DescribeInstanceAttribute(ctx context.Context, params *ec2.DescribeInstanceAttributeInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceAttributeOutput, error) {
	return logAWSCall(w.dl, "EC2.DescribeInstanceAttribute", w.region, params, func() (*ec2.DescribeInstanceAttributeOutput, error) {
		return w.inner.DescribeInstanceAttribute(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) DescribeVolumes(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	return logAWSCall(w.dl, "EC2.DescribeVolumes", w.region, params, func() (*ec2.DescribeVolumesOutput, error) {
		return w.inner.DescribeVolumes(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) CreateLaunchTemplate(ctx context.Context, params *ec2.CreateLaunchTemplateInput, optFns ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateOutput, error) {
	return logAWSCall(w.dl, "EC2.CreateLaunchTemplate", w.region, params, func() (*ec2.CreateLaunchTemplateOutput, error) {
		return w.inner.CreateLaunchTemplate(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) CreateLaunchTemplateVersion(ctx context.Context, params *ec2.CreateLaunchTemplateVersionInput, optFns ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error) {
	return logAWSCall(w.dl, "EC2.CreateLaunchTemplateVersion", w.region, params, func() (*ec2.CreateLaunchTemplateVersionOutput, error) {
		return w.inner.CreateLaunchTemplateVersion(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) DescribeLaunchTemplates(ctx context.Context, params *ec2.DescribeLaunchTemplatesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplatesOutput, error) {
	return logAWSCall(w.dl, "EC2.DescribeLaunchTemplates", w.region, params, func() (*ec2.DescribeLaunchTemplatesOutput, error) {
		return w.inner.DescribeLaunchTemplates(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) DescribeLaunchTemplateVersions(ctx context.Context, params *ec2.DescribeLaunchTemplateVersionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplateVersionsOutput, error) {
	return logAWSCall(w.dl, "EC2.DescribeLaunchTemplateVersions", w.region, params, func() (*ec2.DescribeLaunchTemplateVersionsOutput, error) {
		return w.inner.DescribeLaunchTemplateVersions(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) ModifyLaunchTemplate(ctx context.Context, params *ec2.ModifyLaunchTemplateInput, optFns ...func(*ec2.Options)) (*ec2.ModifyLaunchTemplateOutput, error) {
	return logAWSCall(w.dl, "EC2.ModifyLaunchTemplate", w.region, params, func() (*ec2.ModifyLaunchTemplateOutput, error) {
		return w.inner.ModifyLaunchTemplate(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) DeleteLaunchTemplate(ctx context.Context, params *ec2.DeleteLaunchTemplateInput, optFns ...func(*ec2.Options)) (*ec2.DeleteLaunchTemplateOutput, error) {
	return logAWSCall(w.dl, "EC2.DeleteLaunchTemplate", w.region, params, func() (*ec2.DeleteLaunchTemplateOutput, error) {
		return w.inner.DeleteLaunchTemplate(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) DeleteLaunchTemplateVersions(ctx context.Context, params *ec2.DeleteLaunchTemplateVersionsInput, optFns ...func(*ec2.Options)) (*ec2.DeleteLaunchTemplateVersionsOutput, error) {
	return logAWSCall(w.dl, "EC2.DeleteLaunchTemplateVersions", w.region, params, func() (*ec2.DeleteLaunchTemplateVersionsOutput, error) {
		return w.inner.DeleteLaunchTemplateVersions(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) ModifyVolume(ctx context.Context, params *ec2.ModifyVolumeInput, optFns ...func(*ec2.Options)) (*ec2.ModifyVolumeOutput, error) {
	return logAWSCall(w.dl, "EC2.ModifyVolume", w.region, params, func() (*ec2.ModifyVolumeOutput, error) {
		return w.inner.ModifyVolume(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) DescribeVolumesModifications(ctx context.Context, params *ec2.DescribeVolumesModificationsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesModificationsOutput, error) {
	return logAWSCall(w.dl, "EC2.DescribeVolumesModifications", w.region, params, func() (*ec2.DescribeVolumesModificationsOutput, error) {
		return w.inner.DescribeVolumesModifications(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) AssociateIamInstanceProfile(ctx context.Context, params *ec2.AssociateIamInstanceProfileInput, optFns ...func(*ec2.Options)) (*ec2.AssociateIamInstanceProfileOutput, error) {
	return logAWSCall(w.dl, "EC2.AssociateIamInstanceProfile", w.region, params, func() (*ec2.AssociateIamInstanceProfileOutput, error) {
		return w.inner.AssociateIamInstanceProfile(ctx, params, optFns...)
	})
}

func (w *loggingEC2Client) ReplaceIamInstanceProfileAssociation(ctx context.Context, params *ec2.ReplaceIamInstanceProfileAssociationInput, optFns ...func(*ec2.Options)) (*ec2.ReplaceIamInstanceProfileAssociationOutput, error) {
	return logAWSCall(w.dl, "EC2.ReplaceIamInstanceProfileAssociation", w.region, params, func() (*ec2.ReplaceIamInstanceProfileAssociationOutput, error) {
		return w.inner.ReplaceIamInstanceProfileAssociation(ctx, params, optFns...)
	})
}

// CreateKeyPair does not use the shared logAWSCall helper: its output
// carries the new key pair's unencrypted private key material, which
// must never be written to the debug log. The rest of the output is
// still useful for debugging, so it's logged individually with
// KeyMaterial replaced by a fixed redaction marker rather than omitting
// the whole output.
func (w *loggingEC2Client) CreateKeyPair(ctx context.Context, params *ec2.CreateKeyPairInput, optFns ...func(*ec2.Options)) (*ec2.CreateKeyPairOutput, error) {
	start := time.Now()
	out, err := w.inner.CreateKeyPair(ctx, params, optFns...)

	fields := map[string]any{
		"method":      "EC2.CreateKeyPair",
		"region":      w.region,
		"params":      params,
		"duration_ms": time.Since(start).Milliseconds(),
	}
	if err != nil {
		fields["error"] = err.Error()
	} else {
		fields["output"] = map[string]any{
			"KeyName":        aws.ToString(out.KeyName),
			"KeyPairId":      aws.ToString(out.KeyPairId),
			"KeyFingerprint": aws.ToString(out.KeyFingerprint),
			"KeyMaterial":    "[REDACTED]",
		}
	}
	w.dl.Log("aws_call", fields)

	return out, err
}

package workflow

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// instanceTypeArchitecture reports instanceType's CPU architecture
// ("x86_64"/"arm64"/...), via ec2:DescribeInstanceTypes -- same API and
// shape as instanceTypeRequiresENA, reading ProcessorInfo instead of
// NetworkInfo. Used by Modify Launch Template Size (DESIGN.md) to catch
// an instance-type/AMI architecture mismatch before creating a new
// launch template version -- CreateLaunchTemplateVersion doesn't
// validate this itself, only RunInstances does, later. Returns "" (not
// an error) when the type is unrecognized or reports no architecture,
// matching instanceTypeRequiresENA's own "skip gracefully" philosophy.
func instanceTypeArchitecture(ctx context.Context, client awsclient.EC2API, instanceType string) (string, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{
		InstanceTypes: []types.InstanceType{types.InstanceType(instanceType)},
	})
	if err != nil {
		return "", err
	}
	if len(out.InstanceTypes) == 0 || out.InstanceTypes[0].ProcessorInfo == nil || len(out.InstanceTypes[0].ProcessorInfo.SupportedArchitectures) == 0 {
		return "", nil
	}
	return string(out.InstanceTypes[0].ProcessorInfo.SupportedArchitectures[0]), nil
}

package workflow

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/ui"
)

// instanceTypeRequiresENA reports whether instanceType requires Enhanced
// Networking (ENA), via ec2:DescribeInstanceTypes -- the pre-flight
// check for AWS's own "InvalidParameterCombination: Enhanced networking
// with the Elastic Network Adapter (ENA) is required for the '<type>'
// instance type" RunInstances error (see DECISIONS.md, "Pre-flight
// check: instance type vs. AMI ENA support").
func instanceTypeRequiresENA(ctx context.Context, client awsclient.EC2API, instanceType string) (bool, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{
		InstanceTypes: []types.InstanceType{types.InstanceType(instanceType)},
	})
	if err != nil {
		return false, err
	}
	if len(out.InstanceTypes) == 0 || out.InstanceTypes[0].NetworkInfo == nil {
		return false, nil
	}
	return out.InstanceTypes[0].NetworkInfo.EnaSupport == types.EnaSupportRequired, nil
}

// enaIncompatibilityChoices offers the same "change instance type" /
// "abort" shape as instanceTypeAZIncompatibilityChoices, minus "pick a
// different subnet" -- swapping subnets can't fix an AMI that isn't
// ENA-enabled, only a different instance type or a different AMI can,
// and changing the AMI this late would mean redoing earlier choices
// that depend on it (e.g. the Project tag default); aborting covers
// that case, same as any other declined confirmation.
var enaIncompatibilityChoices = []incompatibilityChoice{
	{label: "Change instance type", kind: incompatibilityChangeInstanceType},
	{label: "Abort this launch", kind: incompatibilityAbort},
}

// ensureInstanceTypeENACompatible checks instanceType against the
// already-picked AMI's ENA support (amiEnaSupport, from
// inventory.Image.EnaSupport) and, if instanceType requires ENA but the
// AMI doesn't support it, offers a pick list to change instance type or
// abort -- rather than a dead-end error or a doomed RunInstances call.
// Returns the (possibly updated) instance type, or ui.ErrCancelled if
// the operator aborts. Skips gracefully if the check itself errors,
// consistent with ensureInstanceTypeSupportedInSubnet's philosophy.
func ensureInstanceTypeENACompatible(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, client awsclient.EC2API, instanceType string, amiEnaSupport bool) (string, error) {
	for {
		requires, err := instanceTypeRequiresENA(ctx, client, instanceType)
		if err != nil || !requires || amiEnaSupport {
			return instanceType, nil
		}

		t.Printf("Instance type %q requires Enhanced Networking (ENA), but the picked AMI is not ENA-enabled.\n", instanceType)
		t.Println("Non-Nitro types (e.g. t2.micro, t2.medium) don't require ENA and work with this AMI as-is; permanently fixing the AMI itself requires enabling ENA on the source instance and re-creating the AMI (outside awsops).")
		t.Refresh()

		choice, err := ui.PickList(t, le, enaIncompatibilityChoices, incompatibilityChoiceLabel, "How would you like to proceed?")
		if err != nil {
			return "", err
		}

		switch choice.kind {
		case incompatibilityChangeInstanceType:
			instanceType, err = promptInstanceType(t, le)
			if err != nil {
				return "", err
			}
		case incompatibilityAbort:
			return "", ui.ErrCancelled
		}
	}
}

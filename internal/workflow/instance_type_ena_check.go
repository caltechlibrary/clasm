package workflow

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/ui"
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
// menuInput/menuOutput are nil in production (the "how would you like to
// proceed?" and any nested instance-type huh.Selects run interactively
// on the real terminal, DESIGN.md's full conversion punch list) and are
// supplied by tests to drive them through their accessible-mode pipe
// path instead. Both share one reader/writer pair, read in sequence one
// line at a time, same as a domain menu's own loop-iteration reads.
func ensureInstanceTypeENACompatible(ctx context.Context, w io.Writer, client awsclient.EC2API, instanceType string, amiEnaSupport bool, menuInput io.Reader, menuOutput io.Writer) (string, error) {
	for {
		requires, err := instanceTypeRequiresENA(ctx, client, instanceType)
		if err != nil || !requires || amiEnaSupport {
			return instanceType, nil
		}

		fmt.Fprintf(w, "Instance type %q requires Enhanced Networking (ENA), but the picked AMI is not ENA-enabled.\n", instanceType)
		fmt.Fprintln(w, "Non-Nitro types (e.g. t2.micro, t2.medium) don't require ENA and work with this AMI as-is; permanently fixing the AMI itself requires enabling ENA on the source instance and re-creating the AMI (outside awsops).")

		choice, err := pickComparable(w, "How would you like to proceed?", "The instance type requires Enhanced Networking, which the picked AMI doesn't support.", hintCancel, enaIncompatibilityChoices, incompatibilityChoiceLabel, menuInput, menuOutput)
		if err != nil {
			return "", err
		}

		switch choice.kind {
		case incompatibilityChangeInstanceType:
			// "" -- unfiltered by architecture, deliberately (DECISIONS.md,
			// "ARM64/Ubuntu 26.04: filter the instance-type list by AMI
			// architecture, no new pre-flight check"): a non-ENA-required
			// AMI old enough to need this remediation path predates
			// Graviton's existence in practice.
			instanceType, err = promptInstanceType(w, "", menuInput, menuOutput)
			if err != nil {
				return "", err
			}
		case incompatibilityAbort:
			return "", ui.ErrCancelled
		}
	}
}

package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// The non-interactive "Associate/replace IAM instance profile" (DR-0185). It
// swaps a running instance's profile in place, no reboot and no relaunch, and is
// safe to repeat: asking for the profile the instance already has changes nothing
// and succeeds, so a script can put the restore role on and the original back
// without reading state first. It has no confirmation gate -- the swap is
// reversible and destroys nothing -- and it prints the old and the new profile.
// The SSM-capable enforcement applies: a profile none of whose roles has
// AmazonSSMManagedInstanceCore would cut clasm off from the instance, so it is
// refused (exit 1, the state forbids it).

// AssociateInstanceProfileCLISlug is the leaf's cliSlug (DR-0177's mechanical
// rule applied to "Associate/replace IAM instance profile").
const AssociateInstanceProfileCLISlug = "associate-replace-iam-instance-profile"

// AssociateInstanceProfileUsage is the form's one-line usage, repeated in every
// usage error.
const AssociateInstanceProfileUsage = "usage: clasm compute " + AssociateInstanceProfileCLISlug +
	" [-text|-json] <instance-name-or-id> <instance-profile-name>"

const associateInstanceProfileHelp = `

Puts the named IAM instance profile on a running instance, in place: no reboot,
no relaunch. If the instance has no profile one is associated; if it has another
the association is replaced. If the instance already has the profile asked for
nothing is sent and the command succeeds, so it is safe to repeat.

The profile must already exist and must be SSM-capable (one of its roles has
AmazonSSMManagedInstanceCore attached); a profile that is not is refused with exit
1, because clasm manages instances over SSM and would lose this one. There is no
confirmation: the swap is reversible. The old and the new profile are printed.
`

// ParseAssociateInstanceProfileArgs parses the form's options and its two words,
// the instance (name or ID) and the profile name. Any problem is a *UsageError
// (exit 2) raised before AWS is touched; --help is a *HelpRequested.
func ParseAssociateInstanceProfileArgs(args []string) (instance, profile string, format ui.Format, err error) {
	opts, words, err := ParseComputeArgs(AssociateInstanceProfileCLISlug, AssociateInstanceProfileUsage, ComputeAllowNone, args)
	if err != nil {
		var h *HelpRequested
		if errors.As(err, &h) {
			return "", "", ui.FormatText, &HelpRequested{Usage: h.Usage + associateInstanceProfileHelp}
		}
		return "", "", ui.FormatText, err
	}
	if len(words) != 2 || strings.TrimSpace(words[0]) == "" || strings.TrimSpace(words[1]) == "" {
		return "", "", ui.FormatText, &UsageError{Msg: fmt.Sprintf("%s: want an instance and an instance profile, got %d arguments\n%s",
			AssociateInstanceProfileCLISlug, len(words), AssociateInstanceProfileUsage)}
	}
	return strings.TrimSpace(words[0]), strings.TrimSpace(words[1]), opts.Format, nil
}

// profileNameFromARN is the profile name at the end of an instance profile ARN,
// whatever IAM path precedes it (arn:aws:iam::1:instance-profile/dld/name).
func profileNameFromARN(arn string) string {
	if i := strings.LastIndex(arn, "/"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}

type associateProfileJSON struct {
	InstanceID      string `json:"instance_id"`
	Name            string `json:"name"`
	Region          string `json:"region"`
	PreviousProfile string `json:"previous_profile"`
	Profile         string `json:"profile"`
	Action          string `json:"action"` // associated, replaced or unchanged
}

// RunAssociateInstanceProfileCLI is `clasm compute associate-replace-iam-instance-profile`.
// Everything before the one EC2 call that changes anything is read-only, and a
// refusal (unknown instance or profile, a profile without SSM) sends nothing.
func RunAssociateInstanceProfileCLI(ctx context.Context, w io.Writer, ec2Clients map[string]awsclient.EC2API, iamClient awsclient.IAMAPI, instances []inventory.Instance, instanceArg, profileName string, format ui.Format) error {
	inst, err := resolveInstanceForCLI(instanceArg, instances)
	if err != nil {
		return err
	}
	ec2Client, err := resolveEC2(ec2Clients, inst.Region)
	if err != nil {
		return err
	}

	profiles, err := listInstanceProfiles(ctx, iamClient)
	if err != nil {
		return err
	}
	var profile *InstanceProfileInfo
	for i := range profiles {
		if profiles[i].Name == profileName {
			profile = &profiles[i]
		}
	}
	if profile == nil {
		return &UsageError{Msg: fmt.Sprintf("instance profile %q not found (this form never creates one)", profileName)}
	}
	capable, err := instanceProfileIsSSMCapable(ctx, iamClient, *profile)
	if err != nil {
		return err
	}
	if !capable {
		return fmt.Errorf("instance profile %q is not SSM-capable: none of its roles has %s attached, so clasm could no longer manage %s over SSM", profileName, ssmManagedInstanceCorePolicyArn, inst.InstanceID)
	}

	associationID, currentARN, found, err := describeCurrentInstanceProfileAssociation(ctx, ec2Client, inst.InstanceID)
	if err != nil {
		return err
	}
	previous := ""
	if found {
		previous = profileNameFromARN(currentARN)
	}

	action := "associated"
	switch {
	case found && previous == profileName:
		action = "unchanged"
	case found:
		action = "replaced"
		callCtx, cancel := withCallTimeout(ctx)
		defer cancel()
		if _, err := ec2Client.ReplaceIamInstanceProfileAssociation(callCtx, &ec2.ReplaceIamInstanceProfileAssociationInput{
			AssociationId:      aws.String(associationID),
			IamInstanceProfile: &types.IamInstanceProfileSpecification{Name: aws.String(profileName)},
		}); err != nil {
			return fmt.Errorf("replacing instance profile association for %s: %w", inst.InstanceID, err)
		}
	default:
		callCtx, cancel := withCallTimeout(ctx)
		defer cancel()
		if _, err := ec2Client.AssociateIamInstanceProfile(callCtx, &ec2.AssociateIamInstanceProfileInput{
			InstanceId:         aws.String(inst.InstanceID),
			IamInstanceProfile: &types.IamInstanceProfileSpecification{Name: aws.String(profileName)},
		}); err != nil {
			return fmt.Errorf("associating instance profile with %s: %w", inst.InstanceID, err)
		}
	}

	if format != ui.FormatText {
		return ui.WriteJSONValue(w, associateProfileJSON{inst.InstanceID, inst.Name, inst.Region, previous, profileName, action}, format)
	}
	switch action {
	case "unchanged":
		fmt.Fprintf(w, "%s (%s) already has instance profile %q; nothing was changed.\n", inst.InstanceID, inst.Name, profileName)
	case "replaced":
		fmt.Fprintf(w, "Replaced the instance profile on %s (%s): %q -> %q.\n", inst.InstanceID, inst.Name, previous, profileName)
	default:
		fmt.Fprintf(w, "Associated instance profile %q with %s (%s); it had none.\n", profileName, inst.InstanceID, inst.Name)
	}
	return nil
}

package workflow

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

type launchedInstanceJSON struct {
	InstanceID   string `json:"instance_id"`
	State        string `json:"state"`
	PublicIP     string `json:"public_ip"`
	PrivateIP    string `json:"private_ip"`
	Region       string `json:"region"`
	TemplateID   string `json:"template_id"`
	TemplateName string `json:"template_name"`
	Version      string `json:"version"`
}

// RunLaunchFromTemplateCLI is `clasm compute create-ec2-instance-from-launch-template
// <template> [version]` (DR-0177), the non-interactive form of "Create EC2
// instance from launch template". The template matches by name or ID; an
// unmatched or ambiguous one is a *UsageError and nothing is launched. version
// is "" for $Default, or a number ("2" or "v2"), or $Latest. There is no
// confirmation, as with the archive forms: naming the template is the intent.
// It launches one billable instance and waits for it to be running, sending the
// progress line to eout so stdout is the result alone -- connection info, or
// one JSON object with -json.
func RunLaunchFromTemplateCLI(ctx context.Context, w, eout io.Writer, clients map[string]awsclient.EC2API, templates []inventory.LaunchTemplate, arg, version string, format ui.Format) error {
	lt, err := resolveLaunchTemplateArg(arg, templates)
	if err != nil {
		return err
	}
	client, err := resolveEC2(clients, lt.Region)
	if err != nil {
		return err
	}
	if version == "" {
		version = defaultVersionSelector
	}
	version = normalizeVersionSelector(version)

	instanceID, err := launchFromTemplate(ctx, client, lt.TemplateID, version)
	if err != nil {
		return fmt.Errorf("launching instance from template: %w", err)
	}
	fmt.Fprintf(eout, "Launched %s from %s (%s), version %s; waiting for it to reach running...\n", instanceID, lt.TemplateID, lt.Name, version)
	inst, err := WaitUntilRunning(ctx, client, instanceID, DefaultLaunchTimeout, DefaultLaunchPollInterval)
	if err != nil {
		return err
	}
	if format == ui.FormatText {
		displayConnectionInfo(ctx, w, client, instanceID, inst)
		return nil
	}
	return ui.WriteJSONValue(w, launchedInstanceJSON{
		instanceID, string(inst.State.Name), aws.ToString(inst.PublicIpAddress), aws.ToString(inst.PrivateIpAddress),
		lt.Region, lt.TemplateID, lt.Name, version,
	}, format)
}

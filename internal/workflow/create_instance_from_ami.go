package workflow

import (
	"context"
	"errors"

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
	"github.com/caltechlibrary/awstools/internal/ui"
)

// CreateInstanceFromAMI runs the full Create EC2 Instance from AMI
// workflow (DESIGN.md, Feature 2): collect parameters AMI-first, then
// confirm/launch/wait/cloud-init-check via runLaunch, shared with
// CreateInstanceFromCloudInit (Feature 3). Returns nil (not an error) on
// user cancellation, matching every other workflow's confirmation gate.
// Takes per-region client maps (Phase 2 aggregates instances/AMIs across
// all four regions, so the specific client needed isn't known until
// after the AMI is picked) -- CollectLaunchInstanceParams resolves and
// returns the region-specific clients itself, since it also needs them
// to list key pairs/security groups/subnets.
func CreateInstanceFromAMI(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, iamClient awsclient.IAMAPI, images []inventory.Image) error {
	params, ec2Client, ssmClient, err := CollectLaunchInstanceParams(ctx, t, le, ec2Clients, ssmClients, iamClient, images)
	if err != nil {
		if errors.Is(err, ui.ErrCancelled) {
			t.Println("Cancelled.")
			t.Refresh()
			return nil
		}
		return err
	}
	return runLaunch(ctx, t, le, ec2Client, ssmClient, params)
}

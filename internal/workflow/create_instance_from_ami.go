package workflow

import (
	"context"
	"errors"
	"fmt"

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
// after the AMI is picked) and resolves the one matching the picked
// AMI's region.
func CreateInstanceFromAMI(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, images []inventory.Image) error {
	params, err := CollectLaunchInstanceParams(t, le, images)
	if err != nil {
		if errors.Is(err, ui.ErrCancelled) {
			t.Println("Cancelled.")
			t.Refresh()
			return nil
		}
		return err
	}
	img, found := findImageByID(images, params.ImageID)
	if !found {
		return fmt.Errorf("AMI %s not found", params.ImageID)
	}
	ec2Client, ssmClient, err := resolveEC2AndSSM(ec2Clients, ssmClients, img.Region)
	if err != nil {
		return err
	}
	return runLaunch(ctx, t, le, ec2Client, ssmClient, params)
}

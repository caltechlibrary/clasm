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
func CreateInstanceFromAMI(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ec2Client awsclient.EC2API, ssmClient awsclient.SSMAPI, images []inventory.Image) error {
	params, err := CollectLaunchInstanceParams(t, le, images)
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

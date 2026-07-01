package workflow

import (
	"context"
	"errors"

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
	"github.com/caltechlibrary/awstools/internal/ui"
)

// CreateInstanceFromCloudInit runs the full Create EC2 Instance from
// Cloud-Init YAML workflow (DESIGN.md, Feature 3): collect parameters
// cloud-init-first (then pick a base AMI), then confirm/launch/wait/
// cloud-init-check via runLaunch, shared with CreateInstanceFromAMI
// (Feature 2). Returns nil (not an error) on user cancellation, matching
// every other workflow's confirmation gate.
func CreateInstanceFromCloudInit(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ec2Client awsclient.EC2API, ssmClient awsclient.SSMAPI, images []inventory.Image) error {
	params, err := CollectLaunchInstanceParamsFromCloudInit(t, le, images)
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

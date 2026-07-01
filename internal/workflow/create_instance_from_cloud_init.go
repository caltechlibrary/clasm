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

// CreateInstanceFromCloudInit runs the full Create EC2 Instance from
// Cloud-Init YAML workflow (DESIGN.md, Feature 3): collect parameters
// cloud-init-first (then pick a base AMI), then confirm/launch/wait/
// cloud-init-check via runLaunch, shared with CreateInstanceFromAMI
// (Feature 2). Returns nil (not an error) on user cancellation, matching
// every other workflow's confirmation gate. Takes per-region client maps
// and resolves the one matching the picked AMI's region, same as
// CreateInstanceFromAMI.
func CreateInstanceFromCloudInit(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, images []inventory.Image) error {
	params, err := CollectLaunchInstanceParamsFromCloudInit(t, le, images)
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

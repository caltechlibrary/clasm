package workflow

import (
	"context"

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
)

// CreateKeyPairStandalone runs the Key Management domain's standalone
// "Create Key Pair" workflow (DESIGN.md, Feature 14): pick a region, then
// create -- reusing createNewKeyPairInteractive, the same name-prompt/
// duplicate-retry primitive Phase 15.2's inline "type new" shortcut
// already uses during instance launch, so both call sites share one
// implementation.
func CreateKeyPairStandalone(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, clients map[string]awsclient.EC2API) error {
	region, err := promptRegion(t, le, clients)
	if err != nil {
		return cancelledIsNil(t, err)
	}
	client, err := resolveEC2(clients, region)
	if err != nil {
		return err
	}
	_, err = createNewKeyPairInteractive(ctx, t, le, client, sshKeyDir())
	return err
}

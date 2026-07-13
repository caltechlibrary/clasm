package workflow

import (
	"context"
	"io"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// CreateKeyPairStandalone runs the Key Management domain's standalone
// "Create Key Pair" workflow (DESIGN.md, Feature 14): pick a region, then
// create -- reusing createNewKeyPairInteractive, the same name-prompt/
// duplicate-retry primitive Phase 15.2's inline "type new" shortcut
// already uses during instance launch, so both call sites share one
// implementation.
func CreateKeyPairStandalone(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API) error {
	return createKeyPairStandalone(ctx, w, clients, nil, nil)
}

// createKeyPairStandalone is CreateKeyPairStandalone's testable core:
// regionInput/regionOutput are nil in production (the region huh.Select
// runs interactively on the real terminal) and are supplied by tests to
// drive it through its accessible-mode pipe path instead.
func createKeyPairStandalone(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, regionInput io.Reader, regionOutput io.Writer) error {
	region, err := promptRegion(w, clients, regionInput, regionOutput)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	client, err := resolveEC2(clients, region)
	if err != nil {
		return err
	}
	_, err = createNewKeyPairInteractive(ctx, w, client, sshKeyDir(), regionInput, regionOutput)
	return err
}

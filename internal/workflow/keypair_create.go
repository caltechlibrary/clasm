package workflow

import (
	"context"
	"io"

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// CreateKeyPairStandalone runs the Key Management domain's standalone
// "Create Key Pair" workflow (DESIGN.md, Feature 14): pick a region, then
// create -- reusing createNewKeyPairInteractive, the same name-prompt/
// duplicate-retry primitive Phase 15.2's inline "type new" shortcut
// already uses during instance launch, so both call sites share one
// implementation.
func CreateKeyPairStandalone(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, clients map[string]awsclient.EC2API) error {
	return createKeyPairStandalone(ctx, t, le, clients, nil, nil)
}

// createKeyPairStandalone is CreateKeyPairStandalone's testable core:
// regionInput/regionOutput are nil in production (the region huh.Select
// runs interactively on the real terminal) and are supplied by tests to
// drive it through its accessible-mode pipe path instead, separate from
// le, which still feeds createNewKeyPairInteractive's own prompts.
func createKeyPairStandalone(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, clients map[string]awsclient.EC2API, regionInput io.Reader, regionOutput io.Writer) error {
	region, err := promptRegion(t, clients, regionInput, regionOutput)
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

package workflow

import (
	"io"
	"sort"

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// promptRegion lets the operator pick which configured region to operate
// in, for Key Management workflows (Create/Import Key Pair) that have no
// other resource already pinning them to a region. Delete Key Pair
// doesn't need this -- it picks from an already-listed key pair, which
// already carries its own Region. input/output are nil in production
// (the huh.Select runs interactively on the real terminal, DESIGN.md's
// full conversion punch list) and are supplied by tests for the
// accessible-mode pipe path.
func promptRegion(t *termlib.Terminal, clients map[string]awsclient.EC2API, input io.Reader, output io.Writer) (string, error) {
	regions := make([]string, 0, len(clients))
	for region := range clients {
		regions = append(regions, region)
	}
	sort.Strings(regions)
	return pickString(t, "Select a region", "(q to cancel)", regions, input, output)
}

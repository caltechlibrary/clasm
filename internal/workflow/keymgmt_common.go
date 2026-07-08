package workflow

import (
	"sort"

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/ui"
)

func regionLabel(r string) string { return r }

// promptRegion lets the operator pick which configured region to operate
// in, for Key Management workflows (Create/Import Key Pair) that have no
// other resource already pinning them to a region. Delete Key Pair
// doesn't need this -- it picks from an already-listed key pair, which
// already carries its own Region.
func promptRegion(t *termlib.Terminal, le *termlib.LineEditor, clients map[string]awsclient.EC2API) (string, error) {
	regions := make([]string, 0, len(clients))
	for region := range clients {
		regions = append(regions, region)
	}
	sort.Strings(regions)
	return ui.PickList(t, le, regions, regionLabel, "Select a region")
}

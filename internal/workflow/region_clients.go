package workflow

import (
	"fmt"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// Phase 2 aggregates instances and AMIs across all four configured
// regions into one unified listing, so an orchestrator doesn't know
// which single-region client it needs until after the user has picked a
// specific resource. These helpers resolve that client from the picked
// resource's Region, failing clearly if a region is missing from the
// caller's client map rather than silently using a nil client.

func resolveEC2(clients map[string]awsclient.EC2API, region string) (awsclient.EC2API, error) {
	client, ok := clients[region]
	if !ok {
		return nil, fmt.Errorf("no EC2 client configured for region %s", region)
	}
	return client, nil
}

func resolveSSM(clients map[string]awsclient.SSMAPI, region string) (awsclient.SSMAPI, error) {
	client, ok := clients[region]
	if !ok {
		return nil, fmt.Errorf("no SSM client configured for region %s", region)
	}
	return client, nil
}

func resolveEC2AndSSM(ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, region string) (awsclient.EC2API, awsclient.SSMAPI, error) {
	ec2Client, err := resolveEC2(ec2Clients, region)
	if err != nil {
		return nil, nil, err
	}
	ssmClient, err := resolveSSM(ssmClients, region)
	if err != nil {
		return nil, nil, err
	}
	return ec2Client, ssmClient, nil
}

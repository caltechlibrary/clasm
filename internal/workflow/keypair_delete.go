package workflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

func keyPairLabel(kp inventory.KeyPair) string {
	return fmt.Sprintf("%s (%s, %s)", kp.KeyName, kp.Region, kp.KeyType)
}

// instancesUsingKeyPair finds instances whose KeyName matches keyName,
// from the already-fetched inventory listing -- no extra AWS call
// needed, same pattern as instancesUsingAMI.
func instancesUsingKeyPair(instances []inventory.Instance, keyName string) []inventory.Instance {
	var out []inventory.Instance
	for _, inst := range instances {
		if inst.KeyName == keyName {
			out = append(out, inst)
		}
	}
	return out
}

// DeleteKeyPair runs the Key Management domain's "Delete Key Pair"
// workflow (DESIGN.md, Feature 16): pick a key pair, show any dependent
// instances, type-to-confirm by name, then deregister -- the same
// safety tier as Remove AMI (Feature 9), one notch below Terminate/
// Remove AMI's dry-run because deleting a key pair doesn't destroy
// already-running infrastructure, only future launches with it.
func DeleteKeyPair(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, clients map[string]awsclient.EC2API, keyPairs []inventory.KeyPair, instances []inventory.Instance) error {
	if len(keyPairs) == 0 {
		t.Println("No key pairs found.")
		t.Refresh()
		return nil
	}

	kp, err := ui.PickList(t, le, keyPairs, keyPairLabel, "Select a key pair to delete")
	if err != nil {
		return cancelledIsNil(t, err)
	}
	client, err := resolveEC2(clients, kp.Region)
	if err != nil {
		return err
	}

	dependents := instancesUsingKeyPair(instances, kp.KeyName)
	ok, err := confirmDeleteKeyPair(t, le, kp, dependents)
	if err != nil {
		return err
	}
	if !ok {
		t.Println("Cancelled.")
		t.Refresh()
		return nil
	}

	if _, err := client.DeleteKeyPair(ctx, &ec2.DeleteKeyPairInput{KeyName: aws.String(kp.KeyName)}); err != nil {
		return fmt.Errorf("deleting key pair %s: %w", kp.KeyName, err)
	}

	t.Printf("Key pair %s deleted. Any local ~/.ssh/%s.pem private key file is untouched -- this tool only removes AWS's own registration.\n", kp.KeyName, kp.KeyName)
	t.Refresh()
	return nil
}

// confirmDeleteKeyPair shows the dry-run display (which instances were
// launched with this key pair -- still able to keep running, but unable
// to launch new instances with it once deleted), then runs the
// type-to-confirm gate.
func confirmDeleteKeyPair(t *termlib.Terminal, le *termlib.LineEditor, kp inventory.KeyPair, dependents []inventory.Instance) (bool, error) {
	t.Printf("\n=== DRY RUN: deleting key pair %s ===\n", kp.KeyName)
	if len(dependents) > 0 {
		labels := make([]string, 0, len(dependents))
		for _, inst := range dependents {
			labels = append(labels, fmt.Sprintf("%s (%s)", inst.InstanceID, inst.Name))
		}
		t.Printf("WARNING: %d instance(s) were launched with this key pair: %s -- they will keep running, but this key pair can no longer be used to launch new ones once deleted.\n", len(dependents), strings.Join(labels, ", "))
	}
	t.Refresh()

	return ConfirmDestructive(t, le, kp.KeyName)
}

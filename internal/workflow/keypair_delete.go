package workflow

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/tui"
	"github.com/caltechlibrary/clasm/internal/ui"
)

func keyPairLabel(kp inventory.KeyPair) string {
	return fmt.Sprintf("%s (%s, %s)", kp.KeyName, kp.Region, kp.KeyType)
}

// pickKeyPairForDeletion runs a Picker-tier tui.RunPicker (DESIGN.md's
// full conversion punch list) over keyPairs and returns the chosen one.
// Like pickInstance/pickImage/pickSubnet, this drives a real bubbletea
// Program that can't be pipe-tested.
func pickKeyPairForDeletion(ctx context.Context, title string, keyPairs []inventory.KeyPair) (inventory.KeyPair, error) {
	rows := make([]string, len(keyPairs))
	for i, kp := range keyPairs {
		rows[i] = keyPairLabel(kp)
	}
	idx, err := tui.RunPicker(ctx, tui.PickerConfig{
		Title:        title,
		Rows:         rows,
		ColorEnabled: ui.ColorEnabled(),
	})
	if err != nil {
		return inventory.KeyPair{}, err
	}
	return keyPairs[idx], nil
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
func DeleteKeyPair(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, keyPairs []inventory.KeyPair, instances []inventory.Instance) error {
	if len(keyPairs) == 0 {
		fmt.Fprintln(w, "No key pairs found.")
		return nil
	}

	kp, err := pickKeyPairForDeletion(ctx, "Select a key pair to delete", keyPairs)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return deleteKeyPair(ctx, w, clients, kp, instances, nil, nil)
}

// deleteKeyPair is DeleteKeyPair's testable core, once a key pair is
// resolved -- key pair selection runs a real bubbletea Program
// (tui.RunPicker, DESIGN.md's full conversion punch list) that can't be
// driven by a test's pipe input, same limitation as every other
// Picker-tier conversion this session. input/output are nil in
// production and supplied by tests to drive the type-to-confirm gate
// through its accessible-mode pipe path instead.
func deleteKeyPair(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, kp inventory.KeyPair, instances []inventory.Instance, input io.Reader, output io.Writer) error {
	client, err := resolveEC2(clients, kp.Region)
	if err != nil {
		return err
	}

	dependents := instancesUsingKeyPair(instances, kp.KeyName)
	ok, err := confirmDeleteKeyPair(w, kp, dependents, input, output)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	if _, err := client.DeleteKeyPair(ctx, &ec2.DeleteKeyPairInput{KeyName: aws.String(kp.KeyName)}); err != nil {
		return fmt.Errorf("deleting key pair %s: %w", kp.KeyName, err)
	}

	fmt.Fprintf(w, "Key pair %s deleted. Any local ~/.ssh/%s.pem private key file is untouched -- this tool only removes AWS's own registration.\n", kp.KeyName, kp.KeyName)
	return nil
}

// confirmDeleteKeyPair shows the dry-run display (which instances were
// launched with this key pair -- still able to keep running, but unable
// to launch new instances with it once deleted), then runs the
// type-to-confirm gate.
func confirmDeleteKeyPair(w io.Writer, kp inventory.KeyPair, dependents []inventory.Instance, input io.Reader, output io.Writer) (bool, error) {
	fmt.Fprintf(w, "\n=== DRY RUN: deleting key pair %s ===\n", kp.KeyName)
	if len(dependents) > 0 {
		labels := make([]string, 0, len(dependents))
		for _, inst := range dependents {
			labels = append(labels, fmt.Sprintf("%s (%s)", inst.InstanceID, inst.Name))
		}
		fmt.Fprintf(w, "WARNING: %d instance(s) were launched with this key pair: %s -- they will keep running, but this key pair can no longer be used to launch new ones once deleted.\n", len(dependents), strings.Join(labels, ", "))
	}

	return ConfirmDestructive([]string{kp.KeyName}, WithConfirmIO(input, output))
}

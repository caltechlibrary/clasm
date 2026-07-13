package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/smithy-go"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

func writePubKeyFile(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "id_ed25519.pub")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing test pub key file: %v", err)
	}
	return path
}

// The region picker converted to huh.Select (DESIGN.md's full conversion
// punch list): its selection is fed via a separate newHuhAccessibleInput
// reader (regionInput), not le, which still feeds every other prompt in
// this function. Cancelling it is only reachable via 'q'/ctrl+c, which
// accessible mode has no keyboard to simulate (mapMenuPickerErr's doc
// comment covers the same limitation), so the old "0=Cancel" test is
// retired rather than kept.

func TestImportKeyPairStandalone_Success(t *testing.T) {
	pubPath := writePubKeyFile(t, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyMaterial user@host\n")
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-west-1": fake}
	term, input, buf := newPipeEditor("1\n" + "my-imported-key\n" + pubPath + "\n")

	err := importKeyPairStandalone(context.Background(), term, clients, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastImportKeyPairInput == nil {
		t.Fatal("ImportKeyPair was never called")
	}
	if aws.ToString(fake.lastImportKeyPairInput.KeyName) != "my-imported-key" {
		t.Errorf("KeyName = %q, want %q", aws.ToString(fake.lastImportKeyPairInput.KeyName), "my-imported-key")
	}
	if !strings.Contains(buf.String(), "Imported key pair") {
		t.Errorf("expected a success message, got:\n%s", buf.String())
	}
}

// TestImportKeyPairStandalone_PromptLabelStaysShort used to guard
// against a real termlib.LineEditor.Prompt bug: its input viewport was
// computed as terminal-width minus prompt length, assuming the whole
// prompt fit on one row, so an overlong label caused garbled, repeated
// redraws in a real terminal. Retired with termlib itself (DECISIONS.md,
// "Remove termlib entirely: input via huh, output via io.Writer") --
// huh.Input's own rendering doesn't share that implementation or its
// single-row assumption, so the invariant no longer applies.

func TestImportKeyPairStandalone_MalformedFileRejectedAndReprompted(t *testing.T) {
	badPath := writePubKeyFile(t, "this is not a public key\n")
	goodPath := writePubKeyFile(t, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyMaterial user@host\n")
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-west-1": fake}
	term, input, buf := newPipeEditor("1\n" + "my-key\n" + badPath + "\n" + goodPath + "\n")

	err := importKeyPairStandalone(context.Background(), term, clients, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastImportKeyPairInput == nil {
		t.Fatal("ImportKeyPair was never called")
	}
	if !strings.Contains(buf.String(), "does not") {
		t.Errorf("expected a local validation error message, got:\n%s", buf.String())
	}
}

func TestImportKeyPairStandalone_PrivateKeyFileRejectionSuggestsSSHKeygen(t *testing.T) {
	// A .pem private key's content doesn't start with a recognized
	// public-key-type token, so it hits the same "does not start with a
	// recognized public key type" branch as any other malformed file --
	// this is where the ssh-keygen derivation guidance now lives
	// (reactively, on the actual mistake), instead of upfront in every
	// prompt.
	pemPath := writePubKeyFile(t, "-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----\n")
	goodPath := writePubKeyFile(t, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyMaterial user@host\n")
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-west-1": fake}
	term, input, buf := newPipeEditor("1\n" + "my-key\n" + pemPath + "\n" + goodPath + "\n")

	err := importKeyPairStandalone(context.Background(), term, clients, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "ssh-keygen -y -f") {
		t.Errorf("expected the ssh-keygen derivation hint in the rejection message, got:\n%s", buf.String())
	}
}

func TestImportKeyPairStandalone_ExpandsHomeTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("creating %s: %v", sshDir, err)
	}
	pubPath := filepath.Join(sshDir, "id_ed25519.pub")
	if err := os.WriteFile(pubPath, []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyMaterial user@host\n"), 0o644); err != nil {
		t.Fatalf("writing test pub key file: %v", err)
	}
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-west-1": fake}
	term, input, buf := newPipeEditor("1\n" + "my-key\n~/.ssh/id_ed25519.pub\n")

	err := importKeyPairStandalone(context.Background(), term, clients, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastImportKeyPairInput == nil {
		t.Fatal("ImportKeyPair was never called -- \"~\" was not expanded to the home directory")
	}
}

func TestImportKeyPairStandalone_MissingFileRejected(t *testing.T) {
	goodPath := writePubKeyFile(t, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyMaterial user@host\n")
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-west-1": fake}
	term, input, buf := newPipeEditor("1\n" + "my-key\n/no/such/file.pub\n" + goodPath + "\n")

	err := importKeyPairStandalone(context.Background(), term, clients, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastImportKeyPairInput == nil {
		t.Fatal("ImportKeyPair was never called")
	}
	if !strings.Contains(buf.String(), "cannot read") {
		t.Errorf("expected a cannot-read message, got:\n%s", buf.String())
	}
}

func TestImportKeyPairStandalone_RetriesOnDuplicateName(t *testing.T) {
	pubPath := writePubKeyFile(t, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyMaterial user@host\n")
	fake := &fakeEC2Client{
		importKeyPairErr:     &smithy.GenericAPIError{Code: "InvalidKeyPair.Duplicate", Message: "already exists"},
		importKeyPairErrOnce: true,
	}
	clients := map[string]awsclient.EC2API{"us-west-1": fake}
	term, input, buf := newPipeEditor("1\n" + "taken-name\n" + pubPath + "\nfresh-name\n" + pubPath + "\n")

	err := importKeyPairStandalone(context.Background(), term, clients, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.importKeyPairCalls != 2 {
		t.Errorf("ImportKeyPair calls = %d, want 2", fake.importKeyPairCalls)
	}
	if !strings.Contains(buf.String(), "already exists") {
		t.Errorf("expected a duplicate-name message in output, got:\n%s", buf.String())
	}
}

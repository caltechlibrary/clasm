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

func TestImportKeyPairStandalone_Success(t *testing.T) {
	pubPath := writePubKeyFile(t, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyMaterial user@host\n")
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-west-1": fake}
	term, le, buf := newPipeEditor(t, "1\nmy-imported-key\n"+pubPath+"\n")

	err := ImportKeyPairStandalone(context.Background(), term, le, clients)
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

func TestImportKeyPairStandalone_PromptLabelStaysShort(t *testing.T) {
	// termlib's LineEditor.Prompt computes its input viewport as
	// terminal-width minus prompt length, assuming the whole prompt fits
	// on one row -- an overlong prompt label causes garbled, repeated
	// redraws in a real terminal (this was a real reported bug: a ~180
	// char label embedding the full ssh-keygen derivation hint). This
	// can't be reproduced via this pipe-based test harness -- a non-TTY
	// input makes LineEditor always fall back to its plain, non-raw-mode
	// path, which has none of the affected redraw logic -- so this
	// guards the actual invariant directly instead: the prompt label
	// handed to ui.Prompt must stay well under a safe terminal width,
	// even with the label's own "(.pub -- e.g. ...)" suffix plus room
	// left over for whatever the operator types.
	const maxSafePromptLen = 70
	pubPath := writePubKeyFile(t, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyMaterial user@host\n")
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-west-1": fake}
	term, le, buf := newPipeEditor(t, "1\nmy-imported-key\n"+pubPath+"\n")

	if err := ImportKeyPairStandalone(context.Background(), term, le, clients); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	const marker = "Public key file path"
	i := strings.Index(buf.String(), marker)
	if i < 0 {
		t.Fatalf("expected to find the public key file prompt, got:\n%s", buf.String())
	}
	promptLine := buf.String()[i:]
	if j := strings.Index(promptLine, ": "); j >= 0 {
		promptLine = promptLine[:j]
	}
	if len(promptLine) > maxSafePromptLen {
		t.Errorf("prompt label is %d chars (%q), want <= %d -- long prompts break termlib's raw-mode redraw", len(promptLine), promptLine, maxSafePromptLen)
	}
}

func TestImportKeyPairStandalone_MalformedFileRejectedAndReprompted(t *testing.T) {
	badPath := writePubKeyFile(t, "this is not a public key\n")
	goodPath := writePubKeyFile(t, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyMaterial user@host\n")
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-west-1": fake}
	term, le, buf := newPipeEditor(t, "1\nmy-key\n"+badPath+"\n"+goodPath+"\n")

	err := ImportKeyPairStandalone(context.Background(), term, le, clients)
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
	term, le, buf := newPipeEditor(t, "1\nmy-key\n"+pemPath+"\n"+goodPath+"\n")

	err := ImportKeyPairStandalone(context.Background(), term, le, clients)
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
	term, le, _ := newPipeEditor(t, "1\nmy-key\n~/.ssh/id_ed25519.pub\n")

	err := ImportKeyPairStandalone(context.Background(), term, le, clients)
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
	term, le, buf := newPipeEditor(t, "1\nmy-key\n/no/such/file.pub\n"+goodPath+"\n")

	err := ImportKeyPairStandalone(context.Background(), term, le, clients)
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
	term, le, buf := newPipeEditor(t, "1\ntaken-name\n"+pubPath+"\nfresh-name\n"+pubPath+"\n")

	err := ImportKeyPairStandalone(context.Background(), term, le, clients)
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

func TestImportKeyPairStandalone_CancelledRegionPick(t *testing.T) {
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-west-1": fake}
	term, le, _ := newPipeEditor(t, "0\n")

	err := ImportKeyPairStandalone(context.Background(), term, le, clients)
	if err != nil {
		t.Fatalf("expected a clean cancel (nil error), got: %v", err)
	}
	if fake.lastImportKeyPairInput != nil {
		t.Error("ImportKeyPair was called despite cancelling the region pick")
	}
}

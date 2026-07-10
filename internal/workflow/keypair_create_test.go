package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/smithy-go"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// The region picker converted to huh.Select (DESIGN.md's full conversion
// punch list): its selection is fed via a separate newHuhAccessibleInput
// reader (regionInput), not le, which still feeds createNewKeyPair
// Interactive's own name prompt. Cancelling it is only reachable via
// 'q'/ctrl+c, which accessible mode has no keyboard to simulate
// (mapMenuPickerErr's doc comment covers the same limitation), so the
// old "0=Cancel" test is retired rather than kept.

func TestCreateKeyPairStandalone_Success(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-west-1": fake}
	term, le, buf := newPipeEditor(t, "my-new-key\n")

	err := createKeyPairStandalone(context.Background(), term, le, clients, newHuhAccessibleInput("1\n"), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.createKeyPairCalls != 1 {
		t.Errorf("CreateKeyPair calls = %d, want 1", fake.createKeyPairCalls)
	}

	wantPath := filepath.Join(dir, ".ssh", "my-new-key.pem")
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("expected the private key to be saved at %s: %v", wantPath, err)
	}
	if !strings.Contains(buf.String(), wantPath) {
		t.Errorf("expected the saved key path to be printed, got:\n%s", buf.String())
	}
}

func TestCreateKeyPairStandalone_RetriesOnDuplicateName(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	fake := &fakeEC2Client{
		createKeyPairErr:     &smithy.GenericAPIError{Code: "InvalidKeyPair.Duplicate", Message: "already exists"},
		createKeyPairErrOnce: true,
	}
	clients := map[string]awsclient.EC2API{"us-west-1": fake}
	term, le, buf := newPipeEditor(t, "taken-name\nfresh-name\n")

	err := createKeyPairStandalone(context.Background(), term, le, clients, newHuhAccessibleInput("1\n"), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.createKeyPairCalls != 2 {
		t.Errorf("CreateKeyPair calls = %d, want 2", fake.createKeyPairCalls)
	}
	if !strings.Contains(buf.String(), "already exists") {
		t.Errorf("expected a duplicate-name message in output, got:\n%s", buf.String())
	}
}

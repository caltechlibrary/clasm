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

// The region picker (huh.Select) and createNewKeyPairInteractive's own
// name prompt now share one accessible-mode reader, read in sequence
// one line at a time -- region first, then name. Cancelling the picker
// is only reachable via 'q'/ctrl+c, which accessible mode has no
// keyboard to simulate (mapMenuPickerErr's doc comment covers the same
// limitation), so the old "0=Cancel" test is retired rather than kept.

func TestCreateKeyPairStandalone_Success(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-west-1": fake}
	term, input, buf := newPipeEditor("1\nmy-new-key\n") // region, then key pair name

	err := createKeyPairStandalone(context.Background(), term, clients, input, buf)
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
	term, input, buf := newPipeEditor("1\ntaken-name\nfresh-name\n")

	err := createKeyPairStandalone(context.Background(), term, clients, input, buf)
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

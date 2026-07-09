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

func TestCreateKeyPairStandalone_Success(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-west-1": fake}
	term, le, buf := newPipeEditor(t, "1\nmy-new-key\n") // 1) us-west-1 -> name

	err := CreateKeyPairStandalone(context.Background(), term, le, clients)
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
	term, le, buf := newPipeEditor(t, "1\ntaken-name\nfresh-name\n")

	err := CreateKeyPairStandalone(context.Background(), term, le, clients)
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

func TestCreateKeyPairStandalone_CancelledRegionPick(t *testing.T) {
	fake := &fakeEC2Client{}
	clients := map[string]awsclient.EC2API{"us-west-1": fake}
	term, le, _ := newPipeEditor(t, "0\n")

	err := CreateKeyPairStandalone(context.Background(), term, le, clients)
	if err != nil {
		t.Fatalf("expected a clean cancel (nil error), got: %v", err)
	}
	if fake.createKeyPairCalls != 0 {
		t.Error("CreateKeyPair was called despite cancelling the region pick")
	}
}

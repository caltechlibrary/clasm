package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/smithy-go"
)

func TestCreateKeyPair_SavesPrivateKeyWithCorrectPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ssh")
	fake := &fakeEC2Client{createKeyPairKeyMaterial: "-----BEGIN OPENSSH PRIVATE KEY-----\nsecret\n-----END OPENSSH PRIVATE KEY-----\n"}

	path, err := createKeyPair(context.Background(), fake, "my-new-key", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantPath := filepath.Join(dir, "my-new-key.pem")
	if path != wantPath {
		t.Errorf("path = %q, want %q", path, wantPath)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 0600", perm)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if !strings.Contains(string(data), "secret") {
		t.Errorf("saved key material = %q, want it to contain the fake private key", data)
	}

	if fake.lastCreateKeyPairInput == nil {
		t.Fatal("CreateKeyPair was never called")
	}
	if got := string(fake.lastCreateKeyPairInput.KeyType); got != "ed25519" {
		t.Errorf("KeyType = %q, want %q", got, "ed25519")
	}
}

func TestCreateKeyPair_CreatesSSHDirIfMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does", "not", "exist", "yet")
	fake := &fakeEC2Client{}

	if _, err := createKeyPair(context.Background(), fake, "my-key", dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("expected %s to have been created as a directory", dir)
	}
}

func TestCreateKeyPair_PropagatesErrorWithoutWritingAFile(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeEC2Client{createKeyPairErr: &smithy.GenericAPIError{Code: "UnauthorizedOperation", Message: "no"}}

	if _, err := createKeyPair(context.Background(), fake, "my-key", dir); err == nil {
		t.Fatal("expected an error")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no files written on error, got %v", entries)
	}
}

func TestPromptKeyPairNameOrCreate_ReturnsTypedNameWhenNotCreating(t *testing.T) {
	fake := &fakeEC2Client{}
	term, le, _ := newPipeEditor(t, "existing-key\n")

	got, err := promptKeyPairNameOrCreate(context.Background(), term, le, fake, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "existing-key" {
		t.Errorf("got %q, want %q", got, "existing-key")
	}
	if fake.createKeyPairCalls != 0 {
		t.Errorf("CreateKeyPair was called %d times, want 0", fake.createKeyPairCalls)
	}
}

func TestPromptKeyPairNameOrCreate_CreatesNewKeyPair(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor(t, "new\nmy-fresh-key\n")

	got, err := promptKeyPairNameOrCreate(context.Background(), term, le, fake, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "my-fresh-key" {
		t.Errorf("got %q, want %q", got, "my-fresh-key")
	}
	if fake.createKeyPairCalls != 1 {
		t.Errorf("CreateKeyPair calls = %d, want 1", fake.createKeyPairCalls)
	}

	wantPath := filepath.Join(dir, "my-fresh-key.pem")
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("expected the private key to be saved at %s: %v", wantPath, err)
	}
	if !strings.Contains(buf.String(), wantPath) {
		t.Errorf("expected the saved key path to be printed, got:\n%s", buf.String())
	}
}

func TestPromptKeyPairNameOrCreate_RetriesOnDuplicateName(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeEC2Client{
		createKeyPairErr:     &smithy.GenericAPIError{Code: "InvalidKeyPair.Duplicate", Message: "already exists"},
		createKeyPairErrOnce: true,
	}
	term, le, buf := newPipeEditor(t, "new\ntaken-name\nfresh-name\n")

	got, err := promptKeyPairNameOrCreate(context.Background(), term, le, fake, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "fresh-name" {
		t.Errorf("got %q, want %q", got, "fresh-name")
	}
	if fake.createKeyPairCalls != 2 {
		t.Errorf("CreateKeyPair calls = %d, want 2", fake.createKeyPairCalls)
	}
	if !strings.Contains(buf.String(), "already exists") {
		t.Errorf("expected a duplicate-name message in output, got:\n%s", buf.String())
	}
}

func TestPromptKeyPairNameOrCreate_PropagatesNonDuplicateError(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeEC2Client{createKeyPairErr: &smithy.GenericAPIError{Code: "UnauthorizedOperation", Message: "no ec2:CreateKeyPair permission"}}
	term, le, _ := newPipeEditor(t, "new\nmy-key\n")

	_, err := promptKeyPairNameOrCreate(context.Background(), term, le, fake, dir)
	if err == nil {
		t.Fatal("expected an error")
	}
	if fake.createKeyPairCalls != 1 {
		t.Errorf("CreateKeyPair calls = %d, want 1 (should not retry a non-duplicate error)", fake.createKeyPairCalls)
	}
}

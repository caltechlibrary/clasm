package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
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

// Regression: a real launch failed with AWS's own
// InvalidKeyPair.NotFound after every prompt had already been answered
// and confirmed, because the picked AMI's region (surfaced by the
// official-Ubuntu-AMI feature, which fans out across every configured
// region) had zero key pairs -- yet the old free-text-only prompt
// accepted any typed name unconditionally, with no way to know it
// didn't exist there until the distant RunInstances call failed. See
// DECISIONS.md, "Validate key pair name against the AMI's region".
func TestPromptKeyPairNameOrCreate_NoKeyPairsInRegionOffersCreateFirst(t *testing.T) {
	fake := &fakeEC2Client{}                             // zero key pairs in this region
	term, le, buf := newPipeEditor(t, "1\nmy-new-key\n") // 1) Create new key pair

	got, err := promptKeyPairNameOrCreate(context.Background(), term, le, fake, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "my-new-key" {
		t.Errorf("got %q, want %q", got, "my-new-key")
	}
	if fake.createKeyPairCalls != 1 {
		t.Errorf("createKeyPairCalls = %d, want 1", fake.createKeyPairCalls)
	}
	if !strings.Contains(buf.String(), "No key pairs found in this region") {
		t.Errorf("expected a message noting no key pairs exist yet, got:\n%s", buf.String())
	}
}

func TestPromptKeyPairNameOrCreate_PicksFromExistingKeyPairsInRegion(t *testing.T) {
	fake := &fakeEC2Client{keyPairs: []types.KeyPairInfo{
		{KeyName: aws.String("etd-ami-test")},
		{KeyName: aws.String("other-key")},
	}}
	term, le, _ := newPipeEditor(t, "1\n")

	got, err := promptKeyPairNameOrCreate(context.Background(), term, le, fake, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "etd-ami-test" {
		t.Errorf("got %q, want %q", got, "etd-ami-test")
	}
	if fake.createKeyPairCalls != 0 {
		t.Errorf("createKeyPairCalls = %d, want 0", fake.createKeyPairCalls)
	}
}

func TestPromptKeyPairNameOrCreate_CreatesNewKeyPair(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeEC2Client{keyPairs: []types.KeyPairInfo{{KeyName: aws.String("existing-key")}}}
	term, le, buf := newPipeEditor(t, "2\nmy-fresh-key\n") // 1) existing-key, 2) Create new key pair

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
	term, le, buf := newPipeEditor(t, "1\ntaken-name\nfresh-name\n") // zero existing keys -> 1) Create new key pair

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
	term, le, _ := newPipeEditor(t, "1\nmy-key\n") // zero existing keys -> 1) Create new key pair

	_, err := promptKeyPairNameOrCreate(context.Background(), term, le, fake, dir)
	if err == nil {
		t.Fatal("expected an error")
	}
	if fake.createKeyPairCalls != 1 {
		t.Errorf("CreateKeyPair calls = %d, want 1 (should not retry a non-duplicate error)", fake.createKeyPairCalls)
	}
}

func TestPromptKeyPairNameOrCreate_FallsBackToFreeTextWhenListErrors(t *testing.T) {
	fake := &fakeEC2Client{describeKeyPairsErr: errors.New("access denied")}
	term, le, _ := newPipeEditor(t, "some-existing-key\n")

	got, err := promptKeyPairNameOrCreate(context.Background(), term, le, fake, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "some-existing-key" {
		t.Errorf("got %q, want %q", got, "some-existing-key")
	}
}

func TestPromptKeyPairNameOrCreate_FreeTextFallbackStillSupportsNewKeyword(t *testing.T) {
	fake := &fakeEC2Client{describeKeyPairsErr: errors.New("access denied")}
	term, le, _ := newPipeEditor(t, "new\nmy-fresh-key\n")

	got, err := promptKeyPairNameOrCreate(context.Background(), term, le, fake, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "my-fresh-key" {
		t.Errorf("got %q, want %q", got, "my-fresh-key")
	}
}

func TestLooksLikeKeyFilename(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"existing-key", false},
		{"etd-ami-test", false},
		{"etd-ami-test.pem", true},
		{"etd-ami-test.PEM", true},
		{"my-key.ppk", true},
		{"my-key.key", true},
		{"my-key.txt", false},
		{"~/.ssh/etd-ami-test.pem", true},
		{"~", true},
		{"keys/my-key", true},
		{"./relative/my-key", true},
		{"/absolute/my-key", true},
	}
	for _, c := range cases {
		if got := looksLikeKeyFilename(c.in); got != c.want {
			t.Errorf("looksLikeKeyFilename(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestKeyPairNameFromFilePath_DerivesNameFromReadableFile(t *testing.T) {
	dir := t.TempDir()
	fullPath := filepath.Join(dir, "etd-ami-test.pem")
	if err := os.WriteFile(fullPath, []byte("fake key"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	got, err := keyPairNameFromFilePath(fullPath, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "etd-ami-test" {
		t.Errorf("got %q, want %q", got, "etd-ami-test")
	}
}

func TestKeyPairNameFromFilePath_FallsBackToKeyDirForBareFilename(t *testing.T) {
	keyDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(keyDir, "etd-ami-test.pem"), []byte("fake key"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	// "etd-ami-test.pem" has no directory component and doesn't exist
	// relative to cwd -- keyPairNameFromFilePath should also check keyDir
	// (where this tool's own Create Key Pair saves keys) before giving up.
	got, err := keyPairNameFromFilePath("etd-ami-test.pem", keyDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "etd-ami-test" {
		t.Errorf("got %q, want %q", got, "etd-ami-test")
	}
}

func TestKeyPairNameFromFilePath_ExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "etd-ami-test.pem"), []byte("fake key"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	got, err := keyPairNameFromFilePath("~/.ssh/etd-ami-test.pem", t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "etd-ami-test" {
		t.Errorf("got %q, want %q", got, "etd-ami-test")
	}
}

func TestKeyPairNameFromFilePath_ErrorsWhenUnreadableEverywhere(t *testing.T) {
	_, err := keyPairNameFromFilePath("/no/such/file.pem", t.TempDir())
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestPromptKeyPairNameOrCreate_DerivesNameFromFullPath(t *testing.T) {
	dir := t.TempDir()
	fullPath := filepath.Join(dir, "etd-ami-test.pem")
	if err := os.WriteFile(fullPath, []byte("fake key"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	fake := &fakeEC2Client{describeKeyPairsErr: errors.New("access denied")} // free-text fallback path
	term, le, buf := newPipeEditor(t, fullPath+"\n")

	got, err := promptKeyPairNameOrCreate(context.Background(), term, le, fake, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "etd-ami-test" {
		t.Errorf("got %q, want %q", got, "etd-ami-test")
	}
	if !strings.Contains(buf.String(), "etd-ami-test") {
		t.Errorf("expected the derived name to be echoed in output, got:\n%s", buf.String())
	}
}

func TestPromptKeyPairNameOrCreate_DerivesNameFromBareFilenameViaKeyDir(t *testing.T) {
	keyDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(keyDir, "etd-ami-test.pem"), []byte("fake key"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	fake := &fakeEC2Client{describeKeyPairsErr: errors.New("access denied")} // free-text fallback path
	term, le, _ := newPipeEditor(t, "etd-ami-test.pem\n")

	got, err := promptKeyPairNameOrCreate(context.Background(), term, le, fake, keyDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "etd-ami-test" {
		t.Errorf("got %q, want %q", got, "etd-ami-test")
	}
}

func TestPromptKeyPairNameOrCreate_RetriesWhenKeyFileUnreadable(t *testing.T) {
	fake := &fakeEC2Client{describeKeyPairsErr: errors.New("access denied")} // free-text fallback path
	term, le, buf := newPipeEditor(t, "/no/such/file.pem\nexisting-key\n")

	got, err := promptKeyPairNameOrCreate(context.Background(), term, le, fake, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "existing-key" {
		t.Errorf("got %q, want %q", got, "existing-key")
	}
	if !strings.Contains(buf.String(), "invalid input") {
		t.Errorf("expected an invalid-input message in output, got:\n%s", buf.String())
	}
}

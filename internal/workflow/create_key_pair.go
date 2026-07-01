package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/ui"
)

// createKeyPair calls ec2:CreateKeyPair for name (ED25519, PEM format --
// see DECISIONS.md, "Support creating a new key pair from within
// awsops") and saves the returned private key material to
// keyDir/<name>.pem with 0600 permissions, since AWS never stores or
// re-displays it. keyDir is created if it doesn't exist. Returns the
// saved file's path.
func createKeyPair(ctx context.Context, client awsclient.EC2API, name, keyDir string) (string, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()

	out, err := client.CreateKeyPair(ctx, &ec2.CreateKeyPairInput{
		KeyName:   aws.String(name),
		KeyType:   types.KeyTypeEd25519,
		KeyFormat: types.KeyFormatPem,
	})
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", keyDir, err)
	}
	path := filepath.Join(keyDir, name+".pem")
	if err := os.WriteFile(path, []byte(aws.ToString(out.KeyMaterial)), 0o600); err != nil {
		return "", fmt.Errorf("saving private key to %s: %w", path, err)
	}
	return path, nil
}

func isDuplicateKeyPairError(err error) bool {
	apiErr, ok := errors.AsType[smithy.APIError](err)
	return ok && apiErr.ErrorCode() == "InvalidKeyPair.Duplicate"
}

// promptKeyPairNameOrCreate prompts for a key pair name, offering "new"
// as a keyword to create a fresh key pair via createKeyPair instead of
// naming an existing one (see DECISIONS.md, "Support creating a new key
// pair from within awsops" -- an operator who doesn't want to reuse
// keys across instances shouldn't have to leave awsops to create one).
// A name collision (AWS's own InvalidKeyPair.Duplicate) re-prompts for
// a different new name; any other CreateKeyPair error is returned as a
// real error, matching every other unrecoverable-here AWS failure.
func promptKeyPairNameOrCreate(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, client awsclient.EC2API, keyDir string) (string, error) {
	raw, err := ui.Prompt(t, le, "Key pair name (the name registered in AWS, not a local file path; type 'new' to create one; e.g. my-laptop-key; see EC2 console > Key Pairs)", ui.WithValidator(requireNonEmpty))
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(strings.TrimSpace(raw), "new") {
		return raw, nil
	}

	for {
		name, err := ui.Prompt(t, le, "New key pair name", ui.WithValidator(requireNonEmpty))
		if err != nil {
			return "", err
		}

		path, err := createKeyPair(ctx, client, name, keyDir)
		if err != nil {
			if isDuplicateKeyPairError(err) {
				t.Printf("invalid input: a key pair named %q already exists -- choose a different name\n", name)
				t.Refresh()
				continue
			}
			return "", err
		}

		t.Printf("Created key pair %q (ED25519); private key saved to %s (mode 0600)\n", name, path)
		t.Refresh()
		return name, nil
	}
}

// sshKeyDir returns the directory new key pairs' private key material
// is saved to (~/.ssh), falling back to a cwd-relative ".ssh" if the
// home directory can't be resolved rather than failing the whole
// launch flow over it.
func sshKeyDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".ssh"
	}
	return filepath.Join(home, ".ssh")
}

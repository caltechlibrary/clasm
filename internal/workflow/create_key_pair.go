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

// keyPairChoice is one entry in promptKeyPairNameOrCreate's pick list:
// either an existing key pair in the AMI's region, or "Create new key
// pair".
type keyPairChoice struct {
	label     string
	name      string
	createNew bool
}

func keyPairChoiceLabel(c keyPairChoice) string { return c.label }

// promptKeyPairNameOrCreate lists key pairs in client's region and lets
// the operator pick one, plus "Create new key pair" (see DECISIONS.md,
// "Validate key pair name against the AMI's region"). Unlike Security
// group IDs/Subnet ID, there's no "Other: type a name" escape hatch --
// ec2:DescribeKeyPairs is a complete, small, fully-enumerable list for
// this region (key pairs, unlike AMIs or instance types, have no
// "public"/cross-account concept to escape to), so a name it doesn't
// know about is guaranteed not to work here. Falls back entirely to the
// original free-text prompt (with its "new" keyword and key-file-path
// auto-detection) only if the list itself can't be fetched (e.g.
// missing ec2:DescribeKeyPairs permission) -- in which case there's
// nothing more reliable to offer than free text.
func promptKeyPairNameOrCreate(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, client awsclient.EC2API, keyDir string) (string, error) {
	keyPairs, err := listKeyPairs(ctx, client)
	if err != nil {
		return promptKeyPairNameFreeText(ctx, t, le, client, keyDir)
	}
	if len(keyPairs) == 0 {
		t.Println("No key pairs found in this region.")
		t.Refresh()
	}

	choices := make([]keyPairChoice, 0, len(keyPairs)+1)
	for _, name := range keyPairs {
		choices = append(choices, keyPairChoice{label: name, name: name})
	}
	choices = append(choices, keyPairChoice{label: "Create new key pair", createNew: true})

	picked, err := ui.PickList(t, le, choices, keyPairChoiceLabel, "Select a key pair")
	if err != nil {
		return "", err
	}
	if picked.createNew {
		return createNewKeyPairInteractive(ctx, t, le, client, keyDir)
	}
	return picked.name, nil
}

// promptKeyPairNameFreeText is the original free-text key pair prompt:
// "new" creates a fresh key pair via createKeyPair instead of naming an
// existing one (see DECISIONS.md, "Support creating a new key pair from
// within awsops"); a value that looks like a private key filename or
// path -- real-AWS testing showed this happens even with the prompt's
// own warning, presumably from `ssh -i` muscle memory -- is validated as
// readable and the AWS key pair name is derived from it instead (see
// DECISIONS.md, "Derive the AWS key pair name from a private key
// filename/path"). Kept as promptKeyPairNameOrCreate's fallback for when
// the region's key pairs can't be listed at all.
func promptKeyPairNameFreeText(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, client awsclient.EC2API, keyDir string) (string, error) {
	for {
		raw, err := ui.Prompt(t, le, "Key pair name (the name registered in AWS, not a local file path; type 'new' to create one; e.g. my-laptop-key; see EC2 console > Key Pairs)", ui.WithValidator(requireNonEmpty))
		if err != nil {
			return "", err
		}
		raw = strings.TrimSpace(raw)

		if strings.EqualFold(raw, "new") {
			return createNewKeyPairInteractive(ctx, t, le, client, keyDir)
		}

		if !looksLikeKeyFilename(raw) {
			return raw, nil
		}

		name, err := keyPairNameFromFilePath(raw, keyDir)
		if err != nil {
			t.Printf("invalid input: %v -- enter the AWS key pair name instead (see EC2 console > Key Pairs)\n", err)
			t.Refresh()
			continue
		}

		t.Printf("%q looks like a private key file -- using AWS key pair name %q (derived from its filename)\n", raw, name)
		t.Refresh()
		return name, nil
	}
}

// createNewKeyPairInteractive is promptKeyPairNameOrCreate's "new"
// sub-flow, extracted so the outer function can also loop on a bad
// key-filename input without duplicating this retry logic.
func createNewKeyPairInteractive(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, client awsclient.EC2API, keyDir string) (string, error) {
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

// keyFileExtensions are the private-key file extensions
// looksLikeKeyFilename recognizes even without a path separator (e.g.
// "etd-ami-test.pem" typed with no directory).
var keyFileExtensions = map[string]bool{".pem": true, ".ppk": true, ".key": true}

// looksLikeKeyFilename reports whether s looks like a private key file
// path (contains a path separator or starts with "~") or a bare private
// key filename (ends in a recognized key extension), as opposed to a
// bare AWS key pair name.
func looksLikeKeyFilename(s string) bool {
	if strings.Contains(s, "/") || strings.HasPrefix(s, "~") {
		return true
	}
	return keyFileExtensions[strings.ToLower(filepath.Ext(s))]
}

// keyPairNameFromFilePath validates that path -- a private key file the
// operator apparently typed instead of an AWS key pair name -- is
// actually readable, then derives the AWS key pair name from its
// filename: this tool's own Create Key Pair (createKeyPair) always
// saves to exactly keyDir/<name>.pem, so the filename reliably encodes
// the real name regardless of what directory the operator typed (or
// omitted). "~" is expanded against the user's home directory. If path
// has no directory component and isn't readable as given (e.g. relative
// to the current directory), keyDir is also checked before giving up,
// since a bare filename most plausibly refers to a key this tool itself
// saved there.
func keyPairNameFromFilePath(path, keyDir string) (string, error) {
	candidate := path
	if strings.HasPrefix(candidate, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving home directory for %q: %w", path, err)
		}
		candidate = filepath.Join(home, strings.TrimPrefix(candidate, "~"))
	}

	if !isReadableFile(candidate) {
		if filepath.Dir(path) != "." {
			return "", fmt.Errorf("cannot read key file %q", path)
		}
		fallback := filepath.Join(keyDir, path)
		if !isReadableFile(fallback) {
			return "", fmt.Errorf("cannot read key file %q (also checked %q)", path, fallback)
		}
		candidate = fallback
	}

	base := filepath.Base(candidate)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	if name == "" {
		return "", fmt.Errorf("cannot derive a key pair name from %q", path)
	}
	return name, nil
}

func isReadableFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	f.Close()
	return true
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

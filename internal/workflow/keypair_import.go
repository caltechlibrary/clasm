package workflow

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/ui"
)

// sshPublicKeyPrefixes are the key-type tokens a well-formed
// authorized_keys-style public key line starts with -- enough to catch
// "this obviously isn't a public key" (a private key, a random text
// file, an empty file) locally, before ever calling AWS (DESIGN.md,
// Feature 15). Not a full RFC4253 parse.
var sshPublicKeyPrefixes = []string{
	"ssh-ed25519",
	"ssh-rsa",
	"ecdsa-sha2-nistp256",
	"ecdsa-sha2-nistp384",
	"ecdsa-sha2-nistp521",
}

// validatePublicKeyFile checks that path (after expanding a leading "~",
// the same convention every other key-file prompt in this tool follows)
// is readable and its contents look like a well-formed SSH public key (a
// recognized key-type prefix followed by a base64 body), so a malformed
// file fails locally with a clear message instead of surfacing AWS's raw
// InvalidKeyPair.Format error.
func validatePublicKeyFile(path string) ([]byte, error) {
	expanded, err := expandHome(path)
	if err != nil {
		return nil, err
	}
	if !isReadableFile(expanded) {
		return nil, fmt.Errorf("cannot read %q", path)
	}
	data, err := os.ReadFile(expanded)
	if err != nil {
		return nil, fmt.Errorf("cannot read %q: %w", path, err)
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return nil, fmt.Errorf("%q does not look like a public key file (expected \"<key-type> <base64-key> [comment]\")", path)
	}
	if !slices.Contains(sshPublicKeyPrefixes, fields[0]) {
		return nil, fmt.Errorf("%q does not start with a recognized public key type (expected one of %s) -- if this is a private key, derive its public half with: ssh-keygen -y -f %s > file.pub", path, strings.Join(sshPublicKeyPrefixes, ", "), path)
	}
	return data, nil
}

// ImportKeyPairStandalone runs the Key Management domain's "Import Key
// Pair" workflow (DESIGN.md, Feature 15): register an existing public
// key with AWS instead of generating a new one. Unlike Create Key Pair,
// there's no private key material to save -- ec2:ImportKeyPair never
// returns one, since AWS never sees the private half.
func ImportKeyPairStandalone(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, clients map[string]awsclient.EC2API) error {
	region, err := promptRegion(t, le, clients)
	if err != nil {
		return cancelledIsNil(t, err)
	}
	client, err := resolveEC2(clients, region)
	if err != nil {
		return err
	}

	for {
		name, err := ui.Prompt(t, le, "New key pair name", ui.WithValidator(requireNonEmpty))
		if err != nil {
			return err
		}

		// The public key file gets its own validator-driven retry (via
		// ui.Prompt), scoped to just this one prompt -- a bad path or a
		// malformed file re-asks only for the file, not the name too.
		// validatePublicKeyFile's own error message covers the
		// not-a-private-key/derive-with-ssh-keygen guidance when the
		// operator gets the file type wrong; the prompt itself only
		// needs a short example.
		var publicKey []byte
		_, err = ui.Prompt(t, le, "Public key file path (.pub -- e.g. ~/.ssh/id_ed25519.pub)", ui.WithValidator(func(raw string) error {
			data, verr := validatePublicKeyFile(strings.TrimSpace(raw))
			if verr != nil {
				return verr
			}
			publicKey = data
			return nil
		}))
		if err != nil {
			return err
		}

		_, err = client.ImportKeyPair(ctx, &ec2.ImportKeyPairInput{
			KeyName:           aws.String(name),
			PublicKeyMaterial: publicKey,
		})
		if err != nil {
			if isDuplicateKeyPairError(err) {
				t.Printf("invalid input: a key pair named %q already exists -- choose a different name\n", name)
				t.Refresh()
				continue
			}
			return err
		}

		t.Printf("Imported key pair %q\n", name)
		t.Refresh()
		return nil
	}
}

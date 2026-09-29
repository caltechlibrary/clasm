package workflow

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// ensureDirModePattern accepts the octal modes install -m takes as a
// number: three or four octal digits. Symbolic modes are refused, since
// every caller passes a fixed constant and a pattern this narrow leaves
// nothing to inject.
var ensureDirModePattern = regexp.MustCompile(`^[0-7]{3,4}$`)

// buildEnsureDirCommand builds the command that creates dir, or repairs it
// if it already exists, with the given owner and mode (DR-0176 decision 4).
//
// `install -d` is create-or-repair and idempotent, and it acts on the
// directory itself only. That is the whole point of choosing it over a
// mkdir/chown/chmod chain: this step runs on directories that already hold
// backups, so it must never recurse and never remove, and DR-0176 rejected
// both by name. A test pins the command text against `-R`, `rm` and
// friends. Anything below the directory is a separate, explicit step
// (buildChownTreeCommand), taken only where a decision says so.
//
// Parent directories, if missing, are created by install with its defaults
// (root, 0755) rather than the owner given here; only dir itself is owned.
func buildEnsureDirCommand(dir string, owner ServiceOwner, mode string) string {
	return fmt.Sprintf("install -d -o %d -g %d -m %s %s", owner.UID, owner.GID, mode, shellQuote(dir))
}

// EnsureBackupDirectory runs buildEnsureDirCommand via SSM.
//
// A separate SSM round trip, for error attribution: this project has been
// bitten by compound remote commands hiding which half failed (DR-0175
// decision 1, DR-0176).
//
// It refuses two kinds of directory before sending anything, because the
// command runs as root and sets an owner: "/" (or anything that cleans to
// it), which would hand the filesystem root to the service account, and a
// relative path, which would land wherever SSM's working directory is.
// The directory is operator-typed at the backup-directory prompt, so both
// are one typo away.
func EnsureBackupDirectory(ctx context.Context, client awsclient.SSMAPI, instanceID, dir string, owner ServiceOwner, mode string, timeout, pollInterval time.Duration) error {
	if err := checkBackupDirectory(dir, instanceID); err != nil {
		return err
	}
	if !ensureDirModePattern.MatchString(mode) {
		return fmt.Errorf("refusing to set mode %q on %q: want three or four octal digits", mode, dir)
	}
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildEnsureDirCommand(dir, owner, mode), timeout, pollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return curlFailureError(fmt.Sprintf("creating or repairing the backup directory %q on %s failed", dir, instanceID), status, stdout)
	}
	return nil
}

// checkBackupDirectory is the refusal shared by every step here that runs as
// root and sets an owner on an operator-typed directory: "/" (or anything that
// cleans to it) and relative paths.
func checkBackupDirectory(dir, instanceID string) error {
	if !strings.HasPrefix(dir, "/") || path.Clean(dir) == "/" {
		return fmt.Errorf("refusing to set ownership of %q on %s: want an absolute path to a backup directory, not the filesystem root", dir, instanceID)
	}
	return nil
}

// ChownBackupDirectory hands everything under dir to owner via SSM
// (`chown -R`), as its own step. Unlike EnsureBackupDirectory this *does*
// recurse, and it is only called where a decision says so: after a dump, so
// that the new file -- root-owned, because SSM runs as root -- and any
// root-owned dumps an older clasm left behind end up the service user's
// (DR-0176 decisions 2 and 3). It changes ownership only; it never deletes.
//
// Refuses the same directories EnsureBackupDirectory does, and for the same
// reason, before sending anything: `chown -R /` as root is the worst command
// this package could send.
func ChownBackupDirectory(ctx context.Context, client awsclient.SSMAPI, instanceID, dir string, owner ServiceOwner, timeout, pollInterval time.Duration) error {
	if err := checkBackupDirectory(dir, instanceID); err != nil {
		return err
	}
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildChownTreeCommand(dir, owner), timeout, pollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return curlFailureError(fmt.Sprintf("setting ownership of the backup directory %q on %s failed", dir, instanceID), status, stdout)
	}
	return nil
}

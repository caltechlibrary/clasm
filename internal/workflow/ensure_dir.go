package workflow

import (
	"context"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
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

// buildCreateDirIfMissingCommand builds the command that creates dir with the
// given owner, group and mode if -- and only if -- it does not exist. An
// existing directory is not touched at all: not its owner, not its group, not
// its mode. That is the difference from buildEnsureDirCommand, which repairs.
//
// The SQL backup directory is created, never repaired, because what already
// exists there is the site's own arrangement and clasm cannot see who relies
// on it. The cron that writes the dumps runs as whichever account the site
// chose -- rsdoiel, through the www-data group, on caltechauthors-v13 and
// new-data -- so re-owning the directory to ubuntu:ubuntu 0750 locked that
// account out and stopped caltechauthors-v13's dumps (2026-09-30; DR-0183).
// group is a name, not a number, because www-data's gid is not clasm's to know.
func buildCreateDirIfMissingCommand(dir string, uid int, group, mode string) string {
	q := shellQuote(dir)
	return fmt.Sprintf("[ -d %s ] || install -d -o %d -g %s -m %s %s", q, uid, shellQuote(group), mode, q)
}

// CreateBackupDirectoryIfMissing runs buildCreateDirIfMissingCommand via SSM, as
// its own step, with the same refusals EnsureBackupDirectory makes (the
// filesystem root, a relative path) before anything is sent.
func CreateBackupDirectoryIfMissing(ctx context.Context, client awsclient.SSMAPI, instanceID, dir string, uid int, group, mode string, timeout, pollInterval time.Duration) error {
	if err := checkBackupDirectory(dir, instanceID); err != nil {
		return err
	}
	if !ensureDirModePattern.MatchString(mode) {
		return fmt.Errorf("refusing to set mode %q on %q: want three or four octal digits", mode, dir)
	}
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildCreateDirIfMissingCommand(dir, uid, group, mode), timeout, pollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return curlFailureError(fmt.Sprintf("creating the backup directory %q on %s failed", dir, instanceID), status, stdout)
	}
	return nil
}

// buildHandOverDumpCommand builds the command that makes one new dump usable by
// the account that writes the next one: owned by the service user, in the
// directory's own group, group-writable (0664, the mode the cron script's dumps
// have). SSM runs as root, so a dump clasm makes is otherwise root-owned and the
// cron script's same-day redirect onto it is refused. It names the one file and
// recurses into nothing: the directory, and every dump already in it, are left
// exactly as they were. The group is read from the directory at run time, so
// whatever group the site chose for it is the group the dump gets.
func buildHandOverDumpCommand(dir, dumpFile string, uid int) string {
	return fmt.Sprintf("set -e; chown %d:\"$(stat -c %%g %s)\" %s; chmod 0664 %s",
		uid, shellQuote(dir), shellQuote(dumpFile), shellQuote(dumpFile))
}

// HandOverDump runs buildHandOverDumpCommand via SSM, as its own step.
func HandOverDump(ctx context.Context, client awsclient.SSMAPI, instanceID, dir, dumpFile string, uid int, timeout, pollInterval time.Duration) error {
	if err := checkBackupDirectory(dir, instanceID); err != nil {
		return err
	}
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildHandOverDumpCommand(dir, dumpFile, uid), timeout, pollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return curlFailureError(fmt.Sprintf("handing the new dump in %q over to the service user on %s failed", dir, instanceID), status, stdout)
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

// ownershipProbeTag marks the probe's one line of output, so a parse does not
// depend on anything else the remote shell happens to print.
const ownershipProbeTag = "clasm-owner-probe"

// buildOwnershipProbeCommand builds the read-only command that looks at a
// repository directory before it is repaired (DR-0179): how many entries below
// the top level are not owned by the service user, and whether the directory
// looks like an OpenSearch snapshot repository -- an index.latest file or an
// indices directory, the two things OpenSearch itself creates there. It prints
// one line, "clasm-owner-probe <count> <yes|no>", and changes nothing: no
// chown, no chmod, no delete, no redirect into the tree.
func buildOwnershipProbeCommand(dir string, owner ServiceOwner) string {
	q := shellQuote(dir)
	return fmt.Sprintf(`n=$(find %s -mindepth 1 ! -user %d | wc -l | tr -d ' '); if [ -e %s/index.latest ] || [ -d %s/indices ]; then m=yes; else m=no; fi; echo "%s $n $m"`,
		q, owner.UID, q, q, ownershipProbeTag)
}

// parseOwnershipProbe reads buildOwnershipProbeCommand's output. known is
// false when no well-formed probe line is present, so an unreadable answer is
// never taken for "nothing to repair" or "not a repository".
func parseOwnershipProbe(stdout string) (n int, looksLikeRepo, known bool) {
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != ownershipProbeTag {
			continue
		}
		count, err := strconv.Atoi(fields[1])
		if err != nil || count < 0 {
			return 0, false, false
		}
		switch fields[2] {
		case "yes":
			return count, true, true
		case "no":
			return count, false, true
		}
		return 0, false, false
	}
	return 0, false, false
}

// RepairSnapshotRepoOwnership hands everything under an OpenSearch snapshot
// repository directory to the service user when entries below its top level
// are owned by someone else (DR-0179). EnsureBackupDirectory repairs the top
// level only, by design (DR-0176); a tree left root-owned below it -- by a
// clasm that predates DR-0175, or anything that wrote there as root -- passes
// that step and then fails in the search container, after a multi-minute
// snapshot, with an AccessDeniedException. This finds it first.
//
// It looks before it touches (one read-only SSM call), does nothing when the
// count is zero, and refuses to recurse across a directory that does not look
// like a snapshot repository: the path is operator-typed, and a recursive
// chown of a mistyped /opt or /home is exactly the mistake the guard exists
// for. The repair itself is ChownBackupDirectory, as its own SSM step. It asks
// no confirmation: it changes ownership only, is idempotent, and is what
// Restore already does unprompted. An unreadable probe warns and proceeds,
// since the repair is a courtesy and the archive reports its own failures.
func RepairSnapshotRepoOwnership(ctx context.Context, w io.Writer, client awsclient.SSMAPI, instanceID, dir string, owner ServiceOwner, timeout, pollInterval time.Duration) error {
	if err := checkBackupDirectory(dir, instanceID); err != nil {
		return err
	}
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildOwnershipProbeCommand(dir, owner), timeout, pollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return curlFailureError(fmt.Sprintf("checking the ownership of the backup directory %q on %s failed", dir, instanceID), status, stdout)
	}
	n, looksLikeRepo, known := parseOwnershipProbe(stdout)
	if !known {
		fmt.Fprintf(w, "warning: could not read the ownership of %s on %s; skipping the ownership repair.\n", dir, instanceID)
		return nil
	}
	if n == 0 {
		return nil
	}
	noun := "entries"
	if n == 1 {
		noun = "entry"
	}
	if !looksLikeRepo {
		return fmt.Errorf("the backup directory %q on %s holds %d %s not owned by the service user, but it does not look like an OpenSearch snapshot repository (no index.latest file, no indices directory), so clasm will not change ownership recursively across it; if that really is the directory, fix its ownership by hand", dir, instanceID, n, noun)
	}
	if err := ChownBackupDirectory(ctx, client, instanceID, dir, owner, timeout, pollInterval); err != nil {
		return err
	}
	fmt.Fprintf(w, "Repaired ownership of %d %s under %s (not owned by uid %d, left over from an earlier run).\n", n, noun, dir, owner.UID)
	return nil
}

package workflow

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// DefaultRDMServiceUser is the account that owns /Sites/<repo> and runs
// invenio-cli and the RDM services on every instance clasm manages, and so
// the account that must own everything clasm writes there (DR-0176 decision
// 1). SSM runs commands as root, so without this anything clasm creates is
// root-owned and the service account cannot write to it afterwards.
//
// A constant rather than configuration, for the reason DR-0175 gave for the
// OpenSearch uid: the standard is stated as "ubuntu" everywhere, and a knob
// is speculative until a deployment differs.
const DefaultRDMServiceUser = "ubuntu"

// DefaultOwnershipTimeout bounds the short ownership steps: the owner lookup,
// the ensure-directory step, and a recursive chown over a directory of dumps.
// None is slow, but a chown -R walks every entry, so it gets headroom.
const DefaultOwnershipTimeout = 5 * time.Minute

// ServiceOwner is DefaultRDMServiceUser's numeric identity on one instance.
//
// The uid and gid are resolved on the instance and kept separate because
// they are not the same number: on the current RDM images ubuntu is uid
// 1000 but gid 1001, and gid 1000 is the docker group. Treating one number
// as both is what left the OpenSearch repository owned by ubuntu:docker.
type ServiceOwner struct {
	UID, GID int
}

// buildResolveServiceOwnerCommand builds the command that prints "<uid>
// <gid>" for user, and exits non-zero if user does not exist.
//
// Two separate assignments under `set -e` on purpose, and for POSIX sh
// (SSM send-command runs under /bin/sh): the tempting one-liner
// `echo "$(id -u u) $(id -g u)"` exits 0 and prints a bare space when the
// user is missing, because echo's own status is what counts -- a missing
// service user would then look like two empty ids rather than a failed
// step. A plain assignment takes the status of its command substitution,
// so `set -e` sees the failure and stops before anything is printed.
func buildResolveServiceOwnerCommand(user string) string {
	u := shellQuote(user)
	return fmt.Sprintf("set -e; u=$(id -u %s); g=$(id -g %s); echo \"$u $g\"", u, u)
}

// ResolveServiceOwner looks up DefaultRDMServiceUser's uid and gid on
// instanceID via SSM. Read-only.
func ResolveServiceOwner(ctx context.Context, client awsclient.SSMAPI, instanceID string, timeout, pollInterval time.Duration) (ServiceOwner, error) {
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildResolveServiceOwnerCommand(DefaultRDMServiceUser), timeout, pollInterval)
	if err != nil {
		return ServiceOwner{}, err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return ServiceOwner{}, curlFailureError(fmt.Sprintf("looking up the uid and gid of the %q service user on %s failed", DefaultRDMServiceUser, instanceID), status, stdout)
	}
	fields := strings.Fields(stdout)
	if len(fields) != 2 {
		return ServiceOwner{}, fmt.Errorf("looking up the %q service user on %s returned %q, want \"<uid> <gid>\"", DefaultRDMServiceUser, instanceID, strings.TrimSpace(stdout))
	}
	uid, uerr := strconv.Atoi(fields[0])
	gid, gerr := strconv.Atoi(fields[1])
	if uerr != nil || gerr != nil || uid < 0 || gid < 0 {
		return ServiceOwner{}, fmt.Errorf("looking up the %q service user on %s returned %q, want \"<uid> <gid>\" as non-negative numbers", DefaultRDMServiceUser, instanceID, strings.TrimSpace(stdout))
	}
	return ServiceOwner{UID: uid, GID: gid}, nil
}

// CheckOwnerMatchesOpenSearch stops a workflow, before it writes anything,
// when the service user cannot also be the owner the search container
// writes as (DR-0176 decision 5).
//
// The snapshot repository directory has to be writable from inside the
// container, which runs as DefaultOpenSearchRepoUID, and manageable by the
// service account. One owner satisfies both only if the two uids are the
// same. When they are not, either choice silently breaks something -- the
// container's writes or the service account's reads -- and the operator is
// the one who knows which is intended, so this reports rather than picks.
//
// Only the uid is compared. The gid is deliberately ignored: ubuntu's gid is
// 1001 on the current images and the container's is 1000, and that is fine
// because the owner already has full rights.
func CheckOwnerMatchesOpenSearch(o ServiceOwner) error {
	if o.UID == DefaultOpenSearchRepoUID {
		return nil
	}
	return fmt.Errorf("the %q service user is uid %d on this instance, but the OpenSearch container writes as uid %d, so one owner cannot serve both and the snapshot repository would be unwritable by one of them; nothing was changed -- decide which account should own the OpenSearch backup directory, or make the two uids agree, and run this again", DefaultRDMServiceUser, o.UID, DefaultOpenSearchRepoUID)
}

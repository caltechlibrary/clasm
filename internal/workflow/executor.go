package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// CommandStatus is the terminal outcome of a command run through an Executor.
// Its values are the strings SSM reports, so any message that prints a status
// is unchanged by the Executor, and another implementation (Multipass) maps
// its own outcomes onto the same four.
type CommandStatus string

// The terminal outcomes an Executor reports.
const (
	StatusSuccess   CommandStatus = "Success"
	StatusFailed    CommandStatus = "Failed"
	StatusCancelled CommandStatus = "Cancelled"
	StatusTimedOut  CommandStatus = "TimedOut"
)

// Executor runs shell commands on one host (DR-0201). Everything in this
// package that used to take an awsclient.SSMAPI and an instance id takes an
// Executor instead, so the same workflows run on an EC2 instance (SSMExecutor)
// or a Multipass VM (a later phase).
type Executor interface {
	// Run runs command as root on the host and returns its stdout and its
	// terminal status. A command that exits non-zero is a non-Success status
	// with a nil error; an error means the command could not be run or did
	// not finish within timeout. pollInterval paces an implementation that
	// has to poll for completion.
	//
	// @param ctx {context.Context} cancels the call
	// @param command {string} the shell text to run
	// @param timeout {time.Duration} how long the command may take
	// @param pollInterval {time.Duration} how often to poll, where polling is needed
	// @returns {string} the command's standard output
	// @returns {CommandStatus} how the command ended
	// @returns {error} why it could not be run or finished, or nil
	Run(ctx context.Context, command string, timeout, pollInterval time.Duration) (stdout string, status CommandStatus, err error)

	// Put copies a local file to an absolute path on the host and verifies it
	// by checksum. The file lands owned by root with the local file's mode;
	// a caller that needs another owner changes it with Run.
	//
	// @param ctx {context.Context} cancels the copy
	// @param local {string} the local file to send
	// @param remote {string} the absolute destination path on the host
	// @returns {error} why the file did not arrive intact, or nil
	Put(ctx context.Context, local, remote string) error

	// Get copies a file from the host to a local path and verifies it by
	// checksum. Nothing is left at local if the copy fails.
	//
	// @param ctx {context.Context} cancels the copy
	// @param remote {string} the absolute path of the file on the host
	// @param local {string} where to write it locally
	// @returns {error} why the file did not arrive intact, or nil
	Get(ctx context.Context, remote, local string) error

	// Name is the host's instance id or name, for messages and logs.
	Name() string
}

// SSMExecutor is the Executor for an EC2 instance, over AWS Systems Manager.
// It is RunShellCommand bound to one client and one instance id, and behaves
// exactly as RunShellCommand does.
type SSMExecutor struct {
	client     awsclient.SSMAPI
	instanceID string
}

// NewSSMExecutor binds an SSM client and an instance id into an Executor.
//
// @param client {awsclient.SSMAPI} the SSM client in the instance's region
// @param instanceID {string} the EC2 instance id the commands run on
// @returns {*SSMExecutor} the executor
// @example
//
//	exec := NewSSMExecutor(ssmClient, "i-0123456789abcdef0")
//	out, status, err := exec.Run(ctx, "uname -a", time.Minute, DefaultSSMPollInterval)
func NewSSMExecutor(client awsclient.SSMAPI, instanceID string) *SSMExecutor {
	return &SSMExecutor{client: client, instanceID: instanceID}
}

// Run implements Executor by calling RunShellCommand.
//
// @param ctx {context.Context} cancels the call
// @param command {string} the shell text to run
// @param timeout {time.Duration} how long the command may take
// @param pollInterval {time.Duration} how often to poll SSM for completion
// @returns {string} the command's standard output
// @returns {CommandStatus} how the command ended
// @returns {error} why it could not be run or finished, or nil
func (e *SSMExecutor) Run(ctx context.Context, command string, timeout, pollInterval time.Duration) (string, CommandStatus, error) {
	stdout, status, err := RunShellCommand(ctx, e.client, e.instanceID, command, timeout, pollInterval)
	return stdout, CommandStatus(status), err
}

// Name implements Executor and returns the instance id.
//
// @returns {string} the EC2 instance id
func (e *SSMExecutor) Name() string { return e.instanceID }

// maxSSMPutBytes is the largest file SSMExecutor.Put will send. A file travels
// inside the command text, which SSM limits; larger files move through S3.
const maxSSMPutBytes = 60 * 1024

// defaultSSMPutTimeout bounds the one SSM command that writes a small file.
const defaultSSMPutTimeout = 2 * time.Minute

// Put implements Executor for small files, carried base64-encoded in the
// command, and verifies the written file by checksum.
//
// @param ctx {context.Context} cancels the call
// @param local {string} the local file to send, at most 60 KB
// @param remote {string} the absolute destination path on the instance
// @returns {error} why the file did not arrive intact, or nil
func (e *SSMExecutor) Put(ctx context.Context, local, remote string) error {
	if !path.IsAbs(remote) {
		return fmt.Errorf("SSMExecutor.Put: the destination must be an absolute path, got %q", remote)
	}
	info, err := os.Stat(local)
	if err != nil {
		return fmt.Errorf("SSMExecutor.Put: %w", err)
	}
	if info.Size() > maxSSMPutBytes {
		return fmt.Errorf("SSMExecutor.Put: %s is %d bytes, over the %d-byte limit for a file carried in an SSM command; put it in S3 and download it on the instance", local, info.Size(), maxSSMPutBytes)
	}
	content, err := os.ReadFile(local)
	if err != nil {
		return fmt.Errorf("SSMExecutor.Put: %w", err)
	}
	sum := sha256.Sum256(content)
	want := hex.EncodeToString(sum[:])
	command := fmt.Sprintf("set -e\nmkdir -p %[1]s\nbase64 -d > %[2]s <<'__CLASM_B64__'\n%[3]s\n__CLASM_B64__\nchmod %[4]s %[2]s\nsha256sum %[2]s\n",
		shellQuote(path.Dir(remote)), shellQuote(remote), base64.StdEncoding.EncodeToString(content), fmt.Sprintf("%04o", info.Mode().Perm()))
	stdout, status, err := e.Run(ctx, command, defaultSSMPutTimeout, DefaultSSMPollInterval)
	if err != nil {
		return fmt.Errorf("copying %s to %s on %s: %w", local, remote, e.instanceID, err)
	}
	if status != StatusSuccess {
		return fmt.Errorf("copying %s to %s on %s failed (%s): %s", local, remote, e.instanceID, status, strings.TrimSpace(stdout))
	}
	if fields := strings.Fields(stdout); len(fields) == 0 || fields[0] != want {
		return fmt.Errorf("checksum mismatch copying %s to %s on %s: sent %s, the instance reports %q", local, remote, e.instanceID, want, strings.TrimSpace(stdout))
	}
	return nil
}

// Get implements Executor by refusing: SSM cannot carry a file back, since a
// command's output is limited to 24,000 characters.
//
// @param ctx {context.Context} unused
// @param remote {string} unused
// @param local {string} unused
// @returns {error} always an error saying to use S3
func (e *SSMExecutor) Get(ctx context.Context, remote, local string) error {
	return errors.New("SSMExecutor.Get: a file cannot be fetched over SSM (a command's output is limited to 24,000 characters); write it to S3 from the instance and download it from there")
}

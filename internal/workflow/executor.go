package workflow

import (
	"context"
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

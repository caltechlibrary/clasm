package workflow

import "github.com/caltechlibrary/clasm/internal/executor"

// The executors live in internal/executor (DR-0201, phase C1) so clmpm can
// use them without the AWS workflow code. These aliases keep the names this
// package's workflows already use; they are the same types, not copies.
type (
	// Executor is executor.Executor.
	Executor = executor.Executor
	// CommandStatus is executor.CommandStatus.
	CommandStatus = executor.CommandStatus
	// SSMExecutor is executor.SSMExecutor.
	SSMExecutor = executor.SSMExecutor
)

// The terminal outcomes an Executor reports.
const (
	StatusSuccess   = executor.StatusSuccess
	StatusFailed    = executor.StatusFailed
	StatusCancelled = executor.StatusCancelled
	StatusTimedOut  = executor.StatusTimedOut
)

// NewSSMExecutor is executor.NewSSMExecutor.
var NewSSMExecutor = executor.NewSSMExecutor

func shellQuote(s string) string { return executor.ShellQuote(s) }

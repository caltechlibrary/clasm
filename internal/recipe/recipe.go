// Package recipe runs an upgrade recipe (DR-0202): a directory of numbered
// shell scripts, each with a small header, on one host through an
// executor.Executor. It orders the steps, validates parameters, gates the steps
// that change production data, detaches the long ones, resumes after a failure
// and writes one JSON line per step.
package recipe

import (
	"errors"
	"io/fs"
	"time"
)

// DefaultTimeout is a step's timeout when its header has no "# timeout:" line.
const DefaultTimeout = 600 * time.Second

// DefaultRemoteDir is where the runner keeps a run's files on the host.
const DefaultRemoteDir = "/var/tmp/clasm-recipe"

// Step is one script of a recipe with its parsed header.
type Step struct {
	File     string        // base file name, e.g. 10_load.sh
	Name     string        // the "# step:" name
	Changes  []string      // nothing, or one or more of database, indices, host
	Timeout  time.Duration // the step's ceiling, detached or not
	Detached bool          // run in the background with a log and a finish marker
	User     string        // root or ubuntu
	Script   []byte        // the whole file
}

// Gated reports whether the step changes data a production gate protects: its
// "changes" list holds database or indices (DR-0202 point 7).
//
// @returns {bool} true when the step needs --confirm on a protected host
// @example
//
//	if step.Gated() { /* refuse without --confirm */ }
func (s Step) Gated() bool {
	for _, c := range s.Changes {
		if c == "database" || c == "indices" {
			return true
		}
	}
	return false
}

// Param is one NAME=value parameter exported to every step.
type Param struct{ Name, Value string }

// HeaderError is a recipe file whose header (or name) is wrong; it is content
// the tool reads, so the CLI exits 65.
type HeaderError struct {
	File string
	Msg  string
}

func (e *HeaderError) Error() string { return e.File + ": " + e.Msg }

// UsageError is a bad value on the command line: a malformed parameter, an
// unknown --from, a --confirm that is not the host. Nothing was attempted; the
// CLI exits 2.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// GateError is a refusal: a step that changes the database or indices on a
// protected host, and no --confirm. Nothing was attempted; the CLI exits 1.
type GateError struct {
	Step    string
	Changes []string
}

func (e *GateError) Error() string {
	return "step " + e.Step + " changes " + joinComma(e.Changes) + " on a production or staged host; pass --confirm with the instance id or name"
}

// StepError is a step that failed or timed out. The run stopped there;
// Resume is the --from value to continue from.
type StepError struct {
	Result StepResult
	Resume string
}

func (e *StepError) Error() string {
	return "step " + e.Result.Step + " failed (" + e.Result.ExitClass + "); fix it and resume with --from " + e.Resume
}

// StepResult is the one JSON line a step leaves.
type StepResult struct {
	Step      string         `json:"step"`
	File      string         `json:"file"`
	Start     time.Time      `json:"start"`
	Seconds   float64        `json:"seconds"`
	ExitClass string         `json:"exit_class"`
	ExitCode  int            `json:"exit_code"`
	Counts    map[string]any `json:"counts"`
}

func joinComma(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}

// HostError is a failure to reach or drive the host, not a failure of a step:
// the executor returned an error. The CLI exits 69.
type HostError struct {
	Step string
	Err  error
}

func (e *HostError) Error() string {
	return "step " + e.Step + ": the host could not be driven: " + e.Err.Error()
}

// Unwrap returns the executor's error.
func (e *HostError) Unwrap() error { return e.Err }

// Classify maps an error this package returns to its DR-0014 exit code and
// class name, so the clasm and clmpm front ends agree. ok is false for an error
// the package does not own, which the caller classifies itself.
//
// @param err {error} an error from Load, ParseParams or Run
// @returns {int} the exit code
// @returns {string} the class name
// @returns {bool} true when the package recognised the error
// @example
//
//	if code, class, ok := Classify(err); ok { fmt.Fprintln(os.Stderr, class); os.Exit(code) }
func Classify(err error) (code int, class string, ok bool) {
	var ue *UsageError
	var he *HeaderError
	var ge *GateError
	var hoe *HostError
	var se *StepError
	switch {
	case err == nil:
		return 0, "", false
	case errors.As(err, &ue):
		return 2, "usage", true
	case errors.As(err, &he):
		return 65, "data", true
	case errors.As(err, &ge):
		return 1, "negative", true
	case errors.As(err, &hoe):
		return 69, "unavailable", true
	case errors.Is(err, fs.ErrNotExist):
		return 66, "no_input", true
	case errors.As(err, &se):
		if se.Result.ExitClass == "timeout" {
			return 69, "unavailable", true
		}
		class = ExitClass(se.Result.ExitCode)
		if class == "internal" {
			return 70, class, true
		}
		return se.Result.ExitCode, class, true
	}
	return 0, "", false
}

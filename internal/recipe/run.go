package recipe

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/caltechlibrary/clasm/internal/executor"
)

// Options configures a run.
type Options struct {
	Params    []Param
	Apply     bool          // false: print the plan and run nothing
	From      string        // resume at this step name, "" for the first
	Protected bool          // the host is tagged production or staged (or is a Multipass VM)
	Confirm   []string      // the --confirm values given
	HostNames []string      // the host's id and name, any of which --confirm may equal
	RemoteDir string        // default DefaultRemoteDir
	RunID     string        // names this run's directory on the host; default from the clock
	Poll      time.Duration // how often a detached step is polled; default 5 s
	Now       func() time.Time
	Text      io.Writer // human progress, nil for none
	JSONL     io.Writer // one JSON line per finished step, nil for none
}

// The markers the runner's own commands print so it can read a step's result
// back out of the host's output (a command's output is all an Executor returns).
const (
	rcMarker      = "CLASM_RC="
	countsMarker  = "CLASM_COUNTS"
	runningMarker = "CLASM_RUNNING"
	logTailBytes  = 20000 // SSM returns at most 24,000 characters
	pollFailures  = 3     // consecutive failed polls tolerated before giving up on a detached step
	commandTime   = 2 * time.Minute
)

var (
	countLine = regexp.MustCompile(`^COUNT ([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
	safePath  = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)
	safeID    = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// exitClasses are the DR-0014 classes by exit code; a code outside the table is
// internal, as that record says of an error nothing classified.
var exitClasses = map[int]string{
	0: "ok", 1: "negative", 2: "usage", 65: "data", 66: "no_input", 69: "unavailable", 70: "internal",
	73: "cant_create", 74: "io", 75: "temp_fail", 77: "no_permission", 78: "config",
}

// ExitClass names the DR-0014 class of a step's exit code.
//
// @param code {int} the step's exit code
// @returns {string} the class name, internal for a code outside the table
// @example
//
//	ExitClass(65) // "data"
func ExitClass(code int) string {
	if c, ok := exitClasses[code]; ok {
		return c
	}
	return "internal"
}

// Run orders and runs a recipe on a host (DR-0202). Before it touches the host
// it validates everything it can: the --from step exists, every --confirm is
// the host's id or name, and, on a protected host, that no step it would run
// changes the database or indices unless a --confirm was given. Without Apply
// it then prints the plan and runs nothing. With it, each step is sent to the
// host, run (in the foreground, or detached and polled if its header says so),
// and reported as one JSON line; the run stops at the first failure.
//
// @param ctx {context.Context} cancels the run
// @param ex {executor.Executor} the host
// @param steps {[]Step} the recipe from Load
// @param opts {Options} the run's settings
// @returns {[]StepResult} one result per step that ran, the failed one included
// @returns {error} *UsageError, *GateError, *StepError, *HostError, or ctx's error
// @example
//
//	steps, _, _ := Load("upgrade/v13-to-v14")
//	results, err := Run(ctx, exec, steps, Options{Apply: true, Text: os.Stderr, JSONL: os.Stdout})
func Run(ctx context.Context, ex executor.Executor, steps []Step, opts Options) ([]StepResult, error) {
	if opts.RemoteDir == "" {
		opts.RemoteDir = DefaultRemoteDir
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.RunID == "" {
		opts.RunID = opts.Now().UTC().Format("20060102T150405Z")
	}
	if opts.Poll <= 0 {
		opts.Poll = 5 * time.Second
	}
	if opts.Text == nil {
		opts.Text = io.Discard
	}
	if !safePath.MatchString(opts.RemoteDir) || !safeID.MatchString(opts.RunID) {
		return nil, &UsageError{Msg: "the remote directory or run id has characters outside letters, digits and . _ - /"}
	}

	todo, err := selectSteps(steps, opts.From)
	if err != nil {
		return nil, err
	}
	for _, c := range opts.Confirm {
		if len(opts.HostNames) > 0 && !contains(opts.HostNames, c) {
			return nil, &UsageError{Msg: fmt.Sprintf("--confirm %q is not this host: it must equal %s", c, strings.Join(opts.HostNames, " or "))}
		}
	}
	confirmed := len(opts.Confirm) > 0
	var gated *Step
	if opts.Protected && !confirmed {
		for i := range todo {
			if todo[i].Gated() {
				gated = &todo[i]
				break
			}
		}
	}
	if !opts.Apply {
		writePlan(opts.Text, ex.Name(), todo, opts, gated)
		return nil, nil
	}
	if gated != nil {
		return nil, &GateError{Step: gated.Name, Changes: gated.Changes}
	}

	var results []StepResult
	for i, s := range todo {
		fmt.Fprintf(opts.Text, "[%d/%d] %s (%s) changes: %s\n", i+1, len(todo), s.Name, s.File, joinComma(s.Changes))
		res, err := runStep(ctx, ex, s, opts)
		if err != nil {
			return results, err
		}
		results = append(results, res)
		if opts.JSONL != nil {
			line, _ := json.Marshal(res)
			fmt.Fprintf(opts.JSONL, "%s\n", line)
		}
		fmt.Fprintf(opts.Text, "  -> %s in %.1fs\n", res.ExitClass, res.Seconds)
		if res.ExitClass != "ok" {
			return results, &StepError{Result: res, Resume: s.Name}
		}
	}
	return results, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// selectSteps returns the steps from the named one on.
func selectSteps(steps []Step, from string) ([]Step, error) {
	if from == "" {
		return steps, nil
	}
	var names []string
	for i, s := range steps {
		if s.Name == from {
			return steps[i:], nil
		}
		names = append(names, s.Name)
	}
	return nil, &UsageError{Msg: fmt.Sprintf("--from %q is not a step of this recipe (steps: %s)", from, strings.Join(names, ", "))}
}

// writePlan prints what a run would do and what the gate would stop.
func writePlan(w io.Writer, host string, todo []Step, opts Options, gated *Step) {
	fmt.Fprintf(w, "Plan for %s: %d step(s)\n", host, len(todo))
	for i, s := range todo {
		extra := ""
		if s.Detached {
			extra += " detached"
		}
		if s.User != "root" {
			extra += " as " + s.User
		}
		fmt.Fprintf(w, "  %d. %s (%s) changes: %s, timeout %ds%s\n", i+1, s.Name, s.File, joinComma(s.Changes), int(s.Timeout/time.Second), extra)
	}
	if gated != nil {
		fmt.Fprintf(w, "This host is production or staged: step %s changes %s, so --apply is refused without --confirm %s.\n", gated.Name, joinComma(gated.Changes), strings.Join(opts.HostNames, " or "))
	}
	fmt.Fprintln(w, "Nothing was run. Re-run with --apply to run these steps.")
}

// runStep sends one step to the host, runs it and reads its result back.
func runStep(ctx context.Context, ex executor.Executor, s Step, opts Options) (StepResult, error) {
	start := opts.Now()
	stem := strings.TrimSuffix(s.File, ".sh")
	if !safeID.MatchString(stem) {
		return StepResult{}, &UsageError{Msg: fmt.Sprintf("the file name %q has characters outside letters, digits and . _ -", s.File)}
	}
	dir := path.Join(opts.RemoteDir, opts.RunID)
	f := remoteFiles{dir: dir, step: path.Join(dir, s.File), driver: path.Join(dir, stem+".driver.sh"), log: path.Join(dir, stem+".log"), rc: path.Join(dir, stem+".rc")}
	setup := setupCommand(f, s, opts)

	finish := func(code int, counts map[string]any, class string) StepResult {
		if class == "" {
			class = ExitClass(code)
		}
		return StepResultOf(s, start, opts.Now(), class, code, counts)
	}

	if !s.Detached {
		cmd := setup + "bash " + q(f.driver) + "\n" + finalBlock(f)
		out, status, err := ex.Run(ctx, cmd, s.Timeout, opts.Poll)
		if err != nil {
			if ctx.Err() != nil {
				return StepResult{}, ctx.Err()
			}
			if isTimeout(err, start, opts.Now(), s.Timeout) {
				return finish(69, nil, "timeout"), nil
			}
			return StepResult{}, &HostError{Step: s.Name, Err: err}
		}
		body, counts, code, ok := parseFinal(out)
		if !ok {
			return StepResult{}, &HostError{Step: s.Name, Err: fmt.Errorf("the host reported no exit code (status %s): %s", status, strings.TrimSpace(tail(out, 300)))}
		}
		fmt.Fprint(opts.Text, indent(body))
		return finish(code, counts, ""), nil
	}

	// Detached: start it, then poll for the finish marker.
	startCmd := setup + "nohup bash " + q(f.driver) + " </dev/null >/dev/null 2>&1 &\necho started\n"
	if _, _, err := ex.Run(ctx, startCmd, commandTime, opts.Poll); err != nil {
		if ctx.Err() != nil {
			return StepResult{}, ctx.Err()
		}
		return StepResult{}, &HostError{Step: s.Name, Err: err}
	}
	pollCmd := fmt.Sprintf("if [ -f %s ]; then\n%selse\nprintf '%%s\\n' %s\ntail -n 1 %s 2>/dev/null\nfi\n", q(f.rc), finalBlock(f), runningMarker, q(f.log))
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return StepResult{}, ctx.Err()
		case <-time.After(opts.Poll):
		}
		now := opts.Now()
		if now.Sub(start) > s.Timeout {
			fmt.Fprintf(opts.Text, "  step %s is still running on the host after %ds; its log is %s\n", s.Name, int(s.Timeout/time.Second), f.log)
			return finish(69, nil, "timeout"), nil
		}
		out, _, err := ex.Run(ctx, pollCmd, commandTime, opts.Poll)
		if err != nil {
			if ctx.Err() != nil {
				return StepResult{}, ctx.Err()
			}
			if failures++; failures >= pollFailures {
				return StepResult{}, &HostError{Step: s.Name, Err: fmt.Errorf("polling failed %d times in a row: %w", failures, err)}
			}
			continue
		}
		failures = 0
		if body, counts, code, ok := parseFinal(out); ok {
			fmt.Fprint(opts.Text, indent(body))
			return finish(code, counts, ""), nil
		}
		last := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), runningMarker))
		fmt.Fprintf(opts.Text, "  ... running, %ds: %s\n", int(now.Sub(start)/time.Second), last)
	}
}

// StepResultOf builds the JSON line of a finished step.
//
// @param s {Step} the step
// @param start {time.Time} when it started
// @param end {time.Time} when it finished
// @param class {string} the exit class name
// @param code {int} the exit code
// @param counts {map[string]any} the COUNT values the step printed, nil for none
// @returns {StepResult} the result, its Counts never nil
func StepResultOf(s Step, start, end time.Time, class string, code int, counts map[string]any) StepResult {
	if counts == nil {
		counts = map[string]any{}
	}
	return StepResult{Step: s.Name, File: s.File, Start: start.UTC(), Seconds: end.Sub(start).Seconds(), ExitClass: class, ExitCode: code, Counts: counts}
}

type remoteFiles struct{ dir, step, driver, log, rc string }

// q single-quotes a path or value for sh; the callers have already restricted
// the characters, so no quote can appear inside.
func q(s string) string { return "'" + s + "'" }

// setupCommand writes the step and its driver onto the host. The driver exports
// the parameters, runs the step with stdin closed (a command inside it cannot
// swallow the script), and records the exit code in a file as its last act.
func setupCommand(f remoteFiles, s Step, opts Options) string {
	var d strings.Builder
	d.WriteString("#!/bin/bash\n")
	for _, p := range opts.Params {
		fmt.Fprintf(&d, "export %s=%s\n", p.Name, q(p.Value))
	}
	d.WriteString("export APPLY=yes\ncd /\n")
	run := "bash " + q(f.step)
	if s.User == "ubuntu" {
		run = "sudo -u ubuntu -H -E " + run
	}
	fmt.Fprintf(&d, "%s </dev/null > %s 2>&1\nrc=$?\necho \"$rc\" > %s.tmp && mv %s.tmp %s\n", run, q(f.log), q(f.rc), q(f.rc), q(f.rc))

	var b strings.Builder
	fmt.Fprintf(&b, "mkdir -p %s && chmod 755 %s\n", q(f.dir), q(f.dir))
	fmt.Fprintf(&b, "rm -f %s %s %s\n", q(f.rc), q(f.rc+".tmp"), q(f.log))
	writeFile(&b, f.step, s.Script, "STEP")
	writeFile(&b, f.driver, []byte(d.String()), "DRIVER")
	fmt.Fprintf(&b, "chmod 755 %s %s\n", q(f.step), q(f.driver))
	return b.String()
}

// writeFile appends commands that create a file from base64 text in a heredoc.
func writeFile(b *strings.Builder, remote string, content []byte, tag string) {
	enc := base64.StdEncoding.EncodeToString(content)
	var lines []string
	for len(enc) > 76 {
		lines = append(lines, enc[:76])
		enc = enc[76:]
	}
	lines = append(lines, enc)
	fmt.Fprintf(b, "base64 -d > %s <<'__CLASM_%s__'\n%s\n__CLASM_%s__\n", q(remote), tag, strings.Join(lines, "\n"), tag)
}

// finalBlock prints a finished step's log tail, its COUNT lines and its exit
// code, each introduced by a marker the runner reads back.
func finalBlock(f remoteFiles) string {
	return fmt.Sprintf("tail -c %d %s 2>/dev/null\nprintf '\\n%%s\\n' %s\ngrep -a '^COUNT ' %s 2>/dev/null\nprintf '%%s%%s\\n' %s \"$(cat %s 2>/dev/null)\"\n",
		logTailBytes, q(f.log), countsMarker, q(f.log), rcMarker, q(f.rc))
}

// parseFinal reads finalBlock's output: the log tail, the COUNT values and the
// exit code. ok is false when the output carries no exit code.
func parseFinal(out string) (body string, counts map[string]any, code int, ok bool) {
	i := strings.LastIndex(out, "\n"+countsMarker+"\n")
	if i < 0 {
		return "", nil, 0, false
	}
	body = out[:i+1]
	rest := out[i+len(countsMarker)+2:]
	counts = map[string]any{}
	got := false
	for _, line := range strings.Split(rest, "\n") {
		line = strings.TrimRight(line, "\r")
		if c, found := strings.CutPrefix(line, rcMarker); found {
			n, err := strconv.Atoi(strings.TrimSpace(c))
			if err != nil {
				return "", nil, 0, false
			}
			code, got = n, true
			continue
		}
		if m := countLine.FindStringSubmatch(line); m != nil {
			v := strings.TrimSpace(m[2])
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				counts[m[1]] = n
			} else {
				counts[m[1]] = v
			}
		}
	}
	return body, counts, code, got
}

// isTimeout reports whether an executor error is the step running out of time:
// the executors say "timed out", and a slow one may only return after the
// deadline.
func isTimeout(err error, start, now time.Time, limit time.Duration) bool {
	return strings.Contains(err.Error(), "timed out") || now.Sub(start) >= limit
}

func indent(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n") + "\n"
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// CannedReply returns the output a host gives for a finished step that exited
// with code and printed nothing, for tests of callers that fake an Executor.
//
// @param code {int} the step's exit code
// @returns {string} the host's output, in the runner's own protocol
// @example
//
//	stdout := recipe.CannedReply(0)
func CannedReply(code int) string {
	return fmt.Sprintf("\n%s\n%s%d\n", countsMarker, rcMarker, code)
}

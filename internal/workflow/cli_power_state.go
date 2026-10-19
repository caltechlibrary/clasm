package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// The non-interactive "Start EC2 instance" (DR-0194). Like the instance-profile
// swap it has no confirmation: starting destroys nothing and the menu asks only
// a yes/no. It acts on the instance's live state, not the inventory's, which was
// read when clasm started, and is safe to repeat: an instance that is already
// running (or pending) is not sent a second start.

// StartInstanceCLISlug is the leaf's cliSlug (DR-0177's mechanical rule applied
// to "Start EC2 instance").
//
// @example
//
//	clasm compute start-ec2-instance box
const StartInstanceCLISlug = "start-ec2-instance"

// StartInstanceUsage is the form's one-line usage, repeated in every usage error.
//
// @example
//
//	return &UsageError{Msg: "start-ec2-instance: missing instance\n" + StartInstanceUsage}
const StartInstanceUsage = "usage: clasm compute " + StartInstanceCLISlug + " [-text|-json] <instance-name-or-id>"

const startInstanceHelp = `

Starts a stopped instance and waits for it to reach running, then prints how to
reach it. An instance that is already running, or already starting, is not sent
a second start; the command says so and succeeds, so it is safe to repeat. An
instance that is stopping, shutting down or terminated cannot be started: that
is exit 1 and nothing is sent. There is no confirmation: starting is reversible.
The progress line goes to stderr, so stdout is the result alone.
`

// startInstanceJSON is the form's -json result.
type startInstanceJSON struct {
	InstanceID      string `json:"instance_id"`
	Name            string `json:"name"`
	Region          string `json:"region"`
	PreviousState   string `json:"previous_state"`
	State           string `json:"state"`
	Changed         bool   `json:"changed"` // false when nothing was sent
	PublicIP        string `json:"public_ip"`
	PrivateIP       string `json:"private_ip"`
	SSMStartSession string `json:"ssm_start_session"`
}

// ParseStartInstanceArgs parses start-ec2-instance's options and its one word,
// the instance name or ID. Any problem is a *UsageError (exit 2) raised before
// AWS is touched; --help is a *HelpRequested.
//
// @param args {[]string} the words after the leaf's slug
// @returns {string} the instance name or ID
// @returns {ui.Format} the output format, text unless -json was given
// @returns {error} a *UsageError for a bad command line, a *HelpRequested for --help
// @example
//
//	instance, format, err := ParseStartInstanceArgs([]string{"-json", "box"})
func ParseStartInstanceArgs(args []string) (instance string, format ui.Format, err error) {
	opts, words, err := ParseComputeArgs(StartInstanceCLISlug, StartInstanceUsage, ComputeAllowNone, args)
	if err != nil {
		var h *HelpRequested
		if errors.As(err, &h) {
			return "", ui.FormatText, &HelpRequested{Usage: h.Usage + startInstanceHelp}
		}
		return "", ui.FormatText, err
	}
	if len(words) != 1 || strings.TrimSpace(words[0]) == "" {
		return "", ui.FormatText, &UsageError{Msg: fmt.Sprintf("%s: want one instance name or ID, got %d arguments\n%s",
			StartInstanceCLISlug, len(words), StartInstanceUsage)}
	}
	return strings.TrimSpace(words[0]), opts.Format, nil
}

// RunStartInstanceCLI is `clasm compute start-ec2-instance`. It reads the
// instance's live state first; only a stopped instance is sent a start, and the
// form then waits (up to DefaultLaunchTimeout) for running. The progress line
// goes to eout and the result to w.
//
// @param ctx {context.Context} cancels the calls and the wait
// @param w {io.Writer} receives the result: connection details, or one JSON object
// @param eout {io.Writer} receives the progress line
// @param clients {map[string]awsclient.EC2API} EC2 clients by region
// @param instances {[]inventory.Instance} the inventory the name or ID is resolved against
// @param arg {string} the instance name or ID
// @param format {ui.Format} text or JSON
// @returns {error} nil on success or when already running; a *UsageError for an unknown or ambiguous instance; otherwise an error for exit 1, or an *ExitCodeError for exit 69
// @example
//
//	err := RunStartInstanceCLI(ctx, os.Stdout, os.Stderr, clients, instances, "box", ui.FormatText)
func RunStartInstanceCLI(ctx context.Context, w, eout io.Writer, clients map[string]awsclient.EC2API, instances []inventory.Instance, arg string, format ui.Format) error {
	return runStartInstanceCLI(ctx, w, eout, clients, instances, arg, format, DefaultLaunchTimeout, DefaultLaunchPollInterval)
}

// runStartInstanceCLI is RunStartInstanceCLI with the wait's timeout and poll
// interval as parameters, so a test need not wait minutes.
func runStartInstanceCLI(ctx context.Context, w, eout io.Writer, clients map[string]awsclient.EC2API, instances []inventory.Instance, arg string, format ui.Format, timeout, poll time.Duration) error {
	inst, err := resolveInstanceForCLI(arg, instances)
	if err != nil {
		return err
	}
	client, err := resolveEC2(clients, inst.Region)
	if err != nil {
		return err
	}
	live, err := describeLiveInstance(ctx, client, inst.InstanceID)
	if err != nil {
		return classifyUnavailable(fmt.Errorf("reading the state of %s: %w", inst.InstanceID, err))
	}

	previous := liveStateName(live)
	changed := false
	switch previous {
	case types.InstanceStateNameRunning:
		// Nothing to send and nothing to wait for.
	case types.InstanceStateNamePending:
		fmt.Fprintf(eout, "%s (%s) is already starting; waiting for it to reach running...\n", inst.InstanceID, inst.Name)
	case types.InstanceStateNameStopped:
		if err := StartInstance(ctx, client, inst.InstanceID); err != nil {
			return classifyUnavailable(fmt.Errorf("starting instance %s: %w", inst.InstanceID, err))
		}
		changed = true
		fmt.Fprintf(eout, "Starting %s (%s), waiting for it to reach running...\n", inst.InstanceID, inst.Name)
	case types.InstanceStateNameStopping:
		return fmt.Errorf("%s (%s) is %s; it cannot be started until it has stopped: run this again in a minute", inst.InstanceID, inst.Name, previous)
	default:
		return fmt.Errorf("%s (%s) is %s; it cannot be started from that state", inst.InstanceID, inst.Name, previous)
	}

	final := live
	if previous != types.InstanceStateNameRunning {
		final, err = WaitUntilRunning(ctx, client, inst.InstanceID, timeout, poll)
		if err != nil {
			return waitFailure(err, inst.InstanceID, types.InstanceStateNameRunning)
		}
	}

	if format != ui.FormatText {
		return ui.WriteJSONValue(w, startInstanceJSON{
			InstanceID: inst.InstanceID, Name: inst.Name, Region: inst.Region,
			PreviousState: string(previous), State: string(liveStateName(final)), Changed: changed,
			PublicIP: aws.ToString(final.PublicIpAddress), PrivateIP: aws.ToString(final.PrivateIpAddress),
			SSMStartSession: ssmStartSessionCommand(inst.InstanceID, regionOfInstance(final)),
		}, format)
	}
	if previous == types.InstanceStateNameRunning {
		fmt.Fprintf(w, "%s (%s) is already running; nothing was changed.\n", inst.InstanceID, inst.Name)
	}
	displayConnectionInfo(ctx, w, client, inst.InstanceID, final)
	if previous != types.InstanceStateNameRunning {
		fmt.Fprintln(w, "Note: the public IP may have changed since this instance was last running, unless it uses an Elastic IP.")
	}
	return nil
}

// describeLiveInstance reads one instance's current description from AWS.
func describeLiveInstance(ctx context.Context, client awsclient.EC2API, instanceID string) (types.Instance, error) {
	callCtx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DescribeInstances(callCtx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		return types.Instance{}, err
	}
	inst, found := findInstance(out, instanceID)
	if !found {
		return types.Instance{}, fmt.Errorf("instance %s was not returned by AWS", instanceID)
	}
	return inst, nil
}

// liveStateName is an instance's state name, "" when AWS reported none.
func liveStateName(inst types.Instance) types.InstanceStateName {
	if inst.State == nil {
		return ""
	}
	return inst.State.Name
}

// waitFailure turns a failed wait into the form's error. Giving up (this
// package's own timeout, or the wait's deadline reaching a call) is exit 1 with
// the command that says where the instance got to; anything else is classified
// as any other call.
func waitFailure(err error, instanceID string, want types.InstanceStateName) error {
	if errors.Is(err, errWaitTimedOut) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w; the instance may still reach %s -- check with: clasm compute show-instance-detail %s", err, want, instanceID)
	}
	return classifyUnavailable(err)
}

// classifyUnavailable marks an error that means AWS could not be reached (a
// network or URL error) as DR-0014's exit 69; any other error is returned as it
// is and stays exit 1.
func classifyUnavailable(err error) error {
	var netErr net.Error
	var urlErr *url.Error
	if errors.As(err, &netErr) || errors.As(err, &urlErr) {
		return &ExitCodeError{Code: ExitUnavailable, Err: err}
	}
	return err
}

// The non-interactive "Stop EC2 instance" (DR-0194, DR-0195). Like Start it acts
// on the instance's live state and is safe to repeat: an instance that is
// stopped is not sent a second stop, and one that is already stopping is waited
// out. It is ungated except for an instance whose live Environment tag is
// production, which takes DR-0180's gate: a dry run by default, the
// type-to-confirm prompt on a terminal, --confirm <id-or-name> to run without
// one. --confirm is accepted on any instance and checked against it, so a script
// can pass it always.

// StopInstanceCLISlug is the leaf's cliSlug (DR-0177's mechanical rule applied to
// "Stop EC2 instance").
//
// @example
//
//	clasm compute stop-ec2-instance box
const StopInstanceCLISlug = "stop-ec2-instance"

// StopInstanceUsage is the form's one-line usage, repeated in every usage error.
//
// @example
//
//	return &UsageError{Msg: "stop-ec2-instance: missing instance\n" + StopInstanceUsage}
const StopInstanceUsage = "usage: clasm compute " + StopInstanceCLISlug + " [-text|-json] [--confirm <instance-id-or-name>] <instance-name-or-id>"

const stopInstanceHelp = `

Stops a running instance and waits for it to reach stopped. An instance that is
already stopped is not sent a second stop; the command says so and succeeds. One
that is already stopping is waited out. A pending, shutting-down or terminated
instance cannot be stopped: that is exit 1 and nothing is sent. Its EBS volumes
persist and it can be started again.

An instance tagged Environment=production is not stopped without --confirm: with
no terminal the plan is printed and nothing is changed (exit 0). The progress
line goes to stderr, so stdout is the result alone; with -json the plan goes to
stderr too, and stdout is one object whose "declined" says nothing was done.
`

// stopInstanceJSON is the form's -json result.
type stopInstanceJSON struct {
	InstanceID    string `json:"instance_id"`
	Name          string `json:"name"`
	Region        string `json:"region"`
	PreviousState string `json:"previous_state"`
	State         string `json:"state"`
	Changed       bool   `json:"changed"`  // false when nothing was sent
	Declined      bool   `json:"declined"` // true when the production gate stopped it
}

// ParseStopInstanceArgs parses stop-ec2-instance's options and its one word, the
// instance name or ID. Any problem is a *UsageError (exit 2) raised before AWS
// is touched; --help is a *HelpRequested.
//
// @param args {[]string} the words after the leaf's slug
// @returns {string} the instance name or ID
// @returns {ui.Format} the output format, text unless -json was given
// @returns {[]string} the --confirm values, none if the flag was not given
// @returns {error} a *UsageError for a bad command line, a *HelpRequested for --help
// @example
//
//	instance, format, confirms, err := ParseStopInstanceArgs([]string{"--confirm", "box", "box"})
func ParseStopInstanceArgs(args []string) (instance string, format ui.Format, confirms []string, err error) {
	opts, words, err := ParseComputeArgs(StopInstanceCLISlug, StopInstanceUsage, ComputeAllowConfirm, args)
	if err != nil {
		var h *HelpRequested
		if errors.As(err, &h) {
			return "", ui.FormatText, nil, &HelpRequested{Usage: h.Usage + stopInstanceHelp}
		}
		return "", ui.FormatText, nil, err
	}
	if len(words) != 1 || strings.TrimSpace(words[0]) == "" {
		return "", ui.FormatText, nil, &UsageError{Msg: fmt.Sprintf("%s: want one instance name or ID, got %d arguments\n%s",
			StopInstanceCLISlug, len(words), StopInstanceUsage)}
	}
	return strings.TrimSpace(words[0]), opts.Format, opts.Confirms, nil
}

// RunStopInstanceCLI is `clasm compute stop-ec2-instance`. It checks --confirm
// against the instance before any AWS call, reads the live state, and stops only
// a running instance, behind the production gate where DR-0195 puts it; the form
// then waits (up to DefaultLaunchTimeout) for stopped. The progress line goes to
// eout and the result to w.
//
// @param ctx {context.Context} cancels the calls and the wait
// @param w {io.Writer} receives the result (and, in text, the gate's plan)
// @param eout {io.Writer} receives the progress line (and, with -json, the plan)
// @param clients {map[string]awsclient.EC2API} EC2 clients by region
// @param instances {[]inventory.Instance} the inventory the name or ID is resolved against
// @param arg {string} the instance name or ID
// @param format {ui.Format} text or JSON
// @param confirms {[]string} the --confirm values
// @param interactive {bool} whether a terminal is attached, for the gate's prompt
// @param input {io.Reader} the prompt's input, nil in production
// @param output {io.Writer} the prompt's output, nil in production
// @returns {error} nil on success, when already stopped, or when the gate declined; a *UsageError for a bad instance or a --confirm that is not the target; otherwise an error for exit 1, or an *ExitCodeError for exit 69
// @example
//
//	err := RunStopInstanceCLI(ctx, os.Stdout, os.Stderr, clients, instances, "box", ui.FormatText, nil, ui.StdinIsTerminal(), nil, nil)
func RunStopInstanceCLI(ctx context.Context, w, eout io.Writer, clients map[string]awsclient.EC2API, instances []inventory.Instance, arg string, format ui.Format, confirms []string, interactive bool, input io.Reader, output io.Writer) error {
	return runStopInstanceCLI(ctx, w, eout, clients, instances, arg, format, confirms, interactive, input, output, DefaultLaunchTimeout, DefaultLaunchPollInterval)
}

// runStopInstanceCLI is RunStopInstanceCLI with the wait's timeout and poll
// interval as parameters, so a test need not wait minutes.
func runStopInstanceCLI(ctx context.Context, w, eout io.Writer, clients map[string]awsclient.EC2API, instances []inventory.Instance, arg string, format ui.Format, confirms []string, interactive bool, input io.Reader, output io.Writer, timeout, poll time.Duration) error {
	inst, err := resolveInstanceForCLI(arg, instances)
	if err != nil {
		return err
	}
	gate := Gate{Target: inst, Confirms: confirms, Interactive: interactive,
		Summary: fmt.Sprintf("stop instance %s (%s)", inst.InstanceID, inst.Name)}
	if err := gate.CheckConfirm(); err != nil {
		return err
	}
	client, err := resolveEC2(clients, inst.Region)
	if err != nil {
		return err
	}
	live, err := describeLiveInstance(ctx, client, inst.InstanceID)
	if err != nil {
		return classifyUnavailable(fmt.Errorf("reading the state of %s: %w", inst.InstanceID, err))
	}

	previous := liveStateName(live)
	switch previous {
	case types.InstanceStateNameRunning, types.InstanceStateNameStopped:
	case types.InstanceStateNameStopping:
		fmt.Fprintf(eout, "%s (%s) is already stopping; waiting for it to reach stopped...\n", inst.InstanceID, inst.Name)
	case types.InstanceStateNamePending:
		return fmt.Errorf("%s (%s) is %s; it cannot be stopped until it is running: run this again in a minute", inst.InstanceID, inst.Name, previous)
	default:
		return fmt.Errorf("%s (%s) is %s; it cannot be stopped from that state", inst.InstanceID, inst.Name, previous)
	}

	changed, declined := false, false
	if previous == types.InstanceStateNameRunning {
		if isProductionInstance(live) {
			gate.Plan = []string{
				fmt.Sprintf("stop instance %s (%s) in %s, tagged Environment=production", inst.InstanceID, inst.Name, inst.Region),
				"its EBS volumes persist; whatever runs on it stops until it is started again",
			}
			planW := w
			if format != ui.FormatText {
				planW = eout // stdout stays one JSON object
			}
			decision, err := gate.Decide(planW, input, output)
			if err != nil {
				return err
			}
			declined = decision != GateProceed
		}
		if !declined {
			if err := StopInstance(ctx, client, inst.InstanceID); err != nil {
				return classifyUnavailable(fmt.Errorf("stopping instance %s: %w", inst.InstanceID, err))
			}
			changed = true
			fmt.Fprintf(eout, "Stopping %s (%s), waiting for it to reach stopped...\n", inst.InstanceID, inst.Name)
		}
	}

	final := live
	if !declined && previous != types.InstanceStateNameStopped {
		final, err = WaitUntilStopped(ctx, client, inst.InstanceID, timeout, poll)
		if err != nil {
			return waitFailure(err, inst.InstanceID, types.InstanceStateNameStopped)
		}
	}

	if format != ui.FormatText {
		return ui.WriteJSONValue(w, stopInstanceJSON{
			InstanceID: inst.InstanceID, Name: inst.Name, Region: inst.Region,
			PreviousState: string(previous), State: string(liveStateName(final)), Changed: changed, Declined: declined,
		}, format)
	}
	switch {
	case declined:
		// The gate printed the plan and said nothing was changed.
	case previous == types.InstanceStateNameStopped:
		fmt.Fprintf(w, "%s (%s) is already stopped; nothing was changed.\n", inst.InstanceID, inst.Name)
	default:
		fmt.Fprintf(w, "Instance %s (%s) is now stopped.\n", inst.InstanceID, inst.Name)
	}
	return nil
}

// isProductionInstance reports whether the instance's live Environment tag is
// exactly "production", the value the launch flows write and the production
// warnings read. An untagged instance, or any other value, is not.
func isProductionInstance(inst types.Instance) bool {
	for _, tag := range inst.Tags {
		if aws.ToString(tag.Key) == "Environment" {
			return aws.ToString(tag.Value) == "production"
		}
	}
	return false
}

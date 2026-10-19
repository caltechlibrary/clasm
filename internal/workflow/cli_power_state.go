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

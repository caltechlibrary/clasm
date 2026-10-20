package workflow

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

const convertLogIndicesUsage = "usage: clasm rdm-backup-and-restore " + ConvertLogIndicesCLISlug +
	" [-text|-json] [-wait <duration>] [--confirm <instance-id-or-name>] <instance-name-or-id>"

const convertLogIndicesHelp = `

Converts the audit log and the job logs of an RDM instance to real data streams, copy first. A
restore that does not bring the cluster's global state back leaves them as an orphaned .ds- backing
index and/or a plain, dynamically mapped index, and the admin pages stop working (caltechauthors
#185). Run install-index-templates first: a stream needs its template. The order is fixed and is the
safety: copy the orphan and the plain index to holding indices named clasm-fix-..., verify the
orphan's copy by count, take the alias off both, delete the orphan (its name is the stream's first
backing index), copy what the app wrote meanwhile until the counts agree, delete the plain index and
create the stream back to back, load the copies into it with op_type create, and verify the stream
holds at least what the copies hold. The copies are kept and named; clasm never deletes them, so
remove them yourself once the pages are confirmed. A failure at any step leaves every document in the
orphan, the plain index, the stream or a copy, and says which copies. An instance whose logs are
already data streams is a no-op (exit 0). A state clasm will not convert -- no data-stream template,
a copy name already taken, more than one orphan -- is refused with the reason, and nothing is changed.

On an instance whose live Environment tag is production the run is a dry run unless confirmed: with
--confirm <instance-id-or-name> it runs; without it a terminal is asked to type the instance's ID or
Name tag; with no terminal the plan is printed and nothing is changed (exit 0). Any other instance runs
with no confirmation (--confirm is accepted and checked). Progress goes to stderr, the result to stdout.

Options:
  -text
      The result as text (the default).
  -json
      The result as one JSON document. On a failure, stdout is an error object
      {"error":{"class","code","message"}} instead.
  -wait <duration>
      How long to wait for OpenSearch to answer, for a run right after a start
      (default 5m; 0 checks once).
  --confirm <instance-id-or-name>
      Required to convert an instance tagged Environment=production. The value must equal the
      instance's ID or Name tag, exactly; anything else is a usage error before any AWS call.
  -h, --help
      Show this help.

Exit status: 0 converted, nothing to convert, or a dry run; 1 refused, or a step failed (the copies
hold every document); 2 usage; 65 output not readable; 66 no app on the instance; 69 the instance or
OpenSearch did not answer; 70 internal.
`

// ParseConvertLogIndicesArgs parses convert-log-indices' options and its one word, the instance name
// or ID. A problem is a *UsageError (exit 2), raised before AWS is touched; --help is a *HelpRequested.
//
// @param args {[]string} the words after the leaf name
// @returns {string, ui.Format, time.Duration, DestructiveOptions, error} the instance word, the format, the wait, the --confirm values, and any usage error
// @example
//
//	inst, format, wait, opts, err := ParseConvertLogIndicesArgs([]string{"--confirm", "box", "box"})
func ParseConvertLogIndicesArgs(args []string) (instance string, format ui.Format, wait time.Duration, opts DestructiveOptions, err error) {
	instance, format, wait, confirms, err := parseIndexFormArgs(ConvertLogIndicesCLISlug, convertLogIndicesUsage, convertLogIndicesHelp, true, args)
	return instance, format, wait, DestructiveOptions{Confirms: confirms}, err
}

// conversionJSON is the form's -json result.
type conversionJSON struct {
	InstanceID string      `json:"instance_id"`
	Instance   string      `json:"instance"`
	Production bool        `json:"production"`
	Declined   bool        `json:"declined"` // true when the production gate stopped it
	Converted  []logResult `json:"converted"`
	Notes      []string    `json:"notes"`
}

// conversionPlanLines is what a dry run and the prompt print: one block per log, from read-only facts.
func conversionPlanLines(plan conversionPlan) []string {
	var lines []string
	for _, lc := range plan.Logs {
		var from, into []string
		if lc.Orphan != "" {
			from = append(from, fmt.Sprintf("the orphaned index %s (%d documents)", lc.Orphan, lc.OrphanDocs))
			into = append(into, lc.HoldOrphan)
		}
		if lc.Plain != "" {
			from = append(from, fmt.Sprintf("the plain index %s (%d documents)", lc.Plain, lc.PlainDocs))
			into = append(into, lc.HoldPlain)
		}
		lines = append(lines,
			fmt.Sprintf("%s: convert to the data stream %s: copy %s into %s", lc.Label, lc.Stream, strings.Join(from, " and "), strings.Join(into, " and ")),
			fmt.Sprintf("%s: then take the alias off, delete the originals once their copies are verified, create the stream and load the copies into it", lc.Label))
	}
	if len(plan.Logs) > 0 {
		lines = append(lines, "the copies are kept and named; clasm never deletes them")
	}
	return lines
}

// convertWithGate is the form after discovery: it applies DR-0180's gate on a production instance, runs
// the conversion, and writes the result. Progress goes to eout so stdout is the result alone (and, with
// -json, one object).
func convertWithGate(ctx context.Context, w, eout io.Writer, ops indexOps, plan conversionPlan, inst inventory.Instance, production bool, confirms []string, interactive bool, input io.Reader, output io.Writer, format ui.Format) error {
	gate := Gate{Target: inst, Confirms: confirms, Interactive: interactive}
	if err := gate.CheckConfirm(); err != nil {
		return err
	}
	result := conversionJSON{InstanceID: inst.InstanceID, Instance: inst.Name, Production: production, Converted: []logResult{}, Notes: plan.Notes}
	if result.Notes == nil {
		result.Notes = []string{}
	}
	finish := func() error {
		if format != ui.FormatText {
			return ui.WriteJSONValue(w, result, format)
		}
		switch {
		case result.Declined:
			// the gate printed the plan and said nothing was changed
		case len(result.Converted) == 0:
			fmt.Fprintf(w, "Nothing to convert on %s (%s): %s\n", inst.Name, inst.InstanceID, strings.Join(plan.Notes, "; "))
		default:
			fmt.Fprintf(w, "Converted %d log(s) on %s (%s) to data streams:\n", len(result.Converted), inst.Name, inst.InstanceID)
			var copies []string
			for _, r := range result.Converted {
				fmt.Fprintf(w, "  %s: %s now holds %d documents (orphan %d + plain %d)\n", r.Label, r.Stream, r.StreamDocs, r.OrphanDocs, r.PlainDocs)
				copies = append(copies, r.Copies...)
			}
			fmt.Fprintf(w, "Kept, never deleted by clasm: %s. Remove them yourself once the pages are confirmed.\n", strings.Join(copies, ", "))
			fmt.Fprintf(w, "Check with: clasm rdm-backup-and-restore %s %s\n", RDMIndexHealthCLISlug, inst.InstanceID)
		}
		return nil
	}

	if len(plan.Logs) == 0 {
		return finish()
	}
	if production {
		gate.Summary = fmt.Sprintf("convert the logs of %s (%s) to data streams, copies kept", inst.Name, inst.InstanceID)
		gate.Plan = conversionPlanLines(plan)
		planW := w
		if format != ui.FormatText {
			planW = eout
		}
		decision, err := gate.Decide(planW, input, output)
		if err != nil {
			return err
		}
		if decision != GateProceed {
			result.Declined = true
			return finish()
		}
	}
	results, err := ConvertLogIndices(ctx, eout, ops, plan)
	if err != nil {
		writeIndexJSONError(w, format, err)
		return err
	}
	result.Converted = results
	return finish()
}

// RunConvertLogIndicesCLI is `clasm rdm-backup-and-restore convert-log-indices`: it checks --confirm and
// resolves the instance before any AWS call, reads the instance (read-only), and, only when there is
// something to convert, reads its live Environment tag to decide whether DR-0180's gate applies. A
// healthy instance is a no-op and never touches EC2.
//
// @param ctx {context.Context} cancels the run before its next step
// @param w {io.Writer} receives the result (or the JSON error object)
// @param eout {io.Writer} receives progress, and with -json the plan
// @param ssmClients {map[string]awsclient.SSMAPI} SSM clients by region
// @param ec2Clients {map[string]awsclient.EC2API} EC2 clients by region, for the live Environment tag
// @param instances {[]inventory.Instance} the inventory the name or ID is resolved against
// @param arg {string} the instance name or ID
// @param confirms {[]string} the --confirm values
// @param format {ui.Format} text or JSON
// @param wait {time.Duration} how long to wait for OpenSearch to answer
// @param poll {time.Duration} how often to poll SSM and a reindex task
// @param interactive {bool} whether a terminal is attached, for the gate's prompt
// @param input {io.Reader} the prompt's input, nil in production
// @param output {io.Writer} the prompt's output, nil in production
// @returns {error} nil on success, a no-op or a dry run; a *UsageError for a bad instance or a --confirm that is not the target; a refusal or failed step for exit 1; otherwise an *ExitCodeError
// @example
//
//	err := RunConvertLogIndicesCLI(ctx, os.Stdout, os.Stderr, ssmClients, ec2Clients, instances, "box", nil, ui.FormatText, DefaultIndexHealthWait, DefaultSSMPollInterval, ui.StdinIsTerminal(), nil, nil)
func RunConvertLogIndicesCLI(ctx context.Context, w, eout io.Writer, ssmClients map[string]awsclient.SSMAPI, ec2Clients map[string]awsclient.EC2API, instances []inventory.Instance, arg string, confirms []string, format ui.Format, wait, poll time.Duration, interactive bool, input io.Reader, output io.Writer) error {
	inst, err := resolveInstanceForCLI(arg, instances)
	if err != nil {
		return err
	}
	if err := (Gate{Target: inst, Confirms: confirms}).CheckConfirm(); err != nil {
		return err // a typo fails before any AWS call
	}
	ssmClient, err := resolveSSM(ssmClients, inst.Region)
	if err != nil {
		return err
	}
	plan, err := DiscoverLogConversion(ctx, ssmClient, inst, wait, poll)
	if err != nil {
		writeIndexJSONError(w, format, err)
		return err
	}
	production := false
	if len(plan.Logs) > 0 {
		ec2Client, err := resolveEC2(ec2Clients, inst.Region)
		if err != nil {
			return err
		}
		live, err := describeLiveInstance(ctx, ec2Client, inst.InstanceID)
		if err != nil {
			err = classifyUnavailable(fmt.Errorf("reading the Environment tag of %s: %w", inst.InstanceID, err))
			writeIndexJSONError(w, format, err)
			return err
		}
		production = isProductionInstance(live)
	}
	return convertWithGate(ctx, w, eout, newSSMIndexOps(ssmClient, inst.InstanceID, poll, DefaultReindexTimeout), plan, inst, production, confirms, interactive, input, output, format)
}

// ConvertLogIndicesMenu is the interactive Convert Log Indices leaf: pick an instance and run the form's
// logic with the terminal prompt as the gate.
//
// @param ctx {context.Context} cancels the run
// @param w {io.Writer} receives the plan, progress and result
// @param ssmClients {map[string]awsclient.SSMAPI} SSM clients by region
// @param ec2Clients {map[string]awsclient.EC2API} EC2 clients by region
// @param instances {[]inventory.Instance} the instances to pick from
// @returns {error} nil on success or a cancellation; an error for a refusal or a failed step
// @example
//
//	err := ConvertLogIndicesMenu(ctx, os.Stdout, ssmClients, ec2Clients, instances)
func ConvertLogIndicesMenu(ctx context.Context, w io.Writer, ssmClients map[string]awsclient.SSMAPI, ec2Clients map[string]awsclient.EC2API, instances []inventory.Instance) error {
	if len(instances) == 0 {
		fmt.Fprintln(w, "No instances found.")
		return nil
	}
	inst, err := pickInstance(ctx, "Select an instance to repair", "Converts the audit log and job logs to real data streams, copy first, by running OpenSearch calls on the instance over SSM. The copies are kept. On a production instance you are asked to type its ID or Name tag first.", instances)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return RunConvertLogIndicesCLI(ctx, w, w, ssmClients, ec2Clients, instances, inst.InstanceID, nil, ui.FormatText, DefaultIndexHealthWait, DefaultSSMPollInterval, true, nil, nil)
}

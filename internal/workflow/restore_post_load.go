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

// What Restore OpenSearch does after the load (DR-0196, DR-0198, DR-0199).
//
// The restore keeps include_global_state:false, which carries neither index templates nor data-stream
// definitions, so a restored instance has none: the app's first write then auto-creates plain,
// dynamically mapped audit-log and job-logs indices and the admin pages silently stop updating
// (caltechauthors #185). So once the snapshot is restored and verified and the local repository is
// cleaned up, Restore goes on in a fixed order: install the missing index templates, convert the log
// indices that came back plain or orphaned to data streams (copy first, copies kept), then run the
// RDM index health check. The check's verdict is the restore's verdict: a restore that leaves the
// page broken exits 1.
//
// There is no second confirmation. The conversion deletes indices, but only after verified copies, and
// the one confirmation the restore already takes (DR-0180) names it in its plan (DR-0199); a prompt
// after a long download would also stall an unattended --confirm run with the instance restored but
// not yet usable.

// restorePostLoadNote is the sentence the restore's plan, in the dry run, the terminal prompt and the
// menu flow, uses to say what the one confirmation covers beyond the restore itself.
const restorePostLoadNote = "after the load, install any missing index templates, convert any audit-log or job-logs index that comes back plain or orphaned to a data stream (copy first, copies kept), then check RDM index health; the restore exits 1 if the check reports a problem"

// postRestoreWait is how long each post-load step waits for OpenSearch to answer. It has only just
// restored a snapshot, so it is up; a long wait would hide a real outage.
const postRestoreWait = time.Minute

// postRestoreSteps are the three steps, injectable so their order and what stops what can be tested
// without a cluster. newPostRestoreSteps builds the real ones.
type postRestoreSteps struct {
	install  func(ctx context.Context) (IndexTemplateInstallReport, error)
	discover func(ctx context.Context) (conversionPlan, error)
	convert  func(ctx context.Context, w io.Writer, plan conversionPlan) ([]logResult, error)
	health   func(ctx context.Context) (IndexHealthReport, error)
}

// newPostRestoreSteps builds the real steps for inst over client.
//
// @param client {awsclient.SSMAPI} the SSM client for the instance's region
// @param inst {inventory.Instance} the instance that was just restored
// @returns {postRestoreSteps} the install, discovery, conversion and health check
// @example
//
//	err := runPostRestoreRepair(ctx, os.Stdout, inst, newPostRestoreSteps(client, inst))
func newPostRestoreSteps(client awsclient.SSMAPI, inst inventory.Instance) postRestoreSteps {
	poll := DefaultSSMPollInterval
	return postRestoreSteps{
		install: func(ctx context.Context) (IndexTemplateInstallReport, error) {
			return InstallRDMIndexTemplates(ctx, client, inst, postRestoreWait, poll)
		},
		discover: func(ctx context.Context) (conversionPlan, error) {
			return DiscoverLogConversion(ctx, client, inst, postRestoreWait, poll)
		},
		convert: func(ctx context.Context, w io.Writer, plan conversionPlan) ([]logResult, error) {
			return ConvertLogIndices(ctx, w, newSSMIndexOps(client, inst.InstanceID, poll, DefaultReindexTimeout), plan)
		},
		health: func(ctx context.Context) (IndexHealthReport, error) {
			return CheckRDMIndexHealth(ctx, client, inst, postRestoreWait, poll)
		},
	}
}

// postRestoreError is a post-load step that failed. It says the restore itself finished, so nobody
// repeats it, and which form repeats the step. It keeps the step's own exit class.
type postRestoreError struct {
	Step, Repeat string
	Err          error
}

func (e *postRestoreError) Error() string {
	return fmt.Sprintf("the restore itself finished and was verified, but %s failed: %v; repeat it with: clasm rdm-backup-and-restore %s",
		e.Step, e.Err, e.Repeat)
}

func (e *postRestoreError) Unwrap() error { return e.Err }

// runPostRestoreRepair runs the post-load steps in order and stops at the first failure. Progress
// and the health report go to w. It asks nothing: the restore's one confirmation covers it.
//
// @param ctx {context.Context} cancels the run before its next step
// @param w {io.Writer} receives what each step did and the health report
// @param inst {inventory.Instance} the instance that was just restored
// @param steps {postRestoreSteps} the steps to run
// @returns {error} nil when the instance is healthy; a *postRestoreError for a step that failed, keeping its exit class; an error for exit 1 when the health check reports a problem
// @example
//
//	err := runPostRestoreRepair(ctx, os.Stdout, inst, newPostRestoreSteps(client, inst))
func runPostRestoreRepair(ctx context.Context, w io.Writer, inst inventory.Instance, steps postRestoreSteps) error {
	fmt.Fprintf(w, "\nPost-load repair: a restore without the cluster's global state drops index templates and data-stream definitions.\n")

	templates, err := steps.install(ctx)
	if err != nil {
		return &postRestoreError{Step: "installing the index templates", Repeat: InstallIndexTemplatesCLISlug + " " + inst.InstanceID, Err: err}
	}
	if len(templates.Failed) > 0 {
		names := make([]string, len(templates.Failed))
		for i, f := range templates.Failed {
			names[i] = f.Name + " (" + f.Error + ")"
		}
		return &postRestoreError{Step: "installing the index templates", Repeat: InstallIndexTemplatesCLISlug + " " + inst.InstanceID,
			Err: fmt.Errorf("%d template(s) could not be installed: %s", len(templates.Failed), strings.Join(names, ", "))}
	}
	if len(templates.Installed) > 0 {
		fmt.Fprintf(w, "  index templates: installed %d: %s\n", len(templates.Installed), strings.Join(templates.Installed, ", "))
	} else {
		fmt.Fprintf(w, "  index templates: all present\n")
	}

	plan, err := steps.discover(ctx)
	if err != nil {
		return &postRestoreError{Step: "reading the log indices", Repeat: ConvertLogIndicesCLISlug + " " + inst.InstanceID, Err: err}
	}
	if len(plan.Logs) == 0 {
		fmt.Fprintf(w, "  log indices: already data streams, nothing to convert\n")
	} else {
		results, err := steps.convert(ctx, w, plan)
		if err != nil {
			return &postRestoreError{Step: "converting the log indices", Repeat: ConvertLogIndicesCLISlug + " " + inst.InstanceID, Err: err}
		}
		for _, r := range results {
			fmt.Fprintf(w, "  log indices: converted the %s to the data stream %s (%d documents); copies kept, never deleted by clasm: %s\n",
				r.Label, r.Stream, r.StreamDocs, strings.Join(r.Copies, ", "))
		}
	}

	report, err := steps.health(ctx)
	if err != nil {
		return &postRestoreError{Step: "the index health check", Repeat: RDMIndexHealthCLISlug + " " + inst.InstanceID, Err: err}
	}
	fmt.Fprintln(w)
	if err := WriteIndexHealth(w, report, ui.FormatText); err != nil {
		return err
	}
	if !report.Healthy {
		return fmt.Errorf("the restore finished, but RDM Index Health reports %d problem(s) on %s; fix them and re-check with: clasm rdm-backup-and-restore %s %s",
			len(report.Problems), inst.Name, RDMIndexHealthCLISlug, inst.InstanceID)
	}
	return nil
}

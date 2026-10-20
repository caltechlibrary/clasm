package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// Install the app's missing index templates (DR-0198, DR-0199). A restore with
// include_global_state:false drops every index template; without them the app's
// first write creates plain, dynamically mapped indices (caltechauthors #185). The
// templates are installed by the app's own routine, run as the service user in the
// app's own context, so they always match the installed RDM version and the
// instance's SEARCH_INDEX_PREFIX: clasm carries no template JSON, which would go stale
// and apply the wrong prefix. Every template the app would install is covered, not
// only the log ones, and clasm names none of them. Only the missing ones are put and
// an existing template is never overwritten, so a run with nothing missing changes
// nothing and exits 0 and a second run is a no-op. There is no confirmation gate:
// installing a template is additive, undoable by deleting it, and touches no index.

// InstallIndexTemplatesCLISlug is the leaf's cliSlug (DR-0177's mechanical rule applied
// to "Install Index Templates").
//
// @example
//
//	clasm rdm-backup-and-restore install-index-templates box
const InstallIndexTemplatesCLISlug = "install-index-templates"

// TemplateInstallFailure is one template the app's routine could not install.
type TemplateInstallFailure struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

// IndexTemplateInstallReport is what InstallRDMIndexTemplates did. Names are the app's own,
// without the instance's prefix; Before and After are the live names, with it.
type IndexTemplateInstallReport struct {
	InstanceID     string                   `json:"instance_id"`
	Instance       string                   `json:"instance"`
	Prefix         string                   `json:"prefix"`
	Before         []string                 `json:"templates_before"`
	After          []string                 `json:"templates_after"`
	Expected       []string                 `json:"expected_templates"`
	AlreadyPresent []string                 `json:"already_present"`
	Installed      []string                 `json:"installed"`
	Failed         []TemplateInstallFailure `json:"failed"`
}

// installTemplatesBody follows indexScriptStart. It reads the live templates, runs the app's
// routine once (a Python snippet in `invenio shell`, as the by-hand repair did) to install
// only the ones the instance lacks, and reads the live templates again. clasm's own curl
// calls are reads; the app does the write. No credentials: OpenSearch answers on localhost.
const installTemplatesBody = `APP=$(dirname "$CFG")
sec before
BEFORE=$(get '/_index_template?filter_path=index_templates.name')
echo "$BEFORE"
sec app
if [ ! -x /home/ubuntu/.local/bin/uv ]; then
  echo status=no_uv
elif [ ! -f "$APP/.env" ]; then
  echo status=no_env
else
  LIVE=$(echo "$BEFORE" | python3 -c 'import sys,json;print(" ".join(t["name"] for t in json.load(sys.stdin).get("index_templates",[])))')
  T=$(mktemp --suffix=.py)
  cat > "$T" <<'PYEOF'
import os
from flask import current_app
from invenio_search import current_search
live = os.environ.get("LIVE_TEMPLATES", "").split()
prefix = current_app.config.get("SEARCH_INDEX_PREFIX", "")
put = current_search.client.indices.put_index_template
for n in sorted(current_search.index_templates):
    print("EXPECTED", n)
    if n in live or prefix + n in live:
        continue
    try:
        current_search._put_template(n, current_search.index_templates[n], put, None)
        print("INSTALL", n)
    except Exception as e:
        print("FAILED", n, str(e).replace("\n", " ")[:200])
print("DONE")
PYEOF
  chmod 644 "$T"
  OUT=$(timeout 180 sudo -u ubuntu -H bash -lc "cd '$APP' && set -a && . ./.env && set +a && export INVENIO_SEARCH_HOSTS='[\"localhost:9200\"]' LIVE_TEMPLATES='$LIVE' && /home/ubuntu/.local/bin/uv run --no-sync invenio shell '$T'" 2>&1)
  rm -f "$T"
  if echo "$OUT" | grep -q '^DONE$'; then
    echo status=ok
    echo "$OUT" | grep -E '^(EXPECTED|INSTALL|FAILED|DONE)'
  else
    echo status=failed
    echo "$OUT" | tail -15 | cut -c1-300 | sed 's/^/OUT /'
  fi
fi
sec after
get '/_index_template?filter_path=index_templates.name'
`

// buildInstallIndexTemplatesCommand builds the one script the install sends.
//
// @param wait {time.Duration} how long to poll for OpenSearch; zero checks once
// @returns {string} the /bin/sh script
// @example
//
//	cmd := buildInstallIndexTemplatesCommand(DefaultIndexHealthWait)
func buildInstallIndexTemplatesCommand(wait time.Duration) string {
	return indexScriptStart(wait) + installTemplatesBody
}

// templateNamesOf lists the template names of a listing, sorted.
func templateNamesOf(b templatesBody) []string {
	names := []string{}
	for _, t := range b.IndexTemplates {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	return names
}

// buildInstallReport turns the script's sections into what was done. Errors carry DR-0014
// classes: 66 no app, uv or .env; 69 OpenSearch not answering; 65 unreadable output; 70 the
// routine crashed, never finished, listed nothing or overwrote a template. A template that
// was reported installed but is not there afterwards is a plain error (exit 1).
func buildInstallReport(sections map[string]string) (IndexTemplateInstallReport, error) {
	r := IndexTemplateInstallReport{Before: []string{}, After: []string{}, Expected: []string{}, AlreadyPresent: []string{}, Installed: []string{}, Failed: []TemplateInstallFailure{}}
	prefix, err := checkPrefixAndEndpoint(sections)
	r.Prefix = prefix
	if err != nil {
		return r, err
	}
	before, err := decodeSection[templatesBody](sections, "before")
	if err != nil {
		return r, err
	}
	after, err := decodeSection[templatesBody](sections, "after")
	if err != nil {
		return r, err
	}
	r.Before, r.After = templateNamesOf(before), templateNamesOf(after)

	app, ok := sections["app"]
	if !ok {
		return r, dataError("app", errors.New("missing from the script's output"))
	}
	switch status := sectionValue(sections, "app", "status"); status {
	case "ok":
	case "no_uv":
		return r, &ExitCodeError{Code: ExitNoInput, Err: errors.New("uv was not found at /home/ubuntu/.local/bin/uv on the instance, so the app's own routine cannot be run; nothing was written")}
	case "no_env":
		return r, &ExitCodeError{Code: ExitNoInput, Err: errors.New("the app directory has no .env file on the instance, so the app's own routine cannot be run; nothing was written")}
	case "failed":
		var out []string
		for _, line := range strings.Split(app, "\n") {
			if rest, ok := strings.CutPrefix(line, "OUT "); ok {
				out = append(out, rest)
			}
		}
		return r, &ExitCodeError{Code: ExitInternal, Err: fmt.Errorf("the app's template routine failed on the instance; its last output:\n%s", strings.Join(out, "\n"))}
	default:
		return r, dataError("app", fmt.Errorf("unknown status %q", status))
	}

	var done bool
	for _, line := range strings.Split(app, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "DONE":
			done = true
		case strings.HasPrefix(line, "EXPECTED "):
			r.Expected = append(r.Expected, strings.TrimPrefix(line, "EXPECTED "))
		case strings.HasPrefix(line, "INSTALL "):
			r.Installed = append(r.Installed, strings.TrimPrefix(line, "INSTALL "))
		case strings.HasPrefix(line, "FAILED "):
			name, msg, _ := strings.Cut(strings.TrimPrefix(line, "FAILED "), " ")
			r.Failed = append(r.Failed, TemplateInstallFailure{Name: name, Error: msg})
		}
	}
	if !done {
		return r, &ExitCodeError{Code: ExitInternal, Err: errors.New("the app's template routine did not finish")}
	}
	if len(r.Expected) == 0 {
		return r, &ExitCodeError{Code: ExitInternal, Err: errors.New("the app's template routine reported no index templates at all")}
	}
	for _, n := range r.Expected {
		if templatePresent(r.Before, r.Prefix, n) {
			r.AlreadyPresent = append(r.AlreadyPresent, n)
		}
	}
	for _, n := range r.Installed {
		if templatePresent(r.Before, r.Prefix, n) {
			return r, &ExitCodeError{Code: ExitInternal, Err: fmt.Errorf("the routine installed %q, which was already present on the instance: an existing template must never be overwritten", n)}
		}
	}
	for _, n := range r.Installed {
		if !templatePresent(r.After, r.Prefix, n) {
			return r, fmt.Errorf("the routine reported installing %q, but it is not on the instance afterwards", n)
		}
	}
	return r, nil
}

// InstallRDMIndexTemplates runs the install on inst through one SSM command and returns what it
// did. Failures to make the run are *ExitCodeError values classed by DR-0014 (see
// buildInstallReport); templates the routine tried and could not install are in the report's
// Failed, with no error, so the caller can show what worked first.
//
// @param ctx {context.Context} cancels the SSM command
// @param client {awsclient.SSMAPI} the SSM client for the instance's region
// @param inst {inventory.Instance} the instance to repair
// @param wait {time.Duration} how long to wait for OpenSearch to answer; zero checks once
// @param poll {time.Duration} how often to poll SSM for the command's result
// @returns {IndexTemplateInstallReport, error} what was installed, or an error naming why the run could not be made
// @example
//
//	report, err := InstallRDMIndexTemplates(ctx, client, inst, DefaultIndexHealthWait, DefaultSSMPollInterval)
func InstallRDMIndexTemplates(ctx context.Context, client awsclient.SSMAPI, inst inventory.Instance, wait, poll time.Duration) (IndexTemplateInstallReport, error) {
	stdout, status, err := RunShellCommand(ctx, client, inst.InstanceID, buildInstallIndexTemplatesCommand(wait), wait+indexHealthScriptSlack, poll)
	if err != nil {
		return IndexTemplateInstallReport{}, &ExitCodeError{Code: ExitUnavailable, Err: fmt.Errorf("running the index template install on %s through SSM: %w", inst.InstanceID, err)}
	}
	if status != "Success" {
		return IndexTemplateInstallReport{}, &ExitCodeError{Code: ExitInternal, Err: fmt.Errorf("the index template script failed on %s (SSM status %s): %s", inst.InstanceID, status, strings.TrimSpace(stdout))}
	}
	report, err := buildInstallReport(splitIndexHealthSections(stdout))
	report.InstanceID, report.Instance = inst.InstanceID, inst.Name
	return report, err
}

// WriteIndexTemplateInstall writes r as text or one JSON document.
//
// @param w {io.Writer} receives the report
// @param r {IndexTemplateInstallReport} the report to write
// @param format {ui.Format} ui.FormatText or ui.FormatJSON
// @returns {error} nil, or the write's error
// @example
//
//	err := WriteIndexTemplateInstall(os.Stdout, report, ui.FormatText)
func WriteIndexTemplateInstall(w io.Writer, r IndexTemplateInstallReport, format ui.Format) error {
	if format != ui.FormatText {
		return ui.WriteJSONValue(w, r, format)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Index templates on %s (%s), index prefix %q\n", r.Instance, r.InstanceID, r.Prefix)
	if len(r.Installed) > 0 {
		fmt.Fprintf(&b, "Installed %d: %s\n", len(r.Installed), strings.Join(r.Installed, ", "))
	} else if len(r.Failed) == 0 {
		fmt.Fprintf(&b, "Nothing to install: all %d of the app's index templates are present\n", len(r.Expected))
	}
	if len(r.AlreadyPresent) > 0 {
		fmt.Fprintf(&b, "Already present, left alone: %s\n", strings.Join(r.AlreadyPresent, ", "))
	}
	for _, f := range r.Failed {
		fmt.Fprintf(&b, "FAILED %s: %s\n", f.Name, f.Error)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

const installIndexTemplatesUsage = "usage: clasm rdm-backup-and-restore " + InstallIndexTemplatesCLISlug + " [-text|-json] [-wait <duration>] <instance-name-or-id>"

const installIndexTemplatesHelp = `

Installs the index templates the instance's RDM app would install and the instance lacks.
A restore that does not bring the cluster's global state back drops every index template;
without them the app's first write creates plain, dynamically mapped indices and the admin
pages stop working (caltechauthors #185). It runs the app's own routine as the service user
on the instance, so the templates match the installed RDM version and the index prefix;
clasm carries no template of its own. Every template the app would install is covered, and
only the missing ones are put: an existing template is never overwritten, so a run with
nothing missing changes nothing and a second run is a no-op. It touches no index and has no
confirmation; a template it installs can be undone by deleting it.

Options:
  -text
      The result as text (the default).
  -json
      The result as one JSON document. On a failure, stdout is an error object
      {"error":{"class","code","message"}} instead.
  -wait <duration>
      How long to wait for OpenSearch to answer, for a run right after a start
      (default 5m; 0 checks once).
  -h, --help
      Show this help.

Exit status: 0 done, or nothing to do; 1 a template failed to install, or one reported
installed is not there; 2 usage; 65 output not readable; 66 no app, uv or .env on the
instance (nothing written); 69 the instance or OpenSearch did not answer; 70 internal.
`

// ParseInstallIndexTemplatesArgs parses install-index-templates' options and its one word,
// the instance name or ID. A problem is a *UsageError (exit 2), raised before AWS is
// touched; --help is a *HelpRequested. The form has no --confirm: it has no gate.
//
// @param args {[]string} the words after the leaf name
// @returns {string, ui.Format, time.Duration, error} the instance word, the format, the wait, and any usage error
// @example
//
//	inst, format, wait, err := ParseInstallIndexTemplatesArgs([]string{"-json", "box"})
func ParseInstallIndexTemplatesArgs(args []string) (instance string, format ui.Format, wait time.Duration, err error) {
	return parseIndexFormArgs(InstallIndexTemplatesCLISlug, installIndexTemplatesUsage, installIndexTemplatesHelp, args)
}

// RunInstallIndexTemplatesCLI is `clasm rdm-backup-and-restore install-index-templates`: it
// resolves the instance (a usage error before any AWS call), runs the install, and writes what
// it did. Templates that failed are written with the ones that worked, then returned as an
// error for exit 1. A run that could not be made returns its *ExitCodeError, and in -json
// mode stdout then holds an error object carrying the class name.
//
// @param ctx {context.Context} cancels the run
// @param w {io.Writer} receives the result (or the JSON error object)
// @param clients {map[string]awsclient.SSMAPI} SSM clients by region
// @param instances {[]inventory.Instance} the inventory the name or ID is resolved against
// @param arg {string} the instance name or ID
// @param format {ui.Format} text or JSON
// @param wait {time.Duration} how long to wait for OpenSearch to answer
// @param poll {time.Duration} how often to poll SSM for the result
// @returns {error} nil when done or nothing to do; a *UsageError for a bad instance; an error for exit 1 when a template failed; otherwise an *ExitCodeError
// @example
//
//	err := RunInstallIndexTemplatesCLI(ctx, os.Stdout, ssmClients, instances, "box", ui.FormatText, DefaultIndexHealthWait, DefaultSSMPollInterval)
func RunInstallIndexTemplatesCLI(ctx context.Context, w io.Writer, clients map[string]awsclient.SSMAPI, instances []inventory.Instance, arg string, format ui.Format, wait, poll time.Duration) error {
	inst, err := resolveInstanceForCLI(arg, instances)
	if err != nil {
		return err
	}
	client, err := resolveSSM(clients, inst.Region)
	if err != nil {
		return err
	}
	report, err := InstallRDMIndexTemplates(ctx, client, inst, wait, poll)
	if err != nil {
		writeIndexJSONError(w, format, err)
		return err
	}
	if err := WriteIndexTemplateInstall(w, report, format); err != nil {
		return err
	}
	if len(report.Failed) > 0 {
		return fmt.Errorf("%d index template(s) failed to install on %s (%s)", len(report.Failed), inst.Name, inst.InstanceID)
	}
	return nil
}

// InstallIndexTemplates is the interactive Install Index Templates leaf: pick an instance, run
// the install, and show what it did.
//
// @param ctx {context.Context} cancels the run
// @param w {io.Writer} receives the result
// @param ssmClients {map[string]awsclient.SSMAPI} SSM clients by region
// @param instances {[]inventory.Instance} the instances to pick from
// @returns {error} nil on success or a cancellation; an error when a template failed or the run could not be made
// @example
//
//	err := InstallIndexTemplates(ctx, os.Stdout, ssmClients, instances)
func InstallIndexTemplates(ctx context.Context, w io.Writer, ssmClients map[string]awsclient.SSMAPI, instances []inventory.Instance) error {
	if len(instances) == 0 {
		fmt.Fprintln(w, "No instances found.")
		return nil
	}
	inst, err := pickInstance(ctx, "Select an instance to repair", "Installs the index templates the RDM app would install and this instance lacks, by running the app's own routine on the instance over SSM. Existing templates are never overwritten and no index is touched.", instances)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return installIndexTemplatesForInstance(ctx, w, ssmClients, inst)
}

// installIndexTemplatesForInstance is InstallIndexTemplates' testable core, once an instance is picked.
func installIndexTemplatesForInstance(ctx context.Context, w io.Writer, clients map[string]awsclient.SSMAPI, inst inventory.Instance) error {
	client, err := resolveSSM(clients, inst.Region)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Installing missing index templates on %s (%s); waiting up to %s for OpenSearch to answer...\n", inst.Name, inst.InstanceID, DefaultIndexHealthWait)
	report, err := InstallRDMIndexTemplates(ctx, client, inst, DefaultIndexHealthWait, DefaultSSMPollInterval)
	if err != nil {
		return err
	}
	if err := WriteIndexTemplateInstall(w, report, ui.FormatText); err != nil {
		return err
	}
	if len(report.Failed) > 0 {
		return fmt.Errorf("%d index template(s) failed to install", len(report.Failed))
	}
	return nil
}

package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// The RDM index health check (DR-0197): a read-only look, through SSM at the
// instance's own unauthenticated OpenSearch on localhost:9200, at whether the
// audit log and the job logs are what the admin page needs. #185 was invisible
// because every layer answered HTTP 200; the one signal that failed was
// _shards.failed in the body of the page's own aggregation, so the check runs
// that aggregation, on the alias <prefix>auditlog and the stream
// <prefix>job-logs by exact name. It never uses a wildcard: a kept rehearsal
// copy such as auditlog-fix-old is plain and text-mapped, and a wildcard swept
// it into a healthy instance's verdict (the survey's false positive).

// RDMIndexHealthCLISlug is the leaf's cliSlug (DR-0177's mechanical rule applied
// to "RDM Index Health").
//
// @example
//
//	clasm rdm-backup-and-restore rdm-index-health box
const RDMIndexHealthCLISlug = "rdm-index-health"

// DefaultIndexHealthWait is how long the check waits for OpenSearch to answer
// before calling the instance unreachable: its JVM took about three minutes to
// answer after a boot, so a check run right after a start must not give up first.
const DefaultIndexHealthWait = 5 * time.Minute

// DR-0014 exit codes this check can return besides 0, 1, 2 and 69.
const (
	// ExitData is DR-0014's 65: content the tool reads is wrong.
	ExitData = 65
	// ExitNoInput is DR-0014's 66: a named input, or the app, is missing.
	ExitNoInput = 66
	// ExitInternal is DR-0014's 70: a bug, or an error nothing classified.
	ExitInternal = 70
)

// indexHealthScriptSlack is added to the wait for the SSM timeout: the app's own
// template list (loading the Flask app) takes up to its own 180-second bound.
const indexHealthScriptSlack = 6 * time.Minute

// IndexHealthKind says what a log index is on the instance.
type IndexHealthKind string

const (
	// IndexKindDataStream is the healthy state: a real data stream.
	IndexKindDataStream IndexHealthKind = "data_stream"
	// IndexKindPlain is an ordinary index where a stream belongs (the #185 state).
	IndexKindPlain IndexHealthKind = "plain"
	// IndexKindOrphaned is a .ds- backing index whose stream does not exist.
	IndexKindOrphaned IndexHealthKind = "orphaned"
	// IndexKindAbsent is nothing at all; the app creates it on its first write.
	IndexKindAbsent IndexHealthKind = "absent"
)

// FieldMapping is the mapping type of one field of one index.
type FieldMapping struct {
	Index string `json:"index"`
	Field string `json:"field"`
	Type  string `json:"type"`
}

// ShardCheck is the result of the admin page's own aggregation.
type ShardCheck struct {
	Ran    bool   `json:"ran"`
	Total  int    `json:"shards_total"`
	Failed int    `json:"shards_failed"`
	Error  string `json:"error,omitempty"`
}

// LogIndexHealth is the state of the audit log or the job logs.
type LogIndexHealth struct {
	Name        string          `json:"name"`
	Kind        IndexHealthKind `json:"kind"`
	Mappings    []FieldMapping  `json:"mappings"`
	Aggregation ShardCheck      `json:"aggregation"`
}

// IndexHealthReport is what CheckRDMIndexHealth found.
type IndexHealthReport struct {
	InstanceID       string         `json:"instance_id"`
	Instance         string         `json:"instance"`
	Prefix           string         `json:"prefix"`
	Templates        []string       `json:"templates"`
	ExpectedKnown    bool           `json:"expected_templates_known"`
	MissingTemplates []string       `json:"missing_templates"`
	DataStreams      []string       `json:"data_streams"`
	AuditLog         LogIndexHealth `json:"audit_log"`
	JobLogs          LogIndexHealth `json:"job_logs"`
	// Strays are indices that look like log indices but are not what the app
	// uses (kept copies, holding indices). Informational: they never fail.
	Strays   []string `json:"strays"`
	Problems []string `json:"problems"`
	Healthy  bool     `json:"healthy"`
}

// the fields whose mapping type decides the admin page: keyword is right, text
// is the #185 defect.
var (
	auditLogFields = []string{"action", "resource.type", "user.id"}
	jobLogFields   = []string{"level"}
)

const (
	auditAggregationBody = `{"aggs":{"r":{"terms":{"field":"resource.type"}},"a":{"terms":{"field":"action"}}}}`
	jobAggregationBody   = `{"aggs":{"l":{"terms":{"field":"level"}}}}`
)

// indexScriptPrelude is the start both RDM index scripts share, the health check and the
// template install (rdm_index_templates.go): find the app and its SEARCH_INDEX_PREFIX, wait a
// bounded time for OpenSearch, and stop with the status when either is missing. SSM runs it
// under /bin/sh. WAIT_LOOP is replaced by the bounded wait; zero removes the polling.
const indexScriptPrelude = `OS=http://localhost:9200
sec() { echo "@@ $1"; }
get() { curl -s -m 30 "$OS$1"; echo; }
CFG=$(find /Sites -maxdepth 2 -name invenio.cfg 2>/dev/null | sort | head -1)
P=
if [ -n "$CFG" ]; then
  P=$(grep '^SEARCH_INDEX_PREFIX' "$CFG" | head -1 | cut -d= -f2 | cut -d'#' -f1 | tr -d " \"'")
fi
sec prefix; echo "cfg=$CFG"; echo "prefix=$P"
[ -n "$P" ] || exit 0
WAIT_LOOP
sec endpoint; echo "status=$c"
[ "$c" = 200 ] || exit 0
`

// indexHealthBody is the health check after the prelude: GET requests, one read-only POST
// for _search, no credentials, no wildcard.
const indexHealthBody = `agg() { curl -s -m 60 -X POST -H 'Content-Type: application/json' "$OS/$1/_search?size=0" -d "$2"; echo; }
sec templates; get '/_index_template?filter_path=index_templates.name'
sec data_streams; get '/_data_stream?filter_path=data_streams.name,data_streams.template,data_streams.indices.index_name'
sec indices; get '/_cat/indices?format=json&h=index,docs.count&expand_wildcards=all'
sec aliases; get '/_cat/aliases?format=json&h=alias,index'
sec mapping_audit; get "/${P}auditlog/_mapping/field/MAPPING_AUDIT"
sec mapping_jobs; get "/${P}job-logs/_mapping/field/MAPPING_JOBS"
sec agg_audit; agg "${P}auditlog" 'AGG_AUDIT'
sec agg_jobs; agg "${P}job-logs" 'AGG_JOBS'
sec app_templates
APP=$(dirname "$CFG")
T=$(mktemp --suffix=.py)
cat > "$T" <<'PYEOF'
from invenio_search import current_search
for n in sorted(current_search.index_templates):
    print("TEMPLATE", n)
PYEOF
chmod 644 "$T"
LIST=$(timeout 180 sudo -u ubuntu -H bash -lc "cd '$APP' && set -a && . ./.env && set +a && export INVENIO_SEARCH_HOSTS='[\"localhost:9200\"]' && /home/ubuntu/.local/bin/uv run --no-sync invenio shell '$T'" 2>&1 | grep '^TEMPLATE ')
rm -f "$T"
if [ -n "$LIST" ]; then echo status=ok; echo "$LIST"; else echo status=failed; fi
`

// indexScriptStart is the shared prelude with its bounded wait filled in.
//
// @param wait {time.Duration} how long to poll for the endpoint; zero checks once
// @returns {string} the start of an RDM index script
// @example
//
//	script := indexScriptStart(5*time.Minute) + body
func indexScriptStart(wait time.Duration) string {
	loop := indexHealthNoWait
	if wait > 0 {
		loop = strings.Replace(indexHealthWaitLoop, "WAIT_SECONDS", strconv.Itoa(int(wait.Seconds())), 1)
	}
	return strings.Replace(indexScriptPrelude, "WAIT_LOOP", loop, 1)
}

const indexHealthWaitLoop = `W=WAIT_SECONDS
i=0
while :; do
  c=$(curl -s -m 5 -o /dev/null -w '%{http_code}' "$OS")
  [ "$c" = 200 ] && break
  [ "$i" -ge "$W" ] && break
  sleep 5
  i=$((i+5))
done`

const indexHealthNoWait = `c=$(curl -s -m 5 -o /dev/null -w '%{http_code}' "$OS")`

// buildIndexHealthCommand builds the one read-only script the check sends.
//
// @param wait {time.Duration} how long to poll for the endpoint; zero checks once
// @returns {string} the /bin/sh script
// @example
//
//	cmd := buildIndexHealthCommand(5 * time.Minute)
func buildIndexHealthCommand(wait time.Duration) string {
	return strings.NewReplacer(
		"MAPPING_AUDIT", strings.Join(auditLogFields, ","),
		"MAPPING_JOBS", strings.Join(jobLogFields, ","),
		"AGG_AUDIT", auditAggregationBody,
		"AGG_JOBS", jobAggregationBody,
	).Replace(indexScriptStart(wait) + indexHealthBody)
}

// splitIndexHealthSections splits the script's output at its "@@ name" markers.
// Text before the first marker is ignored; an empty section exists and is empty.
func splitIndexHealthSections(stdout string) map[string]string {
	sections := map[string]string{}
	var name string
	var body strings.Builder
	flush := func() {
		if name != "" {
			sections[name] = body.String()
		}
		body.Reset()
	}
	for _, line := range strings.SplitAfter(stdout, "\n") {
		if rest, ok := strings.CutPrefix(line, "@@ "); ok {
			flush()
			name = strings.TrimSpace(rest)
			continue
		}
		if name != "" {
			body.WriteString(line)
		}
	}
	flush()
	return sections
}

func dataError(section string, err error) error {
	return &ExitCodeError{Code: ExitData, Err: fmt.Errorf("the index health output is not readable: section %q: %w", section, err)}
}

func decodeSection[T any](sections map[string]string, name string) (T, error) {
	var v T
	body, ok := sections[name]
	if !ok {
		return v, dataError(name, errors.New("missing from the script's output"))
	}
	if strings.TrimSpace(body) == "" {
		return v, dataError(name, errors.New("empty"))
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return v, dataError(name, err)
	}
	return v, nil
}

func sectionValue(sections map[string]string, section, key string) string {
	for _, line := range strings.Split(sections[section], "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key+"="); ok {
			return v
		}
	}
	return ""
}

// templatePresent reports whether the app's template name expected is among the
// live templates: exactly, or with the instance's search prefix the app adds
// when it installs one. A name that is merely the tail of a live name is not a match.
//
// @param live {[]string} the template names on the instance
// @param prefix {string} the instance's SEARCH_INDEX_PREFIX
// @param expected {string} a template name from the app's own list
// @returns {bool} whether the instance has that template
// @example
//
//	templatePresent([]string{"p-job-logs-v1.0.0"}, "p-", "job-logs-v1.0.0") // true
func templatePresent(live []string, prefix, expected string) bool {
	for _, l := range live {
		if l == expected || l == prefix+expected {
			return true
		}
	}
	return false
}

type catIndex struct {
	Index string `json:"index"`
	Docs  string `json:"docs.count"`
}

type catAlias struct {
	Alias string `json:"alias"`
	Index string `json:"index"`
}

type dataStreamsBody struct {
	DataStreams []struct {
		Name    string `json:"name"`
		Indices []struct {
			IndexName string `json:"index_name"`
		} `json:"indices"`
	} `json:"data_streams"`
}

type templatesBody struct {
	IndexTemplates []struct {
		Name string `json:"name"`
	} `json:"index_templates"`
}

type shardsBody struct {
	Shards struct {
		Total  int `json:"total"`
		Failed int `json:"failed"`
	} `json:"_shards"`
	Error json.RawMessage `json:"error"`
}

// openSearchErrorText reads the reason out of an OpenSearch error object (or a
// bare string), falling back to its type.
func openSearchErrorText(raw json.RawMessage) string {
	var obj struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal(raw, &obj) == nil && (obj.Reason != "" || obj.Type != "") {
		if obj.Reason != "" {
			return obj.Reason
		}
		return obj.Type
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return s
	}
	return strings.TrimSpace(string(raw))
}

// classifyAliasTarget decides what the alias stands for: a real stream, a plain
// index, an orphaned .ds- backing index, or nothing. accounted lists every index
// the audit log accounts for, so the rest can be reported as strays.
func classifyAliasTarget(alias string, aliases map[string][]string, indexSet map[string]bool, streams map[string][]string) (kind IndexHealthKind, orphans, accounted []string) {
	var plain, ds bool
	accounted = []string{alias}
	if indexSet[alias] {
		plain = true
	}
	for _, target := range aliases[alias] {
		accounted = append(accounted, target)
		stream, isBacking := dataStreamOfBackingIndex(target)
		switch {
		case !isBacking:
			plain = true
		case streams[stream] != nil:
			ds = true
			accounted = append(accounted, streams[stream]...)
		default:
			orphans = append(orphans, target)
		}
	}
	switch {
	case plain:
		return IndexKindPlain, orphans, accounted
	case len(orphans) > 0:
		return IndexKindOrphaned, orphans, accounted
	case ds:
		return IndexKindDataStream, nil, accounted
	}
	return IndexKindAbsent, nil, accounted
}

// classifyStreamName decides what the exact name stands for, as the job logs'
// stream <prefix>job-logs: a real stream, a plain index of that name, an
// orphaned backing index of it, or nothing.
func classifyStreamName(name string, indexSet map[string]bool, streams map[string][]string) (kind IndexHealthKind, orphans, accounted []string) {
	if backing, ok := streams[name]; ok {
		return IndexKindDataStream, nil, append([]string{name}, backing...)
	}
	if indexSet[name] {
		return IndexKindPlain, nil, []string{name}
	}
	re := regexp.MustCompile(`^\.ds-` + regexp.QuoteMeta(name) + `-\d{6}$`)
	for idx := range indexSet {
		if re.MatchString(idx) {
			orphans = append(orphans, idx)
		}
	}
	sort.Strings(orphans)
	if len(orphans) > 0 {
		return IndexKindOrphaned, orphans, orphans
	}
	return IndexKindAbsent, nil, nil
}

// readMappings reads the field-mapping response of one target. An error body
// (the target does not exist) has no mappings.
func readMappings(section, body string, fields []string) ([]FieldMapping, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &top); err != nil {
		return nil, dataError(section, err)
	}
	if _, isError := top["error"]; isError {
		return nil, nil
	}
	var out []FieldMapping
	for index, raw := range top {
		var one struct {
			Mappings map[string]struct {
				Mapping map[string]struct {
					Type string `json:"type"`
				} `json:"mapping"`
			} `json:"mappings"`
		}
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, dataError(section, err)
		}
		for _, field := range fields {
			for _, m := range one.Mappings[field].Mapping {
				out = append(out, FieldMapping{Index: index, Field: field, Type: m.Type})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Index != out[j].Index {
			return out[i].Index < out[j].Index
		}
		return out[i].Field < out[j].Field
	})
	return out, nil
}

// checkPrefixAndEndpoint reads the two sections every RDM index script starts with and
// classifies what is wrong: 66 when no app (so no prefix) was found, 69 when OpenSearch did
// not answer, 65 when a section is missing.
func checkPrefixAndEndpoint(sections map[string]string) (prefix string, err error) {
	if _, ok := sections["prefix"]; !ok {
		return "", dataError("prefix", errors.New("missing from the script's output"))
	}
	prefix = sectionValue(sections, "prefix", "prefix")
	if prefix == "" {
		return "", &ExitCodeError{Code: ExitNoInput, Err: errors.New("no invenio.cfg with SEARCH_INDEX_PREFIX was found under /Sites on the instance, so there is no app to check")}
	}
	if _, ok := sections["endpoint"]; !ok {
		return prefix, dataError("endpoint", errors.New("missing from the script's output"))
	}
	if status := sectionValue(sections, "endpoint", "status"); status != "200" {
		msg := fmt.Sprintf("OpenSearch on the instance did not answer on localhost:9200 (HTTP status %s)", status)
		if status == "401" || status == "403" {
			msg += "; it wants credentials, and this check carries none"
		}
		return prefix, &ExitCodeError{Code: ExitUnavailable, Err: errors.New(msg)}
	}
	return prefix, nil
}

// buildIndexHealthReport turns the script's sections into the verdict. Errors
// carry DR-0014 classes: 66 no app, 69 OpenSearch not answering, 65 unreadable.
func buildIndexHealthReport(sections map[string]string) (IndexHealthReport, error) {
	r := IndexHealthReport{Templates: []string{}, MissingTemplates: []string{}, DataStreams: []string{}, Strays: []string{}, Problems: []string{}}

	prefix, err := checkPrefixAndEndpoint(sections)
	r.Prefix = prefix
	if err != nil {
		return r, err
	}

	templates, err := decodeSection[templatesBody](sections, "templates")
	if err != nil {
		return r, err
	}
	streamsBody, err := decodeSection[dataStreamsBody](sections, "data_streams")
	if err != nil {
		return r, err
	}
	catIndices, err := decodeSection[[]catIndex](sections, "indices")
	if err != nil {
		return r, err
	}
	catAliases, err := decodeSection[[]catAlias](sections, "aliases")
	if err != nil {
		return r, err
	}

	for _, t := range templates.IndexTemplates {
		r.Templates = append(r.Templates, t.Name)
	}
	sort.Strings(r.Templates)
	streams := map[string][]string{}
	for _, s := range streamsBody.DataStreams {
		backing := []string{}
		for _, i := range s.Indices {
			backing = append(backing, i.IndexName)
		}
		streams[s.Name] = backing
		r.DataStreams = append(r.DataStreams, s.Name)
	}
	sort.Strings(r.DataStreams)
	indexSet := map[string]bool{}
	for _, i := range catIndices {
		indexSet[i.Index] = true
	}
	aliases := map[string][]string{}
	for _, a := range catAliases {
		aliases[a.Alias] = append(aliases[a.Alias], a.Index)
	}

	// Expected templates: whatever the app would install, read from the app.
	if sectionValue(sections, "app_templates", "status") == "ok" {
		for _, line := range strings.Split(sections["app_templates"], "\n") {
			if name, ok := strings.CutPrefix(strings.TrimSpace(line), "TEMPLATE "); ok {
				r.ExpectedKnown = true
				if !templatePresent(r.Templates, r.Prefix, name) {
					r.MissingTemplates = append(r.MissingTemplates, name)
					r.Problems = append(r.Problems, "missing index template: "+name)
				}
			}
		}
		sort.Strings(r.MissingTemplates)
	}

	audit := r.Prefix + "auditlog"
	jobs := r.Prefix + "job-logs"
	var auditOrphans, jobOrphans, auditAccounted, jobAccounted []string
	r.AuditLog.Name, r.JobLogs.Name = audit, jobs
	r.AuditLog.Kind, auditOrphans, auditAccounted = classifyAliasTarget(audit, aliases, indexSet, streams)
	r.JobLogs.Kind, jobOrphans, jobAccounted = classifyStreamName(jobs, indexSet, streams)

	for _, l := range []struct {
		label   string
		log     *LogIndexHealth
		orphans []string
		mapping string
		fields  []string
		agg     string
	}{
		{"audit log", &r.AuditLog, auditOrphans, "mapping_audit", auditLogFields, "agg_audit"},
		{"job logs", &r.JobLogs, jobOrphans, "mapping_jobs", jobLogFields, "agg_jobs"},
	} {
		// Both are reported when both are there: the kind names the worse one,
		// but a repair needs to know about each.
		if l.log.Kind == IndexKindPlain {
			r.Problems = append(r.Problems, fmt.Sprintf("%s %s is a plain index, not a data stream", l.label, l.log.Name))
		}
		if len(l.orphans) > 0 {
			r.Problems = append(r.Problems, fmt.Sprintf("%s %s is an orphaned backing index (%s) with no data stream", l.label, l.log.Name, strings.Join(l.orphans, ", ")))
		}

		mappings, err := readMappings(l.mapping, sections[l.mapping], l.fields)
		if err != nil {
			return r, err
		}
		l.log.Mappings = mappings
		if l.log.Mappings == nil {
			l.log.Mappings = []FieldMapping{}
		}
		for _, m := range mappings {
			if m.Type == "text" {
				r.Problems = append(r.Problems, fmt.Sprintf("%s: field %s in %s is mapped as text; keyword is expected", l.label, m.Field, m.Index))
			}
		}

		if l.log.Kind == IndexKindAbsent {
			continue
		}
		agg, err := decodeSection[shardsBody](sections, l.agg)
		if err != nil {
			return r, err
		}
		l.log.Aggregation = ShardCheck{Ran: true, Total: agg.Shards.Total, Failed: agg.Shards.Failed}
		switch {
		case len(agg.Error) > 0:
			l.log.Aggregation.Error = openSearchErrorText(agg.Error)
			r.Problems = append(r.Problems, fmt.Sprintf("%s: the page's aggregation failed: %s", l.label, l.log.Aggregation.Error))
		case agg.Shards.Failed > 0:
			r.Problems = append(r.Problems, fmt.Sprintf("%s: the page's aggregation had %d of %d shards fail", l.label, agg.Shards.Failed, agg.Shards.Total))
		}
	}

	accounted := map[string]bool{}
	for _, n := range append(auditAccounted, jobAccounted...) {
		accounted[n] = true
	}
	for idx := range indexSet {
		lower := strings.ToLower(idx)
		looksLikeLog := strings.Contains(lower, "auditlog") || strings.Contains(lower, "audit-log") ||
			strings.Contains(lower, "job-logs") || strings.Contains(lower, "joblogs")
		if looksLikeLog && !accounted[idx] {
			r.Strays = append(r.Strays, idx)
		}
	}
	sort.Strings(r.Strays)

	r.Healthy = len(r.Problems) == 0
	return r, nil
}

// runIndexScript sends one RDM index script to an instance through SSM and splits its output into
// sections. An instance SSM cannot reach is 69; a script that did not succeed is 70, since the
// scripts end successfully on every known condition.
func runIndexScript(ctx context.Context, client awsclient.SSMAPI, instanceID, what, script string, timeout, poll time.Duration) (map[string]string, error) {
	stdout, status, err := RunShellCommand(ctx, client, instanceID, script, timeout, poll)
	if err != nil {
		return nil, &ExitCodeError{Code: ExitUnavailable, Err: fmt.Errorf("running %s on %s through SSM: %w", what, instanceID, err)}
	}
	if status != "Success" {
		return nil, &ExitCodeError{Code: ExitInternal, Err: fmt.Errorf("%s script failed on %s (SSM status %s): %s", what, instanceID, status, strings.TrimSpace(stdout))}
	}
	return splitIndexHealthSections(stdout), nil
}

// CheckRDMIndexHealth runs the check on inst through one read-only SSM command and
// returns the verdict. An unhealthy instance is a report with Healthy false and no
// error; an error means the check could not be made, classed by DR-0014: 69 for an
// instance or OpenSearch that does not answer, 66 for no app, 65 for output that
// cannot be read, 70 for a script that failed.
//
// @param ctx {context.Context} cancels the SSM command
// @param client {awsclient.SSMAPI} the SSM client for the instance's region
// @param inst {inventory.Instance} the instance to check
// @param wait {time.Duration} how long to wait for OpenSearch to answer; zero checks once
// @param poll {time.Duration} how often to poll SSM for the command's result
// @returns {IndexHealthReport, error} the verdict, or an *ExitCodeError naming why none could be made
// @example
//
//	report, err := CheckRDMIndexHealth(ctx, client, inst, DefaultIndexHealthWait, DefaultSSMPollInterval)
func CheckRDMIndexHealth(ctx context.Context, client awsclient.SSMAPI, inst inventory.Instance, wait, poll time.Duration) (IndexHealthReport, error) {
	sections, err := runIndexScript(ctx, client, inst.InstanceID, "the index health check", buildIndexHealthCommand(wait), wait+indexHealthScriptSlack, poll)
	if err != nil {
		return IndexHealthReport{}, err
	}
	report, err := buildIndexHealthReport(sections)
	report.InstanceID, report.Instance = inst.InstanceID, inst.Name
	return report, err
}

// WriteIndexHealth writes r as the screen layout (text) or one JSON document.
//
// @param w {io.Writer} receives the report
// @param r {IndexHealthReport} the report to write
// @param format {ui.Format} ui.FormatText or ui.FormatJSON
// @returns {error} nil, or the write's error
// @example
//
//	err := WriteIndexHealth(os.Stdout, report, ui.FormatText)
func WriteIndexHealth(w io.Writer, r IndexHealthReport, format ui.Format) error {
	if format != ui.FormatText {
		return ui.WriteJSONValue(w, r, format)
	}
	verdict := "HEALTHY"
	if !r.Healthy {
		verdict = "UNHEALTHY"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "RDM index health: %s (%s), index prefix %q\nResult: %s\n\n", r.Instance, r.InstanceID, r.Prefix, verdict)
	if r.ExpectedKnown {
		fmt.Fprintf(&b, "Index templates: %d present, %d missing\n", len(r.Templates), len(r.MissingTemplates))
	} else {
		fmt.Fprintf(&b, "Index templates: %d present (the app's own list could not be read, so none is called missing)\n", len(r.Templates))
	}
	fmt.Fprintf(&b, "Data streams: %s\n\n", strings.Join(r.DataStreams, ", "))
	for _, l := range []struct {
		label string
		log   LogIndexHealth
	}{{"Audit log", r.AuditLog}, {"Job logs", r.JobLogs}} {
		fmt.Fprintf(&b, "%s %s: %s\n", l.label, l.log.Name, l.log.Kind)
		for _, m := range l.log.Mappings {
			fmt.Fprintf(&b, "  mapping %s %s = %s\n", m.Index, m.Field, m.Type)
		}
		switch {
		case !l.log.Aggregation.Ran:
			b.WriteString("  aggregation: not run (nothing to aggregate)\n")
		case l.log.Aggregation.Error != "":
			fmt.Fprintf(&b, "  aggregation: error: %s\n", l.log.Aggregation.Error)
		default:
			fmt.Fprintf(&b, "  aggregation: %d of %d shards failed\n", l.log.Aggregation.Failed, l.log.Aggregation.Total)
		}
	}
	if len(r.Strays) > 0 {
		fmt.Fprintf(&b, "\nInformational, not checked: %s look like log indices but are not what the app uses\n", strings.Join(r.Strays, ", "))
	}
	if len(r.Problems) > 0 {
		b.WriteString("\nProblems:\n")
		for _, p := range r.Problems {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// exitClassName names a DR-0014 exit code, for the "class" of a -json error object.
//
// @param code {int} the exit code
// @returns {string} the class name from the table, "" for a code the table does not list
// @example
//
//	exitClassName(69) // "unavailable"
func exitClassName(code int) string {
	return map[int]string{
		0: "ok", 1: "negative", 2: "usage", 65: "data", 66: "no_input", 69: "unavailable",
		70: "internal", 73: "cant_create", 74: "io", 75: "temp_fail", 77: "no_permission", 78: "config",
	}[code]
}

const rdmIndexHealthUsage = "usage: clasm rdm-backup-and-restore " + RDMIndexHealthCLISlug + " [-text|-json] [-wait <duration>] <instance-name-or-id>"

const rdmIndexHealthHelp = `

Checks, read-only, that the instance's audit log and job logs are what the RDM
admin pages need: both are real data streams (not a plain index, not an orphaned
.ds- backing index), the fields the pages aggregate on are keyword, the pages' own
aggregation has no failed shards, and every index template the app would install is
present. It runs through SSM against the instance's own OpenSearch, with no
credentials and no wildcards. Indices that only look like logs, such as kept copies,
are listed as informational and never fail.

Options:
  -text
      The report as text (the default).
  -json
      The report as one JSON document. On a failure to check, stdout is an error
      object {"error":{"class","code","message"}} instead.
  -wait <duration>
      How long to wait for OpenSearch to answer, for a check right after a start
      (default 5m; 0 checks once).
  -h, --help
      Show this help.

Exit status: 0 healthy; 1 a problem was found; 2 usage; 65 output not readable; 66
no app found on the instance; 69 the instance or OpenSearch did not answer; 70 internal.
`

// parseIndexFormArgs parses the options the RDM index forms share, -text, -json and -wait, and their
// one word, the instance name or ID. withConfirm adds --confirm, repeatable, for a form behind the
// DR-0180 gate; a form without a gate rejects it as an unknown option. Every problem is a *UsageError
// (exit 2) raised before AWS is touched; --help is a *HelpRequested carrying usage and help.
func parseIndexFormArgs(leaf, usage, help string, withConfirm bool, args []string) (instance string, format ui.Format, wait time.Duration, confirms []string, err error) {
	fs := flag.NewFlagSet(leaf, flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the errors below carry the usage; nothing prints twice
	text := fs.Bool("text", false, "")
	asJSON := fs.Bool("json", false, "")
	waitFlag := fs.Duration("wait", DefaultIndexHealthWait, "")
	if withConfirm {
		fs.Func("confirm", "", func(v string) error {
			if v == "" {
				return errors.New("--confirm needs a value")
			}
			confirms = append(confirms, v)
			return nil
		})
	}
	if perr := fs.Parse(args); perr != nil {
		if errors.Is(perr, flag.ErrHelp) {
			return "", ui.FormatText, 0, nil, &HelpRequested{Usage: usage + help}
		}
		return "", ui.FormatText, 0, nil, &UsageError{Msg: fmt.Sprintf("%s: %v\n%s", leaf, perr, usage)}
	}
	if *text && *asJSON {
		return "", ui.FormatText, 0, nil, &UsageError{Msg: fmt.Sprintf("%s: give only one of -text, -json\n%s", leaf, usage)}
	}
	if *waitFlag < 0 {
		return "", ui.FormatText, 0, nil, &UsageError{Msg: fmt.Sprintf("%s: -wait must not be negative\n%s", leaf, usage)}
	}
	words := fs.Args()
	if len(words) != 1 || strings.TrimSpace(words[0]) == "" {
		return "", ui.FormatText, 0, nil, &UsageError{Msg: fmt.Sprintf("%s: want one instance name or ID, got %d arguments\n%s", leaf, len(words), usage)}
	}
	format = ui.FormatText
	if *asJSON {
		format = ui.FormatJSON
	}
	return strings.TrimSpace(words[0]), format, *waitFlag, confirms, nil
}

// writeIndexJSONError writes the -json error object, {"error":{"class","code","message"}},
// so a program reads the DR-0014 class by name as well as by number. Text mode writes nothing:
// the error goes to stderr.
func writeIndexJSONError(w io.Writer, format ui.Format, err error) {
	if format == ui.FormatText {
		return
	}
	code := CLIExitCode(err)
	obj := map[string]any{"error": map[string]any{"class": exitClassName(code), "code": code, "message": err.Error()}}
	_ = ui.WriteJSONValue(w, obj, format)
}

// ParseRDMIndexHealthArgs parses rdm-index-health's options and its one word, the
// instance name or ID. A problem is a *UsageError (exit 2), raised before AWS is
// touched; --help is a *HelpRequested.
//
// @param args {[]string} the words after the leaf name
// @returns {string, ui.Format, time.Duration, error} the instance word, the format, the wait, and any usage error
// @example
//
//	inst, format, wait, err := ParseRDMIndexHealthArgs([]string{"-json", "box"})
func ParseRDMIndexHealthArgs(args []string) (instance string, format ui.Format, wait time.Duration, err error) {
	instance, format, wait, _, err = parseIndexFormArgs(RDMIndexHealthCLISlug, rdmIndexHealthUsage, rdmIndexHealthHelp, false, args)
	return instance, format, wait, err
}

// RunRDMIndexHealthCLI is `clasm rdm-backup-and-restore rdm-index-health`: it
// resolves the instance (a usage error before any AWS call), runs the check, and
// writes the report. An unhealthy instance is written as the report and returned as
// an error for exit 1; a check that could not be made returns its *ExitCodeError,
// and in -json mode stdout then holds an error object carrying the class name.
//
// @param ctx {context.Context} cancels the check
// @param w {io.Writer} receives the report (or the JSON error object)
// @param clients {map[string]awsclient.SSMAPI} SSM clients by region
// @param instances {[]inventory.Instance} the inventory the name or ID is resolved against
// @param arg {string} the instance name or ID
// @param format {ui.Format} text or JSON
// @param wait {time.Duration} how long to wait for OpenSearch to answer
// @param poll {time.Duration} how often to poll SSM for the result
// @returns {error} nil when healthy; a *UsageError for a bad instance; an error for exit 1 when unhealthy; otherwise an *ExitCodeError
// @example
//
//	err := RunRDMIndexHealthCLI(ctx, os.Stdout, ssmClients, instances, "box", ui.FormatText, DefaultIndexHealthWait, DefaultSSMPollInterval)
func RunRDMIndexHealthCLI(ctx context.Context, w io.Writer, clients map[string]awsclient.SSMAPI, instances []inventory.Instance, arg string, format ui.Format, wait, poll time.Duration) error {
	inst, err := resolveInstanceForCLI(arg, instances)
	if err != nil {
		return err
	}
	client, err := resolveSSM(clients, inst.Region)
	if err != nil {
		return err
	}
	report, err := CheckRDMIndexHealth(ctx, client, inst, wait, poll)
	if err != nil {
		writeIndexJSONError(w, format, err)
		return err
	}
	if err := WriteIndexHealth(w, report, format); err != nil {
		return err
	}
	if !report.Healthy {
		return fmt.Errorf("%s (%s) is not healthy: %d problem(s) found", inst.Name, inst.InstanceID, len(report.Problems))
	}
	return nil
}

// RDMIndexHealth is the interactive RDM Index Health leaf: pick an instance, run
// the check, and show the report.
//
// @param ctx {context.Context} cancels the check
// @param w {io.Writer} receives the report
// @param ssmClients {map[string]awsclient.SSMAPI} SSM clients by region
// @param instances {[]inventory.Instance} the instances to pick from
// @returns {error} nil on a report (healthy or not) or a cancellation; an error when the check could not be made
// @example
//
//	err := RDMIndexHealth(ctx, os.Stdout, ssmClients, instances)
func RDMIndexHealth(ctx context.Context, w io.Writer, ssmClients map[string]awsclient.SSMAPI, instances []inventory.Instance) error {
	if len(instances) == 0 {
		fmt.Fprintln(w, "No instances found.")
		return nil
	}
	inst, err := pickInstance(ctx, "Select an instance to check", "Connects to this instance via SSM and reads its OpenSearch (read-only) to check the audit log and job logs the RDM admin pages depend on.", instances)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return rdmIndexHealthForInstance(ctx, w, ssmClients, inst)
}

// rdmIndexHealthForInstance is RDMIndexHealth's testable core, once an instance is
// picked. An unhealthy report is shown and returns nil: the menu would otherwise
// print the same verdict a second time as an Error line.
func rdmIndexHealthForInstance(ctx context.Context, w io.Writer, clients map[string]awsclient.SSMAPI, inst inventory.Instance) error {
	client, err := resolveSSM(clients, inst.Region)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Checking %s (%s); waiting up to %s for OpenSearch to answer...\n", inst.Name, inst.InstanceID, DefaultIndexHealthWait)
	report, err := CheckRDMIndexHealth(ctx, client, inst, DefaultIndexHealthWait, DefaultSSMPollInterval)
	if err != nil {
		return err
	}
	return WriteIndexHealth(w, report, ui.FormatText)
}

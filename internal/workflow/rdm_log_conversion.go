package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// Convert the audit log and the job logs to real data streams, copy first (DR-0198).
//
// A restore with include_global_state:false leaves them as an orphaned .ds- backing index (the
// audit log's old history) and/or a plain, dynamically mapped index with the stream's name (what the
// app wrote once the templates were gone). The admin pages aggregate on keyword fields, so they stop
// working (caltechauthors #185). The repair, rehearsed by hand on a test instance and run on
// production, has a fixed order that is the whole of its safety:
//
//  1. read-only preconditions (a data-stream template must match, the copy names must be free);
//  2. create the holding indices -- named outside the template's pattern, since a holding index named
//     <prefix>auditlog* would itself be taken for a stream -- and copy the orphan and the plain index
//     into them, verifying the orphan's copy by count;
//  3. take the alias off both, then delete the orphan, which has to go first: it is named exactly what
//     the stream's first backing index must be;
//  4. copy what the app wrote meanwhile (a delta) until the counts of the plain index and its copy agree;
//  5. delete the plain index and create the stream in one step, since from then the app's writes land in
//     the stream;
//  6. load the copies into the stream with op_type create (a stream rejects index and update operations)
//     and verify it holds at least what the copies hold.
//
// The copies are kept and named; clasm never deletes them. A failure at any step leaves every document
// in the orphan, the plain index, the stream or a copy, and the error says where the copies are.

// ConvertLogIndicesCLISlug is the leaf's cliSlug (DR-0177's mechanical rule applied to
// "Convert Log Indices").
//
// @example
//
//	clasm rdm-backup-and-restore convert-log-indices --confirm box box
const ConvertLogIndicesCLISlug = "convert-log-indices"

// deltaAttempts bounds how many times the plain index is copied again while the app keeps writing
// before the conversion gives up without deleting it.
const deltaAttempts = 3

// DefaultReindexTimeout bounds one reindex: 306,000 audit-log documents do not fit one SSM call, so a
// reindex runs as a task that is polled, and this is how long it may take.
const DefaultReindexTimeout = 30 * time.Minute

// indexOps is what the conversion needs from OpenSearch. The algorithm is written against it so
// every ordering rule can be tested on a fake that records its calls and can fail any of them; the
// SSM implementation (ssmIndexOps) is tested for its exact commands.
type indexOps interface {
	// Count refreshes the index and returns its document count.
	Count(ctx context.Context, index string) (int64, error)
	// CreateIndex creates an empty index with no replicas.
	CreateIndex(ctx context.Context, name string) error
	// Reindex copies src into dst with op_type create (an id already in dst is skipped) and waits for it.
	Reindex(ctx context.Context, src, dst string) error
	// RemoveAlias takes alias off index.
	RemoveAlias(ctx context.Context, index, alias string) error
	// DeleteIndex deletes exactly one index by exact name.
	DeleteIndex(ctx context.Context, name string) error
	// CreateDataStream creates the stream; one that already exists is success.
	CreateDataStream(ctx context.Context, name string) error
}

// logConversion is the conversion of one log: what exists, what is copied where.
type logConversion struct {
	Label  string // "audit log" or "job logs"
	Stream string // the data stream's name
	// Orphan is the orphaned .ds-<Stream>-000001 backing index, "" if none; Plain is the plain index
	// named Stream, "" if none. At least one is set.
	Orphan, Plain         string
	OrphanDocs, PlainDocs int64
	// Aliases are the (index, alias) pairs to remove before the orphan is deleted.
	Aliases [][2]string
	// HoldOrphan and HoldPlain name the copies; "" when there is nothing to copy.
	HoldOrphan, HoldPlain string
}

// conversionPlan is what DiscoverLogConversion found: the logs to convert (none when every log is
// already a data stream) and notes about the ones that are not touched.
type conversionPlan struct {
	Prefix string
	Logs   []logConversion
	Notes  []string
}

// logResult is what converting one log did.
type logResult struct {
	Label      string   `json:"label"`
	Stream     string   `json:"stream"`
	OrphanDocs int64    `json:"orphan_documents"`
	PlainDocs  int64    `json:"plain_documents"`
	StreamDocs int64    `json:"stream_documents"`
	Copies     []string `json:"copies_kept"`
}

// conversionError is a failed conversion step. It names the copies that exist, which hold every
// document, so the operator can find them.
type conversionError struct {
	Label, Step string
	Err         error
	Copies      []string
	// Earlier are the copies kept by logs that converted before this one failed.
	Earlier []string
}

func (e *conversionError) Error() string {
	var msg string
	if len(e.Copies) == 0 {
		msg = fmt.Sprintf("converting the %s failed at %s: %v; nothing was changed", e.Label, e.Step, e.Err)
	} else {
		msg = fmt.Sprintf("converting the %s failed at %s: %v; every document is safe, the copies %s hold them and clasm never deletes them",
			e.Label, e.Step, e.Err, strings.Join(e.Copies, ", "))
	}
	if len(e.Earlier) > 0 {
		msg += fmt.Sprintf("; the logs converted before it kept their copies %s", strings.Join(e.Earlier, ", "))
	}
	return msg
}

func (e *conversionError) Unwrap() error { return e.Err }

// holdingNames are the copy names. They start with neither the instance prefix nor anything a
// data-stream template pattern covers.
func holdingNames(kind string) (orphan, plain string) {
	return "clasm-fix-" + kind + "-orphan", "clasm-fix-" + kind + "-plain"
}

// dsTemplate is a composable index template that defines a data stream.
type dsTemplate struct {
	Name     string
	Patterns []string
}

func streamTemplateFor(templates []dsTemplate, stream string) (dsTemplate, bool) {
	for _, t := range templates {
		for _, p := range t.Patterns {
			if ok, _ := path.Match(p, stream); ok {
				return t, true
			}
		}
	}
	return dsTemplate{}, false
}

type templatesDSBody struct {
	IndexTemplates []struct {
		Name          string `json:"name"`
		IndexTemplate struct {
			IndexPatterns []string        `json:"index_patterns"`
			DataStream    json.RawMessage `json:"data_stream"`
		} `json:"index_template"`
	} `json:"index_templates"`
}

// conversionFacts is what discovery read, indexed for the planners.
type conversionFacts struct {
	prefix    string
	streams   map[string][]string
	docs      map[string]int64
	aliases   map[string][]string
	templates []dsTemplate
}

func refuse(format string, a ...any) error { return fmt.Errorf("not converting: "+format, a...) }

// checkConversionPreconditions is the part of the preconditions every log shares.
func checkConversionPreconditions(f conversionFacts, lc logConversion) error {
	if _, ok := streamTemplateFor(f.templates, lc.Stream); !ok {
		return refuse("no index template on the instance defines a data stream for %q, so the stream would be created with the wrong mapping; run install-index-templates first", lc.Stream)
	}
	for _, hold := range []string{lc.HoldOrphan, lc.HoldPlain} {
		if hold == "" {
			continue
		}
		if _, taken := f.docs[hold]; taken {
			return refuse("the copy name %q is already taken, left by an earlier conversion; clasm never deletes copies, so remove it yourself once the page is confirmed", hold)
		}
		if strings.HasPrefix(hold, f.prefix) {
			return &ExitCodeError{Code: ExitInternal, Err: fmt.Errorf("the copy name %q starts with the instance prefix, where a data-stream template could take it for a stream", hold)}
		}
		if _, ok := streamTemplateFor(f.templates, hold); ok {
			return &ExitCodeError{Code: ExitInternal, Err: fmt.Errorf("the copy name %q matches a data-stream template pattern, so it would become a stream", hold)}
		}
	}
	return nil
}

var orphanGenerationRE = regexp.MustCompile(`^\.ds-(.+)-(\d{6})$`)

// planAuditLog plans the audit log: the alias <prefix>auditlog points at the orphan, the plain index, or both.
func planAuditLog(f conversionFacts) (*logConversion, string, error) {
	alias := f.prefix + "auditlog"
	if _, isIndex := f.docs[alias]; isIndex {
		return nil, "", refuse("an index is named %q, the audit log's alias; remove or rename it first", alias)
	}
	var orphans, plains []string
	for _, target := range f.aliases[alias] {
		stream, backing := dataStreamOfBackingIndex(target)
		switch {
		case !backing:
			plains = append(plains, target)
		case f.streams[stream] == nil:
			orphans = append(orphans, target)
		}
	}
	if len(orphans) == 0 && len(plains) == 0 {
		if len(f.aliases[alias]) == 0 {
			return nil, "audit log: nothing found behind " + alias + " (the app creates it on its first write)", nil
		}
		return nil, "audit log: already a data stream", nil
	}
	if len(orphans) > 1 {
		return nil, "", refuse("the audit log has more than one orphaned backing index (%s); that is not a case clasm converts, repair it by hand", strings.Join(orphans, ", "))
	}
	if len(plains) > 1 {
		return nil, "", refuse("the audit log has more than one plain index (%s); that is not a case clasm converts, repair it by hand", strings.Join(plains, ", "))
	}
	lc := logConversion{Label: "audit log"}
	lc.HoldOrphan, lc.HoldPlain = holdingNames("auditlog")
	if len(orphans) == 1 {
		m := orphanGenerationRE.FindStringSubmatch(orphans[0])
		if m[2] != "000001" {
			return nil, "", refuse("the orphaned backing index %q is generation %s; a new stream starts at 000001, so only generation 000001 can be converted", orphans[0], m[2])
		}
		lc.Orphan, lc.Stream, lc.OrphanDocs = orphans[0], m[1], f.docs[orphans[0]]
		lc.Aliases = append(lc.Aliases, [2]string{orphans[0], alias})
	} else {
		lc.HoldOrphan = ""
	}
	if len(plains) == 1 {
		if lc.Stream != "" && lc.Stream != plains[0] {
			return nil, "", refuse("the audit log's orphan (stream %q) and plain index (%q) disagree about the stream's name", lc.Stream, plains[0])
		}
		lc.Plain, lc.Stream, lc.PlainDocs = plains[0], plains[0], f.docs[plains[0]]
		lc.Aliases = append(lc.Aliases, [2]string{plains[0], alias})
	} else {
		lc.HoldPlain = ""
	}
	if f.streams[lc.Stream] != nil {
		return nil, "", refuse("a data stream %q already exists alongside its orphan or plain index; that needs attention by hand", lc.Stream)
	}
	if err := checkConversionPreconditions(f, lc); err != nil {
		return nil, "", err
	}
	return &lc, "", nil
}

// planJobLogs plans the job logs: the stream <prefix>job-logs, which has no alias.
func planJobLogs(f conversionFacts) (*logConversion, string, error) {
	name := f.prefix + "job-logs"
	_, plain := f.docs[name]
	if f.streams[name] != nil {
		if plain {
			return nil, "", refuse("a data stream %q already exists and so does a plain index of that name; that needs attention by hand", name)
		}
		return nil, "job logs: already a data stream", nil
	}
	orphanRE := regexp.MustCompile(`^\.ds-` + regexp.QuoteMeta(name) + `-(\d{6})$`)
	var orphans []string
	for index := range f.docs {
		if orphanRE.MatchString(index) {
			orphans = append(orphans, index)
		}
	}
	sort.Strings(orphans)
	if !plain && len(orphans) == 0 {
		return nil, "job logs: nothing found (the app creates them on the first job)", nil
	}
	if len(orphans) > 1 {
		return nil, "", refuse("the job logs have more than one orphaned backing index (%s); that is not a case clasm converts, repair it by hand", strings.Join(orphans, ", "))
	}
	lc := logConversion{Label: "job logs", Stream: name}
	lc.HoldOrphan, lc.HoldPlain = holdingNames("job-logs")
	if len(orphans) == 1 {
		if gen := orphanRE.FindStringSubmatch(orphans[0])[1]; gen != "000001" {
			return nil, "", refuse("the orphaned backing index %q is generation %s; only generation 000001 can be converted", orphans[0], gen)
		}
		lc.Orphan, lc.OrphanDocs = orphans[0], f.docs[orphans[0]]
	} else {
		lc.HoldOrphan = ""
	}
	if plain {
		lc.Plain, lc.PlainDocs = name, f.docs[name]
	} else {
		lc.HoldPlain = ""
	}
	if err := checkConversionPreconditions(f, lc); err != nil {
		return nil, "", err
	}
	return &lc, "", nil
}

// buildConversionPlan turns the discovery script's sections into a plan, or a refusal. A refusal is
// a plain error (exit 1) naming what is wrong; instance problems carry DR-0014 classes as everywhere.
func buildConversionPlan(sections map[string]string) (conversionPlan, error) {
	prefix, err := checkPrefixAndEndpoint(sections)
	plan := conversionPlan{Prefix: prefix}
	if err != nil {
		return plan, err
	}
	streamsBody, err := decodeSection[dataStreamsBody](sections, "data_streams")
	if err != nil {
		return plan, err
	}
	indices, err := decodeSection[[]catIndex](sections, "indices")
	if err != nil {
		return plan, err
	}
	aliasRows, err := decodeSection[[]catAlias](sections, "aliases")
	if err != nil {
		return plan, err
	}
	templates, err := decodeSection[templatesDSBody](sections, "templates_ds")
	if err != nil {
		return plan, err
	}
	f := conversionFacts{prefix: prefix, streams: map[string][]string{}, docs: map[string]int64{}, aliases: map[string][]string{}}
	for _, s := range streamsBody.DataStreams {
		backing := []string{}
		for _, i := range s.Indices {
			backing = append(backing, i.IndexName)
		}
		f.streams[s.Name] = backing
	}
	for _, i := range indices {
		n, _ := strconv.ParseInt(i.Docs, 10, 64)
		f.docs[i.Index] = n
	}
	for _, a := range aliasRows {
		f.aliases[a.Alias] = append(f.aliases[a.Alias], a.Index)
	}
	for _, t := range templates.IndexTemplates {
		if len(t.IndexTemplate.DataStream) > 0 && string(t.IndexTemplate.DataStream) != "null" {
			f.templates = append(f.templates, dsTemplate{Name: t.Name, Patterns: t.IndexTemplate.IndexPatterns})
		}
	}
	for _, planner := range []func(conversionFacts) (*logConversion, string, error){planAuditLog, planJobLogs} {
		lc, note, err := planner(f)
		if err != nil {
			return plan, err
		}
		if lc != nil {
			plan.Logs = append(plan.Logs, *lc)
		}
		if note != "" {
			plan.Notes = append(plan.Notes, note)
		}
	}
	return plan, nil
}

// conversionDiscoveryBody follows indexScriptStart: GET requests only. Hidden indices are included
// (expand_wildcards=all): an orphaned backing index keeps the hidden setting it had as a stream's.
const conversionDiscoveryBody = `sec data_streams; get '/_data_stream?filter_path=data_streams.name,data_streams.indices.index_name'
sec indices; get '/_cat/indices?format=json&h=index,docs.count&expand_wildcards=all'
sec aliases; get '/_cat/aliases?format=json&h=alias,index'
sec templates_ds; get '/_index_template?filter_path=index_templates.name,index_templates.index_template.index_patterns,index_templates.index_template.data_stream'
`

// buildConversionDiscoveryCommand builds the read-only script that says what a conversion would do.
//
// @param wait {time.Duration} how long to poll for OpenSearch; zero checks once
// @returns {string} the /bin/sh script
// @example
//
//	cmd := buildConversionDiscoveryCommand(DefaultIndexHealthWait)
func buildConversionDiscoveryCommand(wait time.Duration) string {
	return indexScriptStart(wait) + conversionDiscoveryBody
}

// DiscoverLogConversion reads inst through one read-only SSM command and says what a conversion would
// do. An instance that is already healthy yields a plan with no logs; a state clasm will not convert
// is a refusal (a plain error, exit 1) naming why.
//
// @param ctx {context.Context} cancels the SSM command
// @param client {awsclient.SSMAPI} the SSM client for the instance's region
// @param inst {inventory.Instance} the instance to read
// @param wait {time.Duration} how long to wait for OpenSearch to answer
// @param poll {time.Duration} how often to poll SSM for the command's result
// @returns {conversionPlan, error} the plan, or a refusal or an *ExitCodeError
// @example
//
//	plan, err := DiscoverLogConversion(ctx, client, inst, DefaultIndexHealthWait, DefaultSSMPollInterval)
func DiscoverLogConversion(ctx context.Context, client awsclient.SSMAPI, inst inventory.Instance, wait, poll time.Duration) (conversionPlan, error) {
	sections, err := runIndexScript(ctx, client, inst.InstanceID, "the log index discovery", buildConversionDiscoveryCommand(wait), wait+indexHealthScriptSlack, poll)
	if err != nil {
		return conversionPlan{}, err
	}
	return buildConversionPlan(sections)
}

// ConvertLogIndices converts every log in plan, copy first, in the fixed order of the header comment,
// and stops at the first log that fails. It carries no confirmation: the caller has gated it.
//
// @param ctx {context.Context} cancels the run before its next step
// @param w {io.Writer} receives one progress line per step
// @param ops {indexOps} the OpenSearch calls
// @param plan {conversionPlan} what to convert
// @returns {[]logResult, error} one result per converted log; a *conversionError naming the copies when a step fails
// @example
//
//	results, err := ConvertLogIndices(ctx, os.Stdout, newSSMIndexOps(client, id, DefaultSSMPollInterval, DefaultReindexTimeout), plan)
func ConvertLogIndices(ctx context.Context, w io.Writer, ops indexOps, plan conversionPlan) ([]logResult, error) {
	var results []logResult
	for _, lc := range plan.Logs {
		r, err := convertOne(ctx, w, ops, lc)
		if err != nil {
			var ce *conversionError
			if errors.As(err, &ce) {
				for _, done := range results {
					ce.Earlier = append(ce.Earlier, done.Copies...)
				}
			}
			return results, err
		}
		results = append(results, r)
	}
	return results, nil
}

func convertOne(ctx context.Context, w io.Writer, ops indexOps, lc logConversion) (logResult, error) {
	var copies []string
	fail := func(step string, err error) (logResult, error) {
		return logResult{}, &conversionError{Label: lc.Label, Step: step, Err: err, Copies: slicesClone(copies)}
	}
	// alive is checked before every step: a cancelled run stops without starting the next one.
	step := func(name string, do func() error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		fmt.Fprintf(w, "  %s: %s\n", lc.Label, name)
		return do()
	}
	count := func(index string) (n int64, err error) {
		err = step("counting "+index, func() (e error) { n, e = ops.Count(ctx, index); return })
		return
	}
	copyInto := func(src, dst string) error {
		return step("copying "+src+" into "+dst, func() error { return ops.Reindex(ctx, src, dst) })
	}

	// 2. holding indices, then the copies
	for _, hold := range []string{lc.HoldOrphan, lc.HoldPlain} {
		if hold == "" {
			continue
		}
		if err := step("creating the copy "+hold, func() error { return ops.CreateIndex(ctx, hold) }); err != nil {
			return fail("creating the copy "+hold, err)
		}
		copies = append(copies, hold)
	}
	if lc.Orphan != "" {
		if err := copyInto(lc.Orphan, lc.HoldOrphan); err != nil {
			return fail("copying the orphan", err)
		}
		src, err := count(lc.Orphan)
		if err != nil {
			return fail("counting the orphan", err)
		}
		dst, err := count(lc.HoldOrphan)
		if err != nil {
			return fail("counting the orphan's copy", err)
		}
		if src != dst {
			return fail("verifying the orphan's copy", fmt.Errorf("%s has %d documents but its copy %s has %d", lc.Orphan, src, lc.HoldOrphan, dst))
		}
	}
	if lc.Plain != "" {
		if err := copyInto(lc.Plain, lc.HoldPlain); err != nil {
			return fail("copying the plain index", err)
		}
	}

	// 3. the alias off both, then the orphan, whose name the stream needs
	for _, pair := range lc.Aliases {
		if err := step("removing alias "+pair[1]+" from "+pair[0], func() error { return ops.RemoveAlias(ctx, pair[0], pair[1]) }); err != nil {
			return fail("removing an alias", err)
		}
	}
	if lc.Orphan != "" {
		if err := step("deleting the orphan "+lc.Orphan+" (its copy is verified)", func() error { return ops.DeleteIndex(ctx, lc.Orphan) }); err != nil {
			return fail("deleting the orphan", err)
		}
	}

	// 4. the delta: what the app wrote since the first copy, until the plain index and its copy agree
	if lc.Plain != "" {
		agreed := false
		for attempt := 0; attempt < deltaAttempts && !agreed; attempt++ {
			if err := copyInto(lc.Plain, lc.HoldPlain); err != nil {
				return fail("copying what the app wrote meanwhile", err)
			}
			src, err := count(lc.Plain)
			if err != nil {
				return fail("counting the plain index", err)
			}
			dst, err := count(lc.HoldPlain)
			if err != nil {
				return fail("counting the plain index's copy", err)
			}
			agreed = src == dst
		}
		if !agreed {
			return fail("verifying the plain index's copy", fmt.Errorf("%s is still growing and its copy %s did not catch up in %d tries; the plain index was not deleted", lc.Plain, lc.HoldPlain, deltaAttempts))
		}
		// 5. delete the plain index and create the stream, back to back
		if err := step("deleting the plain index "+lc.Plain+" (its copy is verified)", func() error { return ops.DeleteIndex(ctx, lc.Plain) }); err != nil {
			return fail("deleting the plain index", err)
		}
	}
	if err := step("creating the data stream "+lc.Stream, func() error { return ops.CreateDataStream(ctx, lc.Stream) }); err != nil {
		return fail("creating the data stream", err)
	}

	// 6. load the copies and verify
	var expected, orphanDocs, plainDocs int64
	for _, hold := range []struct {
		name string
		into *int64
	}{{lc.HoldOrphan, &orphanDocs}, {lc.HoldPlain, &plainDocs}} {
		if hold.name == "" {
			continue
		}
		if err := copyInto(hold.name, lc.Stream); err != nil {
			return fail("loading "+hold.name+" into the stream", err)
		}
		n, err := count(hold.name)
		if err != nil {
			return fail("counting "+hold.name, err)
		}
		*hold.into = n
		expected += n
	}
	got, err := count(lc.Stream)
	if err != nil {
		return fail("counting the stream", err)
	}
	if got < expected {
		return fail("verifying the stream", fmt.Errorf("the stream %s has %d documents but the copies hold %d", lc.Stream, got, expected))
	}
	return logResult{Label: lc.Label, Stream: lc.Stream, OrphanDocs: orphanDocs, PlainDocs: plainDocs, StreamDocs: got, Copies: copies}, nil
}

func slicesClone(s []string) []string { return append([]string(nil), s...) }

// ---- the SSM implementation ----

// ssmIndexOps is indexOps over SSM: one curl to the instance's own unauthenticated OpenSearch per call.
type ssmIndexOps struct {
	client     awsclient.SSMAPI
	instanceID string
	poll       time.Duration
	timeout    time.Duration
}

// newSSMIndexOps makes the SSM implementation.
//
// @param client {awsclient.SSMAPI} the SSM client for the instance's region
// @param instanceID {string} the instance
// @param poll {time.Duration} how often to poll SSM, and a reindex task
// @param reindexTimeout {time.Duration} how long one reindex may take; zero means DefaultReindexTimeout
// @returns {indexOps} the OpenSearch calls
// @example
//
//	ops := newSSMIndexOps(client, "i-0abc", DefaultSSMPollInterval, DefaultReindexTimeout)
func newSSMIndexOps(client awsclient.SSMAPI, instanceID string, poll, reindexTimeout time.Duration) indexOps {
	if reindexTimeout == 0 {
		reindexTimeout = DefaultReindexTimeout
	}
	return &ssmIndexOps{client: client, instanceID: instanceID, poll: poll, timeout: reindexTimeout}
}

const osBase = "http://localhost:9200"

// curlOS builds one curl to OpenSearch. A 4xx or 5xx makes curl fail and keep the body, so the
// error text reaches the caller.
func curlOS(method, urlPath, body string) string {
	cmd := "curl -sS --fail-with-body -m 120"
	if method != "GET" {
		cmd += " -X " + method
	}
	cmd += " " + shellQuote(osBase+urlPath)
	if body != "" {
		cmd += " -H 'Content-Type: application/json' -d " + shellQuote(body)
	}
	return cmd
}

func (o *ssmIndexOps) run(ctx context.Context, what, command string) (string, error) {
	stdout, status, err := RunShellCommand(ctx, o.client, o.instanceID, command, DefaultOpenSearchRESTTimeout, o.poll)
	if err != nil {
		return "", &ExitCodeError{Code: ExitUnavailable, Err: fmt.Errorf("%s on %s through SSM: %w", what, o.instanceID, err)}
	}
	if status != "Success" {
		return "", fmt.Errorf("%s failed on %s (SSM status %s): %s", what, o.instanceID, status, strings.TrimSpace(stdout))
	}
	return stdout, nil
}

// bodyError reads an OpenSearch error object out of a response, nil if there is none.
func bodyError(stdout string) error {
	var b struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal([]byte(stdout), &b) == nil && len(b.Error) > 0 && string(b.Error) != "null" {
		return errors.New(openSearchErrorText(b.Error))
	}
	return nil
}

// acknowledged checks the response of a write that answers {"acknowledged":true}.
func acknowledged(what, stdout string) error {
	if err := bodyError(stdout); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	var b struct {
		Acknowledged bool `json:"acknowledged"`
	}
	if err := json.Unmarshal([]byte(stdout), &b); err != nil || !b.Acknowledged {
		return fmt.Errorf("%s: OpenSearch did not acknowledge it: %s", what, strings.TrimSpace(stdout))
	}
	return nil
}

// validIndexName is true only for a name that cannot match more than one index.
func validIndexName(name string) bool {
	if name == "" || name == "all" || name == "_all" || name == "." || name == ".." || strings.ContainsAny(name, " \t\n*?,/\\\"<>|#:") {
		return false
	}
	return !strings.HasPrefix(name, "-") && !strings.HasPrefix(name, "_") && !strings.HasPrefix(name, "+")
}

func (o *ssmIndexOps) Count(ctx context.Context, index string) (int64, error) {
	if !validIndexName(index) {
		return 0, fmt.Errorf("refusing to count %q: not an exact index name", index)
	}
	cmd := curlOS("POST", "/"+index+"/_refresh", "") + " -o /dev/null && " + curlOS("GET", "/"+index+"/_count", "")
	stdout, err := o.run(ctx, "counting "+index, cmd)
	if err != nil {
		return 0, err
	}
	if err := bodyError(stdout); err != nil {
		return 0, fmt.Errorf("counting %s: %w", index, err)
	}
	var b struct {
		Count  *int64 `json:"count"`
		Shards struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
	}
	if err := json.Unmarshal([]byte(stdout), &b); err != nil || b.Count == nil {
		return 0, fmt.Errorf("counting %s: unreadable answer: %s", index, strings.TrimSpace(stdout))
	}
	if b.Shards.Failed > 0 {
		return 0, fmt.Errorf("counting %s: %d shard(s) failed, so the count cannot be trusted", index, b.Shards.Failed)
	}
	return *b.Count, nil
}

func (o *ssmIndexOps) CreateIndex(ctx context.Context, name string) error {
	if !validIndexName(name) {
		return fmt.Errorf("refusing to create %q: not an exact index name", name)
	}
	stdout, err := o.run(ctx, "creating "+name, curlOS("PUT", "/"+name, `{"settings":{"number_of_replicas":0}}`))
	if err != nil {
		return err
	}
	return acknowledged("creating "+name, stdout)
}

type reindexRequest struct {
	Conflicts string `json:"conflicts"`
	Source    struct {
		Index string `json:"index"`
		Size  int    `json:"size"`
	} `json:"source"`
	Dest struct {
		Index  string `json:"index"`
		OpType string `json:"op_type"`
	} `json:"dest"`
}

func (o *ssmIndexOps) Reindex(ctx context.Context, src, dst string) error {
	if !validIndexName(src) || !validIndexName(dst) {
		return fmt.Errorf("refusing to reindex %q into %q: not exact index names", src, dst)
	}
	var req reindexRequest
	req.Conflicts = "proceed"
	req.Source.Index, req.Source.Size = src, 2000
	req.Dest.Index, req.Dest.OpType = dst, "create"
	body, _ := json.Marshal(req)
	what := "reindexing " + src + " into " + dst
	stdout, err := o.run(ctx, what, curlOS("POST", "/_reindex?wait_for_completion=false&refresh=true", string(body)))
	if err != nil {
		return err
	}
	if err := bodyError(stdout); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	var started struct {
		Task string `json:"task"`
	}
	if err := json.Unmarshal([]byte(stdout), &started); err != nil || started.Task == "" {
		return fmt.Errorf("%s: OpenSearch returned no task id: %s", what, strings.TrimSpace(stdout))
	}
	deadline := time.Now().Add(o.timeout)
	for {
		out, err := o.run(ctx, "polling the reindex task "+started.Task, curlOS("GET", "/_tasks/"+started.Task, ""))
		if err != nil {
			return err
		}
		var t struct {
			Completed bool            `json:"completed"`
			Error     json.RawMessage `json:"error"`
			Response  *struct {
				Failures []json.RawMessage `json:"failures"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(out), &t); err != nil {
			return fmt.Errorf("%s: unreadable task status: %s", what, strings.TrimSpace(out))
		}
		if t.Completed {
			switch {
			case len(t.Error) > 0 && string(t.Error) != "null":
				return fmt.Errorf("%s: %s", what, openSearchErrorText(t.Error))
			case t.Response == nil:
				return fmt.Errorf("%s: the task finished with no response", what)
			case len(t.Response.Failures) > 0:
				return fmt.Errorf("%s: %d document(s) failed: %s", what, len(t.Response.Failures), string(t.Response.Failures[0]))
			}
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%s did not finish within %s (task %s is still running on the instance)", what, o.timeout, started.Task)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(o.poll):
		}
	}
}

func (o *ssmIndexOps) RemoveAlias(ctx context.Context, index, alias string) error {
	if !validIndexName(index) || !validIndexName(alias) {
		return fmt.Errorf("refusing to remove alias %q from %q: not exact names", alias, index)
	}
	type removal struct {
		Index string `json:"index"`
		Alias string `json:"alias"`
	}
	body, _ := json.Marshal(map[string]any{"actions": []any{map[string]any{"remove": removal{Index: index, Alias: alias}}}})
	stdout, err := o.run(ctx, "removing alias "+alias+" from "+index, curlOS("POST", "/_aliases", string(body)))
	if err != nil {
		return err
	}
	return acknowledged("removing alias "+alias+" from "+index, stdout)
}

func (o *ssmIndexOps) DeleteIndex(ctx context.Context, name string) error {
	if !validIndexName(name) {
		return fmt.Errorf("refusing to delete %q: not an exact index name", name)
	}
	stdout, err := o.run(ctx, "deleting "+name, curlOS("DELETE", "/"+name, ""))
	if err != nil {
		return err
	}
	return acknowledged("deleting "+name, stdout)
}

func (o *ssmIndexOps) CreateDataStream(ctx context.Context, name string) error {
	if !validIndexName(name) {
		return fmt.Errorf("refusing to create the data stream %q: not an exact name", name)
	}
	stdout, err := o.run(ctx, "creating the data stream "+name, curlOS("PUT", "/_data_stream/"+name, ""))
	if err == nil {
		err = acknowledged("creating the data stream "+name, stdout)
	}
	if err == nil {
		return nil
	}
	// The app may write between the delete and this create; the template then builds the stream.
	if out, gerr := o.run(ctx, "checking for the data stream "+name, curlOS("GET", "/_data_stream/"+name, "")); gerr == nil && bodyError(out) == nil {
		var b dataStreamsBody
		if json.Unmarshal([]byte(out), &b) == nil {
			for _, s := range b.DataStreams {
				if s.Name == name {
					return nil
				}
			}
		}
	}
	return err
}

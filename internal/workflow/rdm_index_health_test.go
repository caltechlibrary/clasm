package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// Canned OpenSearch bodies, taken from the production survey of 2026-10-19
// (notes/index-health-survey/result-i-0effbcc6cfb91097e.txt).

const (
	healthTemplatesBoth = `{"index_templates":[{"name":"caltechauthors-auditlogs-datastream-auditlog-v1.0.0"},{"name":"caltechauthors-job-logs-v1.0.0"}]}`

	healthStreamsBoth = `{"data_streams":[` +
		`{"name":"caltechauthors-auditlog-audit-log-v1.0.0","indices":[{"index_name":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001"}],"template":"caltechauthors-auditlogs-datastream-auditlog-v1.0.0"},` +
		`{"name":"caltechauthors-job-logs","indices":[{"index_name":".ds-caltechauthors-job-logs-000001"}],"template":"caltechauthors-job-logs-v1.0.0"}]}`

	healthIndicesBoth = `[{"index":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001","docs.count":"306325"},` +
		`{"index":".ds-caltechauthors-job-logs-000001","docs.count":"582"},` +
		`{"index":"caltechauthors-records-record-v7.0.0-1","docs.count":"1000"}]`

	healthAliasesBoth = `[{"alias":"caltechauthors-audit-log","index":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001"},` +
		`{"alias":"caltechauthors-auditlog","index":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001"}]`

	healthAuditKeyword = `{".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001":{"mappings":{` +
		`"action":{"full_name":"action","mapping":{"action":{"type":"keyword"}}},` +
		`"resource.type":{"full_name":"resource.type","mapping":{"type":{"type":"keyword"}}},` +
		`"user.id":{"full_name":"user.id","mapping":{"id":{"type":"keyword"}}}}}}`

	healthJobsKeyword = `{".ds-caltechauthors-job-logs-000001":{"mappings":{"level":{"full_name":"level","mapping":{"level":{"type":"keyword"}}}}}}`

	healthAggOK = `{"took":3,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"hits":[]}}`

	healthAggFailed = `{"took":3,"_shards":{"total":2,"successful":0,"skipped":0,"failed":2},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`

	healthNotFound = `{"error":{"type":"index_not_found_exception","reason":"no such index [caltechauthors-auditlog]"},"status":404}`
)

// healthySections is a production-like healthy instance: both templates, both
// logs real streams, keyword mappings, no shard failures.
func healthySections() map[string]string {
	return map[string]string{
		"endpoint":      "status=200\n",
		"prefix":        "cfg=/Sites/caltechauthors/invenio.cfg\nprefix=caltechauthors-\n",
		"templates":     healthTemplatesBoth,
		"app_templates": "status=ok\nTEMPLATE auditlogs-datastream-auditlog-v1.0.0\nTEMPLATE job-logs-v1.0.0\n",
		"data_streams":  healthStreamsBoth,
		"indices":       healthIndicesBoth,
		"aliases":       healthAliasesBoth,
		"mapping_audit": healthAuditKeyword,
		"mapping_jobs":  healthJobsKeyword,
		"agg_audit":     healthAggOK,
		"agg_jobs":      healthAggOK,
	}
}

func with(base map[string]string, overrides map[string]string) map[string]string {
	out := make(map[string]string, len(base))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

func mustReport(t *testing.T, s map[string]string) IndexHealthReport {
	t.Helper()
	r, err := buildIndexHealthReport(s)
	if err != nil {
		t.Fatalf("buildIndexHealthReport: %v", err)
	}
	return r
}

func hasProblem(r IndexHealthReport, sub string) bool {
	return slices.ContainsFunc(r.Problems, func(p string) bool { return strings.Contains(p, sub) })
}

func TestIndexHealth_HealthyInstance(t *testing.T) {
	r := mustReport(t, healthySections())
	if !r.Healthy || len(r.Problems) != 0 {
		t.Fatalf("a healthy instance must have no problems, got healthy=%v problems=%v", r.Healthy, r.Problems)
	}
	if r.Prefix != "caltechauthors-" {
		t.Errorf("Prefix = %q, want the invenio.cfg value", r.Prefix)
	}
	if r.AuditLog.Name != "caltechauthors-auditlog" || r.AuditLog.Kind != IndexKindDataStream {
		t.Errorf("AuditLog = %+v, want the alias caltechauthors-auditlog as a data stream", r.AuditLog)
	}
	if r.JobLogs.Name != "caltechauthors-job-logs" || r.JobLogs.Kind != IndexKindDataStream {
		t.Errorf("JobLogs = %+v, want caltechauthors-job-logs as a data stream", r.JobLogs)
	}
	if !r.AuditLog.Aggregation.Ran || r.AuditLog.Aggregation.Failed != 0 || r.AuditLog.Aggregation.Total != 1 {
		t.Errorf("AuditLog.Aggregation = %+v, want ran with 0 of 1 failed", r.AuditLog.Aggregation)
	}
	if !r.ExpectedKnown || len(r.MissingTemplates) != 0 {
		t.Errorf("ExpectedKnown=%v Missing=%v, want known and none missing", r.ExpectedKnown, r.MissingTemplates)
	}
	if len(r.Strays) != 0 {
		t.Errorf("Strays = %v, want none", r.Strays)
	}
}

func TestIndexHealth_LeftoverCopiesDoNotFailAHealthyInstance(t *testing.T) {
	// The survey's own false positive: the kept rehearsal copies are plain,
	// text-mapped indices that a *auditlog* wildcard swept in and reported as
	// _shards.failed: 2 on a healthy instance (DR-0197).
	indices := `[{"index":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001","docs.count":"306325"},` +
		`{"index":".ds-caltechauthors-job-logs-000001","docs.count":"582"},` +
		`{"index":"auditlog-fix-old","docs.count":"300000"},{"index":"auditlog-fix-holding","docs.count":"6325"},` +
		`{"index":"joblogs-fix-holding","docs.count":"582"}]`
	r := mustReport(t, with(healthySections(), map[string]string{"indices": indices}))
	if !r.Healthy || len(r.Problems) != 0 {
		t.Fatalf("leftover copies must not fail a healthy instance; problems = %v", r.Problems)
	}
	for _, want := range []string{"auditlog-fix-old", "auditlog-fix-holding", "joblogs-fix-holding"} {
		if !slices.Contains(r.Strays, want) {
			t.Errorf("Strays = %v, want %q reported as informational", r.Strays, want)
		}
	}
}

func TestIndexHealth_AuditLogKinds(t *testing.T) {
	plainIndices := `[{"index":"caltechauthors-auditlog-audit-log-v1.0.0","docs.count":"306325"},{"index":".ds-caltechauthors-job-logs-000001","docs.count":"582"}]`
	plainAliases := `[{"alias":"caltechauthors-auditlog","index":"caltechauthors-auditlog-audit-log-v1.0.0"}]`
	orphanStreams := `{"data_streams":[{"name":"caltechauthors-job-logs","indices":[{"index_name":".ds-caltechauthors-job-logs-000001"}],"template":"caltechauthors-job-logs-v1.0.0"}]}`
	cases := []struct {
		name      string
		overrides map[string]string
		want      IndexHealthKind
		problem   string
	}{
		{"stream", nil, IndexKindDataStream, ""},
		{"plain index behind the alias", map[string]string{"indices": plainIndices, "aliases": plainAliases}, IndexKindPlain, "audit log"},
		{"orphaned backing index with no stream", map[string]string{"data_streams": orphanStreams}, IndexKindOrphaned, "orphan"},
		{"plain index with the alias's own name", map[string]string{
			"indices": `[{"index":"caltechauthors-auditlog","docs.count":"5"},{"index":".ds-caltechauthors-job-logs-000001","docs.count":"582"}]`,
			"aliases": `[]`, "data_streams": orphanStreams}, IndexKindPlain, "audit log"},
		{"absent", map[string]string{"indices": `[{"index":".ds-caltechauthors-job-logs-000001","docs.count":"582"}]`, "aliases": `[]`,
			"data_streams": orphanStreams, "mapping_audit": healthNotFound, "agg_audit": healthNotFound}, IndexKindAbsent, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := mustReport(t, with(healthySections(), tc.overrides))
			if r.AuditLog.Kind != tc.want {
				t.Fatalf("AuditLog.Kind = %q, want %q (problems %v)", r.AuditLog.Kind, tc.want, r.Problems)
			}
			if tc.problem == "" && !r.Healthy {
				t.Errorf("want healthy, got problems %v", r.Problems)
			}
			if tc.problem != "" && (r.Healthy || !hasProblem(r, tc.problem)) {
				t.Errorf("want an unhealthy report mentioning %q, got healthy=%v problems=%v", tc.problem, r.Healthy, r.Problems)
			}
		})
	}
}

func TestIndexHealth_AbsentAuditLogSkipsTheAggregation(t *testing.T) {
	r := mustReport(t, with(healthySections(), map[string]string{
		"indices": `[{"index":".ds-caltechauthors-job-logs-000001","docs.count":"582"}]`, "aliases": `[]`,
		"data_streams":  `{"data_streams":[{"name":"caltechauthors-job-logs","indices":[{"index_name":".ds-caltechauthors-job-logs-000001"}],"template":"caltechauthors-job-logs-v1.0.0"}]}`,
		"mapping_audit": healthNotFound, "agg_audit": healthNotFound,
	}))
	if r.AuditLog.Aggregation.Ran {
		t.Errorf("an absent audit log has nothing to aggregate; Aggregation = %+v", r.AuditLog.Aggregation)
	}
	if hasProblem(r, "aggregation") {
		t.Errorf("an absent log is not a failed aggregation: %v", r.Problems)
	}
}

func TestIndexHealth_JobLogsKinds(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]string
		want      IndexHealthKind
	}{
		{"stream", nil, IndexKindDataStream},
		{"plain index", map[string]string{
			"data_streams": `{"data_streams":[{"name":"caltechauthors-auditlog-audit-log-v1.0.0","indices":[{"index_name":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001"}],"template":"t"}]}`,
			"indices":      `[{"index":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001","docs.count":"3"},{"index":"caltechauthors-job-logs","docs.count":"582"}]`,
		}, IndexKindPlain},
		{"orphaned backing index", map[string]string{
			"data_streams": `{"data_streams":[{"name":"caltechauthors-auditlog-audit-log-v1.0.0","indices":[{"index_name":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001"}],"template":"t"}]}`,
		}, IndexKindOrphaned},
		{"absent", map[string]string{
			"data_streams": `{"data_streams":[{"name":"caltechauthors-auditlog-audit-log-v1.0.0","indices":[{"index_name":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001"}],"template":"t"}]}`,
			"indices":      `[{"index":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001","docs.count":"3"}]`,
		}, IndexKindAbsent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := mustReport(t, with(healthySections(), tc.overrides))
			if r.JobLogs.Kind != tc.want {
				t.Fatalf("JobLogs.Kind = %q, want %q", r.JobLogs.Kind, tc.want)
			}
			wantHealthy := tc.want == IndexKindDataStream || tc.want == IndexKindAbsent
			if r.Healthy != wantHealthy {
				t.Errorf("Healthy = %v, want %v (problems %v)", r.Healthy, wantHealthy, r.Problems)
			}
		})
	}
}

func TestIndexHealth_MappingTextIsTheDefect(t *testing.T) {
	text := strings.Replace(healthAuditKeyword, `"resource.type":{"full_name":"resource.type","mapping":{"type":{"type":"keyword"}}}`,
		`"resource.type":{"full_name":"resource.type","mapping":{"type":{"type":"text"}}}`, 1)
	r := mustReport(t, with(healthySections(), map[string]string{"mapping_audit": text}))
	if r.Healthy || !hasProblem(r, "resource.type") || !hasProblem(r, "text") {
		t.Fatalf("a text mapping of resource.type must fail naming the field; healthy=%v problems=%v", r.Healthy, r.Problems)
	}
	var found bool
	for _, m := range r.AuditLog.Mappings {
		if m.Field == "resource.type" && m.Type == "text" {
			found = true
		}
	}
	if !found {
		t.Errorf("Mappings = %+v, want resource.type reported as text", r.AuditLog.Mappings)
	}

	textLevel := strings.Replace(healthJobsKeyword, `"type":"keyword"`, `"type":"text"`, 1)
	r = mustReport(t, with(healthySections(), map[string]string{"mapping_jobs": textLevel}))
	if r.Healthy || !hasProblem(r, "level") {
		t.Fatalf("a text mapping of level must fail; healthy=%v problems=%v", r.Healthy, r.Problems)
	}

	r = mustReport(t, healthySections())
	if len(r.AuditLog.Mappings) != 3 || len(r.JobLogs.Mappings) != 1 {
		t.Errorf("want 3 audit-log and 1 job-log mappings reported, got %d and %d", len(r.AuditLog.Mappings), len(r.JobLogs.Mappings))
	}
}

func TestIndexHealth_ShardFailuresFail(t *testing.T) {
	r := mustReport(t, with(healthySections(), map[string]string{"agg_audit": healthAggFailed}))
	if r.Healthy || !hasProblem(r, "2 of 2 shards") {
		t.Fatalf("_shards.failed 2 of 2 must fail the audit log; healthy=%v problems=%v", r.Healthy, r.Problems)
	}
	if r.AuditLog.Aggregation.Failed != 2 || r.AuditLog.Aggregation.Total != 2 {
		t.Errorf("Aggregation = %+v, want 2 of 2 failed", r.AuditLog.Aggregation)
	}

	r = mustReport(t, with(healthySections(), map[string]string{"agg_jobs": healthAggFailed}))
	if r.Healthy || !hasProblem(r, "job logs") {
		t.Fatalf("a job-logs shard failure must fail; healthy=%v problems=%v", r.Healthy, r.Problems)
	}

	r = mustReport(t, with(healthySections(), map[string]string{"agg_audit": healthNotFound}))
	if r.Healthy || !hasProblem(r, "no such index") {
		t.Fatalf("an error body from a log that is present must fail and say why; healthy=%v problems=%v", r.Healthy, r.Problems)
	}
}

func TestIndexHealth_MissingTemplates(t *testing.T) {
	onlyAudit := `{"index_templates":[{"name":"caltechauthors-auditlogs-datastream-auditlog-v1.0.0"}]}`
	r := mustReport(t, with(healthySections(), map[string]string{"templates": onlyAudit}))
	if r.Healthy || !slices.Equal(r.MissingTemplates, []string{"job-logs-v1.0.0"}) || !hasProblem(r, "job-logs-v1.0.0") {
		t.Fatalf("want job-logs-v1.0.0 reported missing; healthy=%v missing=%v problems=%v", r.Healthy, r.MissingTemplates, r.Problems)
	}

	// Every template gone, as after a restore with include_global_state:false.
	r = mustReport(t, with(healthySections(), map[string]string{"templates": `{}`}))
	if r.Healthy || len(r.MissingTemplates) != 2 {
		t.Fatalf("want both templates missing; healthy=%v missing=%v", r.Healthy, r.MissingTemplates)
	}
}

func TestIndexHealth_UnknownExpectedTemplatesAreNotGuessed(t *testing.T) {
	r := mustReport(t, with(healthySections(), map[string]string{"app_templates": "status=failed\n", "templates": `{}`}))
	if r.ExpectedKnown {
		t.Errorf("ExpectedKnown = true although the app's list could not be read")
	}
	if hasProblem(r, "template") {
		t.Errorf("with no expected list a template cannot be called missing: %v", r.Problems)
	}
}

func TestTemplatePresent(t *testing.T) {
	live := []string{"caltechauthors-job-logs-v1.0.0", "other-template"}
	if !templatePresent(live, "caltechauthors-", "job-logs-v1.0.0") {
		t.Errorf("an unprefixed app name must match the prefixed live template")
	}
	if !templatePresent(live, "caltechauthors-", "caltechauthors-job-logs-v1.0.0") {
		t.Errorf("an already-prefixed app name must match itself")
	}
	if !templatePresent(live, "caltechauthors-", "other-template") {
		t.Errorf("a name present exactly must match")
	}
	if templatePresent(live, "caltechauthors-", "auditlogs-datastream-auditlog-v1.0.0") {
		t.Errorf("an absent template must not match")
	}
	if templatePresent(live, "caltechauthors-", "logs-v1.0.0") {
		t.Errorf("a name that is only a suffix of a live template must not match")
	}
}

func TestIndexHealth_UnreachableIsExit69(t *testing.T) {
	for _, status := range []string{"000", "401", "503"} {
		_, err := buildIndexHealthReport(with(healthySections(), map[string]string{"endpoint": "status=" + status + "\n"}))
		var coded *ExitCodeError
		if !errors.As(err, &coded) || coded.Code != ExitUnavailable {
			t.Errorf("endpoint status %s: err = %v, want an ExitCodeError 69", status, err)
		}
	}
}

func TestIndexHealth_NoAppIsExit66(t *testing.T) {
	_, err := buildIndexHealthReport(with(healthySections(), map[string]string{"prefix": "cfg=\nprefix=\n"}))
	var coded *ExitCodeError
	if !errors.As(err, &coded) || coded.Code != 66 {
		t.Errorf("no invenio.cfg: err = %v, want an ExitCodeError 66", err)
	}
}

func TestIndexHealth_UnreadableOutputIsExit65(t *testing.T) {
	for name, s := range map[string]map[string]string{
		"malformed JSON":  with(healthySections(), map[string]string{"data_streams": `{"data_streams":[`}),
		"missing section": func() map[string]string { m := with(healthySections(), nil); delete(m, "indices"); return m }(),
	} {
		_, err := buildIndexHealthReport(s)
		var coded *ExitCodeError
		if !errors.As(err, &coded) || coded.Code != 65 {
			t.Errorf("%s: err = %v, want an ExitCodeError 65", name, err)
		}
	}
}

func TestSplitIndexHealthSections(t *testing.T) {
	got := splitIndexHealthSections("noise\n@@ endpoint\nstatus=200\n@@ templates\n{\"a\":1}\n@@ empty\n")
	if got["endpoint"] != "status=200\n" || got["templates"] != "{\"a\":1}\n" {
		t.Errorf("sections = %#v", got)
	}
	if v, ok := got["empty"]; !ok || v != "" {
		t.Errorf("an empty section must exist and be empty, got %#v", got)
	}
}

// ---- the command ----

var wildcardRE = regexp.MustCompile(`\*`)

func TestIndexHealthCommand_IsReadOnlyAndNamesNoWildcard(t *testing.T) {
	cmd := buildIndexHealthCommand(0)
	if wildcardRE.MatchString(cmd) {
		t.Errorf("the script must never contain a wildcard (DR-0197): %q", wildcardRE.FindString(cmd))
	}
	for _, banned := range []string{"-X PUT", "-X DELETE", "-X PATCH", "_delete_by_query", "_reindex", "PASSWORD", "--user", "Authorization"} {
		if strings.Contains(cmd, banned) {
			t.Errorf("the script must be read-only and carry no credentials; found %q", banned)
		}
	}
	// sudo -u names the service user; only a curl -u would be a credential.
	if regexp.MustCompile(`curl[^\n]* -u `).MatchString(cmd) {
		t.Errorf("curl must not be given credentials: OpenSearch answers unauthenticated on localhost")
	}
	for _, m := range regexp.MustCompile(`-X (\w+)`).FindAllStringSubmatch(cmd, -1) {
		if m[1] != "POST" {
			t.Errorf("only the aggregation may use a method other than GET, found -X %s", m[1])
		}
	}
	if n := strings.Count(cmd, "-X POST"); n != 1 {
		t.Errorf("want exactly one POST helper (the _search aggregation), got %d", n)
	}
	for _, want := range []string{"${P}auditlog", "${P}job-logs", "_search?size=0", "resource.type", "SEARCH_INDEX_PREFIX", "invenio.cfg", "localhost:9200"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("the script must contain %q", want)
		}
	}
	for _, bash := range []string{"[[", "<<<", "declare ", "local -", "mapfile", "${!"} {
		if strings.Contains(cmd, bash) {
			t.Errorf("SSM runs the script under /bin/sh; found a bashism %q", bash)
		}
	}
}

func TestIndexHealthCommand_WaitIsBounded(t *testing.T) {
	if strings.Contains(buildIndexHealthCommand(0), "sleep") {
		t.Errorf("a wait of zero must not poll")
	}
	cmd := buildIndexHealthCommand(5 * time.Minute)
	if !strings.Contains(cmd, "sleep") || !strings.Contains(cmd, "W=300") {
		t.Errorf("a five-minute wait must poll with a 300-second bound; got:\n%s", cmd)
	}
}

func TestIndexHealthCommand_AppListingIsReadOnly(t *testing.T) {
	cmd := buildIndexHealthCommand(0)
	if !strings.Contains(cmd, "current_search.index_templates") {
		t.Errorf("the expected templates must come from the app's own list")
	}
	for _, banned := range []string{"_put_template", "put_index_template", "put_templates"} {
		if strings.Contains(cmd, banned) {
			t.Errorf("listing the app's templates must not install any; found %q", banned)
		}
	}
}

// ---- running it ----

func indexHealthStdout(sections map[string]string) string {
	order := []string{"endpoint", "prefix", "templates", "app_templates", "data_streams", "indices", "aliases", "mapping_audit", "mapping_jobs", "agg_audit", "agg_jobs"}
	var b strings.Builder
	for _, k := range order {
		if v, ok := sections[k]; ok {
			b.WriteString("@@ " + k + "\n" + v)
			if !strings.HasSuffix(v, "\n") {
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

func healthInstance() inventory.Instance {
	return inventory.Instance{InstanceID: "i-057d8cc298da8f186", Name: "caltechauthors-rsdoiel-v13b", Region: "us-west-2"}
}

func TestCheckRDMIndexHealth_RunsOneScriptAndReportsTheInstance(t *testing.T) {
	ssm := &fakeSSMClient{commandID: "c-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: indexHealthStdout(healthySections())}
	r, err := CheckRDMIndexHealth(context.Background(), ssm, healthInstance(), 0, time.Millisecond)
	if err != nil {
		t.Fatalf("CheckRDMIndexHealth: %v", err)
	}
	if !r.Healthy || r.InstanceID != "i-057d8cc298da8f186" || r.Instance != "caltechauthors-rsdoiel-v13b" {
		t.Errorf("report = %+v", r)
	}
	if got := ssm.sendCommandCallCount; got != 1 {
		t.Errorf("want one read-only SSM call, got %d", got)
	}
	if !strings.Contains(ssm.lastCommandText, "SEARCH_INDEX_PREFIX") {
		t.Errorf("the command sent is not the health script: %q", ssm.lastCommandText)
	}
}

func TestCheckRDMIndexHealth_AWaitLengthensTheSSMTimeout(t *testing.T) {
	ssm := &fakeSSMClient{commandID: "c-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: indexHealthStdout(healthySections())}
	if _, err := CheckRDMIndexHealth(context.Background(), ssm, healthInstance(), 5*time.Minute, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if ssm.lastTimeoutSeconds == nil || *ssm.lastTimeoutSeconds <= 300 {
		t.Errorf("SSM must be given more than the 300-second wait, got %v", ssm.lastTimeoutSeconds)
	}
}

func TestCheckRDMIndexHealth_UnreachableInstanceIsExit69(t *testing.T) {
	ssm := &fakeSSMClient{commandID: "c-1", sendCommandErr: errors.New("InvalidInstanceId: instance not in a valid state")}
	_, err := CheckRDMIndexHealth(context.Background(), ssm, healthInstance(), 0, time.Millisecond)
	var coded *ExitCodeError
	if !errors.As(err, &coded) || coded.Code != ExitUnavailable {
		t.Fatalf("err = %v, want an ExitCodeError 69", err)
	}
}

func TestCheckRDMIndexHealth_ScriptFailureIsExit70(t *testing.T) {
	ssm := &fakeSSMClient{commandID: "c-1", finalStatus: types.CommandInvocationStatusFailed, stdout: "sh: 3: syntax error"}
	_, err := CheckRDMIndexHealth(context.Background(), ssm, healthInstance(), 0, time.Millisecond)
	var coded *ExitCodeError
	if !errors.As(err, &coded) || coded.Code != 70 {
		t.Fatalf("err = %v, want an ExitCodeError 70 (internal): the script never fails on a known condition", err)
	}
}

// ---- output ----

func TestWriteIndexHealth_JSONCarriesTheVerdict(t *testing.T) {
	r := mustReport(t, with(healthySections(), map[string]string{"agg_audit": healthAggFailed}))
	var buf bytes.Buffer
	if err := WriteIndexHealth(&buf, r, ui.FormatJSON); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not one JSON document: %v\n%s", err, buf.String())
	}
	if got["healthy"] != false {
		t.Errorf("healthy = %v, want false", got["healthy"])
	}
	if probs, _ := got["problems"].([]any); len(probs) == 0 {
		t.Errorf("problems missing from the JSON: %s", buf.String())
	}
	if got["instance_id"] != "" && got["prefix"] != "caltechauthors-" {
		t.Errorf("prefix = %v", got["prefix"])
	}
}

func TestWriteIndexHealth_TextSaysHealthyOrNot(t *testing.T) {
	var ok, bad bytes.Buffer
	if err := WriteIndexHealth(&ok, mustReport(t, healthySections()), ui.FormatText); err != nil {
		t.Fatal(err)
	}
	if err := WriteIndexHealth(&bad, mustReport(t, with(healthySections(), map[string]string{"agg_audit": healthAggFailed})), ui.FormatText); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ok.String(), "HEALTHY") || strings.Contains(ok.String(), "UNHEALTHY") {
		t.Errorf("a healthy report must say HEALTHY:\n%s", ok.String())
	}
	if !strings.Contains(bad.String(), "UNHEALTHY") || !strings.Contains(bad.String(), "2 of 2 shards") {
		t.Errorf("an unhealthy report must say UNHEALTHY and why:\n%s", bad.String())
	}
}

func TestWriteIndexHealth_StraysAreInformational(t *testing.T) {
	r := mustReport(t, with(healthySections(), map[string]string{"indices": `[{"index":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001","docs.count":"1"},{"index":".ds-caltechauthors-job-logs-000001","docs.count":"1"},{"index":"auditlog-fix-old","docs.count":"9"}]`}))
	var buf bytes.Buffer
	if err := WriteIndexHealth(&buf, r, ui.FormatText); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "auditlog-fix-old") || !strings.Contains(strings.ToLower(out), "informational") || strings.Contains(out, "UNHEALTHY") {
		t.Errorf("a stray is listed as informational and does not fail:\n%s", out)
	}
}

// ---- the exit-code classes ----

func TestExitClassName(t *testing.T) {
	for code, want := range map[int]string{0: "ok", 1: "negative", 2: "usage", 65: "data", 66: "no_input", 69: "unavailable", 70: "internal", 73: "cant_create", 74: "io", 75: "temp_fail", 77: "no_permission", 78: "config"} {
		if got := exitClassName(code); got != want {
			t.Errorf("exitClassName(%d) = %q, want %q", code, got, want)
		}
	}
}

func TestParseRDMIndexHealthArgs(t *testing.T) {
	inst, format, wait, err := ParseRDMIndexHealthArgs([]string{"box"})
	if err != nil || inst != "box" || format != ui.FormatText || wait != DefaultIndexHealthWait {
		t.Errorf("defaults: %q %v %v %v", inst, format, wait, err)
	}
	if _, f, _, err := ParseRDMIndexHealthArgs([]string{"-json", "box"}); err != nil || f != ui.FormatJSON {
		t.Errorf("-json: %v %v", f, err)
	}
	if _, f, _, err := ParseRDMIndexHealthArgs([]string{"-text", "box"}); err != nil || f != ui.FormatText {
		t.Errorf("-text: %v %v", f, err)
	}
	if _, _, w, err := ParseRDMIndexHealthArgs([]string{"-wait", "90s", "box"}); err != nil || w != 90*time.Second {
		t.Errorf("-wait 90s: %v %v", w, err)
	}
	if _, _, w, err := ParseRDMIndexHealthArgs([]string{"-wait", "0", "box"}); err != nil || w != 0 {
		t.Errorf("-wait 0 must skip the wait: %v %v", w, err)
	}
}

func TestParseRDMIndexHealthArgs_UsageErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"no instance":     {},
		"surplus word":    {"box", "other"},
		"unknown option":  {"-bogus", "box"},
		"two formats":     {"-text", "-json", "box"},
		"bad wait":        {"-wait", "soon", "box"},
		"negative wait":   {"-wait", "-5s", "box"},
		"blank instance":  {"  "},
		"confirm is n/a":  {"--confirm", "box", "box"},
		"jsonl is not on": {"-jsonl", "box"},
	} {
		_, _, _, err := ParseRDMIndexHealthArgs(args)
		var usage *UsageError
		if !errors.As(err, &usage) {
			t.Errorf("%s: err = %v, want a *UsageError (exit 2)", name, err)
		}
	}
	_, _, _, err := ParseRDMIndexHealthArgs([]string{"--help"})
	var help *HelpRequested
	if !errors.As(err, &help) || !strings.Contains(help.Usage, RDMIndexHealthCLISlug) {
		t.Errorf("--help: err = %v, want a HelpRequested naming the form", err)
	}
}

func healthInstances() []inventory.Instance {
	return []inventory.Instance{{InstanceID: "i-1", Name: "box", Region: "us-west-2"}}
}

func healthClients(ssm awsclient.SSMAPI) map[string]awsclient.SSMAPI {
	return map[string]awsclient.SSMAPI{"us-west-2": ssm}
}

func TestRunRDMIndexHealthCLI_ExitCodePerClass(t *testing.T) {
	good := healthySections()
	cases := []struct {
		name string
		ssm  *fakeSSMClient
		arg  string
		want int
	}{
		{"healthy", &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusSuccess, stdout: indexHealthStdout(good)}, "box", 0},
		{"unhealthy", &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusSuccess, stdout: indexHealthStdout(with(good, map[string]string{"agg_audit": healthAggFailed}))}, "box", 1},
		{"unreachable endpoint", &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusSuccess, stdout: indexHealthStdout(with(good, map[string]string{"endpoint": "status=000\n"}))}, "box", 69},
		{"ssm unreachable", &fakeSSMClient{commandID: "c", sendCommandErr: errors.New("InvalidInstanceId")}, "box", 69},
		{"no app", &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusSuccess, stdout: indexHealthStdout(with(good, map[string]string{"prefix": "cfg=\nprefix=\n"}))}, "box", 66},
		{"unreadable", &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusSuccess, stdout: "garbage"}, "box", 65},
		{"script failed", &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusFailed}, "box", 70},
		{"unknown instance", &fakeSSMClient{commandID: "c"}, "nope", 2},
	}
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "box", Region: "us-west-2"}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := RunRDMIndexHealthCLI(context.Background(), &out, healthClients(tc.ssm), instances, tc.arg, ui.FormatText, 0, time.Millisecond)
			if got := CLIExitCode(err); got != tc.want {
				t.Errorf("exit code = %d (err %v), want %d", got, err, tc.want)
			}
		})
	}
}

func TestRunRDMIndexHealthCLI_UsageErrorSendsNothing(t *testing.T) {
	ssm := &fakeSSMClient{commandID: "c"}
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "box", Region: "us-west-2"}}
	err := RunRDMIndexHealthCLI(context.Background(), &bytes.Buffer{}, healthClients(ssm), instances, "nope", ui.FormatText, 0, time.Millisecond)
	var usage *UsageError
	if !errors.As(err, &usage) || ssm.sendCommandCallCount != 0 {
		t.Errorf("an unknown instance is a usage error raised before any SSM call; err=%v calls=%d", err, ssm.sendCommandCallCount)
	}
}

func TestRunRDMIndexHealthCLI_JSONErrorObjectCarriesTheClass(t *testing.T) {
	ssm := &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusSuccess, stdout: indexHealthStdout(with(healthySections(), map[string]string{"endpoint": "status=000\n"}))}
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "box", Region: "us-west-2"}}
	var out bytes.Buffer
	err := RunRDMIndexHealthCLI(context.Background(), &out, healthClients(ssm), instances, "box", ui.FormatJSON, 0, time.Millisecond)
	if CLIExitCode(err) != 69 {
		t.Fatalf("err = %v, want exit 69", err)
	}
	var got struct {
		Error struct {
			Class   string `json:"class"`
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal(out.Bytes(), &got); jerr != nil {
		t.Fatalf("-json must put one JSON object on stdout even on failure: %v\n%s", jerr, out.String())
	}
	if got.Error.Class != "unavailable" || got.Error.Code != 69 || got.Error.Message == "" {
		t.Errorf("error object = %+v, want class unavailable, code 69 and a message", got.Error)
	}
}

func TestRunRDMIndexHealthCLI_UnhealthyJSONIsTheReportNotAnErrorObject(t *testing.T) {
	ssm := &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusSuccess, stdout: indexHealthStdout(with(healthySections(), map[string]string{"agg_audit": healthAggFailed}))}
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "box", Region: "us-west-2"}}
	var out bytes.Buffer
	err := RunRDMIndexHealthCLI(context.Background(), &out, healthClients(ssm), instances, "box", ui.FormatJSON, 0, time.Millisecond)
	if CLIExitCode(err) != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	var got map[string]any
	if jerr := json.Unmarshal(out.Bytes(), &got); jerr != nil || got["healthy"] != false || got["error"] != nil {
		t.Errorf("an unhealthy instance is a finding, so stdout is the report: %v\n%s", jerr, out.String())
	}
}

// ---- the interactive leaf ----

func TestRDMIndexHealthForInstance_PrintsTheReportAndDoesNotRepeatAnUnhealthyVerdictAsAnError(t *testing.T) {
	ssm := &fakeSSMClient{commandID: "c", finalStatus: types.CommandInvocationStatusSuccess,
		stdout: indexHealthStdout(with(healthySections(), map[string]string{"agg_audit": healthAggFailed}))}
	var out bytes.Buffer
	err := rdmIndexHealthForInstance(context.Background(), &out, healthClients(ssm), healthInstance())
	if err != nil {
		t.Fatalf("an unhealthy report is shown, not raised: the menu would print it a second time as an Error line: %v", err)
	}
	if !strings.Contains(out.String(), "UNHEALTHY") || !strings.Contains(out.String(), "2 of 2 shards") {
		t.Errorf("the report must be on screen:\n%s", out.String())
	}
}

func TestRDMIndexHealthForInstance_AnUnreachableInstanceIsAnError(t *testing.T) {
	ssm := &fakeSSMClient{commandID: "c", sendCommandErr: errors.New("InvalidInstanceId")}
	err := rdmIndexHealthForInstance(context.Background(), &bytes.Buffer{}, healthClients(ssm), healthInstance())
	if err == nil {
		t.Fatal("a check that could not be made must be an error the menu shows")
	}
}

func TestIndexHealth_OneFailedShardFails(t *testing.T) {
	// Production's #185 signature was _shards.failed: 1 of 1, so a tolerance of
	// one failed shard would have passed the very defect this check exists for.
	for name, body := range map[string]string{
		"1 of 1": `{"_shards":{"total":1,"successful":0,"skipped":0,"failed":1},"hits":{"hits":[]}}`,
		"1 of 2": `{"_shards":{"total":2,"successful":1,"skipped":0,"failed":1},"hits":{"hits":[]}}`,
	} {
		r := mustReport(t, with(healthySections(), map[string]string{"agg_audit": body}))
		if r.Healthy || !hasProblem(r, "shards fail") || r.AuditLog.Aggregation.Failed != 1 {
			t.Errorf("%s: one failed shard must fail the audit log; healthy=%v problems=%v", name, r.Healthy, r.Problems)
		}
	}
}

func TestIndexHealth_MixedPlainAndOrphanedIsPlainAndNamesBoth(t *testing.T) {
	r := mustReport(t, with(healthySections(), map[string]string{
		"data_streams": `{"data_streams":[{"name":"caltechauthors-job-logs","indices":[{"index_name":".ds-caltechauthors-job-logs-000001"}],"template":"t"}]}`,
		"indices": `[{"index":"caltechauthors-auditlog-audit-log-v1.0.0","docs.count":"5"},` +
			`{"index":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001","docs.count":"9"},{"index":".ds-caltechauthors-job-logs-000001","docs.count":"1"}]`,
		"aliases": `[{"alias":"caltechauthors-auditlog","index":"caltechauthors-auditlog-audit-log-v1.0.0"},` +
			`{"alias":"caltechauthors-auditlog","index":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001"}]`,
	}))
	if r.AuditLog.Kind != IndexKindPlain {
		t.Errorf("Kind = %q, want plain (the documented precedence over orphaned)", r.AuditLog.Kind)
	}
	if !hasProblem(r, "plain index") || !hasProblem(r, "orphaned backing index") {
		t.Errorf("a repair needs both findings; problems = %v", r.Problems)
	}
}

func TestIndexHealth_OrphanOfAnotherStreamIsNotTheJobLogs(t *testing.T) {
	// .ds-caltechauthors-job-logs-archive-000001 belongs to some other stream
	// name; it must not make caltechauthors-job-logs look orphaned.
	r := mustReport(t, with(healthySections(), map[string]string{
		"data_streams": `{"data_streams":[{"name":"caltechauthors-auditlog-audit-log-v1.0.0","indices":[{"index_name":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001"}],"template":"t"}]}`,
		"indices": `[{"index":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001","docs.count":"1"},` +
			`{"index":".ds-caltechauthors-job-logs-archive-000001","docs.count":"3"},{"index":".ds-caltechauthors-job-logs-000001x","docs.count":"3"}]`,
	}))
	if r.JobLogs.Kind != IndexKindAbsent {
		t.Errorf("JobLogs.Kind = %q, want absent: neither index is a backing index of caltechauthors-job-logs", r.JobLogs.Kind)
	}
	if !slices.Contains(r.Strays, ".ds-caltechauthors-job-logs-archive-000001") {
		t.Errorf("the other stream's index is informational, got strays %v", r.Strays)
	}
}

func TestIndexHealth_ARolledOverStreamWithSeveralBackingIndicesIsHealthy(t *testing.T) {
	r := mustReport(t, with(healthySections(), map[string]string{
		"data_streams": `{"data_streams":[` +
			`{"name":"caltechauthors-auditlog-audit-log-v1.0.0","indices":[{"index_name":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001"},{"index_name":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000002"}],"template":"t"},` +
			`{"name":"caltechauthors-job-logs","indices":[{"index_name":".ds-caltechauthors-job-logs-000001"}],"template":"t"}]}`,
		"indices": `[{"index":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001","docs.count":"1"},{"index":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000002","docs.count":"1"},` +
			`{"index":".ds-caltechauthors-job-logs-000001","docs.count":"1"}]`,
		"aliases": `[{"alias":"caltechauthors-auditlog","index":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001"},` +
			`{"alias":"caltechauthors-auditlog","index":".ds-caltechauthors-auditlog-audit-log-v1.0.0-000002"}]`,
	}))
	if !r.Healthy || r.AuditLog.Kind != IndexKindDataStream || len(r.Strays) != 0 {
		t.Errorf("both generations are the one stream; healthy=%v kind=%q strays=%v problems=%v", r.Healthy, r.AuditLog.Kind, r.Strays, r.Problems)
	}
}

func TestIndexHealth_AFailedTemplateListIsNotReadAsAnExpectedList(t *testing.T) {
	r := mustReport(t, with(healthySections(), map[string]string{
		"app_templates": "status=failed\nTEMPLATE never-installed-v1.0.0\n", "templates": `{}`}))
	if r.ExpectedKnown || len(r.MissingTemplates) != 0 {
		t.Errorf("the status line decides whether the list is trusted; known=%v missing=%v", r.ExpectedKnown, r.MissingTemplates)
	}
}

func TestIndexListingsIncludeHiddenIndices(t *testing.T) {
	// A data stream's backing indices are hidden, and an orphaned one keeps the setting. Without
	// expand_wildcards=all the listing omits exactly the index both the health check and the
	// conversion are looking for.
	for name, cmd := range map[string]string{"health": buildIndexHealthCommand(0), "conversion": buildConversionDiscoveryCommand(0)} {
		if !strings.Contains(cmd, "_cat/indices?format=json&h=index,docs.count&expand_wildcards=all") {
			t.Errorf("%s: the index listing must include hidden indices", name)
		}
	}
}

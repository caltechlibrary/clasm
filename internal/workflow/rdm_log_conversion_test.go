package workflow

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The conversion (DR-0198) deletes indices, so its tests are about what can never go wrong: whatever
// step fails, every document still exists somewhere. The algorithm runs against an in-memory fake
// OpenSearch that records every call and can fail any one of them; the SSM transport is tested apart.

const (
	cvPrefix = "caltechauthors-"
	cvStream = "caltechauthors-auditlog-audit-log-v1.0.0"
	cvOrphan = ".ds-caltechauthors-auditlog-audit-log-v1.0.0-000001"
	cvAlias  = "caltechauthors-auditlog"
	cvJobs   = "caltechauthors-job-logs"
)

// fakeOS is a small OpenSearch: indices hold document ids, a data stream's documents live under its name,
// and it enforces the two rules the by-hand rehearsal found (an orphan named .ds-<stream>-000001 blocks
// the stream, and a plain index with the stream's name blocks it too).
type fakeOS struct {
	idx     map[string]map[string]bool
	streams map[string]bool
	aliases map[string]map[string]bool // alias -> indices
	calls   []string
	deleted []string
	// failAt fails the Nth call (1-based) and does nothing else; 0 never fails.
	failAt int
	// onCall runs after a call is recorded and before it takes effect: the app writing meanwhile.
	onCall func(f *fakeOS, call string)
	// dropOn silently loses the documents of any reindex whose call string it matches: a copy that
	// reports success but is short, which only a count can catch.
	dropOn func(call string) bool
}

func newFakeOS() *fakeOS {
	return &fakeOS{idx: map[string]map[string]bool{}, streams: map[string]bool{}, aliases: map[string]map[string]bool{}}
}

func (f *fakeOS) put(index string, n int, tag string) {
	if f.idx[index] == nil {
		f.idx[index] = map[string]bool{}
	}
	for i := 1; i <= n; i++ {
		f.idx[index][fmt.Sprintf("%s%d", tag, i)] = true
	}
}

func (f *fakeOS) alias(alias, index string) {
	if f.aliases[alias] == nil {
		f.aliases[alias] = map[string]bool{}
	}
	f.aliases[alias][index] = true
}

func (f *fakeOS) begin(call string) error {
	f.calls = append(f.calls, call)
	if f.failAt > 0 && len(f.calls) == f.failAt {
		return fmt.Errorf("injected failure at call %d (%s)", len(f.calls), call)
	}
	if f.onCall != nil {
		f.onCall(f, call)
	}
	return nil
}

func (f *fakeOS) Count(ctx context.Context, index string) (int64, error) {
	if err := f.begin("Count " + index); err != nil {
		return 0, err
	}
	docs, ok := f.idx[index]
	if !ok {
		return 0, fmt.Errorf("no such index %s", index)
	}
	return int64(len(docs)), nil
}

func (f *fakeOS) CreateIndex(ctx context.Context, name string) error {
	if err := f.begin("CreateIndex " + name); err != nil {
		return err
	}
	if _, ok := f.idx[name]; ok {
		return fmt.Errorf("index %s already exists", name)
	}
	f.idx[name] = map[string]bool{}
	return nil
}

func (f *fakeOS) Reindex(ctx context.Context, src, dst string) error {
	call := "Reindex " + src + " " + dst
	if err := f.begin(call); err != nil {
		return err
	}
	from, ok := f.idx[src]
	if !ok {
		return fmt.Errorf("no such source %s", src)
	}
	to, ok := f.idx[dst]
	if !ok {
		return fmt.Errorf("no such destination %s", dst)
	}
	if f.dropOn != nil && f.dropOn(call) {
		return nil
	}
	for id := range from {
		to[id] = true // op_type create: an existing id is skipped, which is the same set
	}
	return nil
}

func (f *fakeOS) RemoveAlias(ctx context.Context, index, alias string) error {
	if err := f.begin("RemoveAlias " + index + " " + alias); err != nil {
		return err
	}
	delete(f.aliases[alias], index)
	return nil
}

func (f *fakeOS) DeleteIndex(ctx context.Context, name string) error {
	if err := f.begin("DeleteIndex " + name); err != nil {
		return err
	}
	if f.streams[name] {
		return fmt.Errorf("%s is a data stream; delete it as one", name)
	}
	if _, ok := f.idx[name]; !ok {
		return fmt.Errorf("no such index %s", name)
	}
	delete(f.idx, name)
	f.deleted = append(f.deleted, name)
	return nil
}

func (f *fakeOS) CreateDataStream(ctx context.Context, name string) error {
	if err := f.begin("CreateDataStream " + name); err != nil {
		return err
	}
	if f.streams[name] {
		return fmt.Errorf("data stream %s already exists", name)
	}
	if _, plain := f.idx[name]; plain {
		return fmt.Errorf("a plain index named %s exists", name)
	}
	if _, orphan := f.idx[".ds-"+name+"-000001"]; orphan {
		return fmt.Errorf("the backing index .ds-%s-000001 already exists", name)
	}
	f.streams[name] = true
	f.idx[name] = map[string]bool{}
	return nil
}

func (f *fakeOS) at(call string) int { return slices.Index(f.calls, call) }

func (f *fakeOS) lastIndexOf(call string) int { return slices.Index(reversed(f.calls), call) }

func reversed(s []string) []string {
	out := slices.Clone(s)
	slices.Reverse(out)
	return out
}

// everyDocument lists the ids that exist anywhere.
func (f *fakeOS) everyDocument() map[string]bool {
	all := map[string]bool{}
	for _, docs := range f.idx {
		for id := range docs {
			all[id] = true
		}
	}
	return all
}

// auditConversion is production's pre-fix audit log: an orphaned .ds- index (the old history) and a plain
// index with the stream's name (what the app wrote since the cutover), the alias on both.
func auditConversion() logConversion {
	return logConversion{
		Label: "audit log", Stream: cvStream, Orphan: cvOrphan, Plain: cvStream, OrphanDocs: 5, PlainDocs: 3,
		Aliases:    [][2]string{{cvOrphan, cvAlias}, {cvStream, cvAlias}},
		HoldOrphan: "clasm-fix-auditlog-orphan", HoldPlain: "clasm-fix-auditlog-plain",
	}
}

func auditFake() *fakeOS {
	f := newFakeOS()
	f.put(cvOrphan, 5, "o")
	f.put(cvStream, 3, "p")
	f.alias(cvAlias, cvOrphan)
	f.alias(cvAlias, cvStream)
	return f
}

func jobsConversion() logConversion {
	return logConversion{Label: "job logs", Stream: cvJobs, Plain: cvJobs, PlainDocs: 4, HoldPlain: "clasm-fix-job-logs-plain"}
}

func jobsFake() *fakeOS {
	f := newFakeOS()
	f.put(cvJobs, 4, "j")
	return f
}

func runConversion(t *testing.T, f *fakeOS, logs ...logConversion) ([]logResult, error) {
	t.Helper()
	return ConvertLogIndices(context.Background(), &strings.Builder{}, f, conversionPlan{Prefix: cvPrefix, Logs: logs})
}

func mustConvert(t *testing.T, f *fakeOS, logs ...logConversion) []logResult {
	t.Helper()
	r, err := runConversion(t, f, logs...)
	if err != nil {
		t.Fatalf("ConvertLogIndices: %v\ncalls:\n%s", err, strings.Join(f.calls, "\n"))
	}
	return r
}

func neverDeletedACopy(t *testing.T, f *fakeOS) {
	t.Helper()
	for _, d := range f.deleted {
		if strings.HasPrefix(d, "clasm-fix-") {
			t.Errorf("clasm must never delete a copy, but deleted %s", d)
		}
	}
}

// ---- the conversion, form by form ----

func TestConvert_AuditLogOrphanAndPlain(t *testing.T) {
	f := auditFake()
	res := mustConvert(t, f, auditConversion())
	if !f.streams[cvStream] || len(f.idx[cvStream]) != 8 {
		t.Fatalf("want the stream with 5 + 3 documents, got stream=%v docs=%d", f.streams[cvStream], len(f.idx[cvStream]))
	}
	if _, still := f.idx[cvOrphan]; still {
		t.Errorf("the orphan must be gone")
	}
	if len(f.idx["clasm-fix-auditlog-orphan"]) != 5 || len(f.idx["clasm-fix-auditlog-plain"]) != 3 {
		t.Errorf("both copies are kept with their documents: %v", f.idx)
	}
	if len(res) != 1 || res[0].StreamDocs != 8 || !slices.Equal(res[0].Copies, []string{"clasm-fix-auditlog-orphan", "clasm-fix-auditlog-plain"}) {
		t.Errorf("result = %+v", res)
	}
	neverDeletedACopy(t, f)
}

func TestConvert_PlainOnly(t *testing.T) {
	f := jobsFake()
	mustConvert(t, f, jobsConversion())
	if !f.streams[cvJobs] || len(f.idx[cvJobs]) != 4 || len(f.idx["clasm-fix-job-logs-plain"]) != 4 {
		t.Errorf("job logs: stream=%v docs=%d copy=%d", f.streams[cvJobs], len(f.idx[cvJobs]), len(f.idx["clasm-fix-job-logs-plain"]))
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "RemoveAlias") {
			t.Errorf("the job logs have no alias to remove: %s", c)
		}
	}
	neverDeletedACopy(t, f)
}

func TestConvert_OrphanOnly(t *testing.T) {
	f := newFakeOS()
	f.put(cvOrphan, 5, "o")
	f.alias(cvAlias, cvOrphan)
	lc := auditConversion()
	lc.Plain, lc.PlainDocs, lc.HoldPlain, lc.Aliases = "", 0, "", [][2]string{{cvOrphan, cvAlias}}
	mustConvert(t, f, lc)
	if !f.streams[cvStream] || len(f.idx[cvStream]) != 5 || len(f.idx["clasm-fix-auditlog-orphan"]) != 5 {
		t.Errorf("orphan only: %v", f.idx)
	}
	if f.at("CreateIndex clasm-fix-auditlog-plain") >= 0 {
		t.Errorf("no plain index, so no plain copy")
	}
	neverDeletedACopy(t, f)
}

// ---- the order is the safety (DR-0198) ----

func TestConvert_OrderOfAuditLogOrphanAndPlain(t *testing.T) {
	f := auditFake()
	mustConvert(t, f, auditConversion())
	pos := func(call string) int {
		i := f.at(call)
		if i < 0 {
			t.Fatalf("expected call %q, calls:\n%s", call, strings.Join(f.calls, "\n"))
		}
		return i
	}
	holdO, holdP := "clasm-fix-auditlog-orphan", "clasm-fix-auditlog-plain"
	copyO := pos("Reindex " + cvOrphan + " " + holdO)
	delO := pos("DeleteIndex " + cvOrphan)
	delP := pos("DeleteIndex " + cvStream)
	mk := pos("CreateDataStream " + cvStream)
	rmA1, rmA2 := pos("RemoveAlias "+cvOrphan+" "+cvAlias), pos("RemoveAlias "+cvStream+" "+cvAlias)

	if !(pos("CreateIndex "+holdO) < copyO && pos("CreateIndex "+holdP) < copyO) {
		t.Errorf("the holding indices are created before anything is copied")
	}
	// the orphan is deleted only after a copy whose count matches
	countsBetween := func(from, to int, want ...string) {
		for _, w := range want {
			found := false
			for _, c := range f.calls[from+1 : to] {
				found = found || c == w
			}
			if !found {
				t.Errorf("want %q between calls %d and %d:\n%s", w, from, to, strings.Join(f.calls, "\n"))
			}
		}
	}
	countsBetween(copyO, delO, "Count "+cvOrphan, "Count "+holdO)
	// the aliases come off before the orphan is deleted, both of them
	if !(rmA1 < delO && rmA2 < delO) {
		t.Errorf("the alias is removed from both indices before the orphan is deleted")
	}
	// the plain index is deleted after the delta copy and its count check, never before the orphan work
	delta := -1
	for i, c := range f.calls {
		if c == "Reindex "+cvStream+" "+holdP && i > delO {
			delta = i
		}
	}
	if delta < 0 || delta > delP {
		t.Fatalf("a delta copy of the plain index must follow the orphan delete and precede the plain delete (delta=%d delete=%d)", delta, delP)
	}
	countsBetween(delta, delP, "Count "+cvStream, "Count "+holdP)
	// delete and create happen in one step: nothing runs between them
	if mk != delP+1 {
		t.Errorf("the plain index is deleted and the stream created back to back, got delete at %d and create at %d", delP, mk)
	}
	// the copies load into the stream only once it exists, orphan first
	loadO, loadP := pos("Reindex "+holdO+" "+cvStream), pos("Reindex "+holdP+" "+cvStream)
	if !(mk < loadO && loadO < loadP) {
		t.Errorf("the copies are loaded after the stream exists")
	}
	if f.calls[len(f.calls)-1] != "Count "+cvStream {
		t.Errorf("the last call verifies the stream's count, got %q", f.calls[len(f.calls)-1])
	}
}

func TestConvert_AppWritesDuringTheConversionAreCarried(t *testing.T) {
	f := auditFake()
	writes := 0
	f.onCall = func(f *fakeOS, call string) {
		// the app writes to the plain index after a copy of it, just before it is counted: the first two
		// counts of the plain index see documents the copy does not have yet
		if call == "Count "+cvStream && writes < 2 {
			writes++
			f.put(cvStream, 2, fmt.Sprintf("w%d_", writes))
		}
	}
	mustConvert(t, f, auditConversion())
	if got := len(f.idx[cvStream]); got != 5+3+4 {
		t.Errorf("stream holds %d documents, want 12 (5 old + 3 + the 4 written meanwhile)", got)
	}
	deltas := 0
	for _, c := range f.calls {
		if c == "Reindex "+cvStream+" clasm-fix-auditlog-plain" {
			deltas++
		}
	}
	if deltas != 4 {
		t.Errorf("the plain index is copied once up front and three times in the delta loop (the writes forced a third), got %d", deltas)
	}
}

func TestConvert_ARelentlessWriterStopsItBeforeThePlainIndexIsDeleted(t *testing.T) {
	f := auditFake()
	n := 0
	f.onCall = func(f *fakeOS, call string) {
		if call == "Count "+cvStream {
			n++
			f.put(cvStream, 1, fmt.Sprintf("r%d_", n)) // the plain index is always ahead of its copy
		}
	}
	_, err := runConversion(t, f, auditConversion())
	if err == nil {
		t.Fatalf("a plain index that keeps growing cannot be verified; the conversion must stop")
	}
	if f.at("DeleteIndex "+cvStream) >= 0 || f.at("CreateDataStream "+cvStream) >= 0 {
		t.Errorf("the plain index must not be deleted unless the copy matches")
	}
	if !strings.Contains(err.Error(), "clasm-fix-auditlog-plain") || !strings.Contains(err.Error(), "did not catch up") {
		t.Errorf("the error names the copy and says why: %v", err)
	}
	neverDeletedACopy(t, f)
}

func TestConvert_AShortOrphanCopyStopsBeforeAnythingIsDestroyed(t *testing.T) {
	f := auditFake()
	f.dropOn = func(call string) bool { return call == "Reindex "+cvOrphan+" clasm-fix-auditlog-orphan" }
	_, err := runConversion(t, f, auditConversion())
	if err == nil {
		t.Fatal("a copy with the wrong count must stop the conversion")
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "DeleteIndex") || strings.HasPrefix(c, "RemoveAlias") || strings.HasPrefix(c, "CreateDataStream") {
			t.Errorf("nothing may be destroyed or changed after a short copy; saw %q", c)
		}
	}
	if !f.aliases[cvAlias][cvOrphan] || !f.aliases[cvAlias][cvStream] {
		t.Errorf("the aliases are untouched")
	}
}

func TestConvert_AShortStreamIsAnErrorAndTheCopiesRemain(t *testing.T) {
	f := auditFake()
	f.dropOn = func(call string) bool { return call == "Reindex clasm-fix-auditlog-plain "+cvStream }
	_, err := runConversion(t, f, auditConversion())
	if err == nil || !strings.Contains(err.Error(), "clasm-fix-auditlog-orphan") || !strings.Contains(err.Error(), "clasm-fix-auditlog-plain") {
		t.Fatalf("a stream with fewer documents than the copies is an error naming both copies: %v", err)
	}
	neverDeletedACopy(t, f)
}

func TestConvert_ReindexingTheOrphanWhileTheStreamBlocksFailsLoudly(t *testing.T) {
	// If a step were ever reordered so the stream is created while the orphan exists, the fake (like
	// OpenSearch) refuses. This pins that the fake models the rule the order exists for.
	f := auditFake()
	if err := f.CreateDataStream(context.Background(), cvStream); err == nil {
		t.Errorf("the stream cannot be created while the plain index or the orphan exists")
	}
}

// ---- whatever fails, no document is lost ----

func TestConvert_NoFailurePointLosesADocument(t *testing.T) {
	scenarios := map[string]struct {
		build func() *fakeOS
		logs  []logConversion
	}{
		"audit orphan and plain": {auditFake, []logConversion{auditConversion()}},
		"job logs plain":         {jobsFake, []logConversion{jobsConversion()}},
		"both logs": {func() *fakeOS {
			f := auditFake()
			f.put(cvJobs, 4, "j")
			return f
		}, []logConversion{auditConversion(), jobsConversion()}},
	}
	for name, sc := range scenarios {
		clean := sc.build()
		mustConvert(t, clean, sc.logs...)
		total := len(clean.calls)
		original := sc.build().everyDocument()
		for k := 1; k <= total; k++ {
			f := sc.build()
			f.failAt = k
			_, err := runConversion(t, f, sc.logs...)
			if err == nil {
				t.Errorf("%s: a failure injected at call %d (%s) was swallowed", name, k, clean.calls[k-1])
				continue
			}
			got := f.everyDocument()
			for id := range original {
				if !got[id] {
					t.Errorf("%s: failing at call %d (%s) lost document %s", name, k, clean.calls[k-1], id)
				}
			}
			neverDeletedACopy(t, f)
			// the error names every copy that exists, so the operator can find the documents
			for index := range f.idx {
				if strings.HasPrefix(index, "clasm-fix-") && !strings.Contains(err.Error(), index) {
					t.Errorf("%s: failing at call %d, the error does not name the copy %s: %v", name, k, index, err)
				}
			}
		}
	}
}

func TestConvert_StopsAtTheFirstFailedLog(t *testing.T) {
	f := auditFake()
	f.put(cvJobs, 4, "j")
	f.failAt = 3
	_, err := runConversion(t, f, auditConversion(), jobsConversion())
	if err == nil {
		t.Fatal("want an error")
	}
	for _, c := range f.calls {
		if strings.Contains(c, cvJobs) {
			t.Errorf("the job logs are untouched when the audit log failed; saw %q", c)
		}
	}
}

func TestConvert_ANoOpPlanTouchesNothing(t *testing.T) {
	f := newFakeOS()
	res, err := runConversion(t, f)
	if err != nil || len(res) != 0 || len(f.calls) != 0 {
		t.Errorf("an empty plan converts nothing and makes no call: res=%v err=%v calls=%v", res, err, f.calls)
	}
}

func TestConvert_ACancelledContextStopsBeforeTheNextStep(t *testing.T) {
	f := auditFake()
	ctx, cancel := context.WithCancel(context.Background())
	f.onCall = func(f *fakeOS, call string) {
		if strings.HasPrefix(call, "Reindex "+cvOrphan) {
			cancel()
		}
	}
	_, err := ConvertLogIndices(ctx, &strings.Builder{}, f, conversionPlan{Prefix: cvPrefix, Logs: []logConversion{auditConversion()}})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the cancellation", err)
	}
	if f.at("DeleteIndex "+cvOrphan) >= 0 {
		t.Errorf("a cancelled run must not go on to delete anything")
	}
}

// ---- discovery: from what the instance says to a plan, or a refusal ----

const (
	cvTemplates = `{"index_templates":[` +
		`{"name":"caltechauthors-auditlogs-datastream-auditlog-v1.0.0","index_template":{"index_patterns":["caltechauthors-auditlog*"],"data_stream":{}}},` +
		`{"name":"caltechauthors-job-logs-v1.0.0","index_template":{"index_patterns":["caltechauthors-job-logs*"],"data_stream":{}}},` +
		`{"name":"caltechauthors-rdmrecords","index_template":{"index_patterns":["caltechauthors-rdmrecords*"]}}]}`
	cvNoStreams = `{"data_streams":[]}`
)

func cvSections() map[string]string {
	return map[string]string{
		"prefix":       "cfg=/Sites/caltechauthors/invenio.cfg\nprefix=caltechauthors-\n",
		"endpoint":     "status=200\n",
		"data_streams": cvNoStreams,
		"indices": `[{"index":"` + cvOrphan + `","docs.count":"306325"},{"index":"` + cvStream + `","docs.count":"120"},` +
			`{"index":"` + cvJobs + `","docs.count":"582"},{"index":"caltechauthors-records-record-v7.0.0-1","docs.count":"9"}]`,
		"aliases":      `[{"alias":"` + cvAlias + `","index":"` + cvOrphan + `"},{"alias":"` + cvAlias + `","index":"` + cvStream + `"}]`,
		"templates_ds": cvTemplates,
	}
}

func mustPlan(t *testing.T, s map[string]string) conversionPlan {
	t.Helper()
	p, err := buildConversionPlan(s)
	if err != nil {
		t.Fatalf("buildConversionPlan: %v", err)
	}
	return p
}

func TestConversionPlan_ProductionsPreFixState(t *testing.T) {
	p := mustPlan(t, cvSections())
	if p.Prefix != cvPrefix || len(p.Logs) != 2 {
		t.Fatalf("want the audit log and the job logs to convert: %+v", p)
	}
	a, j := p.Logs[0], p.Logs[1]
	if a.Label != "audit log" || a.Stream != cvStream || a.Orphan != cvOrphan || a.Plain != cvStream || a.OrphanDocs != 306325 || a.PlainDocs != 120 {
		t.Errorf("audit log = %+v", a)
	}
	if !slices.Equal(sortedPairs(a.Aliases), sortedPairs([][2]string{{cvOrphan, cvAlias}, {cvStream, cvAlias}})) {
		t.Errorf("both alias pairs come off: %v", a.Aliases)
	}
	if a.HoldOrphan != "clasm-fix-auditlog-orphan" || a.HoldPlain != "clasm-fix-auditlog-plain" {
		t.Errorf("holding names = %q %q", a.HoldOrphan, a.HoldPlain)
	}
	if j.Label != "job logs" || j.Stream != cvJobs || j.Orphan != "" || j.Plain != cvJobs || j.PlainDocs != 582 || len(j.Aliases) != 0 || j.HoldPlain != "clasm-fix-job-logs-plain" {
		t.Errorf("job logs = %+v", j)
	}
}

func sortedPairs(p [][2]string) []string {
	var out []string
	for _, x := range p {
		out = append(out, x[0]+"|"+x[1])
	}
	sort.Strings(out)
	return out
}

func TestConversionPlan_HoldingNamesCannotBecomeStreamsOrBeSweptIntoALogPattern(t *testing.T) {
	// A holding index named <prefix>auditlog* would itself be taken for a stream by the template.
	p := mustPlan(t, cvSections())
	for _, lc := range p.Logs {
		for _, name := range []string{lc.HoldOrphan, lc.HoldPlain} {
			if name == "" {
				continue
			}
			if strings.HasPrefix(name, cvPrefix) {
				t.Errorf("%s starts with the instance prefix", name)
			}
			for _, pattern := range []string{"caltechauthors-auditlog*", "caltechauthors-job-logs*"} {
				if ok, _ := path.Match(pattern, name); ok {
					t.Errorf("%s matches the data-stream template pattern %s", name, pattern)
				}
			}
		}
	}
}

func TestConversionPlan_AlreadyDataStreamsIsNothingToDo(t *testing.T) {
	s := with(cvSections(), map[string]string{
		"data_streams": `{"data_streams":[{"name":"` + cvStream + `","indices":[{"index_name":"` + cvOrphan + `"}]},{"name":"` + cvJobs + `","indices":[{"index_name":".ds-` + cvJobs + `-000001"}]}]}`,
		"indices":      `[{"index":"` + cvOrphan + `","docs.count":"5"},{"index":".ds-` + cvJobs + `-000001","docs.count":"4"}]`,
		"aliases":      `[{"alias":"` + cvAlias + `","index":"` + cvOrphan + `"}]`,
	})
	p := mustPlan(t, s)
	if len(p.Logs) != 0 {
		t.Errorf("healthy streams convert nothing: %+v", p.Logs)
	}
}

func TestConversionPlan_OrphanOnlyAfterARestore(t *testing.T) {
	s := with(cvSections(), map[string]string{
		"indices": `[{"index":"` + cvOrphan + `","docs.count":"306325"},{"index":".ds-` + cvJobs + `-000001","docs.count":"582"}]`,
		"aliases": `[{"alias":"` + cvAlias + `","index":"` + cvOrphan + `"}]`,
	})
	p := mustPlan(t, s)
	if len(p.Logs) != 2 {
		t.Fatalf("both logs came back as orphans: %+v", p)
	}
	a, j := p.Logs[0], p.Logs[1]
	if a.Orphan != cvOrphan || a.Plain != "" || a.Stream != cvStream || a.HoldPlain != "" || !slices.Equal(sortedPairs(a.Aliases), []string{cvOrphan + "|" + cvAlias}) {
		t.Errorf("audit log = %+v", a)
	}
	if j.Orphan != ".ds-"+cvJobs+"-000001" || j.Plain != "" || j.Stream != cvJobs || j.HoldOrphan != "clasm-fix-job-logs-orphan" {
		t.Errorf("job logs = %+v", j)
	}
}

func TestConversionPlan_PlainOnlyAudit(t *testing.T) {
	s := with(cvSections(), map[string]string{
		"indices":      `[{"index":"` + cvStream + `","docs.count":"120"},{"index":".ds-` + cvJobs + `-000001","docs.count":"1"}]`,
		"aliases":      `[{"alias":"` + cvAlias + `","index":"` + cvStream + `"}]`,
		"data_streams": `{"data_streams":[{"name":"` + cvJobs + `","indices":[{"index_name":".ds-` + cvJobs + `-000001"}]}]}`,
	})
	p := mustPlan(t, s)
	if len(p.Logs) != 1 || p.Logs[0].Orphan != "" || p.Logs[0].Plain != cvStream || p.Logs[0].Stream != cvStream {
		t.Errorf("plan = %+v", p.Logs)
	}
}

func TestConversionPlan_RefusalsSendNothingAndSayWhy(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]string
		mention   string
	}{
		{"no data-stream template matches", map[string]string{"templates_ds": `{"index_templates":[{"name":"x","index_template":{"index_patterns":["other*"]}}]}`}, "install-index-templates"},
		{"a template without data_stream does not count", map[string]string{"templates_ds": `{"index_templates":[{"name":"x","index_template":{"index_patterns":["caltechauthors-*"]}}]}`}, "install-index-templates"},
		{"no templates at all", map[string]string{"templates_ds": `{}`}, "install-index-templates"},
		{"the orphan's copy name is taken", map[string]string{"indices": strings.Replace(cvSections()["indices"], `{"index":"caltechauthors-records`, `{"index":"clasm-fix-auditlog-orphan","docs.count":"1"},{"index":"caltechauthors-records`, 1)}, "clasm-fix-auditlog-orphan"},
		{"the plain copy name is taken", map[string]string{"indices": strings.Replace(cvSections()["indices"], `{"index":"caltechauthors-records`, `{"index":"clasm-fix-job-logs-plain","docs.count":"1"},{"index":"caltechauthors-records`, 1)}, "clasm-fix-job-logs-plain"},
		{"two orphan generations", map[string]string{
			"indices": strings.Replace(cvSections()["indices"], `{"index":"caltechauthors-records`, `{"index":"`+strings.Replace(cvOrphan, "000001", "000002", 1)+`","docs.count":"1"},{"index":"caltechauthors-records`, 1),
			"aliases": `[{"alias":"` + cvAlias + `","index":"` + cvOrphan + `"},{"alias":"` + cvAlias + `","index":"` + strings.Replace(cvOrphan, "000001", "000002", 1) + `"}]`}, "000002"},
		{"an orphan that is not generation one", map[string]string{
			"indices": `[{"index":"` + strings.Replace(cvOrphan, "000001", "000002", 1) + `","docs.count":"5"}]`,
			"aliases": `[{"alias":"` + cvAlias + `","index":"` + strings.Replace(cvOrphan, "000001", "000002", 1) + `"}]`}, "000002"},
		{"orphan and plain disagree about the stream", map[string]string{
			"indices": `[{"index":"` + cvOrphan + `","docs.count":"5"},{"index":"caltechauthors-auditlog-other-v9","docs.count":"3"}]`,
			"aliases": `[{"alias":"` + cvAlias + `","index":"` + cvOrphan + `"},{"alias":"` + cvAlias + `","index":"caltechauthors-auditlog-other-v9"}]`}, "disagree"},
		{"a stream exists and so does a plain index of its name", map[string]string{
			"data_streams": `{"data_streams":[{"name":"` + cvJobs + `","indices":[{"index_name":".ds-` + cvJobs + `-000001"}]}]}`,
			"indices":      strings.Replace(cvSections()["indices"], `"index":"`+cvJobs+`"`, `"index":"`+cvJobs+`"`, 1)}, "already"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildConversionPlan(with(cvSections(), tc.overrides))
			if err == nil {
				t.Fatalf("want a refusal")
			}
			if exitCodeOf(err) != -1 {
				t.Errorf("a refusal is exit 1 (a plain error), got an ExitCodeError %d: %v", exitCodeOf(err), err)
			}
			if !strings.Contains(err.Error(), tc.mention) {
				t.Errorf("the refusal should mention %q: %v", tc.mention, err)
			}
		})
	}
}

func TestConversionPlan_InstanceProblemsAreClassified(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]string
		want      int
	}{
		{"no app", map[string]string{"prefix": "cfg=\nprefix=\n"}, ExitNoInput},
		{"endpoint down", map[string]string{"endpoint": "status=000\n"}, ExitUnavailable},
		{"unreadable streams", map[string]string{"data_streams": `{"data_streams":[`}, ExitData},
		{"unreadable indices", map[string]string{"indices": `nope`}, ExitData},
		{"missing section", map[string]string{"aliases": ""}, ExitData},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildConversionPlan(with(cvSections(), tc.overrides))
			if exitCodeOf(err) != tc.want {
				t.Errorf("err = %v (code %d), want %d", err, exitCodeOf(err), tc.want)
			}
		})
	}
}

func TestConversionDiscoveryCommand_IsReadOnlyAndNamesNoWildcard(t *testing.T) {
	cmd := buildConversionDiscoveryCommand(0)
	if wildcardRE.MatchString(cmd) {
		t.Errorf("no wildcard in the discovery script: %q", wildcardRE.FindString(cmd))
	}
	for _, banned := range []string{"-X PUT", "-X POST", "-X DELETE", "_reindex", "_delete_by_query"} {
		if strings.Contains(cmd, banned) {
			t.Errorf("discovery is read-only; found %q", banned)
		}
	}
	for _, want := range []string{"sec templates_ds", "sec data_streams", "sec indices", "sec aliases", "index_patterns", "data_stream"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("discovery must read %q", want)
		}
	}
}

// ---- the SSM transport: exact commands ----

func opsWith(stdouts ...string) (indexOps, *fakeSSMClient) {
	f := &fakeSSMClient{commandID: "c", finalStatus: "Success", stdoutSequence: stdouts}
	return newSSMIndexOps(f, "i-1", 0, 0), f
}

func TestSSMOps_DeleteNamesExactlyOneIndex(t *testing.T) {
	ops, f := opsWith(`{"acknowledged":true}`)
	if err := ops.DeleteIndex(context.Background(), cvOrphan); err != nil {
		t.Fatal(err)
	}
	cmd := f.sentCommands[0]
	if !strings.Contains(cmd, "-X DELETE") || !strings.Contains(cmd, "'http://localhost:9200/"+cvOrphan+"'") {
		t.Errorf("want one DELETE of the exact name, got %q", cmd)
	}
	if wildcardRE.MatchString(cmd) || strings.Contains(cmd, ",") {
		t.Errorf("a delete never carries a wildcard or a list: %q", cmd)
	}
}

func TestSSMOps_DeleteRefusesNamesThatCouldMatchMoreThanOneIndex(t *testing.T) {
	for _, bad := range []string{"", "  ", "*", "caltechauthors-*", "a,b", "_all", "all", "..", "a b", "-x", "a/b", "a?b", ".ds-*"} {
		ops, f := opsWith(`{"acknowledged":true}`)
		if err := ops.DeleteIndex(context.Background(), bad); err == nil {
			t.Errorf("DeleteIndex(%q) must be refused", bad)
		}
		if f.sendCommandCallCount != 0 {
			t.Errorf("DeleteIndex(%q) sent a command before refusing: %v", bad, f.sentCommands)
		}
	}
}

func TestSSMOps_CountRefreshesThenCounts(t *testing.T) {
	ops, f := opsWith(`{"count":306325,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0}}`)
	n, err := ops.Count(context.Background(), cvOrphan)
	if err != nil || n != 306325 {
		t.Fatalf("Count = %d, %v", n, err)
	}
	cmd := f.sentCommands[0]
	if strings.Index(cmd, "_refresh") < 0 || strings.Index(cmd, "_refresh") > strings.Index(cmd, "_count") {
		t.Errorf("an unrefreshed index undercounts; refresh must come first: %q", cmd)
	}
}

func TestSSMOps_CountFailsOnShardFailures(t *testing.T) {
	ops, _ := opsWith(`{"count":10,"_shards":{"total":2,"successful":1,"skipped":0,"failed":1}}`)
	if _, err := ops.Count(context.Background(), cvStream); err == nil {
		t.Errorf("a count with failed shards is not a count")
	}
	ops, _ = opsWith(`{"error":{"type":"index_not_found_exception","reason":"no such index"},"status":404}`)
	if _, err := ops.Count(context.Background(), cvStream); err == nil {
		t.Errorf("an error body is not a count")
	}
}

func TestSSMOps_CreateIndexIsOneReplicaFreeIndex(t *testing.T) {
	ops, f := opsWith(`{"acknowledged":true,"shards_acknowledged":true,"index":"x"}`)
	if err := ops.CreateIndex(context.Background(), "clasm-fix-auditlog-orphan"); err != nil {
		t.Fatal(err)
	}
	cmd := f.sentCommands[0]
	if !strings.Contains(cmd, "-X PUT") || !strings.Contains(cmd, "clasm-fix-auditlog-orphan") || !strings.Contains(cmd, `"number_of_replicas":0`) {
		t.Errorf("want a PUT of the exact name with no replicas, got %q", cmd)
	}
	ops, _ = opsWith(`{"error":{"type":"resource_already_exists_exception","reason":"index exists"},"status":400}`)
	if err := ops.CreateIndex(context.Background(), "x"); err == nil {
		t.Errorf("an error body is a failure")
	}
}

func TestSSMOps_ReindexCreatesOnlyAndIsPolled(t *testing.T) {
	ops, f := opsWith(
		`{"task":"abc:123"}`,
		`{"completed":false,"task":{"status":{"created":10}}}`,
		`{"completed":true,"response":{"created":42,"version_conflicts":5,"failures":[]}}`)
	if err := ops.Reindex(context.Background(), cvStream, "clasm-fix-auditlog-plain"); err != nil {
		t.Fatal(err)
	}
	if len(f.sentCommands) != 3 {
		t.Fatalf("one start and two polls, got %d commands: %v", len(f.sentCommands), f.sentCommands)
	}
	start := f.sentCommands[0]
	for _, want := range []string{"wait_for_completion=false", `"op_type":"create"`, `"conflicts":"proceed"`, `"index":"` + cvStream + `"`, `"index":"clasm-fix-auditlog-plain"`, "-X POST"} {
		if !strings.Contains(start, want) {
			t.Errorf("the reindex start must contain %q: %q", want, start)
		}
	}
	for _, poll := range f.sentCommands[1:] {
		if !strings.Contains(poll, "_tasks/abc:123") || strings.Contains(poll, "-X") {
			t.Errorf("a poll is a GET of the task, got %q", poll)
		}
	}
}

func TestSSMOps_ReindexFailuresAreErrors(t *testing.T) {
	cases := map[string][]string{
		"the start is refused":      {`{"error":{"type":"x","reason":"bad request"},"status":400}`},
		"no task id":                {`{}`},
		"the task reports an error": {`{"task":"a:1"}`, `{"completed":true,"error":{"type":"x","reason":"boom"}}`},
		"documents failed":          {`{"task":"a:1"}`, `{"completed":true,"response":{"created":1,"version_conflicts":0,"failures":[{"id":"1","cause":{"reason":"nope"}}]}}`},
		"the response is missing":   {`{"task":"a:1"}`, `{"completed":true}`},
	}
	for name, outs := range cases {
		ops, _ := opsWith(outs...)
		if err := ops.Reindex(context.Background(), "a", "b"); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestSSMOps_ReindexGivesUpAtItsTimeout(t *testing.T) {
	f := &fakeSSMClient{commandID: "c", finalStatus: "Success", stdoutSequence: []string{`{"task":"a:1"}`, `{"completed":false}`}}
	ops := newSSMIndexOps(f, "i-1", 1, 50*1_000_000) // 1ns poll, 50ms limit
	if err := ops.Reindex(context.Background(), "a", "b"); err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Errorf("a reindex that never completes must time out and say so: %v", err)
	}
}

func TestSSMOps_RemoveAliasAndCreateDataStream(t *testing.T) {
	ops, f := opsWith(`{"acknowledged":true}`, `{"acknowledged":true}`)
	if err := ops.RemoveAlias(context.Background(), cvOrphan, cvAlias); err != nil {
		t.Fatal(err)
	}
	if err := ops.CreateDataStream(context.Background(), cvStream); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.sentCommands[0], `"remove":{"index":"`+cvOrphan+`","alias":"`+cvAlias+`"}`) || !strings.Contains(f.sentCommands[0], "_aliases") {
		t.Errorf("alias removal: %q", f.sentCommands[0])
	}
	if !strings.Contains(f.sentCommands[1], "-X PUT") || !strings.Contains(f.sentCommands[1], "_data_stream/"+cvStream) {
		t.Errorf("stream creation: %q", f.sentCommands[1])
	}
}

func TestSSMOps_CreateDataStreamAcceptsOneTheAppMadeFirst(t *testing.T) {
	// The app may write between the delete and the create; the template then builds the stream.
	ops, f := opsWith(`{"error":{"type":"resource_already_exists_exception","reason":"data_stream [x] already exists"},"status":400}`,
		`{"data_streams":[{"name":"`+cvStream+`"}]}`)
	if err := ops.CreateDataStream(context.Background(), cvStream); err != nil {
		t.Errorf("a stream that exists is the goal: %v (commands %v)", err, f.sentCommands)
	}
	ops, _ = opsWith(`{"error":{"type":"x","reason":"no matching index template"},"status":400}`, `{"error":{"type":"resource_not_found_exception"},"status":404}`)
	if err := ops.CreateDataStream(context.Background(), cvStream); err == nil {
		t.Errorf("a stream that cannot be created and does not exist is a failure")
	}
}

func TestSSMOps_EveryCommandIsLocalAndCarriesNoCredentials(t *testing.T) {
	ops, f := opsWith(`{"acknowledged":true}`, `{"acknowledged":true}`, `{"acknowledged":true}`, `{"count":1,"_shards":{"total":1,"failed":0}}`, `{"acknowledged":true}`)
	ctx := context.Background()
	_ = ops.CreateIndex(ctx, "clasm-fix-x")
	_ = ops.RemoveAlias(ctx, "a", "b")
	_ = ops.DeleteIndex(ctx, "clasm-fix-y")
	_, _ = ops.Count(ctx, "c")
	_ = ops.CreateDataStream(ctx, "d")
	for _, cmd := range f.sentCommands {
		if !strings.Contains(cmd, "http://localhost:9200/") || strings.Contains(cmd, " -u ") || strings.Contains(cmd, "PASSWORD") || wildcardRE.MatchString(cmd) {
			t.Errorf("commands are plain curl to localhost, no credentials, no wildcard: %q", cmd)
		}
	}
}

func TestSSMOps_AFailedCommandIsAnErrorWithItsOutput(t *testing.T) {
	f := &fakeSSMClient{commandID: "c", finalStatus: "Failed", stdout: "curl: (7) Failed to connect"}
	ops := newSSMIndexOps(f, "i-1", 0, 0)
	err := ops.DeleteIndex(context.Background(), "clasm-fix-y")
	if err == nil || !strings.Contains(err.Error(), "Failed to connect") {
		t.Errorf("the curl failure's output belongs in the error: %v", err)
	}
	f = &fakeSSMClient{commandID: "c", sendCommandErr: errors.New("InvalidInstanceId")}
	if err := newSSMIndexOps(f, "i-1", 0, 0).DeleteIndex(context.Background(), "clasm-fix-y"); exitCodeOf(err) != ExitUnavailable {
		t.Errorf("an unreachable instance is 69: %v", err)
	}
}

func TestConvert_TheStreamMayHoldMoreThanTheCopiesBecauseTheAppWritesToIt(t *testing.T) {
	// Once the stream exists the app writes straight into it. The check is "at least what the copies
	// hold", never equality, or a busy instance's conversion would report failure on success.
	f := auditFake()
	f.onCall = func(f *fakeOS, call string) {
		if call == "Reindex clasm-fix-auditlog-plain "+cvStream {
			f.put(cvStream, 2, "live")
		}
	}
	res := mustConvert(t, f, auditConversion())
	if res[0].StreamDocs != 10 {
		t.Errorf("StreamDocs = %d, want 10 (8 from the copies + 2 the app wrote)", res[0].StreamDocs)
	}
}

func TestConversionPlan_AStreamNextToAPlainIndexOfItsNameIsRefused(t *testing.T) {
	// Defensive: OpenSearch cannot hold both, but if the listings ever said so, deleting the plain
	// index and creating the stream would be the wrong repair.
	s := with(cvSections(), map[string]string{
		"data_streams": `{"data_streams":[{"name":"` + cvStream + `","indices":[{"index_name":".ds-` + cvStream + `-000007"}]}]}`,
		"indices":      `[{"index":"` + cvStream + `","docs.count":"3"},{"index":".ds-` + cvStream + `-000007","docs.count":"9"}]`,
		"aliases":      `[{"alias":"` + cvAlias + `","index":"` + cvStream + `"}]`,
	})
	_, err := buildConversionPlan(s)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("want a refusal saying the stream already exists, got %v", err)
	}
}

func TestConversionPlan_ACopyNameThatTheInstancePrefixCouldCaptureIsAnInternalError(t *testing.T) {
	// An instance whose prefix is "clasm-" would have copies named inside its own namespace, where a
	// data-stream template pattern could take one for a stream. clasm stops rather than risk it.
	s := map[string]string{
		"prefix":       "cfg=/Sites/x/invenio.cfg\nprefix=clasm-\n",
		"endpoint":     "status=200\n",
		"data_streams": cvNoStreams,
		"indices":      `[{"index":"clasm-job-logs","docs.count":"4"}]`,
		"aliases":      `[]`,
		"templates_ds": `{"index_templates":[{"name":"t","index_template":{"index_patterns":["clasm-job-logs*"],"data_stream":{}}}]}`,
	}
	_, err := buildConversionPlan(s)
	if exitCodeOf(err) != ExitInternal || !strings.Contains(err.Error(), "prefix") {
		t.Errorf("err = %v, want an ExitCodeError 70 naming the instance prefix", err)
	}
}

// ---- found live on 2026-10-20 (R1): the log indices need not carry the alias ----
//
// Restoring rdm-20261019-123545 -- a snapshot taken between the cutover and the by-hand alias fix -- brought back
// the orphan WITH the alias and a plain index named like the stream WITHOUT it. Discovery followed the alias, so it
// planned an orphan-only conversion, deleted the orphan, and could not create the stream: a plain index of that
// name was still there. Every document survived (the copy held them), but the order's whole point is that nothing
// that can be foreseen fails after a delete. These tests are that state, and its neighbours.

func aliaslessPlain(overrides map[string]string) map[string]string {
	return with(cvSections(), overrides)
}

func TestConversionPlan_APlainIndexWithoutTheAliasIsStillTheAuditLogsPlainIndex(t *testing.T) {
	s := aliaslessPlain(map[string]string{
		"aliases": `[{"alias":"` + cvAlias + `","index":"` + cvOrphan + `"}]`, // the alias is on the orphan only
	})
	p := mustPlan(t, s)
	a := p.Logs[0]
	if a.Orphan != cvOrphan || a.Plain != cvStream || a.PlainDocs != 120 || a.HoldPlain == "" {
		t.Fatalf("the plain index named like the stream is part of the conversion even with no alias on it: %+v", a)
	}
	if !slices.Equal(sortedPairs(a.Aliases), []string{cvOrphan + "|" + cvAlias}) {
		t.Errorf("only an alias that exists is removed: %v", a.Aliases)
	}
}

func TestConversionPlan_APlainAuditIndexWithNoAliasAndNoOrphanIsFoundByItsName(t *testing.T) {
	s := aliaslessPlain(map[string]string{
		"indices":      `[{"index":"` + cvStream + `","docs.count":"120"},{"index":".ds-` + cvJobs + `-000001","docs.count":"1"}]`,
		"aliases":      `[]`,
		"data_streams": `{"data_streams":[{"name":"` + cvJobs + `","indices":[{"index_name":".ds-` + cvJobs + `-000001"}]}]}`,
	})
	p := mustPlan(t, s)
	if len(p.Logs) != 1 || p.Logs[0].Plain != cvStream || p.Logs[0].Stream != cvStream || p.Logs[0].Orphan != "" || len(p.Logs[0].Aliases) != 0 {
		t.Errorf("a plain index that matches the data-stream template's pattern is what the template wants to be a stream: %+v", p.Logs)
	}
}

func TestConversionPlan_AnOrphanWithNoAliasIsFoundByItsName(t *testing.T) {
	s := aliaslessPlain(map[string]string{
		"indices":      `[{"index":"` + cvOrphan + `","docs.count":"5"},{"index":".ds-` + cvJobs + `-000001","docs.count":"1"}]`,
		"aliases":      `[]`,
		"data_streams": `{"data_streams":[{"name":"` + cvJobs + `","indices":[{"index_name":".ds-` + cvJobs + `-000001"}]}]}`,
	})
	p := mustPlan(t, s)
	if len(p.Logs) != 1 || p.Logs[0].Orphan != cvOrphan || p.Logs[0].Stream != cvStream || len(p.Logs[0].Aliases) != 0 {
		t.Errorf("an orphaned backing index is an orphan whether or not an alias points at it: %+v", p.Logs)
	}
}

func TestConversionPlan_ThePlainIndexIsNotCountedTwiceWhenItHasTheAliasToo(t *testing.T) {
	p := mustPlan(t, cvSections()) // the alias is on both
	a := p.Logs[0]
	if a.Plain != cvStream || len(a.Aliases) != 2 {
		t.Errorf("found by alias and by name, planned once: %+v", a)
	}
}

func TestConversionPlan_TwoPlainCandidatesAreRefused(t *testing.T) {
	s := aliaslessPlain(map[string]string{
		"indices":      `[{"index":"` + cvStream + `","docs.count":"1"},{"index":"caltechauthors-auditlog-other-v9","docs.count":"2"},{"index":".ds-` + cvJobs + `-000001","docs.count":"1"}]`,
		"aliases":      `[]`,
		"data_streams": `{"data_streams":[{"name":"` + cvJobs + `","indices":[{"index_name":".ds-` + cvJobs + `-000001"}]}]}`,
	})
	_, err := buildConversionPlan(s)
	if err == nil || !strings.Contains(err.Error(), "more than one plain index") {
		t.Errorf("err = %v, want a refusal for two plain candidates", err)
	}
}

func TestConversionPlan_ACopyOrAnUnrelatedIndexIsNotACandidate(t *testing.T) {
	// The kept copies and anything outside the data-stream template's pattern are not log indices.
	s := aliaslessPlain(map[string]string{
		"indices": `[{"index":"auditlog-fix-old","docs.count":"9"},{"index":"clasm-fix-auditlog-orphan","docs.count":"9"},` +
			`{"index":"caltechauthors-records-record-v7.0.0-1","docs.count":"9"},{"index":".ds-` + cvJobs + `-000001","docs.count":"1"}]`,
		"aliases":      `[]`,
		"data_streams": `{"data_streams":[{"name":"` + cvJobs + `","indices":[{"index_name":".ds-` + cvJobs + `-000001"}]}]}`,
	})
	p := mustPlan(t, s)
	if len(p.Logs) != 0 {
		t.Errorf("nothing here is a log index to convert: %+v", p.Logs)
	}
}

// The whole thing, from the instance's state to the fake cluster: the plan must account for every index that would
// stand in the stream's way, or the fake (like OpenSearch) refuses at the create, after the orphan is gone.
func TestConvert_FromTheStateTheSnapshotProducedNothingStandsInTheStreamsWay(t *testing.T) {
	s := aliaslessPlain(map[string]string{
		"aliases": `[{"alias":"` + cvAlias + `","index":"` + cvOrphan + `"}]`,
	})
	plan := mustPlan(t, s)
	f := newFakeOS()
	f.put(cvOrphan, 5, "o")
	f.put(cvStream, 3, "p") // no alias on it
	f.alias(cvAlias, cvOrphan)
	f.put(cvJobs, 4, "j")
	before := f.everyDocument()
	res, err := ConvertLogIndices(context.Background(), &strings.Builder{}, f, plan)
	if err != nil {
		t.Fatalf("%v\ncalls:\n%s", err, strings.Join(f.calls, "\n"))
	}
	if !f.streams[cvStream] || !f.streams[cvJobs] || len(res) != 2 {
		t.Errorf("both logs are streams: %v %v %d", f.streams[cvStream], f.streams[cvJobs], len(res))
	}
	for id := range before {
		if !f.everyDocument()[id] {
			t.Errorf("document %s was lost", id)
		}
	}
	if len(f.idx[cvStream]) != 8 {
		t.Errorf("the stream holds the orphan's 5 and the plain index's 3, got %d", len(f.idx[cvStream]))
	}
}

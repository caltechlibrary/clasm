package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

func TestClassifyCLIArgs_NoArgsFallsThroughToTUI(t *testing.T) {
	mode, domainSlug, leafSlug, leafArgs, err := classifyCLIArgs(nil)
	if mode != cliModeNone || domainSlug != "" || leafSlug != "" || leafArgs != nil || err != nil {
		t.Fatalf("got mode=%q domainSlug=%q leafSlug=%q leafArgs=%v err=%v, want all zero values", mode, domainSlug, leafSlug, leafArgs, err)
	}
}

func TestClassifyCLIArgs_UnknownDomainIsAUsageError(t *testing.T) {
	mode, _, _, _, err := classifyCLIArgs([]string{"no-such-domain"})
	if mode != cliModeNone {
		t.Errorf("mode = %q, want %q", mode, cliModeNone)
	}
	if err == nil || !strings.Contains(err.Error(), "no-such-domain") {
		t.Errorf("expected a usage error naming the unknown domain, got: %v", err)
	}
}

func TestClassifyCLIArgs_DomainOnlyDeepLinksToThatDomain(t *testing.T) {
	mode, domainSlug, leafSlug, leafArgs, err := classifyCLIArgs([]string{"rdm-backup-and-restore"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != cliModeDomain || domainSlug != "rdm-backup-and-restore" || leafSlug != "" || leafArgs != nil {
		t.Errorf("got mode=%q domainSlug=%q leafSlug=%q leafArgs=%v, want domain-only", mode, domainSlug, leafSlug, leafArgs)
	}
}

func TestClassifyCLIArgs_UnknownLeafIsAUsageError(t *testing.T) {
	mode, _, _, _, err := classifyCLIArgs([]string{"rdm-backup-and-restore", "no-such-leaf"})
	if mode != cliModeNone {
		t.Errorf("mode = %q, want %q", mode, cliModeNone)
	}
	if err == nil || !strings.Contains(err.Error(), "no-such-leaf") {
		t.Errorf("expected a usage error naming the unknown leaf, got: %v", err)
	}
}

func TestClassifyCLIArgs_LeafOnlyDeepLinksToThatLeaf(t *testing.T) {
	mode, domainSlug, leafSlug, leafArgs, err := classifyCLIArgs([]string{"rdm-backup-and-restore", "archive-sql-backups-to-s3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != cliModeLeaf || domainSlug != "rdm-backup-and-restore" || leafSlug != "archive-sql-backups-to-s3" || leafArgs != nil {
		t.Errorf("got mode=%q domainSlug=%q leafSlug=%q leafArgs=%v, want leaf-only", mode, domainSlug, leafSlug, leafArgs)
	}
}

// TestClassifyCLIArgs_TrailingArgsAlwaysMeanRunRegardlessOfCount pins
// that classifyCLIArgs itself only distinguishes "zero trailing args"
// (leaf-only deep-link) from "any trailing args at all" (a full,
// non-interactive run attempt) -- exact arity is validated later by
// ParseBackupArchiveArgs/ParseOpenSearchArchiveArgs (already tested in
// cli_args_test.go), once AWS clients exist to resolve the instance
// argument against. A wrong count here must still classify as "run" (so
// it never silently falls back to an interactive prompt, decision 4),
// not surface as a separate error at this layer.
func TestClassifyCLIArgs_TrailingArgsAlwaysMeanRunRegardlessOfCount(t *testing.T) {
	for _, args := range [][]string{
		{"rdm-backup-and-restore", "archive-sql-backups-to-s3", "only-one-arg"},
		{"rdm-backup-and-restore", "archive-sql-backups-to-s3", "i-1", "/opt/dir", "s3://bucket", ""},
	} {
		mode, _, _, leafArgs, err := classifyCLIArgs(args)
		if err != nil {
			t.Fatalf("unexpected error for %v: %v", args, err)
		}
		if mode != cliModeRun {
			t.Errorf("args=%v: mode = %q, want %q", args, mode, cliModeRun)
		}
		if len(leafArgs) != len(args)-2 {
			t.Errorf("args=%v: leafArgs = %v, want the trailing %d arg(s)", args, leafArgs, len(args)-2)
		}
	}
}

// TestRunCLILeaf_UnknownSlugIsAnInternalErrorNotAPanic pins runCLILeaf's
// defensive default case -- reachable only if classifyCLIArgs and
// runCLILeaf's own switch ever drift apart, but must fail loudly (exit
// 2) rather than panic or silently do nothing. No AWS client is touched
// for this case, so nil arguments are safe to pass.
func TestRunCLILeaf_UnknownSlugIsAnInternalErrorNotAPanic(t *testing.T) {
	var eout bytes.Buffer
	code := runCLILeaf(context.Background(), &bytes.Buffer{}, &eout, "no-such-leaf", nil, cliEnv{ssmClients: map[string]awsclient.SSMAPI{}})
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(eout.String(), "no-such-leaf") {
		t.Errorf("expected the error to name the unhandled slug, got:\n%s", eout.String())
	}
}

// 2026-09-30: every domain has a slug, so `clasm <domain>` deep-links into any
// of them, not only RDM Backup & Restore (which is all the manual could have
// been describing truthfully before). Domains other than RDM still have no
// leaf-level CLI forms, so a path *under* one is a clear usage error, not a
// silent fall-through to some other domain's leaf registry.
func TestClassifyCLIArgs_EveryDomainDeepLinks(t *testing.T) {
	for _, slug := range []string{"compute", "key-management", "s3", "tag-management", "iam", "configuration", "rdm-backup-and-restore"} {
		mode, domainSlug, leafSlug, leafArgs, err := classifyCLIArgs([]string{slug})
		if err != nil {
			t.Errorf("%q: unexpected error: %v", slug, err)
			continue
		}
		if mode != cliModeDomain || domainSlug != slug || leafSlug != "" || leafArgs != nil {
			t.Errorf("%q: got mode=%q domainSlug=%q leafSlug=%q leafArgs=%v, want domain-only", slug, mode, domainSlug, leafSlug, leafArgs)
		}
	}
}

func TestClassifyCLIArgs_PathUnderADomainWithNoLeafFormsIsAUsageError(t *testing.T) {
	for _, args := range [][]string{{"s3", "show-buckets"}, {"tag-management", "show-all-tags", "extra"}, {"configuration", "edit-regions"}} {
		mode, _, _, _, err := classifyCLIArgs(args)
		if mode != cliModeNone {
			t.Errorf("%v: mode = %q, want %q", args, mode, cliModeNone)
		}
		if err == nil || !strings.Contains(err.Error(), args[0]) || !strings.Contains(err.Error(), "no CLI sub-commands yet") {
			t.Errorf("%v: expected a usage error saying %q has no CLI sub-commands yet, got: %v", args, args[0], err)
		}
	}
}

// Restore OpenSearch is the first destructive leaf with a CLI form (DR-0177,
// DR-0180). The dispatch must route it, map its errors to 0/1/2, and never reach
// AWS for a usage error or a help request.
func TestClassifyCLIArgs_RestoreOpenSearchLeaf(t *testing.T) {
	const leaf = "restore-opensearch-snapshot-from-s3"
	mode, _, leafSlug, _, err := classifyCLIArgs([]string{"rdm-backup-and-restore", leaf})
	if err != nil || mode != cliModeLeaf || leafSlug != leaf {
		t.Errorf("no further words must deep-link into the leaf's prompts: got mode=%q leaf=%q err=%v", mode, leafSlug, err)
	}
	// Options alone are still a run, never a deep-link, so a script cannot fall
	// into a prompt with no terminal.
	mode, _, _, leafArgs, err := classifyCLIArgs([]string{"rdm-backup-and-restore", leaf, "--confirm", "i-1"})
	if err != nil || mode != cliModeRun || len(leafArgs) != 2 {
		t.Errorf("options alone must be a run: got mode=%q args=%v err=%v", mode, leafArgs, err)
	}
}

func TestRunCLILeaf_RestoreOpenSearchUsageErrorExitsTwoWithoutTouchingAWS(t *testing.T) {
	var out, eout bytes.Buffer
	code := runCLILeaf(context.Background(), &out, &eout, "restore-opensearch-snapshot-from-s3", []string{"only-one-arg"}, cliEnv{})
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(eout.String(), "want 6 arguments") || !strings.Contains(eout.String(), "--confirm") {
		t.Errorf("expected the arity error with the usage on stderr, got:\n%s", eout.String())
	}
}

func TestRunCLILeaf_RestoreOpenSearchHelpPrintsTheUsageAndExitsZero(t *testing.T) {
	var out, eout bytes.Buffer
	code := runCLILeaf(context.Background(), &out, &eout, "restore-opensearch-snapshot-from-s3", []string{"--help"}, cliEnv{})
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "--confirm <instance-id-or-name>") || eout.Len() != 0 {
		t.Errorf("the usage belongs on stdout and nothing on stderr; stdout:\n%s\nstderr:\n%s", out.String(), eout.String())
	}
}

func TestRunCLILeaf_RestoreOpenSearchUnknownInstanceExitsTwo(t *testing.T) {
	var eout bytes.Buffer
	code := runCLILeaf(context.Background(), &bytes.Buffer{}, &eout, "restore-opensearch-snapshot-from-s3",
		[]string{"no-such-box", "/d", "b", "s", "latest", "p"}, cliEnv{})
	if code != 2 || !strings.Contains(eout.String(), "no-such-box") {
		t.Errorf("want exit 2 naming the instance, got %d:\n%s", code, eout.String())
	}
}

// The second destructive leaf with a CLI form: Restore SQL Backup.
func TestClassifyCLIArgs_RestoreSQLLeaf(t *testing.T) {
	const leaf = "restore-sql-backup-from-s3"
	mode, _, leafSlug, _, err := classifyCLIArgs([]string{"rdm-backup-and-restore", leaf})
	if err != nil || mode != cliModeLeaf || leafSlug != leaf {
		t.Errorf("no further words must deep-link into the leaf's prompts: got mode=%q leaf=%q err=%v", mode, leafSlug, err)
	}
	mode, _, _, leafArgs, err := classifyCLIArgs([]string{"rdm-backup-and-restore", leaf, "--confirm", "i-1"})
	if err != nil || mode != cliModeRun || len(leafArgs) != 2 {
		t.Errorf("options alone must be a run: got mode=%q args=%v err=%v", mode, leafArgs, err)
	}
}

func TestRunCLILeaf_RestoreSQLUsageErrorExitsTwoWithoutTouchingAWS(t *testing.T) {
	var out, eout bytes.Buffer
	code := runCLILeaf(context.Background(), &out, &eout, "restore-sql-backup-from-s3", []string{"only-one-arg"}, cliEnv{})
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(eout.String(), "want 4 arguments") || !strings.Contains(eout.String(), "--confirm") {
		t.Errorf("expected the arity error with the usage on stderr, got:\n%s", eout.String())
	}
}

func TestRunCLILeaf_RestoreSQLHelpPrintsTheUsageAndExitsZero(t *testing.T) {
	var out, eout bytes.Buffer
	code := runCLILeaf(context.Background(), &out, &eout, "restore-sql-backup-from-s3", []string{"--help"}, cliEnv{})
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "--confirm <instance-id-or-name>") || eout.Len() != 0 {
		t.Errorf("the usage belongs on stdout and nothing on stderr; stdout:\n%s\nstderr:\n%s", out.String(), eout.String())
	}
}

func TestRunCLILeaf_RestoreSQLUnknownInstanceExitsTwo(t *testing.T) {
	var eout bytes.Buffer
	code := runCLILeaf(context.Background(), &bytes.Buffer{}, &eout, "restore-sql-backup-from-s3",
		[]string{"no-such-box", "b", "s", "latest"}, cliEnv{})
	if code != 2 || !strings.Contains(eout.String(), "no-such-box") {
		t.Errorf("want exit 2 naming the instance, got %d:\n%s", code, eout.String())
	}
}

func TestRunCLILeaf_RestoreSQLConfirmTypoExitsTwoBeforeAnyAWSCall(t *testing.T) {
	var eout bytes.Buffer
	insts := []inventory.Instance{{InstanceID: "i-1", Name: "box", Region: "us-east-1"}}
	// nil clients: any AWS call would panic, so reaching preflight would fail this test.
	code := runCLILeaf(context.Background(), &bytes.Buffer{}, &eout, "restore-sql-backup-from-s3",
		[]string{"--confirm", "not-the-box", "box", "b", "s", "latest"}, cliEnv{instances: insts})
	if code != 2 || !strings.Contains(eout.String(), "does not match") {
		t.Errorf("want exit 2 with the mismatch, got %d:\n%s", code, eout.String())
	}
}

// Compute's seven read-only leaves have CLI forms (DR-0177). A leaf slug is only
// valid under the domain that registered it.
func TestClassifyCLIArgs_ComputeLeaves(t *testing.T) {
	mode, domainSlug, leafSlug, leafArgs, err := classifyCLIArgs([]string{"compute", "show-instances"})
	if err != nil || mode != cliModeLeaf || domainSlug != "compute" || leafSlug != "show-instances" || leafArgs != nil {
		t.Errorf("no further words must deep-link into the leaf: got mode=%q domain=%q leaf=%q args=%v err=%v", mode, domainSlug, leafSlug, leafArgs, err)
	}
	// Options alone are a run, so `-json` never falls into the TUI.
	mode, _, _, leafArgs, err = classifyCLIArgs([]string{"compute", "show-instances", "-json"})
	if err != nil || mode != cliModeRun || len(leafArgs) != 1 {
		t.Errorf("an option alone must be a run: got mode=%q args=%v err=%v", mode, leafArgs, err)
	}
	for _, args := range [][]string{
		{"compute", "start-ec2-instance"},
		{"compute", "archive-sql-backups-to-s3"},
		{"rdm-backup-and-restore", "show-instances"},
	} {
		if _, _, _, _, err := classifyCLIArgs(args); err == nil {
			t.Errorf("%v: want a usage error for a leaf under the wrong domain", args)
		}
	}
}

func computeEnv() cliEnv {
	return cliEnv{
		instances:       []inventory.Instance{{InstanceID: "i-1", Name: "box", State: "running", Region: "us-west-2"}},
		images:          []inventory.Image{{ImageID: "ami-1", Name: "img", Region: "us-west-2"}},
		launchTemplates: []inventory.LaunchTemplate{{TemplateID: "lt-1", Name: "tmpl", DefaultVersion: 1, LatestVersion: 1, Region: "us-west-2"}},
	}
}

// The three listings are the state refresh() already loaded: no AWS client is
// touched, so a nil ec2Clients map must be enough.
func TestRunCLILeaf_ShowListings(t *testing.T) {
	for _, tc := range []struct {
		leaf, textWant, jsonWant string
	}{
		{"show-instances", "INSTANCE ID", `"instance_id": "i-1"`},
		{"show-amis", "AMI ID", `"ami_id": "ami-1"`},
		{"show-launch-templates", "TEMPLATE ID", `"template_id": "lt-1"`},
	} {
		var out, eout bytes.Buffer
		if code := runCLILeaf(context.Background(), &out, &eout, tc.leaf, []string{"-text"}, computeEnv()); code != 0 || !strings.Contains(out.String(), tc.textWant) || eout.Len() != 0 {
			t.Errorf("%s -text: code=%d out=%q err=%q", tc.leaf, code, out.String(), eout.String())
		}
		out.Reset()
		if code := runCLILeaf(context.Background(), &out, &eout, tc.leaf, []string{"-json"}, computeEnv()); code != 0 || !strings.Contains(out.String(), tc.jsonWant) {
			t.Errorf("%s -json: code=%d out=%q", tc.leaf, code, out.String())
		}
		out.Reset()
		if code := runCLILeaf(context.Background(), &out, &eout, tc.leaf, []string{"-jsonl"}, computeEnv()); code != 0 || strings.Count(out.String(), "\n") != 1 {
			t.Errorf("%s -jsonl: want exactly one line, code=%d out=%q", tc.leaf, code, out.String())
		}
	}
}

func TestRunCLILeaf_ShowListingsUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"-json", "-text"}, {"-yaml"}, {"-json", "stray-word"}} {
		var out, eout bytes.Buffer
		if code := runCLILeaf(context.Background(), &out, &eout, "show-instances", args, computeEnv()); code != 2 || out.Len() != 0 || !strings.Contains(eout.String(), "usage: clasm compute show-instances") {
			t.Errorf("%v: want exit 2, usage on stderr, nothing on stdout; code=%d out=%q err=%q", args, code, out.String(), eout.String())
		}
	}
	var out, eout bytes.Buffer
	if code := runCLILeaf(context.Background(), &out, &eout, "show-instances", []string{"--help"}, computeEnv()); code != 0 || !strings.Contains(out.String(), "-jsonl") || eout.Len() != 0 {
		t.Errorf("--help: code=%d out=%q err=%q", code, out.String(), eout.String())
	}
}

// Every detail form needs its one argument (two for cloud-init's optional file);
// a wrong count is a usage error that never reaches AWS (nil ec2Clients).
func TestRunCLILeaf_ComputeDetailArityAndOptions(t *testing.T) {
	for _, tc := range []struct {
		leaf string
		args []string
		want string
	}{
		{"show-instance-detail", nil, "usage: clasm compute show-instance-detail"},
		{"show-instance-detail", []string{"-json"}, "usage: clasm compute show-instance-detail"},
		{"show-instance-detail", []string{"box", "extra"}, "usage: clasm compute show-instance-detail"},
		{"show-instance-detail", []string{"-jsonl", "box"}, "usage: clasm compute show-instance-detail"}, // list-only option
		{"show-ami-detail", []string{"img", "extra"}, "usage: clasm compute show-ami-detail"},
		{"show-launch-template-detail", []string{"a", "1", "extra"}, "usage: clasm compute show-launch-template-detail"},
		{"show-launch-template-detail", []string{"-versions", "tmpl", "2"}, "-versions"},
		{"show-export-cloud-init-for-an-instance-or-ami", []string{"a", "f", "extra"}, "usage: clasm compute show-export-cloud-init"},
		{"show-export-cloud-init-for-an-instance-or-ami", []string{"-launch-temporary-instance"}, "usage: clasm compute show-export-cloud-init"},
		{"show-instance-detail", []string{"no-such-box"}, "no-such-box"},
	} {
		var out, eout bytes.Buffer
		code := runCLILeaf(context.Background(), &out, &eout, tc.leaf, tc.args, computeEnv())
		if code != 2 || out.Len() != 0 || !strings.Contains(eout.String(), tc.want) {
			t.Errorf("%s %v: want exit 2 mentioning %q; code=%d out=%q err=%q", tc.leaf, tc.args, tc.want, code, out.String(), eout.String())
		}
	}
}

func TestRunCLILeaf_ComputeDetailHelp(t *testing.T) {
	for leaf, want := range map[string]string{
		"show-instance-detail":                          "-json",
		"show-launch-template-detail":                   "-versions",
		"show-export-cloud-init-for-an-instance-or-ami": "-launch-temporary-instance",
	} {
		var out, eout bytes.Buffer
		if code := runCLILeaf(context.Background(), &out, &eout, leaf, []string{"--help"}, cliEnv{}); code != 0 || !strings.Contains(out.String(), want) || eout.Len() != 0 {
			t.Errorf("%s --help: code=%d out=%q err=%q", leaf, code, out.String(), eout.String())
		}
	}
}

func TestRunCLILeaf_ExportCloudInitAMIWithoutConsentExitsTwo(t *testing.T) {
	var out, eout bytes.Buffer
	code := runCLILeaf(context.Background(), &out, &eout, "show-export-cloud-init-for-an-instance-or-ami", []string{"ami-1"}, computeEnv())
	if code != 2 || !strings.Contains(eout.String(), "-launch-temporary-instance") {
		t.Errorf("code=%d err=%q", code, eout.String())
	}
}

// Key Management and IAM read-only leaves (DR-0177).
func TestClassifyCLIArgs_KeyManagementAndIAMLeaves(t *testing.T) {
	for _, tc := range [][]string{{"key-management", "show-key-pairs"}, {"iam", "show-roles"}, {"iam", "show-instance-profile-detail"}} {
		mode, domainSlug, leafSlug, _, err := classifyCLIArgs(tc)
		if err != nil || mode != cliModeLeaf || domainSlug != tc[0] || leafSlug != tc[1] {
			t.Errorf("%v: mode=%q domain=%q leaf=%q err=%v", tc, mode, domainSlug, leafSlug, err)
		}
	}
	for _, tc := range [][]string{{"iam", "delete-role"}, {"iam", "show-key-pairs"}, {"key-management", "show-roles"}, {"key-management", "create-key-pair"}} {
		if _, _, _, _, err := classifyCLIArgs(tc); err == nil {
			t.Errorf("%v: want a usage error", tc)
		}
	}
}

func TestRunCLILeaf_ShowKeyPairs(t *testing.T) {
	env := cliEnv{keyPairs: []inventory.KeyPair{{KeyName: "caltechauthors", KeyPairID: "key-1", Region: "us-west-2"}}}
	var out, eout bytes.Buffer
	if code := runCLILeaf(context.Background(), &out, &eout, "show-key-pairs", []string{"-json"}, env); code != 0 || !strings.Contains(out.String(), `"key_name": "caltechauthors"`) || eout.Len() != 0 {
		t.Errorf("-json: code=%d out=%q err=%q", code, out.String(), eout.String())
	}
	out.Reset()
	if code := runCLILeaf(context.Background(), &out, &eout, "show-key-pairs", []string{"-jsonl"}, env); code != 0 || strings.Count(out.String(), "\n") != 1 {
		t.Errorf("-jsonl: code=%d out=%q", code, out.String())
	}
	out.Reset()
	if code := runCLILeaf(context.Background(), &out, &eout, "show-key-pairs", []string{"--help"}, env); code != 0 || !strings.Contains(out.String(), "-jsonl") {
		t.Errorf("--help: code=%d out=%q", code, out.String())
	}
	if code := runCLILeaf(context.Background(), &out, &eout, "show-key-pairs", []string{"-json", "stray"}, env); code != 2 {
		t.Errorf("stray word: code=%d", code)
	}
}

// IAM forms: usage errors never reach AWS (nil iamClient would panic).
func TestRunCLILeaf_IAMUsageErrorsExitTwoBeforeAnyAWSCall(t *testing.T) {
	for _, tc := range []struct {
		leaf string
		args []string
		want string
	}{
		{"show-roles", []string{"-json", "-text"}, "usage: clasm iam show-roles"},
		{"show-roles", []string{"-json", "stray"}, "usage: clasm iam show-roles"},
		{"show-instance-profiles", []string{"-yaml"}, "usage: clasm iam show-instance-profiles"},
		{"show-policies", []string{"-json", "stray"}, "usage: clasm iam show-policies"},
		{"show-role-detail", []string{"-json"}, "usage: clasm iam show-role-detail"},
		{"show-role-detail", []string{"a", "b"}, "usage: clasm iam show-role-detail"},
		{"show-role-detail", []string{"-jsonl", "a"}, "usage: clasm iam show-role-detail"},
		{"show-instance-profile-detail", []string{"a", "b"}, "usage: clasm iam show-instance-profile-detail"},
	} {
		var out, eout bytes.Buffer
		if code := runCLILeaf(context.Background(), &out, &eout, tc.leaf, tc.args, cliEnv{}); code != 2 || out.Len() != 0 || !strings.Contains(eout.String(), tc.want) {
			t.Errorf("%s %v: code=%d out=%q err=%q", tc.leaf, tc.args, code, out.String(), eout.String())
		}
	}
	for _, leaf := range []string{"show-roles", "show-instance-profiles", "show-policies", "show-role-detail", "show-instance-profile-detail"} {
		var out, eout bytes.Buffer
		if code := runCLILeaf(context.Background(), &out, &eout, leaf, []string{"--help"}, cliEnv{}); code != 0 || !strings.Contains(out.String(), "-json") || eout.Len() != 0 {
			t.Errorf("%s --help: code=%d out=%q err=%q", leaf, code, out.String(), eout.String())
		}
	}
}

// Each domain loads only the data its leaves read: IAM must not wait on (or
// fail because of) the EC2 listings.
func TestRefreshForCLIDomain_LoadsOnlyWhatTheDomainReads(t *testing.T) {
	for domain, want := range map[string]string{
		"compute": "ec2", "rdm-backup-and-restore": "ec2", "key-management": "keys", "iam": "",
	} {
		var called []string
		ec2 := func(context.Context) error { called = append(called, "ec2"); return nil }
		keys := func(context.Context) error { called = append(called, "keys"); return nil }
		if err := refreshForCLIDomain(context.Background(), domain, ec2, keys); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(called, ","); got != want {
			t.Errorf("%s loaded %q, want %q", domain, got, want)
		}
	}
}

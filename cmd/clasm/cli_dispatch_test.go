package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
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
// been describing truthfully before).
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

// Every domain now has at least one leaf form, so "no CLI sub-commands yet" is
// no longer reachable through the real registry; a path to a leaf without a form
// (Configuration's editors stay interactive-only) is an unknown command.
func TestClassifyCLIArgs_LeafWithoutAFormIsAUsageError(t *testing.T) {
	for _, args := range [][]string{{"configuration", "edit-regions"}, {"configuration", "show-config", "extra"}, {"configuration", "save"}} {
		mode, _, _, _, err := classifyCLIArgs(args)
		if mode != cliModeNone {
			t.Errorf("%v: mode = %q, want %q", args, mode, cliModeNone)
		}
		if err == nil || !strings.Contains(err.Error(), "unknown command") || !strings.Contains(err.Error(), args[0]) {
			t.Errorf("%v: expected an unknown-command usage error naming %q, got: %v", args, args[0], err)
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
		{"compute", "terminate-ec2-instance"}, // menu-only: destructive, no form yet
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
	for _, tc := range [][]string{{"iam", "attach-policy-to-role"}, {"iam", "show-key-pairs"}, {"key-management", "show-roles"}, {"key-management", "create-key-pair"}} {
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
// fail because of) the EC2 listings, and S3 must not load key pairs.
func TestRefreshForCLIDomain_LoadsOnlyWhatTheDomainReads(t *testing.T) {
	for domain, want := range map[string]string{
		"compute": "ec2", "rdm-backup-and-restore": "ec2", "key-management": "keys", "s3": "s3", "tag-management": "tags", "iam": "",
	} {
		var called []string
		mark := func(name string) func(context.Context) error {
			return func(context.Context) error { called = append(called, name); return nil }
		}
		loaders := map[string]func(context.Context) error{
			"compute": mark("ec2"), "rdm-backup-and-restore": mark("ec2"), "key-management": mark("keys"),
			"s3": mark("s3"), "tag-management": mark("tags"),
		}
		if err := refreshForCLIDomain(context.Background(), domain, loaders); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(called, ","); got != want {
			t.Errorf("%s loaded %q, want %q", domain, got, want)
		}
	}
}

func TestClassifyCLIArgs_S3AndTagManagementLeaves(t *testing.T) {
	for _, tc := range [][]string{{"s3", "show-buckets"}, {"tag-management", "show-all-tags"}} {
		mode, _, leafSlug, _, err := classifyCLIArgs(tc)
		if err != nil || mode != cliModeLeaf || leafSlug != tc[1] {
			t.Errorf("%v: mode=%q leaf=%q err=%v", tc, mode, leafSlug, err)
		}
	}
	mode, _, _, leafArgs, err := classifyCLIArgs([]string{"tag-management", "show-all-tags", "instance"})
	if err != nil || mode != cliModeRun || len(leafArgs) != 1 {
		t.Errorf("a kind argument is a run: mode=%q args=%v err=%v", mode, leafArgs, err)
	}
	for _, tc := range [][]string{{"s3", "delete-bucket"}, {"tag-management", "manage-tags"}} {
		if _, _, _, _, err := classifyCLIArgs(tc); err == nil {
			t.Errorf("%v: want a usage error", tc)
		}
	}
}

func TestRunCLILeaf_ShowBuckets(t *testing.T) {
	env := cliEnv{buckets: []inventory.Bucket{{Name: "sql-backups", Region: "us-west-2", Purpose: "backup"}}}
	var out, eout bytes.Buffer
	if code := runCLILeaf(context.Background(), &out, &eout, "show-buckets", []string{"-json"}, env); code != 0 || !strings.Contains(out.String(), `"name": "sql-backups"`) || eout.Len() != 0 {
		t.Errorf("-json: code=%d out=%q err=%q", code, out.String(), eout.String())
	}
	out.Reset()
	if code := runCLILeaf(context.Background(), &out, &eout, "show-buckets", []string{"-jsonl"}, env); code != 0 || strings.Count(out.String(), "\n") != 1 {
		t.Errorf("-jsonl: code=%d out=%q", code, out.String())
	}
	if code := runCLILeaf(context.Background(), &out, &eout, "show-buckets", []string{"-json", "stray"}, env); code != 2 {
		t.Errorf("stray word: code=%d", code)
	}
}

func TestRunCLILeaf_ShowAllTags(t *testing.T) {
	env := cliEnv{instances: []inventory.Instance{{InstanceID: "i-1", Name: "web-1", Tags: map[string]string{"Owner": "dld"}}}}
	var out, eout bytes.Buffer
	if code := runCLILeaf(context.Background(), &out, &eout, "show-all-tags", []string{"-json", "instance"}, env); code != 0 || !strings.Contains(out.String(), `"Owner": "dld"`) || !strings.Contains(out.String(), `"kind": "instance"`) {
		t.Errorf("code=%d out=%q err=%q", code, out.String(), eout.String())
	}
	for _, args := range [][]string{nil, {"-json"}, {"instance", "ami"}, {"-json", "-text", "instance"}} {
		out.Reset()
		eout.Reset()
		if code := runCLILeaf(context.Background(), &out, &eout, "show-all-tags", args, env); code != 2 || out.Len() != 0 || !strings.Contains(eout.String(), "usage: clasm tag-management show-all-tags") {
			t.Errorf("%v: want exit 2 with usage; code=%d out=%q err=%q", args, code, out.String(), eout.String())
		}
	}
	out.Reset()
	eout.Reset()
	if code := runCLILeaf(context.Background(), &out, &eout, "show-all-tags", []string{"volume"}, env); code != 2 || !strings.Contains(eout.String(), `unknown kind "volume"`) || !strings.Contains(eout.String(), "iam-policy") {
		t.Errorf("unknown kind: code=%d err=%q", code, eout.String())
	}
	out.Reset()
	if code := runCLILeaf(context.Background(), &out, &eout, "show-all-tags", []string{"--help"}, env); code != 0 || !strings.Contains(out.String(), "launch-template") || !strings.Contains(out.String(), "iam-instance-profile") {
		t.Errorf("--help should list the kinds: code=%d out=%q", code, out.String())
	}
}

// The last RDM leaf with a form: Generate SQL Backup (DR-0177).
func TestClassifyCLIArgs_GenerateSQLBackupLeaf(t *testing.T) {
	const leaf = "generate-sql-backup"
	mode, _, leafSlug, _, err := classifyCLIArgs([]string{"rdm-backup-and-restore", leaf})
	if err != nil || mode != cliModeLeaf || leafSlug != leaf {
		t.Errorf("no further words must deep-link into the prompts: mode=%q leaf=%q err=%v", mode, leafSlug, err)
	}
	mode, _, _, leafArgs, err := classifyCLIArgs([]string{"rdm-backup-and-restore", leaf, "box", "/d"})
	if err != nil || mode != cliModeRun || len(leafArgs) != 2 {
		t.Errorf("arguments are a run: mode=%q args=%v err=%v", mode, leafArgs, err)
	}
}

func TestRunCLILeaf_GenerateSQLBackupUsageErrorsExitTwoWithoutTouchingAWS(t *testing.T) {
	insts := []inventory.Instance{{InstanceID: "i-1", Name: "box", Region: "us-east-1"}}
	for name, args := range map[string][]string{
		"one argument": {"box"}, "three": {"box", "/d", "x"}, "unknown box": {"nope", "/d"}, "empty dir": {"box", ""},
	} {
		var out, eout bytes.Buffer
		// nil clients: any AWS call would panic, so reaching one would fail this test.
		code := runCLILeaf(context.Background(), &out, &eout, "generate-sql-backup", args, cliEnv{instances: insts})
		if code != 2 || out.Len() != 0 || !strings.Contains(eout.String(), "generate-sql-backup") {
			t.Errorf("%s: code=%d out=%q err=%q", name, code, out.String(), eout.String())
		}
	}
	var out, eout bytes.Buffer
	if code := runCLILeaf(context.Background(), &out, &eout, "generate-sql-backup", []string{"--help"}, cliEnv{}); code != 0 || !strings.Contains(out.String(), "<instance> <directory>") || eout.Len() != 0 {
		t.Errorf("--help: code=%d out=%q err=%q", code, out.String(), eout.String())
	}
}

// A region with no SSM client is a failure (exit 1), not a usage error.
func TestRunCLILeaf_GenerateSQLBackupWithoutAnSSMClientIsAFailure(t *testing.T) {
	insts := []inventory.Instance{{InstanceID: "i-1", Name: "box", Region: "us-east-1"}}
	var eout bytes.Buffer
	code := runCLILeaf(context.Background(), &bytes.Buffer{}, &eout, "generate-sql-backup", []string{"box", "/d"},
		cliEnv{instances: insts, ssmClients: map[string]awsclient.SSMAPI{}})
	if code != 1 || eout.Len() == 0 {
		t.Errorf("code=%d err=%q, want exit 1 with a message", code, eout.String())
	}
}

func TestClassifyCLIArgs_ComputeCreateLeaves(t *testing.T) {
	for _, leaf := range []string{"create-ec2-instance-from-launch-template", "create-launch-template-from-cloud-init-yaml"} {
		mode, _, leafSlug, _, err := classifyCLIArgs([]string{"compute", leaf})
		if err != nil || mode != cliModeLeaf || leafSlug != leaf {
			t.Errorf("%s: no further words must deep-link: mode=%q leaf=%q err=%v", leaf, mode, leafSlug, err)
		}
		mode, _, _, leafArgs, err := classifyCLIArgs([]string{"compute", leaf, "-json"})
		if err != nil || mode != cliModeRun || len(leafArgs) != 1 {
			t.Errorf("%s: an option alone is a run: mode=%q args=%v err=%v", leaf, mode, leafArgs, err)
		}
	}
}

func TestRunCLILeaf_CreateEC2InstanceFromLaunchTemplate(t *testing.T) {
	// Usage errors never reach AWS (nil ec2Clients would fail a call).
	for name, args := range map[string][]string{
		"no template": nil, "only an option": {"-json"}, "three": {"a", "1", "x"}, "list-only option": {"-jsonl", "a"},
		"unknown template": {"no-such-template"},
	} {
		var out, eout bytes.Buffer
		code := runCLILeaf(context.Background(), &out, &eout, "create-ec2-instance-from-launch-template", args, computeEnv())
		if code != 2 || out.Len() != 0 || eout.Len() == 0 {
			t.Errorf("%s: code=%d out=%q err=%q", name, code, out.String(), eout.String())
		}
	}
	var out, eout bytes.Buffer
	if code := runCLILeaf(context.Background(), &out, &eout, "create-ec2-instance-from-launch-template", []string{"--help"}, cliEnv{}); code != 0 || !strings.Contains(out.String(), "<template") || eout.Len() != 0 {
		t.Errorf("--help: code=%d out=%q err=%q", code, out.String(), eout.String())
	}
}

func TestRunCLILeaf_CreateLaunchTemplateFromCloudInitYAML(t *testing.T) {
	const leaf = "create-launch-template-from-cloud-init-yaml"
	valid := []string{
		"--name", "t", "--ami", "ami-1", "--instance-type", "t3.micro", "--key-pair", "k", "--security-group", "sg-1",
		"--subnet", "subnet-1", "--iam-instance-profile", "p", "--name-tag", "n", "--environment", "test",
	}
	// Usage errors never reach AWS (an empty cliEnv has no clients).
	for name, args := range map[string][]string{
		"no arguments":  nil,
		"no file":       valid,
		"missing ami":   {"--name", "t", "x.yaml"},
		"bad env":       append(append([]string{}, valid[:len(valid)-1]...), "staging", "x.yaml"),
		"jsonl refused": append([]string{"-jsonl"}, append(append([]string{}, valid...), "x.yaml")...),
	} {
		var out, eout bytes.Buffer
		code := runCLILeaf(context.Background(), &out, &eout, leaf, args, cliEnv{})
		if code != 2 || out.Len() != 0 || !strings.Contains(eout.String(), "usage: clasm compute "+leaf) {
			t.Errorf("%s: want exit 2 with the usage; code=%d out=%q err=%q", name, code, out.String(), eout.String())
		}
	}
	// Valid options reach the form itself: an unreadable file is refused before
	// any AWS call, so nil clients are safe.
	var out, eout bytes.Buffer
	code := runCLILeaf(context.Background(), &out, &eout, leaf, append(append([]string{}, valid...), "/no/such/cloud-init.yaml"), cliEnv{})
	if code != 2 || !strings.Contains(eout.String(), "/no/such/cloud-init.yaml") {
		t.Errorf("unreadable file: code=%d err=%q", code, eout.String())
	}
	out.Reset()
	eout.Reset()
	if code := runCLILeaf(context.Background(), &out, &eout, leaf, []string{"--help"}, cliEnv{}); code != 0 || !strings.Contains(out.String(), "--security-group") || !strings.Contains(out.String(), "never creates") || eout.Len() != 0 {
		t.Errorf("--help: code=%d out=%q err=%q", code, out.String(), eout.String())
	}
}

func TestClassifyCLIArgs_ConfigurationShowCurrentConfig(t *testing.T) {
	mode, domainSlug, leafSlug, _, err := classifyCLIArgs([]string{"configuration", "show-current-config"})
	if err != nil || mode != cliModeLeaf || domainSlug != "configuration" || leafSlug != "show-current-config" {
		t.Errorf("mode=%q domain=%q leaf=%q err=%v", mode, domainSlug, leafSlug, err)
	}
	if _, _, _, _, err := classifyCLIArgs([]string{"configuration", "edit-regions"}); err == nil {
		t.Error("an editor has no CLI form: want a usage error")
	}
}

func TestRunCLILeaf_ShowCurrentConfig(t *testing.T) {
	env := cliEnv{config: config.Config{Regions: []string{"us-west-2"}, CloudInitExtractionSecurityGroups: map[string]string{"us-west-2": "sg-open"}}}
	var out, eout bytes.Buffer
	if code := runCLILeaf(context.Background(), &out, &eout, "show-current-config", []string{"-json"}, env); code != 0 || !strings.Contains(out.String(), `"us-west-2": "sg-open"`) || eout.Len() != 0 {
		t.Errorf("-json: code=%d out=%q err=%q", code, out.String(), eout.String())
	}
	out.Reset()
	if code := runCLILeaf(context.Background(), &out, &eout, "show-current-config", []string{"-text"}, env); code != 0 || !strings.Contains(out.String(), "us-west-2: sg-open") {
		t.Errorf("-text: code=%d out=%q", code, out.String())
	}
	out.Reset()
	if code := runCLILeaf(context.Background(), &out, &eout, "show-current-config", []string{"--help"}, env); code != 0 || !strings.Contains(out.String(), "usage: clasm configuration show-current-config") {
		t.Errorf("--help: code=%d out=%q", code, out.String())
	}
	if code := runCLILeaf(context.Background(), &out, &eout, "show-current-config", []string{"-json", "stray"}, env); code != 2 {
		t.Errorf("a stray word: code=%d, want 2", code)
	}
}

// create-role-from-template (DR-0186): the first mutating IAM form. A bare slug
// deep-links into the TUI template flow; options or words after it are a run.
func TestClassifyCLIArgs_CreateRoleFromTemplate(t *testing.T) {
	mode, domainSlug, leafSlug, leafArgs, err := classifyCLIArgs([]string{"iam", "create-role-from-template"})
	if err != nil || mode != cliModeLeaf || domainSlug != "iam" || leafSlug != "create-role-from-template" || leafArgs != nil {
		t.Errorf("a bare slug must deep-link: mode=%q domain=%q leaf=%q args=%v err=%v", mode, domainSlug, leafSlug, leafArgs, err)
	}
	mode, _, _, leafArgs, err = classifyCLIArgs([]string{"iam", "create-role-from-template", "--backup-bucket", "b", "rdm-restore-source-read-only", "r"})
	if err != nil || mode != cliModeRun || len(leafArgs) != 4 {
		t.Errorf("options and words must be a run: mode=%q args=%v err=%v", mode, leafArgs, err)
	}
	if _, _, _, _, err := classifyCLIArgs([]string{"compute", "create-role-from-template"}); err == nil {
		t.Errorf("the leaf under the wrong domain must be a usage error")
	}
}

// Usage errors and --help never reach AWS (a nil iamClient would panic).
func TestRunCLILeaf_CreateRoleFromTemplateUsageErrorsExitTwoBeforeAnyAWSCall(t *testing.T) {
	for _, args := range [][]string{
		{"-json"},
		{"no-such-template", "r"},
		{"rdm-restore-source-read-only", "r"}, // required --backup-bucket missing
		{"--backup-bucket", "b", "--distribution", "E1", "rdm-restore-source-read-only", "r"},
	} {
		var out, eout bytes.Buffer
		if code := runCLILeaf(context.Background(), &out, &eout, "create-role-from-template", args, cliEnv{}); code != 2 || out.Len() != 0 || !strings.Contains(eout.String(), "usage: clasm iam create-role-from-template") {
			t.Errorf("%v: code=%d out=%q err=%q", args, code, out.String(), eout.String())
		}
	}
	var out, eout bytes.Buffer
	if code := runCLILeaf(context.Background(), &out, &eout, "create-role-from-template", []string{"--help"}, cliEnv{}); code != 0 || !strings.Contains(out.String(), "rdm-restore-source-read-only") || eout.Len() != 0 {
		t.Errorf("--help: code=%d out=%q err=%q", code, out.String(), eout.String())
	}
}

// recordingIAM stands in for the IAM client: it records the policy document the
// creating form sends and fails any other call it was not meant to make.
type recordingIAM struct {
	awsclient.IAMAPI
	policyDoc string
}

func (r *recordingIAM) CreateRole(_ context.Context, p *iam.CreateRoleInput, _ ...func(*iam.Options)) (*iam.CreateRoleOutput, error) {
	return &iam.CreateRoleOutput{Role: &iamtypes.Role{RoleName: p.RoleName, Arn: aws.String("arn:aws:iam::1:role/" + aws.ToString(p.RoleName))}}, nil
}

func (r *recordingIAM) CreatePolicy(_ context.Context, p *iam.CreatePolicyInput, _ ...func(*iam.Options)) (*iam.CreatePolicyOutput, error) {
	r.policyDoc = aws.ToString(p.PolicyDocument)
	return &iam.CreatePolicyOutput{Policy: &iamtypes.Policy{Arn: aws.String("arn:aws:iam::1:policy/x")}}, nil
}

func (r *recordingIAM) AttachRolePolicy(context.Context, *iam.AttachRolePolicyInput, ...func(*iam.Options)) (*iam.AttachRolePolicyOutput, error) {
	return &iam.AttachRolePolicyOutput{}, nil
}

// The form builds ARNs from the account and region main resolved.
func TestRunCLILeaf_CreateRoleFromTemplateUsesTheAccountAndRegion(t *testing.T) {
	rec := &recordingIAM{}
	env := cliEnv{iamClient: rec, accountID: "123456789012", region: "us-west-2"}
	var out, eout bytes.Buffer
	args := []string{"--log-group", "/bridge/app", "bridge-service", "ec2-bridge"}
	if code := runCLILeaf(context.Background(), &out, &eout, "create-role-from-template", args, env); code != 0 || !strings.Contains(out.String(), "Created role ec2-bridge") {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), eout.String())
	}
	if !strings.Contains(rec.policyDoc, "arn:aws:logs:us-west-2:123456789012:log-group:/bridge/app") {
		t.Errorf("policy = %s", rec.policyDoc)
	}
}

// delete-role and delete-instance-profile (DR-0186) are destructive forms on
// DR-0180's gate. Usage errors, --help and a --confirm mismatch never reach AWS
// (a nil iamClient would panic).
func TestClassifyCLIArgs_IAMDeleteLeaves(t *testing.T) {
	for _, leaf := range []string{"delete-role", "delete-instance-profile"} {
		mode, _, got, _, err := classifyCLIArgs([]string{"iam", leaf})
		if err != nil || mode != cliModeLeaf || got != leaf {
			t.Errorf("%s: a bare slug must deep-link: mode=%q leaf=%q err=%v", leaf, mode, got, err)
		}
		mode, _, _, args, err := classifyCLIArgs([]string{"iam", leaf, "--confirm", "x", "x"})
		if err != nil || mode != cliModeRun || len(args) != 3 {
			t.Errorf("%s: options and a name must be a run: mode=%q args=%v err=%v", leaf, mode, args, err)
		}
	}
}

func TestRunCLILeaf_IAMDeleteLeavesRefuseBeforeAnyAWSCall(t *testing.T) {
	for _, tc := range []struct {
		leaf, usage string
		args        []string
	}{
		{"delete-role", "usage: clasm iam delete-role", []string{}},
		{"delete-role", "usage: clasm iam delete-role", []string{"a", "b"}},
		{"delete-role", "usage: clasm iam delete-role", []string{"--nope", "a"}},
		{"delete-role", "does not match the target IAM role", []string{"--confirm", "other", "ec2-test-restore-role"}},
		{"delete-instance-profile", "usage: clasm iam delete-instance-profile", []string{}},
		{"delete-instance-profile", "does not match the target IAM instance profile", []string{"--confirm", "other", "restore-profile"}},
	} {
		var out, eout bytes.Buffer
		if code := runCLILeaf(context.Background(), &out, &eout, tc.leaf, tc.args, cliEnv{}); code != 2 || out.Len() != 0 || !strings.Contains(eout.String(), tc.usage) {
			t.Errorf("%s %v: code=%d out=%q err=%q", tc.leaf, tc.args, code, out.String(), eout.String())
		}
	}
	for leaf, want := range map[string]string{"delete-role": "--confirm <role-name>", "delete-instance-profile": "--confirm <profile-name>"} {
		var out, eout bytes.Buffer
		if code := runCLILeaf(context.Background(), &out, &eout, leaf, []string{"--help"}, cliEnv{}); code != 0 || !strings.Contains(out.String(), want) || eout.Len() != 0 {
			t.Errorf("%s --help: code=%d out=%q err=%q", leaf, code, out.String(), eout.String())
		}
	}
}

// The instance-profile swap (DR-0185). Usage errors and --help never reach AWS
// (nil clients would panic); an unknown instance is a usage error too.
func TestClassifyCLIArgs_AssociateInstanceProfile(t *testing.T) {
	const leaf = "associate-replace-iam-instance-profile"
	mode, domainSlug, got, args, err := classifyCLIArgs([]string{"compute", leaf})
	if err != nil || mode != cliModeLeaf || domainSlug != "compute" || got != leaf || args != nil {
		t.Errorf("a bare slug must deep-link: mode=%q domain=%q leaf=%q err=%v", mode, domainSlug, got, err)
	}
	mode, _, _, args, err = classifyCLIArgs([]string{"compute", leaf, "box", "restore-profile"})
	if err != nil || mode != cliModeRun || len(args) != 2 {
		t.Errorf("two words must be a run: mode=%q args=%v err=%v", mode, args, err)
	}
	if _, _, _, _, err := classifyCLIArgs([]string{"iam", leaf}); err == nil {
		t.Errorf("the leaf under the wrong domain must be a usage error")
	}
}

func TestRunCLILeaf_AssociateInstanceProfileUsageErrorsExitTwoBeforeAnyAWSCall(t *testing.T) {
	const leaf = "associate-replace-iam-instance-profile"
	for _, args := range [][]string{{"-json"}, {"box"}, {"a", "b", "c"}, {"-jsonl", "a", "b"}} {
		var out, eout bytes.Buffer
		if code := runCLILeaf(context.Background(), &out, &eout, leaf, args, cliEnv{}); code != 2 || out.Len() != 0 || !strings.Contains(eout.String(), "usage: clasm compute "+leaf) {
			t.Errorf("%v: code=%d out=%q err=%q", args, code, out.String(), eout.String())
		}
	}
	var out, eout bytes.Buffer
	if code := runCLILeaf(context.Background(), &out, &eout, leaf, []string{"no-such-box", "restore-profile"}, computeEnv()); code != 2 || !strings.Contains(eout.String(), "no instance found") {
		t.Errorf("unknown instance: code=%d err=%q", code, eout.String())
	}
	out.Reset()
	eout.Reset()
	if code := runCLILeaf(context.Background(), &out, &eout, leaf, []string{"--help"}, cliEnv{}); code != 0 || !strings.Contains(out.String(), "already has") || eout.Len() != 0 {
		t.Errorf("--help: code=%d out=%q err=%q", code, out.String(), eout.String())
	}
}

// Start EC2 instance's form (DR-0194). Usage errors and --help never reach AWS
// (nil clients would panic); an unknown instance is a usage error too.
func TestClassifyCLIArgs_StartInstance(t *testing.T) {
	const leaf = "start-ec2-instance"
	mode, domainSlug, got, args, err := classifyCLIArgs([]string{"compute", leaf})
	if err != nil || mode != cliModeLeaf || domainSlug != "compute" || got != leaf || args != nil {
		t.Errorf("a bare slug must deep-link: mode=%q domain=%q leaf=%q err=%v", mode, domainSlug, got, err)
	}
	mode, _, _, args, err = classifyCLIArgs([]string{"compute", leaf, "box"})
	if err != nil || mode != cliModeRun || len(args) != 1 {
		t.Errorf("one word must be a run: mode=%q args=%v err=%v", mode, args, err)
	}
	if _, _, _, _, err := classifyCLIArgs([]string{"iam", leaf}); err == nil {
		t.Errorf("the leaf under the wrong domain must be a usage error")
	}
}

func TestRunCLILeaf_StartInstanceUsageErrorsExitTwoBeforeAnyAWSCall(t *testing.T) {
	const leaf = "start-ec2-instance"
	for _, args := range [][]string{{"-json"}, {"a", "b"}, {"-jsonl", "a"}, {"-bogus", "box"}} {
		var out, eout bytes.Buffer
		if code := runCLILeaf(context.Background(), &out, &eout, leaf, args, cliEnv{}); code != 2 || out.Len() != 0 || !strings.Contains(eout.String(), "usage: clasm compute "+leaf) {
			t.Errorf("%v: code=%d out=%q err=%q", args, code, out.String(), eout.String())
		}
	}
	var out, eout bytes.Buffer
	if code := runCLILeaf(context.Background(), &out, &eout, leaf, []string{"no-such-box"}, computeEnv()); code != 2 || !strings.Contains(eout.String(), "no instance found") {
		t.Errorf("unknown instance: code=%d err=%q", code, eout.String())
	}
	out.Reset()
	eout.Reset()
	if code := runCLILeaf(context.Background(), &out, &eout, leaf, []string{"--help"}, cliEnv{}); code != 0 || !strings.Contains(out.String(), leaf) || eout.Len() != 0 {
		t.Errorf("--help: code=%d out=%q err=%q", code, out.String(), eout.String())
	}
}

// Stop EC2 instance's form (DR-0194, DR-0195): --confirm is accepted, and every
// usage error and --help stops before AWS (nil clients would panic).
func TestClassifyCLIArgs_StopInstance(t *testing.T) {
	const leaf = "stop-ec2-instance"
	mode, domainSlug, got, args, err := classifyCLIArgs([]string{"compute", leaf})
	if err != nil || mode != cliModeLeaf || domainSlug != "compute" || got != leaf || args != nil {
		t.Errorf("a bare slug must deep-link: mode=%q domain=%q leaf=%q err=%v", mode, domainSlug, got, err)
	}
	mode, _, _, args, err = classifyCLIArgs([]string{"compute", leaf, "--confirm", "box", "box"})
	if err != nil || mode != cliModeRun || len(args) != 3 {
		t.Errorf("options and a word must be a run: mode=%q args=%v err=%v", mode, args, err)
	}
	if _, _, _, _, err := classifyCLIArgs([]string{"iam", leaf}); err == nil {
		t.Errorf("the leaf under the wrong domain must be a usage error")
	}
}

func TestRunCLILeaf_StopInstanceUsageErrorsExitTwoBeforeAnyAWSCall(t *testing.T) {
	const leaf = "stop-ec2-instance"
	for _, args := range [][]string{{"-json"}, {"a", "b"}, {"--confirm"}, {"-bogus", "box"}, {"--confirm", "other", "box"}} {
		var out, eout bytes.Buffer
		env := cliEnv{}
		if len(args) == 3 { // a mismatched --confirm needs the inventory to know the target
			env = computeEnv()
		}
		if code := runCLILeaf(context.Background(), &out, &eout, leaf, args, env); code != 2 || out.Len() != 0 || eout.Len() == 0 {
			t.Errorf("%v: code=%d out=%q err=%q", args, code, out.String(), eout.String())
		}
	}
	var out, eout bytes.Buffer
	if code := runCLILeaf(context.Background(), &out, &eout, leaf, []string{"no-such-box"}, computeEnv()); code != 2 || !strings.Contains(eout.String(), "no instance found") {
		t.Errorf("unknown instance: code=%d err=%q", code, eout.String())
	}
	out.Reset()
	eout.Reset()
	if code := runCLILeaf(context.Background(), &out, &eout, leaf, []string{"--help"}, cliEnv{}); code != 0 || !strings.Contains(out.String(), "--confirm") || eout.Len() != 0 {
		t.Errorf("--help: code=%d out=%q err=%q", code, out.String(), eout.String())
	}
}

// Restore SQL's --confirm is repeatable (DR-0187): a bucket value beside the
// instance's passes the early check, which cannot yet know which buckets are
// writable. A bucket value without the instance's is still exit 2 before any call.
func TestRunCLILeaf_RestoreSQLBucketConfirmPassesTheEarlyCheckButNeedsTheInstance(t *testing.T) {
	insts := []inventory.Instance{{InstanceID: "i-1", Name: "box", Region: "us-east-1"}}
	var out, eout bytes.Buffer
	code := runCLILeaf(context.Background(), &out, &eout, "restore-sql-backup-from-s3",
		[]string{"--confirm", "box", "--confirm", "caltechauthors", "box", "b", "s", "latest"}, cliEnv{instances: insts})
	if strings.Contains(eout.String(), "does not match") || code == 2 {
		t.Errorf("a bucket confirm beside the instance must pass the early check, got %d:\n%s", code, eout.String())
	}
	eout.Reset()
	code = runCLILeaf(context.Background(), &out, &eout, "restore-sql-backup-from-s3",
		[]string{"--confirm", "caltechauthors", "box", "b", "s", "latest"}, cliEnv{instances: insts})
	if code != 2 || !strings.Contains(eout.String(), "does not match") {
		t.Errorf("a bucket confirm alone must be exit 2, got %d:\n%s", code, eout.String())
	}
}

// The OpenSearch restore has no bucket guard: any --confirm value but its target is
// still a mismatch.
func TestRunCLILeaf_RestoreOpenSearchRefusesAnExtraConfirm(t *testing.T) {
	insts := []inventory.Instance{{InstanceID: "i-1", Name: "box", Region: "us-east-1"}}
	var eout bytes.Buffer
	code := runCLILeaf(context.Background(), &bytes.Buffer{}, &eout, "restore-opensearch-snapshot-from-s3",
		[]string{"--confirm", "box", "--confirm", "some-bucket", "box", "b", "s", "latest", "x", "y"}, cliEnv{instances: insts})
	if code != 2 {
		t.Errorf("want exit 2, got %d:\n%s", code, eout.String())
	}
}

// create-instance-profile and remove-role-from-instance-profile (DR-0188). Usage
// errors, --help and a --confirm mismatch never reach AWS (a nil iamClient would panic).
func TestClassifyCLIArgs_InstanceProfileLeaves(t *testing.T) {
	for _, leaf := range []string{"create-instance-profile", "remove-role-from-instance-profile"} {
		mode, _, got, _, err := classifyCLIArgs([]string{"iam", leaf})
		if err != nil || mode != cliModeLeaf || got != leaf {
			t.Errorf("%s: a bare slug must deep-link: mode=%q leaf=%q err=%v", leaf, mode, got, err)
		}
		mode, _, _, args, err := classifyCLIArgs([]string{"iam", leaf, "a", "b"})
		if err != nil || mode != cliModeRun || len(args) != 2 {
			t.Errorf("%s: two words must be a run: mode=%q args=%v err=%v", leaf, mode, args, err)
		}
	}
}

func TestRunCLILeaf_InstanceProfileLeavesRefuseBeforeAnyAWSCall(t *testing.T) {
	for _, tc := range []struct {
		leaf, usage string
		args        []string
	}{
		{"create-instance-profile", "usage: clasm iam create-instance-profile", []string{}},
		{"create-instance-profile", "usage: clasm iam create-instance-profile", []string{"p"}},
		{"create-instance-profile", "usage: clasm iam create-instance-profile", []string{"-jsonl", "p", "r"}},
		{"remove-role-from-instance-profile", "usage: clasm iam remove-role-from-instance-profile", []string{"r"}},
		{"remove-role-from-instance-profile", "does not match the target IAM instance profile", []string{"--confirm", "r", "r", "p"}},
	} {
		var out, eout bytes.Buffer
		if code := runCLILeaf(context.Background(), &out, &eout, tc.leaf, tc.args, cliEnv{}); code != 2 || out.Len() != 0 || !strings.Contains(eout.String(), tc.usage) {
			t.Errorf("%s %v: code=%d out=%q err=%q", tc.leaf, tc.args, code, out.String(), eout.String())
		}
	}
	for leaf, want := range map[string]string{"create-instance-profile": "DLD-owned", "remove-role-from-instance-profile": "--confirm <profile-name>"} {
		var out, eout bytes.Buffer
		if code := runCLILeaf(context.Background(), &out, &eout, leaf, []string{"--help"}, cliEnv{}); code != 0 || !strings.Contains(out.String(), want) || eout.Len() != 0 {
			t.Errorf("%s --help: code=%d out=%q err=%q", leaf, code, out.String(), eout.String())
		}
	}
}

// EBS volumes, snapshots and network interfaces are Tag Management kinds (DR-0192):
// show-all-tags accepts their slugs, lists them fresh from the regions' clients, and its
// usage names them.
type volumeOnlyEC2 struct {
	awsclient.EC2API
}

func (volumeOnlyEC2) DescribeVolumes(_ context.Context, _ *ec2.DescribeVolumesInput, _ ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	return &ec2.DescribeVolumesOutput{Volumes: []ec2types.Volume{{
		VolumeId: aws.String("vol-0abc"), Size: aws.Int32(250),
		Tags: []ec2types.Tag{{Key: aws.String("origin"), Value: aws.String("dld")}},
	}}}, nil
}

func TestRunCLILeaf_ShowAllTagsListsEBSVolumes(t *testing.T) {
	env := cliEnv{ec2Clients: map[string]awsclient.EC2API{"us-west-2": volumeOnlyEC2{}}}
	var out, eout bytes.Buffer
	if code := runCLILeaf(context.Background(), &out, &eout, "show-all-tags", []string{"-json", "ebs-volume"}, env); code != 0 || !strings.Contains(out.String(), "vol-0abc") || !strings.Contains(out.String(), "dld") {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), eout.String())
	}
	out.Reset()
	eout.Reset()
	if code := runCLILeaf(context.Background(), &out, &eout, "show-all-tags", []string{"no-such-kind"}, env); code != 2 ||
		!strings.Contains(eout.String(), "ebs-volume") || !strings.Contains(eout.String(), "ebs-snapshot") || !strings.Contains(eout.String(), "network-interface") {
		t.Errorf("an unknown kind must list all eleven, code=%d err=%q", code, eout.String())
	}
}

// show-untagged (DR-0192) is a Tag Management leaf: a bare slug deep-links into its
// interactive view, kind words after it are a run, and an unknown kind is exit 2 before
// any AWS call.
func TestClassifyCLIArgs_ShowUntagged(t *testing.T) {
	mode, domainSlug, leafSlug, _, err := classifyCLIArgs([]string{"tag-management", "show-untagged"})
	if err != nil || mode != cliModeLeaf || domainSlug != "tag-management" || leafSlug != "show-untagged" {
		t.Errorf("a bare slug must deep-link: mode=%q domain=%q leaf=%q err=%v", mode, domainSlug, leafSlug, err)
	}
	mode, _, _, args, err := classifyCLIArgs([]string{"tag-management", "show-untagged", "instance", "ami"})
	if err != nil || mode != cliModeRun || len(args) != 2 {
		t.Errorf("kind words must be a run: mode=%q args=%v err=%v", mode, args, err)
	}
	if _, _, _, _, err := classifyCLIArgs([]string{"iam", "show-untagged"}); err == nil {
		t.Errorf("the leaf under the wrong domain must be a usage error")
	}
}

func TestRunCLILeaf_ShowUntagged(t *testing.T) {
	env := cliEnv{
		originTag: config.OriginTagConfig{Key: "origin", DLDValue: "dld"},
		instances: []inventory.Instance{
			{InstanceID: "i-bare", Name: "new-data", Region: "us-west-2", Tags: map[string]string{"Name": "new-data"}},
			{InstanceID: "i-ok", Name: "tagged", Region: "us-west-2", Tags: map[string]string{"origin": "dld"}},
		},
	}
	var out, eout bytes.Buffer
	if code := runCLILeaf(context.Background(), &out, &eout, "show-untagged", []string{"-jsonl", "instance"}, env); code != 0 ||
		!strings.Contains(out.String(), "i-bare") || strings.Contains(out.String(), "i-ok") || eout.Len() != 0 {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), eout.String())
	}
	out.Reset()
	if code := runCLILeaf(context.Background(), &out, &eout, "show-untagged", []string{"-json", "no-such-kind"}, env); code != 2 || out.Len() != 0 ||
		!strings.Contains(eout.String(), "no-such-kind") || !strings.Contains(eout.String(), "ebs-volume") {
		t.Errorf("an unknown kind is exit 2 and lists the kinds: code=%d out=%q err=%q", code, out.String(), eout.String())
	}
	out.Reset()
	eout.Reset()
	if code := runCLILeaf(context.Background(), &out, &eout, "show-untagged", []string{"--help"}, env); code != 0 || !strings.Contains(out.String(), "-jsonl") || !strings.Contains(out.String(), "instance") {
		t.Errorf("--help: code=%d out=%q err=%q", code, out.String(), eout.String())
	}
}

func TestRunCLILeaf_ShowUntaggedTakesSeveralKinds(t *testing.T) {
	env := cliEnv{
		originTag: config.OriginTagConfig{Key: "origin", DLDValue: "dld"},
		instances: []inventory.Instance{{InstanceID: "i-bare", Name: "new-data"}},
		images:    []inventory.Image{{ImageID: "ami-bare", Name: "old"}},
	}
	var out, eout bytes.Buffer
	if code := runCLILeaf(context.Background(), &out, &eout, "show-untagged", []string{"-jsonl", "instance", "ami"}, env); code != 0 ||
		!strings.Contains(out.String(), "i-bare") || !strings.Contains(out.String(), "ami-bare") {
		t.Errorf("code=%d out=%q err=%q", code, out.String(), eout.String())
	}
}

// tag-resource (DR-0192) is the command-line form of "Manage tags". Usage errors and --help
// never reach AWS (nil clients would panic); a run tags through the real dispatch.
type taggingEC2 struct {
	awsclient.EC2API
	tags    []ec2types.Tag
	created []*ec2.CreateTagsInput
}

func (f *taggingEC2) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return &ec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{{InstanceId: aws.String("i-bare"), Tags: f.tags}}}}}, nil
}

func (f *taggingEC2) CreateTags(_ context.Context, p *ec2.CreateTagsInput, _ ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	f.created = append(f.created, p)
	return &ec2.CreateTagsOutput{}, nil
}

func TestClassifyCLIArgs_TagResource(t *testing.T) {
	mode, domainSlug, leafSlug, _, err := classifyCLIArgs([]string{"tag-management", "tag-resource"})
	if err != nil || mode != cliModeLeaf || domainSlug != "tag-management" || leafSlug != "tag-resource" {
		t.Errorf("a bare slug must deep-link into Manage tags: mode=%q domain=%q leaf=%q err=%v", mode, domainSlug, leafSlug, err)
	}
	mode, _, _, args, err := classifyCLIArgs([]string{"tag-management", "tag-resource", "instance", "new-data", "--tag", "origin=dld"})
	if err != nil || mode != cliModeRun || len(args) != 4 {
		t.Errorf("words and options must be a run: mode=%q args=%v err=%v", mode, args, err)
	}
	if _, _, _, _, err := classifyCLIArgs([]string{"iam", "tag-resource"}); err == nil {
		t.Errorf("the leaf under the wrong domain must be a usage error")
	}
}

func TestRunCLILeaf_TagResourceRefusesBadArgumentsBeforeAnyAWSCall(t *testing.T) {
	for _, args := range [][]string{
		{}, {"instance", "new-data"}, {"--tag", "origin", "instance", "n"}, {"--tag", "origin=dld", "no-such-kind", "n"},
		{"--tag", "aws:x=y", "instance", "n"}, {"--tag", "Environment=prod", "instance", "n"},
	} {
		var out, eout bytes.Buffer
		if code := runCLILeaf(context.Background(), &out, &eout, "tag-resource", args, cliEnv{}); code != 2 || out.Len() != 0 || !strings.Contains(eout.String(), "usage: clasm tag-management tag-resource") {
			t.Errorf("%v: code=%d out=%q err=%q", args, code, out.String(), eout.String())
		}
	}
	var out, eout bytes.Buffer
	if code := runCLILeaf(context.Background(), &out, &eout, "tag-resource", []string{"--help"}, cliEnv{}); code != 0 || !strings.Contains(out.String(), "--replace") || eout.Len() != 0 {
		t.Errorf("--help: code=%d out=%q err=%q", code, out.String(), eout.String())
	}
}

func TestRunCLILeaf_TagResourceTagsAndRefusesThroughTheDispatch(t *testing.T) {
	fake := &taggingEC2{tags: []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String("new-data")}}}
	env := cliEnv{
		ec2Clients: map[string]awsclient.EC2API{"us-west-2": fake},
		instances:  []inventory.Instance{{InstanceID: "i-bare", Name: "new-data", Region: "us-west-2"}},
	}
	var out, eout bytes.Buffer
	if code := runCLILeaf(context.Background(), &out, &eout, "tag-resource", []string{"instance", "new-data", "--tag", "origin=dld"}, env); code != 0 ||
		len(fake.created) != 1 || !strings.Contains(out.String(), "added origin=dld") {
		t.Fatalf("code=%d out=%q err=%q created=%v", code, out.String(), eout.String(), fake.created)
	}
	fake.tags = append(fake.tags, ec2types.Tag{Key: aws.String("origin"), Value: aws.String("imss")})
	fake.created = nil
	out.Reset()
	eout.Reset()
	if code := runCLILeaf(context.Background(), &out, &eout, "tag-resource", []string{"--tag", "origin=dld", "instance", "new-data"}, env); code != 1 ||
		len(fake.created) != 0 || !strings.Contains(eout.String(), "--replace") {
		t.Errorf("a different value is exit 1 and changes nothing: code=%d err=%q created=%v", code, eout.String(), fake.created)
	}
	out.Reset()
	eout.Reset()
	if code := runCLILeaf(context.Background(), &out, &eout, "tag-resource", []string{"instance", "ghost", "--tag", "origin=dld"}, env); code != 2 {
		t.Errorf("an unknown instance is exit 2: code=%d err=%q", code, eout.String())
	}
}

// RDM Index Health (DR-0197) is a read-only leaf: the dispatch must route it, and
// a usage error or a help request never reaches AWS.
func TestClassifyCLIArgs_RDMIndexHealthLeaf(t *testing.T) {
	const leaf = "rdm-index-health"
	mode, _, leafSlug, _, err := classifyCLIArgs([]string{"rdm-backup-and-restore", leaf})
	if err != nil || mode != cliModeLeaf || leafSlug != leaf {
		t.Errorf("no further words must deep-link into the leaf's picker: got mode=%q leaf=%q err=%v", mode, leafSlug, err)
	}
	mode, _, _, leafArgs, err := classifyCLIArgs([]string{"rdm-backup-and-restore", leaf, "-json", "box"})
	if err != nil || mode != cliModeRun || len(leafArgs) != 2 {
		t.Errorf("options and an instance must be a run: got mode=%q args=%v err=%v", mode, leafArgs, err)
	}
}

func TestRunCLILeaf_RDMIndexHealthUsageErrorsExitTwoWithoutTouchingAWS(t *testing.T) {
	for name, args := range map[string][]string{
		"no instance":    {"-json"},
		"surplus word":   {"box", "other"},
		"bad flag":       {"-bogus", "box"},
		"unknown box":    {"no-such-box"},
		"two formats":    {"-text", "-json", "box"},
		"a bad duration": {"-wait", "soon", "box"},
	} {
		var out, eout bytes.Buffer
		code := runCLILeaf(context.Background(), &out, &eout, "rdm-index-health", args, cliEnv{})
		if code != 2 {
			t.Errorf("%s: exit code = %d, want 2 (stderr %q)", name, code, eout.String())
		}
		if out.Len() != 0 {
			t.Errorf("%s: a usage error writes nothing to stdout, got %q", name, out.String())
		}
		// The unhandled-slug fallback also exits 2; this must be the form's own error.
		named := "rdm-index-health"
		if name == "unknown box" {
			named = "no-such-box" // the instance lookup's own message names the box
		}
		if strings.Contains(eout.String(), "internal error") || !strings.Contains(eout.String(), named) {
			t.Errorf("%s: the leaf was not routed to its own parser; stderr %q", name, eout.String())
		}
	}
}

func TestRunCLILeaf_RDMIndexHealthHelpPrintsTheUsageAndExitsZero(t *testing.T) {
	var out, eout bytes.Buffer
	code := runCLILeaf(context.Background(), &out, &eout, "rdm-index-health", []string{"--help"}, cliEnv{})
	if code != 0 || eout.Len() != 0 || !strings.Contains(out.String(), "rdm-index-health") || !strings.Contains(out.String(), "Exit status") {
		t.Errorf("want exit 0, usage and the exit status on stdout, nothing on stderr; got %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), eout.String())
	}
}

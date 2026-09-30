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
	code := runCLILeaf(context.Background(), &bytes.Buffer{}, &eout, "no-such-leaf", nil, map[string]awsclient.SSMAPI{}, nil, nil, nil, nil, nil)
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
	for _, args := range [][]string{{"compute", "show-instances"}, {"iam", "show-roles", "extra"}} {
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
	code := runCLILeaf(context.Background(), &out, &eout, "restore-opensearch-snapshot-from-s3", []string{"only-one-arg"}, nil, nil, nil, nil, nil, nil)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(eout.String(), "want 6 arguments") || !strings.Contains(eout.String(), "--confirm") {
		t.Errorf("expected the arity error with the usage on stderr, got:\n%s", eout.String())
	}
}

func TestRunCLILeaf_RestoreOpenSearchHelpPrintsTheUsageAndExitsZero(t *testing.T) {
	var out, eout bytes.Buffer
	code := runCLILeaf(context.Background(), &out, &eout, "restore-opensearch-snapshot-from-s3", []string{"--help"}, nil, nil, nil, nil, nil, nil)
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
		[]string{"no-such-box", "/d", "b", "s", "latest", "p"}, nil, nil, nil, nil, nil, nil)
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
	code := runCLILeaf(context.Background(), &out, &eout, "restore-sql-backup-from-s3", []string{"only-one-arg"}, nil, nil, nil, nil, nil, nil)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(eout.String(), "want 4 arguments") || !strings.Contains(eout.String(), "--confirm") {
		t.Errorf("expected the arity error with the usage on stderr, got:\n%s", eout.String())
	}
}

func TestRunCLILeaf_RestoreSQLHelpPrintsTheUsageAndExitsZero(t *testing.T) {
	var out, eout bytes.Buffer
	code := runCLILeaf(context.Background(), &out, &eout, "restore-sql-backup-from-s3", []string{"--help"}, nil, nil, nil, nil, nil, nil)
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
		[]string{"no-such-box", "b", "s", "latest"}, nil, nil, nil, nil, nil, nil)
	if code != 2 || !strings.Contains(eout.String(), "no-such-box") {
		t.Errorf("want exit 2 naming the instance, got %d:\n%s", code, eout.String())
	}
}

func TestRunCLILeaf_RestoreSQLConfirmTypoExitsTwoBeforeAnyAWSCall(t *testing.T) {
	var eout bytes.Buffer
	insts := []inventory.Instance{{InstanceID: "i-1", Name: "box", Region: "us-east-1"}}
	// nil clients: any AWS call would panic, so reaching preflight would fail this test.
	code := runCLILeaf(context.Background(), &bytes.Buffer{}, &eout, "restore-sql-backup-from-s3",
		[]string{"--confirm", "not-the-box", "box", "b", "s", "latest"}, nil, nil, nil, insts, nil, nil)
	if code != 2 || !strings.Contains(eout.String(), "does not match") {
		t.Errorf("want exit 2 with the mismatch, got %d:\n%s", code, eout.String())
	}
}

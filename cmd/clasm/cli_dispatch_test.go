package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/caltechlibrary/clasm/internal/awsclient"
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
	code := runCLILeaf(context.Background(), &bytes.Buffer{}, &eout, "no-such-leaf", nil, map[string]awsclient.SSMAPI{}, nil, nil, nil)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(eout.String(), "no-such-leaf") {
		t.Errorf("expected the error to name the unhandled slug, got:\n%s", eout.String())
	}
}

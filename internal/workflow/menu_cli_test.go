package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
)

// TestMainMenuItems_CLISlugs pins which Compute leaves have a cliSlug: the seven
// read-only "Show" leaves (DR-0177's mechanical rule: lowercase, hyphenate,
// drop the slash and apostrophes). Every lifecycle leaf stays "" -- unreachable
// from the CLI path -- until its own form is built.
func TestMainMenuItems_CLISlugs(t *testing.T) {
	want := map[string]string{
		"Show instances":                                "show-instances",
		"Show instance detail":                          "show-instance-detail",
		"Show AMIs":                                     "show-amis",
		"Show AMI detail":                               "show-ami-detail",
		"Show launch templates":                         "show-launch-templates",
		"Show launch template detail":                   "show-launch-template-detail",
		"Show/export cloud-init for an instance or AMI": "show-export-cloud-init-for-an-instance-or-ami",
	}
	seen := map[string]bool{}
	for _, item := range mainMenuItems {
		if item.cliSlug != want[item.label] {
			t.Errorf("mainMenuItems[%q].cliSlug = %q, want %q", item.label, item.cliSlug, want[item.label])
		}
		if item.cliSlug != "" {
			if seen[item.cliSlug] {
				t.Errorf("cliSlug %q is used twice", item.cliSlug)
			}
			seen[item.cliSlug] = true
		}
	}
	if len(seen) != len(want) {
		t.Errorf("found %d slugged Compute leaves, want %d", len(seen), len(want))
	}
}

// LeafCLISlugExists is the per-domain registry classifyCLIArgs asks: a slug is
// only valid under the domain that registered it.
func TestLeafCLISlugExists_IsPerDomain(t *testing.T) {
	for _, tc := range []struct {
		domain, leaf string
		want         bool
	}{
		{"compute", "show-instances", true},
		{"compute", "show-launch-template-detail", true},
		{"compute", "start-ec2-instance", false},
		{"compute", "archive-sql-backups-to-s3", false}, // an RDM slug, not Compute's
		{"rdm-backup-and-restore", "archive-sql-backups-to-s3", true},
		{"rdm-backup-and-restore", "show-instances", false}, // a Compute slug, not RDM's
		{"iam", "show-roles", false},                        // no leaf forms yet
		{"no-such-domain", "show-instances", false},
	} {
		if got := LeafCLISlugExists(tc.domain, tc.leaf); got != tc.want {
			t.Errorf("LeafCLISlugExists(%q, %q) = %t, want %t", tc.domain, tc.leaf, got, tc.want)
		}
	}
}

func TestDomainHasLeafCLIForms(t *testing.T) {
	for domain, want := range map[string]bool{
		"compute": true, "rdm-backup-and-restore": true,
		"iam": false, "s3": false, "key-management": false, "tag-management": false, "configuration": false,
	} {
		if got := DomainHasLeafCLIForms(domain); got != want {
			t.Errorf("DomainHasLeafCLIForms(%q) = %t, want %t", domain, got, want)
		}
	}
}

func TestRunMainMenuFromSlug_UnknownSlugIsNotFound(t *testing.T) {
	term, buf := newTermOnly()
	var refreshCalls int
	ok, err := runMainMenuFromSlug(context.Background(), term, testMenuActions(&refreshCalls), "no-such-leaf", nil, nil)
	if ok || err != nil || buf.Len() != 0 {
		t.Errorf("got ok=%t err=%v output=%q, want nothing run", ok, err, buf.String())
	}
}

// The deep-link runs the leaf's own action without consuming menu input, then
// behaves as if the operator were at the Compute menu.
func TestRunMainMenuFromSlug_RunsThatLeafThenContinuesTheNormalMenu(t *testing.T) {
	var refreshCalls, showInstancesCalls, showAMIsCalls int
	ctx, cancel := context.WithCancel(context.Background())
	term, buf := newTermOnly()

	actions := testMenuActions(&refreshCalls)
	actions.ShowInstances = countingAction(&showInstancesCalls)
	actions.ShowAMIs = cancelingAction(&showAMIsCalls, cancel)

	// One pause after the deep-linked leaf; "3" is Show AMIs in the menu; one
	// pause after that.
	ok, err := runMainMenuFromSlug(ctx, term, actions, "show-instances", newHuhAccessibleInput("\n3\n\n"), buf)
	if !ok || err != nil {
		t.Fatalf("got ok=%t err=%v, want ok=true err=nil", ok, err)
	}
	if showInstancesCalls != 1 || showAMIsCalls != 1 {
		t.Errorf("showInstances=%d showAMIs=%d, want 1 and 1", showInstancesCalls, showAMIsCalls)
	}
}

func TestRunMainMenuFromSlug_ExitSignalEndsTheRunWithoutTheMenu(t *testing.T) {
	term, buf := newTermOnly()
	var refreshCalls int
	actions := testMenuActions(&refreshCalls)
	actions.ShowInstances = failingAction(huh.ErrUserAborted)

	ok, err := runMainMenuFromSlug(context.Background(), term, actions, "show-instances", nil, nil)
	if !ok || err != nil {
		t.Fatalf("got ok=%t err=%v, want ok=true err=nil", ok, err)
	}
	if refreshCalls != 0 || !strings.Contains(buf.String(), "Exiting") {
		t.Errorf("an exit signal must not refresh or continue; refreshCalls=%d output=%q", refreshCalls, buf.String())
	}
}

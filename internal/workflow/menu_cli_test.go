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
		{"iam", "show-roles", true},
		{"iam", "delete-role", false}, // no form yet
		{"key-management", "show-key-pairs", true},
		{"key-management", "show-roles", false}, // IAM's slug, not Key Management's
		{"s3", "show-buckets", true},
		{"s3", "delete-bucket", false}, // no form yet
		{"tag-management", "show-all-tags", true},
		{"tag-management", "manage-tags", false},
		{"configuration", "edit-regions", false}, // no leaf forms yet
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
		"iam": true, "key-management": true,
		"s3": true, "tag-management": true, "configuration": false,
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

// Key Management's one read-only leaf and IAM's five get slugs; every mutating
// and destructive leaf stays "" until its own form is built.
func TestKeyMgmtAndIAMMenuItems_CLISlugs(t *testing.T) {
	wantKey := map[string]string{
		"Show Key Pairs": "show-key-pairs", "Create Key Pair": "", "Import Key Pair": "", "Delete Key Pair": "",
	}
	for _, item := range keyMgmtMenuItems {
		if want, ok := wantKey[item.label]; !ok || item.cliSlug != want {
			t.Errorf("keyMgmtMenuItems[%q].cliSlug = %q, want %q (known=%t)", item.label, item.cliSlug, want, ok)
		}
	}
	wantIAM := map[string]string{
		"Show Roles": "show-roles", "Show Instance Profiles": "show-instance-profiles", "Show Policies": "show-policies",
		"Show Role Detail": "show-role-detail", "Show Instance Profile Detail": "show-instance-profile-detail",
		"Create Role from Template": "", "Attach Policy to Role": "", "Detach Policy from Role": "",
		"Remove Role from Instance Profile": "", "Delete Instance Profile": "", "Delete Role": "",
	}
	for _, item := range iamMenuItems {
		if want, ok := wantIAM[item.label]; !ok || item.cliSlug != want {
			t.Errorf("iamMenuItems[%q].cliSlug = %q, want %q (known=%t)", item.label, item.cliSlug, want, ok)
		}
	}
}

func TestRunKeyMgmtMenuFromSlug(t *testing.T) {
	term, buf := newTermOnly()
	var refreshCalls int
	if ok, err := runKeyMgmtMenuFromSlug(context.Background(), term, testKeyMgmtActions(&refreshCalls), "no-such-leaf", nil, nil); ok || err != nil || buf.Len() != 0 {
		t.Errorf("unknown slug: ok=%t err=%v out=%q", ok, err, buf.String())
	}

	var showCalls, createCalls int
	ctx, cancel := context.WithCancel(context.Background())
	actions := testKeyMgmtActions(&refreshCalls)
	actions.ShowResourceLists = countingAction(&showCalls)
	actions.CreateKeyPair = cancelingAction(&createCalls, cancel)
	// A pause after the deep-linked leaf; "2" is Create Key Pair; a pause after it.
	ok, err := runKeyMgmtMenuFromSlug(ctx, term, actions, "show-key-pairs", newHuhAccessibleInput("\n2\n\n"), buf)
	if !ok || err != nil || showCalls != 1 || createCalls != 1 {
		t.Errorf("ok=%t err=%v show=%d create=%d, want the leaf once then the menu", ok, err, showCalls, createCalls)
	}
}

func TestRunIAMMenuFromSlug(t *testing.T) {
	term, buf := newTermOnly()
	if ok, err := runIAMMenuFromSlug(context.Background(), term, testIAMActions(), "no-such-leaf", nil, nil); ok || err != nil || buf.Len() != 0 {
		t.Errorf("unknown slug: ok=%t err=%v out=%q", ok, err, buf.String())
	}

	var rolesCalls, profilesCalls int
	ctx, cancel := context.WithCancel(context.Background())
	actions := testIAMActions()
	actions.ShowRoles = countingAction(&rolesCalls)
	actions.ShowInstanceProfiles = cancelingAction(&profilesCalls, cancel)
	ok, err := runIAMMenuFromSlug(ctx, term, actions, "show-roles", newHuhAccessibleInput("\n2\n\n"), buf)
	if !ok || err != nil || rolesCalls != 1 || profilesCalls != 1 {
		t.Errorf("ok=%t err=%v roles=%d profiles=%d, want the leaf once then the menu", ok, err, rolesCalls, profilesCalls)
	}

	// An exit signal from the deep-linked leaf ends the run without the menu.
	actions.ShowRoles = failingAction(huh.ErrUserAborted)
	term2, buf2 := newTermOnly()
	if ok, err := runIAMMenuFromSlug(context.Background(), term2, actions, "show-roles", nil, nil); !ok || err != nil || !strings.Contains(buf2.String(), "Exiting") {
		t.Errorf("exit signal: ok=%t err=%v out=%q", ok, err, buf2.String())
	}
}

func TestS3AndTagMgmtMenuItems_CLISlugs(t *testing.T) {
	wantS3 := map[string]string{
		"Show Buckets": "show-buckets", "Create Bucket": "", "Configure Static Website Hosting": "",
		"Browse & Manage Objects": "", "Manage Bucket Lifecycle Policies": "", "Delete Bucket": "",
	}
	for _, item := range s3MenuItems {
		if want, ok := wantS3[item.label]; !ok || item.cliSlug != want {
			t.Errorf("s3MenuItems[%q].cliSlug = %q, want %q (known=%t)", item.label, item.cliSlug, want, ok)
		}
	}
	wantTag := map[string]string{"Show all tags": "show-all-tags", "Manage tags": ""}
	for _, item := range tagMgmtMenuItems {
		if want, ok := wantTag[item.label]; !ok || item.cliSlug != want {
			t.Errorf("tagMgmtMenuItems[%q].cliSlug = %q, want %q (known=%t)", item.label, item.cliSlug, want, ok)
		}
	}
}

func TestRunS3MenuFromSlug(t *testing.T) {
	term, buf := newTermOnly()
	var refreshCalls int
	if ok, err := runS3MenuFromSlug(context.Background(), term, testS3Actions(&refreshCalls), "no-such-leaf", nil, nil); ok || err != nil || buf.Len() != 0 {
		t.Errorf("unknown slug: ok=%t err=%v out=%q", ok, err, buf.String())
	}
	var showCalls, createCalls int
	ctx, cancel := context.WithCancel(context.Background())
	actions := testS3Actions(&refreshCalls)
	actions.ShowResourceLists = countingAction(&showCalls)
	actions.CreateBucket = cancelingAction(&createCalls, cancel)
	ok, err := runS3MenuFromSlug(ctx, term, actions, "show-buckets", newHuhAccessibleInput("\n2\n\n"), buf)
	if !ok || err != nil || showCalls != 1 || createCalls != 1 {
		t.Errorf("ok=%t err=%v show=%d create=%d", ok, err, showCalls, createCalls)
	}
}

func TestRunTagMgmtMenuFromSlug(t *testing.T) {
	term, buf := newTermOnly()
	var refreshCalls int
	if ok, err := runTagMgmtMenuFromSlug(context.Background(), term, testTagMgmtActions(&refreshCalls), "no-such-leaf", nil, nil); ok || err != nil || buf.Len() != 0 {
		t.Errorf("unknown slug: ok=%t err=%v out=%q", ok, err, buf.String())
	}
	var showCalls, manageCalls int
	ctx, cancel := context.WithCancel(context.Background())
	actions := testTagMgmtActions(&refreshCalls)
	actions.ShowAllTags = countingAction(&showCalls)
	actions.ManageTags = cancelingAction(&manageCalls, cancel)
	ok, err := runTagMgmtMenuFromSlug(ctx, term, actions, "show-all-tags", newHuhAccessibleInput("\n2\n\n"), buf)
	if !ok || err != nil || showCalls != 1 || manageCalls != 1 {
		t.Errorf("ok=%t err=%v show=%d manage=%d", ok, err, showCalls, manageCalls)
	}
}

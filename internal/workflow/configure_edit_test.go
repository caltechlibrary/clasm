package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"

	"github.com/caltechlibrary/clasm/internal/config"
)

func TestEditRegions_AddsARegion(t *testing.T) {
	cfg := config.Config{Regions: []string{"us-west-1"}}
	_, input, buf := newPipeEditor("1\nus-east-1\n3\n") // Add a region, "us-east-1", Done

	changed, err := editRegions(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed = true")
	}
	if len(cfg.Regions) != 2 || cfg.Regions[1] != "us-east-1" {
		t.Errorf("Regions = %v, want [us-west-1 us-east-1]", cfg.Regions)
	}
	_ = buf
}

func TestEditRegions_RemovesARegion(t *testing.T) {
	cfg := config.Config{Regions: []string{"us-west-1", "us-west-2"}}
	_, input, buf := newPipeEditor("2\n2\n3\n") // Remove a region, pick "us-west-2", Done

	changed, err := editRegions(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed = true")
	}
	if len(cfg.Regions) != 1 || cfg.Regions[0] != "us-west-1" {
		t.Errorf("Regions = %v, want [us-west-1]", cfg.Regions)
	}
}

func TestEditRegions_RemoveWithNoRegionsMessageThenDone(t *testing.T) {
	cfg := config.Config{}
	_, input, buf := newPipeEditor("2\n3\n") // Remove a region (none), Done

	changed, err := editRegions(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected changed = false")
	}
	if !strings.Contains(buf.String(), "No regions to remove") {
		t.Errorf("expected a no-regions-to-remove message, got:\n%s", buf.String())
	}
}

func TestEditRegions_BlankRegionNotAdded(t *testing.T) {
	cfg := config.Config{}
	_, input, buf := newPipeEditor("1\n\n3\n") // Add a region, blank, Done

	changed, err := editRegions(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected changed = false")
	}
	if len(cfg.Regions) != 0 {
		t.Errorf("Regions = %v, want empty", cfg.Regions)
	}
}

func TestEditBackupDirectoryRules_AddsARule(t *testing.T) {
	cfg := config.Config{}
	_, input, buf := newPipeEditor("1\nrdm-*\n/opt/rdm_sql_backups\n3\n") // Add, pattern, directory, Done

	changed, err := editBackupDirectoryRules(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed = true")
	}
	if len(cfg.BackupDirectories) != 1 || cfg.BackupDirectories[0].Pattern != "rdm-*" || cfg.BackupDirectories[0].Directory != "/opt/rdm_sql_backups" {
		t.Errorf("BackupDirectories = %+v", cfg.BackupDirectories)
	}
}

func TestEditBackupDirectoryRules_RemovesARule(t *testing.T) {
	cfg := config.Config{BackupDirectories: []config.BackupDirectoryRule{
		{Pattern: "rdm-*", Directory: "/opt/rdm_sql_backups"},
		{Pattern: "newt-*", Directory: "/opt/newt/backups"},
	}}
	_, input, buf := newPipeEditor("2\n1\n3\n") // Remove, pick first rule, Done

	changed, err := editBackupDirectoryRules(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed = true")
	}
	if len(cfg.BackupDirectories) != 1 || cfg.BackupDirectories[0].Pattern != "newt-*" {
		t.Errorf("BackupDirectories = %+v, want only the newt-* rule left", cfg.BackupDirectories)
	}
}

func TestEditBackupDirectoryRules_BlankPatternNotAdded(t *testing.T) {
	cfg := config.Config{}
	_, input, buf := newPipeEditor("1\n\n3\n") // Add, blank pattern, Done

	changed, err := editBackupDirectoryRules(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected changed = false")
	}
	if len(cfg.BackupDirectories) != 0 {
		t.Errorf("BackupDirectories = %+v, want empty", cfg.BackupDirectories)
	}
}

func TestEditRDMPostgresRules_AddsARuleWithAllFields(t *testing.T) {
	cfg := config.Config{}
	_, input, buf := newPipeEditor("1\ncaltechauthors\ncaltechauthors-db-1\ncaltechauthors\ncaltechauthors\n3\n") // Add, pattern, container, db name, db user, Done

	changed, err := editRDMPostgresRules(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed = true")
	}
	want := config.RDMPostgresRule{Pattern: "caltechauthors", ContainerName: "caltechauthors-db-1", DBName: "caltechauthors", DBUser: "caltechauthors"}
	if len(cfg.RDMPostgresConfig) != 1 || cfg.RDMPostgresConfig[0] != want {
		t.Errorf("RDMPostgresConfig = %+v, want [%+v]", cfg.RDMPostgresConfig, want)
	}
}

func TestEditRDMPostgresRules_AddsARuleWithBlankOptionalFields(t *testing.T) {
	cfg := config.Config{}
	_, input, buf := newPipeEditor("1\ncaltechauthors\n\n\n\n3\n") // Add, pattern, blank container/db name/db user, Done

	changed, err := editRDMPostgresRules(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed = true")
	}
	want := config.RDMPostgresRule{Pattern: "caltechauthors"}
	if len(cfg.RDMPostgresConfig) != 1 || cfg.RDMPostgresConfig[0] != want {
		t.Errorf("RDMPostgresConfig = %+v, want [%+v] (blank container/db fields allowed)", cfg.RDMPostgresConfig, want)
	}
}

func TestEditRDMPostgresRules_RemovesARule(t *testing.T) {
	cfg := config.Config{RDMPostgresConfig: []config.RDMPostgresRule{
		{Pattern: "caltechauthors", ContainerName: "caltechauthors-db-1"},
		{Pattern: "caltechdata", ContainerName: "caltechdata-db-1"},
	}}
	_, input, buf := newPipeEditor("2\n1\n3\n") // Remove, pick first rule, Done

	changed, err := editRDMPostgresRules(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed = true")
	}
	if len(cfg.RDMPostgresConfig) != 1 || cfg.RDMPostgresConfig[0].Pattern != "caltechdata" {
		t.Errorf("RDMPostgresConfig = %+v, want only the caltechdata rule left", cfg.RDMPostgresConfig)
	}
}

func TestEditRDMPostgresRules_BlankPatternNotAdded(t *testing.T) {
	cfg := config.Config{}
	_, input, buf := newPipeEditor("1\n\n3\n") // Add, blank pattern, Done

	changed, err := editRDMPostgresRules(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected changed = false")
	}
	if len(cfg.RDMPostgresConfig) != 0 {
		t.Errorf("RDMPostgresConfig = %+v, want empty", cfg.RDMPostgresConfig)
	}
}

func TestRDMPostgresRuleLabel_BlankFieldsShowPlaceholders(t *testing.T) {
	got := rdmPostgresRuleLabel(config.RDMPostgresRule{Pattern: "caltechauthors"})
	if !strings.Contains(got, "caltechauthors") || !strings.Contains(got, "discover") {
		t.Errorf("got %q, want it to mention the pattern and that the container is auto-discovered", got)
	}
}

func TestEditOriginTag_UpdatesKeyAndValue(t *testing.T) {
	cfg := config.Config{OriginTag: config.OriginTagConfig{Key: "Origin", DLDValue: ""}}
	_, input, buf := newPipeEditor("Owner\nDLD\n")

	changed, err := editOriginTag(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed = true")
	}
	if cfg.OriginTag.Key != "Owner" || cfg.OriginTag.DLDValue != "DLD" {
		t.Errorf("OriginTag = %+v, want {Owner DLD}", cfg.OriginTag)
	}
}

func TestEditOriginTag_BlankKeepsDefaults(t *testing.T) {
	cfg := config.Config{OriginTag: config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"}}
	_, input, buf := newPipeEditor("\n\n") // accept both pre-filled defaults

	changed, err := editOriginTag(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected changed = false when both values are unchanged")
	}
	if cfg.OriginTag.Key != "Origin" || cfg.OriginTag.DLDValue != "DLD" {
		t.Errorf("OriginTag = %+v, want unchanged", cfg.OriginTag)
	}
}

func TestEditOriginTag_BlankKeyFallsBackToDefault(t *testing.T) {
	cfg := config.Config{OriginTag: config.OriginTagConfig{Key: "", DLDValue: ""}}
	_, input, buf := newPipeEditor("\n\n")

	_, err := editOriginTag(buf, &cfg, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OriginTag.Key != config.DefaultOriginTagKey {
		t.Errorf("OriginTag.Key = %q, want default %q", cfg.OriginTag.Key, config.DefaultOriginTagKey)
	}
}

func TestDisplayConfig_PrintsAllFields(t *testing.T) {
	cfg := config.Config{
		Regions:           []string{"us-west-1", "us-west-2"},
		BackupDirectories: []config.BackupDirectoryRule{{Pattern: "rdm-*", Directory: "/opt/rdm_sql_backups"}},
		OriginTag:         config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"},
	}
	_, _, buf := newPipeEditor("")
	displayConfig(buf, cfg)

	out := buf.String()
	for _, want := range []string{"us-west-1", "us-west-2", "rdm-*", "/opt/rdm_sql_backups", "Origin", "DLD"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDisplayConfig_UnsetOriginValueShowsPlaceholder(t *testing.T) {
	cfg := config.Config{OriginTag: config.OriginTagConfig{Key: "Origin", DLDValue: ""}}
	_, _, buf := newPipeEditor("")
	displayConfig(buf, cfg)

	if !strings.Contains(buf.String(), "none") {
		t.Errorf("expected a placeholder for an unset DLD value, got:\n%s", buf.String())
	}
}

func TestWarnIfDirtyOnQuit_WarnsWhenDirty(t *testing.T) {
	_, _, buf := newPipeEditor("")
	warnIfDirtyOnQuit(buf, true)
	if !strings.Contains(buf.String(), "discarded") {
		t.Errorf("expected an unsaved-changes warning, got:\n%s", buf.String())
	}
}

func TestWarnIfDirtyOnQuit_SilentWhenClean(t *testing.T) {
	_, _, buf := newPipeEditor("")
	warnIfDirtyOnQuit(buf, false)
	if buf.String() != "" {
		t.Errorf("expected no output when there are no unsaved changes, got:\n%s", buf.String())
	}
}

// extractionGroupsFake is a region holding one group that allows outbound HTTPS
// (sg-open) and one that does not (sg-closed).
func extractionGroupsFake() *fakeEC2Client {
	return &fakeEC2Client{securityGroups: []ec2types.SecurityGroup{
		{GroupId: aws.String("sg-open"), GroupName: aws.String("ssm-egress"), VpcId: aws.String("vpc-1"),
			IpPermissionsEgress: []ec2types.IpPermission{{IpProtocol: aws.String("-1")}}},
		{GroupId: aws.String("sg-closed"), GroupName: aws.String("no-egress"), VpcId: aws.String("vpc-1")},
	}}
}

func editExtractionGroups(t *testing.T, cfg *config.Config, clients map[string]awsclient.EC2API, script string) (bool, string) {
	t.Helper()
	_, input, buf := newPipeEditor(script)
	changed, err := editExtractionSecurityGroups(context.Background(), buf, cfg, clients, input, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return changed, buf.String()
}

func TestEditExtractionSecurityGroups_SetsAGroupForARegion(t *testing.T) {
	cfg := config.Config{Regions: []string{"us-west-1", "us-west-2"}}
	clients := map[string]awsclient.EC2API{"us-west-1": extractionGroupsFake(), "us-west-2": extractionGroupsFake()}
	// region 2 (us-west-2), group 1 (sg-open), then Done (the third region-menu entry)
	changed, out := editExtractionGroups(t, &cfg, clients, "2\n1\n3\n")
	if !changed {
		t.Error("expected changed = true")
	}
	if got := cfg.CloudInitExtractionSecurityGroups; len(got) != 1 || got["us-west-2"] != "sg-open" {
		t.Errorf("groups = %v, want only us-west-2 -> sg-open", got)
	}
	// Each group is marked, so the operator can see which would work.
	for _, want := range []string{"sg-open", "sg-closed", "outbound HTTPS: yes", "outbound HTTPS: NO"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing missing %q:\n%s", want, out)
		}
	}
}

func TestEditExtractionSecurityGroups_RefusesAGroupWithoutOutboundHTTPS(t *testing.T) {
	cfg := config.Config{Regions: []string{"us-west-2"}}
	clients := map[string]awsclient.EC2API{"us-west-2": extractionGroupsFake()}
	changed, out := editExtractionGroups(t, &cfg, clients, "1\n2\n2\n") // region 1, sg-closed, Done
	if changed || len(cfg.CloudInitExtractionSecurityGroups) != 0 {
		t.Errorf("a group with no outbound 443 must not be saved: changed=%v groups=%v", changed, cfg.CloudInitExtractionSecurityGroups)
	}
	if !strings.Contains(out, "no outbound rule allowing HTTPS") {
		t.Errorf("expected the reason, got:\n%s", out)
	}
}

func TestEditExtractionSecurityGroups_ClearsARegion(t *testing.T) {
	cfg := config.Config{Regions: []string{"us-west-2"}, CloudInitExtractionSecurityGroups: map[string]string{"us-west-2": "sg-open", "us-west-1": "sg-keep"}}
	clients := map[string]awsclient.EC2API{"us-west-2": extractionGroupsFake()}
	// region 1, then the entry after the two groups: clear; then Done
	changed, _ := editExtractionGroups(t, &cfg, clients, "1\n3\n2\n")
	if !changed {
		t.Error("expected changed = true")
	}
	if got := cfg.CloudInitExtractionSecurityGroups; len(got) != 1 || got["us-west-1"] != "sg-keep" {
		t.Errorf("groups = %v, want only the other region's entry kept", got)
	}
}

func TestEditExtractionSecurityGroups_BackLeavesItUnchanged(t *testing.T) {
	cfg := config.Config{Regions: []string{"us-west-2"}, CloudInitExtractionSecurityGroups: map[string]string{"us-west-2": "sg-open"}}
	clients := map[string]awsclient.EC2API{"us-west-2": extractionGroupsFake()}
	changed, _ := editExtractionGroups(t, &cfg, clients, "1\n4\n2\n") // region 1, Back, Done
	if changed || cfg.CloudInitExtractionSecurityGroups["us-west-2"] != "sg-open" {
		t.Errorf("Back must change nothing: changed=%v groups=%v", changed, cfg.CloudInitExtractionSecurityGroups)
	}
}

func TestEditExtractionSecurityGroups_ShowsTheCurrentValuePerRegion(t *testing.T) {
	cfg := config.Config{Regions: []string{"us-west-1", "us-west-2"}, CloudInitExtractionSecurityGroups: map[string]string{"us-west-2": "sg-open"}}
	clients := map[string]awsclient.EC2API{"us-west-1": extractionGroupsFake(), "us-west-2": extractionGroupsFake()}
	_, out := editExtractionGroups(t, &cfg, clients, "3\n")
	for _, want := range []string{"us-west-2: sg-open", "us-west-1: (none -- the VPC default)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestEditExtractionSecurityGroups_ALookupFailureIsReportedNotFatal(t *testing.T) {
	cfg := config.Config{Regions: []string{"us-west-2"}}
	broken := extractionGroupsFake()
	broken.describeSecurityGroupsErr = errors.New("UnauthorizedOperation")
	changed, out := editExtractionGroups(t, &cfg, map[string]awsclient.EC2API{"us-west-2": broken}, "1\n2\n") // region 1, Done
	if changed || !strings.Contains(out, "UnauthorizedOperation") {
		t.Errorf("want the error shown and nothing changed: changed=%v\n%s", changed, out)
	}
}

func TestEditExtractionSecurityGroups_NoUsableRegions(t *testing.T) {
	cfg := config.Config{Regions: []string{"eu-west-1"}}
	changed, out := editExtractionGroups(t, &cfg, map[string]awsclient.EC2API{"us-west-2": extractionGroupsFake()}, "")
	if changed || !strings.Contains(out, "No configured region") {
		t.Errorf("want a no-regions message: changed=%v\n%s", changed, out)
	}
}

func TestDisplayConfig_ShowsExtractionSecurityGroups(t *testing.T) {
	var buf strings.Builder
	displayConfig(&buf, config.Config{Regions: []string{"us-west-1", "us-west-2"}, CloudInitExtractionSecurityGroups: map[string]string{"us-west-2": "sg-open"}})
	for _, want := range []string{"us-west-2: sg-open", "us-west-1: (none"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("Show current config missing %q:\n%s", want, buf.String())
		}
	}
}

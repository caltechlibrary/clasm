package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".clasm")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ---- config_version (DR-0206) ----

func TestLoad_NoVersionLoadsAsVersion1(t *testing.T) {
	cfg, err := Load(writeConfig(t, "regions: [us-west-2]\n"))
	if err != nil || cfg.ConfigVersion != 1 {
		t.Errorf("version=%d err=%v", cfg.ConfigVersion, err)
	}
}

func TestLoad_AMissingFileIsTheCurrentVersion(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent"))
	if err != nil || cfg.ConfigVersion != CurrentVersion {
		t.Errorf("version=%d err=%v", cfg.ConfigVersion, err)
	}
}

func TestLoad_ANewerVersionIsRefusedAsAConfigErrorAndReadsNothing(t *testing.T) {
	p := writeConfig(t, "config_version: 99\nregions: [eu-west-1]\nfuture_field: x\n")
	cfg, err := Load(p)
	if !errors.Is(err, ErrNewerConfig) {
		t.Fatalf("err = %v, want ErrNewerConfig", err)
	}
	if !strings.Contains(err.Error(), "99") || !strings.Contains(err.Error(), "update") {
		t.Errorf("the message should name the version and say to update: %v", err)
	}
	if len(cfg.Regions) != 0 {
		t.Errorf("nothing may be read from a newer file: %+v", cfg)
	}
}

func TestLoad_TheCurrentVersionLoads(t *testing.T) {
	cfg, err := Load(writeConfig(t, "config_version: 2\nregions: [us-west-2]\n"))
	if err != nil || cfg.ConfigVersion != 2 {
		t.Errorf("version=%d err=%v", cfg.ConfigVersion, err)
	}
}

func TestSave_AlwaysWritesTheCurrentVersion(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".clasm")
	cfg := Config{Regions: []string{"us-west-2"}, ConfigVersion: 1}
	if err := Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "config_version: 2") {
		t.Errorf("file:\n%s", b)
	}
	if got, err := Load(p); err != nil || got.ConfigVersion != CurrentVersion {
		t.Errorf("reload: %d %v", got.ConfigVersion, err)
	}
}

// ---- host ----

const hostConfig = `config_version: 2
backup_directories:
  - pattern: "caltech*"
    directory: /aws/sql
  - pattern: "caltech*"
    host: multipass
    directory: /opt/rdm_sql_backups
opensearch_backup_directories:
  - pattern: "caltech*"
    host: multipass
    directory: /opt/rdm_opensearch_backups
rdm_postgres_config:
  - pattern: "caltech*"
    host: multipass
    container_name: vm-db
  - pattern: "caltech*"
    container_name: aws-db
`

func TestLoad_ParsesHostOnAllThreeRuleTypes(t *testing.T) {
	cfg, err := Load(writeConfig(t, hostConfig))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BackupDirectories[1].Host != HostMultipass || cfg.OpenSearchBackupDirectories[0].Host != HostMultipass || cfg.RDMPostgresConfig[0].Host != HostMultipass {
		t.Errorf("hosts not parsed: %+v", cfg)
	}
	if cfg.BackupDirectories[0].Host != "" {
		t.Errorf("a rule with no host stays blank on load: %q", cfg.BackupDirectories[0].Host)
	}
}

func TestLoad_AnUnknownHostIsAConfigError(t *testing.T) {
	_, err := Load(writeConfig(t, "config_version: 2\nbackup_directories:\n  - pattern: x\n    host: gcp\n    directory: /d\n"))
	if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "gcp") {
		t.Errorf("err = %v", err)
	}
}

func TestBackupDirectoryFor_ReachesOnlyAWSRules(t *testing.T) {
	cfg, _ := Load(writeConfig(t, hostConfig))
	if got := BackupDirectoryFor(cfg.BackupDirectories, "caltechauthors-x"); got != "/aws/sql" {
		t.Errorf("clasm resolver = %q", got)
	}
	multipassOnly := []BackupDirectoryRule{{Pattern: "caltech*", Host: HostMultipass, Directory: "/vm"}}
	if got := BackupDirectoryFor(multipassOnly, "caltechauthors-x"); got != "" {
		t.Errorf("a multipass rule must never reach clasm, got %q", got)
	}
}

func TestBackupDirectoryForHost_ReachesOnlyThatHostsRules(t *testing.T) {
	cfg, _ := Load(writeConfig(t, hostConfig))
	if got := BackupDirectoryForHost(cfg.BackupDirectories, HostMultipass, "caltechauthors-x"); got != "/opt/rdm_sql_backups" {
		t.Errorf("clmpm resolver = %q", got)
	}
	awsOnly := []BackupDirectoryRule{{Pattern: "caltech*", Directory: "/aws"}}
	if got := BackupDirectoryForHost(awsOnly, HostMultipass, "caltechauthors-x"); got != "" {
		t.Errorf("an aws rule must never reach clmpm, got %q", got)
	}
	if got := BackupDirectoryForHost(awsOnly, HostAWS, "caltechauthors-x"); got != "/aws" {
		t.Errorf("a blank host is aws: %q", got)
	}
}

func TestRDMPostgresConfigFor_KeepsTheHostsApart(t *testing.T) {
	cfg, _ := Load(writeConfig(t, hostConfig))
	if c, _, _ := RDMPostgresConfigFor(cfg.RDMPostgresConfig, "caltechauthors-x", "fb"); c != "aws-db" {
		t.Errorf("clasm got %q", c)
	}
	if c, db, _ := RDMPostgresConfigForHost(cfg.RDMPostgresConfig, HostMultipass, "caltechauthors-x", "fb"); c != "vm-db" || db != "fb" {
		t.Errorf("clmpm got %q %q", c, db)
	}
}

func TestUpsertRDMPostgresRule_NeverTouchesAnotherHostsRule(t *testing.T) {
	rules := []RDMPostgresRule{{Pattern: "vm1", Host: HostMultipass, ContainerName: "vm-db"}}
	got := UpsertRDMPostgresRule(rules, "vm1", "aws-db")
	if len(got) != 2 || got[0].ContainerName != "vm-db" || got[1].Host != "" || got[1].ContainerName != "aws-db" {
		t.Errorf("clasm's upsert must append its own aws rule: %+v", got)
	}
	got = UpsertRDMPostgresRuleForHost(rules, HostMultipass, "vm1", "new-db")
	if len(got) != 1 || got[0].ContainerName != "new-db" || got[0].Host != HostMultipass {
		t.Errorf("clmpm's upsert should update its own rule: %+v", got)
	}
}

func TestSave_RoundTripsHostAndOmitsABlankOne(t *testing.T) {
	cfg, _ := Load(writeConfig(t, hostConfig))
	p := filepath.Join(t.TempDir(), ".clasm")
	if err := Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil || got.BackupDirectories[1].Host != HostMultipass || got.RDMPostgresConfig[0].Host != HostMultipass || got.OpenSearchBackupDirectories[0].Host != HostMultipass {
		t.Errorf("round trip lost a host: %+v %v", got, err)
	}
	b, _ := os.ReadFile(p)
	if n := strings.Count(string(b), "host:"); n != 3 {
		t.Errorf("a blank host must be omitted; %d host lines in:\n%s", n, b)
	}
}

func TestExitCode_ConfigErrorsAre78AndOthersAre1(t *testing.T) {
	if ExitCode(ErrNewerConfig) != 78 || ExitCode(fmt.Errorf("x: %w", ErrInvalidConfig)) != 78 {
		t.Error("a newer or invalid config is exit 78 (DR-0014)")
	}
	if ExitCode(errors.New("reading: permission denied")) != 1 {
		t.Error("any other load failure keeps the existing exit 1")
	}
}

func TestUpsertRDMPostgresRuleForHost_AppendsANewMultipassRuleCarryingItsHost(t *testing.T) {
	got := UpsertRDMPostgresRuleForHost(nil, HostMultipass, "vm9", "db-9")
	if len(got) != 1 || got[0].Host != HostMultipass || got[0].Pattern != "vm9" || got[0].ContainerName != "db-9" {
		t.Errorf("got %+v", got)
	}
	if aws := UpsertRDMPostgresRuleForHost(nil, HostAWS, "i9", "db"); aws[0].Host != "" {
		t.Errorf("an aws rule stays unmarked: %+v", aws)
	}
}

// ---- written_by: informational, never read to decide anything ----

func withWriter(t *testing.T, w string) {
	t.Helper()
	old := Writer
	Writer = w
	t.Cleanup(func() { Writer = old })
}

func TestSave_RecordsWhichProgramWroteTheFile(t *testing.T) {
	withWriter(t, "clasm 0.0.13")
	p := filepath.Join(t.TempDir(), ".clasm")
	if err := Save(p, Config{Regions: []string{"us-west-2"}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "written_by: clasm 0.0.13") {
		t.Errorf("file:\n%s", b)
	}
	got, err := Load(p)
	if err != nil || got.WrittenBy != "clasm 0.0.13" {
		t.Errorf("load: %q %v", got.WrittenBy, err)
	}
}

func TestSave_ReplacesAnEarlierWriterAndOmitsAnUnsetOne(t *testing.T) {
	withWriter(t, "clmpm 0.0.14")
	p := filepath.Join(t.TempDir(), ".clasm")
	if err := Save(p, Config{WrittenBy: "clasm 0.0.12"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); strings.Contains(string(b), "clasm 0.0.12") || !strings.Contains(string(b), "clmpm 0.0.14") {
		t.Errorf("the writer must be the saving program:\n%s", b)
	}
	withWriter(t, "")
	if err := Save(p, Config{WrittenBy: "stale"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); strings.Contains(string(b), "written_by") {
		t.Errorf("an unset writer is omitted, and a stale one is not carried:\n%s", b)
	}
}

func TestLoad_WrittenByNeverAffectsLoading(t *testing.T) {
	cfg, err := Load(writeConfig(t, "config_version: 2\nwritten_by: some future program 9.9\nregions: [us-west-2]\n"))
	if err != nil || cfg.Regions[0] != "us-west-2" {
		t.Errorf("cfg=%+v err=%v", cfg, err)
	}
}

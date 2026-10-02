package workflow

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/ui"
)

func cliConfigFixture() config.Config {
	return config.Config{
		Regions:                           []string{"us-west-1", "us-west-2"},
		BackupDirectories:                 []config.BackupDirectoryRule{{Pattern: "rdm-*", Directory: "/opt/rdm_sql_backups"}},
		OpenSearchBackupDirectories:       []config.BackupDirectoryRule{{Pattern: "rdm-*", Directory: "/opt/os"}},
		RDMPostgresConfig:                 []config.RDMPostgresRule{{Pattern: "etd-*", ContainerName: "etd-db-1", DBName: "etd", DBUser: "etd"}},
		OriginTag:                         config.OriginTagConfig{Key: "Origin", DLDValue: "DLD"},
		CloudInitExtractionSecurityGroups: map[string]string{"us-west-2": "sg-open"},
	}
}

// The text form is the interactive "Show current config", byte for byte.
func TestRunShowConfigCLI_TextIsTheInteractiveDisplay(t *testing.T) {
	cfg := cliConfigFixture()
	var want, got bytes.Buffer
	displayConfig(&want, cfg)
	if err := RunShowConfigCLI(&got, cfg, ui.FormatText); err != nil {
		t.Fatal(err)
	}
	if got.String() != want.String() {
		t.Errorf("text form differs from displayConfig:\n%s\nwant:\n%s", got.String(), want.String())
	}
}

func TestRunShowConfigCLI_JSON(t *testing.T) {
	var out bytes.Buffer
	if err := RunShowConfigCLI(&out, cliConfigFixture(), ui.FormatJSON); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Regions           []string `json:"regions"`
		BackupDirectories []struct {
			Pattern   string `json:"pattern"`
			Directory string `json:"directory"`
		} `json:"backup_directories"`
		OpenSearchBackupDirectories []struct {
			Directory string `json:"directory"`
		} `json:"opensearch_backup_directories"`
		RDMPostgresConfig []struct {
			ContainerName string `json:"container_name"`
			DBName        string `json:"db_name"`
			DBUser        string `json:"db_user"`
		} `json:"rdm_postgres_config"`
		OriginTag struct {
			Key      string `json:"key"`
			DLDValue string `json:"dld_value"`
		} `json:"origin_tag"`
		Groups map[string]string `json:"cloud_init_extraction_security_groups"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if len(got.Regions) != 2 || got.Regions[1] != "us-west-2" ||
		len(got.BackupDirectories) != 1 || got.BackupDirectories[0].Directory != "/opt/rdm_sql_backups" ||
		len(got.OpenSearchBackupDirectories) != 1 || got.OpenSearchBackupDirectories[0].Directory != "/opt/os" ||
		len(got.RDMPostgresConfig) != 1 || got.RDMPostgresConfig[0].ContainerName != "etd-db-1" ||
		got.OriginTag.DLDValue != "DLD" || got.Groups["us-west-2"] != "sg-open" {
		t.Errorf("JSON does not carry the config: %+v", got)
	}
}

// Lists are [] and maps {} when empty, never null, so a script can iterate them.
func TestRunShowConfigCLI_JSONEmptyCollectionsAreNotNull(t *testing.T) {
	var out bytes.Buffer
	if err := RunShowConfigCLI(&out, config.Config{}, ui.FormatJSON); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "null") {
		t.Errorf("empty collections must not be null:\n%s", out.String())
	}
}

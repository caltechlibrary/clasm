package workflow

import (
	"io"

	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// ConfigurationDomainCLISlug is the Configuration domain's cliSlug.
const ConfigurationDomainCLISlug = "configuration"

// ShowCurrentConfigCLISlug is "Show current config"'s cliSlug (DR-0177's
// mechanical rule). The editors and Save stay interactive-only: they are
// multi-step, and a scripted edit would race any other run reading ~/.clasm.
const ShowCurrentConfigCLISlug = "show-current-config"

// configJSON is the JSON form of the configuration. Every collection is
// non-nil, so an empty one is [] or {} and a script can iterate it.
type configJSON struct {
	Regions                           []string                     `json:"regions"`
	BackupDirectories                 []config.BackupDirectoryRule `json:"backup_directories"`
	OpenSearchBackupDirectories       []config.BackupDirectoryRule `json:"opensearch_backup_directories"`
	RDMPostgresConfig                 []config.RDMPostgresRule     `json:"rdm_postgres_config"`
	OriginTag                         config.OriginTagConfig       `json:"origin_tag"`
	CloudInitExtractionSecurityGroups map[string]string            `json:"cloud_init_extraction_security_groups"`
}

// RunShowConfigCLI is the non-interactive "Show current config": the text form
// is exactly the interactive display; the JSON form carries every setting,
// including the OpenSearch backup directories the text display omits. It reads
// the configuration already loaded and touches neither AWS nor the file.
func RunShowConfigCLI(w io.Writer, cfg config.Config, format ui.Format) error {
	if format == ui.FormatText {
		displayConfig(w, cfg)
		return nil
	}
	out := configJSON{
		Regions:                           nonNil(cfg.Regions),
		BackupDirectories:                 nonNil(cfg.BackupDirectories),
		OpenSearchBackupDirectories:       nonNil(cfg.OpenSearchBackupDirectories),
		RDMPostgresConfig:                 nonNil(cfg.RDMPostgresConfig),
		OriginTag:                         cfg.OriginTag,
		CloudInitExtractionSecurityGroups: cfg.CloudInitExtractionSecurityGroups,
	}
	if out.CloudInitExtractionSecurityGroups == nil {
		out.CloudInitExtractionSecurityGroups = map[string]string{}
	}
	return ui.WriteJSONValue(w, out, format)
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

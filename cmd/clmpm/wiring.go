package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/executor"
	"github.com/caltechlibrary/clasm/internal/mpreflight"
	"github.com/caltechlibrary/clasm/internal/rdmfetch"
	"github.com/caltechlibrary/clasm/internal/workflow"
)

// defaultCacheDir is where fetched backups are kept: ~/.cache/clasm/fetch.
func defaultCacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "clasm-fetch")
	}
	return filepath.Join(home, ".cache", "clasm", "fetch")
}

// realBackupDeps wires the backup and restore verbs to the real S3 client, the
// real pre-flights and the shared restore code in internal/workflow.
func realBackupDeps() backupDeps {
	cfgPath := config.DefaultPath()
	return backupDeps{
		cacheDir: defaultCacheDir(),
		newFetcher: func(ctx context.Context, region, cacheDir string) (backupFetcher, error) {
			client, err := awsclient.NewS3Client(ctx, region)
			if err != nil {
				return nil, err
			}
			return &rdmfetch.Fetcher{S3: client, CacheDir: cacheDir}, nil
		},
		pack:      rdmfetch.PackSnapshot,
		preflight: mpreflight.Run,
		discoverPostgres: func(ctx context.Context, ex executor.Executor) (string, error) {
			return workflow.DiscoverPostgresContainer(ctx, ex)
		},
		existingSQLData:   workflow.DetectExistingSQLData,
		planOpenSearch:    workflow.PlanOpenSearchRestore,
		restoreSQL:        workflow.RestoreSQLOnHost,
		restoreOpenSearch: workflow.RestoreOpenSearchOnHost,
		indexHealth:       workflow.CheckRDMIndexHealth,
		loadConfig:        func() (config.Config, error) { return config.Load(cfgPath) },
		saveConfig:        func(c config.Config) error { return config.Save(cfgPath, c) },
	}
}

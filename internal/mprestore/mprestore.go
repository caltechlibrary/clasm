// Package mprestore holds the Multipass acquirers for the restore workers
// (DR-0207): they send a backup that was fetched and verified on this machine
// to the VM with Executor.Put, in place of the on-host aws s3 commands.
package mprestore

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/caltechlibrary/clasm/internal/executor"
	"github.com/caltechlibrary/clasm/internal/rdmfetch"
	"github.com/caltechlibrary/clasm/internal/workflow"
)

// ScratchTarPath is where a packed snapshot lands on the VM before it is unpacked.
const ScratchTarPath = "/var/tmp/clasm-snapshot.tar"

// unpackTimeout bounds the unpack of a snapshot tar on the VM. The executor
// runs a step this long detached and polls it.
const unpackTimeout = 30 * time.Minute

// decompressTimeout bounds gunzip of a dump on the VM.
const decompressTimeout = 30 * time.Minute

// Snapshot is a packed snapshot repository ready to send.
type Snapshot struct {
	TarPath string // the local tar from rdmfetch.PackSnapshot
	Objects int    // the file count the tar must hold, from rdmfetch.SnapshotReport
}

// SQLAcquirer returns the acquirer for a verified local dump: the file is sent
// to the restore's download path, and a .gz is decompressed there by the same
// step the on-host download uses. It returns the path of the plain SQL.
//
// @param dump {rdmfetch.DumpReport} the dump fetched and verified on this machine
// @returns {workflow.SQLAcquirer} the acquirer to pass to workflow.RestoreSQLOnHost
// @example
//
//	err := workflow.RestoreSQLOnHost(ctx, os.Stdout, ex, params, mprestore.SQLAcquirer(report))
func SQLAcquirer(dump rdmfetch.DumpReport) workflow.SQLAcquirer {
	return func(ctx context.Context, ex executor.Executor) (string, error) {
		if _, err := os.Stat(dump.Path); err != nil {
			return "", fmt.Errorf("the local dump: %w", err)
		}
		if err := ex.Put(ctx, dump.Path, workflow.RestoreDownloadPath); err != nil {
			return "", fmt.Errorf("sending the dump to %s: %w", ex.Name(), err)
		}
		if !workflow.NeedsDecompression(dump.Key) {
			return workflow.RestoreDownloadPath, nil
		}
		stdout, status, err := ex.Run(ctx, workflow.DecompressRestoreDownloadCommand(), decompressTimeout, 10*time.Second)
		if err != nil {
			return "", fmt.Errorf("decompressing the dump on %s: %w", ex.Name(), err)
		}
		if status != executor.StatusSuccess {
			return "", fmt.Errorf("decompressing the dump on %s failed (%s): %s", ex.Name(), status, strings.TrimSpace(stdout))
		}
		return workflow.RestoreSQLPath, nil
	}
}

// SnapshotAcquirer returns the acquirer for a packed local snapshot: the tar is
// sent in one verified transfer and unpacked into the repository directory, and
// the number of files in it is checked against the number S3 listed.
//
// @param s {Snapshot} the packed tar and the file count it must hold
// @returns {workflow.SnapshotAcquirer} the acquirer to pass to workflow.RestoreOpenSearchOnHost
// @example
//
//	err := workflow.RestoreOpenSearchOnHost(ctx, os.Stdout, ex, params, mprestore.SnapshotAcquirer(mprestore.Snapshot{TarPath: tar, Objects: rep.Objects}))
func SnapshotAcquirer(s Snapshot) workflow.SnapshotAcquirer {
	return snapshotAcquirer(s, ScratchTarPath)
}

func snapshotAcquirer(s Snapshot, scratch string) workflow.SnapshotAcquirer {
	return func(ctx context.Context, ex executor.Executor, directory string) error {
		if _, err := os.Stat(s.TarPath); err != nil {
			return fmt.Errorf("the local snapshot tar: %w", err)
		}
		if err := ex.Put(ctx, s.TarPath, scratch); err != nil {
			return fmt.Errorf("sending the snapshot to %s: %w", ex.Name(), err)
		}
		stdout, status, err := ex.Run(ctx, unpackScript(scratch, directory), unpackTimeout, 10*time.Second)
		if err != nil {
			return fmt.Errorf("unpacking the snapshot on %s: %w", ex.Name(), err)
		}
		if status != executor.StatusSuccess {
			return fmt.Errorf("unpacking the snapshot on %s failed (%s): %s", ex.Name(), status, strings.TrimSpace(stdout))
		}
		n, ok := parseExtracted(stdout)
		if !ok {
			return fmt.Errorf("unpacking the snapshot on %s did not report a file count: %s", ex.Name(), strings.TrimSpace(stdout))
		}
		if n != s.Objects {
			return fmt.Errorf("the snapshot tar on %s held %d files, S3 listed %d", ex.Name(), n, s.Objects)
		}
		return nil
	}
}

// unpackScript unpacks tarPath into dir, counts the files the tar holds, and
// removes the tar whether or not it worked. Ownership is left to the restore's
// own ownership step, which runs next.
func unpackScript(tarPath, dir string) string {
	q := executor.ShellQuote
	return fmt.Sprintf("set -e\nT=%[1]s\ntrap 'rm -f \"$T\"' EXIT\nmkdir -p %[2]s\nn=$(tar -tf \"$T\" | grep -v '/$' | wc -l | tr -d ' ')\ntar -xf \"$T\" -C %[2]s --no-same-owner\necho \"clasm-extracted $n\"\n", q(tarPath), q(dir))
}

// parseExtracted reads the "clasm-extracted N" line the unpack script prints.
func parseExtracted(stdout string) (int, bool) {
	for _, line := range strings.Split(stdout, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "clasm-extracted "); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

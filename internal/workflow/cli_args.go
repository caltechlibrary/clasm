package workflow

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/caltechlibrary/clasm/internal/inventory"
)

// resolveInstanceArg resolves a CLI-supplied instance argument (PLAN.md
// Phase 20.64) against the fleet: exact Name match first, falling back
// to exact InstanceID. Zero or multiple matches is an error naming what
// was ambiguous -- a same-Name collision across regions is possible in
// this fleet (see CaltechAUTHORS's own Name-tag history) and must never
// be silently resolved to one of the matches.
func resolveInstanceArg(arg string, instances []inventory.Instance) (inventory.Instance, error) {
	var byName []inventory.Instance
	for _, inst := range instances {
		if inst.Name == arg {
			byName = append(byName, inst)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		// Fall through to an InstanceID match below.
	default:
		return inventory.Instance{}, fmt.Errorf("instance name %q is ambiguous: matches %s", arg, joinInstanceIDs(byName))
	}

	var byID []inventory.Instance
	for _, inst := range instances {
		if inst.InstanceID == arg {
			byID = append(byID, inst)
		}
	}
	switch len(byID) {
	case 1:
		return byID[0], nil
	case 0:
		return inventory.Instance{}, fmt.Errorf("no instance found with name or instance ID %q", arg)
	default:
		// EC2 instance IDs are globally unique -- unreachable in
		// practice, but fail loudly rather than silently pick one.
		return inventory.Instance{}, fmt.Errorf("instance ID %q matched more than one instance -- this should not happen", arg)
	}
}

func joinInstanceIDs(instances []inventory.Instance) string {
	ids := make([]string, len(instances))
	for i, inst := range instances {
		ids[i] = inst.InstanceID
	}
	return strings.Join(ids, ", ")
}

// parseTrimDaysValue parses promptLocalTrimDays' own blank/0/positive-
// integer answer -- shared between that interactive validator and
// ParseBackupArchiveArgs' non-interactive positional parsing (PLAN.md
// Phase 20.64), so the two paths can't drift apart.
func parseTrimDaysValue(raw string) (days int, requested bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false, nil
	}
	n, convErr := strconv.Atoi(raw)
	if convErr != nil || n < 0 {
		return 0, false, fmt.Errorf("must be blank (keep all local copies), 0, or a positive integer, got %q", raw)
	}
	return n, true, nil
}

// parseCleanupDaysValue parses promptOpenSearchCleanupDays' own blank/
// positive-integer answer -- shared the same way as parseTrimDaysValue.
// Unlike trim, "0" is not valid: there is no "clean up everything
// regardless of age" reading for S3-side snapshot cleanup.
func parseCleanupDaysValue(raw string) (days int, requested bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false, nil
	}
	n, convErr := strconv.Atoi(raw)
	if convErr != nil || n <= 0 {
		return 0, false, fmt.Errorf("must be blank (skip cleanup), or a positive integer, got %q", raw)
	}
	return n, true, nil
}

// ParseBackupArchiveArgs parses archive-sql-backups-to-s3's four
// positional CLI arguments -- instance, directory, bucket, trim -- into
// the resolved instance and a BackupArchiveParams (PLAN.md Phase 20.64).
// Arity is exact: anything but 4 args is a usage error naming the leaf
// and the expected argument list, never a partial prompt (design brief,
// decision 4).
func ParseBackupArchiveArgs(args []string, instances []inventory.Instance) (inventory.Instance, BackupArchiveParams, error) {
	const leaf = ArchiveSQLBackupsCLISlug
	const usage = "usage: " + leaf + ` <instance> <directory> <bucket> <trim-days-or-"">`
	if len(args) != 4 {
		return inventory.Instance{}, BackupArchiveParams{}, fmt.Errorf("%s: want 4 arguments, got %d\n%s", leaf, len(args), usage)
	}

	inst, err := resolveInstanceArg(args[0], instances)
	if err != nil {
		return inventory.Instance{}, BackupArchiveParams{}, fmt.Errorf("%s: %w", leaf, err)
	}

	directory := args[1]
	if directory == "" {
		return inventory.Instance{}, BackupArchiveParams{}, fmt.Errorf("%s: directory must not be empty\n%s", leaf, usage)
	}

	bucket := args[2]
	if bucket == "" {
		return inventory.Instance{}, BackupArchiveParams{}, fmt.Errorf("%s: bucket must not be empty\n%s", leaf, usage)
	}

	days, requested, err := parseTrimDaysValue(args[3])
	if err != nil {
		return inventory.Instance{}, BackupArchiveParams{}, fmt.Errorf("%s: trim: %w\n%s", leaf, err, usage)
	}

	params := BackupArchiveParams{InstanceID: inst.InstanceID, Directory: directory, Bucket: bucket, AgeDays: days, TrimRequested: requested}
	return inst, params, nil
}

// ParseOpenSearchArchiveArgs parses archive-opensearch-snapshot-to-s3's
// four positional CLI arguments -- instance, directory, bucket, cleanup
// -- the same shape as ParseBackupArchiveArgs, but the fourth argument
// reuses parseCleanupDaysValue's blank/positive-int-only rule rather
// than parseTrimDaysValue's.
func ParseOpenSearchArchiveArgs(args []string, instances []inventory.Instance) (inst inventory.Instance, directory, bucket string, cleanupDays int, cleanupRequested bool, err error) {
	const leaf = ArchiveOpenSearchSnapshotCLISlug
	const usage = "usage: " + leaf + ` <instance> <directory> <bucket> <cleanup-days-or-"">`
	if len(args) != 4 {
		return inventory.Instance{}, "", "", 0, false, fmt.Errorf("%s: want 4 arguments, got %d\n%s", leaf, len(args), usage)
	}

	inst, err = resolveInstanceArg(args[0], instances)
	if err != nil {
		return inventory.Instance{}, "", "", 0, false, fmt.Errorf("%s: %w", leaf, err)
	}

	directory = args[1]
	if directory == "" {
		return inventory.Instance{}, "", "", 0, false, fmt.Errorf("%s: directory must not be empty\n%s", leaf, usage)
	}

	bucket = args[2]
	if bucket == "" {
		return inventory.Instance{}, "", "", 0, false, fmt.Errorf("%s: bucket must not be empty\n%s", leaf, usage)
	}

	cleanupDays, cleanupRequested, err = parseCleanupDaysValue(args[3])
	if err != nil {
		return inventory.Instance{}, "", "", 0, false, fmt.Errorf("%s: cleanup: %w\n%s", leaf, err, usage)
	}

	return inst, directory, bucket, cleanupDays, cleanupRequested, nil
}

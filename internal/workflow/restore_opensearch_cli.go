package workflow

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// The non-interactive Restore OpenSearch Snapshot form (DR-0177, DR-0180), the
// first destructive leaf built on the shared gate (cli_gate.go):
//
//	restore-opensearch-snapshot-from-s3 [--confirm <instance-id-or-name>]
//	    <instance> <directory> <bucket> <source> <snapshot-name-or-latest> <index-prefix>
//
// The arguments are the interactive prompts in order, all explicit: the
// directory and the index prefix have interactive defaults, but a script should
// not restore from a default, and the index prefix in particular differs from
// the target's own tags whenever another instance's backup is restored onto it
// (the mistake that emptied caltechauthors-test-v13 on 2026-09-30).
//
// It is a dry run unless --confirm names the target (or a terminal answers the
// prompt): it makes only read-only calls -- the owner lookup, the index listing
// and the S3 listing -- prints a plan, and changes nothing.

// RestoreOpenSearchParams are the resolved arguments of the form.
type RestoreOpenSearchParams struct {
	Directory   string
	Bucket      string
	Source      string
	Snapshot    string // a snapshot name, or "latest"
	IndexPrefix string
}

const restoreOpenSearchUsage = "usage: " + RestoreOpenSearchSnapshotCLISlug +
	" [--confirm <instance-id-or-name>] <instance> <directory> <bucket> <source> <snapshot-name-or-latest> <index-prefix>"

// ParseOpenSearchRestoreArgs parses the form's options and six positional
// arguments. Every problem is a *UsageError (exit 2) raised before AWS is
// touched; --help is a *HelpRequested. The instance is matched by Name tag
// first, then by ID, exactly as the archive forms do.
func ParseOpenSearchRestoreArgs(args []string, instances []inventory.Instance) (inventory.Instance, RestoreOpenSearchParams, DestructiveOptions, error) {
	const leaf = RestoreOpenSearchSnapshotCLISlug
	opts, pos, err := ParseDestructiveArgs(leaf, restoreOpenSearchUsage, args)
	if err != nil {
		return inventory.Instance{}, RestoreOpenSearchParams{}, DestructiveOptions{}, err
	}
	if len(pos) != 6 {
		return inventory.Instance{}, RestoreOpenSearchParams{}, DestructiveOptions{},
			&UsageError{Msg: fmt.Sprintf("%s: want 6 arguments, got %d\n%s", leaf, len(pos), restoreOpenSearchUsage)}
	}
	inst, err := resolveInstanceArg(pos[0], instances)
	if err != nil {
		return inventory.Instance{}, RestoreOpenSearchParams{}, DestructiveOptions{}, &UsageError{Msg: fmt.Sprintf("%s: %v", leaf, err)}
	}
	p := RestoreOpenSearchParams{Directory: pos[1], Bucket: pos[2], Source: pos[3], Snapshot: pos[4], IndexPrefix: pos[5]}
	for _, f := range []struct{ name, value string }{
		{"directory", p.Directory}, {"bucket", p.Bucket}, {"source", p.Source},
		{"snapshot", p.Snapshot}, {"index-prefix", p.IndexPrefix},
	} {
		if f.value == "" {
			return inventory.Instance{}, RestoreOpenSearchParams{}, DestructiveOptions{},
				&UsageError{Msg: fmt.Sprintf("%s: %s must not be empty\n%s", leaf, f.name, restoreOpenSearchUsage)}
		}
	}
	return inst, p, opts, nil
}

// maxPlanIndexNames bounds how many existing index names the plan lists before
// summarising the rest; a real instance has about 60.
const maxPlanIndexNames = 5

// RunRestoreOpenSearchSnapshotAuto runs the non-interactive form. The caller has
// already done the preflight the interactive form does (the AWS CLI check and
// the bucket region and access), so bucketClient is scoped to the bucket.
//
// Order: the --confirm value is checked first, before any call; then the
// read-only work that builds the plan (the owner lookup and uid check, the
// existing indices, the S3 listing and the snapshot's size); then the gate
// decides. Only a confirmed run goes on to executeOpenSearchRestore, the same
// steps in the same order as the interactive form.
func RunRestoreOpenSearchSnapshotAuto(ctx context.Context, w io.Writer, ssmClient awsclient.SSMAPI, bucketClient awsclient.S3API, inst inventory.Instance, p RestoreOpenSearchParams, confirm string, interactive bool, input io.Reader, output io.Writer) error {
	gate := Gate{Target: inst, Confirm: confirm, Interactive: interactive}
	if err := gate.CheckConfirm(); err != nil {
		return err
	}

	owner, err := ResolveServiceOwner(ctx, ssmClient, inst.InstanceID, DefaultOwnershipTimeout, DefaultSSMPollInterval)
	if err != nil {
		return err
	}
	if err := CheckOwnerMatchesOpenSearch(owner); err != nil {
		return err
	}

	indices := rdmOpenSearchSnapshotIndexPatterns(p.IndexPrefix)
	existing, err := detectExistingOpenSearchIndices(ctx, ssmClient, inst.InstanceID, p.IndexPrefix, indices, DefaultOpenSearchRESTTimeout, DefaultSSMPollInterval)
	if err != nil {
		return err
	}

	prefixes, err := ListArchivedSnapshotPrefixes(ctx, bucketClient, p.Bucket, p.Source)
	if err != nil {
		return err
	}
	if len(prefixes) == 0 {
		return noSnapshotsError(ctx, bucketClient, p.Bucket, p.Source, inst.Name)
	}
	sort.Slice(prefixes, func(i, j int) bool { return prefixes[i].CreatedAt.After(prefixes[j].CreatedAt) })
	snap, err := chooseSnapshot(prefixes, p)
	if err != nil {
		return err
	}
	size, err := snapshotSize(ctx, bucketClient, p.Bucket, p.Source, snap.Name)
	if err != nil {
		return err
	}

	gate.Summary = fmt.Sprintf("restore snapshot %s from s3://%s/%s/ onto %s (%s): %s, restore index prefix %q",
		snap.Name, p.Bucket, openSearchSnapshotsPrefix(p.Source), inst.Name, inst.InstanceID, describeDeletion(existing), p.IndexPrefix)
	gate.Plan = []string{
		fmt.Sprintf("restore snapshot %s (%s, created %s) from s3://%s/%s/ onto %s (%s)",
			snap.Name, humanBytes(size), snap.CreatedAt.Format("2006-01-02 15:04:05"), p.Bucket, openSearchSnapshotsPrefix(p.Source), inst.Name, inst.InstanceID),
		describeDeletionDetail(existing, p.IndexPrefix),
		fmt.Sprintf("download the snapshot into %s on the instance, hand it to the service user, and register it as a repository", p.Directory),
		fmt.Sprintf("check the snapshot holds indices matching %q, delete the existing indices, then restore the %d index patterns", p.IndexPrefix, len(indices)),
		"delete the local copy of the snapshot and deregister the repository",
	}
	decision, err := gate.Decide(w, input, output)
	if err != nil {
		return err
	}
	if decision != GateProceed {
		return nil
	}

	return executeOpenSearchRestore(ctx, w, ssmClient, openSearchRestoreRun{
		inst: inst, owner: owner, directory: p.Directory, bucket: p.Bucket, sourceName: p.Source,
		indexPrefix: p.IndexPrefix, snap: snap, indices: indices, existing: existing,
	})
}

// chooseSnapshot resolves the snapshot argument against prefixes (newest first):
// "latest" is the newest, anything else must be an exact name. An unknown name is
// an action failure that lists what does exist, most recent first, since the
// likeliest cause is a mistyped or pasted name.
func chooseSnapshot(prefixes []SnapshotPrefixInfo, p RestoreOpenSearchParams) (SnapshotPrefixInfo, error) {
	if p.Snapshot == "latest" {
		return prefixes[0], nil
	}
	names := make([]string, 0, len(prefixes))
	for _, pf := range prefixes {
		if pf.Name == p.Snapshot {
			return pf, nil
		}
		names = append(names, pf.Name)
	}
	return SnapshotPrefixInfo{}, fmt.Errorf("snapshot %q not found under s3://%s/%s/; available, most recent first: %s",
		p.Snapshot, p.Bucket, openSearchSnapshotsPrefix(p.Source), summarizeNames(names, 10))
}

// snapshotSize sums the size of every object under one archived snapshot, from
// the S3 listing: a read-only figure for the plan that costs no instance time.
func snapshotSize(ctx context.Context, client awsclient.S3API, bucket, source, snapshot string) (int64, error) {
	prefix := openSearchSnapshotsPrefix(source) + "/" + snapshot + "/"
	var total int64
	var token *string
	for {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: aws.String(bucket), Prefix: aws.String(prefix), ContinuationToken: token,
		})
		if err != nil {
			return 0, err
		}
		for _, o := range out.Contents {
			total += aws.ToInt64(o.Size)
		}
		if !aws.ToBool(out.IsTruncated) {
			return total, nil
		}
		token = out.NextContinuationToken
	}
}

func describeDeletion(existing []string) string {
	switch len(existing) {
	case 0:
		return "delete no existing indices"
	case 1:
		return "delete 1 existing index"
	default:
		return fmt.Sprintf("delete %d existing indices", len(existing))
	}
}

// describeDeletionDetail is the plan's line about what the restore replaces:
// the count and the first few names, or that nothing matches.
func describeDeletionDetail(existing []string, prefix string) string {
	if len(existing) == 0 {
		return fmt.Sprintf("no existing indices match %s-* (nothing to delete)", prefix)
	}
	shown := existing
	if len(shown) > maxPlanIndexNames {
		shown = shown[:maxPlanIndexNames]
	}
	list := strings.Join(shown, ", ")
	if len(existing) > len(shown) {
		list += fmt.Sprintf(", and %d more", len(existing)-len(shown))
	}
	return fmt.Sprintf("%s: %s", describeDeletion(existing), list)
}

// humanBytes formats n with binary units, one decimal: 2048 -> "2.0 KiB".
func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	i := -1
	for value >= 1024 && i < len(units)-1 {
		value /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", value, units[i])
}

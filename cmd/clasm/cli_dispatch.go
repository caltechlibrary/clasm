package main

import (
	"context"
	"fmt"
	"io"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/workflow"
)

// CLI dispatch modes (PLAN.md Phase 20.64, design brief
// "cli_forms_for_tui_leaves.md"): classifyCLIArgs resolves flag.Args()
// into exactly one of these before any AWS client exists.
const (
	// cliModeNone: no args at all (plain `clasm`) -- today's behavior,
	// unchanged, falls through to the root TUI.
	cliModeNone = ""
	// cliModeDomain: a domain slug with no further args -- deep-links
	// into that domain's own interactive flow (decision 1).
	cliModeDomain = "domain"
	// cliModeLeaf: a domain and leaf slug with no further args --
	// deep-links into that leaf's own interactive flow, one level
	// deeper (decision 1, applied again).
	cliModeLeaf = "leaf"
	// cliModeRun: a domain and leaf slug with trailing args present --
	// always a non-interactive run attempt, whatever the trailing arg
	// count turns out to be. Exact arity is validated later by
	// ParseBackupArchiveArgs/ParseOpenSearchArchiveArgs, once AWS
	// clients exist to resolve the instance argument against -- a wrong
	// count is still a hard usage error (decision 4), just reported one
	// layer down rather than duplicating an arity table here that could
	// drift from the parsers' own.
	cliModeRun = "run"
)

// classifyCLIArgs resolves flag.Args() against the registered domain/
// leaf CLI slugs into one of the four cliMode outcomes, needing no AWS
// client at all -- so an unrecognized domain or leaf, or a domain with
// no CLI sub-commands yet, usage-errors out before any of the setup a
// real dispatch would need (fast, and free of any AWS side effect on a
// mistyped cron line).
func classifyCLIArgs(args []string) (mode, domainSlug, leafSlug string, leafArgs []string, err error) {
	if len(args) == 0 {
		return cliModeNone, "", "", nil, nil
	}

	domainSlug = args[0]
	if !workflow.DomainCLISlugExists(domainSlug) {
		return cliModeNone, "", "", nil, fmt.Errorf("unknown command %q", domainSlug)
	}
	if len(args) == 1 {
		return cliModeDomain, domainSlug, "", nil, nil
	}

	// Currently unreachable with today's registry: DomainCLISlugExists
	// above already rejects every domain except RDM, since none of them
	// has a cliSlug assigned yet. Kept as an explicit guard rather than
	// removed, so that the day another domain earns its own slug (before
	// it also gains its own leaf-slug dispatch below), a path under it
	// fails loudly instead of silently checking RDM's leaf registry for
	// a domain that isn't RDM.
	if domainSlug != workflow.RDMBackupRestoreDomainCLISlug {
		return cliModeNone, "", "", nil, fmt.Errorf("%q has no CLI sub-commands yet", domainSlug)
	}

	leafSlug = args[1]
	if !workflow.RDMLeafCLISlugExists(leafSlug) {
		return cliModeNone, "", "", nil, fmt.Errorf("unknown command %q under %q", leafSlug, domainSlug)
	}

	leafArgs = args[2:]
	if len(leafArgs) == 0 {
		return cliModeLeaf, domainSlug, leafSlug, nil, nil
	}
	return cliModeRun, domainSlug, leafSlug, leafArgs, nil
}

// runCLILeaf resolves leafSlug's positional leafArgs and runs that
// leaf's non-interactive form to completion (PLAN.md Phase 20.64,
// design brief decision 4 -- reached only once classifyCLIArgs has
// already produced cliModeRun for a registered leaf slug). The
// preflight checks the interactive path's own wrapper always runs
// (CheckAWSCLIAvailable, BucketRegion, CheckS3BucketAccess) run here
// too; only the confirmation prompt is skipped, per the design brief's
// "preflight checks always run" rule. Returns the process exit code to
// use: 0 on success, 1 on an AWS/workflow failure, 2 on a usage error
// (bad arguments -- never reached AWS at all).
func runCLILeaf(ctx context.Context, out, eout io.Writer, leafSlug string, leafArgs []string, ssmClients map[string]awsclient.SSMAPI, s3Client awsclient.S3API, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), instances []inventory.Instance) int {
	switch leafSlug {
	case workflow.ArchiveSQLBackupsCLISlug:
		inst, params, err := workflow.ParseBackupArchiveArgs(leafArgs, instances)
		if err != nil {
			fmt.Fprintf(eout, "%v\n", err)
			return 2
		}
		ssmClient, bucketClient, err := preflightForArchive(ctx, eout, ssmClients, s3Client, newS3Client, inst, params.Bucket)
		if err != nil {
			return 1
		}
		if err := workflow.RunBackupArchiveAndTrimAuto(ctx, out, ssmClient, bucketClient, inst, params); err != nil {
			fmt.Fprintf(eout, "%v\n", err)
			return 1
		}
		return 0

	case workflow.ArchiveOpenSearchSnapshotCLISlug:
		inst, directory, bucket, cleanupDays, cleanupRequested, err := workflow.ParseOpenSearchArchiveArgs(leafArgs, instances)
		if err != nil {
			fmt.Fprintf(eout, "%v\n", err)
			return 2
		}
		ssmClient, bucketClient, err := preflightForArchive(ctx, eout, ssmClients, s3Client, newS3Client, inst, bucket)
		if err != nil {
			return 1
		}
		prefix := workflow.InstanceUploadPrefix(inst)
		indexPrefix := workflow.OpenSearchIndexPrefix(inst)
		if err := workflow.RunArchiveOpenSearchSnapshotAuto(ctx, out, ssmClient, bucketClient, inst, directory, bucket, indexPrefix, prefix, cleanupDays, cleanupRequested); err != nil {
			fmt.Fprintf(eout, "%v\n", err)
			return 1
		}
		return 0

	default:
		// Unreachable: classifyCLIArgs already validated leafSlug
		// against the registered leaf slugs before mode became
		// cliModeRun.
		fmt.Fprintf(eout, "internal error: unhandled CLI leaf %q\n", leafSlug)
		return 2
	}
}

// preflightForArchive runs the archive leaves' shared preflight
// sequence -- resolve the instance's SSM client, CheckAWSCLIAvailable,
// resolve the bucket's own region, build a client scoped to it, and
// CheckS3BucketAccess -- identical to what backupArchiveAndTrim/
// archiveOpenSearchSnapshot's interactive wrappers already do before
// their first prompt. Any failure here is printed and reported via a
// non-nil error; the caller maps that to exit code 1.
func preflightForArchive(ctx context.Context, eout io.Writer, ssmClients map[string]awsclient.SSMAPI, s3Client awsclient.S3API, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), inst inventory.Instance, bucket string) (awsclient.SSMAPI, awsclient.S3API, error) {
	ssmClient, err := workflow.ResolveSSMClient(ssmClients, inst.Region)
	if err != nil {
		fmt.Fprintf(eout, "%v\n", err)
		return nil, nil, err
	}
	if err := workflow.CheckAWSCLIAvailable(ctx, ssmClient, inst.InstanceID, workflow.DefaultBackupListTimeout, workflow.DefaultSSMPollInterval); err != nil {
		fmt.Fprintf(eout, "%v\n", err)
		return nil, nil, err
	}
	bucketRegion, err := workflow.BucketRegion(ctx, s3Client, bucket)
	if err != nil {
		fmt.Fprintf(eout, "%v\n", err)
		return nil, nil, err
	}
	bucketClient, err := newS3Client(ctx, bucketRegion)
	if err != nil {
		fmt.Fprintf(eout, "%v\n", err)
		return nil, nil, err
	}
	if err := workflow.CheckS3BucketAccess(ctx, bucketClient, bucket); err != nil {
		fmt.Fprintf(eout, "%v\n", err)
		return nil, nil, err
	}
	return ssmClient, bucketClient, nil
}

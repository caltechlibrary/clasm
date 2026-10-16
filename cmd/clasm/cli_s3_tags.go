package main

import (
	"context"
	"io"
	"strings"

	"github.com/caltechlibrary/clasm/internal/ui"
	"github.com/caltechlibrary/clasm/internal/workflow"
)

const showBucketsUsage = "usage: clasm s3 show-buckets [-text|-json|-jsonl]"

// showAllTagsUsage names every kind, so a usage error and --help both list them.
var showUntaggedUsage = "usage: clasm tag-management show-untagged [-text|-json|-jsonl] [<kind>...]\n  kind: " + strings.Join(workflow.TagKindSlugs(), ", ") + " (default: all)"

var showAllTagsUsage = "usage: clasm tag-management show-all-tags [-text|-json|-jsonl] <kind>\n  kind: " + strings.Join(workflow.TagKindSlugs(), " | ")

// runS3TagLeaf runs the read-only S3 and Tag Management forms (DR-0177);
// handled is false for any other slug. Both read the lists main loaded for
// their own domain.
func runS3TagLeaf(ctx context.Context, out, eout io.Writer, leafSlug string, leafArgs []string, env cliEnv) (code int, handled bool) {
	switch leafSlug {
	case workflow.ShowBucketsCLISlug:
		return runListing(out, eout, leafSlug, showBucketsUsage, leafArgs, func(f ui.Format) error {
			return ui.WriteBuckets(out, env.buckets, f)
		}), true
	case workflow.TagResourceCLISlug:
		opts, err := workflow.ParseTagResourceArgs(leafArgs)
		if err != nil {
			return reportCLIError(out, eout, err), true
		}
		return reportCLIError(out, eout, workflow.RunTagResourceCLI(ctx, out, tagSourcesOf(env), opts)), true
	case workflow.ShowUntaggedCLISlug:
		opts, words, err := parseLeafWords(leafSlug, showUntaggedUsage, workflow.ComputeAllowJSONL, leafArgs, 0, len(workflow.TagKindSlugs()))
		if err != nil {
			return reportCLIError(out, eout, err), true
		}
		return reportCLIError(out, eout, workflow.RunShowUntaggedCLI(ctx, out, tagSourcesOf(env), words, opts.Format)), true
	case workflow.ShowAllTagsCLISlug:
		opts, words, err := parseLeafWords(leafSlug, showAllTagsUsage, workflow.ComputeAllowJSONL, leafArgs, 1, 1)
		if err != nil {
			return reportCLIError(out, eout, err), true
		}
		err = workflow.RunShowAllTagsCLI(ctx, out, tagSourcesOf(env), words[0], opts.Format)
		return reportCLIError(out, eout, err), true
	}
	return 0, false
}

// tagSourcesOf is everything the Tag Management listings read, from the CLI's environment.
func tagSourcesOf(env cliEnv) workflow.TagSources {
	return workflow.TagSources{
		EC2Clients: env.ec2Clients, NewS3Client: env.newS3Client, IAMClient: env.iamClient, OriginTag: env.originTag,
		Instances: env.instances, Images: env.images, LaunchTemplates: env.launchTemplates,
		KeyPairs: env.keyPairs, Buckets: env.buckets,
	}
}

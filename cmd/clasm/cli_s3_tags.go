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
	case workflow.ShowAllTagsCLISlug:
		opts, words, err := parseLeafWords(leafSlug, showAllTagsUsage, workflow.ComputeAllowJSONL, leafArgs, 1, 1)
		if err != nil {
			return reportCLIError(out, eout, err), true
		}
		err = workflow.RunShowAllTagsCLI(ctx, out, workflow.TagSources{
			NewS3Client: env.newS3Client, IAMClient: env.iamClient, OriginTag: env.originTag,
			Instances: env.instances, Images: env.images, LaunchTemplates: env.launchTemplates,
			KeyPairs: env.keyPairs, Buckets: env.buckets,
		}, words[0], opts.Format)
		return reportCLIError(out, eout, err), true
	}
	return 0, false
}

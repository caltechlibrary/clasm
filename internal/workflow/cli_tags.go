package workflow

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/caltechlibrary/clasm/internal/ui"
)

// tagKindSlugs maps each resource kind the Tag Management picker offers to its
// CLI kind argument (DR-0177's mechanical rule applied to the kind's label). A
// test requires every picker kind to have one.
var tagKindSlugs = map[string]string{
	"Instance":             "instance",
	"AMI":                  "ami",
	"Launch Template":      "launch-template",
	"Key Pair":             "key-pair",
	"S3 Bucket":            "s3-bucket",
	"IAM Role":             "iam-role",
	"IAM Instance Profile": "iam-instance-profile",
	"IAM Policy":           "iam-policy",
}

// TagKindSlugs lists the valid kind arguments in picker order, for usage text.
func TagKindSlugs() []string {
	out := make([]string, 0, len(tagManagementKinds))
	for _, kind := range tagManagementKinds {
		out = append(out, tagKindSlugs[kind])
	}
	return out
}

// RunShowAllTagsCLI is `clasm tag-management show-all-tags <kind>`: one kind's
// resources with their complete tag sets. As in the interactive view the kind is
// chosen first, because covering all eight at once would cost a GetBucketTagging
// call per bucket and the IAM tag calls every time. An unknown kind is a usage
// error listing the valid ones. Only reads are sent.
func RunShowAllTagsCLI(ctx context.Context, w io.Writer, src TagSources, kindSlug string, format ui.Format) error {
	var kind string
	for label, slug := range tagKindSlugs {
		if slug == kindSlug {
			kind = label
		}
	}
	if kind == "" {
		valid := TagKindSlugs()
		sort.Strings(valid)
		return &UsageError{Msg: fmt.Sprintf("unknown kind %q; the kinds are: %s", kindSlug, strings.Join(valid, ", "))}
	}
	_, rows, err := taggedResourcesForKind(ctx, kind, src)
	if err != nil {
		return err
	}
	return ui.WriteTaggedResources(w, kindSlug, rows, format)
}

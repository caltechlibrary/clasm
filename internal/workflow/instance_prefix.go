package workflow

import (
	"cmp"

	"github.com/caltechlibrary/clasm/internal/inventory"
)

// InstanceUploadPrefix is the S3 key-namespacing prefix Backup Archive &
// Trim and Archive OpenSearch Snapshot to S3 both use (DECISIONS.md,
// "Namespace backup uploads by instance"): the instance's Name tag, or
// its InstanceID when Name is blank -- an untagged instance still needs
// a non-empty, unique prefix. Shared here (PLAN.md Phase 20.64) rather
// than duplicated inline in each workflow, so the CLI dispatch path
// (cmd/clasm/main.go) computes exactly the same value the interactive
// path already does.
func InstanceUploadPrefix(inst inventory.Instance) string {
	if inst.Name != "" {
		return inst.Name
	}
	return inst.InstanceID
}

// OpenSearchIndexPrefix is the prefix Archive/Restore OpenSearch
// Snapshot match against real OpenSearch index names -- distinct from
// InstanceUploadPrefix's S3 key namespacing: an instance's Project tag
// when set, else the same Name-or-InstanceID fallback InstanceUploadPrefix
// uses. A real incident (2026-08-17, CaltechAUTHORS production) found
// inst.Name silently matching zero indices because this instance's real
// index prefix is its Project tag, not its (legacy) Name tag.
func OpenSearchIndexPrefix(inst inventory.Instance) string {
	return cmp.Or(inst.Project, InstanceUploadPrefix(inst))
}

---
id: "0098"
title: "Launch templates: build directly from cloud-init YAML, diff-then-new-version sync, fold in IMDSv2"
date: "2026-07-20"
status: accepted
kind: decision
trigger: ""
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "92cb1d98-3a20-445a-94e7-415771242718"
origin_host: "MACMINI-RD.local"
---

**Context.** Requested directly (`notes-from-tom.txt`) and confirmed as
v0.0.2's headline feature, 2026-07-20 -- clasm's Compute domain
currently has no concept of EC2 launch templates at all, and the
operator's work group uses them to encapsulate what a running instance
needs (RDM's software requirements, primarily), evolving that
definition over time as requirements change. TODO.md separately carried
an IMDSv2 bug (new instances launched by clasm set no
`MetadataOptions`, triggering security warnings) that turned out to
share the exact same AWS concept as the new work.

**Decision.** Build launch templates directly from cloud-init YAML,
never derived from an existing running instance: `CreateLaunchTemplate`/
`CreateLaunchTemplateVersion` take a `LaunchTemplateData` struct
constructed by clasm itself (reusing Feature 3's existing AMI/
instance-type/subnet/security-group/IAM-profile/tag prompts, with the
YAML's content as `UserData`), not `GetLaunchTemplateData`'s
instance-derived path. New template versions are created only after a
diff: decode the target version's `UserData`, compare against the
local YAML file, skip entirely if identical ("no changes -- nothing to
sync"), otherwise show a plain-text unified diff and require explicit
confirmation before `CreateLaunchTemplateVersion`. Promoting a version
to `$Default` is always a separate, explicit action
(`ModifyLaunchTemplate`), never a side effect of syncing. "Create EC2
Instance from Launch Template" is a third, parallel entry point
alongside Create-from-AMI and Create-from-Cloud-Init -- not a hybrid
wizard that also lets the operator override individual template
fields. IMDSv2 (`HttpTokens: required`) is folded into this same pass:
enforced unconditionally on every new template and on the existing
plain `RunInstances` launch paths (closing the pre-existing TODO.md
bug), and flagged passively (not auto-fixed) on any existing template
found without it.

**Rationale.**
- The operator has never used launch templates before and, in an
  earlier round of this design conversation, initially framed the
  sync question as "which YAML file is this template tied to" --
  clarified directly to "is there a mechanism to create a template
  without an existing instance," confirming the build-from-YAML-
  directly model rather than a persistent file↔template association
  clasm would need to track as new state.
- Diff-before-version avoids the AWS default of tools that always bump
  a version number regardless of content -- Tom's own framing of "does
  this actually require a new version" is exactly this no-op check,
  and it keeps a template's version history meaningful (one version
  per actual content change) rather than accumulating no-op versions
  from repeated syncs of unchanged YAML.
- Explicit promote-to-default (never automatic) matches the operator's
  own stated expectation: "I can see people experimenting with launch
  templates during the development process" -- an in-progress sync
  shouldn't change what a plain "Create from Launch Template" launch
  picks up by default.
- Folding IMDSv2 into this pass rather than deferring it with the
  tags-screen/backup-bucket-default/top-level-tag-management items
  (also open in TODO.md) is justified narrowly by shared surface area
  (`MetadataOptions`/`InstanceMetadataOptionsRequest` touches the same
  `RunInstances`/`RequestLaunchTemplateData` code this phase already
  changes) -- not a general precedent for bundling unrelated bug fixes
  into feature work.
- The plain-text diff (`github.com/aymanbagabas/go-udiff`, already
  present in `go.sum` as an indirect dependency via
  `charmbracelet/x/exp/teatest`, used by `internal/filemanager`'s own
  tests) means no new third-party dependency is actually introduced --
  only promoted from indirect to direct, the same move Phase 20.24
  already made for `x/ansi`.

**Rejected alternatives.**
- *Derive templates from an existing instance's live config*
  (`GetLaunchTemplateData`) -- doesn't fit the operator's actual flow
  (YAML authored first, template built from it), and would require an
  instance to already exist before a template could, backwards from
  "create a template without an existing EC2 instance."
- *Always create a new version on sync, no diff check* -- simpler, but
  produces meaningless version history (every sync bumps a version
  even with no actual content change) and doesn't answer Tom's own
  question about whether a sync is even needed.
- *Auto-promote a new version to default after sync* -- more
  convenient, but means an unreviewed, possibly-experimental version
  silently becomes what the next plain launch picks up.
- *Hybrid "launch from template but override individual fields"
  wizard* -- an earlier framing in this same design conversation,
  dropped once the operator clarified the actual ask was a third
  parallel entry point, matching Create-from-AMI/Create-from-Cloud-
  Init's existing shape.
- *A dedicated "audit all templates for IMDSv2" action* -- more
  visible, but more to build than the operator actually asked for
  ("recommended for existing templates if missing"); passive flagging
  on the existing Show/List screens covers that without a new
  top-level action.
- *Defer IMDSv2 alongside the tags-screen/backup-bucket-default/
  top-level-tag-management items* -- considered, since those are also
  open TODO.md items being deliberately deferred past this phase, but
  rejected specifically because IMDSv2's `MetadataOptions` surface
  overlaps directly with code this phase already touches, unlike the
  other three.

**Consequences.** New `internal/inventory.LaunchTemplate` type and
per-version detail type; `EC2API` gains 7 methods; six new Compute-menu
actions; `github.com/aymanbagabas/go-udiff` promoted to a direct
dependency; `launch_execute.go`'s `RunInstances` call gains
`MetadataOptions` (previously absent). No change to any existing
v0.0.1 workflow's behavior otherwise -- v0.0.1 is already piloting in
production, so this is additive. See `PLAN.md` Phase 20.27.

---


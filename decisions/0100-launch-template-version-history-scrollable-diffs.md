---
id: "0100"
title: "Launch Template version history, scrollable diffs, and split Show resource lists"
date: "2026-07-20"
status: accepted
kind: decision
trigger: live-test
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "fac3ef20-6123-4180-bd4b-1138963ef3bb"
origin_host: "MACMINI-RD.local"
---

**Context.** First real-AWS pass over Phase 20.27's launch template
support surfaced three UX gaps, all from actual use rather than design
review: (1) Show Launch Template only reports "there's another
version," not what changed in it; (2) Sync's confirmation diff is a
raw `fmt.Fprintln` dump that can scroll off screen with no way to page
back through it; (3) Compute's "Show resource lists" pages through
Instances -> AMIs -> Launch Templates as one combined flow, which felt
awkward when the operator only wanted one of the three.

**Decision.**
- Show Launch Template gains a sub-choice after picking a template:
  show one version's detail (existing behavior), list every version
  (number/creation time/default flag, via new
  `inventory.ListLaunchTemplateVersions`), or diff any two versions'
  decoded cloud-init content (reusing Sync's own diff mechanism,
  read-only -- never creates a version).
- Both Sync's diff and the new version-diff render through the shared
  List-tier component (`tui.RunListView`) in real interactive use, via
  new `displayRows`/`displayDiff` helpers -- scrollable, consistent
  chrome with every other resource listing in the app, rather than a
  second, purpose-built diff viewer. Accessible/test mode (no real
  bubbletea loop available) falls back to the same plain dump Sync
  already printed, so no existing test needed rewriting.
- Compute's single "Show resource lists" becomes three menu entries:
  "Show instances," "Show AMIs," "Show launch templates." S3 and Key
  Management are deliberately left alone -- each has exactly one
  resource type, so there's no paging-through-others problem to fix
  there.

**Rationale.**
- Listing versions and diffing two of them are different questions
  ("what versions exist" vs. "what changed") -- collapsing them into
  one action would either overload a single screen or force the
  operator through a diff they didn't ask for just to see a version
  list.
- Reusing the List-tier component for diffs (rather than a bespoke
  viewer) keeps the diff-in-a-scrollable-box mechanism identical
  everywhere it's needed (Sync's confirmation step and Show's
  version-diff), and matches this project's general preference for
  reusing existing chrome over inventing new UI per feature.
- Splitting Show resource lists only where the reported problem
  actually exists (Compute's three resource types) avoids restructuring
  S3/Key Management for a problem they don't have.

**Rejected alternatives.**
- *A single "list versions with diff" screen* -- combines two distinct
  questions into one, and would need to diff by default or require an
  extra step to opt out, neither of which is simpler than two separate
  choices.
- *A dedicated diff viewer component* (syntax-aware, side-by-side) --
  more capable, more to build than this need calls for; the List-tier's
  existing scroll/filter chrome is sufficient for plain-text unified
  diffs.
- *Keep Show resource lists combined, let `q` advance instead of exit*
  -- considered as a lighter-touch fix, but three separate, directly
  reachable menu entries is more discoverable than a "press q to see
  the next one" convention the operator would have to learn.

**Consequences.** New `inventory.ListLaunchTemplateVersions` +
`LaunchTemplateVersionSummary`; `show_launch_template.go` restructured
around a template-level sub-menu (existing tests updated for the new
leading choice-prompt); `MenuActions.ShowResourceLists` replaced by
`ShowInstances`/`ShowAMIs`/`ShowLaunchTemplates`; `mainMenuItems` grows
from 18 to 20, requiring the same hardcoded-index maintenance in
`menu_test.go` every prior menu-ordering change in this project has
needed. See `PLAN.md` Phase 20.28.

---


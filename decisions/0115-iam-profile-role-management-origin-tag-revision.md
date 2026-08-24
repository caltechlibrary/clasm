---
id: "0115"
title: "IAM Profile & Role Management: Origin tag revision (IMSS naming, no hardcoded vocabulary, tagging exempt from the read-only guard)"
date: "2026-07-23"
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
uuid: "5e188969-c555-4b45-8ee1-43c9d5b00c55"
origin_host: "MACMINI-RD.local"
---

**Context.** Same-day follow-up to "IAM Profile & Role Management: seven
scoping decisions, bundled into v0.0.5" (below), after further
discussion. Three corrections surfaced: (1) "central IT" was the wrong
name -- Caltech's central IT organization is IMSS, and the security team
is one component *within* IMSS, not a separate category; (2) DLD
operates as an independent group, so functionally AWS-provided and
IMSS-provided resources get identical treatment (both "not DLD's"), not
a three-way split with different rules; (3) the actual tag vocabulary
(key name and which value means "DLD-owned") isn't decided yet -- it's
pending a demo of this feature and feedback from the user's group -- so
nothing about it should be hardcoded in clasm's source.

**Decision 1 (revises the prior entry's Decision 1): `Owner` with a
fixed `DLD`/`CentralIT` vocabulary is replaced by a general, config-driven
`Origin` tag.** Both the tag's key name and which value means "DLD-owned"
move to a new `origin_tag` config section (`~/.clasm`, mirroring
`regions`/`backup_directories`), left unset by default (see DESIGN.md,
"New Configuration: `origin_tag`"). Until the user's group settles on
real values and the config is updated, nothing is recognized as
DLD-owned via tag -- accepted, since guessing at a vocabulary that isn't
decided would just create rework once it is.

**Rejected alternative.** *Keep `Owner` as a fixed, clasm-hardcoded
enum* (the prior entry's original decision) -- rejected once it became
clear the actual vocabulary is still an open question for the user's
group, not something clasm should presume to answer for them.

**Decision 2 (revises the prior entry's Decision 4): the read-only guard
exempts tagging.** The guard still blocks anything that changes a role's/
profile's/policy's actual permissions (attach/detach a managed policy,
edit a trust policy, delete) on a resource not recognized as DLD-owned --
but tagging itself is never gated. DLD needs to record who to contact
for support on IMSS- and AWS-owned resources too; blocking that would
defeat the reason for touching those resources in the first place.

**Rejected alternative.** *Read-only for everything, including tags* --
simpler (one gate, no exception), but directly blocks the concrete,
stated need (support-contact recording) that motivated allowing any
interaction with non-DLD resources at all.

**Decision 3 (revises the prior entry's Decision 5): drop the dedicated
"Tag as DLD-owned" action.** It made sense when `Owner=DLD` was a
clasm-hardcoded value; once the vocabulary moved to "TBD, decided by the
user's group," a bespoke shortcut for one specific, not-yet-known value
no longer made sense. Setting `Origin` on a legacy resource is now just
an ordinary Tag Management edit (Phase 20.37) -- no special-cased action.

**Decision 4 (new): the browse list shows `Origin`'s literal value, or
an explicit "(unset)" -- not a fixed multi-way category, not collapsed to
a simple editable/read-only label.** An unset `Origin` tag is itself
useful information -- it tells the group "this one still needs a call
made on it" -- which a binary or pre-categorized display would hide.
Filterable via the existing List-tier filter ("/").

**Rejected alternatives.** *A fixed DLD/IMSS/AWS/Unknown category label*
-- rejected because clasm has no reliable way to distinguish IMSS from
AWS-managed before the vocabulary exists; the label would be a guess
dressed up as a category. *Collapse to binary editable/read-only* --
considered and initially favored earlier the same day, but reversed once
it was clear this throws away the "nobody's decided yet" signal that
makes an unset tag valuable to surface at all.

**Decision 5 (new): this is a general mechanism, but IAM-only in
display for v0.0.5.** `Origin` is designed as a tool-wide convention
(joining `Project`/`Environment`), not IAM-specific, so it costs little
extra to build generally now. But the column is only added to the IAM
domain's three list views this release -- not to the five existing
taggable kinds (instances, AMIs, launch templates, key pairs, buckets).

**Rejected alternative.** *Surface `Origin` everywhere immediately* --
rejected for this release: those five resource kinds don't share IAM's
"is this even ours to touch" ambiguity, so the need there is weaker, and
proving the convention out in one place before a group demo is safer
than rolling it out unproven everywhere at once.

**Consequences.** The IAM-domain implementation phases (PLAN.md Phases
20.36/20.37) gain a config-loading dependency (`internal/config` needs
an `OriginTag` struct with `Key`/`DLDValue` fields, both defaulting to
their documented values) that the prior pass hadn't scoped; the
previously-planned "Tag as DLD-owned" menu action is removed from Phase
20.36's work items, since Decision 3 makes it redundant with Phase
20.37's general tagging. No change to Decisions 2, 3, 6, 7 from the
prior entry (creation-capability scope, trust-principal scope, template
source, Policy-as-top-level-kind) -- those stand as originally recorded.

---


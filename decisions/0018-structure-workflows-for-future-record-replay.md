---
id: "0018"
title: "Structure workflows for future record/replay (\"Recorded Scripts\")"
date: "2026-07-01"
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
uuid: "9e007793-e66f-4271-bbdd-fdde7f893882"
origin_host: "MACMINI-RD.local"
---

**Context.** Discussing "bake an AMI from cloud-init" led to a bigger idea:
capture the sequence of actions taken in an interactive session as an
editable, replayable script — analogous to how a "skill" packages a
procedure for a language model, but for this deterministic tool. The user
wants this to use YAML (prior success with YAML-driven configuration in
other projects, e.g. `dataset`'s web service config) and templated values
(not just literal captured values), with safety gates enforced on replay,
not bypassed. This is a substantial feature — a new execution mode
(record / replay) that cuts across the whole menu/dispatch loop, not a
single primitive — so it is not built in v1 (see "Recorded Scripts" under
"Deferred to a Later Version" in `DESIGN.md`/`PLAN.md`). It also
potentially subsumes the earlier-deferred "Clone instance for testing",
"Upgrade with rollback point", and "Bake AMI from cloud-init" composite
workflows: if a user can record a sequence once and replay it with
different values, those don't need to be bespoke Go features.

**Decision.** v1 does not build the recorder, the YAML schema, or the
replay engine. It does structure every confirmation-gated workflow
(Phases 4, 5, 6's AMI path, 7, 8) around a specific seam so that adding
record/replay later does not require reopening already-finished code:
1. Each workflow separates **building a resolved parameters struct**
   (interactive prompts fill it in v1) from **executing it against AWS**.
   The execution code takes a plain, typed struct (e.g.
   `CreateAMIParams{InstanceID, Name, Description, NoReboot, Tags}`) and
   never knows whether prompts or a future YAML file produced it
2. The **confirmation/dry-run gate is its own reusable step**, not inlined
   into each workflow's prompt loop, so a future replay engine can route
   through the identical gate rather than a second, parallel
   implementation of "is this safe to do"
3. When templating is eventually built, it applies via Go's standard
   library `text/template` to the YAML text before parsing — no new
   dependency, consistent with this project's stdlib-first preference

**Rationale.**
- The params-struct/execute split and the reusable confirmation gate are
  good structure on their own merits (testability, single source of truth
  for "is this safe"), so requiring them now costs nothing extra — it's a
  constraint on code already being written, not additional code
- Building the actual recorder/replay engine before v1's primitives have
  proven themselves against real AWS would be scope creep on an already
  large rewrite (see "V1 scope" below)
- Safety gates must not be bypassable by replay without deliberate,
  explicit opt-in — reusing the exact same gate function is the simplest
  way to guarantee that, rather than trusting a second implementation to
  stay in sync

**Rejected alternatives.**
- *Build record/replay now, as part of v1* — most directly useful, but
  conflicts with "V1 scope: ship the four primitives first" and adds a new
  execution mode on top of an already-large Bash→Go rewrite
- *Defer without any structural constraint on v1's workflows* — cheaper
  short-term, but risks having to rewrite Phase 4-8's internals later to
  retrofit the params-struct/confirm-gate seam once record/replay is
  actually built
- *Literal-only captured values (no templating)* — simpler, but the user
  specifically wants templating for repurposing a saved sequence across
  different targets (different instance, different environment), which a
  literal-only capture can't do without hand-editing every value each time

**Consequences.**
- `PLAN.md` Phases 4, 5, 6, 7, 8 each get a work item noting this
  structural requirement
- The "Deferred to a Later Version" entries for "Clone instance for
  testing", "Upgrade with rollback point", and "Bake AMI from cloud-init"
  are annotated as likely to become example Recorded Scripts rather than
  bespoke Go workflows, once the mechanism exists
- No new third-party dependency is introduced by this decision — YAML
  parsing (`gopkg.in/yaml.v3` or similar) would be needed when the feature
  is actually built, and would need the same approval process as
  `aws-sdk-go-v2` did, at that time

---


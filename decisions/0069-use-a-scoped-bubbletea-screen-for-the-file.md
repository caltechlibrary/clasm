---
id: "0069"
title: "Use a scoped bubbletea screen for the file manager's double-pane mode; every other S3 wizard stays on huh"
date: "2026-07-09"
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
uuid: "ae3ce8d0-1f2c-43dc-940d-64dd0f89a489"
origin_host: "MACMINI-RD.local"
---

**Context.** The "0.0.1 scope" decision below evaluated `bubbletea`
against `huh` and chose `huh` as the leading candidate specifically
because its fields are blocking/synchronous, a close match to
`termlib`'s `Prompt`/`PickList`/`Confirm` shape, while `bubbletea`'s
Elm-architecture message loop would mean rewriting every one of
`internal/workflow`'s ~40 wizards into explicit state machines.
Designing the file manager's double-pane mode (local directory + bucket,
live tag-and-move between them) surfaced a case that evaluation didn't
anticipate: two simultaneously-visible, independently-navigable
listings with cross-pane actions is a genuinely stateful,
continuously-redrawing UI -- not a sequence of blocking prompts, no
matter how the prompts are composed.

**Decision.** Build the file manager's screen (both single- and
double-pane modes, since they share one `Model`) directly on
`bubbletea` as one scoped, bounded component. Everything else in the S3
domain -- Create Bucket, Configure Static Website Hosting, Manage
Bucket Lifecycle Policies, Delete Bucket, and this same screen's own
bucket-selection pre-flight -- stays on `huh`'s blocking fields, per the
original evaluation.

**Rejected alternatives.**
- *Approximate the linked mode as a `huh`-only reviewed batch* (three
  sequential filtered-multiselect phases: upload pass, download pass,
  delete pass, each pre-checked from a diff) -- genuinely buildable
  within the existing huh-only architecture and seriously considered;
  rejected once a live, navigable dual-pane experience was preferred
  over a review-then-execute approximation of one.
- *Adopt `bubbletea` project-wide now, since `huh` already pulls it in*
  -- rejected again; the rewrite cost the original evaluation identified
  (~40 wizards' control flow) doesn't shrink just because one new screen
  needs `bubbletea` directly -- it only means this one screen's
  incremental dependency cost is zero, not that a wider migration is
  now free.

**Consequences.** No new dependency weight versus adopting `huh` at
all -- `huh` already pulls in `bubbletea`, `bubbles`, and `lipgloss`
transitively. This is the only place in the S3 domain design that needs
a custom `bubbletea` `Model`; DESIGN.md 21.8 has the detail. Test
strategy for this `Model` is an open question (`PLAN.md` Phase 20.1) --
the project's existing pipe-based test pattern doesn't directly apply
to an `Update`/`View` loop.

---


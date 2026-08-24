---
id: "0064"
title: "0.0.1 scope: ship on termlib as-is; postpone CloudFront and the UI/UX overhaul"
date: "2026-07-09"
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
uuid: "c457459b-f3c4-4dda-858f-044085252aef"
origin_host: "MACMINI-RD.local"
---

**Context.** With Phase 20 (S3 domain) real-AWS verified and this
session's follow-on work landed -- local validation for lifecycle
rule ordering, a read-only "View rule details" action, Delete Bucket,
Delete Objects by Prefix, and filter-as-you-type/alphabetical sort in
`ui.PickList` -- the user judged core functionality complete enough
for colleagues to start using the tool, but not the UI itself: the
current numbered-menu, blocking-prompt style built on `termlib` (the
user's own library) needs a UX/UI pass before wider use.

As part of scoping what "ready for 0.0.1" means, two third-party
libraries were evaluated (by pulling their actual source into a
scratch module, not just reading marketing pages):

- `github.com/charmbracelet/bubbletea`: an Elm-architecture (Model/
  Update/View, async message loop) TUI framework. Replacing `termlib`
  with it would mean rewriting the control flow of every one of
  `internal/workflow`'s ~40 prompt-driven wizards into explicit state
  machines, plus their tests -- not a drop-in swap.
- `github.com/charmbracelet/huh`: forms/prompts built on `bubbletea`,
  but each field is run synchronously/blocking (`huh.Run(field)`),
  much closer to `termlib`'s `Prompt`/`PickList`/`Confirm` shape. Its
  `Select` field's built-in incremental filtering is a nicer target
  than the numbered-list approach, and every field's
  `RunAccessible(w io.Writer, r io.Reader) error` is structurally the
  same shape as this project's existing pipe-based test harness
  (`newPipeEditor`), so the existing test style would largely carry
  over. Cost: `huh` pulls in `bubbletea`+`bubbles`+`lipgloss` and
  ~18 transitive modules, versus `termlib`'s ~2,800 dependency-free
  lines.
- `github.com/peak/s5cmd/v2`: not a good dependency candidate either
  way (its `command/` package is coupled to `urfave/cli/v2.Context`;
  its cleaner `storage/` package is built on `aws-sdk-go` v1, while
  this project is on `aws-sdk-go-v2`). Its `storage.S3.MultiDelete`
  pattern -- batch `s3:DeleteObjects` in chunks of up to 1000 keys
  instead of one `DeleteObject` call per key -- is worth reimplementing
  natively later (see TODO.md, "Nice to have"), since `aws-sdk-go-v2`
  already exposes `DeleteObjects`.

**Decision.** 0.0.1 ships on `termlib` unchanged. `huh` is the leading
candidate for the next release's UI/UX pass (evaluated, not started).
CloudFront (PLAN.md Phase 21-22) is postponed to a later version -- its
domain-picker entry was removed entirely (`DomainActions.CloudFront`,
the `"CloudFront"` menu item, and its `NotYetImplemented` wiring in
`cmd/awsops/main.go`) rather than left as a placeholder, so the
released UI doesn't expose a menu item that goes nowhere. Its design in
DESIGN.md/PLAN.md is untouched and stays valid for when it's picked
back up.

**Rejected alternatives.** Migrating to raw `bubbletea` now, to avoid a
second migration later -- rejected because the architectural mismatch
with `termlib`'s ~40 blocking call sites makes it a full rewrite either
way; better to ship 0.0.1 first and spend that rewrite effort once,
against `huh` (or whatever's chosen), informed by real colleague usage
rather than guessing at UX needs up front.

**Consequences.** No `internal/ui` or `internal/workflow` prompt code
changed as a result of this decision -- it's scope/sequencing only.
`TODO.md`'s "Postponed to a later version" section and `PLAN.md`'s
Phase 21 heading both note the CloudFront postponement.

---


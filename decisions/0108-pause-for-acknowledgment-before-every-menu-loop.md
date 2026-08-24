---
id: "0108"
title: "Pause for acknowledgment before every menu-loop redraw"
date: "2026-07-22"
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
uuid: "c690c82c-99f6-4626-8d15-1942202b3f58"
origin_host: "MACMINI-RD.local"
---

**Context.** Live real-AWS testing of Resize Instance's Root Volume
(PLAN.md Phase 20.31) found that its printed output -- the resize
confirmation, "Volume resize is usable," and (when SSM never comes
online, as it didn't for either test instance) `growRootFilesystem`'s
manual `growpart`/`resize2fs` fallback instructions -- flashed by too
fast to read. Root cause: `resizeInstanceRootVolume` returns
immediately after printing, and the Compute domain's menu loop
(`runMainMenu`, `menu.go`) redraws its full-height `huh.Select` on the
very next iteration, which (per `TUI_REFERENCE.md` §1) always repaints
the entire terminal -- exactly the "silent-scroll" bug class already
found and fixed once before, for Tag Management's "Show tags"
(PLAN.md Phase 20.29/20.30), just recurring at a new call site nobody
had exercised live until now.

Separately, in the same session, a live typo during instance cleanup
reproduced the identical symptom on the *error* path: `runMainMenu`
prints `"Error: %s\n"` after a failed action (or `"Error refreshing
listings: %s\n"` after a failed `Refresh`) and then loops straight
back into `pickMainMenuItem`'s full-height `Select` -- wiping the
error before it can be read. Auditing the other three domain menu
loops (`s3_menu.go`, `keymgmt_menu.go`, `tagmgmt_menu.go`) found the
exact same two print-then-redraw sites duplicated in each, verbatim --
this is a systemic gap in the shared menu-loop shape, not one
workflow's bug.

**Decision.** A single shared helper, `pauseForAcknowledgment`
(`menu.go`), blocks on a plain `ui.Prompt` ("Press Enter to continue")
until the operator explicitly dismisses it. Per `TUI_REFERENCE.md` §5,
plain prompts are deliberately content-sized, not full-height, so they
don't themselves wipe anything already on screen -- the same property
that makes them safe to insert between "something was printed" and
"the next full-height Select renders." Called **unconditionally**,
every time, at all of:
- both print sites (`"Error: ..."`, `"Error refreshing listings:
  ..."`) in all four domain menu loops (`menu.go`, `s3_menu.go`,
  `keymgmt_menu.go`, `tagmgmt_menu.go`) -- 8 call sites total
- the end of `resizeInstanceRootVolume` (`resize_volume.go`), after
  `growRootFilesystem` returns, whether or not automated growth
  succeeded

Unconditional rather than "only pause if something was printed":
simpler to implement and verify, and every one of the 9 call sites
above always prints something immediately beforehand anyway, so the
distinction is moot in practice.

**Rejected alternatives.**
- *Embed the message in the next Select's `Description`*, matching the
  Tag Management fix -- doesn't fit here. That fix worked because
  "Show tags" had one static, current-state snapshot to redisplay.
  These call sites print a *sequence* of status lines building up over
  time (resize progress, growth fallback instructions, an arbitrary
  error string) with no single "current state" to re-embed.
- *Fix only the two call sites found live* (resize's success path,
  the typo's error path) -- rejected once the audit showed the same
  two-print pattern duplicated identically across all four menu loops;
  fixing one and leaving the other three would just mean re-discovering
  this bug three more times, once per domain.

**Consequences.** Every dispatched action's error, every refresh
error, and Resize Instance's Root Volume's own output now require an
explicit Enter to dismiss before the menu reappears -- one extra
keypress per error/resize, in exchange for actually being able to read
what happened. `huh.Input`'s accessible-mode path
(`accessibility.PromptString`) never errors, even on EOF (it returns
the field's default value instead) -- confirmed by reading huh's own
vendored source rather than assumed, given this project's standing
"check vendored source, don't trust memory" rule for huh/bubbletea
behavior -- so the pause is safe to add inside an existing
accessible-mode-tested loop without risking the EOF-hangs-forever
`Select`/`PointerAccessor` gotcha from Phase 20.29. Existing pipe-driven
tests that continue a menu loop past an error (`*_ActionErrorDoesNotCrashLoop`,
one per domain) need one extra blank input line inserted between the
two picks, to account for the pause now consuming a line of input.

---


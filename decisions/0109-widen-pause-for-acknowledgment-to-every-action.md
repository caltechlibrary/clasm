---
id: "0109"
title: "Widen \"pause for acknowledgment\" to every action, not just errors"
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
uuid: "9c92b2f2-e19a-4d30-a3f0-e7161a1703bd"
origin_host: "MACMINI-RD.local"
---

**Context.** Live real-AWS testing of the same day's "Pause for
acknowledgment before every menu-loop redraw" fix (below) found a
third instance of the underlying bug, in a place that fix didn't
cover. `runLaunch` (`launch_execute.go`, shared by "Create EC2 instance
from AMI" and "from cloud-init YAML") calls `checkCloudInitCompletion`
after a launch; when cloud-init itself errors on the instance, that's
deliberately reported as a *result value*
(`CloudInitCheckResult{Status: "error"}`), not a Go error -- the
instance did launch successfully, so this isn't a workflow failure,
just a status worth telling the operator about. `runLaunch` prints
`"cloud-init reported an error -- check the instance before using
it."`, displays connection info, and returns **nil**. Because the
action succeeded (no error), the menu loop's dispatch takes the
success path, which -- after the first pass at this fix -- only paused
in the one specific case hand-patched into `resizeInstanceRootVolume`.
Live-tested: the operator saw the launch confirmation and connection
info flash by with no error and no pause, landing back on the menu
with no visible indication anything but a clean launch had happened.

This generalizes: any of the ~20 dispatched actions across all four
domains can print multi-line success-path status (launch
confirmations, connection info, "cloud-init completed successfully,"
etc.), and every one of those prints was still exposed to the same
redraw-wipes-the-screen problem this session's earlier fix only closed
for the error/refresh-error prints plus one hand-picked success case.
Hunting down and patching each individual success-path print site
one at a time (the way `resizeInstanceRootVolume` was patched) would
mean re-discovering this bug roughly once per action, the same
diminishing-returns pattern that already justified fixing all four
menu loops at once instead of just the one found live.

**Decision.** Add one new pause call, on the *success* path: right
after `choice.action(actions, ctx)` returns nil, before calling
`actions.Refresh(ctx)`, in all four domain menu loops. The pause must
still come *after* whatever text needs reading and *before* the next
redraw -- so this doesn't collapse into a single unconditional call
placed before the `err` check (that would pause before the error
text even prints, on the failure path). The two pauses from earlier
the same day stay exactly as they were: after the `"Error: ..."` print
(failure path) and after the `"Error refreshing listings: ..."` print.
Net result, three pause call sites per loop (was two): the action's
own output (success or failure) and the refresh error are each
guaranteed one pause before anything else redraws. The one-off pause
inside `resizeInstanceRootVolume` is removed as redundant now that the
loop itself always pauses after dispatching a successful action.

**Rejected alternatives.**
- *Patch `runLaunch`'s cloud-init-error branch specifically*, matching
  the original per-call-site approach -- rejected for the same reason
  the four-menu-loop audit was: it fixes the one instance found live
  and leaves every other action's own success-path prints (there are
  many, across four domains) equally exposed and un-discovered.

**Consequences.** Every single dispatched action now costs one extra
Enter keypress before the menu reappears, not just the ones that
error or explicitly opted into a pause -- a bigger UX tax than the
original narrower fix, accepted in exchange for closing the entire bug
class in one place instead of iteratively rediscovering it. Existing
per-domain `*_ActionErrorDoesNotCrashLoop`/`*_RefreshesAfterASuccessfulAction`/
`*_DispatchesToTheChosenAction`-style tests that dispatch more than one
action in sequence need a blank input line inserted after *every*
dispatch now, not just after the ones that errored.

---


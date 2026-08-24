---
id: "0046"
title: "Tolerate GetCommandInvocation's post-SendCommand eventual-consistency window"
date: "2026-07-02"
status: accepted
kind: correction
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
uuid: "453f4859-f46f-4764-9a34-d4b19b17272c"
origin_host: "MACMINI-RD.local"
---

**Context.** A real launch succeeded all the way through `RunInstances`
and reaching `running`, then failed during the cloud-init completion
check: `Error: AWS error [InvocationDoesNotExist]: ` immediately after
`ssm:SendCommand`. The `-debug` log showed exactly one `SendCommand`
followed by exactly one `GetCommandInvocation`, which failed -- the
identical shape of bug as `InvalidInstanceID.NotFound`/`InvalidAMIID.NotFound`
(both fixed earlier this session), just on the SSM side: a newly
submitted command invocation can be briefly invisible to
`ssm:GetCommandInvocation` for a few seconds after `ssm:SendCommand`
returns its ID.

**Decision.** `RunShellCommand`'s poll loop now tolerates AWS's own
`InvocationDoesNotExist` the same way it already tolerates "not in a
terminal status yet" -- keep polling instead of returning the error. Any
other `GetCommandInvocation` error still fails immediately, unchanged.

**Rationale.** Exactly the precedent set by the two earlier fixes in
this same family (DECISIONS.md, "Tolerate DescribeInstances'
post-RunInstances eventual-consistency window"): this is documented AWS
eventual-consistency behavior, not something specific to this account,
so the fix is to expect it during polling, not to work around it
operationally.

**Rejected alternatives.** None -- same reasoning as the two prior fixes
in this family; no alternative was seriously considered.

**Consequences.**
- `internal/workflow/ssm.go`: `isInvocationNotYetVisible`, following the
  exact naming/shape convention of `isInstanceNotYetVisible`
  (`launch_execute.go`) and `isImageNotYetVisible`
  (`create_ami_execute.go`).
- This is now the third instance of the identical bug pattern found in
  as many real-AWS testing sessions (`RunInstances`/`DescribeInstances`,
  `CreateImage`/`DescribeImages`, `SendCommand`/`GetCommandInvocation`)
  -- worth remembering as a general AWS API shape (submit an async
  operation, get an ID back, immediately query that ID) rather than
  three unrelated one-off bugs, if a fourth case turns up in an
  unreviewed code path.

---


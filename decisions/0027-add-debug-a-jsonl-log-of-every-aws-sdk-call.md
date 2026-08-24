---
id: "0027"
title: "Add -debug: a JSONL log of every AWS SDK call"
date: "2026-07-01"
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
uuid: "b35b983c-a7a6-4727-a32f-f661b525457f"
origin_host: "MACMINI-RD.local"
---

**Context.** The user asked for a `-debug` option to make awsops'
behavior easier to diagnose, pointing at a similar feature already
built for `~/Laboratory/harvey` (a JSONL debug log of specific
hand-instrumented events — LLM requests/responses, tool calls, etc.,
via a `*DebugLog` type whose methods are all nil-receiver-safe). Unlike
Harvey, where "interesting events" are a hand-picked subset of a large
surface area, awsops' entire interesting surface *is* its AWS SDK
calls — every EC2/SSM/S3/STS method it calls is already declared in
one of four narrow interfaces (`internal/awsclient`'s `EC2API`/
`SSMAPI`/`S3API`/`STSAPI`), the same interfaces the test fakes already
implement.

**Decision.**
- `internal/debuglog`: a new package with the same nil-safe `*DebugLog`
  shape as Harvey's — `Log(event string, fields map[string]any)`,
  `Path()`, `Close()`, all safe on a nil receiver — so `-debug=false`
  needs no `if debug` conditionals anywhere else in the codebase.
- Rather than hand-instrumenting ~15 call sites (Harvey's approach),
  wrap each of the four client interfaces with a logging decorator
  (`internal/awsclient`'s `WrapEC2`/`WrapSSM`/`WrapS3`/`WrapSTS`) that
  implements every method mechanically via one shared generic helper
  (`logAWSCall`): log method name, region, request params, duration,
  and response-or-error, then delegate. This gets full coverage of
  every AWS action awsops takes, for about the same code as
  hand-picking a subset would have cost, because the interfaces were
  already narrow and enumerable.
- `Wrap*` returns the original client unchanged when `dl` is nil, so
  there's no wrapper-object overhead when `-debug` isn't set.
- Sink: a timestamped file in the current directory
  (`awsops-debug-<timestamp>.jsonl`, `debuglog.DefaultPath()`), printed
  to stderr once at startup — not `agents/logs/` like Harvey, since
  awsops has no `agents/` directory convention of its own.

**Rationale.** Decorating the interfaces instead of instrumenting call
sites means new AWS calls added to `EC2API`/`SSMAPI`/`S3API`/`STSAPI`
in the future are logged automatically (the interface's method set is
enumerable and the compiler enforces the decorator implements all of
it) — no risk of a future workflow silently bypassing the debug log
because nobody remembered to add a manual `dl.Log(...)` call at the new
site.

**Trade-off.** Full-fidelity request/response logging (not just event
names) means `DescribeInstances`/`DescribeImages` calls that return
large collections write correspondingly large JSON records. Accepted:
the log is opt-in, local, and meant for active debugging sessions, not
continuous production telemetry — verbosity favors "can actually see
what happened" over log-file size.

---


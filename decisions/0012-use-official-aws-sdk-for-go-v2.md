---
id: "0012"
title: "Use official AWS SDK for Go v2"
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
uuid: "c4b55e1c-491d-4ee8-8add-6377f013db64"
origin_host: "MACMINI-RD.local"
---

**Context.** Retargeting to Go (see companion decision above) could either
keep the Bash version's shell-out pattern (`exec aws ...` from Go) or adopt
AWS's official Go SDK.

**Decision.** Use `github.com/aws/aws-sdk-go-v2` (with its `ec2` and `ssm`
service packages) for all AWS API calls. This is a third-party dependency
outside the pre-approved `github.com/rsdoiel` / `github.com/caltechlibrary`
namespaces (CLAUDE.md); explicitly approved for this project.

**Rationale.**
- Typed request/response structs eliminate the JSON-through-subshell-through-
  jq round trips that made the Bash version fragile
- No runtime dependency on the `aws` CLI binary or `jq` being installed —
  only the Go binary and AWS credentials
- Official, actively maintained by AWS
- Enables interface-based mocking of AWS calls in tests without a
  hand-rolled mock CLI binary (the pattern `tests/lib/test_helper.bash` used)

**Rejected alternatives.**
- *Shell out to the `aws` CLI from Go (`os/exec`)* — keeps the CLI-argument-
  quoting risk that caused this session's tag-specification bug, just moved
  into Go's `exec.Command` argument building; still requires the `aws` CLI
  as a runtime dependency
- *Hand-rolled AWS API client (SigV4 signing, raw REST calls)* —
  reinventing a well-solved, well-maintained problem for no benefit

**Consequences.**
- `go.mod` declares `github.com/aws/aws-sdk-go-v2` and its `ec2`/`ssm`/`sts`
  submodules as dependencies
- Credential resolution (env vars, `~/.aws/credentials`, SSO) is handled by
  the SDK's default credential chain, matching current Bash behavior
- Region iteration (the four configured regions) becomes explicit per-region
  SDK client construction, replacing the `AWS_REGION` env var wrapper
  pattern used in `ec2_ami_manager.bash`

---


---
id: "0013"
title: "Retarget implementation from Bash to Go"
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
uuid: "ab49863c-9c51-47df-86bf-0532b481f9dc"
origin_host: "MACMINI-RD.local"
---

**Context.** `ec2_ami_manager.bash` reached feature parity with the design
(Phases 0–7) but real-world use against production AWS resources surfaced a
string of bugs rooted in Bash's lack of static typing and its reliance on
runtime string construction for control flow:
- an `eval`-based array-copy helper (`show_pick_list`) had unbalanced
  quoting that crashed the interactive picker outright
- the AMI-name validation regex broke under BSD `grep` combined with a
  UTF-8 locale (`invalid character range`)
- the AMI-creation tag-specification string builder produced syntactically
  invalid AWS CLI shorthand that silently failed `create-image`, with no
  AMI created in any region and only a scrollback error message as evidence

Each bug class (shell quoting/escaping, locale-dependent tool behavior,
hand-built CLI argument strings) is structural to shelling out from Bash
rather than incidental, and none would be caught by static analysis before
runtime.

**Decision.** Set aside the Bash implementation and retarget the
interactive EC2/AMI manager to Go, in place in this repository, targeting
full feature parity with the existing design (all four operations: create
instance from AMI, create AMI from instance, remove AMI, main menu) before
the Bash version is retired.

**Rationale.**
- Go is this workspace's stated primary backend language (CLAUDE.md)
- A typed AWS SDK (see companion decision below) replaces "aws CLI + jq +
  eval" with compiled, typed API calls — the entire class of quoting/
  escaping bugs hit in this session becomes structurally impossible
- Go's `go test` and table-driven tests replace BATS plus a hand-rolled
  mock-`aws`-binary harness, and can mock the AWS SDK client via interfaces
  without shelling out at all
- The feature set, UX flow, and hard-won domain knowledge (multi-region
  aggregation, owned-AMIs-only scope, three-layer removal confirmation,
  fstrim/SSM pre-snapshot step, Invenio RDM crash-consistency guidance,
  volume-size time estimates) all carry forward unchanged — this is a
  reimplementation, not a redesign

**Rejected alternatives.**
- *Patch the specific bugs and stay in Bash* — would fix these three bugs
  but leaves the same eval/quoting/locale hazard class open for the next
  feature added
- *Python + boto3* — also a strong, typed-enough option with less ceremony
  than Go, but Go is this workspace's designated primary backend language
  (CLAUDE.md); Python/Perl are described there as secondary/legacy
- *Deno + TypeScript* — this workspace reserves Deno/TypeScript for
  middleware/frontend, not backend CLI tools (CLAUDE.md's layered
  architecture pattern)

**Consequences.**
- `ec2_ami_manager.bash`, `ami_copy.bash`, and their supporting docs remain
  in the repo unchanged as a working reference/spec until the Go version
  reaches parity and is verified against real AWS (the same retire-after-
  verify pattern already used for `ami_copy.bash`, see below)
- `DESIGN.md` and `PLAN.md` are retargeted for Go in this same update; the
  new `PLAN.md` phases restart from Phase 0 (Go module setup)
- BATS test debt for Phase 6/7 (`test_remove_ami.bats`, `test_menu.bats`) is
  superseded — those workflows get Go tests instead, not backfilled BATS
  tests
- `TEST_PLAN_REAL_AWS.txt`'s manual verification step now targets the Go
  binary, not `ec2_ami_manager.bash`

---


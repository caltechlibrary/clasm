---
id: "0043"
title: "Fix official Ubuntu AMI name filter pattern"
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
uuid: "a041549a-19e4-4484-ad70-a134499d4951"
origin_host: "MACMINI-RD.local"
---

**Context.** Real-AWS testing of the just-added official-Ubuntu-AMI
feature (above): the pick list showed only the account's own AMIs, no
Ubuntu entries, with no error printed -- exactly the designed best-
effort fallback behavior, which made it silent rather than obviously
broken. The `-debug` JSONL log showed why: every `EC2.DescribeImages`
call scoped to Canonical's owner ID (`099720109477`) returned zero
images, with no error, for both curated releases, every time. The
`name` filter value (`"ubuntu-noble-24.04-amd64-server-*"`, no leading
wildcard) only matches AMI names that *start* with that literal string
-- but Canonical's real, published AMI names are prefixed with a
path-like `ubuntu/images/hvm-ssd/` (or the newer
`ubuntu/images/hvm-ssd-gp3/`), so the filter could never match anything,
in any region, ever.

**Decision.** Both curated name patterns gained a leading
`ubuntu/images/hvm-ssd*/` segment --
`"ubuntu/images/hvm-ssd*/ubuntu-noble-24.04-amd64-server-*"` and the
Jammy equivalent -- anchoring to Canonical's actual documented naming
convention (the trailing `*` after `hvm-ssd` covers both the `hvm-ssd`
and `hvm-ssd-gp3` root-volume-type variants) instead of a bare suffix
match.

**Rationale.** This is exactly the kind of mistake real-AWS testing (not
unit tests against a fake) is positioned to catch: the fake's
`officialUbuntuImages` map is keyed by whatever literal string the
production code happens to pass in, so a test using the same wrong
literal for both "what the code searches for" and "what the fake
returns" passes without ever validating that string against AWS's
actual naming rules. Nothing about the unit tests was wrong; they
simply couldn't have caught this class of error on their own -- another
concrete case for why `TEST_PLAN_REAL_AWS.txt` and `-debug` remain load-
bearing, not just a formality after unit tests pass.

**Rejected alternatives.** None -- this is a factual correction to match
Canonical's real naming convention, not a design trade-off.

**Consequences.**
- `internal/workflow/official_ubuntu_amis.go`: `curatedUbuntuReleases`'
  `namePattern` values corrected; a code comment now records the exact
  failure mode (silent zero-match, not an error) so a future change to
  Canonical's naming convention is easier to recognize if it recurs.
- Test fixtures (`nobleNamePattern` constant, shared across
  `official_ubuntu_amis_test.go` and both launch-flow integration tests)
  updated to the corrected pattern for consistency, though as noted
  above this was necessary for consistency, not sufficient on its own to
  have caught the original bug.

---


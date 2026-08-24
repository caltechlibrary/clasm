---
id: "0032"
title: "Derive the AWS key pair name from a private key filename/path"
date: "2026-07-02"
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
uuid: "794a6220-b54f-466b-9365-8a3276692918"
origin_host: "MACMINI-RD.local"
---

**Context.** Real-AWS testing hit `AWS error [InvalidKeyPair.NotFound]:
The key pair '~/.ssh/etd-ami-test.pem' does not exist` -- the operator
typed the private key's file path at "Key pair name" instead of the AWS
key pair name, despite the prompt's own explicit "not a local file path"
wording. The `-debug` JSONL log showed the full sequence of what was
tried in one session: `etd-ami-test.pem` (bare filename with extension,
no directory) failed the same way, then the correct bare name
`etd-ami-test` worked (it got past `RunInstances`' key-pair check
entirely and failed for an unrelated reason -- see the separate
instance-type/Availability-Zone finding this surfaced, tracked in
TODO.md), then `~/.ssh/etd-ami-test.pem` was tried and failed again.
`ssh -i` muscle memory makes typing the key *file* rather than the key
*pair name* a recurring mistake, not a one-off typo.

**Decision.**
- `promptKeyPairNameOrCreate` now recognizes input that looks like a
  private key filename or path -- contains `/`, starts with `~`, or ends
  in `.pem`/`.ppk`/`.key` (case-insensitive) -- as distinct from a bare
  AWS key pair name.
- When recognized, the file is validated as actually readable (`~` is
  expanded against the home directory; a bare filename with no directory
  component that isn't readable relative to the current directory is
  also checked against this tool's own key directory, since that's where
  Create Key Pair saves keys and where a bare filename most plausibly
  lives) before anything is derived from it -- an unreadable path
  re-prompts with a clear local error instead of being sent to AWS,
  which would otherwise fail distantly and confusingly.
- Once validated, the AWS key pair name is derived from the file's
  basename with its extension stripped (e.g. `~/.ssh/etd-ami-test.pem`
  or bare `etd-ami-test.pem` -> `etd-ami-test`) and used as the launch's
  key pair name, with an on-screen note explaining what happened so the
  operator isn't surprised by what gets sent to AWS.
- This works because it's this tool's own convention: Create Key Pair
  (`createKeyPair`) always saves a new key's private material to exactly
  `<keyDir>/<name>.pem`, so the filename reliably encodes the real AWS
  key pair name regardless of which directory (or none) the operator
  typed.

**Rationale.**
- Fixes the actual reported failure and the two variants of it found in
  the same debug session (bare filename-with-extension, and full path),
  not just the one exact string from the bug report.
- Auto-deriving (rather than just rejecting with a "that looks like a
  path" message) is safe specifically because this tool controls the
  naming convention on the writing side (Create Key Pair) as well as the
  reading side (this prompt) -- it isn't guessing at an external
  convention it doesn't control.

**Rejected alternatives.**
- *Reject with a clarifying error instead of deriving* -- raised as an
  explicit scope question and declined in favor of the more helpful
  auto-derive path, consistent with this project's general preference
  (Security groups/Subnet ID, IAM instance profile) for fixing an
  AWS-error class locally rather than just describing it better.

**Consequences.**
- `internal/workflow/create_key_pair.go` gained `looksLikeKeyFilename`,
  `keyPairNameFromFilePath`, and `isReadableFile`; the existing "new"
  sub-flow was extracted into `createNewKeyPairInteractive` unchanged, so
  `promptKeyPairNameOrCreate` could loop on a bad key-filename input
  without duplicating that retry logic.
- No new AWS permissions or SDK dependencies -- this is entirely local
  validation before any AWS call is made.

---


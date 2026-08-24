---
id: "0049"
title: "Configure per-instance backup directories by Name pattern"
date: "2026-07-02"
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
uuid: "1237be52-cb19-41ba-9283-8350e31ee43c"
origin_host: "MACMINI-RD.local"
---

**Context.** Backup Archive & Trim's "Backup directory" prompt has no
default, by design (DESIGN.md, Feature 11) — every run requires an
explicit, deliberate value. In practice, though, the value is
predictable per machine: RDM instances all use `/opt/rdm_sql_backups`,
while some other service's instances use a different fixed path. Typing
the same known path from memory every run invites a typo, and there was
no way to record "this is the answer for this kind of instance" now
that `~/.awsops` (above) exists for exactly this kind of site-specific
setting.

**Decision.** `~/.awsops` gains `backup_directories`, an ordered list of
`{pattern, directory}` rules. `pattern` is matched against the picked
instance's Name tag using `path.Match`'s glob syntax (`*`, `?`,
`[...]`); the first matching rule's `directory` pre-fills the "Backup
directory" prompt as an editable default (`ui.WithDefault`) — pressing
Enter accepts it, typing something else overrides it. No match
(including an untagged instance with a blank Name) leaves the prompt
exactly as it behaved before this setting existed: required, no
default.

**Rationale.**
- Matching by Name (not the Project tag also carried by instances) was
  an explicit choice over Project-tag matching: Name tags already
  encode the kind of machine an operator is looking at (e.g. `rdm-*`
  covers every RDM instance across all its Projects) and glob patterns
  let one rule cover a whole family of instance names without requiring
  every instance to carry a distinct Project value.
- Pre-filling as an *editable* default, not skipping the prompt
  entirely, keeps this consistent with every other field on this
  workflow's confirmation gate: no field is silently accepted without
  the operator seeing it, since Backup Archive & Trim is a genuinely
  destructive operation (DESIGN.md, Feature 11).
- A malformed or non-matching pattern degrades to "no default" rather
  than an error — a typo'd pattern in `~/.awsops` shouldn't be able to
  break the workflow, only fail to save a keystroke.

**Rejected alternatives.**
- *Match by Project tag instead of Name.* Simpler in principle (one tag
  already used for grouping elsewhere), but doesn't fit the stated use
  case as well: multiple Projects can share the same backup directory
  convention (e.g. every RDM Project uses the same path), which a
  Name-glob (`rdm-*`) expresses in one rule where per-Project matching
  would need one entry per Project value.
- *Skip the prompt entirely on a match.* Faster, but removes the
  deliberate per-run check this workflow's other fields (age threshold,
  S3 bucket) also require with no silent defaults.

**Consequences.** `internal/config.BackupDirectoryRule` and
`BackupDirectoryFor` are new; `internal/workflow.BackupArchiveAndTrim`
gains a `backupDirRules []config.BackupDirectoryRule` parameter, wired
from `cfg.BackupDirectories` in `main.go`. `internal/workflow` now
imports `internal/config` for the first time.

---


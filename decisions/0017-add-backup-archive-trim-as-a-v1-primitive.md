---
id: "0017"
title: "Add Backup Archive & Trim as a v1 primitive"
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
uuid: "846f91dd-a4be-4c1e-8c03-1cb2e0da7e4b"
origin_host: "MACMINI-RD.local"
---

**Context.** Today, cleaning up stale Postgres backups on an RDM instance
is a fully manual chore: log in, look at `/opt/rdm_sql_backups`, decide
what's old enough to remove, delete it by hand. A read-only SSM check of
`newauthors` found the real scale of the problem: 87GB in
`/opt/rdm_sql_backups`, one `<project>-db-<n>-<project>-<date>.sql.gz`
file per day at ~980MB, with no rotation at all (root volume: 484GB total,
157GB used, 328GB free — the backups are over half of what's used). This
is the concrete cause of the "over-provisioned disk makes cloning slow"
problem that motivated this whole design conversation. No S3 destination
exists yet for these backups — that's a separate infrastructure task the
user still needs to do, not something this project creates.

This conversation also surfaced a broader framing for the tool: it should
help with ongoing *administration* of these instances (not just
create/remove EC2/AMI lifecycle events), and with improving development,
test, and deployment workflows more generally.

**Decision.** Add "Backup Archive & Trim" as a v1 primitive: given an
instance, a backup directory, and an age threshold (both explicit prompts,
no baked-in default — same reasoning as the `Environment` tag having no
default), it uploads files older than the threshold to S3, independently
verifies each upload, deletes only the verified files, then runs `fstrim`.
The sequence is deliberately split into two separate remote steps with the
tool itself as the arbiter in between, rather than one script that
uploads-then-immediately-deletes based on its own success report:

1. **Dry-run list** (SSM, read-only): candidate files matching the age
   threshold, with size/age, shown before anything happens
2. **Type-to-confirm** (matches the AMI-removal safety tier, since step 5
   is irreversible)
3. **Upload phase** (SSM): the instance uploads each candidate file to S3
   (`aws s3 cp`, run from the instance — see rejected alternatives) and
   reports back a small per-file JSON summary (S3 key, size). Nothing is
   deleted yet
4. **Independent verification**: the tool itself, using its own AWS
   credentials (not the instance's self-report), calls `s3:HeadObject` on
   every uploaded key and confirms it exists with the expected size
5. **Delete phase** (a *second*, separate SSM command): the instance
   deletes exactly the tool-verified file list — it does not re-derive its
   own "what's stale" list, avoiding a time-of-check/time-of-use gap
6. **fstrim**, then a report of bytes freed and any files that failed
   verification (left untouched, flagged, not deleted)

**Rationale.**
- Matches this project's existing safety pattern for destructive
  operations (dry-run, then explicit confirm — see "Multi-layer
  confirmation for AMI removal")
- SSM Run Command is not a bulk file-transfer channel (confirmed when
  designing Show/Export Cloud-Init) — ~980MB backup files are far too
  large to round-trip through SSM output, so the upload must happen
  *from* the instance itself via its own AWS CLI/credentials
- Splitting upload and delete into two round-trips with independent
  verification in between means the tool — not the remote script — decides
  what's safe to delete, based on a second, independent read from S3.
  Trusting a single script's self-report to authorize an irreversible
  delete was considered and explicitly rejected (see below)

**Rejected alternatives.**
- *Single script: upload then immediately delete on self-reported success*
  — simpler, one round-trip, but means a script bug or a transient S3
  error that still reports "success" could delete backups with nothing
  actually saved. Rejected in favor of independent verification
- *Tool downloads/re-uploads the files itself instead of the instance
  doing it* — would let the tool verify with certainty, but requires
  streaming multi-GB files through the operator's machine or through SSM
  (impractical for the same reason raw file transfer was rejected for
  Show/Export Cloud-Init's AMI path)
- *Fold into the existing fstrim step* — considered, but the user chose a
  standalone primitive (see "Should v1 add composite workflows" discussion
  in the Show/Export Cloud-Init decision above); backup cleanup is useful
  on its own, independent of AMI creation

**Consequences.**
- Each target instance's IAM instance profile needs `s3:PutObject` (and
  likely `s3:ListBucket` scoped to its own prefix) on the destination
  bucket — this is a cloud-init/AMI change in `caltechlibrary/cloud-init-
  examples`, not something this Go tool can retrofit from outside. This is
  in scope for this project to specify (per the earlier "prereq scope"
  decision) even though the actual bucket creation and IAM/Terraform work
  happens separately
- The tool's own IAM permissions (the operator's identity, distinct from
  the instance's own instance profile) need `s3:HeadObject` (or
  `s3:GetObject`) on the bucket, for independent verification
- The S3 bucket itself does not exist yet — real-AWS verification of this
  primitive is blocked on that being created first (tracked in `TODO.md`,
  not by this project)
- Testing plan (per discussion with the user): unit tests first, with
  fakes for `EC2API`/`SSMAPI`/a new `S3API` (covering `HeadObject`); then,
  once those pass, a live test against a *throwaway instance launched from
  an existing AMI that already has these backups baked in* — never
  directly against the production instance. This reuses Phase 4
  (Create EC2 Instance from AMI) as a testing tool in its own right, and
  the throwaway instance must be terminated after the test, same cleanup
  discipline as Show/Export Cloud-Init's AMI path
- `DESIGN.md`'s Overview is broadened to describe ongoing instance
  administration and dev/test/deployment workflow support, not just
  EC2/AMI lifecycle management
- `PLAN.md` gets a new Phase 7 ("Backup Archive & Trim"); Phases 7-11 in
  the prior draft are renumbered to 8-12 accordingly

---


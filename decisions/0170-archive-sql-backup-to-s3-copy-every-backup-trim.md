---
id: "0170"
title: "Archive SQL Backup to S3: copy every backup, trim only what is old"
date: "2026-08-27"
status: accepted
kind: decision
trigger: request
project: clasm
phase: "20.62"
supersedes: []
superseded_by: []
relates_to: ["0017", "0135", "0151"]
initiative: ""
session: ""
decisions: ["Upload set is every file the directory listing returns; delete set is (aged ∩ verified present in S3)", "An s3:HeadObject pre-pass skips what is already archived; absent, size-mismatched or errored all fall toward copying", "The pre-pass reuses VerifiedFile, so there is one delete gate and one evidence standard regardless of which run produced the evidence", "The retention prompt is tri-state (blank / 0 / N); promptAgeDays is deleted rather than widened", "The prompt names the instance's EBS volume explicitly rather than mirroring the OpenSearch cleanup wording", "ConfirmDestructive only when files will actually be deleted; plain Confirm when the delete set is empty", "fstrim runs only when something was deleted", "The menu label becomes \"Archive SQL Backups to S3 (and trim local copies)\"; Go identifiers are unchanged"]
tags: []
uuid: "01a04410-2659-7c8a-ad9b-4d65a487a1ba"
origin_host: "MACMINI-RD.local"
---

**Context.** Feature 11 (DR-0017, "Add Backup Archive & Trim as a v1
primitive") was designed as one indivisible act: `backupArchiveAndTrim`
derives a single age-filtered set from the directory listing and uses it
for the upload, the verification and the delete alike. So the menu entry
"Archive SQL Backup to S3" does not archive the SQL backups -- it
archives the subset of them old enough to *delete*. The newest dump,
usually the one taken because a rebuild is coming and the most valuable
thing on the volume, is the one file guaranteed not to reach S3.

Noticed by the user 2026-08-26 in live use, preparing to stand up a new
CaltechAUTHORS instance on uv/granian -- exactly the case the workflow
exists for. The failure is silent: every number in "Archived and deleted
N file(s), freed N bytes" is true, and the report simply never mentions
the files it declined to consider. With nothing old enough the run exits
on "No files match the age threshold. Nothing to do.", which reads as
"there was nothing to archive." Filed as
`archive-all-sql-backups-feature-request.md`; intake conversation and
design addendum (DESIGN.md, same title) both 2026-08-27.

**Decision.** Eight decisions, taken together, all scoped to
`backupArchiveAndTrim`'s orchestration.

1. **Two sets, not one.** The upload set is every file
   `ListBackupFiles` returns, unfiltered. The delete set is `(upload set
   filtered by age) ∩ (confirmed present in S3 at the right size)`.
   Archival is unconditional; trimming is an optional second phase over
   the same listing. A run with nothing old enough to trim copies
   everything and deletes nothing, rather than exiting early.

2. **An `s3:HeadObject` pre-pass classifies the listing first,** so
   already-archived files are not re-uploaded over SSM on every run. New
   `CheckAlreadyArchived(ctx, client, bucket, prefix, files)
   []VerifiedFile` in `backup_verify.go`, using the operator's own
   credentials. `Verified` is true only when `HeadObject` succeeds *and*
   `ContentLength` matches the local size; object absent, size
   different, or the call itself errored all come back false and the
   file joins the upload set.

3. **The pre-pass reuses `VerifiedFile`** rather than introducing a
   parallel type, so the delete gate is one question -- "is this file's
   key verified present?" -- answered from one merged `[]VerifiedFile`,
   with no branch on whether the evidence came from the pre-pass or from
   this run's own upload verification.

4. **The retention prompt is tri-state.** Blank: copy everything, delete
   nothing locally. `0`: copy everything, delete every local file that
   verified. `N`: copy everything, delete verified local files older
   than N days. `promptAgeDays` rejects both blank and `0` and has
   exactly one call site, so it is deleted along with its tests and
   replaced by `promptLocalTrimDays`, returning `(days int, requested
   bool, err error)` -- the shape `promptOpenSearchCleanupDays` already
   uses. `BackupArchiveParams` gains `TrimRequested bool`.

5. **The prompt names the EBS volume explicitly,** even though that
   breaks symmetry with the OpenSearch wording it otherwise aligns with:
   "Delete local backup files on the instance's EBS volume older than
   how many days? (blank to keep all local copies; 0 to delete every
   file successfully archived)".

6. **Confirmation is tiered by what will actually happen.** One
   confirmation still covers the whole run, but it is `ConfirmDestructive`
   (type the instance name) when the delete set is non-empty and plain
   `Confirm` when it is empty.

7. **`fstrim` runs only when something was deleted.**

8. **The menu label becomes "Archive SQL Backups to S3 (and trim local
   copies)".** Go identifiers -- `BackupArchiveAndTrim`,
   `backup_archive.go`, Feature 11's "Backup Archive & Trim" -- are
   unchanged.

**Rationale.** The whole change is one structural move: separate the
question "what should be safe in S3?" (everything) from "what may be
removed from this volume?" (only what is old, and only on evidence).
Every other decision follows from making that separation cheap and hard
to get wrong.

Decision 3 is the one carrying the most weight. Sharing `VerifiedFile`
saves a struct, but the real payoff is that there remains exactly one
code path to deleting a file, and it is still the tool's own independent
`s3:HeadObject` check with the operator's credentials -- preserving the
property DESIGN.md's Security Considerations already claims for Feature
11, that the HeadObject *is* the authorization for the delete rather
than a redundant nice-to-have. Decision 2's single rule then delivers
three behaviours at once: skip what is archived, overwrite on a size
mismatch (`aws s3 cp` overwrites, so no special case is needed), and
fail *toward copying* whenever the evidence is unclear. The asymmetry is
deliberate -- an unnecessary copy costs bandwidth, an unjustified delete
costs a backup.

Decision 5 repeats a lesson rather than a wording. The two prompts
delete different things: OpenSearch's cleanup removes previously-archived
snapshots *in S3*, this one removes local files *on EBS*. DR-0151
clarified the OpenSearch prompt after a real user report about exactly
that ambiguity, which is why it is as long as it is.

Decision 6 makes friction track real risk instead of the menu entry a
run happens to be sitting under. A copy-only run is not destructive and
should not demand the ceremony of one that is. It costs nothing in
testability: `Confirm` and `ConfirmDestructive` both already accept
`WithConfirmIO`.

`FilterByAge` needs no change for the `0` case -- the cutoff becomes
`now` and every second-truncated mtime precedes it, so `0` already means
"everything."

**Rejected alternatives.**

- **Filter the listing to `*.sql`/`*.sql.gz`.** Rejected at intake, and
  the criteria deliberately kept as they are: everything `find -maxdepth
  1 -type f` returns. A stray file getting archived is a smaller
  surprise than a real dump being skipped because of a naming convention
  nothing enforces.
- **Widen `promptAgeDays` to accept blank and `0`.** Rejected: blank and
  `0` are both falsy in a plain `int` return and mean opposite things
  here, so it needs the second return value regardless; with one call
  site, replacing is cheaper than adding a sentinel.
- **Two confirmations, one before the upload and one before the
  delete.** Rejected at intake in favour of one, tiered.
- **No pre-pass; re-upload the whole directory every run.** Simplest
  possible orchestration, rejected on cost: a month of dumps re-sent
  over SSM on every run.
- **A dedicated result type for the pre-pass.** Rejected -- it would
  reintroduce a branch in the delete gate, which is the one place this
  design most wants a single path.
- **Require *this run's own* upload as the evidence for deleting a
  file,** ignoring objects earlier runs put there. Rejected: it is the
  same evidence standard either way, and it would make the pre-pass
  useless for trimming -- a file would be skipped as already archived
  and then kept forever because nothing this run did proved it was
  there.

**Consequences.** `backupArchiveAndTrim`'s orchestration, dry-run
display, summary line and retention prompt change; `backup_verify.go`
gains `CheckAlreadyArchived`; `rdm_menu.go` gains a new label;
`promptAgeDays` and its tests are removed. `ListBackupFiles`,
`FilterByAge`, `VerifyUploads`, `DeleteVerifiedFiles`, `uploadKey`'s
namespacing (DR-0053), the bucket picker and every preflight check are
untouched. This is a rewiring around the existing primitives plus one
new sibling to `VerifyUploads`.

One consequence is genuinely new and worth stating plainly: **a run that
uploads nothing at all can still delete local files**, on the strength
of an object some earlier run put there. Only the run that produced the
evidence differs; the standard does not.

The first run after this ships may be large, since a directory holding a
month of dumps will copy all of them. Subsequent runs are cheap. The dry
run must therefore show the byte total to be uploaded before the
operator confirms, not after.

`DefaultBackupUploadTimeout`'s 30 minutes bounds one file rather than
the batch (`UploadBackupFiles` sends one SSM command per file, DR-0050),
so a larger upload set moves no closer to a ceiling.

Left open for the plan, all presentation-level: whether the dry run
shows two lists or one annotated list; whether already-present files
appear in it at all; whether the pre-pass gets a `startProgressTicker`
heartbeat; whether the pre-pass distinguishes "absent" from "present at
a different size" in its reporting, which the shared `VerifiedFile`
cannot express; and the exact summary wording.

Implementation is test-first, PLAN.md Phase 20.62. "Archive OpenSearch
Snapshot to S3" is out of scope -- its cleanup threshold (DR-0135) has
always been optional and governs S3-side objects, so it does not have
this coupling.

---

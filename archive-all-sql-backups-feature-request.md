# Archive SQL Backup to S3: Copy Everything, Trim Only What's Old — Feature Request

> **2026-08-27: Filed.** Captured from an intake conversation with
> RSDOIEL, before the design/decide/plan cycle has been run. The
> behaviour change itself and the nine intake answers below are settled;
> everything under "Open questions" is not. Treat the shapes proposed
> here as a starting point for that cycle to confirm or revise.

## Motivation

Noticed 2026-08-26 in live use, ahead of standing up a new CaltechAUTHORS
instance on uv/granian. "Archive SQL Backup to S3" does not archive the
SQL backups — it archives *the subset of them old enough to delete*.

That is not what the menu entry says, and not what an operator preparing
for a migration needs. The whole point of running it before rebuilding a
host is to get every dump that exists on that EBS volume safely into S3.
Today the newest dump — usually the most valuable one, and the one taken
specifically because a rebuild is coming — is the one guaranteed *not*
to be copied.

The gap is invisible in normal use: the run reports "Archived and
deleted N file(s), freed N bytes" and every number in it is true. It
just never mentions the files it silently declined to consider. In the
extreme case (no file is old enough) the workflow prints "No files match
the age threshold. Nothing to do." and exits having copied nothing at
all, which reads as "there was nothing to archive."

## Current behaviour

`backupArchiveAndTrim` (`internal/workflow/backup_archive.go`) derives
one set and uses it for all three phases:

```go
allFiles   := ListBackupFiles(...)                    // every file in the directory
candidates := FilterByAge(allFiles, params.AgeDays, time.Now())   // only the old ones
if len(candidates) == 0 { /* "Nothing to do." */ return nil }
→ UploadBackupFiles(candidates) → VerifyUploads(...) → DeleteVerifiedFiles(...)
```

The age threshold is asked as "Age threshold in days," documented as
"of the files in that directory, which are old enough to move to that
bucket." Archival and trimming were designed as one indivisible act.

## Proposed behaviour

Split the one set into two:

- **Upload set** — every file `ListBackupFiles` returns. No age filter.
- **Delete set** — (upload set filtered by age) ∩ (independently
  verified present in S3).

A run with nothing old enough to trim is now a perfectly normal run: it
copies everything and deletes nothing, rather than exiting early.

### Sequence

1. Pick instance, AWS CLI preflight, directory prompt, bucket pick,
   bucket-region resolution, bucket-access preflight — all unchanged.
2. **Retention prompt** (reworded, see below).
3. List all files in the directory.
4. **New: already-archived pre-pass.** `s3:HeadObject` each file's
   destination key with the operator's own credentials.
   - Object absent → upload.
   - Object present, size matches → skip the upload, treat as archived
     and verified.
   - Object present, size differs → upload anyway, overwriting. The
     local file is the newer truth.
5. Dry run showing both sets, and both byte totals.
6. One confirmation covering the whole run (see below).
7. Upload everything not skipped in step 4; verify via `s3:HeadObject`
   as today.
8. Delete the delete set. A file is only ever deleted on the strength of
   a `HeadObject` match — including a match found in step 4 from an
   object some earlier run uploaded.
9. `fstrim` only if something was actually deleted.
10. Summary: copied, skipped-already-present, deleted, bytes freed, plus
    any warnings.

### The retention prompt

Three meanings, matching `promptOpenSearchCleanupDays`'s
`(days int, requested bool, err error)` shape so blank and `0` stay
distinguishable:

| Answer | Meaning |
|---|---|
| blank | Copy everything, delete nothing locally |
| `0` | Copy everything, delete every local file that verified |
| `N` | Copy everything, delete verified local files older than N days |

`promptAgeDays` currently rejects both blank and `0`, so this is a new
prompt function rather than an edit to that one — same reasoning that
gave `promptOpenSearchCleanupDays` its own function.

**Wording needs care.** Aligning on "blank means don't delete" is right,
but the two prompts delete different things: OpenSearch's cleanup
removes *previously-archived snapshots in S3*, this one removes *local
files on the instance's EBS volume*. A 2026-07-29 user report about
exactly this ambiguity is why the OpenSearch prompt is as long as it is.
The SQL prompt must name EBS explicitly. Proposed:

> Delete local backup files on the instance's EBS volume older than how
> many days? (blank to keep all local copies; 0 to delete every file
> successfully archived)

### Menu label

`rdm_menu.go`'s "Archive SQL Backup to S3" no longer describes what
happens — the trim is now a separate, optional phase, and the copy is
unconditional. Proposed: **"Archive SQL Backups to S3 (and trim local
copies)"**. Internally, DESIGN.md Feature 11's "Backup Archive & Trim"
name survives unchanged and becomes accurate for the first time.

## Intake decisions (settled 2026-08-27)

1. **HeadObject pre-pass** before uploading, to skip what is already
   archived. Without it, every run re-uploads the whole directory over
   SSM.
2. **Size mismatch overwrites.** Same key, different size → copy the
   local file over the S3 object.
3. **No filename filter.** "All available SQL backups" keeps its
   existing definition: everything `find -maxdepth 1 -type f` returns
   from the backup directory. Unchanged criteria, deliberately.
4. **`0` is valid** and means copy everything, trim everything verified.
5. **One confirmation** for upload and delete together, not two.
6. **A failed copy of a file that wasn't going to be deleted is a
   non-fatal warning**, surfaced in the summary. Nothing unsafe follows
   from it — the file is simply still only on EBS.
7. **Blank means copy but don't delete**, aligning with the OpenSearch
   index archive.
8. **Update the menu label** for the human reading it.
9. **This document** is the standalone feature request; TODO.md gets a
   pointer to it.

Plus three leans accepted at intake:

- A `HeadObject` match from a *previous* run authorises this run's
  delete. It is the same evidence `VerifyUploads` already treats as the
  authorisation to delete; which run put the object there doesn't change
  what it proves. Consequence worth stating plainly: a run that uploads
  nothing can still delete files.
- **Confirmation friction tracks real risk.** Keep the single
  confirmation, but make it a plain yes/no when the delete set is empty
  and the full `ConfirmDestructive` type-the-instance-name when files
  will actually be removed. A pure copy shouldn't demand the same
  ceremony as a deletion.
- **Skip `fstrim` when nothing was deleted.** Nothing was freed, and
  it's a wasted SSM round trip on a copy-only run.

## What changes, and what doesn't

**Changes:** `backupArchiveAndTrim`'s orchestration, its dry-run
display, its summary line, the retention prompt, the menu label, and the
`BackupArchiveParams` age field's meaning.

**Unchanged:** `ListBackupFiles`, `FilterByAge`, `UploadBackupFiles`,
`VerifyUploads`, `DeleteVerifiedFiles`, `uploadKey`'s
instance-name-prefixed namespacing, the bucket picker, and every
preflight check. This is a rewiring of the orchestration, not a rewrite
of the primitives.

**Not a timeout risk.** `UploadBackupFiles` already runs one SSM command
per file, so `DefaultBackupUploadTimeout`'s 30 minutes is a per-file
bound, not a batch bound. A larger upload set does not push the batch
toward a ceiling — there isn't one.

**First run after this ships may be large.** A directory holding a
month of dumps will copy all of them the first time. Subsequent runs are
cheap because of the pre-pass. The dry run should show the byte total to
be uploaded so the operator sees the size before confirming, not after.

## Open questions for the design cycle

**A per-file SSM-level failure currently aborts the entire run.**
`UploadBackupFiles` returns a hard error on any non-`Success` command
status, distinct from a `FAIL` line *inside* a command (already handled
as a normal per-file outcome). Under the old behaviour that was clearly
right — every file in flight was a delete candidate. Under the new one,
an SSM hiccup while copying one recent file would abort the archival of
the aged files too, which is closer to the spirit of intake decision 6
than to its letter. Lean: record the file as failed, warn, and continue
— the delete phase is independently gated on `HeadObject`, so continuing
cannot delete anything unverified — while still aborting immediately on
context cancellation. Needs a decision before implementation.

Smaller ones: whether the dry run should list the two sets separately or
mark each file with its disposition in one list; whether skipped
already-present files belong in the dry run at all, given they are
discovered by the pre-pass before the operator confirms; and whether the
summary should distinguish "copied" from "already present" or just
report both as archived.

## Testing notes

Test-first, per this project's standing practice. `backup_archive_test.go`'s
existing cases encode the old coupling and will need updating rather than
extending. The cases that matter:

- Nothing old enough → everything uploaded, nothing deleted, no early
  exit, no `fstrim`.
- Blank retention → same, regardless of file ages.
- `0` → everything uploaded, everything verified deleted.
- A file already in S3 at matching size → not uploaded, still deleted if
  aged.
- A file already in S3 at a different size → uploaded, overwriting.
- A recent file failing verification → warning only, nothing deleted.
- An aged file failing verification → left on disk, reported, as today.

## Out of scope

"Archive OpenSearch Snapshot to S3" is not touched. Its cleanup prompt
governs S3-side objects rather than local files and has always been
optional, so it does not have this coupling. If a review of it turns up
something similar, that is its own request.

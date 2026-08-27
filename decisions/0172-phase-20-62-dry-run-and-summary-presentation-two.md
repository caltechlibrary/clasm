---
id: "0172"
title: "Phase 20.62 dry-run and summary presentation: two sections, already-archived as a count except when it is also being deleted"
date: "2026-08-27"
status: proposed
kind: refinement
trigger: plan-review
project: clasm
phase: "20.62"
supersedes: []
superseded_by: []
relates_to: ["0170"]
initiative: ""
session: ""
decisions: ["Dry run shows two labelled sections, To copy and To delete after verification, each with its own count and byte total", "Already-archived files are a count line, except one that is also in the delete set, which stays listed and marked (already in S3)", "The pre-pass gets a startProgressTicker heartbeat", "No absent-vs-size-mismatch distinction in the reporting", "Summary reports copied, already in S3, deleted and bytes freed, then the warning block"]
tags: []
uuid: "01a04432-f840-77c0-a67b-3db6ca070ef5"
origin_host: "MACMINI-RD.local"
---

**Context.** DR-0170 deliberately left five presentation-level questions
unanswered, on the grounds that none of them blocked the plan. Writing
PLAN.md Phase 20.62 forced all five, since the dry run is what the
operator actually reads before confirming, and the confirmation itself
now varies with what the dry run says (DR-0170, decision 6).

**Decision.**

1. **Two labelled sections in the dry run** -- "To copy" and "To delete
   after verification" -- each with its own file count and byte total.
2. **Already-archived files are a count line in the copy section**
   ("N file(s) already in S3, skipped"), not a list -- *except* when
   such a file is also in the delete set, in which case it stays listed
   in the delete section, marked `(already in S3)`.
3. **The pre-pass prints a `startProgressTicker` heartbeat** while it
   runs ("checking which backups are already archived").
4. **No absent-vs-size-mismatch distinction** in any output.
5. **Summary:** `Copied N file(s) (X); M already in S3; deleted K local
   file(s), freed Y.`, followed by the warning block when there is one.

**Rationale.** Decision 1 follows from the two sets answering different
questions: the copy section is informational, the delete section is what
the operator is actually consenting to, and it alone determines whether
the confirmation is `ConfirmDestructive` or a plain `Confirm`.

Decision 2's exception is the one that changed while writing it down.
Suppressing already-archived files to a count is right for the copy
section -- there is no decision to make about them. It is wrong in the
delete section, because such a file will be **deleted this run without
being uploaded this run**, on evidence some earlier run produced. That
is precisely the consequence DR-0170 calls out as genuinely new, and
hiding it behind a count would make the one novel case the least visible
thing on screen.

Decision 3 is a straight application of the lesson behind DR-0156: a
silent pause reads as a hang. The pre-pass is one `HeadObject` per file
with no output of its own, and a directory holding a month of dumps is
long enough to notice.

Decision 4 keeps `VerifiedFile` shared, per DR-0170 decision 3 -- the
type cannot express the distinction, and the operator's actionable
question is copy vs. skip, not why.

**Rejected alternatives.**

- **One list with a per-file disposition column** instead of two
  sections. Rejected: it reads as one homogeneous set of files being
  acted on, which is exactly the conflation this phase exists to undo.
- **Omit already-archived files from the dry run entirely.** Rejected by
  decision 2's exception -- it would hide a real deletion.
- **List every already-archived file in the copy section too,** for
  symmetry. Rejected: on a steady-state run that is the entire directory,
  and it would bury the files actually about to be copied.
- **Extend `VerifiedFile` with a reason field** so the pre-pass can
  report absent vs. size-mismatch. Rejected: it re-introduces a
  distinction the delete gate must then be careful to ignore, for
  reporting nobody asked for.

**Consequences.** `displayBackupDryRun` takes both sets rather than one
and gains section headers; the summary line gains two counts. The
delete section needs the already-archived flag threaded through to it,
which is the only place the merged `[]VerifiedFile`'s provenance is
visible in the UI at all -- everywhere else, deliberately, it is not.

PLAN.md Phase 20.62 work items 4 and 5. No change to any decision in
DR-0170 or DR-0171.

---

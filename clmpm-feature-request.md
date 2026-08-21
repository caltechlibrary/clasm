# clmpm — Feature Request

> **2026-08-21: Filed.** Captured from a design conversation with RSDOIEL,
> before any design/decide/plan cycle has been run. This document exists
> to preserve context for that future cycle — it proposes a shape, not a
> committed design. Treat every "would"/"should" below as a starting
> point for that cycle to confirm or revise, not as a decision already
> made.

## Motivation

Local Invenio RDM development currently runs against demo data. The
`granian-rdm-v14` and `caltechauthors-granian` projects have already
proven that a Multipass VM, provisioned from the same cloud-init used on
AWS, is a fast, cheap local iteration loop for the RDM app layer itself
(see those projects' KB observations, 2026-07-27 through 2026-08-19).
What it doesn't yet have is realistic *data* — clasm's RDM Backup &
Restore domain (`PLAN.md`, Phases 20.48–20.61) can restore a real SQL
dump and OpenSearch snapshot from S3, but only onto an AWS EC2 instance,
reached over SSM.

The ask: be able to take the same production SQL/OpenSearch backups
clasm already archives to S3 and restore them into a local Multipass
instance instead of (or in addition to) an EC2 instance — without
re-deriving the restore edge cases clasm's SSM-based path already paid
for live (gzip-suffix detection, `/var/tmp` vs. tmpfs `/tmp`, terminating
other DB connections before drop/create, OpenSearch index-prefix
conflict detection).

## Proposal

A new tool, **clmpm** (Caltech Library Multipass Manager), built as a
new `cmd/clmpm` binary inside the existing clasm Go module — not a mode
of the `clasm` binary itself, and not a separate repository.

Same module matters mechanically, not just organizationally: clasm's
reusable pieces (`internal/config`, `internal/awsclient`,
`internal/workflow`, `internal/ui`, `internal/tui`, `internal/debuglog`)
are Go `internal` packages, importable only by code inside
`github.com/caltechlibrary/clasm`'s own module tree. A separate
repository could not import them without vendoring or duplicating —
which would mean re-forking the already-debugged restore logic instead
of reusing it.

## What's shared vs. what's new

**Shared as-is, no changes needed:**

- `internal/config` — `~/.clasm`'s `BackupDirectoryRule`/
  `BackupDirectoryFor` and `RDMPostgresRule`/`RDMPostgresConfigFor` both
  match by glob pattern against an *instance name*. A Multipass VM name
  slots into that mechanism unchanged — same config file, same
  pattern-matching, no new schema. `Regions`/`OriginTag` stay meaningless
  to clmpm but harmless to have loaded alongside the fields it does use.
- `internal/awsclient/s3.go`, `credentials.go`, `retry.go` — clmpm still
  needs an S3 client to pull production backups down. It needs none of
  `ec2.go`, `iam.go`, or `ssm.go`.
- `internal/workflow/restore_common.go` (`S3Object`,
  `ListObjectsByPrefix`, `pickS3Object`) — reusable verbatim.
- `internal/ui`, `internal/tui`, `internal/debuglog` — generic
  terminal/menu-shell/logging helpers, no AWS coupling.

**Ported, with the execution channel swapped:**

- The restore script bodies in `restore_sql.go`/`restore_opensearch.go`
  — the actual bash/psql/OpenSearch-REST commands, and every edge case
  fixed against them live (2026-08-18/19, `caltechdata-restore-test`) —
  are reusable content. Only the wrapper that runs them changes: SSM's
  `RunShellCommand`/`WaitForSSMOnline` (`ssm.go`) becomes something like
  `multipass exec <vm> -- sudo bash -c '<script>'` /
  `multipass transfer <local-file> <vm>:<path>`. The design cycle should
  work out how to factor the script-body functions so both the existing
  SSM path (in `clasm`) and the new Multipass path (in `clmpm`) can call
  them without duplication.

**Net new, deliberately thin — not a port of clasm's Compute domain:**

- Multipass instance lifecycle: `multipass list`, `multipass info`,
  `multipass launch --cloud-init <file> --name --cpus --memory --disk
  <image>`, `start`/`stop`/`delete`. Multipass has no equivalent to AMIs,
  EBS volumes, IAM instance profiles, key pairs, launch templates, or
  instance-type/arch/AZ checks — none of clasm's Compute-domain machinery
  for those concepts applies, and none of it should be ported.
- One consequence worth calling out: clmpm's only AWS dependency is
  read-only S3 access for backups. No IAM roles, no SSM agent, no
  per-instance AWS setup of any kind.

## Proposed UX shape

Same domain-picker feel as clasm (`domain_menu.go`'s pattern — a list of
domains dispatching to a domain-scoped submenu), reused as-is, with a
shorter, shallower domain list:

- **Compute** — manage Multipass instances. Show instances, show
  instance detail (with start/stop/delete hanging off the detail view,
  the way clasm's own instance detail carries its actions, rather than
  as separate top-level menu entries), create an instance from an
  existing cloud-init YAML file (e.g. `granian-rdm-v14/cloud-init-
  multipass.yaml`). Modeled on clasm's Compute domain but pared down —
  fewer options, matching what Multipass actually needs.
- **RDM Restore** — browse/pick a SQL dump or OpenSearch snapshot from
  S3 (read-only) and restore it into a chosen Multipass instance
  (read-write).
- **Configuration** — edits the same `~/.clasm` file clasm reads,
  scoped to the fields clmpm actually uses (`BackupDirectories`,
  `OpenSearchBackupDirectories`, `RDMPostgresConfig`).

## Settled constraints (from the design conversation)

- S3 access from clmpm is read-only.
- Multipass instances are read-write.
- Same `~/.clasm` config file as clasm, not a separate `~/.clmpm`.
- clmpm is its own binary (`cmd/clmpm`), not a flag/mode of `clasm`.

## Open questions for the design/decide/plan cycle

- **v1 scope** — SQL restore only first (it's the path already
  live-verified against real CaltechDATA production data, 2026-08-19),
  with OpenSearch restore added once the executor-swap pattern is
  proven? Or both from the start, since the script logic for both
  already exists?
- **Executor abstraction** — exactly how to factor the SSM-specific
  execution out of `restore_sql.go`/`restore_opensearch.go`/
  `rdm_postgres_config.go` (which discovers the Postgres container name
  live via `docker ps` over SSM today) so clasm's existing AWS path
  keeps working unchanged while clmpm gets a Multipass path, without a
  disruptive refactor of code already shipped and real-AWS-verified.
- **Compute detail-view action set** — confirm start/stop/delete belong
  on the instance-detail view; decide whether "create from cloud-init
  YAML" only takes a path to an existing file, or also helps author/
  validate one.
- **Instance/VM naming conventions** — how `RDMPostgresRule` patterns
  (already keyed on instance name) should be authored so AWS instance
  names and Multipass VM names can coexist unambiguously in the same
  `~/.clasm` rule lists.

## References

- `TODO.md`, "Requested features" — "It would be nice to be able to
  Restore SQL and OpenSearch indexes into a Multipass instance used for
  development and debugging RDM." The tracked, one-line version of this
  feature request.
- `PLAN.md`, Phases 20.48–20.61 — clasm's RDM Backup & Restore domain,
  the pattern this proposal ports.
- `restore_common.go`, `restore_sql.go`, `restore_opensearch.go`,
  `ssm.go`, `internal/config/config.go` — the specific code surveyed
  during this conversation.
- `granian-rdm-v14/cloud-init-multipass.yaml` — the existing convention
  (`/opt/rdm_sql_backups`, `/opt/rdm_opensearch_backups`, both baked into
  the VM and wired into `docker-services.yml`'s OpenSearch `path.repo`)
  this proposal's restore path would target.
- KB observations, 2026-07-27 through 2026-08-19 (`granian-rdm-v14`,
  `caltechauthors-granian` projects) — Multipass-as-local-dev-loop
  precedent and known environment quirks (`multipassd` launchd
  registration, `multipass stop` convergence retries).

---
id: "0047"
title: "Add a `~/.awsops` YAML config file for awsops' own operational settings"
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
uuid: "757ec4b5-4e3e-4f31-a59c-dcdb4c802a49"
origin_host: "MACMINI-RD.local"
---

**Context.** Narrowing configured regions (above) meant editing a Go
source constant and rebuilding. That's fine for a one-off, but the
conversation that led to it -- and the mention of S3 bucket settings the
S3 domain will eventually need -- made clear this will keep happening as
the tool grows into more domains (S3, CloudFront, Key Management), each
likely wanting its own site-specific defaults. Rebuilding the binary to
change a setting doesn't scale past the first one or two changes.

**Decision.** `awsops` now reads its own operational settings from an
optional YAML file at `~/.awsops` (overridable with `-config <path>`),
parsed with `gopkg.in/yaml.v3`. Scope is deliberately narrow: this file
covers only settings `awsops` itself needs to decide *how it operates*
(starting with which regions to manage) -- it explicitly does **not**
cover AWS credentials, profiles, or SSO configuration, which remain
entirely the AWS SDK's own responsibility via the standard chain
(`~/.aws/credentials`, `~/.aws/config`, environment variables) exactly
as today. `internal/config.Config` is a single flat struct with one
YAML-tagged field per setting (`Regions []string` today); the file is
optional (missing = built-in defaults, `[us-west-1, us-west-2]` for
regions), a field left unset in an otherwise-valid file falls back to
its own default independently (not all-or-nothing), and a malformed
file is a hard error, not a silent fallback.

**Rationale.**
- Directly what was asked: config that will "grow to have more fields
  over time," built for that now rather than retrofitted later, while
  keeping today's actual change (regions) the only field that does
  anything yet.
- The `~/.aws` boundary matters: conflating "which regions does awsops
  manage" with "which AWS account/credentials is this" would blur two
  genuinely different concerns and risk awsops silently reading or
  fighting with the AWS CLI's/SDK's own config files.
- Per-field defaults (not all-or-nothing) mean a config file only ever
  needs to state what's actually being overridden -- a two-line
  `regions:` file today doesn't need to also restate every future
  setting just because the file exists at all.
- No versioning/migration machinery: this is a single-operator-
  maintained local dotfile, not a multi-tenant service config pushed to
  many machines by many people -- schema evolution here is "add a field
  to the struct," not a problem that needs solving in advance.

**Rejected alternatives.**
- *JSON via the standard library, no new dependency* -- raised as the
  no-new-dependency alternative; explicitly declined in favor of YAML
  for hand-editability, with `gopkg.in/yaml.v3` approved for this
  specific use.
- *Environment variables only* -- rejected as not scaling to structured/
  list settings (a region list today; likely nested settings later, e.g.
  per-domain defaults) the way a YAML file naturally does.
- *A versioned/migrating config schema* -- rejected as solving a problem
  this tool doesn't have: there's one file, on one machine, maintained
  by the person running the tool, not a config format needing backward-
  compatibility guarantees across independently-upgrading consumers.

**Consequences.**
- New dependency: `gopkg.in/yaml.v3`.
- New package: `internal/config` (`Config`, `DefaultRegions`,
  `DefaultPath`, `Load`).
- `internal/awsclient/regions.go` and `regions_test.go` removed --
  region-list ownership moves entirely to `internal/config`;
  `internal/awsclient/client_test.go`'s sanity test now iterates a
  small test-local region literal instead of a shared package var,
  decoupling it from wherever the "real" list lives.
- `cmd/awsops/main.go` gains a `-config` flag (default
  `config.DefaultPath()`), loads the config early (failing fast on a
  parse error, matching every other startup failure mode), and uses
  `cfg.Regions` everywhere `awsclient.Regions` was read before.

---


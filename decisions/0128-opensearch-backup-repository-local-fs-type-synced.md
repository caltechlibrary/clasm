---
id: "0128"
title: "OpenSearch backup repository: local `fs` type synced by clasm, not direct-to-S3 via the repository-s3 plugin"
date: "2026-07-28"
status: accepted
kind: decision
trigger: request
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "1eb8d24c-2255-43db-a2e3-9032e36a9965"
origin_host: "MACMINI-RD.local"
---

**Context.** OpenSearch snapshots need a registered repository
somewhere. A direct-to-S3 repository (the `repository-s3` plugin plus
an IAM role on the container) would skip local staging entirely, but
requires giving the OpenSearch container outbound S3 access and an IAM
role it doesn't have today.

**Decision (user's explicit call, between the two options presented).**
`fs` repository on local disk (`/opt/rdm_opensearch_backups`), with
clasm syncing that directory to S3 over the same SSM path already
proven for SQL backups. Rejected direct-to-S3, since it requires no
IAM/plugin changes to the running production containers, matching how
SQL backups already work.

**Consequences.** Requires OpenSearch's `path.repo` setting plus a
bind-mounted volume, which the stock `cookiecutter-invenio-rdm`
`docker-services.yml` template doesn't configure by default (confirmed
against the real master-branch template) -- baked into
`granian-rdm-v14`'s `cloud-init.yaml`/`cloud-init-multipass.yaml` for
new instances; CaltechAUTHORS/CaltechDATA still need a one-time manual
retrofit (DESIGN.md, "Repository prerequisite: path.repo").

---


---
id: "0112"
title: "Always gzip-compress user-data before base64-encoding it"
date: "2026-07-22"
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
uuid: "88169514-2aae-463d-ab32-ea6674295975"
origin_host: "MACMINI-RD.local"
---

**Context.** Live testing hit `InvalidUserData.Malformed: User data is
limited to 16384 bytes` creating a launch template from
`invenio-rdm-13-granian-init.yaml` (16976 bytes raw, already over the
limit before clasm even touches it -- `invenio-rdm-13-gunicorn-init.yaml`
is in the same boat at 16996 bytes). clasm currently just
base64-encodes the raw cloud-init text as-is at every write site
(`Launch`, `buildRequestLaunchTemplateData`,
`createLaunchTemplateVersion`) with no compression. cloud-init itself
auto-detects gzip-compressed user-data (checks the gzip magic bytes)
and transparently decompresses it before running -- a standard,
documented AWS/cloud-init pattern, not a hack. Gzipping the actual
16976-byte file (confirmed via plain `gzip -c | wc -c`, not assumed)
brings it to 5628 bytes, comfortably under the limit.

**Decision.** Two new shared helpers (`userdata_gzip.go`):
`encodeUserData(plainText string) string` gzip-compresses then
base64-encodes, used at all three write sites; `decodeUserData(encoded
string) (string, error)` base64-decodes then checks for the gzip magic
bytes (`0x1f 0x8b`) -- gunzips if present, returns the raw bytes
as-is otherwise -- used at all four read sites
(`ShowCloudInitFromInstance`, `syncLaunchTemplate`'s existing-version
read, `show_launch_template.go`'s two-version diff). The as-is fallback
is what keeps this backward compatible with every already-existing
instance/template whose user-data was written before this change, in
plain (non-gzip) form -- both old and new content read correctly
without needing to know which one a given resource has.

**Rejected alternative.** *Only gzip when the raw content is close to
or over the limit* -- rejected in favor of always gzipping: cloud-init
handles both forms identically, so there's no behavioral reason to
special-case small files, and a size-threshold decision is one more
thing to get wrong (and test) for no benefit. The only cost is a minor
readability regression for someone manually inspecting raw user-data
outside clasm (`aws ec2 describe-instance-attribute` returns gzip'd
bytes now, not readable YAML directly) -- accepted, since clasm's own
"Show/export cloud-init" already exists specifically to make this
readable again through the tool.

**Consequences.** `encodeUserData` has no error return -- gzip-writing
to an in-memory `bytes.Buffer` cannot fail in practice, so there's
nothing meaningful to propagate (avoids threading an error path for a
scenario that can't happen). `decodeUserData` does return an error
(malformed base64 or a corrupt/truncated gzip stream both remain
genuinely possible on read).

---


---
id: "0058"
title: "Add public-key format validation for Import Key Pair"
date: "2026-07-08"
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
uuid: "221a1073-4e53-4bca-bcf0-6f80e009a23e"
origin_host: "MACMINI-RD.local"
---

**Context.** DESIGN.md Feature 15 (Import Key Pair) specifies that a
local `.pub` file should be "read and validated... fail locally with a
clear message rather than surfacing AWS's raw `InvalidKeyPair.Format`
error." No existing helper in this codebase validates SSH public-key
*format* -- `isReadableFile` (used elsewhere for private-key-path
detection) only checks the file opens, and the cloud-init "@file" loader
just reads raw bytes with no format check at all.

**Decision.** New `validatePublicKeyFile` (`internal/workflow/keypair_import.go`)
checks the file is readable, then that its first whitespace-delimited
field is a recognized SSH key-type token (`ssh-ed25519`, `ssh-rsa`,
`ecdsa-sha2-nistp256/384/521`) and that a second field (the base64 key
body) is present. Not a full RFC4253 parse -- just enough to catch "this
obviously isn't a public key" (a private key pasted by mistake, an empty
file, random text) locally before ever calling AWS.

**Rationale.**
- Matches this project's established preference for local, actionable
  validation over letting AWS's own error surface raw (the same
  reasoning behind the AMI-name-length check, the security-group-ID
  format check, etc.) -- see the Bash-to-Go retarget's core motivation.
- Full cryptographic parsing (base64-decoding and structurally validating
  the key blob per RFC4253) would catch more malformed inputs but adds
  real complexity for a false-input class (a resembles-but-isn't-a-key
  file) that hasn't actually been observed as a real failure the way the
  Bash version's three real bugs were -- not worth the added surface
  area pre-emptively.

**Rejected alternatives.**
- *Full RFC4253 key-blob parsing* — rejected per the rationale above;
  revisit if a real malformed-but-prefix-matching file is ever seen in
  practice.
- *No local validation, let `ec2:ImportKeyPair` reject it* — rejected;
  DESIGN.md explicitly calls for local validation here, and AWS's own
  `InvalidKeyPair.Format` error is not actionable on its own.

---


---
id: "0028"
title: "Support creating a new key pair from within awsops"
date: "2026-07-01"
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
uuid: "0101846a-9e4e-4b2c-acf7-00b10cb93630"
origin_host: "MACMINI-RD.local"
---

**Context.** The user asked "what is Key pair name" while testing the
Key pair name prompt, and once it was explained (the name of an
already-registered AWS key pair used to install its public half on the
new instance, not a local file path), asked to be able to create a new
one from inside awsops instead of leaving the tool: "I don't like
re-using keys... this seems like an option that is needed." AWS's
`ec2:CreateKeyPair` generates a new key pair and returns the private key
material exactly once — AWS never stores or re-displays it — so
awsops has to capture and save it immediately or it's gone.

**Decision.**
- At the existing Key pair name prompt, typing `new` (case-insensitive)
  instead of a name switches into a small sub-flow: prompt for a new
  key pair's name, call `ec2:CreateKeyPair` (`KeyType=ed25519`,
  `KeyFormat=pem`), save the returned private key to
  `~/.ssh/<name>.pem` with `0600` permissions (creating `~/.ssh` first
  if it doesn't exist), print the saved path, and use that name as the
  launch's key pair.
- A name collision (`InvalidKeyPair.Duplicate`) re-prompts for a
  different name rather than failing the whole launch; any other
  `CreateKeyPair` error (e.g. missing IAM permission) is returned as a
  real error, same as any other unrecoverable AWS failure elsewhere in
  the tool.
- Default key type is ED25519 (AWS's own current recommendation for
  new key pairs — smaller, faster than RSA) rather than RSA.
- `internal/awsclient`'s `-debug` logging decorator for `CreateKeyPair`
  does not use the shared generic `logAWSCall` helper — its response
  carries the private key material, which must never be written to the
  debug log even though everything else the tool does is logged in
  full. The wrapper logs everything except `KeyMaterial`, which it
  replaces with a fixed redaction marker.

**Rationale.** Inline ("type `new`") beats a separate main-menu action
because the natural point to decide "I want a fresh key for this one"
is exactly when you're being asked for a key pair name — a separate
menu item would mean leaving the launch flow, remembering the name you
picked, then re-entering the flow to use it. Saving straight to
`~/.ssh/` with correct permissions means the key is immediately usable
(`ssh -i ~/.ssh/<name>.pem ...`) without a manual `chmod` step.

**Trade-off.** awsops now writes files outside its own working
directory (`~/.ssh`) for the first time. Accepted: this is the
standard, expected location for SSH private keys, and the alternative
(printing the key material to the terminal for the operator to save
themselves) risks losing an unrecoverable secret in scrollback.

---


---
id: "0127"
title: "SSH connection info: guess the key path (only if it exists on disk) and the login username (Canonical-owned -> ubuntu, else ec2-user)"
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
uuid: "b7abc212-6004-4bbf-a068-bdc07efea7cd"
origin_host: "MACMINI-RD.local"
---

**Context.** TODO.md requested feature: show the SSH command to
connect to a just-launched/started instance. `displayConnectionInfo`
already prints `ssh ec2-user@<ip>`, but never references the actual
private key (`-i`), and hardcodes "ec2-user" even though this tool's
own curated/official AMI list is all Ubuntu. See DESIGN.md, "SSH
Connection Info: Key Path + Username Guess", PLAN.md Phase 20.47.

**Decision (user's explicit call, from three options): add `-i <key
path>`, and guess the username from the AMI, falling back to
"ec2-user" when unknown** -- rejected keeping "ec2-user" hardcoded
always, and rejected always showing "ubuntu" unconditionally (account-
owned custom AMIs aren't guaranteed to be Ubuntu, so an unconditional
"ubuntu" would just trade one wrong hardcoded guess for another).

**Key path:** guessed as `sshKeyDir()` (`~/.ssh`) + the instance's own
key pair name + `.pem` -- exactly where `createKeyPair` saves a newly
created key pair's private key material. Shown only if `os.Stat`
confirms that exact file exists; an imported key pair
(`keypair_import.go`) only ever registers a `.pub` file with AWS, so
its private key's real location isn't knowable and presenting a
guessed path confidently would be actively misleading (a copy-pasted
command that fails with a confusing "Permission denied (publickey)"),
worse than not guessing.

**Username:** a live, best-effort `ec2:DescribeImages` call (new
`sshUsernameForImage`) checks the launched AMI's `OwnerId` against
`ubuntuAMIOwnerID` (the same well-known Canonical account ID
`official_ubuntu_amis.go` already uses) -- "ubuntu" on a match,
"ec2-user" otherwise (including on a DescribeImages error, or an AMI
whose owner can't be determined). Checking `OwnerId` rather than
matching against `curatedUbuntuReleases`' specific name patterns covers
every Canonical Ubuntu AMI, not just this tool's own curated releases,
and works even for Create Instance from Launch Template, which never
resolves an `inventory.Image` at all.

**No new parameters threaded through the three call sites** (`Launch`
via `runLaunch`, `createInstanceFromLaunchTemplate`,
`startEC2Instance`). Both `KeyName` and `ImageId` are already present
directly on the raw SDK `types.Instance` every caller already has in
hand -- only `displayConnectionInfo`'s own signature widens (`ctx`, an
`awsclient.EC2API`), not `LaunchInstanceParams` or any exported
workflow signature.

---


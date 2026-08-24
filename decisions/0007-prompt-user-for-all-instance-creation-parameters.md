---
id: "0007"
title: "Prompt user for all instance creation parameters"
date: "2026-06-30"
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
uuid: "20fceb46-14a0-4624-a825-7b085bdaf236"
origin_host: "MACMINI-RD.local"
---

**Context.** When creating an EC2 instance from an AMI, we need values for instance-type, key-name, security-group-ids, subnet-id, and other optional parameters. We must decide on the default behavior.

**Decision.** **Prompt the user for all required parameters interactively, with no hardcoded defaults.**

**Rationale.**
- Maximum flexibility for users with different requirements
- Prevents launching instances with inappropriate defaults
- Educational: helps users understand what's required for instance creation
- Reduces risk of launching instances in wrong subnet/security group

**Rejected alternatives.**
- *Hardcoded sensible defaults* — defaults may not be appropriate for all use cases; could lead to instances in wrong network configuration
- *Config file with overrides* — adds complexity; users may not have config files set up; harder to use across different projects

**Consequences.**
- More interactive steps required from user
- Need validation for each parameter
- Should provide lists of available options (key pairs, security groups, subnets) to help user choose
- May add helper to show "common" instance types with descriptions

---


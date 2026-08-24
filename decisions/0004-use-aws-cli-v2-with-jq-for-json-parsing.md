---
id: "0004"
title: "Use AWS CLI v2 with jq for JSON parsing"
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
uuid: "26779a17-1323-4a2d-9b69-6a83b8ee6493"
origin_host: "MACMINI-RD.local"
---

**Context.** We need to interact with AWS APIs and parse their JSON output in Bash.

**Decision.** **Use AWS CLI v2 with jq for JSON parsing.**

**Rationale.**
- AWS CLI v2 is the current standard, maintained by AWS
- jq is the de facto standard for JSON parsing in shell scripts
- Both are likely already installed in environments using AWS
- Provides full access to AWS API functionality

**Rejected alternatives.**
- *AWS CLI v1* — deprecated, no longer maintained
- *aws-shell* — not suitable for scripting
- *Boto3 (Python)* — requires Python, not pure Bash
- *Custom JSON parsing with grep/sed* — fragile, error-prone

**Consequences.**
- Script requires AWS CLI v2 and jq as dependencies
- Need to check for these dependencies on startup
- Error messages should guide users to install missing tools

---


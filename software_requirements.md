# AWS Tools — Software Requirements

## Overview

This document lists the software dependencies for building and running
`awsops`, the interactive Go CLI in this repository.

---

## To Build From Source

### 1. Go

- **Version:** 1.26 or higher
- **Purpose:** compiles the `awsops` binary
- **Check:** `go version`
- **Install:** https://go.dev/dl/

### 2. Git

- **Version:** 2.x
- **Purpose:** version control
- **Check:** `git --version`

Build with:

~~~shell
git clone https://github.com/caltechlibrary/awstools
cd awstools
make
make test
make install
~~~

---

## To Run `awsops`

No runtime dependencies beyond the compiled binary itself -- it's a
single static Go binary that calls AWS directly via aws-sdk-go-v2 (the
`aws` CLI is not required on the machine running `awsops`).

### AWS Credentials

Resolved via the AWS SDK's default credential chain -- one of:

- `~/.aws/credentials` / `~/.aws/config` (`aws configure`)
- Environment variables (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`,
  `AWS_DEFAULT_REGION`)
- AWS SSO
- An IAM role (EC2 instance profile, ECS task role, etc.)

### Required IAM Permissions

- **EC2:** `Describe*`, `RunInstances`, `StartInstances`,
  `StopInstances`, `TerminateInstances`, `CreateImage`,
  `DeregisterImage`, `CreateTags`, `DeleteTags`,
  `DescribeInstanceAttribute`, `DescribeVolumes`
- **SSM:** `SendCommand`, `GetCommandInvocation`,
  `DescribeInstanceInformation`
- **S3:** `HeadBucket`, `HeadObject`, `PutObject`, `ListBucket`,
  `GetBucketLocation` (for Backup Archive & Trim)
- **IAM:** `ListInstanceProfilesForRole`, `CreateInstanceProfile`,
  `AddRoleToInstanceProfile`, `ListRoles` (for the instance-profile
  pick-or-create step)

See [DESIGN.md](DESIGN.md), "Assumptions" for the complete list.

### On the Target EC2 Instance (optional features)

Two Compute features reach the target instance via SSM and need these
installed there -- not on the machine running `awsops`:

- **SSM Agent**, running and online -- required for cloud-init-status
  checking and Backup Archive & Trim. Both features degrade gracefully
  (skip cleanly, not an error) if SSM isn't available on the instance.
- **AWS CLI v2** on the instance -- required only for Backup Archive &
  Trim's S3 upload step. awsops checks for this immediately after
  picking the instance and aborts with a clear, actionable error naming
  the instance if it's missing, rather than letting every subsequent
  upload silently fail (see [DECISIONS.md](DECISIONS.md), "Preflight
  check: AWS CLI availability before Backup Archive & Trim").

---

## Optional (maintainers / documentation)

- **CMTools** >= 0.0.46 -- regenerates `version.go`, `about.md`,
  `CITATION.cff`, and the installer scripts from `codemeta.json`
- **GNU Make** >= 3.8 -- runs the Makefile targets above
- **Pandoc** >= 3.9 -- builds this repo's static documentation site
  (`make website`)

---
title: clasm
abstract: |-
  Interactive Go TUI (clasm) for administering Caltech Library DLD's AWS EC2 instances, AMIs, launch templates, and key pairs, plus S3 buckets and backup archives, IAM roles/instance profiles/policies, and Invenio RDM's own SQL/OpenSearch backup and restore lifecycle, with cross-resource tag management and cloud-init inspection.
authors:
  - family_name: Doiel
    given_name: R. S.
    id: https://orcid.org/0000-0003-0900-6903



repository_code: https://github.com/caltechlibrary/clasm
version: 0.0.6

operating_system:
  - POSIX

programming_language:
  - Go >= 1.26.4


date_released: 2026-08-19
---

About this software
===================

## clasm 0.0.6

Fixes a recurring 'InvalidUserData.Malformed: User data is limited to 16384 bytes' error on large cloud-init files: gzip compression now uses maximum compression, and user-data is checked against AWS's limit before any API call, failing with a clear, actionable clasm-side error (stating the compressed size and the overage) instead of AWS's opaque 400. Fixes two related bugs in the cloud-init file picker: a bare 'q' now cancels (matching every other picker's convention, instead of being treated as a literal filename), and cancelling anywhere in Sync Cloud-Init YAML to a Launch Template now correctly returns to the Compute menu instead of exiting clasm entirely. Adds a new action, Modify Launch Template's Instance Type / EBS Root Volume Size, creating a new launch template version with both overrides in one step (never auto-promoted, matching Sync); if the newly chosen instance type's CPU architecture doesn't match the template's current AMI (e.g. switching a Graviton/arm64 template to an x86_64 instance type), it prompts for a compatible replacement AMI automatically. Finally, the connection info shown after launching or starting an instance now includes a ready-to-use '-i <path>' key reference (guessed from where clasm itself saves a key pair's private key, shown only when that file actually exists) and guesses the SSH login username ('ubuntu' for this tool's own curated/official Ubuntu AMIs, 'ec2-user' otherwise) instead of always hardcoding 'ec2-user'. Adds a new RDM Backup & Restore domain (a 7th top-level menu) consolidating Invenio RDM's own backup/restore lifecycle: Generate SQL Backup triggers a pg_dump directly via SSM with no pre-installed script required; Archive SQL Backup to S3 (relocated here from Compute) and a new Archive OpenSearch Snapshot to S3 (via OpenSearch's native snapshot API, with app-managed S3-side cleanup) upload dated backups; and two new restore actions -- Restore SQL Backup from S3 and Restore OpenSearch Snapshot from S3 (the latter safely deleting any conflicting indices before restoring) -- bring either kind of backup back onto a target instance, both real-AWS-verified end to end against real production data. Long OpenSearch snapshot/restore waits and any other long-running remote command now report visible progress instead of leaving the terminal looking hung, and a real reliability bug is fixed where AWS's own SSM execution silently capped any long-running remote command at one hour regardless of clasm's own configured timeout -- this could truncate a legitimate multi-hour restore without any clear error. Several smaller fixes round out this release: Generate SQL Backup no longer auto-chains into Archive SQL Backup; Delete Role's error message and two new IAM actions (Remove Role from Instance Profile, Delete Instance Profile) now give the actual remedy when a role can't be deleted because it's still attached to an instance profile; Associate/replace IAM instance profile handles an already-existing profile name gracefully instead of trapping the operator in a confusing loop; newly created instances/AMIs now tag correctly per the fleet's lowercase 'project' tag convention; and Configure clasm edits (backup directories, Postgres container overrides, the Origin tag) take effect immediately instead of requiring a restart.

## Authors

- [R. S. Doiel](https://orcid.org/0000-0003-0900-6903)






Interactive Go TUI (clasm) for administering Caltech Library DLD's AWS EC2 instances, AMIs, launch templates, and key pairs, plus S3 buckets and backup archives, IAM roles/instance profiles/policies, and Invenio RDM's own SQL/OpenSearch backup and restore lifecycle, with cross-resource tag management and cloud-init inspection.


- [Code Repository](https://github.com/caltechlibrary/clasm)
  - [Issue Tracker](https://github.com/caltechlibrary/clasm/issues)

## Programming languages

- Go >= 1.26.4


## Operating Systems

- POSIX


## Software Requirements

- Go >= 1.26
- CMTools >= 0.0.46
- Pandoc >= 3.9


## Software Suggestions

- GNU Make >= 3.8



---
title: clasm
abstract: |-
  Interactive Go TUI (clasm) for administering Caltech Library DLD's AWS EC2 instances, AMIs, launch templates, and key pairs, plus S3 buckets and backup archives, with cross-resource tag management, cloud-init inspection, and backup archival to S3.
authors:
  - family_name: Doiel
    given_name: R. S.
    id: https://orcid.org/0000-0003-0900-6903



repository_code: https://github.com/caltechlibrary/clasm
version: 0.0.2

operating_system:
  - POSIX

programming_language:
  - Go >= 1.26.4


date_released: 2026-07-20
---

About this software
===================

## clasm 0.0.2

Adds EC2 Launch Templates and a new top-level Tag Management domain; every interactive screen is now full-height and live-resizing. Launch Templates: create/show/sync/promote/delete, built directly from cloud-init YAML, with IMDSv2 enforced by default, plus version history and a scrollable version-to-version diff. Tag Management: a 4th top-level domain (alongside Compute, Key Management, and S3) for managing or listing tags across EC2 instances, AMIs, launch templates, key pairs, and S3 buckets from one place, distinct from Compute's existing per-resource tag editor; S3 bucket tagging is a transparent read-modify-write over PutBucketTagging/GetBucketTagging/DeleteBucketTagging. Full-height Menu tier: every huh.Select-based menu (domain picker, Compute/Key Management/S3/Tag Management) now fills and live-resizes to the terminal, matching the Picker/List/Manager tiers. Real-AWS-verified bug fixes found via live usage: a context-cancellation bug that broke launching an instance from a launch template; a launch template version selector that AWS rejected outright when given a "v"-prefixed version number; and a Manage Tags screen where choosing "Show tags" appeared to do nothing because the full-height menu scrolled the tag listing out of view before it could be read. All prior v0.0.1 functionality (EC2/AMI lifecycle management, Key Management, the S3 domain, cloud-init inspection, and Backup Archive & Trim) is unchanged and remains real-AWS verified. CloudFront remains someday/maybe, undesigned-for-now.

## Authors

- [R. S. Doiel](https://orcid.org/0000-0003-0900-6903)






Interactive Go TUI (clasm) for administering Caltech Library DLD's AWS EC2 instances, AMIs, launch templates, and key pairs, plus S3 buckets and backup archives, with cross-resource tag management, cloud-init inspection, and backup archival to S3.


- [Code Repository](https://github.com/caltechlibrary/clasm)
  - [Issue Tracker](https://github.com/caltechlibrary/clasm/issues)

## Programming languages

- Go >= 1.26.4


## Operating Systems

- POSIX


## Software Requirements

- Go >= 1.26
- CMTools >= 0.0.46


## Software Suggestions

- GNU Make >= 3.8
- Pandoc >= 3.9



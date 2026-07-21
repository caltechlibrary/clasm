---
title: clasm
abstract: |-
  Interactive Go TUI (clasm) for administering Caltech Library DLD's AWS EC2 instances, AMIs, launch templates, and key pairs, plus S3 buckets and backup archives, with cross-resource tag management, cloud-init inspection, and backup archival to S3.
authors:
  - family_name: Doiel
    given_name: R. S.
    id: https://orcid.org/0000-0003-0900-6903



repository_code: https://github.com/caltechlibrary/clasm
version: 0.0.3

operating_system:
  - POSIX

programming_language:
  - Go >= 1.26.4


date_released: 2026-07-21
---

About this software
===================

## clasm 0.0.3

Bug fix release. Cancelling ('q') the S3 bucket picker inside Backup Archive & Trim exited the whole program instead of returning to the previous menu -- a regression, since cancelling the instance picker one step earlier in the same workflow already backed out correctly. backupArchiveAndTrim's bucket-selection step now maps that cancellation the same way, with a regression test covering it. Also improves every Menu-tier huh.Select picker's on-screen hint (bucket/AMI/key-pair/launch-template selection, and every domain/action menu) to mention '/' to filter alongside 'q' to cancel/go back/exit, so it's clear before typing that a bare 'q' quits rather than filters; the hint wording is now centralized as shared constants so it can't drift out of sync again. No functional changes beyond these two fixes -- all v0.0.2 functionality is unchanged.

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



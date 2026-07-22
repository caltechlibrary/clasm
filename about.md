---
title: clasm
abstract: |-
  Interactive Go TUI (clasm) for administering Caltech Library DLD's AWS EC2 instances, AMIs, launch templates, and key pairs, plus S3 buckets and backup archives, with cross-resource tag management, cloud-init inspection, and backup archival to S3.
authors:
  - family_name: Doiel
    given_name: R. S.
    id: https://orcid.org/0000-0003-0900-6903



repository_code: https://github.com/caltechlibrary/clasm
version: 0.0.4

operating_system:
  - POSIX

programming_language:
  - Go >= 1.26.4


date_released: 2026-07-22
---

About this software
===================

## clasm 0.0.4

Adds configurable EBS root volume size: set the size when creating an instance or launch template (instead of always inheriting the AMI's default), and a new 'Resize instance's root volume' menu entry that grows a running instance's EBS volume and automates the OS-level partition/filesystem growth via SSM, falling back to printed manual instructions whenever that automation can't proceed safely. Also a bug fix, found via live testing of the above: any status or error text printed immediately before a domain menu's next full-height selection screen was getting wiped from view before it could be read -- affected every dispatched action's own output (success or failure) and every refresh error, across all four domains (Compute, S3, Key Management, Tag Management). Every menu loop now pauses for an explicit Enter before redrawing, so operators can actually read what an action reported.

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



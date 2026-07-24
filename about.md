---
title: clasm
abstract: |-
  Interactive Go TUI (clasm) for administering Caltech Library DLD's AWS EC2 instances, AMIs, launch templates, and key pairs, plus S3 buckets and backup archives, with cross-resource tag management, cloud-init inspection, and backup archival to S3.
authors:
  - family_name: Doiel
    given_name: R. S.
    id: https://orcid.org/0000-0003-0900-6903



repository_code: https://github.com/caltechlibrary/clasm
version: 0.0.5

operating_system:
  - POSIX

programming_language:
  - Go >= 1.26.4


date_released: 2026-07-24
---

About this software
===================

## clasm 0.0.5

Adds a fifth domain, IAM: browse Roles/Instance Profiles/Policies (each showing a config-driven 'Origin' tag's value, or '(unset)'), a read-only guard on permission-changing actions for anything not recognized as DLD-owned (tagging itself is always allowed), a detail view (trust policy, attached policies, tags, SSM-capability, cross-references), five curated per-use-case role/policy creation templates (Static Website, RDM Repository, Bridge Service, Patron-Facing Service, Data Processing), and CRUD completion for DLD-owned roles (Delete Role, Attach/Detach Policy). Also adds: SSM-capable instance profile enforcement at launch plus associate/replace for already-running instances; automatic gzip compression of user-data, fixing AWS's 16384-byte limit on larger cloud-init files; arm64/Graviton support and Ubuntu 26.04 LTS in the curated AMI/instance-type lists; single-resource detail views for an individual EC2 instance and AMI (Show instance detail/Show AMI detail), alongside the existing per-resource-type list views; and a sixth domain, Configuration, to view and edit clasm's own ~/.clasm settings (regions, backup directory rules, Origin tag config) from within clasm instead of hand-editing YAML, with changes staged in memory until an explicit Save. Finally, every domain menu's item ordering was reviewed and regrouped (View/Inspect -> Instance -> AMI -> Launch Template -> Maintenance for Compute, matching the List -> Detail -> Create -> CRUD shape IAM already used), and several menu labels were unified onto consistent terminology ('List S3 Buckets' -> 'Show Buckets', 'Show resource lists' -> 'Show Key Pairs', 'View Role/Instance Profile Detail' -> 'Show Role/Instance Profile Detail').

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



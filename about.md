---
title: clasm
abstract: |-
  Interactive Go CLI (clasm) for administering Caltech Library DLD's AWS EC2 instances, AMIs, and S3 backup archives, with support for tag management, cloud-init inspection, and backup archival to S3.
authors:
  - family_name: Doiel
    given_name: R. S.
    id: https://orcid.org/0000-0003-0900-6903



repository_code: https://github.com/caltechlibrary/clasm
version: 0.0.1

operating_system:
  - POSIX

programming_language:
  - Go >= 1.26.4



---

About this software
===================

## clasm 0.0.1

First tagged release. Core functionality complete, real-AWS verified: EC2/AMI lifecycle management, tag management, cloud-init inspection, and S3 Backup Archive & Trim across two AWS regions; Key Management (key pairs); S3 domain (buckets, static website hosting, directory sync, object browsing, bulk object/bucket delete, lifecycle policies with local ordering validation). CloudFront and a UI/UX pass (evaluating charmbracelet/huh as a termlib successor) are postponed to a later version.

## Authors

- [R. S. Doiel](https://orcid.org/0000-0003-0900-6903)






Interactive Go CLI (clasm) for administering Caltech Library DLD's AWS EC2 instances, AMIs, and S3 backup archives, with support for tag management, cloud-init inspection, and backup archival to S3.


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





# clasm

Interactive Go TUI (clasm) for administering Caltech Library DLD's AWS EC2 instances, AMIs, and S3 backup archives, with support for tag management, cloud-init inspection, and backup archival to S3.

## Release Notes

- version: 0.0.1
- status: active
- released: 2026-07-09

First tagged release. Core functionality complete, real-AWS verified: EC2/AMI lifecycle management, tag management, cloud-init inspection, and S3 Backup Archive & Trim across two AWS regions; Key Management (key pairs); S3 domain (buckets, static website hosting, directory sync, object browsing, bulk object/bucket delete, lifecycle policies with local ordering validation). CloudFront and a UI/UX pass (evaluating charmbracelet/huh as a termlib successor) are postponed to a later version.


### Authors

- Doiel, R. S.



## Software Requirements

- Go >= 1.26
- CMTools >= 0.0.46

### Software Suggestions

- GNU Make >= 3.8
- Pandoc >= 3.9



## Related resources



- [Getting Help, Reporting bugs](https://github.com/caltechlibrary/clasm/issues)

- [Installation](INSTALL.md)
- [About](about.md)


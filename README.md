

# clasm

Interactive Go TUI (clasm) for administering Caltech Library DLD's AWS EC2 instances, AMIs, launch templates, and key pairs, plus S3 buckets and backup archives, with cross-resource tag management, cloud-init inspection, and backup archival to S3.

## Release Notes

- version: 0.0.6
- status: active
- released: 2026-07-28

Fixes a recurring 'InvalidUserData.Malformed: User data is limited to 16384 bytes' error on large cloud-init files: gzip compression now uses maximum compression, and user-data is checked against AWS's limit before any API call, failing with a clear, actionable clasm-side error (stating the compressed size and the overage) instead of AWS's opaque 400. Fixes two related bugs in the cloud-init file picker: a bare 'q' now cancels (matching every other picker's convention, instead of being treated as a literal filename), and cancelling anywhere in Sync Cloud-Init YAML to a Launch Template now correctly returns to the Compute menu instead of exiting clasm entirely. Adds a new action, Modify Launch Template's Instance Type / EBS Root Volume Size, creating a new launch template version with both overrides in one step (never auto-promoted, matching Sync); if the newly chosen instance type's CPU architecture doesn't match the template's current AMI (e.g. switching a Graviton/arm64 template to an x86_64 instance type), it prompts for a compatible replacement AMI automatically. Finally, the connection info shown after launching or starting an instance now includes a ready-to-use '-i <path>' key reference (guessed from where clasm itself saves a key pair's private key, shown only when that file actually exists) and guesses the SSH login username ('ubuntu' for this tool's own curated/official Ubuntu AMIs, 'ec2-user' otherwise) instead of always hardcoding 'ec2-user'.


### Authors

- Doiel, R. S.



## Software Requirements

- Go >= 1.26
- CMTools >= 0.0.46
- Pandoc >= 3.9

### Software Suggestions

- GNU Make >= 3.8



## Related resources



- [Getting Help, Reporting bugs](https://github.com/caltechlibrary/clasm/issues)

- [Installation](INSTALL.md)
- [About](about.md)


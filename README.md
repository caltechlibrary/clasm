
# awstools

Interactive Go CLI (`awsops`) for administering AWS EC2 instances, AMIs,
and S3 backup archives for Caltech Library DLD's infrastructure. Its
primary use case today is managing instances behind this team's Invenio
RDM deployments, but nothing in its mechanisms (tagging, backup
archival, cloud-init inspection) is RDM-specific.

Run `awsops` with no arguments to launch the interactive menu: pick a
domain (today: Compute -- EC2 & AMI; Key Management, S3, and CloudFront
are planned, see [DESIGN.md](DESIGN.md)), and it lists current
resources before presenting a numbered menu of operations.

## Release Notes

- version: 0.0.0
- status: active

Real-AWS verified as of 2026-07-08 (`TEST_PLAN_REAL_AWS.txt`, 112/112
checks): EC2 instance and AMI lifecycle management, tag management,
cloud-init inspection, and S3 backup archive & trim, across two AWS
regions (us-west-1, us-west-2).

### Authors

- Doiel, R. S.

## Software Requirements

- Go >= 1.26 (to build from source; a pre-built release binary needs
  nothing else)
- AWS credentials resolvable by the AWS SDK's default chain
  (`~/.aws/credentials`, `~/.aws/config`, environment variables, or SSO)
- SSM Agent (and, for backup archival specifically, the AWS CLI) on
  target EC2 instances -- only required for the cloud-init-status and
  Backup Archive & Trim features

See [software_requirements.md](software_requirements.md) for the full
list, including required IAM permissions.

## Documentation

- [User Manual](user_manual.md)
- [Installation](INSTALL.md)
- [Design](DESIGN.md)
- [Decisions](DECISIONS.md)
- [About](about.md)

## Related resources

- [Getting Help, Reporting bugs](https://github.com/caltechlibrary/awstools/issues)

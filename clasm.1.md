%clasm(1) user manual | version 0.0.1 a3c0705
% R. S. Doiel
% 2026-07-09

# NAME

clasm

# SYNOPSIS

clasm [OPTIONS]

# DESCRIPTION

clasm is an interactive command line tool for administering AWS EC2
instances, AMIs, and S3 backup archives for Caltech Library DLD's
infrastructure, across the regions configured in ~/.clasm (default:
us-west-1, us-west-2). Its primary use case today is managing instances
behind this team's Invenio RDM deployments, but nothing in its
mechanisms (tagging, backup archival, cloud-init inspection) is
RDM-specific.

Run with no options to launch the interactive menu: it lists current EC2
instances and owned AMIs, then presents a numbered menu of operations
(create/start/stop/terminate an instance, create/remove an AMI, manage
tags, show/export cloud-init, archive stale backups to S3, and refresh
the listings).

# OPTIONS

-config
: path to clasm' own YAML config file (regions, etc.); defaults to
~/.clasm. AWS credentials and profile selection are never read from
here -- they remain the AWS SDK's responsibility via its standard
chain (~/.aws/credentials, ~/.aws/config, environment variables, SSO)

-debug
: write a JSONL debug log of every AWS SDK call to
./clasm-debug-\<timestamp\>.jsonl in the current directory

-help
: display help

-license
: display license

-version
: display version

# EXAMPLES

~~~
   clasm
~~~



%clasm(1) user manual | version 0.0.4 1589fa6
% R. S. Doiel
% 2026-07-22

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

Run with no options to launch the interactive tool: after authenticating,
it presents a domain picker -- Compute (EC2 instances, AMIs, and launch
templates), Key Management (SSH key pairs), S3 (buckets, static website
hosting, and backup archival), and Tag Management (add/update/remove/
list tags across any of the above from one place). Each domain has its
own menu of operations (e.g. Compute: create/start/stop/terminate an
instance, create/remove an AMI, create/sync/promote/delete a launch
template, show/export cloud-init) reachable after picking that domain;
resource listings are shown on request via each domain's own "Show..."
choice, not dumped automatically at startup.

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



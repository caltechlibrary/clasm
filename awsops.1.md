%awsops(1) user manual | version 0.0.0 57eb69e
% R. S. Doiel
% 2026-07-01

# NAME

awsops

# SYNOPSIS

awsops [OPTIONS]

# DESCRIPTION

awsops is an interactive command line tool for administering AWS EC2
instances, AMIs, and S3 backup archives for Caltech Library DLD's
infrastructure, across four regions (us-east-1, us-east-2, us-west-1,
us-west-2). Its primary use case today is managing instances behind this
team's Invenio RDM deployments, but nothing in its mechanisms (tagging,
backup archival, cloud-init inspection) is RDM-specific.

Run with no options to launch the interactive menu: it lists current EC2
instances and owned AMIs, then presents a numbered menu of operations
(create/start/stop/terminate an instance, create/remove an AMI, manage
tags, show/export cloud-init, archive stale backups to S3, and refresh
the listings).

# OPTIONS

-debug
: write a JSONL debug log of every AWS SDK call to
./awsops-debug-\<timestamp\>.jsonl in the current directory

-help
: display help

-license
: display license

-version
: display version

# EXAMPLES

~~~
   awsops
~~~



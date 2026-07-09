
# User Manual

`clasm` is an interactive command-line tool for administering AWS EC2
instances, AMIs, and S3 backup archives for Caltech Library DLD's
infrastructure, across the regions configured in `~/.clasm` (default:
us-west-1, us-west-2).

## Starting clasm

Run with no arguments:

~~~shell
clasm
~~~

clasm authenticates using the AWS SDK's default credential chain and
prints `clasm <version> -- authenticated as AWS account <account-id>`
before showing the domain picker. If credentials aren't resolvable, it
fails fast with a clear message rather than a raw SDK error.

## Domain Picker

On startup you choose a domain to work in:

- **Compute** (EC2 & AMI) -- fully implemented, see below
- **Key Management** -- fully implemented, see below
- **S3** (Buckets & Static Websites) -- fully implemented, see below
- **CloudFront** -- planned, not yet implemented (see
  [DESIGN.md](DESIGN.md), [PLAN.md](PLAN.md) Phase 21)

## Compute Menu

Choosing Compute lists the account's current EC2 instances and owned
AMIs (aggregated across both configured regions, with Public/Private IP
columns and color-coded state), then presents:

 1. Show resource lists
 2. Create EC2 instance from AMI
 3. Create EC2 instance from cloud-init YAML
 4. Start EC2 instance
 5. Stop EC2 instance
 6. Terminate EC2 instance
 7. Manage tags for an instance or AMI
 8. Create AMI from EC2 instance
 9. Remove AMI
10. Show/export cloud-init for an instance or AMI
11. Archive stale backups to S3 and trim disk space
12. Back to domain picker

Every item is interactive: clasm prompts for each required value in
turn, validates input, and asks for explicit confirmation before any
destructive or billable action (instance termination and AMI removal
require typing the exact instance/AMI ID or Name tag to confirm). Every
successful operation automatically refreshes the resource listing
afterward. See [DESIGN.md](DESIGN.md), "Core Features" for the full
prompt sequence and behavior of each item.

## Key Management Menu

Choosing Key Management lists the account's current EC2 key pairs
(aggregated across both configured regions), then presents:

1. Show resource lists
2. Create Key Pair
3. Import Key Pair
4. Delete Key Pair
5. Back to domain picker

**Create Key Pair** picks a region, generates a new ED25519 key pair via
AWS, and saves the private key to `~/.ssh/<name>.pem` at mode `0600` --
the same underlying primitive Compute's "Create EC2 instance from AMI"
uses for its inline "type `new`" key-pair shortcut. **Import Key Pair**
registers an existing public key (a local `.pub` file -- not a private
key/`.pem` file; if you only have a private key, derive its public half
with `ssh-keygen -y -f <private-key> > file.pub`) with AWS instead of
generating a new one; clasm validates the file looks like a well-formed
SSH public key before calling AWS. **Delete Key Pair** warns
about any instances that were launched with the key pair being deleted
(they keep running; the key pair just can't be used for new launches
afterward) and requires typing the exact key pair name to confirm. See
[DESIGN.md](DESIGN.md), "Key Management Domain" for the full prompt
sequence.

## S3 Menu

Choosing S3 lists the account's current buckets (Name, Region, Static
Website, Purpose), then presents:

1. Show resource lists
2. Create Bucket
3. Configure Static Website Hosting
4. Sync Local Directory to Bucket
5. Browse/Manage Objects
6. Manage Bucket Lifecycle Policies
7. Back to domain picker

**Create Bucket** prompts a name (validated locally against S3's naming
rules before ever calling AWS), a region, and a purpose --
**website**, **backup**, or **internal**. The new bucket always has all
four Public Access Block settings turned on (never public by omission)
and is tagged with its Purpose, which the other S3 items read back later.
**Configure Static Website Hosting** picks a bucket and prompts index/
error documents (defaulting to `index.html`/`error.html`); the bucket
stays private -- fronting it publicly is CloudFront's job once that
domain exists. **Sync Local Directory to Bucket** picks a bucket and a
local directory, shows a dry-run diff (by key and size, not checksum) of
files to upload and bucket-only objects that would be deleted, asks for
confirmation to upload, and -- only if there are bucket-only objects --
asks a **separate**, stronger confirmation (type the exact bucket name)
before deleting them; declining the upload step aborts the whole run
without ever reaching the delete prompt. **Browse/Manage Objects** picks
a bucket, optionally filters by key prefix, then lets you view an
object's metadata or delete it. **Manage Bucket Lifecycle Policies**
picks a bucket and shows its current rules, then lets you add, edit, or
remove one: buckets tagged `Purpose: backup` get a guided flow (expire-
after-days, transition-after-days with a curated storage-class choice);
everything else gets a generic editor (named rules, arbitrary
transitions from the full storage-class list, optional expiration).
Every add/edit/remove is confirmed with a reminder that AWS evaluates
lifecycle rules on its own schedule (typically 24-48 hours), not
immediately. See [DESIGN.md](DESIGN.md), "S3 Domain (Buckets & Static
Websites)" for the full prompt sequence.

## Command-line Options

`-config <path>`
: path to clasm' own YAML config file (regions, per-instance backup
  directory defaults); defaults to `~/.clasm`. AWS credentials are
  never read from here -- they remain the AWS SDK's responsibility.

`-debug`
: write a JSONL debug log of every AWS SDK call to
  `./clasm-debug-<timestamp>.jsonl` in the current directory. When
  diagnosing an unexpected AWS error, check this log first -- every
  entry has the exact API call, region, and either its output or error.

`-help`, `-license`, `-version`
: standard informational flags.

## Configuration (`~/.clasm`)

An optional YAML file for clasm' own operational settings -- never AWS
credentials or profile selection:

~~~yaml
regions:
  - us-west-1
  - us-west-2
backup_directories:
  - pattern: "etd-*"
    directory: /opt/rdm_sql_backups
~~~

`regions` narrows or changes which regions every listing and picker
operates against (default: us-west-1, us-west-2, if the file or key is
absent). `backup_directories` is an ordered list of `{pattern,
directory}` rules, glob-matched against an instance's Name tag, that
pre-fill Backup Archive & Trim's "Backup directory" prompt (still
editable, never a silent default). See [DESIGN.md](DESIGN.md),
"Configuration" for the full schema and validation behavior.

## Getting Help

File an issue at
<https://github.com/caltechlibrary/clasm/issues>.

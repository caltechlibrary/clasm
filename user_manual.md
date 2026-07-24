
# User Manual

`clasm` is an interactive command-line tool for administering AWS EC2
instances, AMIs, launch templates, key pairs, S3 buckets/static websites,
and IAM roles/profiles/policies for Caltech Library DLD's infrastructure,
across the regions configured in `~/.clasm` (default: us-west-1,
us-west-2).

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

- **Compute** (EC2 & AMI) -- instances, AMIs, and launch templates; see
  below
- **Key Management** -- EC2 key pairs; see below
- **S3** (Buckets & Static Websites) -- see below
- **Tag Management** -- add/update/remove/list tags across every taggable
  resource kind (instances, AMIs, launch templates, key pairs, S3
  buckets, IAM roles/instance profiles/policies) from one place; see
  below
- **IAM** -- browse and manage IAM roles, instance profiles, and
  policies; see below
- **Configuration** -- view or edit clasm's own `~/.clasm` settings; see
  below
- **CloudFront** -- designed (see [DESIGN.md](DESIGN.md), [PLAN.md](PLAN.md)
  Phase 21) but not on the active roadmap and not exposed in the domain
  picker

## Compute Menu

Choosing Compute lists the account's current EC2 instances and owned
AMIs (aggregated across both configured regions, with Public/Private IP
columns and color-coded state), then presents, grouped View/Inspect ->
Instance -> AMI -> Launch Template -> Maintenance:

 1. Show instances
 2. Show instance detail
 3. Show AMIs
 4. Show AMI detail
 5. Show launch templates
 6. Show launch template detail
 7. Show/export cloud-init for an instance or AMI
 8. Create EC2 instance from AMI
 9. Create EC2 instance from cloud-init YAML
10. Create EC2 instance from launch template
11. Start EC2 instance
12. Stop EC2 instance
13. Terminate EC2 instance
14. Resize instance's root volume
15. Associate/replace IAM instance profile
16. Manage tags for an instance or AMI
17. Create AMI from EC2 instance (running or stopped)
18. Remove AMI
19. Create launch template from cloud-init YAML
20. Sync cloud-init YAML to a launch template
21. Promote a launch template version to default
22. Delete launch template version(s)
23. Delete a launch template
24. Archive stale backups to S3 and trim disk space

Every item is interactive: clasm prompts for each required value in
turn, validates input, and asks for explicit confirmation before any
destructive or billable action (instance termination and AMI removal
require typing the exact instance/AMI ID or Name tag to confirm). Every
successful operation automatically refreshes the resource listing
afterward.

A few items worth calling out beyond the original single-instance/AMI
core: **Show instance detail**/**Show AMI detail** show one resource's
full curated fields (instance type, security groups, subnet, IAM
instance profile, EBS volume sizes, tags, and -- for AMIs -- block
device mappings), distinct from the list views above them. **Resize
instance's root volume** grows a running instance's EBS root volume and
automates the OS-level partition/filesystem growth via SSM, falling back
to printed manual instructions if that automation can't proceed safely
(e.g. an unrecognized disk layout). **Associate/replace IAM instance
profile** attaches or swaps an instance profile on an already-running
instance -- the launch-time equivalent lives in the Create EC2 instance
flows. **Launch templates** (items 5, 6, 19-23) are built directly from
cloud-init YAML rather than from an existing instance, support version
history/diffing, and enforce the same IMDSv2/SSM requirements as regular
launches. See [DESIGN.md](DESIGN.md), "Core Features" for the full
prompt sequence and behavior of each item.

## Key Management Menu

Choosing Key Management lists the account's current EC2 key pairs
(aggregated across both configured regions), then presents:

1. Show Key Pairs
2. Create Key Pair
3. Import Key Pair
4. Delete Key Pair

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

1. Show Buckets
2. Create Bucket
3. Configure Static Website Hosting
4. Browse & Manage Objects
5. Manage Bucket Lifecycle Policies
6. Delete Bucket

**Create Bucket** prompts a name (validated locally against S3's naming
rules before ever calling AWS), a region, and a purpose --
**website**, **backup**, or **internal**. The new bucket always has all
four Public Access Block settings turned on (never public by omission)
and is tagged with its Purpose, which the other S3 items read back later.
**Configure Static Website Hosting** picks a bucket and prompts index/
error documents (defaulting to `index.html`/`error.html`); the bucket
stays private -- fronting it publicly is CloudFront's job once that
domain exists. **Browse & Manage Objects** picks a bucket (optionally
linking a local directory for a two-pane view) and opens the interactive
file manager: upload, download, delete, view/edit metadata, filter,
find, sync a linked local directory, and tag one or more objects, all
from one screen. This is also where the former separate "Sync Local
Directory to Bucket" and standalone bulk-delete actions live now.
**Manage Bucket Lifecycle Policies** picks a bucket and shows its
current rules, then lets you view a rule's full detail, or add, edit, or
remove one: buckets tagged `Purpose: backup` get a guided flow (expire-
after-days, transition-after-days with a curated storage-class choice);
everything else gets a generic editor (named rules, arbitrary
transitions from the full storage-class list, optional expiration).
Every add/edit/remove is confirmed with a reminder that AWS evaluates
lifecycle rules on its own schedule (typically 24-48 hours), not
immediately. **Delete Bucket** requires the bucket to be empty first and
typing the exact bucket name to confirm. See [DESIGN.md](DESIGN.md), "S3
Domain (Buckets & Static Websites)" for the full prompt sequence.

## Tag Management Menu

Choosing Tag Management presents:

1. Show all tags
2. Manage tags

Both start by picking a resource **kind** -- Instance, AMI, Launch
Template, Key Pair, S3 Bucket, IAM Role, IAM Instance Profile, or IAM
Policy. **Show all tags** then lists every resource of that kind with
its full tag map, for a quick "what's tagged and what isn't" scan.
**Manage tags** picks one specific resource of that kind and lets you
add, update, or remove a single tag key/value pair. This is the
general-purpose tag editor across every taggable resource kind in
clasm -- distinct from Compute's own narrower "Manage tags for an
instance or AMI," which is scoped to just those two kinds and reachable
without leaving the Compute domain. Editing the `Origin` tag (see IAM,
below) through this same flow is how a resource gets marked DLD-owned --
there's no separate, dedicated action for it. See
[DESIGN.md](DESIGN.md), "Tag Management Domain."

## IAM Menu

Choosing IAM presents, grouped List -> Detail -> Create ->
Attach/Detach -> Delete:

1. Show Roles
2. Show Instance Profiles
3. Show Policies
4. Show Role Detail
5. Show Instance Profile Detail
6. Create Role from Template
7. Attach Policy to Role
8. Detach Policy from Role
9. Delete Role

Every role/instance profile/policy row shows its `Origin` tag's literal
value, or "(unset)" if never set -- a config-driven convention (see
Configuration, below), not a hardcoded vocabulary. Roles, instance
profiles, and policies not recognized as DLD-owned (via that `Origin`
tag) are read-only for anything that would change their permissions;
tagging them is still always allowed, so a support contact can be
recorded. **Show Role Detail**/**Show Instance Profile Detail** show
trust policy, attached/inline policies (with on-demand document
viewing), tags, whether the role/profile is SSM-capable, and (for roles)
which instance profiles reference it. **Create Role from Template**
offers five curated, parametrized templates (Static Website, RDM
Repository Instance, Bridge Service, Patron-Facing Service, Data
Processing) -- you supply plain resource names/IDs and clasm constructs
the ARNs; this is the only IAM action that creates new permissions from
scratch, and it's deliberately template-only, not free-form policy
authoring. **Attach/Detach Policy to/from Role** and **Delete Role** are
all scoped to DLD-owned roles only; Delete Role is gated behind
type-to-confirm and cascades to the role's own dedicated policy (if
created by a template and unused elsewhere) -- the one irreversible
action in this menu, which is why it's last. clasm never creates,
modifies, or deletes IAM *users* -- this domain is scoped entirely to
roles, instance profiles, and policies. See [DESIGN.md](DESIGN.md), "IAM
Profile & Role Management Domain."

## Configuration Menu

Choosing Configuration presents:

1. Show current config
2. Edit regions
3. Edit backup directory rules
4. Edit Origin tag config
5. Save

Edits happen against an in-memory working copy -- nothing is written to
`~/.clasm` until you explicitly choose **Save**; quitting with unsaved
changes pending warns first. **Edit regions** adds/removes which AWS
regions clasm operates against (takes effect the next time clasm is
launched, not live). **Edit backup directory rules** adds/removes the
glob-pattern-to-directory rules Backup Archive & Trim uses to pre-fill
its "Backup directory" prompt. **Edit Origin tag config** sets the tag
key and which value means "DLD-owned" for the IAM domain's read-only
guard (see IAM, above). See [DESIGN.md](DESIGN.md), "Configure clasm
Domain."

## Command-line Options

`-config <path>`
: path to clasm' own YAML config file (regions, backup directory rules,
  Origin tag config); defaults to `~/.clasm`. AWS credentials are never
  read from here -- they remain the AWS SDK's responsibility. Can also
  be viewed/edited from within clasm itself via the Configuration domain.

`-debug`
: write a JSONL debug log of every AWS SDK call to
  `./clasm-debug-<timestamp>.jsonl` in the current directory. When
  diagnosing an unexpected AWS error, check this log first -- every
  entry has the exact API call, region, and either its output or error.

`-help`, `-license`, `-version`
: standard informational flags.

## Configuration (`~/.clasm`)

An optional YAML file for clasm' own operational settings -- never AWS
credentials or profile selection. Can be viewed and edited from within
clasm itself (Configuration domain, above), or hand-edited directly:

~~~yaml
regions:
  - us-west-1
  - us-west-2
backup_directories:
  - pattern: "etd-*"
    directory: /opt/rdm_sql_backups
origin_tag:
  key: "Origin"
  dld_value: ""
~~~

`regions` narrows or changes which regions every listing and picker
operates against (default: us-west-1, us-west-2, if the file or key is
absent). `backup_directories` is an ordered list of `{pattern,
directory}` rules, glob-matched against an instance's Name tag, that
pre-fill Backup Archive & Trim's "Backup directory" prompt (still
editable, never a silent default). `origin_tag` names the tag the IAM
domain treats as its DLD-ownership convention -- `key` defaults to
`"Origin"`, `dld_value` defaults to empty (meaning no value is
recognized as DLD-owned yet, until your group settles on one). See
[DESIGN.md](DESIGN.md), "Configuration" for the full schema and
validation behavior.

## Getting Help

File an issue at
<https://github.com/caltechlibrary/clasm/issues>.


# User Manual

`clasm` is an interactive command-line tool for administering AWS EC2
instances, AMIs, launch templates, key pairs, S3 buckets/static websites,
IAM roles/profiles/policies, and Invenio RDM backup/restore for Caltech
Library DLD's infrastructure, across the regions configured in `~/.clasm`
(default: us-west-1, us-west-2).

> `DESIGN.md`, `PLAN.md` and the decision records referenced below are no
> longer kept in this repository. They live in the DLD workspace under
> `agents/projects/clasm/`.

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
- **RDM Backup & Restore** -- generate and archive Invenio RDM Postgres
  dumps and OpenSearch snapshots, and restore either from S3; see below
- **Configuration** -- view or edit clasm's own `~/.clasm` settings; see
  below
- **CloudFront** -- designed (see `DESIGN.md`, `PLAN.md`
  Phase 21) but not on the active roadmap and not exposed in the domain
  picker

## Compute Menu

Choosing Compute lists the account's current EC2 instances and owned
AMIs (aggregated across both configured regions, with Public/Private IP
columns and color-coded state), then presents, grouped View/Inspect ->
Instance -> AMI -> Launch Template:

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
21. Modify launch template's instance type / EBS root volume size
22. Promote a launch template version to default
23. Delete launch template version(s)
24. Delete a launch template

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
flows. **Launch templates** (items 5, 6, 19-24) are built directly from
cloud-init YAML rather than from an existing instance, support version
history/diffing, and enforce the same IMDSv2/SSM requirements as regular
launches. **Modify launch template's instance type / EBS root volume
size** creates a new version of a template with a different instance
type and/or a larger root volume, inheriting everything else (including
cloud-init user data) from the source version you pick -- and if the new
instance type's architecture doesn't match the template's current AMI
(x86_64 vs arm64), it prompts you to pick a replacement AMI, filtered to
that architecture in the template's own region. That is the action to
reach for when an instance launched from a template fails on an
AMI-architecture/instance-type mismatch. Like **Sync cloud-init YAML to
a launch template**, it only creates the new version; use **Promote a
launch template version to default** to make it the one new launches
use. See `DESIGN.md`, "Core Features" for the full prompt
sequence and behavior of each item.

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
`DESIGN.md`, "Key Management Domain" for the full prompt
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
typing the exact bucket name to confirm. See `DESIGN.md`, "S3
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
`DESIGN.md`, "Tag Management Domain."

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
9. Remove Role from Instance Profile
10. Delete Instance Profile
11. Delete Role

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
authoring. **Attach/Detach Policy to/from Role**, **Remove Role from
Instance Profile**, **Delete Instance Profile**, and **Delete Role** are
all scoped to DLD-owned roles and instance profiles only. **Remove Role
from Instance Profile** picks a role, then which of the instance
profiles it currently belongs to should drop it -- the membership
counterpart to Compute's "Associate/replace IAM instance profile," which
works from the instance side. **Delete Instance Profile** deletes a
DLD-owned profile after a simple yes/no confirmation -- AWS itself
refuses the delete while a role is still attached, so remove the role
first. **Delete Role** is gated behind type-to-confirm and cascades to
the role's own dedicated policy (if created by a template and unused
elsewhere) -- the most destructive
action in this menu, which is why it's last. clasm never creates,
modifies, or deletes IAM *users* -- this domain is scoped entirely to
roles, instance profiles, and policies. See `DESIGN.md`, "IAM
Profile & Role Management Domain."

## RDM Backup & Restore Menu

Choosing RDM Backup & Restore presents the Invenio RDM backup/restore
operations, grouped generate -> archive -> restore, SQL before
OpenSearch within each pair:

1. Generate SQL Backup
2. Archive SQL Backups to S3 (and trim local copies)
3. Archive OpenSearch Snapshot to S3
4. Restore SQL Backup from S3
5. Restore OpenSearch Snapshot from S3

Every item starts by picking a target instance and runs its work on that
instance over SSM, so the instance must be SSM-reachable and have the
AWS CLI installed -- clasm checks for the CLI up front and reports one
clear error rather than letting each subsequent step fail. **Generate
SQL Backup** discovers the instance's live Postgres container and
database identity (reconciling it against `rdm_postgres_config`, see
Configuration below) and runs `pg_dump` into the instance's backup
directory; it generates the local dump and stops there. **Archive SQL
Backups to S3 (and trim local copies)** copies the dumps in that
directory to a bucket you pick, independently verifies each upload, then
optionally deletes the verified local copies and `fstrim`s the volume,
reporting bytes freed -- it is the same workflow that manages the
nightly cron job's output, which is why it is separate from Generate SQL
Backup rather than chained to it. **Archive OpenSearch Snapshot to S3**
does the equivalent for OpenSearch's snapshot repository directory
(configured separately from the SQL backup directory). Both of these two
items also have a non-interactive command-line form once you trust them
enough for unattended use -- see "Non-interactive (CLI) Usage" below; a
successful interactive run of either prints the exact command that
reproduces it. The two
**Restore** items are the reverse: pick a target instance, pick an
archive from S3, and load it back. Both are gated behind
type-to-confirm on the exact instance ID or Name tag, and neither keeps
any recall history of the last instance used -- restoring is a rare,
deliberate action, and clasm deliberately does not pre-position the
cursor on a previous target. See `DESIGN.md`, "RDM Backup &
Restore Domain" for the full prompt sequence and the snapshot scope.

### File ownership on the instance

clasm reaches an instance over SSM, which runs commands as root, so
anything it writes would be root-owned and unwritable by the account the
RDM services run as. To prevent that, every workflow that writes into a
backup directory makes that directory belong to the **`ubuntu`** user
(the account that owns `/Sites/<repo>` and runs `invenio-cli`), looking
up `ubuntu`'s uid and gid on the instance each time. You do not create or
`chown` these directories by hand.

| Directory | Owner and mode | What clasm does |
|---|---|---|
| SQL backups (e.g. `/opt/rdm_sql_backups`) | `ubuntu:ubuntu`, `0750` | **Generate SQL Backup** creates or repairs the directory before the dump, then hands the directory and its dumps to `ubuntu` after a successful dump. A failed dump changes no ownership. |
| OpenSearch backups (e.g. `/opt/rdm_opensearch_backups`) | `ubuntu:ubuntu`, `0775` | **Archive** and **Restore OpenSearch Snapshot** create or repair the directory before using it. **Restore** also hands everything it synced down to `ubuntu`. |

Creating or repairing a directory never deletes anything and never
recurses into what is already there; the two workflows that do change
ownership below the top level are Generate SQL Backup (after its dump)
and Restore OpenSearch Snapshot (after its sync).

**OpenSearch needs one extra condition.** The search container writes to
its snapshot repository as uid 1000, so `ubuntu` must also be uid 1000 on
the instance. Both OpenSearch workflows check this first and, if it is
not, stop before changing or deleting anything, naming both uids. The
group id is not compared: on the current images `ubuntu` is uid 1000 but
gid 1001. Generate SQL Backup has no such condition, because nothing in
a container writes into the SQL directory.

clasm refuses to set ownership on `/` or on a relative path, since these
steps run as root on a directory you type.

An existing SQL backup directory that was `root:www-data 0770` becomes
`ubuntu:ubuntu 0750` at the next Generate SQL Backup on that instance,
and its existing dumps become `ubuntu`'s. See `DESIGN.md`, "One Service
Owner for Everything clasm Writes on an RDM Host".

## Configuration Menu

Choosing Configuration presents:

1. Show current config
2. Edit regions
3. Edit backup directory rules
4. Edit RDM Postgres config
5. Edit Origin tag config
6. Edit cloud-init extraction security groups
7. Save

Edits happen against an in-memory working copy -- nothing is written to
`~/.clasm` until you explicitly choose **Save**; quitting with unsaved
changes pending warns first. **Edit regions** adds/removes which AWS
regions clasm operates against (takes effect the next time clasm is
launched, not live). **Edit backup directory rules** adds/removes the
glob-pattern-to-directory rules the RDM Backup & Restore domain uses to
pre-fill its "Backup directory" prompt. **Edit RDM Postgres config**
adds/removes the per-instance Postgres container/database/user rules the
same domain's SQL workflows use; those workflows also discover this
information live and offer to record what they find here, so it is
rarely edited by hand. **Edit Origin tag config** sets the tag
key and which value means "DLD-owned" for the IAM domain's read-only
guard (see IAM, above). **Edit cloud-init extraction security groups**
picks, region by region, the security group for the temporary instance that
reads an AMI's cloud-init: it lists that region's groups, marks each as allowing
outbound HTTPS or not, and refuses one that does not. See `DESIGN.md`,
"Configure clasm Domain."

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

## Non-interactive (CLI) Usage

Beyond the flags above, clasm also accepts a `<domain> [<action>
[args...]]` path on the command line -- the same route you'd take
through the domain picker and its menus, expressed directly, so a menu
action you trust can be scripted or put in a crontab once it no longer
needs a human watching it:

- `clasm` (no path) -- the domain picker, as above.
- `clasm <domain>` -- jumps straight into that domain's own menu, as if
  you'd picked it from the domain picker yourself. Backing out with `q`
  returns you to the domain picker exactly as normal navigation would.
- `clasm <domain> <action>` -- jumps straight into that one action's own
  interactive prompts, skipping the domain menu. Once it finishes,
  you're back at that domain's menu, same as choosing the action
  normally would leave you.
- `clasm <domain> <action> <arg1> <arg2> ...>` -- runs the action
  non-interactively: no prompts, no confirmation, and a real process
  exit status (0 success, 1 the action itself failed, 2 a usage error --
  an unrecognized domain/action name or the wrong number of arguments).
  Safe to use in an unattended job: a mistyped invocation fails
  immediately with a clear message rather than hanging, waiting for
  input from a terminal that isn't there.

**This feature is experimental and under active development. The
command terms (domain and action names, and the order and meaning of
their arguments) and the output formats are not stable and will change
before 1.0, as menu labels are shortened and the forms are refined from
use. Expect to edit any script you write against it, and check the
release notes when you upgrade.**

Command names are derived from the menu labels by a fixed rule:
lowercase, `&` becomes `and`, parenthetical asides are dropped, a
possessive `'s` is dropped, and every other run of punctuation and
spaces becomes one hyphen (so "Resize instance's root volume" is
`resize-instance-root-volume`). Every domain has a name, so
`clasm <domain>` works for all of them: `compute`, `key-management`,
`s3`, `tag-management`, `iam`, `rdm-backup-and-restore` and
`configuration`.

Six domains have actions with the full non-interactive form today:
**RDM Backup & Restore** (`rdm-backup-and-restore`, below) and the
read-only "Show" actions of **Compute** (`compute`), **Key Management**
(`key-management`), **IAM** (`iam`), **S3** (`s3`) and **Tag Management**
(`tag-management`), described under "Read-only forms". A path *under*
the remaining domain, Configuration (`clasm configuration edit-regions`), is
a usage error saying it has no CLI sub-commands yet. An action slug is valid
only under its own domain.

The RDM actions:

`archive-sql-backups-to-s3 <instance> <directory> <bucket> <trim-days-or-"">`
: the non-interactive form of **Archive SQL Backups to S3 (and trim
  local copies)**. `<instance>` matches an instance's Name tag first,
  falling back to its instance ID if no Name matches -- an error, never
  a guess, if either matches more than one instance. `<trim-days-or-"">`
  mirrors the interactive prompt exactly: an empty string (`""` on most
  shells) keeps every local copy, `0` deletes every file successfully
  archived, and a positive integer deletes only files older than that
  many days.

`archive-opensearch-snapshot-to-s3 <instance> <directory> <bucket> <cleanup-days-or-"">`
: the non-interactive form of **Archive OpenSearch Snapshot to S3**.
  Same instance-matching rule as above. `<cleanup-days-or-"">` is a
  *different* threshold from the SQL form's trim argument -- it governs
  deleting this instance's own previously-archived snapshots already in
  S3, never anything on the instance itself or the snapshot this run
  just created. An empty string skips cleanup entirely; unlike the SQL
  form, `0` is not a valid choice here.

`generate-sql-backup <instance> <directory>`
: the non-interactive form of **Generate SQL Backup**: writes a gzipped
  `pg_dump` of the instance's RDM database into `<directory>` on the
  instance, named `<container>-<database>-<date>.sql.gz`, and makes the
  directory and its dumps belong to the service user (see "File ownership
  on the instance"). The Postgres container and database are discovered on
  the instance exactly as the interactive form does, and saved to `~/.clasm`
  the first time. It needs only Docker on the instance, not the AWS CLI. There
  is no confirmation: it writes one file and changes nothing else. A second
  run on the same day **replaces that day's dump**, as the instance's own backup
  script does. The interactive form prints this command after a run.

~~~shell
clasm rdm-backup-and-restore generate-sql-backup caltechauthors-v13 /opt/rdm_sql_backups
clasm rdm-backup-and-restore archive-sql-backups-to-s3 \
  caltechauthors-v13 /opt/rdm_sql_backups \
  s3://sql-backups.library.caltech.edu ""
~~~

The archive forms run every safety check the interactive menu does first
(confirming the AWS CLI is present on the target instance, resolving and
checking access to the destination bucket) -- only the interactive
confirmation prompt is skipped. After a successful *interactive* run of
any of these three actions, clasm prints the exact non-interactive command
that reproduces it, ready to copy into a script or crontab entry.

### Read-only forms

The read-only actions of Compute, Key Management and IAM share one set of
output options, described after the three domains.

#### Compute

The seven "Show" actions of the Compute domain have command-line forms.
They change nothing, ask nothing and need no confirmation; the only AWS
calls they make are reads. (The one exception to "no cost" is an AMI's
cloud-init, below, which is refused unless you ask for it.)

`show-instances`, `show-amis`, `show-launch-templates`
: the listings the Compute menu shows, from the same data. They take no
  arguments.

`show-instance-detail <instance>`, `show-ami-detail <ami>`
: the detail view for one resource. `<instance>` and `<ami>` match the
  Name tag first and fall back to the ID, exactly like the RDM forms; a
  name that matches nothing, or more than one resource, is a usage error
  (exit 2), never a guess.

`show-launch-template-detail [-versions] <template> [version]`
: one version's detail; `version` is a number (`2` or `v2`), `$Latest`,
  or left out for `$Default`. With `-versions` it lists every version
  instead (and takes no `version`). Diffing two versions is interactive
  only for now.

`show-export-cloud-init-for-an-instance-or-ami [-launch-temporary-instance [-security-group <sg-id>]] <instance-or-ami> [file]`
: the decoded cloud-init. Standard output receives the YAML and nothing
  else, so it can be piped; with `file` it is written there instead. An
  instance's cloud-init is one free read. An AMI's can only be read by
  launching a temporary, billable instance, so clasm refuses unless you
  add `-launch-temporary-instance`. That instance needs a security group
  that allows outbound HTTPS, or its SSM agent cannot register: name one with
  `-security-group`, or set the AMI's region in
  `cloud_init_extraction_security_groups` in `~/.clasm` (see "Configuration"). clasm checks the group first and refuses
  at once, exit 2, if it has no outbound rule for port 443. If an instance was
  launched with no user-data, or an AMI's source had none, a note goes to
  standard error, stdout is empty and the exit status is 0. For an AMI the
  result is the user-data of the instance the image was taken from (the most
  recent one recorded on it), decompressed if it was stored gzipped, as clasm's
  own launch templates do. While it waits, a redirected stderr gets one plain
  progress line every 30 seconds.

#### Creating a launch template and launching from it

Unlike the "Show" forms above, these two change things. Neither asks for
confirmation, so they can run from a script.

`create-launch-template-from-cloud-init-yaml [options] <cloud-init.yaml>`
: the non-interactive form of **Create launch template from cloud-init
  YAML**: version 1 of a new template. The wizard's prompts are options:

  ~~~shell
  clasm compute create-launch-template-from-cloud-init-yaml \
    --name caltechauthors-v14 --ami caltechauthors-v13-base \
    --instance-type m7i-flex.2xlarge --key-pair caltechauthors \
    --security-group sg-ada165d0 --subnet subnet-5870b473 \
    --iam-instance-profile rdm-backups \
    --name-tag caltechauthors-v14 --environment test \
    --root-volume-gb 250 cloud-init.yaml
  ~~~

  `--name`, `--ami` (a name or ID, in this account or an official Ubuntu image),
  `--instance-type`, `--key-pair`, `--security-group` (repeat it or separate with
  commas), `--subnet`, `--iam-instance-profile`, `--name-tag` and
  `--environment` (`production`, `development` or `test`) are required.
  `--project` defaults to the AMI's own project tag, and `--root-volume-gb` to
  the AMI's own size (it may not be smaller). IMDSv2 is always required, and
  instances launched from the template are tagged `Name`, `project`,
  `Environment` and `templateName`, the template's own name. `-json` prints
  `{template_id, name, version, region}`.

  Everything the wizard checks is checked, but nothing is offered as a fix:
  an instance type of the wrong architecture, needing ENA the AMI lacks, or not
  offered in the subnet's zone is refused (exit 2) with the reason, and so is a
  security group, subnet, key pair or instance profile that does not exist, an
  instance profile that is not SSM-capable, or a missing or empty cloud-init
  file. **The form never creates a key pair, an instance profile or a role**;
  the ones you name must already exist. A template name already in use is an
  AWS error (exit 1). Nothing is created unless every check passes.

`create-ec2-instance-from-launch-template [-text|-json] <template> [version]`
: the non-interactive form of **Create EC2 instance from launch template**.
  `<template>` matches the template's name or ID, and `version` is a number
  (`2` or `v2`), `$Latest`, or left out for `$Default`. It **launches one
  billable instance** and waits for it to be running, then prints the
  connection info (or, with `-json`, `{instance_id, state, public_ip, private_ip,
  region, template_id, template_name, version}`). The progress line goes to
  standard error, so standard output is the result alone. There is no
  terminate form yet; end the instance from the Compute menu.

#### Key Management and IAM

`key-management show-key-pairs`
: the key pair listing (name, ID, fingerprint, type, region, tags). Only
  public information is listed; no private key is ever printed.

`iam show-roles`, `iam show-instance-profiles`, `iam show-policies`
: the IAM listings, including each item's `Origin` tag value and whether
  it is DLD-owned. Policies are the customer-managed ones. `show-roles` also
  reports whether each role is SSM-capable, which costs a few calls per role,
  so on an account with over a hundred roles it takes about twenty seconds,
  as the interactive view does.

`iam show-role-detail <role-name>`, `iam show-instance-profile-detail <instance-profile-name>`
: the detail view for one role or instance profile. IAM names are exact, so
  the argument is looked up directly (no Name-tag or ID matching); a name
  that does not exist is a usage error (exit 2). In `-json` a role's trust
  policy is embedded as a JSON document, not as an escaped string. Reading
  an attached or inline policy's *document* is interactive only for now.

Timestamps in JSON are RFC 3339 in UTC (`2026-07-23T17:30:00Z`). In the
text view they keep the screen's `2026-07-23 17:30` layout.

#### S3 and Tag Management

`s3 show-buckets`
: the bucket listing: name, region, whether static website hosting is
  configured, and the `Purpose` tag (`website`, `backup`, `internal`, or empty
  when untagged). Browsing objects, lifecycle policies and the bucket editors
  stay interactive.

`tag-management show-all-tags <kind>`
: every resource of one kind with its **complete** tag set. As in the
  interactive view the kind is chosen first, since covering every kind at
  once would cost a tag lookup per bucket and per IAM resource every time.
  `<kind>` is one of `instance`, `ami`, `launch-template`, `key-pair`,
  `s3-bucket`, `iam-role`, `iam-instance-profile` or `iam-policy`; anything
  else is a usage error that lists them. Each JSON record carries its `kind`,
  so `-jsonl` lines stay self-describing when several runs are concatenated.
  In the text view the tags are one `key=value, key=value` column, sorted by
  key; in JSON they are an object, `{}` when untagged. S3 is the slow kind
  (one `GetBucketTagging` call per bucket: about twenty seconds for 78
  buckets); IAM roles take under ten.

#### Output formats

Every read-only form takes one of:

| Option | Output |
|---|---|
| `-text` | The default: the screen layout, plain, with no colour. Columns are truncated as they are on screen; it is for people. |
| `-json` | One JSON document with complete values and the full tag set: an array for a listing, an object for a detail. An empty listing is `[]`. |
| `-jsonl` | Listings only: one compact JSON object per line, for streaming and `jq -c`. An empty listing writes nothing. A detail is a single object, so `-json` covers it. |

Giving more than one format is a usage error. JSON keys are `snake_case`
and are kept separate from clasm's internals, but they are experimental
like everything else here. Unknown or unset values are empty strings, not
the `unknown`/`none` placeholders of the text view.

Options come *before* the arguments (`show-instance-detail -json box`); a
word after an argument is an argument. `--help` on any form prints its
usage and options and exits 0.

A listing has no required argument, so `clasm compute show-instances`
alone opens the interactive view. Any option makes it a non-interactive
run, and `-text` is the way to ask for the default explicitly:

~~~shell
clasm compute show-instances -json | jq -r '.[] | select(.state=="stopped") | .name'
clasm compute show-instance-detail -json caltechauthors-v13 | jq .total_ebs_gib
clasm compute show-export-cloud-init-for-an-instance-or-ami caltechauthors-v13 > cloud-init.yaml
~~~

### Destructive forms: a dry run unless you confirm

The two **Restore** actions replace data on the target instance, so their
non-interactive forms share one rule, and it is not the AWS CLI's (whose
destructive commands just run): **a run is a dry run unless you confirm it.**

`restore-opensearch-snapshot-from-s3 [--confirm <instance-id-or-name>] <instance> <directory> <bucket> <source> <snapshot-name-or-latest> <index-prefix>`
: the non-interactive form of **Restore OpenSearch Snapshot from S3**. Every
  argument is explicit -- the directory and the index prefix have defaults in
  the menu, but a script should not restore from a default. `<source>` is the
  S3 prefix the snapshots were archived under (another instance's name when you
  restore a production backup onto a test box), and `<index-prefix>` is the
  name the indices carry *inside* the snapshot (for example `caltechauthors`,
  not `caltechauthors-v13`); they are different values, and mixing them up is
  the usual mistake. `<snapshot-name-or-latest>` is an exact snapshot name or
  the word `latest`.

`restore-sql-backup-from-s3 [--confirm <instance-id-or-name>] <instance> <bucket> <source> <backup-key-or-name-or-latest>`
: the non-interactive form of **Restore SQL Backup from S3**. The backup is a
  full S3 key, a file name, or `latest` (the most recently modified object under
  `<source>/`).

What happens depends on how you run it:

- **With `--confirm <instance-id-or-name>`:** it runs, with no prompt. The value
  must equal the target instance's ID or its Name tag, exactly; anything else is
  a usage error (exit 2) before any AWS call. It prints one line saying what it
  is about to do, so a log shows what ran.
- **Without `--confirm`, from a terminal:** it prints the plan and then asks you
  to type the instance ID or Name tag, the same prompt as the menu.
- **Without `--confirm`, with no terminal** (cron, a pipe, a redirect): it prints
  the plan, says `Nothing was changed`, and exits 0 **without waiting for input**.

The plan is built from read-only calls -- which indices or database would be
replaced, the snapshot or backup chosen and its size, the directory -- so a dry
run is a real preview. One check cannot run in a dry run: for the OpenSearch
form, confirming that the snapshot holds indices matching the prefix needs the
snapshot downloaded and registered, so it runs in a confirmed run, before
anything is deleted.

Options come **before** the positional arguments (`clasm ... <leaf> --confirm
<name> <instance> ...`); an option after a positional word is treated as a
positional word. `--help` after the leaf name prints that leaf's usage.

~~~shell
# preview, then apply
clasm rdm-backup-and-restore restore-opensearch-snapshot-from-s3 \
  caltechauthors-test-v13 /opt/rdm_opensearch_backups \
  opensearch-backups.library.caltech.edu caltechauthors-v13 latest caltechauthors
clasm rdm-backup-and-restore restore-opensearch-snapshot-from-s3 \
  --confirm caltechauthors-test-v13 \
  caltechauthors-test-v13 /opt/rdm_opensearch_backups \
  opensearch-backups.library.caltech.edu caltechauthors-v13 latest caltechauthors
~~~

Exit codes are the same as for the other forms: 0 success (a dry run and a help
request included), 1 the action failed, 2 a usage error.

Two things a restore does to the repository directory that you may notice.
Before it downloads a snapshot it moves any `index-N` file and `index.latest`
already in the directory aside to `/var/tmp/clasm-stale-repo-<timestamp>/` on the
instance, because OpenSearch uses the highest `index-N` it finds and a leftover
one from an earlier Archive or Restore would hide the snapshot; it says how many
files moved and where, and deletes nothing. And it deletes every existing index
matching the prefix, including an audit-log backing index that a previous restore
left without a data stream.

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
opensearch_backup_directories:
  - pattern: "etd-*"
    directory: /opt/opensearch_snapshots
rdm_postgres_config:
  - pattern: "etd-*"
    container_name: etd-db-1
    db_name: etd
    db_user: etd
origin_tag:
  key: "Origin"
  dld_value: ""
cloud_init_extraction_security_groups:
  us-west-2: sg-ada165d0
~~~

`regions` narrows or changes which regions every listing and picker
operates against (default: us-west-1, us-west-2, if the file or key is
absent). `backup_directories` is an ordered list of `{pattern,
directory}` rules, glob-matched against an instance's Name tag, that
pre-fill the SQL backup workflows' "Backup directory" prompt (still
editable, never a silent default); `opensearch_backup_directories` is
the same shape and does the same job for the OpenSearch snapshot
workflows, kept separate because an instance's SQL and OpenSearch
directories are unrelated paths. `rdm_postgres_config` is an ordered
list of `{pattern, container_name, db_name, db_user}` rules for the RDM
SQL workflows; `db_name`/`db_user` fall back to the instance's own Name
tag when unset, while `container_name` is never guessed -- it is either
discovered live and saved back here, or edited by hand. `origin_tag`
names the tag the IAM domain treats as its DLD-ownership convention --
`key` defaults to `"Origin"`, `dld_value` defaults to empty (meaning no
value is recognized as DLD-owned yet, until your group settles on one).
`cloud_init_extraction_security_groups` maps a region to the ID of an existing
security group for the temporary instance that reads an AMI's cloud-init (Show/export
cloud-init, AMI branch); a security group belongs to one region, hence the map.
It **must allow outbound HTTPS**: the instance's SSM agent has to reach the SSM
endpoints to register, and in an account whose VPC default security group has no
outbound rules the extraction would otherwise time out. A region with no entry
uses its default group, and clasm refuses up front (rather than after three
minutes and a billable launch) if it finds that group has no outbound rule for
port 443. Set it from Configure clasm, "Edit cloud-init extraction security
groups", or by hand.
See `DESIGN.md`, "Configuration" for the full schema and
validation behavior.

## Getting Help

File an issue at
<https://github.com/caltechlibrary/clasm/issues>.

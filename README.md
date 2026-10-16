# clasm

Go TUI (clasm) with scriptable command-line support for administering Caltech Library DLD's AWS EC2 instances, AMIs, launch templates, and key pairs, plus S3 buckets and backup archives, IAM roles/instance profiles/policies, and Invenio RDM's own SQL/OpenSearch backup and restore lifecycle, with cross-resource tag management and cloud-init inspection.

## What it does

Run `clasm` for the menu. It is organised in domains, each a menu of its own:

- **Compute** -- instances, AMIs and launch templates: list and inspect, launch, start, stop, terminate, resize a root volume, create and sync launch templates, create AMIs, swap an instance's IAM instance profile, and read an instance's or an AMI's cloud-init.
- **Key Management**, **S3** (buckets, static websites, lifecycle policies, an object browser) and **IAM** (roles, instance profiles and policies, with curated role templates, including a read-only restore role).
- **Tag Management** -- add, update and remove tags across eleven kinds of resource, list every tag, and list what lacks the `Origin` tag.
- **RDM Backup & Restore** -- generate, archive and restore Invenio RDM SQL dumps and OpenSearch snapshots over SSM.
- **Configure clasm** -- regions, backup directories, the RDM Postgres rules, the Origin tag and more, kept in `~/.clasm`.

Most actions also run without the menu. The command line mirrors the menu: `clasm <domain>` opens that domain's menu, `clasm <domain> <action>` opens that one action's prompts, and `clasm <domain> <action> <arguments>` runs it non-interactively and exits. Listings take `-json` and `-jsonl`:

~~~shell
clasm compute show-instances -json
clasm tag-management show-untagged -jsonl instance ami
~~~

The command line is experimental: names and JSON keys may still change. See [docs/user_manual.md](docs/user_manual.md), "Non-interactive (CLI) Usage", for every form.

## Destructive actions and safeguards

Actions that destroy or replace something share one gate on the command line: a dry run by default that prints the plan and changes nothing, and `--confirm <name>` to run it. At a terminal, the same plan is followed by a type-the-name prompt.

Restoring an RDM database also checks where its files live. A restored database names the S3 buckets that hold its files (CaltechAUTHORS: the content bucket), and an instance that can write to one would write to production's content. **Restore SQL Backup** reads the backup's file locations first, asks IAM's policy simulator whether the instance can write to each bucket, and refuses unless each writable one is named in a `--confirm` of its own. A read-only restore role, built with clasm, is never blocked:

~~~shell
clasm iam create-role-from-template --backup-bucket sql-backups.example.org \
  --key-prefix source-instance rdm-restore-source-read-only restore-source-test
clasm iam create-instance-profile restore-source-test restore-source-test
clasm compute associate-replace-iam-instance-profile test-instance restore-source-test
clasm rdm-backup-and-restore restore-sql-backup-from-s3 test-instance sql-backups.example.org source-instance latest
~~~

The last command is a dry run. Running it again with `--confirm test-instance` applies it.

## Tagging

clasm adds the `Origin` tag to everything it creates in IAM, EC2 and S3 (never to S3 objects), so that what clasm manages can be told from the rest and found in a report. The key and the DLD-owned value come from `origin_tag` in `~/.clasm`; **with no `dld_value` set, nothing is added.** To backfill what was there before:

~~~shell
clasm tag-management show-untagged -jsonl > untagged.jsonl
clasm tag-management tag-resource instance new-data --tag origin=dld
~~~

`tag-resource` adds a missing tag, does nothing if the value is already right, and refuses a different value (changing nothing) unless `--replace` is given.

## Release Notes

- version: 0.0.11
- status: active
- released: not yet released (draft)

See `codemeta.json` (`releaseNotes`) and the GitHub release page for the notes of each version. In short, v0.0.11 adds the scriptable read-only restore role and instance-profile swap, the restore location guard with a repeatable `--confirm`, the `Origin` tag on everything clasm creates, and `show-untagged` and `tag-resource` for tagging the existing fleet.

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




# clasm

Go TUI (clasm) with scriptable command-line support for administering Caltech Library DLD's AWS EC2 instances, AMIs, launch templates, and key pairs, plus S3 buckets and backup archives, IAM roles/instance profiles/policies, and Invenio RDM's own SQL/OpenSearch backup and restore lifecycle, with cross-resource tag management and cloud-init inspection.

## Release Notes

- version: 0.0.9
- status: active
- released: 2026-09-24

clasm can now run two RDM backup archive actions directly from the command line, without the interactive menu: `clasm rdm-backup-and-restore archive-sql-backups-to-s3 <instance> <directory> <bucket> <trim-days-or-"">` and the equivalent for Archive OpenSearch Snapshot to S3. This is meant for a trusted, repeatable operation moved into a script or cron job -- the same preflight checks the interactive menu runs (AWS CLI availability, bucket access) still run, only the confirmation prompt is skipped, and an unrecognized domain/action name or the wrong number of arguments fails immediately with a usage error rather than hanging or guessing.

The command line is a small domain-specific language mirroring the menu itself: `clasm <domain>` drops straight into that domain's own menu, `clasm <domain> <action>` drops straight into that one action's own prompts, and adding the action's own arguments after that runs it non-interactively instead. Backing out of either deep-link behaves exactly like normal menu navigation -- there is no dead end, and no separate command shape to learn for scripted versus interactive use.

After a successful *interactive* run of either archive action, clasm now prints the exact non-interactive command that reproduces it, shell-quoted and ready to paste into a crontab entry -- the natural way to promote a run from "I did this by hand and it worked" to "this runs on a schedule now" without hand-transcribing arguments.

See `user_manual.md`, "Non-interactive (CLI) Usage" for the full syntax.

Note on verification: verified against real AWS -- the incomplete-argument usage error, both deep-link forms, and a full non-interactive Archive OpenSearch Snapshot to S3 run against CaltechAUTHORS production (3,196 objects, ~6.9GB synced), all confirmed live. Archive SQL Backups to S3's own non-interactive form was not separately exercised live; it shares the same code path.


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


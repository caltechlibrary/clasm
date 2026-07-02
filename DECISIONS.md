# AWS Tools — Architecture & UX Decision Log

This file records significant architectural and UX decisions for the interactive EC2/AMI manager, their rationale, and known trade-offs. New decisions are added at the top.

---

## 2026-07-02 — Configure per-instance backup directories by Name pattern

**Context.** Backup Archive & Trim's "Backup directory" prompt has no
default, by design (DESIGN.md, Feature 11) — every run requires an
explicit, deliberate value. In practice, though, the value is
predictable per machine: RDM instances all use `/opt/rdm_sql_backups`,
while some other service's instances use a different fixed path. Typing
the same known path from memory every run invites a typo, and there was
no way to record "this is the answer for this kind of instance" now
that `~/.awsops` (above) exists for exactly this kind of site-specific
setting.

**Decision.** `~/.awsops` gains `backup_directories`, an ordered list of
`{pattern, directory}` rules. `pattern` is matched against the picked
instance's Name tag using `path.Match`'s glob syntax (`*`, `?`,
`[...]`); the first matching rule's `directory` pre-fills the "Backup
directory" prompt as an editable default (`ui.WithDefault`) — pressing
Enter accepts it, typing something else overrides it. No match
(including an untagged instance with a blank Name) leaves the prompt
exactly as it behaved before this setting existed: required, no
default.

**Rationale.**
- Matching by Name (not the Project tag also carried by instances) was
  an explicit choice over Project-tag matching: Name tags already
  encode the kind of machine an operator is looking at (e.g. `rdm-*`
  covers every RDM instance across all its Projects) and glob patterns
  let one rule cover a whole family of instance names without requiring
  every instance to carry a distinct Project value.
- Pre-filling as an *editable* default, not skipping the prompt
  entirely, keeps this consistent with every other field on this
  workflow's confirmation gate: no field is silently accepted without
  the operator seeing it, since Backup Archive & Trim is a genuinely
  destructive operation (DESIGN.md, Feature 11).
- A malformed or non-matching pattern degrades to "no default" rather
  than an error — a typo'd pattern in `~/.awsops` shouldn't be able to
  break the workflow, only fail to save a keystroke.

**Rejected alternatives.**
- *Match by Project tag instead of Name.* Simpler in principle (one tag
  already used for grouping elsewhere), but doesn't fit the stated use
  case as well: multiple Projects can share the same backup directory
  convention (e.g. every RDM Project uses the same path), which a
  Name-glob (`rdm-*`) expresses in one rule where per-Project matching
  would need one entry per Project value.
- *Skip the prompt entirely on a match.* Faster, but removes the
  deliberate per-run check this workflow's other fields (age threshold,
  S3 bucket) also require with no silent defaults.

**Consequences.** `internal/config.BackupDirectoryRule` and
`BackupDirectoryFor` are new; `internal/workflow.BackupArchiveAndTrim`
gains a `backupDirRules []config.BackupDirectoryRule` parameter, wired
from `cfg.BackupDirectories` in `main.go`. `internal/workflow` now
imports `internal/config` for the first time.

---

## 2026-07-02 — Highlight PickList's prompt header when color is enabled

**Context.** Real-AWS testing surfaced a UX gap: after picking a main
menu action by number (e.g. typing 4 for Start instead of 5 for Stop),
the resulting pick list looks the same regardless of which action was
chosen -- the operator has to read through the whole list of instances
to notice the mismatch, since the only place the action name ("Select an
instance to start") appeared was buried at the bottom as the trailing
input prompt.

**Decision.** `ui.PickList` now prints its `prompt` argument as its own
line *before* the numbered list, wrapped in a bold ANSI escape when
color output is enabled (`ui.Highlight`), so the action name is the
first thing on screen. Color enablement for this is a new
package-level flag (`ui.SetColorEnabled`, read by `Highlight`), set once
in `main.go` from the same `ui.ColorEnabled()` result already computed
for `DisplayInstances`'s STATE column.

**Rationale.**
- The header must appear *before* the list, not just as the trailing
  input query, to actually save the reread the operator asked for --
  otherwise the highlighted text is the last thing read, not the first.
- A package-level flag (rather than threading a `colorEnabled bool`
  parameter through `PickList` and the ~26 call sites across 16
  workflow files that reach it) keeps this a small, low-risk change for
  a cosmetic feature. `DisplayInstances`/`DisplayImages` keep their
  existing explicit-parameter style since they're called directly from
  `main.go` with no intervening call chain.
- Bold (not a hue like green/red/yellow) avoids implying success,
  failure, or a warning -- meanings already claimed by
  `stateColor`'s use of color for instance state.

**Rejected alternatives.**
- *Thread `colorEnabled` through every workflow function down to
  `PickList`.* Consistent with `DisplayInstances`'s style, but a large
  diff (16 files, dozens of function signatures) for a purely cosmetic
  feature; revisit if a second cross-cutting per-call setting emerges
  that would justify threading a shared options struct instead.
- *Color the prompt only in its existing position (after the list).*
  Doesn't address the actual complaint -- the operator has already read
  the list by the time they reach it.

**Consequences.** `internal/ui/color.go` gains one small piece of
mutable package state (`colorEnabled`, defaulting to `false`, set once
at startup and never changed thereafter) -- acceptable since it's
write-once and read-only from that point on, and every unit test that
doesn't call `SetColorEnabled` observes the same disabled default as
before this change.

---

## 2026-07-02 — Add a `~/.awsops` YAML config file for awsops' own operational settings

**Context.** Narrowing configured regions (above) meant editing a Go
source constant and rebuilding. That's fine for a one-off, but the
conversation that led to it -- and the mention of S3 bucket settings the
S3 domain will eventually need -- made clear this will keep happening as
the tool grows into more domains (S3, CloudFront, Key Management), each
likely wanting its own site-specific defaults. Rebuilding the binary to
change a setting doesn't scale past the first one or two changes.

**Decision.** `awsops` now reads its own operational settings from an
optional YAML file at `~/.awsops` (overridable with `-config <path>`),
parsed with `gopkg.in/yaml.v3`. Scope is deliberately narrow: this file
covers only settings `awsops` itself needs to decide *how it operates*
(starting with which regions to manage) -- it explicitly does **not**
cover AWS credentials, profiles, or SSO configuration, which remain
entirely the AWS SDK's own responsibility via the standard chain
(`~/.aws/credentials`, `~/.aws/config`, environment variables) exactly
as today. `internal/config.Config` is a single flat struct with one
YAML-tagged field per setting (`Regions []string` today); the file is
optional (missing = built-in defaults, `[us-west-1, us-west-2]` for
regions), a field left unset in an otherwise-valid file falls back to
its own default independently (not all-or-nothing), and a malformed
file is a hard error, not a silent fallback.

**Rationale.**
- Directly what was asked: config that will "grow to have more fields
  over time," built for that now rather than retrofitted later, while
  keeping today's actual change (regions) the only field that does
  anything yet.
- The `~/.aws` boundary matters: conflating "which regions does awsops
  manage" with "which AWS account/credentials is this" would blur two
  genuinely different concerns and risk awsops silently reading or
  fighting with the AWS CLI's/SDK's own config files.
- Per-field defaults (not all-or-nothing) mean a config file only ever
  needs to state what's actually being overridden -- a two-line
  `regions:` file today doesn't need to also restate every future
  setting just because the file exists at all.
- No versioning/migration machinery: this is a single-operator-
  maintained local dotfile, not a multi-tenant service config pushed to
  many machines by many people -- schema evolution here is "add a field
  to the struct," not a problem that needs solving in advance.

**Rejected alternatives.**
- *JSON via the standard library, no new dependency* -- raised as the
  no-new-dependency alternative; explicitly declined in favor of YAML
  for hand-editability, with `gopkg.in/yaml.v3` approved for this
  specific use.
- *Environment variables only* -- rejected as not scaling to structured/
  list settings (a region list today; likely nested settings later, e.g.
  per-domain defaults) the way a YAML file naturally does.
- *A versioned/migrating config schema* -- rejected as solving a problem
  this tool doesn't have: there's one file, on one machine, maintained
  by the person running the tool, not a config format needing backward-
  compatibility guarantees across independently-upgrading consumers.

**Consequences.**
- New dependency: `gopkg.in/yaml.v3`.
- New package: `internal/config` (`Config`, `DefaultRegions`,
  `DefaultPath`, `Load`).
- `internal/awsclient/regions.go` and `regions_test.go` removed --
  region-list ownership moves entirely to `internal/config`;
  `internal/awsclient/client_test.go`'s sanity test now iterates a
  small test-local region literal instead of a shared package var,
  decoupling it from wherever the "real" list lives.
- `cmd/awsops/main.go` gains a `-config` flag (default
  `config.DefaultPath()`), loads the config early (failing fast on a
  parse error, matching every other startup failure mode), and uses
  `cfg.Regions` everywhere `awsclient.Regions` was read before.

---

## 2026-07-02 — Tolerate GetCommandInvocation's post-SendCommand eventual-consistency window

**Context.** A real launch succeeded all the way through `RunInstances`
and reaching `running`, then failed during the cloud-init completion
check: `Error: AWS error [InvocationDoesNotExist]: ` immediately after
`ssm:SendCommand`. The `-debug` log showed exactly one `SendCommand`
followed by exactly one `GetCommandInvocation`, which failed -- the
identical shape of bug as `InvalidInstanceID.NotFound`/`InvalidAMIID.NotFound`
(both fixed earlier this session), just on the SSM side: a newly
submitted command invocation can be briefly invisible to
`ssm:GetCommandInvocation` for a few seconds after `ssm:SendCommand`
returns its ID.

**Decision.** `RunShellCommand`'s poll loop now tolerates AWS's own
`InvocationDoesNotExist` the same way it already tolerates "not in a
terminal status yet" -- keep polling instead of returning the error. Any
other `GetCommandInvocation` error still fails immediately, unchanged.

**Rationale.** Exactly the precedent set by the two earlier fixes in
this same family (DECISIONS.md, "Tolerate DescribeInstances'
post-RunInstances eventual-consistency window"): this is documented AWS
eventual-consistency behavior, not something specific to this account,
so the fix is to expect it during polling, not to work around it
operationally.

**Rejected alternatives.** None -- same reasoning as the two prior fixes
in this family; no alternative was seriously considered.

**Consequences.**
- `internal/workflow/ssm.go`: `isInvocationNotYetVisible`, following the
  exact naming/shape convention of `isInstanceNotYetVisible`
  (`launch_execute.go`) and `isImageNotYetVisible`
  (`create_ami_execute.go`).
- This is now the third instance of the identical bug pattern found in
  as many real-AWS testing sessions (`RunInstances`/`DescribeInstances`,
  `CreateImage`/`DescribeImages`, `SendCommand`/`GetCommandInvocation`)
  -- worth remembering as a general AWS API shape (submit an async
  operation, get an ID back, immediately query that ID) rather than
  three unrelated one-off bugs, if a fourth case turns up in an
  unreviewed code path.

---

## 2026-07-02 — Validate key pair name against the AMI's region

**Context.** A real launch failed with AWS's own
`InvalidKeyPair.NotFound: The key pair 'etd-ami-test' does not exist`,
after every prompt in the flow had already been answered and confirmed.
The `-debug` log traced it to the picked AMI resolving to `us-west-1`
(a newly-surfaced official Ubuntu AMI region), while `etd-ami-test` only
exists as a key pair in `us-west-2` -- key pairs are per-region, and the
"Key pair name" prompt has always been unvalidated free text with no way
to know a typed name didn't exist in the target region until this
distant `RunInstances` failure. Narrowing configured regions (above)
reduces how often this specific pairing can occur, but doesn't fix the
underlying gap: two regions this team genuinely uses can still have
different key pairs.

**Decision.** "Key pair name" is now a pick list of key pairs that
actually exist in the AMI's region (`ec2:DescribeKeyPairs`), plus
"Create new key pair". Unlike Security group IDs/Subnet ID, there is no
"Other: type a name" escape hatch -- `ec2:DescribeKeyPairs` is a
complete, small, fully-enumerable list for a region (key pairs, unlike
AMIs or instance types, have no "public"/cross-account concept to escape
to), so a name it doesn't return is guaranteed not to work there. If the
region has zero key pairs, the list is just "Create new key pair" (with
a "No key pairs found in this region." note first) -- not a dead end,
and no ambiguous free-text guess is ever offered as the default path.
Falls back entirely to the original free-text prompt (with its "new"
keyword and the key-file-path auto-detection added earlier this session)
only if `ec2:DescribeKeyPairs` itself errors (e.g. missing permission) --
in which case there's nothing more reliable to offer than free text.

**Rationale.** Matches the pattern already established for Security
group IDs/Subnet ID (and, earlier the same session, the subnet-vs-
instance-type-AZ filtering): once a resource is region-scoped and can be
listed, offering a validated pick list instead of unvalidated free text
turns a distant, confusing AWS error into either a correct pick or an
explicit, guided "create one" step.

**Rejected alternatives.**
- *Validate the typed free-text name after the fact (check it exists,
  re-prompt if not), keeping free text as the primary input* -- rejected
  as strictly more code for the same outcome: a pick list validates by
  construction and additionally shows what's actually available, which
  a post-hoc check alone wouldn't.
- *Add an "Other" escape hatch matching Security group IDs' pattern* --
  rejected specifically for key pairs (see Decision above) since,
  unique among this tool's region-scoped pick lists, there is no
  legitimate case where a name outside `DescribeKeyPairs`' result could
  actually work.

**Consequences.**
- `internal/workflow/resource_lists.go`: `listKeyPairs`.
- `internal/workflow/create_key_pair.go`: `promptKeyPairNameOrCreate`
  rewritten to pick-list-or-create; original free-text logic preserved
  verbatim as `promptKeyPairNameFreeText`, now solely the list-error
  fallback.
- Every existing test exercising the full launch flow with a
  zero-key-pairs fake needed its key-pair input line updated from a bare
  typed name to "1) Create new key pair" + the name, since a bare fake
  with no configured key pairs now shows a 1-item pick list rather than
  accepting free text directly -- a wide but entirely mechanical ripple
  across `launch_instance_test.go`, `launch_from_cloud_init_test.go`,
  `create_instance_from_ami_test.go`, and
  `create_instance_from_cloud_init_test.go`.

---

## 2026-07-02 — Narrow configured regions to us-west-1/us-west-2

**Context.** The official-Ubuntu-AMI addition (above) surfaced a real
launch failure (`InvalidKeyPair.NotFound`) precisely because it made
`us-west-1` -- one of the four originally-configured regions this team
doesn't actually run anything in -- selectable as a base-AMI region for
the first time. The account's real resources (key pairs, security
groups, subnets already provisioned for real use) only exist in
`us-west-1`/`us-west-2` in practice; `us-east-1`/`us-east-2` were
configured from the start but never actually used.

**Decision.** `awsclient.Regions` narrowed from
`{us-east-1, us-east-2, us-west-1, us-west-2}` to `{us-west-1,
us-west-2}`. Every region-fanned-out listing, pick list, and lookup
(instances, AMIs, key pairs once Key Management ships, official Ubuntu
AMI lookup, etc.) automatically follows since they all iterate over this
one slice -- no other code changes needed.

**Rationale.** Directly shrinks the blast radius of the class of bug
just found: every region-scoped resource (key pairs, security groups,
subnets) that doesn't exist in a region this team never uses can no
longer surface unexpectedly through a feature (like the official-Ubuntu
lookup) that fans out across every configured region. This doesn't
replace the deeper fix (validating a chosen key pair actually exists in
the target region -- tracked separately) but removes the two regions
most likely to produce this exact surprise with zero cost, since nothing
runs there anyway.

**Rejected alternatives.**
- *Leave all four regions configured, rely solely on per-resource
  validation* -- rejected as not mutually exclusive with this change;
  both are worth doing. Narrowing regions fixes the "AMI in a region we
  don't use" case at the root; validation (separate work) still matters
  for genuine two-region mismatches (e.g. a key pair that only exists in
  `us-west-2` being typed while launching into `us-west-1`).

**Consequences.**
- `internal/awsclient/regions.go`, `regions_test.go` updated.
- `DESIGN.md`/`helptext.go` updated; `awsops.1.md` regenerates from
  `helptext.go` via the existing `cmt`/Makefile pipeline, not edited by
  hand.
- Every existing region-fanned-out feature (instance/AMI listing,
  official Ubuntu AMI lookup) now makes two round-trips instead of four
  per refresh/launch -- strictly less work, no behavior change beyond
  which regions are included.

---

## 2026-07-02 — Fix official Ubuntu AMI name filter pattern

**Context.** Real-AWS testing of the just-added official-Ubuntu-AMI
feature (above): the pick list showed only the account's own AMIs, no
Ubuntu entries, with no error printed -- exactly the designed best-
effort fallback behavior, which made it silent rather than obviously
broken. The `-debug` JSONL log showed why: every `EC2.DescribeImages`
call scoped to Canonical's owner ID (`099720109477`) returned zero
images, with no error, for both curated releases, every time. The
`name` filter value (`"ubuntu-noble-24.04-amd64-server-*"`, no leading
wildcard) only matches AMI names that *start* with that literal string
-- but Canonical's real, published AMI names are prefixed with a
path-like `ubuntu/images/hvm-ssd/` (or the newer
`ubuntu/images/hvm-ssd-gp3/`), so the filter could never match anything,
in any region, ever.

**Decision.** Both curated name patterns gained a leading
`ubuntu/images/hvm-ssd*/` segment --
`"ubuntu/images/hvm-ssd*/ubuntu-noble-24.04-amd64-server-*"` and the
Jammy equivalent -- anchoring to Canonical's actual documented naming
convention (the trailing `*` after `hvm-ssd` covers both the `hvm-ssd`
and `hvm-ssd-gp3` root-volume-type variants) instead of a bare suffix
match.

**Rationale.** This is exactly the kind of mistake real-AWS testing (not
unit tests against a fake) is positioned to catch: the fake's
`officialUbuntuImages` map is keyed by whatever literal string the
production code happens to pass in, so a test using the same wrong
literal for both "what the code searches for" and "what the fake
returns" passes without ever validating that string against AWS's
actual naming rules. Nothing about the unit tests was wrong; they
simply couldn't have caught this class of error on their own -- another
concrete case for why `TEST_PLAN_REAL_AWS.txt` and `-debug` remain load-
bearing, not just a formality after unit tests pass.

**Rejected alternatives.** None -- this is a factual correction to match
Canonical's real naming convention, not a design trade-off.

**Consequences.**
- `internal/workflow/official_ubuntu_amis.go`: `curatedUbuntuReleases`'
  `namePattern` values corrected; a code comment now records the exact
  failure mode (silent zero-match, not an error) so a future change to
  Canonical's naming convention is easier to recognize if it recurs.
- Test fixtures (`nobleNamePattern` constant, shared across
  `official_ubuntu_amis_test.go` and both launch-flow integration tests)
  updated to the corrected pattern for consistency, though as noted
  above this was necessary for consistency, not sufficient on its own to
  have caught the original bug.

---

## 2026-07-02 — Offer official Ubuntu LTS AMIs alongside owned AMIs when picking a base AMI

**Context.** Follow-up to clarifying the Create-from-Cloud-Init-YAML
workflow: the user's actual goal was launching an entirely new machine
from a stock base image + cloud-init, not from one of the account's
existing, already-application-specific AMIs (`plots-backup`,
`newauthers-clone-2026-06-25`, `authors-2024-03-07` -- all pre-existing
snapshots, not generic OS images). Since the AMI pick list is scoped to
AMIs the account owns (a deliberate existing decision -- otherwise the
list would include every public AMI in existence), a stock Ubuntu AMI
never appeared as an option. The user's own framing: keep it simple,
cover the likely common case (official Ubuntu images, plus what's
already owned); if something more exotic is needed, copying the
specific public AMI into the account first (already-documented guidance)
remains the answer.

**Decision.** The "Select an AMI"/"Select a base AMI" pick list (Feature
2/3) now also includes a small, curated list of official Ubuntu LTS
releases -- currently 24.04 (Noble Numbat) and 22.04 (Jammy Jellyfish),
amd64/x86_64 only -- resolved via `ec2:DescribeImages` against
Canonical's well-known, publicly documented AWS account ID
(`099720109477`), picking the single most recently published AMI per
release per region. This lookup happens once, on demand, right before
the AMI pick list is shown (not as part of the general resource-listing
refresh, which stays owned-AMIs-only, unchanged) -- launching an
instance is an infrequent, deliberate action, so a handful of extra
`DescribeImages` calls at that moment is not the same cost concern it
would be if it ran on every screen refresh. Best-effort: if the lookup
itself errors, the picker silently falls back to owned AMIs only, same
as this tool's other best-effort diagnostics.

**Rationale.**
- Matches the explicit scope given: "pretty simple," "cover the likely
  bases," with anything more exotic staying a manual (already-documented)
  copy-the-public-AMI-in step -- not a general public-AMI browser.
- amd64-only matches the curated instance-type list's architecture
  (2026-07-02, "Instance type pick list: curated shortlist, not the full
  AWS catalog") -- none of the curated instance types are Graviton/arm64,
  so offering arm64 Ubuntu AMIs would create options that don't actually
  pair with anything in the other curated list.
- Carrying `EnaSupport` through from the real `DescribeImages` response
  (not defaulting it) matters here specifically: official Ubuntu AMIs
  are modern and genuinely ENA-enabled, so without this the instance-
  type-vs-AMI-ENA-support pre-flight check (2026-07-02, above) would
  wrongly flag every one of them as incompatible with the curated
  instance types that actually work fine with them.

**Rejected alternatives.**
- *A general public-AMI browser/search* -- explicitly declined by the
  user in favor of a small curated set; a full public-AMI search is a
  much bigger feature (arbitrary owner IDs, name search, architecture
  filtering UI) that isn't needed for the stated common case.
- *Include arm64/Graviton variants now* -- deferred: no curated instance
  type could launch one today; revisit if/when Graviton types are added
  to the curated instance-type list.
- *Fetch these as part of the general resource-listing refresh* --
  rejected: that listing is specifically scoped to "what does this
  account own" (an oversight/inventory view); Canonical's AMIs aren't
  owned by the account and aren't something this team needs to track,
  only something useful at the moment of picking a base image.

**Consequences.**
- `internal/workflow/official_ubuntu_amis.go` (new): `latestUbuntuAMI`,
  `listOfficialUbuntuAMIsInRegion`, `listOfficialUbuntuAMIs`,
  `imagesWithOfficialUbuntu`.
- `launch_instance.go`/`launch_from_cloud_init.go`: both AMI pick lists
  now go through `imagesWithOfficialUbuntu` before display.
- No new AWS permissions -- `ec2:DescribeImages` is already required
  (DESIGN.md, Assumptions), and querying it against a different `Owners`
  value needs no additional IAM grant.

---

## 2026-07-02 — Create EC2 Instance from Cloud-Init YAML always reads from a file

**Context.** Immediately after fixing the bare-filename-without-"@" bug
(above) for the shared `loadUserData` path, follow-up feedback: for this
specific workflow, the "inline text or @file path" duality is itself the
wrong shape. Feature 3's whole premise is that the cloud-init YAML is
the primary input, not an optional add-on -- and a real cloud-init YAML
document is realistically always authored as a file (e.g. from
`cloud-init-examples`), never typed inline at a terminal prompt. The
`@`-prefix convention exists specifically to disambiguate inline text
from a file reference within one prompt; if inline text was never a
realistic input for this prompt in the first place, the convention (and
the exact mistake it enabled) doesn't need to exist here at all.

**Decision.** `CollectLaunchInstanceParamsFromCloudInit`'s cloud-init
prompt no longer shares `loadUserData` with Feature 2. It now calls a
dedicated `promptCloudInitYAMLFile`, which always treats the input as a
file path (an optional leading `@` is stripped if present, for muscle
memory, but not required) and re-prompts with a clear "cannot read"
message on a missing/unreadable file, instead of ever falling back to
using the raw input as literal text. Feature 2's separate, optional
"User data" field is unchanged -- it still supports genuine inline text
via `loadUserData`, since an ad hoc one-line script typed directly is a
realistic input there.

**Rationale.** Removes the entire failure mode (forgetting `@`) at its
root for this specific prompt, rather than just detecting and recovering
from the one shape of mistake found in real use (a bare filename that
happens to match a real file). A missing or unreadable file now fails
clearly and immediately, with a chance to retry, instead of either the
old silent-literal-text behavior or the newer auto-detection's narrower
"only if a file happens to exist at that exact string" coverage.

**Rejected alternatives.**
- *Keep sharing `loadUserData`, rely on the auto-detection fix alone* --
  rejected: that fix only helps when the mistyped value happens to
  match a real file; a typo'd filename (or a path relative to the wrong
  directory) would still silently become literal garbage user-data,
  since `loadUserData` has no way to know this particular prompt never
  wants inline text.
- *Also require file-only input for Feature 2's "User data" field* --
  out of scope for this decision: that field is optional and genuinely
  sometimes holds a short ad hoc script typed directly, unlike Feature
  3's mandatory, always-a-real-document cloud-init YAML.

**Consequences.**
- `internal/workflow/userdata.go`: new `promptCloudInitYAMLFile`.
- `launch_from_cloud_init.go` no longer calls `loadUserData` at all --
  only `launch_instance.go`'s optional "User data" field does now.
- Existing tests exercising this prompt with inline `"#cloud-config"`
  text were rewritten to use real temp-file fixtures
  (`writeCloudInitFixture` helper), since inline text is no longer a
  supported input here.

---

## 2026-07-02 — Auto-detect a bare existing-file path in User data / Cloud-init YAML input

**Context.** Real-world use: at "Cloud-init YAML (inline text or @file
path)", the operator typed `newt-machine.yaml` (a real file in the
current directory) without the required `@` prefix. `loadUserData`
correctly followed its documented contract -- no `@`, so treat it as
literal inline text -- and the flow moved straight on to picking a base
AMI, with the *filename itself* silently captured as the instance's
user-data. Nothing was technically wrong per the existing contract, but
the outcome (an instance launched with `newt-machine.yaml` as its literal
user-data, not the file's contents) is never what an operator actually
wants -- a bare filename is not valid cloud-init YAML or any other
sensible literal user-data.

**Decision.** `loadUserData` (shared by Features 2 and 3's User data /
Cloud-init YAML prompts) now checks, when given input with no `@`
prefix, whether a file actually exists at that exact path (relative to
the current directory, or absolute). If one does, it's loaded anyway,
with an on-screen note explaining what happened and reminding the
operator to prefix with `@` next time. If no such file exists, the
input is used as literal inline text exactly as before -- this is
additive, not a behavior change for genuine inline text (e.g.
`#cloud-config...`, which never coincides with a real file on disk).

**Rationale.** Same reasoning as the key-pair-filename fix (2026-07-02,
above): when a value can only plausibly be a mistake for a file
reference -- here, "this string is byte-for-byte the name of a real
file, and is not itself valid YAML" -- silently accepting it as literal
text produces a working-looking launch with silently wrong data, which
is worse than either rejecting it or (as chosen) just doing what the
operator almost certainly meant.

**Rejected alternatives.**
- *Require `@` strictly, reject anything else that looks like a bare
  filename* -- rejected: rejecting a value that unambiguously
  corresponds to a real, readable file just because of a missing prefix
  character is unhelpful friction for a case this tool can resolve with
  total confidence.
- *Warn but don't auto-load, forcing a re-prompt* -- rejected as an
  unnecessary extra round-trip when the file both exists and is
  immediately loadable; the printed note already tells the operator
  what happened and how to be explicit next time, without making them
  retype anything.

**Consequences.**
- `loadUserData`'s signature gained a `*termlib.Terminal` parameter (for
  the explanatory note); both call sites (`launch_instance.go`,
  `launch_from_cloud_init.go`) already had `t` in scope.
- No new AWS permissions or calls -- purely local filesystem/string
  handling around a value already being collected.

---

## 2026-07-02 — Move "Show resource lists" to the top of the Compute menu; rename from "Refresh"

**Context.** User feedback: "Refresh resource lists" sat near the bottom
of the Compute menu (item 11 of 12), and "Refresh" was ambiguous about
what it actually does (re-fetch from AWS and redisplay both tables, not
just repaint the screen). Since every other successful action already
triggers an automatic refresh afterward (2026-06-30, "Refresh data after
each operation"), this item's real purpose is letting the operator
deliberately re-orient -- see current state -- without taking an action,
which is a natural first move on entering the domain, not action #11 of
12.

**Decision.** Renamed to "Show resource lists" and moved to menu
position 1; every other item shifts down by one, "Back to domain picker"
stays last (position 12, unchanged, since the total item count didn't
change). The underlying behavior and `MenuActions.Refresh` field name
are unchanged -- this is a label and position change only.

**Rationale.** Matches how the operator actually uses this tool: check
what's running, then act -- not "act ten times, and eleventh, maybe
check." "Show" also describes the operator-visible effect (the two
tables reappear) rather than an AWS-side connotation ("refresh" could
read as "refresh the AWS resources themselves").

**Rejected alternatives.** None seriously considered -- this is a small,
low-risk UX tweak; no behavior, permissions, or data flow changes.

**Consequences.**
- `internal/workflow/menu.go`'s `mainMenuItems` reordered; every test in
  `menu_test.go` that referenced a menu item by number was updated to
  match (item numbers shift by one; "Back to domain picker" stays 12).
- `DESIGN.md`'s Compute Menu ASCII diagram and `TEST_PLAN_REAL_AWS.txt`'s
  menu-order checklist updated to match, preserving existing `[ok]`
  markers against their renamed/renumbered items (the capability was
  already verified; only its label and position changed). Also fixed
  unrelated staleness noticed while there: `TEST_PLAN_REAL_AWS.txt` still
  said item 12 was "Exit" from before the domain-picker refactor
  (2026-07-02, "Redesign navigation as a domain picker...") -- corrected
  to "Back to domain picker".

---

## 2026-07-02 — Filter the subnet picker by instance-type Availability Zone support

**Context.** Real-AWS testing surfaced repeated back-and-forth: pick an
instance type, pick a subnet, get told after the fact ("Instance type
... is not offered in ... this subnet's Availability Zone") that the two
don't work together, then recover via a pick list. The user's framing:
"the choices are out of context" -- since instance type is already
chosen by the time Subnet ID is prompted, the tool already has enough
information to never offer an incompatible subnet in the first place,
rather than offering it and then walking the operator back out.

**Decision.** `promptSubnetID` now takes the already-chosen
`instanceType` and narrows its subnet listing to those whose
Availability Zone actually offers it
(`filterSubnetsByInstanceTypeAZ`, reusing `instanceTypeOfferedAZs`) --
instance type stays the first choice (unchanged position in the flow;
it's the workload-driven decision -- cost, performance, ENA-compatibility
with the AMI), and the network choice narrows around it, not the other
way around. Filtering is best-effort and never a dead end: if the
AZ-offerings lookup itself errors, or if filtering would leave zero
subnets to pick from, `promptSubnetID` falls back to showing the full,
unfiltered list. `ensureInstanceTypeSupportedInSubnet`'s reactive
recovery pick list (2026-07-02, above) is unchanged and stays in place as
the safety net for exactly those two fallback cases (and the free-text-
fallback path, where the AZ isn't known at all) -- in the common case
where filtering succeeds, that reactive check now simply finds the
already-filtered subnet compatible on the first try and returns
immediately, invisible to the operator.

**Rationale.** Matches the "different routes through the same choices
should all reach a running system" framing directly: narrowing options
before they're offered, instead of discovering and recovering from an
incompatible combination after the fact, removes a whole category of
back-and-forth without removing any actual capability -- every subnet
that could have worked is still offered; only the ones that couldn't
are pre-filtered out.

**Rejected alternatives.**
- *Reorder to prompt for subnet before instance type* -- rejected (see
  the exploratory discussion this decision follows from): instance type
  is the more workload-driven decision and should stay a free first
  choice; subnet is more of an implementation detail that can be
  narrowed once the type is known. Reordering would also do nothing for
  the unrelated instance-type-vs-AMI-ENA-support check, which depends on
  the AMI (chosen long before either instance type or subnet), not the
  network.
- *Remove `ensureInstanceTypeSupportedInSubnet` now that filtering
  exists* -- rejected: it's still the only thing that catches an
  incompatibility when filtering itself couldn't run (lookup error) or
  couldn't narrow anything (all known subnets incompatible) or wasn't
  attempted at all (free-text fallback, unknown AZ). Removing it would
  turn those cases back into dead ends.

**Consequences.**
- `promptSubnetID`'s signature gained an `instanceType string` parameter;
  its three call sites (`launch_instance.go`, `launch_from_cloud_init.go`,
  and `ensureInstanceTypeSupportedInSubnet`'s "Pick a different subnet"
  branch) already had the instance type in scope, so no new plumbing was
  needed beyond passing it through.
- No new AWS permissions or calls in the common case -- the same
  `ec2:DescribeInstanceTypeOfferings` call `ensureInstanceTypeSupportedInSubnet`
  already made reactively now happens once, proactively, per launch.

---

## 2026-07-02 — Tolerate DescribeInstances' post-RunInstances eventual-consistency window

**Context.** A real launch (subnet/instance-type mismatch resolved,
confirmed, `RunInstances` succeeded) immediately failed anyway: `Launched
i-088ab06fb0c16eb0b, waiting for it to reach running... Error: AWS error
[InvalidInstanceID.NotFound]: The instance ID 'i-088ab06fb0c16eb0b' does
not exist`. This is documented AWS behavior, not a real failure: a newly
launched instance ID can be briefly invisible to `ec2:DescribeInstances`
for a few seconds after `ec2:RunInstances` returns it, before the
instance is fully registered. `waitUntilState` (backing `WaitUntilRunning`,
used by every launch and by Start Instance) treated *any*
`DescribeInstances` error as fatal, so this blocked every single launch
that happened to hit the window -- not an edge case, a near-certain
race every time.

**Decision.** `waitUntilState` now tolerates AWS's own
`InvalidInstanceID.NotFound` the same way it already tolerates "not in
the wanted state yet" -- keep polling instead of returning the error.
Any other `DescribeInstances` error still fails immediately, unchanged.
Found and fixed the identical exposure on the AMI side while here:
`WaitForAMIAvailable` could hit the equivalent `InvalidAMIID.NotFound`
right after `ec2:CreateImage` returns, before `ec2:DescribeImages`
recognizes the new image -- same tolerance added there
(`isImageNotYetVisible`), even though it hadn't been reported yet, since
it's the exact same class of bug on the exact same code path shape.

**Rationale.** This is a well-known, documented AWS eventual-consistency
behavior (not specific to this account or these instances) -- the fix is
to expect it, not work around it operationally (e.g. "just retry the
whole launch"). Fixing the AMI-side analog preemptively, rather than
waiting for a second bug report, matches this session's pattern of
fixing the failure *class*, not just the exact reported instance of it.

**Rejected alternatives.**
- *Retry the whole launch flow on this error* -- rejected: the instance
  already launched successfully; retrying `RunInstances` would create a
  second, redundant instance instead of just waiting a few more seconds
  for the first one to become visible.
- *A fixed short sleep before the first `DescribeInstances` call* --
  rejected in favor of tolerating the specific error code during normal
  polling: simpler, no new timing constant to tune, and self-correcting
  regardless of how long the window actually is.

**Consequences.**
- `internal/workflow/launch_execute.go`: `isInstanceNotYetVisible`.
- `internal/workflow/create_ami_execute.go`: `isImageNotYetVisible`.
- No new AWS permissions -- purely client-side error handling around
  calls this tool already makes.

---

## 2026-07-02 — Add non-ENA-required options to the curated instance type list

**Context.** Trying to launch from a real, legacy AMI (`etd-workflow-v0.0.1`)
that isn't ENA-enabled, every entry in the newly-added curated instance
type list (t3/m5/c5/r5) failed `ensureInstanceTypeENACompatible` --
all nine are Nitro-based and require ENA unconditionally. The "Change
instance type" recovery pick list technically worked, but every
alternative it could offer was equally incompatible: the operator was
launch-blocked with no way to get unstuck without already knowing (from
outside awsops) that e.g. `t2.micro` would work, and typing it via
"Other". This is a real gap in the curated list, not a one-off: any
sufficiently old AMI (common for long-lived, hand-maintained gold
images) hits the same dead end.

**Decision.** Add `t2.micro` and `t2.medium` to `curatedInstanceTypes`
as the list's only non-Nitro, no-ENA-required entries, each labeled
"no ENA required, works with older/legacy AMIs" so they're
self-explanatory in the pick list, not just a name an operator has to
already recognize. Every ENA-requiring entry's label now also says
"(requires ENA)" for the same reason -- so the *first* pick, not just
the recovery pick, can be an informed choice. `ensureInstanceTypeENACompatible`'s
incompatibility message now explicitly suggests these two by name, plus
a one-line pointer to the actual (out-of-scope-for-awsops) permanent
fix: enabling ENA on the source instance and re-creating the AMI.

**Rationale.** The recovery pick list (DECISIONS.md, "Pre-flight check:
instance type vs. AMI ENA support") is only actually useful if it can
offer *some* type that works; a list where every option shares the same
failure mode isn't a recovery path, it's a longer way to the same dead
end. Two low-cost, universally-available legacy types cover the
common case without expanding the list's scope back toward "the full
AWS catalog," which the curated-list decision (above) already rejected.

**Rejected alternatives.**
- *Make promptInstanceType AMI-aware (filter or reorder based on the
  picked AMI's EnaSupport)* -- rejected as unnecessary complexity for
  now: the static list already contains a working answer once it
  includes non-ENA options; the pre-flight check's message pointing at
  them by name accomplishes the same practical outcome without the
  static-list design changing shape or `promptInstanceType` needing new
  parameters/context it didn't have before.
- *Only fix it via the incompatibility message, without adding to the
  curated list* -- rejected because the *first* instance-type pick
  (before any AMI-compatibility check has even run) should also be able
  to make an informed choice for a known-legacy AMI, not just recover
  from a bad one after the fact.

**Consequences.**
- `curatedInstanceTypes` grew from 9 to 11 entries; "Other" shifted from
  pick-list position 10 to 12.
- No new AWS permissions or calls -- purely a static list change plus a
  message update.

---

## 2026-07-02 — Pre-flight check: instance type vs. AMI ENA support

**Context.** Real-AWS testing hit `AWS error [InvalidParameterCombination]:
Enhanced networking with the Elastic Network Adapter (ENA) is required
for the 't3.small' instance type. Ensure that you are using an AMI that
is enabled for ENA.` -- this is the ENA pre-flight check idea already
queued in TODO.md since an earlier session (two real launch failures
that day: AMI `ami-0da49db6a772dda02` isn't ENA-enabled, both `t3.micro`
and now `t3.small` require it). With the AZ pre-flight check (above)
just implemented as a template, this was the natural next failure class
to close.

**Decision.**
- `inventory.Image` gained an `EnaSupport bool` field, populated for
  free from the same `ec2:DescribeImages` call `ListImages` already
  makes (the SDK's `Image.EnaSupport` field) -- no extra AWS call for
  the AMI side.
- After Instance type is picked (Feature 2/3), check whether it
  requires ENA (`ec2:DescribeInstanceTypes`,
  `NetworkInfo.EnaSupport == Required`) and, if so, whether the
  already-picked AMI supports it. If not, print the incompatibility and
  show a pick list: **Change instance type** or **Abort this launch** --
  no "pick a different AMI" option, unlike the AZ check's "pick a
  different subnet": swapping the AMI this late would mean redoing
  earlier choices that depend on it (e.g. the Project tag default), so
  aborting and restarting covers that case instead, same as any other
  declined confirmation.
- "Abort" reuses `ui.ErrCancelled`, same as the AZ check.
- "Change instance type" reuses `promptInstanceType` (the curated pick
  list, below), not a bespoke free-text prompt -- for consistency, both
  pre-flight checks' recovery flows now go through the same instance-
  type entry point.

**Rationale.**
- Closes the exact TODO.md item this team already flagged, using the
  same pick-list-recovery pattern just established for the AZ check
  rather than inventing a third UX shape.
- Getting the AMI's `EnaSupport` for free from data already fetched
  avoids adding a new per-check AWS call on the AMI side.

**Rejected alternatives.**
- *Also offer "pick a different AMI"* -- rejected because the AMI's
  already-collected downstream effects (Project tag default, region-
  scoped clients) would need to be redone; abort-and-restart is simpler
  and matches this tool's existing cancellation semantics.
- *A shared multi-check framework covering both AZ and ENA together* --
  still rejected for now, per the AZ check's own decision above: two
  checks doesn't justify an abstraction yet.

**Consequences.**
- New EC2 permission required: `ec2:DescribeInstanceTypes` (see
  `DESIGN.md`, "Assumptions").
- `internal/workflow/instance_type_ena_check.go` (new):
  `instanceTypeRequiresENA`, `ensureInstanceTypeENACompatible`,
  `enaIncompatibilityChoices` (reuses `incompatibilityChoice`/
  `incompatibilityChoiceLabel` from the AZ check).

---

## 2026-07-02 — Instance type pick list: curated shortlist, not the full AWS catalog

**Context.** The "Instance type" prompt was free text with only a
suggested default (`t3.micro`). Asked whether it could become a pick
list like Security group IDs/Subnet ID. AWS offers 600+ instance types
per region -- listing them all (even paginated) would reproduce, at a
much larger scale, the exact "flat list of every key pair in the
account... was noise, not help" problem already found and rejected for
key pairs at just 16 entries (2026-07-01 decision, "Support creating a
new key pair from within awsops"). A full list would also need
architecture filtering (x86_64 vs. arm64) against the picked AMI to
avoid creating a *new* incompatibility class right after fixing three
others this session (key pair name, IAM instance profile, instance-
type/AZ) -- `inventory.Image` doesn't carry AMI architecture today.

**Decision.** `promptInstanceType` offers a short, hand-picked list of
~9 types relevant to this team's actual usage (t3 family for testing/
small Invenio RDM instances, m5/c5/r5 for steady-state/compute/memory-
optimized needs), each labeled with vCPU/memory, plus "Other" to type
any value not listed. No AWS call is made to build this list -- it's
static. The instance-type-vs-AZ and instance-type-vs-ENA pre-flight
checks (this file, both entries above) are what actually validate
whatever value is chosen (curated or typed) against AWS, so the list
itself doesn't need to be exhaustive or live to be safe.

**Rationale.** Matches this project's established preference (key
pairs) for a short, curated list plus an escape hatch over an
exhaustive one; the real safety net against picking an incompatible
type is the two pre-flight checks, not an exhaustive picker.

**Rejected alternatives.**
- *Full list filtered by region + AMI architecture* -- rejected for
  now: still likely 100-300+ entries even filtered, and requires adding
  Architecture to `inventory.Image` and a new filtering call. Worth
  reconsidering if the curated list proves too restrictive in practice.
- *Full list, region-only, no architecture filter* -- rejected as the
  most noise for the least benefit: hundreds of entries, some of which
  wouldn't even work with the picked AMI.

**Consequences.**
- `internal/workflow/launch_prompts.go` gained `promptInstanceType`,
  `curatedInstanceTypes`, `instanceTypeChoice`/`instanceTypeChoiceLabel`.
  No new AWS permissions -- the list is static.
- The "Change instance type" recovery step in both pre-flight checks
  (above) now goes through this same function, so a corrected value
  also comes from the curated list + "Other", not a separate free-text
  prompt.

---

## 2026-07-02 — Pre-flight check: instance type vs. subnet Availability Zone

**Context.** The `-debug` JSONL log from the same real-AWS testing
session that surfaced the key-filename bug (above) showed a second,
unrelated real failure in the middle of that sequence: once the key
pair name was correct, `RunInstances` failed with `Unsupported: Your
requested instance type (t2.micro) is not supported in your requested
Availability Zone (us-west-2d). Please retry your request by not
specifying an Availability Zone or choosing us-west-2a, us-west-2b,
us-west-2c.` -- the picked subnet (`subnet-5870b473`) sits in
`us-west-2d`, which doesn't offer `t2.micro`. This is the same general
class of problem as the already-deferred ENA pre-flight check idea
(TODO.md) -- an instance-type/launch-parameter incompatibility AWS only
reports after the fact -- but a different specific incompatibility (AZ
offering, not ENA support).

**Decision.**
- After the Subnet ID prompt (Feature 2/3), check whether the
  already-chosen instance type is actually offered in the picked
  subnet's Availability Zone (`ec2:DescribeInstanceTypeOfferings`,
  `LocationType=availability-zone`).
- If it isn't, print the incompatibility and (best-effort) the AZs the
  instance type *is* offered in, then show a pick list: **Change
  instance type**, **Pick a different subnet**, or **Abort this
  launch** -- rather than a dead-end error message or silently sending
  a `RunInstances` call already known to fail.
- "Abort this launch" returns `ui.ErrCancelled`, reusing the exact same
  cancellation path every other declined/cancelled confirmation in this
  tool already uses (`CreateInstanceFromAMI`/`CreateInstanceFromCloudInit`
  catch it and print "Cancelled.", returning to the domain menu) --
  no new cancellation mechanism needed.
- The check is skipped entirely (not just tolerantly failed) when the
  subnet's Availability Zone is unknown -- i.e. `promptSubnetID` fell
  back to its free-text prompt -- or when the check call itself errors,
  matching this tool's existing "best-effort diagnostic, never blocks
  the whole flow" pattern (e.g. SSM-unavailable fallbacks).
- Scoped to this one incompatibility class for now, not a general
  multi-check framework -- the ENA-support variant (TODO.md) remains a
  separate, not-yet-implemented item; if a third class of
  incompatibility turns up, that's the point to reconsider a shared
  abstraction, not before.

**Rationale.**
- Fixes a real failure found in the same debug-log session as the key-
  filename bug, using the tool that made it discoverable in the first
  place (the `-debug` log) rather than guessing.
- A pick list of concrete remediation options (change type / change
  subnet / abort) is more actionable than a printed error the operator
  has to interpret and act on by restarting the flow -- and matches an
  explicit request that error recovery should offer a pick list, not
  just a message.
- Reusing `ui.ErrCancelled` for "abort" avoids inventing a second
  cancellation contract alongside the one this tool already has.

**Rejected alternatives.**
- *Generalize into a multi-check pre-flight framework covering ENA and
  AZ together* -- rejected for now as premature: only one check is
  actually implemented; building shared abstraction for a framework of
  one (plus one still-deferred idea) isn't justified yet.
- *Just show a better error message, no recovery pick list* -- rejected
  per explicit direction that a pick list to correct or abort is the
  right shape once a chosen setting turns out to be invalid.

**Consequences.**
- `promptSubnetID`'s return type changed from `(string, error)` to
  `(SubnetInfo, error)`, so its caller has the picked subnet's
  Availability Zone available without a redundant lookup -- `SubnetInfo`
  already carried this field for the pick-list label. The free-text
  fallback path returns `SubnetInfo{SubnetID: ...}` with an empty
  `AvailabilityZone`, which is exactly the "unknown, skip the check"
  signal `ensureInstanceTypeSupportedInSubnet` looks for.
- New EC2 permission required: `ec2:DescribeInstanceTypeOfferings` (see
  `DESIGN.md`, "Assumptions").
- No new AWS SDK dependency -- `DescribeInstanceTypeOfferings` is
  already part of the `ec2` package this tool depends on.

---

## 2026-07-02 — Derive the AWS key pair name from a private key filename/path

**Context.** Real-AWS testing hit `AWS error [InvalidKeyPair.NotFound]:
The key pair '~/.ssh/etd-ami-test.pem' does not exist` -- the operator
typed the private key's file path at "Key pair name" instead of the AWS
key pair name, despite the prompt's own explicit "not a local file path"
wording. The `-debug` JSONL log showed the full sequence of what was
tried in one session: `etd-ami-test.pem` (bare filename with extension,
no directory) failed the same way, then the correct bare name
`etd-ami-test` worked (it got past `RunInstances`' key-pair check
entirely and failed for an unrelated reason -- see the separate
instance-type/Availability-Zone finding this surfaced, tracked in
TODO.md), then `~/.ssh/etd-ami-test.pem` was tried and failed again.
`ssh -i` muscle memory makes typing the key *file* rather than the key
*pair name* a recurring mistake, not a one-off typo.

**Decision.**
- `promptKeyPairNameOrCreate` now recognizes input that looks like a
  private key filename or path -- contains `/`, starts with `~`, or ends
  in `.pem`/`.ppk`/`.key` (case-insensitive) -- as distinct from a bare
  AWS key pair name.
- When recognized, the file is validated as actually readable (`~` is
  expanded against the home directory; a bare filename with no directory
  component that isn't readable relative to the current directory is
  also checked against this tool's own key directory, since that's where
  Create Key Pair saves keys and where a bare filename most plausibly
  lives) before anything is derived from it -- an unreadable path
  re-prompts with a clear local error instead of being sent to AWS,
  which would otherwise fail distantly and confusingly.
- Once validated, the AWS key pair name is derived from the file's
  basename with its extension stripped (e.g. `~/.ssh/etd-ami-test.pem`
  or bare `etd-ami-test.pem` -> `etd-ami-test`) and used as the launch's
  key pair name, with an on-screen note explaining what happened so the
  operator isn't surprised by what gets sent to AWS.
- This works because it's this tool's own convention: Create Key Pair
  (`createKeyPair`) always saves a new key's private material to exactly
  `<keyDir>/<name>.pem`, so the filename reliably encodes the real AWS
  key pair name regardless of which directory (or none) the operator
  typed.

**Rationale.**
- Fixes the actual reported failure and the two variants of it found in
  the same debug session (bare filename-with-extension, and full path),
  not just the one exact string from the bug report.
- Auto-deriving (rather than just rejecting with a "that looks like a
  path" message) is safe specifically because this tool controls the
  naming convention on the writing side (Create Key Pair) as well as the
  reading side (this prompt) -- it isn't guessing at an external
  convention it doesn't control.

**Rejected alternatives.**
- *Reject with a clarifying error instead of deriving* -- raised as an
  explicit scope question and declined in favor of the more helpful
  auto-derive path, consistent with this project's general preference
  (Security groups/Subnet ID, IAM instance profile) for fixing an
  AWS-error class locally rather than just describing it better.

**Consequences.**
- `internal/workflow/create_key_pair.go` gained `looksLikeKeyFilename`,
  `keyPairNameFromFilePath`, and `isReadableFile`; the existing "new"
  sub-flow was extracted into `createNewKeyPairInteractive` unchanged, so
  `promptKeyPairNameOrCreate` could loop on a bad key-filename input
  without duplicating that retry logic.
- No new AWS permissions or SDK dependencies -- this is entirely local
  validation before any AWS call is made.

---

## 2026-07-02 — Support picking or creating an IAM instance profile from within awsops

**Context.** Real-AWS testing of Create EC2 Instance from AMI hit AWS's
own error: `AWS error [InvalidParameterValue]: Value (ec2-invenio-role)
for parameter iamInstanceProfile.name is invalid. Invalid IAM Instance
Profile name`. The "IAM instance profile" prompt was free text whose own
hint pointed at "IAM console > Roles" -- but `ec2:RunInstances`'
`IamInstanceProfile.Name` parameter needs the *instance profile* name,
not the *role* name. The two are identical by convention when a role is
created via the AWS console (which auto-creates a matching instance
profile), but not by requirement -- a role created via Terraform/CLI
without an accompanying instance profile of the same name breaks that
assumption silently, and free text let the mismatch through uncaught
until AWS rejected it. The user asked for "a means to picking a profile
(or creating one)."

**Decision.**
- The "IAM instance profile" prompt becomes a pick list of real instance
  profiles (`iam:ListInstanceProfiles`), each labeled with its attached
  role name(s) for clarity -- eliminating the role-name/profile-name
  mix-up at the source, since only real instance profile names are
  selectable.
- Unlike Security group IDs/Subnet ID's pick lists (which fall back to
  free text when the list is empty, since those fields are required and
  there's nothing else useful to offer), this field's list always
  includes a "(none)" entry (this field is optional) and a "Create new
  instance profile (attach an existing role)" entry, even when zero
  instance profiles currently exist -- because covering "I don't have
  one yet" is the whole point of the "or creating one" half of the
  request, not just a nice-to-have when profiles happen to already
  exist. The prompt falls back to the original free-text prompt only if
  the list call itself errors (e.g. missing `iam:ListInstanceProfiles`
  permission), matching the existing security-group/subnet fallback
  pattern for "can't reliably present anything better."
- "Create new instance profile" is scoped to **attaching an existing IAM
  role**, not also creating a new role: pick a role via `iam:ListRoles`,
  prompt for a new instance profile name (defaulting to the role's own
  name, matching the AWS console's own convention), then
  `iam:CreateInstanceProfile` + `iam:AddRoleToInstanceProfile`. A name
  collision re-prompts for a different name, mirroring Create Key Pair's
  collision handling (2026-07-01 decision, above). If there are zero IAM
  roles in the account, "Create new" prints an explanatory message and
  redisplays the instance-profile picker rather than failing outright.
- The success message notes that a newly created instance profile can
  take a few seconds to propagate before `ec2:RunInstances` will accept
  it (a well-known IAM eventual-consistency behavior) -- so a
  launch-time "instance profile not found" error right after creating
  one reads as "wait a moment and retry," not a new bug.

**Rationale.**
- Fixes the actual reported failure at its root: the field is now always
  populated (when non-blank) with a real instance profile name, not a
  free-text guess that might actually be a role name.
- Scoping "create" to attaching an existing role avoids a much bigger,
  genuinely separate design question -- what trust policy and what
  permissions a brand-new role should get by default -- which is a
  real security-relevant default this project shouldn't make silently.
  An operator who needs a new role can create it via the IAM console (or
  Terraform, matching how these roles are provisioned today) and then
  attach it here.

**Rejected alternatives.**
- *Also support creating a brand-new role* -- rejected for this round;
  raised as an explicit scope question and declined in favor of the
  simpler, no-new-security-defaults "attach an existing role" path.
- *Fall back to free text whenever the list is empty*, matching Security
  group IDs/Subnet ID exactly -- rejected because it would leave
  "creating one" unreachable in the (arguably common) case of a fresh
  account or a team that has never made an instance profile through this
  tool before, defeating the point of the feature request.

**Consequences.**
- New AWS SDK dependency: `github.com/aws/aws-sdk-go-v2/service/iam`.
- New IAM permissions required: `iam:ListInstanceProfiles`,
  `iam:ListRoles`, `iam:CreateInstanceProfile`,
  `iam:AddRoleToInstanceProfile` (see `DESIGN.md`, "Assumptions").
- `CollectLaunchInstanceParams`/`CollectLaunchInstanceParamsFromAMI`/
  `CreateInstanceFromAMI`/`CreateInstanceFromCloudInit` all gained an
  `awsclient.IAMAPI` parameter (a single global client, like STS/S3 --
  IAM is account-wide, not region-scoped, so it doesn't need the
  per-region client maps EC2/SSM use).
- `-debug`'s JSONL log now covers IAM calls too
  (`internal/awsclient/logging_iam.go`, `WrapIAM`), via the same shared
  generic logging helper the other wrappers use -- no special redaction
  needed here, unlike `CreateKeyPair`'s private-key material.

---

## 2026-07-02 — CloudFront + OAC by default for static websites, not public-read buckets

**Context.** Scoping the new S3 domain's static-website primitive
(Feature 19/24 in `DESIGN.md`) raised a real security-relevant default:
the classic "S3 static website hosting" pattern most tutorials show
makes the bucket world-readable via a public-read bucket policy. This
team already treats public-AMI/public-exposure as something to warn
about explicitly (`DESIGN.md` Security Considerations #4), and a tool
that makes "public S3 bucket" the path of least resistance for every new
static site works against that stance.

**Decision.** Feature 18 (Create Bucket) enables
`s3:PutPublicAccessBlock` (all four settings) by default on every new
bucket. Feature 19 (Configure Static Website Hosting) only sets the
bucket's website document config; it does not open public access.
Standing up an actual public-facing site is Feature 24 (Create
Distribution): CloudFront + a per-distribution Origin Access Control,
with the bucket policy scoped to that distribution's ARN
(`s3:PutBucketPolicy` restricted by `AWS:SourceArn`) so the bucket stays
private and only that CloudFront distribution can read it. A public-read
bucket policy remains available as an explicit opt-out inside Feature
19, gated by its own separate confirmation that plainly states the
bucket becomes world-readable directly — never the default, never
reachable by just accepting defaults through the flow.

**Rationale.**
- Matches this tool's existing posture toward exposure (dry-run/warn
  before anything that broadens what's publicly reachable).
- CloudFront in front of a private bucket is also just better practice
  independent of security — caching, HTTPS, and a real CDN domain name
  come for free, not just access control.
- Keeping the public-read path available (not removed) avoids blocking a
  legitimate simple-case use if someone genuinely wants it, while making
  sure it's never the accidental default.

**Rejected alternatives.**
- *Public-read bucket policy as an equal, unranked option* — considered
  and rejected in this session's design discussion; the concern was that
  presenting both paths as equivalent choices, with no recommended
  default, makes it too easy to pick the less safe one out of habit or
  unfamiliarity with OAC.

**Consequences.**
- Feature 24 (Create Distribution) needs write access to the bucket
  policy (`s3:PutBucketPolicy`) in addition to CloudFront permissions —
  see `DESIGN.md` Assumptions.
- Standing up a fully working static site now requires walking through
  two features (19 then 24) rather than one; `DESIGN.md` notes the
  handoff between them explicitly so the flow doesn't feel like two
  disconnected tasks.
- ACM certificate provisioning for a custom domain name on the
  distribution is out of scope (see `DESIGN.md`, "Deferred to a Later
  Version") — Feature 24 assumes the certificate already exists.

---

## 2026-07-02 — Redesign navigation as a domain picker; add Key Management, S3, and CloudFront domains

**Context.** With Compute (EC2/AMI) at 12 features and real-AWS
verification (Phase 16) underway, the user raised that `awsops`'s actual
job spans more than EC2/AMI: this team's AWS footprint is really "deploy
and operate Invenio RDM" (Compute, plus the SSH key pairs instances
launch with) and "publish static websites" (S3 + CloudFront) — and the
existing single flat main menu doesn't make that structure visible, nor
does the current feature set actually cover the S3/CloudFront side of
the job at all (S3 today is only ever a write-destination inside Backup
Archive & Trim, never a managed resource in its own right). Growing a
single menu to cover all of this was rejected outright as unusable long
before reaching a final feature count.

**Decision.**
- Replace the single flat main menu with a domain picker: **Compute
  (EC2 & AMI)**, **Key Management**, **S3 (Buckets & Static Websites)**,
  **CloudFront**, **Exit**. Each domain has its own resource listing and
  its own numbered menu underneath (same shape Compute's menu already
  has today), reached via the picker and returned to via a "Back to
  domain picker" entry in every domain menu.
- **EC2 and AMI stay one domain ("Compute"), not two.** They're already
  deeply interleaved — "Create Instance *from* AMI," "Create AMI *from*
  Instance," and both Manage Tags and Show/Export Cloud-Init operate on
  "an instance or an AMI" as a single pick — splitting them would force
  those cross-cutting workflows to pick an arbitrary home or be
  duplicated across two menus.
- **Key Management becomes a first-class domain**, not just a label:
  key pairs get their own List/Create/Import/Delete primitives
  (`DESIGN.md` Features 13-16), not only the inline "type `new`" launch
  shortcut that already existed (2026-07-01 decision above) — that
  shortcut now calls the same standalone Create Key Pair primitive.
- **S3 gets full static-website scope**, not just a backup destination:
  bucket listing/creation, static website hosting configuration, local
  directory sync, and object browsing (`DESIGN.md` Features 17-21) — see
  the paired 2026-07-02 "CloudFront + OAC by default" decision above for
  the specific access-pattern default.
- **CloudFront gets core lifecycle scope**: list, show detail, create (S3
  origin + OAC), and invalidate (`DESIGN.md` Features 22-25) — not just
  read-only listing, since creating and refreshing a distribution are
  routine parts of standing up and updating a static site, not rare
  one-time console tasks.
- This redesign runs **alongside**, not blocking, Phase 16's real-AWS
  verification of Compute — the domain picker is a navigation refactor
  around Compute's existing, already-tested workflows, not a rewrite of
  them; see `PLAN.md` for how the new phases are sequenced relative to
  Phase 16/17.

**Rationale.**
- A two-level menu keeps each screen's numbered choices in the
  single-digit-to-low-teens range the interactive picker pattern
  (2026-06-30 decision, "Use numbered list selection...") was designed
  for, instead of scaling that pattern past where it stays usable.
- Merging EC2/AMI avoids fragmenting workflows that are, in AWS's own
  model, already cross-cutting between the two resource types.
- Scoping Key Management/S3/CloudFront generously now (rather than
  shipping thin listing-only versions and expanding later) matches this
  project's stated goal — reduce manual, undocumented AWS console work
  for this team's actual two use cases — instead of just adding a
  smaller surface that still leaves most of that work in the console.

**Rejected alternatives.**
- *Five separate top-level domains (EC2, AMI, Key Management, S3,
  CloudFront)* — matches the user's original phrasing most literally,
  but splits Compute's interleaved workflows for no real navigational
  benefit, since EC2 and AMI together are still a small enough menu on
  their own.
- *Key Management as a label only, no new primitives* — rejected because
  it leaves key pairs exactly as under-managed as today (create-only,
  buried inside instance launch), which doesn't actually close the gap
  that motivated calling it out as its own domain.
- *S3 scoped to backup-only, static website deferred* — rejected because
  static website hosting is one of the two concrete, named use cases
  driving this whole redesign, not a hypothetical future one.
- *CloudFront read-only for v1* — rejected because creating a
  distribution and invalidating its cache after a content update are
  both routine, not rare, once a site is live; deferring creation would
  leave the tool unable to actually finish standing up a site it just
  helped populate via S3 sync.
- *Pause Phase 16 to do this redesign first* — rejected; the two are
  independent (navigation refactor vs. verifying already-implemented
  Compute workflows against real AWS), so serializing them would waste
  time for no coordination benefit.

**Consequences.**
- `internal/ui` gains a `domainmenu.go` shared loop that Compute's
  existing menu code is refactored to use, rather than owning its own
  bespoke top-level loop (see `DESIGN.md` Architecture).
- Three new `internal/inventory` listers (`keypairs.go`, `buckets.go`,
  `distributions.go`) and a new `internal/awsclient/cloudfront.go` client
  are needed; `internal/awsclient/s3.go` is broadened well beyond
  Feature 11's original HeadObject-only scope.
- New IAM permissions are required beyond what's listed in `DESIGN.md`
  Assumptions as of 2026-07-01 — see that section's 2026-07-02 additions
  for the full list per domain.
- `Environment=production`'s extra safety-gate warning (today gating
  Compute's Terminate/Remove AMI) is *not* extended to the new domains'
  destructive operations in this round — an open item, not an oversight
  (see `DESIGN.md` Feature 26 and "Deferred to a Later Version").
- `PLAN.md` needs new phases for Key Management, S3, and CloudFront,
  sequenced after Phase 15 but not blocking Phase 16/17's completion of
  Compute's real-AWS verification and Bash retirement.

---

## 2026-07-01 — Support creating a new key pair from within awsops

**Context.** The user asked "what is Key pair name" while testing the
Key pair name prompt, and once it was explained (the name of an
already-registered AWS key pair used to install its public half on the
new instance, not a local file path), asked to be able to create a new
one from inside awsops instead of leaving the tool: "I don't like
re-using keys... this seems like an option that is needed." AWS's
`ec2:CreateKeyPair` generates a new key pair and returns the private key
material exactly once — AWS never stores or re-displays it — so
awsops has to capture and save it immediately or it's gone.

**Decision.**
- At the existing Key pair name prompt, typing `new` (case-insensitive)
  instead of a name switches into a small sub-flow: prompt for a new
  key pair's name, call `ec2:CreateKeyPair` (`KeyType=ed25519`,
  `KeyFormat=pem`), save the returned private key to
  `~/.ssh/<name>.pem` with `0600` permissions (creating `~/.ssh` first
  if it doesn't exist), print the saved path, and use that name as the
  launch's key pair.
- A name collision (`InvalidKeyPair.Duplicate`) re-prompts for a
  different name rather than failing the whole launch; any other
  `CreateKeyPair` error (e.g. missing IAM permission) is returned as a
  real error, same as any other unrecoverable AWS failure elsewhere in
  the tool.
- Default key type is ED25519 (AWS's own current recommendation for
  new key pairs — smaller, faster than RSA) rather than RSA.
- `internal/awsclient`'s `-debug` logging decorator for `CreateKeyPair`
  does not use the shared generic `logAWSCall` helper — its response
  carries the private key material, which must never be written to the
  debug log even though everything else the tool does is logged in
  full. The wrapper logs everything except `KeyMaterial`, which it
  replaces with a fixed redaction marker.

**Rationale.** Inline ("type `new`") beats a separate main-menu action
because the natural point to decide "I want a fresh key for this one"
is exactly when you're being asked for a key pair name — a separate
menu item would mean leaving the launch flow, remembering the name you
picked, then re-entering the flow to use it. Saving straight to
`~/.ssh/` with correct permissions means the key is immediately usable
(`ssh -i ~/.ssh/<name>.pem ...`) without a manual `chmod` step.

**Trade-off.** awsops now writes files outside its own working
directory (`~/.ssh`) for the first time. Accepted: this is the
standard, expected location for SSH private keys, and the alternative
(printing the key material to the terminal for the operator to save
themselves) risks losing an unrecoverable secret in scrollback.

---

## 2026-07-01 — Add -debug: a JSONL log of every AWS SDK call

**Context.** The user asked for a `-debug` option to make awsops'
behavior easier to diagnose, pointing at a similar feature already
built for `~/Laboratory/harvey` (a JSONL debug log of specific
hand-instrumented events — LLM requests/responses, tool calls, etc.,
via a `*DebugLog` type whose methods are all nil-receiver-safe). Unlike
Harvey, where "interesting events" are a hand-picked subset of a large
surface area, awsops' entire interesting surface *is* its AWS SDK
calls — every EC2/SSM/S3/STS method it calls is already declared in
one of four narrow interfaces (`internal/awsclient`'s `EC2API`/
`SSMAPI`/`S3API`/`STSAPI`), the same interfaces the test fakes already
implement.

**Decision.**
- `internal/debuglog`: a new package with the same nil-safe `*DebugLog`
  shape as Harvey's — `Log(event string, fields map[string]any)`,
  `Path()`, `Close()`, all safe on a nil receiver — so `-debug=false`
  needs no `if debug` conditionals anywhere else in the codebase.
- Rather than hand-instrumenting ~15 call sites (Harvey's approach),
  wrap each of the four client interfaces with a logging decorator
  (`internal/awsclient`'s `WrapEC2`/`WrapSSM`/`WrapS3`/`WrapSTS`) that
  implements every method mechanically via one shared generic helper
  (`logAWSCall`): log method name, region, request params, duration,
  and response-or-error, then delegate. This gets full coverage of
  every AWS action awsops takes, for about the same code as
  hand-picking a subset would have cost, because the interfaces were
  already narrow and enumerable.
- `Wrap*` returns the original client unchanged when `dl` is nil, so
  there's no wrapper-object overhead when `-debug` isn't set.
- Sink: a timestamped file in the current directory
  (`awsops-debug-<timestamp>.jsonl`, `debuglog.DefaultPath()`), printed
  to stderr once at startup — not `agents/logs/` like Harvey, since
  awsops has no `agents/` directory convention of its own.

**Rationale.** Decorating the interfaces instead of instrumenting call
sites means new AWS calls added to `EC2API`/`SSMAPI`/`S3API`/`STSAPI`
in the future are logged automatically (the interface's method set is
enumerable and the compiler enforces the decorator implements all of
it) — no risk of a future workflow silently bypassing the debug log
because nobody remembered to add a manual `dl.Log(...)` call at the new
site.

**Trade-off.** Full-fidelity request/response logging (not just event
names) means `DescribeInstances`/`DescribeImages` calls that return
large collections write correspondingly large JSON records. Accepted:
the log is opt-in, local, and meant for active debugging sessions, not
continuous production telemetry — verbosity favors "can actually see
what happened" over log-file size.

---

## 2026-07-01 — Key pair stays free text; Name tag moves earlier

**Context.** A prior fix (this same day) added pick lists for key pair
name, security group IDs, and subnet ID, fetched from the AMI's region,
after real-AWS testing surfaced a typo-prone free-text UX (typing a
security group *name* instead of an ID caused a real AWS
`InvalidParameterCombination` error). Further real-AWS testing then
showed the key pair pick list didn't help: unlike opaque `sg-xxxx`/
`subnet-xxxx` IDs, key pair names are already human-readable, and a flat,
unsorted list of every key pair across the account (16+ entries in one
real test run, most irrelevant to the AMI being launched) added noise
without solving a real problem. Separately, the user noted the Name tag
prompt landed too late in the flow — after all of instance type/key
pair/security groups/subnet/IAM profile/user data — when naming the
instance is a natural first step once the AMI is picked.

**Decision.**
- Revert the key pair prompt to free text (`ui.Prompt` with
  `requireNonEmpty`), removing `listKeyPairNames` and the pick-list
  wrapper. Security group IDs and subnet ID keep their pick lists —
  those IDs are the genuinely opaque case the original gap was about.
- Move the Name tag prompt to immediately after the AMI pick (and, for
  the cloud-init-first workflow, immediately after its own AMI pick),
  before Instance type and everything after it.

**Rationale.** Pick lists help when the identifier is opaque and the
account/region has more of them than a person can remember (security
groups, subnets). They don't help when the identifier is already a
name the user chose and knows — showing every key pair in the account
regardless of relevance is worse than just typing the name. Naming the
instance right after picking its AMI matches how a person actually
thinks through the workflow (what is this, then how is it configured).

**Trade-off.** A key pair pick list would still catch a typo'd key pair
name before `RunInstances` runs; free text defers that to AWS's own
error. Accepted, since `ec2:RunInstances` rejects an unknown key pair
name immediately and unambiguously, unlike the security-group-name-vs-ID
confusion this was originally meant to prevent.

---

## 2026-07-01 — Use github.com/rsdoiel/termlib for the Terminal UI

**Context.** PLAN.md's Phase 3 (Terminal UI) originally called for a
stdlib-only implementation (`text/tabwriter` for tables, `bufio.Scanner`
for the numbered pick list and free-text prompts) — no interactive-input
library existed under the pre-approved `github.com/rsdoiel` or
`github.com/caltechlibrary` namespaces (CLAUDE.md) at the time DESIGN.md
was drafted. The user has since pointed at `github.com/rsdoiel/termlib`
("a light weight terminal interface library... ncurses light"), which
already exists and is under the `github.com/rsdoiel` namespace, so no
separate approval discussion is needed per CLAUDE.md's pre-approved
dependency list.

**Decision.** Build `internal/ui` on `termlib` instead of a hand-rolled
stdlib-only implementation:
- `DisplayInstances`/`DisplayImages` use `termlib.Terminal` (buffered
  output, flushed via `Refresh`) plus `termlib.PadRight`/`termlib.Truncate`
  for column formatting, replacing `ec2_ami_manager.bash`'s
  `printf "%-20s ..."` pattern
- `PickList[T]` and `Prompt` are built on `termlib.LineEditor.Prompt`
  (readline-style editing, history, `Ctrl+C`/`Ctrl+D` handling) rather
  than a plain `bufio.Scanner` loop
- Tests drive `LineEditor` through an `os.Pipe()` (not a TTY), which
  `LineEditor.Prompt` detects and falls back to plain line reading for —
  documented and intended by `termlib` itself for exactly this use case,
  so no fake/mock input abstraction was needed

**Rationale.**
- `termlib` is pre-approved (CLAUDE.md: `github.com/rsdoiel` namespace)
  and already provides exactly what Phase 3 needs: buffered flicker-free
  terminal output, readline-style prompts with history and `Ctrl+C`/
  `Ctrl+D` handling, and column-formatting helpers (`PadRight`,
  `Truncate`) that map directly onto the Bash version's `printf`/
  `${var:0:N}` conventions
- Still numbered-menu based, not raw-keystroke arrow navigation —
  consistent with the existing "Numbered menu for pick lists" decision
  (2026-06-30); `termlib`'s raw-mode keystroke reading (`ReadKey`/
  `KeyReader`) is not used here
- Avoids reimplementing readline-style editing/history/multi-line-paste
  handling that a hand-rolled `bufio.Scanner` loop would not have had

**Rejected alternatives.**
- *Stdlib-only (`text/tabwriter` + `bufio.Scanner`)* — the original
  PLAN.md approach; works, but reinvents editing/history niceties
  `termlib` already provides, for no benefit now that an approved library
  covers it
- *A third-party TUI/prompt library outside the `github.com/rsdoiel` /
  `github.com/caltechlibrary` namespaces* — would need discussion per
  CLAUDE.md; moot once `termlib` was pointed out

**Consequences.**
- `go.mod` gains `github.com/rsdoiel/termlib` (direct) and its transitive
  `golang.org/x/term`/`golang.org/x/sys` dependencies
- `DESIGN.md`'s Dependencies section and its "no dependency beyond the Go
  standard library and the AWS SDK" claim are updated to include
  `termlib`
- If a gap surfaces in `termlib` while building later phases (e.g. a
  table-drawing helper `internal/ui` ends up hand-rolling), flag it back
  to the user for `~/Laboratory/termlib/TODO.md` rather than working
  around it silently in `awstools`

---

## 2026-07-01 — Add Create EC2 Instance from Cloud-Init YAML as a v1 primitive

**Context.** Feature 2 (Create Instance from AMI) already got a cloud-init
file-input and completion-check enhancement (see "Enhance Create Instance
from AMI" below), but the user pointed out that burying it as the 6th of
7 parameters inside an AMI-first workflow doesn't serve the actual use
case: someone who starts from "I have a cloud-init recipe, give me a
machine" has a different mental model than someone who starts from "give
me another copy of this AMI." That deserves to be its own visible
primitive, not an option nested inside Feature 2's parameter list.

**Decision.** Add "Create EC2 Instance from Cloud-Init YAML" as its own
v1 primitive (Feature 8): the cloud-init file is the *first* thing
collected, then a base AMI is picked, then the same remaining launch
parameters Feature 2 already collects. It shares Feature 2's underlying
execution path entirely (the same `LaunchInstanceParams` struct, the same
launch/poll/cloud-init-completion-check logic) — the only difference is
the order and framing of the interactive prompts. This is distinct from
the deferred "Bake AMI from cloud-init" idea: that one snapshots the
result into a new AMI and terminates the instance; this one leaves a real,
running, usable instance.

**Rationale.**
- Matches the user's explicit ask: this needed to be visible in the
  primitive list, not folded into another feature's parameter collection
- Sharing execution logic with Feature 2 avoids duplicating the
  launch/poll/cloud-init-check code — only the front-end prompt sequence
  differs, consistent with the params-struct/confirm-gate seam already
  required of every workflow (see "Structure workflows for future
  record/replay")
- Placed as Feature 8 (after Backup Archive & Trim, before Stop/Terminate
  Instance and the cross-cutting Project/Environment Tagging convention —
  see the companion decision below) rather than immediately after
  Feature 2, to avoid a costly renumbering cascade through Features 3-7
  and their many existing cross-references, for a placement decision that
  doesn't carry strong semantic weight either way

**Rejected alternatives.**
- *A sub-mode within Feature 2 ("how do you want to start: AMI or
  cloud-init?")* — was the initial implementation; rejected because the
  user wants it directly visible as its own primitive, not a branch
  hidden inside another feature's flow
- *Insert immediately after Feature 2, renumbering Features 3-7* —
  arguably better thematic grouping, but not worth the renumbering risk
  across this document's many existing Feature N cross-references for a
  placement question without a strong correctness argument either way

**Consequences.**
- `DESIGN.md` gets a new Feature 8
- `PLAN.md` gets a new Phase 10 ("Create Instance from Cloud-Init YAML"),
  inserted before Main Menu and Integration
- No new IAM permissions — reuses exactly what Feature 2/Phase 4 already
  needs (`ec2:RunInstances`, SSM for the completion check)

---

## 2026-07-01 — Add Start/Stop/Terminate EC2 Instance as v1 primitives

**Context.** Immediately after adding "Create Instance from Cloud-Init
YAML," the user pointed out more gaps: there's no way to stop a running
instance or to terminate/remove one, and then — while this very decision
was being written up — no way to start a stopped one back up either. The
v1 list only covered AMI lifecycle and instance *creation*, not the rest
of an instance's power-state lifecycle.

**Decision.** Add three more v1 primitives:
- **Start EC2 Instance** (Feature 9): pick a stopped instance, simple
  yes/no confirm (safe and reversible, the symmetric counterpart to Stop),
  `ec2:StartInstances`, poll until `running`, display connection info
  (public IP may have changed unless an Elastic IP is in use)
- **Stop EC2 Instance** (Feature 10): pick a running instance, simple
  yes/no confirm (stopping is reversible — data on EBS volumes persists,
  the instance can be started again), `ec2:StopInstances`, poll until
  `stopped` (bounded timeout)
- **Terminate EC2 Instance** (Feature 11): pick an instance, dry-run
  showing what would be destroyed — **including whether any attached EBS
  volume has `DeleteOnTermination=true`**, since that volume's data
  (including any not-yet-archived backups — see Backup Archive & Trim) is
  destroyed along with the instance — an `Environment=production` warning
  if tagged, type-to-confirm, then `ec2:TerminateInstances`. Same safety
  tier as Remove AMI (Feature 4)

**Rationale.**
- Stopping/starting and terminating are fundamentally different risk
  levels (reversible vs. permanent), so they get different confirmation
  tiers — matching this project's existing principle of scaling friction
  to actual risk rather than applying one blanket confirmation style
  everywhere
- Surfacing `DeleteOnTermination` in the dry-run closes a real gap this
  project already cares about: an instance can be terminated with its
  root volume set to delete-on-termination, destroying exactly the kind
  of not-yet-archived backup data Backup Archive & Trim exists to protect
- `ec2:TerminateInstances` was already a planned permission (for Show/
  Export Cloud-Init's AMI-path cleanup); only `ec2:StartInstances`/
  `ec2:StopInstances` are new

**Rejected alternatives.**
- *One combined "manage instance power state" primitive covering start/
  stop/terminate* — considered, but start/stop and terminate have
  different confirmation tiers and different risk profiles; combining
  them risks the lighter-weight start/stop confirmation habituating users
  to clicking through what should be a heavier gate for terminate

**Consequences.**
- `DESIGN.md` gets three new Features (9, 10, 11); Project/Environment
  Tagging moves from Feature 8 to Feature 12 (four new features inserted
  ahead of it: Feature 8 Create-from-Cloud-Init-YAML, Feature 9 Start,
  Feature 10 Stop, Feature 11 Terminate)
- `PLAN.md` gets three new Phases after the Create-from-Cloud-Init-YAML
  phase, before Main Menu and Integration; later phases renumbered
  accordingly
- `DESIGN.md`'s IAM permission list gains `ec2:StartInstances` and
  `ec2:StopInstances`

---

## 2026-07-01 — Name the CLI binary `awsops`

**Context.** The Go CLI had been referred to as `ec2-ami-manager`
throughout the Architecture sections, a name inherited from the original
Bash script's narrow scope. That name undersells what v1 now covers
(instance/AMI lifecycle, cloud-init inspection, backup hygiene, tag
management) and, worse, ties the tool's identity to a single operation.
The candidate `rdmctl` was also considered, but the user wants the name
to make two things clear: it's about AWS resource operational hygiene in
general, not explicitly tied to the Invenio RDM project specifically —
even though RDM instances are its primary use case today.

**Decision.** Name the CLI binary `awsops` (`cmd/awsops/`). The repository
itself stays named `awstools` (already fixed by the CMTools scaffold —
`codemeta.json`, `Makefile`'s `PROJECT = awstools`); the user may revisit
the repository name separately later, but that's not part of this
decision.

**Rationale.**
- Communicates general AWS operational hygiene rather than a single
  narrow operation (`ec2-ami-manager`) or a project-specific name
  (`rdmctl`) that would tie the tool's identity to Invenio RDM even though
  its mechanisms (tagging, backup archival, cloud-init inspection) aren't
  actually RDM-specific
- Leaves room for the tool to be useful for non-RDM AWS resources later
  without a confusing name

**Rejected alternatives.**
- *`rdmctl`* — clearer about the current primary use case, but locks the
  name to a project the tool isn't actually coupled to at the
  implementation level
- *`awstools` (reuse the repo name for the binary too)* — simplest, but
  the user prefers a distinct binary name in case the repo hosts more
  than one tool later

**Consequences.**
- `DESIGN.md`/`PLAN.md` Architecture sections and doc titles updated from
  `ec2-ami-manager` to `awsops` throughout
- `ec2_ami_manager.bash` (the Bash file itself) is unaffected — it's a
  filename, not the Go binary name, and stays as-is until retirement per
  the existing retire-after-verify plan

---

## 2026-07-01 — Enhance Create Instance from AMI: cloud-init file input + completion check

**Context.** Feature 2 (Create EC2 Instance from AMI) already had a
generic, optional "user data" text prompt — a cloud-init YAML could
technically go there today via freehand typing/pasting. Two real gaps
remained: no way to load it from a file instead of typing/pasting
multi-line YAML into a terminal, and no verification that cloud-init
actually *finished successfully* after launch — the existing poll only
waits for the EC2-level `running` state, not for cloud-init's own
completion. An instance can be "running" while its user-data provisioning
silently failed partway through, which directly undermines the "test new
versions/changes with confidence" goal this whole project is for.

**Decision.** Enhance Feature 2 with:
1. The user-data prompt accepts a local file path as an alternative to
   inline text (e.g. pointing at a file from a local clone of
   `cloud-init-examples`) — a plain local file read, no new AWS API
   surface
2. After the instance reaches `running`, if user-data was provided, wait
   for SSM to report `Online` and run `cloud-init status --wait` via SSM
   (bounded timeout — unlike Phase 5's unbounded AMI-creation poll, a
   cloud-init run on launch should finish in a bounded, predictable time,
   so an unbounded wait would just mask a real hang), reporting the
   actual completion status (`done` vs `error`) rather than only EC2's
   `running` state. If SSM never comes online, skip this check cleanly
   (not an error) — not every AMI has SSM configured

**Rationale.**
- File-path loading avoids re-typing/pasting multi-line YAML in a
  terminal prompt, at essentially zero implementation cost
- Verifying cloud-init's actual completion status closes a real
  "looks fine but isn't" gap — exactly the kind of silent failure that
  erodes confidence in a test environment
- Reuses Phase 6's SSM client/poll/bounded-timeout pattern rather than
  inventing a new mechanism

**Rejected alternatives.**
- *Fetch templates directly from `cloud-init-examples` via the GitHub
  API* — deferred for the same reason "Inline diff against
  cloud-init-examples" is deferred (see the Show/Export Cloud-Init
  decision): no clean mapping from this account's `Project` tags to the
  repo's filenames yet. Pointing at a local file (from your own clone)
  gets the practical benefit without that dependency
- *Unbounded wait for cloud-init completion* — rejected; unlike AMI
  creation, which can legitimately run for hours on large volumes, a
  launch-time cloud-init run should complete in a bounded, predictable
  window

**Consequences.**
- `DESIGN.md` Feature 2 is updated with both changes (still Feature 2, no
  renumbering)
- `PLAN.md` Phase 4 gains an explicit dependency on Phase 1 (for the SSM
  client, alongside the EC2 client it already needed) and new work items
  for file-path loading and the completion check

---

## 2026-07-01 — Broaden Rename Instance into a general Manage Tags primitive

**Context.** Immediately after adding "Rename Instance" (below), the user
pointed out the obvious generalization: renaming is just "update the
`Name` tag," and if the tool can create tags, it should be able to
manage tags generally — add, update, and remove, on any resource, not
just set `Name` on an instance.

**Decision.** Replace Feature 5 ("Rename Instance") with a general
"Manage Tags" primitive: pick a resource (instance or AMI), see its
current tags, then add a new tag, update an existing one, or remove one.
Renaming is simply the common case of updating `Name` through this same
flow — no separate operation. Confirmation stays at the same lightweight
tier Rename Instance had (simple yes/no — tag edits are cheap and
reversible, not the dry-run/type-to-confirm tier reserved for actually
destructive operations), routed through the same reusable confirmation
gate as every other workflow.

**Rationale.**
- Avoids two overlapping menu items backed by the same underlying API
  (`ec2:CreateTags`) doing the same thing at different scopes
- AMIs get tags too (Project/Environment are set at creation per Feature
  3) but had no way to edit them after the fact — this closes that gap
  symmetrically for both resource types
- One general primitive is simpler to reason about, test, and eventually
  expose to Recorded Scripts than two narrow ones

**Rejected alternatives.**
- *Keep both Rename Instance and a separate Manage Tags primitive* — lets
  renaming stay a one-step action, but two menu items for the same
  underlying operation is redundant and was explicitly rejected in favor
  of consolidation
- *Tag management as an instance-only feature* — considered, but AMIs
  need the same capability (they carry Project/Environment tags too), so
  scoping it to instances only would just recreate the same gap for AMIs

**Consequences.**
- `DESIGN.md` Feature 5 is retitled "Manage Tags" and rewritten; no
  renumbering needed elsewhere since it's a same-slot replacement
- `DESIGN.md`'s IAM permission list gains `ec2:DeleteTags` (removal) —
  `CreateTags`/`DescribeTags` were already planned
- `PLAN.md` Phase 9 is retitled "Manage Tags" and its work items rewritten
  to cover add/update/remove on both instances and AMIs; still Phase 9,
  no renumbering elsewhere

---

## 2026-07-01 — Add Rename Instance as a v1 primitive; AMI Name is immutable

**Context.** The user noticed renaming was missing from the v1 primitive
list and asked whether the AWS SDK supports it. The two resources behave
completely differently:
- An EC2 instance's "Name" is not a real API attribute at all — it's just
  the `Name` tag by convention (the same tag this project's Project/
  Environment tagging convention already reads/writes). Changing it is a
  plain `ec2:CreateTags` call, already a planned permission
- An AMI's `Name` is a real attribute, but it is set once at `CreateImage`
  time and **cannot be changed afterward via the AWS API** — this is an
  AWS EC2 limitation, not a gap in the SDK or this tool. `ModifyImageAttribute`
  allows changing `Description` and launch permissions, but not `Name`.
  The only way to get an AMI with a different name is `CopyImage`
  (produces a brand-new AMI with a new ID and duplicated snapshots) plus
  deregistering the original — a materially heavier operation than a
  rename, closer in cost/risk to Feature 3 (create-AMI) than a quick edit

**Decision.** Add "Rename Instance" as a v1 primitive (pick an instance,
prompt for a new `Name` tag value, confirm, `ec2.CreateTags`). Do not add
an "AMI rename" primitive of any kind. Feature 3 (Create AMI from EC2
Instance) keeps its existing default-name-suggestion behavior unchanged,
but gains an explicit note that the name is permanent once created, so
the user isn't surprised later. "Edit AMI Description" (the one AMI
attribute that *is* mutable) is recorded as a deferred, not-yet-requested
idea rather than built now.

**Rationale.**
- Renaming an instance is cheap, reversible, and was a real gap — no
  reason to defer it
- Silently supporting "rename" for AMIs by only updating a tag while the
  real `Name` attribute stays stale would be actively misleading, since
  AWS's own console/CLI would keep showing the original name
- Building a "rename via copy + deregister" primitive for AMIs was
  considered and rejected: it's a different operation with different
  risk (storage duplication, a new AMI ID, anything referencing the old
  ID breaks) than what a user asking to "rename" would expect

**Rejected alternatives.**
- *AMI rename via CopyImage + DeregisterImage* — technically the only way
  to get a differently-named AMI, but it's a heavyweight operation
  disguised as a rename; not built for v1
- *Only update the AMI's tags, leave Name attribute alone* — would create
  a confusing mismatch between this tool's tag-based "name" and the AMI's
  actual `Name` shown everywhere else in AWS

**Consequences.**
- `DESIGN.md` gets a new Feature 5 ("Rename Instance"), renumbering
  Show/Export Cloud-Init, Backup Archive & Trim, and Project/Environment
  Tagging by one
- `PLAN.md` gets a new Phase 9 ("Rename Instance"); Phases 9-12 in the
  prior draft are renumbered to 10-13 accordingly
- No new IAM permission needed — `ec2:CreateTags` was already planned for
  the tagging convention

---

## 2026-07-01 — Structure workflows for future record/replay ("Recorded Scripts")

**Context.** Discussing "bake an AMI from cloud-init" led to a bigger idea:
capture the sequence of actions taken in an interactive session as an
editable, replayable script — analogous to how a "skill" packages a
procedure for a language model, but for this deterministic tool. The user
wants this to use YAML (prior success with YAML-driven configuration in
other projects, e.g. `dataset`'s web service config) and templated values
(not just literal captured values), with safety gates enforced on replay,
not bypassed. This is a substantial feature — a new execution mode
(record / replay) that cuts across the whole menu/dispatch loop, not a
single primitive — so it is not built in v1 (see "Recorded Scripts" under
"Deferred to a Later Version" in `DESIGN.md`/`PLAN.md`). It also
potentially subsumes the earlier-deferred "Clone instance for testing",
"Upgrade with rollback point", and "Bake AMI from cloud-init" composite
workflows: if a user can record a sequence once and replay it with
different values, those don't need to be bespoke Go features.

**Decision.** v1 does not build the recorder, the YAML schema, or the
replay engine. It does structure every confirmation-gated workflow
(Phases 4, 5, 6's AMI path, 7, 8) around a specific seam so that adding
record/replay later does not require reopening already-finished code:
1. Each workflow separates **building a resolved parameters struct**
   (interactive prompts fill it in v1) from **executing it against AWS**.
   The execution code takes a plain, typed struct (e.g.
   `CreateAMIParams{InstanceID, Name, Description, NoReboot, Tags}`) and
   never knows whether prompts or a future YAML file produced it
2. The **confirmation/dry-run gate is its own reusable step**, not inlined
   into each workflow's prompt loop, so a future replay engine can route
   through the identical gate rather than a second, parallel
   implementation of "is this safe to do"
3. When templating is eventually built, it applies via Go's standard
   library `text/template` to the YAML text before parsing — no new
   dependency, consistent with this project's stdlib-first preference

**Rationale.**
- The params-struct/execute split and the reusable confirmation gate are
  good structure on their own merits (testability, single source of truth
  for "is this safe"), so requiring them now costs nothing extra — it's a
  constraint on code already being written, not additional code
- Building the actual recorder/replay engine before v1's primitives have
  proven themselves against real AWS would be scope creep on an already
  large rewrite (see "V1 scope" below)
- Safety gates must not be bypassable by replay without deliberate,
  explicit opt-in — reusing the exact same gate function is the simplest
  way to guarantee that, rather than trusting a second implementation to
  stay in sync

**Rejected alternatives.**
- *Build record/replay now, as part of v1* — most directly useful, but
  conflicts with "V1 scope: ship the four primitives first" and adds a new
  execution mode on top of an already-large Bash→Go rewrite
- *Defer without any structural constraint on v1's workflows* — cheaper
  short-term, but risks having to rewrite Phase 4-8's internals later to
  retrofit the params-struct/confirm-gate seam once record/replay is
  actually built
- *Literal-only captured values (no templating)* — simpler, but the user
  specifically wants templating for repurposing a saved sequence across
  different targets (different instance, different environment), which a
  literal-only capture can't do without hand-editing every value each time

**Consequences.**
- `PLAN.md` Phases 4, 5, 6, 7, 8 each get a work item noting this
  structural requirement
- The "Deferred to a Later Version" entries for "Clone instance for
  testing", "Upgrade with rollback point", and "Bake AMI from cloud-init"
  are annotated as likely to become example Recorded Scripts rather than
  bespoke Go workflows, once the mechanism exists
- No new third-party dependency is introduced by this decision — YAML
  parsing (`gopkg.in/yaml.v3` or similar) would be needed when the feature
  is actually built, and would need the same approval process as
  `aws-sdk-go-v2` did, at that time

---

## 2026-07-01 — Add Backup Archive & Trim as a v1 primitive

**Context.** Today, cleaning up stale Postgres backups on an RDM instance
is a fully manual chore: log in, look at `/opt/rdm_sql_backups`, decide
what's old enough to remove, delete it by hand. A read-only SSM check of
`newauthors` found the real scale of the problem: 87GB in
`/opt/rdm_sql_backups`, one `<project>-db-<n>-<project>-<date>.sql.gz`
file per day at ~980MB, with no rotation at all (root volume: 484GB total,
157GB used, 328GB free — the backups are over half of what's used). This
is the concrete cause of the "over-provisioned disk makes cloning slow"
problem that motivated this whole design conversation. No S3 destination
exists yet for these backups — that's a separate infrastructure task the
user still needs to do, not something this project creates.

This conversation also surfaced a broader framing for the tool: it should
help with ongoing *administration* of these instances (not just
create/remove EC2/AMI lifecycle events), and with improving development,
test, and deployment workflows more generally.

**Decision.** Add "Backup Archive & Trim" as a v1 primitive: given an
instance, a backup directory, and an age threshold (both explicit prompts,
no baked-in default — same reasoning as the `Environment` tag having no
default), it uploads files older than the threshold to S3, independently
verifies each upload, deletes only the verified files, then runs `fstrim`.
The sequence is deliberately split into two separate remote steps with the
tool itself as the arbiter in between, rather than one script that
uploads-then-immediately-deletes based on its own success report:

1. **Dry-run list** (SSM, read-only): candidate files matching the age
   threshold, with size/age, shown before anything happens
2. **Type-to-confirm** (matches the AMI-removal safety tier, since step 5
   is irreversible)
3. **Upload phase** (SSM): the instance uploads each candidate file to S3
   (`aws s3 cp`, run from the instance — see rejected alternatives) and
   reports back a small per-file JSON summary (S3 key, size). Nothing is
   deleted yet
4. **Independent verification**: the tool itself, using its own AWS
   credentials (not the instance's self-report), calls `s3:HeadObject` on
   every uploaded key and confirms it exists with the expected size
5. **Delete phase** (a *second*, separate SSM command): the instance
   deletes exactly the tool-verified file list — it does not re-derive its
   own "what's stale" list, avoiding a time-of-check/time-of-use gap
6. **fstrim**, then a report of bytes freed and any files that failed
   verification (left untouched, flagged, not deleted)

**Rationale.**
- Matches this project's existing safety pattern for destructive
  operations (dry-run, then explicit confirm — see "Multi-layer
  confirmation for AMI removal")
- SSM Run Command is not a bulk file-transfer channel (confirmed when
  designing Show/Export Cloud-Init) — ~980MB backup files are far too
  large to round-trip through SSM output, so the upload must happen
  *from* the instance itself via its own AWS CLI/credentials
- Splitting upload and delete into two round-trips with independent
  verification in between means the tool — not the remote script — decides
  what's safe to delete, based on a second, independent read from S3.
  Trusting a single script's self-report to authorize an irreversible
  delete was considered and explicitly rejected (see below)

**Rejected alternatives.**
- *Single script: upload then immediately delete on self-reported success*
  — simpler, one round-trip, but means a script bug or a transient S3
  error that still reports "success" could delete backups with nothing
  actually saved. Rejected in favor of independent verification
- *Tool downloads/re-uploads the files itself instead of the instance
  doing it* — would let the tool verify with certainty, but requires
  streaming multi-GB files through the operator's machine or through SSM
  (impractical for the same reason raw file transfer was rejected for
  Show/Export Cloud-Init's AMI path)
- *Fold into the existing fstrim step* — considered, but the user chose a
  standalone primitive (see "Should v1 add composite workflows" discussion
  in the Show/Export Cloud-Init decision above); backup cleanup is useful
  on its own, independent of AMI creation

**Consequences.**
- Each target instance's IAM instance profile needs `s3:PutObject` (and
  likely `s3:ListBucket` scoped to its own prefix) on the destination
  bucket — this is a cloud-init/AMI change in `caltechlibrary/cloud-init-
  examples`, not something this Go tool can retrofit from outside. This is
  in scope for this project to specify (per the earlier "prereq scope"
  decision) even though the actual bucket creation and IAM/Terraform work
  happens separately
- The tool's own IAM permissions (the operator's identity, distinct from
  the instance's own instance profile) need `s3:HeadObject` (or
  `s3:GetObject`) on the bucket, for independent verification
- The S3 bucket itself does not exist yet — real-AWS verification of this
  primitive is blocked on that being created first (tracked in `TODO.md`,
  not by this project)
- Testing plan (per discussion with the user): unit tests first, with
  fakes for `EC2API`/`SSMAPI`/a new `S3API` (covering `HeadObject`); then,
  once those pass, a live test against a *throwaway instance launched from
  an existing AMI that already has these backups baked in* — never
  directly against the production instance. This reuses Phase 4
  (Create EC2 Instance from AMI) as a testing tool in its own right, and
  the throwaway instance must be terminated after the test, same cleanup
  discipline as Show/Export Cloud-Init's AMI path
- `DESIGN.md`'s Overview is broadened to describe ongoing instance
  administration and dev/test/deployment workflow support, not just
  EC2/AMI lifecycle management
- `PLAN.md` gets a new Phase 7 ("Backup Archive & Trim"); Phases 7-11 in
  the prior draft are renumbered to 8-12 accordingly

---

## 2026-07-01 — Add Show/Export Cloud-Init as a v1 primitive

**Context.** This team maintains a separate repository,
`caltechlibrary/cloud-init-examples`, of hand-authored cloud-init YAML
templates (e.g. `invenio-rdm.yaml`) used both for local Multipass
development VMs and as the source of the `--user-data` passed when
launching real EC2 instances. A live check found that `ec2:
DescribeInstanceAttribute` (attribute `userData`) returns the exact
base64-encoded cloud-init that launched a given instance, and that
`newauthors`'s actual deployed cloud-init has already drifted from
`cloud-init-examples`' `invenio-rdm.yaml` template (missing packages, no
`write_files` onboarding scripts, a different `runcmd` approach). This is
a real, live instance of the "accurate test environments" risk this whole
project is meant to reduce.

**Decision.** Add "Show/export cloud-init" as a fifth v1 primitive
(alongside create-instance, create-AMI, remove-AMI, and the tagging
convention), not deferred:
- **Instance path**: `ec2:DescribeInstanceAttribute` — read-only, free,
  instant, works for any existing instance
- **AMI path**: also supported in v1. Since an AMI has no user-data
  attribute of its own, extraction launches a temporary, disposable
  instance from the AMI, waits for SSM to come online (reusing the same
  SSM pattern already planned for the fstrim step), runs an SSM command to
  read `/var/lib/cloud/instance/user-data.txt` off disk, and *always*
  terminates the temporary instance afterward — including on failure or
  timeout. This path costs real AWS time/money (a running instance for
  several minutes) and requires an explicit confirmation before
  proceeding, unlike every other read in this tool
- **Export**: decoded YAML can be saved to a local file path for manual
  diffing against a local clone of `cloud-init-examples`. No inline
  fetch-and-diff against the GitHub repo in v1 — see rejected alternatives

**Rationale.**
- Directly serves the stated project goal: this is the concrete mechanism
  for detecting drift between what's actually deployed and the team's
  canonical cloud-init templates
- The instance path is essentially free to build (one more typed SDK call,
  already-planned dependencies) — there's no reason to defer it
- The AMI path reuses the SSM client and "poll with bounded timeout,
  always clean up" pattern already needed elsewhere, rather than
  introducing a new mechanism (e.g. mounting the AMI's snapshot on a
  helper instance, which would need an existing helper instance in every
  region and is more moving parts for the same result)

**Rejected alternatives.**
- *Instances only, defer AMI extraction* — was the initial recommendation
  (cost/complexity), but the user explicitly wants AMI coverage in v1
  since some of the AMIs this team manages have already outlived the
  instance they were created from
- *Fetch + diff inline against `cloud-init-examples`* — would directly
  answer "has this drifted?" without leaving the CLI, but requires solving
  a non-trivial file-mapping problem first (the repo's files don't map
  1:1 to this account's `Project` tag values today, e.g. there's no
  `caltechauthors-init.yaml`) and adds a runtime network dependency on
  GitHub. Deferred — see `DESIGN.md`/`PLAN.md` "Deferred to a Later
  Version"
- *Extract via snapshot-mount on a helper instance instead of launch+SSM*
  — avoids booting the AMI's OS at all, but requires a pre-existing helper
  instance per region and more novel mechanics for the same outcome

**Consequences.**
- `DESIGN.md`'s IAM permission list gains `ec2:DescribeInstanceAttribute`,
  `ec2:TerminateInstances`, and `ssm:GetCommandInvocation` (SendCommand and
  DescribeInstanceInformation were already listed for the fstrim step)
- The AMI path needs its own explicit confirmation prompt (cost/time, not
  free like every other v1 read) and a cleanup guarantee — tests must
  verify the temporary instance is terminated even when the SSM
  command fails or times out
- `PLAN.md` gets a new Phase 6 ("Show/Export Cloud-Init"); Phases 6-10 in
  the prior draft are renumbered to 7-11 accordingly

---

## 2026-07-01 — Introduce a light Project/Environment tagging convention

**Context.** The stated goal for this tool is to speed up upgrading RDM
deployments and creating accurate test environments across production and
development instances. A read-only check of the live account
(`aws ec2 describe-instances`) found tagging is inconsistent today: a
`project` tag exists on some instances (`caltechauthors`, `caltechdata`,
`caltechthesis`) but not others (`new-plots`, `thesis`,
`authors-test-recovery`), and there is no dedicated environment tag —
production vs. test is encoded only in the instance *name string* (e.g.
`caltechdata-test` vs. `oldcaltechdata`). `newauthors` is additionally
managed via an EC2 Launch Template, not ad hoc parameters.

**Decision.** The tool suggests/requires `Project` and `Environment`
(`production` | `development` | `test`) tags when creating new instances
and AMIs, uses them to group the resource listing, and adds extra
confirmation friction for destructive actions (AMI removal) on anything
tagged `production`. Existing untagged resources display as "unknown"
until tagged — the tool does not retroactively rewrite tags on resources it
didn't create.

**Rationale.**
- Directly serves the stated goal: distinguishing production from
  development/test at a glance, and grouping by application (`Project`),
  is exactly what "manage our RDM production and development instances"
  requires
- Inferring environment from free-text instance names (today's de facto
  approach) is fragile and inconsistent, as the live-account check showed
- Extra confirmation on production-tagged resources targets friction at
  the resource class where a mistake is most costly, rather than applying
  uniform friction everywhere

**Rejected alternatives.**
- *Work with what exists, no enforcement* — keeps inferring environment
  from the name string with no structured signal; considered, but doesn't
  move the account toward consistency and leaves "is this production?"
  a matter of reading a name carefully rather than checking a tag
- *Retroactively tag all existing resources* — out of scope for this tool;
  a one-time cleanup task, not an ongoing tool responsibility

**Consequences.**
- `PLAN.md` Phase 2 (listing) groups/filters by `Project`/`Environment`;
  Phases 4/5 (creation) prompt for and default these tags; Phase 6
  (removal) adds a heightened warning for `Environment=production`
- `DESIGN.md`'s IAM permission list must include `ec2:CreateTags` and
  `ec2:DescribeTags` (already present in `software_requirements.md`'s
  policy but missing from `DESIGN.md`'s own Assumptions section — fixed in
  this update)
- No migration or backfill of existing untagged resources is planned

---

## 2026-07-01 — V1 scope: ship the four primitives first, defer composite workflows

**Context.** The stated goal for this tool — speed up upgrading
deployments, create accurate test environments with confidence — is
naturally served by *composite* operations (e.g. "clone this instance for
testing, inheriting its network/instance-type config" or "upgrade with a
tracked rollback point"), not by the four raw primitives
(create-instance-from-AMI / create-AMI-from-instance / remove-AMI /
refresh) alone. Those primitives require the user to manually chain
operations and re-enter configuration (instance type, security groups,
subnet, IAM profile) from scratch each time, which is exactly the
slowness/accuracy-drift risk the stated goal wants to eliminate.

**Decision.** V1 of the Go tool ships a faithful port of the four existing
primitives (matching `ec2_ami_manager.bash`'s feature set), verified
against real AWS, before any composite workflow is added. "Clone instance
for testing" and "upgrade with a tracked rollback point" are recorded here
as intended fast-follow work, not dropped.

**Rationale.**
- Keeps the rewrite scoped and verifiable: Bash→Go parity is itself
  nontrivial (see "Retarget implementation from Bash to Go" below) and
  mixing in new composite behavior would make it harder to tell whether a
  bug is a porting regression or new-feature bug
- The primitives are the building blocks the composite workflows will call
  — building them first, correctly, is not wasted work
- Real-AWS verification (`TEST_PLAN_REAL_AWS.txt`) is easier to reason
  about against a known, already-specified feature set

**Rejected alternatives.**
- *Build composite workflows into v1 directly* — more directly serves the
  stated goal sooner, but risks conflating porting bugs with new-feature
  bugs during the highest-risk phase (initial Go implementation)

**Consequences.**
- `PLAN.md` gets a "Deferred / Future Work" section describing "Clone
  instance for testing" and "Upgrade with rollback point" so they aren't
  lost, to be scheduled once Phase 9 (real-AWS verification) passes
- The Project/Environment tagging convention (see companion decision
  above) is still built in v1, since the composite workflows will depend
  on it and it's needed for the listing/removal-friction behavior anyway

---

## 2026-07-01 — Retarget implementation from Bash to Go

**Context.** `ec2_ami_manager.bash` reached feature parity with the design
(Phases 0–7) but real-world use against production AWS resources surfaced a
string of bugs rooted in Bash's lack of static typing and its reliance on
runtime string construction for control flow:
- an `eval`-based array-copy helper (`show_pick_list`) had unbalanced
  quoting that crashed the interactive picker outright
- the AMI-name validation regex broke under BSD `grep` combined with a
  UTF-8 locale (`invalid character range`)
- the AMI-creation tag-specification string builder produced syntactically
  invalid AWS CLI shorthand that silently failed `create-image`, with no
  AMI created in any region and only a scrollback error message as evidence

Each bug class (shell quoting/escaping, locale-dependent tool behavior,
hand-built CLI argument strings) is structural to shelling out from Bash
rather than incidental, and none would be caught by static analysis before
runtime.

**Decision.** Set aside the Bash implementation and retarget the
interactive EC2/AMI manager to Go, in place in this repository, targeting
full feature parity with the existing design (all four operations: create
instance from AMI, create AMI from instance, remove AMI, main menu) before
the Bash version is retired.

**Rationale.**
- Go is this workspace's stated primary backend language (CLAUDE.md)
- A typed AWS SDK (see companion decision below) replaces "aws CLI + jq +
  eval" with compiled, typed API calls — the entire class of quoting/
  escaping bugs hit in this session becomes structurally impossible
- Go's `go test` and table-driven tests replace BATS plus a hand-rolled
  mock-`aws`-binary harness, and can mock the AWS SDK client via interfaces
  without shelling out at all
- The feature set, UX flow, and hard-won domain knowledge (multi-region
  aggregation, owned-AMIs-only scope, three-layer removal confirmation,
  fstrim/SSM pre-snapshot step, Invenio RDM crash-consistency guidance,
  volume-size time estimates) all carry forward unchanged — this is a
  reimplementation, not a redesign

**Rejected alternatives.**
- *Patch the specific bugs and stay in Bash* — would fix these three bugs
  but leaves the same eval/quoting/locale hazard class open for the next
  feature added
- *Python + boto3* — also a strong, typed-enough option with less ceremony
  than Go, but Go is this workspace's designated primary backend language
  (CLAUDE.md); Python/Perl are described there as secondary/legacy
- *Deno + TypeScript* — this workspace reserves Deno/TypeScript for
  middleware/frontend, not backend CLI tools (CLAUDE.md's layered
  architecture pattern)

**Consequences.**
- `ec2_ami_manager.bash`, `ami_copy.bash`, and their supporting docs remain
  in the repo unchanged as a working reference/spec until the Go version
  reaches parity and is verified against real AWS (the same retire-after-
  verify pattern already used for `ami_copy.bash`, see below)
- `DESIGN.md` and `PLAN.md` are retargeted for Go in this same update; the
  new `PLAN.md` phases restart from Phase 0 (Go module setup)
- BATS test debt for Phase 6/7 (`test_remove_ami.bats`, `test_menu.bats`) is
  superseded — those workflows get Go tests instead, not backfilled BATS
  tests
- `TEST_PLAN_REAL_AWS.txt`'s manual verification step now targets the Go
  binary, not `ec2_ami_manager.bash`

---

## 2026-07-01 — Use official AWS SDK for Go v2

**Context.** Retargeting to Go (see companion decision above) could either
keep the Bash version's shell-out pattern (`exec aws ...` from Go) or adopt
AWS's official Go SDK.

**Decision.** Use `github.com/aws/aws-sdk-go-v2` (with its `ec2` and `ssm`
service packages) for all AWS API calls. This is a third-party dependency
outside the pre-approved `github.com/rsdoiel` / `github.com/caltechlibrary`
namespaces (CLAUDE.md); explicitly approved for this project.

**Rationale.**
- Typed request/response structs eliminate the JSON-through-subshell-through-
  jq round trips that made the Bash version fragile
- No runtime dependency on the `aws` CLI binary or `jq` being installed —
  only the Go binary and AWS credentials
- Official, actively maintained by AWS
- Enables interface-based mocking of AWS calls in tests without a
  hand-rolled mock CLI binary (the pattern `tests/lib/test_helper.bash` used)

**Rejected alternatives.**
- *Shell out to the `aws` CLI from Go (`os/exec`)* — keeps the CLI-argument-
  quoting risk that caused this session's tag-specification bug, just moved
  into Go's `exec.Command` argument building; still requires the `aws` CLI
  as a runtime dependency
- *Hand-rolled AWS API client (SigV4 signing, raw REST calls)* —
  reinventing a well-solved, well-maintained problem for no benefit

**Consequences.**
- `go.mod` declares `github.com/aws/aws-sdk-go-v2` and its `ec2`/`ssm`/`sts`
  submodules as dependencies
- Credential resolution (env vars, `~/.aws/credentials`, SSO) is handled by
  the SDK's default credential chain, matching current Bash behavior
- Region iteration (the four configured regions) becomes explicit per-region
  SDK client construction, replacing the `AWS_REGION` env var wrapper
  pattern used in `ec2_ami_manager.bash`

---

## 2026-06-30 — Retire check_ami.bash and check_ec2_instances.bash

**Context.** `check_ami.bash` and `check_ec2_instances.bash` predate
`ec2_ami_manager.bash`. Both are non-interactive, read-only listing scripts
across the same four regions; their functionality is fully covered by
`list_ec2_instances()`/`list_amis()` and `display_instances()`/`display_amis()`
in `ec2_ami_manager.bash`, which additionally aggregate and sort consistently.

**Decision.** **Retire both scripts; the unified manager is the single
listing entry point.**

**Rationale.**
- No functionality in either script is missing from the manager
- Two parallel implementations of the same AWS queries is a maintenance cost
  with no offsetting benefit
- DESIGN.md's file-structure section listed them as "Existing" scripts to
  keep alongside the new manager without deciding their long-term role —
  this resolves that gap

**Rejected alternatives.**
- *Keep as quick non-interactive utilities* — considered for cron/scripting
  use cases, but nothing in this project currently invokes them
  non-interactively, and `ec2_ami_manager.bash` could add a non-interactive
  `--list` flag later if that need arises

**Consequences.**
- DESIGN.md's file-structure section should drop these two scripts
- Deletion is a separate, explicit step — not yet performed as of this entry

---

## 2026-06-30 — AMI-from-instance: fold ami_copy.bash capabilities into Phase 5

**Context.** `ami_copy.bash` (and `ami_copy_basic_steps.md`) was merged in
from a separate repository (`git log`: "merged ami copy from ami_copy
repo") before this project's DESIGN.md/DECISIONS.md/PLAN.md existed. It was
never reconciled with the design: it duplicates the "Create AMI from EC2
Instance" feature (Phase 5 — see "Include both running and stopped
instances for AMI creation" above) but is single-region only, and it
contains capabilities Phase 5's `create_ami_from_instance_workflow` lacks:
volume-size-based time estimates, prior-snapshot detection, an SSM `fstrim`
pre-snapshot optimization step, unbounded elapsed-time polling during
creation, and Postgres/OpenSearch-specific crash-consistency guidance for
running instances (relevant because this team's primary AMI target is
Invenio RDM instances).

Separately, Phase 5's `post_ami_creation_actions()` times out after 600
seconds (10 minutes) of polling. Per `ami_copy_basic_steps.md`'s own timing
table, this is too short for real usage — even small volumes take 5–15
minutes, and an Invenio RDM instance is estimated at 20–60+ minutes.

**Decision.** **Port `ami_copy.bash`'s capabilities into Phase 5's
multi-region workflow (tracked as Phase 5b in PLAN.md), then retire
`ami_copy.bash`.** Keep the multi-region aggregation Phase 5 already has
rather than narrowing to `ami_copy.bash`'s single-region scope.

**Rationale.**
- Multi-region aggregation (an earlier decision, see "All four regions
  aggregated in unified view" above) is more valuable than what
  `ami_copy.bash` offers and shouldn't be lost
- The volume-size estimate, fstrim step, and unbounded polling are real
  operational value specific to this team's large, stateful instances —
  losing them by simply deleting `ami_copy.bash` would be a regression
- The 600-second timeout in Phase 5 is a correctness bug independent of the
  consolidation question and should be fixed regardless

**Rejected alternatives.**
- *Keep both scripts indefinitely* — two divergent implementations of the
  same operation with different region scope is confusing and doubles
  future maintenance
- *Retire ami_copy.bash immediately without porting* — would silently drop
  working functionality (fstrim optimization, realistic time estimates,
  Invenio-specific guidance) that was never documented elsewhere

**Consequences.**
- New PLAN.md Phase 5b enumerates the specific functions/behavior to port
- `ami_copy.bash` and `ami_copy_basic_steps.md` are retired only after
  Phase 5b is implemented and verified, not before
- DESIGN.md's Core Features and File Structure sections should be updated
  once Phase 5b lands

---

## 2026-06-30 — AMI scope limited to account-owned only

**Context.** When listing available AMIs for instance creation, we must choose between showing all AMIs (public + private), only AWS marketplace AMIs, only account-owned AMIs, or a filtered subset.

**Decision.** **Show only AMIs owned by the current AWS account.**

**Rationale.**
- Reduces clutter and confusion for users managing their own infrastructure
- Public AMIs are typically accessed through other workflows (Launch Templates, console)
- Owned AMIs are the primary concern for lifecycle management (creation/removal)
- Simplifies the pick list to relevant, actionable items
- Aligns with the AMI removal feature which only makes sense for owned AMIs

**Rejected alternatives.**
- *All AMIs (public + private)* — would overwhelm users with thousands of irrelevant AMIs
- *Owned + AWS marketplace* — AWS marketplace AMIs cannot be deleted, creating inconsistency in the removal feature
- *Custom filter by tags* — adds complexity for initial implementation; can be added later as a filter option

**Consequences.**
- Users cannot launch instances from public AMIs through this script
- If needed, a future enhancement could add a "Show public AMIs" toggle
- The script focuses on managing custom/private infrastructure

---

## 2026-06-30 — All four regions aggregated in unified view

**Context.** The script must operate across four AWS regions. We must decide whether to show each region separately, aggregate all regions, or let the user select a region per operation.

**Decision.** **Aggregate all four regions in a single unified view.**

**Rationale.**
- Provides a complete picture of infrastructure across all regions at a glance
- Matches the user's stated requirement to "list the EC2 instances from one of the four regions we use"
- Simplifies the user experience by eliminating region selection from the critical path
- Instance and AMI IDs are globally unique, so aggregation doesn't cause collisions

**Rejected alternatives.**
- *Single region (user picks first)* — requires extra step and doesn't show full infrastructure state
- *Region per operation* — inconsistent UX, requires region selection for every action

**Consequences.**
- All API calls must be made against each of the four regions
- Aggregation logic needed to combine and deduplicate results
- Display must include region column for all resources
- Performance: slightly slower due to 4x API calls, but acceptable for interactive use

---

## 2026-06-30 — Prompt user for all instance creation parameters

**Context.** When creating an EC2 instance from an AMI, we need values for instance-type, key-name, security-group-ids, subnet-id, and other optional parameters. We must decide on the default behavior.

**Decision.** **Prompt the user for all required parameters interactively, with no hardcoded defaults.**

**Rationale.**
- Maximum flexibility for users with different requirements
- Prevents launching instances with inappropriate defaults
- Educational: helps users understand what's required for instance creation
- Reduces risk of launching instances in wrong subnet/security group

**Rejected alternatives.**
- *Hardcoded sensible defaults* — defaults may not be appropriate for all use cases; could lead to instances in wrong network configuration
- *Config file with overrides* — adds complexity; users may not have config files set up; harder to use across different projects

**Consequences.**
- More interactive steps required from user
- Need validation for each parameter
- Should provide lists of available options (key pairs, security groups, subnets) to help user choose
- May add helper to show "common" instance types with descriptions

---

## 2026-06-30 — User-provided AMI names for new AMIs

**Context.** When creating an AMI from an EC2 instance, we need a name for the new AMI. We must decide how this name is generated.

**Decision.** **Require user to provide the AMI name interactively.**

**Rationale.**
- AMI names are user-facing and should be meaningful
- Users have different naming conventions based on their organization
- Auto-generated names might not match organizational standards
- Simple and predictable for users

**Rejected alternatives.**
- *Auto-generate from instance name* — instance may not have a Name tag; generated names might not be descriptive enough
- *Auto-generate with prefix* — requires configuration of prefix; less flexible
- *Name + tags* — adds complexity for initial implementation

**Consequences.**
- User must think of a name each time (minor friction)
- Can add auto-suggestion in future based on instance metadata
- Name validation needed (AMI name constraints: 3-128 characters, specific allowed characters)

---

## 2026-06-30 — Multi-layer confirmation for AMI removal

**Context.** Removing an AMI is destructive and irreversible. We must implement safety mechanisms to prevent accidental deletion.

**Decision.** **Implement three-layer confirmation:**
1. Dry-run first: Show exactly what would be deleted
2. Show dependencies: List any instances currently using this AMI
3. Type to confirm: User must type the AMI ID or name exactly

**Rationale.**
- AMI deletion cannot be undone
- Instances using a deleted AMI cannot be launched or rebooted (if instance-store backed)
- Typo in selection could cause significant data loss
- Multiple confirmation layers provide defense in depth

**Rejected alternatives.**
- *Simple yes/no prompt* — too easy to accidentally confirm
- *Dry-run only* — doesn't prevent accidental confirmation
- *Show dependencies only* — doesn't verify user intentionality

**Consequences.**
- More steps required to delete an AMI (acceptable for safety-critical operation)
- Need to implement dependency checking (query instances by ImageId)
- Must handle case where AMI has no instances using it
- Error handling for typed confirmation mismatch

---

## 2026-06-30 — Use AWS CLI v2 with jq for JSON parsing

**Context.** We need to interact with AWS APIs and parse their JSON output in Bash.

**Decision.** **Use AWS CLI v2 with jq for JSON parsing.**

**Rationale.**
- AWS CLI v2 is the current standard, maintained by AWS
- jq is the de facto standard for JSON parsing in shell scripts
- Both are likely already installed in environments using AWS
- Provides full access to AWS API functionality

**Rejected alternatives.**
- *AWS CLI v1* — deprecated, no longer maintained
- *aws-shell* — not suitable for scripting
- *Boto3 (Python)* — requires Python, not pure Bash
- *Custom JSON parsing with grep/sed* — fragile, error-prone

**Consequences.**
- Script requires AWS CLI v2 and jq as dependencies
- Need to check for these dependencies on startup
- Error messages should guide users to install missing tools

---

## 2026-06-30 — Numbered menu for pick lists

**Context.** The script needs to present lists of resources (AMIs, instances) for user selection. We must choose an interaction method.

**Decision.** **Use numbered menus for resource selection.**

**Rationale.**
- Works in all terminal environments (no special dependencies)
- Simple and familiar to users
- Easy to implement in pure Bash
- No external tools required (like fzf)

**Rejected alternatives.**
- *fzf fuzzy finder* — requires fzf installation; not available on all systems
- *arrow-key navigation* — complex to implement in pure Bash; requires ncurses or similar
- *Search/filter* — adds complexity for initial implementation

**Consequences.**
- For large lists (>20 items), may need pagination
- User must type number corresponding to selection
- Input validation needed for number range
- Can add fzf support as optional enhancement in future

---

## 2026-06-30 — Include both running and stopped instances for AMI creation

**Context.** When creating an AMI from an instance, the instance can be in various states. We must decide which states are allowed.

**Decision.** **Allow both running and stopped instances to be selected for AMI creation.**

**Rationale.**
- AWS supports creating AMIs from both running and stopped instances
- Stopped instances are common targets for AMI creation (clean state)
- Running instances might need AMIs created for backup purposes
- Matches the user's stated requirement

**Rejected alternatives.**
- *Running only* — excludes common use case of creating AMI from stopped instance
- *Stopped only* — excludes emergency backup scenarios
- *All states including terminated* — terminated instances cannot have AMIs created

**Consequences.**
- Need to filter out terminated instances from the pick list
- May want to warn user about creating AMI from running instance (potential inconsistency)
- For running instances, can offer no-reboot option

---

## 2026-06-30 — Refresh data after each operation

**Context.** After performing operations (create instance, create AMI, remove AMI), the displayed resource lists become stale. We must decide when to refresh.

**Decision.** **Refresh all resource data after each operation that modifies state, and provide a manual refresh option.**

**Rationale.**
- Ensures user always sees current state
- Automated refresh after modifications provides immediate feedback
- Manual refresh option allows user to see latest state at any time
- Simple to implement (re-run the listing functions)

**Rejected alternatives.**
- *Refresh only on demand* — user might not realize data is stale
- *Periodic auto-refresh* — adds complexity, unnecessary API calls
- *No refresh* — poor UX, user sees outdated information

**Consequences.**
- Each operation will include the cost of 8 API calls (4 regions x 2 resource types)
- Need to consider performance for slow connections
- Can add progress indicators during refresh

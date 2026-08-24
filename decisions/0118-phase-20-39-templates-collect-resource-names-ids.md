---
id: "0118"
title: "Phase 20.39 templates collect resource names/IDs, not ARNs"
date: "2026-07-23"
status: accepted
kind: refinement
trigger: ""
project: clasm
phase: ""
supersedes: []
superseded_by: []
relates_to: []
initiative: ""
session: ""
decisions: []
tags: []
uuid: "a300a71a-ba73-4079-b72a-005927c8f28b"
origin_host: "MACMINI-RD.local"
---

**Context.** Found via live usage while testing the Static Website
template: typing a full ARN (`arn:aws:s3:::my-bucket`) for an S3 bucket
forces a cognitive reformatting step the operator wouldn't otherwise
need -- they know the bucket *name*, not its ARN shape. Worse for
CloudFront: finding a distribution's ARN requires either the AWS
Console or a separate CLI lookup (`aws cloudfront list-distributions`),
breaking the interactive workflow entirely. Both are avoidable: clasm
already resolves the account ID once at startup
(`sts:GetCallerIdentity`, `awsclient.CheckCredentials`) and already
knows the region, which is all that's needed to construct these ARNs
from the operator's plain name/ID.

**Decision.** `IAMRoleTemplateParam` gains a `BuildARN(accountID,
region, value string) string` field. Each of the five templates'
params now prompts for a plain name/ID (an S3 bucket name, a CloudFront
distribution ID, a CloudWatch log group name, a Secrets Manager secret
name) instead of a full ARN; `createIAMRoleFromTemplate` calls
`BuildARN` (when set and the answer is non-blank) right after collecting
each answer, storing the *constructed* ARN under the same key
`BuildPolicy` already expects -- so the `BuildPolicy` functions
themselves (`staticWebsiteStatements`, `rdmRepositoryStatements`, etc.)
needed no changes at all, only the collection step and the prompts'
wording. `CreateIAMRoleFromTemplate`/`createIAMRoleFromTemplate` gained
`accountID, region string` parameters, threaded from `main.go`'s
existing `account` (from `CheckCredentials`, already resolved at
startup) and `cfg.Regions[0]`.

For Secrets Manager specifically, the constructed value is a *pattern*
with a trailing wildcard (`arn:...:secret:name-*`), not an exact ARN --
Secrets Manager appends a random 6-character suffix to every secret's
real ARN that the operator can't know in advance, so a trailing
wildcard is the standard, idiomatic way IAM policies scope access to a
secret by name.

**Rejected alternative.** *Keep collecting full ARNs, improve the
prompt wording only* -- rejected: the CloudFront case specifically
can't be fixed by better wording alone, since finding the ARN still
requires leaving the tool. Building the ARN from a shorter, more
memorable identifier removes the friction at its source rather than
just describing it better.

**Consequences.** CloudFront and Secrets Manager ARNs are global/
region-and-account-scoped in ways that don't always match `cfg.
Regions[0]` exactly (e.g. a secret could live in a different region than
the account's primary configured one) -- accepted as a reasonable
default for this phase, matching how the rest of clasm already treats
`cfg.Regions[0]` as the primary region for singleton, not-really-per-
region concerns. All five templates' `BuildPolicy` functions and their
existing unit tests were unaffected -- only `createIAMRoleFromTemplate`'s
tests changed, from typing literal ARN strings as prompt answers to
typing plain names, with an added assertion confirming the constructed
ARN appears correctly in the resulting policy document.

---


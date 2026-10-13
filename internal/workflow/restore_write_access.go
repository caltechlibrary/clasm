package workflow

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// Whether an instance can write to a bucket a restored database names (DR-0187).
// There are two ways an RDM instance writes to S3: the role of its instance
// profile, and the access key in /Sites/.env that the app uses. Each is put to
// IAM's policy simulator. Anything that cannot be told is treated as writable and
// says why: the guard never passes on a guess.
//
// The simulator evaluates identity policies and permissions boundaries only. A
// bucket policy that grants a write to the principal is not seen.

// DefaultRDMEnvFile is where the RDM app's secrets, including the S3 access key,
// live on an instance clasm manages (the caltechauthors README: "a .env file in
// /Sites").
const DefaultRDMEnvFile = "/Sites/.env"

// writeActions are the S3 actions that make a bucket writable: an instance that
// may delete production's objects is as dangerous as one that may overwrite them.
var writeActions = []string{"s3:PutObject", "s3:DeleteObject"}

// BucketWriteAccess is the verdict for one bucket.
type BucketWriteAccess struct {
	Bucket   string
	Writable bool
	// Via says how it is writable, or what was checked and found read-only, or why
	// it is treated as writable ("... (treated as writable)").
	Via []string
}

// accessKeyIDPattern is what an AWS access key ID looks like; anything else read
// from the env file is not trusted as one.
var accessKeyIDPattern = regexp.MustCompile(`^[A-Z0-9]{16,128}$`)

// buildEnvKeyCommand reads the access key ID, and only the ID, from the env file.
// The secret is never read: nothing in the guard needs it, and it must not reach
// a log. Output is one of clasm-env=absent, clasm-env=none or clasm-env-key=<ID>.
func buildEnvKeyCommand() string {
	f := shellQuote(DefaultRDMEnvFile)
	return fmt.Sprintf(`f=%s; if [ ! -e "$f" ]; then echo clasm-env=absent; else k=$(grep -s '^INVENIO_S3_ACCESS_KEY_ID=' "$f" | head -n 1 | cut -d= -f2-); if [ -z "$k" ]; then echo clasm-env=none; else echo "clasm-env-key=$k"; fi; fi`, f)
}

// maskKeyID shows only the last four characters of an access key ID.
func maskKeyID(id string) string {
	if len(id) <= 4 {
		return "…" + id
	}
	return "…" + id[len(id)-4:]
}

// writePrincipal is one identity that might write: a role of the instance's
// profile or the user who owns the app's access key.
type writePrincipal struct {
	label string // "role restore-role", "key …1234 (user caltechauthors-readonly)"
	arn   string
}

// principalSet is what the instance can act as, found once for all buckets.
type principalSet struct {
	principals []writePrincipal
	notes      []string // things checked that make no principal, kept for the read-only reason
	unknowns   []string // things that could not be told, each making every bucket writable
}

func (p *principalSet) unknown(format string, a ...any) {
	p.unknowns = append(p.unknowns, fmt.Sprintf(format, a...)+" (treated as writable)")
}

func (p *principalSet) addRoles(ctx context.Context, ec2Client awsclient.EC2API, iamClient awsclient.IAMAPI, instanceID string) {
	_, profileARN, found, err := describeCurrentInstanceProfileAssociation(ctx, ec2Client, instanceID)
	if err != nil {
		p.unknown("could not read the instance profile association of %s: %v", instanceID, err)
		return
	}
	if !found {
		p.notes = append(p.notes, "no instance profile")
		return
	}
	profileName := profileNameFromARN(profileARN)
	out, err := iamClient.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{InstanceProfileName: aws.String(profileName)})
	if err != nil {
		p.unknown("could not read instance profile %s: %v", profileName, err)
		return
	}
	for _, r := range out.InstanceProfile.Roles {
		p.principals = append(p.principals, writePrincipal{label: "role " + aws.ToString(r.RoleName), arn: aws.ToString(r.Arn)})
	}
}

func (p *principalSet) addEnvKey(ctx context.Context, ssmClient awsclient.SSMAPI, iamClient awsclient.IAMAPI, instanceID string) {
	stdout, status, err := RunShellCommand(ctx, ssmClient, instanceID, buildEnvKeyCommand(), DefaultBackupListTimeout, DefaultSSMPollInterval)
	if err != nil {
		p.unknown("could not read %s on %s: %v", DefaultRDMEnvFile, instanceID, err)
		return
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		p.unknown("reading %s on %s failed (%s)", DefaultRDMEnvFile, instanceID, status)
		return
	}
	line := strings.TrimSpace(stdout)
	switch {
	case line == "clasm-env=absent":
		p.notes = append(p.notes, "no INVENIO_S3 key ("+DefaultRDMEnvFile+" does not exist)")
		return
	case line == "clasm-env=none":
		p.notes = append(p.notes, "no INVENIO_S3 key in "+DefaultRDMEnvFile)
		return
	case !strings.HasPrefix(line, "clasm-env-key="):
		p.unknown("could not make sense of what reading %s returned", DefaultRDMEnvFile)
		return
	}
	keyID := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "clasm-env-key=")), `"'`)
	if !accessKeyIDPattern.MatchString(keyID) {
		p.unknown("the INVENIO_S3_ACCESS_KEY_ID in %s is not an access key ID", DefaultRDMEnvFile)
		return
	}
	masked := maskKeyID(keyID)
	used, err := iamClient.GetAccessKeyLastUsed(ctx, &iam.GetAccessKeyLastUsedInput{AccessKeyId: aws.String(keyID)})
	if err != nil {
		p.unknown("could not find the owner of access key %s: %v", masked, err)
		return
	}
	user := aws.ToString(used.UserName)
	if user == "" {
		p.unknown("access key %s has never been used, so its owner is unknown", masked)
		return
	}
	u, err := iamClient.GetUser(ctx, &iam.GetUserInput{UserName: aws.String(user)})
	if err != nil || u.User == nil || aws.ToString(u.User.Arn) == "" {
		p.unknown("could not read user %s, the owner of access key %s: %v", user, masked, err)
		return
	}
	p.principals = append(p.principals, writePrincipal{label: fmt.Sprintf("key %s (user %s)", masked, user), arn: aws.ToString(u.User.Arn)})
}

// mayWrite asks the simulator whether principal may do any of writeActions on the
// bucket's objects.
func mayWrite(ctx context.Context, iamClient awsclient.IAMAPI, principal writePrincipal, bucket string) (bool, error) {
	out, err := iamClient.SimulatePrincipalPolicy(ctx, &iam.SimulatePrincipalPolicyInput{
		PolicySourceArn: aws.String(principal.arn),
		ActionNames:     writeActions,
		ResourceArns:    []string{"arn:aws:s3:::" + bucket + "/*"},
	})
	if err != nil {
		return false, err
	}
	for _, r := range out.EvaluationResults {
		if r.EvalDecision == iamtypes.PolicyEvaluationDecisionTypeAllowed {
			return true, nil
		}
	}
	return false, nil
}

// CheckBucketWriteAccess decides, for each bucket, whether the instance can write
// to it: through the role of its instance profile or through the access key in
// /Sites/.env, each put to IAM's policy simulator. Whatever cannot be told makes
// every bucket writable, with the reason in Via. Every call is read-only, and only
// the key ID, never the secret, is read from the instance.
//
// @param ctx {context.Context} the call's context
// @param ec2Client {awsclient.EC2API} the EC2 client for the instance's region
// @param ssmClient {awsclient.SSMAPI} the SSM client for the instance's region
// @param iamClient {awsclient.IAMAPI} the IAM client
// @param instanceID {string} the instance that would run the restored app
// @param buckets {[]string} the buckets to judge
// @returns {[]BucketWriteAccess, error} one verdict per bucket, in order
// @example
//
//	verdicts, err := CheckBucketWriteAccess(ctx, ec2, ssm, iamClient, "i-0abc", []string{"caltechauthors"})
func CheckBucketWriteAccess(ctx context.Context, ec2Client awsclient.EC2API, ssmClient awsclient.SSMAPI, iamClient awsclient.IAMAPI, instanceID string, buckets []string) ([]BucketWriteAccess, error) {
	if len(buckets) == 0 {
		return nil, nil
	}
	var set principalSet
	set.addRoles(ctx, ec2Client, iamClient, instanceID)
	set.addEnvKey(ctx, ssmClient, iamClient, instanceID)

	verdicts := make([]BucketWriteAccess, 0, len(buckets))
	for _, bucket := range buckets {
		v := BucketWriteAccess{Bucket: bucket, Writable: len(set.unknowns) > 0}
		for _, p := range set.principals {
			ok, err := mayWrite(ctx, iamClient, p, bucket)
			switch {
			case err != nil:
				v.Writable = true
				v.Via = append(v.Via, fmt.Sprintf("could not simulate %s writing to %s: %v (treated as writable)", p.label, bucket, err))
			case ok:
				v.Writable = true
				v.Via = append(v.Via, fmt.Sprintf("%s may write (%s)", p.label, strings.Join(writeActions, " or ")))
			default:
				v.Via = append(v.Via, fmt.Sprintf("%s may not write", p.label))
			}
		}
		v.Via = append(v.Via, set.notes...)
		v.Via = append(v.Via, set.unknowns...)
		verdicts = append(verdicts, v)
	}
	return verdicts, ctx.Err()
}

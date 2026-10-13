package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// Whether an instance can write to a bucket a restored database names (DR-0187).
// Two ways: the role of its instance profile, and the access key in /Sites/.env
// that RDM's app uses. Each is asked of IAM's policy simulator. What cannot be
// told is treated as writable, with the reason: the guard never passes on a guess.

const (
	writeKeyID    = "AKIAEXAMPLE1234ABCD"
	writeRoleARN  = "arn:aws:iam::123456789012:role/restore-role"
	writeUserARN  = "arn:aws:iam::123456789012:user/caltechauthors-readonly"
	writeProfARN  = "arn:aws:iam::123456789012:instance-profile/restore-profile"
	writeInstance = "i-057d8cc298da8f186"
)

type writeFixture struct {
	ec2 *fakeEC2Client
	ssm *fakeSSMClient
	iam *fakeIAMClient
}

// newWriteFixture is an instance with profile restore-profile holding restore-role,
// and /Sites/.env absent, whose simulator says what decisions maps (principal ARN +
// "|" + resource ARN to a decision; anything absent is implicitDeny).
func newWriteFixture(decisions map[string]iamtypes.PolicyEvaluationDecisionType) writeFixture {
	ec2Fake := &fakeEC2Client{currentInstanceProfileAssociationID: "iip-1", currentInstanceProfileARN: writeProfARN}
	ssmFake := &fakeSSMClient{
		commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess,
		responses: []ssmCommandResponse{{substring: "INVENIO_S3_ACCESS_KEY_ID", stdout: "clasm-env=absent\n", status: types.CommandInvocationStatusSuccess}},
	}
	iamFake := &fakeIAMClient{
		getInstanceProfileOut: &iam.GetInstanceProfileOutput{InstanceProfile: &iamtypes.InstanceProfile{
			InstanceProfileName: aws.String("restore-profile"),
			Roles:               []iamtypes.Role{{RoleName: aws.String("restore-role"), Arn: aws.String(writeRoleARN)}},
		}},
		simulateDecisions: decisions,
	}
	return writeFixture{ec2Fake, ssmFake, iamFake}
}

func (f writeFixture) check(buckets ...string) ([]BucketWriteAccess, error) {
	return CheckBucketWriteAccess(context.Background(), f.ec2, f.ssm, f.iam, writeInstance, buckets)
}

func allowed(principal, bucket string) string { return principal + "|arn:aws:s3:::" + bucket + "/*" }

func TestCheckBucketWriteAccess_RoleThatMayPutIsWritable(t *testing.T) {
	f := newWriteFixture(map[string]iamtypes.PolicyEvaluationDecisionType{allowed(writeRoleARN, "caltechauthors"): iamtypes.PolicyEvaluationDecisionTypeAllowed})
	got, err := f.check("caltechauthors")
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if !got[0].Writable || got[0].Bucket != "caltechauthors" || !strings.Contains(strings.Join(got[0].Via, "; "), "restore-role") {
		t.Errorf("want writable via the role, got %+v", got[0])
	}
	in := f.iam.lastSimulateInput
	if in == nil || aws.ToString(in.PolicySourceArn) != writeRoleARN ||
		strings.Join(in.ActionNames, ",") != "s3:PutObject,s3:DeleteObject" || strings.Join(in.ResourceArns, ",") != "arn:aws:s3:::caltechauthors/*" {
		t.Errorf("simulate input = %+v", in)
	}
}

// DeleteObject alone is enough: a role that may delete production's backups is as
// dangerous as one that may overwrite them.
func TestCheckBucketWriteAccess_DeleteAloneIsWritable(t *testing.T) {
	f := newWriteFixture(nil)
	f.iam.simulateActionDecisions = map[string]iamtypes.PolicyEvaluationDecisionType{"s3:DeleteObject": iamtypes.PolicyEvaluationDecisionTypeAllowed}
	got, err := f.check("b")
	if err != nil || !got[0].Writable {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestCheckBucketWriteAccess_ReadOnlyRoleIsNotWritable(t *testing.T) {
	f := newWriteFixture(nil) // everything implicitly denied
	got, err := f.check("caltechauthors")
	if err != nil || got[0].Writable {
		t.Fatalf("a role with no write allowed is read-only: %+v, %v", got, err)
	}
	via := strings.Join(got[0].Via, "; ")
	if !strings.Contains(via, "restore-role") || !strings.Contains(via, "no INVENIO_S3 key") {
		t.Errorf("the reasons should name the role checked and the absent key: %q", via)
	}
}

func TestCheckBucketWriteAccess_ExplicitDenyIsNotWritable(t *testing.T) {
	f := newWriteFixture(map[string]iamtypes.PolicyEvaluationDecisionType{allowed(writeRoleARN, "b"): iamtypes.PolicyEvaluationDecisionTypeExplicitDeny})
	if got, err := f.check("b"); err != nil || got[0].Writable {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestCheckBucketWriteAccess_BucketsAreJudgedOneByOne(t *testing.T) {
	f := newWriteFixture(map[string]iamtypes.PolicyEvaluationDecisionType{allowed(writeRoleARN, "bucket-a"): iamtypes.PolicyEvaluationDecisionTypeAllowed})
	got, err := f.check("bucket-a", "bucket-b")
	if err != nil || len(got) != 2 || !got[0].Writable || got[1].Writable || got[0].Bucket != "bucket-a" || got[1].Bucket != "bucket-b" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestCheckBucketWriteAccess_AnyRoleOfTheProfileCounts(t *testing.T) {
	f := newWriteFixture(map[string]iamtypes.PolicyEvaluationDecisionType{allowed("arn:aws:iam::123456789012:role/second", "b"): iamtypes.PolicyEvaluationDecisionTypeAllowed})
	f.iam.getInstanceProfileOut.InstanceProfile.Roles = append(f.iam.getInstanceProfileOut.InstanceProfile.Roles,
		iamtypes.Role{RoleName: aws.String("second"), Arn: aws.String("arn:aws:iam::123456789012:role/second")})
	if got, err := f.check("b"); err != nil || !got[0].Writable {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestCheckBucketWriteAccess_NoProfileMeansNoRoleToAsk(t *testing.T) {
	f := newWriteFixture(nil)
	f.ec2.currentInstanceProfileAssociationID = ""
	got, err := f.check("b")
	if err != nil || got[0].Writable {
		t.Fatalf("got %+v, %v", got, err)
	}
	if f.iam.lastSimulateInput != nil {
		t.Errorf("with no profile there is no principal to simulate")
	}
	if !strings.Contains(strings.Join(got[0].Via, "; "), "no instance profile") {
		t.Errorf("via = %v", got[0].Via)
	}
}

func envResponse(stdout string) []ssmCommandResponse {
	return []ssmCommandResponse{{substring: "INVENIO_S3_ACCESS_KEY_ID", stdout: stdout, status: types.CommandInvocationStatusSuccess}}
}

func TestCheckBucketWriteAccess_NoKeyInTheEnvFileIsFine(t *testing.T) {
	for _, out := range []string{"clasm-env=absent\n", "clasm-env=none\n"} {
		f := newWriteFixture(nil)
		f.ssm.responses = envResponse(out)
		if got, err := f.check("b"); err != nil || got[0].Writable {
			t.Errorf("%q: got %+v, %v", out, got, err)
		}
	}
}

func TestCheckBucketWriteAccess_KeyOfAWritingUserIsWritableAndTheKeyIsMasked(t *testing.T) {
	f := newWriteFixture(map[string]iamtypes.PolicyEvaluationDecisionType{allowed(writeUserARN, "caltechauthors"): iamtypes.PolicyEvaluationDecisionTypeAllowed})
	f.ssm.responses = envResponse("clasm-env-key=" + writeKeyID + "\n")
	f.iam.accessKeyUsers = map[string]string{writeKeyID: "caltechauthors-readonly"}
	f.iam.userArns = map[string]string{"caltechauthors-readonly": writeUserARN}
	got, err := f.check("caltechauthors")
	if err != nil || !got[0].Writable {
		t.Fatalf("got %+v, %v", got, err)
	}
	via := strings.Join(got[0].Via, "; ")
	if !strings.Contains(via, "ABCD") || strings.Contains(via, writeKeyID) || strings.Contains(via, "AKIAEXAMPLE") {
		t.Errorf("only the key's last four characters may be shown: %q", via)
	}
}

// The existing read-only setup: a key whose user may only read must not force a
// confirmation.
func TestCheckBucketWriteAccess_KeyOfAReadOnlyUserIsNotWritable(t *testing.T) {
	f := newWriteFixture(nil)
	f.ssm.responses = envResponse("clasm-env-key=" + writeKeyID + "\n")
	f.iam.accessKeyUsers = map[string]string{writeKeyID: "caltechauthors-readonly"}
	f.iam.userArns = map[string]string{"caltechauthors-readonly": writeUserARN}
	got, err := f.check("caltechauthors")
	if err != nil || got[0].Writable {
		t.Fatalf("got %+v, %v", got, err)
	}
	if !strings.Contains(strings.Join(got[0].Via, "; "), "caltechauthors-readonly") {
		t.Errorf("the reasons should name the key's user: %v", got[0].Via)
	}
}

func TestCheckBucketWriteAccess_WhatCannotBeToldIsWritable(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(f *writeFixture)
		reason string // the part of the reason that tells which case it was
	}{
		"the env file cannot be read": {func(f *writeFixture) {
			f.ssm.responses = []ssmCommandResponse{{substring: "INVENIO_S3_ACCESS_KEY_ID", stdout: "boom", status: types.CommandInvocationStatusFailed}}
		}, "failed"},
		"the env output is not recognised": {func(f *writeFixture) { f.ssm.responses = envResponse("something else\n") }, "make sense"},
		"the key is not recognised":        {func(f *writeFixture) { f.ssm.responses = envResponse("clasm-env-key=not a key id!\n") }, "is not an access key ID"},
		"the key was never used": {func(f *writeFixture) {
			f.ssm.responses = envResponse("clasm-env-key=" + writeKeyID + "\n")
			f.iam.accessKeyUsers = map[string]string{writeKeyID: ""}
		}, "never been used"},
		"the key's owner cannot be looked up": {func(f *writeFixture) {
			f.ssm.responses = envResponse("clasm-env-key=" + writeKeyID + "\n")
			f.iam.getAccessKeyLastUsedErr = errors.New("AccessDenied")
		}, "could not find the owner"},
		"the key's user cannot be read": {func(f *writeFixture) {
			f.ssm.responses = envResponse("clasm-env-key=" + writeKeyID + "\n")
			f.iam.accessKeyUsers = map[string]string{writeKeyID: "caltechauthors-readonly"}
			f.iam.getUserErr = errors.New("AccessDenied: iam:GetUser")
		}, "could not read user"},
		"the simulator is refused": {func(f *writeFixture) { f.iam.simulateErr = errors.New("AccessDenied: iam:SimulatePrincipalPolicy") }, "could not simulate"},
		"the profile cannot be read": {func(f *writeFixture) {
			f.iam.getInstanceProfileErr = errors.New("AccessDenied")
		}, "could not read instance profile"},
		"the association cannot be read": {func(f *writeFixture) {
			f.ec2.describeIamInstanceProfileAssociationsErr = errors.New("throttled")
		}, "could not read the instance profile association"},
	} {
		f := newWriteFixture(nil)
		tc.mutate(&f)
		got, err := f.check("b")
		if err != nil || len(got) != 1 || !got[0].Writable {
			t.Errorf("%s: unknown must be writable, got %+v, %v", name, got, err)
			continue
		}
		via := strings.Join(got[0].Via, "; ")
		if !strings.Contains(via, "treated as writable") || !strings.Contains(via, tc.reason) {
			t.Errorf("%s: the reason must say why (%q): %v", name, tc.reason, got[0].Via)
		}
	}
}

// The simulator's own refusal text reaches the operator, so a missing permission
// is obvious.
func TestCheckBucketWriteAccess_TheReasonCarriesTheError(t *testing.T) {
	f := newWriteFixture(nil)
	f.iam.simulateErr = errors.New("AccessDenied: iam:SimulatePrincipalPolicy")
	got, _ := f.check("b")
	if !strings.Contains(strings.Join(got[0].Via, "; "), "iam:SimulatePrincipalPolicy") {
		t.Errorf("via = %v", got[0].Via)
	}
}

// Only the access key ID is ever read from the env file, never the secret.
func TestCheckBucketWriteAccess_NeverReadsTheSecret(t *testing.T) {
	f := newWriteFixture(nil)
	if _, err := f.check("b"); err != nil {
		t.Fatal(err)
	}
	if len(f.ssm.sentCommands) != 1 {
		t.Fatalf("one command reads the env file, got %v", f.ssm.sentCommands)
	}
	cmd := f.ssm.sentCommands[0]
	if strings.Contains(strings.ToUpper(cmd), "SECRET") || !strings.Contains(cmd, "/Sites/.env") || !strings.Contains(cmd, "INVENIO_S3_ACCESS_KEY_ID") {
		t.Errorf("the command must name the key ID line and no secret: %s", cmd)
	}
}

func TestCheckBucketWriteAccess_NoBucketsAsksNothing(t *testing.T) {
	f := newWriteFixture(nil)
	got, err := f.check()
	if err != nil || len(got) != 0 || len(f.ssm.sentCommands) != 0 || f.iam.lastSimulateInput != nil {
		t.Fatalf("got %+v, %v, commands=%v", got, err, f.ssm.sentCommands)
	}
}

package workflow

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/caltechlibrary/clasm/internal/config"
)

func TestDecodePolicyDocument_URLDecodesAndPrettyPrints(t *testing.T) {
	encoded := "%7B%22Version%22%3A%222012-10-17%22%2C%22Statement%22%3A%5B%7B%22Effect%22%3A%22Allow%22%7D%5D%7D"
	got, err := decodePolicyDocument(encoded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "\"Version\": \"2012-10-17\"") {
		t.Errorf("expected pretty-printed JSON with the decoded content, got:\n%s", got)
	}
	if !strings.Contains(got, "\n") {
		t.Errorf("expected multi-line pretty-printed output, got a single line:\n%s", got)
	}
}

func TestDecodePolicyDocument_ErrorOnMalformedURLEncoding(t *testing.T) {
	_, err := decodePolicyDocument("%ZZ")
	if err == nil {
		t.Fatal("expected an error for invalid URL encoding")
	}
}

func TestFetchIAMRoleDetail_AssemblesEverything(t *testing.T) {
	fake := &fakeIAMClient{
		getRoleOut: &iam.GetRoleOutput{
			Role: &iamtypes.Role{
				RoleName:                 aws.String("air-sampling"),
				CreateDate:               awsTimePtr(time.Date(2020, 2, 5, 22, 28, 0, 0, time.UTC)),
				AssumeRolePolicyDocument: aws.String("%7B%22Version%22%3A%222012-10-17%22%7D"),
				Tags:                     []iamtypes.Tag{{Key: aws.String("origin"), Value: aws.String("dld")}},
			},
		},
		attachedPolicyArns: map[string][]string{
			"air-sampling": {"arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"},
		},
		rolePolicyNames: map[string][]string{
			"air-sampling": {"AirSamplingWriteAccess"},
		},
		instanceProfiles: []iamtypes.InstanceProfile{
			{
				InstanceProfileName: aws.String("air-sampling-profile"),
				Roles:               []iamtypes.Role{{RoleName: aws.String("air-sampling")}},
			},
			{
				InstanceProfileName: aws.String("unrelated-profile"),
				Roles:               []iamtypes.Role{{RoleName: aws.String("some-other-role")}},
			},
		},
	}

	got, err := fetchIAMRoleDetail(context.Background(), fake, "air-sampling")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "air-sampling" {
		t.Errorf("Name = %q, want air-sampling", got.Name)
	}
	if got.Tags["origin"] != "dld" {
		t.Errorf("Tags = %v, want origin=dld", got.Tags)
	}
	if !strings.Contains(got.TrustPolicy, "\"Version\": \"2012-10-17\"") {
		t.Errorf("TrustPolicy = %q, want decoded+pretty-printed JSON", got.TrustPolicy)
	}
	if len(got.AttachedPolicies) != 1 || got.AttachedPolicies[0].Name != "AWSLambdaBasicExecutionRole" {
		t.Errorf("AttachedPolicies = %+v, want one entry named AWSLambdaBasicExecutionRole", got.AttachedPolicies)
	}
	if len(got.InlinePolicyNames) != 1 || got.InlinePolicyNames[0] != "AirSamplingWriteAccess" {
		t.Errorf("InlinePolicyNames = %v, want [AirSamplingWriteAccess]", got.InlinePolicyNames)
	}
	if got.SSMCapable {
		t.Error("expected SSMCapable=false: attachedPolicyArns here doesn't include AmazonSSMManagedInstanceCore")
	}
	if len(got.ReferencedByProfiles) != 1 || got.ReferencedByProfiles[0] != "air-sampling-profile" {
		t.Errorf("ReferencedByProfiles = %v, want [air-sampling-profile] (unrelated-profile should be excluded)", got.ReferencedByProfiles)
	}
}

func TestFetchIAMRoleDetail_SSMCapableWhenManagedPolicyAttached(t *testing.T) {
	fake := &fakeIAMClient{
		getRoleOut: &iam.GetRoleOutput{Role: &iamtypes.Role{RoleName: aws.String("ssm-role")}},
		attachedPolicyArns: map[string][]string{
			"ssm-role": {"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"},
		},
	}
	got, err := fetchIAMRoleDetail(context.Background(), fake, "ssm-role")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.SSMCapable {
		t.Error("expected SSMCapable=true when AmazonSSMManagedInstanceCore is attached")
	}
}

func TestFetchIAMRoleDetail_PropagatesGetRoleError(t *testing.T) {
	fake := &fakeIAMClient{getRoleErr: errors.New("boom")}
	_, err := fetchIAMRoleDetail(context.Background(), fake, "air-sampling")
	if err == nil {
		t.Fatal("expected an error to propagate")
	}
}

func TestFetchAttachedPolicyDocument_ReturnsDecodedDocument(t *testing.T) {
	arn := "arn:aws:iam::123456789012:policy/s3-backup-access"
	fake := &fakeIAMClient{
		policiesByArn: map[string]iamtypes.Policy{
			arn: {Arn: aws.String(arn), DefaultVersionId: aws.String("v1")},
		},
		policyVersions: map[string]string{
			arn + "/v1": "%7B%22Version%22%3A%222012-10-17%22%7D",
		},
	}
	got, err := fetchAttachedPolicyDocument(context.Background(), fake, arn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "\"Version\": \"2012-10-17\"") {
		t.Errorf("got %q, want decoded+pretty-printed JSON", got)
	}
}

func TestFetchAttachedPolicyDocument_PropagatesErrors(t *testing.T) {
	getPolicyFake := &fakeIAMClient{getPolicyErr: errors.New("boom")}
	if _, err := fetchAttachedPolicyDocument(context.Background(), getPolicyFake, "arn"); err == nil {
		t.Error("expected GetPolicy's error to propagate")
	}
	getVersionFake := &fakeIAMClient{
		policiesByArn:       map[string]iamtypes.Policy{"arn": {DefaultVersionId: aws.String("v1")}},
		getPolicyVersionErr: errors.New("boom"),
	}
	if _, err := fetchAttachedPolicyDocument(context.Background(), getVersionFake, "arn"); err == nil {
		t.Error("expected GetPolicyVersion's error to propagate")
	}
}

func TestFetchInlinePolicyDocument_ReturnsDecodedDocument(t *testing.T) {
	fake := &fakeIAMClient{
		rolePolicyDocuments: map[string]string{
			"air-sampling/AirSamplingWriteAccess": "%7B%22Version%22%3A%222012-10-17%22%7D",
		},
	}
	got, err := fetchInlinePolicyDocument(context.Background(), fake, "air-sampling", "AirSamplingWriteAccess")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "\"Version\": \"2012-10-17\"") {
		t.Errorf("got %q, want decoded+pretty-printed JSON", got)
	}
}

func TestFetchInlinePolicyDocument_PropagatesError(t *testing.T) {
	fake := &fakeIAMClient{getRolePolicyErr: errors.New("boom")}
	_, err := fetchInlinePolicyDocument(context.Background(), fake, "air-sampling", "AirSamplingWriteAccess")
	if err == nil {
		t.Fatal("expected an error to propagate")
	}
}

func awsTimePtr(t time.Time) *time.Time { return &t }

func TestFetchIAMInstanceProfileDetail_AssemblesEverything(t *testing.T) {
	fake := &fakeIAMClient{
		getInstanceProfileOut: &iam.GetInstanceProfileOutput{
			InstanceProfile: &iamtypes.InstanceProfile{
				InstanceProfileName: aws.String("air-sampling-profile"),
				CreateDate:          awsTimePtr(time.Date(2020, 2, 5, 22, 28, 0, 0, time.UTC)),
				Tags:                []iamtypes.Tag{{Key: aws.String("origin"), Value: aws.String("dld")}},
				Roles:               []iamtypes.Role{{RoleName: aws.String("air-sampling")}},
			},
		},
		attachedPolicyArns: map[string][]string{
			"air-sampling": {"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"},
		},
	}

	got, err := fetchIAMInstanceProfileDetail(context.Background(), fake, "air-sampling-profile")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "air-sampling-profile" {
		t.Errorf("Name = %q, want air-sampling-profile", got.Name)
	}
	if got.Tags["origin"] != "dld" {
		t.Errorf("Tags = %v, want origin=dld", got.Tags)
	}
	if len(got.Roles) != 1 || got.Roles[0].Name != "air-sampling" || !got.Roles[0].SSMCapable {
		t.Errorf("Roles = %+v, want [{air-sampling, SSMCapable: true}]", got.Roles)
	}
}

func TestFetchIAMInstanceProfileDetail_PropagatesGetInstanceProfileError(t *testing.T) {
	fake := &fakeIAMClient{getInstanceProfileErr: errors.New("boom")}
	_, err := fetchIAMInstanceProfileDetail(context.Background(), fake, "air-sampling-profile")
	if err == nil {
		t.Fatal("expected an error to propagate")
	}
}

func TestFetchIAMInstanceProfileDetail_PropagatesSSMCheckError(t *testing.T) {
	fake := &fakeIAMClient{
		getInstanceProfileOut: &iam.GetInstanceProfileOutput{
			InstanceProfile: &iamtypes.InstanceProfile{
				InstanceProfileName: aws.String("p"),
				Roles:               []iamtypes.Role{{RoleName: aws.String("r")}},
			},
		},
		listAttachedRolePoliciesErr: errors.New("boom"),
	}
	_, err := fetchIAMInstanceProfileDetail(context.Background(), fake, "p")
	if err == nil {
		t.Fatal("expected an error to propagate")
	}
}

func TestDisplayIAMInstanceProfileDetail_IncludesEverySection(t *testing.T) {
	var buf bytes.Buffer
	detail := IAMInstanceProfileDetail{
		Name:       "air-sampling-profile",
		CreateDate: time.Date(2020, 2, 5, 22, 28, 0, 0, time.UTC),
		Tags:       map[string]string{"origin": "dld"},
		Roles:      []IAMRoleRef{{Name: "air-sampling", SSMCapable: true}},
	}
	displayIAMInstanceProfileDetail(&buf, detail)
	out := buf.String()
	for _, want := range []string{"air-sampling-profile", "2020-02-05", "origin = dld", "air-sampling", "yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDisplayIAMInstanceProfileDetail_HandlesEmptySections(t *testing.T) {
	var buf bytes.Buffer
	displayIAMInstanceProfileDetail(&buf, IAMInstanceProfileDetail{Name: "empty-profile"})
	if !strings.Contains(buf.String(), "(none)") {
		t.Errorf("expected empty sections to show (none), got:\n%s", buf.String())
	}
}

func TestViewIAMInstanceProfileDetail_NoProfilesFound(t *testing.T) {
	var buf bytes.Buffer
	err := viewIAMInstanceProfileDetail(context.Background(), &buf, &fakeIAMClient{}, config.OriginTagConfig{Key: "Origin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No IAM instance profiles found") {
		t.Errorf("expected a no-profiles message, got:\n%s", buf.String())
	}
}

// TestViewIAMRoleDetail_NoRolesFound exercises the one path reachable
// before any Picker-tier call (pickIAMRole) -- the same accepted
// limitation as manageResourceTags (tag_management_test.go's own doc
// comment): the rest of viewIAMRoleDetail isn't driven end-to-end by an
// automated test.
func TestViewIAMRoleDetail_NoRolesFound(t *testing.T) {
	var buf bytes.Buffer
	err := viewIAMRoleDetail(context.Background(), &buf, &fakeIAMClient{}, config.OriginTagConfig{Key: "Origin"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No IAM roles found") {
		t.Errorf("expected a no-roles message, got:\n%s", buf.String())
	}
}

func TestDisplayIAMRoleDetail_IncludesEverySection(t *testing.T) {
	var buf bytes.Buffer
	detail := IAMRoleDetail{
		Name:                 "air-sampling",
		CreateDate:           time.Date(2020, 2, 5, 22, 28, 0, 0, time.UTC),
		Tags:                 map[string]string{"origin": "dld"},
		TrustPolicy:          "{\n  \"Version\": \"2012-10-17\"\n}",
		AttachedPolicies:     []IAMPolicyRef{{Name: "AWSLambdaBasicExecutionRole", ARN: "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"}},
		InlinePolicyNames:    []string{"AirSamplingWriteAccess"},
		SSMCapable:           false,
		ReferencedByProfiles: []string{"air-sampling-profile"},
	}
	displayIAMRoleDetail(&buf, detail)
	out := buf.String()
	for _, want := range []string{
		"air-sampling", "2020-02-05", "origin = dld", "2012-10-17",
		"AWSLambdaBasicExecutionRole", "AirSamplingWriteAccess", "air-sampling-profile", "no",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDisplayIAMRoleDetail_HandlesEmptySections(t *testing.T) {
	var buf bytes.Buffer
	displayIAMRoleDetail(&buf, IAMRoleDetail{Name: "empty-role"})
	if !strings.Contains(buf.String(), "(none)") {
		t.Errorf("expected empty sections to show (none), got:\n%s", buf.String())
	}
}

func TestRunPolicyDocLoop_EmptyChoicesReturnsImmediately(t *testing.T) {
	term, buf := newTermOnly()
	err := runPolicyDocLoop(context.Background(), term, &fakeIAMClient{}, IAMRoleDetail{Name: "empty-role"}, newHuhAccessibleInput(""), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// cancelingGetPolicyVersionClient/cancelingGetRolePolicyClient mirror
// cancelingGetPolicyClient (above), cancelling ctx on the success path's
// own call -- runPolicyDocLoop's loop never terminates on its own after
// a successful view (no cancellation, no "Done" sentinel; 'q' is the
// only way out, which accessible-mode input can't simulate), so these
// tests need the same explicit-cancel treatment as the error-path test.
type cancelingGetPolicyVersionClient struct {
	*fakeIAMClient
	cancel context.CancelFunc
}

func (c *cancelingGetPolicyVersionClient) GetPolicyVersion(ctx context.Context, params *iam.GetPolicyVersionInput, optFns ...func(*iam.Options)) (*iam.GetPolicyVersionOutput, error) {
	c.cancel()
	return c.fakeIAMClient.GetPolicyVersion(ctx, params, optFns...)
}

type cancelingGetRolePolicyClient struct {
	*fakeIAMClient
	cancel context.CancelFunc
}

func (c *cancelingGetRolePolicyClient) GetRolePolicy(ctx context.Context, params *iam.GetRolePolicyInput, optFns ...func(*iam.Options)) (*iam.GetRolePolicyOutput, error) {
	c.cancel()
	return c.fakeIAMClient.GetRolePolicy(ctx, params, optFns...)
}

func TestRunPolicyDocLoop_ViewsAttachedPolicyDocument(t *testing.T) {
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())
	arn := "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
	client := &cancelingGetPolicyVersionClient{
		fakeIAMClient: &fakeIAMClient{
			policiesByArn:  map[string]iamtypes.Policy{arn: {DefaultVersionId: aws.String("v1")}},
			policyVersions: map[string]string{arn + "/v1": "%7B%22Version%22%3A%222012-10-17%22%7D"},
		},
		cancel: cancel,
	}
	detail := IAMRoleDetail{
		Name:             "air-sampling",
		AttachedPolicies: []IAMPolicyRef{{Name: "AWSLambdaBasicExecutionRole", ARN: arn}},
	}

	err := runPolicyDocLoop(ctx, term, client, detail, newHuhAccessibleInput("1\n\n"), buf)
	if err != nil {
		t.Fatalf("expected a clean exit once ctx is cancelled, got: %v", err)
	}
	if !strings.Contains(buf.String(), "\"Version\": \"2012-10-17\"") {
		t.Errorf("expected the decoded policy document to be shown, got:\n%s", buf.String())
	}
}

func TestRunPolicyDocLoop_ViewsInlinePolicyDocument(t *testing.T) {
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())
	client := &cancelingGetRolePolicyClient{
		fakeIAMClient: &fakeIAMClient{
			rolePolicyDocuments: map[string]string{
				"air-sampling/AirSamplingWriteAccess": "%7B%22Version%22%3A%222012-10-17%22%7D",
			},
		},
		cancel: cancel,
	}
	detail := IAMRoleDetail{
		Name:              "air-sampling",
		InlinePolicyNames: []string{"AirSamplingWriteAccess"},
	}

	err := runPolicyDocLoop(ctx, term, client, detail, newHuhAccessibleInput("1\n\n"), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "\"Version\": \"2012-10-17\"") {
		t.Errorf("expected the decoded inline policy document to be shown, got:\n%s", buf.String())
	}
}

// cancelingGetPolicyClient wraps *fakeIAMClient, cancelling ctx the
// moment GetPolicy is called -- huh's accessible-mode Select has no way
// to signal "input exhausted" as an error (confirmed by reading
// internal/accessibility.PromptString, per manageTagsForResource's own
// documented gotcha): on EOF it silently re-picks the default option
// forever, so a looping accessible-mode workflow's test must cancel ctx
// explicitly rather than let scripted input run out, exactly like
// domain_menu_test.go's cancelingAction.
type cancelingGetPolicyClient struct {
	*fakeIAMClient
	cancel context.CancelFunc
}

func (c *cancelingGetPolicyClient) GetPolicy(ctx context.Context, params *iam.GetPolicyInput, optFns ...func(*iam.Options)) (*iam.GetPolicyOutput, error) {
	c.cancel()
	return c.fakeIAMClient.GetPolicy(ctx, params, optFns...)
}

func TestRunPolicyDocLoop_ShowsErrorAndContinuesLoop(t *testing.T) {
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())
	client := &cancelingGetPolicyClient{
		fakeIAMClient: &fakeIAMClient{getPolicyErr: errors.New("boom")},
		cancel:        cancel,
	}
	detail := IAMRoleDetail{
		Name:             "air-sampling",
		AttachedPolicies: []IAMPolicyRef{{Name: "SomePolicy", ARN: "arn:aws:iam::aws:policy/SomePolicy"}},
	}

	// Pick the policy (errors, and cancels ctx as a side effect), pause
	// consumes a blank line, then the loop's next ctx.Err() check ends
	// it cleanly.
	err := runPolicyDocLoop(ctx, term, client, detail, newHuhAccessibleInput("1\n\n"), buf)
	if err != nil {
		t.Fatalf("expected a clean exit once ctx is cancelled, got: %v", err)
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("expected the error to be shown, got:\n%s", buf.String())
	}
}

func TestDecodePolicyDocument_FallsBackToRawTextWhenNotValidJSON(t *testing.T) {
	// Decodes fine, but isn't valid JSON after decoding -- fail loud isn't
	// appropriate here since the raw text is still useful to show;
	// falls back to the decoded (not pretty-printed) text instead of
	// erroring the whole detail view over a cosmetic formatting step.
	got, err := decodePolicyDocument("not%20json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "not json" {
		t.Errorf("got %q, want the plain decoded text %q", got, "not json")
	}
}

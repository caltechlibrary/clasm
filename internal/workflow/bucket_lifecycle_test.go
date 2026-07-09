package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
)

func TestManageBucketLifecyclePolicies_NoBucketsFound(t *testing.T) {
	term, le, buf := newPipeEditor(t, "")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return nil, nil }

	if err := ManageBucketLifecyclePolicies(context.Background(), term, le, newClient, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No buckets found") {
		t.Errorf("expected a no-buckets message, got:\n%s", buf.String())
	}
}

func TestManageBucketLifecyclePolicies_NoSuchLifecycleConfigurationIsNotAnError(t *testing.T) {
	fake := &fakeS3Client{getBucketLifecycleErr: awsAPIError("NoSuchLifecycleConfiguration")}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2", Purpose: "internal"}}
	input := "1\n" + "5\n" // pick bucket, Back

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := ManageBucketLifecyclePolicies(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No lifecycle rules configured") {
		t.Errorf("expected NoSuchLifecycleConfiguration to be treated as zero rules, got:\n%s", buf.String())
	}
}

func TestManageBucketLifecyclePolicies_BackupPurposeAddGuidedFlow(t *testing.T) {
	fake := &fakeS3Client{}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2", Purpose: "backup"}}
	input := "1\n" + // pick bucket
		"1\n" + // Add rule
		"90\n" + // expire after 90 days
		"30\n" + // transition after 30 days (before the expiration)
		"1\n" + // storage class: Standard-IA (first of the curated 4)
		"\n" + // prefix blank
		"y\n" // confirm

	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := ManageBucketLifecyclePolicies(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fake.putBucketLifecycleCalls) != 1 {
		t.Fatalf("putBucketLifecycleCalls = %d, want 1", len(fake.putBucketLifecycleCalls))
	}
	rules := fake.putBucketLifecycleCalls[0].LifecycleConfiguration.Rules
	if len(rules) != 1 {
		t.Fatalf("rules = %+v, want 1", rules)
	}
	r := rules[0]
	if aws.ToInt32(r.Expiration.Days) != 90 {
		t.Errorf("expiration days = %d, want 90", aws.ToInt32(r.Expiration.Days))
	}
	if len(r.Transitions) != 1 || aws.ToInt32(r.Transitions[0].Days) != 30 || r.Transitions[0].StorageClass != types.TransitionStorageClassStandardIa {
		t.Errorf("transitions = %+v, want one 30-day transition to Standard-IA", r.Transitions)
	}
}

func TestManageBucketLifecyclePolicies_BackupGuidedRejectsTransitionNotBeforeExpiration(t *testing.T) {
	fake := &fakeS3Client{}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2", Purpose: "backup"}}
	input := "1\n" + // pick bucket
		"1\n" + // Add rule
		"30\n" + // expire after 30 days
		"30\n" + // transition after 30 days -- rejected, not before expiration
		"90\n" + // rejected too -- still not before expiration
		"10\n" + // transition after 10 days -- accepted
		"1\n" + // storage class: Standard-IA
		"\n" + // prefix blank
		"y\n" // confirm

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := ManageBucketLifecyclePolicies(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "must be less than the expiration") {
		t.Errorf("expected a local rejection message, got:\n%s", buf.String())
	}

	if len(fake.putBucketLifecycleCalls) != 1 {
		t.Fatalf("putBucketLifecycleCalls = %d, want 1", len(fake.putBucketLifecycleCalls))
	}
	r := fake.putBucketLifecycleCalls[0].LifecycleConfiguration.Rules[0]
	if aws.ToInt32(r.Expiration.Days) != 30 {
		t.Errorf("expiration days = %d, want 30", aws.ToInt32(r.Expiration.Days))
	}
	if len(r.Transitions) != 1 || aws.ToInt32(r.Transitions[0].Days) != 10 {
		t.Errorf("transitions = %+v, want one 10-day transition", r.Transitions)
	}
}

func TestManageBucketLifecyclePolicies_BackupAddWithNoActionsIsNothingToDo(t *testing.T) {
	fake := &fakeS3Client{}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2", Purpose: "backup"}}
	input := "1\n" + "1\n" + "\n" + "\n" // pick bucket, Add rule, blank expire, blank transition

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := ManageBucketLifecyclePolicies(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "nothing to do") {
		t.Errorf("expected a nothing-to-do message, got:\n%s", buf.String())
	}
	if len(fake.putBucketLifecycleCalls) != 0 {
		t.Errorf("putBucketLifecycleCalls = %d, want 0", len(fake.putBucketLifecycleCalls))
	}
}

func TestManageBucketLifecyclePolicies_GenericPurposeAddNamedRule(t *testing.T) {
	fake := &fakeS3Client{}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2", Purpose: "internal"}}
	firstStorageClass := types.TransitionStorageClass("").Values()[0]
	input := "1\n" + // pick bucket
		"1\n" + // Add rule
		"my-rule\n" + // rule ID
		"\n" + // prefix blank
		"y\n" + // add a transition
		"60\n" + // transition days
		"1\n" + // storage class (first of the full enum)
		"n\n" + // no more transitions
		"\n" + // expiration blank
		"y\n" // confirm

	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := ManageBucketLifecyclePolicies(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fake.putBucketLifecycleCalls) != 1 {
		t.Fatalf("putBucketLifecycleCalls = %d, want 1", len(fake.putBucketLifecycleCalls))
	}
	rules := fake.putBucketLifecycleCalls[0].LifecycleConfiguration.Rules
	if len(rules) != 1 || aws.ToString(rules[0].ID) != "my-rule" {
		t.Fatalf("rules = %+v, want one rule named my-rule", rules)
	}
	if len(rules[0].Transitions) != 1 || aws.ToInt32(rules[0].Transitions[0].Days) != 60 || rules[0].Transitions[0].StorageClass != firstStorageClass {
		t.Errorf("transitions = %+v, want one 60-day transition to %s", rules[0].Transitions, firstStorageClass)
	}
	if rules[0].Expiration != nil {
		t.Errorf("expiration = %+v, want nil (left blank)", rules[0].Expiration)
	}
}

func TestManageBucketLifecyclePolicies_GenericAddRejectsExpirationNotAfterLatestTransition(t *testing.T) {
	fake := &fakeS3Client{}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2", Purpose: "internal"}}
	firstStorageClass := types.TransitionStorageClass("").Values()[0]
	input := "1\n" + // pick bucket
		"1\n" + // Add rule
		"my-rule\n" + // rule ID
		"\n" + // prefix blank
		"y\n" + // add a transition
		"60\n" + // transition days
		"1\n" + // storage class (first of the full enum)
		"n\n" + // no more transitions
		"60\n" + // expire after 60 days -- rejected, not after the transition
		"30\n" + // rejected too -- before the transition
		"90\n" + // expire after 90 days -- accepted
		"y\n" // confirm

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := ManageBucketLifecyclePolicies(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "must be greater than the latest transition") {
		t.Errorf("expected a local rejection message, got:\n%s", buf.String())
	}

	if len(fake.putBucketLifecycleCalls) != 1 {
		t.Fatalf("putBucketLifecycleCalls = %d, want 1", len(fake.putBucketLifecycleCalls))
	}
	rules := fake.putBucketLifecycleCalls[0].LifecycleConfiguration.Rules
	if len(rules) != 1 || aws.ToString(rules[0].ID) != "my-rule" {
		t.Fatalf("rules = %+v, want one rule named my-rule", rules)
	}
	if len(rules[0].Transitions) != 1 || aws.ToInt32(rules[0].Transitions[0].Days) != 60 || rules[0].Transitions[0].StorageClass != firstStorageClass {
		t.Errorf("transitions = %+v, want one 60-day transition to %s", rules[0].Transitions, firstStorageClass)
	}
	if aws.ToInt32(rules[0].Expiration.Days) != 90 {
		t.Errorf("expiration days = %d, want 90", aws.ToInt32(rules[0].Expiration.Days))
	}
}

func TestManageBucketLifecyclePolicies_GenericAddRejectsDuplicateID(t *testing.T) {
	fake := &fakeS3Client{lifecycleRules: []types.LifecycleRule{
		{ID: aws.String("existing"), Status: types.ExpirationStatusEnabled, Expiration: &types.LifecycleExpiration{Days: aws.Int32(10)}},
	}}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2", Purpose: "internal"}}
	input := "1\n" + "1\n" +
		"existing\n" + // rejected -- duplicate
		"new-rule\n" +
		"\n" + // prefix blank
		"n\n" + // no transitions
		"5\n" + // expire after 5 days
		"y\n"

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := ManageBucketLifecyclePolicies(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "already exists") {
		t.Errorf("expected the duplicate ID to be rejected locally, got:\n%s", buf.String())
	}
	rules := fake.putBucketLifecycleCalls[0].LifecycleConfiguration.Rules
	if len(rules) != 2 {
		t.Fatalf("rules = %+v, want the existing rule plus the new one", rules)
	}
}

func TestManageBucketLifecyclePolicies_EditRuleUpdatesInPlace(t *testing.T) {
	fake := &fakeS3Client{lifecycleRules: []types.LifecycleRule{
		{ID: aws.String("r1"), Status: types.ExpirationStatusEnabled, Expiration: &types.LifecycleExpiration{Days: aws.Int32(10)}},
	}}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2", Purpose: "internal"}}
	input := "1\n" + // pick bucket
		"2\n" + // Edit rule
		"1\n" + // pick r1
		"\n" + // prefix: keep blank
		"n\n" + // no transitions
		"20\n" + // change expiration from 10 to 20
		"y\n"

	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := ManageBucketLifecyclePolicies(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rules := fake.putBucketLifecycleCalls[0].LifecycleConfiguration.Rules
	if len(rules) != 1 || aws.ToString(rules[0].ID) != "r1" || aws.ToInt32(rules[0].Expiration.Days) != 20 {
		t.Fatalf("rules = %+v, want r1 with Expiration.Days=20", rules)
	}
}

func TestManageBucketLifecyclePolicies_RemoveRuleConfirmed(t *testing.T) {
	fake := &fakeS3Client{lifecycleRules: []types.LifecycleRule{
		{ID: aws.String("r1"), Status: types.ExpirationStatusEnabled},
		{ID: aws.String("r2"), Status: types.ExpirationStatusEnabled},
	}}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2", Purpose: "internal"}}
	input := "1\n" + "3\n" + "1\n" + "y\n" // pick bucket, Remove rule, pick r1, confirm

	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := ManageBucketLifecyclePolicies(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rules := fake.putBucketLifecycleCalls[0].LifecycleConfiguration.Rules
	if len(rules) != 1 || aws.ToString(rules[0].ID) != "r2" {
		t.Fatalf("rules = %+v, want only r2 remaining", rules)
	}
}

func TestManageBucketLifecyclePolicies_RemovingLastRuleCallsDeleteNotPutWithEmptyRules(t *testing.T) {
	fake := &fakeS3Client{lifecycleRules: []types.LifecycleRule{
		{ID: aws.String("only-rule"), Status: types.ExpirationStatusEnabled},
	}}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2", Purpose: "internal"}}
	input := "1\n" + "3\n" + "1\n" + "y\n" // pick bucket, Remove rule, pick only-rule, confirm

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := ManageBucketLifecyclePolicies(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(buf.String(), "Error:") {
		t.Errorf("expected no error, got:\n%s", buf.String())
	}
	if len(fake.putBucketLifecycleCalls) != 0 {
		t.Errorf("putBucketLifecycleCalls = %d, want 0 -- PutBucketLifecycleConfiguration rejects an empty Rules list", len(fake.putBucketLifecycleCalls))
	}
	if len(fake.deleteBucketLifecycleCalls) != 1 || aws.ToString(fake.deleteBucketLifecycleCalls[0].Bucket) != "my-bucket" {
		t.Fatalf("deleteBucketLifecycleCalls = %+v, want one call for my-bucket", fake.deleteBucketLifecycleCalls)
	}
}

func TestManageBucketLifecyclePolicies_RemoveRuleDeclined(t *testing.T) {
	fake := &fakeS3Client{lifecycleRules: []types.LifecycleRule{
		{ID: aws.String("r1"), Status: types.ExpirationStatusEnabled},
	}}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2", Purpose: "internal"}}
	input := "1\n" + "3\n" + "1\n" + "n\n"

	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := ManageBucketLifecyclePolicies(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.putBucketLifecycleCalls) != 0 {
		t.Errorf("putBucketLifecycleCalls = %d, want 0 after declining", len(fake.putBucketLifecycleCalls))
	}
}

func TestManageBucketLifecyclePolicies_EditWithNoRulesReportsAndSkipsPut(t *testing.T) {
	fake := &fakeS3Client{}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2", Purpose: "internal"}}
	input := "1\n" + "2\n" // pick bucket, Edit rule (none exist)

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := ManageBucketLifecyclePolicies(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No rules to edit") {
		t.Errorf("expected a no-rules-to-edit message, got:\n%s", buf.String())
	}
	if len(fake.putBucketLifecycleCalls) != 0 {
		t.Errorf("putBucketLifecycleCalls = %d, want 0", len(fake.putBucketLifecycleCalls))
	}
}

func TestManageBucketLifecyclePolicies_BackActionSkipsPut(t *testing.T) {
	fake := &fakeS3Client{lifecycleRules: []types.LifecycleRule{{ID: aws.String("r1"), Status: types.ExpirationStatusEnabled}}}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2", Purpose: "internal"}}
	input := "1\n" + "5\n"

	term, le, _ := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := ManageBucketLifecyclePolicies(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.putBucketLifecycleCalls) != 0 {
		t.Errorf("putBucketLifecycleCalls = %d, want 0", len(fake.putBucketLifecycleCalls))
	}
}

func TestManageBucketLifecyclePolicies_ViewRuleDetailShowsFullConfigWithoutEditing(t *testing.T) {
	fake := &fakeS3Client{lifecycleRules: []types.LifecycleRule{
		{
			ID:          aws.String("r1"),
			Status:      types.ExpirationStatusEnabled,
			Filter:      &types.LifecycleRuleFilter{Prefix: aws.String("logs/")},
			Expiration:  &types.LifecycleExpiration{Days: aws.Int32(30)},
			Transitions: []types.Transition{{Days: aws.Int32(10), StorageClass: types.TransitionStorageClassGlacier}},
		},
	}}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2", Purpose: "internal"}}
	// pick bucket, View rule details, pick r1 (viewing loops back to the
	// action menu instead of exiting), View again, pick r1 again, Back.
	input := "1\n" + "4\n" + "1\n" + "4\n" + "1\n" + "5\n"

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := ManageBucketLifecyclePolicies(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"Rule r1", "logs/", "Expires after 30 day(s)", "10 day(s) -> Glacier Flexible Retrieval"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
	if strings.Count(out, "Rule r1") < 2 {
		t.Errorf("expected the rule detail to be shown twice (once per View), got:\n%s", out)
	}
	if len(fake.putBucketLifecycleCalls) != 0 {
		t.Errorf("putBucketLifecycleCalls = %d, want 0 -- viewing must not write anything", len(fake.putBucketLifecycleCalls))
	}
}

func TestManageBucketLifecyclePolicies_ViewRuleDetailWithNoRules(t *testing.T) {
	fake := &fakeS3Client{}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2", Purpose: "internal"}}
	input := "1\n" + "4\n" + "5\n" // pick bucket, View rule details (none exist), Back

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := ManageBucketLifecyclePolicies(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No rules to view") {
		t.Errorf("expected a no-rules-to-view message, got:\n%s", buf.String())
	}
}

func TestManageBucketLifecyclePolicies_CancellationAtBucketPick(t *testing.T) {
	fake := &fakeS3Client{}
	buckets := []inventory.Bucket{{Name: "my-bucket", Region: "us-west-2", Purpose: "internal"}}
	term, le, _ := newPipeEditor(t, "0\n")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	if err := ManageBucketLifecyclePolicies(context.Background(), term, le, newClient, buckets); err != nil {
		t.Fatalf("expected a clean cancellation (nil error), got: %v", err)
	}
}

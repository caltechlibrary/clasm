package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// Bucket selection (PLAN.md Phase 20.4) now runs a real bubbletea
// Program (tui.RunPicker), which can't be driven by a test's pipe
// input -- see internal/tui/picker_test.go for that component's own
// thorough test suite. Tests below exercise everything once a bucket
// is already resolved via the unexported manageBucketLifecyclePolicies;
// ManageBucketLifecyclePolicies's own picker-selection step is covered
// only by manual/interactive verification, the same accepted limitation
// object_browser.go's huh-based bucket pre-flight already has.
//
// The lifecycle action menu itself (Add/Edit/Remove/View rule details)
// converted to huh.Select in Phase 20.9 -- its selections are fed via a
// separate newHuhAccessibleInput reader (actionMenuInput), not le, which
// still feeds every other prompt in this function (rule/storage-class
// PickLists, confirms, day-count/ID input) unaffected by that phase.
// "Back" no longer exists as a menu item ('q' replaces it, untestable in
// accessible mode -- see mapMenuPickerErr's doc comment for the same
// limitation), so tests that used to select Back to end the loop now
// end it via a real action that returns directly (most Add/Edit/Remove
// paths already do) instead.

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
	bucket := inventory.Bucket{Name: "my-bucket", Region: "us-west-2", Purpose: "internal"}

	term, le, buf := newPipeEditor(t, "")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	// Edit rule -- with zero rules (NoSuchLifecycleConfiguration), this
	// returns immediately ("No rules to edit."), a clean way to end the
	// loop without needing le input at all.
	actionInput := newHuhAccessibleInput("2\n")
	if err := manageBucketLifecyclePolicies(context.Background(), term, le, newClient, bucket, actionInput, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No lifecycle rules configured") {
		t.Errorf("expected NoSuchLifecycleConfiguration to be treated as zero rules, got:\n%s", buf.String())
	}
}

func TestManageBucketLifecyclePolicies_BackupPurposeAddGuidedFlow(t *testing.T) {
	fake := &fakeS3Client{}
	bucket := inventory.Bucket{Name: "my-bucket", Region: "us-west-2", Purpose: "backup"}
	input := "90\n" + // expire after 90 days
		"30\n" + // transition after 30 days (before the expiration)
		"\n" + // prefix blank
		"y\n" // confirm

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	actionInput := newHuhAccessibleInput("1\n1\n") // Add rule, storage class: Standard-IA (first of the curated 4)
	if err := manageBucketLifecyclePolicies(context.Background(), term, le, newClient, bucket, actionInput, buf); err != nil {
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
	bucket := inventory.Bucket{Name: "my-bucket", Region: "us-west-2", Purpose: "backup"}
	input := "30\n" + // expire after 30 days
		"30\n" + // transition after 30 days -- rejected, not before expiration
		"90\n" + // rejected too -- still not before expiration
		"10\n" + // transition after 10 days -- accepted
		"\n" + // prefix blank
		"y\n" // confirm

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	actionInput := newHuhAccessibleInput("1\n1\n") // Add rule, storage class: Standard-IA
	if err := manageBucketLifecyclePolicies(context.Background(), term, le, newClient, bucket, actionInput, buf); err != nil {
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
	bucket := inventory.Bucket{Name: "my-bucket", Region: "us-west-2", Purpose: "backup"}
	input := "\n" + "\n" // blank expire, blank transition

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	actionInput := newHuhAccessibleInput("1\n") // Add rule
	if err := manageBucketLifecyclePolicies(context.Background(), term, le, newClient, bucket, actionInput, buf); err != nil {
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
	bucket := inventory.Bucket{Name: "my-bucket", Region: "us-west-2", Purpose: "internal"}
	firstStorageClass := types.TransitionStorageClass("").Values()[0]
	input := "my-rule\n" + // rule ID
		"\n" + // prefix blank
		"y\n" + // add a transition
		"60\n" + // transition days
		"n\n" + // no more transitions
		"\n" + // expiration blank
		"y\n" // confirm

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	actionInput := newHuhAccessibleInput("1\n1\n") // Add rule, storage class (first of the full enum)
	if err := manageBucketLifecyclePolicies(context.Background(), term, le, newClient, bucket, actionInput, buf); err != nil {
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
	bucket := inventory.Bucket{Name: "my-bucket", Region: "us-west-2", Purpose: "internal"}
	firstStorageClass := types.TransitionStorageClass("").Values()[0]
	input := "my-rule\n" + // rule ID
		"\n" + // prefix blank
		"y\n" + // add a transition
		"60\n" + // transition days
		"n\n" + // no more transitions
		"60\n" + // expire after 60 days -- rejected, not after the transition
		"30\n" + // rejected too -- before the transition
		"90\n" + // expire after 90 days -- accepted
		"y\n" // confirm

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	actionInput := newHuhAccessibleInput("1\n1\n") // Add rule, storage class (first of the full enum)
	if err := manageBucketLifecyclePolicies(context.Background(), term, le, newClient, bucket, actionInput, buf); err != nil {
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
	bucket := inventory.Bucket{Name: "my-bucket", Region: "us-west-2", Purpose: "internal"}
	input := "existing\n" + // rejected -- duplicate
		"new-rule\n" +
		"\n" + // prefix blank
		"n\n" + // no transitions
		"5\n" + // expire after 5 days
		"y\n"

	term, le, buf := newPipeEditor(t, input)
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	actionInput := newHuhAccessibleInput("1\n") // Add rule
	if err := manageBucketLifecyclePolicies(context.Background(), term, le, newClient, bucket, actionInput, buf); err != nil {
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

// Lifecycle rule selection (view/edit/remove) converted to tui.RunPicker
// (DESIGN.md's full conversion punch list, Picker tier): a real
// bubbletea Program that can't be pipe-tested, so tests below that
// exercise Edit/Remove call editLifecycleRuleForRule/removeLifecycleRule
// ForRule directly with an already-resolved rule, instead of driving the
// whole manageBucketLifecyclePolicies loop (which would otherwise reach
// the picker). manageBucketLifecyclePolicies' own rule-selection step is
// covered only by manual/interactive verification, the same accepted
// limitation this session's other Picker-tier conversions already have.

func TestEditLifecycleRuleForRule_UpdatesInPlace(t *testing.T) {
	existing := types.LifecycleRule{ID: aws.String("r1"), Status: types.ExpirationStatusEnabled, Expiration: &types.LifecycleExpiration{Days: aws.Int32(10)}}
	rules := []types.LifecycleRule{existing}
	input := "\n" + // prefix: keep blank
		"n\n" + // no transitions
		"20\n" + // change expiration from 10 to 20
		"y\n"

	term, le, _ := newPipeEditor(t, input)

	newRules, proceed, err := editLifecycleRuleForRule(term, le, "internal", rules, existing, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !proceed {
		t.Fatal("expected proceed = true")
	}
	if len(newRules) != 1 || aws.ToString(newRules[0].ID) != "r1" || aws.ToInt32(newRules[0].Expiration.Days) != 20 {
		t.Fatalf("rules = %+v, want r1 with Expiration.Days=20", newRules)
	}
}

func TestRemoveLifecycleRuleForRule_Confirmed(t *testing.T) {
	r1 := types.LifecycleRule{ID: aws.String("r1"), Status: types.ExpirationStatusEnabled}
	r2 := types.LifecycleRule{ID: aws.String("r2"), Status: types.ExpirationStatusEnabled}
	rules := []types.LifecycleRule{r1, r2}

	term, le, _ := newPipeEditor(t, "y\n") // confirm

	newRules, proceed, err := removeLifecycleRuleForRule(term, le, rules, r1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !proceed {
		t.Fatal("expected proceed = true")
	}
	if len(newRules) != 1 || aws.ToString(newRules[0].ID) != "r2" {
		t.Fatalf("rules = %+v, want only r2 remaining", newRules)
	}
}

func TestRemoveLifecycleRuleForRule_RemovingLastRuleLeavesRulesEmpty(t *testing.T) {
	only := types.LifecycleRule{ID: aws.String("only-rule"), Status: types.ExpirationStatusEnabled}
	term, le, _ := newPipeEditor(t, "y\n") // confirm

	newRules, proceed, err := removeLifecycleRuleForRule(term, le, []types.LifecycleRule{only}, only)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !proceed || len(newRules) != 0 {
		t.Fatalf("newRules = %+v proceed = %v, want empty/true", newRules, proceed)
	}
	// PutBucketLifecycleConfiguration rejects an empty Rules list
	// client-side -- manageBucketLifecyclePolicies routes an empty result
	// to DeleteBucketLifecycle instead (see DeleteBucketLifecycle in
	// manageBucketLifecyclePolicies's own body); that routing itself, and
	// reaching this scenario end-to-end, now requires the rule picker
	// (Picker tier, tui.RunPicker) and is exercised only via manual/
	// interactive verification.
}

func TestRemoveLifecycleRuleForRule_Declined(t *testing.T) {
	r1 := types.LifecycleRule{ID: aws.String("r1"), Status: types.ExpirationStatusEnabled}
	rules := []types.LifecycleRule{r1}

	term, le, _ := newPipeEditor(t, "n\n") // decline

	_, proceed, err := removeLifecycleRuleForRule(term, le, rules, r1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proceed {
		t.Error("expected proceed = false after declining")
	}
}

func TestManageBucketLifecyclePolicies_EditWithNoRulesReportsAndSkipsPut(t *testing.T) {
	fake := &fakeS3Client{}
	bucket := inventory.Bucket{Name: "my-bucket", Region: "us-west-2", Purpose: "internal"}

	term, le, buf := newPipeEditor(t, "")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	actionInput := newHuhAccessibleInput("2\n") // Edit rule (none exist)
	if err := manageBucketLifecyclePolicies(context.Background(), term, le, newClient, bucket, actionInput, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No rules to edit") {
		t.Errorf("expected a no-rules-to-edit message, got:\n%s", buf.String())
	}
	if len(fake.putBucketLifecycleCalls) != 0 {
		t.Errorf("putBucketLifecycleCalls = %d, want 0", len(fake.putBucketLifecycleCalls))
	}
}

func TestPrintLifecycleRuleDetail_ShowsFullConfig(t *testing.T) {
	// Rule selection for "View rule details" converted to tui.RunPicker
	// (Picker tier) -- a real bubbletea Program that can't be
	// pipe-tested, so this tests printLifecycleRuleDetail (the display
	// logic once a rule is resolved) directly instead of driving the
	// whole manageBucketLifecyclePolicies loop through the picker.
	r := types.LifecycleRule{
		ID:          aws.String("r1"),
		Status:      types.ExpirationStatusEnabled,
		Filter:      &types.LifecycleRuleFilter{Prefix: aws.String("logs/")},
		Expiration:  &types.LifecycleExpiration{Days: aws.Int32(30)},
		Transitions: []types.Transition{{Days: aws.Int32(10), StorageClass: types.TransitionStorageClassGlacier}},
	}
	term, _, buf := newPipeEditor(t, "")

	printLifecycleRuleDetail(term, r)

	out := buf.String()
	for _, want := range []string{"Rule r1", "logs/", "Expires after 30 day(s)", "10 day(s) -> Glacier Flexible Retrieval"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestManageBucketLifecyclePolicies_ViewRuleDetailWithNoRules(t *testing.T) {
	fake := &fakeS3Client{}
	bucket := inventory.Bucket{Name: "my-bucket", Region: "us-west-2", Purpose: "internal"}

	term, le, buf := newPipeEditor(t, "")
	newClient := func(ctx context.Context, region string) (awsclient.S3API, error) { return fake, nil }

	// View rule details (none exist, prints "No rules to view.", loops
	// back), then Edit rule -- also no rules, returns immediately, a
	// clean way to end the loop.
	actionInput := newHuhAccessibleInput("4\n" + "2\n")
	if err := manageBucketLifecyclePolicies(context.Background(), term, le, newClient, bucket, actionInput, buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No rules to view") {
		t.Errorf("expected a no-rules-to-view message, got:\n%s", buf.String())
	}
}

package workflow

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
	"github.com/caltechlibrary/awstools/internal/ui"
)

// errNothingToDo signals that a rule's prompts collected no actual action
// (no expiration, no transition) -- already reported to the operator, not
// a real error, and not a reason to call AWS.
var errNothingToDo = errors.New("no lifecycle action specified")

// backupStorageClasses is the curated 4-value list offered by the guided
// "backup" purpose flow (DESIGN.md, Feature 21.1); the generic editor
// offers the full types.TransitionStorageClass enum instead.
var backupStorageClasses = []types.TransitionStorageClass{
	types.TransitionStorageClassStandardIa,
	types.TransitionStorageClassIntelligentTiering,
	types.TransitionStorageClassGlacier,
	types.TransitionStorageClassDeepArchive,
}

func storageClassLabel(sc types.TransitionStorageClass) string {
	switch sc {
	case types.TransitionStorageClassStandardIa:
		return "Standard-IA"
	case types.TransitionStorageClassIntelligentTiering:
		return "Intelligent-Tiering"
	case types.TransitionStorageClassGlacier:
		return "Glacier Flexible Retrieval"
	case types.TransitionStorageClassDeepArchive:
		return "Glacier Deep Archive"
	default:
		return string(sc)
	}
}

var lifecycleActions = []string{"Add rule", "Edit rule", "Remove rule", "View rule details", "Back"}

func lifecycleActionLabel(s string) string { return s }

func lifecycleRuleLabel(r types.LifecycleRule) string {
	prefix := "(whole bucket)"
	if r.Filter != nil && aws.ToString(r.Filter.Prefix) != "" {
		prefix = aws.ToString(r.Filter.Prefix)
	}
	return fmt.Sprintf("%s [%s]", aws.ToString(r.ID), prefix)
}

func isS3ErrorCode(err error, code string) bool {
	apiErr, ok := errors.AsType[smithy.APIError](err)
	return ok && apiErr.ErrorCode() == code
}

// getLifecycleRules fetches a bucket's current lifecycle rules,
// treating NoSuchLifecycleConfiguration as "no rules yet" rather than an
// error (DESIGN.md, Feature 21.1).
func getLifecycleRules(ctx context.Context, client awsclient.S3API, bucket string) ([]types.LifecycleRule, error) {
	out, err := client.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{Bucket: aws.String(bucket)})
	if err != nil {
		if isS3ErrorCode(err, "NoSuchLifecycleConfiguration") {
			return nil, nil
		}
		return nil, err
	}
	return out.Rules, nil
}

func displayLifecycleRules(t *termlib.Terminal, rules []types.LifecycleRule) {
	if len(rules) == 0 {
		t.Println("No lifecycle rules configured.")
		t.Refresh()
		return
	}
	t.Println("Current lifecycle rules:")
	for _, r := range rules {
		t.Printf("  %s\n", lifecycleRuleLabel(r))
	}
	t.Refresh()
}

// viewLifecycleRuleDetail lets the operator pick one existing rule and
// print its full configuration -- status, filter, expiration, and every
// transition -- without entering the Add/Edit prompts. Added after manual
// testing found the terse ID+prefix line displayLifecycleRules already
// prints isn't enough to check a rule's actual expiration/transition
// schedule at a glance.
func viewLifecycleRuleDetail(t *termlib.Terminal, le *termlib.LineEditor, rules []types.LifecycleRule) error {
	if len(rules) == 0 {
		t.Println("No rules to view.")
		t.Refresh()
		return nil
	}
	rule, err := ui.PickList(t, le, rules, lifecycleRuleLabel, "Select a rule to view")
	if err != nil {
		return err
	}
	printLifecycleRuleDetail(t, rule)
	return nil
}

func printLifecycleRuleDetail(t *termlib.Terminal, r types.LifecycleRule) {
	prefix := "(whole bucket)"
	if r.Filter != nil && aws.ToString(r.Filter.Prefix) != "" {
		prefix = aws.ToString(r.Filter.Prefix)
	}
	t.Printf("Rule %s\n", aws.ToString(r.ID))
	t.Printf("  Status: %s\n", r.Status)
	t.Printf("  Applies to: %s\n", prefix)
	if r.Expiration != nil && r.Expiration.Days != nil {
		t.Printf("  Expires after %d day(s)\n", aws.ToInt32(r.Expiration.Days))
	} else {
		t.Println("  No expiration set")
	}
	if len(r.Transitions) == 0 {
		t.Println("  No transitions set")
	} else {
		t.Println("  Transitions:")
		for _, tr := range r.Transitions {
			t.Printf("    %d day(s) -> %s\n", aws.ToInt32(tr.Days), storageClassLabel(tr.StorageClass))
		}
	}
	t.Refresh()
}

// confirmLifecycleChange gates every add/edit/remove with a reminder that
// AWS evaluates lifecycle rules on its own ~24-48h cadence -- this
// schedules future automated deletion/transition, not an immediate one
// (DESIGN.md Security Consideration #13).
func confirmLifecycleChange(t *termlib.Terminal, le *termlib.LineEditor, action string) (bool, error) {
	return Confirm(t, le, fmt.Sprintf("%s -- AWS applies lifecycle rule changes on its own evaluation cycle (typically 24-48 hours), not immediately. Proceed?", action))
}

// promptOptionalDays prompts for a blank-to-skip, optionally-defaulted
// day count, used for "Expire after N days?"/"Transition after N days?"
// style questions where blank means "don't set this action". Any checks
// run after the built-in positive-integer check, so a rule's transition
// and expiration days can be cross-validated against each other locally
// (see validateOrderedDays) before ever calling AWS -- PutBucketLifecycle
// Configuration rejects a transition scheduled on or after its rule's
// expiration, and previously that rejection only surfaced as AWS's raw
// error message (TODO.md, found during Phase 20's real-AWS verification).
func promptOptionalDays(t *termlib.Terminal, le *termlib.LineEditor, label, def string, checks ...func(int32) error) (int32, bool, error) {
	var days int32
	var set bool
	opts := []ui.PromptOption{ui.WithValidator(func(s string) error {
		s = strings.TrimSpace(s)
		if s == "" {
			set = false
			return nil
		}
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			return errors.New("must be blank or a positive integer")
		}
		for _, check := range checks {
			if err := check(int32(n)); err != nil {
				return err
			}
		}
		days = int32(n)
		set = true
		return nil
	})}
	if def != "" {
		opts = append(opts, ui.WithDefault(def))
	}
	if _, err := ui.Prompt(t, le, label, opts...); err != nil {
		return 0, false, err
	}
	return days, set, nil
}

// validateLessThan and validateGreaterThan return a promptOptionalDays
// check enforcing AWS's ordering constraint on a rule's transition vs.
// expiration days (a transition must fire strictly before the
// expiration) -- whichever of the two is prompted for second gets
// checked against the one already collected. limitSet is false when the
// other action was left blank, in which case there's nothing to check.
func validateLessThan(limit int32, limitSet bool, limitLabel string) func(int32) error {
	return func(n int32) error {
		if limitSet && n >= limit {
			return fmt.Errorf("must be less than %s (%d days)", limitLabel, limit)
		}
		return nil
	}
}

func validateGreaterThan(limit int32, limitSet bool, limitLabel string) func(int32) error {
	return func(n int32) error {
		if limitSet && n <= limit {
			return fmt.Errorf("must be greater than %s (%d days)", limitLabel, limit)
		}
		return nil
	}
}

// promptPositiveDays requires a positive integer -- used once the
// operator has already opted into adding a transition, unlike
// promptOptionalDays' blank-to-skip semantics.
func promptPositiveDays(t *termlib.Terminal, le *termlib.LineEditor, label string) (int32, error) {
	var days int32
	_, err := ui.Prompt(t, le, label, ui.WithValidator(func(s string) error {
		n, convErr := strconv.Atoi(strings.TrimSpace(s))
		if convErr != nil || n <= 0 {
			return errors.New("must be a positive integer")
		}
		days = int32(n)
		return nil
	}))
	return days, err
}

func backupRuleID(prefix string) string {
	if prefix == "" {
		return "awsops-backup-policy"
	}
	return "awsops-backup-policy-" + strings.ReplaceAll(strings.Trim(prefix, "/"), "/", "-")
}

// promptGuidedBackupRule runs the "backup" purpose's guided sub-flow: two
// blank-to-skip day-count questions, a curated storage-class pick when a
// transition is set, and an optional key-prefix scope. current is the
// zero value for Add, or the rule being edited (its values become each
// prompt's default) for Edit.
func promptGuidedBackupRule(t *termlib.Terminal, le *termlib.LineEditor, current types.LifecycleRule) (types.LifecycleRule, error) {
	expireDefault := ""
	if current.Expiration != nil && current.Expiration.Days != nil {
		expireDefault = strconv.Itoa(int(*current.Expiration.Days))
	}
	expireDays, expireSet, err := promptOptionalDays(t, le, "Expire objects after how many days? (blank to skip)", expireDefault)
	if err != nil {
		return types.LifecycleRule{}, err
	}

	transitionDefault := ""
	if len(current.Transitions) > 0 && current.Transitions[0].Days != nil {
		transitionDefault = strconv.Itoa(int(*current.Transitions[0].Days))
	}
	transitionDays, transitionSet, err := promptOptionalDays(t, le, "Transition to cheaper storage after how many days? (blank to skip)", transitionDefault,
		validateLessThan(expireDays, expireSet, "the expiration"))
	if err != nil {
		return types.LifecycleRule{}, err
	}

	var storageClass types.TransitionStorageClass
	if transitionSet {
		storageClass, err = ui.PickList(t, le, backupStorageClasses, storageClassLabel, "Select a storage class to transition to")
		if err != nil {
			return types.LifecycleRule{}, err
		}
	}

	if !expireSet && !transitionSet {
		t.Println("At least one of expiration or transition must be set -- nothing to do.")
		t.Refresh()
		return types.LifecycleRule{}, errNothingToDo
	}

	prefixDefault := ""
	if current.Filter != nil {
		prefixDefault = aws.ToString(current.Filter.Prefix)
	}
	var prefixOpts []ui.PromptOption
	if prefixDefault != "" {
		prefixOpts = append(prefixOpts, ui.WithDefault(prefixDefault))
	}
	prefix, err := ui.Prompt(t, le, "Key prefix (blank for whole bucket)", prefixOpts...)
	if err != nil {
		return types.LifecycleRule{}, err
	}

	id := aws.ToString(current.ID)
	if id == "" {
		id = backupRuleID(prefix)
	}

	rule := types.LifecycleRule{
		ID:     aws.String(id),
		Status: types.ExpirationStatusEnabled,
		Filter: &types.LifecycleRuleFilter{Prefix: aws.String(prefix)},
	}
	if expireSet {
		rule.Expiration = &types.LifecycleExpiration{Days: aws.Int32(expireDays)}
	}
	if transitionSet {
		rule.Transitions = []types.Transition{{Days: aws.Int32(transitionDays), StorageClass: storageClass}}
	}
	return rule, nil
}

// promptGenericRule runs the non-"backup" purposes' generic editor: a
// rule ID (Add only -- an existing rule's ID is immutable once created),
// an optional prefix, a loop collecting zero-or-more transitions from the
// full storage-class enum, and an optional expiration. current is the
// zero value for Add, or the rule being edited for Edit.
func promptGenericRule(t *termlib.Terminal, le *termlib.LineEditor, current types.LifecycleRule, existingRules []types.LifecycleRule) (types.LifecycleRule, error) {
	id := aws.ToString(current.ID)
	if id == "" {
		var err error
		id, err = ui.Prompt(t, le, "Rule ID", ui.WithValidator(func(s string) error {
			s = strings.TrimSpace(s)
			if s == "" {
				return errors.New("must not be blank")
			}
			for _, r := range existingRules {
				if aws.ToString(r.ID) == s {
					return fmt.Errorf("a rule named %q already exists", s)
				}
			}
			return nil
		}))
		if err != nil {
			return types.LifecycleRule{}, err
		}
	}

	prefixDefault := ""
	if current.Filter != nil {
		prefixDefault = aws.ToString(current.Filter.Prefix)
	}
	var prefixOpts []ui.PromptOption
	if prefixDefault != "" {
		prefixOpts = append(prefixOpts, ui.WithDefault(prefixDefault))
	}
	prefix, err := ui.Prompt(t, le, "Key prefix (blank for whole bucket)", prefixOpts...)
	if err != nil {
		return types.LifecycleRule{}, err
	}

	if len(current.Transitions) > 0 {
		t.Println("Current transitions:")
		for _, tr := range current.Transitions {
			t.Printf("  %d days -> %s\n", aws.ToInt32(tr.Days), storageClassLabel(tr.StorageClass))
		}
		t.Refresh()
	}
	var transitions []types.Transition
	for {
		addMore, err := Confirm(t, le, "Add a transition?")
		if err != nil {
			return types.LifecycleRule{}, err
		}
		if !addMore {
			break
		}
		days, err := promptPositiveDays(t, le, "Transition after how many days?")
		if err != nil {
			return types.LifecycleRule{}, err
		}
		class, err := ui.PickList(t, le, types.TransitionStorageClass("").Values(), storageClassLabel, "Select a storage class")
		if err != nil {
			return types.LifecycleRule{}, err
		}
		transitions = append(transitions, types.Transition{Days: aws.Int32(days), StorageClass: class})
	}

	var latestTransitionDays int32
	for _, tr := range transitions {
		if d := aws.ToInt32(tr.Days); d > latestTransitionDays {
			latestTransitionDays = d
		}
	}

	expireDefault := ""
	if current.Expiration != nil && current.Expiration.Days != nil {
		expireDefault = strconv.Itoa(int(*current.Expiration.Days))
	}
	expireDays, expireSet, err := promptOptionalDays(t, le, "Expire objects after how many days? (blank to skip)", expireDefault,
		validateGreaterThan(latestTransitionDays, len(transitions) > 0, "the latest transition"))
	if err != nil {
		return types.LifecycleRule{}, err
	}

	if len(transitions) == 0 && !expireSet {
		t.Println("At least one transition or an expiration must be set -- nothing to do.")
		t.Refresh()
		return types.LifecycleRule{}, errNothingToDo
	}

	rule := types.LifecycleRule{
		ID:     aws.String(id),
		Status: types.ExpirationStatusEnabled,
		Filter: &types.LifecycleRuleFilter{Prefix: aws.String(prefix)},
	}
	if len(transitions) > 0 {
		rule.Transitions = transitions
	}
	if expireSet {
		rule.Expiration = &types.LifecycleExpiration{Days: aws.Int32(expireDays)}
	}
	return rule, nil
}

// addLifecycleRule, editLifecycleRule, and removeLifecycleRule each
// return (updated rule set, whether to proceed with PutBucketLifecycle
// Configuration, error). proceed is false without an error when the
// operator declined a confirmation or the prompts collected nothing to
// do -- both already reported, neither a reason to call AWS.
func addLifecycleRule(t *termlib.Terminal, le *termlib.LineEditor, purpose string, rules []types.LifecycleRule) ([]types.LifecycleRule, bool, error) {
	var rule types.LifecycleRule
	var err error
	if purpose == "backup" {
		rule, err = promptGuidedBackupRule(t, le, types.LifecycleRule{})
	} else {
		rule, err = promptGenericRule(t, le, types.LifecycleRule{}, rules)
	}
	if errors.Is(err, errNothingToDo) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	ok, err := confirmLifecycleChange(t, le, fmt.Sprintf("Add lifecycle rule %s", aws.ToString(rule.ID)))
	if err != nil {
		return nil, false, err
	}
	if !ok {
		t.Println("Cancelled.")
		t.Refresh()
		return nil, false, nil
	}

	return append(append([]types.LifecycleRule{}, rules...), rule), true, nil
}

func editLifecycleRule(t *termlib.Terminal, le *termlib.LineEditor, purpose string, rules []types.LifecycleRule) ([]types.LifecycleRule, bool, error) {
	if len(rules) == 0 {
		t.Println("No rules to edit.")
		t.Refresh()
		return nil, false, nil
	}
	existing, err := ui.PickList(t, le, rules, lifecycleRuleLabel, "Select a rule to edit")
	if err != nil {
		return nil, false, err
	}

	var updated types.LifecycleRule
	if purpose == "backup" {
		updated, err = promptGuidedBackupRule(t, le, existing)
	} else {
		updated, err = promptGenericRule(t, le, existing, rules)
	}
	if errors.Is(err, errNothingToDo) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	ok, err := confirmLifecycleChange(t, le, fmt.Sprintf("Update lifecycle rule %s", aws.ToString(updated.ID)))
	if err != nil {
		return nil, false, err
	}
	if !ok {
		t.Println("Cancelled.")
		t.Refresh()
		return nil, false, nil
	}

	newRules := make([]types.LifecycleRule, len(rules))
	copy(newRules, rules)
	for i, r := range newRules {
		if aws.ToString(r.ID) == aws.ToString(existing.ID) {
			newRules[i] = updated
		}
	}
	return newRules, true, nil
}

func removeLifecycleRule(t *termlib.Terminal, le *termlib.LineEditor, rules []types.LifecycleRule) ([]types.LifecycleRule, bool, error) {
	if len(rules) == 0 {
		t.Println("No rules to remove.")
		t.Refresh()
		return nil, false, nil
	}
	existing, err := ui.PickList(t, le, rules, lifecycleRuleLabel, "Select a rule to remove")
	if err != nil {
		return nil, false, err
	}

	ok, err := Confirm(t, le, fmt.Sprintf("Remove lifecycle rule %s? AWS applies this on its own evaluation cycle (typically 24-48 hours), not immediately.", aws.ToString(existing.ID)))
	if err != nil {
		return nil, false, err
	}
	if !ok {
		t.Println("Cancelled.")
		t.Refresh()
		return nil, false, nil
	}

	var newRules []types.LifecycleRule
	for _, r := range rules {
		if aws.ToString(r.ID) != aws.ToString(existing.ID) {
			newRules = append(newRules, r)
		}
	}
	return newRules, true, nil
}

// ManageBucketLifecyclePolicies runs the S3 domain's "Manage Bucket
// Lifecycle Policies" workflow (DESIGN.md, Feature 21.1): pick a bucket,
// fetch and display its current rules, then one Add/Edit/Remove action,
// branching internally on the bucket's Purpose tag -- "backup" gets the
// guided flow, anything else gets the generic editor (one menu entry, not
// two separate features). The API has no per-rule operations, so every
// action ends by writing the complete modified rule set via one
// s3:PutBucketLifecycleConfiguration call.
func ManageBucketLifecyclePolicies(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), buckets []inventory.Bucket) error {
	if len(buckets) == 0 {
		t.Println("No buckets found.")
		t.Refresh()
		return nil
	}

	bucket, err := ui.PickList(t, le, buckets, bucketLabel, "Select a bucket")
	if err != nil {
		return cancelledIsNil(t, err)
	}

	client, err := newS3Client(ctx, bucket.Region)
	if err != nil {
		return err
	}

	for {
		rules, err := getLifecycleRules(ctx, client, bucket.Name)
		if err != nil {
			return fmt.Errorf("getting lifecycle configuration for bucket %s: %w", bucket.Name, err)
		}
		displayLifecycleRules(t, rules)

		action, err := ui.PickList(t, le, lifecycleActions, lifecycleActionLabel, "Choose an action")
		if err != nil {
			return cancelledIsNil(t, err)
		}

		if action == "View rule details" {
			if err := viewLifecycleRuleDetail(t, le, rules); err != nil {
				return cancelledIsNil(t, err)
			}
			continue // read-only -- back to the action menu, not out of the workflow
		}

		var newRules []types.LifecycleRule
		var proceed bool
		switch action {
		case "Add rule":
			newRules, proceed, err = addLifecycleRule(t, le, bucket.Purpose, rules)
		case "Edit rule":
			newRules, proceed, err = editLifecycleRule(t, le, bucket.Purpose, rules)
		case "Remove rule":
			newRules, proceed, err = removeLifecycleRule(t, le, rules)
		default: // "Back"
			return nil
		}
		if err != nil {
			return cancelledIsNil(t, err)
		}
		if !proceed {
			return nil
		}

		// PutBucketLifecycleConfiguration rejects an empty Rules list client-side
		// (a required field) -- confirmed via real-AWS verification when
		// removing a bucket's last remaining rule. Clearing the configuration
		// entirely goes through the separate DeleteBucketLifecycle operation
		// instead (see DECISIONS.md).
		if len(newRules) == 0 {
			if _, err := client.DeleteBucketLifecycle(ctx, &s3.DeleteBucketLifecycleInput{Bucket: aws.String(bucket.Name)}); err != nil {
				return fmt.Errorf("clearing lifecycle configuration for bucket %s: %w", bucket.Name, err)
			}
		} else if _, err := client.PutBucketLifecycleConfiguration(ctx, &s3.PutBucketLifecycleConfigurationInput{
			Bucket:                 aws.String(bucket.Name),
			LifecycleConfiguration: &types.BucketLifecycleConfiguration{Rules: newRules},
		}); err != nil {
			return fmt.Errorf("updating lifecycle configuration for bucket %s: %w", bucket.Name, err)
		}

		t.Printf("Updated lifecycle configuration for bucket %s (%d rule(s)).\n", bucket.Name, len(newRules))
		t.Refresh()
		return nil
	}
}

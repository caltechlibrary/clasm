package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/tui"
	"github.com/caltechlibrary/clasm/internal/ui"
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

// lifecycleActions is DESIGN.md's lifecycle action menu. No "Back" entry
// -- DECISIONS.md, "TUI keybinding conventions": 'q' is the universal
// back key everywhere, so a redundant menu item would just be a second
// way to do the same thing (matching s3MenuItems' own drop of "Back to
// domain picker" in Phase 20.7).
var lifecycleActions = []string{"Add rule", "Edit rule", "Remove rule", "View rule details"}

// pickLifecycleAction runs the lifecycle action menu as a huh.Select
// (PLAN.md Phase 20.9), reusing the same q-quit-key/accessible-mode-
// testability pattern established for RunS3Menu (Phase 20.2/20.7) via
// pickString. input/output are nil in production (the field runs
// interactively on the real terminal) or supplied by tests to drive it
// through its accessible-mode pipe path instead.
func pickLifecycleAction(w io.Writer, input io.Reader, output io.Writer) (string, error) {
	return pickString(w, "Choose an action", "(q to go back)", lifecycleActions, input, output)
}

func lifecycleRuleLabel(r types.LifecycleRule) string {
	prefix := "(whole bucket)"
	if r.Filter != nil && aws.ToString(r.Filter.Prefix) != "" {
		prefix = aws.ToString(r.Filter.Prefix)
	}
	return fmt.Sprintf("%s [%s]", aws.ToString(r.ID), prefix)
}

// pickLifecycleRule runs a Picker-tier tui.RunPicker (DESIGN.md's full
// conversion punch list) over rules and returns the chosen one. Like
// pickInstance/pickImage/pickSubnet, this drives a real bubbletea
// Program that can't be pipe-tested.
func pickLifecycleRule(ctx context.Context, title string, rules []types.LifecycleRule) (types.LifecycleRule, error) {
	rows := make([]string, len(rules))
	for i, r := range rules {
		rows[i] = lifecycleRuleLabel(r)
	}
	idx, err := tui.RunPicker(ctx, tui.PickerConfig{
		Title:        title,
		Rows:         rows,
		ColorEnabled: ui.ColorEnabled(),
	})
	if err != nil {
		return types.LifecycleRule{}, err
	}
	return rules[idx], nil
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

func displayLifecycleRules(w io.Writer, rules []types.LifecycleRule) {
	if len(rules) == 0 {
		fmt.Fprintln(w, "No lifecycle rules configured.")
		return
	}
	fmt.Fprintln(w, "Current lifecycle rules:")
	for _, r := range rules {
		fmt.Fprintf(w, "  %s\n", lifecycleRuleLabel(r))
	}
}

// viewLifecycleRuleDetail lets the operator pick one existing rule and
// print its full configuration -- status, filter, expiration, and every
// transition -- without entering the Add/Edit prompts. Added after manual
// testing found the terse ID+prefix line displayLifecycleRules already
// prints isn't enough to check a rule's actual expiration/transition
// schedule at a glance.
func viewLifecycleRuleDetail(ctx context.Context, w io.Writer, rules []types.LifecycleRule) error {
	if len(rules) == 0 {
		fmt.Fprintln(w, "No rules to view.")
		return nil
	}
	rule, err := pickLifecycleRule(ctx, "Select a rule to view", rules)
	if err != nil {
		return err
	}
	printLifecycleRuleDetail(w, rule)
	return nil
}

func printLifecycleRuleDetail(w io.Writer, r types.LifecycleRule) {
	prefix := "(whole bucket)"
	if r.Filter != nil && aws.ToString(r.Filter.Prefix) != "" {
		prefix = aws.ToString(r.Filter.Prefix)
	}
	fmt.Fprintf(w, "Rule %s\n", aws.ToString(r.ID))
	fmt.Fprintf(w, "  Status: %s\n", r.Status)
	fmt.Fprintf(w, "  Applies to: %s\n", prefix)
	if r.Expiration != nil && r.Expiration.Days != nil {
		fmt.Fprintf(w, "  Expires after %d day(s)\n", aws.ToInt32(r.Expiration.Days))
	} else {
		fmt.Fprintln(w, "  No expiration set")
	}
	if len(r.Transitions) == 0 {
		fmt.Fprintln(w, "  No transitions set")
	} else {
		fmt.Fprintln(w, "  Transitions:")
		for _, tr := range r.Transitions {
			fmt.Fprintf(w, "    %d day(s) -> %s\n", aws.ToInt32(tr.Days), storageClassLabel(tr.StorageClass))
		}
	}
}

// confirmLifecycleChange gates every add/edit/remove with a reminder that
// AWS evaluates lifecycle rules on its own ~24-48h cadence -- this
// schedules future automated deletion/transition, not an immediate one
// (DESIGN.md Security Consideration #13).
func confirmLifecycleChange(w io.Writer, action string, input io.Reader, output io.Writer) (bool, error) {
	return Confirm(fmt.Sprintf("%s -- AWS applies lifecycle rule changes on its own evaluation cycle (typically 24-48 hours), not immediately. Proceed?", action), WithConfirmIO(input, output))
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
func promptOptionalDays(w io.Writer, label, def string, input io.Reader, output io.Writer, checks ...func(int32) error) (int32, bool, error) {
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
	opts = append(opts, ui.WithIO(input, output))
	if _, err := ui.Prompt(label, opts...); err != nil {
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
func promptPositiveDays(w io.Writer, label string, input io.Reader, output io.Writer) (int32, error) {
	var days int32
	_, err := ui.Prompt(label, ui.WithValidator(func(s string) error {
		n, convErr := strconv.Atoi(strings.TrimSpace(s))
		if convErr != nil || n <= 0 {
			return errors.New("must be a positive integer")
		}
		days = int32(n)
		return nil
	}), ui.WithIO(input, output))
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
func promptGuidedBackupRule(w io.Writer, current types.LifecycleRule, menuInput io.Reader, menuOutput io.Writer) (types.LifecycleRule, error) {
	expireDefault := ""
	if current.Expiration != nil && current.Expiration.Days != nil {
		expireDefault = strconv.Itoa(int(*current.Expiration.Days))
	}
	expireDays, expireSet, err := promptOptionalDays(w, "Expire objects after how many days? (blank to skip)", expireDefault, menuInput, menuOutput)
	if err != nil {
		return types.LifecycleRule{}, err
	}

	transitionDefault := ""
	if len(current.Transitions) > 0 && current.Transitions[0].Days != nil {
		transitionDefault = strconv.Itoa(int(*current.Transitions[0].Days))
	}
	transitionDays, transitionSet, err := promptOptionalDays(w, "Transition to cheaper storage after how many days? (blank to skip)", transitionDefault, menuInput, menuOutput,
		validateLessThan(expireDays, expireSet, "the expiration"))
	if err != nil {
		return types.LifecycleRule{}, err
	}

	var storageClass types.TransitionStorageClass
	if transitionSet {
		storageClass, err = pickComparable(w, "Select a storage class to transition to", "(q to cancel)", backupStorageClasses, storageClassLabel, menuInput, menuOutput)
		if err != nil {
			return types.LifecycleRule{}, err
		}
	}

	if !expireSet && !transitionSet {
		fmt.Fprintln(w, "At least one of expiration or transition must be set -- nothing to do.")
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
	prefixOpts = append(prefixOpts, ui.WithIO(menuInput, menuOutput))
	prefix, err := ui.Prompt("Key prefix (blank for whole bucket)", prefixOpts...)
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
func promptGenericRule(w io.Writer, current types.LifecycleRule, existingRules []types.LifecycleRule, menuInput io.Reader, menuOutput io.Writer) (types.LifecycleRule, error) {
	id := aws.ToString(current.ID)
	if id == "" {
		var err error
		id, err = ui.Prompt("Rule ID", ui.WithValidator(func(s string) error {
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
		}), ui.WithIO(menuInput, menuOutput))
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
	prefixOpts = append(prefixOpts, ui.WithIO(menuInput, menuOutput))
	prefix, err := ui.Prompt("Key prefix (blank for whole bucket)", prefixOpts...)
	if err != nil {
		return types.LifecycleRule{}, err
	}

	if len(current.Transitions) > 0 {
		fmt.Fprintln(w, "Current transitions:")
		for _, tr := range current.Transitions {
			fmt.Fprintf(w, "  %d days -> %s\n", aws.ToInt32(tr.Days), storageClassLabel(tr.StorageClass))
		}
	}
	var transitions []types.Transition
	for {
		addMore, err := Confirm("Add a transition?", WithConfirmIO(menuInput, menuOutput))
		if err != nil {
			return types.LifecycleRule{}, err
		}
		if !addMore {
			break
		}
		days, err := promptPositiveDays(w, "Transition after how many days?", menuInput, menuOutput)
		if err != nil {
			return types.LifecycleRule{}, err
		}
		class, err := pickComparable(w, "Select a storage class", "(q to cancel)", types.TransitionStorageClass("").Values(), storageClassLabel, menuInput, menuOutput)
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
	expireDays, expireSet, err := promptOptionalDays(w, "Expire objects after how many days? (blank to skip)", expireDefault, menuInput, menuOutput,
		validateGreaterThan(latestTransitionDays, len(transitions) > 0, "the latest transition"))
	if err != nil {
		return types.LifecycleRule{}, err
	}

	if len(transitions) == 0 && !expireSet {
		fmt.Fprintln(w, "At least one transition or an expiration must be set -- nothing to do.")
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
func addLifecycleRule(w io.Writer, purpose string, rules []types.LifecycleRule, menuInput io.Reader, menuOutput io.Writer) ([]types.LifecycleRule, bool, error) {
	var rule types.LifecycleRule
	var err error
	if purpose == "backup" {
		rule, err = promptGuidedBackupRule(w, types.LifecycleRule{}, menuInput, menuOutput)
	} else {
		rule, err = promptGenericRule(w, types.LifecycleRule{}, rules, menuInput, menuOutput)
	}
	if errors.Is(err, errNothingToDo) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	ok, err := confirmLifecycleChange(w, fmt.Sprintf("Add lifecycle rule %s", aws.ToString(rule.ID)), menuInput, menuOutput)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil, false, nil
	}

	return append(append([]types.LifecycleRule{}, rules...), rule), true, nil
}

func editLifecycleRule(ctx context.Context, w io.Writer, purpose string, rules []types.LifecycleRule, menuInput io.Reader, menuOutput io.Writer) ([]types.LifecycleRule, bool, error) {
	if len(rules) == 0 {
		fmt.Fprintln(w, "No rules to edit.")
		return nil, false, nil
	}
	existing, err := pickLifecycleRule(ctx, "Select a rule to edit", rules)
	if err != nil {
		return nil, false, err
	}
	return editLifecycleRuleForRule(w, purpose, rules, existing, menuInput, menuOutput)
}

// editLifecycleRuleForRule is editLifecycleRule's testable core, once
// the rule to edit is resolved -- rule selection runs a real bubbletea
// Program (tui.RunPicker, DESIGN.md's full conversion punch list) that
// can't be driven by a test's pipe input, same limitation as every
// other Picker-tier conversion this session.
func editLifecycleRuleForRule(w io.Writer, purpose string, rules []types.LifecycleRule, existing types.LifecycleRule, menuInput io.Reader, menuOutput io.Writer) ([]types.LifecycleRule, bool, error) {
	var updated types.LifecycleRule
	var err error
	if purpose == "backup" {
		updated, err = promptGuidedBackupRule(w, existing, menuInput, menuOutput)
	} else {
		updated, err = promptGenericRule(w, existing, rules, menuInput, menuOutput)
	}
	if errors.Is(err, errNothingToDo) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	ok, err := confirmLifecycleChange(w, fmt.Sprintf("Update lifecycle rule %s", aws.ToString(updated.ID)), menuInput, menuOutput)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
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

func removeLifecycleRule(ctx context.Context, w io.Writer, rules []types.LifecycleRule, input io.Reader, output io.Writer) ([]types.LifecycleRule, bool, error) {
	if len(rules) == 0 {
		fmt.Fprintln(w, "No rules to remove.")
		return nil, false, nil
	}
	existing, err := pickLifecycleRule(ctx, "Select a rule to remove", rules)
	if err != nil {
		return nil, false, err
	}
	return removeLifecycleRuleForRule(w, rules, existing, input, output)
}

// removeLifecycleRuleForRule is removeLifecycleRule's testable core,
// once the rule to remove is resolved -- same limitation as
// editLifecycleRuleForRule above.
func removeLifecycleRuleForRule(w io.Writer, rules []types.LifecycleRule, existing types.LifecycleRule, input io.Reader, output io.Writer) ([]types.LifecycleRule, bool, error) {
	ok, err := Confirm(fmt.Sprintf("Remove lifecycle rule %s? AWS applies this on its own evaluation cycle (typically 24-48 hours), not immediately.", aws.ToString(existing.ID)), WithConfirmIO(input, output))
	if err != nil {
		return nil, false, err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
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
func ManageBucketLifecyclePolicies(ctx context.Context, w io.Writer, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), buckets []inventory.Bucket) error {
	if len(buckets) == 0 {
		fmt.Fprintln(w, "No buckets found.")
		return nil
	}

	bucket, err := pickBucket(ctx, "Select a bucket", buckets)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	return manageBucketLifecyclePolicies(ctx, w, newS3Client, bucket, nil, nil)
}

// manageBucketLifecyclePolicies is ManageBucketLifecyclePolicies's
// testable core, once a bucket is resolved -- bucket selection runs a
// real bubbletea Program (tui.RunPicker, PLAN.md Phase 20.4) that can't
// be driven by a test's pipe input. actionMenuInput/actionMenuOutput are
// nil in production (the action menu's huh.Select runs interactively on
// the real terminal) and are supplied by tests to drive it through its
// accessible-mode pipe path instead (DECISIONS.md, "huh fields are
// pipe-testable...").
func manageBucketLifecyclePolicies(ctx context.Context, w io.Writer, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), bucket inventory.Bucket, actionMenuInput io.Reader, actionMenuOutput io.Writer) error {
	client, err := newS3Client(ctx, bucket.Region)
	if err != nil {
		return err
	}

	for {
		if ctx.Err() != nil {
			printExiting(w)
			return nil
		}

		rules, err := getLifecycleRules(ctx, client, bucket.Name)
		if err != nil {
			return fmt.Errorf("getting lifecycle configuration for bucket %s: %w", bucket.Name, err)
		}
		displayLifecycleRules(w, rules)

		action, err := pickLifecycleAction(w, actionMenuInput, actionMenuOutput)
		if err != nil {
			return huhCancelledIsNil(err)
		}

		if action == "View rule details" {
			if err := viewLifecycleRuleDetail(ctx, w, rules); err != nil {
				return cancelledIsNil(w, err)
			}
			continue // read-only -- back to the action menu, not out of the workflow
		}

		var newRules []types.LifecycleRule
		var proceed bool
		switch action {
		case "Add rule":
			newRules, proceed, err = addLifecycleRule(w, bucket.Purpose, rules, actionMenuInput, actionMenuOutput)
		case "Edit rule":
			newRules, proceed, err = editLifecycleRule(ctx, w, bucket.Purpose, rules, actionMenuInput, actionMenuOutput)
		case "Remove rule":
			newRules, proceed, err = removeLifecycleRule(ctx, w, rules, actionMenuInput, actionMenuOutput)
		}
		if err != nil {
			return cancelledIsNil(w, err)
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

		fmt.Fprintf(w, "Updated lifecycle configuration for bucket %s (%d rule(s)).\n", bucket.Name, len(newRules))
		return nil
	}
}

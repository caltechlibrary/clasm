package workflow

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// backToPickerAction returns a DomainActions entry that counts calls and
// reports ErrBackToDomainPicker, simulating a domain menu loop the
// operator deliberately backed out of (as opposed to a genuine exit
// signal, which reports nil -- see
// TestRunDomainPicker_DomainExitSignalEndsTheWholeProgramWithoutReturningToPicker).
func backToPickerAction(calls *int) func(context.Context) error {
	return func(ctx context.Context) error {
		*calls++
		return ErrBackToDomainPicker
	}
}

// cancelingBackToPickerAction is like backToPickerAction, but also
// cancels ctx -- used to drive one iteration of runDomainPicker's loop
// (a dispatch that returns ErrBackToDomainPicker, continuing the loop)
// and then have the *next* iteration's ctx.Err() check end it cleanly.
// Stands in for choosing "Exit" (removed in this phase: 'q' is now the
// only way, and accessible mode has no way to simulate that abort --
// see mapMenuPickerErr's doc comment for the same limitation).
func cancelingBackToPickerAction(calls *int, cancel context.CancelFunc) func(context.Context) error {
	return func(ctx context.Context) error {
		*calls++
		cancel()
		return ErrBackToDomainPicker
	}
}

func TestRunDomainPicker_DispatchesToTheChosenDomain(t *testing.T) {
	var compute, keyMgmt, s3 int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := DomainActions{
		Compute:       backToPickerAction(&compute),
		KeyManagement: cancelingBackToPickerAction(&keyMgmt, cancel),
		S3:            backToPickerAction(&s3),
	}

	menuInput := newHuhAccessibleInput("2\n") // Key Management
	if err := runDomainPicker(ctx, term, actions, menuInput, buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if keyMgmt != 1 {
		t.Errorf("keyMgmt calls = %d, want 1", keyMgmt)
	}
	if compute != 0 || s3 != 0 {
		t.Errorf("expected only Key Management to be dispatched, got compute=%d s3=%d", compute, s3)
	}
}

func TestRunDomainPicker_DispatchesToTagManagement(t *testing.T) {
	var compute, keyMgmt, s3, tagMgmt int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := DomainActions{
		Compute:       backToPickerAction(&compute),
		KeyManagement: backToPickerAction(&keyMgmt),
		S3:            backToPickerAction(&s3),
		TagManagement: cancelingBackToPickerAction(&tagMgmt, cancel),
	}

	menuInput := newHuhAccessibleInput("4\n") // Tag Management
	if err := runDomainPicker(ctx, term, actions, menuInput, buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if tagMgmt != 1 {
		t.Errorf("tagMgmt calls = %d, want 1", tagMgmt)
	}
	if compute != 0 || keyMgmt != 0 || s3 != 0 {
		t.Errorf("expected only Tag Management to be dispatched, got compute=%d keyMgmt=%d s3=%d", compute, keyMgmt, s3)
	}
}

func TestRunDomainPicker_DispatchesToIAM(t *testing.T) {
	var compute, keyMgmt, s3, tagMgmt, iamDomain int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := DomainActions{
		Compute:       backToPickerAction(&compute),
		KeyManagement: backToPickerAction(&keyMgmt),
		S3:            backToPickerAction(&s3),
		TagManagement: backToPickerAction(&tagMgmt),
		IAM:           cancelingBackToPickerAction(&iamDomain, cancel),
	}

	menuInput := newHuhAccessibleInput("5\n") // IAM
	if err := runDomainPicker(ctx, term, actions, menuInput, buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if iamDomain != 1 {
		t.Errorf("iamDomain calls = %d, want 1", iamDomain)
	}
	if compute != 0 || keyMgmt != 0 || s3 != 0 || tagMgmt != 0 {
		t.Errorf("expected only IAM to be dispatched, got compute=%d keyMgmt=%d s3=%d tagMgmt=%d", compute, keyMgmt, s3, tagMgmt)
	}
}

func TestRunDomainPicker_DispatchesToConfiguration(t *testing.T) {
	var compute, keyMgmt, s3, tagMgmt, iamDomain, configuration int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := DomainActions{
		Compute:       backToPickerAction(&compute),
		KeyManagement: backToPickerAction(&keyMgmt),
		S3:            backToPickerAction(&s3),
		TagManagement: backToPickerAction(&tagMgmt),
		IAM:           backToPickerAction(&iamDomain),
		Configuration: cancelingBackToPickerAction(&configuration, cancel),
	}

	menuInput := newHuhAccessibleInput("7\n") // Configuration
	if err := runDomainPicker(ctx, term, actions, menuInput, buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if configuration != 1 {
		t.Errorf("configuration calls = %d, want 1", configuration)
	}
	if compute != 0 || keyMgmt != 0 || s3 != 0 || tagMgmt != 0 || iamDomain != 0 {
		t.Errorf("expected only Configuration to be dispatched, got compute=%d keyMgmt=%d s3=%d tagMgmt=%d iamDomain=%d", compute, keyMgmt, s3, tagMgmt, iamDomain)
	}
}

func TestRunDomainPicker_DispatchesToRDMBackupRestore(t *testing.T) {
	var compute, keyMgmt, s3, tagMgmt, iamDomain, configuration, rdmBackupRestore int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := DomainActions{
		Compute:          backToPickerAction(&compute),
		KeyManagement:    backToPickerAction(&keyMgmt),
		S3:               backToPickerAction(&s3),
		TagManagement:    backToPickerAction(&tagMgmt),
		IAM:              backToPickerAction(&iamDomain),
		Configuration:    backToPickerAction(&configuration),
		RDMBackupRestore: cancelingBackToPickerAction(&rdmBackupRestore, cancel),
	}

	menuInput := newHuhAccessibleInput("6\n") // RDM Backup & Restore
	if err := runDomainPicker(ctx, term, actions, menuInput, buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if rdmBackupRestore != 1 {
		t.Errorf("rdmBackupRestore calls = %d, want 1", rdmBackupRestore)
	}
	if compute != 0 || keyMgmt != 0 || s3 != 0 || tagMgmt != 0 || iamDomain != 0 || configuration != 0 {
		t.Errorf("expected only RDM Backup & Restore to be dispatched, got compute=%d keyMgmt=%d s3=%d tagMgmt=%d iamDomain=%d configuration=%d", compute, keyMgmt, s3, tagMgmt, iamDomain, configuration)
	}
}

func TestRunDomainPicker_BackToDomainPickerReturnsToThePicker(t *testing.T) {
	var compute int
	term, buf := newTermOnly()
	ctx, cancel := context.WithCancel(context.Background())

	actions := DomainActions{
		Compute:       cancelingBackToPickerAction(&compute, cancel),
		KeyManagement: backToPickerAction(new(int)),
		S3:            backToPickerAction(new(int)),
	}

	menuInput := newHuhAccessibleInput("1\n") // Compute (backs out)
	if err := runDomainPicker(ctx, term, actions, menuInput, buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) once ctx is cancelled, got: %v", err)
	}
	if compute != 1 {
		t.Errorf("compute calls = %d, want 1", compute)
	}
}

func TestRunDomainPicker_CleanExitOnAlreadyCancelledContext(t *testing.T) {
	term, buf := newTermOnly()
	actions := DomainActions{
		Compute:       backToPickerAction(new(int)),
		KeyManagement: backToPickerAction(new(int)),
		S3:            backToPickerAction(new(int)),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := runDomainPicker(ctx, term, actions, newHuhAccessibleInput(""), buf); err != nil {
		t.Fatalf("expected a clean exit (nil error) on an already-cancelled context, got: %v", err)
	}
}

// A domain's own menu loop reports a genuine exit signal (Ctrl+C, EOF,
// cancelled ctx) the same way RunMainMenu already does: nil, not
// ErrBackToDomainPicker. RunDomainPicker must treat that as "the whole
// program exits now", not "return to the picker" -- otherwise an
// operator inside, say, the S3 domain would have to exit twice (once to
// get back to the picker, again to leave the picker) instead of once
// (DESIGN.md, "Navigation: Domain Picker").
func TestRunDomainPicker_DomainExitSignalEndsTheWholeProgramWithoutReturningToPicker(t *testing.T) {
	term, buf := newTermOnly()
	s3Runs := 0

	actions := DomainActions{
		Compute:       backToPickerAction(new(int)),
		KeyManagement: backToPickerAction(new(int)),
		S3: func(ctx context.Context) error {
			s3Runs++
			return nil
		},
	}

	menuInput := newHuhAccessibleInput("3\n") // S3; a second read would starve if this looped back
	if err := runDomainPicker(context.Background(), term, actions, menuInput, buf); err != nil {
		t.Fatalf("expected a clean exit (nil error), got: %v", err)
	}
	if s3Runs != 1 {
		t.Errorf("s3Runs = %d, want 1", s3Runs)
	}
}

func TestRunDomainPicker_RealDomainErrorPropagates(t *testing.T) {
	term, buf := newTermOnly()
	boom := errors.New("boom")

	actions := DomainActions{
		Compute:       backToPickerAction(new(int)),
		KeyManagement: backToPickerAction(new(int)),
		S3:            failingAction(boom),
	}

	menuInput := newHuhAccessibleInput("3\n") // S3
	err := runDomainPicker(context.Background(), term, actions, menuInput, buf)
	if !errors.Is(err, boom) {
		t.Fatalf("expected the domain's error to propagate, got: %v", err)
	}
}

func TestDomainItems_NoExitEntry(t *testing.T) {
	if len(domainItems) != 7 {
		t.Fatalf("len(domainItems) = %d, want 7 (no more explicit \"Exit\" -- 'q' is the only way back/out now)", len(domainItems))
	}
	for _, item := range domainItems {
		if item.action == nil {
			t.Errorf("found a nil-action item %q -- \"Exit\" should have been removed", item.label)
		}
	}
}

// TestDomainItems_CLISlugs pins PLAN.md Phase 20.64: only "RDM Backup &
// Restore" gets a cliSlug in this phase (mechanical rule: lowercase, "&"
// -> "and", hyphenate). Every other domain stays "" -- genuinely
// unreachable from the CLI path, not just missing a full-args form (see
// the design brief, decision 3).
func TestDomainItems_CLISlugs(t *testing.T) {
	want := map[string]string{
		"Compute (EC2 & AMI)":            "",
		"Key Management":                 "",
		"S3 (Buckets & Static Websites)": "",
		"Tag Management":                 "",
		"IAM":                            "",
		"RDM Backup & Restore":           "rdm-backup-and-restore",
		"Configuration":                  "",
	}
	for _, item := range domainItems {
		wantSlug, ok := want[item.label]
		if !ok {
			t.Fatalf("unexpected domain label %q -- update this test's want map", item.label)
		}
		if item.cliSlug != wantSlug {
			t.Errorf("domainItems[%q].cliSlug = %q, want %q", item.label, item.cliSlug, wantSlug)
		}
	}
}

// TestRunDomainPickerFromSlug_UnknownSlugIsNotFound pins that an
// unregistered slug does nothing at all -- no picker, no action, no
// error -- so a CLI entry point can tell "not a domain" apart from
// every other outcome and usage-error out itself.
func TestRunDomainPickerFromSlug_UnknownSlugIsNotFound(t *testing.T) {
	term, buf := newTermOnly()
	actions := DomainActions{}

	ok, err := runDomainPickerFromSlug(context.Background(), term, actions, "no-such-domain", nil, nil)
	if ok {
		t.Error("expected ok = false for an unregistered slug")
	}
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output for an unregistered slug, got:\n%s", buf.String())
	}
}

// TestRunDomainPickerFromSlug_RunsThatDomainThenFallsBackToPickerOnBack
// pins PLAN.md Phase 20.64's domain-level deep-link: `clasm
// rdm-backup-and-restore` runs that domain directly, and if the operator
// backs out of it (ErrBackToDomainPicker), lands on the exact same root
// picker a plain `clasm` would have shown -- not a dead end, not an
// immediate exit.
func TestRunDomainPickerFromSlug_RunsThatDomainThenFallsBackToPickerOnBack(t *testing.T) {
	var rdm, compute int
	ctx, cancel := context.WithCancel(context.Background())
	menuInput := newHuhAccessibleInput("1\n") // the picker's first entry, Compute, once we fall back to it
	term, buf := newTermOnly()

	actions := DomainActions{
		// Cancels ctx so the picker's loop exits cleanly on its next
		// iteration's ctx.Err() check, instead of trying to read a
		// second selection from the now-exhausted menuInput -- same
		// idiom TestRunDomainPicker_DispatchesToTheChosenDomain uses.
		Compute:          cancelingBackToPickerAction(&compute, cancel),
		RDMBackupRestore: backToPickerAction(&rdm),
	}

	ok, err := runDomainPickerFromSlug(ctx, term, actions, "rdm-backup-and-restore", menuInput, buf)
	if !ok {
		t.Fatal("expected ok = true for a registered slug")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rdm != 1 {
		t.Errorf("rdm calls = %d, want 1 (the deep-linked domain, run once)", rdm)
	}
	if compute != 1 {
		t.Errorf("compute calls = %d, want 1 (chosen from the picker after falling back)", compute)
	}
}

// TestRunDomainPickerFromSlug_CleanExitPropagatesAsNil pins that a
// domain action returning nil (a genuine exit signal, not "back to
// picker") ends the whole run cleanly, exactly like RunDomainPicker's
// own top-level loop.
func TestRunDomainPickerFromSlug_CleanExitPropagatesAsNil(t *testing.T) {
	term, buf := newTermOnly()
	var calls int
	actions := DomainActions{
		RDMBackupRestore: func(ctx context.Context) error {
			calls++
			return nil
		},
	}

	ok, err := runDomainPickerFromSlug(context.Background(), term, actions, "rdm-backup-and-restore", nil, buf)
	if !ok || err != nil {
		t.Fatalf("got ok=%t err=%v, want ok=true err=nil", ok, err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

// TestRunDomainPickerFromSlug_ErrorPropagates pins that a genuine error
// (neither ErrBackToDomainPicker nor nil) propagates as-is.
func TestRunDomainPickerFromSlug_ErrorPropagates(t *testing.T) {
	term, _ := newTermOnly()
	boom := errors.New("boom")
	actions := DomainActions{
		RDMBackupRestore: func(ctx context.Context) error { return boom },
	}

	ok, err := runDomainPickerFromSlug(context.Background(), term, actions, "rdm-backup-and-restore", nil, nil)
	if !ok {
		t.Fatal("expected ok = true for a registered slug")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("expected the domain's error to propagate, got: %v", err)
	}
}

func TestNotYetImplemented_PrintsAMessageAndReturnsToPicker(t *testing.T) {
	var buf bytes.Buffer

	err := NotYetImplemented(&buf, "Key Management")
	if !errors.Is(err, ErrBackToDomainPicker) {
		t.Fatalf("expected ErrBackToDomainPicker, got: %v", err)
	}
	if !strings.Contains(buf.String(), "Key Management") {
		t.Errorf("expected the domain name in the message, got:\n%s", buf.String())
	}
}

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

	menuInput := newHuhAccessibleInput("6\n") // Configuration
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
	if len(domainItems) != 6 {
		t.Fatalf("len(domainItems) = %d, want 6 (no more explicit \"Exit\" -- 'q' is the only way back/out now)", len(domainItems))
	}
	for _, item := range domainItems {
		if item.action == nil {
			t.Errorf("found a nil-action item %q -- \"Exit\" should have been removed", item.label)
		}
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

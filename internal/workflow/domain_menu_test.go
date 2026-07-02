package workflow

import (
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

func TestRunDomainPicker_DispatchesToTheChosenDomain(t *testing.T) {
	var compute, keyMgmt, s3, cf int
	term, le, _ := newPipeEditor(t, "2\n5\n") // Key Management, then Exit

	actions := DomainActions{
		Compute:       backToPickerAction(&compute),
		KeyManagement: backToPickerAction(&keyMgmt),
		S3:            backToPickerAction(&s3),
		CloudFront:    backToPickerAction(&cf),
	}

	if err := RunDomainPicker(context.Background(), term, le, actions); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if keyMgmt != 1 {
		t.Errorf("keyMgmt calls = %d, want 1", keyMgmt)
	}
	if compute != 0 || s3 != 0 || cf != 0 {
		t.Errorf("expected only Key Management to be dispatched, got compute=%d s3=%d cloudfront=%d", compute, s3, cf)
	}
}

func TestRunDomainPicker_BackToDomainPickerReturnsToThePicker(t *testing.T) {
	var compute int
	term, le, _ := newPipeEditor(t, "1\n5\n") // Compute (backs out), then Exit

	actions := DomainActions{
		Compute:       backToPickerAction(&compute),
		KeyManagement: backToPickerAction(new(int)),
		S3:            backToPickerAction(new(int)),
		CloudFront:    backToPickerAction(new(int)),
	}

	if err := RunDomainPicker(context.Background(), term, le, actions); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if compute != 1 {
		t.Errorf("compute calls = %d, want 1", compute)
	}
}

func TestRunDomainPicker_ExitEndsTheProgram(t *testing.T) {
	term, le, _ := newPipeEditor(t, "5\n")

	actions := DomainActions{
		Compute:       backToPickerAction(new(int)),
		KeyManagement: backToPickerAction(new(int)),
		S3:            backToPickerAction(new(int)),
		CloudFront:    backToPickerAction(new(int)),
	}

	if err := RunDomainPicker(context.Background(), term, le, actions); err != nil {
		t.Fatalf("expected a clean exit (nil error), got: %v", err)
	}
}

func TestRunDomainPicker_CleanExitOnCancelledPickList(t *testing.T) {
	term, le, _ := newPipeEditor(t, "0\n") // cancel the domain pick

	actions := DomainActions{
		Compute:       backToPickerAction(new(int)),
		KeyManagement: backToPickerAction(new(int)),
		S3:            backToPickerAction(new(int)),
		CloudFront:    backToPickerAction(new(int)),
	}

	if err := RunDomainPicker(context.Background(), term, le, actions); err != nil {
		t.Fatalf("expected a clean exit (nil error), got: %v", err)
	}
}

func TestRunDomainPicker_CleanExitOnAlreadyCancelledContext(t *testing.T) {
	term, le, _ := newPipeEditor(t, "") // no input needed -- should exit before prompting

	actions := DomainActions{
		Compute:       backToPickerAction(new(int)),
		KeyManagement: backToPickerAction(new(int)),
		S3:            backToPickerAction(new(int)),
		CloudFront:    backToPickerAction(new(int)),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := RunDomainPicker(ctx, term, le, actions); err != nil {
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
	term, le, _ := newPipeEditor(t, "3\n") // pick S3; a second read would panic/hang the pipe if this looped back
	s3Runs := 0

	actions := DomainActions{
		Compute:       backToPickerAction(new(int)),
		KeyManagement: backToPickerAction(new(int)),
		S3: func(ctx context.Context) error {
			s3Runs++
			return nil
		},
		CloudFront: backToPickerAction(new(int)),
	}

	if err := RunDomainPicker(context.Background(), term, le, actions); err != nil {
		t.Fatalf("expected a clean exit (nil error), got: %v", err)
	}
	if s3Runs != 1 {
		t.Errorf("s3Runs = %d, want 1", s3Runs)
	}
}

func TestRunDomainPicker_RealDomainErrorPropagates(t *testing.T) {
	term, le, _ := newPipeEditor(t, "4\n") // CloudFront
	boom := errors.New("boom")

	actions := DomainActions{
		Compute:       backToPickerAction(new(int)),
		KeyManagement: backToPickerAction(new(int)),
		S3:            backToPickerAction(new(int)),
		CloudFront:    failingAction(boom),
	}

	err := RunDomainPicker(context.Background(), term, le, actions)
	if !errors.Is(err, boom) {
		t.Fatalf("expected the domain's error to propagate, got: %v", err)
	}
}

func TestNotYetImplemented_PrintsAMessageAndReturnsToPicker(t *testing.T) {
	term, _, buf := newPipeEditor(t, "")

	err := NotYetImplemented(term, "Key Management")
	if !errors.Is(err, ErrBackToDomainPicker) {
		t.Fatalf("expected ErrBackToDomainPicker, got: %v", err)
	}
	if !strings.Contains(buf.String(), "Key Management") {
		t.Errorf("expected the domain name in the message, got:\n%s", buf.String())
	}
}

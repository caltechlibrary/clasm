package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/caltechlibrary/clasm/internal/ui"
)

func TestInstanceTypeRequiresENA_True(t *testing.T) {
	fake := &fakeEC2Client{enaRequiredInstanceTypes: map[string]bool{"t3.small": true}}
	got, err := instanceTypeRequiresENA(context.Background(), fake, "t3.small")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("got false, want true")
	}
}

func TestInstanceTypeRequiresENA_False(t *testing.T) {
	fake := &fakeEC2Client{}
	got, err := instanceTypeRequiresENA(context.Background(), fake, "t2.micro")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("got true, want false")
	}
}

func TestInstanceTypeRequiresENA_PropagatesError(t *testing.T) {
	fake := &fakeEC2Client{describeInstanceTypesErr: errors.New("boom")}
	_, err := instanceTypeRequiresENA(context.Background(), fake, "t3.small")
	if err == nil {
		t.Fatal("expected an error")
	}
}

// The "how would you like to proceed?" incompatibility-remediation
// picker (and any nested instance-type picker) converted to huh.Select
// (DESIGN.md's full conversion punch list): their selections are fed via
// a separate newHuhAccessibleInput reader (menuInput), not le.

func TestEnsureInstanceTypeENACompatible_CompatibleReturnsImmediately(t *testing.T) {
	fake := &fakeEC2Client{}          // no type requires ENA
	term, _, buf := newPipeEditor("") // no input needed -- must not prompt

	got, err := ensureInstanceTypeENACompatible(context.Background(), term, fake, "t2.micro", false, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "t2.micro" {
		t.Errorf("got %q, want unchanged %q", got, "t2.micro")
	}
	if buf.String() != "" {
		t.Errorf("expected no output for a compatible pair, got:\n%s", buf.String())
	}
}

func TestEnsureInstanceTypeENACompatible_ENARequiredButAMISupportsIt(t *testing.T) {
	fake := &fakeEC2Client{enaRequiredInstanceTypes: map[string]bool{"t3.small": true}}
	term, _, buf := newPipeEditor("") // no input needed -- must not prompt

	got, err := ensureInstanceTypeENACompatible(context.Background(), term, fake, "t3.small", true, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "t3.small" {
		t.Errorf("got %q, want unchanged %q", got, "t3.small")
	}
	if buf.String() != "" {
		t.Errorf("expected no output when the AMI already supports ENA, got:\n%s", buf.String())
	}
}

func TestEnsureInstanceTypeENACompatible_CheckErrorSkipsGracefully(t *testing.T) {
	fake := &fakeEC2Client{describeInstanceTypesErr: errors.New("access denied")}
	term, _, buf := newPipeEditor("") // no input needed -- must not prompt

	got, err := ensureInstanceTypeENACompatible(context.Background(), term, fake, "t3.small", false, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "t3.small" {
		t.Errorf("got %q, want unchanged when the check itself errors", got)
	}
	if strings.Contains(buf.String(), "requires") {
		t.Errorf("should not claim incompatibility when the check itself failed, got:\n%s", buf.String())
	}
}

func TestEnsureInstanceTypeENACompatible_ChangeToACompatibleType(t *testing.T) {
	fake := &fakeEC2Client{enaRequiredInstanceTypes: map[string]bool{"t3.small": true}}
	term, _, buf := newPipeEditor("")

	// 1) Change instance type -> pick curated entry 1 (t3.micro, not ENA-required)
	got, err := ensureInstanceTypeENACompatible(context.Background(), term, fake, "t3.small", false, newHuhAccessibleInput("1\n1\n"), buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "t3.micro" {
		t.Errorf("got %q, want %q", got, "t3.micro")
	}
	if !strings.Contains(buf.String(), "ENA") {
		t.Errorf("expected an ENA-incompatibility message in output, got:\n%s", buf.String())
	}
}

func TestEnsureInstanceTypeENACompatible_AbortReturnsErrCancelled(t *testing.T) {
	fake := &fakeEC2Client{enaRequiredInstanceTypes: map[string]bool{"t3.small": true}}
	term, _, buf := newPipeEditor("")

	_, err := ensureInstanceTypeENACompatible(context.Background(), term, fake, "t3.small", false, newHuhAccessibleInput("2\n"), buf) // 2) Abort this launch
	if !errors.Is(err, ui.ErrCancelled) {
		t.Fatalf("expected ui.ErrCancelled, got: %v", err)
	}
}

package workflow

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPollWithProgress_PrintsInitialMessageBeforeFirstCheck(t *testing.T) {
	var buf bytes.Buffer
	calls := 0
	err := pollWithProgress(context.Background(), &buf, "thing i-1", time.Second, testPollInterval, func(ctx context.Context) (bool, error) {
		calls++
		return true, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("check called %d times, want 1", calls)
	}
	if !strings.Contains(buf.String(), "waiting for thing i-1 to complete") {
		t.Errorf("output = %q, want it to mention the label and that it's waiting", buf.String())
	}
}

func TestPollWithProgress_PrintsElapsedTimePerTick(t *testing.T) {
	var buf bytes.Buffer
	calls := 0
	err := pollWithProgress(context.Background(), &buf, "thing i-1", time.Second, testPollInterval, func(ctx context.Context) (bool, error) {
		calls++
		return calls >= 3, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("check called %d times, want 3", calls)
	}
	if got := strings.Count(buf.String(), "elapsed"); got != 2 {
		t.Errorf("elapsed lines = %d, want 2 (one per tick between the 3 checks), got:\n%s", got, buf.String())
	}
}

func TestPollWithProgress_ReturnsCheckErrorImmediatelyWithNoExtraTick(t *testing.T) {
	var buf bytes.Buffer
	wantErr := errors.New("boom")
	err := pollWithProgress(context.Background(), &buf, "thing i-1", time.Second, testPollInterval, func(ctx context.Context) (bool, error) {
		return false, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if strings.Contains(buf.String(), "elapsed") {
		t.Errorf("expected no elapsed-time tick on an immediate check error, got:\n%s", buf.String())
	}
}

func TestPollWithProgress_TimesOutWithLabelInMessage(t *testing.T) {
	var buf bytes.Buffer
	err := pollWithProgress(context.Background(), &buf, "thing i-1", 30*time.Millisecond, testPollInterval, func(ctx context.Context) (bool, error) {
		return false, nil
	})
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "thing i-1") {
		t.Errorf("error = %v, want a timeout message mentioning the label", err)
	}
}

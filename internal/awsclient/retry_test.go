package awsclient

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/smithy-go"
)

func TestCallWithBackoff(t *testing.T) {
	throttled := &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}
	accessDenied := &smithy.GenericAPIError{Code: "AccessDenied", Message: "no"}

	tests := []struct {
		name      string
		failN     int   // number of leading calls that return throttled
		fatal     error // if set, every call returns this instead of throttled
		wantErr   bool
		wantCalls int
	}{
		{name: "succeeds first try", failN: 0, wantCalls: 1},
		{name: "succeeds after two throttles", failN: 2, wantCalls: 3},
		{name: "exhausts retries on persistent throttling", failN: 3, wantErr: true, wantCalls: 3},
		{name: "fails fast on non-throttling error", fatal: accessDenied, wantErr: true, wantCalls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			fn := func() (string, error) {
				calls++
				if tt.fatal != nil {
					return "", tt.fatal
				}
				if calls <= tt.failN {
					return "", throttled
				}
				return "ok", nil
			}

			result, err := callWithBackoff(context.Background(), 3, fn)

			if tt.wantErr && err == nil {
				t.Fatalf("expected an error, got result %q", result)
			}
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if result != "ok" {
					t.Errorf("result = %q, want %q", result, "ok")
				}
			}
			if calls != tt.wantCalls {
				t.Errorf("calls = %d, want %d", calls, tt.wantCalls)
			}
		})
	}
}

func TestIsThrottlingError(t *testing.T) {
	if !isThrottlingError(&smithy.GenericAPIError{Code: "ThrottlingException"}) {
		t.Error("ThrottlingException should be retryable")
	}
	if !isThrottlingError(&smithy.GenericAPIError{Code: "RequestLimitExceeded"}) {
		t.Error("RequestLimitExceeded should be retryable")
	}
	if isThrottlingError(&smithy.GenericAPIError{Code: "AccessDenied"}) {
		t.Error("AccessDenied should not be retryable")
	}
	if isThrottlingError(errors.New("plain error")) {
		t.Error("a non-API error should not be retryable")
	}
}

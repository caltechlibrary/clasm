package workflow

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aws/smithy-go"
)

func TestFormatError_UnwrapsAWSAPIError(t *testing.T) {
	apiErr := &smithy.GenericAPIError{Code: "UnauthorizedOperation", Message: "You are not authorized to perform this operation."}
	wrapped := fmt.Errorf("terminating instance i-1: %w", apiErr)

	got := formatError(wrapped)
	want := "AWS error [UnauthorizedOperation]: You are not authorized to perform this operation."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatError_FallsBackToPlainErrorString(t *testing.T) {
	err := errors.New("something local went wrong")
	if got := formatError(err); got != err.Error() {
		t.Errorf("got %q, want %q", got, err.Error())
	}
}

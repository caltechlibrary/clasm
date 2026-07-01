package workflow

import (
	"errors"

	"github.com/aws/smithy-go"
)

// formatError renders err for display to the user: an AWS API error
// (e.g. UnauthorizedOperation, InvalidAMIID.NotFound) is unwrapped to its
// code and message rather than shown as a raw, wrapped Go error string
// (see PLAN.md, Phase 15, "actionable error messages"). Anything else
// falls back to err.Error() unchanged.
func formatError(err error) string {
	if apiErr, ok := errors.AsType[smithy.APIError](err); ok {
		return "AWS error [" + apiErr.ErrorCode() + "]: " + apiErr.ErrorMessage()
	}
	return err.Error()
}

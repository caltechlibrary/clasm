package awsclient

import (
	"time"

	"github.com/caltechlibrary/clasm/internal/debuglog"
)

// logAWSCall runs call, then logs one "aws_call" record to dl (method,
// region, params, duration_ms, and either output or error) before
// returning call's result unchanged. Every Wrap* constructor below is a
// thin per-method dispatch onto this single helper (see DESIGN.md,
// "Debug Logging").
func logAWSCall[TOut any](dl *debuglog.DebugLog, method, region string, params any, call func() (TOut, error)) (TOut, error) {
	start := time.Now()
	out, err := call()

	fields := map[string]any{
		"method":      method,
		"region":      region,
		"params":      params,
		"duration_ms": time.Since(start).Milliseconds(),
	}
	if err != nil {
		fields["error"] = err.Error()
	} else {
		fields["output"] = out
	}
	dl.Log("aws_call", fields)

	return out, err
}

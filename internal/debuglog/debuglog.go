// Package debuglog writes a line-delimited JSON (JSONL) record of every
// AWS SDK call clasm makes, one JSON object per line, for the -debug
// flag (see DESIGN.md, "Debug Logging"). Every method is nil-receiver
// safe so callers never need an "if debug" guard around a log call --
// a nil *DebugLog is exactly what -debug=false produces, and every
// method on it is a no-op.
package debuglog

import (
	"encoding/json"
	"maps"
	"os"
	"sync"
	"time"
)

// DebugLog appends JSONL records to an open file. All methods are safe
// to call on a nil *DebugLog.
type DebugLog struct {
	mu   sync.Mutex
	file *os.File
}

// New opens path for writing (creating or truncating it) and returns a
// DebugLog backed by it.
func New(path string) (*DebugLog, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return &DebugLog{file: f}, nil
}

// DefaultPath returns a timestamped JSONL path in the current working
// directory, e.g. "clasm-debug-20260701-153012.jsonl".
func DefaultPath() string {
	return "clasm-debug-" + time.Now().Format("20060102-150405") + ".jsonl"
}

// Log writes one JSON object containing fields plus "time" (RFC3339Nano,
// UTC) and "event", followed by a newline. A nil *DebugLog, a nil fields
// map, or a marshal error are all silently ignored -- this is a
// debugging aid, not a path any workflow should fail on.
func (dl *DebugLog) Log(event string, fields map[string]any) {
	if dl == nil {
		return
	}

	record := make(map[string]any, len(fields)+2)
	maps.Copy(record, fields)
	record["time"] = time.Now().UTC().Format(time.RFC3339Nano)
	record["event"] = event

	line, err := json.Marshal(record)
	if err != nil {
		return
	}
	line = append(line, '\n')

	dl.mu.Lock()
	defer dl.mu.Unlock()
	dl.file.Write(line)
}

// Path returns the path of the open log file, or "" for a nil *DebugLog.
func (dl *DebugLog) Path() string {
	if dl == nil {
		return ""
	}
	return dl.file.Name()
}

// Close closes the underlying file. A nil *DebugLog returns nil.
func (dl *DebugLog) Close() error {
	if dl == nil {
		return nil
	}
	return dl.file.Close()
}

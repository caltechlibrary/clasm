package debuglog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLog_WritesOneJSONObjectPerLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.jsonl")
	dl, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dl.Log("aws_call", map[string]any{"method": "EC2.DescribeInstances", "region": "us-east-1"})
	dl.Log("aws_call", map[string]any{"method": "EC2.RunInstances", "region": "us-east-1"})

	if err := dl.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening log file: %v", err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%v", len(lines), lines)
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("line 0 is not valid JSON: %v", err)
	}
	if record["event"] != "aws_call" {
		t.Errorf("event = %v, want %q", record["event"], "aws_call")
	}
	if record["method"] != "EC2.DescribeInstances" {
		t.Errorf("method = %v, want %q", record["method"], "EC2.DescribeInstances")
	}
	if _, ok := record["time"]; !ok {
		t.Errorf("record missing a time field: %v", record)
	}
}

func TestLog_NilReceiverIsSafe(t *testing.T) {
	var dl *DebugLog
	dl.Log("aws_call", map[string]any{"method": "EC2.DescribeInstances"})
	if dl.Path() != "" {
		t.Errorf("Path() = %q, want empty", dl.Path())
	}
	if err := dl.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

func TestPath_ReturnsTheOpenedFilePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.jsonl")
	dl, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer dl.Close()

	if dl.Path() != path {
		t.Errorf("Path() = %q, want %q", dl.Path(), path)
	}
}

func TestDefaultPath_HasExpectedPrefixAndSuffix(t *testing.T) {
	got := DefaultPath()
	if !strings.HasPrefix(got, "clasm-debug-") {
		t.Errorf("DefaultPath() = %q, want prefix %q", got, "clasm-debug-")
	}
	if !strings.HasSuffix(got, ".jsonl") {
		t.Errorf("DefaultPath() = %q, want suffix %q", got, ".jsonl")
	}
}

func TestNew_PropagatesOpenError(t *testing.T) {
	_, err := New(filepath.Join(t.TempDir(), "does-not-exist", "debug.jsonl"))
	if err == nil {
		t.Fatal("expected an error opening a file in a non-existent directory")
	}
}

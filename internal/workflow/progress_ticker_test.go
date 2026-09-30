package workflow

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// asTerminal makes startProgressTicker treat every writer as a terminal for the
// duration of a test, so the animated path can be exercised into a buffer.
func asTerminal(t *testing.T) {
	t.Helper()
	old := writerIsTerminal
	writerIsTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { writerIsTerminal = old })
}

func TestStartProgressTicker_PrintsPeriodically(t *testing.T) {
	asTerminal(t)
	var buf bytes.Buffer

	stop := startProgressTicker(&buf, "waiting")
	time.Sleep(3 * DefaultSpinnerInterval)
	stop()

	out := buf.String()
	count := strings.Count(out, "waiting")
	if count < 2 {
		t.Errorf("got %d frames in output, want at least 2:\n%s", count, out)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{3*time.Minute + 7*time.Second, "3:07"},
		{0, "0:00"},
		{59 * time.Second, "0:59"},
		{time.Hour + 2*time.Minute + 5*time.Second, "1:02:05"},
		{90*time.Minute + 30*time.Second, "1:30:30"},
		// rounding: 500ms rounds up to 1s
		{500 * time.Millisecond, "0:01"},
	}
	for _, c := range cases {
		got := formatDuration(c.d)
		if got != c.want {
			t.Errorf("formatDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestStartProgressTicker_StopsCleanly(t *testing.T) {
	asTerminal(t)
	var buf bytes.Buffer

	stop := startProgressTicker(&buf, "waiting")
	stop()
	lenAfterStop := buf.Len()
	time.Sleep(3 * DefaultSpinnerInterval)

	if buf.Len() != lenAfterStop {
		t.Errorf("output grew after stop() returned: %d -> %d bytes", lenAfterStop, buf.Len())
	}
}

// Redirected to a file or a pipe -- a cron log, `2> log` -- the animated spinner
// wrote a cursor-escape redraw every 120 ms (1,553 of them in a three-minute
// wait). Not a terminal, the ticker writes plain lines: one at the start and one
// every plainProgressInterval.
func TestStartProgressTicker_PlainLinesWhenNotATerminal(t *testing.T) {
	old := plainProgressInterval
	plainProgressInterval = 20 * time.Millisecond
	defer func() { plainProgressInterval = old }()

	var buf bytes.Buffer // not an *os.File, so not a terminal
	stop := startProgressTicker(&buf, "waiting for the thing")
	time.Sleep(110 * time.Millisecond)
	stop()

	out := buf.String()
	if strings.ContainsAny(out, "\x1b\r") {
		t.Errorf("a non-terminal must get no escape or carriage-return bytes, got %q", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("want a start line and several periodic lines, got %d:\n%s", len(lines), out)
	}
	if lines[0] != "waiting for the thing ..." {
		t.Errorf("first line = %q", lines[0])
	}
	for _, l := range lines[1:] {
		if !strings.HasPrefix(l, "waiting for the thing (elapsed ") || !strings.HasSuffix(l, ")") {
			t.Errorf("periodic line = %q", l)
		}
	}
	if len(lines) > 12 {
		t.Errorf("%d lines in 110 ms at a 20 ms interval is far too chatty:\n%s", len(lines), out)
	}
}

func TestStartProgressTicker_PlainModeStopsCleanly(t *testing.T) {
	old := plainProgressInterval
	plainProgressInterval = 10 * time.Millisecond
	defer func() { plainProgressInterval = old }()

	var buf bytes.Buffer
	stop := startProgressTicker(&buf, "waiting")
	time.Sleep(35 * time.Millisecond)
	stop()
	n := buf.Len()
	time.Sleep(50 * time.Millisecond)
	if buf.Len() != n {
		t.Errorf("output grew after stop() returned: %d -> %d bytes", n, buf.Len())
	}
}

// The default is sparse enough for a log: half a minute between lines.
func TestPlainProgressInterval_DefaultIsHalfAMinute(t *testing.T) {
	if plainProgressInterval != 30*time.Second {
		t.Errorf("plainProgressInterval = %v, want 30s", plainProgressInterval)
	}
}

func TestWriterIsTerminal_BuffersAndPipesAreNot(t *testing.T) {
	var buf bytes.Buffer
	if writerIsTerminal(&buf) {
		t.Error("a bytes.Buffer is not a terminal")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if writerIsTerminal(w) {
		t.Error("a pipe is not a terminal")
	}
}

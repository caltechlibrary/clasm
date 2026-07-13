package workflow

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestStartProgressTicker_PrintsPeriodically(t *testing.T) {
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
	var buf bytes.Buffer

	stop := startProgressTicker(&buf, "waiting")
	stop()
	lenAfterStop := buf.Len()
	time.Sleep(3 * DefaultSpinnerInterval)

	if buf.Len() != lenAfterStop {
		t.Errorf("output grew after stop() returned: %d -> %d bytes", lenAfterStop, buf.Len())
	}
}

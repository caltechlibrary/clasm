package workflow

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/rsdoiel/termlib"
)

func TestStartProgressTicker_PrintsPeriodically(t *testing.T) {
	var buf bytes.Buffer
	term := termlib.New(&buf)

	stop := startProgressTicker(term, 10*time.Millisecond, "waiting")
	time.Sleep(35 * time.Millisecond)
	stop()

	out := buf.String()
	count := strings.Count(out, "waiting")
	if count < 2 {
		t.Errorf("got %d ticks in output, want at least 2:\n%s", count, out)
	}
}

func TestStartProgressTicker_StopsCleanly(t *testing.T) {
	var buf bytes.Buffer
	term := termlib.New(&buf)

	stop := startProgressTicker(term, 5*time.Millisecond, "waiting")
	stop()
	lenAfterStop := buf.Len()
	time.Sleep(30 * time.Millisecond)

	if buf.Len() != lenAfterStop {
		t.Errorf("output grew after stop() returned: %d -> %d bytes", lenAfterStop, buf.Len())
	}
}

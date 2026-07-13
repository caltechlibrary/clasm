package workflow

import (
	"fmt"
	"io"
	"time"
)

// formatDuration formats d as "m:ss", or "h:mm:ss" for durations of one
// hour or more, rounded to the nearest second -- replaces termlib's
// equivalent (DECISIONS.md, "Remove termlib entirely: input via huh,
// output via io.Writer").
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// startProgressTicker prints a periodic status line to w, giving visual
// feedback during long unbounded waits (AMI creation, cloud-init AMI
// extraction, backup upload/verify -- PLAN.md, Phase 15, "loading
// indicators"). The returned stop function blocks until the ticker
// goroutine has fully exited, so no tick can race with whatever output
// the caller writes immediately after stopping.
func startProgressTicker(w io.Writer, interval time.Duration, label string) (stop func()) {
	done := make(chan struct{})
	stopped := make(chan struct{})
	start := time.Now()

	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Fprintf(w, "  ... %s (elapsed %s)\n", label, formatDuration(time.Since(start)))
			}
		}
	}()

	return func() {
		close(done)
		<-stopped
	}
}

package workflow

import (
	"time"

	"github.com/rsdoiel/termlib"
)

// startProgressTicker prints a periodic status line to t, giving visual
// feedback during long unbounded waits (AMI creation, cloud-init AMI
// extraction, backup upload/verify -- PLAN.md, Phase 15, "loading
// indicators"). The returned stop function blocks until the ticker
// goroutine has fully exited, so no tick can race with whatever output
// the caller writes immediately after stopping.
func startProgressTicker(t *termlib.Terminal, interval time.Duration, label string) (stop func()) {
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
				t.Printf("  ... %s (elapsed %s)\n", label, termlib.FormatDuration(time.Since(start)))
				t.Refresh()
			}
		}
	}()

	return func() {
		close(done)
		<-stopped
	}
}

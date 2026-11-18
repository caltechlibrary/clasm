package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMultipassExecutor_LiveRoundTrip drives a real Multipass VM. It is skipped
// unless CLASM_LIVE_MULTIPASS_VM names a running VM that may be written to
// (scratch files under /var/tmp and /tmp only). Use a throwaway VM:
//
//	multipass launch 24.04 --name clasm-e2-scratch
//	CLASM_LIVE_MULTIPASS_VM=clasm-e2-scratch go test ./internal/workflow -run LiveRoundTrip -v
func TestMultipassExecutor_LiveRoundTrip(t *testing.T) {
	vm := os.Getenv("CLASM_LIVE_MULTIPASS_VM")
	if vm == "" {
		t.Skip("set CLASM_LIVE_MULTIPASS_VM to a running throwaway VM to run this")
	}
	ctx := context.Background()
	m := NewMultipassExecutor(vm)
	m.longStep = 20 * time.Second

	t.Run("run", func(t *testing.T) {
		out, status, err := m.Run(ctx, "id -un; uname -m; cat /etc/os-release | head -1", 60*time.Second, 2*time.Second)
		if err != nil || status != StatusSuccess || !strings.HasPrefix(out, "root\n") {
			t.Fatalf("out=%q status=%q err=%v; want to run as root", out, status, err)
		}
		t.Logf("guest says: %q", strings.ReplaceAll(out, "\n", " | "))
	})
	t.Run("nonzero", func(t *testing.T) {
		out, status, err := m.Run(ctx, "echo partial; exit 3", 60*time.Second, 2*time.Second)
		if err != nil || status != StatusFailed || out != "partial\n" {
			t.Errorf("out=%q status=%q err=%v", out, status, err)
		}
	})
	t.Run("stdin-and-quotes", func(t *testing.T) {
		out, status, err := m.Run(ctx, "cat <<'EOF'\nit's \"quoted\" $HOME\nEOF\ncat; echo end", 60*time.Second, 2*time.Second)
		if err != nil || status != StatusSuccess || out != "it's \"quoted\" $HOME\nend\n" {
			t.Errorf("out=%q status=%q err=%v", out, status, err)
		}
	})
	t.Run("returns-promptly", func(t *testing.T) {
		start := time.Now()
		for i := 0; i < 5; i++ {
			if _, _, err := m.Run(ctx, "true", 60*time.Second, 2*time.Second); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("5 short runs took %s", time.Since(start))
	})
	t.Run("timeout-kills", func(t *testing.T) {
		start := time.Now()
		_, _, err := m.Run(ctx, "sleep 30", 4*time.Second, 2*time.Second)
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("err = %v, want a timeout", err)
		}
		if el := time.Since(start); el > 15*time.Second {
			t.Errorf("a 4 s timeout took %s", el)
		}
	})
	t.Run("detached", func(t *testing.T) {
		out, status, err := m.Run(ctx, "sleep 3; echo long-done", 60*time.Second, 2*time.Second)
		if err != nil || status != StatusSuccess || out != "long-done\n" {
			t.Errorf("out=%q status=%q err=%v", out, status, err)
		}
	})
	t.Run("put-and-get", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "src.bin")
		payload := make([]byte, 3<<20) // 3 MB: well past what a command could carry
		for i := range payload {
			payload[i] = byte(i * 7)
		}
		if err := os.WriteFile(src, payload, 0o640); err != nil {
			t.Fatal(err)
		}
		remote := "/var/tmp/clasm-e2-live/dir/payload.bin"
		if err := m.Put(ctx, src, remote); err != nil {
			t.Fatalf("Put: %v", err)
		}
		out, status, err := m.Run(ctx, "stat -c '%U %a %s' "+remote, 60*time.Second, 2*time.Second)
		if err != nil || status != StatusSuccess || strings.TrimSpace(out) != "root 640 3145728" {
			t.Errorf("on the VM: %q status=%q err=%v, want \"root 640 3145728\"", out, status, err)
		}
		back := filepath.Join(t.TempDir(), "back.bin")
		if err := m.Get(ctx, remote, back); err != nil {
			t.Fatalf("Get: %v", err)
		}
		a, _ := fileSHA256(src)
		b, _ := fileSHA256(back)
		if a != b {
			t.Errorf("round trip changed the file: %s != %s", a, b)
		}
		m.Run(ctx, "rm -rf /var/tmp/clasm-e2-live", 60*time.Second, 2*time.Second)
	})
	t.Run("missing-vm", func(t *testing.T) {
		_, _, err := NewMultipassExecutor(vm+"-nope").Run(ctx, "true", 30*time.Second, 2*time.Second)
		if err == nil {
			t.Error("a VM that does not exist must be an error")
		}
		t.Logf("error for a missing VM: %v", err)
	})
}

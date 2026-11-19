package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// TestSSMExecutor_LiveRoundTrip drives a real EC2 instance over SSM. It is
// skipped unless CLASM_LIVE_SSM_INSTANCE names a running, SSM-managed throwaway
// instance (scratch files under /tmp only); CLASM_LIVE_SSM_REGION defaults to
// us-west-2. Use a small throwaway instance, never a production host:
//
//	CLASM_LIVE_SSM_INSTANCE=i-0123456789abcdef0 go test ./internal/executor -run SSMExecutor_Live -v
func TestSSMExecutor_LiveRoundTrip(t *testing.T) {
	id := os.Getenv("CLASM_LIVE_SSM_INSTANCE")
	if id == "" {
		t.Skip("set CLASM_LIVE_SSM_INSTANCE to a running throwaway EC2 instance to run this")
	}
	region := os.Getenv("CLASM_LIVE_SSM_REGION")
	if region == "" {
		region = "us-west-2"
	}
	ctx := context.Background()
	client, err := awsclient.NewSSMClient(ctx, region)
	if err != nil {
		t.Fatal(err)
	}
	e := NewSSMExecutor(client, id)

	t.Run("run", func(t *testing.T) {
		out, status, err := e.Run(ctx, "id -un; uname -m; head -1 /etc/os-release", 120*time.Second, 3*time.Second)
		if err != nil || status != StatusSuccess || !strings.HasPrefix(out, "root\n") {
			t.Fatalf("out=%q status=%q err=%v; want to run as root", out, status, err)
		}
		t.Logf("host says: %q", strings.ReplaceAll(out, "\n", " | "))
	})
	t.Run("nonzero", func(t *testing.T) {
		out, status, err := e.Run(ctx, "echo partial; exit 3", 120*time.Second, 3*time.Second)
		if err != nil || status != StatusFailed || !strings.Contains(out, "partial") {
			t.Errorf("out=%q status=%q err=%v", out, status, err)
		}
	})
	t.Run("quotes", func(t *testing.T) {
		out, status, err := e.Run(ctx, "cat <<'EOF'\nit's \"quoted\" $HOME\nEOF", 120*time.Second, 3*time.Second)
		if err != nil || status != StatusSuccess || out != "it's \"quoted\" $HOME\n" {
			t.Errorf("out=%q status=%q err=%v", out, status, err)
		}
	})
	t.Run("put-verified", func(t *testing.T) {
		body := []byte("clasm live put\nline two with 'quotes' and $dollars\n")
		local := filepath.Join(t.TempDir(), "payload.txt")
		if err := os.WriteFile(local, body, 0o600); err != nil {
			t.Fatal(err)
		}
		remote := "/tmp/clasm-live-put.txt"
		if err := e.Put(ctx, local, remote); err != nil {
			t.Fatalf("Put: %v", err)
		}
		sum := sha256.Sum256(body)
		out, status, err := e.Run(ctx, "sha256sum "+remote+"; rm -f "+remote, 120*time.Second, 3*time.Second)
		if err != nil || status != StatusSuccess || !strings.HasPrefix(out, hex.EncodeToString(sum[:])) {
			t.Errorf("remote checksum: out=%q status=%q err=%v, want %x", out, status, err, sum)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		_, _, err := e.Run(ctx, "sleep 120", 5*time.Second, 2*time.Second)
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("err = %v, want a timeout", err)
		}
	})
	t.Run("get-unsupported", func(t *testing.T) {
		if err := e.Get(ctx, "/etc/hostname", filepath.Join(t.TempDir(), "x")); err == nil {
			t.Error("Get over SSM must say it is not supported")
		}
	})
}

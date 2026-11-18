package multipass

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/caltechlibrary/clasm/internal/executor"
)

// TestLive_Lifecycle runs the whole lifecycle against a real throwaway VM.
// It is skipped unless CLASM_LIVE_MULTIPASS_VM names a running VM that may be
// stopped, snapshotted and restored, and that a clone named
// <vm>-clone may be created from and purged.
func TestLive_Lifecycle(t *testing.T) {
	vm := os.Getenv("CLASM_LIVE_MULTIPASS_VM")
	if vm == "" {
		t.Skip("set CLASM_LIVE_MULTIPASS_VM to a throwaway VM to run this")
	}
	ctx := context.Background()
	c := New()
	clone := vm + "-clone"

	if _, err := c.Info(ctx, "clasm-no-such-vm"); err == nil {
		t.Fatal("a missing VM must be ErrNotFound")
	}
	d, err := c.Info(ctx, vm)
	if err != nil || d.State != "Running" || d.CPUs < 1 || d.MemoryTotal == 0 {
		t.Fatalf("running info: %+v %v", d, err)
	}
	res, err := WaitForCloudInit(ctx, executor.NewMultipassExecutor(vm), 2*time.Minute, 2*time.Second)
	if err != nil || res.Status != "done" {
		t.Fatalf("cloud-init: %+v %v", res, err)
	}
	if err := c.Snapshot(ctx, vm, "x", ""); err == nil {
		t.Fatal("a snapshot of a running VM must fail with multipass's message")
	} else {
		t.Logf("snapshot of a running VM: %v", err)
	}
	if err := c.Stop(ctx, vm); err != nil {
		t.Fatal(err)
	}
	if err := c.Snapshot(ctx, vm, "before", "live test"); err != nil {
		t.Fatal(err)
	}
	if d, _ := c.Info(ctx, vm); d.State != "Stopped" || d.Snapshots != 1 {
		t.Errorf("stopped info: %+v", d)
	}
	if err := c.Clone(ctx, vm, clone); err != nil {
		t.Fatal(err)
	}
	if err := c.Restore(ctx, vm, "before", true); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, clone, true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Info(ctx, clone); err == nil {
		t.Error("the purged clone must be gone")
	}
	if err := c.Start(ctx, vm); err != nil {
		t.Fatal(err)
	}
	all, err := c.List(ctx)
	if err != nil || len(all) == 0 {
		t.Fatalf("list: %v %v", all, err)
	}
}

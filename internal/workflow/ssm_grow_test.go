package workflow

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func TestSplitDiskAndPartition_NVMe(t *testing.T) {
	disk, partition, err := splitDiskAndPartition("/dev/nvme0n1p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if disk != "/dev/nvme0n1" || partition != "1" {
		t.Errorf("got (%q, %q), want (/dev/nvme0n1, 1)", disk, partition)
	}
}

func TestSplitDiskAndPartition_Xvd(t *testing.T) {
	disk, partition, err := splitDiskAndPartition("/dev/xvda1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if disk != "/dev/xvda" || partition != "1" {
		t.Errorf("got (%q, %q), want (/dev/xvda, 1)", disk, partition)
	}
}

func TestSplitDiskAndPartition_Sd(t *testing.T) {
	disk, partition, err := splitDiskAndPartition("/dev/sda1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if disk != "/dev/sda" || partition != "1" {
		t.Errorf("got (%q, %q), want (/dev/sda, 1)", disk, partition)
	}
}

func TestSplitDiskAndPartition_UnrecognizedLVMLogicalVolume(t *testing.T) {
	// DESIGN.md, "Configurable EBS Root Volume Size", Part 2: fail loud
	// rather than guess on a layout this project doesn't model (LVM).
	if _, _, err := splitDiskAndPartition("/dev/mapper/ubuntu--vg-ubuntu--lv"); err == nil {
		t.Fatal("expected an error for an LVM logical volume path")
	}
}

func TestSplitDiskAndPartition_UnrecognizedDeviceMapperNode(t *testing.T) {
	if _, _, err := splitDiskAndPartition("/dev/dm-0"); err == nil {
		t.Fatal("expected an error for a device-mapper node")
	}
}

func TestParseFindmntOutput_Valid(t *testing.T) {
	device, fstype, ok := parseFindmntOutput("/dev/xvda1 ext4\n")
	if !ok {
		t.Fatal("expected ok = true")
	}
	if device != "/dev/xvda1" || fstype != "ext4" {
		t.Errorf("got (%q, %q), want (/dev/xvda1, ext4)", device, fstype)
	}
}

func TestParseFindmntOutput_ExtraWhitespaceStillParses(t *testing.T) {
	device, fstype, ok := parseFindmntOutput("  /dev/nvme0n1p1    xfs  \n")
	if !ok {
		t.Fatal("expected ok = true")
	}
	if device != "/dev/nvme0n1p1" || fstype != "xfs" {
		t.Errorf("got (%q, %q), want (/dev/nvme0n1p1, xfs)", device, fstype)
	}
}

func TestParseFindmntOutput_EmptyIsNotOK(t *testing.T) {
	if _, _, ok := parseFindmntOutput(""); ok {
		t.Fatal("expected ok = false for empty output")
	}
}

func TestParseFindmntOutput_SingleTokenIsNotOK(t *testing.T) {
	if _, _, ok := parseFindmntOutput("/dev/xvda1\n"); ok {
		t.Fatal("expected ok = false for output missing the filesystem type")
	}
}

func TestRootFilesystemGrowCommand_Ext4(t *testing.T) {
	cmd, err := rootFilesystemGrowCommand("/dev/xvda", "1", "/dev/xvda1", "ext4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(cmd, "growpart /dev/xvda 1") || !strings.Contains(cmd, "resize2fs /dev/xvda1") {
		t.Errorf("cmd = %q, want growpart and resize2fs", cmd)
	}
}

func TestRootFilesystemGrowCommand_XFS(t *testing.T) {
	cmd, err := rootFilesystemGrowCommand("/dev/nvme0n1", "1", "/dev/nvme0n1p1", "xfs")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(cmd, "growpart /dev/nvme0n1 1") || !strings.Contains(cmd, "xfs_growfs /") {
		t.Errorf("cmd = %q, want growpart and xfs_growfs", cmd)
	}
}

func TestRootFilesystemGrowCommand_UnsupportedType(t *testing.T) {
	if _, err := rootFilesystemGrowCommand("/dev/xvda", "1", "/dev/xvda1", "btrfs"); err == nil {
		t.Fatal("expected an error for an unsupported filesystem type")
	}
}

func TestGrowRootFilesystem_SSMNotOnline_PrintsManualInstructions(t *testing.T) {
	fake := &fakeSSMClient{onlineAfterCalls: 0}
	var buf strings.Builder
	growRootFilesystem(context.Background(), &buf, fake, "i-1", 250, 30*time.Millisecond, 30*time.Millisecond, testPollInterval)

	out := buf.String()
	if !strings.Contains(out, "growpart") || !strings.Contains(out, "resize2fs") {
		t.Errorf("expected manual fallback instructions, got:\n%s", out)
	}
	if fake.sendCommandCalls() != 0 {
		t.Errorf("sendCommandCalls = %d, want 0 (never attempted since SSM isn't online)", fake.sendCommandCalls())
	}
}

func TestGrowRootFilesystem_DetectCommandFails_PrintsManualInstructions(t *testing.T) {
	fake := &fakeSSMClient{onlineAfterCalls: 1, finalStatus: types.CommandInvocationStatusFailed}
	var buf strings.Builder
	growRootFilesystem(context.Background(), &buf, fake, "i-1", 250, 30*time.Millisecond, 30*time.Millisecond, testPollInterval)

	out := buf.String()
	if !strings.Contains(out, "growpart") {
		t.Errorf("expected manual fallback instructions, got:\n%s", out)
	}
}

func TestGrowRootFilesystem_UnrecognizedDevice_PrintsManualInstructions(t *testing.T) {
	fake := &fakeSSMClient{
		onlineAfterCalls: 1,
		finalStatus:      types.CommandInvocationStatusSuccess,
		stdout:           "/dev/mapper/ubuntu--vg-ubuntu--lv ext4\n",
	}
	var buf strings.Builder
	growRootFilesystem(context.Background(), &buf, fake, "i-1", 250, 30*time.Millisecond, 30*time.Millisecond, testPollInterval)

	out := buf.String()
	if !strings.Contains(out, "growpart") {
		t.Errorf("expected manual fallback instructions, got:\n%s", out)
	}
	if fake.sendCommandCalls() != 1 {
		t.Errorf("sendCommandCalls = %d, want 1 (only the detect command -- never attempts growpart on an unrecognized layout)", fake.sendCommandCalls())
	}
}

func TestGrowRootFilesystem_UnsupportedFilesystem_PrintsManualInstructions(t *testing.T) {
	fake := &fakeSSMClient{
		onlineAfterCalls: 1,
		finalStatus:      types.CommandInvocationStatusSuccess,
		stdout:           "/dev/xvda1 btrfs\n",
	}
	var buf strings.Builder
	growRootFilesystem(context.Background(), &buf, fake, "i-1", 250, 30*time.Millisecond, 30*time.Millisecond, testPollInterval)

	out := buf.String()
	if !strings.Contains(out, "growpart") {
		t.Errorf("expected manual fallback instructions, got:\n%s", out)
	}
	if fake.sendCommandCalls() != 1 {
		t.Errorf("sendCommandCalls = %d, want 1 (only the detect command -- never attempts growpart on an unsupported filesystem)", fake.sendCommandCalls())
	}
}

func TestGrowRootFilesystem_Success_RunsGrowpartAndResize2fs(t *testing.T) {
	fake := &fakeSSMClient{
		onlineAfterCalls: 1,
		responses: []ssmCommandResponse{
			{substring: "findmnt", stdout: "/dev/xvda1 ext4\n", status: types.CommandInvocationStatusSuccess},
			{substring: "growpart", stdout: "", status: types.CommandInvocationStatusSuccess},
		},
	}
	var buf strings.Builder
	growRootFilesystem(context.Background(), &buf, fake, "i-1", 250, 30*time.Millisecond, 30*time.Millisecond, testPollInterval)

	out := buf.String()
	if strings.Contains(out, "manually") || strings.Contains(out, "skipping") {
		t.Errorf("expected a success message, not a manual fallback, got:\n%s", out)
	}
	if fake.sendCommandCalls() != 2 {
		t.Fatalf("sendCommandCalls = %d, want 2 (detect + act)", fake.sendCommandCalls())
	}
	actCommand := fake.sentCommands[1]
	if !strings.Contains(actCommand, "growpart /dev/xvda 1") || !strings.Contains(actCommand, "resize2fs /dev/xvda1") {
		t.Errorf("act command = %q, want growpart and resize2fs for /dev/xvda1", actCommand)
	}
}

func TestGrowRootFilesystem_ActCommandFails_PrintsManualInstructions(t *testing.T) {
	fake := &fakeSSMClient{
		onlineAfterCalls: 1,
		responses: []ssmCommandResponse{
			{substring: "findmnt", stdout: "/dev/xvda1 ext4\n", status: types.CommandInvocationStatusSuccess},
			{substring: "growpart", stdout: "", status: types.CommandInvocationStatusFailed},
		},
	}
	var buf strings.Builder
	growRootFilesystem(context.Background(), &buf, fake, "i-1", 250, 30*time.Millisecond, 30*time.Millisecond, testPollInterval)

	out := buf.String()
	if !strings.Contains(out, "growpart") || !strings.Contains(out, "manually") {
		t.Errorf("expected a manual-fallback message after a failed act command, got:\n%s", out)
	}
}

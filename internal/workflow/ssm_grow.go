package workflow

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// nvmeRootDevicePattern and sdOrXvdRootDevicePattern match the two root
// device naming schemes AWS EC2 actually uses for EBS root volumes --
// NVMe-backed ("/dev/nvme0n1p1") and Xen/legacy-backed ("/dev/xvda1",
// "/dev/sda1") -- confirmed by reading AWS's own EBS/NVMe naming docs,
// not assumed. Deliberately narrow: anything else (an LVM logical
// volume such as "/dev/mapper/ubuntu--vg-ubuntu--lv", a device-mapper
// node such as "/dev/dm-0", ...) is a layout this project doesn't
// model, so splitDiskAndPartition errors rather than guessing
// (DESIGN.md, "Configurable EBS Root Volume Size", Part 2: "fail loud,
// don't guess" takes priority over broader layout coverage here).
var (
	nvmeRootDevicePattern    = regexp.MustCompile(`^(/dev/nvme\d+n\d+)p(\d+)$`)
	sdOrXvdRootDevicePattern = regexp.MustCompile(`^(/dev/(?:sd|xvd)[a-z]+)(\d+)$`)
)

// splitDiskAndPartition splits a root device path (as reported by
// `findmnt -no SOURCE /` on the instance) into its underlying
// whole-disk device and partition number -- growpart's own argument
// shape (`growpart <disk> <partition-number>`), which needs the two
// split apart rather than the combined partition device path
// findmnt/df report.
func splitDiskAndPartition(device string) (disk, partition string, err error) {
	device = strings.TrimSpace(device)

	if m := nvmeRootDevicePattern.FindStringSubmatch(device); m != nil {
		return m[1], m[2], nil
	}
	if m := sdOrXvdRootDevicePattern.FindStringSubmatch(device); m != nil {
		return m[1], m[2], nil
	}
	return "", "", fmt.Errorf("unrecognized root device layout %q -- not a single partition directly on the whole disk", device)
}

// parseFindmntOutput parses `findmnt -no SOURCE,FSTYPE /`'s output --
// two whitespace-separated fields, the root partition's device path
// and its filesystem type. strings.Fields tolerates findmnt's own
// column padding since neither field can itself contain whitespace.
func parseFindmntOutput(output string) (device, fstype string, ok bool) {
	fields := strings.Fields(output)
	if len(fields) < 2 {
		return "", "", false
	}
	return fields[0], fields[1], true
}

// rootFilesystemGrowCommand builds the shell command that grows disk's
// partition (identified by device, the full partition device path) to
// fill the just-resized EBS volume, then grows fstype's filesystem to
// fill the partition. ext4 needs resize2fs pointed at the partition
// device; xfs's xfs_growfs instead takes a mount point (always "/"
// here, since this only ever runs against the root filesystem) --
// confirmed via each tool's own man page, not assumed. Errors for any
// other fstype rather than guessing a resize tool that might not even
// be installed.
func rootFilesystemGrowCommand(disk, partition, device, fstype string) (string, error) {
	switch fstype {
	case "ext2", "ext3", "ext4":
		return fmt.Sprintf("set -e; growpart %s %s; resize2fs %s", disk, partition, device), nil
	case "xfs":
		return fmt.Sprintf("set -e; growpart %s %s; xfs_growfs /", disk, partition), nil
	default:
		return "", fmt.Errorf("unsupported root filesystem type %q", fstype)
	}
}

// printManualGrowInstructions prints the same growpart/resize2fs/
// xfs_growfs commands the operator ran by hand for the production
// incident this feature closes (TODO.md) -- the fallback whenever
// automated growth can't proceed safely.
func printManualGrowInstructions(w io.Writer) {
	fmt.Fprintln(w, "Run the following on the instance (over SSH or SSM Session Manager) to finish growing the filesystem manually:")
	fmt.Fprintln(w, "  sudo growpart <disk> <partition-number>   # e.g. sudo growpart /dev/xvda 1")
	fmt.Fprintln(w, "  sudo resize2fs <partition>                # ext4, e.g. sudo resize2fs /dev/xvda1")
	fmt.Fprintln(w, "  # or, if the root filesystem is xfs:")
	fmt.Fprintln(w, "  sudo xfs_growfs /")
}

// growRootFilesystem automates the OS-side half of Resize Instance's
// Root Volume (DESIGN.md, "Configurable EBS Root Volume Size", Part 2):
// once ec2:ModifyVolume's change is usable, the root partition and
// filesystem still need to grow to actually use the extra space. Detects
// the root device and filesystem type via SSM (reusing WaitForSSMOnline/
// RunShellCommand, the same primitives checkCloudInitCompletion already
// uses), then runs growpart + resize2fs/xfs_growfs. Falls back to
// printing the equivalent manual commands -- never erroring the overall
// resize workflow -- whenever SSM isn't online, the detect command
// fails, the device layout isn't recognized (e.g. LVM), the filesystem
// type isn't supported, or the grow command itself fails: "fail loud,
// don't guess" (DESIGN.md) means every one of those cases hands control
// back to the operator rather than risking a partially-grown,
// inconsistent disk. onlineTimeout/commandTimeout/pollInterval are
// threaded through explicitly (rather than reusing the package's Default*
// constants directly) so tests can drive this against a fake SSM client
// without waiting out real production timeouts -- the same shape
// checkCloudInitCompletion already uses.
func growRootFilesystem(ctx context.Context, w io.Writer, client awsclient.SSMAPI, instanceID string, newGB int32, onlineTimeout, commandTimeout, pollInterval time.Duration) {
	online, err := WaitForSSMOnline(ctx, client, instanceID, onlineTimeout, pollInterval)
	if err != nil || !online {
		fmt.Fprintln(w, "SSM is not online for this instance; skipping automated filesystem growth.")
		printManualGrowInstructions(w)
		return
	}

	detectOut, status, err := RunShellCommand(ctx, client, instanceID, "findmnt -no SOURCE,FSTYPE /", commandTimeout, pollInterval)
	if err != nil || status != types.CommandInvocationStatusSuccess {
		fmt.Fprintln(w, "Could not detect the root filesystem's device and type; skipping automated growth.")
		printManualGrowInstructions(w)
		return
	}

	device, fstype, ok := parseFindmntOutput(detectOut)
	if !ok {
		fmt.Fprintf(w, "Unexpected output from the root-filesystem detection command (%q); skipping automated growth.\n", detectOut)
		printManualGrowInstructions(w)
		return
	}

	disk, partition, err := splitDiskAndPartition(device)
	if err != nil {
		fmt.Fprintf(w, "%v; skipping automated growth.\n", err)
		printManualGrowInstructions(w)
		return
	}

	cmd, err := rootFilesystemGrowCommand(disk, partition, device, fstype)
	if err != nil {
		fmt.Fprintf(w, "%v; skipping automated growth.\n", err)
		printManualGrowInstructions(w)
		return
	}

	fmt.Fprintf(w, "Growing %s (%s, partition %s of %s) to use the new %d GB...\n", device, fstype, partition, disk, newGB)
	if _, status, err := RunShellCommand(ctx, client, instanceID, cmd, commandTimeout, pollInterval); err != nil || status != types.CommandInvocationStatusSuccess {
		fmt.Fprintln(w, "Automated filesystem growth failed; finish it manually:")
		printManualGrowInstructions(w)
		return
	}
	fmt.Fprintln(w, "Filesystem grown successfully.")
}

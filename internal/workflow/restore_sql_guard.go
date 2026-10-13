package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// Restore SQL's file-location guard (DR-0187): before a restore replaces anything,
// read the backup's files_location rows, judge each S3 bucket they name writable
// or read-only for the target instance, and require each writable one to be
// confirmed. A restored database carries the file location `s3://caltechauthors`,
// so an instance that can write to that bucket would write to production's content
// after the restore.

// RestoreGuardClients are the clients the guard needs beyond the restore's own: EC2
// for the target's instance profile (by region) and IAM for its role, its access
// key's owner and the policy simulator. A restore without them is an error, never a
// silently unguarded one.
type RestoreGuardClients struct {
	EC2 map[string]awsclient.EC2API
	IAM awsclient.IAMAPI
}

func (g RestoreGuardClients) check() error {
	if g.IAM == nil || len(g.EC2) == 0 {
		return errors.New("the restore guard needs EC2 and IAM clients (it reads the target's instance profile and asks IAM's policy simulator); none were given")
	}
	return nil
}

// restoreLocationReport is what the guard found for one backup.
type restoreLocationReport struct {
	Locations []FileLocation
	Verdicts  []BucketWriteAccess // one per distinct S3 bucket, in order of first appearance
}

// Writable are the buckets the target can write to, in order.
func (r restoreLocationReport) Writable() []string {
	var out []string
	for _, v := range r.Verdicts {
		if v.Writable {
			out = append(out, v.Bucket)
		}
	}
	return out
}

// distinctS3Buckets are the S3 buckets the locations name, in order of first
// appearance.
func distinctS3Buckets(locs []FileLocation) []string {
	var buckets []string
	for _, l := range locs {
		if b, ok := s3BucketOfURI(l.URI); ok && !slices.Contains(buckets, b) {
			buckets = append(buckets, b)
		}
	}
	return buckets
}

// PlanLines is the report as lines of a restore plan.
func (r restoreLocationReport) PlanLines() []string {
	if len(r.Locations) == 0 {
		return []string{"the backup has no file locations: nothing for the location guard to check"}
	}
	parts := make([]string, len(r.Locations))
	for i, l := range r.Locations {
		parts[i] = fmt.Sprintf("%s -> %s", l.Name, l.URI)
		if _, ok := s3BucketOfURI(l.URI); !ok {
			parts[i] += " (not an S3 bucket, not checked)"
		}
	}
	lines := []string{"file locations the backup would restore: " + strings.Join(parts, "; ")}
	for _, v := range r.Verdicts {
		if v.Writable {
			lines = append(lines, fmt.Sprintf("bucket %s: WRITABLE (%s); the restored app would be able to write to it, so it needs --confirm %s",
				v.Bucket, strings.Join(v.Via, "; "), v.Bucket))
		} else {
			lines = append(lines, fmt.Sprintf("bucket %s: read-only (%s)", v.Bucket, strings.Join(v.Via, "; ")))
		}
	}
	return lines
}

// inspectRestoreLocations streams the backup through the target and judges every S3
// bucket its file locations name. It writes nothing to the instance and changes
// nothing, so a dry run can call it. Any failure -- the scan, an unreadable row --
// is an error: the guard does not pass on what it could not read.
func inspectRestoreLocations(ctx context.Context, guard RestoreGuardClients, ssmClient awsclient.SSMAPI, inst inventory.Instance, bucket, key string) (restoreLocationReport, error) {
	if err := guard.check(); err != nil {
		return restoreLocationReport{}, err
	}
	ec2Client, err := resolveEC2(guard.EC2, inst.Region)
	if err != nil {
		return restoreLocationReport{}, err
	}
	locs, err := ScanBackupFileLocations(ctx, ssmClient, inst.InstanceID, bucket, key)
	if err != nil {
		return restoreLocationReport{}, err
	}
	verdicts, err := CheckBucketWriteAccess(ctx, ec2Client, ssmClient, guard.IAM, inst.InstanceID, distinctS3Buckets(locs))
	if err != nil {
		return restoreLocationReport{}, err
	}
	return restoreLocationReport{Locations: locs, Verdicts: verdicts}, nil
}

// recheck reads the file locations of the downloaded file and refuses if it names
// an S3 bucket the report did not judge. The scan and the download are two reads
// of one object; this closes the gap between them, before anything is dropped.
func (r restoreLocationReport) recheck(ctx context.Context, ssmClient awsclient.SSMAPI, instanceID, sqlPath string) error {
	locs, err := ScanLocalFileLocations(ctx, ssmClient, instanceID, sqlPath)
	if err != nil {
		return fmt.Errorf("re-reading the downloaded backup's file locations failed, so nothing was dropped: %w", err)
	}
	judged := distinctS3Buckets(r.Locations)
	var unjudged []string
	for _, b := range distinctS3Buckets(locs) {
		if !slices.Contains(judged, b) {
			unjudged = append(unjudged, b)
		}
	}
	if len(unjudged) > 0 {
		return fmt.Errorf("the downloaded backup names bucket(s) %s that the earlier scan did not (the backup changed between the two reads); nothing was dropped, re-run to judge them", strings.Join(unjudged, ", "))
	}
	return nil
}

// warnAndAsk is the menu flow's version of the gate: print what was found; if the
// instance can write to a bucket, warn and ask to continue or abort. Continuing is
// the confirmation (DR-0187). It returns false when the operator aborts.
func (r restoreLocationReport) warnAndAsk(w io.Writer, input io.Reader, output io.Writer) (bool, error) {
	fmt.Fprintln(w, "\nFile location guard:")
	for _, line := range r.PlanLines() {
		fmt.Fprintf(w, "  - %s\n", line)
	}
	writable := r.Writable()
	if len(writable) == 0 {
		return true, nil
	}
	fmt.Fprintf(w, "\nWARNING: this instance can write to %s. The restored database points its file storage there, so once restored the app on this instance could write to it.\n", strings.Join(writable, ", "))
	ok, err := Confirm("Restore anyway?", WithConfirmIO(input, output))
	if err != nil {
		return false, err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
	}
	return ok, nil
}

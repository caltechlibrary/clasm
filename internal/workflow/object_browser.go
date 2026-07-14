package workflow

import (
	"context"
	"errors"
	"io"

	"github.com/charmbracelet/huh"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/filemanager"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/s3diff"
	"github.com/caltechlibrary/clasm/internal/tui"
)

// BrowseAndManageObjects runs the S3 domain's "Browse & Manage Objects"
// entry point (DESIGN.md 21.2-21.3; PLAN.md Phase 20.1): a Picker-tier
// bucket pre-flight (reusing Feature 17's already-fetched listing, and
// the same pickBucket/cancelledIsNil convention every other
// bucket-selection call site uses -- PLAN.md, Phase 20.19) followed by
// an optional local-directory link, then launches the scoped
// bubbletea file manager screen (internal/filemanager).
func BrowseAndManageObjects(ctx context.Context, w io.Writer, newS3Client func(ctx context.Context, region string) (awsclient.S3API, error), buckets []inventory.Bucket) error {
	if len(buckets) == 0 {
		return errors.New("no buckets found")
	}

	bucket, err := pickBucket(ctx, "Select a bucket", "Browse and manage this bucket's objects, optionally linked to a local directory.", buckets)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	client, err := newS3Client(ctx, bucket.Region)
	if err != nil {
		return err
	}

	var link bool
	confirmLink := huh.NewConfirm().
		Title("Link a local directory?").
		Value(&link)
	if err := runFieldWithHelp(confirmLink); err != nil {
		return huhCancelledIsNil(err)
	}

	var localDir string
	if link {
		input := huh.NewInput().
			Title("Local directory to link").
			Validate(s3diff.ValidateLocalDirectory).
			Value(&localDir)
		if err := runFieldWithHelp(input); err != nil {
			return huhCancelledIsNil(err)
		}
	}

	return filemanager.Run(ctx, filemanager.Config{
		Client:   client,
		Bucket:   bucket.Name,
		Region:   bucket.Region,
		LocalDir: localDir,
	})
}

// huhCancelledIsNil maps huh's ErrUserAborted (Ctrl+C/Esc during a
// pre-flight field) to a clean return, matching cancelledIsNil's
// existing convention for termlib's ui.PickList/Prompt cancellation.
func huhCancelledIsNil(err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		return nil
	}
	return err
}

// runFieldWithHelp runs field the same way its own Run() method would --
// wrapped in a single-field Group/Form -- except it leaves the form's
// help footer on. huh's Field.Run() shortcut (and the package-level
// huh.Run it calls) explicitly does `NewForm(NewGroup(field)).
// WithShowHelp(false)`, which is why this pre-flight's bucket picker
// showed no keybinding hints at all; every other huh usage in this
// codebase should call this helper instead of a bare field.Run().
func runFieldWithHelp(field huh.Field) error {
	return huh.NewForm(huh.NewGroup(field)).WithTheme(tui.Theme()).Run()
}

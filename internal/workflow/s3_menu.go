package workflow

import (
	"context"

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/clasm/internal/ui"
)

// S3Actions bundles the S3 domain's menu entry points, mirroring
// KeyMgmtActions' shape.
type S3Actions struct {
	CreateBucket            func(ctx context.Context) error
	ConfigureWebsite        func(ctx context.Context) error
	SyncDirectory           func(ctx context.Context) error
	BrowseObjects           func(ctx context.Context) error
	ManageLifecyclePolicies func(ctx context.Context) error
	DeleteObjectsByPrefix   func(ctx context.Context) error
	DeleteBucket            func(ctx context.Context) error
	// Refresh re-fetches and re-displays the bucket listing. Called once
	// after every successful dispatched action (DECISIONS.md, "Refresh
	// data after each operation"), and directly for the "Show resource
	// lists" menu item itself.
	Refresh func(ctx context.Context) error
}

// s3Item pairs an S3 menu label with the S3Actions field it dispatches
// to; action is nil for "Back to domain picker".
type s3Item struct {
	label  string
	action func(S3Actions, context.Context) error
}

// s3MenuItems is DESIGN.md's S3 domain menu, in order. "Show resource
// lists" leads the menu, same convention as Compute and Key Management.
var s3MenuItems = []s3Item{
	{"Show resource lists", func(a S3Actions, ctx context.Context) error { return a.Refresh(ctx) }},
	{"Create Bucket", func(a S3Actions, ctx context.Context) error { return a.CreateBucket(ctx) }},
	{"Configure Static Website Hosting", func(a S3Actions, ctx context.Context) error { return a.ConfigureWebsite(ctx) }},
	{"Sync Local Directory to Bucket", func(a S3Actions, ctx context.Context) error { return a.SyncDirectory(ctx) }},
	{"Browse/Manage Objects", func(a S3Actions, ctx context.Context) error { return a.BrowseObjects(ctx) }},
	{"Manage Bucket Lifecycle Policies", func(a S3Actions, ctx context.Context) error { return a.ManageLifecyclePolicies(ctx) }},
	{"Delete Objects by Prefix", func(a S3Actions, ctx context.Context) error { return a.DeleteObjectsByPrefix(ctx) }},
	{"Delete Bucket", func(a S3Actions, ctx context.Context) error { return a.DeleteBucket(ctx) }},
	{"Back to domain picker", nil},
}

func s3ItemLabel(item s3Item) string { return item.label }

// RunS3Menu runs the S3 domain's interactive menu loop, the same shape as
// RunKeyMgmtMenu: show the menu, dispatch the chosen action, refresh the
// listing after a successful dispatch, and repeat -- until "Back to
// domain picker" is chosen (returns ErrBackToDomainPicker) or an exit
// signal is hit (reported as nil). A single action's error is shown and
// the loop continues.
func RunS3Menu(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, actions S3Actions) error {
	for {
		if ctx.Err() != nil {
			printExiting(t)
			return nil
		}

		choice, err := ui.PickList(t, le, s3MenuItems, s3ItemLabel, "Choose an option")
		if err != nil {
			if isExitSignal(err) {
				printExiting(t)
				return nil
			}
			return err
		}

		if choice.action == nil {
			return ErrBackToDomainPicker
		}

		if err := choice.action(actions, ctx); err != nil {
			if isExitSignal(err) {
				printExiting(t)
				return nil
			}
			t.Printf("Error: %s\n", formatError(err))
			t.Refresh()
			continue
		}

		if err := actions.Refresh(ctx); err != nil {
			t.Printf("Error refreshing listings: %s\n", formatError(err))
			t.Refresh()
		}
	}
}

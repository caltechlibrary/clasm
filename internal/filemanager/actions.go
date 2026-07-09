package filemanager

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/caltechlibrary/clasm/internal/s3diff"
)

// overlayKind distinguishes the modal overlay's three shapes (DESIGN.md
// 21.4): a plain yes/no gate, a type-the-name-back destructive gate, and
// a running/finished progress readout.
type overlayKind int

const (
	overlayConfirm overlayKind = iota
	overlayConfirmDestructive
	overlayProgress
)

// pendingAction identifies which handler runs once an overlay's confirm
// gate is accepted.
type pendingAction int

const (
	actionDownload pendingAction = iota
	actionUpload
	actionDelete
	// actionSyncUpload and actionSyncDelete are Sync's two stages
	// (sync.go, DECISIONS.md Decision 2) -- distinct from actionUpload/
	// actionDelete because they operate on a plain key diff (s3diff.Diff)
	// rather than a pane's tagged entry rows.
	actionSyncUpload
	actionSyncDelete
	// actionUnlink (commandline.go's startUnlinkConfirm) is an instant
	// state change, not a background operation -- it never reaches
	// beginAction/overlayProgress, only overlayConfirm's accept branch.
	actionUnlink
)

// overlay is the modal progress/confirm surface centered over the pane
// area (DESIGN.md 21.4). Confirm/ConfirmDestructive gate the action;
// progress lines then scroll inside the same overlay, ending with an
// explicit dismiss (never an auto-dismiss timer, so a FAIL line can't be
// hidden by a timeout).
type overlay struct {
	kind   overlayKind
	title  string
	action pendingAction
	items  []entry // rows the action applies to (Download/Upload/Delete)

	// syncUpload/syncDelete carry Sync's pending key diff (sync.go)
	// through its two-stage confirm/progress flow.
	syncUpload []string
	syncDelete []string

	destructiveInput string // typed-back text for ConfirmDestructive
	mustMatch        string

	lines    []string
	done     bool // progress finished; awaiting a dismiss keypress
	progress <-chan progressLine
}

// progressLine is one update from a running action/find, sent over a
// channel from its background goroutine to the bubbletea Update loop
// (the standard bubbletea streaming-progress pattern).
type progressLine struct {
	text string
	done bool
	err  error
}

// opProgressMsg wraps one value read from a progressLine channel, plus
// whether the channel is still open, so Update can decide whether to
// keep listening.
type opProgressMsg struct {
	ch   <-chan progressLine
	line progressLine
	open bool
}

func waitForProgress(ch <-chan progressLine) tea.Cmd {
	return func() tea.Msg {
		line, open := <-ch
		return opProgressMsg{ch: ch, line: line, open: open}
	}
}

func (m *Model) handleOpProgress(msg opProgressMsg) (tea.Model, tea.Cmd) {
	if m.overlay == nil || m.overlay.progress == nil {
		return m, nil
	}
	if !msg.open {
		m.overlay.done = true
		return m, nil
	}
	if msg.line.text != "" {
		m.overlay.lines = append(m.overlay.lines, msg.line.text)
	}
	if msg.line.done {
		m.overlay.done = true
		return m, nil
	}
	return m, waitForProgress(msg.ch)
}

// handleOverlayKey handles input while an overlay is showing: Confirm
// (y/n), ConfirmDestructive (type the exact identifier, Enter to
// submit), or a running/finished progress readout (any key dismisses
// once done).
func (m *Model) handleOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	o := m.overlay
	key := msg.String()

	switch o.kind {
	case overlayConfirm:
		switch key {
		case "y", "Y":
			if o.action == actionUnlink {
				m.overlay = nil
				return m.applyLink("")
			}
			if o.action == actionSyncUpload {
				return m.beginSyncUpload(o.syncUpload, o.syncDelete)
			}
			return m.beginAction(o.action, o.items)
		case "n", "N", "esc":
			m.overlay = nil
		}
		return m, nil

	case overlayConfirmDestructive:
		switch key {
		case "enter":
			ok := o.destructiveInput == o.mustMatch
			m.overlay = nil
			if ok {
				if o.action == actionSyncDelete {
					return m.beginSyncDelete(o.syncDelete)
				}
				return m.beginAction(o.action, o.items)
			}
			m.status = "Cancelled -- identifier did not match."
		case "esc":
			m.overlay = nil
		case "backspace":
			if len(o.destructiveInput) > 0 {
				o.destructiveInput = o.destructiveInput[:len(o.destructiveInput)-1]
			}
		default:
			if len(msg.Runes) > 0 {
				o.destructiveInput += string(msg.Runes)
			}
		}
		return m, nil

	case overlayProgress:
		if o.done {
			// Any key dismisses; refresh whichever panes the action
			// touched. Tag-clearing happens here (not in the action's
			// background goroutine) since Model/pane state must only be
			// mutated from Update's single goroutine -- the goroutine
			// only ever sends text over the progress channel.
			if o.action == actionDelete {
				m.remote.clearTags()
			}
			// Sync's upload stage, once dismissed, advances straight to
			// the delete confirm stage if there are delete candidates
			// (Security Consideration #11: never bundle the two
			// confirms) instead of closing the overlay.
			if o.action == actionSyncUpload && len(o.syncDelete) > 0 {
				return m.presentSyncStage(nil, o.syncDelete)
			}
			m.overlay = nil
			return m, tea.Batch(m.refreshAfterAction(o.action)...)
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) refreshAfterAction(a pendingAction) []tea.Cmd {
	switch a {
	case actionUpload, actionDelete, actionSyncUpload, actionSyncDelete:
		return []tea.Cmd{m.loadRemoteCmd(m.remote.prefix)}
	case actionDownload:
		if m.local != nil {
			return []tea.Cmd{m.loadLocalCmd(m.local.prefix)}
		}
	}
	return nil
}

// startDownload gates Download (`d`) behind a plain Confirm: requires
// focus on the remote pane and a linked local directory as the
// destination (DESIGN.md 21.6).
func (m *Model) startDownload() (tea.Model, tea.Cmd) {
	if m.focus != sideRemote {
		m.status = "Download applies to bucket objects -- focus the S3 pane."
		return m, nil
	}
	if m.local == nil {
		m.status = "Link a local directory first (L) to choose a download destination."
		return m, nil
	}
	items := filesOnly(m.remote.taggedOrCurrent())
	if len(items) == 0 {
		m.status = "Nothing to download."
		return m, nil
	}
	m.overlay = &overlay{
		kind:   overlayConfirm,
		title:  fmt.Sprintf("Download %s to %s?", describeTargets(items, "object"), joinKey(m.local.root, m.local.prefix)),
		action: actionDownload,
		items:  items,
	}
	return m, nil
}

// startUpload gates Upload (`u`) behind a plain Confirm: requires focus
// on the local pane (only reachable when linked); destination is the
// remote pane's current prefix (DESIGN.md 21.6).
func (m *Model) startUpload() (tea.Model, tea.Cmd) {
	if m.local == nil || m.focus != sideLocal {
		m.status = "Upload applies to local files -- link a directory and focus its pane."
		return m, nil
	}
	items := filesOnly(m.local.taggedOrCurrent())
	if len(items) == 0 {
		m.status = "Nothing to upload."
		return m, nil
	}
	dest := m.bucket + "/" + m.remote.prefix
	m.overlay = &overlay{
		kind:   overlayConfirm,
		title:  fmt.Sprintf("Upload %s to %s?", describeTargets(items, "file"), dest),
		action: actionUpload,
		items:  items,
	}
	return m, nil
}

// startDelete gates Delete (`x`) behind ConfirmDestructive -- the
// operator types the bucket name (or the active prefix, if any) back
// exactly (DESIGN.md 21.6, matching Security Consideration #11).
func (m *Model) startDelete() (tea.Model, tea.Cmd) {
	if m.focus != sideRemote {
		m.status = "Delete applies to bucket objects -- focus the S3 pane."
		return m, nil
	}
	items := filesOnly(m.remote.taggedOrCurrent())
	if len(items) == 0 {
		m.status = "Nothing to delete."
		return m, nil
	}
	mustMatch := m.remote.prefix
	if mustMatch == "" {
		mustMatch = m.bucket
	}
	m.overlay = &overlay{
		kind:      overlayConfirmDestructive,
		title:     fmt.Sprintf("Permanently delete %s from %s. Type %q to confirm:", describeTargets(items, "object"), m.bucket, mustMatch),
		action:    actionDelete,
		items:     items,
		mustMatch: mustMatch,
	}
	return m, nil
}

// startShowMetadata runs `m` immediately (no confirm gate, matching
// Feature 21's original behavior) against the remote row under the
// cursor.
func (m *Model) startShowMetadata() (tea.Model, tea.Cmd) {
	if m.focus != sideRemote {
		return m, nil
	}
	e, ok := m.remote.current()
	if !ok || e.kind != kindFile {
		return m, nil
	}
	return m, m.headObjectCmd(e.key)
}

type metadataMsg struct {
	key  string
	text string
	err  error
}

func (m *Model) headObjectCmd(key string) tea.Cmd {
	client, bucket, ctx := m.client, m.bucket, m.ctx
	return func() tea.Msg {
		callCtx, cancel := s3diff.WithCallTimeout(ctx)
		defer cancel()
		out, err := client.HeadObject(callCtx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
		if err != nil {
			return metadataMsg{key: key, err: err}
		}
		lastModified := "unknown"
		if out.LastModified != nil {
			lastModified = out.LastModified.Format("2006-01-02T15:04:05Z07:00")
		}
		text := fmt.Sprintf("%s: %s, modified %s, content-type %s", key, formatBytes(aws.ToInt64(out.ContentLength)), lastModified, aws.ToString(out.ContentType))
		return metadataMsg{key: key, text: text}
	}
}

// beginAction starts the confirmed action's background work and shows
// the progress overlay it streams lines into.
func (m *Model) beginAction(a pendingAction, items []entry) (tea.Model, tea.Cmd) {
	ch := make(chan progressLine)
	m.overlay = &overlay{kind: overlayProgress, action: a, items: items, progress: ch}

	switch a {
	case actionDownload:
		go m.runDownload(items, ch)
	case actionUpload:
		go m.runUpload(items, ch)
	case actionDelete:
		go m.runDelete(items, ch)
	}
	return m, waitForProgress(ch)
}

// describeTargets summarizes items for a confirm prompt's title: the
// single item's key when there's exactly one, otherwise a count. Naming
// the exact object being acted on matters most for a single-item
// destructive action -- Feature 21's original single-object delete
// wizard did this ("Delete %s from %s?"), and folding that case into
// this screen's generic tagged-action flow shouldn't lose it; a bare
// "delete 1 object(s)" doesn't say which one.
func describeTargets(items []entry, noun string) string {
	if len(items) == 1 {
		return items[0].key
	}
	return fmt.Sprintf("%d %s(s)", len(items), noun)
}

// describeKeys is describeTargets for Sync's plain string-key diff
// (sync.go), which doesn't have entry rows to draw from.
func describeKeys(keys []string, noun string) string {
	if len(keys) == 1 {
		return keys[0]
	}
	return fmt.Sprintf("%d %s(s)", len(keys), noun)
}

func filesOnly(items []entry) []entry {
	out := items[:0:0]
	for _, e := range items {
		if e.kind == kindFile {
			out = append(out, e)
		}
	}
	return out
}

func (m *Model) runDownload(items []entry, ch chan<- progressLine) {
	defer close(ch)
	destDir := joinKey(m.local.root, m.local.prefix)
	var ok, failed int
	for i, e := range items {
		status := "OK"
		if err := downloadOne(m.ctx, m.client, m.bucket, e.key, filepath.Join(destDir, baseOf(e.key))); err != nil {
			status = "FAIL: " + err.Error()
			failed++
		} else {
			ok++
		}
		ch <- progressLine{text: fmt.Sprintf("  %d/%d %s %s", i+1, len(items), status, e.key)}
	}
	ch <- progressLine{text: fmt.Sprintf("Downloaded %d object(s), %d failed.", ok, failed), done: true}
}

func downloadOne(ctx context.Context, client interface {
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}, bucket, key, destPath string) error {
	// The transfer timeout has to span the whole download, not just the
	// initial GetObject call: the response body is streamed lazily via
	// io.Copy below, and that read is still governed by the same
	// request context -- canceling it right after GetObject returns
	// would abort the copy before any bytes were actually read.
	callCtx, cancel := s3diff.WithTransferTimeout(ctx)
	defer cancel()

	out, err := client.GetObject(callCtx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return err
	}
	defer out.Body.Close()

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, out.Body)
	return err
}

func (m *Model) runUpload(items []entry, ch chan<- progressLine) {
	defer close(ch)
	destPrefix := m.remote.prefix
	var ok, failed int
	for i, e := range items {
		key := joinKey(destPrefix, e.name)
		status := "OK"
		// Reuses s3diff.UploadFile (the same helper Sync uses) rather
		// than a second PutObject implementation -- it also infers
		// Content-Type and applies the transfer timeout.
		if err := s3diff.UploadFile(m.ctx, m.client, m.bucket, key, e.key); err != nil {
			status = "FAIL: " + err.Error()
			failed++
		} else {
			ok++
		}
		ch <- progressLine{text: fmt.Sprintf("  %d/%d %s %s", i+1, len(items), status, key)}
	}
	ch <- progressLine{text: fmt.Sprintf("Uploaded %d file(s), %d failed.", ok, failed), done: true}
}

func (m *Model) runDelete(items []entry, ch chan<- progressLine) {
	defer close(ch)
	var ok, failed int
	for i, e := range items {
		status := "OK"
		callCtx, cancel := s3diff.WithCallTimeout(m.ctx)
		_, err := m.client.DeleteObject(callCtx, &s3.DeleteObjectInput{Bucket: aws.String(m.bucket), Key: aws.String(e.key)})
		cancel()
		if err != nil {
			status = "FAIL: " + err.Error()
			failed++
		} else {
			ok++
		}
		ch <- progressLine{text: fmt.Sprintf("  %d/%d %s %s", i+1, len(items), status, e.key)}
	}
	ch <- progressLine{text: fmt.Sprintf("Deleted %d object(s), %d failed.", ok, failed), done: true}
}

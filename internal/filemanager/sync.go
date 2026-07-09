package filemanager

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/caltechlibrary/clasm/internal/s3diff"
)

// startSync is Sync's entry point (DESIGN.md 21.3 "directly reachable"
// directory-mirroring, DECISIONS.md Decision 2): diff the entire linked
// local directory against the entire bucket by key+size (reusing
// s3diff.Compute/WalkLocalTree/ListAllBucketObjects -- the same logic
// the retired "Sync Local Directory to Bucket" wizard used, not
// reimplemented), then present the two-stage confirm gate.
func (m *Model) startSync() (tea.Model, tea.Cmd) {
	if m.local == nil {
		m.status = "Sync requires a linked local directory (l)."
		return m, nil
	}

	local, err := s3diff.WalkLocalTree(m.local.root)
	if err != nil {
		m.status = "Error: " + err.Error()
		return m, nil
	}
	remote, err := s3diff.ListAllBucketObjects(m.ctx, m.client, m.bucket)
	if err != nil {
		m.status = "Error: " + err.Error()
		return m, nil
	}

	diff := s3diff.Compute(local, remote)
	if len(diff.Upload) == 0 && len(diff.Delete) == 0 {
		m.status = "Nothing to sync -- local directory and bucket already match."
		return m, nil
	}
	return m.presentSyncStage(diff.Upload, diff.Delete)
}

// presentSyncStage shows Sync's next confirm gate: upload first (plain
// Confirm), then -- only once upload is done or skipped -- delete
// (ConfirmDestructive). Security Consideration #11 requires these never
// be bundled into one prompt, matching the retired wizard's own
// two-stage gate.
func (m *Model) presentSyncStage(upload, del []string) (tea.Model, tea.Cmd) {
	if len(upload) > 0 {
		m.overlay = &overlay{
			kind:       overlayConfirm,
			title:      fmt.Sprintf("Sync: upload %s to %s?", describeKeys(upload, "file"), m.bucket),
			action:     actionSyncUpload,
			syncUpload: upload,
			syncDelete: del,
		}
		return m, nil
	}
	if len(del) > 0 {
		m.overlay = &overlay{
			kind:       overlayConfirmDestructive,
			title:      fmt.Sprintf("Sync: permanently delete %s (bucket-only) from %s. Type %q to confirm:", describeKeys(del, "object"), m.bucket, m.bucket),
			action:     actionSyncDelete,
			syncDelete: del,
			mustMatch:  m.bucket,
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) beginSyncUpload(upload, del []string) (tea.Model, tea.Cmd) {
	ch := make(chan progressLine)
	m.overlay = &overlay{kind: overlayProgress, action: actionSyncUpload, syncUpload: upload, syncDelete: del, progress: ch}
	go m.runSyncUpload(upload, ch)
	return m, waitForProgress(ch)
}

func (m *Model) beginSyncDelete(del []string) (tea.Model, tea.Cmd) {
	ch := make(chan progressLine)
	m.overlay = &overlay{kind: overlayProgress, action: actionSyncDelete, syncDelete: del, progress: ch}
	go m.runSyncDelete(del, ch)
	return m, waitForProgress(ch)
}

func (m *Model) runSyncUpload(keys []string, ch chan<- progressLine) {
	defer close(ch)
	var ok, failed int
	for i, key := range keys {
		path := joinKey(m.local.root, key)
		status := "OK"
		if err := s3diff.UploadFile(m.ctx, m.client, m.bucket, key, path); err != nil {
			status = "FAIL: " + err.Error()
			failed++
		} else {
			ok++
		}
		ch <- progressLine{text: fmt.Sprintf("  %d/%d %s %s", i+1, len(keys), status, key)}
	}
	ch <- progressLine{text: fmt.Sprintf("Uploaded %d file(s), %d failed.", ok, failed), done: true}
}

func (m *Model) runSyncDelete(keys []string, ch chan<- progressLine) {
	defer close(ch)
	var ok, failed int
	for i, key := range keys {
		status := "OK"
		callCtx, cancel := s3diff.WithCallTimeout(m.ctx)
		_, err := m.client.DeleteObject(callCtx, &s3.DeleteObjectInput{Bucket: aws.String(m.bucket), Key: aws.String(key)})
		cancel()
		if err != nil {
			status = "FAIL: " + err.Error()
			failed++
		} else {
			ok++
		}
		ch <- progressLine{text: fmt.Sprintf("  %d/%d %s %s", i+1, len(keys), status, key)}
	}
	ch <- progressLine{text: fmt.Sprintf("Deleted %d object(s), %d failed.", ok, failed), done: true}
}

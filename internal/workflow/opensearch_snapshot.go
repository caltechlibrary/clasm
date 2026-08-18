package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// Default timeouts/intervals for Archive OpenSearch Snapshot to S3's SSM
// operations (PLAN.md Phase 20.49). Registering the repo and issuing the
// snapshot request are quick, single REST calls; polling for completion
// needs a long outer bound (an ~8GB snapshot can legitimately take a
// while) but a much coarser interval than a direct API poll, since each
// check here is itself a full SSM round trip -- not the sub-second
// cadence WaitForSSMOnline uses.
const (
	DefaultOpenSearchRESTTimeout     = 2 * time.Minute
	DefaultSnapshotStateCheckTimeout = 1 * time.Minute
	DefaultSnapshotPollInterval      = 30 * time.Second
	DefaultSnapshotCreateTimeout     = 2 * time.Hour
)

// buildRegisterRepoCommand builds the curl command that registers (or
// re-registers -- idempotent) an `fs`-type OpenSearch snapshot repository
// at location on the local filesystem, backed by the `path.repo`
// prerequisite documented in rdm-opensearch-path-repo-retrofit.md. Safe
// to call every run.
func buildRegisterRepoCommand(repo, location string) string {
	url := fmt.Sprintf("localhost:9200/_snapshot/%s", repo)
	body := fmt.Sprintf(`{"type":"fs","settings":{"location":%q}}`, location)
	return fmt.Sprintf("curl --fail-with-body -sS -X PUT %s -H 'Content-Type: application/json' -d %s",
		shellQuote(url), shellQuote(body))
}

// curlFailureError formats a non-Success SSM invocation status for one of
// this file's curl-over-SSM calls, including the captured response body
// -- every build*Command function uses `--fail-with-body`, so on a
// non-2xx HTTP response stdout still carries OpenSearch's own JSON error
// (e.g. "location doesn't match any of the locations specified by
// path.repo"), not just curl's own generic "exit status 22". Without
// this, a real incident (2026-07-29, CaltechAUTHORS production) required
// manually inspecting the --debug JSONL log to find the actual cause.
func curlFailureError(action string, status ssmtypes.CommandInvocationStatus, stdout string) error {
	if strings.TrimSpace(stdout) == "" {
		return fmt.Errorf("%s (status: %s)", action, status)
	}
	return fmt.Errorf("%s (status: %s): %s", action, status, strings.TrimSpace(stdout))
}

// RegisterSnapshotRepo runs buildRegisterRepoCommand via SSM and errors
// on a non-Success SSM invocation status.
func RegisterSnapshotRepo(ctx context.Context, client awsclient.SSMAPI, instanceID, repo, location string, timeout, pollInterval time.Duration) error {
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildRegisterRepoCommand(repo, location), timeout, pollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return curlFailureError(fmt.Sprintf("registering snapshot repo %q on %s failed", repo, instanceID), status, stdout)
	}
	return nil
}

// buildCreateSnapshotCommand builds the curl command that starts a new
// snapshot named snapshotName in repo, scoped to indices (comma-joined).
// ignore_unavailable is true so a wrong/renamed pattern degrades to
// "snapshots zero matching indices" rather than failing the whole
// request outright -- see PLAN.md Phase 20.49's own caution about this
// tradeoff (a wrong prefix assumption would fail quietly, not loudly).
// include_global_state is false -- this is a per-project data snapshot,
// not a cluster-wide one. No wait_for_completion: completion is polled
// separately via buildSnapshotStateCommand, since a long-running
// snapshot could easily exceed any single SSM command's own timeout.
func buildCreateSnapshotCommand(repo, snapshotName string, indices []string) string {
	url := fmt.Sprintf("localhost:9200/_snapshot/%s/%s", repo, snapshotName)
	body := fmt.Sprintf(`{"indices":%q,"ignore_unavailable":true,"include_global_state":false}`, strings.Join(indices, ","))
	return fmt.Sprintf("curl --fail-with-body -sS -X PUT %s -H 'Content-Type: application/json' -d %s",
		shellQuote(url), shellQuote(body))
}

// CreateSnapshot runs buildCreateSnapshotCommand via SSM and errors on a
// non-Success SSM invocation status. Returns once the snapshot request
// has been accepted -- it does not wait for the snapshot itself to
// finish; see PollSnapshotUntilComplete for that.
func CreateSnapshot(ctx context.Context, client awsclient.SSMAPI, instanceID, repo, snapshotName string, indices []string, timeout, pollInterval time.Duration) error {
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildCreateSnapshotCommand(repo, snapshotName, indices), timeout, pollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return curlFailureError(fmt.Sprintf("creating snapshot %s/%s on %s failed", repo, snapshotName, instanceID), status, stdout)
	}
	return nil
}

// buildSnapshotStateCommand builds the curl command that fetches a
// snapshot's current state -- the plain `GET /_snapshot/<repo>/<name>`
// endpoint, deliberately not `_status`: it's cheaper (no per-shard file
// stats) and still returns a top-level `state` field directly.
func buildSnapshotStateCommand(repo, snapshotName string) string {
	url := fmt.Sprintf("localhost:9200/_snapshot/%s/%s", repo, snapshotName)
	return fmt.Sprintf("curl --fail-with-body -sS -X GET %s", shellQuote(url))
}

// snapshotStateResponse is the slice of OpenSearch's `GET
// /_snapshot/<repo>/<name>` response body parseSnapshotState actually
// needs.
type snapshotStateResponse struct {
	Snapshots []struct {
		State string `json:"state"`
	} `json:"snapshots"`
}

// parseSnapshotState extracts the `state` field (IN_PROGRESS/SUCCESS/
// PARTIAL/FAILED) from a `GET /_snapshot/<repo>/<name>` response body.
// Errors on malformed JSON or a body with no snapshots entry -- both
// indicate something unexpected happened, not a legitimate in-progress
// state.
func parseSnapshotState(body []byte) (string, error) {
	var resp snapshotStateResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parsing snapshot state response: %w", err)
	}
	if len(resp.Snapshots) == 0 {
		return "", fmt.Errorf("snapshot state response has no snapshots entry: %s", string(body))
	}
	return resp.Snapshots[0].State, nil
}

// PollSnapshotUntilComplete polls buildSnapshotStateCommand once every
// pollInterval, via a fresh SSM round trip each time (each check its own
// RunShellCommand call, bounded by DefaultSnapshotStateCheckTimeout, not
// the outer timeout), until the snapshot reaches SUCCESS, an error state
// (FAILED/PARTIAL -- returned as an error, not just a timeout), or the
// overall timeout elapses (also an error -- unlike WaitForSSMOnline's
// clean-skip timeout, a snapshot that never finishes is a real problem
// this project has been burned by before with a too-short fixed timeout,
// PLAN.md Phase 20.49). Progress is printed to w throughout the wait via
// pollWithProgress (PLAN.md Phase 20.53) -- a real snapshot can take
// several minutes, and printing nothing until the very end reads as a
// hung prompt (confirmed live 2026-08-17, DECISIONS.md, "Poll-loop
// progress output...").
func PollSnapshotUntilComplete(ctx context.Context, w io.Writer, client awsclient.SSMAPI, instanceID, repo, snapshotName string, timeout, pollInterval time.Duration) (string, error) {
	command := buildSnapshotStateCommand(repo, snapshotName)
	label := fmt.Sprintf("snapshot %s/%s on %s", repo, snapshotName, instanceID)

	var finalState string
	err := pollWithProgress(ctx, w, label, timeout, pollInterval, func(ctx context.Context) (bool, error) {
		stdout, status, err := RunShellCommand(ctx, client, instanceID, command, DefaultSnapshotStateCheckTimeout, DefaultSSMPollInterval)
		if err != nil {
			return false, err
		}
		if status != ssmtypes.CommandInvocationStatusSuccess {
			return false, curlFailureError(fmt.Sprintf("snapshot state check for %s/%s on %s failed", repo, snapshotName, instanceID), status, stdout)
		}
		state, err := parseSnapshotState([]byte(stdout))
		if err != nil {
			return false, err
		}
		switch state {
		case "SUCCESS":
			finalState = state
			return true, nil
		case "FAILED", "PARTIAL":
			return false, fmt.Errorf("snapshot %s/%s ended in state %s", repo, snapshotName, state)
		}
		return false, nil
	})
	if err != nil {
		return "", err
	}
	return finalState, nil
}

// buildDeleteSnapshotCommand builds the curl command that deletes
// snapshotName from repo via OpenSearch's own snapshot API -- never a
// raw `rm` on the repo directory (DESIGN.md, "Archive OpenSearch
// Snapshot to S3", step 9).
func buildDeleteSnapshotCommand(repo, snapshotName string) string {
	url := fmt.Sprintf("localhost:9200/_snapshot/%s/%s", repo, snapshotName)
	return fmt.Sprintf("curl --fail-with-body -sS -X DELETE %s", shellQuote(url))
}

// DeleteSnapshot runs buildDeleteSnapshotCommand via SSM and errors on a
// non-Success SSM invocation status.
func DeleteSnapshot(ctx context.Context, client awsclient.SSMAPI, instanceID, repo, snapshotName string, timeout, pollInterval time.Duration) error {
	stdout, status, err := RunShellCommand(ctx, client, instanceID, buildDeleteSnapshotCommand(repo, snapshotName), timeout, pollInterval)
	if err != nil {
		return err
	}
	if status != ssmtypes.CommandInvocationStatusSuccess {
		return curlFailureError(fmt.Sprintf("deleting snapshot %s/%s on %s failed", repo, snapshotName, instanceID), status, stdout)
	}
	return nil
}

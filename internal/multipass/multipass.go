// Package multipass drives the `multipass` command line: instance lifecycle
// and waiting for cloud-init. It knows nothing about AWS (DR-0201, phase C1).
package multipass

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/caltechlibrary/clasm/internal/executor"
)

var (
	// ErrNotFound is returned when Multipass has no instance of that name.
	ErrNotFound = errors.New("no such instance")
	// ErrCloudInit is returned when cloud-init finished in a failed state.
	ErrCloudInit = errors.New("cloud-init did not complete")
	// ErrUnavailable is returned when the multipass program cannot be run at all.
	ErrUnavailable = errors.New("multipass is unavailable")
	// ErrFailed is returned when multipass ran and refused or failed the request.
	ErrFailed = errors.New("multipass reported an error")
	// ErrWaitTimeout is returned when a wait ended at its deadline.
	ErrWaitTimeout = errors.New("timed out waiting")
)

// Runner runs `multipass` with args and returns its stdout. An error carries
// Multipass's own message.
type Runner func(ctx context.Context, args ...string) (string, error)

// Client is a handle on the multipass command line.
type Client struct{ run Runner }

// Instance is one row of `multipass list`.
type Instance struct {
	Name    string   `json:"name"`
	State   string   `json:"state"`
	Release string   `json:"release"`
	IPv4    []string `json:"ipv4"`
}

// Detail is `multipass info` for one instance. A stopped instance reports
// no resources, so those fields are zero.
type Detail struct {
	Instance
	ImageRelease string `json:"image_release"`
	CPUs         int    `json:"cpus"`
	MemoryUsed   int64  `json:"memory_used"`
	MemoryTotal  int64  `json:"memory_total"`
	DiskUsed     int64  `json:"disk_used"`
	DiskTotal    int64  `json:"disk_total"`
	Snapshots    int    `json:"snapshots"`
}

// LaunchOptions are the inputs to Launch; unset fields are left to
// Multipass's own defaults.
type LaunchOptions struct {
	Name      string
	CloudInit string
	CPUs      int
	Memory    string
	Disk      string
	Image     string
}

// CloudInitResult is the outcome of WaitForCloudInit.
type CloudInitResult struct {
	Status string   `json:"status"`
	Errors []string `json:"errors"`
}

// classed is an error that reads as its message and matches one of this
// package's sentinels under errors.Is.
type classed struct {
	msg   string
	class error
}

func (e *classed) Error() string { return e.msg }
func (e *classed) Unwrap() error { return e.class }

// terminalCodes matches the cursor and erase sequences `multipass` writes
// while it shows progress.
var terminalCodes = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// New returns a Client that runs the `multipass` found on PATH.
//
// @returns {*Client} the client
// @example
//
//	instances, err := New().List(ctx)
func New() *Client { return &Client{run: execRunner} }

// NewWithRunner returns a Client that sends every call through r, so a test
// sees the arguments and supplies the output.
//
// @param r {Runner} stands in for the multipass binary
// @returns {*Client} the client
func NewWithRunner(r Runner) *Client { return &Client{run: r} }

func (c *Client) runner() Runner {
	if c.run == nil {
		return execRunner
	}
	return c.run
}

// execRunner is the real runner. The context kills a hung multipass, and
// WaitDelay frees its pipes if a child kept them open.
func execRunner(ctx context.Context, args ...string) (string, error) {
	path, err := exec.LookPath("multipass")
	if err != nil {
		return "", &classed{msg: "multipass is not installed or not on PATH: " + err.Error(), class: ErrUnavailable}
	}
	cmd := exec.CommandContext(ctx, path, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return out.String(), fmt.Errorf("multipass %s: %w", strings.Join(args, " "), ctx.Err())
		}
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), errors.New(msg)
	}
	return out.String(), nil
}

// do runs multipass and cleans the error: no terminal control codes, and a
// "does not exist" answer becomes ErrNotFound.
func (c *Client) do(ctx context.Context, name string, args ...string) (string, error) {
	out, err := c.runner()(ctx, args...)
	if err != nil {
		msg := strings.TrimSpace(terminalCodes.ReplaceAllString(err.Error(), ""))
		switch {
		case errors.Is(err, ErrUnavailable):
			return out, err
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return out, err
		case strings.Contains(msg, "does not exist"):
			return out, &classed{msg: fmt.Sprintf("%q: %s", name, msg), class: ErrNotFound}
		}
		return out, &classed{msg: msg, class: ErrFailed}
	}
	return out, nil
}

func requireName(what, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s: a name is required", what)
	}
	return nil
}

// List returns every Multipass instance.
//
// @param ctx {context.Context} cancels the call
// @returns {[]Instance} the instances, empty when there are none
// @returns {error} why multipass could not be run or its output not read
// @example
//
//	instances, err := New().List(ctx)
func (c *Client) List(ctx context.Context) ([]Instance, error) {
	out, err := c.do(ctx, "", "list", "--format", "json")
	if err != nil {
		return nil, err
	}
	var doc struct {
		List []Instance `json:"list"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return nil, fmt.Errorf("reading `multipass list` output: %w", err)
	}
	if doc.List == nil {
		doc.List = []Instance{}
	}
	return doc.List, nil
}

// Info returns the detail of one instance.
//
// @param ctx {context.Context} cancels the call
// @param name {string} the instance name
// @returns {Detail} the instance's state and resources
// @returns {error} ErrNotFound when there is no such instance, or why it could not be read
// @example
//
//	d, err := New().Info(ctx, "caltechauthors-rsdoiel-v14")
func (c *Client) Info(ctx context.Context, name string) (Detail, error) {
	if err := requireName("info", name); err != nil {
		return Detail{}, err
	}
	out, err := c.do(ctx, name, "info", name, "--format", "json")
	if err != nil {
		return Detail{}, err
	}
	var doc struct {
		Info map[string]struct {
			State        string   `json:"state"`
			Release      string   `json:"release"`
			ImageRelease string   `json:"image_release"`
			CPUCount     string   `json:"cpu_count"`
			Snapshots    string   `json:"snapshot_count"`
			IPv4         []string `json:"ipv4"`
			Memory       struct {
				Used  int64 `json:"used"`
				Total int64 `json:"total"`
			} `json:"memory"`
			Disks map[string]struct {
				Used  string `json:"used"`
				Total string `json:"total"`
			} `json:"disks"`
		} `json:"info"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return Detail{}, fmt.Errorf("reading `multipass info` output: %w", err)
	}
	i, ok := doc.Info[name]
	if !ok {
		return Detail{}, fmt.Errorf("%q: %w", name, ErrNotFound)
	}
	d := Detail{
		Instance:     Instance{Name: name, State: i.State, Release: i.Release, IPv4: i.IPv4},
		ImageRelease: i.ImageRelease,
		MemoryUsed:   i.Memory.Used,
		MemoryTotal:  i.Memory.Total,
	}
	d.CPUs, _ = strconv.Atoi(i.CPUCount)
	d.Snapshots, _ = strconv.Atoi(i.Snapshots)
	for _, disk := range i.Disks {
		u, _ := strconv.ParseInt(disk.Used, 10, 64)
		t, _ := strconv.ParseInt(disk.Total, 10, 64)
		d.DiskUsed += u
		d.DiskTotal += t
	}
	return d, nil
}

// Launch creates and starts an instance. A cloud-init file that does not
// exist is refused before multipass is run.
//
// @param ctx {context.Context} cancels the call
// @param o {LaunchOptions} the name and optional sizing, image and cloud-init file
// @returns {error} why the instance was not launched, or nil
// @example
//
//	err := New().Launch(ctx, LaunchOptions{Name: "caltechauthors-rsdoiel-v14", CloudInit: "cloud-init-multipass.yaml", CPUs: 4, Memory: "8G", Disk: "80G"})
func (c *Client) Launch(ctx context.Context, o LaunchOptions) error {
	if err := requireName("launch", o.Name); err != nil {
		return err
	}
	if o.CloudInit != "" {
		if _, err := os.Stat(o.CloudInit); err != nil {
			return fmt.Errorf("cloud-init file %s: %w", o.CloudInit, err)
		}
	}
	args := []string{"launch"}
	if o.Image != "" {
		args = append(args, o.Image)
	}
	args = append(args, "--name", o.Name)
	if o.CloudInit != "" {
		args = append(args, "--cloud-init", o.CloudInit)
	}
	if o.CPUs > 0 {
		args = append(args, "--cpus", strconv.Itoa(o.CPUs))
	}
	if o.Memory != "" {
		args = append(args, "--memory", o.Memory)
	}
	if o.Disk != "" {
		args = append(args, "--disk", o.Disk)
	}
	_, err := c.do(ctx, o.Name, args...)
	return err
}

// Start starts a stopped instance.
//
// @param ctx {context.Context} cancels the call
// @param name {string} the instance name
// @returns {error} why it did not start, or nil
func (c *Client) Start(ctx context.Context, name string) error {
	if err := requireName("start", name); err != nil {
		return err
	}
	_, err := c.do(ctx, name, "start", name)
	return err
}

// Stop stops a running instance.
//
// @param ctx {context.Context} cancels the call
// @param name {string} the instance name
// @returns {error} why it did not stop, or nil
func (c *Client) Stop(ctx context.Context, name string) error {
	if err := requireName("stop", name); err != nil {
		return err
	}
	_, err := c.do(ctx, name, "stop", name)
	return err
}

// Delete deletes an instance; with purge it is removed for good, otherwise
// Multipass keeps it recoverable until purged.
//
// @param ctx {context.Context} cancels the call
// @param name {string} the instance name
// @param purge {bool} remove it permanently
// @returns {error} why it was not deleted, or nil
func (c *Client) Delete(ctx context.Context, name string, purge bool) error {
	if err := requireName("delete", name); err != nil {
		return err
	}
	args := []string{"delete"}
	if purge {
		args = append(args, "--purge")
	}
	_, err := c.do(ctx, name, append(args, name)...)
	return err
}

// Clone makes an independent copy of a stopped instance.
//
// @param ctx {context.Context} cancels the call
// @param src {string} the stopped instance to copy
// @param dst {string} the name of the copy
// @returns {error} why it was not cloned, or nil
func (c *Client) Clone(ctx context.Context, src, dst string) error {
	if err := requireName("clone source", src); err != nil {
		return err
	}
	if err := requireName("clone destination", dst); err != nil {
		return err
	}
	_, err := c.do(ctx, src, "clone", src, "--name", dst)
	return err
}

// Snapshot takes a named snapshot of a stopped instance.
//
// @param ctx {context.Context} cancels the call
// @param name {string} the stopped instance
// @param snap {string} the snapshot name
// @param comment {string} an optional comment
// @returns {error} why no snapshot was taken, or nil
func (c *Client) Snapshot(ctx context.Context, name, snap, comment string) error {
	if err := requireName("snapshot", name); err != nil {
		return err
	}
	if err := requireName("snapshot name", snap); err != nil {
		return err
	}
	args := []string{"snapshot", name, "--name", snap}
	if comment != "" {
		args = append(args, "--comment", comment)
	}
	_, err := c.do(ctx, name, args...)
	return err
}

// Restore returns a stopped instance to a snapshot. With destructive, the
// instance's current state is discarded without Multipass taking a safety
// snapshot first.
//
// @param ctx {context.Context} cancels the call
// @param name {string} the stopped instance
// @param snap {string} the snapshot to return to
// @param destructive {bool} discard the current state
// @returns {error} why it was not restored, or nil
func (c *Client) Restore(ctx context.Context, name, snap string, destructive bool) error {
	if err := requireName("restore", name); err != nil {
		return err
	}
	if err := requireName("restore snapshot", snap); err != nil {
		return err
	}
	args := []string{"restore"}
	if destructive {
		args = append(args, "--destructive")
	}
	_, err := c.do(ctx, name, append(args, name+"."+snap)...)
	return err
}

// WaitForCloudInit polls cloud-init on the host through ex until it
// finishes. It reads `extended_status`, not the plain status, which says
// "error" while cloud-init is still running. A host that is not answering
// yet, or answers unreadably, does not end the wait; only the deadline
// does. "done" and "degraded done" are success (the latter is reported in
// the result); "error" and "disabled" are ErrCloudInit.
//
// @param ctx {context.Context} cancels the wait
// @param ex {executor.Executor} reaches the instance
// @param wait {time.Duration} how long to wait in all
// @param poll {time.Duration} the pause between checks
// @returns {CloudInitResult} the last status read and any messages
// @returns {error} ErrCloudInit for a failed run, or why the wait ended without a result
// @example
//
//	res, err := WaitForCloudInit(ctx, executor.NewMultipassExecutor("vm1"), 20*time.Minute, 10*time.Second)
func WaitForCloudInit(ctx context.Context, ex executor.Executor, wait, poll time.Duration) (CloudInitResult, error) {
	deadline := time.Now().Add(wait)
	var last CloudInitResult
	for {
		stdout, _, err := ex.Run(ctx, "cloud-init status --format json", 30*time.Second, poll)
		if err == nil {
			if res, ok := parseCloudInit(stdout); ok {
				last = res
				switch res.Status {
				case "done", "degraded done":
					return res, nil
				case "error", "disabled":
					return res, fmt.Errorf("%w on %s: status %q: %s", ErrCloudInit, ex.Name(), res.Status, strings.Join(res.Errors, "; "))
				}
			}
		}
		if ctx.Err() != nil {
			return last, ctx.Err()
		}
		if !time.Now().Add(poll).Before(deadline) {
			return last, &classed{msg: fmt.Sprintf("timed out after %s waiting for cloud-init on %s (last status %q)", wait, ex.Name(), last.Status), class: ErrWaitTimeout}
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// parseCloudInit reads `cloud-init status --format json`, which may be
// preceded by other output.
func parseCloudInit(stdout string) (CloudInitResult, bool) {
	i := strings.Index(stdout, "{")
	if i < 0 {
		return CloudInitResult{}, false
	}
	var doc struct {
		Extended string   `json:"extended_status"`
		Errors   []string `json:"errors"`
	}
	if err := json.NewDecoder(strings.NewReader(stdout[i:])).Decode(&doc); err != nil || doc.Extended == "" {
		return CloudInitResult{}, false
	}
	return CloudInitResult{Status: doc.Extended, Errors: doc.Errors}, true
}

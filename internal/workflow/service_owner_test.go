package workflow

import (
	"context"
	"errors"
	"os/exec"
	"os/user"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func TestDefaultRDMServiceUser(t *testing.T) {
	if DefaultRDMServiceUser != "ubuntu" {
		t.Errorf("DefaultRDMServiceUser = %q, want %q (DR-0176 decision 1)", DefaultRDMServiceUser, "ubuntu")
	}
}

func TestBuildResolveServiceOwnerCommand_QuotesUser(t *testing.T) {
	got := buildResolveServiceOwnerCommand("odd user")
	if !strings.Contains(got, "'odd user'") {
		t.Errorf("command = %q, want the user shell-quoted", got)
	}
}

// The command is what SSM runs, and SSM runs it under /bin/sh, so run it
// under sh here rather than only asserting its text. The current user is
// the one account that is guaranteed to exist.
func TestBuildResolveServiceOwnerCommand_RunsUnderSh(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skipf("no current user: %v", err)
	}
	out, err := exec.Command("sh", "-c", buildResolveServiceOwnerCommand(me.Username)).Output()
	if err != nil {
		t.Fatalf("command failed for an existing user: %v", err)
	}
	want := me.Uid + " " + me.Gid
	if got := strings.TrimSpace(string(out)); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// The reason the command is written as separate assignments under
// `set -e`: `echo "$(id -u x) $(id -g x)"` exits 0 and prints " " when
// the user does not exist, because echo's own status is what counts. A
// missing service user has to fail the step, not produce two empty ids.
func TestBuildResolveServiceOwnerCommand_MissingUserFailsNonZero(t *testing.T) {
	out, err := exec.Command("sh", "-c", buildResolveServiceOwnerCommand("clasm-no-such-user-xyz")).Output()
	if err == nil {
		t.Fatalf("command succeeded for a user that does not exist; output %q", out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("wrote %q to stdout before failing; a failed lookup must print no ids", out)
	}
}

func TestResolveServiceOwner_ParsesUIDAndGID(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "1000 1001\n"}
	got, err := ResolveServiceOwner(context.Background(), fake, "i-1", time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := (ServiceOwner{UID: 1000, GID: 1001}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if fake.sendCommandCalls() != 1 {
		t.Errorf("expected exactly 1 SendCommand call, got %d", fake.sendCommandCalls())
	}
	if !strings.Contains(fake.lastCommandText, "ubuntu") {
		t.Errorf("sent %q, want it to look up %q", fake.lastCommandText, DefaultRDMServiceUser)
	}
}

// A non-Success status has to name both the user and the instance, and
// carry the remote output -- "id: 'ubuntu': no such user" is the whole
// diagnosis.
func TestResolveServiceOwner_FailedStatusNamesUserAndInstance(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed, stdout: "id: 'ubuntu': no such user"}
	_, err := ResolveServiceOwner(context.Background(), fake, "i-abc", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"ubuntu", "i-abc", "no such user"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestResolveServiceOwner_PropagatesTransportError(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", sendCommandErr: errors.New("network is unreachable")}
	_, err := ResolveServiceOwner(context.Background(), fake, "i-1", time.Second, testPollInterval)
	if err == nil || !strings.Contains(err.Error(), "network is unreachable") {
		t.Errorf("expected the transport error to propagate, got: %v", err)
	}
}

func TestResolveServiceOwner_RejectsMalformedOutput(t *testing.T) {
	for _, stdout := range []string{"", " ", "1000", "1000 1001 5", "ubuntu ubuntu", "-1 1001", "1000 x"} {
		fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: stdout}
		_, err := ResolveServiceOwner(context.Background(), fake, "i-1", time.Second, testPollInterval)
		if err == nil {
			t.Errorf("stdout %q: expected an error, got none", stdout)
			continue
		}
		if !strings.Contains(err.Error(), "i-1") {
			t.Errorf("stdout %q: error %q does not name the instance", stdout, err)
		}
	}
}

// DR-0176 decision 5: only the uid is compared. ubuntu's gid is 1001 on
// the current images while the container's is 1000, and that is fine.
func TestCheckOwnerMatchesOpenSearch(t *testing.T) {
	cases := []struct {
		name  string
		owner ServiceOwner
		ok    bool
	}{
		{"uid matches, gid differs (the real images)", ServiceOwner{UID: 1000, GID: 1001}, true},
		{"uid and gid both match", ServiceOwner{UID: 1000, GID: 1000}, true},
		{"uid differs", ServiceOwner{UID: 1001, GID: 1001}, false},
		{"uid differs but gid happens to match the container", ServiceOwner{UID: 1001, GID: 1000}, false},
		{"root", ServiceOwner{UID: 0, GID: 0}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckOwnerMatchesOpenSearch(c.owner)
			if c.ok && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !c.ok && err == nil {
				t.Errorf("expected an error for %+v", c.owner)
			}
		})
	}
}

// The message is the operator's whole guide, so it has to name both
// numbers and the account, and say there is a decision to make rather
// than a thing that will be fixed for them.
func TestCheckOwnerMatchesOpenSearch_MessageNamesBothUIDs(t *testing.T) {
	err := CheckOwnerMatchesOpenSearch(ServiceOwner{UID: 1234, GID: 1234})
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"1234", "1000", DefaultRDMServiceUser} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/ui"
)

func launchFake() *fakeEC2Client {
	return &fakeEC2Client{runInstancesID: "i-new", runningAfterCall: 1, publicIP: "1.2.3.4"}
}

func TestRunLaunchFromTemplateCLI_DefaultVersionAndConnectionInfo(t *testing.T) {
	fake := launchFake()
	clients := map[string]awsclient.EC2API{"us-west-2": fake}
	var out, eout bytes.Buffer
	if err := RunLaunchFromTemplateCLI(context.Background(), &out, &eout, clients, cliTemplates(), "authors-tmpl", "", ui.FormatText); err != nil {
		t.Fatal(err)
	}
	in := fake.lastRunInstancesInput
	if aws.ToString(in.LaunchTemplate.LaunchTemplateId) != "lt-1" || aws.ToString(in.LaunchTemplate.Version) != "$Default" {
		t.Errorf("RunInstances template = %+v, want lt-1 at $Default", in.LaunchTemplate)
	}
	if !strings.Contains(out.String(), "Instance i-new is running") || !strings.Contains(out.String(), "1.2.3.4") {
		t.Errorf("stdout should carry the connection info, got:\n%s", out.String())
	}
	// Progress is for a human watching; stdout stays the result alone.
	if strings.Contains(out.String(), "waiting") || !strings.Contains(eout.String(), "i-new") {
		t.Errorf("progress belongs on stderr; out=%q err=%q", out.String(), eout.String())
	}
}

func TestRunLaunchFromTemplateCLI_VersionSelectors(t *testing.T) {
	for given, want := range map[string]string{"2": "2", "v2": "2", "$Latest": "$Latest", "$Default": "$Default"} {
		fake := launchFake()
		clients := map[string]awsclient.EC2API{"us-west-2": fake}
		if err := RunLaunchFromTemplateCLI(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, clients, cliTemplates(), "lt-1", given, ui.FormatText); err != nil {
			t.Fatalf("%s: %v", given, err)
		}
		if got := aws.ToString(fake.lastRunInstancesInput.LaunchTemplate.Version); got != want {
			t.Errorf("version %q sent as %q, want %q", given, got, want)
		}
	}
}

func TestRunLaunchFromTemplateCLI_JSON(t *testing.T) {
	clients := map[string]awsclient.EC2API{"us-west-2": launchFake()}
	var out, eout bytes.Buffer
	if err := RunLaunchFromTemplateCLI(context.Background(), &out, &eout, clients, cliTemplates(), "authors-tmpl", "3", ui.FormatJSON); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout must be one JSON document: %v\n%s", err, out.String())
	}
	for k, want := range map[string]any{
		"instance_id": "i-new", "state": "running", "public_ip": "1.2.3.4", "region": "us-west-2",
		"template_id": "lt-1", "template_name": "authors-tmpl", "version": "3",
	} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v (in %v)", k, got[k], want, got)
		}
	}
}

// A template that matches nothing, or two, is the caller's mistake: exit 2 and
// nothing is launched (the client map is empty, so any AWS call would error).
func TestRunLaunchFromTemplateCLI_ResolutionFailuresLaunchNothing(t *testing.T) {
	fake := launchFake()
	clients := map[string]awsclient.EC2API{"us-west-2": fake}
	usageErr(t, RunLaunchFromTemplateCLI(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, clients, cliTemplates(), "nope", "", ui.FormatText), "nope")
	twins := append(cliTemplates(), cliTemplates()[0])
	twins[1].TemplateID = "lt-2"
	usageErr(t, RunLaunchFromTemplateCLI(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, clients, twins, "authors-tmpl", "", ui.FormatText), "ambiguous")
	if fake.lastRunInstancesInput != nil {
		t.Error("RunInstances was called for an unresolved template")
	}
}

func TestRunLaunchFromTemplateCLI_AWSFailureIsNotAUsageError(t *testing.T) {
	clients := map[string]awsclient.EC2API{"us-west-2": &fakeEC2Client{runInstancesErr: errors.New("InsufficientInstanceCapacity")}}
	err := RunLaunchFromTemplateCLI(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, clients, cliTemplates(), "lt-1", "", ui.FormatText)
	if err == nil || CLIExitCode(err) != 1 || !strings.Contains(err.Error(), "InsufficientInstanceCapacity") {
		t.Errorf("want exit 1 carrying the AWS error, got %v", err)
	}
	err = RunLaunchFromTemplateCLI(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, map[string]awsclient.EC2API{}, cliTemplates(), "lt-1", "", ui.FormatText)
	if err == nil || CLIExitCode(err) != 1 {
		t.Errorf("a region with no client is a failure (exit 1), got %v", err)
	}
}

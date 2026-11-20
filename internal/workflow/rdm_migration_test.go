package workflow

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/recipe"
	"github.com/caltechlibrary/clasm/internal/ui"
)

func TestParseRunRecipeArgs(t *testing.T) {
	a, err := ParseRunRecipeArgs([]string{"-json", "--apply", "--from", "load", "--confirm", "box", "--confirm", "i-1", "box", "/r/dir", "SNAP=rdm-1", "BUCKET=b"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Instance != "box" || a.Dir != "/r/dir" || !a.Apply || a.From != "load" || a.Format != ui.FormatJSON ||
		len(a.Confirms) != 2 || len(a.Params) != 2 || a.Params[0].Name != "SNAP" {
		t.Errorf("parsed = %+v", a)
	}
	if a, err := ParseRunRecipeArgs([]string{"box", "/r"}); err != nil || a.Apply || a.Format != ui.FormatText {
		t.Errorf("defaults: a dry run in text: %+v %v", a, err)
	}
	for name, args := range map[string][]string{
		"nothing":            nil,
		"only an instance":   {"box"},
		"bad parameter":      {"box", "/r", "lower=x"},
		"unknown flag":       {"--nope", "box", "/r"},
		"from without value": {"box", "/r", "--from"},
		"apply as a param":   {"box", "/r", "APPLY=yes"},
		"text and json":      {"-text", "-json", "box", "/r"},
	} {
		_, err := ParseRunRecipeArgs(args)
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Errorf("%s: want a *UsageError, got %v", name, err)
		}
	}
	var help *HelpRequested
	if _, err := ParseRunRecipeArgs([]string{"-h"}); !errors.As(err, &help) {
		t.Errorf("-h is a help request, got %v", err)
	}
}

type recipeRun struct {
	args  RunRecipeArgs
	tag   string // the instance's Environment tag; "" for none
	reply string // what every SSM command prints
}

func (r recipeRun) go_(t *testing.T) (ssm *fakeSSMClient, ec2 *fakeEC2Client, out, eout string, err error) {
	t.Helper()
	if r.reply == "" {
		r.reply = recipe.CannedReply(0)
	}
	ssm = &fakeSSMClient{commandID: "c", finalStatus: ssmtypes.CommandInvocationStatusSuccess, stdout: r.reply}
	ec2 = runningFake(nil)
	if r.tag != "" {
		ec2 = runningFake(environmentTag(r.tag))
	}
	if r.args.Instance == "" {
		r.args.Instance = "box"
	}
	var w, e bytes.Buffer
	err = RunRecipeCLI(context.Background(), &w, &e, healthClients(ssm), powerClients(ec2), healthInstances(), r.args, time.Millisecond)
	return ssm, ec2, w.String(), e.String(), err
}

func recipeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for n, b := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func rstep(name, changes string) string {
	return "#!/bin/bash\n# step: " + name + "\n# changes: " + changes + "\ntrue\n"
}

func TestRunRecipeCLI_ARecipeProblemFailsBeforeAnyAWSCall(t *testing.T) {
	cases := map[string]struct {
		dir  string
		want int
	}{
		"missing directory": {filepath.Join(t.TempDir(), "absent"), 66},
		"header-less step":  {recipeFixture(t, map[string]string{"00_a.sh": "echo hi\n"}), 65},
	}
	for name, c := range cases {
		ssm, ec2, _, _, err := recipeRun{args: RunRecipeArgs{Dir: c.dir, Apply: true}}.go_(t)
		if CLIExitCode(err) != c.want || ssm.sendCommandCallCount != 0 || ec2.describeCalls != 0 {
			t.Errorf("%s: exit %d (%v), ssm=%d ec2=%d", name, CLIExitCode(err), err, ssm.sendCommandCallCount, ec2.describeCalls)
		}
	}
}

func TestRunRecipeCLI_AnUnknownInstanceIsAUsageError(t *testing.T) {
	dir := recipeFixture(t, map[string]string{"00_a.sh": rstep("a", "nothing")})
	_, _, _, _, err := recipeRun{args: RunRecipeArgs{Instance: "nope", Dir: dir, Apply: true}}.go_(t)
	if CLIExitCode(err) != 2 {
		t.Errorf("exit %d (%v)", CLIExitCode(err), err)
	}
}

func TestRunRecipeCLI_ADryRunSendsNothingToTheHost(t *testing.T) {
	dir := recipeFixture(t, map[string]string{"00_load.sh": rstep("load", "database")})
	ssm, _, out, _, err := recipeRun{tag: "production", args: RunRecipeArgs{Dir: dir}}.go_(t)
	if err != nil || ssm.sendCommandCallCount != 0 {
		t.Fatalf("a dry run reads the tag and sends nothing: err=%v ssm=%d", err, ssm.sendCommandCallCount)
	}
	if !strings.Contains(out, "load") || !strings.Contains(out, "--confirm") {
		t.Errorf("the plan should name the step and the gate:\n%s", out)
	}
}

func TestRunRecipeCLI_ProductionAndStagedRefuseDatabaseStepsWithoutConfirm(t *testing.T) {
	dir := recipeFixture(t, map[string]string{"00_pre.sh": rstep("pre", "nothing"), "10_load.sh": rstep("load", "database")})
	for _, tag := range []string{"production", "staged"} {
		ssm, _, _, _, err := recipeRun{tag: tag, args: RunRecipeArgs{Dir: dir, Apply: true}}.go_(t)
		if CLIExitCode(err) != 1 || ssm.sendCommandCallCount != 0 {
			t.Errorf("%s: exit %d (%v), ssm calls %d: the gate refuses before any step", tag, CLIExitCode(err), err, ssm.sendCommandCallCount)
		}
	}
	for _, confirm := range []string{"box", "i-1"} {
		ssm, _, _, _, err := recipeRun{tag: "staged", args: RunRecipeArgs{Dir: dir, Apply: true, Confirms: []string{confirm}}}.go_(t)
		if err != nil || ssm.sendCommandCallCount == 0 {
			t.Errorf("--confirm %s: err=%v ssm=%d", confirm, err, ssm.sendCommandCallCount)
		}
	}
	ssm, _, _, _, err := recipeRun{tag: "production", args: RunRecipeArgs{Dir: dir, Apply: true, Confirms: []string{"other"}}}.go_(t)
	if CLIExitCode(err) != 2 || ssm.sendCommandCallCount != 0 {
		t.Errorf("a wrong --confirm is a usage error before any call: %d %v", CLIExitCode(err), err)
	}
}

func TestRunRecipeCLI_TestAndUntaggedInstancesNeedNoConfirm(t *testing.T) {
	dir := recipeFixture(t, map[string]string{"10_load.sh": rstep("load", "database")})
	for _, tag := range []string{"test", "development", ""} {
		ssm, _, _, _, err := recipeRun{tag: tag, args: RunRecipeArgs{Dir: dir, Apply: true}}.go_(t)
		if err != nil || ssm.sendCommandCallCount == 0 {
			t.Errorf("tag %q: err=%v ssm=%d", tag, err, ssm.sendCommandCallCount)
		}
	}
}

func TestRunRecipeCLI_JSONModePutsTheStepLinesOnStdout(t *testing.T) {
	dir := recipeFixture(t, map[string]string{"00_a.sh": rstep("a", "nothing")})
	_, _, out, eout, err := recipeRun{tag: "test", args: RunRecipeArgs{Dir: dir, Apply: true, Format: ui.FormatJSON}}.go_(t)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) != 1 || !strings.Contains(lines[0], `"step":"a"`) || !strings.Contains(lines[0], `"exit_class":"ok"`) {
		t.Errorf("stdout:\n%s", out)
	}
	if !strings.Contains(eout, "[1/1] a") {
		t.Errorf("progress goes to stderr in -json mode:\n%s", eout)
	}
}

func TestRunRecipeCLI_AStepFailureExitsWithItsClassCode(t *testing.T) {
	dir := recipeFixture(t, map[string]string{"00_a.sh": rstep("a", "nothing")})
	for code, want := range map[int]int{65: 65, 75: 75, 1: 1, 7: 70} {
		_, _, _, _, err := recipeRun{tag: "test", reply: recipe.CannedReply(code), args: RunRecipeArgs{Dir: dir, Apply: true}}.go_(t)
		if CLIExitCode(err) != want || err == nil || !strings.Contains(err.Error(), "--from a") {
			t.Errorf("step exit %d: process exit %d (%v), want %d with a resume hint", code, CLIExitCode(err), err, want)
		}
	}
}

func TestRunRecipeCLI_ParametersReachTheHost(t *testing.T) {
	dir := recipeFixture(t, map[string]string{"00_a.sh": rstep("a", "nothing")})
	params, err := recipe.ParseParams([]string{"SNAP=rdm-1", "BUCKET=sql.backups"})
	if err != nil {
		t.Fatal(err)
	}
	ssm, _, _, _, err := recipeRun{tag: "test", args: RunRecipeArgs{Dir: dir, Apply: true, Params: params}}.go_(t)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(ssm.sentCommands, "\n")
	const open, shut = "<<'__CLASM_DRIVER__'\n", "\n__CLASM_DRIVER__"
	i := strings.Index(all, open)
	if i < 0 {
		t.Fatal("no driver sent")
	}
	rest := all[i+len(open):]
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(rest[:strings.Index(rest, shut)], "\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"export SNAP='rdm-1'", "export BUCKET='sql.backups'", "export APPLY=yes"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the driver lacks %q:\n%s", want, raw)
		}
	}
}

// The interactive entry into the domain says the forms are CLI-only for now
// (the TUI shell over them is not built) and returns to the picker.
func TestRunRDMMigrationMenu_PointsAtTheCLIFormsAndReturnsToThePicker(t *testing.T) {
	var buf bytes.Buffer
	err := RunRDMMigrationMenu(context.Background(), &buf)
	if !errors.Is(err, ErrBackToDomainPicker) {
		t.Errorf("want ErrBackToDomainPicker, got %v", err)
	}
	for _, want := range []string{"clasm rdm-migration run-recipe", "--apply"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("the hint lacks %q:\n%s", want, buf.String())
		}
	}
}

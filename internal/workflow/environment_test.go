package workflow

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// Phase S1 (DR-0203): staged is a fourth Environment value, protected exactly
// like production, decided in one place.

func TestEnvironmentVocabularyIsClosedAndIncludesStaged(t *testing.T) {
	for _, ok := range []string{"production", "staged", "development", "test"} {
		if err := validateEnvironment(ok); err != nil {
			t.Errorf("%q must be accepted: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Staged", "stage", "prod", "PRODUCTION", "staging"} {
		if err := validateEnvironment(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	err := validateEnvironment("nope")
	if err == nil || !strings.Contains(err.Error(), "staged") {
		t.Errorf("the refusal must list staged, got %v", err)
	}
}

func TestIsProtectedEnvironment(t *testing.T) {
	want := map[string]bool{"production": true, "staged": true, "development": false, "test": false, "": false, "Staged": false}
	for value, protected := range want {
		if got := isProtectedEnvironment(value); got != protected {
			t.Errorf("isProtectedEnvironment(%q) = %v, want %v", value, got, protected)
		}
	}
}

func TestIsProtectedInstanceReadsTheLiveTag(t *testing.T) {
	for value, protected := range map[string]bool{"production": true, "staged": true, "test": false} {
		if got := isProtectedInstance(types.Instance{Tags: environmentTag(value)}); got != protected {
			t.Errorf("%s: got %v, want %v", value, got, protected)
		}
	}
	if isProtectedInstance(types.Instance{}) {
		t.Error("an untagged instance is not protected")
	}
}

// tag-resource must accept staged (DR-0203, "tag-resource and the launch flows accept staged").
func TestTagResourceAcceptsStagedEnvironment(t *testing.T) {
	if _, err := ParseTagResourceArgs([]string{"--tag", "Environment=staged", "instance", "n"}); err != nil {
		t.Errorf("Environment=staged must be accepted: %v", err)
	}
}

// Every destructive guard that warns or gates on production does the same on
// staged, and not on test.
func TestEveryGuardTreatsStagedLikeProduction(t *testing.T) {
	ctx := context.Background()
	type guard struct {
		name string
		run  func(env string) (out string, gated bool)
	}
	guards := []guard{
		{"terminate", func(env string) (string, bool) {
			inst := inventory.Instance{InstanceID: "i-1", Name: "web", State: "running", Environment: env, Region: "us-east-1"}
			term, le, buf := newPipeEditor("i-1\n")
			_ = terminateEC2Instance(ctx, term, map[string]awsclient.EC2API{"us-east-1": &fakeEC2Client{}}, inst, le, buf)
			return buf.String(), strings.Contains(strings.ToLower(buf.String()), "warning: this instance is tagged")
		}},
		{"remove-ami", func(env string) (string, bool) {
			img := inventory.Image{ImageID: "ami-1", Name: "base", Environment: env, Region: "us-east-1"}
			term, le, buf := newPipeEditor("ami-1\n")
			_ = removeAMI(ctx, term, map[string]awsclient.EC2API{"us-east-1": &fakeEC2Client{}}, img, nil, le, buf)
			return buf.String(), strings.Contains(buf.String(), "WARNING: this AMI is tagged")
		}},
		{"delete-launch-template", func(env string) (string, bool) {
			lt := inventory.LaunchTemplate{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1", Environment: env}
			var buf bytes.Buffer
			_ = deleteLaunchTemplate(ctx, &buf, map[string]awsclient.EC2API{"us-east-1": &fakeEC2Client{}}, lt, newHuhAccessibleInput("lt-1\n"), &buf)
			return buf.String(), strings.Contains(buf.String(), "WARNING: this launch template is tagged")
		}},
		{"delete-launch-template-versions", func(env string) (string, bool) {
			lt := inventory.LaunchTemplate{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1", Environment: env}
			var buf bytes.Buffer
			_ = deleteLaunchTemplateVersions(ctx, &buf, map[string]awsclient.EC2API{"us-east-1": &fakeEC2Client{}}, lt, newHuhAccessibleInput("2\nlt-1\n"), &buf)
			return buf.String(), strings.Contains(buf.String(), "WARNING: this launch template is tagged")
		}},
		{"stop-instance", func(env string) (string, bool) {
			f := runningFake(environmentTag(env))
			out, _, _ := runStop(f, "box", stopArgs{inventoryState: "running", input: failingReader{t}})
			return out, f.lastStopInstancesInput == nil // gated: a dry run sent nothing
		}},
		{"convert-log-indices", func(env string) (string, bool) {
			ssm, ec2 := cvFakes(nil, environmentTag(env))
			out, _ := runCVPublic(ssm, ec2, "box", nil)
			return out, strings.Contains(out, "Nothing was changed")
		}},
	}
	for _, g := range guards {
		for env, wantGated := range map[string]bool{"production": true, "staged": true, "test": false} {
			out, gated := g.run(env)
			if gated != wantGated {
				t.Errorf("%s with Environment=%s: gated=%v, want %v\n%s", g.name, env, gated, wantGated, out)
			}
		}
	}
}

// No guard may compare against the literal "production" itself: a new guard
// would then protect production and forget staged. Only environment.go may.
func TestNoGuardComparesTheProductionLiteral(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "environment.go" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, `== "production"`) || strings.Contains(line, `"production", "development"`) {
				t.Errorf("%s:%d compares or enumerates the production literal; use environment.go: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}

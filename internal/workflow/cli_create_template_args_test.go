package workflow

import (
	"errors"
	"strings"
	"testing"

	"github.com/caltechlibrary/clasm/internal/ui"
)

func validCreateArgs() []string {
	return []string{
		"--name", "caltechauthors-v14", "--ami", "ami-1", "--instance-type", "t3.micro", "--key-pair", "caltechauthors",
		"--security-group", "sg-1", "--subnet", "subnet-1", "--iam-instance-profile", "rdm-backups",
		"--name-tag", "caltechauthors-v14", "--environment", "test", "cloud-init.yaml",
	}
}

func TestParseCreateLaunchTemplateArgs_Valid(t *testing.T) {
	opts, path, err := ParseCreateLaunchTemplateArgs(validCreateArgs())
	if err != nil {
		t.Fatal(err)
	}
	want := CreateLaunchTemplateOptions{
		Name: "caltechauthors-v14", AMI: "ami-1", InstanceType: "t3.micro", KeyPair: "caltechauthors",
		SecurityGroups: []string{"sg-1"}, Subnet: "subnet-1", IAMInstanceProfile: "rdm-backups",
		NameTag: "caltechauthors-v14", Environment: "test", Format: ui.FormatText,
	}
	if opts.Name != want.Name || opts.AMI != want.AMI || opts.InstanceType != want.InstanceType || opts.KeyPair != want.KeyPair ||
		opts.Subnet != want.Subnet || opts.IAMInstanceProfile != want.IAMInstanceProfile || opts.NameTag != want.NameTag ||
		opts.Environment != want.Environment || opts.Format != want.Format || len(opts.SecurityGroups) != 1 || opts.SecurityGroups[0] != "sg-1" {
		t.Errorf("got %+v", opts)
	}
	if opts.Project != "" || opts.RootVolumeGB != 0 {
		t.Errorf("project and root volume default to empty/0 (resolved later from the AMI), got %q / %d", opts.Project, opts.RootVolumeGB)
	}
	if path != "cloud-init.yaml" {
		t.Errorf("path = %q", path)
	}
}

func TestParseCreateLaunchTemplateArgs_RepeatedAndCommaSeparatedSecurityGroups(t *testing.T) {
	args := append([]string{"--security-group", "sg-2,sg-3", "--security-group", "sg-4"}, validCreateArgs()...)
	opts, _, err := ParseCreateLaunchTemplateArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(opts.SecurityGroups, ","); got != "sg-2,sg-3,sg-4,sg-1" {
		t.Errorf("security groups = %q", got)
	}
}

func TestParseCreateLaunchTemplateArgs_OptionalOnes(t *testing.T) {
	args := append([]string{"--project", "authors", "--root-volume-gb", "250", "-json"}, validCreateArgs()...)
	opts, _, err := ParseCreateLaunchTemplateArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Project != "authors" || opts.RootVolumeGB != 250 || opts.Format != ui.FormatJSON {
		t.Errorf("got %+v", opts)
	}
}

func without(args []string, flag string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if args[i] == flag {
			i++ // drop its value too
			continue
		}
		out = append(out, args[i])
	}
	return out
}

// Every required option is named in the error when it is missing.
func TestParseCreateLaunchTemplateArgs_MissingRequiredOption(t *testing.T) {
	for _, flag := range []string{"--name", "--ami", "--instance-type", "--key-pair", "--security-group", "--subnet", "--iam-instance-profile", "--name-tag", "--environment"} {
		_, _, err := ParseCreateLaunchTemplateArgs(without(validCreateArgs(), flag))
		var ue *UsageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), flag) || CLIExitCode(err) != 2 {
			t.Errorf("without %s: want an exit-2 usage error naming it, got %v", flag, err)
		}
	}
}

func TestParseCreateLaunchTemplateArgs_UsageErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"no file":        without(validCreateArgs(), "cloud-init.yaml"),
		"two files":      append(validCreateArgs(), "second.yaml"),
		"bad env":        replace(validCreateArgs(), "test", "staging"),
		"negative root":  append([]string{"--root-volume-gb", "-5"}, validCreateArgs()...),
		"two formats":    append([]string{"-json", "-text"}, validCreateArgs()...),
		"jsonl":          append([]string{"-jsonl"}, validCreateArgs()...),
		"unknown option": append([]string{"--region", "us-east-1"}, validCreateArgs()...),
		"blank name":     replace(validCreateArgs(), "caltechauthors-v14", ""),
	} {
		_, _, err := ParseCreateLaunchTemplateArgs(args)
		var ue *UsageError
		if !errors.As(err, &ue) || CLIExitCode(err) != 2 || !strings.Contains(err.Error(), "usage: ") {
			t.Errorf("%s: want an exit-2 usage error repeating the usage, got %v", name, err)
		}
	}
}

// replace swaps every occurrence of old for new (values only, not flags).
func replace(args []string, old, new string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if a == old {
			a = new
		}
		out[i] = a
	}
	return out
}

func TestParseCreateLaunchTemplateArgs_Help(t *testing.T) {
	_, _, err := ParseCreateLaunchTemplateArgs([]string{"--help"})
	var help *HelpRequested
	if !errors.As(err, &help) || CLIExitCode(err) != 0 {
		t.Fatalf("want a help request, got %v", err)
	}
	for _, want := range []string{"--name", "--ami", "--instance-type", "--key-pair", "--security-group", "--subnet", "--iam-instance-profile", "--root-volume-gb", "--name-tag", "--project", "--environment", "-json", "never creates"} {
		if !strings.Contains(help.Usage, want) {
			t.Errorf("help should mention %q:\n%s", want, help.Usage)
		}
	}
}

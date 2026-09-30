package workflow

import (
	"errors"
	"strings"
	"testing"

	"github.com/caltechlibrary/clasm/internal/ui"
)

func TestParseComputeArgs_DefaultsToTextAndReturnsPositionals(t *testing.T) {
	opts, pos, err := ParseComputeArgs(ShowInstanceDetailCLISlug, "usage: x", ComputeAllowNone, []string{"box"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Format != ui.FormatText || len(pos) != 1 || pos[0] != "box" {
		t.Errorf("got %+v %v", opts, pos)
	}
}

func TestParseComputeArgs_FormatFlags(t *testing.T) {
	for args, want := range map[string]ui.Format{"-json": ui.FormatJSON, "--json": ui.FormatJSON, "-text": ui.FormatText, "-jsonl": ui.FormatJSONL} {
		opts, _, err := ParseComputeArgs(ShowInstancesCLISlug, "usage: x", ComputeAllowJSONL, []string{args})
		if err != nil || opts.Format != want {
			t.Errorf("%s: got %q err=%v, want %q", args, opts.Format, err, want)
		}
	}
}

// -jsonl is for listings; a detail form is one object, so -json covers it.
func TestParseComputeArgs_JSONLOnlyWhereAllowed(t *testing.T) {
	_, _, err := ParseComputeArgs(ShowInstanceDetailCLISlug, "usage: x", ComputeAllowNone, []string{"-jsonl", "box"})
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Errorf("want a *UsageError, got %v", err)
	}
}

func TestParseComputeArgs_FormatsAreMutuallyExclusive(t *testing.T) {
	for _, args := range [][]string{{"-json", "-text"}, {"-json", "-jsonl"}, {"-jsonl", "-text"}} {
		_, _, err := ParseComputeArgs(ShowInstancesCLISlug, "usage: x", ComputeAllowJSONL, args)
		var ue *UsageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "only one") {
			t.Errorf("%v: want a usage error saying only one format, got %v", args, err)
		}
	}
}

func TestParseComputeArgs_UnknownOptionAndHelp(t *testing.T) {
	_, _, err := ParseComputeArgs(ShowInstancesCLISlug, "usage: x", ComputeAllowJSONL, []string{"-yaml"})
	var ue *UsageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "usage: x") || CLIExitCode(err) != 2 {
		t.Errorf("want exit-2 usage error repeating the usage, got %v", err)
	}
	_, _, err = ParseComputeArgs(ShowInstancesCLISlug, "usage: x", ComputeAllowJSONL, []string{"--help"})
	var help *HelpRequested
	if !errors.As(err, &help) || !strings.Contains(help.Usage, "-json") || !strings.Contains(help.Usage, "-jsonl") || CLIExitCode(err) != 0 {
		t.Errorf("want a help request naming the formats, got %v", err)
	}
}

// Extra options exist only on the leaves that declare them.
func TestParseComputeArgs_LeafSpecificOptions(t *testing.T) {
	opts, _, err := ParseComputeArgs(ShowLaunchTemplateDetailCLISlug, "usage: x", ComputeAllowVersions, []string{"-versions", "lt"})
	if err != nil || !opts.Versions {
		t.Errorf("got %+v err=%v", opts, err)
	}
	if _, _, err := ParseComputeArgs(ShowInstanceDetailCLISlug, "usage: x", ComputeAllowNone, []string{"-versions", "box"}); err == nil {
		t.Error("-versions must be refused on a leaf that does not declare it")
	}
	opts, _, err = ParseComputeArgs(ShowExportCloudInitCLISlug, "usage: x", ComputeAllowLaunch, []string{"-launch-temporary-instance", "ami-1"})
	if err != nil || !opts.LaunchTemporaryInstance {
		t.Errorf("got %+v err=%v", opts, err)
	}
}

// Only a listing leaf may be run with options alone; a flag after a positional
// word is just a word, as in the destructive forms.
func TestParseComputeArgs_OptionAfterPositionalIsPositional(t *testing.T) {
	opts, pos, err := ParseComputeArgs(ShowInstanceDetailCLISlug, "usage: x", ComputeAllowNone, []string{"box", "-json"})
	if err != nil || opts.Format != ui.FormatText || len(pos) != 2 {
		t.Errorf("got %+v %v err=%v", opts, pos, err)
	}
}

// -security-group names the group for an AMI's temporary instance; only the
// cloud-init form declares it.
func TestParseComputeArgs_SecurityGroupOption(t *testing.T) {
	opts, _, err := ParseComputeArgs(ShowExportCloudInitCLISlug, "usage: x", ComputeAllowLaunch, []string{"-launch-temporary-instance", "-security-group", "sg-open", "ami-1"})
	if err != nil || opts.SecurityGroup != "sg-open" || !opts.LaunchTemporaryInstance {
		t.Errorf("got %+v err=%v", opts, err)
	}
	if _, _, err := ParseComputeArgs(ShowInstanceDetailCLISlug, "usage: x", ComputeAllowNone, []string{"-security-group", "sg-1", "box"}); err == nil {
		t.Error("-security-group must be refused on a leaf that does not declare it")
	}
	_, _, err = ParseComputeArgs(ShowExportCloudInitCLISlug, "usage: x", ComputeAllowLaunch, []string{"--help"})
	var help *HelpRequested
	if !errors.As(err, &help) || !strings.Contains(help.Usage, "-security-group") {
		t.Errorf("help should describe -security-group, got %v", err)
	}
}

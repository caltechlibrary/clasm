package workflow

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/caltechlibrary/clasm/internal/ui"
)

// ComputeAllow says which optional options a Compute leaf declares. The format
// options -text and -json are on every read-only leaf.
type ComputeAllow int

const (
	ComputeAllowNone     ComputeAllow = 0
	ComputeAllowJSONL    ComputeAllow = 1 << iota // listings: one object per line
	ComputeAllowVersions                          // launch template detail: list versions
	ComputeAllowLaunch                            // cloud-init: permit the billable AMI extraction
)

// ComputeOptions is what a read-only Compute leaf's options parse to.
type ComputeOptions struct {
	// Format defaults to ui.FormatText.
	Format ui.Format
	// Versions (show-launch-template-detail -versions) lists every version
	// instead of showing one.
	Versions bool
	// LaunchTemporaryInstance (-launch-temporary-instance) is the explicit
	// consent to the billable temporary instance an AMI's cloud-init needs.
	LaunchTemporaryInstance bool
	// SecurityGroup (-security-group) is an existing group for an AMI's temporary
	// instance; "" means the configured one, else the VPC default.
	SecurityGroup string
}

// ParseComputeArgs parses a read-only Compute leaf's options with a FlagSet of
// its own, as ParseDestructiveArgs does, and returns the positional words.
// Options come before positionals (Go's flag package stops at the first word).
// An unknown option, or more than one format, is a *UsageError; --help is a
// *HelpRequested. Arity is the leaf's to check.
func ParseComputeArgs(leaf, usage string, allow ComputeAllow, args []string) (ComputeOptions, []string, error) {
	fs := flag.NewFlagSet(leaf, flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the errors below carry the usage; nothing prints twice
	text := fs.Bool("text", false, "")
	asJSON := fs.Bool("json", false, "")
	var jsonl, versions, launch *bool
	var securityGroup *string
	if allow&ComputeAllowJSONL != 0 {
		jsonl = fs.Bool("jsonl", false, "")
	}
	if allow&ComputeAllowVersions != 0 {
		versions = fs.Bool("versions", false, "")
	}
	if allow&ComputeAllowLaunch != 0 {
		launch = fs.Bool("launch-temporary-instance", false, "")
		securityGroup = fs.String("security-group", "", "")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ComputeOptions{}, nil, &HelpRequested{Usage: computeHelp(usage, allow)}
		}
		return ComputeOptions{}, nil, &UsageError{Msg: fmt.Sprintf("%s: %v\n%s", leaf, err, usage)}
	}

	opts := ComputeOptions{Format: ui.FormatText}
	chosen := 0
	if *text {
		chosen++
	}
	if *asJSON {
		chosen++
		opts.Format = ui.FormatJSON
	}
	if jsonl != nil && *jsonl {
		chosen++
		opts.Format = ui.FormatJSONL
	}
	if chosen > 1 {
		return ComputeOptions{}, nil, &UsageError{Msg: fmt.Sprintf("%s: give only one of -text, -json, -jsonl\n%s", leaf, usage)}
	}
	opts.Versions = versions != nil && *versions
	opts.LaunchTemporaryInstance = launch != nil && *launch
	if securityGroup != nil {
		opts.SecurityGroup = *securityGroup
	}
	return opts, fs.Args(), nil
}

func computeHelp(usage string, allow ComputeAllow) string {
	var b bytes.Buffer
	b.WriteString(usage)
	b.WriteString("\n\nOptions:\n")
	b.WriteString("  -text\n      Plain text, the screen layout without colour (the default). Columns are\n      truncated as on screen; use -json for complete values.\n")
	b.WriteString("  -json\n      One JSON document with complete values and full tags.\n")
	if allow&ComputeAllowJSONL != 0 {
		b.WriteString("  -jsonl\n      One compact JSON object per line, for streaming.\n")
	}
	if allow&ComputeAllowVersions != 0 {
		b.WriteString("  -versions\n      List every version of the template instead of showing one.\n")
	}
	if allow&ComputeAllowLaunch != 0 {
		b.WriteString("  -launch-temporary-instance\n      Required for an AMI: its cloud-init can only be read by launching a\n      temporary billable instance. Not needed for an instance.\n")
		b.WriteString("  -security-group <sg-id>\n      An existing security group for that temporary instance. It must allow\n      outbound HTTPS, or the instance's SSM agent cannot register. Default: the\n      cloud_init_extraction_security_group setting in ~/.clasm, else the VPC default.\n")
	}
	b.WriteString("  -h, --help\n      Show this help.\n")
	return b.String()
}

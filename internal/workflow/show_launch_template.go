package workflow

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/tui"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// launchTemplateLabel formats one launch template's Picker-tier row
// label -- same shape as instanceLabel/imageLabel.
func launchTemplateLabel(lt inventory.LaunchTemplate) string {
	return fmt.Sprintf("%s - %s (%s, default v%d)", lt.TemplateID, lt.Name, lt.Region, lt.DefaultVersion)
}

// pickLaunchTemplate runs a Picker-tier tui.RunPicker (DESIGN.md's full
// conversion punch list) over templates and returns the chosen one --
// same shape as pickInstance/pickImage. Like those, this drives a real
// bubbletea Program that can't be pipe-tested -- every caller splits
// into a thin entry point (calls pickLaunchTemplate) and a testable
// core taking the already-resolved template directly.
func pickLaunchTemplate(ctx context.Context, title, description string, templates []inventory.LaunchTemplate) (inventory.LaunchTemplate, error) {
	rows := make([]string, len(templates))
	for i, lt := range templates {
		rows[i] = launchTemplateLabel(lt)
	}
	idx, err := tui.RunPicker(ctx, tui.PickerConfig{
		Title:        title,
		Description:  description,
		Rows:         rows,
		ColorEnabled: ui.ColorEnabled(),
	})
	if err != nil {
		return inventory.LaunchTemplate{}, err
	}
	return templates[idx], nil
}

// defaultVersionSelector is the value promptLaunchTemplateVersion
// pre-fills -- AWS's own convention for "whatever $Default currently
// points to," editable to an explicit version number or "$Latest" (the
// operator's own framing: pre-filled but overridable, the same shape as
// Backup Archive & Trim's recalled instance/directory defaults).
const defaultVersionSelector = "$Default"

// promptLaunchTemplateVersion prompts for a version selector
// ("$Default", "$Latest", or a literal version number), pre-filled to
// $Default -- shared by Show Launch Template, Create EC2 Instance from
// Launch Template, and Sync's "which version to compare against."
func promptLaunchTemplateVersion(input io.Reader, output io.Writer) (string, error) {
	return ui.Prompt("Version ($Default, $Latest, or a version number)", ui.WithDefault(defaultVersionSelector), ui.WithIO(input, output))
}

// ShowLaunchTemplate runs the Show Launch Template workflow (DESIGN.md,
// "Launch Templates"): pick a template, pick a version (defaulting to
// $Default), display its curated detail fields, and flag it if IMDSv2
// isn't required.
func ShowLaunchTemplate(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, templates []inventory.LaunchTemplate) error {
	if len(templates) == 0 {
		fmt.Fprintln(w, "No launch templates found.")
		return nil
	}
	lt, err := pickLaunchTemplate(ctx, "Select a launch template", "", templates)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return showLaunchTemplate(ctx, w, clients, lt, nil, nil)
}

// showLaunchTemplate is ShowLaunchTemplate's testable core, once a
// template is resolved -- template selection runs a real bubbletea
// Program (tui.RunPicker) that can't be pipe-tested, same limitation as
// every other Picker-tier conversion in this package.
func showLaunchTemplate(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, lt inventory.LaunchTemplate, input io.Reader, output io.Writer) error {
	client, err := resolveEC2(clients, lt.Region)
	if err != nil {
		return err
	}
	version, err := promptLaunchTemplateVersion(input, output)
	if err != nil {
		return err
	}
	detail, err := inventory.DescribeLaunchTemplateVersion(ctx, client, lt.TemplateID, version)
	if err != nil {
		return err
	}
	displayLaunchTemplateVersion(w, lt, detail)
	return nil
}

// displayLaunchTemplateVersion prints a launch template version's
// curated detail fields, modeled on the existing AMI summary plus
// MetadataOptions (DESIGN.md, "Launch Templates"). Flags the version
// passively if IMDSv2 isn't required, rather than a separate audit
// action (DECISIONS.md, "Launch templates: build directly from
// cloud-init YAML, diff-then-new-version sync, fold in IMDSv2").
func displayLaunchTemplateVersion(w io.Writer, lt inventory.LaunchTemplate, detail inventory.LaunchTemplateVersionDetail) {
	fmt.Fprintf(w, "\nLaunch template %s (%s), region %s\n", lt.TemplateID, lt.Name, lt.Region)
	version := fmt.Sprintf("%d", detail.VersionNumber)
	if detail.IsDefaultVersion {
		version += " (default)"
	}
	fmt.Fprintf(w, "  Version:              %s\n", version)
	fmt.Fprintf(w, "  Created:              %s\n", displayOrNone(detail.CreateTime))
	fmt.Fprintf(w, "  AMI:                  %s\n", displayOrNone(detail.ImageID))
	fmt.Fprintf(w, "  Instance type:        %s\n", displayOrNone(detail.InstanceType))
	fmt.Fprintf(w, "  Key pair:             %s\n", displayOrNone(detail.KeyName))
	fmt.Fprintf(w, "  IAM instance profile: %s\n", displayOrNone(detail.IAMInstanceProfile))
	fmt.Fprintf(w, "  Security groups:      %s\n", displayOrNone(strings.Join(detail.SecurityGroupIDs, ", ")))
	fmt.Fprintf(w, "  Subnet:               %s\n", displayOrNone(detail.SubnetID))
	fmt.Fprintf(w, "  Project:              %s\n", displayOrNone(detail.Project))
	fmt.Fprintf(w, "  Environment:          %s\n", displayOrNone(detail.Environment))
	if detail.IMDSv2Required {
		fmt.Fprintln(w, "  IMDSv2:               required")
		return
	}
	fmt.Fprintln(w, "  IMDSv2:               NOT required -- flagged: AWS security recommendations require IMDSv2 (HttpTokens: required). Sync this template to bring it into compliance.")
}

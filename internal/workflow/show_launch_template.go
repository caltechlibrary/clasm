package workflow

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"github.com/aymanbagabas/go-udiff"

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
	return promptLaunchTemplateVersionLabeled("Version ($Default, $Latest, or a version number)", input, output)
}

// promptLaunchTemplateVersionLabeled is promptLaunchTemplateVersion
// with a caller-supplied label -- Show Launch Template's version-diff
// needs two distinct prompts ("First version to compare"/"Second
// version to compare"), each still normalized and pre-filled to
// $Default the same way.
func promptLaunchTemplateVersionLabeled(label string, input io.Reader, output io.Writer) (string, error) {
	raw, err := ui.Prompt(label, ui.WithDefault(defaultVersionSelector), ui.WithIO(input, output))
	if err != nil {
		return "", err
	}
	return normalizeVersionSelector(raw), nil
}

// normalizeVersionSelector strips a leading "v"/"V" from a plain
// version number (e.g. "v1" -> "1") before it reaches an AWS call --
// found during real-AWS testing 2026-07-20: AWS's launch-template
// version parameter accepts only "$Default", "$Latest", or a bare
// numeric string, but this project's own launchTemplateLabel/display
// format shows versions as "v2", "v3", making "v1" a natural, likely
// thing to type and a hard rejection otherwise ("Invalid launch
// template version: either '$Default', '$Latest', or a numeric version
// are allowed"). "$Default"/"$Latest" (and anything not of the form
// v<digits>) pass through unchanged.
func normalizeVersionSelector(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 2 || (s[0] != 'v' && s[0] != 'V') {
		return s
	}
	rest := s[1:]
	for _, r := range rest {
		if r < '0' || r > '9' {
			return s
		}
	}
	return rest
}

// ShowLaunchTemplate runs the Show Launch Template workflow (DESIGN.md,
// "Launch Templates"): pick a template, then choose to view its current
// version detail, list every version, or diff two versions' content --
// version history/diffing added 2026-07-20 in response to real usage
// feedback ("I only know there's another version, not what changed").
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

	choice, err := pickString(w, fmt.Sprintf("Show for %s (%s)", lt.TemplateID, lt.Name), "",
		hintCancel, []string{"Show version detail", "List all versions", "Diff two versions"}, input, output)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	switch choice {
	case "Show version detail":
		return showLaunchTemplateDetail(ctx, w, client, lt, input, output)
	case "List all versions":
		return showLaunchTemplateVersionList(ctx, w, client, lt, input)
	case "Diff two versions":
		return showLaunchTemplateVersionDiff(ctx, w, client, lt, input, output)
	}
	return nil
}

// showLaunchTemplateDetail displays one version's curated detail
// fields -- the original Show Launch Template behavior, now one of
// three choices under the template-level menu above.
func showLaunchTemplateDetail(ctx context.Context, w io.Writer, client awsclient.EC2API, lt inventory.LaunchTemplate, input io.Reader, output io.Writer) error {
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

// showLaunchTemplateVersionList lists every version of lt: number,
// creation time, and whether it's the default -- deliberately no
// per-version detail or content diffing here, just "which versions
// exist and when" (see showLaunchTemplateVersionDiff for content
// comparison).
func showLaunchTemplateVersionList(ctx context.Context, w io.Writer, client awsclient.EC2API, lt inventory.LaunchTemplate, input io.Reader) error {
	versions, err := inventory.ListLaunchTemplateVersions(ctx, client, lt.TemplateID)
	if err != nil {
		return err
	}
	return displayRows(ctx, w, fmt.Sprintf("Versions of %s (%s)", lt.TemplateID, lt.Name), launchTemplateVersionRows(versions), input)
}

// launchTemplateVersionRows formats ListLaunchTemplateVersions' output
// for display -- extracted so it's testable without driving
// displayRows' own interactive/accessible-mode split.
func launchTemplateVersionRows(versions []inventory.LaunchTemplateVersionSummary) []string {
	rows := make([]string, len(versions))
	for i, v := range versions {
		label := fmt.Sprintf("v%d", v.VersionNumber)
		if v.IsDefaultVersion {
			label += " (default)"
		}
		rows[i] = fmt.Sprintf("%-14s created %s", label, displayOrNone(v.CreateTime))
	}
	return rows
}

// showLaunchTemplateVersionDiff prompts for two versions and shows a
// plain-text diff of their decoded cloud-init content -- read-only,
// never creates a new version (see Sync Cloud-Init YAML to a Template
// for that).
func showLaunchTemplateVersionDiff(ctx context.Context, w io.Writer, client awsclient.EC2API, lt inventory.LaunchTemplate, input io.Reader, output io.Writer) error {
	v1, err := promptLaunchTemplateVersionLabeled("First version to compare ($Default, $Latest, or a version number)", input, output)
	if err != nil {
		return err
	}
	v2, err := promptLaunchTemplateVersionLabeled("Second version to compare ($Default, $Latest, or a version number)", input, output)
	if err != nil {
		return err
	}

	d1, err := inventory.DescribeLaunchTemplateVersion(ctx, client, lt.TemplateID, v1)
	if err != nil {
		return err
	}
	d2, err := inventory.DescribeLaunchTemplateVersion(ctx, client, lt.TemplateID, v2)
	if err != nil {
		return err
	}

	yaml1, err := base64.StdEncoding.DecodeString(d1.UserData)
	if err != nil {
		return fmt.Errorf("decoding version %d's user-data: %w", d1.VersionNumber, err)
	}
	yaml2, err := base64.StdEncoding.DecodeString(d2.UserData)
	if err != nil {
		return fmt.Errorf("decoding version %d's user-data: %w", d2.VersionNumber, err)
	}

	if string(yaml1) == string(yaml2) {
		fmt.Fprintf(w, "Versions %d and %d have identical cloud-init content.\n", d1.VersionNumber, d2.VersionNumber)
		return nil
	}

	diff := udiff.Unified(fmt.Sprintf("version %d", d1.VersionNumber), fmt.Sprintf("version %d", d2.VersionNumber), string(yaml1), string(yaml2))
	return displayDiff(ctx, w, fmt.Sprintf("Diff: %s version %d vs version %d", lt.TemplateID, d1.VersionNumber, d2.VersionNumber), diff, input)
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

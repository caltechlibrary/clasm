package workflow

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// PromoteLaunchTemplateVersion runs the Promote Launch Template Version
// to Default workflow (DESIGN.md, "Launch Templates"): pick a template,
// pick which version becomes $Default. Always its own explicit action,
// never a side effect of Sync (DECISIONS.md, "Launch templates: build
// directly from cloud-init YAML, diff-then-new-version sync, fold in
// IMDSv2") -- the operator expects to experiment with in-progress
// versions without silently changing what a plain launch picks up.
func PromoteLaunchTemplateVersion(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, templates []inventory.LaunchTemplate) error {
	if len(templates) == 0 {
		fmt.Fprintln(w, "No launch templates found.")
		return nil
	}
	lt, err := pickLaunchTemplate(ctx, "Select a launch template", "", templates)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return promoteLaunchTemplateVersion(ctx, w, clients, lt, nil, nil)
}

// promoteLaunchTemplateVersion is PromoteLaunchTemplateVersion's
// testable core, once a template is resolved -- same limitation as
// every other Picker-tier conversion in this package.
func promoteLaunchTemplateVersion(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, lt inventory.LaunchTemplate, input io.Reader, output io.Writer) error {
	client, err := resolveEC2(clients, lt.Region)
	if err != nil {
		return err
	}

	version, err := ui.Prompt("Version number to make the default", ui.WithValidator(requireNonEmpty), ui.WithIO(input, output))
	if err != nil {
		return err
	}

	ok, err := Confirm(fmt.Sprintf("Make version %s the default version of %s?", version, lt.TemplateID), WithConfirmIO(input, output))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	if _, err := client.ModifyLaunchTemplate(ctx, &ec2.ModifyLaunchTemplateInput{
		LaunchTemplateId: aws.String(lt.TemplateID),
		DefaultVersion:   aws.String(version),
	}); err != nil {
		return fmt.Errorf("promoting version %s of %s: %w", version, lt.TemplateID, err)
	}

	fmt.Fprintf(w, "Version %s of %s is now the default.\n", version, lt.TemplateID)
	return nil
}

// DeleteLaunchTemplateVersions runs the Delete Launch Template
// Version(s) workflow (DESIGN.md, "Launch Templates"): prune specific
// stale versions without touching the whole template -- "so no one
// accidentally chooses them" (an abandoned experimental version from
// mid-development). Same safety-first shape as Feature 9 (Remove AMI):
// dry-run display, an Environment=production warning, type-to-confirm.
func DeleteLaunchTemplateVersions(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, templates []inventory.LaunchTemplate) error {
	if len(templates) == 0 {
		fmt.Fprintln(w, "No launch templates found.")
		return nil
	}
	lt, err := pickLaunchTemplate(ctx, "Select a launch template", "Prune specific stale versions without deleting the whole template.", templates)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return deleteLaunchTemplateVersions(ctx, w, clients, lt, nil, nil)
}

// deleteLaunchTemplateVersions is DeleteLaunchTemplateVersions's
// testable core, once a template is resolved -- same limitation as
// every other Picker-tier conversion in this package.
func deleteLaunchTemplateVersions(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, lt inventory.LaunchTemplate, input io.Reader, output io.Writer) error {
	client, err := resolveEC2(clients, lt.Region)
	if err != nil {
		return err
	}

	raw, err := ui.Prompt("Version number(s) to delete (comma-separated)", ui.WithValidator(requireNonEmpty), ui.WithIO(input, output))
	if err != nil {
		return err
	}
	versions := splitCSV(raw)
	if len(versions) == 0 {
		fmt.Fprintln(w, "No version numbers given; nothing to delete.")
		return nil
	}

	fmt.Fprintf(w, "\n=== DRY RUN: deleting version(s) %s of launch template %s (%s) ===\n", strings.Join(versions, ", "), lt.TemplateID, lt.Name)
	if lt.Environment == "production" {
		fmt.Fprintln(w, "WARNING: this launch template is tagged Environment=production.")
	}
	ok, err := ConfirmDestructive([]string{lt.TemplateID, lt.Name}, WithConfirmIO(input, output))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DeleteLaunchTemplateVersions(ctx, &ec2.DeleteLaunchTemplateVersionsInput{
		LaunchTemplateId: aws.String(lt.TemplateID),
		Versions:         versions,
	})
	if err != nil {
		return fmt.Errorf("deleting versions of %s: %w", lt.TemplateID, err)
	}

	fmt.Fprintf(w, "Deleted %d version(s) of %s.\n", len(out.SuccessfullyDeletedLaunchTemplateVersions), lt.TemplateID)
	for _, failed := range out.UnsuccessfullyDeletedLaunchTemplateVersions {
		fmt.Fprintf(w, "  FAILED to delete version %d: %s\n", aws.ToInt64(failed.VersionNumber), failed.ResponseError.Code)
	}
	return nil
}

// DeleteLaunchTemplate runs the Delete Launch Template workflow
// (DESIGN.md, "Launch Templates"): removes the whole template --
// intended for when the software system it was built for is retired
// entirely. Same safety-first shape as Feature 9 (Remove AMI).
func DeleteLaunchTemplate(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, templates []inventory.LaunchTemplate) error {
	if len(templates) == 0 {
		fmt.Fprintln(w, "No launch templates found.")
		return nil
	}
	lt, err := pickLaunchTemplate(ctx, "Select a launch template to delete", "This permanently deletes the template and every version of it.", templates)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return deleteLaunchTemplate(ctx, w, clients, lt, nil, nil)
}

// deleteLaunchTemplate is DeleteLaunchTemplate's testable core, once a
// template is resolved -- same limitation as every other Picker-tier
// conversion in this package.
func deleteLaunchTemplate(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, lt inventory.LaunchTemplate, input io.Reader, output io.Writer) error {
	client, err := resolveEC2(clients, lt.Region)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "\n=== DRY RUN: deleting launch template %s (%s) and all its versions ===\n", lt.TemplateID, lt.Name)
	if lt.Environment == "production" {
		fmt.Fprintln(w, "WARNING: this launch template is tagged Environment=production.")
	}
	ok, err := ConfirmDestructive([]string{lt.TemplateID, lt.Name}, WithConfirmIO(input, output))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	if _, err := client.DeleteLaunchTemplate(ctx, &ec2.DeleteLaunchTemplateInput{LaunchTemplateId: aws.String(lt.TemplateID)}); err != nil {
		return fmt.Errorf("deleting launch template %s: %w", lt.TemplateID, err)
	}

	fmt.Fprintf(w, "Launch template %s deleted.\n", lt.TemplateID)
	return nil
}

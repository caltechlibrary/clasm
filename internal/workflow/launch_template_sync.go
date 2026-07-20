package workflow

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aymanbagabas/go-udiff"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

// SyncLaunchTemplate runs the Sync Cloud-Init YAML to a Template
// workflow (DESIGN.md, "Launch Templates"): pick a template, pick a
// version to compare against (pre-filled to $Default), pick a local
// YAML file, and diff its content against that version's decoded
// UserData. Identical content is a no-op (Tom's own framing: "does
// this actually require a new version" -- DECISIONS.md, "Launch
// templates: build directly from cloud-init YAML, diff-then-new-
// version sync, fold in IMDSv2"); different content shows a plain-text
// diff and requires explicit confirmation before creating a new
// version. Never promotes the new version to default -- that's always
// Promote Launch Template Version to Default's own separate action.
func SyncLaunchTemplate(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, templates []inventory.LaunchTemplate) error {
	if len(templates) == 0 {
		fmt.Fprintln(w, "No launch templates found.")
		return nil
	}
	lt, err := pickLaunchTemplate(ctx, "Select a launch template to sync", "", templates)
	if err != nil {
		return cancelledIsNil(w, err)
	}
	return syncLaunchTemplate(ctx, w, clients, lt, nil, nil)
}

// syncLaunchTemplate is SyncLaunchTemplate's testable core, once a
// template is resolved -- same limitation as every other Picker-tier
// conversion in this package: template selection runs a real bubbletea
// Program that can't be pipe-tested.
func syncLaunchTemplate(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, lt inventory.LaunchTemplate, input io.Reader, output io.Writer) error {
	client, err := resolveEC2(clients, lt.Region)
	if err != nil {
		return err
	}

	version, err := promptLaunchTemplateVersion(input, output)
	if err != nil {
		return err
	}

	newYAML, err := promptCloudInitYAMLFile(w, input, output)
	if err != nil {
		return err
	}

	detail, err := inventory.DescribeLaunchTemplateVersion(ctx, client, lt.TemplateID, version)
	if err != nil {
		return err
	}

	oldYAMLBytes, err := base64.StdEncoding.DecodeString(detail.UserData)
	if err != nil {
		return fmt.Errorf("decoding existing user-data: %w", err)
	}
	oldYAML := string(oldYAMLBytes)

	if oldYAML == newYAML {
		fmt.Fprintln(w, "No changes -- nothing to sync.")
		return nil
	}

	sourceVersion := fmt.Sprintf("%d", detail.VersionNumber)
	diff := udiff.Unified(fmt.Sprintf("%s version %s", lt.TemplateID, sourceVersion), "local file", oldYAML, newYAML)
	fmt.Fprintln(w, "\n--- diff ---")
	fmt.Fprint(w, diff)
	fmt.Fprintln(w, "------------")

	ok, err := Confirm(fmt.Sprintf("Create a new version of %s with these changes?", lt.TemplateID), WithConfirmIO(input, output))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	newVersion, err := createLaunchTemplateVersion(ctx, client, lt.TemplateID, sourceVersion, newYAML)
	if err != nil {
		return fmt.Errorf("creating new launch template version: %w", err)
	}

	fmt.Fprintf(w, "Created version %d of %s. It is NOT the default version yet -- use Promote Launch Template Version to Default when ready.\n", newVersion, lt.TemplateID)
	return nil
}

// createLaunchTemplateVersion creates a new version of templateID,
// based on sourceVersion (a resolved literal version number, not a
// selector like $Default -- resolved by the caller so the new version
// is guaranteed to inherit from the exact content the diff was shown
// against, not whatever $Default happens to point to at call time).
// SourceVersion means every field other than UserData is inherited
// unchanged (confirmed by reading CreateLaunchTemplateVersionInput's
// own field comments, not assumed) -- this never touches IMDSv2 or any
// other setting, only the cloud-init content.
func createLaunchTemplateVersion(ctx context.Context, client awsclient.EC2API, templateID, sourceVersion, newYAML string) (int64, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.CreateLaunchTemplateVersion(ctx, &ec2.CreateLaunchTemplateVersionInput{
		LaunchTemplateId: aws.String(templateID),
		SourceVersion:    aws.String(sourceVersion),
		LaunchTemplateData: &types.RequestLaunchTemplateData{
			UserData: aws.String(base64.StdEncoding.EncodeToString([]byte(newYAML))),
		},
	})
	if err != nil {
		return 0, err
	}
	return aws.ToInt64(out.LaunchTemplateVersion.VersionNumber), nil
}

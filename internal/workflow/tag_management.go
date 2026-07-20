package workflow

import (
	"context"
	"fmt"
	"io"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/tui"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// pickKeyPair runs a Picker-tier tui.RunPicker (DESIGN.md's full
// conversion punch list) over keyPairs and returns the chosen one --
// same shape as pickInstance/pickImage/pickLaunchTemplate, generic
// (unlike pickKeyPairForDeletion, whose Description is specific to
// deletion). Like those, this drives a real bubbletea Program that
// can't be pipe-tested -- every caller splits into a thin entry point
// (calls pickKeyPair) and a testable core taking the already-resolved
// key pair directly.
func pickKeyPair(ctx context.Context, title, description string, keyPairs []inventory.KeyPair) (inventory.KeyPair, error) {
	rows := make([]string, len(keyPairs))
	for i, kp := range keyPairs {
		rows[i] = keyPairLabel(kp)
	}
	idx, err := tui.RunPicker(ctx, tui.PickerConfig{
		Title:        title,
		Description:  description,
		Rows:         rows,
		ColorEnabled: ui.ColorEnabled(),
	})
	if err != nil {
		return inventory.KeyPair{}, err
	}
	return keyPairs[idx], nil
}

// ManageResourceTags runs the Tag Management domain's "Manage tags"
// workflow (DESIGN.md, "Tag Management Domain"; DECISIONS.md, "Tag
// Management: a fourth domain..."): pick a resource kind, pick a
// specific resource of that kind, then hand off to the same loop
// Compute's own narrower "Manage tags for an instance or AMI"
// (manageTags/manageTagsForResource, Phase 20.29) already uses --
// manageTagsForResource is fully resource-agnostic (client,
// resourceID, resourceLabel, tags, fetchTags), so only the kind picker
// and the per-kind fetch/pick functions are new here. S3 Bucket isn't
// included yet -- its read-modify-write tag semantics need
// applyOneTagChange to take a pluggable apply closure first (a later
// step; PLAN.md Phase 20.30).
func ManageResourceTags(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, instances []inventory.Instance, images []inventory.Image, launchTemplates []inventory.LaunchTemplate, keyPairs []inventory.KeyPair) error {
	return manageResourceTags(ctx, w, clients, instances, images, launchTemplates, keyPairs, nil, nil)
}

// manageResourceTags is ManageResourceTags's testable core: menuInput/
// menuOutput are nil in production and supplied by tests to drive the
// kind picker (a Menu-tier huh.Select, pipe-testable) through its
// accessible-mode path -- the resource picker for whichever kind is
// chosen (pickInstance/pickImage/pickLaunchTemplate/pickKeyPair) still
// isn't, the same accepted limitation manageTags itself has, so this
// function as a whole isn't driven end-to-end by an automated test;
// manageTagsForResource (which it dispatches into) already has full
// coverage.
func manageResourceTags(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, instances []inventory.Instance, images []inventory.Image, launchTemplates []inventory.LaunchTemplate, keyPairs []inventory.KeyPair, menuInput io.Reader, menuOutput io.Writer) error {
	kind, err := pickString(w, "Manage tags on", "Add, update, or remove a tag on an EC2 instance, AMI, launch template, or key pair.", "(q to cancel)", []string{"Instance", "AMI", "Launch Template", "Key Pair"}, menuInput, menuOutput)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	var resourceID, resourceLabel string
	var tags map[string]string
	var client awsclient.EC2API
	var fetchTags func(ctx context.Context) (map[string]string, error)

	switch kind {
	case "Instance":
		if len(instances) == 0 {
			fmt.Fprintln(w, "No instances found.")
			return nil
		}
		inst, err := pickInstance(ctx, "Select an instance", "", instances)
		if err != nil {
			return cancelledIsNil(w, err)
		}
		client, err = resolveEC2(clients, inst.Region)
		if err != nil {
			return err
		}
		resourceID, resourceLabel = inst.InstanceID, instanceLabel(inst)
		tags, err = fetchInstanceTags(ctx, client, resourceID)
		if err != nil {
			return err
		}
		fetchTags = func(ctx context.Context) (map[string]string, error) {
			return fetchInstanceTags(ctx, client, resourceID)
		}
	case "AMI":
		if len(images) == 0 {
			fmt.Fprintln(w, "No AMIs found.")
			return nil
		}
		img, err := pickImage(ctx, "Select an AMI", "", images)
		if err != nil {
			return cancelledIsNil(w, err)
		}
		client, err = resolveEC2(clients, img.Region)
		if err != nil {
			return err
		}
		resourceID, resourceLabel = img.ImageID, imageLabel(img)
		tags, err = fetchImageTags(ctx, client, resourceID)
		if err != nil {
			return err
		}
		fetchTags = func(ctx context.Context) (map[string]string, error) {
			return fetchImageTags(ctx, client, resourceID)
		}
	case "Launch Template":
		if len(launchTemplates) == 0 {
			fmt.Fprintln(w, "No launch templates found.")
			return nil
		}
		lt, err := pickLaunchTemplate(ctx, "Select a launch template", "", launchTemplates)
		if err != nil {
			return cancelledIsNil(w, err)
		}
		client, err = resolveEC2(clients, lt.Region)
		if err != nil {
			return err
		}
		resourceID, resourceLabel = lt.TemplateID, launchTemplateLabel(lt)
		tags, err = fetchLaunchTemplateTags(ctx, client, resourceID)
		if err != nil {
			return err
		}
		fetchTags = func(ctx context.Context) (map[string]string, error) {
			return fetchLaunchTemplateTags(ctx, client, resourceID)
		}
	case "Key Pair":
		if len(keyPairs) == 0 {
			fmt.Fprintln(w, "No key pairs found.")
			return nil
		}
		kp, err := pickKeyPair(ctx, "Select a key pair", "", keyPairs)
		if err != nil {
			return cancelledIsNil(w, err)
		}
		client, err = resolveEC2(clients, kp.Region)
		if err != nil {
			return err
		}
		resourceID, resourceLabel = kp.KeyPairID, keyPairLabel(kp)
		tags, err = fetchKeyPairTags(ctx, client, resourceID)
		if err != nil {
			return err
		}
		fetchTags = func(ctx context.Context) (map[string]string, error) {
			return fetchKeyPairTags(ctx, client, resourceID)
		}
	}

	return manageTagsForResource(ctx, w, client, resourceID, resourceLabel, tags, fetchTags, menuInput, menuOutput)
}

// instanceTaggedResources/imageTaggedResources/launchTemplateTaggedResources/
// keyPairTaggedResources convert one resource kind's already-fetched
// inventory listing into ui.TaggedResource rows for "Show all tags" --
// pure data transforms, unit-testable without driving
// tui.RunListView's interactive loop (same split as internal/ui's own
// *ListViewConfig helpers).
func instanceTaggedResources(instances []inventory.Instance) []ui.TaggedResource {
	rows := make([]ui.TaggedResource, len(instances))
	for i, inst := range instances {
		rows[i] = ui.TaggedResource{ID: inst.InstanceID, Label: instanceLabel(inst), Tags: inst.Tags}
	}
	return rows
}

func imageTaggedResources(images []inventory.Image) []ui.TaggedResource {
	rows := make([]ui.TaggedResource, len(images))
	for i, img := range images {
		rows[i] = ui.TaggedResource{ID: img.ImageID, Label: imageLabel(img), Tags: img.Tags}
	}
	return rows
}

func launchTemplateTaggedResources(templates []inventory.LaunchTemplate) []ui.TaggedResource {
	rows := make([]ui.TaggedResource, len(templates))
	for i, lt := range templates {
		rows[i] = ui.TaggedResource{ID: lt.TemplateID, Label: launchTemplateLabel(lt), Tags: lt.Tags}
	}
	return rows
}

func keyPairTaggedResources(keyPairs []inventory.KeyPair) []ui.TaggedResource {
	rows := make([]ui.TaggedResource, len(keyPairs))
	for i, kp := range keyPairs {
		rows[i] = ui.TaggedResource{ID: kp.KeyPairID, Label: keyPairLabel(kp), Tags: kp.Tags}
	}
	return rows
}

// ShowAllTags runs the Tag Management domain's "Show all tags" action
// (DESIGN.md, "Tag Management Domain"): pick a resource kind, then
// list every resource of that kind with its full tag set in the
// shared List-tier component. Deliberately one type-scoped listing at
// a time -- reusing the same kind picker as ManageResourceTags, not a
// separate resource picker (there's nothing to narrow down to; every
// resource of the chosen kind is shown at once) -- rather than one
// combined table across every kind (DECISIONS.md, "Tag Management: a
// fourth domain...", rejected alternatives: no shared row shape, and
// tag key sets vary per resource regardless).
func ShowAllTags(ctx context.Context, w io.Writer, instances []inventory.Instance, images []inventory.Image, launchTemplates []inventory.LaunchTemplate, keyPairs []inventory.KeyPair) error {
	return showAllTags(ctx, w, instances, images, launchTemplates, keyPairs, nil, nil)
}

// showAllTags is ShowAllTags's testable core: menuInput/menuOutput are
// nil in production and supplied by tests to drive the kind picker (a
// Menu-tier huh.Select, pipe-testable) through its accessible-mode
// path. The subsequent ui.DisplayAllTags call is a real bubbletea
// Program (tui.RunListView) that can't be pipe-tested -- same accepted
// limitation as DisplayInstances/DisplayImages/etc, so only the
// row-building conversion functions above and tagsListViewConfig
// (internal/ui) are unit-tested directly.
func showAllTags(ctx context.Context, w io.Writer, instances []inventory.Instance, images []inventory.Image, launchTemplates []inventory.LaunchTemplate, keyPairs []inventory.KeyPair, menuInput io.Reader, menuOutput io.Writer) error {
	kind, err := pickString(w, "Show all tags for", "Lists every resource of the chosen kind with its complete tag set.", "(q to cancel)", []string{"Instance", "AMI", "Launch Template", "Key Pair"}, menuInput, menuOutput)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	switch kind {
	case "Instance":
		return ui.DisplayAllTags(ctx, "EC2 Instances -- All Tags", instanceTaggedResources(instances))
	case "AMI":
		return ui.DisplayAllTags(ctx, "AMIs -- All Tags", imageTaggedResources(images))
	case "Launch Template":
		return ui.DisplayAllTags(ctx, "Launch Templates -- All Tags", launchTemplateTaggedResources(launchTemplates))
	case "Key Pair":
		return ui.DisplayAllTags(ctx, "Key Pairs -- All Tags", keyPairTaggedResources(keyPairs))
	}
	return nil
}

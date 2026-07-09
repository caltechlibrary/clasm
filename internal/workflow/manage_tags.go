package workflow

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// TagChangeParams is the resolved parameter set for a single tag
// add/update/remove on an instance or AMI. Action is "add", "update", or
// "remove"; Value is ignored for "remove".
type TagChangeParams struct {
	ResourceID string
	Action     string
	Key        string
	Value      string
}

// ApplyTagChange calls ec2:CreateTags (add/update) or ec2:DeleteTags
// (remove). Renaming an instance is simply updating its Name tag through
// this same call -- no separate operation (see DECISIONS.md, "Broaden
// Rename Instance into a general Manage Tags primitive"). This never
// touches an AMI's Name *attribute*, which is immutable once set at
// CreateImage time -- only tags.
func ApplyTagChange(ctx context.Context, client awsclient.EC2API, params TagChangeParams) error {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	if params.Action == "remove" {
		_, err := client.DeleteTags(ctx, &ec2.DeleteTagsInput{
			Resources: []string{params.ResourceID},
			Tags:      []types.Tag{{Key: aws.String(params.Key)}},
		})
		return err
	}
	_, err := client.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{params.ResourceID},
		Tags:      []types.Tag{{Key: aws.String(params.Key), Value: aws.String(params.Value)}},
	})
	return err
}

func fetchInstanceTags(ctx context.Context, client awsclient.EC2API, instanceID string) (map[string]string, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		return nil, err
	}
	inst, found := findInstance(out, instanceID)
	if !found {
		return nil, fmt.Errorf("instance %s not found", instanceID)
	}
	return tagsToMap(inst.Tags), nil
}

func fetchImageTags(ctx context.Context, client awsclient.EC2API, imageID string) (map[string]string, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: []string{imageID}})
	if err != nil {
		return nil, err
	}
	if len(out.Images) == 0 {
		return nil, fmt.Errorf("AMI %s not found", imageID)
	}
	return tagsToMap(out.Images[0].Tags), nil
}

func tagsToMap(tags []types.Tag) map[string]string {
	m := make(map[string]string, len(tags))
	for _, tag := range tags {
		m[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	return m
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func displayTags(t *termlib.Terminal, label string, tags map[string]string) {
	t.Printf("\nCurrent tags for %s:\n", label)
	keys := sortedKeys(tags)
	if len(keys) == 0 {
		t.Println("  (no tags)")
	}
	for _, k := range keys {
		t.Printf("  %s = %s\n", k, tags[k])
	}
	t.Refresh()
}

// ManageTags runs the full Manage Tags workflow (DESIGN.md, Feature 7):
// pick an instance or AMI, display its current tags, choose add/update/
// remove, collect the key/value, confirm (simple yes/no -- cheap and
// reversible, not the dry-run/type-to-confirm tier), then apply. This is
// the general-purpose tag-editing *mechanism*; the Project/Environment
// Tagging Convention (Feature 12) is the separate *policy* that gives
// those two tag keys meaning elsewhere in this tool (see DESIGN.md,
// "Manage Tags vs. the Tagging Convention").
// Takes a per-region client map and resolves the one matching the
// picked resource's region.
func ManageTags(ctx context.Context, t *termlib.Terminal, le *termlib.LineEditor, clients map[string]awsclient.EC2API, instances []inventory.Instance, images []inventory.Image) error {
	kind, err := ui.PickList(t, le, []string{"Instance", "AMI"}, identity, "Manage tags on")
	if err != nil {
		return cancelledIsNil(t, err)
	}

	var resourceID, resourceLabel string
	var tags map[string]string
	var client awsclient.EC2API

	switch kind {
	case "Instance":
		if len(instances) == 0 {
			t.Println("No instances found.")
			t.Refresh()
			return nil
		}
		inst, err := ui.PickList(t, le, instances, instanceLabel, "Select an instance")
		if err != nil {
			return cancelledIsNil(t, err)
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
	case "AMI":
		if len(images) == 0 {
			t.Println("No AMIs found.")
			t.Refresh()
			return nil
		}
		img, err := ui.PickList(t, le, images, imageLabel, "Select an AMI")
		if err != nil {
			return cancelledIsNil(t, err)
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
	}

	displayTags(t, resourceLabel, tags)

	action, err := ui.PickList(t, le, []string{"Add", "Update", "Remove"}, identity, "Choose an action")
	if err != nil {
		return cancelledIsNil(t, err)
	}

	params := TagChangeParams{ResourceID: resourceID}
	switch action {
	case "Add":
		params.Action = "add"
		params.Key, err = ui.Prompt(t, le, "New tag key", ui.WithValidator(requireNonEmpty))
		if err != nil {
			return err
		}
		params.Value, err = ui.Prompt(t, le, "New tag value")
		if err != nil {
			return err
		}
	case "Update":
		params.Action = "update"
		keys := sortedKeys(tags)
		if len(keys) == 0 {
			t.Println("No existing tags to update.")
			t.Refresh()
			return nil
		}
		params.Key, err = ui.PickList(t, le, keys, identity, "Select a tag to update")
		if err != nil {
			return cancelledIsNil(t, err)
		}
		params.Value, err = ui.Prompt(t, le, fmt.Sprintf("New value for %s", params.Key), ui.WithDefault(tags[params.Key]))
		if err != nil {
			return err
		}
	case "Remove":
		params.Action = "remove"
		keys := sortedKeys(tags)
		if len(keys) == 0 {
			t.Println("No existing tags to remove.")
			t.Refresh()
			return nil
		}
		params.Key, err = ui.PickList(t, le, keys, identity, "Select a tag to remove")
		if err != nil {
			return cancelledIsNil(t, err)
		}
	}

	if params.Key == "Environment" {
		t.Println("Note: Environment is the same tag this tool uses elsewhere (Terminate Instance, Remove AMI) to gate production warnings.")
		t.Refresh()
	}

	ok, err := Confirm(t, le, fmt.Sprintf("%s tag %q on %s?", params.Action, params.Key, resourceLabel))
	if err != nil {
		return err
	}
	if !ok {
		t.Println("Cancelled.")
		t.Refresh()
		return nil
	}

	if err := ApplyTagChange(ctx, client, params); err != nil {
		return fmt.Errorf("updating tags on %s: %w", resourceID, err)
	}

	t.Println("Tags updated.")
	t.Refresh()
	return nil
}

func identity(s string) string { return s }

// cancelledIsNil turns a PickList cancellation into a clean nil return
// (printing "Cancelled."), passing any other error through unchanged.
func cancelledIsNil(t *termlib.Terminal, err error) error {
	if errors.Is(err, ui.ErrCancelled) {
		t.Println("Cancelled.")
		t.Refresh()
		return nil
	}
	return err
}

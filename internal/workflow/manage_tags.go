package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/charmbracelet/huh"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/tui"
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

func displayTags(w io.Writer, label string, tags map[string]string) {
	fmt.Fprintf(w, "\nCurrent tags for %s:\n", label)
	keys := sortedKeys(tags)
	if len(keys) == 0 {
		fmt.Fprintln(w, "  (no tags)")
	}
	for _, k := range keys {
		fmt.Fprintf(w, "  %s = %s\n", k, tags[k])
	}
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
func ManageTags(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, instances []inventory.Instance, images []inventory.Image) error {
	return manageTags(ctx, w, clients, instances, images, nil, nil)
}

// manageTags is ManageTags's testable core: menuInput/menuOutput are nil
// in production (the Instance-vs-AMI kind, Add/Update/Remove action, and
// select-a-tag pickers all run interactively on the real terminal,
// DESIGN.md's full conversion punch list) and are supplied by tests to
// drive them through their accessible-mode pipe path instead
// (DECISIONS.md, "huh fields are pipe-testable..."). All three
// huh.Selects share one reader/writer pair, read in sequence one line
// at a time, same as a domain menu's own loop-iteration reads.
func manageTags(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, instances []inventory.Instance, images []inventory.Image, menuInput io.Reader, menuOutput io.Writer) error {
	kind, err := pickString(w, "Manage tags on", "Add, update, or remove a tag on an EC2 instance or an AMI.", "(q to cancel)", []string{"Instance", "AMI"}, menuInput, menuOutput)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	var resourceID, resourceLabel string
	var tags map[string]string
	var client awsclient.EC2API

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
	}

	return manageTagsForResource(ctx, w, client, resourceID, resourceLabel, tags, menuInput, menuOutput)
}

// manageTagsForResource is manageTags' testable core for a single
// resource, once an instance or AMI is resolved -- instance/AMI
// selection runs a real bubbletea Program (tui.RunPicker, DESIGN.md's
// full conversion punch list) that can't be driven by a test's pipe
// input, same limitation as every other Picker-tier conversion this
// session.
func manageTagsForResource(ctx context.Context, w io.Writer, client awsclient.EC2API, resourceID, resourceLabel string, tags map[string]string, menuInput io.Reader, menuOutput io.Writer) error {
	displayTags(w, resourceLabel, tags)

	action, err := pickString(w, "Choose an action", "The resource's current tags are listed above.", "(q to cancel)", []string{"Add", "Update", "Remove"}, menuInput, menuOutput)
	if err != nil {
		return cancelledIsNil(w, err)
	}

	params := TagChangeParams{ResourceID: resourceID}
	switch action {
	case "Add":
		params.Action = "add"
		params.Key, err = ui.Prompt("New tag key", ui.WithValidator(requireNonEmpty), ui.WithIO(menuInput, menuOutput))
		if err != nil {
			return err
		}
		params.Value, err = ui.Prompt("New tag value", ui.WithIO(menuInput, menuOutput))
		if err != nil {
			return err
		}
	case "Update":
		params.Action = "update"
		keys := sortedKeys(tags)
		if len(keys) == 0 {
			fmt.Fprintln(w, "No existing tags to update.")
			return nil
		}
		params.Key, err = pickString(w, "Select a tag to update", "You'll be prompted for the new value next.", "(q to cancel)", keys, menuInput, menuOutput)
		if err != nil {
			return cancelledIsNil(w, err)
		}
		params.Value, err = ui.Prompt(fmt.Sprintf("New value for %s", params.Key), ui.WithDefault(tags[params.Key]), ui.WithIO(menuInput, menuOutput))
		if err != nil {
			return err
		}
	case "Remove":
		params.Action = "remove"
		keys := sortedKeys(tags)
		if len(keys) == 0 {
			fmt.Fprintln(w, "No existing tags to remove.")
			return nil
		}
		params.Key, err = pickString(w, "Select a tag to remove", "This deletes the tag entirely, not just its value.", "(q to cancel)", keys, menuInput, menuOutput)
		if err != nil {
			return cancelledIsNil(w, err)
		}
	}

	if params.Key == "Environment" {
		fmt.Fprintln(w, "Note: Environment is the same tag this tool uses elsewhere (Terminate Instance, Remove AMI) to gate production warnings.")
	}

	ok, err := Confirm(fmt.Sprintf("%s tag %q on %s?", params.Action, params.Key, resourceLabel), WithConfirmIO(menuInput, menuOutput))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}

	if err := ApplyTagChange(ctx, client, params); err != nil {
		return fmt.Errorf("updating tags on %s: %w", resourceID, err)
	}

	fmt.Fprintln(w, "Tags updated.")
	return nil
}

// cancelledIsNil turns a PickList, Picker, or Menu-tier huh.Select
// cancellation into a clean nil return (printing "Cancelled."), passing
// any other error through unchanged.
func cancelledIsNil(w io.Writer, err error) error {
	if errors.Is(err, ui.ErrCancelled) || errors.Is(err, tui.ErrCancelled) || errors.Is(err, huh.ErrUserAborted) {
		fmt.Fprintln(w, "Cancelled.")
		return nil
	}
	return err
}

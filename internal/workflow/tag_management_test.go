package workflow

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// manageResourceTags mixes a pipe-testable Menu-tier kind picker
// (pickString) with a not-pipe-testable Picker-tier resource picker
// (pickInstance/pickImage/pickLaunchTemplate/pickKeyPair) once a kind
// with resources is chosen -- the same accepted limitation manageTags
// itself has (see manage_tags_test.go's own comment). These tests only
// exercise the paths reachable before any Picker-tier call: an empty
// resource list for the chosen kind returns immediately.

func TestManageResourceTags_NoInstancesFound(t *testing.T) {
	term, menuInput, buf := newPipeEditor("1\n") // kind = Instance
	err := manageResourceTags(context.Background(), term, nil, nil, nil, nil, nil, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No instances found") {
		t.Errorf("expected a no-instances message, got:\n%s", buf.String())
	}
}

func TestManageResourceTags_NoAMIsFound(t *testing.T) {
	term, menuInput, buf := newPipeEditor("2\n") // kind = AMI
	err := manageResourceTags(context.Background(), term, nil, nil, nil, nil, nil, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No AMIs found") {
		t.Errorf("expected a no-AMIs message, got:\n%s", buf.String())
	}
}

func TestManageResourceTags_NoLaunchTemplatesFound(t *testing.T) {
	term, menuInput, buf := newPipeEditor("3\n") // kind = Launch Template
	err := manageResourceTags(context.Background(), term, nil, nil, nil, nil, nil, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No launch templates found") {
		t.Errorf("expected a no-launch-templates message, got:\n%s", buf.String())
	}
}

func TestManageResourceTags_NoKeyPairsFound(t *testing.T) {
	term, menuInput, buf := newPipeEditor("4\n") // kind = Key Pair
	err := manageResourceTags(context.Background(), term, nil, nil, nil, nil, nil, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No key pairs found") {
		t.Errorf("expected a no-key-pairs message, got:\n%s", buf.String())
	}
}

// instanceTaggedResources/imageTaggedResources/launchTemplateTaggedResources/
// keyPairTaggedResources are pure data transforms feeding "Show all
// tags" -- unit-testable without driving tui.RunListView's interactive
// loop (showAllTags itself, below, isn't fully drivable end-to-end for
// the same reason DisplayInstances/DisplayImages/etc aren't).

func TestInstanceTaggedResources(t *testing.T) {
	instances := []inventory.Instance{
		{InstanceID: "i-1", Name: "web", Region: "us-east-1", Tags: map[string]string{"Name": "web", "Owner": "dld"}},
	}
	got := instanceTaggedResources(instances)
	want := []ui.TaggedResource{
		{ID: "i-1", Label: instanceLabel(instances[0]), Tags: map[string]string{"Name": "web", "Owner": "dld"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestImageTaggedResources(t *testing.T) {
	images := []inventory.Image{
		{ImageID: "ami-1", Name: "base", Region: "us-east-1", Tags: map[string]string{"Project": "caltechauthors"}},
	}
	got := imageTaggedResources(images)
	want := []ui.TaggedResource{
		{ID: "ami-1", Label: imageLabel(images[0]), Tags: map[string]string{"Project": "caltechauthors"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestLaunchTemplateTaggedResources(t *testing.T) {
	templates := []inventory.LaunchTemplate{
		{TemplateID: "lt-1", Name: "rdm-app", Region: "us-east-1", Tags: map[string]string{"Team": "dld"}},
	}
	got := launchTemplateTaggedResources(templates)
	want := []ui.TaggedResource{
		{ID: "lt-1", Label: launchTemplateLabel(templates[0]), Tags: map[string]string{"Team": "dld"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestKeyPairTaggedResources(t *testing.T) {
	keyPairs := []inventory.KeyPair{
		{KeyPairID: "key-1", KeyName: "my-laptop-key", Region: "us-east-1", Tags: map[string]string{"Owner": "rsdoiel"}},
	}
	got := keyPairTaggedResources(keyPairs)
	want := []ui.TaggedResource{
		{ID: "key-1", Label: keyPairLabel(keyPairs[0]), Tags: map[string]string{"Owner": "rsdoiel"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// showAllTags's kind picker itself is pipe-testable, but every branch
// dispatches into ui.DisplayAllTags (tui.RunListView, a real bubbletea
// Program) that can't be pipe-tested -- and, per the huh-EOF gotcha
// documented in manage_tags.go (isCancellation/manageTagsForResource's
// doc comment), an exhausted accessible-mode input on a
// PointerAccessor-backed huh.Select doesn't surface as an error either;
// it silently defaults to option 1 and would drive that real Program.
// So unlike manageResourceTags (which has "no resources of this kind"
// early-return branches reachable before any Picker/List-tier call),
// showAllTags has no branch reachable via pipe input alone that stops
// short of the interactive loop -- only its row-building conversion
// functions above and tagsListViewConfig (internal/ui) are unit-tested
// directly.

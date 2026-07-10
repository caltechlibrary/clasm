package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestApplyTagChange_Add(t *testing.T) {
	fake := &fakeEC2Client{}
	err := ApplyTagChange(context.Background(), fake, TagChangeParams{ResourceID: "i-1", Action: "add", Key: "Owner", Value: "dld"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	in := fake.lastCreateTagsInput
	if in == nil || in.Resources[0] != "i-1" || len(in.Tags) != 1 || aws.ToString(in.Tags[0].Key) != "Owner" || aws.ToString(in.Tags[0].Value) != "dld" {
		t.Errorf("CreateTags called with %+v, want Resources=[i-1] Tags=[Owner=dld]", in)
	}
}

func TestApplyTagChange_Update(t *testing.T) {
	fake := &fakeEC2Client{}
	err := ApplyTagChange(context.Background(), fake, TagChangeParams{ResourceID: "ami-1", Action: "update", Key: "Environment", Value: "production"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastCreateTagsInput == nil {
		t.Fatal("CreateTags was never called for an update")
	}
}

func TestApplyTagChange_Remove(t *testing.T) {
	fake := &fakeEC2Client{}
	err := ApplyTagChange(context.Background(), fake, TagChangeParams{ResourceID: "i-1", Action: "remove", Key: "Owner"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	in := fake.lastDeleteTagsInput
	if in == nil || in.Resources[0] != "i-1" || len(in.Tags) != 1 || aws.ToString(in.Tags[0].Key) != "Owner" {
		t.Errorf("DeleteTags called with %+v, want Resources=[i-1] Tags=[Owner]", in)
	}
	if in.Tags[0].Value != nil {
		t.Errorf("DeleteTags Tags[0].Value = %v, want nil (delete regardless of value)", aws.ToString(in.Tags[0].Value))
	}
}

func TestApplyTagChange_CreateTagsFailure(t *testing.T) {
	fake := &fakeEC2Client{createTagsErr: errors.New("boom")}
	err := ApplyTagChange(context.Background(), fake, TagChangeParams{ResourceID: "i-1", Action: "add", Key: "K", Value: "V"})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestApplyTagChange_DeleteTagsFailure(t *testing.T) {
	fake := &fakeEC2Client{deleteTagsErr: errors.New("boom")}
	err := ApplyTagChange(context.Background(), fake, TagChangeParams{ResourceID: "i-1", Action: "remove", Key: "K"})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestFetchInstanceTags(t *testing.T) {
	fake := &fakeEC2Client{instanceTags: []types.Tag{
		{Key: aws.String("Name"), Value: aws.String("web")},
		{Key: aws.String("Project"), Value: aws.String("caltechauthors")},
	}}
	got, err := fetchInstanceTags(context.Background(), fake, "i-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["Name"] != "web" || got["Project"] != "caltechauthors" {
		t.Errorf("got %+v, want Name=web Project=caltechauthors", got)
	}
}

func TestFetchImageTags(t *testing.T) {
	fake := &fakeEC2Client{describeImagesTags: []types.Tag{
		{Key: aws.String("Environment"), Value: aws.String("production")},
	}}
	got, err := fetchImageTags(context.Background(), fake, "ami-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["Environment"] != "production" {
		t.Errorf("got %+v, want Environment=production", got)
	}
}

// The Add/Update/Remove action and select-a-tag pickers converted to
// huh.Select (DESIGN.md's full conversion punch list): their selections
// are fed via a separate newHuhAccessibleInput reader (menuInput), not
// le, which still feeds every other prompt in this function (key/value
// input, confirms). The Instance-vs-AMI kind picker and the instance/AMI
// picker itself (also converted to tui.RunPicker, Picker tier -- a real
// bubbletea Program that can't be pipe-tested) both now run in
// manageTags, before manageTagsForResource -- tests below call
// manageTagsForResource directly with an already-resolved resource;
// manageTags' own kind/picker-selection steps are covered only by
// manual/interactive verification, the same accepted limitation this
// session's other Picker-tier conversions already have.

func TestManageTags_AddOnInstance(t *testing.T) {
	fake := &fakeEC2Client{}
	tags, err := fetchInstanceTags(context.Background(), fake, "i-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	input := "Owner\n" + // key
		"dld\n" + // value
		"y\n" // confirm
	term, le, buf := newPipeEditor(t, input)

	err = manageTagsForResource(context.Background(), term, le, fake, "i-1", "i-1 - web", tags, newHuhAccessibleInput("1\n"), buf) // Add
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastCreateTagsInput == nil || fake.lastCreateTagsInput.Resources[0] != "i-1" {
		t.Errorf("CreateTags called with %+v, want Resources=[i-1]", fake.lastCreateTagsInput)
	}
}

func TestManageTags_UpdateOnAMI(t *testing.T) {
	fake := &fakeEC2Client{describeImagesTags: []types.Tag{{Key: aws.String("Project"), Value: aws.String("caltechdata")}}}
	tags, err := fetchImageTags(context.Background(), fake, "ami-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	input := "caltechauthors\n" + // new value
		"y\n" // confirm
	term, le, buf := newPipeEditor(t, input)

	err = manageTagsForResource(context.Background(), term, le, fake, "ami-1", "ami-1 - base", tags, newHuhAccessibleInput("2\n1\n"), buf) // Update, Project
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	in := fake.lastCreateTagsInput
	if in == nil || in.Resources[0] != "ami-1" || aws.ToString(in.Tags[0].Key) != "Project" || aws.ToString(in.Tags[0].Value) != "caltechauthors" {
		t.Errorf("CreateTags called with %+v, want ami-1 Project=caltechauthors", in)
	}
}

func TestManageTags_RemoveOnInstance(t *testing.T) {
	fake := &fakeEC2Client{instanceTags: []types.Tag{{Key: aws.String("Owner"), Value: aws.String("dld")}}}
	tags, err := fetchInstanceTags(context.Background(), fake, "i-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	term, le, buf := newPipeEditor(t, "y\n") // confirm

	err = manageTagsForResource(context.Background(), term, le, fake, "i-1", "i-1 - web", tags, newHuhAccessibleInput("3\n1\n"), buf) // Remove, Owner
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	in := fake.lastDeleteTagsInput
	if in == nil || in.Resources[0] != "i-1" || aws.ToString(in.Tags[0].Key) != "Owner" {
		t.Errorf("DeleteTags called with %+v, want i-1 Owner", in)
	}
}

func TestManageTags_EnvironmentNoteShown(t *testing.T) {
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor(t, "Environment\nproduction\ny\n")

	err := manageTagsForResource(context.Background(), term, le, fake, "i-1", "i-1 - web", nil, newHuhAccessibleInput("1\n"), buf) // Add
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "production warnings") {
		t.Errorf("expected the Environment note in output, got:\n%s", buf.String())
	}
}

func TestManageTags_DeclinedConfirmationDoesNotApply(t *testing.T) {
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor(t, "Owner\ndld\nn\n")

	err := manageTagsForResource(context.Background(), term, le, fake, "i-1", "i-1 - web", nil, newHuhAccessibleInput("1\n"), buf) // Add
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastCreateTagsInput != nil {
		t.Error("CreateTags was called despite a declined confirmation")
	}
}

func TestManageTags_NoExistingTagsToUpdate(t *testing.T) {
	fake := &fakeEC2Client{} // no tags -> empty tag map
	term, le, buf := newPipeEditor(t, "")

	err := manageTagsForResource(context.Background(), term, le, fake, "i-1", "i-1 - web", nil, newHuhAccessibleInput("2\n"), buf) // Update
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No existing tags") {
		t.Errorf("expected a no-existing-tags message, got:\n%s", buf.String())
	}
}

func TestManageTags_RejectsBlankTagKeyOnAdd(t *testing.T) {
	fake := &fakeEC2Client{}
	input := "\nOwner\ndld\ny\n" // blank key (rejected), retry key, value, confirm
	term, le, buf := newPipeEditor(t, input)

	err := manageTagsForResource(context.Background(), term, le, fake, "i-1", "i-1 - web", nil, newHuhAccessibleInput("1\n"), buf) // Add
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastCreateTagsInput == nil || aws.ToString(fake.lastCreateTagsInput.Tags[0].Key) != "Owner" {
		t.Errorf("CreateTags called with %+v, want Key=Owner", fake.lastCreateTagsInput)
	}
	if !strings.Contains(buf.String(), "invalid input") {
		t.Errorf("expected a validation error message for the blank key, got:\n%s", buf.String())
	}
}

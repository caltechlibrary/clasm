package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/inventory"
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

func TestManageTags_AddOnInstance(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", State: "running", Region: "us-east-1"}}
	input := "1\n" + // pick "Instance"
		"1\n" + // pick i-1
		"1\n" + // pick "Add"
		"Owner\n" + // key
		"dld\n" + // value
		"y\n" // confirm
	term, le, _ := newPipeEditor(t, input)
	fake := &fakeEC2Client{}

	err := ManageTags(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastCreateTagsInput == nil || fake.lastCreateTagsInput.Resources[0] != "i-1" {
		t.Errorf("CreateTags called with %+v, want Resources=[i-1]", fake.lastCreateTagsInput)
	}
}

func TestManageTags_UpdateOnAMI(t *testing.T) {
	images := []inventory.Image{{ImageID: "ami-1", Name: "base", Region: "us-east-1"}}
	fake := &fakeEC2Client{describeImagesTags: []types.Tag{{Key: aws.String("Project"), Value: aws.String("caltechdata")}}}
	input := "2\n" + // pick "AMI"
		"1\n" + // pick ami-1
		"2\n" + // pick "Update"
		"1\n" + // pick the only existing key (Project)
		"caltechauthors\n" + // new value
		"y\n" // confirm
	term, le, _ := newPipeEditor(t, input)

	err := ManageTags(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, nil, images)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	in := fake.lastCreateTagsInput
	if in == nil || in.Resources[0] != "ami-1" || aws.ToString(in.Tags[0].Key) != "Project" || aws.ToString(in.Tags[0].Value) != "caltechauthors" {
		t.Errorf("CreateTags called with %+v, want ami-1 Project=caltechauthors", in)
	}
}

func TestManageTags_RemoveOnInstance(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", Region: "us-east-1"}}
	fake := &fakeEC2Client{instanceTags: []types.Tag{{Key: aws.String("Owner"), Value: aws.String("dld")}}}
	input := "1\n" + // Instance
		"1\n" + // i-1
		"3\n" + // Remove
		"1\n" + // pick the only key (Owner)
		"y\n" // confirm
	term, le, _ := newPipeEditor(t, input)

	err := ManageTags(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	in := fake.lastDeleteTagsInput
	if in == nil || in.Resources[0] != "i-1" || aws.ToString(in.Tags[0].Key) != "Owner" {
		t.Errorf("DeleteTags called with %+v, want i-1 Owner", in)
	}
}

func TestManageTags_EnvironmentNoteShown(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", Region: "us-east-1"}}
	fake := &fakeEC2Client{}
	input := "1\n1\n1\nEnvironment\nproduction\ny\n"
	term, le, buf := newPipeEditor(t, input)

	err := ManageTags(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "production warnings") {
		t.Errorf("expected the Environment note in output, got:\n%s", buf.String())
	}
}

func TestManageTags_DeclinedConfirmationDoesNotApply(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", Region: "us-east-1"}}
	fake := &fakeEC2Client{}
	input := "1\n1\n1\nOwner\ndld\nn\n"
	term, le, _ := newPipeEditor(t, input)

	err := ManageTags(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastCreateTagsInput != nil {
		t.Error("CreateTags was called despite a declined confirmation")
	}
}

func TestManageTags_NoExistingTagsToUpdate(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", Region: "us-east-1"}}
	fake := &fakeEC2Client{} // no instanceTags set -> empty tag map
	input := "1\n1\n2\n"     // Instance, i-1, Update
	term, le, buf := newPipeEditor(t, input)

	err := ManageTags(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No existing tags") {
		t.Errorf("expected a no-existing-tags message, got:\n%s", buf.String())
	}
}

func TestManageTags_RejectsBlankTagKeyOnAdd(t *testing.T) {
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", Region: "us-east-1"}}
	fake := &fakeEC2Client{}
	input := "1\n1\n1\n\nOwner\ndld\ny\n" // Instance, i-1, Add, blank key (rejected), retry key, value, confirm
	term, le, buf := newPipeEditor(t, input)

	err := ManageTags(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, instances, nil)
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

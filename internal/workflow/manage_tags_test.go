package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// statefulTagsFakeEC2Client is a minimal stateful fake, distinct from
// the shared fakeEC2Client (which just echoes back whatever
// f.instanceTags was configured with once, regardless of any
// CreateTags/DeleteTags call) -- TestManageTags_LoopRefreshesTagsAfterChange
// needs a *second* DescribeInstances (triggered by the loop's own
// post-change refetch) to actually reflect a CreateTags call that just
// happened, which the shared fake was never built to simulate.
type statefulTagsFakeEC2Client struct {
	awsclient.EC2API
	tags map[string]string
}

func (f *statefulTagsFakeEC2Client) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	var tags []types.Tag
	for k, v := range f.tags {
		tags = append(tags, types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	inst := types.Instance{
		InstanceId: aws.String(params.InstanceIds[0]),
		State:      &types.InstanceState{Name: types.InstanceStateNameRunning},
		Tags:       tags,
	}
	return &ec2.DescribeInstancesOutput{Reservations: []types.Reservation{{Instances: []types.Instance{inst}}}}, nil
}

func (f *statefulTagsFakeEC2Client) CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	if f.tags == nil {
		f.tags = map[string]string{}
	}
	for _, t := range params.Tags {
		f.tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return &ec2.CreateTagsOutput{}, nil
}

func (f *statefulTagsFakeEC2Client) DeleteTags(ctx context.Context, params *ec2.DeleteTagsInput, optFns ...func(*ec2.Options)) (*ec2.DeleteTagsOutput, error) {
	for _, t := range params.Tags {
		delete(f.tags, aws.ToString(t.Key))
	}
	return &ec2.DeleteTagsOutput{}, nil
}

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

// The Add/Update/Remove action menu, the select-a-tag pickers, and
// every other prompt in this function (key/value input, confirms) now
// share one accessible-mode reader, read in sequence one line at a time
// -- the action choice first, then whatever that action needs. The
// Instance-vs-AMI kind picker and the instance/AMI picker itself (also
// converted to tui.RunPicker, Picker tier -- a real bubbletea Program
// that can't be pipe-tested) both now run in manageTags, before
// manageTagsForResource -- tests below call manageTagsForResource
// directly with an already-resolved resource; manageTags' own
// kind/picker-selection steps are covered only by manual/interactive
// verification, the same accepted limitation this session's other
// Picker-tier conversions already have.

// applyOneTagChange is directly, simply testable: it's a single,
// non-looping function, so these tests exercise it straight rather
// than through manageTagsForResource's own loop -- the loop itself
// (which really does need to run more than one iteration in a test) is
// covered separately below, with explicit ctx-cancellation.

func TestManageTags_AddOnInstance(t *testing.T) {
	fake := &fakeEC2Client{}
	input := "Owner\n" + "dld\n" + "y\n" // key, value, confirm
	term, menuInput, buf := newPipeEditor(input)

	changed, err := applyOneTagChange(context.Background(), term, fake, "i-1", "i-1 - web", "Add", nil, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	if fake.lastCreateTagsInput == nil || fake.lastCreateTagsInput.Resources[0] != "i-1" {
		t.Errorf("CreateTags called with %+v, want Resources=[i-1]", fake.lastCreateTagsInput)
	}
}

func TestManageTags_UpdateOnAMI(t *testing.T) {
	fake := &fakeEC2Client{}
	tags := map[string]string{"Project": "caltechdata"}
	input := "1\n" + "caltechauthors\n" + "y\n" // pick Project (only tag), new value, confirm
	term, menuInput, buf := newPipeEditor(input)

	changed, err := applyOneTagChange(context.Background(), term, fake, "ami-1", "ami-1 - base", "Update", tags, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	in := fake.lastCreateTagsInput
	if in == nil || in.Resources[0] != "ami-1" || aws.ToString(in.Tags[0].Key) != "Project" || aws.ToString(in.Tags[0].Value) != "caltechauthors" {
		t.Errorf("CreateTags called with %+v, want ami-1 Project=caltechauthors", in)
	}
}

func TestManageTags_RemoveOnInstance(t *testing.T) {
	fake := &fakeEC2Client{}
	tags := map[string]string{"Owner": "dld"}
	term, menuInput, buf := newPipeEditor("1\ny\n") // pick Owner (only tag), confirm

	changed, err := applyOneTagChange(context.Background(), term, fake, "i-1", "i-1 - web", "Remove", tags, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	in := fake.lastDeleteTagsInput
	if in == nil || in.Resources[0] != "i-1" || aws.ToString(in.Tags[0].Key) != "Owner" {
		t.Errorf("DeleteTags called with %+v, want i-1 Owner", in)
	}
}

func TestManageTags_EnvironmentNoteShown(t *testing.T) {
	fake := &fakeEC2Client{}
	term, menuInput, buf := newPipeEditor("Environment\nproduction\ny\n")

	_, err := applyOneTagChange(context.Background(), term, fake, "i-1", "i-1 - web", "Add", nil, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "production warnings") {
		t.Errorf("expected the Environment note in output, got:\n%s", buf.String())
	}
}

func TestManageTags_DeclinedConfirmationDoesNotApply(t *testing.T) {
	fake := &fakeEC2Client{}
	term, menuInput, buf := newPipeEditor("Owner\ndld\nn\n")

	changed, err := applyOneTagChange(context.Background(), term, fake, "i-1", "i-1 - web", "Add", nil, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("changed = true, want false (confirmation was declined)")
	}
	if fake.lastCreateTagsInput != nil {
		t.Error("CreateTags was called despite a declined confirmation")
	}
}

func TestManageTags_NoExistingTagsToUpdate(t *testing.T) {
	fake := &fakeEC2Client{}
	term, menuInput, buf := newPipeEditor("") // Update needs no input at all -- there's nothing to pick

	changed, err := applyOneTagChange(context.Background(), term, fake, "i-1", "i-1 - web", "Update", nil, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("changed = true, want false (no existing tags)")
	}
	if !strings.Contains(buf.String(), "No existing tags") {
		t.Errorf("expected a no-existing-tags message, got:\n%s", buf.String())
	}
}

func TestManageTags_RejectsBlankTagKeyOnAdd(t *testing.T) {
	fake := &fakeEC2Client{}
	input := "\nOwner\ndld\ny\n" // blank key (rejected), retry key, value, confirm
	term, menuInput, buf := newPipeEditor(input)

	changed, err := applyOneTagChange(context.Background(), term, fake, "i-1", "i-1 - web", "Add", nil, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	if fake.lastCreateTagsInput == nil || aws.ToString(fake.lastCreateTagsInput.Tags[0].Key) != "Owner" {
		t.Errorf("CreateTags called with %+v, want Key=Owner", fake.lastCreateTagsInput)
	}
	if !strings.Contains(buf.String(), "invalid input") {
		t.Errorf("expected a validation error message for the blank key, got:\n%s", buf.String())
	}
}

// cancelAfterNFetches wraps a fetchTags closure so ctx is cancelled
// after its Nth call. manageTagsForResource's loop can only be driven
// through a test at all by cancelling ctx at the exact point the test
// wants it to stop -- huh's own accessible-mode Select has no way to
// signal "the input is exhausted" (confirmed by reading
// internal/accessibility.PromptString: on EOF it silently defaults to
// the first option and returns nil, it does not error), so simply
// running out of scripted input would spin the loop forever
// reconstructing forms rather than stopping it. This mirrors
// menu_test.go's own cancelingAction, adapted to trigger from a data
// fetch instead of a dispatched menu action.
func cancelAfterNFetches(n int, cancel context.CancelFunc, fetch func(context.Context) (map[string]string, error)) func(context.Context) (map[string]string, error) {
	calls := 0
	return func(ctx context.Context) (map[string]string, error) {
		calls++
		tags, err := fetch(ctx)
		if calls >= n {
			cancel()
		}
		return tags, err
	}
}

// TestManageTags_ShowTagsRedisplaysAndContinues covers the literal ask
// in the bug report: a "Show tags" choice exists, and choosing it
// doesn't exit the workflow -- the operator lands back at the action
// picker afterward (here: Add, to prove the loop kept going).
func TestManageTags_ShowTagsRedisplaysAndContinues(t *testing.T) {
	fake := &fakeEC2Client{instanceTags: []types.Tag{{Key: aws.String("Owner"), Value: aws.String("dld")}}}
	tags, err := fetchInstanceTags(context.Background(), fake, "i-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	input := "1\n" + // Show tags
		"2\n" + // Add
		"Project\n" + "caltechauthors\n" + "y\n" // confirm
	term, menuInput, buf := newPipeEditor(input)

	ctx, cancel := context.WithCancel(context.Background())
	fetchTags := cancelAfterNFetches(1, cancel, func(ctx context.Context) (map[string]string, error) {
		return fetchInstanceTags(ctx, fake, "i-1")
	})

	err = manageTagsForResource(ctx, term, fake, "i-1", "i-1 - web", tags, fetchTags, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastCreateTagsInput == nil || aws.ToString(fake.lastCreateTagsInput.Tags[0].Key) != "Project" {
		t.Errorf("CreateTags called with %+v, want Key=Project (Show tags must not have exited the loop)", fake.lastCreateTagsInput)
	}
	// "Owner" (the tag shown before "Show tags" was chosen) must still
	// appear -- Show tags redisplays, it doesn't clear anything.
	if !strings.Contains(buf.String(), "Owner") {
		t.Errorf("expected the original tag to still be shown, got:\n%s", buf.String())
	}
}

// TestManageTags_LoopRefreshesTagsAfterChange is the actual bug fix:
// after a successful Add, the loop must refresh from AWS before
// looping back, not keep showing the pre-change snapshot -- proven
// here by immediately Updating the tag that was *just* added, which
// only appears in the Update tag-picker's option list if the refresh
// happened.
func TestManageTags_LoopRefreshesTagsAfterChange(t *testing.T) {
	fake := &statefulTagsFakeEC2Client{}
	input := "2\n" + "Project\n" + "caltechauthors\n" + "y\n" + // Add Project=caltechauthors
		"3\n" + "1\n" + "caltechdata\n" + "y\n" // Update, the only tag (must be "Project"), new value, confirm

	term, menuInput, buf := newPipeEditor(input)
	ctx, cancel := context.WithCancel(context.Background())
	fetchTags := cancelAfterNFetches(2, cancel, func(ctx context.Context) (map[string]string, error) {
		return fetchInstanceTags(ctx, fake, "i-1")
	})

	err := manageTagsForResource(ctx, term, fake, "i-1", "i-1 - web", nil, fetchTags, menuInput, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fake.tags["Project"] != "caltechdata" {
		t.Errorf("tags = %+v, want Project=caltechdata (the Update must have found \"Project\" -- only possible if the post-Add refresh worked)", fake.tags)
	}
}

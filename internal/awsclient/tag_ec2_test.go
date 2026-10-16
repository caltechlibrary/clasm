package awsclient

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// Everything clasm creates in EC2 carries the Origin tag (DR-0190), the same tag
// and the same way as IAM (DR-0189): one wrapper on the client, so no workflow can
// forget it. Each call is tagged for every resource type it can tag, so the volumes,
// network interfaces and snapshots behind an instance or an AMI are not left out.

type recordingEC2 struct {
	EC2API
	run      *ec2.RunInstancesInput
	template *ec2.CreateLaunchTemplateInput
	version  *ec2.CreateLaunchTemplateVersionInput
	image    *ec2.CreateImageInput
	create   *ec2.CreateKeyPairInput
	imp      *ec2.ImportKeyPairInput
	describe int
	err      error
}

func (r *recordingEC2) RunInstances(_ context.Context, p *ec2.RunInstancesInput, _ ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	r.run = p
	return &ec2.RunInstancesOutput{}, r.err
}

func (r *recordingEC2) CreateLaunchTemplate(_ context.Context, p *ec2.CreateLaunchTemplateInput, _ ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateOutput, error) {
	r.template = p
	return &ec2.CreateLaunchTemplateOutput{}, r.err
}

func (r *recordingEC2) CreateLaunchTemplateVersion(_ context.Context, p *ec2.CreateLaunchTemplateVersionInput, _ ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error) {
	r.version = p
	return &ec2.CreateLaunchTemplateVersionOutput{}, r.err
}

func (r *recordingEC2) CreateImage(_ context.Context, p *ec2.CreateImageInput, _ ...func(*ec2.Options)) (*ec2.CreateImageOutput, error) {
	r.image = p
	return &ec2.CreateImageOutput{}, r.err
}

func (r *recordingEC2) CreateKeyPair(_ context.Context, p *ec2.CreateKeyPairInput, _ ...func(*ec2.Options)) (*ec2.CreateKeyPairOutput, error) {
	r.create = p
	return &ec2.CreateKeyPairOutput{}, r.err
}

func (r *recordingEC2) ImportKeyPair(_ context.Context, p *ec2.ImportKeyPairInput, _ ...func(*ec2.Options)) (*ec2.ImportKeyPairOutput, error) {
	r.imp = p
	return &ec2.ImportKeyPairOutput{}, r.err
}

func (r *recordingEC2) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	r.describe++
	return &ec2.DescribeInstancesOutput{}, nil
}

func tag(k, v string) types.Tag { return types.Tag{Key: aws.String(k), Value: aws.String(v)} }

// specFor is the specification for one resource type, or nil.
func specFor(specs []types.TagSpecification, rt types.ResourceType) *types.TagSpecification {
	for i := range specs {
		if specs[i].ResourceType == rt {
			return &specs[i]
		}
	}
	return nil
}

func tagValue(tags []types.Tag, key string) (string, int) {
	n, v := 0, ""
	for _, t := range tags {
		if aws.ToString(t.Key) == key {
			n++
			v = aws.ToString(t.Value)
		}
	}
	return v, n
}

func wantOrigin(t *testing.T, what string, tags []types.Tag) {
	t.Helper()
	if v, n := tagValue(tags, "origin"); v != "dld" || n != 1 {
		t.Errorf("%s: want exactly one origin=dld, got %+v", what, tags)
	}
}

func TestTagEC2Creations_RunInstancesTagsTheInstanceItsVolumesAndItsInterfaces(t *testing.T) {
	rec := &recordingEC2{}
	in := &ec2.RunInstancesInput{
		ImageId: aws.String("ami-1"),
		TagSpecifications: []types.TagSpecification{
			{ResourceType: types.ResourceTypeInstance, Tags: []types.Tag{tag("Name", "box"), tag("project", "authors")}},
		},
	}
	if _, err := TagEC2Creations(rec, dld).RunInstances(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	inst := specFor(rec.run.TagSpecifications, types.ResourceTypeInstance)
	if inst == nil {
		t.Fatal("the instance specification must stay")
	}
	wantOrigin(t, "instance", inst.Tags)
	if v, _ := tagValue(inst.Tags, "Name"); v != "box" {
		t.Errorf("the instance's own tags must be kept: %+v", inst.Tags)
	}
	for _, rt := range []types.ResourceType{types.ResourceTypeVolume, types.ResourceTypeNetworkInterface} {
		spec := specFor(rec.run.TagSpecifications, rt)
		if spec == nil {
			t.Errorf("%s: a specification must be added", rt)
			continue
		}
		wantOrigin(t, string(rt), spec.Tags)
	}
	if aws.ToString(rec.run.ImageId) != "ami-1" {
		t.Errorf("the rest of the request must pass through")
	}
}

func TestTagEC2Creations_RunInstancesWithNoSpecificationAtAllStillGetsTheTag(t *testing.T) {
	rec := &recordingEC2{}
	if _, err := TagEC2Creations(rec, dld).RunInstances(context.Background(), &ec2.RunInstancesInput{}); err != nil {
		t.Fatal(err)
	}
	for _, rt := range []types.ResourceType{types.ResourceTypeInstance, types.ResourceTypeVolume, types.ResourceTypeNetworkInterface} {
		spec := specFor(rec.run.TagSpecifications, rt)
		if spec == nil {
			t.Fatalf("%s: a specification must be added", rt)
		}
		wantOrigin(t, string(rt), spec.Tags)
	}
}

// A launch that names a launch template is tagged too (DR-0191). Real AWS showed that
// a tag specification sent with RunInstances is merged into the template's, nothing
// dropped, so adding Origin is additive and covers launches from templates made before
// the tag existed. The template reference itself is left as it is.
func TestTagEC2Creations_ALaunchFromATemplateIsTaggedToo(t *testing.T) {
	rec := &recordingEC2{}
	in := &ec2.RunInstancesInput{LaunchTemplate: &types.LaunchTemplateSpecification{LaunchTemplateId: aws.String("lt-1"), Version: aws.String("$Default")}}
	if _, err := TagEC2Creations(rec, dld).RunInstances(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	for _, rt := range []types.ResourceType{types.ResourceTypeInstance, types.ResourceTypeVolume, types.ResourceTypeNetworkInterface} {
		spec := specFor(rec.run.TagSpecifications, rt)
		if spec == nil {
			t.Errorf("%s: a specification must be added to a template launch", rt)
			continue
		}
		wantOrigin(t, string(rt), spec.Tags)
	}
	if rec.run.LaunchTemplate == nil || aws.ToString(rec.run.LaunchTemplate.LaunchTemplateId) != "lt-1" || aws.ToString(rec.run.LaunchTemplate.Version) != "$Default" {
		t.Errorf("the template reference must pass through: %+v", rec.run.LaunchTemplate)
	}
	if len(in.TagSpecifications) != 0 {
		t.Errorf("the caller's request must not be modified: %+v", in.TagSpecifications)
	}
}

func TestTagEC2Creations_ATemplateLaunchWithItsOwnOriginTagKeepsIt(t *testing.T) {
	rec := &recordingEC2{}
	in := &ec2.RunInstancesInput{
		LaunchTemplate:    &types.LaunchTemplateSpecification{LaunchTemplateId: aws.String("lt-1")},
		TagSpecifications: []types.TagSpecification{{ResourceType: types.ResourceTypeInstance, Tags: []types.Tag{tag("origin", "someone-else")}}},
	}
	if _, err := TagEC2Creations(rec, dld).RunInstances(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if v, n := tagValue(specFor(rec.run.TagSpecifications, types.ResourceTypeInstance).Tags, "origin"); v != "someone-else" || n != 1 {
		t.Errorf("an explicit value wins and is not doubled")
	}
}

func TestTagEC2Creations_CreateLaunchTemplateTagsTheTemplateAndWhatItLaunches(t *testing.T) {
	rec := &recordingEC2{}
	in := &ec2.CreateLaunchTemplateInput{
		LaunchTemplateName: aws.String("lt"),
		TagSpecifications:  []types.TagSpecification{{ResourceType: types.ResourceTypeLaunchTemplate, Tags: []types.Tag{tag("Name", "lt")}}},
		LaunchTemplateData: &types.RequestLaunchTemplateData{
			ImageId: aws.String("ami-1"),
			TagSpecifications: []types.LaunchTemplateTagSpecificationRequest{
				{ResourceType: types.ResourceTypeInstance, Tags: []types.Tag{tag("Name", "box"), tag("templateName", "lt")}},
			},
		},
	}
	if _, err := TagEC2Creations(rec, dld).CreateLaunchTemplate(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	tmpl := specFor(rec.template.TagSpecifications, types.ResourceTypeLaunchTemplate)
	if tmpl == nil {
		t.Fatal("the template specification must stay")
	}
	wantOrigin(t, "template", tmpl.Tags)
	if v, _ := tagValue(tmpl.Tags, "Name"); v != "lt" {
		t.Errorf("the template's own tag must be kept: %+v", tmpl.Tags)
	}
	data := rec.template.LaunchTemplateData.TagSpecifications
	found := map[types.ResourceType]bool{}
	for _, s := range data {
		found[s.ResourceType] = true
		wantOrigin(t, "data "+string(s.ResourceType), s.Tags)
		if s.ResourceType == types.ResourceTypeInstance {
			if v, _ := tagValue(s.Tags, "templateName"); v != "lt" {
				t.Errorf("the instance specification's own tags must be kept: %+v", s.Tags)
			}
		}
	}
	for _, rt := range []types.ResourceType{types.ResourceTypeInstance, types.ResourceTypeVolume, types.ResourceTypeNetworkInterface} {
		if !found[rt] {
			t.Errorf("the template's data must tag launched %s too", rt)
		}
	}
	if aws.ToString(rec.template.LaunchTemplateData.ImageId) != "ami-1" {
		t.Errorf("the rest of the template data must pass through")
	}
}

func TestTagEC2Creations_CreateLaunchTemplateWithNoDataTagsOnlyTheTemplate(t *testing.T) {
	rec := &recordingEC2{}
	if _, err := TagEC2Creations(rec, dld).CreateLaunchTemplate(context.Background(), &ec2.CreateLaunchTemplateInput{LaunchTemplateName: aws.String("lt")}); err != nil {
		t.Fatal(err)
	}
	if rec.template.LaunchTemplateData != nil {
		t.Errorf("no template data was given, none may be invented: %+v", rec.template.LaunchTemplateData)
	}
	if spec := specFor(rec.template.TagSpecifications, types.ResourceTypeLaunchTemplate); spec == nil {
		t.Errorf("the template itself is still tagged")
	} else {
		wantOrigin(t, "template", spec.Tags)
	}
}

// A version inherits its source's specification, and sending one could replace it.
func TestTagEC2Creations_ALaunchTemplateVersionIsNotTouched(t *testing.T) {
	rec := &recordingEC2{}
	in := &ec2.CreateLaunchTemplateVersionInput{LaunchTemplateData: &types.RequestLaunchTemplateData{ImageId: aws.String("ami-2")}}
	if _, err := TagEC2Creations(rec, dld).CreateLaunchTemplateVersion(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if rec.version != in || len(rec.version.LaunchTemplateData.TagSpecifications) != 0 {
		t.Errorf("a version must reach the inner client untouched: %+v", rec.version)
	}
}

func TestTagEC2Creations_CreateImageTagsTheImageAndItsSnapshots(t *testing.T) {
	rec := &recordingEC2{}
	in := &ec2.CreateImageInput{
		InstanceId:        aws.String("i-1"),
		Name:              aws.String("ami"),
		TagSpecifications: []types.TagSpecification{{ResourceType: types.ResourceTypeImage, Tags: []types.Tag{tag("Name", "ami")}}},
	}
	if _, err := TagEC2Creations(rec, dld).CreateImage(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	img := specFor(rec.image.TagSpecifications, types.ResourceTypeImage)
	snap := specFor(rec.image.TagSpecifications, types.ResourceTypeSnapshot)
	if img == nil || snap == nil {
		t.Fatalf("want an image and a snapshot specification: %+v", rec.image.TagSpecifications)
	}
	wantOrigin(t, "image", img.Tags)
	wantOrigin(t, "snapshot", snap.Tags)
	if v, _ := tagValue(img.Tags, "Name"); v != "ami" {
		t.Errorf("the image's own tag must be kept: %+v", img.Tags)
	}
}

func TestTagEC2Creations_KeyPairsAreTaggedWhetherCreatedOrImported(t *testing.T) {
	rec := &recordingEC2{}
	c := TagEC2Creations(rec, dld)
	ctx := context.Background()
	if _, err := c.CreateKeyPair(ctx, &ec2.CreateKeyPairInput{KeyName: aws.String("k")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ImportKeyPair(ctx, &ec2.ImportKeyPairInput{KeyName: aws.String("k2"), PublicKeyMaterial: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	for what, specs := range map[string][]types.TagSpecification{"create": rec.create.TagSpecifications, "import": rec.imp.TagSpecifications} {
		spec := specFor(specs, types.ResourceTypeKeyPair)
		if spec == nil {
			t.Errorf("%s: a key-pair specification must be added", what)
			continue
		}
		wantOrigin(t, what, spec.Tags)
	}
	if aws.ToString(rec.create.KeyName) != "k" || string(rec.imp.PublicKeyMaterial) != "x" {
		t.Errorf("the rest of each request must pass through")
	}
}

func TestTagEC2Creations_AnExplicitOriginTagWinsAndIsNeverDuplicated(t *testing.T) {
	rec := &recordingEC2{}
	in := &ec2.CreateKeyPairInput{TagSpecifications: []types.TagSpecification{{ResourceType: types.ResourceTypeKeyPair, Tags: []types.Tag{tag("origin", "someone-else")}}}}
	if _, err := TagEC2Creations(rec, dld).CreateKeyPair(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	spec := specFor(rec.create.TagSpecifications, types.ResourceTypeKeyPair)
	if v, n := tagValue(spec.Tags, "origin"); v != "someone-else" || n != 1 {
		t.Errorf("an explicit value wins and is not doubled: %+v", spec.Tags)
	}
}

func TestTagEC2Creations_NothingConfiguredMeansNothingAdded(t *testing.T) {
	for name, get := range map[string]func() (string, string){
		"no value": func() (string, string) { return "origin", "" },
		"no key":   func() (string, string) { return "", "dld" },
	} {
		rec := &recordingEC2{}
		c := TagEC2Creations(rec, get)
		_, _ = c.RunInstances(context.Background(), &ec2.RunInstancesInput{})
		_, _ = c.CreateImage(context.Background(), &ec2.CreateImageInput{})
		_, _ = c.CreateKeyPair(context.Background(), &ec2.CreateKeyPairInput{})
		_, _ = c.CreateLaunchTemplate(context.Background(), &ec2.CreateLaunchTemplateInput{LaunchTemplateData: &types.RequestLaunchTemplateData{}})
		if len(rec.run.TagSpecifications) != 0 || len(rec.image.TagSpecifications) != 0 || len(rec.create.TagSpecifications) != 0 ||
			len(rec.template.TagSpecifications) != 0 || len(rec.template.LaunchTemplateData.TagSpecifications) != 0 {
			t.Errorf("%s: nothing may be added", name)
		}
	}
}

func TestTagEC2Creations_TheConfigurationIsReadAtEachCall(t *testing.T) {
	rec := &recordingEC2{}
	value := "dld"
	c := TagEC2Creations(rec, func() (string, string) { return "origin", value })
	_, _ = c.CreateKeyPair(context.Background(), &ec2.CreateKeyPairInput{})
	value = "other"
	_, _ = c.CreateKeyPair(context.Background(), &ec2.CreateKeyPairInput{})
	if v, _ := tagValue(specFor(rec.create.TagSpecifications, types.ResourceTypeKeyPair).Tags, "origin"); v != "other" {
		t.Errorf("the second call must carry the new value, got %q", v)
	}
}

// The caller's request, including the tag slices inside it, is left as it was: a
// retry or a log of the request must not see tags it never wrote.
func TestTagEC2Creations_TheCallersRequestIsNotMutated(t *testing.T) {
	rec := &recordingEC2{}
	tags := make([]types.Tag, 1, 8) // spare capacity: an append in place would write into it
	tags[0] = tag("Name", "box")
	specs := []types.TagSpecification{{ResourceType: types.ResourceTypeInstance, Tags: tags}}
	in := &ec2.RunInstancesInput{TagSpecifications: specs}
	if _, err := TagEC2Creations(rec, dld).RunInstances(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if len(in.TagSpecifications) != 1 || len(in.TagSpecifications[0].Tags) != 1 || len(tags[:cap(tags)]) != 8 {
		t.Fatalf("the caller's request changed: %+v", in.TagSpecifications)
	}
	if extra := tags[:2][1]; extra.Key != nil {
		t.Errorf("a tag was written into the caller's backing array: %+v", extra)
	}
}

func TestTagEC2Creations_ErrorsAndOtherCallsPassThrough(t *testing.T) {
	rec := &recordingEC2{err: errors.New("InvalidKeyPair.Duplicate")}
	c := TagEC2Creations(rec, dld)
	if _, err := c.CreateKeyPair(context.Background(), &ec2.CreateKeyPairInput{}); err == nil || err.Error() != "InvalidKeyPair.Duplicate" {
		t.Errorf("the inner error must come back as it is: %v", err)
	}
	if _, err := c.DescribeInstances(context.Background(), &ec2.DescribeInstancesInput{}); err != nil || rec.describe != 1 {
		t.Errorf("a call that is not a create must reach the inner client: %v %d", err, rec.describe)
	}
}

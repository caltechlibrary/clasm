package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// createEnv is a complete, valid world for the form: one AMI, key pair, security
// group, subnet, SSM-capable instance profile and a t3.micro offered in the
// subnet's zone.
type createEnv struct {
	ec2    *fakeEC2Client
	iam    *fakeIAMClient
	images []inventory.Image
	opts   CreateLaunchTemplateOptions
	yaml   string
}

func newCreateEnv(t *testing.T) *createEnv {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cloud-init.yaml")
	if err := os.WriteFile(path, []byte("#cloud-config\npackages: [git]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &createEnv{
		ec2: &fakeEC2Client{
			keyPairs:                     []ec2types.KeyPairInfo{{KeyName: aws.String("caltechauthors")}},
			securityGroups:               []ec2types.SecurityGroup{{GroupId: aws.String("sg-1")}, {GroupId: aws.String("sg-2")}},
			subnets:                      []ec2types.Subnet{{SubnetId: aws.String("subnet-1"), AvailabilityZone: aws.String("us-west-2a")}},
			instanceTypeOfferings:        map[string][]string{"t3.micro": {"us-west-2a"}, "t3.large": {"us-west-2b"}},
			instanceTypeArchitectures:    map[string]string{"t3.micro": "x86_64", "t3.large": "x86_64", "t4g.micro": "arm64"},
			enaRequiredInstanceTypes:     map[string]bool{"m7i.large": true},
			describeImagesRootDeviceName: "/dev/sda1",
			describeImagesBlockDeviceMappings: []ec2types.BlockDeviceMapping{
				{DeviceName: aws.String("/dev/sda1"), Ebs: &ec2types.EbsBlockDevice{VolumeSize: aws.Int32(8)}},
			},
			createLaunchTemplateID: "lt-new",
		},
		iam: &fakeIAMClient{
			instanceProfiles: []iamtypes.InstanceProfile{
				{InstanceProfileName: aws.String("rdm-backups"), Roles: []iamtypes.Role{{RoleName: aws.String("rdm-role")}}},
				{InstanceProfileName: aws.String("no-ssm"), Roles: []iamtypes.Role{{RoleName: aws.String("plain-role")}}},
			},
			attachedPolicyArns: map[string][]string{"rdm-role": {ssmManagedInstanceCorePolicyArn}},
		},
		images: []inventory.Image{{ImageID: "ami-1", Name: "base", Region: "us-west-2", Architecture: "x86_64", EnaSupport: true, Project: "caltechauthors"}},
		opts: CreateLaunchTemplateOptions{
			Name: "caltechauthors-v14", AMI: "ami-1", InstanceType: "t3.micro", KeyPair: "caltechauthors",
			SecurityGroups: []string{"sg-1", "sg-2"}, Subnet: "subnet-1", IAMInstanceProfile: "rdm-backups",
			NameTag: "caltechauthors-v14", Environment: "test", Format: ui.FormatText,
		},
		yaml: path,
	}
}

func (e *createEnv) run(w *bytes.Buffer) error {
	clients := map[string]awsclient.EC2API{"us-west-2": e.ec2}
	return RunCreateLaunchTemplateCLI(context.Background(), w, clients, e.iam, e.images, e.opts, e.yaml)
}

func tagsOf(specs []ec2types.TagSpecification, rt ec2types.ResourceType) map[string]string {
	got := map[string]string{}
	for _, s := range specs {
		if s.ResourceType == rt {
			for _, tg := range s.Tags {
				got[aws.ToString(tg.Key)] = aws.ToString(tg.Value)
			}
		}
	}
	return got
}

func TestRunCreateLaunchTemplateCLI_CreatesTheTemplateTheWizardWould(t *testing.T) {
	e := newCreateEnv(t)
	var out bytes.Buffer
	if err := e.run(&out); err != nil {
		t.Fatal(err)
	}
	in := e.ec2.lastCreateLaunchTemplateInput
	if in == nil {
		t.Fatal("CreateLaunchTemplate was never called")
	}
	d := in.LaunchTemplateData
	if aws.ToString(in.LaunchTemplateName) != "caltechauthors-v14" || aws.ToString(d.ImageId) != "ami-1" ||
		string(d.InstanceType) != "t3.micro" || aws.ToString(d.KeyName) != "caltechauthors" || aws.ToString(d.IamInstanceProfile.Name) != "rdm-backups" {
		t.Errorf("name/image/type/key/profile wrong: %+v / %+v", in, d)
	}
	ni := d.NetworkInterfaces[0]
	if aws.ToString(ni.SubnetId) != "subnet-1" || len(ni.Groups) != 2 {
		t.Errorf("network interface = %+v", ni)
	}
	if d.MetadataOptions == nil || d.MetadataOptions.HttpTokens != ec2types.LaunchTemplateHttpTokensStateRequired {
		t.Error("IMDSv2 must be required")
	}
	if ud, err := decodeUserData(aws.ToString(d.UserData)); err != nil || ud != "#cloud-config\npackages: [git]\n" {
		t.Errorf("user-data = %q err=%v", ud, err)
	}
	// Root volume defaults to the AMI's own size, as the wizard's prefill does.
	if len(d.BlockDeviceMappings) != 1 || aws.ToInt32(d.BlockDeviceMappings[0].Ebs.VolumeSize) != 8 || aws.ToString(d.BlockDeviceMappings[0].DeviceName) != "/dev/sda1" {
		t.Errorf("block devices = %+v", d.BlockDeviceMappings)
	}
	// Instance tags: the wizard's three (project defaulted from the AMI) plus the
	// template's name (issue #1). The resource's own tags are the wizard's three.
	inst := instanceTags(d)
	if inst["Name"] != "caltechauthors-v14" || inst["project"] != "caltechauthors" || inst["Environment"] != "test" || inst["templateName"] != "caltechauthors-v14" || len(inst) != 4 {
		t.Errorf("instance tags = %v", inst)
	}
	res := tagsOf(in.TagSpecifications, ec2types.ResourceTypeLaunchTemplate)
	if res["Name"] != "caltechauthors-v14" || res["project"] != "caltechauthors" || res["Environment"] != "test" || len(res) != 3 {
		t.Errorf("template resource tags = %v", res)
	}
	if !strings.Contains(out.String(), "Created launch template lt-new (caltechauthors-v14), version 1.") {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestRunCreateLaunchTemplateCLI_JSONAndResolvingTheAMIByName(t *testing.T) {
	e := newCreateEnv(t)
	e.opts.AMI, e.opts.Format = "base", ui.FormatJSON
	var out bytes.Buffer
	if err := e.run(&out); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if got["template_id"] != "lt-new" || got["name"] != "caltechauthors-v14" || got["version"] != float64(1) || got["region"] != "us-west-2" {
		t.Errorf("got %v", got)
	}
	if aws.ToString(e.ec2.lastCreateLaunchTemplateInput.LaunchTemplateData.ImageId) != "ami-1" {
		t.Error("the AMI name was not resolved to its ID")
	}
}

func TestRunCreateLaunchTemplateCLI_ExplicitProjectAndRootVolume(t *testing.T) {
	e := newCreateEnv(t)
	e.opts.Project, e.opts.RootVolumeGB = "authors", 250
	if err := e.run(&bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	d := e.ec2.lastCreateLaunchTemplateInput.LaunchTemplateData
	if instanceTags(d)["project"] != "authors" || aws.ToInt32(d.BlockDeviceMappings[0].Ebs.VolumeSize) != 250 {
		t.Errorf("tags=%v bdm=%+v", instanceTags(d), d.BlockDeviceMappings)
	}
}

// Every refusal is a usage error (exit 2) that names the problem and sends no
// CreateLaunchTemplate; and none of them creates a key pair or a profile.
func TestRunCreateLaunchTemplateCLI_Refusals(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*createEnv)
		want   string
	}{
		"unknown AMI": {func(e *createEnv) { e.opts.AMI = "ami-nope" }, "ami-nope"},
		"ambiguous AMI name": {func(e *createEnv) {
			e.images = append(e.images, inventory.Image{ImageID: "ami-2", Name: "base", Region: "us-west-2"})
			e.opts.AMI = "base"
		}, "ambiguous"},
		"arm instance on x86":   {func(e *createEnv) { e.opts.InstanceType = "t4g.micro" }, "arm64"},
		"ENA type, non-ENA AMI": {func(e *createEnv) { e.images[0].EnaSupport = false; e.opts.InstanceType = "m7i.large" }, "ENA"},
		"unknown key pair":      {func(e *createEnv) { e.opts.KeyPair = "no-such-key" }, "no-such-key"},
		"unknown sec group":     {func(e *createEnv) { e.opts.SecurityGroups = []string{"sg-1", "sg-nope"} }, "sg-nope"},
		"unknown subnet":        {func(e *createEnv) { e.opts.Subnet = "subnet-nope" }, "subnet-nope"},
		"type not in subnet AZ": {func(e *createEnv) { e.opts.InstanceType = "t3.large" }, "us-west-2b"},
		"unknown profile":       {func(e *createEnv) { e.opts.IAMInstanceProfile = "no-such-profile" }, "no-such-profile"},
		"profile not SSM":       {func(e *createEnv) { e.opts.IAMInstanceProfile = "no-ssm" }, "SSM"},
		"root smaller than AMI": {func(e *createEnv) { e.opts.RootVolumeGB = 4 }, "8"},
		"missing file":          {func(e *createEnv) { e.yaml = filepath.Join(filepath.Dir(e.yaml), "nope.yaml") }, "nope.yaml"},
		"empty file":            {func(e *createEnv) { _ = os.WriteFile(e.yaml, nil, 0o644) }, "empty"},
	} {
		e := newCreateEnv(t)
		tc.mutate(e)
		var out bytes.Buffer
		err := e.run(&out)
		var ue *UsageError
		if !errors.As(err, &ue) || CLIExitCode(err) != 2 || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want an exit-2 usage error mentioning %q, got: %v", name, tc.want, err)
		}
		if e.ec2.lastCreateLaunchTemplateInput != nil || out.Len() != 0 {
			t.Errorf("%s: a refused run must create nothing and print nothing on stdout", name)
		}
		if e.ec2.createKeyPairCalls != 0 || e.ec2.importKeyPairCalls != 0 || e.iam.createInstanceProfileCalls != 0 || e.iam.lastCreateRoleInput != nil {
			t.Errorf("%s: a refusal must never create a key pair, instance profile or role", name)
		}
	}
}

// A region the AMI lives in with no configured client is a failure of the
// environment (exit 1); so is any AWS error, including a duplicate name.
func TestRunCreateLaunchTemplateCLI_AWSFailuresAreExitOne(t *testing.T) {
	e := newCreateEnv(t)
	e.ec2.createLaunchTemplateErr = errors.New("InvalidLaunchTemplateName.AlreadyExistsException")
	err := e.run(&bytes.Buffer{})
	if err == nil || CLIExitCode(err) != 1 || !strings.Contains(err.Error(), "AlreadyExists") {
		t.Errorf("duplicate name: want exit 1 carrying the AWS error, got %v", err)
	}

	e = newCreateEnv(t)
	e.ec2.describeKeyPairsErr = errors.New("UnauthorizedOperation")
	if err := e.run(&bytes.Buffer{}); err == nil || CLIExitCode(err) != 1 {
		t.Errorf("a failed lookup is not the caller's mistake, want exit 1, got %v", err)
	}

	e = newCreateEnv(t)
	e.images[0].Region = "eu-west-1"
	if err := e.run(&bytes.Buffer{}); err == nil || CLIExitCode(err) != 1 {
		t.Errorf("no client for the AMI's region: want exit 1, got %v", err)
	}
}

package workflow

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// The non-interactive "Create launch template from cloud-init YAML" (DR-0177).
// The wizard asks about a dozen things; here each is a named option, and the
// cloud-init file is the one positional argument. Everything the wizard checks
// is checked here too, but where the wizard offers a fix (another instance type,
// another subnet) this form refuses with exit 2 and says why -- and it never
// creates a key pair, an instance profile or a role: a missing one is an error.
// It asks for no confirmation: a template costs nothing and can be deleted.

// CreateLaunchTemplateUsage is the form's one-line usage, repeated in every
// usage error.
const CreateLaunchTemplateUsage = "usage: clasm compute " + CreateLaunchTemplateFromCloudInitCLISlug +
	" [-text|-json] --name <template> --ami <ami-name-or-id> --instance-type <type> --key-pair <name>" +
	" --security-group <sg-id>[,<sg-id>...] --subnet <subnet-id> --iam-instance-profile <name>" +
	" --name-tag <name> --environment <production|development|test> [--project <tag>] [--root-volume-gb <n>] <cloud-init.yaml>"

// CreateLaunchTemplateOptions are the form's parsed options.
type CreateLaunchTemplateOptions struct {
	Name               string
	AMI                string // a name or an ID
	InstanceType       string
	KeyPair            string
	SecurityGroups     []string
	Subnet             string
	IAMInstanceProfile string
	RootVolumeGB       int32 // 0 means the AMI's own size
	NameTag            string
	Project            string // "" means the AMI's Project tag
	Environment        string
	Format             ui.Format
}

// ParseCreateLaunchTemplateArgs parses the form's options and its one positional
// argument, the cloud-init file. Every problem -- a missing required option, a
// bad environment, a stray word -- is a *UsageError (exit 2) raised before AWS
// is touched; --help is a *HelpRequested. Options come before the file.
func ParseCreateLaunchTemplateArgs(args []string) (CreateLaunchTemplateOptions, string, error) {
	const leaf = CreateLaunchTemplateFromCloudInitCLISlug
	fs := flag.NewFlagSet(leaf, flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the errors below carry the usage; nothing prints twice
	var o CreateLaunchTemplateOptions
	var sgs []string
	var rootGB int
	var asText, asJSON bool
	fs.StringVar(&o.Name, "name", "", "")
	fs.StringVar(&o.AMI, "ami", "", "")
	fs.StringVar(&o.InstanceType, "instance-type", "", "")
	fs.StringVar(&o.KeyPair, "key-pair", "", "")
	fs.Func("security-group", "", func(v string) error {
		for _, id := range strings.Split(v, ",") {
			if id = strings.TrimSpace(id); id != "" {
				sgs = append(sgs, id)
			}
		}
		return nil
	})
	fs.StringVar(&o.Subnet, "subnet", "", "")
	fs.StringVar(&o.IAMInstanceProfile, "iam-instance-profile", "", "")
	fs.IntVar(&rootGB, "root-volume-gb", 0, "")
	fs.StringVar(&o.NameTag, "name-tag", "", "")
	fs.StringVar(&o.Project, "project", "", "")
	fs.StringVar(&o.Environment, "environment", "", "")
	fs.BoolVar(&asText, "text", false, "")
	fs.BoolVar(&asJSON, "json", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return CreateLaunchTemplateOptions{}, "", &HelpRequested{Usage: CreateLaunchTemplateUsage + createLaunchTemplateHelp}
		}
		return CreateLaunchTemplateOptions{}, "", &UsageError{Msg: fmt.Sprintf("%s: %v\n%s", leaf, err, CreateLaunchTemplateUsage)}
	}
	usage := func(format string, a ...any) error {
		return &UsageError{Msg: fmt.Sprintf("%s: %s\n%s", leaf, fmt.Sprintf(format, a...), CreateLaunchTemplateUsage)}
	}

	for _, r := range []struct{ flag, value string }{
		{"--name", o.Name}, {"--ami", o.AMI}, {"--instance-type", o.InstanceType}, {"--key-pair", o.KeyPair},
		{"--subnet", o.Subnet}, {"--iam-instance-profile", o.IAMInstanceProfile}, {"--name-tag", o.NameTag}, {"--environment", o.Environment},
	} {
		if strings.TrimSpace(r.value) == "" {
			return CreateLaunchTemplateOptions{}, "", usage("%s is required", r.flag)
		}
	}
	if len(sgs) == 0 {
		return CreateLaunchTemplateOptions{}, "", usage("--security-group is required")
	}
	if err := validateEnvironment(o.Environment); err != nil {
		return CreateLaunchTemplateOptions{}, "", usage("--environment: %v", err)
	}
	if rootGB < 0 {
		return CreateLaunchTemplateOptions{}, "", usage("--root-volume-gb must not be negative")
	}
	if asText && asJSON {
		return CreateLaunchTemplateOptions{}, "", usage("give only one of -text, -json")
	}
	pos := fs.Args()
	if len(pos) != 1 {
		return CreateLaunchTemplateOptions{}, "", usage("want one cloud-init file, got %d arguments", len(pos))
	}
	o.SecurityGroups, o.RootVolumeGB, o.Format = sgs, int32(rootGB), ui.FormatText
	if asJSON {
		o.Format = ui.FormatJSON
	}
	return o, pos[0], nil
}

const createLaunchTemplateHelp = `

Creates a launch template, version 1, from a cloud-init YAML file, exactly as
the interactive "Create launch template from cloud-init YAML" does, with each
prompt an option. IMDSv2 is always required, and instances launched from the
template are tagged Name, project, Environment and templateName (its own name).

Required:
  --name <template>          the launch template's name
  --ami <name-or-id>         the base AMI, in this account or an official Ubuntu image
  --instance-type <type>     must match the AMI's architecture and be offered in the subnet's zone
  --key-pair <name>          must already exist
  --security-group <ids>     one or more, comma-separated or repeated
  --subnet <subnet-id>
  --iam-instance-profile <name>
                             must already exist and be SSM-capable
  --name-tag <name>          the instances' Name tag
  --environment <env>        production, development or test
Optional:
  --project <tag>            the instances' project tag (default: the AMI's)
  --root-volume-gb <n>       root volume size (default: the AMI's own; not smaller)
  -text, -json               output format (default -text)
  -h, --help                 show this help

It never creates a key pair, instance profile or role, and where the
interactive form would offer a fix it fails instead (exit 2). A template name
that already exists is an AWS error (exit 1).
`

type createdTemplateJSON struct {
	TemplateID string `json:"template_id"`
	Name       string `json:"name"`
	Version    int64  `json:"version"`
	Region     string `json:"region"`
}

// RunCreateLaunchTemplateCLI runs the form. All checking that can fail as the
// caller's mistake -- the file, the AMI, the instance type against the AMI and
// the subnet, the key pair, security groups, subnet and instance profile -- comes
// first and returns a *UsageError; only then is the template created. A lookup
// that itself fails is an AWS failure (exit 1), not a usage error.
func RunCreateLaunchTemplateCLI(ctx context.Context, w io.Writer, ec2Clients map[string]awsclient.EC2API, iamClient awsclient.IAMAPI, images []inventory.Image, opts CreateLaunchTemplateOptions, yamlPath string) error {
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return &UsageError{Msg: fmt.Sprintf("cannot read the cloud-init file %q: %v", yamlPath, err)}
	}
	if strings.TrimSpace(string(data)) == "" {
		return &UsageError{Msg: fmt.Sprintf("the cloud-init file %q is empty", yamlPath)}
	}

	image, err := resolveImageArg(opts.AMI, imagesWithOfficialUbuntu(ctx, ec2Clients, images))
	if err != nil {
		return err
	}
	client, err := resolveEC2(ec2Clients, image.Region)
	if err != nil {
		return err
	}

	// The instance type must suit the AMI and the subnet.
	arch, err := instanceTypeArchitecture(ctx, client, opts.InstanceType)
	if err != nil {
		return err
	}
	if arch != "" && image.Architecture != "" && arch != image.Architecture {
		return &UsageError{Msg: fmt.Sprintf("instance type %s is %s but AMI %s is %s", opts.InstanceType, arch, image.ImageID, image.Architecture)}
	}
	needsENA, err := instanceTypeRequiresENA(ctx, client, opts.InstanceType)
	if err != nil {
		return err
	}
	if needsENA && !image.EnaSupport {
		return &UsageError{Msg: fmt.Sprintf("instance type %s requires ENA (Enhanced Networking), which AMI %s does not support", opts.InstanceType, image.ImageID)}
	}

	rootDevice, rootDefault, _, err := describeImageRootVolume(ctx, client, image.ImageID)
	if err != nil {
		return err
	}
	rootGB := opts.RootVolumeGB
	switch {
	case rootGB == 0:
		rootGB = rootDefault
	case rootGB < rootDefault:
		return &UsageError{Msg: fmt.Sprintf("--root-volume-gb %d is smaller than the AMI's own root volume, %d GiB", rootGB, rootDefault)}
	}

	if err := requireKeyPair(ctx, client, opts.KeyPair, image.Region); err != nil {
		return err
	}
	groups, err := listSecurityGroups(ctx, client)
	if err != nil {
		return err
	}
	var missing []string
	for _, id := range opts.SecurityGroups {
		if !hasSecurityGroup(groups, id) {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return &UsageError{Msg: fmt.Sprintf("security group(s) not found in %s: %s", image.Region, strings.Join(missing, ", "))}
	}

	subnets, err := listSubnets(ctx, client)
	if err != nil {
		return err
	}
	subnet, found := findSubnet(subnets, opts.Subnet)
	if !found {
		return &UsageError{Msg: fmt.Sprintf("subnet %q not found in %s", opts.Subnet, image.Region)}
	}
	offered, err := instanceTypeOfferedInAZ(ctx, client, opts.InstanceType, subnet.AvailabilityZone)
	if err != nil {
		return err
	}
	if !offered {
		msg := fmt.Sprintf("instance type %s is not offered in %s (subnet %s)", opts.InstanceType, subnet.AvailabilityZone, subnet.SubnetID)
		if azs, azErr := instanceTypeOfferedAZs(ctx, client, opts.InstanceType); azErr == nil && len(azs) > 0 {
			msg += "; it is offered in: " + strings.Join(azs, ", ")
		}
		return &UsageError{Msg: msg}
	}

	profiles, err := listInstanceProfiles(ctx, iamClient)
	if err != nil {
		return err
	}
	var profile *InstanceProfileInfo
	for i := range profiles {
		if profiles[i].Name == opts.IAMInstanceProfile {
			profile = &profiles[i]
		}
	}
	if profile == nil {
		return &UsageError{Msg: fmt.Sprintf("instance profile %q not found (this form never creates one)", opts.IAMInstanceProfile)}
	}
	capable, err := instanceProfileIsSSMCapable(ctx, iamClient, *profile)
	if err != nil {
		return err
	}
	if !capable {
		return &UsageError{Msg: fmt.Sprintf("instance profile %q is not SSM-capable: none of its roles has %s attached, so clasm could not manage instances launched with it", opts.IAMInstanceProfile, ssmManagedInstanceCorePolicyArn)}
	}

	project := opts.Project
	if project == "" {
		project = image.Project
	}
	id, version, err := createLaunchTemplateFromParams(ctx, client, opts.Name, LaunchInstanceParams{
		ImageID:            image.ImageID,
		InstanceType:       opts.InstanceType,
		KeyName:            opts.KeyPair,
		SecurityGroupIDs:   opts.SecurityGroups,
		SubnetID:           subnet.SubnetID,
		IAMInstanceProfile: opts.IAMInstanceProfile,
		UserData:           string(data),
		RootDeviceName:     rootDevice,
		RootVolumeSizeGB:   rootGB,
		Tags:               map[string]string{"Name": opts.NameTag, "project": project, "Environment": opts.Environment},
	})
	if err != nil {
		return err
	}
	if opts.Format == ui.FormatJSON {
		return ui.WriteJSONValue(w, createdTemplateJSON{id, opts.Name, version, image.Region}, opts.Format)
	}
	fmt.Fprintf(w, "Created launch template %s (%s), version %d.\n", id, opts.Name, version)
	return nil
}

func hasSecurityGroup(groups []SecurityGroupInfo, id string) bool {
	for _, g := range groups {
		if g.GroupID == id {
			return true
		}
	}
	return false
}

func findSubnet(subnets []SubnetInfo, id string) (SubnetInfo, bool) {
	for _, s := range subnets {
		if s.SubnetID == id {
			return s, true
		}
	}
	return SubnetInfo{}, false
}

// requireKeyPair is a usage error unless a key pair of that name exists in the
// client's region. It only reads; the interactive flow's "new" is not offered.
func requireKeyPair(ctx context.Context, client awsclient.EC2API, name, region string) error {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{})
	if err != nil {
		return err
	}
	for _, kp := range out.KeyPairs {
		if aws.ToString(kp.KeyName) == name {
			return nil
		}
	}
	return &UsageError{Msg: fmt.Sprintf("key pair %q not found in %s (this form never creates one)", name, region)}
}

package workflow

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// The read-only Compute CLI forms (DR-0177). Each takes the argument as typed on
// the command line, resolves it against the listing refresh() loaded, and sends
// only Describe* calls -- except an AMI's cloud-init, which launches a
// temporary instance and so needs explicit consent. A name that matches nothing
// or more than one resource is a *UsageError (exit 2), never a guess; an AWS
// failure is a plain error (exit 1).

// matchByNameOrID returns the items whose Name equals arg exactly, or, when none
// does, those whose ID equals arg exactly.
func matchByNameOrID[T any](items []T, arg string, name, id func(T) string) []T {
	var byName, byID []T
	for _, it := range items {
		if name(it) == arg {
			byName = append(byName, it)
		}
		if id(it) == arg {
			byID = append(byID, it)
		}
	}
	if len(byName) > 0 {
		return byName
	}
	return byID
}

// resolveOne turns the matches for arg into exactly one resource or a usage error.
func resolveOne[T any](kind, arg string, items []T, name, id func(T) string) (T, error) {
	var zero T
	m := matchByNameOrID(items, arg, name, id)
	switch len(m) {
	case 1:
		return m[0], nil
	case 0:
		return zero, &UsageError{Msg: fmt.Sprintf("no %s found with name or ID %q", kind, arg)}
	}
	ids := make([]string, len(m))
	for i, it := range m {
		ids[i] = id(it)
	}
	return zero, &UsageError{Msg: fmt.Sprintf("%s name %q is ambiguous: matches %s", kind, arg, strings.Join(ids, ", "))}
}

func resolveImageArg(arg string, images []inventory.Image) (inventory.Image, error) {
	return resolveOne("AMI", arg, images, func(i inventory.Image) string { return i.Name }, func(i inventory.Image) string { return i.ImageID })
}

func resolveLaunchTemplateArg(arg string, templates []inventory.LaunchTemplate) (inventory.LaunchTemplate, error) {
	return resolveOne("launch template", arg, templates, func(l inventory.LaunchTemplate) string { return l.Name }, func(l inventory.LaunchTemplate) string { return l.TemplateID })
}

// resolveInstanceForCLI wraps resolveInstanceArg's failure as a usage error.
func resolveInstanceForCLI(arg string, instances []inventory.Instance) (inventory.Instance, error) {
	inst, err := resolveInstanceArg(arg, instances)
	if err != nil {
		return inventory.Instance{}, &UsageError{Msg: err.Error()}
	}
	return inst, nil
}

// The JSON shapes are fixed here, snake_case, apart from the inventory structs
// (see internal/ui/cli_output.go).
type instanceDetailJSON struct {
	InstanceID         string            `json:"instance_id"`
	Name               string            `json:"name"`
	State              string            `json:"state"`
	InstanceType       string            `json:"instance_type"`
	AMIID              string            `json:"ami_id"`
	Region             string            `json:"region"`
	VPCID              string            `json:"vpc_id"`
	SubnetID           string            `json:"subnet_id"`
	SecurityGroupIDs   []string          `json:"security_group_ids"`
	IAMInstanceProfile string            `json:"iam_instance_profile"`
	KeyName            string            `json:"key_name"`
	PublicIP           string            `json:"public_ip"`
	PrivateIP          string            `json:"private_ip"`
	Project            string            `json:"project"`
	Environment        string            `json:"environment"`
	EBSVolumes         []ebsVolumeJSON   `json:"ebs_volumes"`
	TotalEBSGiB        int32             `json:"total_ebs_gib"`
	Tags               map[string]string `json:"tags"`
}

type ebsVolumeJSON struct {
	VolumeID string `json:"volume_id"`
	SizeGiB  int32  `json:"size_gib"`
}

type imageDetailJSON struct {
	AMIID          string            `json:"ami_id"`
	Name           string            `json:"name"`
	Region         string            `json:"region"`
	CreationDate   string            `json:"creation_date"`
	Architecture   string            `json:"architecture"`
	EnaSupport     bool              `json:"ena_support"`
	RootDeviceName string            `json:"root_device_name"`
	Project        string            `json:"project"`
	Environment    string            `json:"environment"`
	BlockDevices   []blockDeviceJSON `json:"block_devices"`
	Tags           map[string]string `json:"tags"`
}

type blockDeviceJSON struct {
	DeviceName string `json:"device_name"`
	SizeGiB    int32  `json:"size_gib"`
	SnapshotID string `json:"snapshot_id"`
}

type launchTemplateDetailJSON struct {
	TemplateID         string   `json:"template_id"`
	Name               string   `json:"name"`
	Region             string   `json:"region"`
	DefaultVersion     int64    `json:"default_version"`
	LatestVersion      int64    `json:"latest_version"`
	Version            int64    `json:"version"`
	IsDefaultVersion   bool     `json:"is_default_version"`
	Created            string   `json:"created"`
	ImageID            string   `json:"image_id"`
	InstanceType       string   `json:"instance_type"`
	KeyName            string   `json:"key_name"`
	IAMInstanceProfile string   `json:"iam_instance_profile"`
	SecurityGroupIDs   []string `json:"security_group_ids"`
	SubnetID           string   `json:"subnet_id"`
	IMDSv2Required     bool     `json:"imdsv2_required"`
	RootVolumeSizeGiB  int32    `json:"root_volume_size_gib"`
	Project            string   `json:"project"`
	Environment        string   `json:"environment"`
}

type launchTemplateVersionJSON struct {
	Version int64  `json:"version"`
	Created string `json:"created"`
	Default bool   `json:"default"`
}

// nonNilStrings makes an empty list write [] rather than null.
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilTagMap(t map[string]string) map[string]string {
	if t == nil {
		return map[string]string{}
	}
	return t
}

// writeDetailText renders one of the interactive detail views to w without its
// leading blank line, so a script's first line is the heading.
func writeDetailText(w io.Writer, render func(io.Writer)) {
	var b bytes.Buffer
	render(&b)
	_, _ = io.WriteString(w, strings.TrimLeft(b.String(), "\n"))
}

// RunShowInstanceDetailCLI is `clasm compute show-instance-detail <instance>`.
func RunShowInstanceDetailCLI(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, instances []inventory.Instance, arg string, format ui.Format) error {
	inst, err := resolveInstanceForCLI(arg, instances)
	if err != nil {
		return err
	}
	client, err := resolveEC2(clients, inst.Region)
	if err != nil {
		return err
	}
	detail, err := inventory.DescribeInstanceDetail(ctx, client, inst.Region, inst.InstanceID)
	if err != nil {
		return err
	}
	volumes, total, _, err := GatherVolumeInfo(ctx, client, inst.InstanceID)
	if err != nil {
		return err
	}
	if format == ui.FormatText {
		writeDetailText(w, func(b io.Writer) { displayInstanceDetail(b, detail, volumes, total) })
		return nil
	}
	vols := make([]ebsVolumeJSON, len(volumes))
	for i, v := range volumes {
		vols[i] = ebsVolumeJSON{v.VolumeID, v.SizeGB}
	}
	return ui.WriteJSONValue(w, instanceDetailJSON{
		detail.InstanceID, detail.Name, detail.State, detail.InstanceType, detail.ImageID, detail.Region, detail.VPCID, detail.SubnetID,
		nonNilStrings(detail.SecurityGroupIDs), detail.IAMInstanceProfile, detail.KeyName, detail.PublicIP, detail.PrivateIP,
		detail.Project, detail.Environment, vols, total, nonNilTagMap(detail.Tags),
	}, format)
}

// RunShowAMIDetailCLI is `clasm compute show-ami-detail <ami>`.
func RunShowAMIDetailCLI(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, images []inventory.Image, arg string, format ui.Format) error {
	img, err := resolveImageArg(arg, images)
	if err != nil {
		return err
	}
	client, err := resolveEC2(clients, img.Region)
	if err != nil {
		return err
	}
	detail, err := inventory.DescribeImageDetail(ctx, client, img.Region, img.ImageID)
	if err != nil {
		return err
	}
	if format == ui.FormatText {
		writeDetailText(w, func(b io.Writer) { displayAMIDetail(b, detail) })
		return nil
	}
	bdms := make([]blockDeviceJSON, len(detail.BlockDeviceMappings))
	for i, b := range detail.BlockDeviceMappings {
		bdms[i] = blockDeviceJSON{b.DeviceName, b.VolumeSizeGB, b.SnapshotID}
	}
	return ui.WriteJSONValue(w, imageDetailJSON{
		detail.ImageID, detail.Name, detail.Region, detail.CreationDate, detail.Architecture, detail.EnaSupport,
		detail.RootDeviceName, detail.Project, detail.Environment, bdms, nonNilTagMap(detail.Tags),
	}, format)
}

// RunShowLaunchTemplateDetailCLI is `clasm compute show-launch-template-detail
// [-versions] <template> [version]`. version is "" for $Default. listVersions
// lists every version instead, and takes no version.
func RunShowLaunchTemplateDetailCLI(ctx context.Context, w io.Writer, clients map[string]awsclient.EC2API, templates []inventory.LaunchTemplate, arg, version string, listVersions bool, format ui.Format) error {
	if listVersions && version != "" {
		return &UsageError{Msg: "-versions lists every version; it takes no version argument"}
	}
	lt, err := resolveLaunchTemplateArg(arg, templates)
	if err != nil {
		return err
	}
	client, err := resolveEC2(clients, lt.Region)
	if err != nil {
		return err
	}
	if listVersions {
		versions, err := inventory.ListLaunchTemplateVersions(ctx, client, lt.TemplateID)
		if err != nil {
			return err
		}
		if format == ui.FormatText {
			for _, row := range launchTemplateVersionRows(versions) {
				fmt.Fprintln(w, row)
			}
			return nil
		}
		recs := make([]launchTemplateVersionJSON, len(versions))
		for i, v := range versions {
			recs[i] = launchTemplateVersionJSON{v.VersionNumber, v.CreateTime, v.IsDefaultVersion}
		}
		return ui.WriteJSONValue(w, recs, format)
	}

	if version == "" {
		version = defaultVersionSelector
	}
	detail, err := inventory.DescribeLaunchTemplateVersion(ctx, client, lt.TemplateID, normalizeVersionSelector(version))
	if err != nil {
		return err
	}
	if format == ui.FormatText {
		writeDetailText(w, func(b io.Writer) { displayLaunchTemplateVersion(b, lt, detail) })
		return nil
	}
	return ui.WriteJSONValue(w, launchTemplateDetailJSON{
		lt.TemplateID, lt.Name, lt.Region, lt.DefaultVersion, lt.LatestVersion, detail.VersionNumber, detail.IsDefaultVersion,
		detail.CreateTime, detail.ImageID, detail.InstanceType, detail.KeyName, detail.IAMInstanceProfile,
		nonNilStrings(detail.SecurityGroupIDs), detail.SubnetID, detail.IMDSv2Required, detail.RootVolumeSizeGB,
		detail.Project, detail.Environment,
	}, format)
}

type cloudInitJSON struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	UserDataSet bool   `json:"user_data_set"`
	UserData    string `json:"user_data"`
	SavedTo     string `json:"saved_to,omitempty"`
}

// RunExportCloudInitCLI is `clasm compute show-export-cloud-init-for-an-instance-or-ami
// <instance-or-ami> [file]`. Text mode writes the raw YAML to w (or to file,
// announcing it), so a pipe gets exactly the YAML; notices go to eout. An
// instance's user-data is one free read. An AMI's needs a temporary billable
// instance and is refused unless opts.LaunchTemporaryInstance is set.
func RunExportCloudInitCLI(ctx context.Context, w, eout io.Writer, ec2Clients map[string]awsclient.EC2API, ssmClients map[string]awsclient.SSMAPI, instances []inventory.Instance, images []inventory.Image, arg, file string, opts ComputeOptions) error {
	instMatches := matchByNameOrID(instances, arg, func(i inventory.Instance) string { return i.Name }, func(i inventory.Instance) string { return i.InstanceID })
	imgMatches := matchByNameOrID(images, arg, func(i inventory.Image) string { return i.Name }, func(i inventory.Image) string { return i.ImageID })
	switch n := len(instMatches) + len(imgMatches); {
	case n == 0:
		return &UsageError{Msg: fmt.Sprintf("no instance or AMI found with name or ID %q", arg)}
	case n > 1:
		var ids []string
		for _, i := range instMatches {
			ids = append(ids, i.InstanceID)
		}
		for _, i := range imgMatches {
			ids = append(ids, i.ImageID)
		}
		return &UsageError{Msg: fmt.Sprintf("%q is ambiguous: matches %s", arg, strings.Join(ids, ", "))}
	}

	var id, kind, data string
	var set bool
	if len(instMatches) == 1 {
		inst := instMatches[0]
		client, err := resolveEC2(ec2Clients, inst.Region)
		if err != nil {
			return err
		}
		if data, set, err = ShowCloudInitFromInstance(ctx, client, inst.InstanceID); err != nil {
			return err
		}
		id, kind = inst.InstanceID, "instance"
	} else {
		img := imgMatches[0]
		if !opts.LaunchTemporaryInstance {
			return &UsageError{Msg: fmt.Sprintf("reading the cloud-init of AMI %s launches a temporary billable instance; add -launch-temporary-instance to allow it", img.ImageID)}
		}
		ec2Client, ssmClient, err := resolveEC2AndSSM(ec2Clients, ssmClients, img.Region)
		if err != nil {
			return err
		}
		stop := startProgressTicker(eout, "extracting cloud-init from a temporary instance")
		data, err = ExtractCloudInitFromAMI(ctx, ec2Client, ssmClient, img.ImageID, opts.SecurityGroup, DefaultCloudInitExtractionTimeout, DefaultSSMPollInterval)
		stop()
		if err != nil {
			return err
		}
		// An AMI whose source had no user-data reads back empty: none found.
		id, kind, set = img.ImageID, "ami", data != ""
	}

	if !set && opts.Format == ui.FormatText {
		if kind == "ami" {
			fmt.Fprintf(eout, "No user-data was found in AMI %s.\n", id)
		} else {
			fmt.Fprintf(eout, "No user-data was set at launch for %s.\n", id)
		}
		return nil
	}
	rec := cloudInitJSON{ID: id, Kind: kind, UserDataSet: set, UserData: data}
	if file != "" && set {
		if err := os.WriteFile(file, []byte(data), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", file, err)
		}
		rec.SavedTo = file
	}
	if opts.Format != ui.FormatText {
		return ui.WriteJSONValue(w, rec, opts.Format)
	}
	if file != "" {
		fmt.Fprintf(w, "Saved to %s\n", file)
		return nil
	}
	_, err := io.WriteString(w, data)
	return err
}

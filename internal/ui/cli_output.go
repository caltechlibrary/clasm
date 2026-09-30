package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/caltechlibrary/clasm/internal/inventory"
)

// Format is how a read-only CLI form writes its result (DR-0177: experimental
// until 1.0, like every command term).
type Format string

const (
	// FormatText is the screen layout without the TUI: a header and one row per
	// resource, no colour. Columns are truncated exactly as on screen, so it is
	// for people; a program wants FormatJSON.
	FormatText Format = "text"
	// FormatJSON is one JSON document: an array of objects for a listing.
	FormatJSON Format = "json"
	// FormatJSONL is one compact JSON object per line, for streaming and
	// `jq -c`. An empty listing writes nothing.
	FormatJSONL Format = "jsonl"
)

// The JSON shapes are deliberately separate from the inventory structs: the keys
// are part of a scripting interface, so they are snake_case and fixed here
// rather than following a Go field rename. Values are complete (no truncation)
// and empty when unknown; the "unknown"/"none" placeholders are display-only.
type instanceJSON struct {
	InstanceID  string            `json:"instance_id"`
	Name        string            `json:"name"`
	State       string            `json:"state"`
	AMIID       string            `json:"ami_id"`
	Region      string            `json:"region"`
	Project     string            `json:"project"`
	Environment string            `json:"environment"`
	PublicIP    string            `json:"public_ip"`
	PrivateIP   string            `json:"private_ip"`
	KeyName     string            `json:"key_name"`
	Tags        map[string]string `json:"tags"`
}

type imageJSON struct {
	AMIID        string            `json:"ami_id"`
	Name         string            `json:"name"`
	CreationDate string            `json:"creation_date"`
	Region       string            `json:"region"`
	Project      string            `json:"project"`
	Environment  string            `json:"environment"`
	Architecture string            `json:"architecture"`
	EnaSupport   bool              `json:"ena_support"`
	Tags         map[string]string `json:"tags"`
}

type launchTemplateJSON struct {
	TemplateID     string            `json:"template_id"`
	Name           string            `json:"name"`
	DefaultVersion int64             `json:"default_version"`
	LatestVersion  int64             `json:"latest_version"`
	Region         string            `json:"region"`
	Project        string            `json:"project"`
	Environment    string            `json:"environment"`
	Tags           map[string]string `json:"tags"`
}

// nonNilTags makes an untagged resource write {} rather than null.
func nonNilTags(t map[string]string) map[string]string {
	if t == nil {
		return map[string]string{}
	}
	return t
}

// WriteInstances writes instances in format.
func WriteInstances(w io.Writer, instances []inventory.Instance, format Format) error {
	if format == FormatText {
		cfg := instanceListViewConfigColor(instances, false)
		return writeText(w, cfg.Header, cfg.Rows)
	}
	recs := make([]any, len(instances))
	for i, in := range instances {
		recs[i] = instanceJSON{in.InstanceID, in.Name, in.State, in.ImageID, in.Region, in.Project, in.Environment, in.PublicIP, in.PrivateIP, in.KeyName, nonNilTags(in.Tags)}
	}
	return writeRecords(w, recs, format)
}

// WriteImages writes images in format.
func WriteImages(w io.Writer, images []inventory.Image, format Format) error {
	if format == FormatText {
		cfg := imageListViewConfig(images)
		return writeText(w, cfg.Header, cfg.Rows)
	}
	recs := make([]any, len(images))
	for i, im := range images {
		recs[i] = imageJSON{im.ImageID, im.Name, im.CreationDate, im.Region, im.Project, im.Environment, im.Architecture, im.EnaSupport, nonNilTags(im.Tags)}
	}
	return writeRecords(w, recs, format)
}

// WriteLaunchTemplates writes templates in format.
func WriteLaunchTemplates(w io.Writer, templates []inventory.LaunchTemplate, format Format) error {
	if format == FormatText {
		cfg := launchTemplateListViewConfig(templates)
		return writeText(w, cfg.Header, cfg.Rows)
	}
	recs := make([]any, len(templates))
	for i, lt := range templates {
		recs[i] = launchTemplateJSON{lt.TemplateID, lt.Name, lt.DefaultVersion, lt.LatestVersion, lt.Region, lt.Project, lt.Environment, nonNilTags(lt.Tags)}
	}
	return writeRecords(w, recs, format)
}

// WriteJSONValue writes one value as a single JSON document (indented) or, for
// FormatJSONL, as one compact line. Detail forms use it.
func WriteJSONValue(w io.Writer, v any, format Format) error {
	switch format {
	case FormatJSON:
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%s\n", b)
		return err
	case FormatJSONL:
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%s\n", b)
		return err
	}
	return fmt.Errorf("unknown output format %q", format)
}

func writeText(w io.Writer, header string, rows []string) error {
	if _, err := fmt.Fprintln(w, header); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := fmt.Fprintln(w, r); err != nil {
			return err
		}
	}
	return nil
}

func writeRecords(w io.Writer, recs []any, format Format) error {
	switch format {
	case FormatJSON:
		return WriteJSONValue(w, recs, FormatJSON)
	case FormatJSONL:
		for _, r := range recs {
			if err := WriteJSONValue(w, r, FormatJSONL); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("unknown output format %q", format)
}

type keyPairJSON struct {
	KeyName     string            `json:"key_name"`
	KeyPairID   string            `json:"key_pair_id"`
	Fingerprint string            `json:"fingerprint"`
	KeyType     string            `json:"key_type"`
	Region      string            `json:"region"`
	Tags        map[string]string `json:"tags"`
}

type iamRoleJSON struct {
	Name       string            `json:"name"`
	CreateDate string            `json:"create_date"`
	Origin     string            `json:"origin"`
	DLDOwned   bool              `json:"dld_owned"`
	SSMCapable bool              `json:"ssm_capable"`
	Tags       map[string]string `json:"tags"`
}

type iamInstanceProfileJSON struct {
	Name       string            `json:"name"`
	CreateDate string            `json:"create_date"`
	Origin     string            `json:"origin"`
	DLDOwned   bool              `json:"dld_owned"`
	RoleNames  []string          `json:"role_names"`
	Tags       map[string]string `json:"tags"`
}

type iamPolicyJSON struct {
	Name       string            `json:"name"`
	ARN        string            `json:"arn"`
	CreateDate string            `json:"create_date"`
	Origin     string            `json:"origin"`
	DLDOwned   bool              `json:"dld_owned"`
	Tags       map[string]string `json:"tags"`
}

// FormatTimeJSON renders a time as RFC 3339 in UTC, whatever zone AWS returned.
func FormatTimeJSON(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// WriteKeyPairs writes key pairs in format.
func WriteKeyPairs(w io.Writer, keyPairs []inventory.KeyPair, format Format) error {
	if format == FormatText {
		cfg := keyPairListViewConfig(keyPairs)
		return writeText(w, cfg.Header, cfg.Rows)
	}
	recs := make([]any, len(keyPairs))
	for i, k := range keyPairs {
		recs[i] = keyPairJSON{k.KeyName, k.KeyPairID, k.KeyFingerprint, k.KeyType, k.Region, nonNilTags(k.Tags)}
	}
	return writeRecords(w, recs, format)
}

// WriteIAMRoles writes role rows in format.
func WriteIAMRoles(w io.Writer, rows []IAMRoleRow, format Format) error {
	if format == FormatText {
		cfg := iamRoleListViewConfig(rows)
		return writeText(w, cfg.Header, cfg.Rows)
	}
	recs := make([]any, len(rows))
	for i, r := range rows {
		recs[i] = iamRoleJSON{r.Name, FormatTimeJSON(r.CreateDate), r.Origin, r.DLDOwned, r.SSMCapable, nonNilTags(r.Tags)}
	}
	return writeRecords(w, recs, format)
}

// WriteIAMInstanceProfiles writes instance profile summaries in format.
func WriteIAMInstanceProfiles(w io.Writer, summaries []inventory.IAMInstanceProfileSummary, format Format) error {
	if format == FormatText {
		cfg := iamInstanceProfileListViewConfig(summaries)
		return writeText(w, cfg.Header, cfg.Rows)
	}
	recs := make([]any, len(summaries))
	for i, p := range summaries {
		roles := p.RoleNames
		if roles == nil {
			roles = []string{}
		}
		recs[i] = iamInstanceProfileJSON{p.Name, FormatTimeJSON(p.CreateDate), p.Origin, p.DLDOwned, roles, nonNilTags(p.Tags)}
	}
	return writeRecords(w, recs, format)
}

// WriteIAMPolicies writes customer-managed policy summaries in format.
func WriteIAMPolicies(w io.Writer, summaries []inventory.IAMPolicySummary, format Format) error {
	if format == FormatText {
		cfg := iamPolicyListViewConfig(summaries)
		return writeText(w, cfg.Header, cfg.Rows)
	}
	recs := make([]any, len(summaries))
	for i, p := range summaries {
		recs[i] = iamPolicyJSON{p.Name, p.ARN, FormatTimeJSON(p.CreateDate), p.Origin, p.DLDOwned, nonNilTags(p.Tags)}
	}
	return writeRecords(w, recs, format)
}

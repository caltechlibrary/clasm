package ui

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/caltechlibrary/clasm/internal/inventory"
)

func testInstances() []inventory.Instance {
	return []inventory.Instance{
		{InstanceID: "i-0aaa", Name: "a-name-that-is-longer-than-twenty-columns", State: "running", ImageID: "ami-1", Region: "us-west-2",
			Project: "authors", Environment: "prod", PublicIP: "1.2.3.4", PrivateIP: "10.0.0.1", KeyName: "k",
			Tags: map[string]string{"Name": "a-name-that-is-longer-than-twenty-columns", "Owner": "dld"}},
		{InstanceID: "i-0bbb", State: "stopped", Region: "us-east-1"},
	}
}

// The text form is the screen layout without the TUI: a header, one row each,
// and never an ANSI escape even when colour is on.
func TestWriteInstances_TextHasHeaderRowsAndNoEscapes(t *testing.T) {
	SetColorEnabled(true)
	defer SetColorEnabled(false)
	var b bytes.Buffer
	if err := WriteInstances(&b, testInstances(), FormatText); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "INSTANCE ID") || !strings.HasPrefix(lines[1], "i-0aaa") {
		t.Errorf("want a header and two rows, got:\n%s", b.String())
	}
	if strings.Contains(b.String(), "\x1b") {
		t.Errorf("text output must carry no ANSI escapes:\n%q", b.String())
	}
}

// -json carries complete values (the text layout truncates) and the full tags.
func TestWriteInstances_JSONIsOneArrayWithFullValues(t *testing.T) {
	var b bytes.Buffer
	if err := WriteInstances(&b, testInstances(), FormatJSON); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("not one JSON document: %v\n%s", err, b.String())
	}
	if len(got) != 2 {
		t.Fatalf("want 2 elements, got %d", len(got))
	}
	first := got[0]
	if first["name"] != "a-name-that-is-longer-than-twenty-columns" || first["instance_id"] != "i-0aaa" || first["public_ip"] != "1.2.3.4" {
		t.Errorf("wanted complete snake_case fields, got %v", first)
	}
	if tags, _ := first["tags"].(map[string]any); tags["Owner"] != "dld" {
		t.Errorf("wanted the full tag set, got %v", first["tags"])
	}
	// Missing data stays empty: the "unknown"/"none" placeholders are display-only.
	if got[1]["project"] != "" || got[1]["public_ip"] != "" {
		t.Errorf("JSON must not carry display placeholders, got %v", got[1])
	}
	if tags, ok := got[1]["tags"].(map[string]any); !ok || len(tags) != 0 {
		t.Errorf("an untagged instance wants {} not null, got %v", got[1]["tags"])
	}
}

func TestWriteInstances_JSONLIsOneCompactObjectPerLine(t *testing.T) {
	var b bytes.Buffer
	if err := WriteInstances(&b, testInstances(), FormatJSONL); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d:\n%s", len(lines), b.String())
	}
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Errorf("line is not a JSON object: %v\n%s", err, l)
		}
	}
}

// An empty listing is valid output in every format: [] and no lines, never null.
func TestWriteListings_EmptyIsWellFormed(t *testing.T) {
	var b bytes.Buffer
	if err := WriteInstances(&b, nil, FormatJSON); err != nil || strings.TrimSpace(b.String()) != "[]" {
		t.Errorf("empty -json want [], got %q err=%v", b.String(), err)
	}
	b.Reset()
	if err := WriteImages(&b, nil, FormatJSONL); err != nil || b.Len() != 0 {
		t.Errorf("empty -jsonl want no output, got %q err=%v", b.String(), err)
	}
	b.Reset()
	if err := WriteLaunchTemplates(&b, nil, FormatText); err != nil || !strings.HasPrefix(b.String(), "TEMPLATE ID") {
		t.Errorf("empty text still prints the header, got %q err=%v", b.String(), err)
	}
}

func TestWriteImagesAndTemplates_JSONFields(t *testing.T) {
	var b bytes.Buffer
	imgs := []inventory.Image{{ImageID: "ami-1", Name: "n", CreationDate: "2026-01-02T03:04:05Z", Region: "us-west-2", Architecture: "arm64", EnaSupport: true}}
	if err := WriteImages(&b, imgs, FormatJSON); err != nil {
		t.Fatal(err)
	}
	var gi []map[string]any
	if err := json.Unmarshal(b.Bytes(), &gi); err != nil || gi[0]["ami_id"] != "ami-1" || gi[0]["architecture"] != "arm64" || gi[0]["ena_support"] != true || gi[0]["creation_date"] != "2026-01-02T03:04:05Z" {
		t.Errorf("image JSON wrong: %v err=%v", gi, err)
	}
	b.Reset()
	lts := []inventory.LaunchTemplate{{TemplateID: "lt-1", Name: "t", DefaultVersion: 2, LatestVersion: 5, Region: "us-west-2"}}
	if err := WriteLaunchTemplates(&b, lts, FormatJSON); err != nil {
		t.Fatal(err)
	}
	var gl []map[string]any
	if err := json.Unmarshal(b.Bytes(), &gl); err != nil || gl[0]["template_id"] != "lt-1" || gl[0]["default_version"] != float64(2) || gl[0]["latest_version"] != float64(5) {
		t.Errorf("launch template JSON wrong: %v err=%v", gl, err)
	}
}

func TestWriteListings_UnknownFormatIsAnError(t *testing.T) {
	if err := WriteInstances(&bytes.Buffer{}, nil, Format("xml")); err == nil {
		t.Error("want an error for an unknown format")
	}
}

func TestWriteKeyPairs(t *testing.T) {
	kps := []inventory.KeyPair{{KeyName: "caltechauthors", KeyPairID: "key-0abc", KeyFingerprint: "aa:bb", KeyType: "rsa", Region: "us-west-2", Tags: map[string]string{"Owner": "dld"}}, {KeyName: "bare", Region: "us-east-1"}}
	var b bytes.Buffer
	if err := WriteKeyPairs(&b, kps, FormatText); err != nil || !strings.HasPrefix(b.String(), "KEY NAME") || !strings.Contains(b.String(), "caltechauthors") {
		t.Errorf("text: %q err=%v", b.String(), err)
	}
	b.Reset()
	if err := WriteKeyPairs(&b, kps, FormatJSON); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil || len(got) != 2 {
		t.Fatalf("%v\n%s", err, b.String())
	}
	if got[0]["key_name"] != "caltechauthors" || got[0]["key_pair_id"] != "key-0abc" || got[0]["fingerprint"] != "aa:bb" || got[0]["key_type"] != "rsa" {
		t.Errorf("got %v", got[0])
	}
	if tags, ok := got[1]["tags"].(map[string]any); !ok || len(tags) != 0 {
		t.Errorf("untagged wants {}, got %v", got[1]["tags"])
	}
}

func TestWriteIAMListings_JSONFieldsAndTimes(t *testing.T) {
	created := time.Date(2026, 7, 23, 10, 30, 0, 0, time.FixedZone("PDT", -7*3600))
	var b bytes.Buffer
	roles := []IAMRoleRow{{Name: "rdm-backups", CreateDate: created, Origin: "dld", DLDOwned: true, SSMCapable: true, Tags: map[string]string{"origin": "dld"}}}
	if err := WriteIAMRoles(&b, roles, FormatJSON); err != nil {
		t.Fatal(err)
	}
	var gr []map[string]any
	if err := json.Unmarshal(b.Bytes(), &gr); err != nil {
		t.Fatal(err)
	}
	// RFC 3339 in UTC, whatever zone AWS handed back.
	if gr[0]["name"] != "rdm-backups" || gr[0]["create_date"] != "2026-07-23T17:30:00Z" || gr[0]["dld_owned"] != true || gr[0]["ssm_capable"] != true || gr[0]["origin"] != "dld" {
		t.Errorf("role JSON: %v", gr[0])
	}
	b.Reset()
	profiles := []inventory.IAMInstanceProfileSummary{{Name: "p", CreateDate: created, Origin: "dld", DLDOwned: true, RoleNames: []string{"r1"}}}
	if err := WriteIAMInstanceProfiles(&b, profiles, FormatJSONL); err != nil {
		t.Fatal(err)
	}
	var gp map[string]any
	if err := json.Unmarshal(b.Bytes(), &gp); err != nil || gp["name"] != "p" || len(gp["role_names"].([]any)) != 1 {
		t.Errorf("profile JSONL: %v err=%v", gp, err)
	}
	b.Reset()
	policies := []inventory.IAMPolicySummary{{Name: "pol", ARN: "arn:aws:iam::1:policy/pol", CreateDate: created}}
	if err := WriteIAMPolicies(&b, policies, FormatJSON); err != nil {
		t.Fatal(err)
	}
	var gl []map[string]any
	if err := json.Unmarshal(b.Bytes(), &gl); err != nil || gl[0]["arn"] != "arn:aws:iam::1:policy/pol" {
		t.Errorf("policy JSON: %v err=%v", gl, err)
	}
}

func TestWriteIAMListings_TextAndEmpty(t *testing.T) {
	for name, write := range map[string]func(*bytes.Buffer, Format) error{
		"ROLE NAME":    func(b *bytes.Buffer, f Format) error { return WriteIAMRoles(b, nil, f) },
		"PROFILE NAME": func(b *bytes.Buffer, f Format) error { return WriteIAMInstanceProfiles(b, nil, f) },
		"POLICY NAME":  func(b *bytes.Buffer, f Format) error { return WriteIAMPolicies(b, nil, f) },
		"KEY NAME":     func(b *bytes.Buffer, f Format) error { return WriteKeyPairs(b, nil, f) },
	} {
		var b bytes.Buffer
		if err := write(&b, FormatText); err != nil || !strings.HasPrefix(b.String(), name) || strings.Contains(b.String(), "\x1b") {
			t.Errorf("%s text: %q err=%v", name, b.String(), err)
		}
		b.Reset()
		if err := write(&b, FormatJSON); err != nil || strings.TrimSpace(b.String()) != "[]" {
			t.Errorf("%s empty json: %q err=%v", name, b.String(), err)
		}
	}
}

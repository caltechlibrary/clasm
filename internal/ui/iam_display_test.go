package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/caltechlibrary/clasm/internal/inventory"
)

func TestIAMRoleListViewConfig_Empty(t *testing.T) {
	cfg := iamRoleListViewConfig(nil)
	if len(cfg.Rows) != 0 {
		t.Errorf("got %d rows, want 0 for an empty role list", len(cfg.Rows))
	}
	if !strings.Contains(cfg.Header, "ROLE NAME") || !strings.Contains(cfg.Header, "ORIGIN") || !strings.Contains(cfg.Header, "SSM") {
		t.Errorf("header = %q, want it to still show column titles even when empty", cfg.Header)
	}
}

func TestIAMRoleListViewConfig_Populated(t *testing.T) {
	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	rows := []IAMRoleRow{
		{Name: "ec2-granian-test-role", CreateDate: created, Origin: "DLD", DLDOwned: true, SSMCapable: true},
		{Name: "imss-crowdstrike-agent-role", CreateDate: created, Origin: inventory.OriginUnset, DLDOwned: false, SSMCapable: false},
	}

	cfg := iamRoleListViewConfig(rows)
	if len(cfg.Rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(cfg.Rows))
	}
	out := cfg.Header + "\n" + strings.Join(cfg.Rows, "\n")
	for _, want := range []string{"ec2-granian-test-role", "DLD", "imss-crowdstrike-agent-role", inventory.OriginUnset, "2026-06-01"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if cfg.Title == "" {
		t.Error("expected a non-empty Title")
	}
}

func TestIAMInstanceProfileListViewConfig_Populated(t *testing.T) {
	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	summaries := []inventory.IAMInstanceProfileSummary{
		{Name: "ec2-granian-test-profile", CreateDate: created, Origin: "DLD", DLDOwned: true},
	}

	cfg := iamInstanceProfileListViewConfig(summaries)
	if len(cfg.Rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(cfg.Rows))
	}
	out := cfg.Header + "\n" + cfg.Rows[0]
	for _, want := range []string{"ec2-granian-test-profile", "DLD", "2026-06-01"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestIAMPolicyListViewConfig_Populated(t *testing.T) {
	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	summaries := []inventory.IAMPolicySummary{
		{Name: "s3-backup-access", ARN: "arn:aws:iam::123456789012:policy/s3-backup-access", CreateDate: created, Origin: inventory.OriginUnset},
	}

	cfg := iamPolicyListViewConfig(summaries)
	if len(cfg.Rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(cfg.Rows))
	}
	out := cfg.Header + "\n" + cfg.Rows[0]
	for _, want := range []string{"s3-backup-access", inventory.OriginUnset, "2026-06-01"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/caltechlibrary/clasm/internal/inventory"
)

func TestIAMRoleRows_ResolvesSSMCapabilityPerRole(t *testing.T) {
	fake := &fakeIAMClient{
		attachedPolicyArns: map[string][]string{
			"capable-role": {"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"},
		},
	}
	summaries := []inventory.IAMRoleSummary{
		{Name: "capable-role", CreateDate: time.Now(), Origin: "DLD", DLDOwned: true},
		{Name: "not-capable-role", CreateDate: time.Now(), Origin: inventory.OriginUnset},
	}

	rows, err := iamRoleRows(context.Background(), fake, summaries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if !rows[0].SSMCapable {
		t.Error("capable-role should be reported SSM-capable")
	}
	if rows[1].SSMCapable {
		t.Error("not-capable-role should not be reported SSM-capable")
	}
	if rows[0].Origin != "DLD" || !rows[0].DLDOwned {
		t.Errorf("expected Origin/DLDOwned to carry through from the summary, got Origin=%q DLDOwned=%v", rows[0].Origin, rows[0].DLDOwned)
	}
}

func TestIAMRoleRows_PropagatesSSMCheckError(t *testing.T) {
	fake := &fakeIAMClient{listAttachedRolePoliciesErr: errors.New("boom")}
	summaries := []inventory.IAMRoleSummary{{Name: "some-role"}}

	_, err := iamRoleRows(context.Background(), fake, summaries)
	if err == nil {
		t.Fatal("expected the SSM-capability check's error to propagate")
	}
}

func TestIAMRoleRows_EmptyInputReturnsEmpty(t *testing.T) {
	rows, err := iamRoleRows(context.Background(), &fakeIAMClient{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}

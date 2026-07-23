package workflow

import (
	"context"
	"errors"
	"testing"
)

func TestRoleHasSSMPermissions_TrueWhenManagedPolicyAttached(t *testing.T) {
	fake := &fakeIAMClient{
		attachedPolicyArns: map[string][]string{
			"my-role": {"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"},
		},
	}
	ok, err := roleHasSSMPermissions(context.Background(), fake, "my-role")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected true, got false")
	}
}

func TestRoleHasSSMPermissions_FalseWhenManagedPolicyMissing(t *testing.T) {
	fake := &fakeIAMClient{
		attachedPolicyArns: map[string][]string{
			"my-role": {"arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess"},
		},
	}
	ok, err := roleHasSSMPermissions(context.Background(), fake, "my-role")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected false, got true")
	}
}

func TestRoleHasSSMPermissions_FalseWhenNoPoliciesAttached(t *testing.T) {
	fake := &fakeIAMClient{}
	ok, err := roleHasSSMPermissions(context.Background(), fake, "my-role")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected false, got true")
	}
}

func TestRoleHasSSMPermissions_PropagatesError(t *testing.T) {
	fake := &fakeIAMClient{listAttachedRolePoliciesErr: errors.New("boom")}
	if _, err := roleHasSSMPermissions(context.Background(), fake, "my-role"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestInstanceProfileIsSSMCapable_TrueWhenAnyRoleCapable(t *testing.T) {
	fake := &fakeIAMClient{
		attachedPolicyArns: map[string][]string{
			"role-a": {"arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess"},
			"role-b": {"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"},
		},
	}
	profile := InstanceProfileInfo{Name: "my-profile", Roles: []string{"role-a", "role-b"}}
	ok, err := instanceProfileIsSSMCapable(context.Background(), fake, profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected true, got false")
	}
}

func TestInstanceProfileIsSSMCapable_FalseWhenNoRoleCapable(t *testing.T) {
	fake := &fakeIAMClient{
		attachedPolicyArns: map[string][]string{
			"role-a": {"arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess"},
		},
	}
	profile := InstanceProfileInfo{Name: "my-profile", Roles: []string{"role-a"}}
	ok, err := instanceProfileIsSSMCapable(context.Background(), fake, profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected false, got true")
	}
}

func TestInstanceProfileIsSSMCapable_FalseWhenNoRolesAttached(t *testing.T) {
	fake := &fakeIAMClient{}
	profile := InstanceProfileInfo{Name: "my-profile"}
	ok, err := instanceProfileIsSSMCapable(context.Background(), fake, profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected false, got true")
	}
}

func TestInstanceProfileIsSSMCapable_PropagatesError(t *testing.T) {
	fake := &fakeIAMClient{listAttachedRolePoliciesErr: errors.New("boom")}
	profile := InstanceProfileInfo{Name: "my-profile", Roles: []string{"role-a"}}
	if _, err := instanceProfileIsSSMCapable(context.Background(), fake, profile); err == nil {
		t.Fatal("expected an error")
	}
}

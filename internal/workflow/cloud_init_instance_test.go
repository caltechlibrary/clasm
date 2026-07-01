package workflow

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
)

func TestShowCloudInitFromInstance_UserDataSet(t *testing.T) {
	raw := "#cloud-config\npackages: [docker]"
	fake := &fakeEC2Client{userDataValue: base64.StdEncoding.EncodeToString([]byte(raw))}

	got, set, err := ShowCloudInitFromInstance(context.Background(), fake, "i-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !set {
		t.Error("set = false, want true")
	}
	if got != raw {
		t.Errorf("got %q, want %q", got, raw)
	}
}

func TestShowCloudInitFromInstance_NoUserDataSet(t *testing.T) {
	fake := &fakeEC2Client{}

	got, set, err := ShowCloudInitFromInstance(context.Background(), fake, "i-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if set {
		t.Error("set = true, want false")
	}
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestShowCloudInitFromInstance_PropagatesError(t *testing.T) {
	fake := &fakeEC2Client{describeInstanceAttrErr: errors.New("boom")}
	_, _, err := ShowCloudInitFromInstance(context.Background(), fake, "i-1")
	if err == nil {
		t.Fatal("expected an error")
	}
}

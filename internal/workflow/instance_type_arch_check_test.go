package workflow

import (
	"context"
	"errors"
	"testing"
)

func TestInstanceTypeArchitecture_ReturnsFirstSupportedArchitecture(t *testing.T) {
	fake := &fakeEC2Client{instanceTypeArchitectures: map[string]string{"m7g.large": "arm64"}}
	got, err := instanceTypeArchitecture(context.Background(), fake, "m7g.large")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "arm64" {
		t.Errorf("got %q, want %q", got, "arm64")
	}
}

func TestInstanceTypeArchitecture_UnknownTypeReturnsEmptyNotError(t *testing.T) {
	fake := &fakeEC2Client{}
	got, err := instanceTypeArchitecture(context.Background(), fake, "some.unknown.type")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestInstanceTypeArchitecture_PropagatesError(t *testing.T) {
	fake := &fakeEC2Client{describeInstanceTypesErr: errors.New("boom")}
	if _, err := instanceTypeArchitecture(context.Background(), fake, "m7i-flex.2xlarge"); err == nil {
		t.Fatal("expected an error")
	}
}

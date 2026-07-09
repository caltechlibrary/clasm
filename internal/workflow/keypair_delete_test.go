package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

func TestDeleteKeyPair_Success(t *testing.T) {
	keyPairs := []inventory.KeyPair{{KeyName: "my-key", Region: "us-east-1", KeyType: "ed25519"}}
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor(t, "1\nmy-key\n")

	err := DeleteKeyPair(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, keyPairs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastDeleteKeyPairInput == nil {
		t.Fatal("DeleteKeyPair was never called")
	}
	if !strings.Contains(buf.String(), "DRY RUN") || !strings.Contains(buf.String(), "my-key") {
		t.Errorf("expected a dry-run display mentioning my-key, got:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "untouched") {
		t.Errorf("expected a reminder that the local .pem file is untouched, got:\n%s", buf.String())
	}
}

func TestDeleteKeyPair_DependencyWarningShownWhenInUse(t *testing.T) {
	keyPairs := []inventory.KeyPair{{KeyName: "my-key", Region: "us-east-1"}}
	instances := []inventory.Instance{
		{InstanceID: "i-1", Name: "web", KeyName: "my-key"},
		{InstanceID: "i-2", Name: "other", KeyName: "other-key"},
	}
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor(t, "1\nmy-key\n")

	err := DeleteKeyPair(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, keyPairs, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "i-1") {
		t.Errorf("expected the dependent instance i-1 in output, got:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "i-2") {
		t.Errorf("did not expect the unrelated instance i-2 in output, got:\n%s", buf.String())
	}
}

func TestDeleteKeyPair_NoDependencyWarningWhenUnused(t *testing.T) {
	keyPairs := []inventory.KeyPair{{KeyName: "my-key", Region: "us-east-1"}}
	instances := []inventory.Instance{{InstanceID: "i-1", Name: "web", KeyName: "other-key"}}
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor(t, "1\nmy-key\n")

	err := DeleteKeyPair(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, keyPairs, instances)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(buf.String(), "were launched with this key pair") {
		t.Errorf("did not expect a dependency warning when the key pair is unused, got:\n%s", buf.String())
	}
}

func TestDeleteKeyPair_TypeToConfirmMismatchCancels(t *testing.T) {
	keyPairs := []inventory.KeyPair{{KeyName: "my-key", Region: "us-east-1"}}
	fake := &fakeEC2Client{}
	term, le, _ := newPipeEditor(t, "1\nwrong\n")

	err := DeleteKeyPair(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, keyPairs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastDeleteKeyPairInput != nil {
		t.Error("DeleteKeyPair was called despite a type-to-confirm mismatch")
	}
}

func TestDeleteKeyPair_CancelledPickList(t *testing.T) {
	keyPairs := []inventory.KeyPair{{KeyName: "my-key", Region: "us-east-1"}}
	fake := &fakeEC2Client{}
	term, le, _ := newPipeEditor(t, "0\n")

	err := DeleteKeyPair(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, keyPairs, nil)
	if err != nil {
		t.Fatalf("expected a clean cancel (nil error), got: %v", err)
	}
	if fake.lastDeleteKeyPairInput != nil {
		t.Error("DeleteKeyPair was called despite cancelling the pick list")
	}
}

func TestDeleteKeyPair_NoKeyPairs(t *testing.T) {
	fake := &fakeEC2Client{}
	term, le, buf := newPipeEditor(t, "")

	err := DeleteKeyPair(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No key pairs") {
		t.Errorf("expected a no-key-pairs message, got:\n%s", buf.String())
	}
}

func TestDeleteKeyPair_PropagatesDeleteError(t *testing.T) {
	keyPairs := []inventory.KeyPair{{KeyName: "my-key", Region: "us-east-1"}}
	fake := &fakeEC2Client{deleteKeyPairErr: errors.New("boom")}
	term, le, _ := newPipeEditor(t, "1\nmy-key\n")

	err := DeleteKeyPair(context.Background(), term, le, map[string]awsclient.EC2API{"us-east-1": fake}, keyPairs, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
}

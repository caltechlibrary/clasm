package inventory

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

func sdkKeyPair(name, id, fingerprint string, keyType types.KeyType) types.KeyPairInfo {
	return types.KeyPairInfo{
		KeyName:        aws.String(name),
		KeyPairId:      aws.String(id),
		KeyFingerprint: aws.String(fingerprint),
		KeyType:        keyType,
	}
}

func sortKeyPairs(keyPairs []KeyPair) {
	sort.Slice(keyPairs, func(i, j int) bool { return keyPairs[i].KeyName < keyPairs[j].KeyName })
}

func TestListKeyPairs_AggregatesAcrossRegions(t *testing.T) {
	clients := map[string]awsclient.EC2API{
		"us-east-1": &fakeEC2Client{keyPairs: []types.KeyPairInfo{
			sdkKeyPair("my-laptop-key", "key-1", "aa:bb:cc", types.KeyTypeEd25519),
		}},
		"us-west-2": &fakeEC2Client{keyPairs: []types.KeyPairInfo{
			sdkKeyPair("team-shared-key", "key-2", "dd:ee:ff", types.KeyTypeRsa),
		}},
	}

	got, err := ListKeyPairs(context.Background(), clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sortKeyPairs(got)

	want := []KeyPair{
		{KeyName: "my-laptop-key", KeyPairID: "key-1", KeyFingerprint: "aa:bb:cc", KeyType: "ed25519", Region: "us-east-1"},
		{KeyName: "team-shared-key", KeyPairID: "key-2", KeyFingerprint: "dd:ee:ff", KeyType: "rsa", Region: "us-west-2"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d key pairs, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestListKeyPairs_EmptyRegion(t *testing.T) {
	clients := map[string]awsclient.EC2API{
		"us-east-1": &fakeEC2Client{keyPairs: nil},
	}
	got, err := ListKeyPairs(context.Background(), clients)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d key pairs, want 0", len(got))
	}
}

func TestListKeyPairs_PropagatesError(t *testing.T) {
	clients := map[string]awsclient.EC2API{
		"us-east-1": &fakeEC2Client{err: errors.New("boom")},
	}
	_, err := ListKeyPairs(context.Background(), clients)
	if err == nil {
		t.Fatal("expected an error")
	}
}

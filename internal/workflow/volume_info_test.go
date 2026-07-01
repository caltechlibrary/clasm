package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestGatherVolumeInfo_SumsSizesAcrossVolumes(t *testing.T) {
	fake := &fakeEC2Client{describeVolumesOutput: []types.Volume{
		{VolumeId: aws.String("vol-1"), Size: aws.Int32(20)},
		{VolumeId: aws.String("vol-2"), Size: aws.Int32(100)},
	}}

	volumes, totalGB, hasPriorSnapshot, err := GatherVolumeInfo(context.Background(), fake, "i-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if totalGB != 120 {
		t.Errorf("totalGB = %d, want 120", totalGB)
	}
	if len(volumes) != 2 {
		t.Fatalf("got %d volumes, want 2", len(volumes))
	}
	if hasPriorSnapshot {
		t.Error("hasPriorSnapshot = true, want false (no volume has a SnapshotId)")
	}
}

func TestGatherVolumeInfo_DetectsPriorSnapshot(t *testing.T) {
	fake := &fakeEC2Client{describeVolumesOutput: []types.Volume{
		{VolumeId: aws.String("vol-1"), Size: aws.Int32(20), SnapshotId: aws.String("snap-1")},
	}}

	volumes, _, hasPriorSnapshot, err := GatherVolumeInfo(context.Background(), fake, "i-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasPriorSnapshot {
		t.Error("hasPriorSnapshot = false, want true")
	}
	if !volumes[0].HasSnapshot {
		t.Error("volumes[0].HasSnapshot = false, want true")
	}
}

func TestGatherVolumeInfo_PropagatesError(t *testing.T) {
	fake := &fakeEC2Client{describeVolumesErr: errors.New("boom")}
	_, _, _, err := GatherVolumeInfo(context.Background(), fake, "i-1")
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestEstimateAMICreationTime(t *testing.T) {
	tests := []struct {
		gb   int32
		want string
	}{
		{gb: 10, want: "5-15 minutes"},
		{gb: 19, want: "5-15 minutes"},
		{gb: 20, want: "15-45 minutes"},
		{gb: 99, want: "15-45 minutes"},
		{gb: 100, want: "45-90 minutes"},
		{gb: 199, want: "45-90 minutes"},
		{gb: 200, want: "1.5-3+ hours"},
		{gb: 5000, want: "1.5-3+ hours"},
	}
	for _, tt := range tests {
		if got := EstimateAMICreationTime(tt.gb); got != tt.want {
			t.Errorf("EstimateAMICreationTime(%d) = %q, want %q", tt.gb, got, tt.want)
		}
	}
}

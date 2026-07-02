package awsclient

import (
	"context"
	"testing"
)

func TestNewClients(t *testing.T) {
	ctx := context.Background()

	// A representative sample, not the "real" configured list (which
	// now lives in internal/config) -- this test is only a sanity check
	// that client construction works for a region string, decoupled
	// from wherever the actual region list is decided.
	regions := []string{"us-west-1", "us-west-2"}

	for _, region := range regions {
		t.Run(region, func(t *testing.T) {
			ec2Client, err := NewEC2Client(ctx, region)
			if err != nil {
				t.Errorf("NewEC2Client(%q) returned error: %v", region, err)
			}
			if ec2Client == nil {
				t.Errorf("NewEC2Client(%q) returned a nil client", region)
			}

			ssmClient, err := NewSSMClient(ctx, region)
			if err != nil {
				t.Errorf("NewSSMClient(%q) returned error: %v", region, err)
			}
			if ssmClient == nil {
				t.Errorf("NewSSMClient(%q) returned a nil client", region)
			}

			s3Client, err := NewS3Client(ctx, region)
			if err != nil {
				t.Errorf("NewS3Client(%q) returned error: %v", region, err)
			}
			if s3Client == nil {
				t.Errorf("NewS3Client(%q) returned a nil client", region)
			}

			stsClient, err := NewSTSClient(ctx, region)
			if err != nil {
				t.Errorf("NewSTSClient(%q) returned error: %v", region, err)
			}
			if stsClient == nil {
				t.Errorf("NewSTSClient(%q) returned a nil client", region)
			}
		})
	}
}

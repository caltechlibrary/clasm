package awsclient

import (
	"context"
	"testing"
)

func TestNewClients(t *testing.T) {
	ctx := context.Background()

	for _, region := range Regions {
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

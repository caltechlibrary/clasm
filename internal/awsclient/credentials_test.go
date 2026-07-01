package awsclient

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
)

type fakeSTSClient struct {
	calls   int
	failN   int   // number of leading calls that fail with a throttling error
	fatal   error // if set, every call returns this instead of throttling
	account string
}

func (f *fakeSTSClient) GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	f.calls++
	if f.fatal != nil {
		return nil, f.fatal
	}
	if f.calls <= f.failN {
		return nil, &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}
	}
	return &sts.GetCallerIdentityOutput{Account: aws.String(f.account)}, nil
}

func TestCheckCredentials(t *testing.T) {
	t.Run("succeeds after retries", func(t *testing.T) {
		fake := &fakeSTSClient{failN: 2, account: "123456789012"}
		account, err := CheckCredentials(context.Background(), fake)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if account != "123456789012" {
			t.Errorf("account = %q, want %q", account, "123456789012")
		}
		if fake.calls != 3 {
			t.Errorf("calls = %d, want 3", fake.calls)
		}
	})

	t.Run("fails fast on a non-throttling error", func(t *testing.T) {
		fake := &fakeSTSClient{fatal: &smithy.GenericAPIError{Code: "AccessDenied", Message: "no"}}
		_, err := CheckCredentials(context.Background(), fake)
		if err == nil {
			t.Fatal("expected an error")
		}
		if fake.calls != 1 {
			t.Errorf("calls = %d, want 1 (should not retry non-throttling errors)", fake.calls)
		}
	})
}

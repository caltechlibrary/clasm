package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func TestCheckAWSCLIAvailable_Success(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "/usr/local/bin/aws\n"}

	if err := CheckAWSCLIAvailable(context.Background(), fake, "i-1", testPollInterval, testPollInterval); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckAWSCLIAvailable_MissingReturnsClearError(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed}

	err := CheckAWSCLIAvailable(context.Background(), fake, "i-1", testPollInterval, testPollInterval)
	if err == nil {
		t.Fatal("expected an error when the AWS CLI is missing")
	}
	if !strings.Contains(err.Error(), "i-1") || !strings.Contains(err.Error(), "AWS CLI") {
		t.Errorf("expected an actionable error naming the instance and the AWS CLI, got: %v", err)
	}
}

func TestCheckAWSCLIAvailable_PropagatesSSMError(t *testing.T) {
	fake := &fakeSSMClient{sendCommandErr: errors.New("SSM unavailable")}

	err := CheckAWSCLIAvailable(context.Background(), fake, "i-1", testPollInterval, testPollInterval)
	if err == nil {
		t.Fatal("expected the underlying SSM error to propagate")
	}
}

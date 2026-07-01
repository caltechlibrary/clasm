package awsclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/caltechlibrary/awstools/internal/debuglog"
)

type stubEC2Client struct {
	EC2API
	describeErr      error
	createKeyPairErr error
}

func (s *stubEC2Client) CreateKeyPair(ctx context.Context, params *ec2.CreateKeyPairInput, optFns ...func(*ec2.Options)) (*ec2.CreateKeyPairOutput, error) {
	if s.createKeyPairErr != nil {
		return nil, s.createKeyPairErr
	}
	return &ec2.CreateKeyPairOutput{
		KeyName:        params.KeyName,
		KeyPairId:      aws.String("key-0123456789"),
		KeyFingerprint: aws.String("aa:bb:cc"),
		KeyMaterial:    aws.String("-----BEGIN OPENSSH PRIVATE KEY-----\nSECRET\n-----END OPENSSH PRIVATE KEY-----"),
	}, nil
}

func (s *stubEC2Client) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	if s.describeErr != nil {
		return nil, s.describeErr
	}
	return &ec2.DescribeInstancesOutput{}, nil
}

type stubSSMClient struct {
	SSMAPI
}

func (s *stubSSMClient) SendCommand(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	return &ssm.SendCommandOutput{}, nil
}

type stubS3Client struct {
	S3API
}

func (s *stubS3Client) HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	return &s3.HeadObjectOutput{}, nil
}

type stubSTSClient struct {
	STSAPI
}

func (s *stubSTSClient) GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return &sts.GetCallerIdentityOutput{Account: aws.String("123456789012")}, nil
}

func readJSONLRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer f.Close()

	var records []map[string]any
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("line is not valid JSON: %v", err)
		}
		records = append(records, record)
	}
	return records
}

func TestWrapEC2_NilDebugLogReturnsUnwrappedClient(t *testing.T) {
	inner := &stubEC2Client{}
	got := WrapEC2(inner, nil, "us-east-1")
	if got != EC2API(inner) {
		t.Error("WrapEC2 with a nil DebugLog should return the original client unchanged")
	}
}

func TestWrapEC2_LogsMethodRegionAndParams(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.jsonl")
	dl, err := debuglog.New(path)
	if err != nil {
		t.Fatalf("debuglog.New: %v", err)
	}
	defer dl.Close()

	wrapped := WrapEC2(&stubEC2Client{}, dl, "us-west-2")
	if _, err := wrapped.DescribeInstances(context.Background(), &ec2.DescribeInstancesInput{InstanceIds: []string{"i-123"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dl.Close()

	records := readJSONLRecords(t, path)
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	r := records[0]
	if r["event"] != "aws_call" {
		t.Errorf("event = %v, want %q", r["event"], "aws_call")
	}
	if r["method"] != "EC2.DescribeInstances" {
		t.Errorf("method = %v, want %q", r["method"], "EC2.DescribeInstances")
	}
	if r["region"] != "us-west-2" {
		t.Errorf("region = %v, want %q", r["region"], "us-west-2")
	}
	if _, ok := r["error"]; ok {
		t.Errorf("record has an error field on a successful call: %v", r)
	}
}

func TestWrapEC2_LogsErrorInsteadOfOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.jsonl")
	dl, err := debuglog.New(path)
	if err != nil {
		t.Fatalf("debuglog.New: %v", err)
	}
	defer dl.Close()

	wrapped := WrapEC2(&stubEC2Client{describeErr: errors.New("boom")}, dl, "us-east-1")
	if _, err := wrapped.DescribeInstances(context.Background(), &ec2.DescribeInstancesInput{}); err == nil {
		t.Fatal("expected an error")
	}
	dl.Close()

	records := readJSONLRecords(t, path)
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if records[0]["error"] != "boom" {
		t.Errorf("error = %v, want %q", records[0]["error"], "boom")
	}
}

func TestWrapEC2_CreateKeyPairRedactsPrivateKeyMaterial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.jsonl")
	dl, err := debuglog.New(path)
	if err != nil {
		t.Fatalf("debuglog.New: %v", err)
	}
	defer dl.Close()

	wrapped := WrapEC2(&stubEC2Client{}, dl, "us-east-1")
	if _, err := wrapped.CreateKeyPair(context.Background(), &ec2.CreateKeyPairInput{KeyName: aws.String("my-new-key")}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dl.Close()

	records := readJSONLRecords(t, path)
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	raw, err := json.Marshal(records[0])
	if err != nil {
		t.Fatalf("re-marshaling record: %v", err)
	}
	if strings.Contains(string(raw), "SECRET") {
		t.Fatalf("debug log record contains the private key material: %s", raw)
	}
	output, ok := records[0]["output"].(map[string]any)
	if !ok {
		t.Fatalf("output is not a map: %v", records[0]["output"])
	}
	if output["KeyMaterial"] != "[REDACTED]" {
		t.Errorf("KeyMaterial = %v, want %q", output["KeyMaterial"], "[REDACTED]")
	}
	if output["KeyName"] != "my-new-key" {
		t.Errorf("KeyName = %v, want %q", output["KeyName"], "my-new-key")
	}
}

func TestWrapSSM_NilDebugLogReturnsUnwrappedClient(t *testing.T) {
	inner := &stubSSMClient{}
	got := WrapSSM(inner, nil, "us-east-1")
	if got != SSMAPI(inner) {
		t.Error("WrapSSM with a nil DebugLog should return the original client unchanged")
	}
}

func TestWrapSSM_Logs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.jsonl")
	dl, err := debuglog.New(path)
	if err != nil {
		t.Fatalf("debuglog.New: %v", err)
	}
	defer dl.Close()

	wrapped := WrapSSM(&stubSSMClient{}, dl, "us-east-1")
	if _, err := wrapped.SendCommand(context.Background(), &ssm.SendCommandInput{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dl.Close()

	records := readJSONLRecords(t, path)
	if len(records) != 1 || records[0]["method"] != "SSM.SendCommand" {
		t.Fatalf("got %v, want one record with method SSM.SendCommand", records)
	}
}

func TestWrapS3_NilDebugLogReturnsUnwrappedClient(t *testing.T) {
	inner := &stubS3Client{}
	got := WrapS3(inner, nil, "us-east-1")
	if got != S3API(inner) {
		t.Error("WrapS3 with a nil DebugLog should return the original client unchanged")
	}
}

func TestWrapS3_Logs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.jsonl")
	dl, err := debuglog.New(path)
	if err != nil {
		t.Fatalf("debuglog.New: %v", err)
	}
	defer dl.Close()

	wrapped := WrapS3(&stubS3Client{}, dl, "us-east-1")
	if _, err := wrapped.HeadObject(context.Background(), &s3.HeadObjectInput{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dl.Close()

	records := readJSONLRecords(t, path)
	if len(records) != 1 || records[0]["method"] != "S3.HeadObject" {
		t.Fatalf("got %v, want one record with method S3.HeadObject", records)
	}
}

func TestWrapSTS_NilDebugLogReturnsUnwrappedClient(t *testing.T) {
	inner := &stubSTSClient{}
	got := WrapSTS(inner, nil, "us-east-1")
	if got != STSAPI(inner) {
		t.Error("WrapSTS with a nil DebugLog should return the original client unchanged")
	}
}

func TestWrapSTS_Logs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.jsonl")
	dl, err := debuglog.New(path)
	if err != nil {
		t.Fatalf("debuglog.New: %v", err)
	}
	defer dl.Close()

	wrapped := WrapSTS(&stubSTSClient{}, dl, "us-east-1")
	if _, err := wrapped.GetCallerIdentity(context.Background(), &sts.GetCallerIdentityInput{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dl.Close()

	records := readJSONLRecords(t, path)
	if len(records) != 1 || records[0]["method"] != "STS.GetCallerIdentity" {
		t.Fatalf("got %v, want one record with method STS.GetCallerIdentity", records)
	}
}

// Package awsclient provides thin, typed wrappers over aws-sdk-go-v2 for
// per-region EC2, SSM, and S3 client construction.
package awsclient

// Regions lists the AWS regions awsops manages EC2 instances and AMIs in.
var Regions = []string{"us-east-1", "us-east-2", "us-west-1", "us-west-2"}

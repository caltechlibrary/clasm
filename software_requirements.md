# AWS Tools — Software Requirements

## Overview

This document lists all software dependencies required to develop and run the AWS Tools scripts.

---

## Core Requirements

### 1. Bash

- **Version:** 4.0 or higher recommended
- **Purpose:** Script execution environment
- **Check:** `bash --version`
- **Install:** Pre-installed on macOS and most Linux distributions

---

### 2. AWS CLI v2

- **Version:** 2.x (latest recommended)
- **Purpose:** AWS API interaction for EC2 and AMI operations
- **Check:** `aws --version`
- **Install:**
  - **macOS (MacPorts):** `sudo port install awscli`
  - **macOS (Homebrew):** `brew install awscli`
  - **Linux (apt):** `sudo apt install awscli`
  - **Linux (yum):** `sudo yum install awscli`
  - **Official:** https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html
- **Post-install:** Configure credentials with `aws configure` or via environment variables

---

### 3. jq

- **Version:** 1.6 or higher
- **Purpose:** JSON parsing for AWS CLI output
- **Check:** `jq --version`
- **Install:**
  - **macOS (MacPorts):** `sudo port install jq`
  - **macOS (Homebrew):** `brew install jq`
  - **Linux (apt):** `sudo apt install jq`
  - **Linux (yum):** `sudo yum install jq`
- **Website:** https://stedolan.github.io/jq/

---

## Development & Testing Requirements

### 4. BATS (Bash Automated Testing System)

- **Version:** 1.13.0 (or latest)
- **Purpose:** Unit testing framework for Bash scripts
- **Check:** `bats --version`
- **Install:**
  - **macOS (MacPorts):** `sudo port install bats-core`
  - **macOS (Homebrew):** `brew install bats-core`
  - **Linux:** See https://github.com/bats-core/bats-core
- **Website:** https://github.com/bats-core/bats-core

---

### 5. Git

- **Version:** 2.x
- **Purpose:** Version control
- **Check:** `git --version`
- **Install:**
  - **macOS:** `xcode-select --install` or via MacPorts/Homebrew
  - **Linux:** `sudo apt install git` or `sudo yum install git`

---

## Optional Dependencies

### 6. fzf (Optional)

- **Version:** Any recent version
- **Purpose:** Fuzzy finder for enhanced pick list UX (future enhancement)
- **Install:**
  - **macOS (MacPorts):** `sudo port install fzf`
  - **macOS (Homebrew):** `brew install fzf`
- **Website:** https://github.com/junegunn/fzf

---

## AWS Configuration

The scripts assume AWS credentials are configured. Set up one of:

### Option A: AWS CLI Configuration File
```bash
aws configure
# Enter Access Key ID, Secret Access Key, default region, output format
```
File location: `~/.aws/credentials` and `~/.aws/config`

### Option B: Environment Variables
```bash
export AWS_ACCESS_KEY_ID=AKIA...
export AWS_SECRET_ACCESS_KEY=...
export AWS_DEFAULT_REGION=us-east-1
```

### Option C: IAM Role (EC2, ECS, etc.)
Automatically picked up when running on AWS infrastructure with appropriate IAM role.

### Required IAM Permissions

Minimum permissions required for the scripts to function:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeInstances",
        "ec2:DescribeImages",
        "ec2:DescribeKeyPairs",
        "ec2:DescribeSecurityGroups",
        "ec2:DescribeSubnets",
        "ec2:DescribeVpcs",
        "ec2:DescribeIamInstanceProfileAssociations",
        "ec2:RunInstances",
        "ec2:CreateImage",
        "ec2:DeregisterImage",
        "ec2:CreateTags",
        "ec2:DescribeTags"
      ],
      "Resource": "*"
    }
  ]
}
```

---

## Installation Verification

Run the following commands to verify all dependencies are installed:

```bash
# Check all core requirements
echo "=== Bash ===" && bash --version
echo "=== AWS CLI ===" && aws --version
echo "=== jq ===" && jq --version
echo "=== BATS ===" && bats --version
echo "=== Git ===" && git --version
```

All commands should return version information without errors.

---

## macOS Quick Install (MacPorts)

For a fresh macOS setup with MacPorts:

```bash
# Install MacPorts if not already installed
# https://www.macports.org/install.php

# Install all core dependencies
sudo port install awscli jq bats-core git

# Verify
bats --version
aws --version
jq --version
```

---

## Troubleshooting

### BATS: Command not found
- Ensure `/opt/local/bin` is in your PATH (MacPorts default)
- Run: `export PATH="/opt/local/bin:$PATH"`
- Or add to your shell config (`~/.bashrc`, `~/.bash_profile`, or `~/.zshrc`)

### AWS CLI: Not configured
- Run `aws configure`
- Or set environment variables as shown above

### jq: Command not found
- Verify installation: `port installed jq` or `brew list jq`
- Reinstall if needed

### Permission errors
- Verify IAM user has the required permissions listed above
- Check AWS credentials are correct and not expired
- Verify the AWS region matches where your resources exist

---

## Version Compatibility Notes

| Dependency | Minimum Version | Tested Version | Notes |
|------------|-----------------|----------------|-------|
| Bash | 4.0 | 5.x | Some features may require 4.0+ (associative arrays) |
| AWS CLI | 2.0 | 2.15+ | v1 is deprecated and unsupported |
| jq | 1.6 | 1.7+ | Earlier versions may lack some features |
| BATS | 1.0 | 1.13.0 | All 1.x versions should work |
| Git | 2.0 | Any | Used for version control only |

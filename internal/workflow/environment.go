package workflow

import (
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// EnvironmentValues is the closed vocabulary of the Environment tag (DR-0193,
// amended by DR-0203): production, staged, development and test. Every prompt,
// validator and help text reads this list, so a value is added in one place.
var EnvironmentValues = []string{"production", "staged", "development", "test"}

// protectedEnvironments are the values a destructive guard treats as live data:
// production, and staged (a host built to replace production, not yet serving).
var protectedEnvironments = map[string]bool{"production": true, "staged": true}

// environmentList renders the vocabulary for a message, "a, b, c".
func environmentList() string { return strings.Join(EnvironmentValues, ", ") }

// isProtectedEnvironment reports whether an Environment tag value is protected:
// every guard that treats production as protected calls this, so production and
// staged cannot drift apart (DR-0203). The match is exact and case-sensitive.
//
// @param value {string} an Environment tag value, possibly empty
// @returns {bool} true for production and staged
// @example
//
//	if isProtectedEnvironment(inst.Environment) { /* warn and gate */ }
func isProtectedEnvironment(value string) bool { return protectedEnvironments[value] }

// isProtectedInstance reports whether the instance's live Environment tag is a
// protected value. An untagged instance is not protected.
//
// @param inst {types.Instance} an instance as the EC2 API returned it
// @returns {bool} true when its Environment tag is protected
// @example
//
//	gated := isProtectedInstance(live)
func isProtectedInstance(inst types.Instance) bool {
	for _, tag := range inst.Tags {
		if aws.ToString(tag.Key) == "Environment" {
			return isProtectedEnvironment(aws.ToString(tag.Value))
		}
	}
	return false
}

// liveEnvironment returns the instance's live Environment tag value, or "".
func liveEnvironment(inst types.Instance) string {
	for _, tag := range inst.Tags {
		if aws.ToString(tag.Key) == "Environment" {
			return aws.ToString(tag.Value)
		}
	}
	return ""
}

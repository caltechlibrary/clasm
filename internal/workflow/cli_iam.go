package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// The read-only IAM CLI forms (DR-0177). Unlike Compute's, the IAM listings are
// not preloaded by main: each fetches what it shows, exactly as the menu action
// does, and sends only List*/Get* calls.

// RunShowIAMRolesCLI is `clasm iam show-roles`.
func RunShowIAMRolesCLI(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, format ui.Format) error {
	summaries, err := inventory.ListIAMRoleSummaries(ctx, client, originTag)
	if err != nil {
		return err
	}
	rows, err := iamRoleRows(ctx, client, summaries)
	if err != nil {
		return err
	}
	return ui.WriteIAMRoles(w, rows, format)
}

// RunShowIAMInstanceProfilesCLI is `clasm iam show-instance-profiles`.
func RunShowIAMInstanceProfilesCLI(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, format ui.Format) error {
	summaries, err := inventory.ListIAMInstanceProfileSummaries(ctx, client, originTag)
	if err != nil {
		return err
	}
	return ui.WriteIAMInstanceProfiles(w, summaries, format)
}

// RunShowIAMPoliciesCLI is `clasm iam show-policies` (customer-managed only).
func RunShowIAMPoliciesCLI(ctx context.Context, w io.Writer, client awsclient.IAMAPI, originTag config.OriginTagConfig, format ui.Format) error {
	summaries, err := inventory.ListIAMPolicySummaries(ctx, client, originTag)
	if err != nil {
		return err
	}
	return ui.WriteIAMPolicies(w, summaries, format)
}

type iamPolicyRefJSON struct {
	Name string `json:"name"`
	ARN  string `json:"arn"`
}

type iamRoleDetailJSON struct {
	Name                 string             `json:"name"`
	CreateDate           string             `json:"create_date"`
	Tags                 map[string]string  `json:"tags"`
	TrustPolicy          any                `json:"trust_policy"`
	AttachedPolicies     []iamPolicyRefJSON `json:"attached_policies"`
	InlinePolicyNames    []string           `json:"inline_policy_names"`
	SSMCapable           bool               `json:"ssm_capable"`
	ReferencedByProfiles []string           `json:"referenced_by_profiles"`
}

type iamRoleRefJSON struct {
	Name       string `json:"name"`
	SSMCapable bool   `json:"ssm_capable"`
}

type iamInstanceProfileDetailJSON struct {
	Name       string            `json:"name"`
	CreateDate string            `json:"create_date"`
	Tags       map[string]string `json:"tags"`
	Roles      []iamRoleRefJSON  `json:"roles"`
}

// iamLookupError turns IAM's NoSuchEntity into a usage error naming what was
// asked for (the caller's mistake, exit 2); anything else stays an AWS failure.
func iamLookupError(kind, name string, err error) error {
	var nse *iamtypes.NoSuchEntityException
	if errors.As(err, &nse) {
		return &UsageError{Msg: fmt.Sprintf("no IAM %s found named %q", kind, name)}
	}
	return err
}

// RunShowIAMRoleDetailCLI is `clasm iam show-role-detail <role-name>`. IAM names
// are exact, so the argument is looked up directly. The policy-document
// drill-down stays interactive.
func RunShowIAMRoleDetailCLI(ctx context.Context, w io.Writer, client awsclient.IAMAPI, name string, format ui.Format) error {
	detail, err := fetchIAMRoleDetail(ctx, client, name)
	if err != nil {
		return iamLookupError("role", name, err)
	}
	if format == ui.FormatText {
		writeDetailText(w, func(b io.Writer) { displayIAMRoleDetail(b, detail) })
		return nil
	}
	attached := make([]iamPolicyRefJSON, len(detail.AttachedPolicies))
	for i, p := range detail.AttachedPolicies {
		attached[i] = iamPolicyRefJSON{p.Name, p.ARN}
	}
	// The trust policy is itself JSON: embed it as a document when it parses.
	var trust any = detail.TrustPolicy
	var parsed any
	if json.Unmarshal([]byte(detail.TrustPolicy), &parsed) == nil {
		trust = parsed
	}
	return ui.WriteJSONValue(w, iamRoleDetailJSON{
		detail.Name, ui.FormatTimeJSON(detail.CreateDate), nonNilTagMap(detail.Tags), trust, attached,
		nonNilStrings(detail.InlinePolicyNames), detail.SSMCapable, nonNilStrings(detail.ReferencedByProfiles),
	}, format)
}

// RunShowIAMInstanceProfileDetailCLI is `clasm iam show-instance-profile-detail
// <profile-name>`.
func RunShowIAMInstanceProfileDetailCLI(ctx context.Context, w io.Writer, client awsclient.IAMAPI, name string, format ui.Format) error {
	detail, err := fetchIAMInstanceProfileDetail(ctx, client, name)
	if err != nil {
		return iamLookupError("instance profile", name, err)
	}
	if format == ui.FormatText {
		writeDetailText(w, func(b io.Writer) { displayIAMInstanceProfileDetail(b, detail) })
		return nil
	}
	roles := make([]iamRoleRefJSON, len(detail.Roles))
	for i, r := range detail.Roles {
		roles[i] = iamRoleRefJSON{r.Name, r.SSMCapable}
	}
	return ui.WriteJSONValue(w, iamInstanceProfileDetailJSON{detail.Name, ui.FormatTimeJSON(detail.CreateDate), nonNilTagMap(detail.Tags), roles}, format)
}

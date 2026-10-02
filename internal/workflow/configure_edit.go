package workflow

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/ui"
)

// displayConfig prints the working copy's regions, backup directory
// rules, and Origin tag settings (DESIGN.md, "Configure clasm Domain",
// "Show current config").
func displayConfig(w io.Writer, cfg config.Config) {
	fmt.Fprintln(w, "\nCurrent configuration:")
	displayRegionsList(w, cfg.Regions)
	displayBackupDirectoryRulesList(w, cfg.BackupDirectories)
	displayRDMPostgresRulesList(w, cfg.RDMPostgresConfig)
	displayExtractionGroups(w, cfg)
	fmt.Fprintf(w, "Origin tag key:             %s\n", cfg.OriginTag.Key)
	fmt.Fprintf(w, "Origin tag DLD-owned value: %s\n", displayOrNone(cfg.OriginTag.DLDValue))
}

func displayRegionsList(w io.Writer, regions []string) {
	if len(regions) == 0 {
		fmt.Fprintln(w, "No regions configured.")
		return
	}
	fmt.Fprintf(w, "Regions: %s\n", strings.Join(regions, ", "))
}

// regionsEditDescription formats the current regions for the "Edit
// regions" Select's own Description, so they're rendered inside that
// Select's full-height chrome (DESIGN.md, "Full-height Menu Tier")
// instead of relying solely on displayRegionsList's separate plain print
// staying visible above it -- the instant that Select redraws, it fills
// the whole terminal and scrolls whatever was printed just before it out
// of view (the same bug class as manage_tags.go's actionMenuDescription
// fix, Phase 20.29's "Show tags appeared to do nothing"). displayRegionsList
// itself is kept too -- accessible mode never prints a field's
// Description, only its Title and options.
func regionsEditDescription(regions []string) string {
	if len(regions) == 0 {
		return "No regions configured yet. Region changes take effect the next time clasm is launched."
	}
	return fmt.Sprintf("Current regions: %s. Region changes take effect the next time clasm is launched.", strings.Join(regions, ", "))
}

func backupDirectoryRuleLabel(r config.BackupDirectoryRule) string {
	return fmt.Sprintf("%s -> %s", r.Pattern, r.Directory)
}

func displayBackupDirectoryRulesList(w io.Writer, rules []config.BackupDirectoryRule) {
	if len(rules) == 0 {
		fmt.Fprintln(w, "No backup directory rules configured.")
		return
	}
	fmt.Fprintln(w, "Backup directory rules (first match wins):")
	for _, r := range rules {
		fmt.Fprintf(w, "  %s\n", backupDirectoryRuleLabel(r))
	}
}

// backupDirectoryRulesEditDescription formats the current rules for the
// "Edit backup directory rules" Select's own Description -- same
// wipe-avoidance rationale as regionsEditDescription above.
// displayBackupDirectoryRulesList itself is kept too, for the same
// accessible-mode reason.
func backupDirectoryRulesEditDescription(rules []config.BackupDirectoryRule) string {
	if len(rules) == 0 {
		return "No backup directory rules configured yet."
	}
	var b strings.Builder
	b.WriteString("Current rules (first match wins):")
	for _, r := range rules {
		fmt.Fprintf(&b, "\n  %s", backupDirectoryRuleLabel(r))
	}
	return b.String()
}

// editRegionsChoices is the Edit regions sub-menu, in order. "Done"
// returns to the Configuration menu -- deliberately not 'q' (which would
// exit the whole domain) since this is a nested, bounded loop.
var editRegionsChoices = []string{"Add a region", "Remove a region", "Done"}

// editRegions lets the operator add/remove entries in cfg.Regions
// in place, looping until "Done" or a cancellation. Returns whether
// anything actually changed, so the caller can set its own dirty flag
// (DESIGN.md, "Configure clasm Domain").
func editRegions(w io.Writer, cfg *config.Config, input io.Reader, output io.Writer) (bool, error) {
	changed := false
	for {
		displayRegionsList(w, cfg.Regions)
		action, err := pickString(w, "Edit regions", regionsEditDescription(cfg.Regions), hintGoBack, editRegionsChoices, input, output)
		if err != nil {
			return changed, cancelledIsNil(w, err)
		}

		switch action {
		case "Add a region":
			region, err := ui.Prompt("New region (e.g. us-west-1)", ui.WithIO(input, output))
			if err != nil {
				return changed, cancelledIsNil(w, err)
			}
			region = strings.TrimSpace(region)
			if region == "" {
				fmt.Fprintln(w, "No region entered.")
				continue
			}
			cfg.Regions = append(cfg.Regions, region)
			changed = true
		case "Remove a region":
			if len(cfg.Regions) == 0 {
				fmt.Fprintln(w, "No regions to remove.")
				continue
			}
			region, err := pickString(w, "Remove which region?", "", hintGoBack, cfg.Regions, input, output)
			if err != nil {
				return changed, cancelledIsNil(w, err)
			}
			if idx := slices.Index(cfg.Regions, region); idx >= 0 {
				cfg.Regions = slices.Delete(cfg.Regions, idx, idx+1)
				changed = true
			}
		case "Done":
			return changed, nil
		}
	}
}

// editBackupDirChoices is the Edit backup directory rules sub-menu, same
// bounded-loop shape as editRegionsChoices.
var editBackupDirChoices = []string{"Add a rule", "Remove a rule", "Done"}

// editBackupDirectoryRules lets the operator add/remove entries in
// cfg.BackupDirectories in place, appending new rules to the end
// (first-match-wins order, per config.BackupDirectoryFor).
func editBackupDirectoryRules(w io.Writer, cfg *config.Config, input io.Reader, output io.Writer) (bool, error) {
	changed := false
	for {
		displayBackupDirectoryRulesList(w, cfg.BackupDirectories)
		action, err := pickString(w, "Edit backup directory rules", backupDirectoryRulesEditDescription(cfg.BackupDirectories), hintGoBack, editBackupDirChoices, input, output)
		if err != nil {
			return changed, cancelledIsNil(w, err)
		}

		switch action {
		case "Add a rule":
			pattern, err := ui.Prompt(`Glob pattern (matched against an instance's Name tag, e.g. "rdm-*")`, ui.WithIO(input, output))
			if err != nil {
				return changed, cancelledIsNil(w, err)
			}
			pattern = strings.TrimSpace(pattern)
			if pattern == "" {
				fmt.Fprintln(w, "No pattern entered.")
				continue
			}
			directory, err := ui.Prompt("Backup directory", ui.WithIO(input, output))
			if err != nil {
				return changed, cancelledIsNil(w, err)
			}
			directory = strings.TrimSpace(directory)
			if directory == "" {
				fmt.Fprintln(w, "No directory entered.")
				continue
			}
			cfg.BackupDirectories = append(cfg.BackupDirectories, config.BackupDirectoryRule{Pattern: pattern, Directory: directory})
			changed = true
		case "Remove a rule":
			if len(cfg.BackupDirectories) == 0 {
				fmt.Fprintln(w, "No rules to remove.")
				continue
			}
			labels := make([]string, len(cfg.BackupDirectories))
			for i, r := range cfg.BackupDirectories {
				labels[i] = backupDirectoryRuleLabel(r)
			}
			label, err := pickString(w, "Remove which rule?", "", hintGoBack, labels, input, output)
			if err != nil {
				return changed, cancelledIsNil(w, err)
			}
			if idx := slices.Index(labels, label); idx >= 0 {
				cfg.BackupDirectories = slices.Delete(cfg.BackupDirectories, idx, idx+1)
				changed = true
			}
		case "Done":
			return changed, nil
		}
	}
}

// rdmPostgresRuleLabel formats one RDMPostgresRule for display -- blank
// ContainerName/DBName/DBUser get an explanatory placeholder rather than
// an empty string, since blank means something specific for each field
// (DESIGN.md, "New Configuration: rdm_postgres_config"): ContainerName
// is never assumed, only discovered, so blank means "not yet discovered,
// or discover fresh next run"; DBName/DBUser blank means "fall back to
// the instance's own Name tag."
func rdmPostgresRuleLabel(r config.RDMPostgresRule) string {
	container := r.ContainerName
	if container == "" {
		container = "(discover automatically via docker ps)"
	}
	dbName := r.DBName
	if dbName == "" {
		dbName = "(instance Name)"
	}
	dbUser := r.DBUser
	if dbUser == "" {
		dbUser = "(instance Name)"
	}
	return fmt.Sprintf("%s -> %s (%s/%s)", r.Pattern, container, dbName, dbUser)
}

func displayRDMPostgresRulesList(w io.Writer, rules []config.RDMPostgresRule) {
	if len(rules) == 0 {
		fmt.Fprintln(w, "No RDM Postgres config rules configured.")
		return
	}
	fmt.Fprintln(w, "RDM Postgres config (first match wins):")
	for _, r := range rules {
		fmt.Fprintf(w, "  %s\n", rdmPostgresRuleLabel(r))
	}
}

// rdmPostgresRulesEditDescription formats the current rules for the
// "Edit RDM Postgres config" Select's own Description -- same
// wipe-avoidance rationale as backupDirectoryRulesEditDescription above.
func rdmPostgresRulesEditDescription(rules []config.RDMPostgresRule) string {
	if len(rules) == 0 {
		return "No RDM Postgres config rules configured yet."
	}
	var b strings.Builder
	b.WriteString("Current rules (first match wins):")
	for _, r := range rules {
		fmt.Fprintf(&b, "\n  %s", rdmPostgresRuleLabel(r))
	}
	return b.String()
}

// editRDMPostgresChoices is the Edit RDM Postgres config sub-menu, same
// bounded-loop shape as editBackupDirChoices.
var editRDMPostgresChoices = []string{"Add a rule", "Remove a rule", "Done"}

// editRDMPostgresRules lets the operator add/remove entries in
// cfg.RDMPostgresConfig in place, appending new rules to the end
// (first-match-wins order, per config.RDMPostgresConfigFor).
// ContainerName/DBName/DBUser are each independently optional -- only
// Pattern is required to add a rule.
func editRDMPostgresRules(w io.Writer, cfg *config.Config, input io.Reader, output io.Writer) (bool, error) {
	changed := false
	for {
		displayRDMPostgresRulesList(w, cfg.RDMPostgresConfig)
		action, err := pickString(w, "Edit RDM Postgres config", rdmPostgresRulesEditDescription(cfg.RDMPostgresConfig), hintGoBack, editRDMPostgresChoices, input, output)
		if err != nil {
			return changed, cancelledIsNil(w, err)
		}

		switch action {
		case "Add a rule":
			pattern, err := ui.Prompt(`Glob pattern (matched against an instance's Name tag, e.g. "caltechauthors")`, ui.WithIO(input, output))
			if err != nil {
				return changed, cancelledIsNil(w, err)
			}
			pattern = strings.TrimSpace(pattern)
			if pattern == "" {
				fmt.Fprintln(w, "No pattern entered.")
				continue
			}
			containerName, err := ui.Prompt("Container name (blank = discover automatically via docker ps)", ui.WithIO(input, output))
			if err != nil {
				return changed, cancelledIsNil(w, err)
			}
			dbName, err := ui.Prompt("Database name (blank = use the instance's own Name tag)", ui.WithIO(input, output))
			if err != nil {
				return changed, cancelledIsNil(w, err)
			}
			dbUser, err := ui.Prompt("Database user (blank = use the instance's own Name tag)", ui.WithIO(input, output))
			if err != nil {
				return changed, cancelledIsNil(w, err)
			}
			cfg.RDMPostgresConfig = append(cfg.RDMPostgresConfig, config.RDMPostgresRule{
				Pattern:       pattern,
				ContainerName: strings.TrimSpace(containerName),
				DBName:        strings.TrimSpace(dbName),
				DBUser:        strings.TrimSpace(dbUser),
			})
			changed = true
		case "Remove a rule":
			if len(cfg.RDMPostgresConfig) == 0 {
				fmt.Fprintln(w, "No rules to remove.")
				continue
			}
			labels := make([]string, len(cfg.RDMPostgresConfig))
			for i, r := range cfg.RDMPostgresConfig {
				labels[i] = rdmPostgresRuleLabel(r)
			}
			label, err := pickString(w, "Remove which rule?", "", hintGoBack, labels, input, output)
			if err != nil {
				return changed, cancelledIsNil(w, err)
			}
			if idx := slices.Index(labels, label); idx >= 0 {
				cfg.RDMPostgresConfig = slices.Delete(cfg.RDMPostgresConfig, idx, idx+1)
				changed = true
			}
		case "Done":
			return changed, nil
		}
	}
}

// editOriginTag prompts for cfg.OriginTag's Key and DLDValue, pre-filled
// with the working copy's current values. A blank key falls back to
// config.DefaultOriginTagKey, matching config.Load's own per-field
// default behavior.
func editOriginTag(w io.Writer, cfg *config.Config, input io.Reader, output io.Writer) (bool, error) {
	key, err := ui.Prompt("Origin tag key", ui.WithDefault(cfg.OriginTag.Key), ui.WithIO(input, output))
	if err != nil {
		return false, cancelledIsNil(w, err)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = config.DefaultOriginTagKey
	}

	dldValue, err := ui.Prompt(`Origin tag value meaning "DLD-owned" (blank = none recognized yet)`, ui.WithDefault(cfg.OriginTag.DLDValue), ui.WithIO(input, output))
	if err != nil {
		return false, cancelledIsNil(w, err)
	}

	changed := key != cfg.OriginTag.Key || dldValue != cfg.OriginTag.DLDValue
	cfg.OriginTag.Key = key
	cfg.OriginTag.DLDValue = dldValue
	return changed, nil
}

// warnIfDirtyOnQuit prints an unsaved-changes warning if dirty is true --
// separated from the pipe-driven quit path itself so it's directly
// testable without needing to simulate a real accessible-mode
// cancellation (huh's accessible mode never reliably surfaces an error
// on exhausted scripted input, the same gotcha that requires every
// looping accessible-mode workflow in this codebase to be tested via
// explicit ctx cancellation instead -- see runConfigureMenu's own tests).
func warnIfDirtyOnQuit(w io.Writer, dirty bool) {
	if dirty {
		fmt.Fprintln(w, "Unsaved changes will be discarded.")
	}
}

// displayExtractionGroups prints, for each configured region, the security group
// the disposable instance that reads an AMI's cloud-init is launched into.
func displayExtractionGroups(w io.Writer, cfg config.Config) {
	fmt.Fprintln(w, "Cloud-init extraction security groups:")
	for _, region := range cfg.Regions {
		fmt.Fprintf(w, "  %s\n", extractionGroupLine(cfg, region))
	}
}

func extractionGroupLine(cfg config.Config, region string) string {
	if id := cfg.ExtractionSecurityGroupFor(region); id != "" {
		return region + ": " + id
	}
	return region + ": (none -- the VPC default)"
}

// extractionGroupCandidate is one security group offered by the editor, with
// whether it would let the disposable instance's SSM agent reach AWS.
type extractionGroupCandidate struct {
	ID, Name, VPC string
	HTTPS         bool
}

func (c extractionGroupCandidate) label() string {
	https := "NO"
	if c.HTTPS {
		https = "yes"
	}
	return fmt.Sprintf("%s  %s  %s  outbound HTTPS: %s", c.ID, c.Name, c.VPC, https)
}

func listExtractionCandidates(ctx context.Context, client awsclient.EC2API) ([]extractionGroupCandidate, error) {
	ctx, cancel := withCallTimeout(ctx)
	defer cancel()
	out, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{})
	if err != nil {
		return nil, err
	}
	groups := make([]extractionGroupCandidate, 0, len(out.SecurityGroups))
	for _, g := range out.SecurityGroups {
		groups = append(groups, extractionGroupCandidate{
			ID: aws.ToString(g.GroupId), Name: aws.ToString(g.GroupName), VPC: aws.ToString(g.VpcId), HTTPS: allowsHTTPSEgress(g),
		})
	}
	return groups, nil
}

// editExtractionSecurityGroups edits cfg.CloudInitExtractionSecurityGroups one
// region at a time: lists that region's security groups, marks each as allowing
// outbound HTTPS or not, and saves the one picked -- refusing, through
// checkExtractionSecurityGroup, any group that would leave the disposable
// instance unable to reach SSM. Clearing a region returns it to the VPC
// default. Only configured regions with an AWS client are offered, since a
// group can only be listed and checked through that region's client.
func editExtractionSecurityGroups(ctx context.Context, w io.Writer, cfg *config.Config, ec2Clients map[string]awsclient.EC2API, input io.Reader, output io.Writer) (bool, error) {
	var regions []string
	for _, r := range cfg.Regions {
		if ec2Clients[r] != nil {
			regions = append(regions, r)
		}
	}
	if len(regions) == 0 {
		fmt.Fprintln(w, "No configured region has an AWS client, so there is nothing to list. Region changes take effect the next time clasm is launched.")
		return false, nil
	}

	const done = ""
	changed := false
	for {
		displayExtractionGroups(w, *cfg)
		choices := append(slices.Clone(regions), done)
		region, err := pickComparable(w, "Edit cloud-init extraction security groups",
			"The security group for the temporary instance that reads an AMI's cloud-init, per region. It must allow outbound HTTPS. A region with none uses the VPC default.",
			hintGoBack, choices, func(r string) string {
				if r == done {
					return "Done"
				}
				return extractionGroupLine(*cfg, r)
			}, input, output)
		if err != nil {
			return changed, cancelledIsNil(w, err)
		}
		if region == done {
			return changed, nil
		}

		client := ec2Clients[region]
		groups, err := listExtractionCandidates(ctx, client)
		if err != nil {
			fmt.Fprintf(w, "Could not list security groups in %s: %s\n", region, formatError(err))
			continue
		}

		// Indexes into groups, then two more entries: clear, back.
		clearIdx, backIdx := len(groups), len(groups)+1
		idxs := make([]int, backIdx+1)
		for i := range idxs {
			idxs[i] = i
		}
		pick, err := pickComparable(w, "Security group for "+region, "Groups marked NO cannot be used: the temporary instance could not reach SSM.",
			hintGoBack, idxs, func(i int) string {
				switch i {
				case clearIdx:
					return "Clear this region's setting (use the VPC default)"
				case backIdx:
					return "Back"
				}
				return groups[i].label()
			}, input, output)
		if err != nil {
			return changed, cancelledIsNil(w, err)
		}

		switch pick {
		case backIdx:
		case clearIdx:
			if _, ok := cfg.CloudInitExtractionSecurityGroups[region]; ok {
				delete(cfg.CloudInitExtractionSecurityGroups, region)
				changed = true
			}
		default:
			id := groups[pick].ID
			if err := checkExtractionSecurityGroup(ctx, client, id); err != nil {
				fmt.Fprintf(w, "Not saved: %s\n", formatError(err))
				continue
			}
			if cfg.CloudInitExtractionSecurityGroups == nil {
				cfg.CloudInitExtractionSecurityGroups = map[string]string{}
			}
			if cfg.CloudInitExtractionSecurityGroups[region] != id {
				cfg.CloudInitExtractionSecurityGroups[region] = id
				changed = true
			}
		}
	}
}

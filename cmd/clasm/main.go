// Command clasm is an interactive CLI for administering AWS EC2 instances,
// AMIs, and S3 backup archives for Caltech Library DLD's infrastructure.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path"
	"syscall"

	"github.com/caltechlibrary/clasm"
	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/debuglog"
	"github.com/caltechlibrary/clasm/internal/inventory"
	appstate "github.com/caltechlibrary/clasm/internal/state"
	"github.com/caltechlibrary/clasm/internal/ui"
	"github.com/caltechlibrary/clasm/internal/workflow"
)

var (
	// Standard Options
	showHelp    bool
	showLicense bool
	showVersion bool
	debugMode   bool
	configPath  string
)

func main() {
	appName := path.Base(os.Args[0])
	helpText := clasm.ClasmHelpText
	version, releaseDate, releaseHash, licenseText := clasm.Version, clasm.ReleaseDate, clasm.ReleaseHash, clasm.LicenseText
	fmtHelp := clasm.FmtHelp

	// Standard Options
	flag.BoolVar(&showHelp, "help", false, "display help")
	flag.BoolVar(&showLicense, "license", false, "display license")
	flag.BoolVar(&showVersion, "version", false, "display version")
	flag.BoolVar(&debugMode, "debug", false, "write a JSONL debug log of every AWS SDK call to ./clasm-debug-<timestamp>.jsonl")
	flag.StringVar(&configPath, "config", config.DefaultPath(), "path to clasm' own YAML config file (regions, etc.); AWS credentials are never read from here")

	flag.Parse()

	out := os.Stdout
	eout := os.Stderr

	if showHelp {
		fmt.Fprintf(out, "%s\n", fmtHelp(helpText, appName, version, releaseDate, releaseHash))
		os.Exit(0)
	}
	if showLicense {
		fmt.Fprintf(out, "%s\n", licenseText)
		os.Exit(0)
	}
	if showVersion {
		fmt.Fprintf(out, "%s %s %s\n", appName, version, releaseHash)
		os.Exit(0)
	}

	// Clear the terminal before clasm's first line of output, so old
	// scrollback never lingers behind the app (DECISIONS.md, "Clear the
	// screen at startup") -- but only for the actual interactive
	// session, not -help/-license/-version above, which should stay
	// script/pipe-friendly.
	ui.ClearScreen(out)

	// Ctrl+C (or SIGTERM) between prompts cancels ctx, which every
	// workflow's poll loop already selects on; Ctrl+C *during* an active
	// huh field is instead surfaced as huh.ErrUserAborted (see
	// internal/workflow/menu.go's isExitSignal).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(eout, "%v\n", err)
		os.Exit(1)
	}

	// appState is clasm's own runtime memory (~/.clasm_state, distinct
	// from cfg's hand-edited ~/.clasm) -- currently only Backup Archive
	// & Trim's recalled instance/directory (DECISIONS.md, "Recall Backup
	// Archive & Trim's instance/directory choices per-instance").
	statePath := appstate.DefaultPath()
	appState, err := appstate.Load(statePath)
	if err != nil {
		fmt.Fprintf(eout, "%v\n", err)
		os.Exit(1)
	}
	saveBackupHistory := func(instanceID, directory string) error {
		if appState.BackupArchive.LastDirectoryByInstance == nil {
			appState.BackupArchive.LastDirectoryByInstance = map[string]string{}
		}
		appState.BackupArchive.LastInstanceID = instanceID
		appState.BackupArchive.LastDirectoryByInstance[instanceID] = directory
		return appstate.Save(statePath, appState)
	}

	// -debug wraps every AWS client below in a logging decorator that
	// writes one JSONL record per SDK call (see DESIGN.md, "Debug
	// Logging"); a nil *debuglog.DebugLog makes every Wrap* call below a
	// no-op, so this is the only debug-mode branch in main().
	var dl *debuglog.DebugLog
	if debugMode {
		debugPath := debuglog.DefaultPath()
		var err error
		dl, err = debuglog.New(debugPath)
		if err != nil {
			fmt.Fprintf(eout, "opening debug log: %v\n", err)
			os.Exit(1)
		}
		defer dl.Close()
		fmt.Fprintf(eout, "Debug log: %s\n", debugPath)
	}

	ec2Clients := make(map[string]awsclient.EC2API, len(cfg.Regions))
	ssmClients := make(map[string]awsclient.SSMAPI, len(cfg.Regions))
	for _, region := range cfg.Regions {
		ec2Client, err := awsclient.NewEC2Client(ctx, region)
		if err != nil {
			fmt.Fprintf(eout, "creating EC2 client for %s: %v\n", region, err)
			os.Exit(1)
		}
		ec2Clients[region] = awsclient.WrapEC2(ec2Client, dl, region)

		ssmClient, err := awsclient.NewSSMClient(ctx, region)
		if err != nil {
			fmt.Fprintf(eout, "creating SSM client for %s: %v\n", region, err)
			os.Exit(1)
		}
		ssmClients[region] = awsclient.WrapSSM(ssmClient, dl, region)
	}

	// A bucket's home region is unrelated to any instance's region, and
	// unknown up front -- Backup Archive & Trim discovers it via
	// workflow.BucketRegion (called on this initial client, which works
	// from any region) and then builds a client actually scoped to that
	// region via newS3Client for every other S3 call (see DECISIONS.md,
	// "Resolve a bucket's actual region before Backup Archive & Trim's
	// access check").
	s3Client, err := awsclient.NewS3Client(ctx, cfg.Regions[0])
	if err != nil {
		fmt.Fprintf(eout, "creating S3 client: %v\n", err)
		os.Exit(1)
	}
	s3Client = awsclient.WrapS3(s3Client, dl, cfg.Regions[0])
	newS3Client := func(ctx context.Context, region string) (awsclient.S3API, error) {
		c, err := awsclient.NewS3Client(ctx, region)
		if err != nil {
			return nil, err
		}
		return awsclient.WrapS3(c, dl, region), nil
	}

	stsClient, err := awsclient.NewSTSClient(ctx, cfg.Regions[0])
	if err != nil {
		fmt.Fprintf(eout, "creating STS client: %v\n", err)
		os.Exit(1)
	}
	stsClient = awsclient.WrapSTS(stsClient, dl, cfg.Regions[0])

	// IAM is a global service, like STS -- one client suffices (Feature 2:
	// "IAM instance profile" pick-or-create).
	iamClient, err := awsclient.NewIAMClient(ctx, cfg.Regions[0])
	if err != nil {
		fmt.Fprintf(eout, "creating IAM client: %v\n", err)
		os.Exit(1)
	}
	iamClient = awsclient.WrapIAM(iamClient, dl, cfg.Regions[0])
	account, err := awsclient.CheckCredentials(ctx, stsClient)
	if err != nil {
		fmt.Fprintf(eout, "%v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(out, "clasm %s -- authenticated as AWS account %s\n", version, account)

	colorEnabled := ui.ColorEnabled()
	ui.SetColorEnabled(colorEnabled)

	var state struct {
		instances       []inventory.Instance
		images          []inventory.Image
		launchTemplates []inventory.LaunchTemplate
	}
	// refresh only re-fetches instance/AMI/launch-template data -- it no
	// longer displays it (DESIGN.md, "Terminal UI Architecture: Menus,
	// Actions, Lists, and Managers"). Displaying is showInstances/
	// showAMIs/showLaunchTemplatesList, each reachable only via the
	// Compute menu's own explicit "Show ..." choice (DECISIONS.md,
	// "Split Show resource lists into per-resource-type Compute menu
	// entries" -- same split as refreshS3/showS3ResourceLists below,
	// just three listings instead of one).
	refresh := func(ctx context.Context) error {
		instances, err := inventory.ListInstances(ctx, ec2Clients)
		if err != nil {
			return fmt.Errorf("listing instances: %w", err)
		}
		images, err := inventory.ListImages(ctx, ec2Clients)
		if err != nil {
			return fmt.Errorf("listing AMIs: %w", err)
		}
		launchTemplates, err := inventory.ListLaunchTemplates(ctx, ec2Clients)
		if err != nil {
			return fmt.Errorf("listing launch templates: %w", err)
		}
		state.instances, state.images, state.launchTemplates = instances, images, launchTemplates
		return nil
	}
	showInstances := func(ctx context.Context) error {
		return ui.DisplayInstances(ctx, state.instances)
	}
	showAMIs := func(ctx context.Context) error {
		return ui.DisplayImages(ctx, state.images)
	}
	showLaunchTemplatesList := func(ctx context.Context) error {
		return ui.DisplayLaunchTemplates(ctx, state.launchTemplates)
	}

	var s3State struct {
		buckets []inventory.Bucket
	}
	// refreshS3 only re-fetches bucket data -- it no longer displays it
	// (DESIGN.md, "S3 Resource List Display -- Paged, Accessible-
	// Compatible"). Displaying is showS3ResourceLists, reachable only
	// via the S3 menu's explicit "Show resource lists" choice.
	refreshS3 := func(ctx context.Context) error {
		buckets, err := inventory.ListBuckets(ctx, s3Client, newS3Client)
		if err != nil {
			return fmt.Errorf("listing buckets: %w", err)
		}
		s3State.buckets = buckets
		return nil
	}
	showS3ResourceLists := func(ctx context.Context) error {
		return ui.DisplayBuckets(ctx, s3State.buckets)
	}

	var keyMgmtState struct {
		keyPairs []inventory.KeyPair
	}
	// refreshKeyMgmt only re-fetches key pair (and instance) data -- it no
	// longer displays it. Displaying is showKeyMgmtResourceLists, reachable
	// only via the Key Management menu's explicit "Show resource lists"
	// choice (same split as refresh/showComputeResourceLists above).
	refreshKeyMgmt := func(ctx context.Context) error {
		keyPairs, err := inventory.ListKeyPairs(ctx, ec2Clients)
		if err != nil {
			return fmt.Errorf("listing key pairs: %w", err)
		}
		// Also independently fetch instances (not just key pairs): Delete
		// Key Pair's dependency check needs current instance data
		// regardless of whether the operator has visited Compute yet in
		// this run -- state.instances is only populated once Compute has
		// been entered at least once, and would otherwise silently
		// under-report dependents (see DECISIONS.md, "Key Management
		// independently refreshes instances for Delete Key Pair's
		// dependency check").
		instances, err := inventory.ListInstances(ctx, ec2Clients)
		if err != nil {
			return fmt.Errorf("listing instances: %w", err)
		}
		state.instances = instances
		keyMgmtState.keyPairs = keyPairs
		return nil
	}
	showKeyMgmtResourceLists := func(ctx context.Context) error {
		return ui.DisplayKeyPairs(ctx, keyMgmtState.keyPairs)
	}

	var tagMgmtState struct {
		instances       []inventory.Instance
		images          []inventory.Image
		launchTemplates []inventory.LaunchTemplate
		keyPairs        []inventory.KeyPair
		buckets         []inventory.Bucket
	}
	// refreshTagMgmt independently re-fetches all five resource types
	// the Tag Management domain covers -- not reused from state/
	// keyMgmtState/s3State, since an operator may reach this domain
	// before visiting Compute, Key Management, or S3 in this run (see
	// refreshKeyMgmt's own comment for the same reasoning).
	refreshTagMgmt := func(ctx context.Context) error {
		instances, err := inventory.ListInstances(ctx, ec2Clients)
		if err != nil {
			return fmt.Errorf("listing instances: %w", err)
		}
		images, err := inventory.ListImages(ctx, ec2Clients)
		if err != nil {
			return fmt.Errorf("listing AMIs: %w", err)
		}
		launchTemplates, err := inventory.ListLaunchTemplates(ctx, ec2Clients)
		if err != nil {
			return fmt.Errorf("listing launch templates: %w", err)
		}
		keyPairs, err := inventory.ListKeyPairs(ctx, ec2Clients)
		if err != nil {
			return fmt.Errorf("listing key pairs: %w", err)
		}
		buckets, err := inventory.ListBuckets(ctx, s3Client, newS3Client)
		if err != nil {
			return fmt.Errorf("listing buckets: %w", err)
		}
		tagMgmtState.instances, tagMgmtState.images, tagMgmtState.launchTemplates, tagMgmtState.keyPairs, tagMgmtState.buckets = instances, images, launchTemplates, keyPairs, buckets
		return nil
	}

	actions := workflow.MenuActions{
		CreateInstanceFromAMI: func(ctx context.Context) error {
			return workflow.CreateInstanceFromAMI(ctx, out, ec2Clients, ssmClients, iamClient, state.images)
		},
		CreateInstanceFromCloudInit: func(ctx context.Context) error {
			return workflow.CreateInstanceFromCloudInit(ctx, out, ec2Clients, ssmClients, iamClient, state.images)
		},
		StartEC2Instance: func(ctx context.Context) error {
			return workflow.StartEC2Instance(ctx, out, ec2Clients, state.instances)
		},
		StopEC2Instance: func(ctx context.Context) error {
			return workflow.StopEC2Instance(ctx, out, ec2Clients, state.instances)
		},
		TerminateEC2Instance: func(ctx context.Context) error {
			return workflow.TerminateEC2Instance(ctx, out, ec2Clients, state.instances)
		},
		ResizeInstanceRootVolume: func(ctx context.Context) error {
			return workflow.ResizeInstanceRootVolume(ctx, out, ec2Clients, ssmClients, state.instances)
		},
		AssociateOrReplaceInstanceProfile: func(ctx context.Context) error {
			return workflow.AssociateOrReplaceInstanceProfile(ctx, out, ec2Clients, iamClient, state.instances)
		},
		ManageTags: func(ctx context.Context) error {
			return workflow.ManageTags(ctx, out, ec2Clients, state.instances, state.images)
		},
		CreateAMIFromInstance: func(ctx context.Context) error {
			return workflow.CreateAMIFromInstance(ctx, out, ec2Clients, ssmClients, state.instances)
		},
		RemoveAMI: func(ctx context.Context) error {
			return workflow.RemoveAMI(ctx, out, ec2Clients, state.images, state.instances)
		},
		ShowCloudInit: func(ctx context.Context) error {
			return workflow.ShowCloudInit(ctx, out, ec2Clients, ssmClients, state.instances, state.images)
		},
		BackupArchiveAndTrim: func(ctx context.Context) error {
			return workflow.BackupArchiveAndTrim(ctx, out, ssmClients, s3Client, newS3Client, state.instances, cfg.BackupDirectories, workflow.BackupHistory{
				LastInstanceID:          appState.BackupArchive.LastInstanceID,
				LastDirectoryByInstance: appState.BackupArchive.LastDirectoryByInstance,
				Save:                    saveBackupHistory,
			})
		},
		ShowLaunchTemplate: func(ctx context.Context) error {
			return workflow.ShowLaunchTemplate(ctx, out, ec2Clients, state.launchTemplates)
		},
		CreateLaunchTemplateFromCloudInit: func(ctx context.Context) error {
			return workflow.CreateLaunchTemplateFromCloudInit(ctx, out, ec2Clients, ssmClients, iamClient, state.images)
		},
		CreateInstanceFromLaunchTemplate: func(ctx context.Context) error {
			return workflow.CreateInstanceFromLaunchTemplate(ctx, out, ec2Clients, state.launchTemplates)
		},
		SyncLaunchTemplate: func(ctx context.Context) error {
			return workflow.SyncLaunchTemplate(ctx, out, ec2Clients, state.launchTemplates)
		},
		PromoteLaunchTemplateVersion: func(ctx context.Context) error {
			return workflow.PromoteLaunchTemplateVersion(ctx, out, ec2Clients, state.launchTemplates)
		},
		DeleteLaunchTemplateVersions: func(ctx context.Context) error {
			return workflow.DeleteLaunchTemplateVersions(ctx, out, ec2Clients, state.launchTemplates)
		},
		DeleteLaunchTemplate: func(ctx context.Context) error {
			return workflow.DeleteLaunchTemplate(ctx, out, ec2Clients, state.launchTemplates)
		},
		Refresh:             refresh,
		ShowInstances:       showInstances,
		ShowAMIs:            showAMIs,
		ShowLaunchTemplates: showLaunchTemplatesList,
	}

	s3Actions := workflow.S3Actions{
		CreateBucket: func(ctx context.Context) error {
			return workflow.CreateBucket(ctx, out, newS3Client, cfg.Regions)
		},
		ConfigureWebsite: func(ctx context.Context) error {
			return workflow.ConfigureBucketWebsite(ctx, out, newS3Client, s3State.buckets)
		},
		BrowseAndManageObjects: func(ctx context.Context) error {
			return workflow.BrowseAndManageObjects(ctx, out, newS3Client, s3State.buckets)
		},
		ManageLifecyclePolicies: func(ctx context.Context) error {
			return workflow.ManageBucketLifecyclePolicies(ctx, out, newS3Client, s3State.buckets)
		},
		DeleteBucket: func(ctx context.Context) error {
			return workflow.DeleteBucket(ctx, out, newS3Client, s3State.buckets)
		},
		Refresh:           refreshS3,
		ShowResourceLists: showS3ResourceLists,
	}

	keyMgmtActions := workflow.KeyMgmtActions{
		CreateKeyPair: func(ctx context.Context) error {
			return workflow.CreateKeyPairStandalone(ctx, out, ec2Clients)
		},
		ImportKeyPair: func(ctx context.Context) error {
			return workflow.ImportKeyPairStandalone(ctx, out, ec2Clients)
		},
		DeleteKeyPair: func(ctx context.Context) error {
			return workflow.DeleteKeyPair(ctx, out, ec2Clients, keyMgmtState.keyPairs, state.instances)
		},
		Refresh:           refreshKeyMgmt,
		ShowResourceLists: showKeyMgmtResourceLists,
	}

	tagMgmtActions := workflow.TagMgmtActions{
		ManageTags: func(ctx context.Context) error {
			return workflow.ManageResourceTags(ctx, out, ec2Clients, newS3Client, iamClient, cfg.OriginTag, tagMgmtState.instances, tagMgmtState.images, tagMgmtState.launchTemplates, tagMgmtState.keyPairs, tagMgmtState.buckets)
		},
		ShowAllTags: func(ctx context.Context) error {
			return workflow.ShowAllTags(ctx, out, newS3Client, iamClient, cfg.OriginTag, tagMgmtState.instances, tagMgmtState.images, tagMgmtState.launchTemplates, tagMgmtState.keyPairs, tagMgmtState.buckets)
		},
		Refresh: refreshTagMgmt,
	}

	iamActions := workflow.IAMActions{
		ShowRoles: func(ctx context.Context) error {
			return workflow.ShowIAMRoles(ctx, iamClient, cfg.OriginTag)
		},
		ShowInstanceProfiles: func(ctx context.Context) error {
			return workflow.ShowIAMInstanceProfiles(ctx, iamClient, cfg.OriginTag)
		},
		ShowPolicies: func(ctx context.Context) error {
			return workflow.ShowIAMPolicies(ctx, iamClient, cfg.OriginTag)
		},
		ViewRoleDetail: func(ctx context.Context) error {
			return workflow.ViewIAMRoleDetail(ctx, out, iamClient, cfg.OriginTag)
		},
		ViewInstanceProfileDetail: func(ctx context.Context) error {
			return workflow.ViewIAMInstanceProfileDetail(ctx, out, iamClient, cfg.OriginTag)
		},
		CreateRoleFromTemplate: func(ctx context.Context) error {
			return workflow.CreateIAMRoleFromTemplate(ctx, out, iamClient, cfg.OriginTag)
		},
	}

	domains := workflow.DomainActions{
		Compute: func(ctx context.Context) error {
			// Fetch and display the Compute listing on every entry into
			// this domain (DESIGN.md, "Navigation: Domain Picker") --
			// not once at startup, since the operator may return here
			// after working in another domain.
			if err := refresh(ctx); err != nil {
				return err
			}
			return workflow.RunMainMenu(ctx, out, actions)
		},
		KeyManagement: func(ctx context.Context) error {
			if err := refreshKeyMgmt(ctx); err != nil {
				return err
			}
			return workflow.RunKeyMgmtMenu(ctx, out, keyMgmtActions)
		},
		S3: func(ctx context.Context) error {
			// Fetch (not display -- see refreshS3's own comment) the S3
			// listing on every entry into this domain, so it's current by
			// the time the operator might choose "Show resource lists."
			// Unlike Compute/Key Management, the S3 menu itself no longer
			// shows a resource list on entry (DESIGN.md, "S3 Resource List
			// Display -- Paged, Accessible-Compatible").
			if err := refreshS3(ctx); err != nil {
				return err
			}
			return workflow.RunS3Menu(ctx, out, s3Actions)
		},
		TagManagement: func(ctx context.Context) error {
			if err := refreshTagMgmt(ctx); err != nil {
				return err
			}
			return workflow.RunTagMgmtMenu(ctx, out, tagMgmtActions)
		},
		IAM: func(ctx context.Context) error {
			return workflow.RunIAMMenu(ctx, out, iamActions)
		},
	}

	if err := workflow.RunDomainPicker(ctx, out, domains); err != nil {
		fmt.Fprintf(eout, "%v\n", err)
		os.Exit(1)
	}
}

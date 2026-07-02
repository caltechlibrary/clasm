// Command awsops is an interactive CLI for administering AWS EC2 instances,
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

	"github.com/rsdoiel/termlib"

	"github.com/caltechlibrary/awstools"
	"github.com/caltechlibrary/awstools/internal/awsclient"
	"github.com/caltechlibrary/awstools/internal/config"
	"github.com/caltechlibrary/awstools/internal/debuglog"
	"github.com/caltechlibrary/awstools/internal/inventory"
	"github.com/caltechlibrary/awstools/internal/ui"
	"github.com/caltechlibrary/awstools/internal/workflow"
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
	helpText := awstools.AwsopsHelpText
	version, releaseDate, releaseHash, licenseText := awstools.Version, awstools.ReleaseDate, awstools.ReleaseHash, awstools.LicenseText
	fmtHelp := awstools.FmtHelp

	// Standard Options
	flag.BoolVar(&showHelp, "help", false, "display help")
	flag.BoolVar(&showLicense, "license", false, "display license")
	flag.BoolVar(&showVersion, "version", false, "display version")
	flag.BoolVar(&debugMode, "debug", false, "write a JSONL debug log of every AWS SDK call to ./awsops-debug-<timestamp>.jsonl")
	flag.StringVar(&configPath, "config", config.DefaultPath(), "path to awsops' own YAML config file (regions, etc.); AWS credentials are never read from here")

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

	// Ctrl+C (or SIGTERM) between prompts cancels ctx, which every
	// workflow's poll loop already selects on; Ctrl+C *during* an active
	// prompt is instead surfaced by termlib as ErrInterrupted (see
	// internal/workflow/menu.go's isExitSignal).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(eout, "%v\n", err)
		os.Exit(1)
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

	// S3 bucket regions are unrelated to any instance's region (Backup
	// Archive & Trim's independent verification targets whatever bucket
	// the operator specifies), so a single S3 client suffices.
	s3Client, err := awsclient.NewS3Client(ctx, cfg.Regions[0])
	if err != nil {
		fmt.Fprintf(eout, "creating S3 client: %v\n", err)
		os.Exit(1)
	}
	s3Client = awsclient.WrapS3(s3Client, dl, cfg.Regions[0])

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

	term := termlib.New(out)
	le := termlib.NewLineEditor(os.Stdin, out)

	term.Printf("awsops %s -- authenticated as AWS account %s\n", version, account)
	term.Refresh()

	colorEnabled := ui.ColorEnabled()
	ui.SetColorEnabled(colorEnabled)

	var state struct {
		instances []inventory.Instance
		images    []inventory.Image
	}
	refresh := func(ctx context.Context) error {
		instances, err := inventory.ListInstances(ctx, ec2Clients)
		if err != nil {
			return fmt.Errorf("listing instances: %w", err)
		}
		images, err := inventory.ListImages(ctx, ec2Clients)
		if err != nil {
			return fmt.Errorf("listing AMIs: %w", err)
		}
		state.instances, state.images = instances, images
		ui.DisplayInstances(term, state.instances, colorEnabled)
		ui.DisplayImages(term, state.images)
		return nil
	}

	actions := workflow.MenuActions{
		CreateInstanceFromAMI: func(ctx context.Context) error {
			return workflow.CreateInstanceFromAMI(ctx, term, le, ec2Clients, ssmClients, iamClient, state.images)
		},
		CreateInstanceFromCloudInit: func(ctx context.Context) error {
			return workflow.CreateInstanceFromCloudInit(ctx, term, le, ec2Clients, ssmClients, iamClient, state.images)
		},
		StartEC2Instance: func(ctx context.Context) error {
			return workflow.StartEC2Instance(ctx, term, le, ec2Clients, state.instances)
		},
		StopEC2Instance: func(ctx context.Context) error {
			return workflow.StopEC2Instance(ctx, term, le, ec2Clients, state.instances)
		},
		TerminateEC2Instance: func(ctx context.Context) error {
			return workflow.TerminateEC2Instance(ctx, term, le, ec2Clients, state.instances)
		},
		ManageTags: func(ctx context.Context) error {
			return workflow.ManageTags(ctx, term, le, ec2Clients, state.instances, state.images)
		},
		CreateAMIFromInstance: func(ctx context.Context) error {
			return workflow.CreateAMIFromInstance(ctx, term, le, ec2Clients, ssmClients, state.instances)
		},
		RemoveAMI: func(ctx context.Context) error {
			return workflow.RemoveAMI(ctx, term, le, ec2Clients, state.images, state.instances)
		},
		ShowCloudInit: func(ctx context.Context) error {
			return workflow.ShowCloudInit(ctx, term, le, ec2Clients, ssmClients, state.instances, state.images)
		},
		BackupArchiveAndTrim: func(ctx context.Context) error {
			return workflow.BackupArchiveAndTrim(ctx, term, le, ssmClients, s3Client, state.instances)
		},
		Refresh: refresh,
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
			return workflow.RunMainMenu(ctx, term, le, actions)
		},
		KeyManagement: func(ctx context.Context) error {
			return workflow.NotYetImplemented(term, "Key Management")
		},
		S3: func(ctx context.Context) error {
			return workflow.NotYetImplemented(term, "S3")
		},
		CloudFront: func(ctx context.Context) error {
			return workflow.NotYetImplemented(term, "CloudFront")
		},
	}

	if err := workflow.RunDomainPicker(ctx, term, le, domains); err != nil {
		fmt.Fprintf(eout, "%v\n", err)
		os.Exit(1)
	}
}

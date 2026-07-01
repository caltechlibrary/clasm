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
	"github.com/caltechlibrary/awstools/internal/inventory"
	"github.com/caltechlibrary/awstools/internal/ui"
	"github.com/caltechlibrary/awstools/internal/workflow"
)

var (
	// Standard Options
	showHelp    bool
	showLicense bool
	showVersion bool
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

	ec2Clients := make(map[string]awsclient.EC2API, len(awsclient.Regions))
	ssmClients := make(map[string]awsclient.SSMAPI, len(awsclient.Regions))
	for _, region := range awsclient.Regions {
		ec2Client, err := awsclient.NewEC2Client(ctx, region)
		if err != nil {
			fmt.Fprintf(eout, "creating EC2 client for %s: %v\n", region, err)
			os.Exit(1)
		}
		ec2Clients[region] = ec2Client

		ssmClient, err := awsclient.NewSSMClient(ctx, region)
		if err != nil {
			fmt.Fprintf(eout, "creating SSM client for %s: %v\n", region, err)
			os.Exit(1)
		}
		ssmClients[region] = ssmClient
	}

	// S3 bucket regions are unrelated to any instance's region (Backup
	// Archive & Trim's independent verification targets whatever bucket
	// the operator specifies), so a single S3 client suffices.
	s3Client, err := awsclient.NewS3Client(ctx, awsclient.Regions[0])
	if err != nil {
		fmt.Fprintf(eout, "creating S3 client: %v\n", err)
		os.Exit(1)
	}

	stsClient, err := awsclient.NewSTSClient(ctx, awsclient.Regions[0])
	if err != nil {
		fmt.Fprintf(eout, "creating STS client: %v\n", err)
		os.Exit(1)
	}
	account, err := awsclient.CheckCredentials(ctx, stsClient)
	if err != nil {
		fmt.Fprintf(eout, "%v\n", err)
		os.Exit(1)
	}

	term := termlib.New(out)
	le := termlib.NewLineEditor(os.Stdin, out)

	term.Printf("awsops %s -- authenticated as AWS account %s\n", version, account)
	term.Refresh()

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
		ui.DisplayInstances(term, state.instances)
		ui.DisplayImages(term, state.images)
		return nil
	}

	if err := refresh(ctx); err != nil {
		fmt.Fprintf(eout, "%v\n", err)
		os.Exit(1)
	}

	actions := workflow.MenuActions{
		CreateInstanceFromAMI: func(ctx context.Context) error {
			return workflow.CreateInstanceFromAMI(ctx, term, le, ec2Clients, ssmClients, state.images)
		},
		CreateInstanceFromCloudInit: func(ctx context.Context) error {
			return workflow.CreateInstanceFromCloudInit(ctx, term, le, ec2Clients, ssmClients, state.images)
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

	if err := workflow.RunMainMenu(ctx, term, le, actions); err != nil {
		fmt.Fprintf(eout, "%v\n", err)
		os.Exit(1)
	}
}

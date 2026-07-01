// Command awsops is an interactive CLI for administering AWS EC2 instances,
// AMIs, and S3 backup archives for Caltech Library DLD's infrastructure.
package main

import (
	"flag"
	"fmt"
	"os"
	"path"

	"github.com/caltechlibrary/awstools"
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

	// Application Logic: launch the interactive menu (Phase 14).
	fmt.Fprintf(eout, "%s %s -- interactive menu not yet implemented\n", appName, version)
	os.Exit(1)
}

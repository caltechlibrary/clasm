package workflow

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/inventory"
)

func sqlBackupInstances() []inventory.Instance {
	return []inventory.Instance{{InstanceID: "i-1", Name: "caltechauthors", Region: "us-east-1"}}
}

func TestParseSQLBackupArgs(t *testing.T) {
	inst, p, err := ParseSQLBackupArgs([]string{"caltechauthors", "/opt/rdm_sql_backups"}, sqlBackupInstances())
	if err != nil {
		t.Fatal(err)
	}
	if inst.InstanceID != "i-1" || p.InstanceID != "i-1" || p.Directory != "/opt/rdm_sql_backups" {
		t.Errorf("got inst=%+v params=%+v", inst, p)
	}
	// By ID as well as by Name tag, like every other form.
	if _, _, err := ParseSQLBackupArgs([]string{"i-1", "/d"}, sqlBackupInstances()); err != nil {
		t.Errorf("by ID: %v", err)
	}
}

func TestParseSQLBackupArgs_UsageErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"no arguments":   nil,
		"one argument":   {"caltechauthors"},
		"three":          {"caltechauthors", "/d", "extra"},
		"unknown box":    {"no-such-box", "/d"},
		"empty dir":      {"caltechauthors", ""},
		"unknown option": {"--confirm", "x", "caltechauthors", "/d"},
	} {
		_, _, err := ParseSQLBackupArgs(args, sqlBackupInstances())
		var ue *UsageError
		if !errors.As(err, &ue) || CLIExitCode(err) != 2 || !strings.Contains(err.Error(), "generate-sql-backup") {
			t.Errorf("%s: want an exit-2 usage error naming the leaf, got: %v", name, err)
		}
	}
	_, _, err := ParseSQLBackupArgs([]string{"--help"}, sqlBackupInstances())
	var help *HelpRequested
	if !errors.As(err, &help) || !strings.Contains(help.Usage, "<instance> <directory>") || CLIExitCode(err) != 0 {
		t.Errorf("--help: want a help request with the usage, got: %v", err)
	}
}

// The non-interactive form is the interactive one minus the prompt: the same six
// commands in the same order, no input consumed.
func TestRunSQLBackupAuto_SendsTheSameSixCommandsWithoutAPrompt(t *testing.T) {
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	var out bytes.Buffer
	inst := sqlBackupInstances()[0]
	err := RunSQLBackupAuto(context.Background(), &out, ssmClient, inst, SQLBackupParams{InstanceID: "i-1", Directory: "/opt/rdm_sql_backups"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sent := ssmClient.sentCommands
	if len(sent) != 6 {
		t.Fatalf("sent %d commands, want 6: %v", len(sent), sent)
	}
	docker := commandIndex(t, sent, "command -v docker")
	lookup := commandIndex(t, sent, sqlOwnerLookupFrg)
	ensure := commandIndex(t, sent, sqlEnsureExact)
	dump := commandIndex(t, sent, sqlDumpFrg)
	chown := commandIndex(t, sent, sqlChownExact)
	if !(docker == 0 && docker < lookup && lookup < ensure && ensure < dump && dump < chown) {
		t.Errorf("want docker check first, then lookup < ensure < dump < chown; sent: %v", sent)
	}
	if !strings.Contains(out.String(), "SQL backup created in /opt/rdm_sql_backups on i-1") {
		t.Errorf("the success line belongs in the log a cron job keeps, got:\n%s", out.String())
	}
}

func TestRunSQLBackupAuto_FailuresAreErrorsAndAbortEarly(t *testing.T) {
	inst := sqlBackupInstances()[0]
	p := SQLBackupParams{InstanceID: "i-1", Directory: "/d"}

	noDocker := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	setResponseStatus(noDocker, "command -v docker", types.CommandInvocationStatusFailed, "")
	if err := RunSQLBackupAuto(context.Background(), &bytes.Buffer{}, noDocker, inst, p, nil, nil); err == nil || noDocker.sendCommandCalls() != 1 {
		t.Errorf("no docker: err=%v calls=%d, want an error after one call", err, noDocker.sendCommandCalls())
	}

	noDB := sqlBackupFake("", types.CommandInvocationStatusSuccess)
	if err := RunSQLBackupAuto(context.Background(), &bytes.Buffer{}, noDB, inst, p, nil, nil); err == nil || noDB.sendCommandCalls() != 2 {
		t.Errorf("no container: err=%v calls=%d, want an error after docker check and discovery", err, noDB.sendCommandCalls())
	}

	failedDump := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusFailed)
	if err := RunSQLBackupAuto(context.Background(), &bytes.Buffer{}, failedDump, inst, p, nil, nil); err == nil || commandSent(failedDump.sentCommands, "chmod 0664") {
		t.Errorf("failed dump: err=%v, sent %v; want an error and no hand-over", err, failedDump.sentCommands)
	}
}

func TestRunSQLBackupAuto_SavesDiscoveredRulesOnlyWhenChanged(t *testing.T) {
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	var saved [][]config.RDMPostgresRule
	save := func(r []config.RDMPostgresRule) error { saved = append(saved, r); return nil }
	err := RunSQLBackupAuto(context.Background(), &bytes.Buffer{}, ssmClient, sqlBackupInstances()[0], SQLBackupParams{InstanceID: "i-1", Directory: "/d"}, nil, save)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 {
		t.Errorf("a first discovery is saved once, saved %d times", len(saved))
	}
}

// The interactive form reports what it used, so main can print the pastable
// command -- including when it was the directory prompt that supplied it.
func TestRunSQLBackup_ReportsTheInstanceAndDirectoryItUsed(t *testing.T) {
	ssmClient := sqlBackupFake("postgres:14.13\tcaltechauthors-db-1\n", types.CommandInvocationStatusSuccess)
	term, le, buf := newPipeEditor("/opt/rdm_sql_backups\n")
	var got []SQLBackupParams
	err := runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, sqlBackupInstances()[0], nil, nil, BackupHistory{}, nil, le, buf,
		func(p SQLBackupParams) { got = append(got, p) })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].InstanceID != "i-1" || got[0].Directory != "/opt/rdm_sql_backups" {
		t.Errorf("reported %+v", got)
	}
}

// A run that failed before it had a directory reports nothing: there is no
// command to reproduce.
func TestRunSQLBackup_NoReportWhenDockerCheckFails(t *testing.T) {
	ssmClient := sqlBackupFake("", types.CommandInvocationStatusSuccess)
	setResponseStatus(ssmClient, "command -v docker", types.CommandInvocationStatusFailed, "")
	term, le, buf := newPipeEditor("/d\n")
	var n int
	_ = runSQLBackup(context.Background(), term, map[string]awsclient.SSMAPI{"us-east-1": ssmClient}, sqlBackupInstances()[0], nil, nil, BackupHistory{}, nil, le, buf,
		func(SQLBackupParams) { n++ })
	if n != 0 {
		t.Errorf("reported %d times, want 0", n)
	}
}

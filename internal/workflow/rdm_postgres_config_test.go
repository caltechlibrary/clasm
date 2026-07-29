package workflow

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/caltechlibrary/clasm/internal/config"
)

// realCaltechauthorsDockerPS reproduces the exact `docker ps --format
// '{{.Image}}\t{{.Names}}'` output confirmed against CaltechAUTHORS
// production 2026-07-29, real bug: `docker ps --filter ancestor=postgres`
// returned zero results against this exact fleet, even though
// caltechauthors-db-1 (postgres:14.13) was confirmed running -- Docker's
// ancestor filter doesn't match a bare repository name against a
// specific tag. See DECISIONS.md, "docker ps --filter ancestor=postgres
// doesn't match a tagged image -- filter client-side instead."
const realCaltechauthorsDockerPS = "rabbitmq:3-management\tcaltechauthors-mq-1\n" +
	"minio/minio\tcaltechauthors-s3-1\n" +
	"opensearchproject/opensearch-dashboards:2.17.1\tcaltechauthors-opensearch-dashboards-1\n" +
	"redis:7\tcaltechauthors-cache-1\n" +
	"opensearchproject/opensearch:2.17.1\tcaltechauthors-search-1\n" +
	"dpage/pgadmin4:6\tcaltechauthors-pgadmin-1\n" +
	"postgres:14.13\tcaltechauthors-db-1\n"

func TestDiscoverPostgresContainer_RealCaltechauthorsFleetFindsOnlyThePostgresContainer(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: realCaltechauthorsDockerPS}

	got, err := discoverPostgresContainer(context.Background(), fake, "i-1", time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "caltechauthors-db-1" {
		t.Errorf("got %q, want %q", got, "caltechauthors-db-1")
	}
}

func TestDiscoverPostgresContainer_SingleResult(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "postgres:14.13\tcaltechauthors-db-1\n"}

	got, err := discoverPostgresContainer(context.Background(), fake, "i-1", time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "caltechauthors-db-1" {
		t.Errorf("got %q, want %q", got, "caltechauthors-db-1")
	}
	if len(fake.sentCommands) != 1 || !strings.Contains(fake.sentCommands[0], "docker ps") {
		t.Errorf("expected a docker ps command, got: %v", fake.sentCommands)
	}
	if strings.Contains(fake.sentCommands[0], "--filter") || strings.Contains(fake.sentCommands[0], "ancestor") {
		t.Errorf("expected no --filter ancestor= clause (doesn't match a tagged image, confirmed against real CaltechAUTHORS production) -- filtering happens client-side, got: %v", fake.sentCommands)
	}
}

func TestDiscoverPostgresContainer_MatchesBarePostgresImageNoTag(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "postgres\tsome-db-1\n"}

	got, err := discoverPostgresContainer(context.Background(), fake, "i-1", time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "some-db-1" {
		t.Errorf("got %q, want %q", got, "some-db-1")
	}
}

func TestDiscoverPostgresContainer_MatchesFullyQualifiedImageReference(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "docker.io/library/postgres:16-alpine\tsome-db-1\n"}

	got, err := discoverPostgresContainer(context.Background(), fake, "i-1", time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "some-db-1" {
		t.Errorf("got %q, want %q", got, "some-db-1")
	}
}

func TestDiscoverPostgresContainer_DoesNotMatchImageMerelyContainingPostgresAsASubstring(t *testing.T) {
	// e.g. a hypothetical "my-postgres-exporter" image shouldn't match --
	// only "postgres", "postgres:<tag>", or "<path>/postgres[:<tag>]".
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "my-postgres-exporter:latest\texporter-1\n"}

	_, err := discoverPostgresContainer(context.Background(), fake, "i-1", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected no match for an image that merely contains \"postgres\" as a substring, not as its own repository name")
	}
}

func TestDiscoverPostgresContainer_ZeroResultsErrors(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "\n"}

	_, err := discoverPostgresContainer(context.Background(), fake, "i-1", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error when no Postgres container is found")
	}
	if !strings.Contains(err.Error(), "no running Postgres container") {
		t.Errorf("expected a clear 'no running Postgres container' message, got: %v", err)
	}
}

func TestDiscoverPostgresContainer_MultipleResultsErrors(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "postgres:14.13\tcaltechauthors-db-1\npostgres:14.13\tcaltechauthors-db-2\n"}

	_, err := discoverPostgresContainer(context.Background(), fake, "i-1", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error when more than one Postgres container is found")
	}
	if !strings.Contains(err.Error(), "more than one") {
		t.Errorf("expected a clear 'more than one' message, got: %v", err)
	}
}

func TestDiscoverPostgresContainer_CommandFailureStatusErrors(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusFailed}

	_, err := discoverPostgresContainer(context.Background(), fake, "i-1", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected an error when the discovery command itself fails")
	}
}

func TestDiscoverPostgresContainer_SendCommandErrorPropagates(t *testing.T) {
	boom := errors.New("boom")
	fake := &fakeSSMClient{sendCommandErr: boom}

	_, err := discoverPostgresContainer(context.Background(), fake, "i-1", time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected the SendCommand error to propagate")
	}
}

func TestResolveRDMPostgresConfig_NewInstanceDiscoversAndSaves(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "postgres:14.13\tcaltechauthors-db-1\n"}
	var buf bytes.Buffer

	containerName, dbName, dbUser, updatedRules, err := resolveRDMPostgresConfig(context.Background(), &buf, fake, "i-1", "caltechauthors", "caltechauthors", nil, time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if containerName != "caltechauthors-db-1" {
		t.Errorf("containerName = %q, want %q", containerName, "caltechauthors-db-1")
	}
	if dbName != "caltechauthors" || dbUser != "caltechauthors" {
		t.Errorf("dbName/dbUser = %q/%q, want both to default to the fallback identifier", dbName, dbUser)
	}
	want := []config.RDMPostgresRule{{Pattern: "caltechauthors", ContainerName: "caltechauthors-db-1"}}
	if len(updatedRules) != 1 || updatedRules[0] != want[0] {
		t.Errorf("updatedRules = %v, want %v", updatedRules, want)
	}
	if !strings.Contains(buf.String(), "Discovered and saved") {
		t.Errorf("expected a discovery message, got:\n%s", buf.String())
	}
}

// TestResolveRDMPostgresConfig_UsesFallbackIdentifierNotInstanceName
// reproduces the real 2026-07-29 CaltechAUTHORS incident: instanceName
// (an EC2 Name tag, "newauthors" for this real instance) is used for
// Pattern matching, but dbName/dbUser must default to fallbackIdentifier
// ("caltechauthors", that instance's real Project tag) instead --
// getting this wrong produced a pg_dump against a nonexistent
// "newauthors" database.
func TestResolveRDMPostgresConfig_UsesFallbackIdentifierNotInstanceName(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "postgres:14.13\tcaltechauthors-db-1\n"}
	var buf bytes.Buffer

	_, dbName, dbUser, _, err := resolveRDMPostgresConfig(context.Background(), &buf, fake, "i-1", "newauthors", "caltechauthors", nil, time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dbName != "caltechauthors" || dbUser != "caltechauthors" {
		t.Errorf("dbName/dbUser = %q/%q, want both to use the fallback identifier %q, not instanceName %q", dbName, dbUser, "caltechauthors", "newauthors")
	}
}

func TestResolveRDMPostgresConfig_UnchangedContainerPrintsNothing(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "postgres:14.13\tcaltechauthors-db-1\n"}
	var buf bytes.Buffer
	existing := []config.RDMPostgresRule{{Pattern: "caltechauthors", ContainerName: "caltechauthors-db-1"}}

	_, _, _, updatedRules, err := resolveRDMPostgresConfig(context.Background(), &buf, fake, "i-1", "caltechauthors", "caltechauthors", existing, time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updatedRules) != 1 || updatedRules[0] != existing[0] {
		t.Errorf("updatedRules = %v, want unchanged %v", updatedRules, existing)
	}
	if buf.String() != "" {
		t.Errorf("expected no message when nothing changed, got:\n%s", buf.String())
	}
}

func TestResolveRDMPostgresConfig_ChangedContainerReportsAndUpdates(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "postgres:14.13\tcaltechauthors-db-1\n"}
	var buf bytes.Buffer
	existing := []config.RDMPostgresRule{{Pattern: "caltechauthors", ContainerName: "caltechauthors_db_1"}}

	containerName, _, _, updatedRules, err := resolveRDMPostgresConfig(context.Background(), &buf, fake, "i-1", "caltechauthors", "caltechauthors", existing, time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if containerName != "caltechauthors-db-1" {
		t.Errorf("containerName = %q, want %q", containerName, "caltechauthors-db-1")
	}
	if len(updatedRules) != 1 || updatedRules[0].ContainerName != "caltechauthors-db-1" {
		t.Errorf("updatedRules = %v, want ContainerName updated to %q", updatedRules, "caltechauthors-db-1")
	}
	out := buf.String()
	if !strings.Contains(out, "caltechauthors_db_1") || !strings.Contains(out, "caltechauthors-db-1") {
		t.Errorf("expected a change message naming both the old and new container names, got:\n%s", out)
	}
}

func TestResolveRDMPostgresConfig_PreservesDBOverridesThroughReconcile(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "postgres:14.13\tcaltechauthors-db-1\n"}
	var buf bytes.Buffer
	existing := []config.RDMPostgresRule{{Pattern: "caltechauthors", ContainerName: "caltechauthors_db_1", DBName: "custom_db", DBUser: "custom_user"}}

	_, dbName, dbUser, _, err := resolveRDMPostgresConfig(context.Background(), &buf, fake, "i-1", "caltechauthors", "caltechauthors", existing, time.Second, testPollInterval)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dbName != "custom_db" || dbUser != "custom_user" {
		t.Errorf("dbName/dbUser = %q/%q, want the preserved overrides %q/%q", dbName, dbUser, "custom_db", "custom_user")
	}
}

func TestResolveRDMPostgresConfig_DiscoveryFailurePropagates(t *testing.T) {
	fake := &fakeSSMClient{commandID: "cmd-1", finalStatus: types.CommandInvocationStatusSuccess, stdout: "\n"}
	var buf bytes.Buffer

	_, _, _, _, err := resolveRDMPostgresConfig(context.Background(), &buf, fake, "i-1", "caltechauthors", "caltechauthors", nil, time.Second, testPollInterval)
	if err == nil {
		t.Fatal("expected the discovery failure to propagate")
	}
}

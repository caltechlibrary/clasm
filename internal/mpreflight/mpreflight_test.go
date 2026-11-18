package mpreflight

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caltechlibrary/clasm/internal/executor"
)

// localExecutor runs the checks' real scripts with the local bash, against files
// in a temporary directory, so the scripts themselves are what is tested.
type localExecutor struct{ name string }

func (l localExecutor) Run(ctx context.Context, command string, timeout, poll time.Duration) (string, executor.CommandStatus, error) {
	out, err := exec.CommandContext(ctx, "bash", "-c", command).CombinedOutput()
	if err != nil {
		return string(out), executor.StatusFailed, nil
	}
	return string(out), executor.StatusSuccess, nil
}
func (localExecutor) Put(ctx context.Context, local, remote string) error { return nil }
func (localExecutor) Get(ctx context.Context, remote, local string) error { return nil }
func (l localExecutor) Name() string {
	if l.name == "" {
		return "local"
	}
	return l.name
}

var ex = localExecutor{}

func writeFile(t *testing.T, dir, rel, content string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// ---- mail suppression ----

func TestCheckMailSuppression_TrueIsOKAndAnythingElseIsNot(t *testing.T) {
	cases := map[string]bool{
		"INVENIO_MAIL_SUPPRESS_SEND=True\n":                                   true,
		"INVENIO_MAIL_SUPPRESS_SEND=\"True\"\n":                               true,
		"export INVENIO_MAIL_SUPPRESS_SEND=True\n":                            true,
		"INVENIO_MAIL_SUPPRESS_SEND=False\n":                                  false,
		"INVENIO_MAIL_SUPPRESS_SEND=false\n":                                  false,
		"INVENIO_MAIL_SUPPRESS_SEND=\n":                                       false,
		"FOO=1\n":                                                             false,
		"# INVENIO_MAIL_SUPPRESS_SEND=True\nFOO=1\n":                          false,
		"INVENIO_MAIL_SUPPRESS_SEND=False\nINVENIO_MAIL_SUPPRESS_SEND=True\n": true, // the last one wins
		"INVENIO_MAIL_SUPPRESS_SEND=True\nINVENIO_MAIL_SUPPRESS_SEND=False\n": false,
	}
	for content, want := range cases {
		dir := t.TempDir()
		writeFile(t, dir, "site/.env", content)
		f, err := CheckMailSuppression(context.Background(), ex, []string{dir + "/site/.env"})
		if err != nil || f.OK != want || f.Check == "" {
			t.Errorf("%q: %+v err=%v, want ok=%v", content, f, err, want)
		}
	}
}

func TestCheckMailSuppression_NoEnvFileCannotBeConfirmedSoItIsRefused(t *testing.T) {
	f, err := CheckMailSuppression(context.Background(), ex, []string{t.TempDir() + "/*/.env"})
	if err != nil || f.OK || !strings.Contains(f.Detail, ".env") {
		t.Errorf("%+v err=%v", f, err)
	}
}

func TestCheckMailSuppression_EveryEnvFileMustBeSuppressed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a/.env", "INVENIO_MAIL_SUPPRESS_SEND=True\n")
	writeFile(t, dir, "b/.env", "FOO=1\n")
	f, _ := CheckMailSuppression(context.Background(), ex, []string{dir + "/*/.env"})
	if f.OK || !strings.Contains(f.Detail, "b/.env") {
		t.Errorf("%+v", f)
	}
}

func TestCheckMailSuppression_ALineNotAtTheStartOfTheLineDoesNotCount(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "site/.env", "XINVENIO_MAIL_SUPPRESS_SEND=True\nFOO=INVENIO_MAIL_SUPPRESS_SEND=True\n")
	f, _ := CheckMailSuppression(context.Background(), ex, []string{dir + "/site/.env"})
	if f.OK {
		t.Errorf("%+v", f)
	}
}

func TestSetMailSuppression_ReplacesOrAddsTheLineKeepsOtherLinesAndBacksUpOnce(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a/.env", "FOO=1\nINVENIO_MAIL_SUPPRESS_SEND=False\nBAR=2\n")
	b := writeFile(t, dir, "b/.env", "FOO=1\n")
	if err := SetMailSuppression(context.Background(), ex, []string{dir + "/*/.env"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{a, b} {
		got, _ := os.ReadFile(p)
		if strings.Count(string(got), "INVENIO_MAIL_SUPPRESS_SEND=") != 1 || !strings.Contains(string(got), "INVENIO_MAIL_SUPPRESS_SEND=True") || !strings.Contains(string(got), "FOO=1") {
			t.Errorf("%s:\n%s", p, got)
		}
		if info, _ := os.Stat(p); info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v, a .env must stay private", p, info.Mode().Perm())
		}
	}
	if got, _ := os.ReadFile(a); !strings.Contains(string(got), "BAR=2") {
		t.Error("another line was lost")
	}
	bak, err := os.ReadFile(a + ".clasm-bak")
	if err != nil || !strings.Contains(string(bak), "INVENIO_MAIL_SUPPRESS_SEND=False") {
		t.Errorf("the original must be kept: %q %v", bak, err)
	}
	// a second run must not replace the backup with the edited file
	if err := SetMailSuppression(context.Background(), ex, []string{dir + "/*/.env"}); err != nil {
		t.Fatal(err)
	}
	if bak2, _ := os.ReadFile(a + ".clasm-bak"); !strings.Contains(string(bak2), "INVENIO_MAIL_SUPPRESS_SEND=False") {
		t.Error("the backup was overwritten by a later run")
	}
	f, _ := CheckMailSuppression(context.Background(), ex, []string{dir + "/*/.env"})
	if !f.OK {
		t.Errorf("after setting: %+v", f)
	}
}

func TestSetMailSuppression_ADuplicatedSettingBecomesOne(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "a/.env", "INVENIO_MAIL_SUPPRESS_SEND=False\nFOO=1\nexport INVENIO_MAIL_SUPPRESS_SEND=False\n")
	if err := SetMailSuppression(context.Background(), ex, []string{p}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if strings.Count(string(got), "INVENIO_MAIL_SUPPRESS_SEND=") != 1 || !strings.Contains(string(got), "FOO=1") {
		t.Errorf("file:\n%s", got)
	}
}

func TestSetMailSuppression_WithNoEnvFileIsAnError(t *testing.T) {
	if err := SetMailSuppression(context.Background(), ex, []string{t.TempDir() + "/*/.env"}); err == nil {
		t.Error("nothing to set must not look like success")
	}
}

// ---- credentials ----

const secretValue = "AKIAEXAMPLEVALUE0123"

func TestCheckNoProductionCredentials_CleanIsOK(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "site/.env", "INVENIO_SECRET_KEY=abc\nINVENIO_S3_ACCESS_KEY_ID=\nINVENIO_MAIL_PASSWORD=\n# AWS_SECRET_ACCESS_KEY=commented\n")
	f, err := CheckNoProductionCredentials(context.Background(), ex, Options{EnvGlobs: []string{dir + "/site/.env"}, HomeGlobs: []string{dir + "/home/*"}})
	if err != nil || !f.OK {
		t.Errorf("%+v err=%v", f, err)
	}
}

func TestCheckNoProductionCredentials_FindsEachKindAndNeverReportsAValue(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "site/.env", "INVENIO_S3_ACCESS_KEY_ID="+secretValue+"\nexport INVENIO_S3_SECRET_ACCESS_KEY='"+secretValue+"'\nINVENIO_DATACITE_PASSWORD="+secretValue+"\nINVENIO_MAIL_PASSWORD="+secretValue+"\nAWS_SESSION_TOKEN=\""+secretValue+"\"\n")
	writeFile(t, dir, "home/ubuntu/.aws/credentials", "[default]\naws_access_key_id = "+secretValue+"\naws_secret_access_key = "+secretValue+"\n")
	writeFile(t, dir, "home/other/.aws/config", "[default]\naws_session_token = "+secretValue+"\n")
	writeFile(t, dir, "etc/environment", "AWS_ACCESS_KEY_ID="+secretValue+"\n")
	f, err := CheckNoProductionCredentials(context.Background(), ex, Options{
		EnvGlobs: []string{dir + "/site/.env"}, HomeGlobs: []string{dir + "/home/*"}, CredentialGlobs: []string{dir + "/etc/environment"}})
	if err != nil || f.OK {
		t.Fatalf("%+v err=%v", f, err)
	}
	for _, want := range []string{"INVENIO_S3_ACCESS_KEY_ID", "INVENIO_S3_SECRET_ACCESS_KEY", "INVENIO_DATACITE_PASSWORD", "INVENIO_MAIL_PASSWORD", "AWS_SESSION_TOKEN", "credentials", "aws_access_key_id", "other/.aws/config", "etc/environment", "AWS_ACCESS_KEY_ID"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("the finding should name %q:\n%s", want, f.Detail)
		}
	}
	if strings.Contains(f.Detail, secretValue) {
		t.Errorf("a secret value leaked into the finding:\n%s", f.Detail)
	}
}

func TestCheckNoProductionCredentials_EachNamePatternIsCaughtOnItsOwn(t *testing.T) {
	for _, name := range []string{"INVENIO_S3_ACCESS_KEY_ID", "INVENIO_S3_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "INVENIO_DATACITE_PASSWORD", "INVENIO_MAIL_PASSWORD"} {
		dir := t.TempDir()
		writeFile(t, dir, "site/.env", name+"="+secretValue+"\n")
		f, err := CheckNoProductionCredentials(context.Background(), ex, Options{EnvGlobs: []string{dir + "/site/.env"}, HomeGlobs: []string{dir + "/none/*"}})
		if err != nil || f.OK || !strings.Contains(f.Detail, name) {
			t.Errorf("%s: %+v err=%v", name, f, err)
		}
	}
}

func TestCheckNoProductionCredentials_AnEmptyValueIsNotACredential(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "site/.env", "INVENIO_S3_ACCESS_KEY_ID=\nINVENIO_S3_SECRET_ACCESS_KEY=\"\"\nINVENIO_MAIL_PASSWORD='  '\n")
	f, _ := CheckNoProductionCredentials(context.Background(), ex, Options{EnvGlobs: []string{dir + "/site/.env"}, HomeGlobs: []string{dir + "/none/*"}})
	if !f.OK {
		t.Errorf("%+v", f)
	}
}

func TestCheckNoProductionCredentials_AnAWSCredentialsFileWithoutAKeyIsFine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "home/ubuntu/.aws/credentials", "[default]\n")
	f, _ := CheckNoProductionCredentials(context.Background(), ex, Options{EnvGlobs: []string{dir + "/x/.env"}, HomeGlobs: []string{dir + "/home/*"}})
	if !f.OK {
		t.Errorf("%+v", f)
	}
}

func TestOptionsAreRefusedWhenAGlobCouldInjectACommand(t *testing.T) {
	for _, bad := range []string{"/Sites/*/.env; rm -rf /", "/a b/.env", "$(id)", "`id`", "/a'b", "/a/../b", "relative/.env"} {
		if _, err := CheckNoProductionCredentials(context.Background(), ex, Options{EnvGlobs: []string{bad}}); err == nil {
			t.Errorf("%q must be refused", bad)
		}
		if _, err := CheckMailSuppression(context.Background(), ex, []string{bad}); err == nil {
			t.Errorf("%q must be refused by the mail check", bad)
		}
		if err := SetMailSuppression(context.Background(), ex, []string{bad}); err == nil {
			t.Errorf("%q must be refused by the setter", bad)
		}
	}
}

// ---- disk space ----

func TestCheckDiskSpace_EnoughRoomIsOKAndTooLittleNamesBothNumbers(t *testing.T) {
	dir := t.TempDir()
	if f, err := CheckDiskSpace(context.Background(), ex, dir, 1); err != nil || !f.OK {
		t.Errorf("1 byte: %+v err=%v", f, err)
	}
	f, err := CheckDiskSpace(context.Background(), ex, dir, 1<<60)
	if err != nil || f.OK || !strings.Contains(f.Detail, "free") || !strings.Contains(f.Detail, dir) {
		t.Errorf("an exabyte: %+v err=%v", f, err)
	}
}

func TestCheckDiskSpace_ANonPositiveNeedIsNotChecked(t *testing.T) {
	if f, err := CheckDiskSpace(context.Background(), ex, "/no/such/place", 0); err != nil || !f.OK {
		t.Errorf("%+v err=%v", f, err)
	}
}

func TestCheckDiskSpace_AnUnreadablePathIsRefusedNotIgnored(t *testing.T) {
	f, err := CheckDiskSpace(context.Background(), ex, "/no/such/place/anywhere", 1)
	if err == nil && f.OK {
		t.Errorf("an unknown free space must not pass: %+v", f)
	}
}

func TestEstimates_GrowWithTheInputAndAGzipDumpNeedsMoreRoomThanAPlainOne(t *testing.T) {
	const gb = int64(1) << 30
	if SnapshotNeed(2*gb) < 4*gb || SnapshotNeed(2*gb) > 6*gb {
		t.Errorf("a 2 GB repository needs its tar and its unpacked copy: %d", SnapshotNeed(2*gb))
	}
	if DumpNeed(300<<20, true) <= DumpNeed(300<<20, false) {
		t.Error("a gzip dump expands, so it needs more than a plain one of the same size")
	}
	if DumpNeed(300<<20, true) < 3*gb {
		t.Errorf("a 317 MB production dump expands to about 3.3 GB and loads into a database: %d", DumpNeed(300<<20, true))
	}
	if SnapshotNeed(0) != 0 || DumpNeed(0, true) != 0 {
		t.Error("nothing to load needs no room")
	}
}

// ---- Run ----

func TestRun_AllPassingReturnsTheFindingsAndNoError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "site/.env", "INVENIO_MAIL_SUPPRESS_SEND=True\n")
	fs, err := Run(context.Background(), ex, Options{EnvGlobs: []string{dir + "/site/.env"}, HomeGlobs: []string{dir + "/h/*"}, DiskPath: dir, DiskNeed: 1})
	if err != nil || len(fs) != 3 {
		t.Fatalf("fs=%+v err=%v", fs, err)
	}
	for _, f := range fs {
		if !f.OK {
			t.Errorf("%+v", f)
		}
	}
}

func TestRun_AFailureIsARefusalCarryingEveryFindingAndRunsAllChecks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "site/.env", "INVENIO_S3_SECRET_ACCESS_KEY="+secretValue+"\n")
	fs, err := Run(context.Background(), ex, Options{EnvGlobs: []string{dir + "/site/.env"}, HomeGlobs: []string{dir + "/h/*"}, DiskPath: dir, DiskNeed: 1 << 60})
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v", err)
	}
	if len(refused.Findings) != 3 || len(fs) != 3 {
		t.Errorf("every check should run and report: %+v", refused.Findings)
	}
	bad := 0
	for _, f := range refused.Findings {
		if !f.OK {
			bad++
		}
	}
	if bad != 3 {
		t.Errorf("mail unset, a credential present and no room: want 3 failures, got %d", bad)
	}
	if strings.Contains(err.Error(), secretValue) {
		t.Error("the refusal message leaked a value")
	}
}

func TestRun_SetMailSuppressionFixesThatCheckOnly(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "site/.env", "FOO=1\n")
	fs, err := Run(context.Background(), ex, Options{EnvGlobs: []string{dir + "/site/.env"}, HomeGlobs: []string{dir + "/h/*"}, SetMailSuppression: true})
	if err != nil {
		t.Fatalf("err = %v, findings %+v", err, fs)
	}
	if got, _ := os.ReadFile(p); !strings.Contains(string(got), "INVENIO_MAIL_SUPPRESS_SEND=True") {
		t.Errorf("not set:\n%s", got)
	}
	if !strings.Contains(fs[0].Detail, "set by clasm") {
		t.Errorf("the finding should say it was set: %q", fs[0].Detail)
	}
}

func TestRun_SetMailSuppressionDoesNotMaskACredentialRefusal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "site/.env", "INVENIO_MAIL_PASSWORD="+secretValue+"\n")
	_, err := Run(context.Background(), ex, Options{EnvGlobs: []string{dir + "/site/.env"}, HomeGlobs: []string{dir + "/h/*"}, SetMailSuppression: true})
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Errorf("err = %v", err)
	}
}

func TestRun_NothingIsChangedWhenSetMailSuppressionIsNotAsked(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "site/.env", "FOO=1\n")
	Run(context.Background(), ex, Options{EnvGlobs: []string{dir + "/site/.env"}, HomeGlobs: []string{dir + "/h/*"}})
	if got, _ := os.ReadFile(p); string(got) != "FOO=1\n" {
		t.Errorf("a check changed the file:\n%s", got)
	}
}

// ---- what holds user data afterwards ----

func TestLocalUserData_ListsEveryFileWithItsSize(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "d.sql.gz", "12345")
	writeFile(t, dir, "rdm-1/indices/a/0/d.dat", "123")
	got, err := LocalUserData(dir)
	if err != nil || len(got) != 2 {
		t.Fatalf("got %+v err %v", got, err)
	}
	var total int64
	for _, f := range got {
		total += f.Bytes
		if !strings.HasPrefix(f.Path, dir) {
			t.Errorf("%s", f.Path)
		}
	}
	if total != 8 {
		t.Errorf("total = %d", total)
	}
}

func TestLocalUserData_ListsFilesNotDirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a/b/c.dat", "1")
	got, _ := LocalUserData(dir)
	if len(got) != 1 || !strings.HasSuffix(got[0].Path, "c.dat") {
		t.Errorf("got %+v", got)
	}
}

func TestLocalUserData_ANonExistentCacheIsEmptyNotAnError(t *testing.T) {
	got, err := LocalUserData(filepath.Join(t.TempDir(), "absent"))
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v err %v", got, err)
	}
}

// The removal tests only ever delete inside a temporary directory. What is
// refused is tested on the guard alone, which deletes nothing, so a regression
// in the guard cannot delete anything the test happens to point it at.
func TestRemoveLocalUserData_RemovesTheFilesAndLeavesTheDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "d.sql.gz", "x")
	writeFile(t, dir, "rdm-1/indices/a/0/d.dat", "y")
	if err := RemoveLocalUserData(dir); err != nil {
		t.Fatal(err)
	}
	if got, _ := LocalUserData(dir); len(got) != 0 {
		t.Errorf("left behind: %+v", got)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the directory itself must remain: %v", err)
	}
	if err := RemoveLocalUserData(filepath.Join(dir, "absent")); err != nil {
		t.Errorf("a cache that does not exist is already empty: %v", err)
	}
}

func TestCheckRemovable_RefusesEmptyRelativeRootAndHomeAndAnythingAboveIt(t *testing.T) {
	const home = "/Users/someone"
	for _, bad := range []string{"", "/", ".", "relative/dir", "/Users/someone", "/Users/someone/", "/Users", "//"} {
		if err := checkRemovable(bad, home); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	for _, ok := range []string{"/Users/someone/.cache/clasm", "/tmp/clasm-cache", "/Users/someone2"} {
		if err := checkRemovable(ok, home); err != nil {
			t.Errorf("%q must be allowed: %v", ok, err)
		}
	}
	if err := checkRemovable("/tmp/x", ""); err != nil {
		t.Errorf("an unknown home must not block an ordinary path: %v", err)
	}
	if err := checkRemovable("/", ""); err == nil {
		t.Error("the root is refused even when the home is unknown")
	}
}

func TestVMUserData_NamesWhatHoldsUserData(t *testing.T) {
	joined := strings.Join(VMUserData(), "\n")
	for _, want := range []string{"database", "OpenSearch", ".env", "snapshots"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q:\n%s", want, joined)
		}
	}
}

func TestRun_ASettingThatIsAlreadyRightIsLeftAloneEvenWhenSettingIsAsked(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "site/.env", "INVENIO_MAIL_SUPPRESS_SEND=True\n")
	fs, err := Run(context.Background(), ex, Options{EnvGlobs: []string{dir + "/site/.env"}, HomeGlobs: []string{dir + "/h/*"}, SetMailSuppression: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p + ".clasm-bak"); err == nil {
		t.Error("a file that was already right must not be touched or backed up")
	}
	if strings.Contains(fs[0].Detail, "set by clasm") {
		t.Errorf("nothing was set: %q", fs[0].Detail)
	}
}

// ---- containers ----

// fakeDocker puts a docker on PATH whose inspect output has the shape the real
// one prints for the format the check asks for: a name line, then one
// environment line each, then a blank line.
func fakeDocker(t *testing.T, inspectOutput string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/bash\ncase \"$1\" in\n  ps) echo abc123; echo def456;;\n  inspect) cat <<'EOT'\n" + inspectOutput + "EOT\n;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestCheckNoProductionCredentials_ReadsEveryContainersEnvironmentNamesOnlyAndOnlyWhenAsked(t *testing.T) {
	fakeDocker(t, "/web\nFOO=1\nINVENIO_S3_ACCESS_KEY_ID="+secretValue+"\nINVENIO_MAIL_PASSWORD=\n\n/db\nPOSTGRES_PASSWORD=x\n\n")
	dir := t.TempDir()
	o := Options{EnvGlobs: []string{dir + "/x/.env"}, HomeGlobs: []string{dir + "/h/*"}}
	if f, _ := CheckNoProductionCredentials(context.Background(), ex, o); !f.OK {
		t.Errorf("containers are not read unless asked: %+v", f)
	}
	o.Docker = true
	f, err := CheckNoProductionCredentials(context.Background(), ex, o)
	if err != nil || f.OK {
		t.Fatalf("%+v err=%v", f, err)
	}
	if !strings.Contains(f.Detail, "docker:/web: INVENIO_S3_ACCESS_KEY_ID") {
		t.Errorf("want the container and the name:\n%s", f.Detail)
	}
	if strings.Contains(f.Detail, secretValue) {
		t.Error("a value leaked")
	}
	if strings.Contains(f.Detail, "INVENIO_MAIL_PASSWORD") || strings.Contains(f.Detail, "POSTGRES_PASSWORD") || strings.Contains(f.Detail, "/db") {
		t.Errorf("an empty value and an unrelated password are not production credentials:\n%s", f.Detail)
	}
}

func TestCheckNoProductionCredentials_NoDockerOnTheVMIsNotAnError(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin") // may hold a docker on a developer's Mac; the check tolerates either
	dir := t.TempDir()
	f, err := CheckNoProductionCredentials(context.Background(), ex, Options{EnvGlobs: []string{dir + "/x/.env"}, HomeGlobs: []string{dir + "/h/*"}, Docker: true})
	if err != nil {
		t.Errorf("err = %v (finding %+v)", err, f)
	}
}

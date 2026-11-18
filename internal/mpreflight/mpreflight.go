// Package mpreflight holds the checks that must pass before production data is
// loaded into a Multipass VM (DR-0207 and the clmpm feature request): mail is
// suppressed, the VM holds no credentials that could act on production, and
// there is room for the load. They refuse; they do not warn. They never print
// or return a secret's value, only where it is and what it is called.
package mpreflight

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/caltechlibrary/clasm/internal/executor"
)

// Finding is the result of one check.
type Finding struct {
	Check  string `json:"check"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// RefusedError is returned by Run when one or more checks failed. Findings holds
// every check's result, passing or not.
type RefusedError struct{ Findings []Finding }

func (e *RefusedError) Error() string {
	var bad []string
	for _, f := range e.Findings {
		if !f.OK {
			bad = append(bad, f.Check+": "+f.Detail)
		}
	}
	return "refused: " + strings.Join(bad, "; ")
}

// Options say where to look and how much room is needed.
type Options struct {
	EnvGlobs              []string // RDM .env files; default /Sites/*/.env
	CredentialGlobs       []string // more files to scan for credential names; default /etc/environment
	HomeGlobs             []string // home directories whose .aws files are checked; default /root /home/*
	Docker                bool     // also scan running and stopped containers' environment names
	DiskPath              string   // where the load writes; default /var/tmp
	DiskNeed              int64    // bytes the load needs; 0 skips the check
	SetMailSuppression    bool     // set INVENIO_MAIL_SUPPRESS_SEND=True when it is not already
	HostsFile             string   // the hosts file to read and extend; default /etc/hosts
	SetDataCiteSafeguards bool     // set DataCite test mode and block its API hosts when they are not already
}

func (o Options) withDefaults() Options {
	if len(o.EnvGlobs) == 0 {
		o.EnvGlobs = []string{"/Sites/*/.env"}
	}
	if len(o.CredentialGlobs) == 0 {
		o.CredentialGlobs = []string{"/etc/environment"}
	}
	if len(o.HomeGlobs) == 0 {
		o.HomeGlobs = []string{"/root", "/home/*"}
	}
	if o.HostsFile == "" {
		o.HostsFile = "/etc/hosts"
	}
	if o.DiskPath == "" {
		o.DiskPath = "/var/tmp"
	}
	return o
}

// safeGlob admits only absolute paths made of characters that cannot end a shell
// word or start a command, since a glob is placed unquoted in a script so the
// shell expands it.
var safeGlob = regexp.MustCompile(`^/[A-Za-z0-9_./*@:+-]*$`)

func globWords(globs []string) (string, error) {
	var words []string
	for _, g := range globs {
		if !safeGlob.MatchString(g) || strings.Contains(g, "..") {
			return "", fmt.Errorf("refusing the path pattern %q: it must be absolute and use only letters, digits and _ . / * @ : + -", g)
		}
		words = append(words, g)
	}
	return strings.Join(words, " "), nil
}

const stepTimeout = 2 * time.Minute

func run(ctx context.Context, ex executor.Executor, script string) (string, executor.CommandStatus, error) {
	out, status, err := ex.Run(ctx, script, stepTimeout, 2*time.Second)
	if err != nil {
		return "", "", fmt.Errorf("running a pre-flight check on %s: %w", ex.Name(), err)
	}
	return out, status, nil
}

// CheckMailSuppression reads INVENIO_MAIL_SUPPRESS_SEND from every .env file
// matching envGlobs. It passes only if there is at least one file and the last
// setting in each is exactly True; a missing setting, a missing file, or any
// other value fails, because mail cannot be confirmed off.
//
// @param ctx {context.Context} cancels the check
// @param ex {executor.Executor} reaches the VM
// @param envGlobs {[]string} absolute path patterns of the RDM .env files
// @returns {Finding} the result, naming the files that fail
// @returns {error} an unsafe pattern, or why the VM could not be reached
// @example
//
//	f, err := CheckMailSuppression(ctx, ex, []string{"/Sites/*/.env"})
func CheckMailSuppression(ctx context.Context, ex executor.Executor, envGlobs []string) (Finding, error) {
	const check = "mail suppression"
	words, err := globWords(envGlobs)
	if err != nil {
		return Finding{}, err
	}
	script := `for f in ` + words + `; do
  [ -f "$f" ] || continue
  v=$(awk -v q="'" '{ l=$0; sub(/^[ \t]*export[ \t]+/, "", l) } l ~ /^INVENIO_MAIL_SUPPRESS_SEND=/ { v=substr(l, index(l, "=")+1) } END { gsub("^[\"" q " \t]+|[\"" q " \t]+$", "", v); print v }' "$f")
  echo "MAIL $f [$v]"
done
`
	out, _, err := run(ctx, ex, script)
	if err != nil {
		return Finding{}, err
	}
	var bad []string
	files := 0
	for _, line := range strings.Split(out, "\n") {
		rest, ok := strings.CutPrefix(line, "MAIL ")
		if !ok {
			continue
		}
		files++
		i := strings.LastIndex(rest, " [")
		if i < 0 {
			continue
		}
		if rest[i+2:] != "True]" {
			bad = append(bad, rest[:i]+": INVENIO_MAIL_SUPPRESS_SEND is not set to True")
		}
	}
	switch {
	case files == 0:
		return Finding{Check: check, Detail: "no .env file found under " + words + ", so mail suppression cannot be confirmed"}, nil
	case len(bad) > 0:
		return Finding{Check: check, Detail: strings.Join(bad, "\n")}, nil
	}
	return Finding{Check: check, OK: true, Detail: fmt.Sprintf("INVENIO_MAIL_SUPPRESS_SEND=True in %d file(s)", files)}, nil
}

// SetMailSuppression sets INVENIO_MAIL_SUPPRESS_SEND=True in every .env file
// matching envGlobs, replacing any existing setting and keeping the other lines
// and the file's mode. The original is saved once beside it as .clasm-bak and a
// later run never replaces that backup. The application must be restarted for
// the setting to take effect.
//
// @param ctx {context.Context} cancels the change
// @param ex {executor.Executor} reaches the VM
// @param envGlobs {[]string} absolute path patterns of the RDM .env files
// @returns {error} an unsafe pattern, no file found, or why the edit failed
// @example
//
//	err := SetMailSuppression(ctx, ex, []string{"/Sites/*/.env"})
func SetMailSuppression(ctx context.Context, ex executor.Executor, envGlobs []string) error {
	words, err := globWords(envGlobs)
	if err != nil {
		return err
	}
	script := `set -e
umask 077
n=0
for f in ` + words + `; do
  [ -f "$f" ] || continue
  [ -e "$f.clasm-bak" ] || cp -p "$f" "$f.clasm-bak"
  awk '{ t=$0; sub(/^[ \t]*export[ \t]+/, "", t) } t ~ /^INVENIO_MAIL_SUPPRESS_SEND=/ { if (!done) { print "INVENIO_MAIL_SUPPRESS_SEND=True"; done=1 } next } { print $0 } END { if (!done) print "INVENIO_MAIL_SUPPRESS_SEND=True" }' "$f" > "$f.clasm-tmp"
  cat "$f.clasm-tmp" > "$f"
  rm -f "$f.clasm-tmp"
  n=$((n+1))
done
[ "$n" -gt 0 ] || { echo "no .env file found under ` + words + `"; exit 1; }
echo "SET $n"
`
	out, status, err := run(ctx, ex, script)
	if err != nil {
		return err
	}
	if status != executor.StatusSuccess {
		return fmt.Errorf("setting INVENIO_MAIL_SUPPRESS_SEND on %s failed (%s): %s", ex.Name(), status, strings.TrimSpace(out))
	}
	return nil
}

// CheckNoProductionCredentials looks, in the .env files, /etc/environment, each
// home directory's .aws files and (when asked) every container's environment,
// for a setting whose name is an AWS or S3 access key, secret key or session
// token, a DataCite password, or the mail password, and whose value is not
// empty. Any such setting fails the check: this VM could then act on
// production. Only the file and the setting's name are reported, never a value.
//
// @param ctx {context.Context} cancels the check
// @param ex {executor.Executor} reaches the VM
// @param o {Options} where to look
// @returns {Finding} the result, listing each file and name found
// @returns {error} an unsafe pattern, or why the VM could not be reached
// @example
//
//	f, err := CheckNoProductionCredentials(ctx, ex, Options{Docker: true})
func CheckNoProductionCredentials(ctx context.Context, ex executor.Executor, o Options) (Finding, error) {
	const check = "no production credentials"
	o = o.withDefaults()
	files, err := globWords(append(append([]string{}, o.EnvGlobs...), o.CredentialGlobs...))
	if err != nil {
		return Finding{}, err
	}
	homes, err := globWords(o.HomeGlobs)
	if err != nil {
		return Finding{}, err
	}
	docker := ""
	if o.Docker {
		docker = `if command -v docker >/dev/null 2>&1; then
  ids=$(docker ps -aq 2>/dev/null || true)
  if [ -n "$ids" ]; then
    docker inspect --format '{{.Name}}{{range .Config.Env}}{{"\n"}}{{.}}{{end}}{{"\n"}}' $ids 2>/dev/null | awk -v pat="$pat" -v q="'" '
      /^\// { name=$0; next }
      { i=index($0, "="); if (i==0) next; k=substr($0,1,i-1); v=substr($0,i+1); gsub("^[\"" q " \t]+|[\"" q " \t]+$", "", v)
        if (v != "" && toupper(k) ~ pat) print "FOUND docker:" name " " k }'
  fi
fi
`
	}
	script := `umask 077
pat='ACCESS_KEY|SECRET_ACCESS|SESSION_TOKEN|DATACITE[A-Z_]*PASSWORD|MAIL_PASSWORD'
scan() {
  awk -v f="$1" -v pat="$pat" -v q="'" '
    { l=$0; sub(/^[ \t]*export[ \t]+/, "", l) }
    l ~ /^[ \t]*#/ { next }
    { i=index(l, "="); if (i==0) next; k=substr(l,1,i-1); v=substr(l,i+1)
      gsub("^[ \t]+|[ \t]+$", "", k); gsub("^[\"" q " \t]+|[\"" q " \t]+$", "", v)
      if (v != "" && toupper(k) ~ pat) print "FOUND " f " " k }' "$1"
}
for f in ` + files + `; do [ -f "$f" ] && scan "$f"; done
for h in ` + homes + `; do
  for f in "$h/.aws/credentials" "$h/.aws/config"; do [ -f "$f" ] && scan "$f"; done
done
` + docker + `true
`
	out, _, err := run(ctx, ex, script)
	if err != nil {
		return Finding{}, err
	}
	var found []string
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "FOUND "); ok {
			if i := strings.LastIndex(rest, " "); i > 0 {
				found = append(found, rest[:i]+": "+rest[i+1:])
			}
		}
	}
	if len(found) > 0 {
		sort.Strings(found)
		return Finding{Check: check, Detail: "this VM holds credentials that could act on production (values not shown):\n" + strings.Join(found, "\n")}, nil
	}
	return Finding{Check: check, OK: true, Detail: "no AWS, S3, DataCite or mail credentials found"}, nil
}

// CheckDiskSpace compares the free space under path on the VM with need bytes.
// A need of zero or less is not checked. Free space that cannot be read fails
// the check.
//
// @param ctx {context.Context} cancels the check
// @param ex {executor.Executor} reaches the VM
// @param path {string} a directory on the filesystem the load writes to
// @param need {int64} the bytes the load needs
// @returns {Finding} the result, with both numbers
// @returns {error} an unsafe path, or why the VM could not be reached
// @example
//
//	f, err := CheckDiskSpace(ctx, ex, "/var/tmp", SnapshotNeed(rep.Bytes))
func CheckDiskSpace(ctx context.Context, ex executor.Executor, path string, need int64) (Finding, error) {
	const check = "disk space"
	if need <= 0 {
		return Finding{Check: check, OK: true, Detail: "no space requirement given, not checked"}, nil
	}
	if !safeGlob.MatchString(path) || strings.ContainsAny(path, "*") || strings.Contains(path, "..") {
		return Finding{}, fmt.Errorf("refusing the path %q", path)
	}
	out, status, err := run(ctx, ex, `df -Pk `+path+` | awk 'NR==2 { print "AVAIL " $4*1024 }'`)
	if err != nil {
		return Finding{}, err
	}
	var avail int64 = -1
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "AVAIL "); ok {
			if n, perr := strconv.ParseInt(strings.TrimSpace(rest), 10, 64); perr == nil {
				avail = n
			}
		}
	}
	if status != executor.StatusSuccess || avail < 0 {
		return Finding{Check: check, Detail: fmt.Sprintf("could not read the free space under %s on %s", path, ex.Name())}, nil
	}
	if avail < need {
		return Finding{Check: check, Detail: fmt.Sprintf("%s has %s free under %s and the load needs about %s", ex.Name(), human(avail), path, human(need))}, nil
	}
	return Finding{Check: check, OK: true, Detail: fmt.Sprintf("%s free under %s, about %s needed", human(avail), path, human(need))}, nil
}

func human(b int64) string {
	const gb = float64(1 << 30)
	if b >= 1<<30 {
		return fmt.Sprintf("%.1f GiB", float64(b)/gb)
	}
	return fmt.Sprintf("%.0f MiB", float64(b)/float64(1<<20))
}

// Run runs every check and returns all the findings. When any fails it also
// returns a *RefusedError carrying them. With SetMailSuppression, a failed mail
// check is fixed and re-read once; the other checks are never changed by it.
//
// @param ctx {context.Context} cancels the checks
// @param ex {executor.Executor} reaches the VM
// @param o {Options} where to look, how much room, and whether to set mail suppression
// @returns {[]Finding} the mail, credential and disk findings, in that order
// @returns {error} *RefusedError when a check failed, or why a check could not run
// @example
//
//	findings, err := Run(ctx, ex, Options{DiskNeed: SnapshotNeed(rep.Bytes), Docker: true})
func Run(ctx context.Context, ex executor.Executor, o Options) ([]Finding, error) {
	o = o.withDefaults()
	mail, err := CheckMailSuppression(ctx, ex, o.EnvGlobs)
	if err != nil {
		return nil, err
	}
	if !mail.OK && o.SetMailSuppression {
		if err := SetMailSuppression(ctx, ex, o.EnvGlobs); err != nil {
			return nil, err
		}
		if mail, err = CheckMailSuppression(ctx, ex, o.EnvGlobs); err != nil {
			return nil, err
		}
		if mail.OK {
			mail.Detail += " (set by clasm; the original is kept as .clasm-bak, and the application must be restarted)"
		}
	}
	dc, err := CheckDataCite(ctx, ex, o.EnvGlobs, o.HostsFile)
	if err != nil {
		return nil, err
	}
	if !dc.OK && o.SetDataCiteSafeguards {
		if err := SetDataCiteSafeguards(ctx, ex, o.EnvGlobs, o.HostsFile); err != nil {
			return nil, err
		}
		if dc, err = CheckDataCite(ctx, ex, o.EnvGlobs, o.HostsFile); err != nil {
			return nil, err
		}
		if dc.OK {
			dc.Detail += " (set by clasm; the originals are kept as .clasm-bak, and the application must be restarted)"
		}
	}
	creds, err := CheckNoProductionCredentials(ctx, ex, o)
	if err != nil {
		return nil, err
	}
	disk, err := CheckDiskSpace(ctx, ex, o.DiskPath, o.DiskNeed)
	if err != nil {
		return nil, err
	}
	findings := []Finding{mail, creds, disk, dc}
	for _, f := range findings {
		if !f.OK {
			return findings, &RefusedError{Findings: findings}
		}
	}
	return findings, nil
}

// SnapshotNeed estimates the room a snapshot load needs under /var/tmp and the
// repository directory: the tar and the unpacked repository, plus ten percent.
//
// @param repoBytes {int64} the repository's size, from the fetch report
// @returns {int64} the estimated bytes needed
// @example
//
//	need := SnapshotNeed(rep.Bytes)
func SnapshotNeed(repoBytes int64) int64 {
	if repoBytes <= 0 {
		return 0
	}
	return repoBytes*2 + repoBytes/10
}

// DumpNeed estimates the room a SQL load needs: the file as sent, the plain SQL
// a gzip expands to, and the database it loads into. The 2026-10-30 hand run
// went from a 317 MB gzip to 3.3 GB of SQL, about ten times, and the database is
// about as large as the SQL, so a gzip dump is estimated at twenty-one times its
// size and a plain one at three.
//
// @param dumpBytes {int64} the dump's size as sent
// @param gzip {bool} whether it is gzip-compressed
// @returns {int64} the estimated bytes needed
// @example
//
//	need := DumpNeed(rep.Size, strings.HasSuffix(rep.Key, ".gz"))
func DumpNeed(dumpBytes int64, gzip bool) int64 {
	if dumpBytes <= 0 {
		return 0
	}
	if gzip {
		return dumpBytes * 21
	}
	return dumpBytes * 3
}

// LocalFile is one file of user data on this machine.
type LocalFile struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// LocalUserData lists every file under cacheDir, which holds fetched dumps and
// snapshots and so holds user data. A cache that does not exist is empty.
//
// @param cacheDir {string} the fetch cache directory
// @returns {[]LocalFile} the files, in path order
// @returns {error} why the directory could not be read
// @example
//
//	files, err := LocalUserData(cacheDir)
func LocalUserData(cacheDir string) ([]LocalFile, error) {
	files := []LocalFile{}
	err := filepath.WalkDir(cacheDir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			files = append(files, LocalFile{Path: p, Bytes: info.Size()})
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return []LocalFile{}, nil
	}
	return files, err
}

// RemoveLocalUserData deletes everything under cacheDir and leaves the
// directory. It refuses an empty or relative path, the root, and the home
// directory or anything above it, so a wrong setting cannot delete more than a
// cache.
//
// @param cacheDir {string} the fetch cache directory
// @returns {error} why it refused, or why a removal failed
// @example
//
//	err := RemoveLocalUserData(cacheDir)
func RemoveLocalUserData(cacheDir string) error {
	home, _ := os.UserHomeDir()
	if err := checkRemovable(cacheDir, home); err != nil {
		return err
	}
	clean := filepath.Clean(cacheDir)
	entries, err := os.ReadDir(clean)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(clean, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// checkRemovable is the guard RemoveLocalUserData applies, kept apart so it can
// be tested without anything being deleted: it refuses an empty or relative
// path, the root, and the home directory or any directory above it.
func checkRemovable(cacheDir, home string) error {
	if cacheDir == "" || !filepath.IsAbs(cacheDir) {
		return fmt.Errorf("refusing to remove %q: the cache directory must be an absolute path", cacheDir)
	}
	clean := filepath.Clean(cacheDir)
	if clean == string(filepath.Separator) {
		return errors.New("refusing to remove the root directory")
	}
	if home != "" {
		if h := filepath.Clean(home); h == clean || strings.HasPrefix(h+string(filepath.Separator), clean+string(filepath.Separator)) {
			return fmt.Errorf("refusing to remove %s: it is or contains the home directory", clean)
		}
	}
	return nil
}

// VMUserData says what on the VM holds user data after a load, so the author can
// decide what to keep.
//
// @returns {[]string} one line for each place
// @example
//
//	for _, line := range VMUserData() { fmt.Println(line) }
func VMUserData() []string {
	return []string{
		"the PostgreSQL database and its Docker volume: every user, record and request from the dump",
		"the OpenSearch indices and their Docker volume, and the repository directory (/opt/rdm_opensearch_backups) that held the snapshot",
		"the application's .env file: secrets for this VM only, no production credentials",
		"the VM's snapshots and clones (multipass snapshot, clone): each one holds a copy of all of the above",
	}
}

// dataCiteHosts are the DataCite API hosts a development VM must not be able to
// reach, so that a publish cannot register a DOI at DataCite.
var dataCiteHosts = []string{"api.datacite.org", "api.test.datacite.org"}

// CheckDataCite confirms a development VM cannot register DOIs at DataCite:
// INVENIO_DATACITE_TEST_MODE is exactly True in every .env file, and both
// DataCite API hosts resolve to a loopback address in the hosts file. A password
// is a separate matter, refused by CheckNoProductionCredentials. This is the
// set-up that lets a VM be tested without a DataCite account: DataCite stays
// enabled, so records with DataCite DOIs still display, but every registration
// goes nowhere. (INVENIO_DATACITE_ENABLED=false does not work: Invenio parses
// the value as a Python literal, so lowercase false is a truthy string, and
// False makes every record with a DataCite DOI return a 500.)
//
// @param ctx {context.Context} cancels the check
// @param ex {executor.Executor} reaches the VM
// @param envGlobs {[]string} absolute path patterns of the RDM .env files
// @param hostsFile {string} the absolute path of the hosts file, normally /etc/hosts
// @returns {Finding} the result, naming what is not safe
// @returns {error} an unsafe path, or why the VM could not be reached
// @example
//
//	f, err := CheckDataCite(ctx, ex, []string{"/Sites/*/.env"}, "/etc/hosts")
func CheckDataCite(ctx context.Context, ex executor.Executor, envGlobs []string, hostsFile string) (Finding, error) {
	const check = "datacite registration blocked"
	words, err := globWords(envGlobs)
	if err != nil {
		return Finding{}, err
	}
	if !safeGlob.MatchString(hostsFile) || strings.ContainsAny(hostsFile, "*") || strings.Contains(hostsFile, "..") {
		return Finding{}, fmt.Errorf("refusing the hosts file path %q", hostsFile)
	}
	script := `for f in ` + words + `; do
  [ -f "$f" ] || continue
  v=$(awk -v q="'" '{ l=$0; sub(/^[ \t]*export[ \t]+/, "", l) } l ~ /^INVENIO_DATACITE_TEST_MODE=/ { v=substr(l, index(l, "=")+1) } END { gsub("^[\"" q " \t]+|[\"" q " \t]+$", "", v); print v }' "$f")
  echo "DC $f [$v]"
done
if [ -f ` + hostsFile + ` ]; then
  for h in ` + strings.Join(dataCiteHosts, " ") + `; do
    if awk -v h="$h" '{ sub(/#.*/, "") } ($1 == "127.0.0.1" || $1 == "0.0.0.0" || $1 == "::1") { for (i = 2; i <= NF; i++) if ($i == h) found = 1 } END { exit !found }' ` + hostsFile + `; then echo "BLOCKED $h"; else echo "OPEN $h"; fi
  done
else
  echo "NOHOSTS"
fi
`
	out, _, err := run(ctx, ex, script)
	if err != nil {
		return Finding{}, err
	}
	var problems []string
	files := 0
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "DC "):
			files++
			rest := strings.TrimPrefix(line, "DC ")
			if i := strings.LastIndex(rest, " ["); i >= 0 && rest[i+2:] != "True]" {
				problems = append(problems, rest[:i]+": INVENIO_DATACITE_TEST_MODE is not set to True")
			}
		case strings.HasPrefix(line, "OPEN "):
			problems = append(problems, strings.TrimPrefix(line, "OPEN ")+" is not blocked in "+hostsFile)
		case line == "NOHOSTS":
			problems = append(problems, hostsFile+" not found, so the DataCite hosts cannot be confirmed blocked")
		}
	}
	if files == 0 {
		problems = append([]string{"no .env file found under " + words + ", so DataCite test mode cannot be confirmed"}, problems...)
	}
	if len(problems) > 0 {
		return Finding{Check: check, Detail: strings.Join(problems, "\n")}, nil
	}
	return Finding{Check: check, OK: true, Detail: "INVENIO_DATACITE_TEST_MODE=True, and " + strings.Join(dataCiteHosts, " and ") + " resolve to loopback"}, nil
}

// SetDataCiteSafeguards sets INVENIO_DATACITE_TEST_MODE=True in every .env file
// matching envGlobs and adds a loopback line to hostsFile for each DataCite API
// host that is not already blocked. Each file it changes is saved once beside
// itself as .clasm-bak, and a later run never replaces that backup. The
// application must be restarted for the setting to take effect.
//
// @param ctx {context.Context} cancels the change
// @param ex {executor.Executor} reaches the VM
// @param envGlobs {[]string} absolute path patterns of the RDM .env files
// @param hostsFile {string} the absolute path of the hosts file
// @returns {error} an unsafe path, no .env file found, or why an edit failed
// @example
//
//	err := SetDataCiteSafeguards(ctx, ex, []string{"/Sites/*/.env"}, "/etc/hosts")
func SetDataCiteSafeguards(ctx context.Context, ex executor.Executor, envGlobs []string, hostsFile string) error {
	words, err := globWords(envGlobs)
	if err != nil {
		return err
	}
	if !safeGlob.MatchString(hostsFile) || strings.ContainsAny(hostsFile, "*") || strings.Contains(hostsFile, "..") {
		return fmt.Errorf("refusing the hosts file path %q", hostsFile)
	}
	script := `set -e
umask 077
n=0
for f in ` + words + `; do
  [ -f "$f" ] || continue
  [ -e "$f.clasm-bak" ] || cp -p "$f" "$f.clasm-bak"
  awk '{ t=$0; sub(/^[ \t]*export[ \t]+/, "", t) } t ~ /^INVENIO_DATACITE_TEST_MODE=/ { if (!done) { print "INVENIO_DATACITE_TEST_MODE=True"; done=1 } next } { print $0 } END { if (!done) print "INVENIO_DATACITE_TEST_MODE=True" }' "$f" > "$f.clasm-tmp"
  cat "$f.clasm-tmp" > "$f"
  rm -f "$f.clasm-tmp"
  n=$((n+1))
done
[ "$n" -gt 0 ] || { echo "no .env file found under ` + words + `"; exit 1; }
H=` + hostsFile + `
[ -f "$H" ] || { echo "$H not found"; exit 1; }
for h in ` + strings.Join(dataCiteHosts, " ") + `; do
  if ! awk -v h="$h" '{ sub(/#.*/, "") } ($1 == "127.0.0.1" || $1 == "0.0.0.0" || $1 == "::1") { for (i = 2; i <= NF; i++) if ($i == h) found = 1 } END { exit !found }' "$H"; then
    [ -e "$H.clasm-bak" ] || cp -p "$H" "$H.clasm-bak"
    printf '127.0.0.1 %s # clasm: no DataCite registration from a development VM\n' "$h" >> "$H"
  fi
done
echo "SET $n"
`
	out, status, err := run(ctx, ex, script)
	if err != nil {
		return err
	}
	if status != executor.StatusSuccess {
		return fmt.Errorf("setting the DataCite safeguards on %s failed (%s): %s", ex.Name(), status, strings.TrimSpace(out))
	}
	return nil
}

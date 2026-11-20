package recipe

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	headerLine  = regexp.MustCompile(`^#\s*([a-z][a-z_-]*):(?:\s+(.*))?$`)
	stepName    = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	stepFile    = regexp.MustCompile(`^(\d{2,})_.+\.sh$`)
	paramName   = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	paramValue  = regexp.MustCompile(`^[A-Za-z0-9._:/@=+,-]*$`)
	knownChange = map[string]bool{"nothing": true, "database": true, "indices": true, "host": true}
)

// ParseStep parses one recipe script's header (DR-0202). The header is the
// leading block of "#" lines after an optional shebang; a blank or non-comment
// line ends it. In it, a line "# name: value" with a lowercase name is a header
// line and must be one of step, changes, timeout, detached or user; any other
// "#" line is prose and ignored. step and changes are required.
//
// @param file {string} the base file name, for messages
// @param script {[]byte} the whole script
// @returns {Step} the step, with defaults filled in
// @returns {error} a *HeaderError when the header is missing or wrong
// @example
//
//	s, err := ParseStep("10_load.sh", []byte("# step: load\n# changes: database\ntrue\n"))
func ParseStep(file string, script []byte) (Step, error) {
	bad := func(format string, a ...any) (Step, error) {
		return Step{}, &HeaderError{File: file, Msg: fmt.Sprintf(format, a...)}
	}
	s := Step{File: file, Timeout: DefaultTimeout, User: "root", Script: script}
	seen := map[string]bool{}
	for i, line := range strings.Split(string(script), "\n") {
		line = strings.TrimRight(line, "\r")
		if i == 0 && strings.HasPrefix(line, "#!") {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			break
		}
		m := headerLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key, value := m[1], strings.TrimSpace(m[2])
		if seen[key] {
			return bad("the header line %q appears twice", key)
		}
		switch key {
		case "step":
			if !stepName.MatchString(value) {
				return bad("step: %q is not a name (letters, digits, - and _)", value)
			}
			s.Name = value
		case "changes":
			changes, err := parseChanges(value)
			if err != nil {
				return bad("changes: %v", err)
			}
			s.Changes = changes
		case "timeout":
			n, err := strconv.Atoi(value)
			if err != nil || n <= 0 {
				return bad("timeout: %q is not a positive number of seconds", value)
			}
			s.Timeout = time.Duration(n) * time.Second
		case "detached":
			switch value {
			case "yes":
				s.Detached = true
			case "no":
			default:
				return bad("detached: %q is not yes or no", value)
			}
		case "user":
			if value != "root" && value != "ubuntu" {
				return bad("user: %q is not root or ubuntu", value)
			}
			s.User = value
		default:
			return bad("unknown header line %q (known: step, changes, timeout, detached, user)", key)
		}
		seen[key] = true
	}
	if !seen["step"] {
		return bad("no \"# step: NAME\" header line")
	}
	if !seen["changes"] {
		return bad("no \"# changes: nothing|database|indices|host\" header line")
	}
	return s, nil
}

// parseChanges reads the comma list of a "changes" header.
func parseChanges(value string) ([]string, error) {
	if value == "" {
		return nil, fmt.Errorf("empty; use nothing, or one or more of database, indices, host")
	}
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if !knownChange[part] {
			return nil, fmt.Errorf("%q is not one of nothing, database, indices, host", part)
		}
		if seen[part] {
			return nil, fmt.Errorf("%q is listed twice", part)
		}
		seen[part] = true
		out = append(out, part)
	}
	if seen["nothing"] && len(out) > 1 {
		return nil, fmt.Errorf("nothing cannot be combined with another value")
	}
	return out, nil
}

// Load reads a recipe directory (DR-0202): the files named NN_name.sh (two or
// more digits) are the steps, run in order of their number and then their name,
// so 100_x follows 20_y. Every other regular file is ignored and returned so a
// mistyped name does not vanish silently. A directory with no step, a step
// with a bad header, or two steps with one name is refused.
//
// @param dir {string} the recipe directory
// @returns {[]Step} the steps in run order
// @returns {[]string} the ignored file names, sorted
// @returns {error} a *HeaderError, or the read error (wrapping fs.ErrNotExist for a missing directory)
// @example
//
//	steps, ignored, err := Load("upgrade/v13-to-v14")
func Load(dir string) ([]Step, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	type numbered struct {
		n    int
		name string
	}
	var files []numbered
	var ignored []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := stepFile.FindStringSubmatch(e.Name())
		if m == nil {
			ignored = append(ignored, e.Name())
			continue
		}
		n, _ := strconv.Atoi(m[1])
		files = append(files, numbered{n, e.Name()})
	}
	sort.Strings(ignored)
	sort.Slice(files, func(i, j int) bool {
		if files[i].n != files[j].n {
			return files[i].n < files[j].n
		}
		return files[i].name < files[j].name
	})
	if len(files) == 0 {
		return nil, nil, &HeaderError{File: dir, Msg: "no step files (NN_name.sh) in the recipe directory"}
	}
	var steps []Step
	names := map[string]string{}
	for _, f := range files {
		script, err := os.ReadFile(dir + string(os.PathSeparator) + f.name)
		if err != nil {
			return nil, nil, err
		}
		s, err := ParseStep(f.name, script)
		if err != nil {
			return nil, nil, err
		}
		if other, dup := names[s.Name]; dup {
			return nil, nil, &HeaderError{File: f.name, Msg: fmt.Sprintf("step name %q is already used by %s", s.Name, other)}
		}
		names[s.Name] = f.name
		steps = append(steps, s)
	}
	return steps, ignored, nil
}

// ParseParams validates NAME=value arguments (DR-0202 point 4): a name of
// capitals, digits and underscores starting with a capital, and a value of
// letters, digits and . _ : / @ = + , - only, so nothing in a value can be read
// as shell. APPLY is the runner's own and a name given twice is refused.
//
// @param args {[]string} the NAME=value words
// @returns {[]Param} the parameters in the order given
// @returns {error} a *UsageError for the first bad one
// @example
//
//	params, err := ParseParams([]string{"SNAP=rdm-20261118", "BUCKET=sql-backups"})
func ParseParams(args []string) ([]Param, error) {
	var out []Param
	seen := map[string]bool{}
	for _, a := range args {
		name, value, ok := strings.Cut(a, "=")
		switch {
		case !ok:
			return nil, &UsageError{Msg: fmt.Sprintf("%q is not NAME=value", a)}
		case !paramName.MatchString(name):
			return nil, &UsageError{Msg: fmt.Sprintf("%q is not a parameter name (capitals, digits and _, starting with a capital)", name)}
		case name == "APPLY":
			return nil, &UsageError{Msg: "APPLY is set by --apply; it cannot be passed as a parameter"}
		case !paramValue.MatchString(value):
			return nil, &UsageError{Msg: fmt.Sprintf("the value of %s has characters outside letters, digits and . _ : / @ = + , -", name)}
		case seen[name]:
			return nil, &UsageError{Msg: fmt.Sprintf("%s is given more than once", name)}
		}
		seen[name] = true
		out = append(out, Param{name, value})
	}
	return out, nil
}

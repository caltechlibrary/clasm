package recipe

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParseStep_FullHeader(t *testing.T) {
	src := "#!/bin/bash\n# step: load-dump\n# changes: database,indices\n# timeout: 3000\n# detached: yes\n# user: ubuntu\necho hi\n"
	s, err := ParseStep("10_load.sh", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Step{File: "10_load.sh", Name: "load-dump", Changes: []string{"database", "indices"}, Timeout: 3000 * time.Second, Detached: true, User: "ubuntu", Script: []byte(src)}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("got %+v\nwant %+v", s, want)
	}
	if !s.Gated() {
		t.Error("database,indices is gated")
	}
}

func TestParseStep_Defaults(t *testing.T) {
	s, err := ParseStep("00_pre.sh", []byte("# step: pre\n# changes: nothing\ntrue\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Timeout != DefaultTimeout || s.Detached || s.User != "root" || s.Gated() {
		t.Errorf("defaults wrong: %+v", s)
	}
}

func TestParseStep_Refusals(t *testing.T) {
	cases := map[string]string{
		"no header at all":      "echo hi\n",
		"empty file":            "",
		"no step":               "# changes: nothing\n",
		"no changes":            "# step: a\n",
		"unknown header":        "# step: a\n# changes: nothing\n# colour: red\n",
		"duplicate header":      "# step: a\n# step: b\n# changes: nothing\n",
		"bad step name":         "# step: has space\n# changes: nothing\n",
		"empty step name":       "# step:\n# changes: nothing\n",
		"unknown change":        "# step: a\n# changes: network\n",
		"nothing with another":  "# step: a\n# changes: nothing,database\n",
		"empty changes":         "# step: a\n# changes:\n",
		"timeout not a number":  "# step: a\n# changes: nothing\n# timeout: soon\n",
		"timeout zero":          "# step: a\n# changes: nothing\n# timeout: 0\n",
		"timeout negative":      "# step: a\n# changes: nothing\n# timeout: -5\n",
		"detached maybe":        "# step: a\n# changes: nothing\n# detached: maybe\n",
		"user other":            "# step: a\n# changes: nothing\n# user: bob\n",
		"uppercase header name": "# Step: a\n# changes: nothing\n",
		"repeated change value": "# step: a\n# changes: database,database\n",
	}
	for name, src := range cases {
		_, err := ParseStep("x.sh", []byte(src))
		var he *HeaderError
		if !errors.As(err, &he) {
			t.Errorf("%s: want a *HeaderError, got %v", name, err)
		} else if he.File != "x.sh" {
			t.Errorf("%s: the error must name the file, got %q", name, he.File)
		}
	}
}

// The header is the leading comment block; a "# step:" later in the body is
// the script's own text and neither counts nor trips the duplicate check.
func TestParseStep_HeaderEndsAtTheFirstNonCommentLine(t *testing.T) {
	src := "#!/bin/bash\n# step: a\n# changes: nothing\n\n# step: not-a-header\necho hi\n"
	s, err := ParseStep("x.sh", []byte(src))
	if err != nil || s.Name != "a" {
		t.Errorf("got %+v, %v", s, err)
	}
	// A blank line before the header ends it before it starts.
	if _, err := ParseStep("x.sh", []byte("\n# step: a\n# changes: nothing\n")); err == nil {
		t.Error("a header after a blank line is not a header")
	}
}

func TestParseStep_PlainCommentsInTheHeaderBlockAreAllowed(t *testing.T) {
	src := "#!/bin/bash\n# Load the dump into the database.\n#\n# step: a\n# changes: nothing\ntrue\n"
	if _, err := ParseStep("x.sh", []byte(src)); err != nil {
		t.Errorf("prose comments beside the header are fine: %v", err)
	}
}

func writeRecipe(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func hdr(name, changes string) string {
	return "#!/bin/bash\n# step: " + name + "\n# changes: " + changes + "\necho " + name + "\n"
}

func TestLoad_OrdersByNameAndIgnoresOtherFiles(t *testing.T) {
	dir := writeRecipe(t, map[string]string{
		"20_b.sh": hdr("b", "nothing"), "00_a.sh": hdr("a", "nothing"), "100_z.sh": hdr("z", "nothing"), "10_m.sh": hdr("m", "nothing"),
		"README.md": "notes", "helper.sh": "echo x", "5_short.sh": hdr("s", "nothing"), "30_c.txt": "x",
	})
	steps, ignored, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The order is by the number, then the rest of the name: 100 follows 20, as a
	// person numbering the steps means it (byte order would put 100_z before 10_m).
	want := []string{"00_a.sh", "10_m.sh", "20_b.sh", "100_z.sh"}
	var got []string
	for _, s := range steps {
		got = append(got, s.File)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	wantIgnored := []string{"30_c.txt", "5_short.sh", "README.md", "helper.sh"}
	if !reflect.DeepEqual(ignored, wantIgnored) {
		t.Errorf("ignored = %v, want %v", ignored, wantIgnored)
	}
}

func TestLoad_Refusals(t *testing.T) {
	cases := map[string]map[string]string{
		"no steps":             {"README.md": "x"},
		"a header-less script": {"00_a.sh": hdr("a", "nothing"), "10_b.sh": "echo no header\n"},
		"duplicate step names": {"00_a.sh": hdr("same", "nothing"), "10_b.sh": hdr("same", "nothing")},
	}
	for name, files := range cases {
		if _, _, err := Load(writeRecipe(t, files)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, _, err := Load(filepath.Join(t.TempDir(), "absent")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing directory must wrap fs.ErrNotExist, got %v", err)
	}
	var he *HeaderError
	_, _, err := Load(writeRecipe(t, map[string]string{"00_a.sh": "echo x\n"}))
	if !errors.As(err, &he) {
		t.Errorf("a header problem is a *HeaderError, got %v", err)
	}
}

func TestParseParams(t *testing.T) {
	got, err := ParseParams([]string{"SNAP=rdm-20261118-133546", "BUCKET=sql-backups.library.caltech.edu", "EMPTY=", "A_1=x/y:z@w=v+1,2-3"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Param{{"SNAP", "rdm-20261118-133546"}, {"BUCKET", "sql-backups.library.caltech.edu"}, {"EMPTY", ""}, {"A_1", "x/y:z@w=v+1,2-3"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
	for _, bad := range []string{"noequals", "lower=x", "1ABC=x", "=x", "A=has space", "A=semi;colon", "A=$(id)", "A=`id`", "A=quote'", "A=dq\"", "A=new\nline", "A=back\\slash", "A=star*", "APPLY=yes", "A=1,A=2"} {
		args := []string{bad}
		if bad == "A=1,A=2" {
			args = []string{"A=1", "A=2"}
		}
		_, err := ParseParams(args)
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Errorf("%q: want a *UsageError, got %v", bad, err)
		}
	}
}

// Sub-directories (even one named like a step) are not steps and are not an error.
func TestLoad_SubdirectoriesAreSkipped(t *testing.T) {
	dir := writeRecipe(t, map[string]string{"00_a.sh": hdr("a", "nothing")})
	for _, d := range []string{"50_sub.sh", "notes"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	steps, ignored, err := Load(dir)
	if err != nil || len(steps) != 1 || len(ignored) != 0 {
		t.Errorf("steps=%d ignored=%v err=%v", len(steps), ignored, err)
	}
}

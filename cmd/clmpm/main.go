// Command clmpm manages Multipass instances for rehearsing Invenio RDM
// upgrades and loading production data: the Multipass counterpart of clasm
// (DR-0201). It never imports the AWS workflow code.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/caltechlibrary/clasm"
	"github.com/caltechlibrary/clasm/internal/config"
	"github.com/caltechlibrary/clasm/internal/executor"
	"github.com/caltechlibrary/clasm/internal/multipass"
)

// Exit codes (workspace DR-0014). clmpm uses this subset.
const (
	exitOK          = 0
	exitNegative    = 1  // the answer is no: no such instance, or the state forbids it
	exitUsage       = 2  // the command line is wrong
	exitNoInput     = 66 // a named input file is missing
	exitUnavailable = 69 // multipass cannot be run
	exitConfig      = 78 // a configuration file is present but wrong
	exitInternal    = 70 // a bug, or an error nothing classified
	exitTempFail    = 75 // retrying later may succeed (a wait timed out)
)

// deps is what the command needs from outside, so a test can supply a fake
// multipass and a fake host.
type deps struct {
	ctx         context.Context
	client      *multipass.Client
	newExecutor func(vm string) executor.Executor
}

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// classify maps an error to its DR-0014 exit code and class name.
func classify(err error) (int, string) {
	var u *usageError
	switch {
	case errors.As(err, &u):
		return exitUsage, "usage"
	case errors.Is(err, fs.ErrNotExist):
		return exitNoInput, "no_input"
	case errors.Is(err, config.ErrNewerConfig), errors.Is(err, config.ErrInvalidConfig):
		return exitConfig, "config"
	case errors.Is(err, multipass.ErrUnavailable):
		return exitUnavailable, "unavailable"
	case errors.Is(err, multipass.ErrWaitTimeout):
		return exitTempFail, "temp_fail"
	case errors.Is(err, multipass.ErrNotFound), errors.Is(err, multipass.ErrFailed),
		errors.Is(err, multipass.ErrCloudInit), errors.Is(err, context.Canceled):
		return exitNegative, "negative"
	}
	return exitInternal, "internal"
}

const verbList = `  list-instances                    show every instance
  show-instance-detail NAME         show one instance's state and resources
  launch-instance-from-cloud-init NAME CLOUD-INIT-FILE
                                    create an instance (-cpus -memory -disk -image)
  start-instance NAME               start a stopped instance
  stop-instance NAME                stop a running instance
  delete-instance NAME              dry run; -confirm deletes, -purge removes for good
  clone-instance SOURCE NAME        copy a stopped instance
  snapshot-instance NAME SNAPSHOT   snapshot a stopped instance (-comment)
  restore-snapshot NAME SNAPSHOT    dry run; -confirm discards current state and restores
  wait-for-cloud-init NAME          wait until cloud-init finishes (-wait -poll)`

const helpText = `{app_name} {version} -- manage Multipass instances for RDM rehearsals

USAGE

  {app_name} VERB [-text|-json] [OPTIONS] [ARGUMENTS]
  {app_name} -help | -version | -license

VERBS (experimental until 1.0, like clasm's forms: names and output may change)

%s

Launching defaults to Ubuntu 24.04, which the RDM cloud-init files target.
Options go before the arguments. A verb that changes or discards state is a dry
run until it is given -confirm.

EXIT STATUS

  0 ok    1 no such instance, or the state forbids it    2 usage
  66 a named file is missing    69 multipass cannot be run
  70 internal error    75 a wait timed out (retrying may succeed)
`

func main() {
	config.Writer = path.Base(os.Args[0]) + " " + clasm.Version
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	d := deps{
		ctx:         ctx,
		client:      multipass.New(),
		newExecutor: func(vm string) executor.Executor { return executor.NewMultipassExecutor(vm) },
	}
	stop2 := run(os.Args[1:], os.Stdout, os.Stderr, d)
	stop()
	os.Exit(stop2)
}

// run executes one clmpm command line and returns its exit code.
func run(args []string, stdout, stderr io.Writer, d deps) int {
	if d.ctx == nil {
		d.ctx = context.Background()
	}
	appName := path.Base(os.Args[0])
	if len(args) == 0 {
		fmt.Fprintf(stderr, "%s: a verb is required\n\n%s\n", appName, verbList)
		return exitUsage
	}
	switch args[0] {
	case "-help", "--help", "-h", "help":
		fmt.Fprintf(stdout, "%s\n", clasm.FmtHelp(fmt.Sprintf(helpText, verbList), appName, clasm.Version, clasm.ReleaseDate, clasm.ReleaseHash))
		return exitOK
	case "-version", "--version":
		fmt.Fprintf(stdout, "%s %s %s\n", appName, clasm.Version, clasm.ReleaseHash)
		return exitOK
	case "-license", "--license":
		fmt.Fprintf(stdout, "%s\n", clasm.LicenseText)
		return exitOK
	}
	verb, rest := args[0], args[1:]
	h, ok := verbs[verb]
	if !ok {
		fmt.Fprintf(stderr, "%s: unknown verb %q\n\n%s\n", appName, verb, verbList)
		return exitUsage
	}
	fsl := flag.NewFlagSet(verb, flag.ContinueOnError)
	fsl.SetOutput(io.Discard)
	asJSON := fsl.Bool("json", false, "print JSON")
	fsl.Bool("text", false, "print text (the default)")
	o := &options{}
	if h.flags != nil {
		h.flags(fsl, o)
	}
	err := fsl.Parse(rest)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintf(stdout, "usage: %s %s [-text|-json] %s\n", appName, verb, h.usage)
		return exitOK
	}
	if err == nil && (fsl.NArg() < h.minArgs || fsl.NArg() > h.maxArgs) {
		err = &usageError{fmt.Sprintf("usage: %s %s [-text|-json] %s", appName, verb, h.usage)}
	} else if err != nil {
		err = &usageError{fmt.Sprintf("%v\nusage: %s %s [-text|-json] %s", err, appName, verb, h.usage)}
	}
	if err == nil {
		err = h.do(d, o, fsl.Args(), cmdOut{w: stdout, json: *asJSON})
	}
	if err == nil {
		return exitOK
	}
	code, class := classify(err)
	if *asJSON {
		b, _ := json.Marshal(map[string]any{"error": map[string]any{"code": code, "class": class, "message": err.Error()}})
		fmt.Fprintf(stderr, "%s\n", b)
	} else {
		fmt.Fprintf(stderr, "%s: %v\n", appName, err)
	}
	return code
}

// options holds the flags the verbs share.
type options struct {
	cpus                int
	memory, disk, image string
	comment             string
	confirm, purge      bool
	wait, poll          time.Duration
}

type cmdOut struct {
	w    io.Writer
	json bool
}

func (o cmdOut) printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(o.w, "%s\n", b)
	return err
}

// done reports a finished action: a line of text, or a JSON object.
func (o cmdOut) done(action, instance, text string) error {
	if o.json {
		return o.printJSON(map[string]any{"action": action, "instance": instance, "result": "ok"})
	}
	_, err := fmt.Fprintln(o.w, text)
	return err
}

type verbDef struct {
	usage            string
	minArgs, maxArgs int
	flags            func(fs *flag.FlagSet, o *options)
	do               func(d deps, o *options, args []string, out cmdOut) error
}

var verbs map[string]verbDef

func init() {
	verbs = map[string]verbDef{
		"list-instances":                  {usage: "", do: doList},
		"show-instance-detail":            {usage: "NAME", minArgs: 1, maxArgs: 1, do: doDetail},
		"launch-instance-from-cloud-init": {usage: "[-cpus N] [-memory SIZE] [-disk SIZE] [-image NAME] NAME CLOUD-INIT-FILE", minArgs: 2, maxArgs: 2, flags: launchFlags, do: doLaunch},
		"start-instance":                  {usage: "NAME", minArgs: 1, maxArgs: 1, do: doStart},
		"stop-instance":                   {usage: "NAME", minArgs: 1, maxArgs: 1, do: doStop},
		"delete-instance":                 {usage: "[-confirm] [-purge] NAME", minArgs: 1, maxArgs: 1, flags: deleteFlags, do: doDelete},
		"clone-instance":                  {usage: "SOURCE NAME", minArgs: 2, maxArgs: 2, do: doClone},
		"snapshot-instance":               {usage: "[-comment TEXT] NAME SNAPSHOT", minArgs: 2, maxArgs: 2, flags: snapshotFlags, do: doSnapshot},
		"restore-snapshot":                {usage: "[-confirm] NAME SNAPSHOT", minArgs: 2, maxArgs: 2, flags: confirmFlag, do: doRestore},
		"wait-for-cloud-init":             {usage: "[-wait DURATION] [-poll DURATION] NAME", minArgs: 1, maxArgs: 1, flags: waitFlags, do: doWait},
	}
	// list-instances takes no arguments: minArgs and maxArgs are already 0.
}

func launchFlags(fs *flag.FlagSet, o *options) {
	fs.IntVar(&o.cpus, "cpus", 0, "number of CPUs")
	fs.StringVar(&o.memory, "memory", "", "memory, such as 8G")
	fs.StringVar(&o.disk, "disk", "", "disk size, such as 80G")
	fs.StringVar(&o.image, "image", "24.04", "image; the RDM cloud-init files target Ubuntu 24.04")
}
func deleteFlags(fs *flag.FlagSet, o *options) {
	fs.BoolVar(&o.confirm, "confirm", false, "do it; without this the command is a dry run")
	fs.BoolVar(&o.purge, "purge", false, "remove the instance permanently")
}
func confirmFlag(fs *flag.FlagSet, o *options) {
	fs.BoolVar(&o.confirm, "confirm", false, "do it; without this the command is a dry run")
}
func snapshotFlags(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.comment, "comment", "", "a comment stored with the snapshot")
}
func waitFlags(fs *flag.FlagSet, o *options) {
	fs.DurationVar(&o.wait, "wait", 20*time.Minute, "how long to wait in all")
	fs.DurationVar(&o.poll, "poll", 10*time.Second, "pause between checks")
}

func doList(d deps, o *options, args []string, out cmdOut) error {
	instances, err := d.client.List(d.ctx)
	if err != nil {
		return err
	}
	if out.json {
		return out.printJSON(instances)
	}
	tw := tabwriter.NewWriter(out.w, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATE\tIPV4\tRELEASE")
	for _, i := range instances {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", i.Name, i.State, strings.Join(i.IPv4, ","), i.Release)
	}
	return tw.Flush()
}

func gib(b int64) string { return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30)) }

func doDetail(d deps, o *options, args []string, out cmdOut) error {
	det, err := d.client.Info(d.ctx, args[0])
	if err != nil {
		return err
	}
	if out.json {
		return out.printJSON(det)
	}
	fmt.Fprintf(out.w, "name:      %s\nstate:     %s\nrelease:   %s\nipv4:      %s\ncpus:      %d\nmemory:    %s of %s\ndisk:      %s of %s\nsnapshots: %d\n",
		det.Name, det.State, det.Release, strings.Join(det.IPv4, ","), det.CPUs,
		gib(det.MemoryUsed), gib(det.MemoryTotal), gib(det.DiskUsed), gib(det.DiskTotal), det.Snapshots)
	return nil
}

func doLaunch(d deps, o *options, args []string, out cmdOut) error {
	err := d.client.Launch(d.ctx, multipass.LaunchOptions{Name: args[0], CloudInit: args[1], CPUs: o.cpus, Memory: o.memory, Disk: o.disk, Image: o.image})
	if err != nil {
		return err
	}
	return out.done("launch-instance-from-cloud-init", args[0], fmt.Sprintf("launched %s; run `clmpm wait-for-cloud-init %s` to wait for cloud-init", args[0], args[0]))
}

func doStart(d deps, o *options, args []string, out cmdOut) error {
	if err := d.client.Start(d.ctx, args[0]); err != nil {
		return err
	}
	return out.done("start-instance", args[0], "started "+args[0])
}

func doStop(d deps, o *options, args []string, out cmdOut) error {
	if err := d.client.Stop(d.ctx, args[0]); err != nil {
		return err
	}
	return out.done("stop-instance", args[0], "stopped "+args[0])
}

func doClone(d deps, o *options, args []string, out cmdOut) error {
	if err := d.client.Clone(d.ctx, args[0], args[1]); err != nil {
		return err
	}
	return out.done("clone-instance", args[1], fmt.Sprintf("cloned %s to %s", args[0], args[1]))
}

func doSnapshot(d deps, o *options, args []string, out cmdOut) error {
	if err := d.client.Snapshot(d.ctx, args[0], args[1], o.comment); err != nil {
		return err
	}
	return out.done("snapshot-instance", args[0], fmt.Sprintf("snapshot %s taken of %s", args[1], args[0]))
}

// dryRun prints what a confirmed run would do, as text or JSON.
func (o cmdOut) dryRun(action, instance, text string) error {
	if o.json {
		return o.printJSON(map[string]any{"action": action, "instance": instance, "dry_run": true})
	}
	_, err := fmt.Fprintln(o.w, "Dry run: "+text+" Nothing was changed; add -confirm to do it.")
	return err
}

func doDelete(d deps, o *options, args []string, out cmdOut) error {
	name := args[0]
	if !o.confirm {
		det, err := d.client.Info(d.ctx, name)
		if err != nil {
			return err
		}
		how := "would delete"
		if o.purge {
			how = "would delete and purge"
		}
		return out.dryRun("delete-instance", name, fmt.Sprintf("%s %s (%s, snapshots: %d).", how, name, det.State, det.Snapshots))
	}
	if err := d.client.Delete(d.ctx, name, o.purge); err != nil {
		return err
	}
	return out.done("delete-instance", name, "deleted "+name)
}

func doRestore(d deps, o *options, args []string, out cmdOut) error {
	name, snap := args[0], args[1]
	if !o.confirm {
		return out.dryRun("restore-snapshot", name, fmt.Sprintf("would discard %s's current state and restore snapshot %s.", name, snap))
	}
	if err := d.client.Restore(d.ctx, name, snap, true); err != nil {
		return err
	}
	return out.done("restore-snapshot", name, fmt.Sprintf("restored %s to snapshot %s", name, snap))
}

func doWait(d deps, o *options, args []string, out cmdOut) error {
	res, err := multipass.WaitForCloudInit(d.ctx, d.newExecutor(args[0]), o.wait, o.poll)
	if err != nil {
		return err
	}
	if out.json {
		return out.printJSON(map[string]any{"instance": args[0], "status": res.Status, "errors": res.Errors})
	}
	fmt.Fprintf(out.w, "cloud-init on %s: %s\n", args[0], res.Status)
	return nil
}

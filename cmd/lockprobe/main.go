// Command lockprobe checks whether a directory is safe to keep an
// embedded database (SQLite, BoltDB, LevelDB, ...) on when more than one
// process or host opens it.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/DushaneW/lockprobe/internal/probe"
)

var version = "dev"

const usage = `lockprobe: test file locking and coherence of a directory

usage:
  lockprobe check DIR [DIR2]      test this host (DIR2: second mount of the same share)
  lockprobe pair DIR              start a two-host test, prints a join command
  lockprobe pair --join ID DIR    join it from the other host
  lockprobe version

exit status: 0 all checks passed, 1 a check failed,
             3 nothing failed but something could not be verified, 2 usage or setup error
`

func main() {
	probe.RunHolderIfRequested()
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, out, errw io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(errw, usage)
		return 2
	}
	switch args[0] {
	case "check":
		return cmdCheck(args[1:], out, errw)
	case "pair":
		return cmdPair(args[1:], out, errw)
	case "version":
		fmt.Fprintln(out, version)
		return 0
	case "-h", "--help", "help":
		fmt.Fprint(out, usage)
		return 0
	default:
		fmt.Fprintf(errw, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func cmdCheck(args []string, out, errw io.Writer) int {
	if len(args) < 1 || len(args) > 2 {
		fmt.Fprint(errw, usage)
		return 2
	}
	a, b := args[0], args[0]
	if len(args) == 2 {
		b = args[1]
	}
	fi, err := probe.DetectFS(a)
	if err != nil {
		fmt.Fprintf(errw, "lockprobe: %v\n", err)
		return 2
	}
	checks, err := probe.LocalChecks(a, b)
	if err != nil {
		fmt.Fprintf(errw, "lockprobe: %v\n", err)
		return 2
	}
	fmt.Fprintf(out, "filesystem: %s (%s)\n\n", fi.Type, fi.Class)
	if fi.Class == probe.FSLocal {
		fmt.Fprintln(out, "This filesystem is attached to this kernel, so only processes on this host share it.")
	} else {
		checks = append(checks, probe.Check{
			Name:   "cross-host",
			Status: probe.Unverified,
			Detail: "this host cannot observe locks taken on other hosts; run `lockprobe pair` on two of them",
		})
	}
	return report(out, checks)
}

func cmdPair(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("pair", flag.ContinueOnError)
	fs.SetOutput(errw)
	join := fs.String("join", "", "session id printed by the first host")
	timeout := fs.Duration("timeout", 2*time.Minute, "how long to wait for each step of the other host")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprint(errw, usage)
		return 2
	}
	dir := fs.Arg(0)

	var p *probe.Pair
	if *join == "" {
		var id string
		var err error
		p, id, err = probe.NewSession(dir)
		if err != nil {
			fmt.Fprintf(errw, "lockprobe: %v\n", err)
			return 2
		}
		fmt.Fprintf(out, "On the other host, run (with its path to this same share):\n\n  lockprobe pair --join %s <path>\n\nwaiting up to %s...\n", id, *timeout)
	} else {
		var err error
		if p, err = probe.Join(dir, *join); err != nil {
			fmt.Fprintf(errw, "lockprobe: %v\n", err)
			return 2
		}
	}
	p.Timeout = *timeout
	p.Log = func(s string) { fmt.Fprintln(out, s) }
	checks := p.Run()
	fmt.Fprintln(out)
	code := report(out, checks)
	fmt.Fprintln(out, "\nNote: SQLite WAL mode needs shared memory between processes and does not work")
	fmt.Fprintln(out, "across hosts on any network filesystem, whatever the result above.")
	return code
}

func report(out io.Writer, checks []probe.Check) int {
	for _, c := range checks {
		fmt.Fprintf(out, "%-10s  %-16s %s\n", c.Status, c.Name, c.Detail)
	}
	fmt.Fprintln(out)
	switch probe.Worst(checks) {
	case probe.Pass:
		fmt.Fprintln(out, "Result: all checks passed. This is evidence for the moment of the test, not a guarantee.")
		return 0
	case probe.Fail:
		fmt.Fprintln(out, "Result: FAILED. Do not let more than one process/host write the same database file here.")
		return 1
	default:
		fmt.Fprintln(out, "Result: nothing failed, but not everything could be verified (see UNVERIFIED above).")
		return 3
	}
}

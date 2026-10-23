package main

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DushaneW/lockprobe/internal/probe"
	"github.com/DushaneW/lockprobe/internal/testfs"
)

// The test binary doubles as the lockprobe binary: lock tests need
// separate processes, because POSIX locks never conflict within one.
func TestMain(m *testing.M) {
	probe.RunHolderIfRequested()
	if os.Getenv("LOCKPROBE_AS_CLI") == "1" {
		os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func cli(args ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "LOCKPROBE_AS_CLI=1")
	return cmd
}

func runCLI(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	cmd := cli(args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), code
}

func wantStatus(t *testing.T, out, check, status string) {
	t.Helper()
	re := regexp.MustCompile(`(?m)^` + status + `\s+` + check + `\s`)
	if !re.MatchString(out) {
		t.Errorf("want %s %s in output:\n%s", status, check, out)
	}
}

func TestCheckLocalDir(t *testing.T) {
	out, code := runCLI(t, "check", t.TempDir())
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, out)
	}
	for _, c := range []string{"write-visibility", "lock-exclusion", "shared-mmap"} {
		wantStatus(t, out, c, "PASS")
	}
}

func TestCheckTwoMountsWithoutSharedLocksFails(t *testing.T) {
	back := t.TempDir()
	a, b := testfs.Mount(t, back, false), testfs.Mount(t, back, false)
	out, code := runCLI(t, "check", a, b)
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	wantStatus(t, out, "lock-exclusion", "FAIL")
}

func TestCheckTwoMountsWithSharedLocksPassesButCrossHostUnverified(t *testing.T) {
	back := t.TempDir()
	a, b := testfs.Mount(t, back, true), testfs.Mount(t, back, true)
	out, code := runCLI(t, "check", a, b)
	wantStatus(t, out, "lock-exclusion", "PASS")
	wantStatus(t, out, "cross-host", "UNVERIFIED")
	if code != 3 {
		t.Fatalf("exit %d, want 3:\n%s", code, out)
	}
}

// The limitation the tool exists to be honest about: one view of a
// broken share looks fine. It must not be reported as a pass.
func TestCheckSingleViewOfBrokenShareIsNotAPass(t *testing.T) {
	back := t.TempDir()
	a := testfs.Mount(t, back, false)
	out, code := runCLI(t, "check", a)
	wantStatus(t, out, "lock-exclusion", "PASS")
	wantStatus(t, out, "cross-host", "UNVERIFIED")
	if code != 3 {
		t.Fatalf("exit %d, want 3:\n%s", code, out)
	}
}

var joinRe = regexp.MustCompile(`--join ([0-9a-f]+)`)

// runPair runs both sides as separate processes on two directories and
// returns their outputs and exit codes.
func runPair(t *testing.T, dirA, dirB string) (outA, outB string, codeA, codeB int) {
	t.Helper()
	a := cli("pair", "--timeout", "20s", dirA)
	pipe, err := a.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	a.Stderr = a.Stdout
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	var bufA strings.Builder
	sc := bufio.NewScanner(pipe)
	id := ""
	for id == "" && sc.Scan() {
		bufA.WriteString(sc.Text() + "\n")
		if m := joinRe.FindStringSubmatch(sc.Text()); m != nil {
			id = m[1]
		}
	}
	if id == "" {
		a.Process.Kill()
		t.Fatalf("no session id in output:\n%s", bufA.String())
	}
	doneA := make(chan int)
	go func() {
		for sc.Scan() {
			bufA.WriteString(sc.Text() + "\n")
		}
		err := a.Wait()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		doneA <- code
	}()
	outB, codeB = runCLI(t, "pair", "--timeout", "20s", "--join", id, dirB)
	select {
	case codeA = <-doneA:
	case <-time.After(60 * time.Second):
		a.Process.Kill()
		t.Fatal("initiator did not finish")
	}
	return bufA.String(), outB, codeA, codeB
}

func TestPairLocalDir(t *testing.T) {
	d := t.TempDir()
	outA, outB, ca, cb := runPair(t, d, d)
	if ca != 0 || cb != 0 {
		t.Fatalf("exit %d/%d, want 0/0\nA:\n%s\nB:\n%s", ca, cb, outA, outB)
	}
	entries, _ := os.ReadDir(d + "/.lockprobe-pair")
	if len(entries) != 0 {
		t.Errorf("session directory not cleaned up: %v", entries)
	}
}

func TestPairBrokenShareFailsOnBothSides(t *testing.T) {
	back := t.TempDir()
	a, b := testfs.Mount(t, back, false), testfs.Mount(t, back, false)
	outA, outB, ca, cb := runPair(t, a, b)
	for _, o := range []string{outA, outB} {
		wantStatus(t, o, "lock-exclusion", "FAIL")
	}
	if ca != 1 || cb != 1 {
		t.Fatalf("exit %d/%d, want 1/1\nA:\n%s\nB:\n%s", ca, cb, outA, outB)
	}
}

func TestPairWorkingShare(t *testing.T) {
	back := t.TempDir()
	a, b := testfs.Mount(t, back, true), testfs.Mount(t, back, true)
	outA, outB, ca, cb := runPair(t, a, b)
	if ca != 0 || cb != 0 {
		t.Fatalf("exit %d/%d, want 0/0\nA:\n%s\nB:\n%s", ca, cb, outA, outB)
	}
}

func TestJoinWithoutSessionTimesOutUnverified(t *testing.T) {
	out, code := runCLI(t, "pair", "--timeout", "1s", "--join", "deadbeef", t.TempDir())
	if code != 3 {
		t.Fatalf("exit %d, want 3:\n%s", code, out)
	}
	wantStatus(t, out, "write-visibility", "UNVERIFIED")
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{}, {"check"}, {"nope"}, {"pair", "--join", "../x", "/tmp"}} {
		if _, code := runCLI(t, args...); code != 2 {
			t.Errorf("args %v: exit %d, want 2", args, code)
		}
	}
}

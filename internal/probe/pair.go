package probe

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Two-host protocol. Both sides talk only through files in a session
// directory on the share under test, so no network port is needed and
// the coordination itself exercises the share. Every message is written
// to a temp name, fsync'd and renamed; the reader polls for it.
//
// Lock test, run twice with the roles swapped:
//
//	holder: lock range        -> "<r>.holding"
//	tester: try same range    -> "<r>.attempt"  refused | acquired | error
//	holder: unlock            -> "<r>.released"
//	tester: retry up to 5s    -> "<r>.after"    acquired | stuck | error

const sessionRoot = ".lockprobe-pair"

// Pair is one side of a two-host session.
type Pair struct {
	dir     string // session directory
	self    string // "a" (initiator) or "b" (joiner)
	peer    string
	Timeout time.Duration // per message
	Log     func(string)
}

// NewSession creates a session under dir and returns the initiator side
// together with the id the other host passes to Join.
func NewSession(dir string) (*Pair, string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return nil, "", err
	}
	id := fmt.Sprintf("%x", b)
	sdir := filepath.Join(dir, sessionRoot, id)
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		return nil, "", fmt.Errorf("create session dir: %w", err)
	}
	if err := writeSynced(filepath.Join(sdir, "lk"), make([]byte, 4096)); err != nil {
		return nil, "", err
	}
	return &Pair{dir: sdir, self: "a", peer: "b", Timeout: 60 * time.Second}, id, nil
}

// Join returns the joining side of an existing session.
func Join(dir, id string) (*Pair, error) {
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return nil, fmt.Errorf("invalid session id %q", id)
	}
	sdir := filepath.Join(dir, sessionRoot, id)
	return &Pair{dir: sdir, self: "b", peer: "a", Timeout: 60 * time.Second}, nil
}

var errTimeout = errors.New("timed out waiting for the other host")

func writeSynced(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (p *Pair) send(name, body string) error {
	return writeSynced(filepath.Join(p.dir, p.self+"."+name), []byte(body))
}

func (p *Pair) recv(name string) (string, error) {
	path := filepath.Join(p.dir, p.peer+"."+name)
	deadline := time.Now().Add(p.Timeout)
	for {
		// Opening the directory forces NFS close-to-open revalidation of
		// its attributes; without it a cached negative lookup can hide a
		// newly created message for up to the client's acdirmax (60s).
		if d, err := os.Open(p.dir); err == nil {
			d.Readdirnames(-1)
			d.Close()
		}
		b, err := os.ReadFile(path)
		if err == nil {
			return string(b), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("%w (%s)", errTimeout, name)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (p *Pair) logf(format string, a ...any) {
	if p.Log != nil {
		p.Log(fmt.Sprintf(format, a...))
	}
}

// Run executes the protocol. It always returns the checks it could
// decide; checks it could not reach are reported as Unverified.
func (p *Pair) Run() []Check {
	vis := p.handshake()
	if vis.Status != Pass {
		return []Check{vis,
			unverified("lock-exclusion", "not run: handshake failed"),
			unverified("lock-release", "not run: handshake failed")}
	}
	p.logf("handshake ok, testing locks")

	// Round 1: a holds, b tests. Round 2: b holds, a tests.
	r1 := p.round("r1", p.self == "a")
	r2 := p.round("r2", p.self == "b")
	checks := []Check{vis,
		combine("lock-exclusion", r1.excl, r2.excl),
		combine("lock-release", r1.release, r2.release)}

	if p.self == "b" {
		p.send("done", "")
	} else if _, err := p.recv("done"); err == nil {
		os.RemoveAll(p.dir)
	}
	return checks
}

func (p *Pair) handshake() Check {
	const name = "write-visibility"
	mine := string(randomToken())
	if p.self == "a" {
		if err := p.send("hello", mine); err != nil {
			return unverified(name, "write: %v", err)
		}
		reply, err := p.recv("hello")
		if err != nil {
			return unverified(name, "%v", err)
		}
		if !strings.HasPrefix(reply, mine+" ") {
			return fail(name, "the other host read a different value than this host wrote")
		}
		return pass(name, "files written on each host read back intact on the other")
	}
	theirs, err := p.recv("hello")
	if err != nil {
		return unverified(name, "no session found (is --join correct and the path the same share?): %v", err)
	}
	if err := p.send("hello", theirs+" "+mine); err != nil {
		return unverified(name, "write: %v", err)
	}
	return pass(name, "files written on each host read back intact on the other")
}

type roundResult struct{ excl, release Check }

// round runs one holder/tester exchange. Both sides return the same
// result: the tester decides it and the holder learns it from messages.
func (p *Pair) round(r string, holder bool) roundResult {
	var res roundResult
	unv := func(format string, a ...any) roundResult {
		c := fmt.Sprintf(format, a...)
		return roundResult{unverified("lock-exclusion", "%s", c), unverified("lock-release", "%s", c)}
	}
	f, err := os.OpenFile(filepath.Join(p.dir, "lk"), os.O_RDWR, 0)
	if err != nil {
		return unv("open lock file: %v", err)
	}
	defer f.Close()

	if holder {
		ok, err := tryLock(f)
		if err != nil || !ok {
			p.send(r+".holding", "error")
			return unv("could not take the lock on this host (err=%v)", err)
		}
		if err := p.send(r+".holding", "ok"); err != nil {
			return unv("%v", err)
		}
		attempt, err := p.recv(r + ".attempt")
		if err != nil {
			return unv("%v", err)
		}
		unlock(f)
		if err := p.send(r+".released", ""); err != nil {
			return unv("%v", err)
		}
		after, err := p.recv(r + ".after")
		if err != nil {
			return roundResult{exclFrom(r, attempt), unverified("lock-release", "%v", err)}
		}
		return roundResult{exclFrom(r, attempt), releaseFrom(r, after)}
	}

	state, err := p.recv(r + ".holding")
	if err != nil {
		return unv("%v", err)
	}
	if state != "ok" {
		return unv("the other host could not take the lock")
	}
	got, err := tryLock(f)
	attempt := "refused"
	switch {
	case err != nil:
		attempt = "error: " + err.Error()
	case got:
		attempt = "acquired"
		unlock(f)
	}
	if err := p.send(r+".attempt", attempt); err != nil {
		return unv("%v", err)
	}
	res.excl = exclFrom(r, attempt)
	if _, err := p.recv(r + ".released"); err != nil {
		res.release = unverified("lock-release", "%v", err)
		return res
	}
	got, err = lockWithin(f, 5*time.Second)
	after := "stuck"
	switch {
	case err != nil:
		after = "error: " + err.Error()
	case got:
		after = "acquired"
		unlock(f)
	}
	p.send(r+".after", after)
	res.release = releaseFrom(r, after)
	return res
}

func dirOf(r string) string {
	if r == "r1" {
		return "host B vs lock held on host A"
	}
	return "host A vs lock held on host B"
}

func exclFrom(r, attempt string) Check {
	switch {
	case attempt == "refused":
		return pass("lock-exclusion", "%s: refused", dirOf(r))
	case attempt == "acquired":
		return fail("lock-exclusion", "%s: acquired anyway, locks are not shared between hosts", dirOf(r))
	default:
		return unverified("lock-exclusion", "%s: %s", dirOf(r), attempt)
	}
}

func releaseFrom(r, after string) Check {
	switch {
	case after == "acquired":
		return pass("lock-release", "%s: acquirable after release", dirOf(r))
	case after == "stuck":
		return fail("lock-release", "%s: still locked 5s after release", dirOf(r))
	default:
		return unverified("lock-release", "%s: %s", dirOf(r), after)
	}
}

// combine merges the two directions of one property into one check.
func combine(name string, a, b Check) Check {
	st := Worst([]Check{a, b})
	return Check{name, st, a.Detail + "; " + b.Detail}
}

package probe

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// holderEnv makes the current executable act as a lock holder instead of
// running normally. POSIX locks never conflict within one process, so
// the conflicting owner has to be a separate process.
const holderEnv = "LOCKPROBE_HOLD"

// RunHolderIfRequested must be called first thing in main (and in
// TestMain). If this process was started as a holder it takes the lock,
// reports "ok", waits for stdin to close, and exits.
func RunHolderIfRequested() {
	path := os.Getenv(holderEnv)
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		os.Exit(1)
	}
	if ok, err := tryLock(f); !ok || err != nil {
		os.Exit(1)
	}
	os.Stdout.WriteString("ok\n")
	io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

// LocalChecks tests what processes on this host see through dirA and
// dirB. They are usually the same directory; passing two mount points of
// one share checks that the two mounts agree with each other.
func LocalChecks(dirA, dirB string) ([]Check, error) {
	name := fmt.Sprintf(".lockprobe-%d", os.Getpid())
	fa, fb := filepath.Join(dirA, name), filepath.Join(dirB, name)
	if err := os.WriteFile(fa, make([]byte, 4096), 0o600); err != nil {
		return nil, fmt.Errorf("create scratch file: %w", err)
	}
	defer os.Remove(fa)
	return []Check{checkVisibility(fa, fb), checkLock(fa, fb), checkMmap(fa, fb)}, nil
}

func randomToken() []byte {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return []byte(fmt.Sprintf("%x", b))
}

func checkVisibility(fa, fb string) Check {
	const name = "write-visibility"
	want := randomToken()
	f, err := os.OpenFile(fa, os.O_WRONLY, 0)
	if err != nil {
		return unverified(name, "open: %v", err)
	}
	_, err = f.WriteAt(want, 0)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return unverified(name, "write+fsync: %v", err)
	}
	g, err := os.Open(fb)
	if err != nil {
		return unverified(name, "reopen: %v", err)
	}
	defer g.Close()
	got := make([]byte, len(want))
	if _, err := g.ReadAt(got, 0); err != nil {
		return unverified(name, "read: %v", err)
	}
	if !bytes.Equal(got, want) {
		return fail(name, "fsync'd write not visible to a fresh open")
	}
	return pass(name, "fsync'd write visible to a fresh open")
}

func checkLock(fa, fb string) Check {
	const name = "lock-exclusion"
	exe, err := os.Executable()
	if err != nil {
		return unverified(name, "find own executable: %v", err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), holderEnv+"="+fa)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return unverified(name, "%v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return unverified(name, "%v", err)
	}
	if err := cmd.Start(); err != nil {
		return unverified(name, "start holder: %v", err)
	}
	released := false
	release := func() {
		if !released {
			stdin.Close()
			cmd.Wait()
			released = true
		}
	}
	defer release()

	buf := make([]byte, 3)
	if _, err := io.ReadFull(stdout, buf); err != nil || string(buf) != "ok\n" {
		return unverified(name, "holder process could not take a write lock")
	}
	f, err := os.OpenFile(fb, os.O_RDWR, 0)
	if err != nil {
		return unverified(name, "open: %v", err)
	}
	defer f.Close()
	got, err := tryLock(f)
	if err != nil {
		return unverified(name, "F_SETLK: %v", err)
	}
	if got {
		return fail(name, "acquired a write lock another process already holds: locks are not enforced")
	}
	release()
	got, err = lockWithin(f, 3*time.Second)
	if err != nil {
		return unverified(name, "F_SETLK after release: %v", err)
	}
	if !got {
		return fail(name, "lock still held 3s after its owner exited")
	}
	return pass(name, "conflicting lock refused; lock released when owner exited")
}

// checkMmap mirrors SQLite's WAL index (the -shm file): a MAP_SHARED
// region that both mappings must observe with no syscalls in between.
func checkMmap(fa, fb string) Check {
	const name = "shared-mmap"
	ma, closeA, err := mapFile(fa)
	if err != nil {
		return unverified(name, "%v", err)
	}
	defer closeA()
	mb, closeB, err := mapFile(fb)
	if err != nil {
		return unverified(name, "%v", err)
	}
	defer closeB()
	token := randomToken()
	copy(ma[2048:], token)
	deadline := time.Now().Add(time.Second)
	for !bytes.Equal(mb[2048:2048+len(token)], token) {
		if time.Now().After(deadline) {
			return fail(name, "store through one mapping not visible through the other within 1s")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return pass(name, "shared mappings coherent")
}

func mapFile(path string) ([]byte, func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	m, err := unix.Mmap(int(f.Fd()), 0, 4096, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("mmap: %w", err)
	}
	return m, func() { unix.Munmap(m); f.Close() }, nil
}

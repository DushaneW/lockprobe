package probe

import (
	"errors"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// The byte range locked by the probes. Arbitrary, but kept away from
// offset 0 so the range does not overlap data the checks write.
const (
	lockStart = 1024
	lockLen   = 512
)

// tryLock attempts a non-blocking POSIX write lock on the probe range.
// It returns (false, nil) when another owner holds a conflicting lock.
// Any other error means the filesystem did not answer the question.
func tryLock(f *os.File) (bool, error) {
	lk := unix.Flock_t{Type: unix.F_WRLCK, Whence: 0, Start: lockStart, Len: lockLen}
	err := unix.FcntlFlock(f.Fd(), unix.F_SETLK, &lk)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EACCES) {
		return false, nil
	}
	return false, err
}

func unlock(f *os.File) error {
	lk := unix.Flock_t{Type: unix.F_UNLCK, Whence: 0, Start: lockStart, Len: lockLen}
	return unix.FcntlFlock(f.Fd(), unix.F_SETLK, &lk)
}

// lockWithin retries tryLock until it succeeds or d elapses. Used after
// the other side released, to catch locks that are never dropped.
func lockWithin(f *os.File, d time.Duration) (bool, error) {
	deadline := time.Now().Add(d)
	for {
		ok, err := tryLock(f)
		if ok || err != nil {
			return ok, err
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

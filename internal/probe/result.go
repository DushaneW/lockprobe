// Package probe tests whether a directory's filesystem gives separate
// processes, and separate hosts, the guarantees embedded databases such
// as SQLite rely on: visible writes, POSIX advisory lock exclusion and
// coherent shared memory maps.
package probe

import "fmt"

// Status of one check. Unverified is never rounded up to Pass: a check
// that could not run, or whose answer depends on hosts this process
// cannot see, says so.
type Status int

const (
	Pass Status = iota
	Fail
	Unverified
)

func (s Status) String() string {
	switch s {
	case Pass:
		return "PASS"
	case Fail:
		return "FAIL"
	default:
		return "UNVERIFIED"
	}
}

// Check is the outcome of one property test.
type Check struct {
	Name   string
	Status Status
	Detail string
}

func pass(name, format string, a ...any) Check {
	return Check{name, Pass, fmt.Sprintf(format, a...)}
}

func fail(name, format string, a ...any) Check {
	return Check{name, Fail, fmt.Sprintf(format, a...)}
}

func unverified(name, format string, a ...any) Check {
	return Check{name, Unverified, fmt.Sprintf(format, a...)}
}

// Worst returns Fail if any check failed, else Unverified if any could
// not be verified, else Pass.
func Worst(checks []Check) Status {
	w := Pass
	for _, c := range checks {
		if c.Status == Fail {
			return Fail
		}
		if c.Status == Unverified {
			w = Unverified
		}
	}
	return w
}

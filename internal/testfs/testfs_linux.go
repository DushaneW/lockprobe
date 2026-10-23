// Package testfs mounts loopback FUSE views of a directory for tests.
//
// Two mounts of one backing directory model two hosts on a shared
// filesystem. With shareLocks=false each mount keeps POSIX locks in its
// own kernel lock table, reproducing NFS without working lockd
// (`nolock`, `local_lock=all`) and similar broken setups. With
// shareLocks=true locks are forwarded to the backing filesystem, so the
// two mounts agree, like a correctly configured share.
package testfs

import (
	"os"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Mount mounts a loopback view of backing at a fresh temp directory and
// returns its path. The test is skipped when FUSE is unavailable.
func Mount(t *testing.T, backing string, shareLocks bool) string {
	t.Helper()
	// CI sets LOCKPROBE_REQUIRE_FUSE so a broken FUSE setup fails the
	// build instead of silently skipping the tests that matter most.
	skip := t.Skipf
	if os.Getenv("LOCKPROBE_REQUIRE_FUSE") != "" {
		skip = t.Fatalf
	}
	if _, err := os.Stat("/dev/fuse"); err != nil {
		skip("FUSE not available: %v", err)
	}
	root, err := fs.NewLoopbackRoot(backing)
	if err != nil {
		t.Fatal(err)
	}
	mnt := t.TempDir()
	sec := time.Second
	srv, err := fs.Mount(mnt, root, &fs.Options{
		AttrTimeout:  &sec,
		EntryTimeout: &sec,
		MountOptions: fuse.MountOptions{EnableLocks: shareLocks, FsName: "lockprobe-test"},
	})
	if err != nil {
		skip("FUSE mount failed (no permission in this environment?): %v", err)
	}
	t.Cleanup(func() {
		if err := srv.Unmount(); err != nil {
			t.Errorf("unmount %s: %v", mnt, err)
		}
	})
	return mnt
}

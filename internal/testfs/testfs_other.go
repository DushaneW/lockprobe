//go:build !linux

package testfs

import "testing"

// Mount is only implemented on Linux; elsewhere FUSE-backed tests skip.
func Mount(t *testing.T, backing string, shareLocks bool) string {
	t.Skip("loopback FUSE test mounts are Linux-only")
	return ""
}

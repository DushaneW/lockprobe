package probe

import "golang.org/x/sys/unix"

// DetectFS reports the filesystem holding dir.
func DetectFS(dir string) (FSInfo, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return FSInfo{}, err
	}
	name := unix.ByteSliceToString(st.Fstypename[:])
	switch {
	case name == "apfs" || name == "hfs" || name == "msdos" || name == "exfat":
		return FSInfo{name, FSLocal}, nil
	case name == "nfs" || name == "smbfs" || name == "afpfs" || name == "webdav":
		return FSInfo{name, FSNetwork}, nil
	default: // includes macfuse/osxfuse
		return FSInfo{name, FSUnknown}, nil
	}
}

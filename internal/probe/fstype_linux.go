package probe

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// Magic numbers from linux/magic.h and the respective filesystems.
var linuxFS = map[int64]FSInfo{
	0xEF53:     {"ext2/3/4", FSLocal},
	0x58465342: {"xfs", FSLocal},
	0x9123683E: {"btrfs", FSLocal},
	0x2FC12FC1: {"zfs", FSLocal},
	0xF2F52010: {"f2fs", FSLocal},
	0x01021994: {"tmpfs", FSLocal},
	0x858458F6: {"ramfs", FSLocal},
	0x794C7630: {"overlay", FSLocal},
	0x4D44:     {"vfat", FSLocal},
	0x2011BAB0: {"exfat", FSLocal},
	0x5346544E: {"ntfs", FSLocal},
	0x7366746E: {"ntfs3", FSLocal},
	0x6969:     {"nfs", FSNetwork},
	0xFF534D42: {"cifs", FSNetwork},
	0xFE534D42: {"smb2", FSNetwork},
	0x00C36400: {"ceph", FSNetwork},
	0x6B414653: {"afs", FSNetwork},
	0x47504653: {"gpfs", FSNetwork},
	0x0BD00BD0: {"lustre", FSNetwork},
	0x01021997: {"9p", FSHostShare},
	0x65735546: {"fuse", FSUnknown},
}

// DetectFS reports the filesystem holding dir.
func DetectFS(dir string) (FSInfo, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return FSInfo{}, err
	}
	if fi, ok := linuxFS[int64(st.Type)]; ok {
		return fi, nil
	}
	return FSInfo{Type: fmt.Sprintf("0x%X", uint64(st.Type)), Class: FSUnknown}, nil
}

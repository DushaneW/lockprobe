package probe

// FSClass says what one host can learn about a directory on its own.
type FSClass int

const (
	// FSLocal is a block or memory filesystem attached to this kernel.
	// Every process that sees it shares one lock table and page cache.
	FSLocal FSClass = iota
	// FSNetwork is a filesystem whose locks and caches are coordinated
	// over a network protocol (NFS, SMB, Ceph, ...).
	FSNetwork
	// FSHostShare is a share from a hypervisor or container host
	// (9p, virtiofs, Docker Desktop file sharing, WSL drvfs).
	FSHostShare
	// FSUnknown covers FUSE and anything not recognised: the backing
	// store could be local, remote, or a sync service.
	FSUnknown
)

func (c FSClass) String() string {
	switch c {
	case FSLocal:
		return "local"
	case FSNetwork:
		return "network"
	case FSHostShare:
		return "host share"
	default:
		return "unknown"
	}
}

// FSInfo is the filesystem a directory lives on.
type FSInfo struct {
	Type  string
	Class FSClass
}

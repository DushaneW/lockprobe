# lockprobe

Test whether a shared directory (NFS, SMB, FUSE, a NAS or cluster volume) actually enforces file locks between hosts, before you put an SQLite database on it.

SQLite, BoltDB, LevelDB and similar embedded databases rely on POSIX advisory locks to keep two writers apart.
On a share where those locks are not propagated, each writer believes it holds the lock.
Commits are then lost silently: no error, and `PRAGMA integrity_check` still says `ok`.

The SQLite docs warn about network filesystems. The usual next step is guessing from mount options.
lockprobe replaces the guess with a test: it takes a lock on one host and checks whether the other host is refused.

## Example

On the first host:

```
$ lockprobe pair /mnt/nas/app
On the other host, run (with its path to this same share):

  lockprobe pair --join 3f9a1c2e <path>
```

On the second host:

```
$ lockprobe pair --join 3f9a1c2e /srv/app-data
handshake ok, testing locks

PASS        write-visibility files written on each host read back intact on the other
FAIL        lock-exclusion   host B vs lock held on host A: acquired anyway, locks are not shared between hosts; host A vs lock held on host B: acquired anyway, locks are not shared between hosts
PASS        lock-release     host B vs lock held on host A: acquirable after release; host A vs lock held on host B: acquirable after release

Result: FAILED. Do not let more than one process/host write the same database file here.

Note: SQLite WAL mode needs shared memory between processes and does not work
across hosts on any network filesystem, whatever the result above.
```

(Output from the broken-share test fixture. Paths and the session id are illustrative.)

Both hosts print the same result. The two sides coordinate only through files in the share, so no ports or SSH are needed.

## What happens to SQLite when this check fails

Measured with [`scripts/sqlite-two-writers.py`](scripts/sqlite-two-writers.py).
Two processes each run 2000 `UPDATE c SET v = v + 1` transactions on one database file, through two loopback FUSE mounts of one directory with locks not shared between the mounts.
That setup is the test fixture in this repo. It is an emulation of a broken share, not a real NAS.

| journal mode | final value (expected 4000) | committed but lost | integrity_check |
|---|---|---|---|
| delete (rollback) | 2015 | 1106 | ok |
| wal | 1011 | 2980 | ok |

With locks forwarded between the mounts, no commits were lost in either mode.
These are single runs on one Linux 6.18 VM. The numbers show the failure mode, not a rate you should expect.
The CI job runs the same script on real kernel NFS mounts and prints its results in the log.

## Install

```
go install github.com/DushaneW/lockprobe/cmd/lockprobe@latest
```

Prebuilt static binaries for Linux and macOS (amd64, arm64) are attached to [releases](https://github.com/DushaneW/lockprobe/releases).
Windows is not supported.

## Commands

```
lockprobe check DIR [DIR2]      test this host only (DIR2: a second mount of the same share)
lockprobe pair DIR              start a two-host test, prints a join command
lockprobe pair --join ID DIR    join it from the other host
```

| exit | meaning |
|---|---|
| 0 | all checks passed |
| 1 | a check failed |
| 3 | nothing failed, but something could not be verified |
| 2 | usage or setup error |

## Checks

- **write-visibility**: data written and `fsync`'d by one side is read back intact by a fresh open on the other.
- **lock-exclusion**: a process holds an `fcntl` write lock, and the other side's `F_SETLK` on the same range must be refused. `pair` tests both directions.
- **lock-release**: after the holder unlocks or exits, the other side can take the lock within a few seconds.
- **shared-mmap** (`check` only): two `MAP_SHARED` mappings of one file see each other's stores. SQLite's WAL index depends on this.

lockprobe only touches its own scratch files: `.lockprobe-<pid>` for `check`, and the `.lockprobe-pair/<id>/` directory for `pair`, which the initiator removes after a completed session.
It never opens your database files.

## What a pass does and does not mean

- **`check` on one host cannot prove a share is safe for several hosts.** Locks can be consistent inside one client and still invisible to every other client. The test suite contains exactly this case: a single view of the broken fixture passes.
  For network and unknown filesystems, `check` therefore reports `cross-host UNVERIFIED` and exits 3. Use `pair` for the real answer.
- A pass is evidence about the moment of the test. It does not cover lock loss after a server or client restart, lease expiry during a network partition, or bugs that only appear under load.
- **SQLite WAL mode does not work across hosts on any network filesystem, whatever lockprobe says.** Its shared-memory index lives on each host. Use rollback journal mode, or one writer host.
- Several containers on one Linux host share that host's kernel lock table, so they do coordinate. A Kubernetes rolling update on a single node does not by itself break SQLite locking. Multiple nodes mounting one ReadWriteMany volume is the case to test.
- The negative case for `shared-mmap` has not been reproduced in testing yet, so its ability to detect a broken setup is unproven.

## Tests

```
go test ./...
```

- **Linux, FUSE fixtures:** the tests mount loopback FUSE views of a temp directory (needs `/dev/fuse` and `fusermount3`).
  One variant keeps locks local to each mount (broken share), the other forwards them (working share).
  Both `check` and `pair` must fail on the first and pass on the second.
  The tests skip without FUSE locally; CI sets `LOCKPROBE_REQUIRE_FUSE=1` so they cannot silently skip there.
- **Real NFS ([`ci.yml`](.github/workflows/ci.yml)):** mounts a kernel NFS export twice with `nosharecache`.
  With working locks (NFSv3 + lockd, NFSv4.2) both sides must exit 0. With `local_lock=posix` both must exit 1.

Not tested: SMB/CIFS, virtiofs, 9p (WSL2), Docker Desktop file sharing, Ceph, and real multi-machine setups.
Reports from those environments are the most useful issues you can open: include the mount line from `/proc/mounts` and the full output.

## License

MIT

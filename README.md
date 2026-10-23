<div align="center">

# lockprobe

**Does your network share actually enforce file locks? Test it before you put SQLite on it.**

[![CI](https://github.com/DushaneW/lockprobe/actions/workflows/ci.yml/badge.svg)](https://github.com/DushaneW/lockprobe/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.22%2B-00ADD8.svg)](#installation)
[![License: MIT](https://img.shields.io/badge/License-MIT-white.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-Linux_%7C_macOS-lightgrey.svg)](#installation)
[![Tested on](https://img.shields.io/badge/CI_tested-kernel_NFSv3_%7C_NFSv4.2_%7C_FUSE-blue.svg)](#verification)

[Why](#the-problem) · [Quick start](#quick-start) · [How it works](#how-it-works) · [Checks](#checks) · [Limits](#what-a-pass-does-and-does-not-mean) · [Verification](#verification)

</div>

---

**lockprobe** is a single static binary that tests whether a shared directory gives separate hosts the guarantees embedded databases rely on.
It covers NFS, SMB, FUSE, NAS mounts and ReadWriteMany cluster volumes.
On two hosts, it takes a POSIX lock on one and checks that the other is refused, in both directions.
It also checks write visibility and lock release.
The two sides coordinate only through files on the share, so you need no open ports, no SSH and no agent.

---

## The problem

SQLite, BoltDB, LevelDB and similar databases keep two writers apart with `fcntl` advisory locks.
On a share that does not propagate those locks between clients, every writer believes it holds the lock.
Nothing returns an error. Commits disappear, and `PRAGMA integrity_check` still says `ok`.

Measured with [`scripts/sqlite-two-writers.py`](scripts/sqlite-two-writers.py): two processes, each running 2000 `UPDATE c SET v = v + 1` transactions on one database file through two different mounts.

| Setup | Journal | Final value (expected 4000) | Committed but lost | `integrity_check` | lockprobe |
| :--- | :--- | ---: | ---: | :---: | :---: |
| Kernel NFSv3 + lockd, 2 mounts (CI) | delete | 4000 | 0 | ok | **PASS** |
| Kernel NFSv4.2, 2 mounts on one client (CI) | delete | 2000 | 838 | ok | **FAIL** |
| FUSE fixture, locks not shared (local VM) | delete | 2015 | 1106 | ok | **FAIL** |
| FUSE fixture, locks not shared (local VM) | wal | 1011 | 2980 | ok | **FAIL** |
| FUSE fixture, locks forwarded (local VM) | wal | 4000 | 0 | ok | PASS |

Each row is a single run. The numbers show the failure mode, not a rate to expect.
The NFSv4.2 row describes two `nosharecache` mounts on one client, not two separate NFSv4 hosts.
Every CI run prints the kernel NFS numbers next to lockprobe's verdict.

---

## Quick start

On the first host:

```console
$ lockprobe pair /mnt/nas/app
On the other host, run (with its path to this same share):

  lockprobe pair --join 3f9a1c2e <path>
```

On the second host:

```console
$ lockprobe pair --join 3f9a1c2e /srv/app-data
handshake ok, testing locks

PASS        write-visibility files written on each host read back intact on the other
FAIL        lock-exclusion   host B vs lock held on host A: acquired anyway, locks are not shared between hosts; host A vs lock held on host B: acquired anyway, locks are not shared between hosts
PASS        lock-release     host B vs lock held on host A: acquirable after release; host A vs lock held on host B: acquirable after release

Result: FAILED. Do not let more than one process/host write the same database file here.
```

Both hosts print the same verdict.
This output comes from the broken-share test fixture; the paths and session id are illustrative.

---

## Installation

```sh
go install github.com/DushaneW/lockprobe/cmd/lockprobe@latest
```

Static binaries for Linux and macOS (amd64, arm64) with `SHA256SUMS` are on the [releases page](https://github.com/DushaneW/lockprobe/releases).
Windows is not supported.

---

## Commands

| Command | What it does |
| :--- | :--- |
| `lockprobe check DIR [DIR2]` | Tests this host only. Optional `DIR2` is a second mount of the same share. |
| `lockprobe pair DIR` | Starts a two-host test and prints a join command. |
| `lockprobe pair --join ID DIR` | Joins that test from the other host. |
| `--timeout D` (pair) | How long to wait for each step of the other host. Default `2m`. |

| Exit code | Meaning |
| :---: | :--- |
| `0` | All checks passed |
| `1` | A check failed |
| `3` | Nothing failed, but something could not be verified |
| `2` | Usage or setup error |

---

## How it works

```text
        host A                     shared directory                    host B
  lockprobe pair DIR        DIR/.lockprobe-pair/<id>/        lockprobe pair --join <id> DIR
  ------------------        -------------------------        ------------------------------
  write a.hello  ------------------>  nonce  ---------------------->  read, echo back
  read  b.hello  <------------------  nonce+nonce  <----------------  write b.hello
                                         lk
  fcntl F_WRLCK on lk  ---- r1.holding --------------------------->   F_SETLK on lk
                       <--- r1.attempt  (refused | acquired) ------
  unlock               ---- r1.released -------------------------->   retry 5s
                       <--- r1.after    (acquired | stuck) --------
                     ... round 2: roles swapped (B holds, A tests) ...
  both sides print the same verdict; A removes the session directory
```

- Every message is written to a temp file, `fsync`'d and renamed, so a half-written message is never read.
- The reader re-opens the session directory before each poll. This defeats NFS negative-lookup caching, which would otherwise hide a new file for up to `acdirmax`.
- A step that times out is reported as `UNVERIFIED`, never as `PASS`.

---

## Checks

| Check | Mode | Passes when |
| :--- | :---: | :--- |
| **write-visibility** | both | Data written and `fsync`'d on one side reads back intact from a fresh open on the other. |
| **lock-exclusion** | both | While one process holds an `fcntl` write lock, the other side's `F_SETLK` on the same range is refused. `pair` tests both directions. |
| **lock-release** | pair | After the holder unlocks, the other side can take the lock within 5 s. |
| **shared-mmap** | check | Two `MAP_SHARED` mappings of one file see each other's stores. The SQLite WAL index needs this. |
| **cross-host** | check | Never passes on network or unknown filesystems. One host cannot see other clients' lock tables, so this is always `UNVERIFIED`; use `pair`. |

lockprobe only touches its own scratch files: `.lockprobe-<pid>` for `check`, and `.lockprobe-pair/<id>/` for `pair`.
It never opens your databases.

---

## What a pass does and does not mean

- **`check` on one host cannot prove that a share is safe for several hosts.** Locks can be consistent within one client and invisible to every other client. The test suite includes exactly this case: a single view of the broken fixture passes. For that reason `check` reports `cross-host UNVERIFIED` and exits `3`.
- **A pass describes the moment of the test.** It does not cover lock loss after a server or client restart, lease expiry during a network partition, or bugs that only appear under load.
- **SQLite WAL mode never works across hosts on a network filesystem, whatever lockprobe says.** The WAL index is shared memory local to each host. Use rollback-journal mode, or keep a single writer host.
- **Containers on one Linux host share that host's kernel lock table**, so a Kubernetes rolling update on a single node does not break SQLite locking by itself. Multiple nodes mounting one ReadWriteMany volume is the case to test.
- **`shared-mmap` fails between two mounts with separate page caches.** In CI this happened with two FUSE mounts and with two `nosharecache` NFS mounts. That is the correct answer: an mmap-based WAL index cannot work there.

---

## Verification

```sh
go test ./...
```

| Suite | Environment | What it asserts |
| :--- | :--- | :--- |
| FUSE fixtures | Linux CI and local; needs `/dev/fuse` | `check` and `pair` fail when two loopback FUSE mounts keep locks local, and the lock checks pass when locks are forwarded. One view of a broken share is never reported as a pass. |
| Kernel NFS | GitHub Actions, [`ci.yml`](.github/workflows/ci.yml) | Two `nosharecache` mounts of one export, `pair` between them. Expected exit codes: NFSv3 + lockd → `0`/`0`; NFSv3 + `local_lock=posix` → `1`/`1`; NFSv4.2 on one client → `1`/`1`. |
| Build | Ubuntu and macOS CI | `gofmt`, `go vet`, and tests on both platforms. |

- FUSE tests skip on machines without FUSE. CI sets `LOCKPROBE_REQUIRE_FUSE=1`, so they cannot skip silently there.
- **Not yet tested:** SMB/CIFS, virtiofs, 9p (WSL2), Docker Desktop file sharing, Ceph, and real multi-machine setups.
- Results from those environments are the most valuable issue you can open. Include the mount line from `/proc/mounts` and the full output.

---

<div align="center">

[MIT License](LICENSE)

</div>

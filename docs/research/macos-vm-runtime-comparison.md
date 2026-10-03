---
title: "Which macOS VM runs a Python, Django and Postgres workload best? Apple Container, Podman Machine on libkrun and on applehv, and OrbStack, measured without yolo"
date: 2026-10-03
status: in-review
stage: DESIGN
next: "The maintainer decides between a Docker-API backend aimed at OrbStack (closed source, paid for commercial use, and the fastest shared folders and the only memory return measured here) and VM-local volumes for chosen workspace folders on the backends yolo already has (apple-container-file-cost.md §4)"
tags: [research, macos, apple-container, podman, libkrun, orbstack, virtiofs, memory, postgres, benchmark]
summary: "The maintainer asked whether re-adding a Docker-style backend on macOS would make development faster, and whether keeping hot files on the VM's own disk would. The same Python, Django and Postgres workload ran natively and in four VMs, each on a shared Mac folder and on a VM-local disk, without yolo. On a VM-local disk every VM beat native macOS at the Python steps (pytest 0.9 s against 1.85 s, pip install 1.8 to 2.0 s against 4.0 s). On a shared folder, the two Virtualization.framework VMs took 2 to 5 times native on file-heavy steps and libkrun up to 9 times slower again, while OrbStack came within 1.3 to 2.6 times native on all but one step and ran Postgres's reads at 90 percent of native. OrbStack was also the only VM to give a freed 2 GiB back to macOS, within 10 s; libkrun's free page reporting returned nothing even under pressure. OrbStack is closed source and paid for commercial use; Docker Desktop was not run, its licence ruling it out for the maintainer's commercial work."
vantage:
  status-chip: true
---

# Which macOS VM runs a Python, Django and Postgres workload best?

**Status:** 2026-10-03.
- **MEASURED** on one Mac for native, Apple Container, Podman Machine on libkrun and on applehv,
  and OrbStack (a trial install, which the maintainer made).
- **Not run:** Docker Desktop. The maintainer ruled it out (2026-10-03): its licence makes it
  *"non-viable to even test … for commercial work."*
- **INFERRED:** every reading of the numbers that a row does not show directly.
- Nothing is ruled.

It follows [the macOS backend benchmark](macos-backend-performance.md),
[why Apple Container's file work is slow](apple-container-file-cost.md) and
[whether a macOS VM can give memory back](macos-vm-memory-reclaim.md).

**The question,** in the maintainer's words (2026-10-03): *"I would be willing to consider adding
back support for Docker or OrbStack or whatever on a Mac. If they're going to give us meaningful
performance improvements. … another option would be somehow optimizing to run on the VM's disk?
… do we need more benchmark results?"*, then *"let's plan on benchmarking them through trials or
whatever we can, without yolo support yet."*

> **In short.**
>
> - **The VM's own disk beats the Mac for this workload, in every VM.** Building a virtualenv,
>   Django start-up, 2,000 pytest tests and Postgres set-up run 1.2 to 2.5 times as fast as native
>   macOS ([§3.1](#31-on-a-vm-local-disk)). Postgres's read-write rate is the exception, at 59 to 64 percent of native.
> - **A shared Mac folder is where the cost is, except on OrbStack**, and libkrun's sharing is
>   far slower than Virtualization.framework's. On a shared folder, pip install takes 8.4 s on Apple Container and
>   32.7 s on libkrun against 4.0 s native, and Postgres's read-only rate drops to 46 percent of
>   native on Apple Container and to 5 percent on libkrun. **OrbStack's shared folder is in another
>   class**: pip install 5.3 s, `stat` 9 times as fast as Apple Container's, and Postgres's reads at
>   90 percent of native ([§3.2](#32-on-a-shared-mac-folder)).
> - **Only OrbStack gave memory back.** A freed 2 GiB left its VM within 10 s, and a deleted 2 GiB
>   file's cache within 120 s. It stayed resident in the other three; under host memory pressure,
>   libkrun's VM was compressed rather than dropping the pages its guest had reported free
>   ([§4](#4-memory-does-a-vm-give-a-freed-2-gib-back)).
> - **OrbStack's weak spot is Postgres writes on its own disk**: 2,940 read-write transactions per
>   second against 9,000 to 10,000 on the other VMs ([§3.1](#31-on-a-vm-local-disk)).
> - **Two ways forward, both inferred** ([§5](#5-what-this-means-for-the-maintainers-question)):
>   - a Docker-API backend aimed at OrbStack, which fixes both of Apple Container's weaknesses
>     but is closed source and paid for commercial use;
>   - [VM-local volumes](apple-container-file-cost.md#4-a-design-sketch-vm-local-volumes-for-chosen-workspace-folders)
>     on the backends yolo already has, which fixes file speed for chosen folders and not memory.

## Terms

- **VZ**: Apple's Virtualization.framework. Apple Container and Podman Machine's `applehv`
  provider are built on it.
- **libkrun**: Red Hat's library that runs a VM on macOS's lower-level Hypervisor.framework.
  **krunkit** is its launcher, and Podman Machine's `libkrun` provider runs it.
- **Shared folder**: a Mac folder shown inside the VM through virtiofs, the Linux file-sharing
  protocol both VZ and libkrun use. It is the path a yolo workspace takes today.
- **VM-local disk**: a disk image that the guest formats and serves itself: a named volume on
  Apple Container, or the Podman Machine's own disk for a podman volume.
- **Free page reporting**: the virtio balloon feature by which a guest tells the host which pages
  it has freed (defined in [the memory reclaim doc](macos-vm-memory-reclaim.md#terms)).
- **OrbStack**: a closed-source macOS app from OrbStack, Inc. running one Linux VM that serves the
  Docker API. It is free for personal use and paid for commercial use
  ([pricing](https://orbstack.dev/pricing); SOURCED).
- **RSS**: the resident memory macOS's `ps` shows for a process, here the process holding the
  VM's memory.

## 1. The setup

**The Mac:**
- Apple M1 Max, 32 GiB, macOS 26.5 (25F71);
- `container` CLI 1.1.0;
- podman 6.0.2 from Homebrew;
- CrowdStrike Falcon's endpoint-security extension active, as in the earlier runs.

**The runtimes:**

| Runtime | VM | CPUs | Memory |
| :--- | :--- | ---: | ---: |
| `native` | none; macOS, APFS | 10 | 32 GiB |
| `container` | Apple Container, one VM per container | 5 | 8 GiB |
| `krun` | Podman Machine `bench-krun`, libkrun provider, krunkit 1.3.2 with libkrun 1.19.0 | 5 | 8 GiB |
| `applehv` | Podman Machine `podman-machine-default`, applehv (VZ) provider, the maintainer's existing machine | **4** | 8 GiB |
| `orbstack` | OrbStack 2.2.3, its Docker engine (guest kernel 7.0.14), set down from its default 10 CPUs and 16 GiB | 5 | 8 GiB |

For `krun`, Homebrew's krunkit tap carried libkrun 1.16.0, older than the free page reporting
change, so the run used krunkit's own release build instead ([Appendix B](#appendix-b-setting-up-the-libkrun-machine)).

**What else ran:**
- yolo's Apple Container builder VM (8 CPUs, a 12 GiB cap) ran throughout.
- The maintainer's own jail was not listed as running two minutes into the VM runs.
- Each Podman Machine was started before its run and stopped after it, and only one runtime
  ran at a time. OrbStack's VM stayed up between its runs.

**The workload** ([`wl.sh`](#appendix-a-the-scripts)) runs in one folder:
1. the earlier docs' `fs.js` and `imp.py`;
2. a Python virtualenv built offline from local wheels for Django 5.2, pytest 8 and psycopg 3.2,
   then ripgrep over it;
3. a generated Django project with 40 models and 200 test modules of 10 tests each, timing
   `django.setup()`, `manage.py check` and pytest over the 2,000 tests;
4. Postgres 17: `initdb`, a `pgbench` load at scale 20, then 20 s each of read-write and
   read-only `pgbench` with 8 clients.

Every VM runs one image built from [`Containerfile`](#appendix-a-the-scripts) (the
`postgres:17-bookworm` base, Python 3.11). The native run uses Homebrew's Python 3.14 and
Postgres 17.

**Repetition:**
- Every row is one run, so a difference under about 10 percent is noise.
- Repeated steps show the median of their repeats: three for ripgrep, `manage.py check` and
  pytest, five for `django.setup()`.
- pytest shows its first run and the median of all three, because the first run writes the
  bytecode caches.

## 2. What differs between the runs, and does not matter here

- **Python versions differ**: 3.14 natively and 3.11 in the VMs. The VMs are compared with each
  other on one interpreter, and native is the reference.
- **Syncs mean different things.** Native `fs.js` syncs reach the drive (`F_FULLFSYNC`, 249 per
  second). The guests' syncs evidently stop above it (2,900 to 7,100 per second; INFERRED, as in
  [the file cost doc](apple-container-file-cost.md#21-per-file-cost-not-bandwidth)). Postgres on
  macOS does not use `F_FULLFSYNC` unless `wal_sync_method = fsync_writethrough` is set
  (SOURCED: Postgres's WAL configuration documentation), so all four Postgres rates are at the same, development-grade
  durability. OrbStack's guest syncs at 1,164 per second on its own disk and 1,779 on a shared
  folder, between the two, so its writes may reach further down (INFERRED).
- **applehv has 4 CPUs where the others have 5.** This touches only the parallel steps (ripgrep,
  `pgbench`), and applehv still matches or beats the 5-CPU VMs on both.

## 3. Results

### 3.1 On a VM-local disk

| Step | native | Apple Container | libkrun | applehv | OrbStack |
| :--- | ---: | ---: | ---: | ---: | ---: |
| create 20,000 small files | 3.16 s | 0.13 s | 0.36 s | 0.30 s | 0.28 s |
| `stat` 20,000 | 91 ms | 29 ms | 39 ms | 36 ms | 31 ms |
| `stat` 20,000 missing names | 183 ms | 347 ms | 377 ms | 375 ms | 357 ms |
| open and read 20,000 | 1284 ms | 93 ms | 143 ms | 130 ms | 96 ms |
| delete 20,000 | 1917 ms | 43 ms | 196 ms | 134 ms | 304 ms |
| sequential write / read, MB/s | 4509 / 9190 | 1424 / 3572 | 2494 / 3706 | 2319 / 3665 | 1088 / 3353 |
| 40 imports, first / warm process | 806 / 176 ms | 408 / 70 ms | 415 / 74 ms | 414 / 74 ms | 432 / 74 ms |
| create the virtualenv | 2.07 s | 1.73 s | 1.77 s | 1.75 s | 1.74 s |
| pip install, offline | 4.01 s | 2.01 s | 1.87 s | 1.85 s | 1.80 s |
| ripgrep over the virtualenv | 0.292 s | 0.019 s | 0.022 s | 0.024 s | 0.027 s |
| `django.setup()` | 0.290 s | 0.136 s | 0.147 s | 0.146 s | 0.140 s |
| `manage.py check` | 0.302 s | 0.159 s | 0.166 s | 0.165 s | 0.163 s |
| pytest, 2,000 tests, first / median | 2.79 / 1.85 s | 1.65 / 0.90 s | 1.71 / 0.97 s | 1.70 / 0.95 s | 1.69 / 0.88 s |
| Postgres `initdb` | 0.91 s | 0.37 s | 0.41 s | 0.38 s | 1.18 s |
| `pgbench` load, scale 20 | 2.34 s | 1.27 s | 1.39 s | 1.26 s | 1.79 s |
| `pgbench` read-write, transactions/s | 15,575 | 9,205 | 9,173 | 9,952 | 2,940 |
| `pgbench` read-only, transactions/s | 120,508 | 92,744 | 145,537 | 135,170 | 153,119 |

MEASURED. What it shows:

- **The four VMs are within about 12 percent of each other** on every Python step, and the three
  non-OrbStack VMs on every Postgres set-up step too. Which hypervisor runs the guest hardly matters once its files are local. The
  file primitives and Postgres's read-only rate vary more.
- **The VMs beat native by 1.2 to 2.5 times** on every Python and Postgres set-up step, and by
  9 to 45 times on creating, reading and deleting small files. `stat` on missing names is the
  one file step where native wins, at about half the VMs' time. INFERRED: as
  in the earlier doc, the guest's kernel answers from its own caches, while macOS's file path
  (APFS and the endpoint-security extension) is the slower one.
- **Native wins only on Postgres's read-write rate**, at 1.6 times the VMs. The read-only rate is
  higher on the two Podman VMs than natively. INFERRED: Apple Container's lower read-only rate
  may come from sharing the CPU with the builder VM. Nothing here separates that cause from the
  runtime.
- **OrbStack's own disk is slow for Postgres writes**: `initdb` 3 times the other VMs and 2,940
  read-write transactions per second, about a third of theirs, while its read-only rate is the
  highest measured. That fits its slower syncs ([§2](#2-what-differs-between-the-runs-and-does-not-matter-here)), and makes its shared folder the faster home
  for a Postgres it writes to (7,101, [§3.2](#32-on-a-shared-mac-folder)).

### 3.2 On a shared Mac folder

| Step | native | Apple Container | libkrun | applehv | OrbStack |
| :--- | ---: | ---: | ---: | ---: | ---: |
| create 20,000 small files | 3.16 s | 11.1 s | **20.2 s** | 12.8 s | 5.12 s |
| `stat` 20,000 | 91 ms | 2171 ms | **6899 ms** | 2306 ms | 241 ms |
| `stat` 20,000 missing names | 183 ms | 1713 ms | **6457 ms** | 1847 ms | 1551 ms |
| open and read 20,000 | 1284 ms | 6070 ms | **15,483 ms** | 7421 ms | 2204 ms |
| delete 20,000 | 1917 ms | 4638 ms | **12,845 ms** | 5412 ms | 2556 ms |
| sequential write / read, MB/s | 4509 / 9190 | 972 / 1670 | 947 / 1718 | 973 / 1569 | 1822 / 1702 |
| 40 imports, first / warm process | 806 / 176 ms | 742 / 163 ms | 1600 / 690 ms | 823 / 188 ms | 582 / 107 ms |
| create the virtualenv | 2.07 s | 3.82 s | 9.81 s | 3.75 s | 2.80 s |
| pip install, offline | 4.01 s | 8.35 s | **32.7 s** | 9.26 s | 5.34 s |
| ripgrep over the virtualenv | 0.292 s | 1.568 s | 3.921 s | 1.411 s | 0.370 s |
| `django.setup()` | 0.290 s | 0.307 s | 1.332 s | 0.364 s | 0.211 s |
| `manage.py check` | 0.302 s | 0.361 s | 1.538 s | 0.420 s | 0.240 s |
| pytest, 2,000 tests, first / median | 2.79 / 1.85 s | 2.29 / 1.32 s | 6.18 / 4.89 s | 2.51 / 1.44 s | 2.08 / 1.17 s |
| Postgres `initdb` | 0.91 s | 2.27 s | 4.90 s | 2.68 s | 1.18 s |
| `pgbench` load, scale 20 | 2.34 s | 11.36 s | 9.47 s | 11.86 s | 2.12 s |
| `pgbench` read-write, transactions/s | 15,575 | 4,103 | 2,554 | 4,605 | 7,101 |
| `pgbench` read-only, transactions/s | 120,508 | 55,383 | **6,097** | 51,009 | 107,871 |

MEASURED. What it shows:

- **The two VZ runtimes share one cost.** Apple Container and applehv are within about 25
  percent of each other on every row, so it is VZ's virtiofs, not Apple Container's own code, that
  sets this cost.
- **OrbStack's shared folder is 1.1 to 9 times as fast as VZ's**, and the only one close to
  native. `stat` is 2.6 times native against VZ's 24; pip install 1.3 times against 2.1; the
  `pgbench` load is faster than native; read-only Postgres runs at 90 percent of native and
  read-write at 46 percent, against VZ's 26 to 30. Warm pytest (1.17 s) and `django.setup()`
  (0.21 s) beat native. How it does this is undisclosed; it is closed source.
- **libkrun's virtiofs costs 1.6 to 9 times VZ's on all but one step.** `stat` is 3.2 times VZ's, pip install 3.9
  times, warm pytest 3.7 times, and Postgres's read-only rate falls to a ninth; the `pgbench` load, about a fifth faster than
  on VZ, is the exception. INFERRED:
  libkrun's guest re-asks the Mac for data that VZ's guest keeps in its own cache. The read-only
  rate, which only reads pages Postgres has already loaded, points that way, but nothing here
  checks it.
- **Once their files are cached, read-mostly steps run as fast as native on VZ.** Warm imports,
  `django.setup()` and pytest's median are within 25 percent of native or faster. Creating
  files, installing packages, and Postgres's load and writes stay 2 to 5 times native.

## 4. Memory: does a VM give a freed 2 GiB back?

[`memsess.sh`](#appendix-a-the-scripts) starts one container and records the VM process's RSS
after each of three steps:
- a process in the guest holds 2 GiB of random bytes for 20 s and exits;
- 120 s pass;
- the container writes a 2 GiB file to its VM-local disk, reads it back and deletes it.

| RSS of the VM process, MiB | Apple Container | libkrun | applehv | OrbStack |
| :--- | ---: | ---: | ---: | ---: |
| idle | 617 | 1540 | 1636 | 1181 |
| holding 2 GiB | 2751 | 3490 | 3725 | 3118 |
| 120 s after it exited | 2752 | 3494 | 3725 | 1066 |
| after the 2 GiB file was written and read | 4275 | 6831 | 5100 | 4527 |
| 120 s after the file was deleted | 4275 | 6833 | 5101 | 1016 |

MEASURED. **Only OrbStack's RSS fell.** It dropped by 2 GiB within 10 s of the process
exiting, and the deleted file's cache left within 120 s. The compressor did not move, and after
the file was deleted the Mac's free memory rose by 1.8 GiB, so the memory went back to macOS
rather than into compression. How
OrbStack does it is undisclosed. No other VM's RSS fell, libkrun's included.

That was expected of the VZ runtimes. libkrun's guest does support reporting:
- the Fedora CoreOS guest has `virtio_balloon` and `page_reporting` loaded, with a reporting
  order of 9 (2 MiB blocks of 4 KiB pages);
- the device offers the reporting feature (bit 5);
- libkrun 1.19.0 frees each reported range with `MADV_FREE` (SOURCED: libkrun's
  `balloon/device.rs` at `v1.19.0`).

On macOS, `MADV_FREE` leaves a page resident until macOS needs it, so a quiet Mac shows nothing.
Inside the guest, `free` reported the 2 GiB free again 30 s after the process exited.

**Under pressure, the VM was compressed.** [`pressure.py`](#appendix-a-the-scripts) then
allocated random bytes on the Mac in 512 MiB steps, while the libkrun VM sat with its 2 GiB freed
and reported:
- up to 8.5 GiB allocated, macOS took the memory from its file cache, and the VM's RSS stayed
  at 3,666 MiB;
- at 9 GiB, the VM's RSS fell by 992 MiB;
- in the same step, the compressor grew from 446 MiB to 9,259 MiB;
- `vmmap`'s physical footprint for the VM, which counts compressed pages and not pages dropped
  after `MADV_FREE`, went from 3.6 to 3.5 GiB.

So the VM's pages were mostly compressed, not dropped as free (INFERRED from those two figures).
On this Mac, free page reporting did not give the memory back even when macOS needed it.

What this run cannot settle:
- **Whether the reports reached the device at all.** libkrun logs each release only at debug
  level, and Podman Machine gives no way to turn that on.
- **Whether `MADV_FREE` works on memory mapped into a Hypervisor.framework guest.**

[containers/libkrun #703](https://github.com/containers/libkrun/pull/703)'s author and
[lima #4220](https://github.com/lima-vm/lima/issues/4220) both reported memory coming back. The
difference from this run is unexplained.

⚠ **`footprint` does not see libkrun's or OrbStack's guest memory.** It reported 305 MiB for a
krunkit process whose RSS was 3.6 GiB, while `vmmap -summary` showed a 3.6 GiB physical
footprint, and 90 to 212 MiB for OrbStack's helper throughout. So `memsess.sh`'s footprint column
means something only for the VZ runtimes, and the table uses RSS.

⚠ **Running `pressure.py` costs the Mac.** It pushed 9 GiB into the compressor and evicted most
of the file cache, both recovered within minutes. Run it on a Mac nobody is using.

## 5. What this means for the maintainer's question

INFERRED from [§3](#3-results) and [§4](#4-memory-does-a-vm-give-a-freed-2-gib-back).

- **A VM-local disk is fast in every VM, and Apple Container already has one.** Every VM on its
  own disk beat native on the Python steps, within about 12 percent of each other.
- **libkrun is not a reason to switch.** Its shared folders are the slowest measured, and the
  memory reclaim that made it the candidate did not show up.
- **OrbStack is the one runtime that fixes both of Apple Container's weaknesses**: its shared
  folder is near native, so a workspace needs no relocation, and it gives memory back. Using it
  means:
  - re-adding a Docker-API backend, which yolo removed;
  - one shared VM for every jail, as on Podman Machine, rather than one VM per jail;
  - a closed-source, paid dependency for commercial use, with this Mac's trial lasting 30 days.
- **VM-local volumes on the backends yolo has** fix file speed for the folders a user names
  (the virtualenv, `node_modules`, build caches, a database's data folder), on Apple Container
  and Podman alike, and leave memory where it is:
  [the design sketch](apple-container-file-cost.md#4-a-design-sketch-vm-local-volumes-for-chosen-workspace-folders).
- **Docker Desktop** was not measured, by the maintainer's ruling on its licence.

## 6. Re-running it

1. Build the image and save it beside the scripts:
   `podman build -t localhost/vmbench:1 vmbench && podman save --format oci-archive -o vmbench/vmbench.oci.tar localhost/vmbench:1`.
2. For the native run, download macOS wheels for the host's Python into `vmbench/wheels-darwin`:
   `pip download --dest vmbench/wheels-darwin 'django==5.2.*' 'pytest==8.*' 'psycopg[binary]==3.2.*'`.
3. Run `./run-all.sh native container krun applehv orbstack`, naming only the runtimes
   present (`docker` is wired for Docker Desktop but was never run). OrbStack's `docker` CLI is
   in `~/.orbstack/bin`; put it on `PATH`. It writes `results/<runtime>-share.tsv` and `results/<runtime>-vol.tsv`.
4. Run `./memsess.sh <runtime>` for each runtime, one at a time.
5. `python3 pivot.py results/*.tsv` prints one table.

Two traps:
- **Port 5432.** The native run moves Postgres to port 5499 and a socket in `/tmp`, because a
  Postgres the Mac already runs holds 5432. Stop nothing of the user's.
- **bash 5.** `wl.sh` needs bash 5 for `EPOCHREALTIME`. macOS's own bash is 3.2, so the native
  run needs Homebrew's.

## Appendix A: the scripts

All live in one folder beside `fs.js` and `imp.py` from
[the file cost doc's appendices](apple-container-file-cost.md#appendix-fsjs). Every VM mounts that
folder at `/b`, its `share/` subfolder at `/share`, and a volume named `vmb` at `/vol`.

### `Containerfile`

```dockerfile
FROM docker.io/library/postgres:17-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends python3 python3-venv python3-pip nodejs ripgrep procps \
 && rm -rf /var/lib/apt/lists/*
RUN python3 -m pip download --no-cache-dir --dest /wheels django==5.2.* pytest==8.* "psycopg[binary]==3.2.*"
```

### `wl.sh`

```bash
#!/usr/bin/env bash
# wl.sh <dir> <label>: a Python/Django/Postgres development workload in one directory.
# Prints "label<TAB>metric<TAB>value" lines. Needs python3, node, rg, postgres 17 and pgbench on
# PATH, and WHEELS naming a folder holding django, pytest and psycopg wheels for this platform.
set -euo pipefail
D=$1 L=$2 HERE=$(cd "$(dirname "$0")" && pwd)
W=$D/wl-$$; mkdir -p "$W"; trap 'pg_ctl_stop; rm -rf "$W"' EXIT
PG_USER=${PG_USER:-}
[ -n "${EPOCHREALTIME:-}" ] || { echo "wl.sh needs bash 5 (EPOCHREALTIME)" >&2; exit 1; }
t() { local n=$1 s e; shift; s=$EPOCHREALTIME; "$@" >/dev/null 2>&1 || { echo -e "$L\t$n\tFAILED"; return 0; }; e=$EPOCHREALTIME
  awk -v l="$L" -v n="$n" -v s="$s" -v e="$e" 'BEGIN { printf "%s\t%s\t%.3f\n", l, n, e - s }'; }
med() { local n=$1 k=$2 i; shift 2; for i in $(seq "$k"); do t "$n.$i" "$@"; done; }
asuser() { if [ -n "$PG_USER" ]; then gosu "$PG_USER" "$@"; else "$@"; fi; }
pg_ctl_stop() { [ -f "$W/pg/postmaster.pid" ] && asuser pg_ctl -D "$W/pg" -m fast stop >/dev/null 2>&1 || true; }

# 1. filesystem primitives and imports
node "$HERE/fs.js" "$W" "$L"
cp -R "$(python3 -c 'import os; print(os.path.dirname(os.__file__))')" "$W/lib"
python3 "$HERE/imp.py" "$W/lib" "$L"; rm -rf "$W/lib"

# 2. a virtualenv, installed offline
t venv_create python3 -m venv "$W/venv"
t pip_install "$W/venv/bin/pip" install -q --no-index --find-links "$WHEELS" django pytest 'psycopg[binary]'
echo -e "$L\tvenv_files\t$(find "$W/venv" -type f | wc -l | tr -d ' ')"
med rg_venv 3 rg -c -j 5 --no-ignore django "$W/venv"

# 3. a Django project: 40 models, 200 test modules of 10 tests each
P=$W/proj; mkdir -p "$P"; "$W/venv/bin/django-admin" startproject proj "$P" >/dev/null
mkdir -p "$P/app/tests" "$P/app/migrations"; touch "$P/app/__init__.py" "$P/app/tests/__init__.py" "$P/app/migrations/__init__.py"
python3 - "$P" <<'EOF'
import sys, os
p = sys.argv[1]
with open(f"{p}/app/models.py", "w") as f:
    f.write("from django.db import models\n")
    for i in range(40):
        f.write(f"class M{i}(models.Model):\n    name = models.CharField(max_length=50)\n    n = models.IntegerField(default=0)\n    created = models.DateTimeField(auto_now_add=True)\n")
for m in range(200):
    with open(f"{p}/app/tests/test_m{m}.py", "w") as f:
        f.write(f"import json, decimal, datetime\nfrom app.models import M{m % 40}\n")
        for i in range(10):
            f.write(f"def test_{i}():\n    assert json.loads(json.dumps({{'a': {i}}}))['a'] == {i}\n    assert M{m % 40}._meta.model_name\n")
s = f"{p}/proj/settings.py"
src = open(s).read().replace("INSTALLED_APPS = [", "INSTALLED_APPS = [\n    'app',")
open(s, "w").write(src)
open(f"{p}/conftest.py", "w").write("import os, django\nos.environ.setdefault('DJANGO_SETTINGS_MODULE', 'proj.settings')\ndjango.setup()\n")
EOF
cd "$P"
export DJANGO_SETTINGS_MODULE=proj.settings PYTHONPATH=$P
med django_setup 5 "$W/venv/bin/python" -c 'import django; django.setup()'
med manage_check 3 "$W/venv/bin/python" manage.py check
med pytest_2000 3 "$W/venv/bin/python" -m pytest -q -p no:cacheprovider app/tests
cd /

# 4. Postgres: init, load, read-write and read-only throughput
if [ -n "$PG_USER" ]; then chown -R "$PG_USER" "$W"; fi
t pg_initdb asuser initdb -D "$W/pg" -U postgres --auth=trust
asuser pg_ctl -D "$W/pg" -o "-p 5499 -k /tmp -c listen_addresses=''" -l "$W/pg.log" -w start >/dev/null ||
  { echo -e "$L\tpg_start\tFAILED"; tail -3 "$W/pg.log" >&2 || true; exit 0; }
t pgbench_init_s20 asuser pgbench -h /tmp -p 5499 -U postgres -i -s 20 postgres
for mode in rw ro; do
  extra=; [ $mode = ro ] && extra=-S
  tps=$(asuser pgbench -h /tmp -p 5499 -U postgres -c 8 -j 4 -T 20 $extra postgres 2>/dev/null | awk '/^tps/ { printf "%.0f", $3 }')
  echo -e "$L\tpgbench_${mode}_tps\t${tps:-FAILED}"
done
pg_ctl_stop
```

### `run-all.sh`

```bash
#!/opt/homebrew/bin/bash
# run-all.sh <runtime>...: wl.sh on each runtime's shared folder and VM-local disk.
set -uo pipefail
B=$(cd "$(dirname "$0")" && pwd); R=$B/results; mkdir -p "$R" "$B/share"
IMG=localhost/vmbench:1
log() { echo "$(date +%T) $*" >&2; }
for rt in "$@"; do
  log "$rt"
  case $rt in
    native)
      PATH=/opt/homebrew/bin:$HOME/.local/share/mise/installs/node/24.19.0/bin:$PATH WHEELS=$B/wheels-darwin \
        "$B/wl.sh" "$B/share" native >"$R/native.tsv" ;;
    container)
      container image inspect $IMG >/dev/null 2>&1 || container image load -i "$B/vmbench.oci.tar"
      container volume inspect vmb >/dev/null 2>&1 || container volume create vmb >/dev/null
      for fs in share vol; do
        tgt=/share; [ $fs = vol ] && tgt=/vol
        container run --rm --cpus 5 --memory 8g -e WHEELS=/wheels -e PG_USER=postgres \
          -v "$B:/b" -v "$B/share:/share" -v vmb:/vol $IMG /b/wl.sh $tgt container-$fs >"$R/container-$fs.tsv"
      done ;;
    krun|applehv)
      m=bench-krun; [ $rt = applehv ] && m=podman-machine-default
      export CONTAINERS_CONF_OVERRIDE=$HOME/.config/bench-krun/containers.conf
      [ $rt = applehv ] && unset CONTAINERS_CONF_OVERRIDE
      podman machine start $m >/dev/null 2>&1
      podman -c $m image exists $IMG || podman -c $m load -i "$B/vmbench.oci.tar" >/dev/null
      podman -c $m volume exists vmb || podman -c $m volume create vmb >/dev/null
      cpus=5; [ $rt = applehv ] && cpus=4
      for fs in share vol; do
        tgt=/share; [ $fs = vol ] && tgt=/vol
        podman -c $m run --rm --cpus $cpus -e WHEELS=/wheels -e PG_USER=postgres \
          -v "$B:/b" -v "$B/share:/share" -v vmb:/vol $IMG /b/wl.sh $tgt $rt-$fs >"$R/$rt-$fs.tsv"
      done
      podman machine stop $m >/dev/null 2>&1 ;;
    orbstack|docker)
      ctx=orbstack; [ $rt = docker ] && ctx=desktop-linux
      docker --context $ctx image inspect $IMG >/dev/null 2>&1 || docker --context $ctx load -i "$B/vmbench.oci.tar" >/dev/null
      docker --context $ctx volume create vmb >/dev/null
      for fs in share vol; do
        tgt=/share; [ $fs = vol ] && tgt=/vol
        docker --context $ctx run --rm --cpus 5 -e WHEELS=/wheels -e PG_USER=postgres \
          -v "$B:/b" -v "$B/share:/share" -v vmb:/vol $IMG /b/wl.sh $tgt $rt-$fs >"$R/$rt-$fs.tsv"
      done ;;
  esac
done
log done
```

### `memsess.sh`

```bash
#!/opt/homebrew/bin/bash
# memsess.sh <runtime>: does the VM give a 2 GiB load back to macOS while it keeps running?
# Starts one long-lived container, finds the host process holding the VM's memory by diffing
# process lists, then runs two loads in the container and reads the host after each:
#   anon: 2 GiB of incompressible bytes (os.urandom) held 20 s by a process that then exits
#   file: a 2 GiB incompressible file written to the VM's own disk, read back, deleted
# Rows: label, when, VM footprint MiB, VM reclaimable MiB (footprint's own column; MADV_FREE'd
# pages land there), VM RSS MiB, host free MiB, host compressor MiB, memorystatus level.
set -uo pipefail
B=$(cd "$(dirname "$0")" && pwd); R=$B/results; mkdir -p "$R"
rt=$1 IMG=localhost/vmbench:1 PG=16384
vmprocs() { pgrep -f "${PAT}" | sort; }
run() { case $rt in
  container) container "$@" ;;
  krun) CONTAINERS_CONF_OVERRIDE=$HOME/.config/bench-krun/containers.conf podman -c bench-krun "$@" ;;
  applehv) podman -c podman-machine-default "$@" ;;
  orbstack) docker --context orbstack "$@" ;;
  docker) docker --context desktop-linux "$@" ;;
esac; }
case $rt in
  krun) PAT='krunkit' ;;
  orbstack) PAT='OrbStack Helper.*vmgr' ;; # OrbStack runs its VM inside this helper
  *) PAT='com.apple.Virtualization.VirtualMachine' ;;
esac
before=$(vmprocs)
case $rt in
  container) container run -d --name memsess --cpus 5 --memory 8g -v "$B:/b" -v vmb:/vol $IMG sleep 3600 >/dev/null ;;
  krun) CONTAINERS_CONF_OVERRIDE=$HOME/.config/bench-krun/containers.conf podman machine start bench-krun >/dev/null 2>&1
        run run -d --name memsess -v vmb:/vol $IMG sleep 3600 >/dev/null ;;
  applehv) podman machine start podman-machine-default >/dev/null 2>&1
           run run -d --name memsess -v vmb:/vol $IMG sleep 3600 >/dev/null ;;
  *) run run -d --name memsess -v vmb:/vol $IMG sleep 3600 >/dev/null ;;
esac
sleep 5
VM=$(comm -13 <(echo "$before") <(vmprocs) | head -1)
if [ -z "$VM" ]; then # the VM was already running (OrbStack, Docker Desktop): take the largest
  VM=$(for p in $(vmprocs); do echo "$(ps -o rss= -p "$p") $p"; done | sort -rn | awk 'NR == 1 { print $2 }')
fi
echo "# $rt: VM process $VM: $(ps -o comm= -p "$VM")" | tee "$R/mem-$rt.tsv" >&2
row() {
  local fp rec rss free comp lvl
  read -r fp rec < <(footprint "$VM" 2>/dev/null | awk '
    function mb(v, u) { return u == "GB" ? v * 1024 : u == "MB" ? v : u == "KB" ? v / 1024 : 0 }
    /Footprint:/ { for (i = 1; i <= NF; i++) if ($i == "Footprint:") f = mb($(i + 1), $(i + 2)) }
    /TOTAL/ { r = mb($5, $6) } END { printf "%.0f %.0f\n", f, r }')
  rss=$(( $(ps -o rss= -p "$VM") / 1024 ))
  free=$(vm_stat | awk -v p=$PG '/Pages free/ { gsub("\\.", "", $3); printf "%d", $3 * p / 1048576 }')
  comp=$(vm_stat | awk -v p=$PG '/occupied by compressor/ { gsub("\\.", "", $5); printf "%d", $5 * p / 1048576 }')
  lvl=$(sysctl -n kern.memorystatus_level)
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$rt" "$1" "$fp" "$rec" "$rss" "$free" "$comp" "$lvl" | tee -a "$R/mem-$rt.tsv"
}
row idle
run exec memsess python3 -c 'import os, time
b = [bytearray(os.urandom(64 << 20)) for _ in range(32)]
time.sleep(20)' & sleep 15; row anon-held; wait
for s in 2 10 60 120; do sleep $(( s - ${last:-0} )); last=$s; row anon-freed+${s}s; done
last=0
run exec memsess sh -c 'dd if=/dev/urandom of=/vol/memsess bs=64M count=32 status=none && cat /vol/memsess > /dev/null'
row file-written
run exec memsess rm -f /vol/memsess
for s in 10 120; do sleep $(( s - last )); last=$s; row file-deleted+${s}s; done
run rm -f memsess >/dev/null 2>&1
case $rt in krun|applehv) row container-removed ;; esac
case $rt in
  krun) CONTAINERS_CONF_OVERRIDE=$HOME/.config/bench-krun/containers.conf podman machine stop bench-krun >/dev/null 2>&1 ;;
  applehv) podman machine stop podman-machine-default >/dev/null 2>&1 ;;
esac
```

### `pressure.py`

```python
# pressure.py <vm-pid> <max-GiB>: allocate incompressible memory on the host in 512 MiB steps,
# printing the VM process's RSS, the host compressor and swap after each step. Stops early once
# the VM's RSS has fallen by 1.5 GiB or the compressor has grown by 2 GiB, then frees everything.
import os, sys, subprocess, time
pid, cap = sys.argv[1], int(sys.argv[2])
def rss(): return int(subprocess.run(["ps", "-o", "rss=", "-p", pid], capture_output=True, text=True).stdout) // 1024
def vmstat(key):
    for l in subprocess.run(["vm_stat"], capture_output=True, text=True).stdout.splitlines():
        if l.startswith(key): return int(l.split()[-1].rstrip(".")) * 16384 >> 20
def swap(): return subprocess.run(["sysctl", "-n", "vm.swapusage"], capture_output=True, text=True).stdout.split()[5]
r0, c0, held = rss(), vmstat("Pages occupied by compressor"), []
print(f"host_GiB\tvm_rss_MiB\tcompressor_MiB\tfree_MiB\tinactive_MiB\tswap_used", flush=True)
print(f"0\t{r0}\t{c0}\t{vmstat('Pages free')}\t{vmstat('Pages inactive')}\t{swap()}", flush=True)
for i in range(1, cap * 2 + 1):
    held.append(bytearray(os.urandom(512 << 20))); time.sleep(1)
    r, c = rss(), vmstat("Pages occupied by compressor")
    print(f"{i / 2:g}\t{r}\t{c}\t{vmstat('Pages free')}\t{vmstat('Pages inactive')}\t{swap()}", flush=True)
    if r0 - r > 1536 or c - c0 > 2048: break
del held; time.sleep(5)
print(f"released\t{rss()}\t{vmstat('Pages occupied by compressor')}\t{vmstat('Pages free')}\t{vmstat('Pages inactive')}\t{swap()}")
```

### `pivot.py`

```python
# pivot.py <tsv>...: one row per metric, one column per file. A repeated metric (name.N) shows its
# median; pytest shows first/median.
import sys, os, statistics, collections
cols, vals, order = [], collections.defaultdict(dict), []
for p in sys.argv[1:]:
    c = os.path.basename(p)[:-4]; cols.append(c); runs = collections.defaultdict(list)
    for line in open(p):
        if line.startswith("#") or not line.strip(): continue
        f = line.rstrip("\n").split("\t"); m, v = f[1], f[2].removesuffix(" ms")
        if m == "import_40_pkgs": v = f"{f[2].split()[1]}/{f[3].split()[2]}"
        base, _, n = m.rpartition(".")
        if n.isdigit(): runs[base].append(v); m = base; v = None
        if m not in order: order.append(m)
        if v is not None: vals[m][c] = v
    for m, r in runs.items():
        try: x = [float(v) for v in r]
        except ValueError: vals[m][c] = "FAILED"; continue
        vals[m][c] = f"{x[0]:.2f}/{statistics.median(x[1:]):.2f}" if m == "pytest_2000" else f"{statistics.median(x):.3f}"
print("| metric | " + " | ".join(cols) + " |"); print("| :--- |" + " ---: |" * len(cols))
for m in order: print(f"| {m} | " + " | ".join(vals[m].get(c, "-") for c in cols) + " |")
```

## Appendix B: setting up the libkrun machine

Homebrew's podman has no krunkit of its own, and the `slp/krunkit` tap's libkrun (1.16.0)
predates free page reporting. krunkit's release build is unsigned, and macOS will not run a
hypervisor without the entitlement. So:

```console
$ cd krunkit && tar xzf krunkit-podman-unsigned-1.3.2.tgz   # from github.com/containers/krunkit releases
$ install_name_tool -add_rpath @executable_path/../lib bin/krunkit
$ codesign -f -s - --entitlements ent.plist bin/krunkit     # the dylibs keep their ad-hoc signatures
$ mkdir -p ~/.config/bench-krun && cat > ~/.config/bench-krun/containers.conf <<'CONF'
[machine]
provider = "libkrun"
[engine]
helper_binaries_dir = ["<this folder>/krunkit/bin", "/opt/homebrew/opt/podman/libexec/podman", "/opt/homebrew/bin"]
CONF
$ export CONTAINERS_CONF_OVERRIDE=~/.config/bench-krun/containers.conf
$ podman machine init bench-krun --cpus 5 --memory 8192 --disk-size 60 -v "$PWD/..:$PWD/.."
```

`ent.plist` grants `com.apple.security.hypervisor` and
`com.apple.security.cs.disable-library-validation`; the second lets the ad-hoc-signed binary load
the ad-hoc-signed `libkrun.dylib`. The machine's guest showed the balloon device offering
feature bits 1, 3 and 5 (stats, free page hinting and free page reporting).

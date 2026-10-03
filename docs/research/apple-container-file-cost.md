---
title: "Why file work is slow in an Apple Container jail, and what it means for a large Python monorepo"
date: 2026-10-03
status: accepted
stage: DECIDED
next: "Decide whether yolo should offer VM-local volumes for chosen workspace subdirectories on Apple Container (the design sketch in §4); file Apple Container's per-file virtiofs cost upstream with fs.js as the reproduction"
tags: [research, macos, apple-container, virtiofs, performance, python, postgres]
summary: "A follow-up to the macOS backend benchmark. File work in an Apple Container jail is slow because every file costs a round trip to the Mac through virtiofs, not because bytes move slowly: 20,000 small files take 12 to 115 times as long to create, stat, read or delete on the shared workspace as on the VM's own ext4 disk, which beats even native APFS. A large Python and Django monorepo keeps its virtualenv, node_modules and build caches in the workspace, so it pays that cost on every import and every test run. The fix that the numbers point to is keeping those trees on a VM-local disk; yolo has no key for that today."
vantage:
  status-chip: true
---

# Why file work is slow in an Apple Container jail, and what it means for a large Python monorepo

**Status:** 2026-10-03. MEASURED on one Mac ([§2](#2-results)); the workload assessment ([§3](#3-what-this-means-for-a-large-python-and-django-monorepo)) and the design sketch
([§4](#4-a-design-sketch-vm-local-volumes-for-chosen-workspace-folders)) are INFERRED from those numbers, and nothing is ruled. It follows
[the macOS backend benchmark](macos-backend-performance.md), whose M5 to M7 showed Apple
Container at 3 to 5 times native on files in the shared workspace, and asks the maintainer's next
question (2026-10-03): *"are we IO bound? or number of files bound?"* — for a development setup
with *"some IO, lots of CPU processing, databases, things like that. Historically, it's been really
slow in VMs."*

> **In short.**
>
> - **Bound by the number of files, not by bandwidth.** On the shared workspace a large file
>   streams at 1.3 to 1.6 GB/s, a fifth to a third of native. A small file's create, stat, open or delete
>   costs 60 to 400 µs more than native, and that is where the 3 to 5 times comes from ([§2](#2-results)).
> - **The VM's own ext4 disk is faster than the Mac for small files**: 20,000 files are created
>   in 0.2 s on it against 3.0 s natively and 11.1 s on the shared workspace. ripgrep over the
>   benchmark's 100,000-file tree takes 0.08 s there, 2.5 s natively and 14.8 s shared.
> - **A Python monorepo's hot trees all sit on the slow path today**: the virtualenv,
>   `node_modules`, the build tool's caches and the jail's `~/.cache` ([§3](#3-what-this-means-for-a-large-python-and-django-monorepo)). Python imports from
>   the shared workspace took 2.4 times as long as from ext4.
> - **CPU work is native, and process start is faster than the Mac's** (the benchmark's M9 to
>   M12), so the VM's cost for this kind of work is almost entirely the file path and the memory it
>   keeps.

## Terms

- **virtiofs** — the Linux file-sharing protocol Apple Container uses to show a Mac folder
  inside the VM. Each file operation the guest cannot answer from its own cache is a request to
  the Mac. yolo shares three folders this way: the workspace (`/workspace`), the jail's home
  (`/home/agent`) and its cache (`/home/agent/.cache`)
  ([assemble_parts.go](../../internal/cli/run/assemble_parts.go)).
- **VM-local ext4** — a disk image attached to the VM as a block device and formatted ext4 inside
  it, so the guest's own kernel serves every file operation. yolo mounts one, the named volume
  `yolo-mise-data-v2` at `/mise`, for mise's tool installs.
- **tmpfs** — a filesystem held in RAM. Every scratch folder of an Apple Container jail (`/tmp`,
  `/var/tmp` and the rest) is one, and in a VM its pages are kept until the jail stops
  ([§2.2 of the benchmark](macos-backend-performance.md#22-memory-backed-on-first-touch-kept-until-the-container-stops)).
- **`F_FULLFSYNC`** — the macOS `fcntl` that makes a drive flush its own write cache. A plain
  `fsync` on macOS does not, and Node's `fs.fsyncSync` and `fdatasyncSync` use `F_FULLFSYNC`.
- **Per-file cost** (coined here) — the time an operation on one small file takes, beyond the
  bytes it moves.

## 1. The setup

The benchmark's Mac: Apple M1 Max, 32 GiB, macOS 26.5 (25F71), `container` CLI 1.1.0, yolo
`0.11.1+15.g5ca9b748`; one Apple Container jail with 5 CPUs and a 16 GiB cap, and yolo's builder VM
running beside it. CrowdStrike Falcon's endpoint-security extension was active, which touches
native file operations and not the guest's ext4 or tmpfs. Native ran on APFS in the same
workspace folder. Each figure is one run of each script unless it shows a range (two runs).

**[`fs.js`](#appendix-fsjs)** times, in one folder: a 1 GiB sequential write with one `fsync` and
its read; creating 20,000 4 KiB files in 200 folders; `stat` on each; `stat` on 20,000 missing
names (an import search's common case); reading each; listing the 200 folders; deleting the
files; and 2,000 appends of 8 KiB, each followed by `fdatasync` (a database's write-ahead log).
**[`imp.py`](#appendix-imppy)** times a fresh Python process importing 40 standard-library
packages from a copied standard library, behind 10 empty `sys.path` entries.

## 2. Results

### 2.1 Per-file cost, not bandwidth

| `fs.js` | native APFS | shared workspace (virtiofs) | VM-local ext4 (`/mise`) | tmpfs (`/tmp`) |
| :--- | ---: | ---: | ---: | ---: |
| sequential write, MB/s | 4263-4601 | 1285 | 1754-2232 | 3396 |
| sequential read, MB/s | 8683-8954 | 1622 | 2759-3564 | 2596 |
| create 20,000 files | 3.0 s | **11.1 s** | 0.13-0.22 s | 0.07 s |
| `stat` 20,000 | 85-90 ms | **2150 ms** | 19 ms | 17 ms |
| `stat` 20,000 missing | 170 ms | **1341 ms** | 106-110 ms | 95 ms |
| open and read 20,000 | 1255 ms | **6163 ms** | 85-90 ms | 80 ms |
| list 200 folders | 30 ms | 179 ms | 3-4 ms | 3 ms |
| delete 20,000 | 1900 ms | **4867 ms** | 41-42 ms | 24 ms |
| `fdatasync` after each 8 KiB, per s | 248 | 3986 | 4151-4926 | 603,462 |

MEASURED. What it shows:

- **Bandwidth costs a factor of 3 to 5.5**, but few development tasks move gigabytes.
- **Per-file operations on the shared workspace cost 12 to 115 times ext4's, and 2.5 to 25
  times native's.** `stat` is 25 times native: each one is about 107 µs, against 4 µs natively and 1 µs
  on ext4. This is the cost behind the benchmark's M5 to M7.
- **ext4 inside the VM beats native APFS for small files**, by 1.6 times (missing names) to 45
  times (deletes). The guest kernel
  answers from its own caches, and macOS's per-file path (APFS plus the endpoint-security
  extension) is the slower one.
- **Syncs are not comparable as speed.** Native's 248 per second is `F_FULLFSYNC` reaching the
  drive. The guest's 4,000 to 5,000 means its `fdatasync` does not reach the drive's cache
  (INFERRED from the rate; the path below the VM was not read). For a development database that is
  speed bought with durability on power loss, which is usually the right trade.

### 2.2 The benchmark's tree on each filesystem

The benchmark's 100,000-file git repository, copied onto `/mise` (the copy itself took 61 s,
from the shared workspace):

| | native | shared workspace | VM-local ext4 |
| :--- | ---: | ---: | ---: |
| ripgrep over the tree | 2.5 s | 14.8 s | **0.08 s** |
| `git status`, first | - | 29.3 s | 0.54 s |
| `git status`, again | 0.2 s | 1.0 s | **0.035-0.037 s** |

MEASURED, one run each. The first `git status` on the shared workspace, in a jail that had not
read the tree before, took half a minute.

### 2.3 Python imports

| `imp.py`, 40 packages | first process | median of the next 5 |
| :--- | ---: | ---: |
| native APFS (Python 3.13) | 660 ms | 117 ms |
| shared workspace (Python 3.14) | 281 ms | **273 ms** |
| VM-local ext4 (Python 3.14) | - | 112 ms |

MEASURED. From the shared workspace every fresh process pays about 160 ms more than from ext4 for
these 40 packages. Natively the first process paid an extra half second, the first-exec scan the
benchmark's M12 measured; the jail never does. The two Python versions differ, which this
comparison does not control; ext4 against the shared workspace is the same interpreter.

## 3. What this means for a large Python and Django monorepo

The workload the maintainer asked about, described generically: a Python and Django monorepo with
a Node front end, built and tested with Pants, running Postgres and a dozen or so Django processes
under a process manager. Its working tree is about 200,000 files, most of them in two folders: the
virtualenv (about 84,000) and the front end's `node_modules` (about 95,000). All INFERRED from [§2](#2-results)
and from reading that repository's layout.

- **Every hot tree is on the slow path.** The virtualenv, `node_modules` and Pants's work folders
  are in the workspace; Pants's caches are under `~/.cache`. All are virtiofs.
- **Imports multiply.** A Django process imports thousands of modules, and Python's search tries
  several names in each `sys.path` entry for each one, most of them missing. A dozen processes,
  each test worker and each Pants subprocess pay that from scratch. [§2.3](#23-python-imports)'s 160 ms per 40 packages
  becomes seconds per process.
- **The host's virtualenv cannot be used in the jail.** It holds macOS binaries, and a Linux one
  at the same path would overwrite it, so the two sides need separate trees.
- **Postgres belongs on ext4.** Its data folder in the workspace pays the per-file cost on every
  table and index file; on a VM-local disk it gets [§2.1](#21-per-file-cost-not-bandwidth)'s 4,000 to 5,000 syncs per second.
- **Memory fills and stays full.** Test runs and builds fill the guest's file cache, and `/tmp`
  is RAM. The jail grows to its 16 GiB cap on a 32 GiB Mac and keeps it until it stops (the
  benchmark's M4).
- **CPU-heavy work is fine.** One thread runs at native speed, and starting processes is faster
  than on the Mac. A parallel build gets half the cores by default.

**macos-user is the other answer.** It ran at native speed on every metric
([the macos-user run](macos-backend-performance.md#macos-user-2026-10-03)), so the host's own virtualenv, a native
Postgres and the existing macOS setup work as they do outside a jail. In exchange it is macOS, not
Linux, and needs `sudo` at every launch.

## 4. A design sketch: VM-local volumes for chosen workspace folders

Not a plan; what the numbers suggest. A per-workspace setting naming workspace-relative folders
(`.venv`, `node_modules`, the build tool's folders, a database's data folder) and `~/.cache`. On
Apple Container each would be backed by a named ext4 volume mounted over that path inside the jail.
Its costs:

- **The host no longer sees those folders' contents.** The host's editor would see an empty
  folder or the host's own copy. For a virtualenv that is the point, since the two sides need
  different binaries anyway.
- **A volume can be attached to only one running VM**
  ([§7 of the benchmark](macos-backend-performance.md#7-found-on-the-way-two-apple-container-jails-may-mount-one-ext4-disk)),
  so two jails of one workspace would need separate volumes or a refusal.
- **Today no key does this.** The `mounts` key accepts writable entries only under `/ctx`, and
  only at user scope, and the only VM-local volume yolo mounts is `/mise`.

On podman these folders are already on the Linux host's own filesystem, so the setting would do
nothing there.

## Appendix: fs.js

Run with `node fs.js <folder> <label>`; it writes and deletes `fsbench-<pid>` inside the folder.
In the jail, `/workspace` is virtiofs, `/mise` is ext4 and `/tmp` is tmpfs; the root filesystem
is not writable by the agent.

```js
// fs.js <dir> <label>: throughput, small-file and fsync costs of one filesystem.
const fs = require('fs'), path = require('path');
const [dir, label] = process.argv.slice(2);
const root = path.join(dir, `fsbench-${process.pid}`);
fs.mkdirSync(root, { recursive: true });
const t = (name, f) => { const s = process.hrtime.bigint(); const n = f(); const ms = Number(process.hrtime.bigint() - s) / 1e6;
  console.log(`${label}\t${name}\t${ms.toFixed(0)} ms${n ? `\t${n}` : ''}`); return ms; };
const MB = 1 << 20, SEQ = 1024, chunk = Buffer.alloc(64 * MB, 7);
const big = path.join(root, 'big');
let ms = t('seq_write_1GiB+fsync', () => { const fd = fs.openSync(big, 'w'); for (let i = 0; i < SEQ / 64; i++) fs.writeSync(fd, chunk); fs.fsyncSync(fd); fs.closeSync(fd); });
console.log(`${label}\tseq_write_MBps\t${(SEQ / ms * 1000).toFixed(0)}`);
ms = t('seq_read_1GiB', () => { const fd = fs.openSync(big, 'r'); const b = Buffer.alloc(64 * MB); while (fs.readSync(fd, b) > 0); fs.closeSync(fd); });
console.log(`${label}\tseq_read_MBps\t${(SEQ / ms * 1000).toFixed(0)}`);
fs.rmSync(big);
const N = 20000, small = Buffer.alloc(4096, 1), files = [];
for (let i = 0; i < N; i++) files.push(path.join(root, 'd' + (i % 200), 'f' + i + '.py'));
t('mkdir+create_20k_4KiB', () => { for (let d = 0; d < 200; d++) fs.mkdirSync(path.join(root, 'd' + d)); for (const f of files) fs.writeFileSync(f, small); });
t('stat_20k', () => { for (const f of files) fs.statSync(f); });
t('stat_missing_20k', () => { for (const f of files) try { fs.statSync(f + 'c'); } catch {} });
t('open+read_20k', () => { for (const f of files) fs.readFileSync(f); });
t('readdir_200', () => { for (let d = 0; d < 200; d++) fs.readdirSync(path.join(root, 'd' + d)); });
t('unlink_20k', () => { for (const f of files) fs.unlinkSync(f); });
const wal = path.join(root, 'wal'), page = Buffer.alloc(8192, 3), K = 2000;
ms = t('fdatasync_2000x8KiB', () => { const fd = fs.openSync(wal, 'w'); for (let i = 0; i < K; i++) { fs.writeSync(fd, page); fs.fdatasyncSync(fd); } fs.closeSync(fd); });
console.log(`${label}\tsyncs_per_s\t${(K / ms * 1000).toFixed(0)}`);
fs.rmSync(root, { recursive: true, force: true });
```

## Appendix: imp.py

Copy a Python standard library into the folder under test first (`cp -R "$(python3 -c 'import
os; print(os.path.dirname(os.__file__))')" <folder>/lib`), then run `python3 imp.py <folder>/lib
<label>`.

```python
# imp.py <stdlib-copy> <label>: cold-process import of ~40 stdlib packages from a copied stdlib,
# with 10 empty sys.path entries ahead of it (a venv's search path has several).
import sys, os, time, subprocess
lib, label = sys.argv[1], sys.argv[2]
empties = [os.path.join(os.path.dirname(lib), f"empty{i}") for i in range(10)]
for e in empties: os.makedirs(e, exist_ok=True)
mods = "asyncio email.mime.multipart http.server json logging.handlers unittest.mock xml.dom.minidom sqlite3 decimal argparse multiprocessing concurrent.futures urllib.request ssl csv tomllib zipfile tarfile pydoc dataclasses typing inspect ast difflib calendar gettext locale shutil tempfile uuid hashlib hmac secrets statistics fractions pathlib pprint configparser smtplib imaplib ftplib wsgiref.simple_server xmlrpc.client html.parser"
code = f"import sys; sys.path[:0] = {empties + [lib]!r}\n" + "\n".join(f"import {m}" for m in mods.split())
times = []
for i in range(6):
    s = time.perf_counter(); subprocess.run([sys.executable, "-I", "-S", "-c", code], check=True); times.append(time.perf_counter() - s)
print(f"{label}\timport_40_pkgs\tfirst {times[0]*1000:.0f} ms\twarm median {sorted(times[1:])[2]*1000:.0f} ms")
```

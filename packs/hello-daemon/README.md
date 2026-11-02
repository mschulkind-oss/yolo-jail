# `hello-daemon` — the pack-shipped-jail-binary experiment

An **experiment**, not a feature. It is
the second sequencing step of the design behind
[`pack-binaries.md`](../../docs/reference/pack-binaries.md#a-packs-own-tree-cannot-carry-an-executable), quoted whole:

> **Second, prove P1 cheaply** — a throwaway local pack whose `jail_daemon.cmd` is
> `["{jail_loophole_dir}/bin/hello"]`, carrying a statically linked two-line binary.
> This is an afternoon, and it converts "the mechanism appears to exist" into "the
> mechanism works".

[The design's inventory](../../docs/reference/pack-binaries.md#a-packs-own-tree-cannot-carry-an-executable) said every clause a pack-shipped jail binary needs is already built —
the module dir is bind-mounted `:ro` **without `noexec`**, `{jail_loophole_dir}` is
legal in `jail_daemon.cmd`, the pack-shipped subset never constrained `jail_daemon`,
and `yolo-jaild supervise` spawns the argv with no per-loophole code — and then says
the honest thing about that list: **"nothing has ever tried."** This pack is the
trying, and the answer turned out to be *"yes through one delivery route, and no
through the other."*

## The result

**A pack can ship the executable its jail daemon runs, by either route.** When this ran,
only a pack selected BY PATH could; the same directory selected by BARE NAME could not,
because the embedded channel stripped the execute bit. That gap closed on 2026-10-17.

| Route | How it is selected | What reaches the jail |
|---|---|---|
| **configured** | `"packs": ["/abs/path/to/packs/hello-daemon"]` | `bin/hello` at `0755` — `packstage.copyFile` carries the `0o111` bits |
| **embedded** | `"packs": ["hello-daemon"]` | `bin/hello` at `0755` — [`packs/executables.txt`](../executables.txt) records the bit |

There were two causes on the embedded route:

1. **`embed.FS` cannot represent an exec bit at all.** It reports `0444` for every
   file and `0555` for every directory, whatever the mode on disk — measured, not
   inferred: a `0755` source file embedded with `//go:embed all:sub` reads back as
   `perm=0444`. That is still true, so the bit now travels beside the bytes:
   `packs/executables.txt` lists every embedded file whose repository mode carries one,
   and a test fails when the list and the tree disagree.
2. **`packload.copyEmbeddedTree` wrote `0o644` unconditionally.** It now restores the
   listed bit, and the read-only cache tree seals such a file `0555` rather than `0444`.

**The failure was silent when this ran, and is not now.** A start that fails —
`permission denied` from `cmd.Start` on a non-executable file — used to take a
spawn-failure arm that discarded the error and retried for the life of the jail, leaving
`~/.local/state/yolo-jail-daemons/hello-daemon.log` empty. That arm now writes
`spawn failed: …` to the log and obeys the restart policy, so this manifest's
`"restart": "no"` gives up after one line that names the error
([the reference](../../docs/reference/pack-binaries.md#a-packs-own-tree-cannot-carry-an-executable) records the fix).

**A compiled program still takes a download** (2026-09-30,
[`pack-binaries.md`](../../docs/reference/pack-binaries.md), [BP-D1](../../docs/reference/pack-binaries.md#BP-D1)). A pack shipping a
compiled program declares it as a DOWNLOAD in the loophole's
manifest — `binaries`, one build per platform with a mandatory `sha256` — and names it
`{jail_binary:<name>}` in `jail_daemon.cmd`. `yolo pack install` fetches and verifies each
build and caches it with its exec bit, and the launch mounts that file into the jail, so
one build per platform reaches the jail. This pack keeps its checked-in script because a
script needs no per-platform build.

## Why it ships a script and not an ELF binary

[The design](../../docs/reference/pack-binaries.md#a-packs-own-tree-cannot-carry-an-executable) said "statically linked two-line binary". This ships a two-line `#!/bin/sh`
program instead, and the substitution is deliberate.

Every property the experiment is about is **identical** for the two: a file the pack
ships, delivered through pack staging, mounted `:ro` into the container, named by
`{jail_loophole_dir}`, and executed by the kernel off that read-only bind. A `#!`
script needs the execute bit exactly as an ELF image does — which is why this pack
found the gap above rather than dodging it.

What a committed ELF would have cost is the reason not to: `packs/` is inside
[`flake.nix`](../../flake.nix)'s `goSrc` fileset **and** inside
[`packs/embed.go`](../embed.go), so the blob would ride in every `yolo` binary and every
hermetic image build — once per platform, under [the design's](../../docs/reference/pack-binaries.md#a-packs-own-tree-cannot-carry-an-executable) `bin/<goos>-<goarch>/`
convention — forever, for a throwaway.

**What the script therefore leaves unproven is the ELF half**: that a non-nix,
dynamically-linked binary finds its interpreter through `nix-ld` inside the jail. [The design](../../docs/reference/pack-binaries.md#a-packs-own-tree-cannot-carry-an-executable)
claims that from `nix-ld`'s shipped behaviour; this pack does not test it. A
*statically* linked binary needs no interpreter and should need nothing beyond what is
measured here.

## Running it

Select the pack, by path or by its bare name; its loophole runs whenever the pack is
selected, and it declares no grant:

```toml
# ~/.config/yolo-jail/config.toml
packs = ["claude", "/home/you/code/yolo-jail/packs/hello-daemon"]
```

```console
$ yolo loopholes list                                    # hello-daemon, source `pack`
$ yolo -- bash
$ cat ~/.local/state/yolo-jail-daemons/hello-daemon.log
hello from /etc/yolo-jail/loopholes/hello-daemon/bin/hello (pid 41) at ...
```

That one line is the whole proof, and it carries four facts at once: the module dir was
mounted, `{jail_loophole_dir}` resolved to the container path, the `:ro` bind permitted
execution, and the supervisor ran a program **this pack shipped**. Everything up to the
launch itself is pinned by
[`internal/packload/packshippedjailbinary_test.go`](../../internal/packload/packshippedjailbinary_test.go);
that log line is the part only a real jail can produce.

On **`macos-user`** the same config runs it in the sandbox. There is no mount there:
`{jail_loophole_dir}` resolves to the module dir's place in this launch's root-owned copy of
the staged packs. Its guest root uses this host tree's unique directory leaf and is reserved
exclusively, so a later launch cannot replace it. For the embedded `hello-daemon` pack, the
illustrative guest path is `/var/yolo-jail/packs/<jail>.<host-tree-leaf>/_official/hello-daemon/loopholes/hello-daemon/bin/hello`.
The bootstrap and session environments name that same root with `YOLO_PACK_ROOT`
([JD-10](../../docs/reference/macos-user-provisioning.md#JD-10)). The log remains at
`<workspace>/.yolo/home/local/state/yolo-jail-daemons/hello-daemon.log` on the Mac. This describes
source behavior, not a native run: root ownership and modes, Seatbelt execution and
overlapping-session restart behavior remain **UNMEASURED**. The code allows a macOS script there;
a Linux binary would not run, and the launch declines one by name and names a container runtime
that runs it.

## Retiring it

Delete the directory, its entry in [`packs/embed.go`](../embed.go) and its line in
[`packs/executables.txt`](../executables.txt), which refuses to load every shipped pack while it
names a missing file. Nothing depends
on it: the loophole crosses nothing, no other pack names it, and the test above builds
its own fixtures from this tree — it will fail loudly rather than pass vacuously if the
tree goes.

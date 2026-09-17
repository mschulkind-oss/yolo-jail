# `hello-daemon` — the pack-shipped-jail-binary experiment

An **experiment**, not a feature. It is
[`broker-as-a-pack.md`](../../docs/design/broker-as-a-pack.md) §10's second sequencing
step, quoted whole:

> **Second, prove P1 cheaply** — a throwaway local pack whose `jail_daemon.cmd` is
> `["{jail_loophole_dir}/bin/hello"]`, carrying a statically linked two-line binary.
> This is an afternoon, and it converts "the mechanism appears to exist" into "the
> mechanism works".

§3's inventory says every clause a pack-shipped jail binary needs is already built —
the module dir is bind-mounted `:ro` **without `noexec`**, `{jail_loophole_dir}` is
legal in `jail_daemon.cmd`, the pack-shipped subset never constrained `jail_daemon`,
and `yolo-jaild supervise` spawns the argv with no per-loophole code — and then says
the honest thing about that list: **"nothing has ever tried."** This pack is the
trying, and the answer turned out to be *"yes through one delivery route, and no
through the other."*

## The result

**A pack can ship the executable its jail daemon runs — but only a pack selected BY
PATH.** The same directory selected by BARE NAME cannot, because the embedded channel
strips the execute bit and nothing puts it back.

| Route | How it is selected | What reaches the jail |
|---|---|---|
| **configured** | `"packs": ["/abs/path/to/packs/hello-daemon"]` | `bin/hello` at `0755` — `packstage.copyFile` carries the `0o111` bits |
| **embedded** | `"packs": ["hello-daemon"]` | `bin/hello` at `0644` — **it cannot be executed** |

Two independent causes sit on the embedded route, and the second one would still bite
if the first were fixed:

1. **`embed.FS` cannot represent an exec bit at all.** It reports `0444` for every
   file and `0555` for every directory, whatever the mode on disk — measured, not
   inferred: a `0755` source file embedded with `//go:embed all:sub` reads back as
   `perm=0444`. So no mode-preserving copier could recover it; the bit is gone before
   `packs.FS` is ever read.
2. **`packload.copyEmbeddedTree` writes `0o644` unconditionally**, and the comment
   justifying it — *"packstage enforces the same rule for configured packs"* — is no
   longer true of `packstage`, which carries the bit deliberately (its `copyFile` doc:
   *"that is what made a pack unable to ship a working script through any channel"*).
   `internal/cli/run`'s own `copyTree`, which moves the materialized tree into the
   staged one, already carries the bit too. packload's is the last stripper standing,
   and its stated reason has moved out from under it.

**The failure is silent.** `supervisor.superviseOne` consults the restart policy only
after a *successful* start; a start that fails — `permission denied` from
`cmd.Start` on a non-executable file — takes the spawn-failure arm, which discards the
error, sleeps, doubles a `1s→30s` backoff and retries **for the life of the jail**.
`openLog` has already created `~/.local/state/yolo-jail-daemons/hello-daemon.log`, and
nothing is ever written to it. So the observable symptom of shipping a jail binary the
wrong way is an empty log file and no process, forever, with no diagnostic anywhere.

## Why it ships a script and not an ELF binary

§10 says "statically linked two-line binary". This ships a two-line `#!/bin/sh`
program instead, and the substitution is deliberate.

Every property the experiment is about is **identical** for the two: a file the pack
ships, delivered through pack staging, mounted `:ro` into the container, named by
`{jail_loophole_dir}`, and executed by the kernel off that read-only bind. A `#!`
script needs the execute bit exactly as an ELF image does — which is why this pack
still finds the gap above rather than dodging it.

What a committed ELF would have cost is the reason not to: `packs/` is inside
[`flake.nix`](../../flake.nix)'s `goSrc` fileset **and** inside
[`packs/embed.go`](../embed.go), so the blob would ride in every `yolo` binary and every
hermetic image build — once per platform, under §3.1's `bin/<goos>-<goarch>/`
convention — forever, for a throwaway. And it would not have worked anyway: the embed
channel would have stripped its exec bit just the same.

**What the script therefore leaves unproven is the ELF half**: that a non-nix,
dynamically-linked binary finds its interpreter through `nix-ld` inside the jail. §3
claims that from `nix-ld`'s shipped behaviour; this pack does not test it. A
*statically* linked binary needs no interpreter and should need nothing beyond what is
measured here.

## Running it

It is off behind two gates. Select the pack **by path** (the bare name takes the
embedded route, which is the one that cannot work), then enable the loophole:

```jsonc
// ~/.config/yolo-jail/config.jsonc
{
  "packs": ["claude", "/home/you/code/yolo-jail/packs/hello-daemon"],
  "loopholes": { "hello-daemon": { "enabled": true } }
}
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

## Retiring it

Delete the directory and its entry in [`packs/embed.go`](../embed.go). Nothing depends
on it: the loophole crosses nothing, no other pack names it, and the test above builds
its own fixtures from this tree — it will fail loudly rather than pass vacuously if the
tree goes.

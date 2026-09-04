---
name: developing-yolo-jail
description: "Build, deploy, and verify changes to yolo-jail's own Go code (cmd/, internal/) or flake.nix: build-go vs deploy, the baked install prefix, the goSrc trap, nested-jail verification. Use when editing this repo's source."
---

# Developing yolo-jail

This jail is running against the yolo-jail source tree itself. `/workspace` is
bind-mounted live. The image bakes the CLI as real-file copies at
`/opt/yolo-jail/bin/` (with `/bin/<name>` symlinks and the flake bundle at
`/opt/yolo-jail/share/yolo-jail`) — there is **no `/opt/yolo-jail` source bind
and no dev-override wrapper any more**. A nested jail rebuilds the live checkout
from source and runs THAT image — **but only when you pass
`YOLO_REPO_ROOT=/workspace`, and only from a throwaway workspace** (see "The
flake a launch builds from" and "Never launch a nested jail on `/workspace`"
below). Then your edited Go code is what runs. These are
the build/deploy traps that have no `yolo --help` home — the authoritative
version lives in `/workspace/AGENTS.md` (bind-mounted, always current); read it
for the full detail.

## Verifying a change on macos-user

A macos-user jail **cannot launch a jail**, so the nested-jail loop does not exist
there. Two kernel rules, not a missing feature: `sudo` cannot exec inside any
Seatbelt sandbox, and `sandbox_apply` refuses a profile that differs from the active
one. Nesting is unavailable however the code is written.

What DOES work from inside, and covers most of the backend:

- `yolo run --dry-run` — the whole plan, invariants included.
- `yolo internal darwin-bootstrap` with `HOME`/`JAIL_HOME` pointed at a temp dir —
  the real generators, the real shims, the real overlay install.
- the staging argv from the plan, with `sudo` dropped and the destination
  redirected — the copies, the modes, and the fresh-inode rule.

Both of the last two are automated (`internal/entrypoint/darwinbootstrap_darwin_test.go`,
`internal/macosuser/staging_darwin_test.go`) and run on any Mac with no privilege.

What is left needs a password and is four commands:
`docs/plans/runbooks/macos-user-manual-checks.md`.

## Build vs. deploy — they are not the same

- `just build-go` → `dist-go/<goos>-<goarch>/` — the **cross-compile** step,
  now purely **for shipping** (prebuilt `bin/linux-<arch>` artifacts a shipped
  bundle consumes). It does **NOT** feed any in-jail run — a nested jail
  compiles the checkout from source itself.
- `just deploy` does **NOT** cross-compile — it is `just install`
  (host `go install ./cmd/yolo`) plus Claude-broker priming.

## What iterates in-jail vs. needs a host rebuild

- **All four binaries iterate the same way now** (`yolo`, `yolo-entrypoint`,
  `yolo-jaild`, `yolo-ps`): the dev-override fast loop is gone, so the outer
  jail's binaries are **frozen at the host-loaded image** — you cannot
  live-patch them in-jail. Verify any Go change by launching a **nested** jail.
- Both Go and `flake.nix` changes are verifiable in a nested
  `YOLO_REPO_ROOT=/workspace yolo -- bash` (run from `/tmp/yolo-nested`, not
  `/workspace`): its `AutoLoadImage` runs
  `nix build .#ociImage --impure` on the live `/workspace` checkout (nix
  delegates to the host daemon), notices the store path changed, and loads + runs
  the **freshly built** image in the nested podman — carrying your edits for all
  four binaries. Watch the build output.
- A host `just load` is only needed to **ship** the change to the maintainer's
  own day-to-day jails — not to validate it.

## Traps that fail silently

- **The goSrc fileset trap** (`flake.nix`): the hermetic image build only sees
  `go.mod`, `go.sum`, `vendor/`, `cmd/`, `internal/`, and `packs/`.
  A Go package outside that set vanishes from the image while `go build ./...`
  stays green. Add new top-level packages to the fileset by hand. (Content under
  `internal/` and `cmd/` is already covered.)
- **A failed nix build STOPS the jail** (fatal since 2026-08-15): it prints
  nix's own stderr plus a classification and exits, rather than falling back to
  the loaded image or the newest cached tar. It used to fall back, and a broken
  build then looked like a working jail running **stale** code.
  `YOLO_ALLOW_STALE_IMAGE=1` opts back into continuing, loudly.
- **`vendor/` is committed and the build is hermetic** (`-mod=vendor`, no
  network). A new dependency needs `go mod vendor` committed or the image build
  breaks while `go test` still passes.

## The flake a launch builds from

**The cwd does not choose it** (since 2026-08-31). Resolution is, in order:
`YOLO_REPO_ROOT`, then a `share/yolo-jail` bundle beside the binary (in-jail:
the baked `/opt/yolo-jail` prefix), then `~/.local/share/yolo-jail/flake-bundle`.
Standing in `/workspace` means nothing to it.

So **in-jail, a bare `yolo` builds from the BAKED bundle** — the image you
already have, not your edits. To verify anything you changed, name the live tree:

    mkdir -p /tmp/yolo-nested && cd /tmp/yolo-nested
    YOLO_REPO_ROOT=/workspace yolo -- bash

Every launch prints the one it took, before the build starts:

    Flake source: /workspace (YOLO_REPO_ROOT)
    Flake source: /opt/yolo-jail/share/yolo-jail (flake bundle beside the binary)

**Read that line before believing a nested green.** The second spelling means
you verified the baked image against itself.

## Never launch a nested jail on `/workspace`

`cd` somewhere else first. **Your own home is a directory inside this
workspace**: the per-workspace home overlay is `<workspace>/.yolo/home`, so
`/workspace/.yolo/home/claude` and `/home/agent/.claude` are the same inode
(check it: `stat -c '%d:%i' /home/agent/.claude /workspace/.yolo/home/claude`).
A nested launch on `/workspace` regenerates agent config straight over the
session you are running in — on 2026-07-21 that deleted 479 Claude history
entries mid-session, along with `~/.yolo-ca-bundle.crt`.

Any other workspace gets its own `.yolo/home` and cannot reach ours.
`/tmp/yolo-nested` is a good one: `/tmp` is a tmpfs, so it is always writable and
leaves nothing behind. This is not merely safer — a fresh workspace exercises
first-boot provisioning, which `/workspace` (already provisioned by the jail you
are sitting in) skips.

Since the flake source is now named by `YOLO_REPO_ROOT` rather than found from
the cwd, the workspace and the source tree are independent. Before that they were
welded together: the only way to build a nested image from live source was to
launch on `/workspace`, which is exactly what made the collision unavoidable.

## Verification is mandatory

For any `cmd/` or `internal/` change, **verify with a nested jail**. After
`just build-go`, run the **freshly-built binary BY PATH**, pointed at the live
tree:

    just build-go
    mkdir -p /tmp/yolo-nested && cd /tmp/yolo-nested
    YOLO_REPO_ROOT=/workspace /workspace/dist-go/linux-$(go env GOARCH)/yolo -- bash

(Without the env var this one does not merely test the wrong thing — it refuses:
`dist-go/` has no bundle beside it and the jail home has no staged one. Without
the `cd`, it eats your home — see above.)

Mount failures, permission errors, and read-only-fs conflicts only appear when a
container actually starts — unit tests do not catch them.

**Why by path, not bare `yolo`:** bare `yolo` is the baked `/bin/yolo`,
version-locked to THIS jail's image (frozen at the last host `just load`). It is
the LAUNCHER — it builds the `podman run` argv. A change to launcher/argv
construction (mounts, env, flags) is NOT in the baked binary, only in your fresh
build. Running the fresh binary by path exercises YOUR argv against the nested
image (which the run rebuilds from live source, given `YOLO_REPO_ROOT`).
Verifying a launcher change via bare `yolo` silently tests the OLD launcher — and a stale launcher emitting an
argv the new image rejects is exactly how a fixed jail looks broken.

**NEVER `just install` inside a jail.** In-jail (`YOLO_VERSION` set) the recipe
refuses, by design: `go install` drops a copy in `$GOBIN` — a mise Go dir that
sits AHEAD of `/bin` on PATH and is host-shared + persistent — silently
shadowing the baked `/bin/yolo` with a possibly-stale binary that lingers across
sessions. In-jail you rebuild the IMAGE, never a GOBIN binary. (If bare `yolo`
ever misbehaves in-jail, check `command -v yolo` resolves to the mise shim →
`/bin/yolo`; a hit under `/mise/installs/go/*/bin/yolo` is this shadow — delete
it.)

## Stop and hand off for host-side steps

`just load` and `just install` run on the **host**, not in-jail. Once your
change is validated in a nested jail, **shipping** it to the maintainer's own
day-to-day jails needs a host `just load` (image changes) or `just install`
(host `yolo` binary). Finish your in-jail build + nested-jail verification, then
STOP and tell the human exactly what to run.

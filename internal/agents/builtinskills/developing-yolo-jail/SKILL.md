---
name: developing-yolo-jail
description: Build, deploy, and verify changes to yolo-jail's own Go code (cmd/, internal/) or flake.nix: build-go vs deploy, the baked install prefix, the goSrc trap, nested-jail verification. Use when editing this repo's source.
---

# Developing yolo-jail

This jail is running against the yolo-jail source tree itself. `/workspace` is
bind-mounted live. The image bakes the CLI as real-file copies at
`/opt/yolo-jail/bin/` (with `/bin/<name>` symlinks and the flake bundle at
`/opt/yolo-jail/share/yolo-jail`) — there is **no `/opt/yolo-jail` source bind
and no dev-override wrapper any more**. A nested jail rebuilds the live checkout
from source and runs THAT image, so your edited Go code is what runs. These are
the build/deploy traps that have no `yolo --help` home — the authoritative
version lives in `/workspace/AGENTS.md` (bind-mounted, always current); read it
for the full detail.

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
- Both Go and `flake.nix` changes are verifiable in a nested `yolo -- bash`:
  its `AutoLoadImage` runs `nix build .#ociImage --impure` on the live
  `/workspace` checkout (nix delegates to the host daemon), notices the store
  path changed, and loads + runs the **freshly built** image in the nested
  podman — carrying your edits for all four binaries. Watch the build output: a
  failed build silently falls back to stale code.
- A host `just load` is only needed to **ship** the change to the maintainer's
  own day-to-day jails — not to validate it.

## Traps that fail silently

- **The goSrc fileset trap** (`flake.nix`): the hermetic image build only sees
  `go.mod`, `go.sum`, `vendor/`, `cmd/`, `internal/`, and `bundled_loopholes/`.
  A Go package outside that set vanishes from the image while `go build ./...`
  stays green. Add new top-level packages to the fileset by hand. (Content under
  `internal/` and `cmd/` is already covered.)
- **A failed nix build does not stop the jail** — it falls back to the loaded
  image, then the newest cached tar. A broken build looks like a working jail
  running **stale** code. Only a real nested-jail run catches this.
- **`vendor/` is committed and the build is hermetic** (`-mod=vendor`, no
  network). A new dependency needs `go mod vendor` committed or the image build
  breaks while `go test` still passes.

## Verification is mandatory

For any `cmd/` or `internal/` change, **verify with a nested jail**: run
`yolo -- bash` from inside this jail. Mount failures, permission errors, and
read-only-fs conflicts only appear when a container actually starts — unit tests
do not catch them.

## Stop and hand off for host-side steps

`just load` and `just install` run on the **host**, not in-jail. Once your
change is validated in a nested jail, **shipping** it to the maintainer's own
day-to-day jails needs a host `just load` (image changes) or `just install`
(host `yolo` binary). Finish your in-jail build + nested-jail verification, then
STOP and tell the human exactly what to run.

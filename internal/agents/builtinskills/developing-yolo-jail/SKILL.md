---
name: developing-yolo-jail
description: Build, deploy, and verify changes to yolo-jail's own Go code (cmd/, internal/) or flake.nix: build-go vs deploy, dev-override wrappers, the goSrc trap, nested-jail verification. Use when editing this repo's source.
---

# Developing yolo-jail

This jail is running against the yolo-jail source tree itself. `/workspace` is
bind-mounted live and also backs `/opt/yolo-jail`, so nested jails run your
edited Go code via dev-override wrappers. These are the build/deploy traps that
have no `yolo --help` home — the authoritative version lives in
`/workspace/AGENTS.md` (bind-mounted, always current); read it for the full
detail.

## Build vs. deploy — they are not the same

- `just build-go` → `dist-go/<goos>-<goarch>/` — the **cross-compile** step.
  This is what makes edited Go code visible to a nested jail.
- `just deploy` does **NOT** cross-compile — it is `just install`
  (host `go install ./cmd/yolo`) plus Claude-broker priming.

## What iterates in-jail vs. needs a host rebuild

- **Dev-override wrappers exist for `yolo` and `yolo-entrypoint` only.** They
  prefer `/opt/yolo-jail/dist-go/linux-<arch>/` over the baked binary, so those
  two iterate with `just build-go` alone.
- `yolo-jaild` and `yolo-ps` are plain symlinks to the baked build — editing
  them requires a full image rebuild (`just load` on the host).
- `flake.nix` changes: **build + eval in-jail** (`nix build .#ociImage
  --impure` works — nix delegates to the host daemon), but **load + run the new
  image on the host** (`just load`). A nested `yolo -- bash` reuses the current
  baked image, not your freshly built one.

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

`just load` and `just install` run on the **host**, not in-jail. When your change
needs them (any `flake.nix` change, or editing `yolo-jaild`/`yolo-ps`), finish
your in-jail build + eval, then STOP and tell the human exactly what to run.

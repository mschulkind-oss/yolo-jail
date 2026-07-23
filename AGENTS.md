# YOLO Jail: Agent Developer Guide

yolo-jail runs coding agents in an isolated container against a live-mounted
workspace, without exposing host credentials or identity. Six agents are
supported (`claude`, `copilot`, `gemini`, `opencode`, `pi`, `codex`); the
`agents` config key selects which get installed, and that selection gates
overlay dirs, briefings, and skills. Backends are `podman`, `container`
(Apple Container), and `macos-user` (macOS Seatbelt, no VM) — **Docker was
removed**; `internal/config/validate.go` hard-errors on it.

**This file is the guide for developing yolo-jail itself.** It deliberately does
not restate usage or config reference material — see "Where things live" below.

## Architecture

Five commands in `cmd/`, ~43 packages (`go list ./...`). Everything is Go; the
only bash/Python is generated *content* (shims, `.bashrc`, `yolo-cglimit`)
emitted by `internal/entrypoint`.

| Binary | Runs where | Role |
|---|---|---|
| `yolo` | host **and** in-jail | the CLI; also every host daemon (see below) |
| `yolo-entrypoint` | container PID 1-ish | provisions the jail at startup |
| `yolo-jaild` | container | in-jail daemons |
| `yolo-ps` | container | host-process view (the `host-processes` loophole) |
| `goprobe` | nowhere | deployment tripwire; excluded from runtime PATH |

**Host ship set is just `{yolo}`** — `just install` runs `go install ./cmd/yolo`
and nothing else. The other four are image-side only.

**Daemons are subcommands, not separate binaries.** Host daemons are hidden
self-exec subcommands of `yolo`:
`yolo internal daemon <claude-oauth-broker|host-processes|broker-relay|journal>`,
under `yolo internal <config-dump|daemon|migrate-host>`. In-jail daemons are
`yolo-jaild <supervise|oauth-terminator>` (`supervise` reads `YOLO_JAIL_DAEMONS`).
Both dispatch on plain `args[0]` — **not** argv[0]/symlink. Easy to get wrong.

CLI code lives under `internal/cli` (top level), `internal/cli/run` (the run
pipeline: assemble, mounts, lifecycle, host ports), and `internal/cli/check`.

**Self-bootstrapping:** this project is developed from inside its own jail.
`/workspace` is bind-mounted live, so edits are visible on the host instantly —
there is no sync step.

## Build & deploy — the traps

- `just build-go` → `scripts/build-go.sh` → `dist-go/<goos>-<goarch>/`. This is
  the cross-compile step. **`just deploy` does NOT cross-compile** — it is
  `just install` (host `go install ./cmd/yolo`) plus Claude-broker priming.
- **Dev-override wrappers exist for `yolo` and `yolo-entrypoint` only.** They
  prefer `/opt/yolo-jail/dist-go/linux-<arch>/` over the baked binary, so those
  two iterate with `just build-go` alone. `yolo-jaild` and `yolo-ps` are plain
  symlinks to the baked build (`goBinariesLinks` in `flake.nix`) — **editing
  them requires a full image rebuild** (`just load` on the host).
- `flake.nix` changes are **fully verifiable in-jail**, runtime behavior
  included. A nested `yolo -- bash` runs the CLI's own `AutoLoadImage`, which
  builds the flake (`nix build` delegates to the host daemon; see "Nix inside
  the jail"), notices the nix store path changed, materializes + loads the new
  image into the **nested** podman, and runs *that* — not the current baked
  image. Verified 2026-07-22: adding `imagePkgs.hello` to `corePackages` made
  `hello` resolve to `/bin/hello` and run inside the very next nested
  `yolo -- bash`. So a newly-baked package on PATH, a changed `Env`, a new shim —
  all observable from in here. Do the whole edit → build → run-the-new-image
  loop in-jail. (The confusing part: the nested run BUILDS a fresh image every
  time the flake changes; it only prints "loaded image from cache" when the
  store path is unchanged from a prior build. "Building yolo-jail-…" + "Image
  load needed: nix store path changed" is the fresh-build path.)
  - Two real caveats remain. (1) **A failed nix build does not stop the jail** —
    `AutoLoadImage` silently falls back to the loaded/cached image, so a broken
    flake looks like a working jail on **stale** code (see the bullet below);
    watch the build output, don't just trust that the jail came up. (2) The
    *host's own* jails keep running the host-loaded image until a host `just
    load` — so host-gating is real for **shipping** a flake change to the
    maintainer's day-to-day jails, not for **validating** it.
- **The `goSrc` fileset trap** (`flake.nix`): the hermetic image build only sees
  `go.mod`, `go.sum`, `vendor/`, `cmd/`, `internal/`, and `bundled_loopholes/`.
  A Go package outside that set **silently vanishes from the image**; the moment
  anything under `cmd/` imports it the build fails with "cannot find module
  providing package" while `go build ./...` stays green. Add it to the fileset
  by hand. `bundled_loopholes/` is the live example of an explicit entry;
  `tools/` and `integration/` are excluded on purpose (nothing in `cmd/` imports
  them).
- **A failed nix build does not stop the jail.** `AutoLoadImage` falls back to
  the already-loaded image, then to the newest cached tar. So a broken build
  looks like a working jail running **stale** code. Only a real nested-jail run
  catches this.
- `vendor/` is committed and the nix build is hermetic (`-mod=vendor`, no
  network). A new dependency needs `go mod vendor` committed, or the image build
  breaks while `go test` still passes.
- Image reload sentinel is `BUILD_DIR/last-load-<runtime>` (not `.last-load`).
  `nix build --impure` exists so `builtins.getEnv` can read
  `YOLO_EXTRA_PACKAGES` from the config's `packages` list.

## Testing

- `just test-fast` = `go test -short ./...` — unit tests plus the short-gated
  compile of `integration/`. No containers. Run by the pre-commit hook
  (`just check-ci` = `lint-ci` + `test-fast`).
- `just test` adds `go test -count=1 -timeout 0 ./integration`. Run by CI.
- **`integration/` rules**: all files are package `integration`, gated by
  `requireJail(t)` (skipped under `testing.Short()`). Do **not** add
  `t.Parallel()` — the package runs serially by design (real containers; the
  session image load must not run per worker). Each `run*` helper honors
  `YOLO_TEST_JAIL_TIMEOUT` (integer seconds, default 300) as its per-command
  deadline; the suite runs under `-timeout 0` so only those deadlines and CI's
  `timeout-minutes` bound it.
- **No agent tests.** Automated tests must never start `claude`/`copilot`/
  `gemini`/etc. interactively or make API calls. `--version` probes only.
- **Nested-jail verification is mandatory** for `cmd/` and `internal/` changes:
  run `yolo -- bash` from inside this jail. Mount failures, permission errors,
  and read-only-fs conflicts only appear when a container actually starts.
  Unit tests do not catch them.

## Invariants & gotchas

- **Run `yolo check` after every edit** to `yolo-jail.jsonc` or
  `~/.config/yolo-jail/config.jsonc`, before asking a human to restart. Use
  `yolo check --no-build` for a fast in-jail preflight. Config changes also
  trigger a y/N diff prompt at startup — don't rely on it to catch mistakes.
- **Shims are unconditional.** Blocked tools (`grep -r`, `find`, …) are
  generated from config and always active unless `YOLO_BYPASS_SHIMS=1`. Set it
  for installers and scripts that need the real tool.
- **Use `shquote.Join`** (`internal/shquote`) for anything crossing into the
  container's `bash -c`.
- **Podman-in-podman**: when already inside a container the CLI uses
  `--userns=host` (doubly-nested user namespaces fail mounting `/proc`) and
  forces `--net=host` (netavark can't create netns without `NET_ADMIN`). Inner
  containers also need `--cgroups=disabled` — both are image defaults in
  `/etc/containers/containers.conf`.
- **Nix inside the jail** delegates to the host daemon: the CLI mounts
  `/nix/var/nix/daemon-socket` + `/nix/store:ro` and sets `NIX_REMOTE=daemon`.
  Without this you get "build users group has no members".
- **Claude YOLO** is `--dangerously-skip-permissions` + `IS_SANDBOX=1` (the env
  var bypasses the UID-0 refusal). `settings.json` sets `permissions.allow` to
  **`[]`** and `defaultMode: acceptEdits` — it is not an allowlist mechanism.
- **Bootstrap installs only** `chrome-devtools-mcp` and
  `@modelcontextprotocol/server-sequential-thinking`. LSP servers are
  config-gated, tracked by the `~/.yolo-installed-lsps` sentinel, and
  uninstalled when dropped from config. Agent CLIs install lazily on first use
  via launchers in `~/.yolo-shims/`.
- **PATH order** (exact):
  `$HOME/.yolo-shims:$HOME/.local/bin:$NPM_CONFIG_PREFIX/bin:<mise-shims>:$GOPATH/bin:/bin:/usr/bin`.
- **Env hygiene** (agents can't handle interactive UI): `PAGER`/`GIT_PAGER`
  =`cat`, `BAT_PAGER=""`; `EDITOR=cat` (stops `git commit` hanging) but
  `VISUAL=nvim` (human ctrl-g editing); the host's `TERM` is forwarded so color
  output survives; `OVERMIND_SOCKET=/tmp/overmind.sock` so jail overmind doesn't collide with the
  host's; `LD_LIBRARY_PATH=/lib:/usr/lib:/usr/lib/<multilib>` baked into the
  image Env to survive agents sanitizing the environment.
- The built-in `jail-startup` skill is injected into every jail and reads
  `.yolo/handover.md`. Priority: built-in < host user-level < workspace.

## Where things live

| Topic | Authority |
|---|---|
| Config keys, all of them | `yolo config-ref` |
| CLI surface | `yolo --help` |
| End-user usage, devices/GPU, mise tools, `yolo-cglimit` | `docs/guides/USER_GUIDE.md` |
| Mounts, overlays, home layout | `docs/design/jail-home.md` |
| Per-agent briefing generation | `docs/design/agent-briefings.md` |
| MCP/LSP config, node wrappers, `LD_LIBRARY_PATH` story | `docs/design/mcp-configuration.md` |
| Loopholes (`audio`, `claude-oauth-broker`, `host-processes`) | `docs/guides/loopholes.md`, `docs/design/loophole-protocol.md` |
| Config-change confirmation flow | `docs/design/config-safety.md` |
| Storage paths and state separation | `docs/design/storage-and-config.md` |
| Cgroup delegate security model | `docs/design/security-shim.md` |
| macOS backends | `docs/guides/macos.md` |
| macos-user nix integration + disabled-feature surface | `docs/design/macos-user-nix-and-features.md` |

Agent logs, for debugging: `~/.copilot/logs/`, `~/.cache/gemini-cli/logs/`,
`~/.claude/projects/` inside the jail; same paths under
`~/.local/share/yolo-jail/home/` on the host.

## Workflow

1. Image change → edit `flake.nix`, then verify end-to-end in a nested jail
   (`yolo -- bash`): the nested run rebuilds the flake and runs the NEW image, so
   runtime behavior is observable in-jail (see "Build & deploy"). Watch the build
   output — a failed build silently falls back to stale code. A host `just load`
   is only needed to ship the change to the maintainer's own jails, not to
   validate it.
2. Logic change → edit `cmd/`/`internal/`, `just build-go`, verify in a nested
   jail (`yolo -- bash`).
3. `just format` (gofmt) before committing.
4. Conventional commit messages. The pre-commit hook runs `just check-ci`; if it
   rejects, fix forward — never `--no-verify`, never `--amend`.
5. End of task: `git status` clean, `just done` green.

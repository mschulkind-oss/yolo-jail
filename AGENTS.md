# YOLO Jail: Agent Developer Guide

yolo-jail runs coding agents in an isolated container against a live-mounted
workspace, without exposing host credentials or identity.

**This file is the guide for developing yolo-jail itself**, and it is deliberately terse: each rule keeps
its imperative, the file that enforces it, and its escape hatch — the linked authority carries the
measurement and the history that produced it. Where this file and a linked authority disagree, the
authority wins. Usage and config reference material is not here; see
[Where things live](#where-things-live).

## Packs, and what core does not know

**AGENTS ARE PACKS. Core does not know what an agent is.** No agent registry, no `agents` config key, no
`YOLO_AGENTS`. Config carries ONE list of `packs`; shipped packs live in `packs/*/pack.json` and are
selected by BARE NAME — `"packs": ["claude"]`.
[`internal/config/validate.go`](./internal/config/validate.go) hard-errors on `agents` on the host (and
warns in-jail, where the config is the generated snapshot).

**A pack that installs an agent is just one that declares a `kind: "program"` surface**, and **most
shipped packs install no CLI at all** — some ship only a loophole, some only a provider and a profile, one
only blocked-tool refusals. `rg -l '"kind": "program"' packs/*/pack.json` is the agent list and
`rg -l loophole packs/*/pack.json` the loophole list; writing either membership down here is what rots.
Anything in this corpus saying "the six" names the agent SUBSET from when it had six members. Two
structural facts, not guessable from a manifest:

- `openai-auth` declares the `openai-codex` PROVIDER (capabilities-only — no endpoint, no credential
  pointer) rather than an agent pack owning it, because `claude` and `pi` each ship a `codex` profile
  selecting it and both `needs` it.
- `wire-bridge` is the only `kind: "service"` pack — one in-jail daemon and its endpoint file, joined to a
  launch by any SELECTED pack whose `needs` names it. `packs/claude` names it UNCONDITIONALLY, so a bare
  `"packs": ["claude"]` gets it; `cerebras` and `kilo` name it only when their `when_bins` lists `claude`
  or `copilot` ([`needs`](./docs/reference/wire-bridge.md#needs--a-conditional-pack-dependency)).

**Every loophole yolo ships is a pack's, and there is no other channel.** `bundled_loopholes/` and its
embed are DELETED; `claude-oauth-broker` is a **contribution of `packs/claude`**, not a pack of its own,
the dependency being structural — selecting the claude pack IS the dependency
([`OQ-A10`](./docs/reference/loophole-system.md#oq-a10)). So: `paths.BuiltinLoopholeNames` and
`loopholes.ReservedLoopholeNames` are GONE; the top-level `journal` and `host_processes` config keys are
REFUSALS naming their replacements; and the pack-shipped subset is the only vocabulary left, so
`publishes: "endpoint"`, `jail_env` and an absolute `requires.file_exists` are refused for every manifest
yolo reads.

**Nothing is active by default**: an empty config yields a jail with no coding agent, and says so at launch
(`run.warnIfNoPacks`).

A pack declares (`internal/packdecl`, read through `internal/packload`) an install spec, mounts,
writable/shared dirs, host-file grants, composed `surfaces`, launch flags and named `hooks`. The boot path
renders every one in a single loop (`entrypoint/packsurfaces.go`) with **no switch on any tool name**.
Three things to know before debugging it:

- **The MOUNT is the filter.** The entrypoint renders every pack under `YOLO_PACK_ROOT`, so `stagePacks`
  copies only the SELECTED packs in. A dropped pack must therefore be UNSTAGED or it keeps rendering:
  `_official/` is cleared wholesale, and each configured pack's dir is pruned when its slug leaves `packs` —
  contents-only, never the staging root itself, whose inode a live jail's `/ctx/packs` bind captured
  (`packstage` rule 3). A pack still configured but unresolvable this launch (offline git remote) is KEPT.
- **`packload.Embedded*` is deliberately NOT selection-gated.** The reservation lists (`host_files` writable
  roots, `writable_home_dirs` segments, GlobalHome subdirs) cover every pack yolo SHIPS, or a `host_files`
  entry could claim a path a pack added tomorrow needs.
- **`packload.Embedded()` LEASES ONE IMMUTABLE TREE PER BUILD**, not one per process: a content hash of the
  embedded FS names `~/.local/share/yolo-jail/embedded-packs/<hash>`, the first reader populates it
  atomically, and every later process of that build adopts it under a shared `flock` on its `.lease`
  ([`embeddedcache.go`](./internal/packload/embeddedcache.go)). Nothing is materialized until the first
  real caller — package init writes nothing, so `yolo --version` creates nothing. ⚠ **Never move it under
  `cache/`**, which every jail mounts read-write: host yolo loads this tree with a shipped pack's authority
  (`paths.EmbeddedPacksDir` states why). When the base is unusable the process takes a per-process
  **fallback** tree, `$TMPDIR/yolo-embedded-lease-*`, which `ReleaseEmbedded` deletes. The defer-skipping
  exits yolo CONTROLS release it explicitly — `yolo host`'s exec, every ttyproxy signal arm (inside
  `runWithProxy`, so arms passing no `onTerminate` are covered), `yolo-jaild`, `entrypoint.Main` before
  `execBash`. ⚠ **Any other death leaves the fallback tree behind**: SIGKILL, OOM, or a signal arriving where
  no handler is installed (Ctrl-C during a launch's nix build). Its lease dies with the process, so the next
  fallback's sweep or `yolo prune` reaps it once past the sweep's age floor; nothing reaps it sooner. A
  second process-lifetime copy is still the bug to watch for. Call `MaterializeEmbedded` directly only when
  you delete the dest yourself ([`packs.go`](./internal/cli/run/packs.go) stages out of one).

`agentcfg.BuiltinManifest()` is core's own surfaces only (`mise/config`); callers wanting the full set merge
pack surfaces via `ManifestWith`. `internal/jailcontent` (was `internal/agents` until the name outlived the
registry) keeps only what was never per-agent: skills staging, briefing composition, loophole descriptions,
the source-tree probe.

Backends are `podman`, `container` (Apple Container) and `macos-user` (macOS Seatbelt, no VM) — **Docker was
removed**; validate.go hard-errors on it too.

## Architecture

Every command lives in `cmd/`, and the table below is the list — a count in prose is one more thing to keep
true, and the two that used to sit here were both wrong within weeks. Everything is Go; the only
bash/Python left is generated *content* (shims, `.bashrc`) emitted by `internal/entrypoint` — **no
generated in-jail CLIENT survives**, two implementations of one client being the drift the transport
unification exists to end ([`loophole-transport.md`](./docs/reference/loophole-transport.md)).

| Binary | Runs where | Role |
|---|---|---|
| `yolo` | host **and** in-jail | the CLI; also every host daemon |
| `yolo-entrypoint` | container PID 1-ish | provisions the jail at startup |
| `yolo-jaild` | container | in-jail daemons |
| `yolo-ps` | container | host-process view (the `host-processes` loophole) |
| `yolo-cglimit` | container | cgroup-delegate client (the one AF_UNIX consumer left) |
| `yolo-journalctl` | container | journal-bridge client (loopback-TLS) |
| `yolo-serial` | container | serial-bridge client (loopback-TLS; the `serial` loophole) |
| `goprobe` | nowhere | deployment tripwire; excluded from runtime PATH |

**A new `cmd/` binary must be added to [`flake.nix`](./flake.nix)'s `shippedBinaries` AND to
[`scripts/stage-source-bundle.sh`](./scripts/stage-source-bundle.sh)'s `SHIPPED_BINARIES`** or it silently
vanishes from the jail (source build) or from a shipped bundle while `go build ./...` stays green.
[`shippedclients_test.go`](./internal/entrypoint/shippedclients_test.go) pins all three spellings together,
`goprobe` being the one declared exemption; `run.TestFlakeAndLauncherAgreeOnThePrefixLayout` pins the
resulting prefix layout across the two languages.

**Host ship set is just `{yolo}`** — `just install` runs `go install ./cmd/yolo` and nothing else; the other
four are image-side only. It also **publishes a flake-bundle GENERATION**, staging into a fresh
`~/.local/share/yolo-jail/flake-bundles/<stamp>/` and swapping the stable `flake-bundle` symlink at it
(`internal/flakebundle`). **Never hand
[`stage-source-bundle.sh`](./scripts/stage-source-bundle.sh) the stable path** — restaging in place deletes
pid1 out from under every RUNNING jail, a launch having bind-mounted `<bundle>/bin/linux-<arch>`, and a bind
mount pinning an inode rather than a path.

Generations are collected by LIVENESS, never by age. The TRI-STATE half of that is universal here —
"unreferenced" and "I could not ask the runtime" are the same empty answer, so a reaper that cannot ask
declines rather than sweeping. ⚠ **The liveness half is NOT universal**, the nix GC-root reaper being a pure
ONE-WEEK AGE cutoff with no liveness veto by ruling
([`OQ-LS1`](./docs/reference/image-retention.md#why-its-this-way)). Pick per reaper.

**Daemons are subcommands, not separate binaries.** Host daemons are hidden self-exec subcommands of
`yolo` (`yolo internal daemon <name>`); in-jail daemons are `yolo-jaild <name>` (`supervise` reads
`YOLO_JAIL_DAEMONS`). Run either group bare for its members: spelling them here is what rotted all
three lists this line used to carry. Both dispatch on plain `args[0]` — **not** argv[0]/symlink. Easy
to get wrong.

CLI code lives under `internal/cli` (top level), `internal/cli/run` (the run pipeline) and
`internal/cli/check`.

**Self-bootstrapping:** this project is developed from inside its own jail. `/workspace` is bind-mounted
live, so edits are visible on the host instantly — there is no sync step.

## Build & deploy — the traps

- `just build-go` → [`scripts/build-go.sh`](./scripts/build-go.sh) → `dist-go/<goos>-<goarch>/` is the
  **cross-compile-for-shipping** step only, feeding the flake's prebuilt short-circuit in a shipped bundle;
  it feeds no in-jail run. **`just deploy` does NOT cross-compile** — it is `just install` plus Claude-broker
  priming.
- **THE IMAGE DOES NOT CONTAIN YOLO ANY MORE.** `/opt/yolo-jail` is TWO `:ro` BIND MOUNTS the launch
  supplies — linux binaries at `bin/`, the flake bundle at `share/yolo-jail/` — and the container argv names
  `/opt/yolo-jail/bin/yolo-entrypoint` absolutely ([`jailprefix.go`](./internal/cli/run/jailprefix.go)). The
  image bakes only the mountpoints and the `/bin/<name>` symlinks into them
  ([`flake.nix`](./flake.nix): `jailPrefixLinks`). **That takes `goSrc` out of the image derivation**, so a
  `cmd/`- or `internal/`-only commit no longer moves the image, which now moves only for
  [`flake.nix`](./flake.nix), [`flake.lock`](./flake.lock) or `packages:`. The
  [security delta](docs/reference/image-staging-vs-baking.md#the-security-delta) states the trade
  deliberately: what executes in the jail is host-mutable with no rebuild.
- ⚠ **On a ROOTLESS podman — the common configuration — the image copy must run as
  `podman unshare -- <copier> copy …`**, because writing a rootless store reproduces layer ownership under
  `/etc/subuid` and so needs a user namespace AppArmor 4 denies unprivileged. **Rootful podman takes the bare
  copy** — `podman unshare` refuses there outright. The branch is decided from `podman info` BEFORE the copy,
  never by retrying a failure. macOS podman and Apple Container still take an archive, the VM owning the
  store.
- **The outer jail's binaries are frozen for the session**, chosen by the host launcher at start, so you
  cannot live-patch them in-jail. Verify Go changes in a **nested** jail: it builds the live `/workspace`
  checkout's `.#installPrefix` and mounts THAT. See [Testing](#testing) for the command and the carve-outs.
- **[`flake.nix`](./flake.nix) changes are fully verifiable in-jail**, runtime behavior included: a nested
  launch's `AutoLoadImage` rebuilds the flake, notices the store path changed, loads the new image into the
  **nested** podman and runs *that*. A host `just load` only **ships** a flake change to the maintainer's
  day-to-day jails; it does not validate it.
- **A failed nix build STOPS the jail** — fatal, printing nix's stderr. It does NOT fall back to the loaded
  image or a cached tar; it used to, and a broken build then looked like a working jail running **stale**
  code. `YOLO_ALLOW_STALE_IMAGE=1` opts back in, for the offline or disk-starved machine that was the good
  case. **`SkipBuild` is untouched:** no build ran, so nothing failed.
- **The `goSrc` fileset trap** ([`flake.nix`](./flake.nix)): the hermetic Go build only sees
  [`go.mod`](./go.mod), [`go.sum`](./go.sum), `vendor/`, `cmd/`, `internal/` and `packs/`. A Go package
  outside that set **silently vanishes from the jail**; the moment anything under `cmd/` imports it the
  build fails with "cannot find module providing package" while `go build ./...` stays green. Add it to the
  fileset by hand. `tools/` and `integration/` are excluded on purpose.
- **THE TWO HALVES DEPLOY ON DIFFERENT CADENCES, and a launch REFUSES when they disagree.** The jail's
  `yolo-entrypoint` is built and mounted every launch from whatever flake `reporoot.Resolve` picked; the
  host `yolo` changes only when a human runs `just install`. So any commit moving a host↔jail contract — a
  mount destination, an env var name, an argv the entrypoint parses — would otherwise leave the machine
  skewed by default and silently. `version.SourceSkew` compares the binary's ldflags commit stamp against
  HEAD **through the `goSrc` fileset + the flake files**, and `Run` refuses before the build, naming
  `just install`; `YOLO_ALLOW_SOURCE_SKEW=1` overrules it. It is silent for a docs-only commit, for
  uncommitted work, and for anything it cannot prove. **The integration suite cannot see this class**: it
  always builds a fresh CLI and refuses a stale image, so the one pairing that ships is the one it cannot
  represent.
- **The cwd does not choose the flake.** Three sources remain — `YOLO_REPO_ROOT`, a `share/yolo-jail` bundle
  beside the binary (Homebrew, the release archive, the baked prefix), then
  `~/.local/share/yolo-jail/flake-bundle` from `just install` — and every launch prints which it took
  (`Flake source: <path> (<what selected it>)`), as does `yolo check`. So a from-source developer gets the
  STAGED BUNDLE even inside the checkout, making `just install` how an image change is delivered (export
  `YOLO_REPO_ROOT=~/code/yolo-jail` to build from live source everywhere — the skew gate still guards it);
  and in-jail, bare `yolo` resolves the BAKED bundle, which is why every nested-jail command here carries
  `YOLO_REPO_ROOT=/workspace`.
- `vendor/` is committed and the nix build is hermetic (`-mod=vendor`, no network). A new dependency needs
  `go mod vendor` committed, or the image build breaks while `go test` passes.
- Image reload sentinel is `BUILD_DIR/last-load-<runtime>` (not `.last-load`). `nix build --impure` exists
  so `builtins.getEnv` can read `YOLO_EXTRA_PACKAGES` from the config's `packages` list.

## Testing

- `just test-fast` = `go test -short ./...` — unit tests plus the short-gated compile of `integration/`. No
  containers. It is the `test-fast` half of `just check-ci` (= `lint-ci` + `test-fast`), the pre-commit
  gate — the repo provides it at [`hooks/pre-commit`](hooks/pre-commit) and `just install-hooks` puts it
  in `.git/hooks`, but git cannot track hooks, so installing is a per-clone step; see
  [Workflow](#workflow) step 4. `just test` adds
  `go test -count=1 -timeout 0 ./integration`. Run by CI.
- **`integration/` rules**: all files are package `integration`, gated by `requireJail(t)` (skipped under
  `testing.Short()`). Do **not** add `t.Parallel()` — the package runs serially by design (real containers;
  the session image load must not run per worker). That rule is about workers inside one job; the macOS
  nightly shards across four JOBS, each a separate runner, and computes its shards from `go test -list`
  rather than a hand-maintained `-run` regex that would drop a new test silently. Each `run*` helper honors
  `YOLO_TEST_JAIL_TIMEOUT` (integer seconds, default 300) as its per-command deadline, the suite running
  under `-timeout 0` so only those and CI's `timeout-minutes` bound it.
- **The suite refuses to run against a STALE jail image.** The entrypoint is MOUNTED from this tree, so the
  suite's `yolo` and the `yolo-entrypoint` it runs come from one tree by construction; what a stale image can
  still be wrong about is what the FLAKE decides. So `ensureJailImage` compares
  `nix eval --raw .#imageIdentity` (an eval, never a build) against `/etc/yolo-jail-image-identity` in the
  loaded image and **aborts with the fix command** on a mismatch. ⚠ **`imageIdentity` must stay a content
  hash declared OUTSIDE `eachDefaultSystem`**, so every host computes the same value: a store path carries
  the evaluating host's `system`, and inside that scope a darwin host cannot vouch for a Linux-built image of
  its own commit (`TestImageIdentityIsSystemInvariant` in
  [`imageskew_test.go`](./integration/imageskew_test.go) guards it). `YOLO_TEST_REBUILD_IMAGE=1` forces a
  rebuild+reload; `YOLO_TEST_IMAGE_SKEW=warn|off` downgrades the check (`fail` is the default; darwin
  auto-downgrades). **`git add` before rebuilding** — nix sees tracked files only, so an untracked new file
  moves neither side and the check reports a false "matches".
- **A test that pins the CALLEE while the CALL SITE is unpinned is not a test**, and this repo has shipped
  that shape repeatedly — nothing fails when the production caller is deleted, so the feature can be switched
  off wholesale with the unit gate green. The variant to watch for is a test that asserts the SENTENCE a
  comment makes rather than the system. When adding one, ask: **does it fail if I delete the call site?**
- **The darwin PATH-RESOLUTION class is reproducible on Linux**, and `check-macos` is where it otherwise
  surfaces. On macOS `t.TempDir()` returns `/var/folders/…`, which **is a symlink** to
  `/private/var/folders/…`, so any test comparing a fixture path against code that resolves symlinks
  (`filepath.EvalSymlinks`, and anything built on it) passes on Linux and fails on darwin. Reproduce:
  ```console
  $ mkdir -p /tmp/real && ln -sfn /tmp/real /tmp/link
  $ TMPDIR=/tmp/link go test -short ./...
  ```
  A fixture handing out `EvalSymlinks(t.TempDir())` is the fix — resolve where the path is MINTED, or the
  next comparison added forgets.
- **No agent tests.** Automated tests must never start `claude`/`copilot`/`codex`/etc. interactively or make
  API calls. `--version` probes only.
- **Nested-jail verification is mandatory** for `cmd/` and `internal/` changes: after `just build-go`, run
  the freshly-built binary BY PATH, pointed at the live tree, **from a throwaway workspace**:
  ```console
  $ mkdir -p /tmp/yolo-nested && cd /tmp/yolo-nested
  $ YOLO_REPO_ROOT=/workspace /workspace/dist-go/linux-$(go env GOARCH)/yolo -- bash
  ```
  The two halves are unrelated protections. `YOLO_REPO_ROOT` names the flake (without it `dist-go/` has no
  bundle beside it and the launch refuses). The `cd` protects the session you are running in: the
  per-workspace home overlay is `<workspace>/.yolo/home`, and for `/workspace` that **is** the live jail's
  home — the same inode as `/home/agent/.claude` — so a nested launch there regenerates agent config over the
  running session's own home. **The launcher REFUSES that** (`refuseLiveWorkspaceLaunch`, the first thing
  `Run` does) whenever in-jail and the workspace resolves to `/workspace`;
  `YOLO_ALLOW_LIVE_WORKSPACE=1` is the hatch. A fresh workspace also verifies MORE — first-boot
  provisioning, and the mount, permission and read-only-fs failures that only appear once a container starts.
  **Not bare `yolo`**, which is the baked launcher frozen at the last host `just load`: a stale launcher
  emitting an argv the fresh nested image rejects is exactly how a fixed jail looks broken. **Never
  `just install` in-jail** — it refuses, `go install` shadowing the baked `/bin/yolo` with a stale GOBIN
  copy.
- **TWO CARVE-OUTS, where the rule above is actively misleading rather than merely insufficient: a nested
  jail gives a FREE GREEN to two whole classes, however broken the change is.** Podman-in-podman forces
  `--net=host`, the one mode in which loopback-forwarding bugs **cannot** reproduce (the jail shares the
  launcher's stack, so the two loopbacks are one), so anything touching how a jail reaches a host daemon is
  unverifiable here: the `--network` flag ([`hostloopback.go`](./internal/cli/run/hostloopback.go)),
  `internal/svcendpoint`'s bind/advertise pair, the `host.containers.internal` hop, the in-jail reachability
  probe. And `--userns=host` is forced, so a nested podman reports `rootless: false` and takes every rootful
  branch, leaving ID mapping, `/etc/subuid`, `newuidmap` and **image delivery into a rootless store** equally
  unverifiable. Each class has shipped a fault this way. **The only instruments that settle either are a REAL
  jail on a rootless host, or CI** (rootless on both arches); report
  `podman info --format '{{.Host.RootlessNetworkCmd}}'` or `'{{.Host.Security.Rootless}}'` with the claim.
  Bare `podman run` against this jail's own loopback does work — the blindness is `yolo`'s forced
  `--net=host`, not the jail — but proves only that a FLAG behaves, never that a host's passt build has it.
  Reproductions:
  [the blindness section](./docs/reference/loopback-tls-reachability.md#a-nested-jail-is-structurally-blind-to-this),
  and the warning heading [`reachability_test.go`](./integration/reachability_test.go).

## Invariants & gotchas

- **Run `yolo check` after every edit** to [`yolo-jail.jsonc`](./yolo-jail.jsonc) or
  `~/.config/yolo-jail/config.jsonc`, before asking a human to restart. `yolo check --no-build` is the fast
  in-jail preflight. The y/N startup diff prompt is not a substitute.
- **Shims are unconditional ONCE GENERATED, but nothing is blocked by default.** `defaultBlockedList()` is
  EMPTY; `grep -r` and `find` moved to the **`guardrails`** pack, which a user opts into via `packs` — the
  old default assumed the image bakes `rg` and `fd`, false of macos-user, which bakes nothing. A blocker
  declares a `replacement` and is **generated only when that binary is on the agent's PATH** (`agentPath`,
  the one authority for which PATH counts), so a block can never leave a jail with neither the tool nor its
  alternative. A generated shim is unconditional at run time unless `YOLO_BYPASS_SHIMS=1` — set it for
  installers and scripts needing the real tool. A user's own `security.blocked_tools` is unaffected, and an
  entry naming the same tool as a pack's REPLACES it whole.
- **Use `shquote.Join`** (`internal/shquote`) for anything crossing into the container's `bash -c`.
- **A LAUNCH HAS NO QUIET MODE, by ruling** ([`OQ-RO3`](./docs/reference/report-tiers.md#why-its-this-way)).
  Progress may be COMPRESSED to a line — that is the whole density control a launch gets — but a
  **disclosure is never suppressible**: the pack read/exec banners are the entire trust boundary today
  (`packhostgrants.go`: *"the boundary today is DISCLOSURE, not consent"*), so a flag hiding one deletes what
  [`OQ-TP9`](./docs/design/trust-paths.md#decision-ledger) kept when it deleted the approval gate.
  `YOLO_NO_BANNER` is the one hatch, narrow on purpose (the version line, nothing else), and
  `TestTheLaunchHasNoQuietFlag` fails if another appears on `runFlags`. Everything printed is teed to
  `<workspace>/.yolo/launch.log`, beside the entrypoint's `boot.log`.
- **Podman-in-podman**: inside a container the CLI uses `--userns=host` (doubly-nested user namespaces fail
  mounting `/proc`) and forces `--net=host` (netavark can't create netns without `NET_ADMIN`). Inner
  containers also need `--cgroups=disabled` — both are image defaults in
  `/etc/containers/containers.conf`.
- **The default `network.mode: bridge` no longer means silence.** It still emits no `--net` flag, but the
  launcher reads `podman info` and, on a *rootless* podman whose `rootlessNetworkCmd` it recognises, adds the
  option forwarding the host's LOOPBACK into the jail — `--network=pasta:--map-host-loopback,…` or
  `--network=slirp4netns:allow_host_loopback=true`. Without it every loopback-TLS service is unreachable from
  every jail on a pasta host (podman's default since 5.0).
  [`hostloopback.go`](./internal/cli/run/hostloopback.go) is the whole decision and states why every unproven
  fact emits nothing; `YOLO_NO_HOST_LOOPBACK=1` is the loud hatch, and an explicit `network.mode` is never
  overridden.
- **The launcher tells the jail what it decided**, via
  `YOLO_HOST_LOOPBACK=requested|shared|unsupported|unknown`, emitted on EVERY launch — so an absent variable
  means only "launcher older than the variable". The in-jail witness
  ([`reachability.go`](./internal/entrypoint/reachability.go)) cannot derive it: from inside, "this host
  cannot forward loopback" and "yolo asked and the service is still down" are the same observation. **That
  witness is FATAL** — an enabled jail-facing service the jail cannot use REFUSES the launch, in all three
  fault classes ([`OQ-R4`](./docs/reference/loopback-tls-reachability.md#oq-r4)) — and severity is the
  disposition's decision alone: only `requested` and `shared` escalate, a host yolo could not ask never being
  refused for what it cannot help ([`OQ-R3`](./docs/reference/loopback-tls-reachability.md#oq-r3)). Hatch:
  `YOLO_ALLOW_UNREACHABLE_SERVICES=1`, forwarded from the host env and named in the refusal.
- **A WORKSPACE MAY NOT CONTAIN THE CREDENTIAL BOUNDARY, AND THOSE DIRECTORIES MAY NOT HOLD A `.yolo`.** A
  workspace that IS or CONTAINS `$HOME`, `~/.config/yolo-jail` or `~/.local/share/yolo-jail` — or sits INSIDE
  either of the latter two — puts the boundary inside the one host directory a jail reads and writes by
  design: `~/.ssh` and the cloud tokens, the user-scope config deciding the NEXT launch's
  `packs`/`host_files`, and the state dir holding every other workspace's home overlay, the pack approvals
  and the flake bundle each launch binds as pid1. `refuseWorkspaceScope`
  ([`workspacescopeguard.go`](./internal/cli/run/workspacescopeguard.go)) is the SECOND thing `Run` does,
  before the launch log, because the file that tee would create IS the artifact. It needs no mistake beyond a
  `cd`: there is no `--workspace` flag, so a bare `yolo` typed in the home is a launch on the home.
  `workspace_readonly` does not soften it — the READ half is the breach. **The predicate is
  [`paths.WorkspaceScopeBreach`](./internal/paths/workspacescope.go), not the launcher's**, because those
  three directories must also never acquire a stray `.yolo`: one marker there and `workspaceRoot()`'s upward
  walk answers "the home" for every `yolo config` verb run below it, so `paths.EnsureWorkspaceStateDir` is the
  CREATION chokepoint and returns the breach as its error, and the walk STOPS at a boundary directory. There
  is deliberately no `YOLO_ALLOW_*`. ⚠ One path-based exemption exists, `paths.scopeExempt`, naming the
  `yolo capture` scratch store and nothing else.
- **ONE host directory is bind-mounted WRITABLE into the jail, and it is the only one.** A recognised
  **content-addressed** host cache is aliased at the path the jail's own copy of the tool already uses, so it
  stops existing twice (`internal/hostcas`, [`hostcasalias.go`](./internal/cli/run/hostcasalias.go);
  [`OQ-BF10`](./docs/design/disk-levers-and-backfill.md#OQ-BF10)). Today that set is pants' `lmdb_store`
  alone. **The gate is CONTENT ADDRESSING, and the reason is injection rather than size**: a path-keyed cache
  lets a jail write content the host tool later reads *because of where it sits*, the jail choosing both key
  and bytes, while a CAS rejects a blob whose digest does not match its key. ⚠ **npm's `_cacache` and Go's
  build cache look like candidates and are NOT** — npm's index maps a request URL to an integrity digest and
  Go's maps a writer-chosen action ID to an output ID, so both are exactly that injection channel, inside a
  store whose *content* half really is content-addressed. Do not add a store to `hostcas.Stores` without
  checking which half its index keys on. Also gated on matching host OS+arch, a writable non-empty source,
  **never macOS**, and **never a segment the user relocated**. Every failure degrades to the private copy;
  none can refuse a launch. The launch discloses the alias by name and `yolo stores` lists it as `not yolo's`,
  so no reclaimer offers to delete host bytes.
- **Nix inside the jail** delegates to the host daemon: the CLI mounts `/nix/var/nix/daemon-socket` +
  `/nix/store:ro` and sets `NIX_REMOTE=daemon`. Without this you get "build users group has no members".
- **Claude YOLO** is `--dangerously-skip-permissions` + `IS_SANDBOX=1` (the env var bypasses the UID-0
  refusal). `settings.json` sets `permissions.allow` to **`[]`** and `defaultMode: acceptEdits` — it is not
  an allowlist mechanism.
- **Bootstrap installs only** `chrome-devtools-mcp` and
  `@modelcontextprotocol/server-sequential-thinking`. LSP servers are config-gated, tracked by the
  `~/.yolo-installed-lsps` sentinel, and uninstalled when dropped from config. Agent CLIs install lazily on
  first use via launchers in `~/.yolo/bin/launch/`.
- **PATH order** (exact — `BootPath`, [`boot.go`](./internal/entrypoint/boot.go), the authority this line
  mirrors):
  `$HOME/.yolo/bin/block:$HOME/.yolo/bin/launch:$NPM_CONFIG_PREFIX/bin:<mise-shims>:$GOPATH/bin:$HOME/.local/bin:/run/yolo/packages/bin:/bin:/usr/bin`.
  The `.bashrc` export ([`shell.go`](./internal/entrypoint/shell.go)) is a second, independently-written copy
  of the same order, compared to `BootPath` **entry by entry** — the two disagreed about `$HOME/.local/bin`
  for months behind a test that only checked the ends. **`/opt/yolo-jail/bin` is deliberately NOT on it**,
  even though every yolo binary now lives there: the image bakes `/bin/<name>` symlinks into the mount
  instead, which moves none of this list and still works for a consumer that scrubs PATH and spells
  `/bin/yolo`. Do not "fix" that by adding an entry. `/run/yolo/packages/bin` is the store-delivered package
  farm, **absent unless the launch opted in** with `YOLO_STORE_PACKAGES=1`, and positioned to change nothing.
- **`packages:` can come from the mounted nix store instead of the image** — `YOLO_STORE_PACKAGES=1`, podman +
  Linux + a running nix daemon only, otherwise the launcher says so and bakes. **Opt-in fast path, baked path
  retained, per LAUNCH and never per package** — an opt-in launch builds the image with no
  `YOLO_EXTRA_PACKAGES` and takes its tools from a boot-written symlink farm at `/run/yolo/packages`
  ([`storepackages.go`](./internal/entrypoint/storepackages.go)), buying one image per machine instead of one
  per distinct `packages:` list. **Exactly one mechanism is live in any jail** — a package both baked *and*
  staged silently runs the **baked** copy. The SAME dial is C5, on purpose, so that stays a fact about a
  launch rather than a combination: an opt-in launch also builds `.#ociImageLean` and takes those packages
  from `.#yoloImageExtras`, so `chromium` is no longer at `/usr/bin/chromium` and `/etc/fonts` is gone. What
  moves, what Lean drops, and the resolution consequences:
  [store-delivered packages](docs/reference/image-staging-vs-baking.md#store-delivered-packages).
- **Two generated script dirs, ADJACENT AT THE HEAD of PATH**, in an order that carries the meaning.
  `~/.yolo/bin/block` holds **blockers** (`GenerateShims`: refuse, suggest, `exit 127`) and is FIRST,
  interception having to outrank installation. `~/.yolo/bin/launch` holds **lazy installers/updaters**
  (`GenerateAgentLaunchers` / `GeneratePackageManagerLaunchers`) and is SECOND, **ahead of every install
  prefix** — placed after the prefixes it installs INTO, a launcher is unreachable the moment it succeeds and
  the evergreen update never runs again
  ([`OQ-PD12a`](./docs/design/program-delivery.md#decision-ledger)). Shadowing is instead prevented at
  generation time ([`launchercollision.go`](./internal/entrypoint/launchercollision.go)): no launcher is
  written for a name `/bin`, `/usr/bin` or a declared `mise_tools` entry already provides. ⚠ **That check
  must never consider the install prefixes** — spelled "already resolvable on PATH?" it destroys the feature,
  the launcher ceasing to be written after the first successful install. A tool both blocked and
  pack-declared gets one of each, the blocker winning by position. **Both dirs share ONE bind-mount anchor**
  at `~/.yolo/bin`, so both are cleared CONTENTS-ONLY (`resetAnchorDir`), and nothing may put that shared
  parent on PATH.
- **`macos-user` carries a THIRD PATH list** (`macosuser.SandboxPath`) and it is **NOT `BootPath`'s order** —
  the two-copy rule above does not cover three, and nothing compares `SandboxPath` to either other copy.
  `$HOME/.local/bin` is THIRD there and SIXTH in `BootPath`, and `/usr/bin` precedes `/bin`, so a
  pipx-installed tool outranks a mise shim on that backend and loses to it on every container backend. Left
  as a divergence rather than quietly reordered: which order is right is a ruling.
- **Env hygiene** (agents can't handle interactive UI): `PAGER`/`GIT_PAGER`=`cat`, `BAT_PAGER=""`;
  `EDITOR=cat` (stops `git commit` hanging) but `VISUAL=nvim` (human ctrl-g editing); the host's `TERM` is
  forwarded so color survives; `OVERMIND_SOCKET=/tmp/overmind.sock` so jail overmind doesn't collide with the
  host's; `LD_LIBRARY_PATH=/lib:/usr/lib:/usr/lib/<multilib>` baked into the image Env to survive agents
  sanitizing the environment.
- The built-in skills (`configuring-the-jail`, `diagnosing-the-jail`) are injected into every jail. The
  one-time host→jail handoff is NOT a skill: a fresh `.yolo/handover.md` the host agent filed becomes a
  **Handoff** section in the environment briefing and is consumed by the run pipeline — but only once a
  briefing has actually been WRITTEN, so a jail whose packs declare no briefing destination leaves the
  pointer fresh instead of eating it. Core cannot tell an agent launch from `yolo -- bash`, so a consumed
  handoff is announced on stderr with the `mv` that restores it. Skill priority: built-in < shared packs <
  **the conventional local pack** (`~/.config/yolo-jail/local`, appended LAST by `config.LoadPacks`). ⚠ The
  middle term is not "host user-level" — there is no such tree, and a skill in `~/.claude/skills` on the host
  reaches the jail by no path ([`skills.go`](./internal/jailcontent/skills.go), where the deleted
  `SkillTarget.HostSource` was). ⚠ **A skills contribution may FENCE children of its destination**
  (`reserved`, pack-declared — core knows no vendor's directory name, by
  [`OQ-ST2`](./docs/design/synced-skill-trees.md#OQ-ST2)): `packs/claude` reserves `synced`, because
  `~/.claude/skills/synced/` is a sync root Claude Code regenerates from a registration OUTSIDE it, so
  adopting it moved the tree into the local pack, composed a byte-identical copy back, and lost the user's
  edits on the next upstream sync. A reserved child is never adopted and never composed over, and a
  NON-EMPTY one is reported.

## Where things live

| Topic | Authority |
|---|---|
| Config *file* keys, all of them | `yolo config-ref` |
| Pack manifest schema | [`internal/packdecl/packdecl.go`](./internal/packdecl/packdecl.go) (the doc comments ARE the reference) |
| Pack authoring + the `packs` key | `yolo pack --help`, [`pack-system.md`](./docs/reference/pack-system.md) |
| CLI surface | `yolo --help` |
| End-user usage, devices/GPU, mise tools, `yolo-cglimit` | [`USER_GUIDE.md`](./docs/guides/USER_GUIDE.md) |
| Every config key and pack contribution kind, per setup (backend × host OS) | [`settings-per-setup.md`](./docs/reference/settings-per-setup.md) |
| Mounts, overlays, home layout | [`jail-home.md`](./docs/reference/jail-home.md) |
| Briefing generation, skills staging | [`agent-briefings.md`](./docs/reference/agent-briefings.md) |
| MCP/LSP config, the node/npx wrappers | [`mcp-configuration.md`](./docs/reference/mcp-configuration.md) |
| `LD_LIBRARY_PATH` / nix-ld, the `/lib` farm | [`mise-node-dynamic-linking.md`](./docs/reference/mise-node-dynamic-linking.md) |
| Loopholes: what each one opens | [`loopholes.md`](./docs/guides/loopholes.md), [`loophole-protocol.md`](./docs/reference/loophole-protocol.md) |
| The loophole system: activation, trust, the pack-shipped subset | [`loophole-system.md`](./docs/reference/loophole-system.md) |
| The wire-bridge service pack and `needs` | [`wire-bridge.md`](./docs/reference/wire-bridge.md) |
| Config-change confirmation flow | [`config-safety.md`](./docs/reference/config-safety.md) |
| `--timing`, `perf_logging`, the host perf log | [`perf-logging.md`](./docs/reference/perf-logging.md) |
| Storage paths and state separation | [`storage-and-config.md`](./docs/reference/storage-and-config.md) |
| What the image bakes vs. what a launch delivers; the mounted prefix; the rebuild/reload cost model | [`image-staging-vs-baking.md`](docs/reference/image-staging-vs-baking.md) |
| Image and GC-root retention, the reapers | [`image-retention.md`](./docs/reference/image-retention.md) |
| Loopback-TLS reachability, the witness, its severity rule | [`loopback-tls-reachability.md`](./docs/reference/loopback-tls-reachability.md) |
| Report tiers, the launch stream, why there is no quiet mode | [`report-tiers.md`](./docs/reference/report-tiers.md) |
| Program delivery: launchers, evergreen deps, the PATH ruling | [`program-delivery.md`](./docs/design/program-delivery.md) |
| Disk levers, host-cache backfill | [`disk-levers-and-backfill.md`](./docs/design/disk-levers-and-backfill.md) |
| Cgroup delegate security model | [`security-shim.md`](./docs/reference/security-shim.md) |
| macOS backends | [`macos.md`](./docs/guides/macos.md) |
| macos-user nix integration, disabled-feature surface | [`macos-user-nix-and-features.md`](./docs/reference/macos-user-nix-and-features.md) |
| The standing macOS direction | [`macos-no-vm-direction.md`](./docs/reference/macos-no-vm-direction.md) |
| The one-time host→jail handoff | [`host-to-jail-handoff.md`](./docs/reference/host-to-jail-handoff.md) |

**`YOLO_*` environment dials have no authority row, because they have no authority.** `config-ref`'s
`ENVIRONMENT VARIABLES` section lists `YOLO_RUNTIME`, `YOLO_BYPASS_SHIMS`, the two record-only timing gates
and `YOLO_EXTRA_PACKAGES`, and NOTHING else — no capability-changing dial has an entry there: not
`YOLO_STORE_PACKAGES`, `YOLO_REPO_ROOT`, the nix dials, `YOLO_USER_LAYER`, `YOLO_SVC_ADVERTISE_HOST`, nor
any `YOLO_ALLOW_*` hatch. Each is documented where it is ENFORCED instead: the invariant above that names
it, the refusal message that offers it, or the design doc that ruled it. Grep the source for the spelling
before believing any prose about one.

Agent logs, for debugging: `~/.copilot/logs/`, `~/.claude/projects/` inside the jail; same paths under
`~/.local/share/yolo-jail/home/` on the host.

## Workflow

1. **Image change** → edit [`flake.nix`](./flake.nix), then verify end-to-end in a nested jail
   (`cd /tmp/yolo-nested && YOLO_REPO_ROOT=/workspace yolo -- bash` — never from `/workspace`). The nested
   run rebuilds the flake and runs the NEW image. Watch the output: a failed build is fatal.
2. **Logic change** → edit `cmd/`/`internal/`, `just build-go`, then verify by running the freshly-built
   binary BY PATH, from a throwaway workspace:
   `cd /tmp/yolo-nested && YOLO_REPO_ROOT=/workspace /workspace/dist-go/linux-$(go env GOARCH)/yolo -- bash`.
   NOT bare `yolo` — that is the baked launcher and won't carry a launcher/argv-side change. Never
   `just install` in-jail. **Reachability-shaped change** (`--network`, `svcendpoint`'s bind/advertise,
   `host.containers.internal`) or **rootless-only path**? A nested jail cannot see those classes at all —
   read the two carve-outs under [Testing](#testing) before reporting it verified.
3. `just format` (gofmt) before committing.
4. Conventional commit messages. **Run `just check-ci` before every commit, or install the provided hook
   once with `just install-hooks`** and let it run for you. Git cannot track `.git/hooks`, so the hook is
   a versioned script ([`hooks/pre-commit`](hooks/pre-commit)) plus a per-clone installer — a clone does
   not deliver it, which is the only reason it is not simply always on. CI runs the same `just check-ci`,
   so a red commit cannot merge; the hook exists so it is caught locally first. A commit that landed is
   not evidence that anything checked it. If the gate rejects, fix forward — never `--no-verify`, never
   `--amend`. ⚠ It is a WHOLE-TREE gate, so during a fan-out it reports other agents' in-flight files as
   your failure; that is why an orchestrator commits and agents do not.
5. End of task: `git status` clean, `just done` green.
6. **Doc change that makes a claim about the code** → check it before writing it. A number, a `file:line`, a
   commit SHA, or a negative ("X has no caller") is the exact place a reader stops checking, so a wrong one
   is worse than none. The sweeps that keep this corpus honest — and the allowlists they need, since a doc
   recording a deletion is *supposed* to name what it deleted — are in
   [docs/plans/README.md](docs/plans/README.md#keeping-this-corpus-honest--the-five-checks-so-they-are-re-runnable).
   Run them when a sprint closes: **drift clusters at status lines**, not evenly.

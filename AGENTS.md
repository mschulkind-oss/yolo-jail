# YOLO Jail: Agent Developer Guide

yolo-jail runs coding agents in an isolated container against a live-mounted
workspace, without exposing host credentials or identity.

**AGENTS ARE PACKS. Core does not know what an agent is.** There is no agent
registry, no `agents` config key, and no `YOLO_AGENTS`. Config carries ONE list
of `packs`; the fourteen that ship with yolo live in `packs/*/pack.json` and are
selected by BARE NAME — `"packs": ["claude"]` (counted against `ls packs/` 2026-09-04).
Six install an agent (`claude`, `copilot`, `opencode`, `pi`, `codex`, `agy`) and
**eight install no CLI at all**, in three kinds: `audio`, `host-processes`, `journal`,
`cgroup-delegate` and `serial` ship a LOOPHOLE each (`audio` also contributes two env
vars — the only one of the five that ships anything beside its loophole); `zai` and
`cerebras` ship neither CLI nor loophole — a provider and a profile apiece, the two
packs whose whole content is declarative facts (zai the first, cerebras the second);
and `guardrails` ships blocked-tool refusals and install requirements (core blocks
nothing by default since 9caba669 — the blocked tools are opt-in through it).
Anything that says "the six" is
describing the agent SUBSET.

**Every loophole yolo ships is a pack's, and there is no other channel** (2026-08-19).
`journal` and `cgroup-delegate` were Go functions the run pipeline called by hand — one
switched by a top-level `journal` config key, the other by nothing at all. `audio` and
`host-processes` came out of `bundled_loopholes/`. `claude-oauth-broker` was the last
inhabitant of that directory and is now a **contribution of `packs/claude`**, not a pack
of its own: the dependency is structural, so selecting the claude pack is the dependency
(loophole-activation.md OQ-A10). `bundled_loopholes/` and its embed are DELETED.
Three consequences worth knowing before touching any of this: `paths.BuiltinLoopholeNames`
and `loopholes.ReservedLoopholeNames` are both GONE (a reserved name and a pack-shipped
name cannot be the same name — the pre-flight is fatal, so it refuses every launch that
selects the pack); the top-level `journal` and `host_processes` keys are now REFUSALS that
name their replacements; and the pack-shipped SUBSET is the only vocabulary left, so
`publishes: "endpoint"`, `jail_env` and an absolute `requires.file_exists` are refused for
every manifest yolo reads — there is no wider-vocabulary source to fall back to.

**Nothing is active by default**: an empty config yields a
jail with no coding agent, and says so at launch (`run.warnIfNoPacks`).
`internal/config/validate.go` hard-errors on `agents` on the host (and warns
in-jail, where the config is the generated snapshot).

What a pack declares (`internal/packdecl`), all read through `internal/packload`:
install spec, mounts, writable/shared dirs, host-file grants, composed
`surfaces`, launch flags, and named `hooks`. The boot path renders every one in a
single loop (`entrypoint/packsurfaces.go`) with no switch on any tool name. Two
things worth knowing before you debug:

- **The MOUNT is the filter.** The entrypoint renders every pack under
  `YOLO_PACK_ROOT`, so `stagePacks` copies only the SELECTED packs into the
  mounted tree. Staging all six and filtering later renders packs nobody asked
  for. A dropped pack therefore has to be UNSTAGED or it keeps rendering:
  `_official/` is cleared wholesale (it is derived from the binary's embed.FS),
  and each configured pack's dir is pruned when its slug leaves `packs` —
  contents-only, never the staging root itself, whose inode a live jail's
  `/ctx/packs` bind captured (`packstage` rule 3). A pack still configured but
  unresolvable this launch (offline git remote) is KEPT, not pruned.
- **`packload.Embedded*` is deliberately NOT selection-gated.** The reservation
  lists (`host_files` writable roots, `writable_home_dirs` segments, GlobalHome
  subdirs) cover every pack yolo SHIPS, or a `host_files` entry could claim a
  path a pack added tomorrow needs.
- **`packload.Embedded()` is ONE temp tree for the WHOLE PROCESS, released on the
  way out.** It is materialized before argv is even parsed — `internal/config`'s
  `hostFileWritableRoots` is a package-level var whose initializer reaches it — so
  every `yolo` invocation pays for it, `--version` included. `Pack.Root` is a
  handle into that tree, nothing can know when the last read happens, and the only
  exit paths are `cli.Main` (deferred) and `entrypoint.Main` (explicit, since
  `execBash` replaces the process). A second process-lifetime copy is the bug to
  watch for: three call sites made their own, each leaking a never-removed ~200 KB
  directory per invocation (measured live 2026-09-03: 625 dirs, 109 MB) — call
  `MaterializeEmbedded` directly only when you delete the dest yourself
  (`internal/cli/run/packs.go` stages out of one).

`agentcfg.BuiltinManifest()` is now core's own surfaces only (`mise/config`);
callers wanting the full set merge pack surfaces via `ManifestWith`.
`internal/jailcontent` (was `internal/agents` until the name outlived the
registry) keeps only what was never per-agent: skills staging, briefing
composition, loophole descriptions, the source-tree probe.

Backends are `podman`, `container` (Apple Container), and `macos-user` (macOS
Seatbelt, no VM) — **Docker was removed**; validate.go hard-errors on it too.

**This file is the guide for developing yolo-jail itself.** It deliberately does
not restate usage or config reference material — see "Where things live" below.

## Architecture

Seven commands in `cmd/`, **65** packages (`go list ./...`, counted 2026-08-23 — this line said
~43 for long enough that it stopped being an estimate and became wrong). Everything is Go; the
only bash/Python left is generated *content* (shims, `.bashrc`) emitted by
`internal/entrypoint` — **no generated in-jail CLIENT survives**, because two
implementations of one client is the drift the transport unification exists to
end (`docs/design/loophole-transport.md` §8.4).

| Binary | Runs where | Role |
|---|---|---|
| `yolo` | host **and** in-jail | the CLI; also every host daemon (see below) |
| `yolo-entrypoint` | container PID 1-ish | provisions the jail at startup |
| `yolo-jaild` | container | in-jail daemons |
| `yolo-ps` | container | host-process view (the `host-processes` loophole) |
| `yolo-cglimit` | container | cgroup-delegate client (the one AF_UNIX consumer left) |
| `yolo-journalctl` | container | journal-bridge client (loopback-TLS) |
| `goprobe` | nowhere | deployment tripwire; excluded from runtime PATH |

**A new `cmd/` binary must be added to `flake.nix`'s `shippedBinaries` AND to
`scripts/stage-source-bundle.sh`'s `SHIPPED_BINARIES`** or it silently vanishes
from the image (source build) or from a shipped bundle's image, while
`go build ./...` stays green. `internal/entrypoint/shippedclients_test.go` pins
all three spellings together; `goprobe` is the one declared exemption.

**Host ship set is just `{yolo}`** — `just install` runs `go install ./cmd/yolo`
and nothing else. The other four are image-side only.

**Daemons are subcommands, not separate binaries.** Host daemons are hidden
self-exec subcommands of `yolo`:
`yolo internal daemon <claude-oauth-broker|host-processes|journal>`,
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
- **No dev-override fast loop any more.** The old `/opt/yolo-jail/dist-go`
  wrapper (which let the outer jail's `yolo`/`yolo-entrypoint` prefer a
  `just build-go` artifact over the baked binary) is GONE, along with the
  `/opt/yolo-jail` source bind. The image now bakes REAL-FILE copies of all
  four shipped binaries at `/opt/yolo-jail/bin/` (with `/bin/<name>` symlinks
  and the flake bundle at `/opt/yolo-jail/share/yolo-jail`; see `installPrefix`
  in `flake.nix`). **The outer jail's binaries are now frozen at the
  host-loaded image** until a host `just load` — you can no longer live-patch
  them in-jail. Verify Go changes by launching a **nested** jail (from a
  throwaway workspace — see "Nested-jail verification is mandatory" under
  Testing for the full command and why the `cd` matters): its `AutoLoadImage`
  nix-builds the
  live `/workspace` checkout from source (the `goSrc` fileset, NOT `dist-go/`),
  so the nested image carries your edits for all four binaries. This is the
  accepted fast-loop regression. **`YOLO_REPO_ROOT` is not optional** — since
  2026-08-31 the cwd no longer selects the flake, so a bare `yolo -- bash`
  resolves the BAKED `/opt/yolo-jail/share/yolo-jail` bundle and verifies the
  image you already have. The launch prints which one it took
  (`Flake source: … (…)`); read that line before believing a nested green.
- `just build-go` is now purely the **cross-compile-for-shipping** step
  (`bin/linux-<arch>` prebuilt artifacts consumed by the flake's prebuilt
  short-circuit in a shipped bundle) — it no longer feeds any in-jail run.
- `flake.nix` changes are **fully verifiable in-jail**, runtime behavior
  included. A nested launch (`YOLO_REPO_ROOT=/workspace yolo -- bash`, run from a
  throwaway workspace) uses the CLI's own `AutoLoadImage`, which
  builds the flake (`nix build` delegates to the host daemon; see "Nix inside
  the jail"), notices the nix store path changed, materializes + loads the new
  image into the **nested** podman, and runs *that* — not the current baked
  image. Verified 2026-07-22: adding `imagePkgs.hello` to `corePackages` made
  `hello` resolve to `/bin/hello` and run inside the very next nested
  `yolo -- bash` (before the cwd removal; the same run needs
  `YOLO_REPO_ROOT=/workspace` today). So a newly-baked package on PATH, a changed `Env`, a new shim —
  all observable from in here. Do the whole edit → build → run-the-new-image
  loop in-jail. (The confusing part: the nested run BUILDS a fresh image every
  time the flake changes; it only prints "loaded image from cache" when the
  store path is unchanged from a prior build. "Building yolo-jail-…" + "Image
  load needed: nix store path changed" is the fresh-build path.)
  - Two real caveats remain. (1) **A failed nix build now STOPS the jail** —
    fatal by default since 2026-08-15, printing nix's own stderr; it used to fall
    back silently to the loaded/cached image, so a broken flake looked like a
    working jail on **stale** code (see the bullet below). `YOLO_ALLOW_STALE_IMAGE=1`
    opts back into continuing, and says so loudly. (2) The
    *host's own* jails keep running the host-loaded image until a host `just
    load` — so host-gating is real for **shipping** a flake change to the
    maintainer's day-to-day jails, not for **validating** it.
- **The `goSrc` fileset trap** (`flake.nix`): the hermetic image build only sees
  `go.mod`, `go.sum`, `vendor/`, `cmd/`, `internal/`, and `packs/`.
  A Go package outside that set **silently vanishes from the image**; the moment
  anything under `cmd/` imports it the build fails with "cannot find module
  providing package" while `go build ./...` stays green. Add it to the fileset
  by hand. `packs/` is the live example of an explicit entry (`bundled_loopholes/`
  was the other until it was deleted on 2026-08-19); `tools/` and `integration/`
  are excluded on purpose (nothing in `cmd/` imports them).
- **A failed nix build stops the jail** (fatal since 2026-08-15). `AutoLoadImage`
  prints nix's own stderr plus a classification, then exits — it does NOT fall
  back to the already-loaded image or the newest cached tar. It used to, and a
  broken build then looked like a working jail running **stale** code, reported
  two layers from its cause: a macOS nightly failed on a lib-farm assertion
  (`libzbar.so.0 not linked into /lib`) when the real fault was that the image was
  never rebuilt. `YOLO_ALLOW_STALE_IMAGE=1` restores the old behaviour for the
  case it was right for — an offline or disk-starved machine with a good cached
  image — and still prints the whole report. **`SkipBuild` is untouched:** no
  build ran, so nothing failed.
- **THE TWO HALVES DEPLOY ON DIFFERENT CADENCES, and a launch now REFUSES when
  they disagree.** The image rebuilds itself from the live tree on every launch
  (`AutoLoadImage`); the host `yolo` changes only when a human runs `just
  install`. So any commit that moves a host↔jail contract — a mount destination,
  an env var name, an argv the entrypoint parses — leaves the machine skewed **by
  default**, and it stays that way silently: the launcher emits the old argv while
  the freshly-built `yolo-entrypoint` expects the new one. Shipped once (2026-08-30,
  `a813b865`) and cost a boot that died as `mkdir /home/agent/.yolo: read-only file
  system` three genSteps deep, after streaming 3.3 GB. Since `3a348c18`,
  `version.SourceSkew` compares the binary's ldflags commit stamp against the tree's
  HEAD **through the `goSrc` fileset + the flake files** and `Run` refuses before the
  build, naming `just install`. It is silent for a docs-only commit (HEAD moved, the
  image did not), for uncommitted work (HEAD did not move, and `just build-go` stamps
  the by-path binary with the same HEAD), and for anything it cannot prove — an
  unstamped binary, a stamp naming a commit this repo lacks. `YOLO_ALLOW_SOURCE_SKEW=1`
  overrules it. **The integration suite cannot see this class**: `TestMain` always
  `go build`s a fresh CLI and `ensureJailImage` refuses a stale image, so the suite
  only ever runs fresh-launcher + fresh-image — the one pairing that ships is the one
  it cannot represent.
- **The cwd does not choose the flake** (since 2026-08-31). `internal/reporoot`
  used to walk up from the working directory for a dir holding both `flake.nix`
  and `go.mod`, ahead of every bundle — so the same `yolo` built its image from a
  LIVE checkout in one directory and from the snapshot `just install` staged in
  the next, and the startup banner could not tell you which (its version string
  is a `git describe` of whatever root won). That walk was also the only way the
  two halves above could disagree at all: both bundles ship WITH the binary, so
  neither can be older than it. Three sources remain — `YOLO_REPO_ROOT`, a
  `share/yolo-jail` bundle beside the binary (Homebrew, the release archive, the
  baked `/opt/yolo-jail` prefix), then `~/.local/share/yolo-jail/flake-bundle`
  from `just install` — and every container launch now prints which one it took:
  `Flake source: <path> (<what selected it>)`, in Phase 1, before staging and the
  nix build. `yolo check` appends the same provenance to its `flake.nix found:`
  line. Two things follow. A from-source developer gets the STAGED BUNDLE even
  inside the checkout, so `just install` is how an image change is delivered, not
  a chore to remember first (export `YOLO_REPO_ROOT=~/code/yolo-jail` to build
  from live source everywhere instead — the skew gate still guards it). And
  in-jail, bare `yolo` resolves the BAKED bundle, which is why every nested-jail
  command in this file now carries `YOLO_REPO_ROOT=/workspace`.
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
- **The suite refuses to run against a STALE jail image.** `TestMain` always
  builds a fresh host `yolo`, but the image is loaded at most once and reused — so
  the suite used to test new host code against an old baked `yolo-entrypoint`
  (this is what made a new `pack.json` field look like a regression: ~10 tests
  failed with `unknown field "tier"` from the PREVIOUS entrypoint). Now
  `ensureJailImage` compares `nix eval .#installPrefix.outPath` (what this tree
  would bake — an eval, ~0.3s, never a build) against `readlink
  /bin/yolo-entrypoint` inside the loaded image, and **aborts with the fix
  command** on a mismatch. `installPrefix` is the right oracle because it covers
  exactly the `goSrc` fileset + `flake.nix` while being invariant across the
  full/minimal variants and `packages:` lib-farm images. Knobs:
  `YOLO_TEST_REBUILD_IMAGE=1` forces a rebuild+reload (~45s in-jail);
  `YOLO_TEST_IMAGE_SKEW=warn|off` downgrades the check (`fail` is the default,
  and darwin auto-downgrades to `warn` because a Linux-runner-built image can
  never match a darwin eval). **`git add` before rebuilding** — nix sees tracked
  files only, so an untracked new file moves neither side and the check reports a
  false "matches" (same trap as nested-jail verification).
- **A test that pins the CALLEE while the CALL SITE is unpinned is not a test**, and this
  repo has shipped that shape five times. Two spellings of it:
  - the test exercises a function directly and nothing fails when its production
    caller is deleted — so the feature can be switched off wholesale with the unit
    gate green (measured: patching `hostLoopbackPlanFor` changed nothing, because
    the shared-namespace path never calls it);
  - the test asserts the SENTENCE a comment makes rather than the system —
    `packsurfaces_test.go` said *"the JAIL loader trusts the staged tree (the host
    already applied the gate)"* and then bypassed the jail loader, so it verified
    the assumption instead of the behaviour, and the gate was unenforced for months.
  When adding a test, ask: **does it fail if I delete the call site?** If not, add the
  one that does. Adversarial mutation runs in this repo find this class more often
  than they find wrong logic.
- **No agent tests.** Automated tests must never start `claude`/`copilot`/
  `codex`/etc. interactively or make API calls. `--version` probes only.
- **Nested-jail verification is mandatory** for `cmd/` and `internal/` changes:
  after `just build-go`, run the freshly-built binary BY PATH, pointed at the
  live tree, **from a throwaway workspace**:
  ```console
  $ mkdir -p /tmp/yolo-nested && cd /tmp/yolo-nested
  $ YOLO_REPO_ROOT=/workspace /workspace/dist-go/linux-$(go env GOARCH)/yolo -- bash
  ```
  The two halves are unrelated protections. `YOLO_REPO_ROOT` names the flake
  (without it `dist-go/` has no bundle beside it and the launch refuses
  outright). The `cd` protects the session you are running in: the per-workspace
  home overlay is `<workspace>/.yolo/home` (`prepare.go:288`), and for
  `/workspace` that directory **is** the live jail's home — `/workspace/.yolo/
  home/claude` and `/home/agent/.claude` are the same inode. A nested launch on
  `/workspace` therefore regenerates agent config over the running session's own
  home; measured 2026-07-21, it ate 479 Claude history entries. Any other
  workspace gets its own overlay and cannot reach ours. It also verifies MORE: a
  fresh workspace exercises first-boot provisioning, which `/workspace` (already
  provisioned by the outer jail) skips. Mount
  failures, permission errors, and read-only-fs conflicts only appear when a
  container actually starts. Unit tests do not catch them. **Not bare `yolo`:**
  that is the baked launcher (frozen at the last host `just load`), so a
  launcher/argv-side change isn't in it — bare `yolo` silently tests the OLD
  launcher, and a stale launcher emitting an argv the freshly-built nested image
  rejects is exactly how a fixed jail looks broken. **Never `just install`
  in-jail** — it refuses (`YOLO_VERSION` set), because `go install` shadows the
  baked `/bin/yolo` on PATH with a stale GOBIN copy; rebuild the image, not a
  binary.
- **CARVE-OUT: a nested jail is STRUCTURALLY BLIND to host-reachability
  changes** — the one case where the instruction above is actively misleading
  rather than merely insufficient. Podman-in-podman forces `--net=host`
  (netavark cannot create a netns without `NET_ADMIN`), and that is the ONE mode
  in which the loopback-forwarding class of bug **cannot** reproduce: the jail
  shares the launcher's network stack, so the host's loopback and the jail's are
  the same loopback. Anything touching how a jail reaches a host daemon — the
  `--network` flag (`internal/cli/run/hostloopback.go`), `internal/svcendpoint`'s
  bind/advertise pair, the `host.containers.internal` hop, the in-jail
  reachability probe — therefore gets a **free green** from a nested jail no
  matter how broken it is. That is how a total loopback-TLS outage shipped and
  went unnoticed for four days
  (`docs/design/loopback-tls-reachability.md` §3 row 6, §7; the same warning
  heads `integration/reachability_test.go`). A nested green means the plumbing is
  wired, never that the forwarding works: the only measurement that settles it is
  a REAL jail on a rootless host, reported together with
  `podman info --format '{{.Host.RootlessNetworkCmd}}'`.
  **What DOES work from in here is bare `podman run`** — the blindness is
  `yolo`'s forced `--net=host`, not the jail. Bind a listener on this jail's own
  loopback and dial it from a container that uses the stack under test; the jail
  is the "host" for that container, and the bug reproduces exactly:
  ```console
  $ python3 -m http.server 18080 --bind 127.0.0.1 &
  $ probe='(exec 3<>/dev/tcp/169.254.1.2/18080) 2>/dev/null && echo CONNECT || echo FAIL'
  $ podman run --rm --network=pasta localhost/yolo-jail:latest bash -c "$probe"
  FAIL                                        # the outage, reproduced
  $ podman run --rm --network=pasta:--map-host-loopback,169.254.1.2 \
      localhost/yolo-jail:latest bash -c "$probe"
  CONNECT                                     # the fix, measured
  ```
  Measured 2026-08-17 on podman 5.8.4 + pasta 2026_07_16, and the slirp4netns
  twin the same way (`--network=slirp4netns:allow_host_loopback=true`, dialling
  the `10.0.2.2` gateway). This proves the FLAG does what it claims; it still
  says nothing about a given host's passt build, which is why the launcher probes
  for the flag before emitting it.

## Invariants & gotchas

- **Run `yolo check` after every edit** to `yolo-jail.jsonc` or
  `~/.config/yolo-jail/config.jsonc`, before asking a human to restart. Use
  `yolo check --no-build` for a fast in-jail preflight. Config changes also
  trigger a y/N diff prompt at startup — don't rely on it to catch mistakes.
- **Shims are unconditional ONCE GENERATED, but nothing is blocked by default.**
  `defaultBlockedList()` is EMPTY since 2026-09-04: `grep -r` and `find` moved to
  the **`guardrails`** pack (`packs/guardrails`), which a user config opts into via
  `packs`. The old default silently assumed the image bakes `rg` and `fd` — true of
  the container backends, false of macos-user, which bakes nothing, so the block
  removed a tool and pointed at a binary that did not exist. A blocker also declares
  a `replacement` and is **generated only when that binary is on the agent's PATH**
  (`agentPath`, the one authority for which PATH counts), so a block can never leave
  a jail with neither the tool nor its alternative. A generated shim is still
  unconditional at run time unless `YOLO_BYPASS_SHIMS=1` — set it for installers and
  scripts that need the real tool. A user's own `security.blocked_tools` is
  unaffected, and an entry naming the same tool as a pack's REPLACES it whole.
- **Use `shquote.Join`** (`internal/shquote`) for anything crossing into the
  container's `bash -c`.
- **Podman-in-podman**: when already inside a container the CLI uses
  `--userns=host` (doubly-nested user namespaces fail mounting `/proc`) and
  forces `--net=host` (netavark can't create netns without `NET_ADMIN`). Inner
  containers also need `--cgroups=disabled` — both are image defaults in
  `/etc/containers/containers.conf`.
- **The default `network.mode: bridge` no longer means silence.** It still emits
  no `--net` flag, but the launcher now reads `podman info` and, on a *rootless*
  podman whose `rootlessNetworkCmd` it recognises, adds the option that forwards
  the host's LOOPBACK into the jail — `--network=pasta:--map-host-loopback,…` or
  `--network=slirp4netns:allow_host_loopback=true`. Without it every loopback-TLS
  service is unreachable from every jail on a pasta host (podman's default since
  5.0). `internal/cli/run/hostloopback.go` is the whole decision and states why
  every unproven fact emits nothing; `YOLO_NO_HOST_LOOPBACK=1` is the loud escape
  hatch back to the old argv. An explicit `network.mode` is never overridden.
- **The launcher tells the jail what it decided**, via
  `YOLO_HOST_LOOPBACK=requested|shared|unsupported|unknown`, emitted on EVERY
  launch — so an absent variable means only "launcher older than the variable".
  The in-jail reachability witness (`internal/entrypoint/reachability.go`) cannot
  derive it: from inside, "this host cannot forward loopback" and "yolo asked and
  the service is still down" are the same observation. **That witness is FATAL**
  (since 2026-08-18): an enabled jail-facing service the jail cannot use REFUSES
  the launch, in all three fault classes (unreachable, unpublished, rejected —
  OQ-R4). Severity is the disposition's decision alone: only `requested` and
  `shared` escalate, and `unsupported`/`unknown`/absent never do, because a host
  yolo could not ask is never refused for what it cannot help (OQ-R3). The escape
  hatch is `YOLO_ALLOW_UNREACHABLE_SERVICES=1`, forwarded from the host env, and
  the refusal names it.
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
  via launchers in `~/.yolo/bin/launch/`.
- **PATH order** (exact — corrected 2026-08-23 against `BootPath`, `internal/entrypoint/boot.go:356-361`,
  which is the authority this line claims to mirror):
  `$HOME/.yolo/bin/block:$NPM_CONFIG_PREFIX/bin:<mise-shims>:$GOPATH/bin:$HOME/.local/bin:/bin:/usr/bin:$HOME/.yolo/bin/launch`.
  **This line used to put `$HOME/.local/bin` second**, ahead of npm and mise; it is fifth. Only the
  two ends were right — and they are the part that carries meaning (see the next bullet).
- **Two generated script dirs, at opposite ends of PATH** — they are different
  mechanisms, not one dir with two kinds of file in it:
  `~/.yolo/bin/block` holds **blockers** (`GenerateShims`: `grep`, `find` → refuse,
  print a suggestion, `exit 127`) and must PRECEDE the real tool, because
  interception is its whole job. `~/.yolo/bin/launch` holds **lazy installers**
  (`GenerateAgentLaunchers` / `GeneratePackageManagerLaunchers`: `claude`,
  `pnpm` → install on first use, then `exec` the real binary) and is ordered
  LAST, after `/bin`, so a launcher is reached only when nothing else provides
  the name. That is what makes a pack declaring `program fzf` unable to shadow
  the image's `/bin/fzf` — the failure is unrepresentable rather than handled.
  A tool that is both blocked and pack-declared gets one of each, and the
  blocker wins by position. **Both dirs share ONE bind-mount anchor** at
  `~/.yolo/bin` (from `<ws>/.yolo/home/yolo-bin` under a `:ro` `/home/agent`), so
  both are cleared CONTENTS-ONLY (`resetAnchorDir`) — never `RemoveAll`. Gathered
  in the filesystem, never on PATH: nothing may put the shared parent on PATH, or
  a launcher would be reachable from the blockers' position. They were
  `~/.yolo-shims` and `~/.yolo-launchers` until 2026-08-30 (`a813b865`), and
  `removeRetiredGeneratedDirs` empties those for one release.
  Consequence to know: a name the IMAGE bakes now beats a pack's declared
  version. Right for `fzf`; re-check it before baking a package whose name a
  pack also claims (no shipped pack collides today).
- **Env hygiene** (agents can't handle interactive UI): `PAGER`/`GIT_PAGER`
  =`cat`, `BAT_PAGER=""`; `EDITOR=cat` (stops `git commit` hanging) but
  `VISUAL=nvim` (human ctrl-g editing); the host's `TERM` is forwarded so color
  output survives; `OVERMIND_SOCKET=/tmp/overmind.sock` so jail overmind doesn't collide with the
  host's; `LD_LIBRARY_PATH=/lib:/usr/lib:/usr/lib/<multilib>` baked into the
  image Env to survive agents sanitizing the environment.
- The built-in skills (`configuring-the-jail`, `diagnosing-the-jail`, and
  source-tree-only `developing-yolo-jail`) are injected into every jail. The
  one-time host→jail handoff is NOT a skill: a fresh `.yolo/handover.md` the host
  agent filed is surfaced as a **Handoff** section in the environment briefing and
  consumed by the run pipeline — but only once a briefing has actually been WRITTEN,
  so a jail whose packs declare no briefing destination leaves the pointer fresh
  instead of eating it. Core cannot tell an agent launch from `yolo -- bash`, so a
  consumed handoff is announced on stderr with the `mv` that restores it — see
  [docs/design/host-to-jail-handoff.md](docs/design/host-to-jail-handoff.md) §9.
  Skill priority: built-in < host user-level < workspace.

## Where things live

| Topic | Authority |
|---|---|
| Config keys, all of them | `yolo config-ref` |
| Pack manifest schema | `internal/packdecl/packdecl.go` (the doc comments ARE the reference) |
| Pack authoring + the `packs` key | `yolo pack --help`, `docs/design/pack-system.md` |
| CLI surface | `yolo --help` |
| End-user usage, devices/GPU, mise tools, `yolo-cglimit` | `docs/guides/USER_GUIDE.md` |
| Mounts, overlays, home layout | `docs/design/jail-home.md` |
| Per-agent briefing generation | `docs/design/agent-briefings.md` |
| MCP/LSP config, node wrappers, `LD_LIBRARY_PATH` story | `docs/design/mcp-configuration.md` |
| Loopholes (`audio`, `host-processes`, `journal`, `cgroup-delegate` in packs of their own; `claude-oauth-broker` contributed by `packs/claude`) | `docs/guides/loopholes.md`, `docs/design/loophole-protocol.md` |
| Config-change confirmation flow | `docs/design/config-safety.md` |
| Storage paths and state separation | `docs/design/storage-and-config.md` |
| What the image must bake vs. what a launch delivers; the rebuild/reload cost model | `docs/design/image-staging-vs-baking.md` |
| Cgroup delegate security model | `docs/design/security-shim.md` |
| macOS backends | `docs/guides/macos.md` |
| macos-user nix integration + disabled-feature surface | `docs/design/macos-user-nix-and-features.md` |

Agent logs, for debugging: `~/.copilot/logs/`,
`~/.claude/projects/` inside the jail; same paths under
`~/.local/share/yolo-jail/home/` on the host.

## Workflow

1. Image change → edit `flake.nix`, then verify end-to-end in a nested jail
   (`cd /tmp/yolo-nested && YOLO_REPO_ROOT=/workspace yolo -- bash` — never from
   `/workspace`, see Testing): the nested run rebuilds the flake and runs the NEW
   image, so
   runtime behavior is observable in-jail (see "Build & deploy"). Watch the build
   output — a failed build is now fatal and prints nix's stderr. A host `just load`
   is only needed to ship the change to the maintainer's own jails, not to
   validate it.
2. Logic change → edit `cmd/`/`internal/`, `just build-go`, verify by running
   the freshly-built binary BY PATH, from a throwaway workspace:
   `cd /tmp/yolo-nested && YOLO_REPO_ROOT=/workspace /workspace/dist-go/linux-$(go env GOARCH)/yolo -- bash`.
   NOT bare `yolo` — that is the
   baked launcher and won't carry a launcher/argv-side change (see the
   nested-jail-verification invariant above). Never `just install` in-jail.
   **Reachability-shaped change** (`--network`, `svcendpoint`'s bind/advertise,
   `host.containers.internal`)? A nested jail cannot see that class at all — read
   the carve-out under Testing before reporting it verified.
3. `just format` (gofmt) before committing.
4. Conventional commit messages. The pre-commit hook runs `just check-ci`; if it
   rejects, fix forward — never `--no-verify`, never `--amend`.
5. End of task: `git status` clean, `just done` green.
6. **Doc change that makes a claim about the code** → check it before writing it. A number, a
   `file:line`, a commit SHA, or a negative ("X has no caller") is the exact place a reader stops
   checking, so a wrong one is worse than none. The five sweeps that keep this corpus honest — and
   the allowlists they need, since a doc recording a deletion is *supposed* to name what it deleted
   — are written down in [docs/plans/README.md](docs/plans/README.md#keeping-this-corpus-honest--the-five-checks-so-they-are-re-runnable).
   Run them when a sprint closes: **drift clusters at status lines**, not evenly.

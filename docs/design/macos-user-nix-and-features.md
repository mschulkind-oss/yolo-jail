# macos-user: nix integration and the disabled-feature surface

**Status:** DESCRIPTIVE (2026-07-23), **re-verified and amended 2026-08-24** — documents what the
shipped code does, not a proposal. Records both the *by-design* differences and the current *gaps*
(things a config key or message implies but the code does not yet wire).

**What moved on 2026-08-24.** A backend-parity sweep (48 agents, adversarially verified) found ten
silent drops across the non-podman backends. **Seven entries in this doc changed state**, and this
doc was materially incomplete about every one of them:

| New state | Entry | Where |
| :--- | :--- | :--- |
| **FIXED** | pack `launch` contributions never applied (`dc1349a6`) | §3.6 |
| **WARNED** | pack `state` at `scope: workspace` is machine-wide here (`8ab03d2e`) | §3.1 |
| **WARNED** | briefings + skills, including the built-in suite, are never delivered (`6a53a2a3`) | §3.6 |
| **WARNED** | `lsp_servers` binaries never install (`6a53a2a3`) | Part 2 retraction, §3.6 |
| **WARNED** | `resources` are not enforced (`8ab03d2e`) | §3.2 |
| **WARNED** | `cache_relocations` are not implemented — and the symlink workaround is **false** (`8ab03d2e`) | §3.1 |
| **WARNED** | every loophole is inert here, config-declared ones included (`35448719`, `6a53a2a3`, `a639394d`) | §3.5 |

Two claims are **retracted**: `lsp_servers` was never carried by a "bootstrap env → lazy install"
channel (no such channel exists — Part 2), and §3.6 still called the config-diff approval prompt a
live security gap on 2026-08-23 when it had been fixed on 2026-08-18 (`bb825486`). The 2026-08-23
entries below (`d0961f2c`, `workspace_readonly` and `per_side_paths`) still hold.

> **Read every entry below as one of three states, and do not blur them — the distinction is this
> doc's whole value.**
>
> | State | Means |
> | :--- | :--- |
> | **FIXED** | the mechanism now works on this backend. Nothing to work around. |
> | **WARNED-BUT-ABSENT** | the mechanism is still absent, but the launch now SAYS so on stderr. A wiring or design gap, fixable in principle; the warning is the interim contract. |
> | **STRUCTURALLY IMPOSSIBLE** | there is no macOS mechanism to implement it against. Warning is the terminal state, not a stopgap. |
>
> Every `file:line` in an entry dated 2026-08-24 was read against the tree on 2026-08-24. None of
> the 2026-08-24 fixes or warnings has been verified on hardware — they are unit-tested and
> mutation-checked in a Linux jail, and podman-in-podman cannot exercise this backend at all.

**Scope:** the `macos-user` backend only (native macOS user + Seatbelt, **no VM,
no container, no OCI image**).
**Reads with:**
[backend-parity.md](backend-parity.md) (the sweep this doc's 2026-08-24 amendments come from —
the same defect shape across all three backends, and the census proposed to make it unwritable),
[macos-no-vm-direction.md](macos-no-vm-direction.md) (why the backend exists and
the three-axes framing),
[macos-user-build-step-threat-model.md](macos-user-build-step-threat-model.md)
(the host-side nix build as an attack surface),
[../guides/macos.md](../guides/macos.md) (user-facing setup),
[../research/macos-support-matrix.md](../research/macos-support-matrix.md) (the
authoritative status tracker).

## The one thing to internalize

On the container backends (`podman`, `container`) yolo runs a **Linux** agent
inside a VM, and every host↔jail seam is a **bind mount**: `/workspace`, the
`/home/agent` overlay, `/nix`, the host-service socket dir, cache relocations.
Nix's job there is to **build the aarch64-linux OCI image**.

`macos-user` has **none of that**. There is no container, so there are **no bind
mounts of any kind**, no image, and no VM. The agent is a real macOS process
running as the hidden `_yolojail` user, confined by an Apple Seatbelt
(`sandbox-exec`) profile. The "workspace" is the actual host directory (reached
through a shared-group ACL, not a mount); "home" is the real `/Users/_yolojail`.
Nix's job here is completely different: **materialize `packages:` as native
`aarch64-darwin` binaries** and put their store `bin` dirs on the agent's PATH.

Almost everything on the "disabled" list below follows mechanically from *no
container*: a feature that is implemented as a container flag or a bind mount
simply has no host to attach to.

---

## Part 1 — how the nix integration works

### 1.1 What nix produces (and what it does not)

| Backend | Nix builds | Where packages live | Arch |
|---|---|---|---|
| `podman` / `container` | the whole **OCI image** | baked into the image layers | `aarch64-linux` |
| **`macos-user`** | a **`buildEnv` profile** of `packages:` only | a `/nix/store/…` path on the **host**, its `bin` prepended to the agent PATH | **`aarch64-darwin`** |

There is **no image** on macos-user, therefore **no Linux builder and no Cachix
image download** are involved (those exist only for the container runtimes — see
[macos-no-vm-direction.md](macos-no-vm-direction.md) axis 2). The only nix work
is realizing the declared packages natively.

### 1.2 The two nix invocations

Both run **on the host, as the invoking (admin) user, before the sandbox is
entered**, with `cmd.Dir = repoRoot` (the flake dir) and
`cmd.Env = os.Environ() + YOLO_EXTRA_PACKAGES`
(`internal/darwinpkg/materialize.go`, argv from `internal/darwinpkg/darwinpkg.go`):

```
# 1. Realize the buildEnv profile, print its store out-path:
nix --extra-experimental-features 'nix-command flakes' --accept-flake-config \
    build --impure --no-link --print-out-paths --print-build-logs \
    .#packages.aarch64-darwin.yoloDarwinPackages

# 2. Best-effort read of the "no darwin build" skip list (120 s timeout, non-fatal):
nix … eval --impure --json .#darwinUnavailablePackages.aarch64-darwin
```

`yolo` **never execs the build output** — it only reads the out-path from stdout
and prepends `<out>/bin` to the agent's PATH (plus `PKG_CONFIG_PATH=<out>/lib/pkgconfig`
if that dir exists). See `darwinpkg.ProfilePaths`.

**Why invocation 2 is best-effort (120 s timeout, non-fatal).** The skip list is
*advisory only*: the build (invocation 1) already drops packages with no
aarch64-darwin build — `flake.nix` filters `darwinPackages` before the
`yoloDarwinPackages` buildEnv — so it succeeds whether or not this eval runs.
The eval's sole job is to *name* those dropped packages for the "Skipped packages
with no aarch64-darwin build: …" warning (`orchestrator.go`). Because a `nix eval`
can hang (e.g. evaluating an uncached nixpkgs), `skippedNames`
(`internal/darwinpkg/materialize.go`) bounds it at 120 s and returns `nil` on
timeout *or* any error. The only consequence of failure: the user loses that
informational warning — packages are still filtered, the PATH is still correct,
and the run proceeds normally.

Why each flag:

- **`--impure`** — the flake reads `packages:` from the environment via
  `builtins.getEnv("YOLO_EXTRA_PACKAGES")` (the same contract the image build uses
  through `nix build --impure`). Without `--impure` that read returns empty and no
  declared packages are built. This is structural, not optional.
- **`--accept-flake-config`** — makes nix honor *this flake's own* declared
  binary cache (`yolo-jail.cachix.org`). Without it nix prints "ignoring untrusted
  flake configuration setting 'extra-substituters'" and forces a from-source
  darwin build even when a cached closure exists. A trusted user still gates
  whether the substituter is actually consulted; it mutates no system `nix.conf`.
- **`--extra-experimental-features 'nix-command flakes'`** — so the invocation
  works regardless of the host's `nix.conf`.

### 1.3 Ordering and failure handling

`internal/macosuser/orchestrator.go` `RunMacosUser` sequences it as:

1. `--dry-run` short-circuit (pure; builds + prints the plan, no nix, runs on Linux
   CI too).
2. Cheap gates **first** — macOS, not-root, `sandbox-exec` present, sandbox user
   exists — *before* the potentially long build.
3. **Materialize** (`config.EffectivePackages(cfg)` → `MaterializeDarwin`). On
   failure the run **aborts** with an actionable message rather than launching a
   half-provisioned sandbox. The build's stderr streams live (`--print-build-logs`)
   so a from-source darwin build is visible.
4. Build the run plan, run plan invariants (one of which asserts every darwin
   store `bin` dir actually reached the launch PATH — the acceptance-bar guard).
5. Install Seatbelt profile → stage the yolo binary → bootstrap → launch.

**Skipped packages (no aarch64-darwin build) are warn-and-skip today**, not a hard
error (`flake.nix` filters via `darwinUnavailablePackages`; the orchestrator warns
and continues). This was a shipped divergence from the direction doc's original
"aggregate error" intent. **Decided 2026-07-23: revert to the designed behavior** —
an aggregated hard error listing every unavailable package, plus a per-platform
`linux-only` override so a config can legitimately mark a package Linux-only. The
old in-code objection (a hard error would abort the whole nix eval) is handled by
raising the error **host-side after** the eval, from the returned skip list minus
the Linux-only allowlist — the eval stays green. Tracked as roadmap **A2**
([macos-revival-and-distribution-plan.md](../plans/macos-revival-and-distribution-plan.md)),
which supersedes Open Decision #5.

### 1.4 Requirements (`yolo check` verifies these)

- **nix on PATH** (native darwin nix). No nix → the agent gets none of its declared
  tools.
- **`flake.lock` at the repo root** — pins nixpkgs so darwin packages are
  reproducible across machines. Note this is *declarative* reproducibility (same
  nixpkgs attrs), **not byte-identical to the Linux jail** — the builds are darwin,
  not linux.
- **Trusted nix user** — a warning, not a failure. A non-trusted user can still
  build from cache; being trusted is what lets `--accept-flake-config` actually use
  the project's cachix.

### 1.5 Nested nix *inside* the sandbox

On the container backends yolo can bind-mount the host nix daemon socket +
`/nix/store:ro` so `NIX_REMOTE=daemon` "just works" for nested builds (skipped on
macOS by default; `YOLO_NIX_HOST_DAEMON=1` opts back in — see
[../guides/macos.md](../guides/macos.md)). On macos-user the agent runs natively
and simply sees the host's real `/nix` (subject to the Seatbelt read policy), so
there is no mount to arrange and no `YOLO_NIX_HOST_DAEMON` toggle. There is also no
`/nix` bind mount to reason about because there is no container.

### 1.6 Security note

The host-side build is an unconfined step running as the invoking user, with
inputs (`packages:`, the `repoRoot` flake) that a prior agent session could have
written. That trust-boundary inversion — and the `repoRoot`-hijack vector — is
analyzed in full in
[macos-user-build-step-threat-model.md](macos-user-build-step-threat-model.md).
This doc does not restate it; if you touch `resolveRepoRoot` or the darwinpkg
flags, read that one first.

---

## Part 2 — what the agent config path *does* carry

The native bootstrap (`yolo internal darwin-bootstrap`, self-exec'd as
`_yolojail`) runs the **same pure generators** the Linux entrypoint runs
(`internal/entrypoint/darwin.go` `RunDarwinBootstrap`), because they are already
pure functions of `*Env`. So the per-workspace config surface is preserved:

| Config | macos-user | How |
|---|---|---|
| `packages:` | ✅ | native aarch64-darwin nix (Part 1) |
| `security.blocked_tools` | ✅ | generated shims (`GenerateShims`) |
| `mise_tools` | ✅ | `ConfigureMisePrism` |
| `lsp_servers` | ⚠️ | **config renders, binaries never install** — WARNED 2026-08-24, see §3.6 |
| `mcp_servers` + `mcp_presets` | ✅ | `GenerateMCPWrappers` |
| `packs` selection | ⚠️ | staged + `YOLO_PACK_ROOT` → `LoadJailPacks` → surfaces/hooks. **Wired 2026-08-12 (B-0); UNVERIFIED on a Mac** |
| `env_sources` | ✅ | `config.ResolveEnvSources`, layered into the launch env |
| git identity | ✅ | host git config → `YOLO_GIT_*` → `configureGit` (host creds never cross) |
| `macos_log` | ✅ | the `yolo-log` helper (Apple unified-logging bridge): `off`/`user`/`full` |
| pack `launch` flags | ✅ | **FIXED 2026-08-24** (`dc1349a6`) — hoisted above the dispatch, see §3.6 |
| briefings + skills (incl. built-ins) | ❌ | **never delivered** — WARNED 2026-08-24, see §3.6 |
| pack `state`, scope `workspace` | ⚠️ | one shared home, so **machine-wide** here — WARNED 2026-08-24, see §3.1 |

> **The `packs` row was a ✅ that had never been true, and the correction is worth keeping
> visible.** The row used to read "`agents` selection ✅ — `YOLO_AGENTS` → per-agent config",
> naming a mechanism that no longer exists (agents are packs; there is no `YOLO_AGENTS`) for
> a backend that rendered **zero** pack surfaces on every launch. The machinery in
> `RunDarwinBootstrap` was real, but the run pipeline returned at the macos-user branch
> *before* pack staging, so `LoadJailPacks` / `ConfigurePackSurfaces` / `RunPackHooks` each
> looped over an empty list — no error, no warning (`roadmap.md` B-0).
>
> The ordering is fixed: staging now happens above the backend dispatch, the tree is copied
> into the root-owned state dir (`/var/yolo-jail/packs/<session>`, the analogue of the
> container's `:ro` `/ctx/packs`), and its path is baked into the bootstrap argv as
> `YOLO_PACK_ROOT`. Every part of that is asserted by the plan invariants and unit tests on
> Linux. **What no Linux test can answer is whether the sudo stage commands and the
> sandbox-uid read actually work on a Mac**, which is why the row is ⚠️ and not ✅ — the
> lesson of this entry being a stale ✅ in the first place is not one to repeat with a
> fresh one.

### ⚠ Retracted 2026-08-24: `lsp_servers` "bootstrap env → lazy install"

The `lsp_servers` row read **`✅ — bootstrap env → lazy install`** until today. The ✅ was
half-true and the mechanism named does not exist.

What is true: the **config** renders. `runplan.go` bakes `YOLO_LSP_SERVERS` onto the bootstrap
argv (`internal/macosuser/runplan.go:173-174`) and the prism renders it into each agent's config
(`internal/entrypoint/packsurfaces.go:242`), so the agent is *told* the server is enabled.

What is false: nothing installs the binary. The LSP installer is a block of the **generated
bootstrap script** (`internal/entrypoint/shell.go:241-313`), written by `GenerateBootstrapScript`,
whose only caller is the Linux boot loop (`internal/entrypoint/boot.go:459`) — `RunDarwinBootstrap`
deliberately does not run it (`internal/entrypoint/darwin.go:49-80`, and see its own header comment
naming the container bootstrap script as a step not run here). Verified 2026-08-24.

And there is no "lazy install" channel to fall back on. **The only lazy-install channel is
`~/.yolo-launchers`**, and it carries exactly two things: one launcher per pack `program`
contribution (`internal/entrypoint/shims.go:166-228`) and `pnpm`
(`internal/entrypoint/shims.go:272-296`, whose comment states "the only lazily-installed package
manager is pnpm"). An `lsp_servers` entry contributes to neither. So the agent gets a config
pointing at a language server that is not on disk. WARNED as of `6a53a2a3` — see §3.6.

Two stale ✅s in one twelve-row table (this one and `packs`) is why the sibling
[backend-parity.md](backend-parity.md) §7 argues this matrix should eventually be **generated**
from a per-backend census rather than maintained by hand beside the code.

Two macOS-only pieces run here that the Linux boot does not: the `yolo-log`
helper, and the **login-rc PATH re-prepend** (`.zprofile`/`.zshrc`/`.bash_profile`),
which re-asserts the sandbox PATH *after* macOS `path_helper` reorders it. The
Linux-only boot steps (LD cache, cgroup delegation, port forwarding, the daemon
supervisor, the container bootstrap/venv/cglimit/journalctl scripts) are
deliberately **not** run — they are no-ops or nonsensical on a native user.

---

## Part 3 — the disabled / degraded surface

Grouped by *why* it is off. "Disabled by design" / "structurally impossible" = the mechanism
cannot exist without a container/VM/Linux kernel. "Not wired" = a config key, message, or
helper exists but the macos-user run path never reaches it — a real gap, not a
principled omission.

**Since 2026-08-24 a third question applies to every entry: does the launch SAY so?** That is
the distinction the header's three-state legend draws, and it is now the one that matters most
day to day — six entries below changed state on 2026-08-24 with the underlying mechanism
unchanged, purely because the launch started saying so. "Not wired **and** silent" is the defect
([backend-parity.md](backend-parity.md) calls the class a *silent drop*); "not wired and loud"
is an honest, usable backend.

### 3.1 Bind mounts — none exist

There is no container, so **nothing is bind-mounted**. This subsumes a whole class
of container features:

- **`/workspace` mount** → replaced by direct host-directory access under the
  shared root (`/Users/Shared/yolo/<name>`), granted via a shared-group ACL, not a
  mount. The workspace **must be neutral ground** — never inside any user's home;
  the plan invariants reject a home-dir workspace.
- **`/home/agent` overlay** → replaced by the real `/Users/_yolojail` home.
- **`cache_relocations`** → **STRUCTURALLY IMPOSSIBLE as a mount, and WARNED since 2026-08-24**
  (`8ab03d2e`). Relocation moves a cache subdir onto other storage *by bind-mounting it into the
  container*; with no container there is no mount point. The agent's cache is just files under
  `/Users/_yolojail`. A configured `cache_relocations` now prints a per-key warning naming the
  subdirs that stay put (`internal/macosuser/orchestrator.go:189-194`, verified 2026-08-24).

  > [!WARNING]
  > **"Just symlink it yourself" does NOT work here, and other docs still say it does.**
  > The Seatbelt profile denies writes everywhere and re-allows only
  > `{workspace, sandbox home, /tmp, /private/tmp, /var/folders, /private/var/folders, /dev}`
  > (`internal/macosuser/seatbelt.go:47-55`), and separately denies reads under `/Volumes`
  > except the boot volume (`internal/macosuser/seatbelt.go:59-60`). Both verified 2026-08-24.
  > So a host symlink from `~/.cache/<subdir>` to another disk resolves fine for the invoking
  > user and is refused inside the sandbox — which is the *worse* failure, because the cache
  > then silently stays on the boot volume, the one outcome the feature exists to prevent.
  >
  > The false claim is not in this doc; it is in
  > [`../guides/USER_GUIDE.md`](../guides/USER_GUIDE.md) ("The `macos-user` backend has no
  > container and no bind mounts, so a plain host symlink already works there") and in
  > [`../plans/cache-relocation.md`](../plans/cache-relocation.md) ("the `macos-user` backend is
  > unaffected … so a plain host symlink already works there"). Both predate the Seatbelt write
  > deny-list. **This doc is the authority for this backend; those two are wrong.**
- **`writable_home_dirs`** → **not applicable.** This knob carves writable
  subpaths out of an otherwise-`:ro` `/home/agent` container mount. On macos-user
  the home is natively writable (the Seatbelt profile allows writes under the
  sandbox home), so the concept has no target.
- **pack `state` at `scope: workspace`** → **MACHINE-WIDE here, and WARNED since 2026-08-24**
  (`8ab03d2e`). Every other backend gives each workspace its own copy of these dirs by mounting
  a per-workspace host dir at each path. This backend has no mounts and its home is a
  **constant** — `SandboxHome()` returns `/Users/_yolojail` with no workspace component
  (`internal/macosuser/macosuser.go:52-53`) — so the whole per-workspace tier collapses into one
  directory shared by every workspace on the machine. The five shipped dirs affected are
  `.claude`, `.codex`, `.pi`, `.copilot` and `.gemini` (the `state`/`scope: workspace`
  contributions of `packs/{claude,codex,pi,copilot,agy}/pack.json`, resolved through
  `packload.WritableDirs` → `packdecl.WritableDirContributions`,
  `internal/packload/packload.go:433-435`). All verified 2026-08-24.

  **This is the mirror image of issue #39.** There, Apple Container mounted the machine-wide
  tier from the per-workspace state dir and made a *machine* tier per-workspace. Here the same
  two tiers collapse the other way: the *workspace* tier becomes machine-wide.

  > [!WARNING]
  > **The sandbox enforces the boundary one layer down and then leaks it through the shared
  > home.** The Seatbelt profile denies reads under `/Users` and re-allows only `/Users`,
  > `/Users/Shared`, the workspace's own subpath, the sandbox home, and each *intermediate*
  > directory as a bare `(literal)` — chosen precisely so a **sibling checkout beside the
  > workspace stays denied** (`internal/macosuser/seatbelt.go:74-80`, and `ancestorLiterals`'
  > own rationale at `:144-148`; verified 2026-08-24). So the agent cannot read
  > `/Users/Shared/yolo/<other-workspace>/…` — and **can** read a transcript of that same
  > workspace's sessions at `~/.claude/projects/<other-workspace>/*.jsonl`, because that lives
  > under the sandbox home, which the profile allows wholesale. (`~/.claude/projects/` is where
  > the agent's own per-project logs land — `AGENTS.md`, "Agent logs, for debugging".) The
  > denial and the leak are the same content reached two ways.

  **WARNED, not fixed, and the reason is a real design constraint** — this is not a wiring
  gap like the pack `launch` flags. The single home *is* this backend's shared-credentials
  mechanism (§3.5: one real `~/.claude/.credentials.json` is what makes the oauth-broker
  unnecessary here). Splitting the home per workspace would break the **machine** tier to
  repair the **workspace** tier. A fix has to restore both tiers explicitly, which is a design
  change and not a launch-time patch (`internal/cli/run/run.go:188-203`).
- **~~`workspace_readonly`~~ → FIXED 2026-08-23** (`d0961f2c`). It *was* silently inert — built
  only in the container run pipeline — and, as this section predicted, the fix was a wiring gap
  rather than a structural impossibility: the Seatbelt profile is a write deny-list with re-allows,
  so the policy is now expressed there. **This is a behaviour change for anyone who set the key on
  this backend** and has been writing to the workspace; it has a release-note entry.
  Not verified on hardware — the profile and its call sites are pinned by mutation-verified tests in
  a Linux jail, but nobody has watched a write fail on a Mac. See
  [host-execution-from-the-workspace.md](host-execution-from-the-workspace.md) §5.5.
- **`per_side_paths`** → **structurally impossible, and it now SAYS so.** It gives the host and
  the sandbox different contents at the same path, which is a mount-namespace capability: Seatbelt
  can deny a path, it cannot fork one. As of `d0961f2c` it **warns** on this backend rather than
  being silently absent — *shipping a new default that is silently missing on one backend would
  repeat the exact defect the line above fixed.*
- **host-service socket dir mount** → see loopholes below.

### 3.2 Resource limits — no cgroups

macOS has no cgroup filesystem, and there is no VM to size.

- **`resources` (`cpus` / `memory` / `pids_limit`)** → **not enforced, and WARNED since
  2026-08-24** (`8ab03d2e`). The run plan does not apply them; there is nothing to apply them
  to. (On the container path Apple Container applies `--cpus`/`--memory` natively and
  podman-machine sizes the VM; macos-user has neither.) A configured `resources` section now
  prints a warning naming the keys that are read and ignored
  (`internal/macosuser/orchestrator.go:178-182`, verified 2026-08-24).

  **Why a fix is REFUSED rather than pending** — this is the terminal state, not a stopgap.
  The obvious substitute is POSIX rlimits, and neither one means what the config key means:

  - `RLIMIT_AS` is **address space, not RSS**, so it is not what `--memory` means. Capping it
    breaks JITs and the Go runtime, both of which reserve far more virtual address space than
    they ever fault in.
  - `RLIMIT_NPROC` is **per-USER, not per-process-tree**, so on this backend it would collide
    across concurrent sessions — every one of them runs as the same `_yolojail` account.

  A cap the user believes in but that does not hold is worse than a documented absence, so
  this warns and will keep warning (`internal/macosuser/orchestrator.go:173-177`;
  [backend-parity.md](backend-parity.md) §7 records the same refusal).
- **`yolo-cglimit`** and the cgroup-delegate daemon → not present (Linux-only).

### 3.3 Networking and ports

- **`network` modes (bridge/host/none)** → **not applied.** A native process runs
  on the host's real network; there is no network namespace to switch.
- **`ports` / `network.forward_host_ports`** → **not wired.** Port forwarding
  (socat/TCP-gateway on podman, `--publish-socket` on Apple Container) lives in the
  container launch path (`runContainer`), which the macos-user branch returns
  before reaching. A host service is simply reachable directly.

### 3.4 Devices and GPU

- **`gpu`** → unavailable on all macOS backends (Metal, no CUDA/ROCm).
- **`devices` / `cgroup_rule`** → Linux device paths and
  `--device-cgroup-rule`; not applicable.

### 3.5 Loopholes — mostly moot here; the framework still ports

The loophole host-services (`audio`, `host-processes`, `claude-oauth-broker`) and
the per-jail **broker relay** are started/stopped in `runContainer`
(`startLoopholes`/`stopLoopholes`) — the macos-user branch never reaches it.

> **The inertness is now REPORTED, since 2026-08-24** (`35448719`, extended by `6a53a2a3`).
> Until then this backend was the one inert backend that said nothing: a user could install and
> select a pack whose entire purpose is a loophole (`audio`, `journal`, `host-processes`), watch
> it install, and get a successful launch that ran none of it. `notePackLoopholesInert` is now
> called from the macos-user arm itself (`internal/cli/run/run.go:187`), printing one stderr
> line per inert loophole naming the backend as the reason
> (`internal/cli/run/loopholeinert.go:63-74, 113-123`). It is deliberately *not* routed through
> `startLoopholesDisclosed` — that wrapper exists to make disclosure inseparable from the
> **spawn**, and nothing spawns here.
>
> **Config-declared loopholes are covered too, as of `6a53a2a3`.** The report originally walked
> pack contributions only, so a user whose own `loopholes.<name>.command` named a daemon got no
> line at all — the silence read as deliberate. `configInertLines` now emits the `SourceConfig`
> half from the config map (`internal/cli/run/loopholeinert.go:132-144`). Both verified
> 2026-08-24.
>
> The gap survived this long for a reason worth keeping: the test for it called
> `notePackLoopholesInert` **directly** for both backends, so the macos-user half asserted a
> line no launch could produce — the callee was pinned and the call site did not exist. That is
> the shape `AGENTS.md` names under Testing, found inside the code written to prevent the same
> shape one layer down.
>
> Separately, the **briefing** used to advertise these loopholes to the agent under a heading
> reading "host capabilities wired into this jail". It is now gated on the backend
> (`internal/cli/run/prepare.go:70-73`, `a639394d`, verified 2026-08-24). An agent reading a
> false capability list does not merely lack a feature; it plans around one it does not have.

But "not wired" means something different for each, because a loophole is machinery
for punching a *specific* thing through a container boundary — and two of the
three have **no boundary to punch through** on a native process:

- **`audio`** — **moot, not a port gap.** The loophole bind-mounts the host
  PipeWire/PulseAudio sockets + `/dev/snd` into the Linux container; its own
  manifest says "macOS is deliberately unsupported — the macOS container runtimes
  don't bridge host CoreAudio." A native `_yolojail` process needs none of that: it
  can reach CoreAudio directly (subject to the Seatbelt profile and TCC), the same
  as any host process. There is nothing to port.
- **`host-processes`** — **moot, and if anything the native side is *less*
  restricted.** The loophole exists to give a *contained* jail an allowlisted
  read-only window onto host processes via a daemon (`yolo-ps`). A native process
  already sees host processes directly — the Seatbelt profile grants
  `(allow process-info*)` (`internal/macosuser/seatbelt.go`) — so the daemon is
  unnecessary. Note the flip side: the allowlist (`host_processes.visible`) is a
  container-only control, so on macos-user the agent sees the *full* host process
  table, not a filtered view. That is a widening of the surface, not a missing
  feature.
- **`claude-oauth-broker`** — **mostly moot on macos-user; skip it by default.**
  The broker bundles two jobs. (1) *Keep one shared credentials file* so jails
  don't diverge and burn the single-use refresh token — on containers this needs
  the `.claude-shared-credentials` bind (`assemble.go:157-160`). (2) *Serialize the
  refresh HTTP call* via a host-side `flock` (`internal/oauthbroker/refresh.go`
  `RefreshLockPath`); the TLS-intercept exists **only** because Claude Code does
  the refresh itself and will never voluntarily take our lock, so the terminator
  routes `platform.claude.com → 127.0.0.1` and inserts the flock on its behalf.
  On macos-user **job 1 is free**: every session shares the one real
  `/Users/_yolojail` home, hence one real `~/.claude/.credentials.json` — the
  shared home *is* the shared-creds mechanism. **Job 2 only bites with multiple
  *concurrent* Claude sessions** (both read the same single-use token and race);
  a shared file does not fix that, but porting the interception is genuinely hard
  natively (no `--add-host`; redirection would need root-global DNS/hosts control).
  So for the normal single-session case the broker is unneeded, and the awkward
  concurrent case is exactly the one to defer. `macosuser.EndpointGrantCommands`
  *exists* (two `chmod +a` ACEs letting the sandbox **user** read one published
  endpoint file, and traverse — not list — the per-jail directory holding it) but
  is **not called anywhere**. It replaced `BrokerSocketGrantCommands` in the
  loopback-TLS unification: there is no broker socket to grant any more, the
  jail-facing artifact is a `0600` endpoint file carrying a bearer token, and the
  old helper's `chgrp`+`chmod 0750` of the socket's *parent* would now widen a
  directory full of every service's credentials. Recommendation unchanged: leave it
  off; wire it only if concurrent macos-user Claude sessions become a real need —
  see Open item #3.

**The framework itself is worth keeping, and macos-user is arguably a *better*
fit than containers.** A loophole is just "a host-side daemon mediates the jail's
access to a resource" — nothing about that needs a container; only the *transport*
differs. On containers it's a bind-mounted socket + `--add-host` redirection; on
macos-user a native jailed process reaches host `localhost` sockets/ports
**directly** (the Seatbelt profile is `(allow default)` for network) and yolo
already injects the launch env, so a loophole collapses to *host daemon on a
localhost socket/port + a launch-env var pointing the jail's clients at it* — no
mount, no redirection plumbing. An **access-scoping / auditing proxy** (e.g. a
host-side daemon that filters and token-scopes the jail's GitHub traffic) fits
this cleanly: set `HTTPS_PROXY=http://127.0.0.1:PORT` in the launch env and
`git`/`gh`/`curl` all honor it. The *only* loophole shape that doesn't port
cheaply is transparent interception of an opaque client that ignores proxy vars
and pins its host — which is precisely the oauth-broker's awkward case above.
This is now on the roadmap as
[Track L in the revival plan](../plans/macos-revival-and-distribution-plan.md):
the framework plumbing is unblocked, but the specific access-scoping proxy is
gated on **OQ-L1** (the scoping model must be pinned down first — a wrong model
ships a false security boundary).

### 3.6 The container-launch preamble (config-diff prompt, image load, etc.)

The macos-user branch in `run.Run` returns **before** `runContainer`
(`internal/cli/run/run.go:132`, returning at `:209-210` where the container path continues at
`:212`), so everything that lives only in that function is skipped. Some are irrelevant (image
load, stale-container reaping, workspace flock). **Four are not**, and the 2026-08-24 sweep found
three of them — this section is where the `runContainer`-only class collects.

- **Pack `launch` contributions** → **FIXED 2026-08-24** (`dc1349a6`). `packload.InjectLaunchFlags`
  was called inside `runContainer`, so on this backend a `launch` contribution **did nothing at
  all**. The two shipped instances failed differently, and the difference is the reason this
  ranked as the sweep's one must-fix here rather than a warning:
  - copilot's `--yolo --no-auto-update` is a plain `launch` kind with **no autonomy config half
    to fall back on** (`packs/copilot/pack.json:71-83`) — a 100% drop.
  - claude's `--dangerously-skip-permissions` is the `autonomous.launch` half of an `autonomy`
    contribution whose `autonomous.config` half still rendered
    (`packs/claude/pack.json:91-98` and `:82`), so it degraded to `defaultMode: acceptEdits` —
    which auto-accepts **edits** and not Bash or WebFetch. A partial, silent downgrade of the
    autonomy the user asked for.

  Injection is now hoisted above the backend dispatch (`internal/cli/run/run.go:123-126`) and
  threaded into `runContainer` as a parameter (`internal/cli/run/run.go:362-363`) — the same
  move pack staging made for B-0, for the same reason. The empty case is deliberately guarded so
  each arm still reaches its own default (native zsh vs container bash). Pinned at the seam the
  backend actually receives, not on the injector, by
  `internal/cli/run/launchflagsdispatch_test.go:21`. Nothing downstream ever recovered these
  flags: both in-jail launcher templates end `exec "$REAL_BIN" "$@"` and never read the
  contributions. All verified 2026-08-24.

- **Briefings and skills** → **never delivered; WARNED since 2026-08-24** (`6a53a2a3`). This is
  the largest capability gap on this backend. `PrepareSkills` and the briefing composition both
  hang off `refreshJailBriefings` (`internal/cli/run/prepare.go:29`, `:89`), **whose only caller
  is inside `runContainer`** (`internal/cli/run/run.go:402`) — and the container path *delivers*
  them by **mounting** the staged tree (`:ro` from the per-jail staging dir:
  `internal/cli/run/assemble.go:456-457` for skills, `:543-546` for briefings). A mount is a
  mechanism this backend does not have, which is why this is a warning and not the same
  one-line hoist the `launch` flags got. So the agent starts with **no `AGENTS.md`, no
  `CLAUDE.md` and no skills — including the BUILT-IN suite**, which rides the same staging loop
  (`internal/jailcontent/skills.go:103`). All verified 2026-08-24.

  > [!WARNING]
  > **The sharp detail: the shims ARE generated.** `RunDarwinBootstrap` runs `GenerateShims`
  > (`internal/entrypoint/darwin.go:50`), so the blocked-tool blockers exist natively while the
  > prose explaining them does not. `grep -r` therefore exits 127 having **never told the agent
  > why** — the agent gets the enforcement without the contract.

  Warned at `internal/cli/run/loopholeinert.go:283-290`, called from
  `internal/cli/run/run.go:204`. Whether this gets a real delivery mechanism (compose above the
  dispatch, deliver by copy into the sandbox home) or stays a documented absence is
  **OQ-BP-2** in [backend-parity.md](backend-parity.md) — open, and the leaning there is to
  deliver it, alongside a Mac session.

- **`lsp_servers` binaries** → **never installed; WARNED since 2026-08-24** (`6a53a2a3`). The
  config renders and the agent is told the server is enabled; the installer is a block of the
  generated bootstrap script this backend deliberately does not run. Full evidence in the
  retraction under Part 2. Warned at `internal/cli/run/loopholeinert.go:291-296`, naming the
  configured keys and pointing at `packages:` as the native alternative. Verified 2026-08-24.

- **~~Config-change approval (y/N diff) prompt~~** → **FIXED 2026-08-18** (`bb825486`); see the
  retraction immediately below.

### ⚠ Retracted 2026-08-24: "the config-diff prompt is not reached on macos-user"

This section carried, through the 2026-08-23 re-verification, a bullet stating that
`checkConfigChanges` "is called only in `runContainer`" and is therefore "**not currently
reached on the macos-user path**", closing with "until that lands, treat the mitigation as absent
on macos-user".

**That has been false since 2026-08-18** (`bb825486`). The gate is called on the macos-user arm
itself, at `internal/cli/run/run.go:164`, before `MacosUserRun` (verified 2026-08-24). It sits in
the arm rather than hoisted above the dispatch on purpose, and the reason is load-bearing: the
container arm gates the **fresh-launch** path only, because attaching to a running jail
deliberately skips the check (the container was already started with its config). This backend
has **no attach** — every macos-user invocation is a fresh sandbox — so the arm's own call site
is where the two backends actually agree. `--dry-run` is exempt: it prints the plan and launches
nothing, so there is no change to approve, and refusing would only hide the diff the user asked
to inspect.

The threat model's reasoning stands and is now satisfied:
[macos-user-build-step-threat-model.md](macos-user-build-step-threat-model.md) lists this prompt
as the mitigation for a poisoned `packages:` edit (Vector A), and macos-user is the backend where
that nix build runs **unconfined as the invoking user** — the worst place to lose it. Roadmap
**A1** is done.

**Keep this retraction visible**: a doc that re-verified its inert list on 2026-08-23 still
reported a security gap that had been closed five days earlier. Re-reading a list for staleness
catches claims that *became* wrong; it does not catch claims that became *right*, and a false
"you are unprotected" is its own kind of harm.

### 3.7 The `macos_shared_root` config key — referenced, not implemented

**What it's *for*.** macos-user has no bind mount, so the workspace must be
"neutral ground" — a directory outside every user's home that the invoking admin
and the `_yolojail` sandbox user can *both* reach via a shared-group ACL (a
home-dir workspace is rejected by a plan invariant, `runplan.go`). That neutral
root defaults to the hard-coded `/Users/Shared/yolo`
(`macosuser.SharedRootDefault`). `macos_shared_root` was *intended* to be the
escape hatch: relocate that root — e.g. onto another volume, or a site-specific
path — for someone who can't or won't use `/Users/Shared`. The plan-invariant
error message already advertises it ("set config `macos_shared_root` to another
non-home path").

**But no code reads that key.** `SharedRootProvisionCommands` accepts a `root`
argument, yet every caller passes `""`, which falls back to the default. So the
root is effectively fixed and the error message points at a knob that does
nothing.

**Do we need it? Decided: no.** `/Users/Shared` exists on every stock macOS and is
exactly the OS-blessed neutral location for cross-user data — the default satisfies
the real requirement (a non-home shared root) out of the box. An override would
only matter for the narrow "put workspaces on another disk / a policy-mandated
path" case, which no current user has. So the decision (2026-07-23) is to **drop
the key from the error message and not implement it** — roadmap **A3**; revisit only
if a relocated-root use case ever lands (which would then need the key agreed at
*both* setup-time provisioning and the run-time workspace-location check).

---

## Part 4 — at-a-glance matrix

The `macos-user` column is stamped with one of the three states from the header: **FIXED**
(works), **WARNED** (absent, but the launch says so), **IMPOSSIBLE** (no macOS mechanism to
implement it against — warning is terminal). Unstamped rows predate 2026-08-24 and are unchanged.

> [!NOTE]
> This matrix has now drifted twice (the `packs` row, the `lsp_servers` row). It should
> eventually be **generated** from the per-backend census proposed in
> [backend-parity.md](backend-parity.md) §4/§7 rather than maintained by hand beside the code.

| Feature | Container (`podman`/`container`) | `macos-user` | Reason |
|---|---|---|---|
| `packages:` | baked into aarch64-linux image | native aarch64-darwin buildEnv on PATH | different nix target |
| bind mounts (`/workspace`, home overlay) | yes | **none** | no container |
| `cache_relocations` | podman ✅ / AC ⚠️ | **off** — IMPOSSIBLE, **WARNS since 2026-08-24** (`8ab03d2e`); a host symlink is **not** a workaround (§3.1) | no mount; Seatbelt denies writes outside the workspace/sandbox home and reads under `/Volumes` |
| `writable_home_dirs` | yes | n/a | native home is writable |
| pack `state`, scope `workspace` | per-workspace dir per pack | **machine-wide** — WARNED since 2026-08-24 (`8ab03d2e`); mirror image of #39 | `SandboxHome()` is a constant with no workspace component; splitting it would break the machine tier |
| pack `launch` flags | yes | ✅ **FIXED 2026-08-24** (`dc1349a6`) — was a 100% drop for copilot, a silent autonomy downgrade for claude | injection hoisted above the backend dispatch |
| briefings + skills (incl. built-in suite) | `:ro` mount of the staged tree | ❌ **never delivered** — WARNED since 2026-08-24 (`6a53a2a3`); shims still generated, so `grep -r` exits 127 unexplained | delivery is a mount; needs a real native mechanism (OQ-BP-2) |
| `workspace_readonly` | podman ✅ / AC ❌ (`:ro` ignored) | ✅ **ENFORCED since 2026-08-23** (`d0961f2c`) via the Seatbelt profile — was a silent no-op | the wiring gap this doc predicted; see [host-execution-from-the-workspace.md](host-execution-from-the-workspace.md) §5.5 |
| `per_side_paths` | yes | **WARNS since 2026-08-23** (`d0961f2c`) — no equivalent exists, and it no longer pretends otherwise | needs a mount namespace; Seatbelt filters permissions, it cannot give one path two contents |
| `resources` (cpu/mem/pids) | podman-machine / AC native | **off** — IMPOSSIBLE, **WARNS since 2026-08-24** (`8ab03d2e`); a fix is refused, not pending | no cgroups/VM; `RLIMIT_AS` ≠ `--memory` and `RLIMIT_NPROC` is per-USER on a shared account (§3.2) |
| `network` modes | yes | **n/a** | runs on host net |
| `ports` / forward_host_ports | yes | **not wired** | container-path only |
| `gpu` | Linux only | off | Metal, no CUDA/ROCm |
| `devices` / `cgroup_rule` | Linux only | off | Linux kernel feature |
| loopholes: audio / host-processes | yes | **moot** | native process reaches CoreAudio / host procs directly |
| loopholes: claude-oauth-broker | yes | **skip** | shared home = shared creds; serialization only matters for concurrent sessions |
| loopholes (any pack- *or* config-declared) — is the inertness reported? | n/a | ✅ **REPORTED since 2026-08-24** (`35448719` + `6a53a2a3`); briefing no longer advertises them (`a639394d`) | this backend starts no loophole host service at all (§3.5) |
| loophole *framework* (new host-mediated access) | via mount + `--add-host` | ✅ (localhost socket + launch env) | native process reaches host localhost directly |
| config-diff approval prompt | yes | ✅ **FIXED 2026-08-18** (`bb825486`) — this row said "not reached" until 2026-08-24 (§3.6 retraction) | gated on the arm itself; this backend has no attach path |
| `security.blocked_tools` shims | yes | ✅ | pure generator |
| `mise_tools` | yes | ✅ | pure generator |
| `lsp_servers` | yes | ⚠️ **config renders, binaries never install** — WARNED 2026-08-24 (`6a53a2a3`) | installer is the generated bootstrap script this backend does not run (Part 2 retraction) |
| `mcp_servers` / `mcp_presets` | yes | ✅ | pure generator |
| git identity (host-composed, `:ro`) | yes | ✅ (`YOLO_GIT_*` env) | pure generator |
| `env_sources` | yes | ✅ | `ResolveEnvSources` |
| `macos_log` | n/a | ✅ | native-only helper |
| isolation boundary | VM + Linux userns | Unix user + Seatbelt | weaker; documented tradeoff |

---

## Open items

**All four resolved 2026-07-23** (item 2 has since shipped — see its note). The three that need
code are on the roadmap at the **front** of the revival plan's
[Active work section](../plans/macos-revival-and-distribution-plan.md) (do-now);
the fourth is a settled no-op. Two further questions were **opened on 2026-08-24** by the
backend-parity sweep and are owned by that doc, not this one — they follow the four.

1. **`macos_shared_root`** (§3.7) — **decided: drop the mention, don't implement.**
   `/Users/Shared/yolo` covers the real need; remove the key from the
   plan-invariant error message. Roadmap **A3**.
2. **Config-diff prompt on macos-user** (§3.6) — **decided: fix it now** by hoisting
   `checkConfigChanges` ahead of the runtime split so every backend gates on it.
   Roadmap **A1**. — **DONE 2026-08-18** (`bb825486`), landed as a call site on the
   macos-user arm itself rather than a hoist, for the attach-path reason recorded in the
   §3.6 retraction. Confirmed at `internal/cli/run/run.go:164`, 2026-08-24.
3. **`claude-oauth-broker` on macos-user** (§3.5) — **decided: leave off.** The
   shared `/Users/_yolojail` home already gives one shared credentials file (the
   broker's main job on containers); refresh serialization only matters for
   *concurrent* Claude sessions and would need hard-to-port host redirection. Note
   `EndpointGrantCommands` as dead-until-needed. The loophole *framework* does
   port and is the motivating future case — roadmap
   [Track L / OQ-L1](../plans/macos-revival-and-distribution-plan.md).
4. **Skip-list policy** (§1.3) — **decided: implement the written design** (hard
   error + per-platform `linux-only` override), retiring today's warn-and-skip. A
   silently dropped tool that the config *declared* masks typos and diverges from
   the documented contract. Roadmap **A2**.

### Opened 2026-08-24 by the backend-parity sweep

These are **not** owned by this doc — they live in [backend-parity.md](backend-parity.md), which
is the sweep's home. Listed here because their answers change this backend's surface:

- 💬 **OQ-BP-2 (theirs): do briefings and skills get DELIVERED to macos-user, or stay a
  documented absence?** (§3.6, Part 2 table.) Today the agent starts with no `AGENTS.md`, no
  `CLAUDE.md` and no skills — including the built-in suite — while the blocked-tool shims *are*
  generated. Warned as of `6a53a2a3`. A fix means composing above the dispatch and delivering by
  copy into the sandbox home: a real delivery mechanism, not a moved call. Their leaning is
  **deliver it**, landed with a Mac session, because it writes into a shared root as another
  user and nobody can test that from Linux. **This is the largest capability gap on this
  backend.**
- 💬 **OQ-BP-3 (theirs): does a `Warned` disposition need to be suppressible?** Ten new launch
  lines exist as of 2026-08-24, six of them on this backend. A warning people learn to
  skip is worse than none. Their leaning is **not yet**, and per-key when it comes.

**Settled by the sweep and NOT reopened here** (both recorded in
[backend-parity.md](backend-parity.md) §7, both reflected above): per-workspace homes on
macos-user are **refused** — the single home is this backend's shared-credentials mechanism
(§3.1, §3.5) — and enforcing `resources` via rlimits is **refused** on the semantics grounds in
§3.2. Neither is a pending gap; both are terminal answers with a warning attached.

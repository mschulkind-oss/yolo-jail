# macos-user: nix integration and the disabled-feature surface

**Status:** DESCRIPTIVE (2026-07-23) — documents what the shipped code does, not a
proposal. Records both the *by-design* differences and the current *gaps* (things
a config key or message implies but the code does not yet wire).
**Scope:** the `macos-user` backend only (native macOS user + Seatbelt, **no VM,
no container, no OCI image**).
**Reads with:**
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

**Skipped packages (no aarch64-darwin build) are warn-and-skip**, not a hard
error (`flake.nix` filters via `darwinUnavailablePackages`; the orchestrator warns
and continues). This is a shipped divergence from the direction doc's original
"aggregate error" intent — see the plan's Open Decision #5
([macos-revival-and-distribution-plan.md](../plans/macos-revival-and-distribution-plan.md)
§0). The rationale in-code: a hard error would abort the whole nix eval.

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
| `lsp_servers` | ✅ | bootstrap env → lazy install |
| `mcp_servers` + `mcp_presets` | ✅ | `GenerateMCPWrappers` |
| `agents` selection | ✅ | `YOLO_AGENTS` → per-agent config |
| `env_sources` | ✅ | `config.ResolveEnvSources`, layered into the launch env |
| git identity | ✅ | host git config → `YOLO_GIT_*` → `configureGit` (host creds never cross) |
| `macos_log` | ✅ | the `yolo-log` helper (Apple unified-logging bridge): `off`/`user`/`full` |

Two macOS-only pieces run here that the Linux boot does not: the `yolo-log`
helper, and the **login-rc PATH re-prepend** (`.zprofile`/`.zshrc`/`.bash_profile`),
which re-asserts the sandbox PATH *after* macOS `path_helper` reorders it. The
Linux-only boot steps (LD cache, cgroup delegation, port forwarding, the daemon
supervisor, the container bootstrap/venv/cglimit/journalctl scripts) are
deliberately **not** run — they are no-ops or nonsensical on a native user.

---

## Part 3 — the disabled / degraded surface

Grouped by *why* it is off. "Disabled by design" = the mechanism cannot exist
without a container/VM/Linux kernel. "Not wired" = a config key, message, or
helper exists but the macos-user run path never reaches it — a real gap, not a
principled omission.

### 3.1 Bind mounts — none exist

There is no container, so **nothing is bind-mounted**. This subsumes a whole class
of container features:

- **`/workspace` mount** → replaced by direct host-directory access under the
  shared root (`/Users/Shared/yolo/<name>`), granted via a shared-group ACL, not a
  mount. The workspace **must be neutral ground** — never inside any user's home;
  the plan invariants reject a home-dir workspace.
- **`/home/agent` overlay** → replaced by the real `/Users/_yolojail` home.
- **`cache_relocations`** → **disabled by design.** Relocation moves a cache
  subdir onto other storage *by bind-mounting it into the container*; with no
  container there is no mount point. The agent's cache is just files under
  `/Users/_yolojail`.
- **`writable_home_dirs`** → **not applicable.** This knob carves writable
  subpaths out of an otherwise-`:ro` `/home/agent` container mount. On macos-user
  the home is natively writable (the Seatbelt profile allows writes under the
  sandbox home), so the concept has no target.
- **host-service socket dir mount** → see loopholes below.

### 3.2 Resource limits — no cgroups

macOS has no cgroup filesystem, and there is no VM to size.

- **`resources` (`cpus` / `memory` / `pids_limit`)** → **not enforced.** The run
  plan does not read them; there is nothing to apply them to. (On the container
  path Apple Container applies `--cpus`/`--memory` natively and podman-machine
  sizes the VM; macos-user has neither.)
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
(`startLoopholes`/`stopLoopholes`) — the macos-user branch never reaches it. But
"not wired" means something different for each, because a loophole is machinery
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
  concurrent case is exactly the one to defer. `macosuser.BrokerSocketGrantCommands`
  *exists* (it would chmod/chgrp the broker socket for the sandbox group) but is
  **not called anywhere**. Recommendation: leave it off; wire it only if concurrent
  macos-user Claude sessions become a real need — see Open item #3.

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

The macos-user branch in `run.Run` returns **before** `runContainer`, so
everything that lives only in that function is skipped. Most are irrelevant
(image load, stale-container reaping, workspace flock). One is worth flagging:

- **Config-change approval (y/N diff) prompt** — `checkConfigChanges` is called
  only in `runContainer`. It is therefore **not currently reached on the
  macos-user path.** This matters because the threat model
  ([macos-user-build-step-threat-model.md](macos-user-build-step-threat-model.md))
  lists the config-diff prompt as the mitigation for a poisoned `packages:` edit
  (Vector A) — and macos-user is the backend where that build runs *unconfined as
  the invoking user*, so it is the worst place to lose the prompt. This is a
  **security gap to fix, not just document**: it is on the roadmap as
  [J4 in the revival plan](../plans/macos-revival-and-distribution-plan.md), whose
  fix is to hoist `checkConfigChanges` ahead of the runtime split so every backend
  gates on it. Until that lands, treat the mitigation as absent on macos-user.

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

**Do we need it?** Almost certainly not, near-term. `/Users/Shared` exists on
every stock macOS and is exactly the OS-blessed neutral location for
cross-user data — the default satisfies the real requirement (a non-home shared
root) out of the box. An override only matters for the narrow "put workspaces on
another disk / a policy-mandated path" case, which no current user has. The
cheap, honest fix is therefore to **reword the message to drop the key** (remove
the false promise) and defer wiring until a concrete need appears; wiring it later
means agreeing on the key at *both* setup-time provisioning and the run-time
workspace-location check. Captured as Open item #1.

---

## Part 4 — at-a-glance matrix

| Feature | Container (`podman`/`container`) | `macos-user` | Reason |
|---|---|---|---|
| `packages:` | baked into aarch64-linux image | native aarch64-darwin buildEnv on PATH | different nix target |
| bind mounts (`/workspace`, home overlay) | yes | **none** | no container |
| `cache_relocations` | podman ✅ / AC ⚠️ | **off** | no mount |
| `writable_home_dirs` | yes | n/a | native home is writable |
| `resources` (cpu/mem/pids) | podman-machine / AC native | **off** | no cgroups/VM |
| `network` modes | yes | **n/a** | runs on host net |
| `ports` / forward_host_ports | yes | **not wired** | container-path only |
| `gpu` | Linux only | off | Metal, no CUDA/ROCm |
| `devices` / `cgroup_rule` | Linux only | off | Linux kernel feature |
| loopholes: audio / host-processes | yes | **moot** | native process reaches CoreAudio / host procs directly |
| loopholes: claude-oauth-broker | yes | **skip** | shared home = shared creds; serialization only matters for concurrent sessions |
| loophole *framework* (new host-mediated access) | via mount + `--add-host` | ✅ (localhost socket + launch env) | native process reaches host localhost directly |
| config-diff approval prompt | yes | **not reached** | `runContainer`-only (gap) |
| `security.blocked_tools` shims | yes | ✅ | pure generator |
| `mise_tools` / `lsp_servers` | yes | ✅ | pure generators |
| `mcp_servers` / `mcp_presets` | yes | ✅ | pure generator |
| git identity (host-composed, `:ro`) | yes | ✅ (`YOLO_GIT_*` env) | pure generator |
| `env_sources` | yes | ✅ | `ResolveEnvSources` |
| `macos_log` | n/a | ✅ | native-only helper |
| isolation boundary | VM + Linux userns | Unix user + Seatbelt | weaker; documented tradeoff |

---

## Open items (for a future maintainer pass)

1. **Reword `macos_shared_root` out of the error message** (§3.7): the key isn't
   read anywhere and `/Users/Shared/yolo` covers the real need, so drop the false
   promise now; wire the override (setup + run-time, in agreement) only if a
   relocated-root use case ever lands.
2. **Config-diff prompt on macos-user** (§3.6): the threat model assumes it runs;
   the code does not reach it — a security gap, since the poisoned-`packages:` build
   runs unconfined on this backend. **Decided: fix it** by hoisting
   `checkConfigChanges` ahead of the runtime split. Tracked as
   [J4 in the revival plan](../plans/macos-revival-and-distribution-plan.md).
3. **`claude-oauth-broker` on macos-user** (§3.5): **decided — leave off.** The
   shared `/Users/_yolojail` home already gives one shared credentials file (the
   broker's main job on containers); refresh serialization only matters for
   *concurrent* Claude sessions and would need hard-to-port host redirection. Note
   `BrokerSocketGrantCommands` as dead-until-needed; revisit only if concurrent
   macos-user sessions become real. The loophole *framework* itself does port
   (localhost socket + launch env) — an access-scoping/audit proxy is the
   motivating future case.
4. **Skip-list policy** (§1.3) — *the actual open question:* when a `packages:`
   entry has **no aarch64-darwin build**, should the run **warn and continue**
   (what ships today) or **hard-error**? The written design
   ([revival plan](../plans/macos-revival-and-distribution-plan.md) Open Decision
   #5) called for an *aggregated* error — collect every unavailable package and
   refuse to launch — plus per-platform `packages` overrides so a config could say
   "this one is Linux-only." Neither shipped: `flake.nix` filters unavailable
   packages via `darwinUnavailablePackages` and the orchestrator warns and
   continues, and `config.EffectivePackages` has no platform conditional at all.
   The in-code rationale for warn-and-skip: a hard error would have to abort the
   whole nix eval, and a warn-lets an otherwise-fine jail launch with one tool
   missing. The counter-argument: silently dropping a tool the config declared can
   mask a typo (an unknown attr is skipped, not flagged) and diverges from the
   documented contract. This is a **deliberate maintainer call to make**, not a
   bug: either bless warn-and-skip retroactively (a doc-hygiene fix to the plan) or
   add a J-track item implementing the aggregated error + overrides as designed.
   It stayed open after the M1 hardware run because M1 only exercised packages that
   *do* have a darwin build, so the no-build path was never observed live.

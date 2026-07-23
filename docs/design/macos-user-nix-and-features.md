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

### 3.5 Loopholes — not wired on this path

The loophole host-services (`audio`, `host-processes`, `claude-oauth-broker`) and
the per-jail **broker relay** are started/stopped in `runContainer`
(`startLoopholes`/`stopLoopholes`) — the macos-user branch never reaches it. Two
consequences worth calling out:

- **`claude-oauth-broker`** — the broker relay that serializes OAuth refreshes is
  a container-path feature. `macosuser.BrokerSocketGrantCommands` *exists* (it
  would chmod/chgrp the broker socket for the sandbox group) but **is not called
  anywhere** — so on macos-user today the broker loophole is effectively off. If
  multi-jail OAuth serialization is needed on macos-user, this is the wiring gap to
  close.
- **`audio` / `host-processes`** — likewise not wired.

### 3.6 The container-launch preamble (config-diff prompt, image load, etc.)

The macos-user branch in `run.Run` returns **before** `runContainer`, so
everything that lives only in that function is skipped. Most are irrelevant
(image load, stale-container reaping, workspace flock). One is worth flagging:

- **Config-change approval (y/N diff) prompt** — `checkConfigChanges` is called
  only in `runContainer`. It is therefore **not currently reached on the
  macos-user path.** This matters because the threat model
  ([macos-user-build-step-threat-model.md](macos-user-build-step-threat-model.md))
  lists the config-diff prompt as the mitigation for a poisoned `packages:` edit
  (Vector A). On macos-user that mitigation does not currently fire — treat it as a
  known gap, not a guarantee.

### 3.7 The `macos_shared_root` config key — referenced, not implemented

The plan-invariant error message tells the user they may "set config
`macos_shared_root` to another non-home path," but **no code reads that key.**
`SharedRootProvisionCommands` accepts a `root` argument, yet every caller passes
`""`, which defaults to the hard-coded `/Users/Shared/yolo`
(`macosuser.SharedRootDefault`). Until the key is actually plumbed
(setup-time provisioning **and** run-time workspace-location check must agree on
it), the shared root is effectively fixed. This is a documentation-vs-behavior gap
to either wire up or reword.

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
| loopholes (audio/host-proc/oauth-broker) | yes | **not wired** | container-path only |
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

1. **Wire or reword `macos_shared_root`** (§3.7): either read the key at both
   setup and run time, or drop it from the error message.
2. **Decide whether the config-diff prompt should run on macos-user** (§3.6): the
   threat model assumes it does; the code does not. Either move
   `checkConfigChanges` ahead of the runtime split, or update the threat model to
   state the mitigation is container-only.
3. **`claude-oauth-broker` on macos-user** (§3.5): `BrokerSocketGrantCommands` is
   dead code until the loophole is wired into the native launch. Wire it or note
   the loophole is container-only.
4. **Skip-list policy** (§1.3): warn-and-skip vs the direction doc's aggregate
   error is still Open Decision #5 in the revival plan.

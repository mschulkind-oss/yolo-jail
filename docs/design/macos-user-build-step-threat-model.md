# Threat model: the macos-user host-side nix build step

**Status:** DRAFT (2026-07-22), re-verified 2026-08-23 — **and OVERTAKEN on its sharpest vector on
2026-08-31.** `46655873` (*"stop letting the cwd choose which flake yolo builds"*) removed the
cwd walk-up from `internal/reporoot` entirely, for source-skew hygiene rather than for this threat
model — resolution is now `YOLO_REPO_ROOT` env → exe-relative bundle → staged install bundle, with
**no cwd read anywhere** (`reporoot.go:95-117`; the package doc at `:7-29` records the removal).
The same `Resolve` feeds the macos-user build (`run.go:45-46` → `MacosUserRun` →
`darwinpkg/materialize.go:44,62`), so **Vector B is dead**: a workspace-planted
`flake.nix`+`go.mod` can no longer be *selected*, only explicitly pointed at by a human setting
`YOLO_REPO_ROOT`. **H1 and Q1 are therefore moot** — the hardening they proposed is a subset of
what shipped (see Q1's Answer). What survives of this doc: Vector A, and **Q2, which is no longer
hypothetical:** `--accept-flake-config` is passed on every image `nix` invocation
(`internal/image/nixflags.go`) *and* on the darwin materialization
([`darwinpkg.go:91`](../../internal/darwinpkg/darwinpkg.go)), so the substituter surface Q2 asks
about is live rather than proposed. Q3 (the build sandbox) also stands, now motivated by Vector A
alone.

**Scope:** the `macos-user` backend only (native macOS user + Seatbelt, **no VM**).
**Reads with:** [macos-no-vm-direction.md](macos-no-vm-direction.md) (why macos-user
exists and how packages are materialized), [security-shim.md](security-shim.md)
(the privilege-separation model this backend is *not* fully aligned with),
[config-safety.md](config-safety.md) (the config-diff prompt, one of the
mitigations below).

## Summary

On `macos-user`, provisioning runs a **host-side `nix build`** as the *invoking
host user* — outside any sandbox — before the `_yolojail` agent is launched. Its
inputs (`packages:` from config, and the flake at the resolved `repoRoot`) can be
influenced by a prior agent session that had write access to the workspace. This
is a trust-boundary inversion relative to the container backends, where the agent
never triggers a host-user build.

**No path to arbitrary *host-user* or *root* code execution through the intended
inputs** — nix evaluation is a restricted language and `yolo` never execs the
build output host-side. But there **are** reachable outcomes that break the
`_yolojail` confinement or leak host state:

1. **`_nixbld`-level code execution** via an attacker-chosen package/flake build.
   The macOS nix build sandbox is **off by default**, so a malicious `builder`
   runs as the low-privilege `_nixbld*` daemon user with broad filesystem-read +
   network access — an escape from Seatbelt/workspace confinement, though not to
   the host user.
2. **Host environment-variable exfiltration** via `--impure` on an
   attacker-controlled flake (`builtins.getEnv` → fetch URL / fixed-output
   derivation).
3. **Supply-chain injection onto the agent's own PATH** via `--accept-flake-config`
   substituter poisoning — gated by the host being a nix *trusted-user*.

The sharpest vector **was** **`repoRoot` selection**: `resolveRepoRoot` walked *up from
cwd* for any directory holding both `flake.nix` and `go.mod`, and the host
operator typically launches `yolo` from inside the workspace. A flake planted at
the workspace root was therefore selected as the trusted build root, with **no
config-diff prompt** to flag it. **That vector died on 2026-08-31** (`46655873` removed the cwd
walk entirely — see Vector B), leaving Vector A as the live surface.

## Background: what the build step is

`macos-user` has no OCI image. `packages:` is materialized as a **native
aarch64-darwin `buildEnv`** and only the resulting `/nix/store/…/bin` is placed on
the sandboxed agent's PATH (`flake.nix:845` `packages.yoloDarwinPackages`; see
[macos-no-vm-direction.md](macos-no-vm-direction.md) axis 3).

The materialization runs **on the host, as the invoking user, before the sandbox
is entered** (`internal/macosuser/orchestrator.go` materializes first, then puts
the out-path bin dirs on the `_yolojail` PATH). The backend refuses to run under
`sudo` and does no user-switching itself; derivation realization is delegated to
the standard macOS multi-user nix daemon (`_nixbld*` build users).

Two `nix` invocations, both with `cmd.Dir = repoRoot` and
`cmd.Env = os.Environ() + YOLO_EXTRA_PACKAGES` (`internal/darwinpkg/materialize.go:41-52`,
argv from `internal/darwinpkg/darwinpkg.go:72-103`):

```
nix --extra-experimental-features 'nix-command flakes' --accept-flake-config \
    build --impure --no-link --print-out-paths --print-build-logs \
    .#packages.aarch64-darwin.yoloDarwinPackages
nix … eval --impure --json .#darwinUnavailablePackages.aarch64-darwin
```

`yolo` never execs the output; it only reads out-paths from stdout and prepends
`<out>/bin` to the agent PATH.

## Trust boundary

| Component | Runs as | Confinement |
|---|---|---|
| Agent session (writes workspace) | `_yolojail` | Seatbelt profile, workspace ACL |
| **Host-side `nix` client (eval)** | **invoking host user** | **none** |
| Derivation realization (build) | `_nixbld*` (daemon) | nix build sandbox — **off by default on macOS** |
| Resulting binaries on next agent's PATH | executed by `_yolojail` | Seatbelt |

The inputs to the unconfined host-user step (`packages:`, the `repoRoot` flake)
live in, or can be planted in, the space a *previous* `_yolojail` agent could
write. That is the inversion.

## What is and isn't reachable

**Not reachable through intended inputs:**
- Arbitrary **host-user** code execution. Nix eval is a restricted functional
  language: no `system()`/shell-out during evaluation unless
  `allow-unsafe-native-code-during-evaluation` is set (off by default). `yolo`
  never runs the build product host-side.
- **root.** No `sudo`; the backend hard-refuses UID 0.

**Reachable, driven by agent-influenceable inputs:**

### Vector A — poisoned `packages:` (via `YOLO_EXTRA_PACKAGES`)

`packages:` from the workspace `yolo-jail.jsonc` is serialized into
`YOLO_EXTRA_PACKAGES` and read by the flake via `builtins.getEnv`
(`flake.nix:114-117`). The object form (`flake.nix:110-113`) accepts an
attacker-chosen `{"nixpkgs": "<commit>"}` and a `{"url": "mirror://…", "hash":
"…"}` source override. The daemon fetches and builds that (as `_nixbld`, sandbox
off) and the output lands on the agent PATH.

*Mitigation present:* a `packages:` edit is a config change, so it surfaces in the
startup y/N config-diff prompt ([config-safety.md](config-safety.md)) — but only
if the human reads the diff. Object-form version/url specs also bypass the
`flake.lock` nixpkgs pin.

### Vector B — planted `flake.nix` + `go.mod` in the workspace (repoRoot hijack) — **DEAD since 2026-08-31**

> [!NOTE]
> **This vector no longer exists.** `46655873` deleted the cwd walk-up this whole section is
> about — `Resolve` reads `YOLO_REPO_ROOT`, then the exe-relative bundle, then the staged install
> bundle, and never the working directory (`reporoot.go:95-117`). A planted pair in the workspace
> is now unreachable unless a human exports `YOLO_REPO_ROOT` pointing at it, which is the explicit
> act H1 wanted to require. The section is kept as written below because it documents *why* the
> old shape was dangerous — read it as history.

`resolveRepoRoot` step 2 (`internal/reporoot/reporoot.go`, `Resolve`) **walked**
**up from cwd**, selecting the first directory that contains **both**
`flake.nix` and `go.mod`. The workspace lives under `/Users/Shared/yolo/<name>` and the operator
typically launches `yolo` from inside it, while the real yolo-jail checkout lives
elsewhere (e.g. `~/code/yolo-jail`) and is *not* an ancestor of the workspace. So
a `flake.nix`+`go.mod` pair planted at the workspace root is found first and
becomes `repoRoot` — the host user then runs `nix build --impure
--accept-flake-config` against an **attacker-authored flake**. There is **no
config-diff prompt** here: a stray `flake.nix` is not the config file.

The double-file requirement (comment at `reporoot.go:28-30`) exists to stop a
bare `flake.nix` from hijacking a *user's own* flake project — it does **not**
defend against a deliberately planted pair. Consequences of a poisoned flake:

- **`--accept-flake-config`** (`internal/darwinpkg/darwinpkg.go:41-45`) makes nix
  honor the flake's own `nixConfig` substituters + `extra-trusted-public-keys`.
  A poisoned flake can declare an attacker substituter with a matching trusted
  key and serve a signed malicious closure straight onto the agent PATH —
  **gated by the host user being a nix trusted-user** (exactly what
  `internal/cli/check/section_nix_probe.go` warns about when the user is
  "connected but NOT trusted").
- **`--impure`** lets the flake `builtins.getEnv` any host env var and smuggle it
  into a fetch URL or fixed-output derivation → host-env exfiltration.
- The flake defines the `builder` that runs (as `_nixbld`, sandbox off).

## Existing mitigations

- **Config-diff y/N prompt** at startup ([config-safety.md](config-safety.md)) —
  covers Vector A, **not** Vector B.
- **`--accept-flake-config` trust is daemon-gated** — substituter poisoning only
  works if the host user is a nix trusted-user.
- **`yolo check`** warns when the invoking user is connected to the daemon but
  not trusted (`internal/cli/check/section_nix_probe.go`), which is also the
  state that neutralizes Vector B's substituter path.
- **`flake.lock`** pins nixpkgs for the real repo (bypassed by object-form specs
  in Vector A, and irrelevant under a planted flake in Vector B).

## Residual gaps

1. ~~**No integrity check that `repoRoot` is the *real* yolo-jail checkout.**~~ **Closed by
   `46655873`**: the resolver no longer discovers roots by structure at all — it takes an explicit
   `YOLO_REPO_ROOT` or a bundle that shipped with the binary, neither of which an agent-writable
   directory can become.
2. **macOS nix build sandbox off by default** widens what a malicious builder can
   touch (broad FS read + network) — a nix-global default, not a `yolo` choice,
   but it shapes the blast radius.
3. **`--impure` is unavoidable** for the `YOLO_EXTRA_PACKAGES` contract, so
   host-env reads during eval are structurally available to whatever flake is
   selected.

## Proposed hardening (for discussion — see Open Questions)

- ~~**H1. Refuse a `repoRoot` under the workspace.**~~ **Superseded by `46655873`**, which removed
  the walk-up rather than fencing it — a strictly stronger form of the same hardening (see Q1).
- **H2. Verify a repo fingerprint.** Prefer an explicit `repo_path`/`YOLO_REPO_ROOT`
  and/or check a stable marker of the real checkout (module path in `go.mod`,
  a sentinel file) before trusting a discovered flake.
- **H3. Surface the resolved `repoRoot` in the config-diff prompt** so a hijack is
  visible at the same gate as a `packages:` change.
- **H4. Consider dropping `--accept-flake-config`** for the darwin materialization
  and instead pinning the substituter via the CLI's own flags, so a selected
  flake cannot introduce trusted keys.

## Open Questions

Two are live (Q2, Q3); Q1 is mooted by events and carries its Answer. The IDs **Q1 · Q2 · Q3** are
cited from [`../plans/roadmap.md`](../plans/roadmap.md) and are the stable names — do not renumber
them.

### ~~💬~~ Q1 — should `resolveRepoRoot` refuse a repoRoot located under the workspace?

H1 was the highest-leverage fix and low-risk: the real checkout is never under
`/Users/Shared/yolo/<name>`. The only cost was that a developer who deliberately
keeps their yolo-jail checkout *inside* a workspace would need `repo_path`.

_Leaning was:_ Yes — reject at-or-below `opts.Workspace`, with an actionable message
pointing at `repo_path`/`YOLO_REPO_ROOT`. Cheap, closes Vector B.

**Answer (2026-09-02, recording events):** **Mooted by `46655873` (2026-08-31), which shipped a
strictly stronger fix for an unrelated reason.** The cwd walk-up this question wanted to fence was
deleted wholesale — nothing under the workspace (or anywhere else cwd-relative) can be selected at
all, and `YOLO_REPO_ROOT` is the explicit act the leaning wanted to require. Done for source-skew
hygiene, not security, but the security property is what it is. Nothing further to build here;
reopen only if a cwd-relative resolution source is ever reintroduced.

### 💬 Q2 — is `--accept-flake-config` worth the substituter-poisoning surface?

Dropping it (H4) reintroduces the "ignoring untrusted flake configuration" noise
and loses the project's own cachix on untrusted-user hosts, forcing from-source
darwin builds. The gate (trusted-user) already narrows exposure.

_Leaning:_ Keep it for now (the trusted-user gate is a real barrier). *(The original pairing "with
H1+H3 so a planted flake can't reach the flag at all" is now free: the resolver change means no
planted flake reaches the flag, period. What the flag still exposes is the trust extended to
whichever flake IS selected — the staged bundle or an explicit `YOLO_REPO_ROOT` — which is Vector
A's territory.)*

**Answer:**
> _(empty — fill in when decided)_

### 💬 Q3 — do we want the macOS nix build sandbox on for yolo-triggered builds?

Turning it on (e.g. `--option sandbox true` on the darwin materialization) shrinks
the `_nixbld` blast radius, at some compatibility cost for packages that assume an
unsandboxed darwin build.

_Leaning:_ Investigate feasibility; not blocking. *(The original "H1 removes the
attacker-authored-flake path that makes this matter most" is now true via the resolver change, so
what keeps this question alive is Vector A alone: a malicious `packages:` builder still runs as
`_nixbld` unsandboxed.)*

**Answer:**
> _(empty — fill in when decided)_

## Test coverage note

~~No test currently exercises the `resolveRepoRoot` walk-up selection against a workspace-planted
flake.~~ The walk-up itself is gone (`46655873`), so the test H1 wanted is unwritable — there is no
selection to assert against. The property worth pinning instead, if any: `reporoot.Resolve` never
consults the working directory (its own package doc at `reporoot.go:7-29` states this as the
contract).

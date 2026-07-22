# Tool Provisioning: how the jail acquires runtimes and CLIs

How does a jail end up with `node`, `python`, `go`, `gopls`, `copilot`, `claude`,
`chrome-devtools-mcp`, and friends? Through **four different mechanisms**, each
chosen for a different reason — and the seams between them are exactly where the
"wait, why are there three Nodes?" confusion lives. This doc enumerates every
layer with file:line evidence, then untangles the multiple-versions situation so
the picture stops feeling accidental.

All claims below are grounded in the repo at the commit this doc was written
against (baked Node just bumped 22→24 in `230ca27`). Line numbers are current as
of that read; treat them as "look here", not eternal truth.

---

## TL;DR

- **Four layers install runtimes/tools**, in ascending order of "how often it
  changes": (1) baked into the Nix OCI image, (2) mise, (3) npm globals, (4) Go
  `go install` + native curl installers. PATH is wired so the *mise* copies win
  for bare commands; the *baked* copies are reached only by absolute path.
- **By default there is ONE of each runtime, not two.** node, python, AND go
  are now all Nix-baked (`/bin/node` v24.16.0, `/bin/python3`, `/bin/go` — all
  RPATH-self-contained). mise no longer installs any of them by default
  (2026-07-20 for node/python; go baked shortly after), so a mise-managed copy
  exists *only* when a workspace pins one in `mise.toml`. The thing people
  miscount as a node — `~/.local/bin/mcp-wrappers/node`
  — is **not a node**; it's a 4-line shell wrapper that sets `LD_LIBRARY_PATH` and then `exec /bin/node`
  (`internal/entrypoint/mcp_wrappers.go:64`). So "three Nodes" = two binaries + one
  router-to-the-baked-one.
- **Which node runs:** by default, everything (bare `node`, `npx`, shebangs,
  agent CLIs, MCP servers) resolves to the **baked** `/bin/node` — one node. Only
  if a workspace pins `node` in `mise.toml` does a mise node reappear and win for
  PATH-resolved commands (mise shims sit before `/bin`); MCP servers → the **baked** `/bin/node` (the wrapper
  execs it by absolute path). This split is deliberate and documented in
  `docs/design/mise-node-dynamic-linking.md`.
- **A mise runtime is a per-workspace opt-in, not a default.** yolo's mise
  defaults (`miseBaseTools`) are now **empty** — node, python, and go are all
  baked, so installing a second copy is pure duplication (and was the source of
  the `LD_LIBRARY_PATH`/skew problems). A workspace that needs a specific version
  pins it in `mise.toml`; that override is the only case with two copies, and
  it's what nix-ld makes robust. A guard test asserts baked runtimes never
  re-enter the mise defaults. See §2 "one by default, two only on override" for
  the detail and the history 2026-07-20.
- **Go is now baked too** (`imagePkgs.go` in `corePackages`, added 2026-07-20).
  `go` inside the jail is the baked `/bin/go`, RPATH-self-contained like node and
  python; there is no default mise go. The separate `pkgs.go` in `flake.nix:85`
  (`nativeBuildInputs`) is the *host* cross-compiler for the yolo-jail-go
  derivation at image-build time, NOT the jail runtime — a distinct thing.
  `GOTOOLCHAIN=auto` (the default) keeps offline `go build` working because the
  nixos-unstable go is ≥ the `go 1.26` in `go.mod`, so no toolchain download is
  triggered; staticcheck's mise `go:` backend still installs because it shells out
  to the baked `go` on PATH.

---

## 1. The four layers (with exact sources)

### Layer 1 — Baked into the Nix OCI image (`flake.nix`)

The image's package set is `corePackages` (`flake.nix:573-615`) plus
`fullPackages` (`flake.nix:620-648`) for the non-minimal variant. The runtimes and
tool substrate baked in:

| Baked package | `flake.nix` | Notes |
|---|---|---|
| `nodejs_24` | `:587` | Resolves to **24.16.0** in the pinned nixpkgs (per commit `230ca27`). Becomes `/bin/node`. |
| `python3` | `:588` | nixpkgs default python3. Becomes `/bin/python3`. |
| `go` | `:589` | nixpkgs go (nixos-unstable, ≥ go.mod's `go 1.26`). Becomes `/bin/go`, RPATH-self-contained. Baked 2026-07-20. |
| `uv` | `:606` | venv creation (`~/.yolo-venv-precreate.sh`). |
| `mise` | `:584` | The version manager itself must be baked so it can install everything else. |
| `chromium` | `:628` (fullPackages) | Substrate for chrome-devtools MCP + Playwright. |
| `fontconfig`, `noto-fonts-color-emoji` | `:629-630` | Chromium rendering. |
| `git`, `ripgrep`, `fd`, `curl`, `jq`, `gh`, coreutils, … | `:579-604` | POSIX + tooling essentials. |

`corePackages` is explicitly scoped to "everything the integration test suite …
actually touches, plus POSIX essentials" (`flake.nix:570-572`); `fullPackages` is
"extras that bulk the image up but aren't exercised by the integration test
suite" (`flake.nix:617-619`), so CI's minimal image can skip ~2 GB.

**When it rebuilds:** the image is rebuilt only when the package set changes. Extra
packages from `yolo-jail.jsonc`'s `"packages"` array are injected via the
`YOLO_EXTRA_PACKAGES` env var into an `--impure` build (`flake.nix:118-121`), and
the image "rebuilds only when this list changes" (`yolo-jail.jsonc:19`).

**Baked PATH is minimal on purpose:** the image's `config.Env` sets
`PATH=/bin:/usr/bin` (`flake.nix:686`) — "the Default PATH for anything that runs
before the Go entrypoint resets it" (`flake.nix:681-684`). The rich PATH (below)
is assembled by the entrypoint at boot.

### Layer 2 — mise (per-workspace + global, host↔jail shared store)

mise manages the *interactive* / *project* toolchains. Two config scopes:

- **Global** `~/.config/mise/config.toml` — generated by the entrypoint's
  `GenerateMiseConfig` (`internal/entrypoint/mise.go:37`). Its base tool set
  (`miseBaseTools`) is now **empty** — `[tools]` with no default entries.

  **node, python, AND go are all removed (2026-07-20):** all three are baked into
  the image (`flake nodejs_24` + `python3` + `imagePkgs.go`), so listing any of
  them in the mise defaults installed a duplicate non-nix copy — the source of
  the `LD_LIBRARY_PATH`/MCP-wrapper problems and the version skew (§2). With go
  baked too, `miseBaseTools` has no reason to list anything: mise is now purely an
  **override** path. A workspace can still pin node/python/go in its own
  `mise.toml` (an explicit override), which is the only way a second copy
  appears.

  plus any injected `mise_tools` from config (default `{"neovim": "stable"}` —
  `internal/config/config.go:92-93`), merged in via `YOLO_MISE_TOOLS`
  (`mise.go:172-187`, populated by `MergeMiseTools`, `internal/config/derived.go:142`).

  > **This is a generated config surface, and it's slated to move onto the config
  > prism** ([../plans/agent-settings-composition.md](../plans/agent-settings-composition.md),
  > Phase B). The global mise config is nothing but `miseBaseTools` (the
  > `defaults` layer) deep-merged with `mise_tools` from config (the `workspace`
  > layer) — exactly the prism's default composition. So `MergeMiseTools` +
  > `GenerateMiseConfig` + the special-cased `YOLO_MISE_TOOLS` env plumbing
  > **retire** into: a TOML-codec mise surface whose layers merge generically, and
  > the `mise_tools` config key becomes just "the workspace layer for that
  > surface." A user who needs more than a merge (drop a default tool, rewrite a
  > version) uses a Lua transform instead of yolo growing another special key.
  > **The base-tool defaults themselves still have to live somewhere** — they're
  > the `defaults` layer's data — but the *merge machinery* and the env-var
  > plumbing are what the prism replaces. This is the direct answer to "why do we
  > need this / can prism replace it": the *mechanism* yes, the *default values*
  > no.

- **Workspace** `/workspace/mise.toml` — checked into each repo. This repo's
  (`mise.toml`) pins:

  ```
  node = "24"        # mise.toml:2
  go   = "1.26"      # mise.toml:5  (kept in lockstep with go.mod)
  just = "latest"    # mise.toml:6
  "go:honnef.co/go/tools/cmd/staticcheck" = "latest"   # mise.toml:8
  ```

  Workspace config is layered **over** global (mise's normal merge), so the
  workspace `node = "24"` wins here.

**Shared store.** `MISE_DATA_DIR=/mise` (`internal/cli/run/assemble.go:368`) is
backed by a named volume `yolo-mise-data-v2` (`assemble.go:19`) mounted at `/mise`
(`internal/cli/run/assemble_parts.go:24`). All jails share this one store so
installed toolchains persist and are reused across jail boots — the design
history and the host↔jail path pitfalls are in
`docs/research/mise-host-jail-path-mismatch.md`.

**When it installs:** provisioning runs `mise trust`/`mise install`/`mise upgrade`
on every boot (`internal/cli/run/command.go:8-23`); already-present tools are
skipped, so subsequent boots are fast.

### Layer 3 — npm globals (bootstrap + lazy shims)

Installed into `~/.npm-global/bin` (`NPM_CONFIG_PREFIX`, set in
`internal/entrypoint/shell.go:104,155`). Two triggers:

- **Bootstrap `~/.yolo-bootstrap.sh`** (generated in `shell.go:148-256`, run under
  `YOLO_BYPASS_SHIMS=1` during provisioning):
  - Always-on MCP tools: `chrome-devtools-mcp` +
    `@modelcontextprotocol/server-sequential-thinking` (`shell.go:169-175`).
  - LSP servers, gated on configured `lsp_servers`: `pyright` (python),
    `typescript-language-server` + `typescript` (typescript) —
    `internal/cli/run/lsp.go:15-19`, installed in `shell.go:194-222`.
- **Lazy per-agent launchers** in `~/.yolo-shims/` (`internal/entrypoint/shims.go:247`):
  agent CLIs are **not** installed at boot ("Agent CLIs … are NOT updated here …
  Lazy-update launchers … handle install/update on first use", `shell.go:164-166`).
  The npm-backed agents — `copilot` (`@github/copilot`), `gemini`
  (`@google/gemini-cli`), `opencode`, `pi`, `codex` — are declared in
  `internal/agents/agents.go:76-154` and install to `~/.npm-global/bin` on first
  invocation.

### Layer 4 — Go binaries + native installers

- **Go `go install`** into `~/go/bin` (`GOBIN`, `shell.go:158`): `gopls`
  (`golang.org/x/tools/gopls`) and the Gemini LSP bridge
  `github.com/isaacphi/mcp-language-server` (`lsp.go:18,23`), installed by the
  bootstrap's go branch (`shell.go:206-219`). `staticcheck` comes via mise's
  `go:` backend (`mise.toml:8`), also landing in `~/go/bin`.
- **Native curl installer:** `claude` is `Kind: "native"` with
  `InstallerURL: "https://claude.ai/install.sh"` (`agents.go:57-62`), installed to
  `~/.local/bin/claude` on first use by the native lazy launcher
  (`shims.go:304-340`).
- **Python `pip`:** `showboat` is pip-installed unconditionally by the bootstrap
  (`shell.go:251-255`) — "tiny dep, useful for debugging".

---

## 2. The node question, resolved: one by default, two only on override

### Default: one node (baked). A second exists only if a workspace pins one.

As of 2026-07-20, `miseBaseTools` no longer lists `node` (or `python`) — see
Layer 2. So in the **default** setup there is exactly **one** node: the baked
`/bin/node`. A *second* node appears only when a workspace deliberately pins one
in `mise.toml`. The two possible binaries:

```
/bin/node                          → Nix-built node (nodejs_24), ALWAYS present
    interpreter: nix glibc ld.so, RPATH baked → runs with LD_LIBRARY_PATH unset
/mise/installs/node/<ver>/bin/node → mise/upstream node — ONLY if a workspace pins it
    interpreter: /lib64/ld-linux…  → NEEDS LD_LIBRARY_PATH=/lib:/usr/lib to find libstdc++
```

The third artifact people count is `~/.local/bin/mcp-wrappers/node`. It is **not a
node** — it is (`internal/entrypoint/mcp_wrappers.go:64-69`):

```bash
#!/bin/bash
export LD_LIBRARY_PATH="/lib:/usr/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
export FONTCONFIG_FILE="${FONTCONFIG_FILE:-/etc/fonts/fonts.conf}"
export FONTCONFIG_PATH="${FONTCONFIG_PATH:-/etc/fonts}"
exec /bin/node "$@"
```

i.e. it sets the runtime env (because some agents scrub child environments) and
then execs the **baked** `/bin/node`.

### Why the default is one node — and why an override is still allowed

- The **baked** node is self-contained (Nix baked a correct RPATH), so it runs
  even when a launcher scrubs `LD_LIBRARY_PATH`. Making it the *only* default node
  means bare `node`, shebangs, agent CLIs, and MCP servers all resolve to the
  **same** binary — no version skew, no cross-ABI native-addon hazard, and the
  `LD_LIBRARY_PATH`/wrapper machinery isn't exercised on the common path at all.
- A **mise** node still appears when a workspace *pins* one (`node = "20"` in
  `mise.toml`) — the legitimate "this project needs a specific version" case,
  no image rebuild. That override is the *only* thing that reintroduces a non-nix
  node and its `LD_LIBRARY_PATH` dependency — which is exactly the case
  **nix-ld** (`docs/design/mise-node-dynamic-linking.md` §Resolution) makes robust:
  once nix-ld is the `/lib64` interpreter, the pinned mise node links `libstdc++`
  env-free, so overriding node versions is safe even under a scrubbed environment.
  Until nix-ld lands, the baked image env + the MCP wrappers cover it for the
  override case.

### Which node does each consumer use?

**Default (no workspace `node` pin) — everything is the baked `/bin/node`:**

| Consumer | Node it runs | Why |
|---|---|---|
| Bare `node`, `#!/usr/bin/env node` shebangs | **baked** `/bin/node` | no mise node installed; PATH falls through to `/bin` |
| `npx`, `npm`, agent CLIs (`copilot`/`gemini`) | **baked** node | same |
| MCP presets + custom `mcp_servers` | **baked** `/bin/node` | wrapper (presets) or PATH (custom) both land on baked — the old custom-`mcp_servers` gap closes for free in the default case |

**Override (workspace pins `node` in `mise.toml`) — the mise node reappears:**

| Consumer | Node it runs | Why |
|---|---|---|
| Bare `node`, shebangs, `npx`, agent CLIs | **mise** node | mise shims precede `/bin` in PATH (§3) |
| MCP presets | **baked** `/bin/node` | wrapper still execs `/bin/node` explicitly |
| Custom `mcp_servers` with a bare `node` | **mise** node | not wrapper-routed — the known gap (`mise-node-dynamic-linking.md:135-143`), and the case nix-ld fixes |

So "two node versions at once" is now possible **only** when a workspace
deliberately pins one — never in the default setup.

**History (superseded).** Before 2026-07-20, `miseBaseTools` *also* installed
node (and python), so the default setup always had two — and commit `230ca27`
bumped the baked node to 24 but left the mise default at 22, a latent one-major
skew. That was first patched by aligning the versions (mise default → 24), then
**superseded** by removing node/python from the mise defaults entirely (this
section): aligning made the two match; removing means there's only one.

---

## 3. PATH resolution & precedence

The entrypoint assembles the rich PATH. From the generated `.bashrc`
(`internal/entrypoint/shell.go:108-110`):

```
$SHIM_DIR : $HOME/.local/bin : $NPM_CONFIG_PREFIX/bin : $MISE_SHIMS : $GOPATH/bin : /bin : /usr/bin
```

and the pre-exec PATH `execBash` sets is the same idea
(`internal/entrypoint/boot.go:356`): `SHIM_DIR, NpmBin, MiseShims, GoBin, .local/bin, /bin, /usr/bin`.

> Note: `AGENTS.md`'s documented order lists `…/.npm-global/bin:/home/agent/go/bin:/mise/shims:…`
> (go before mise). The actual generated PATH puts **mise shims before `$GOPATH/bin`**.
> This doesn't change `node`/`python`/`go` resolution (see below — `go` itself isn't
> in `$GOPATH/bin`), but the doc string is stale relative to the code.

Walking the common commands. Resolution differs by whether the workspace **pins**
a runtime in `mise.toml` — mise only creates a shim for tools it installs, so a
runtime yolo doesn't default-install and the workspace doesn't pin has **no mise
shim** and falls through PATH to the baked `/bin`:

| You type | Default (no pin) | If workspace pins it |
|---|---|---|
| `node` | **baked** `/bin/node` (no mise shim) — 24.16.0 | `$MISE_SHIMS/node` — the pinned version |
| `npx` / `npm` | baked node's npm/npx | mise node's |
| `python` / `python3` | **baked** `/bin/python3` (no mise shim) | `$MISE_SHIMS/python[3]` |
| `go` | **baked** `/bin/go` (no mise shim) — nixos-unstable go | `$MISE_SHIMS/go` — the pinned version |
| `gopls`, `staticcheck`, `mcp-language-server` | `$GOPATH/bin/…` (go install / mise `go:`) | — |
| `pyright`, `tsserver`, `copilot`, `gemini` | `$NPM_CONFIG_PREFIX/bin/…` (npm global) | — |
| `claude` | `~/.local/bin/claude` (via `~/.yolo-shims/claude` launcher) | — |
| `/bin/node`, `/bin/python3`, `/bin/go` | absolute → **baked** Nix binaries | image (node 24.16.0) |

(This repo's own `mise.toml` pins `node` and `go`, so *inside the yolo-jail
workspace* the "pins it" column applies to those two; most workspaces don't pin,
so the default column is the norm.)

Key consequences of the ordering:

- **mise wins over baked *only for tools mise actually installs*** — a shim
  exists just for installed tools, and `$MISE_SHIMS` precedes `/bin`. Since node,
  python, and go are no longer mise defaults, in the default setup there's no
  `node`/`python`/`go` shim and the baked `/bin/node`,`/bin/python3`,`/bin/go`
  win by PATH fall-through — *and* are still reached by absolute path where it
  matters (the MCP wrapper's `/bin/node`, the venv-precreate script's
  `/bin/python3`, `shell.go:284`). A workspace that pins node/python/go
  reintroduces the shim and mise wins again for those. The baked `go` on PATH is
  also what mise's `go:` backend (staticcheck) shells out to for `go install`.
- **`$SHIM_DIR` (`~/.yolo-shims`) is first**, so blocked-tool shims and the lazy
  agent launchers intercept before anything else.
- **`~/.local/bin/mcp-wrappers/` is NOT on PATH** — only `~/.local/bin` is. The
  wrappers are referenced by absolute path from generated MCP config
  (`internal/entrypoint/mcp.go:121,127`), never resolved via PATH. (AGENTS.md:
  "`.local/bin/` … should only be used by absolute MCP paths".)

---

## 4. Why the split exists (design rationale)

Each layer earns its place by *cadence* — how independently it needs to change
from the image:

- **Baked (Nix)** = reproducible, offline, and needed at image-build time or by
  infrastructure. mise itself must be baked (it installs the rest); chromium +
  fontconfig are a heavy, rarely-changing rendering substrate
  (`flake.nix:628-630`); the baked node exists as an env-scrub-proof runtime for
  MCP. Baking = one pinned closure, byte-reproducible, but changing it costs an
  image rebuild.
- **mise** = per-workspace, user-controllable, persists across restarts **without
  an image rebuild**. "Add tools to your workspace's `mise.toml`. On next jail
  start, `mise install` automatically fetches and makes them available" (AGENTS.md,
  "Agent Package Management"). This is where project runtimes and versions live.
- **npm globals / Go / native** = agent tooling that versions independently of the
  image and of each other (MCP servers, LSP servers, the coding-agent CLIs). Kept
  out of the image so a new copilot/pyright/claude doesn't require a rebuild;
  installed lazily so boot stays fast (`shell.go:164-166`).

The deeper reason the mise/baked *node* duality can't just be collapsed is the
loader wiring analyzed in `docs/design/mise-node-dynamic-linking.md`: the image
points FHS binaries at a Nix `ld.so` that ignores `/etc/ld.so.cache`, and the mise
node binary is host-shared (can't be patchelf'd), so `LD_LIBRARY_PATH` (baked env
+ wrappers) is the only runtime lever. The doc's accepted direction is **nix-ld**
as the `/lib64` interpreter, which would let the mise node run env-free and retire
the per-call-site wrappers — but until that lands, the wrapper-routes-to-baked-node
arrangement is the mitigation.

---

## 5. "Which knob do I turn?" quick reference

| Goal | Knob | Where | Rebuild? |
|---|---|---|---|
| Newer/older **Node/Python/Go for my project** | `[tools]` in workspace `mise.toml` (e.g. `node = "24"`) | repo root | No — `mise install` on next boot |
| A tool for **this workspace only** (typst, terraform, …) | `[tools]` in workspace `mise.toml` | repo root | No |
| A tool for **all my jails** (cross-workspace) | `[tools]` in `~/.config/mise/config.toml`, or `"mise_tools"` in user `yolo-jail.jsonc` | user config | No |
| Change the jail's **global default Node/Python/Go** major | now the **baked** version — bump `imagePkgs.nodejs_*`/`python3`/`go` in `flake.nix` `corePackages` (`miseBaseTools` is empty; mise is override-only) | source | **Yes** — image rebuild; validate in a nested `yolo -- bash`, `just load` on the host to ship |
| A **native package baked for every jail** (a CLI, a library `.so`) | `"packages"` array in `yolo-jail.jsonc` (nixpkgs attr names) | workspace/user config | **Yes** — image rebuilds when the list changes |
| Change the **baked runtime version** (`/bin/node`, chromium, …) | `imagePkgs.*` in `flake.nix` `corePackages`/`fullPackages` | source | **Yes** — image rebuild; nested `yolo -- bash` validates it, host `just load` ships it |
| Add an **MCP server** | `"mcp_presets"` / `"mcp_servers"` in `yolo-jail.jsonc` | config | No (installed via bootstrap/npm) |
| Add an **LSP server** | `"lsp_servers"` in `yolo-jail.jsonc` | config | No (bootstrap installs pyright/tsserver/gopls as needed) |
| Add/enable a **coding agent** (copilot, gemini, …) | `"agents"` in `yolo-jail.jsonc` | config | No (lazy-installed on first use) |

Rule of thumb: **project runtimes → `mise.toml`; every-jail native packages →
`yolo-jail.jsonc "packages"` (rebuild); the runtime/version substrate itself →
`flake.nix` (rebuild).** After *any* `yolo-jail.jsonc` edit, run `yolo check`
before restarting.

---

## Evidence index

- Baked packages: `flake.nix:573-615` (core), `:620-648` (full), `:686-688` (image
  Env / PATH / `LD_LIBRARY_PATH`), `:118-121` (extra-packages `--impure` injection).
- Baked Node bump: commit `230ca27` (`flake.nix` `nodejs_22→24`, `mise.toml`
  `22→24`). Baked `go` added to `corePackages` 2026-07-20 (`imagePkgs.go`).
- mise global config generator + base tools: `internal/entrypoint/mise.go:37`
  (`miseBaseTools` is now an empty slice — all default runtimes baked).
- mise default injected tools (`neovim`): `internal/config/config.go:92-93`,
  `internal/config/derived.go:142`.
- Workspace mise pins: `mise.toml:2,5,6,8`.
- mise store mount: `internal/cli/run/assemble.go:19,368`, `assemble_parts.go:24`.
- Provisioning (`mise install`): `internal/cli/run/command.go:8-23`.
- Bootstrap (npm/go/pip installs): `internal/entrypoint/shell.go:148-256`.
- LSP install recipes: `internal/cli/run/lsp.go:15-23`.
- Agent install specs (npm + native claude): `internal/agents/agents.go:57-154`.
- Lazy agent launchers: `internal/entrypoint/shims.go:247-340`.
- MCP node/npx wrappers: `internal/entrypoint/mcp_wrappers.go:7-77`.
- MCP config wiring to wrappers: `internal/entrypoint/mcp.go:100-137`.
- PATH assembly: `internal/entrypoint/shell.go:108-110,159`,
  `internal/entrypoint/boot.go:356,489-491`.
- Node-binary duality + loader analysis + nix-ld direction:
  `docs/design/mise-node-dynamic-linking.md`.
- mise shared-store host↔jail pitfalls: `docs/research/mise-host-jail-path-mismatch.md`.

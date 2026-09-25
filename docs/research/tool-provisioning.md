# Tool Provisioning: how the jail acquires runtimes and CLIs

How does a jail end up with `node`, `python`, `go`, `gopls`, `copilot`, `claude`,
`chrome-devtools-mcp`, and friends? Through **four different mechanisms**, each
chosen for a different reason — and the seams between them are exactly where the
"wait, why are there three Nodes?" confusion lives. This doc enumerates every
layer and names the code that decides it, then untangles the multiple-versions situation so
the picture stops feeling accidental.

**Status:** CURRENT — the four-layer model and the whole resolution story
re-verified against this tree 2026-09-22; findings first gathered 2026-08-05.
Citations are by **file and symbol, never by line**: the line numbers this page used to
carry all drifted, and a wrong one costs more than no pointer at all.

> [!NOTE]
> **An agent's install spec is a PACK MANIFEST, and there is no `agents` config key.** A pack
> declares the program it installs in `packs/<name>/pack.json` — Claude's native installer is
> `{"url": "https://claude.ai/install.sh", "via": "installer"}` there, the same URL a Go literal
> used to hold — and the config key that selects packs is `"packs"`, by bare name:
> `"packs": ["claude"]`. `agents` is not a key yolo tolerates; it is a hard error on the host
> (`internal/config/validate.go`). That is worth stating outright because `agents` is the
> intuitive spelling and older prose across this corpus reaches for it. The *mechanism* below —
> lazy install on first use, from a generated launcher — is unchanged; only where the spec is
> read from moved. Which packs install a program is
> `rg -l '"kind": "program"' packs/*/pack.json`, never a list written down here; see
> [`../reference/pack-system.md`](../reference/pack-system.md).

---

## TL;DR

- **Four layers install runtimes/tools**, in ascending order of "how often it
  changes": (1) baked into the Nix OCI image, (2) mise, (3) npm globals, (4) Go
  `go install` + native curl installers. PATH puts the mise shim dir ahead of `/bin`, but
  **mise only creates a shim for a tool it installed** — and yolo default-installs none — so
  in a default jail a bare `node`/`python`/`go` falls through to the **baked** copy, and a
  mise copy wins only where a workspace pinned one.
- **By default there is ONE of each runtime, not two.** node, python, AND go
  are now all Nix-baked (`/bin/node`, `/bin/python3`, `/bin/go` — all
  RPATH-self-contained). mise no longer installs any of them by default
  (2026-07-20 for node/python; go baked shortly after), so a mise-managed copy
  exists *only* when a workspace pins one in `mise.toml`. The thing people
  miscount as a node — `~/.local/bin/mcp-wrappers/node`
  — is **not a node**; it's a 4-line shell wrapper that sets the two fontconfig variables and
  then `exec /bin/node` (`internal/entrypoint/mcp_wrappers.go`). So "three Nodes" = two binaries
  + one router-to-the-baked-one.
- **Which node runs:** by default, everything (bare `node`, `npx`, shebangs,
  agent CLIs, MCP servers) resolves to the **baked** `/bin/node` — one node. Only
  if a workspace pins `node` in `mise.toml` does a mise node reappear and win for
  PATH-resolved commands (mise shims sit before `/bin`); MCP servers → the **baked** `/bin/node` (the wrapper
  execs it by absolute path). This split is deliberate and documented in
  [`../reference/mise-node-dynamic-linking.md`](../reference/mise-node-dynamic-linking.md).
- **A mise runtime is a per-workspace opt-in, not a default.** yolo declares **no default
  mise tools at all** — node, python, and go are all
  baked, so installing a second copy is pure duplication (and was the source of
  the `LD_LIBRARY_PATH`/skew problems). A workspace that needs a specific version
  pins it in `mise.toml`; that override is the only case with two copies, and
  it's what nix-ld makes safe. A guard test reads the baked runtimes out of `flake.nix`
  and fails if one re-enters the mise surface. See [§2](#2-the-node-question-resolved-one-by-default-two-only-on-override) "one by default, two only on override" for
  the detail and the history 2026-07-20.
- **Go is now baked too** (`go` in the core name list, added 2026-07-20).
  `go` inside the jail is the baked `/bin/go`, RPATH-self-contained like node and
  python; there is no default mise go. The separate `pkgs.go` in the flake's
  `nativeBuildInputs` is the toolchain that builds the `yolo-jail-go` derivation — the yolo
  binaries a launch mounts at `/opt/yolo-jail/bin` — NOT the jail runtime; a distinct thing.
  `GOTOOLCHAIN=auto` (the default) keeps offline `go build` working because the
  nixos-unstable go is ≥ the `go 1.26` in `go.mod`, so no toolchain download is
  triggered; staticcheck's mise `go:` backend still installs because it shells out
  to the baked `go` on PATH.

---

## 1. The four layers (with exact sources)

### Layer 1 — Baked into the Nix OCI image (`flake.nix`)

The image's package set is `corePackages` plus `fullPackages` for the non-minimal variant
(both in `flake.nix`). The core half is declared as **a list of nixpkgs attribute NAMES**,
`coreFloorNames`, and resolved twice: against the image's package set for `corePackages`,
and against the host's for the `macos-user` non-container floor. That is exactly why they
are names rather than derivations — one list, two backends. The runtimes and tool substrate
baked in:

| Baked package | Notes |
|---|---|
| `nodejs_24` | Becomes `/bin/node`; the minor is whatever the pinned nixpkgs resolves. |
| `python3` | nixpkgs default python3. Becomes `/bin/python3`. |
| `go` | nixpkgs go (nixos-unstable, ≥ go.mod's `go 1.26`). Becomes `/bin/go`, RPATH-self-contained. Baked 2026-07-20. |
| `uv` | venv creation (`~/.yolo-venv-precreate.sh`). |
| `mise` | The version manager itself must be baked so it can install everything else. |
| `neovim` | Baked because the run env sets `VISUAL=nvim` unconditionally, so `nvim` must exist with no `mise_tools` entry. Was the one default `mise_tools` pin. |
| `chromium` (fullPackages) | Substrate for chrome-devtools MCP + Playwright. |
| `fontconfig`, `noto-fonts-color-emoji` (fullPackages) | Chromium rendering. |
| `git`, `ripgrep`, `fd`, `curl`, `jq`, `gh`, coreutils, … | POSIX + tooling essentials. |

`corePackages` is explicitly scoped to "everything the integration test suite …
actually touches, plus POSIX essentials"; `fullPackages` is "extras that bulk the
image up but aren't exercised by the integration test suite", so CI's minimal image
can skip ~2 GB.

**When it rebuilds:** the image is rebuilt only when the package set changes. Extra
packages from `yolo-jail.jsonc`'s `"packages"` array are injected via the
`YOLO_EXTRA_PACKAGES` env var into an `--impure` build, and the image "rebuilds only
when this list changes" (`yolo-jail.jsonc`). A launch can also take `packages:` from the
mounted nix store instead of baking them — `YOLO_STORE_PACKAGES=1`, podman + Linux + a
running nix daemon — in which case they arrive as a symlink farm at `/run/yolo/packages`
([`../reference/image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md)).

**Baked PATH is minimal on purpose:** the image's `config.Env` sets
`PATH=/bin:/usr/bin` — "the Default PATH for anything that runs before the Go
entrypoint resets it". The rich PATH (below) is assembled by the entrypoint at boot.

### Layer 2 — mise (per-workspace + global, one store shared by every jail)

mise manages the *interactive* / *project* toolchains. Two config scopes:

- **Global** `~/.config/mise/config.toml` — rendered through the config prism by
  `ConfigureMisePrism` (`internal/entrypoint/prism_mise.go`). **yolo owns no default
  runtime**: there are no base tools, so the only yolo-owned content in `[tools]` is the
  injected `mise_tools` pins, which ride the prism's *computed* layer (above the captured
  overlay of a user's own `mise use -g`, below managed).

  **node, python, AND go are all removed (2026-07-20):** all three are baked into
  the image, so listing any of them in the mise defaults installed a duplicate non-nix
  copy — the source of the `LD_LIBRARY_PATH`/MCP-wrapper problems and the version skew
  ([§2](#2-the-node-question-resolved-one-by-default-two-only-on-override)). With go
  baked too there is nothing left for a defaults layer to hold: mise is purely an
  **override** path. A workspace can still pin node/python/go in its own
  `mise.toml` (an explicit override), which is the only way a second copy
  appears. A guard test reads the baked runtimes out of `flake.nix` and fails if one
  re-enters the mise surface (`internal/entrypoint/mise_node_version_test.go`).

  The injected pins are `mise_tools` from config, merged by `MergeMiseTools` and carried in
  on `YOLO_MISE_TOOLS`. **The default is now empty** — `{"neovim": "stable"}` was the last
  entry and neovim is baked instead, because a tool yolo wants in *every* jail belongs in
  the image rather than in a per-workspace mise store.

  > **The surface itself is the prism's, and that is the part that shipped** — the port
  > this section used to describe as pending
  > ([../plans/agent-settings-composition.md](../plans/agent-settings-composition.md)).
  > What did *not* retire with it is the `YOLO_MISE_TOOLS` env plumbing and
  > `MergeMiseTools`: the prism owns the composition and the file, the launcher still
  > hands the pins in on an env var. Two side effects stay bespoke on purpose — the
  > computed `[tools]` table above, and the retired-token surgery on
  > `/workspace/mise.toml`, which the prism must never own because yolo owns only
  > user-scope config. A user who needs more than a merge (drop a pin, rewrite a version)
  > writes a `null` tombstone from a declared layer, which deletes a key the layers below
  > supplied; the Lua transform that used to be the answer here was removed on 2026-09-11
  > ([`OQ-LT1`](../reference/pack-system.md#oq-lt1)).

- **Workspace** `/workspace/mise.toml` — checked into each repo. This repo's
  (`mise.toml`) pins:

  ```
  node = "24"
  go   = "1.26"      # kept in lockstep with go.mod
  just = "latest"
  "go:honnef.co/go/tools/cmd/staticcheck" = { version = "latest", install_env = { CGO_ENABLED = "0" } }
  ```

  `install_env` is mise's per-tool install environment, which the `go:` backend hands to
  `go install`. `CGO_ENABLED = "0"` makes staticcheck a static binary: built with cgo on,
  it was dynamically linked against a host `/nix/store` glibc, and a host nix GC broke the
  lint gate. `mise.toml`'s comment carries the reinstall step an option change needs.

  Workspace config is layered **over** global (mise's normal merge), so the
  workspace `node = "24"` wins here.

**Shared store.** `MISE_DATA_DIR=/mise`, mounted at `/mise` in every jail
(`internal/cli/run/assemble.go`, `assemble_parts.go`). All jails share this one store so
installed toolchains persist and are reused across jail boots; the *backing* differs by
platform — a yolo-owned host directory on Linux, the `yolo-mise-data-v2` named volume on
macOS and Apple Container — and the in-jail path is identical either way. The host's own
mise installation is never mounted, so host↔jail mise skew cannot matter; the design
history and the path pitfalls are in
[`mise-host-jail-path-mismatch.md`](./mise-host-jail-path-mismatch.md).

**When it installs:** provisioning runs `mise install` on every boot
(`internal/cli/run/command.go`); already-present tools are skipped, so
subsequent boots are fast. Neither of the two commands this line used to name is
run any more: `mise trust` was removed once `MISE_TRUSTED_CONFIG_PATHS=/workspace`
was verified sufficient on its own (`internal/entrypoint/boot.go`), and the
per-launch `mise upgrade` was removed by ruling [OQ-PD3](../design/program-delivery.md#decision-ledger) — a launch resolves on
install only (`internal/cli/run/provisioning_upgrade_test.go` pins both branches).

### Layer 3 — npm globals (bootstrap + lazy launchers)

Installed into `~/.npm-global/bin` (`NPM_CONFIG_PREFIX`, set in
`internal/entrypoint/shell.go`). Two triggers:

- **Bootstrap `~/.yolo-bootstrap.sh`** (generated in `shell.go`, run under
  `YOLO_BYPASS_SHIMS=1` during provisioning):
  - MCP servers for the **enabled presets only**: `chrome-devtools-mcp` for
    `chrome-devtools`, `@modelcontextprotocol/server-sequential-thinking` for
    `sequential-thinking` (`mcpPresetNpmPackages`). This was an unconditional
    `npm install -g` of both until it was measured: 112 npm packages installed on a jail
    with zero agents and zero configured presets. An environment that does not generate the
    preset *wrappers* installs nothing for them either — the wrapper is what an MCP client
    spawns, so the package without it is a download nothing can exec.
  - LSP servers, gated on configured `lsp_servers`: `pyright` (python),
    `typescript-language-server` + `typescript` (typescript) —
    `internal/config/lsp.go`, installed in `shell.go`.
- **Lazy per-program launchers** in `~/.yolo/bin/launch/`
  (`GenerateAgentLaunchers`, `internal/entrypoint/shims.go`): agent CLIs are **not**
  installed at boot — the launcher handles install *and* update on first use, which is why
  the dir sits ahead of every install prefix on PATH ([§3](#3-path-resolution--precedence)).
  An npm-delivered program is a pack's `program` contribution with `via: "npm"` and its
  package name, and it lands in `~/.npm-global/bin` on first invocation.

### Layer 4 — Go binaries + native installers

- **Go `go install`** into `~/go/bin` (`GOBIN`, `shell.go`): `gopls`
  (`golang.org/x/tools/gopls`) when the `go` LSP is configured, installed by the
  bootstrap's go branch. `staticcheck` comes via mise's `go:` backend (`mise.toml`), also
  landing in `~/go/bin`. The `mcp-language-server` bridge that used to be installed here is
  **gone**: it wrapped each configured LSP as an MCP server for the gemini agent, its only
  consumer, and every surviving agent consumes LSP servers natively.
- **Native curl installer:** a pack declares `via: "installer"` plus the `url` — Claude's
  is `https://claude.ai/install.sh` in `packs/claude/pack.json` — and the native lazy
  launcher (`internal/entrypoint/shims.go`) runs it on first use, installing to
  `~/.local/bin/<bin>`.
- ~~**Python `pip`:** `showboat` is pip-installed unconditionally by the bootstrap.~~
  **REMOVED 2026-08-05.** It was the only ungated install in the script and nothing in the repo
  consumed it; being the LAST command, a jail without a bare `pip` on PATH reported
  "PROVISIONING FAILED (exit 127)" on every boot (PR #29 made it resilient, then it was deleted
  outright). There is now **no `pip` install path in the bootstrap at all** — which is the
  accurate answer to "how does yolo provision Python tools?": it does not. `packages:` (baked)
  or a pack's `requires`/`program` contribution are the mechanisms.

---

## 2. The node question, resolved: one by default, two only on override

### Default: one node (baked). A second exists only if a workspace pins one.

As of 2026-07-20 the mise defaults no longer list `node` (or `python`) — see
Layer 2. So in the **default** setup there is exactly **one** node: the baked
`/bin/node`. A *second* node appears only when a workspace deliberately pins one
in `mise.toml`. The two possible binaries:

```
/bin/node                          → Nix-built node (nodejs_24), ALWAYS present
    interpreter: nix glibc ld.so, RPATH baked → runs with LD_LIBRARY_PATH unset
/mise/installs/node/<ver>/bin/node → mise/upstream node — ONLY if a workspace pins it
    interpreter: /lib64/ld-linux… → nix-ld, which finds libstdc++ with no env at all
```

The third artifact people count is `~/.local/bin/mcp-wrappers/node`. It is **not a
node** — it is (`internal/entrypoint/mcp_wrappers.go`):

```bash
#!/bin/bash
export FONTCONFIG_FILE="${FONTCONFIG_FILE:-/etc/fonts/fonts.conf}"
export FONTCONFIG_PATH="${FONTCONFIG_PATH:-/etc/fonts}"
exec /bin/node "$@"
```

i.e. it sets chromium's font configuration and then execs the **baked**
`/bin/node` — which also skips mise's per-directory environment resolution on every MCP
cold start.

> **The wrapper does NOT set `LD_LIBRARY_PATH`, and re-adding it is the whack-a-mole this
> repo deliberately ended.** The loader problem it used to carry — an FHS node losing
> `libstdc++` whenever an agent scrubbed the child environment — is covered *structurally*
> by nix-ld as the FHS ELF interpreter, so the wrapper's remaining job has nothing to do
> with the loader ([`../reference/mise-node-dynamic-linking.md`](../reference/mise-node-dynamic-linking.md)).

### Why the default is one node — and why an override is still allowed

- The **baked** node is self-contained (Nix baked a correct RPATH), so it runs
  even when a launcher scrubs `LD_LIBRARY_PATH`. Making it the *only* default node
  means bare `node`, shebangs, agent CLIs, and MCP servers all resolve to the
  **same** binary — no version skew, no cross-ABI native-addon hazard, and the
  `LD_LIBRARY_PATH`/wrapper machinery isn't exercised on the common path at all.
- A **mise** node still appears when a workspace *pins* one (`node = "20"` in
  `mise.toml`) — the legitimate "this project needs a specific version" case,
  no image rebuild. That override is the *only* thing that reintroduces a non-nix
  node, and it is safe because **nix-ld** is the image's `/lib64` interpreter
  ([`../reference/mise-node-dynamic-linking.md`](../reference/mise-node-dynamic-linking.md)):
  the pinned mise node links `libstdc++` env-free, so a pin survives a fully scrubbed
  environment without anything on the call path having to re-export
  `LD_LIBRARY_PATH`.

### Which node does each consumer use?

**Default (no workspace `node` pin) — everything is the baked `/bin/node`:**

| Consumer | Node it runs | Why |
|---|---|---|
| Bare `node`, `#!/usr/bin/env node` shebangs | **baked** `/bin/node` | no mise node installed; PATH falls through to `/bin` |
| `npx`, `npm`, agent CLIs | **baked** node | same |
| MCP presets + custom `mcp_servers` | **baked** `/bin/node` | wrapper (presets) or PATH (custom) both land on baked — the old custom-`mcp_servers` gap closes for free in the default case |

**Override (workspace pins `node` in `mise.toml`) — the mise node reappears:**

| Consumer | Node it runs | Why |
|---|---|---|
| Bare `node`, shebangs, `npx`, agent CLIs | **mise** node | mise shims precede `/bin` in PATH ([§3](#3-path-resolution--precedence)) |
| MCP presets | **baked** `/bin/node` | wrapper still execs `/bin/node` explicitly |
| Custom `mcp_servers` with a bare `node` | **mise** node | not wrapper-routed, so it resolves through PATH like any other bare name — harmless now that nix-ld links it env-free, and the reason the wrappers stopped being the fix |

So "two node versions at once" is now possible **only** when a workspace
deliberately pins one — never in the default setup. What a pin still buys you is a
**version** difference between the config-routed MCP presets (always baked) and everything
PATH-resolved (the pin), which is worth knowing before pinning a major.

**History (superseded).** Before 2026-07-20 the mise defaults *also* installed
node (and python), so the default setup always had two — and the commit that
bumped the baked node to 24 left the mise default at 22, a latent one-major
skew. That was first patched by aligning the versions, then
**superseded** by removing node/python from the mise defaults entirely (this
section): aligning made the two match; removing means there's only one.

---

## 3. PATH resolution & precedence

The entrypoint assembles the rich PATH, and **`BootPath` (`internal/entrypoint/boot.go`) is
the one authority for its order**:

```
$HOME/.yolo/bin/block       blockers (blocked-tool shims)
$HOME/.yolo/bin/launch      lazy installers/updaters
$NPM_CONFIG_PREFIX/bin      npm globals
<mise shims>                $MISE_DATA_DIR/shims
$GOPATH/bin                 go install
$HOME/.local/bin            native installers, and anything a user drops there
/run/yolo/packages/bin      store-delivered packages — absent unless the launch opted in
/bin
/usr/bin
```

The `export PATH` in the generated `.bashrc` (`internal/entrypoint/shell.go`) is a
**second, independently-written copy of that same order** — the PATH an interactive shell
and `bash -lc` get. The two are compared **entry by entry** by
`TestBashrcPathMatchesBootPathOrder`: a full order comparison rather than an ends-only one,
because ends-only is exactly how they came to disagree about `$HOME/.local/bin` for months.
Anything that moves one has to move the other.

Three things about the order that are decisions rather than accidents:

- **The two generated script dirs are adjacent at the head, and their relative order
  carries the meaning.** `~/.yolo/bin/block` holds the blocked-tool blockers and is FIRST,
  because interception has to outrank installation: a tool that is both blocked and
  pack-declared must resolve to the blocker. `~/.yolo/bin/launch` holds the lazy
  installer/updater launchers and is SECOND — **ahead of every install prefix**, because a
  launcher placed after what it installs is unreachable from its own second invocation
  onward, so the lazy install worked and the update the same script carries silently never
  ran again. What replaced the old trailing position is a **generation-time** check
  (`internal/entrypoint/launchercollision.go`): no launcher is written for a name the LAUNCH
  already provides — `/bin`, `/usr/bin`, the store-package farm when one is delivered, or a
  declared mise tool. ⚠ The per-home install prefixes are excluded on purpose. Spelled
  "already resolvable on PATH?" the check would destroy the feature: `~/.local/bin/claude`
  exists after the first install, the next boot writes no launcher, and evergreen works
  exactly once.
- **`/run/yolo/packages/bin` sits immediately before `/bin`**, and that position is chosen
  to be a no-op. It is the store-delivered package farm for a launch that opted into
  `YOLO_STORE_PACKAGES=1`; the tools it holds are the ones that would otherwise be baked
  into `/bin`, so putting it one step ahead of `/bin` leaves every precedence relation
  above it exactly as it was. It simply does not exist on a jail that bakes.
- **`/opt/yolo-jail/bin` is deliberately NOT on this list**, even though every yolo binary
  now lives there. The image bakes `/bin/<name>` symlinks into that mount instead, which
  moves nothing here and still works for a consumer that scrubs PATH and spells
  `/bin/yolo`.

Walking the common commands. Resolution differs by whether the workspace **pins**
a runtime in `mise.toml` — mise only creates a shim for tools it installs, so a
runtime yolo doesn't default-install and the workspace doesn't pin has **no mise
shim** and falls through PATH to the baked `/bin`:

| You type | Default (no pin) | If workspace pins it |
|---|---|---|
| `node` | **baked** `/bin/node` (no mise shim) | `<mise shims>/node` — the pinned version |
| `npx` / `npm` | baked node's npm/npx | mise node's |
| `python` / `python3` | **baked** `/bin/python3` (no mise shim) | `<mise shims>/python[3]` |
| `go` | **baked** `/bin/go` (no mise shim) — nixos-unstable go | `<mise shims>/go` — the pinned version |
| `gopls`, `staticcheck` | `$GOPATH/bin/…` (go install / mise `go:`) | — |
| `pyright`, `tsserver`, `copilot` | `$NPM_CONFIG_PREFIX/bin/…` (npm global) | — |
| `claude` | the **launcher**, `~/.yolo/bin/launch/claude`, which installs and then execs `~/.local/bin/claude` | — |
| `/bin/node`, `/bin/python3`, `/bin/go` | absolute → **baked** Nix binaries | the image's, unchanged by a pin |

(This repo's own `mise.toml` pins `node` and `go`, so *inside the yolo-jail
workspace* the "pins it" column applies to those two; most workspaces don't pin,
so the default column is the norm.)

Key consequences of the ordering:

- **mise wins over baked *only for tools mise actually installs*** — a shim
  exists just for installed tools, and the mise shim dir precedes `/bin`. Since node,
  python, and go are no longer mise defaults, in the default setup there's no
  `node`/`python`/`go` shim and the baked `/bin/node`,`/bin/python3`,`/bin/go`
  win by PATH fall-through — *and* are still reached by absolute path where it
  matters (the MCP wrapper's `/bin/node`, the venv-precreate script's
  `/bin/python3`). A workspace that pins node/python/go
  reintroduces the shim and mise wins again for those. The baked `go` on PATH is
  also what mise's `go:` backend (staticcheck) shells out to for `go install`.
- **An installed agent CLI is still reached through its launcher**, not directly: the
  launch dir outranks `~/.npm-global/bin` and `~/.local/bin`, so every invocation is
  mediated. That is the whole point of the position, and it is why re-entrancy is guarded
  inside the generated script (a vendor installer that calls the program by bare name gets
  the launcher back).
- **`~/.local/bin/mcp-wrappers/` is NOT on PATH** — only `~/.local/bin` is. The
  wrappers are referenced by absolute path from generated MCP config
  (`internal/entrypoint/mcp.go`), never resolved via PATH.
- **`macos-user` carries a THIRD list** (`macosuser.SandboxPath`) and it is **not this
  order**: `$HOME/.local/bin` is third there and sixth here, and `/usr/bin` precedes
  `/bin`, so a pipx-installed tool outranks a mise shim on that backend and loses to it on
  every container backend. Nothing compares that list to either copy above; the divergence
  is left standing deliberately rather than quietly reordered.

---

## 4. Why the split exists (design rationale)

Each layer earns its place by *cadence* — how independently it needs to change
from the image:

- **Baked (Nix)** = reproducible, offline, and needed at image-build time or by
  infrastructure. mise itself must be baked (it installs the rest); chromium +
  fontconfig are a heavy, rarely-changing rendering substrate; the baked node exists as an
  env-scrub-proof runtime for MCP. Baking = one pinned closure, byte-reproducible, but
  changing it costs an image rebuild.
- **mise** = per-workspace, user-controllable, persists across restarts **without
  an image rebuild**: add tools to the workspace's `mise.toml` and the next boot's
  `mise install` fetches them ([`../guides/USER_GUIDE.md`](../guides/USER_GUIDE.md)). This is
  where project runtimes and versions live.
- **npm globals / Go / native** = agent tooling that versions independently of the
  image and of each other (MCP servers, LSP servers, the coding-agent CLIs). Kept
  out of the image so a new copilot/pyright/claude doesn't require a rebuild;
  installed lazily so boot stays fast.

The *node* duality used to be much sharper than it is, and the reason is the loader wiring
analyzed in [`../reference/mise-node-dynamic-linking.md`](../reference/mise-node-dynamic-linking.md).
A stock FHS binary in a pure-nix image has an ELF interpreter path (`/lib64/ld-linux-*.so.2`)
the image does not naturally have, and a mise-downloaded node cannot be patchelf'd — so for a
while `LD_LIBRARY_PATH` (the baked env plus a wrapper at each call site) was the only runtime
lever, and one scrubbed environment anywhere broke it. **That is fixed structurally**:
`/lib64` is nix-ld, built with its defaults compiled in, so an FHS binary resolves both the
real loader and a minimal library search path with zero environment dependence. The wrappers
survive for what is left of their job — fontconfig, and skipping mise's per-directory
resolution on an MCP cold start — not for the loader.

---

## 5. "Which knob do I turn?" quick reference

| Goal | Knob | Where | Rebuild? |
|---|---|---|---|
| Newer/older **Node/Python/Go for my project** | `[tools]` in workspace `mise.toml` (e.g. `node = "24"`) | repo root | No — `mise install` on next boot |
| A tool for **this workspace only** (typst, terraform, …) | `[tools]` in workspace `mise.toml` | repo root | No |
| A tool for **all my jails** (cross-workspace) | `[tools]` in `~/.config/mise/config.toml`, or `"mise_tools"` in user `yolo-jail.jsonc` | user config | No |
| Change the jail's **global default Node/Python/Go** major | now the **baked** version — bump `nodejs_*`/`python3`/`go` in `flake.nix`'s `coreFloorNames` (there are no mise defaults; mise is override-only) | source | **Yes** — image rebuild; validate in a nested `yolo -- bash`, `just load` on the host to ship |
| A **native package baked for every jail** (a CLI, a library `.so`) | `"packages"` array in `yolo-jail.jsonc` (nixpkgs attr names) | workspace/user config | **Yes** — image rebuilds when the list changes |
| Change the **baked runtime version** (`/bin/node`, chromium, …) | `coreFloorNames` / `fullPackages` in `flake.nix` | source | **Yes** — image rebuild; nested `yolo -- bash` validates it, host `just load` ships it |
| Add an **MCP server** | `"mcp_presets"` / `"mcp_servers"` in `yolo-jail.jsonc` | config | No (a preset's npm package is installed by the bootstrap) |
| Add an **LSP server** | `"lsp_servers"` in `yolo-jail.jsonc` | config | No (bootstrap installs pyright/tsserver/gopls as needed) |
| Add/enable a **coding agent** (claude, copilot, …) | `"packs"` in `yolo-jail.jsonc`, by bare pack name | config | No (lazy-installed on first use) |

Rule of thumb: **project runtimes → `mise.toml`; every-jail native packages →
`yolo-jail.jsonc "packages"` (rebuild); the runtime/version substrate itself →
`flake.nix` (rebuild).** After *any* `yolo-jail.jsonc` edit, run `yolo check`
before restarting.

---

## Evidence index

- Baked packages: `flake.nix` — `coreFloorNames`/`corePackages` (core), `fullPackages`
  (full), the image `Env` (PATH / `LD_LIBRARY_PATH` / `TZDIR`), and the
  `YOLO_EXTRA_PACKAGES` `--impure` injection.
- Baked `go` added to the core list 2026-07-20; `neovim` followed, replacing the one default
  `mise_tools` pin.
- mise global config: `internal/entrypoint/prism_mise.go` (`ConfigureMisePrism`), with the
  injected-pin reader in `internal/entrypoint/mise.go` (`loadInjectedTools`).
- `mise_tools` merge: `internal/config/derived.go` (`MergeMiseTools`; the defaults are empty).
- Baked-runtime guard: `internal/entrypoint/mise_node_version_test.go`.
- Workspace mise pins: `mise.toml`.
- mise store mount: `internal/cli/run/assemble.go`, `assemble_parts.go`.
- Provisioning (`mise install`): `internal/cli/run/command.go`.
- Bootstrap (npm/go installs, the preset gate): `internal/entrypoint/shell.go`
  (`GenerateBootstrapScript`, `mcpPresetNpmPackages`).
- LSP install recipes: `internal/config/lsp.go` (`config.LSPInstalls`, shared by both backends).
- Program install specs: each pack's `packs/<name>/pack.json` (`kind: "program"` — `via`,
  `package` for npm or `url` for an installer, `update`); the schema and its doc comments
  are `internal/packdecl`.
- Lazy launchers + blockers, and the collision check that keeps a launcher from shadowing a
  baked binary: `internal/entrypoint/shims.go`, `internal/entrypoint/launchercollision.go`.
- MCP node/npx wrappers: `internal/entrypoint/mcp_wrappers.go`.
- MCP config wiring to wrappers: `internal/entrypoint/mcp.go`.
- PATH assembly: `internal/entrypoint/boot.go` (`BootPath`, the authority) and
  `internal/entrypoint/shell.go` (the `.bashrc` copy); `internal/macosuser/macosuser.go`
  (`SandboxPath`, the third list).
- Node-binary duality, the loader analysis and nix-ld:
  [`../reference/mise-node-dynamic-linking.md`](../reference/mise-node-dynamic-linking.md).
- mise shared-store host↔jail pitfalls:
  [`mise-host-jail-path-mismatch.md`](./mise-host-jail-path-mismatch.md).

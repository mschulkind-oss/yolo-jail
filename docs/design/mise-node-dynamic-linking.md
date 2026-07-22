# The mise-node / `LD_LIBRARY_PATH` problem (investigation + handoff)

**Status:** IMPLEMENTED (2026-07-22) — **nix-ld** is the `/lib64` interpreter
(variant A, fully env-free). Landed across four commits: the flake wiring
(`e05666a`), the MCP-wrapper `LD_LIBRARY_PATH` deletion (`1d614e1`), the
`-e LD_LIBRARY_PATH` re-export documented-as-kept (`d38463a`), and the
`yolo check` baseline-drift tripwire (`d6d2e65`). Verified end-to-end in a
nested jail on the new image: an FHS mise node runs under `env -i`
(v22.20.0/v22.23.1), and the `yolo check` tripwire FAILs on the old image /
PASSes on the new one. The originally-designed steps 4–5 (runtime `/run`
fallback wiring, explicit `--tmpfs /run`) **collapsed to nothing** under
variant A — the loader default is compiled into the shim and the library dir
is baked at `/usr/share/nix-ld/lib`, so there is no runtime `/run` symlink to
create. See [Resolution](#resolution-adopt-nix-ld-as-the-lib64-interpreter-2026-07-19).
The step 6 baked `LD_LIBRARY_PATH` (image env + the `-e` re-export) is
retained deliberately — it is the dlopen-by-soname discovery path for
nix-built processes, which never traverse `/lib64` and so are structurally
unreachable by nix-ld. The sections below record the root-cause investigation
and the alternatives that were weighed and rejected.

**Why this doc exists:** the MCP node/npx wrapper (see
[mcp-configuration.md](mcp-configuration.md) §1) keeps generating "just patch it
here too" fixes (the custom-`mcp_servers` gap being the latest). That's
whack-a-mole. This doc pins the actual mechanism so the fix can be structural,
and records the dead ends so they aren't re-explored from scratch.

All claims below were verified empirically from inside a running jail (2026-07-18,
x86_64 Linux host, podman). Commands + observed output are quoted.

---

## The symptom

A non-nix (mise/npm-installed) `node` crashes when `LD_LIBRARY_PATH` is not set:

```
$ ( unset LD_LIBRARY_PATH; /mise/installs/node/22/bin/node --version )
/mise/installs/node/22/bin/node: error while loading shared libraries:
libstdc++.so.6: cannot open shared object file: No such file or directory
```

The image bakes `LD_LIBRARY_PATH=/lib:/usr/lib:/usr/lib/<multilib>` into the
container env, so *most* processes are fine. It only bites when a launcher
**scrubs the child environment** — which some agents' MCP-server spawn paths do.
The wrappers (`~/.local/bin/mcp-wrappers/{node,npx}`) re-assert the var right
before `exec` to survive that.

## The WRONG explanation (correcting an earlier version of this)

An earlier draft said: *"the FHS-assuming node has no RPATH, so it needs
`LD_LIBRARY_PATH` — this would hit anyone with a dynamically-linked node."*

**That is wrong.** On a normal FHS distro, a dynamically-linked node finds
`libstdc++.so.6` with no `LD_LIBRARY_PATH`, because the system `ld.so` reads
`/etc/ld.so.cache` (built by `ldconfig`), which lists
`/usr/lib/.../libstdc++.so.6`. Stripping `LD_LIBRARY_PATH` on real Ubuntu is
harmless. So the problem is **not** intrinsic to dynamic linking — it's specific
to what this image does to the loader.

## The RIGHT explanation (proven)

Two `node`s exist in the jail, with **different interpreters**:

```
/bin/node                     -> /nix/store/…-nodejs-slim-22.23.1/bin/node   (nix-built)
    interpreter: /nix/store/…-glibc-2.42-67/lib/ld-linux-x86-64.so.2
/mise/installs/node/22/bin/node                                              (upstream/mise)
    interpreter: /lib64/ld-linux-x86-64.so.2
```

- **The nix `/bin/node` runs env-free** — `v22.23.1` with `LD_LIBRARY_PATH`
  unset — because nix baked a correct `RPATH` into it pointing at the store
  libs. It never needs the var.

- **The mise node's interpreter `/lib64/ld-linux-x86-64.so.2` is a symlink the
  image created → the SAME nix glibc `ld.so`** (`.../glibc-2.42-67/lib/…`). Why:
  a pure-nix image has no `/lib64/ld-linux-x86-64.so.2`, so a stock FHS binary
  couldn't load *at all*; the flake symlinks the nix `ld.so` there so FHS
  binaries are runnable. That symlink is the origin of the whole problem.

- **That nix `ld.so` does NOT behave like a system `ld.so`.** `strace` (mise
  node, no `LD_LIBRARY_PATH`) shows it consults only:
  1. its own **store-baked cache**, `.../glibc-2.42-67/etc/ld.so.cache`
     — which is **ENOENT** in this image, and
  2. a **system search path derived from its own store build** —
     `.../glibc-2.42-67/lib` and `.../xgcc-…-libgcc/lib` (the hwcaps dirs).

  It never looks in `/lib`, `/usr/lib`, and — critically — **never reads
  `/etc/ld.so.cache`.** And `libstdc++.so.6` lives in a *different* nix store
  path (gcc's `cc.lib` output) that is **not** on that baked search path. So the
  lookup misses:

  ```
  openat("…/glibc-2.42-67/etc/ld.so.cache")               = ENOENT
  openat("…/glibc-2.42-67/lib/libstdc++.so.6")            = ENOENT
  openat("…/xgcc-15.2.0-libgcc/lib/.../libstdc++.so.6")   = ENOENT   (all hwcaps)
  → cannot open shared object file
  ```

- **`LD_LIBRARY_PATH=/lib:/usr/lib` is the only lever that rescues it.** The
  flake symlinks the real libstdc++ into `/lib` and `/usr/lib` (the "lib farm",
  `flake.nix:366-385`); with the var set, `ld.so` searches those dirs and finds
  `/lib/libstdc++.so.6 → calling init`. Confirmed working.

**So the root cause is:** the image points a stock FHS binary at a nix `ld.so`
whose baked cache + search path are blind to `libstdc++`, and `LD_LIBRARY_PATH`
is the only remaining path to the lib. It is entirely a property of this image's
loader wiring — not of "dynamically-linked node." The nix `/bin/node` proves an
env-free node is possible here.

Corroborating facts:
- `/etc/ld.so.cache -> /run/ld.so.cache` exists and DOES list
  `/usr/lib/libstdc++.so.6` (built at startup by the entrypoint's
  `generate_ld_cache`, `src/entrypoint/system.py`). But the nix `ld.so` ignores
  it, so it's inert for this purpose — a diagnostics cache only. (The flake
  comment at `flake.nix:489-499` already says this: *"the nix loader ignores it
  and uses LD_LIBRARY_PATH … a consumer that scrubs LD_LIBRARY_PATH cannot be
  rescued by this cache; that is a documented limitation."*)
- `LD_PRELOAD=/lib/libstdc++.so.6 node --version` → works (proves the lib is
  fine, it just isn't *found*).

---

## Structural fixes considered — and why the clean ones are BLOCKED

| Option | Verdict | Evidence |
|---|---|---|
| `patchelf --set-rpath`/`--set-interpreter` the mise node so it's self-contained | ❌ **Blocked** | The mise store is **bind-mounted from the host** (`~/.local/share/mise`, AGENTS.md). Rewriting the binary to nix-store paths would break node **on the host** (those paths don't exist there). Also: `patchelf` isn't in the image. |
| Make `/run/ld.so.cache` (the FHS cache) authoritative for the mise node | ❌ **Blocked** | The nix `ld.so` reads only its **own store cache** (`.../glibc/etc/ld.so.cache`, read-only) and its store-derived search path. It **never reads `/etc/ld.so.cache`** (proven by strace). Can't be redirected at runtime. |
| `/etc/ld.so.preload` (a file `ld.so` honors unconditionally — scrub-proof) | ❌ Blocked **at runtime only** — see correction | `/etc` is **read-only** at runtime. **Correction (2026-07-19):** this verdict confused runtime with build time. `/etc` is *constructed at image build*, and the nixpkgs glibc loader reads the literal path **`/etc/ld-nix.so.preload`** (via `dont-use-system-ld-so-preload.patch`), so baking that file listing `/lib/libstdc++.so.6` is feasible today. Rejected anyway — it preloads into *every* process, including nix-built ones. See Resolution §rejected. |
| Keep `LD_LIBRARY_PATH` in the image env (current) | ⚠️ Works, scrubbable | Baked in `config.Env`; fine except when a launcher scrubs the child env. |
| Wrapper scripts re-asserting the var (current) | ✅ Works, scrub-proof, but **per-call-site** | The whack-a-mole surface: presets route through it; custom servers don't (the open gap). |

The takeaway: given (a) read-only `/nix/store`, (b) a **host-shared** mise
binary that must stay host-loadable, and (c) a nix `ld.so` that ignores the FHS
cache, **there is no runtime way to make the mise node self-sufficient.** The
`LD_LIBRARY_PATH` manipulation is not gratuitous — it is the only lever left
*because of* those three constraints.

---

## Directions worth a fresh look (SUPERSEDED — kept for history; see Resolution below)

### A. Route MCP servers through the nix node (feasible now, runtime-only)
`/bin/node` needs no env. The wrappers already `exec /bin/node`. Generalize the
preset pattern so **custom** `mcp_servers` with a bare `node`/`npx` command are
rewritten to the wrapper path too (one rewrite in the shared
`_load_mcp_servers` / `LoadMCPServers`). This makes the **server process itself**
self-sufficient (nix node + RPATH) and closes the custom-server gap. It does
NOT eliminate the wrappers' `LD_LIBRARY_PATH` line, which stays as
belt-and-suspenders for any grandchild the server spawns. Caveat: MCP servers
then run under nix node 22.23 rather than the mise-resolved version — usually
fine, worth a conscious decision. **This is a mitigation, not the elimination
the user wants — but it stops the per-call-site whack-a-mole.**

### B. Fix it at IMAGE-BUILD time (the real "remove the manipulation" path — UNEXPLORED)
The runtime is boxed in, but the **image build** is not. Candidates, none yet
investigated:
1. **Put the libstdc++ store path on the nix `ld.so`'s baked search path** — i.e.
   build the glibc/`ld.so` used as the FHS interpreter with the gcc `cc.lib`
   output in its default `RPATH`/search path, so `libstdc++.so.6` is found with
   no env var. If achievable, the mise node "just works" and every layer of this
   manipulation (baked env + wrappers) can go away.
2. **Bake a store `ld.so.cache` that includes the farm** at the exact path the
   nix `ld.so` reads (`.../glibc/etc/ld.so.cache`). Blocked today only because
   that path is read-only *and* the cache is empty; a build-time step could
   populate a cache the interpreter actually consults. (Note the existing
   `generate_ld_cache` writes the *wrong* cache path for this loader.)
3. **Use an FHS-cache-reading `ld.so`** as the `/lib64` interpreter — one whose
   compiled-in cache path is `/etc/ld.so.cache` — so the entrypoint's existing
   `/run/ld.so.cache` (which already lists libstdc++) becomes authoritative and
   `generate_ld_cache` stops being inert.

Any of B.1–B.3 would let us delete the baked `LD_LIBRARY_PATH` env AND the
wrapper `LD_LIBRARY_PATH` lines — the actual goal. They're `flake.nix` changes
(image rebuild), higher-risk, and need someone to validate the nix mechanics.

### C. Do nothing structural; just document + fix the custom-server gap via A
Accept the constraint (it's already half-documented in `flake.nix`), take
direction A to stop the whack-a-mole, and leave B for later.

---

## Key files / evidence pointers
- Lib farm + interpreter symlinks: `flake.nix:360-399` (symlink libstdc++ etc.
  into `/lib`, `/usr/lib`; link nix `ld.so` at `/lib64`).
- `LD_LIBRARY_PATH` baked in image env: `flake.nix:718`; re-exported on the
  container `-e`: `src/cli/run_cmd.py:1937` / `internal/cli/run/assemble.go:381`.
- The "documented limitation" comment: `flake.nix:489-499`.
- Inert FHS cache generator: `generate_ld_cache` in `src/entrypoint/system.py`
  (writes `/run/ld.so.cache`, which the nix loader never reads).
- MCP wrappers: `src/entrypoint/mcp_wrappers.py` (Go: `internal/entrypoint/mcp_wrappers.go`).
- Custom-server gap: `_load_mcp_servers` in `src/entrypoint/agent_configs.py`
  (Go: `internal/entrypoint/mcp.go`) — presets hardcode the wrapper; custom
  servers are stored verbatim.

## Reproduce the diagnosis
```bash
# nix node: env-free OK
( unset LD_LIBRARY_PATH; /bin/node --version )                     # v22.23.1

# mise node: crashes without the var
( unset LD_LIBRARY_PATH; /mise/installs/node/22/bin/node --version )  # libstdc++ error

# why: the nix ld.so it uses ignores /etc/ld.so.cache, searches only its store paths
( unset LD_LIBRARY_PATH; LD_DEBUG=libs /mise/installs/node/22/bin/node --version 2>&1 \
    | rg 'search cache|search path|libstdc' )

# the var rescues it via the /lib farm symlink
( export LD_LIBRARY_PATH=/lib:/usr/lib; LD_DEBUG=libs /mise/installs/node/22/bin/node --version 2>&1 \
    | rg 'trying file=/lib/libstdc|calling init.*libstdc' )
```

---

## Resolution: adopt nix-ld as the /lib64 interpreter (2026-07-19)

Produced by a multi-agent pass (repo recon, live probes, web research on
primary sources, **in-jail empirical validation**, and three adversarial
reviews). All load-bearing claims below were verified, not assumed.

### TL;DR

Replace the `/lib64/ld-linux-x86-64.so.2` symlink target — currently the raw
nix glibc `ld.so` — with **[nix-ld](https://github.com/nix-community/nix-ld)
2.x** (stock nixpkgs binary, ~30 KiB static-PIE, substitutable from
cache.nixos.org). nix-ld is *designed* to sit at exactly that path as the ELF
interpreter for FHS binaries, and since v2.0 (Rust rewrite, 2024) it has an
**env-free fallback**: with a completely scrubbed environment it loads the real
`ld.so` from `/run/current-system/sw/share/nix-ld/lib/ld.so` and points the
loader at that same dir for libraries, then a per-arch entry trampoline
**reverts the env edit before the app's entry point runs** — children inherit
nothing. Because *every* FHS `exec` re-enters the shim at `/lib64`, the
defaults are re-established per-process with zero env dependence. This kills
the per-call-site whack-a-mole as a *class*: custom `mcp_servers` with bare
`node` commands, future FHS tools, scrub-happy launchers — all covered with no
wrapper needed.

This is also the ecosystem-consensus fix: **mise's own docs recommend nix-ld**
for precompiled binaries on NixOS, as do the NixOS wiki and NixOS-WSL docs.
The env-scrub scenario is literally why the fallback exists (nix-ld issue #17;
maintainer: "nix-ld now falls back to the system path since nix-ld 2.0").

### Empirical validation (run in this jail, 2026-07-19)

nix-ld 2.0.6 was fetched via the host daemon and tested against a
`patchelf --set-interpreter`'d **copy** of the real mise node (real `/lib64`
and real mise binaries untouched):

| Test | Result |
|---|---|
| `env -i` + `NIX_LD`/`NIX_LD_LIBRARY_PATH` set → `node --version` | ✅ v22.23.1 |
| **`env -i`, nothing set, fallback dir present** (the crux) | ✅ v22.23.1 — minimal wiring is **2 symlinks**: `/run/current-system/sw/share/nix-ld/lib/{ld.so → nix glibc ld.so, libstdc++.so.6 → gcc cc.lib}` |
| `env -i`, fallback dir absent | ❌ `[nix-ld] FATAL: panicked … Posix(2)` + SIGABRT (exit 134) — cryptic; see amendments |
| Grandchild env hygiene | ✅ children see only `LD_LIBRARY_PATH=""` (present-but-empty; glibc treats it exactly as unset — probed with `LD_DEBUG`), no `NIX_LD*` leakage. **Strictly cleaner than today**, where the baked `/lib:/usr/lib` is searched *before* every nix binary's `DT_RUNPATH` |
| FHS grandchild of a scrubbed-env process | ✅ re-enters nix-ld via PT_INTERP, re-resolves env-free — the chain needs no propagated vars |
| Regressions | ✅ nix `/bin/node` untouched (store PT_INTERP bypasses nix-ld); nix-ld **coexists** with today's baked `LD_LIBRARY_PATH` (safe staged rollout); mise nvim (never had the bug) still works |
| `dlopen` coverage | ✅ glibc snapshots the search-path list at rtld init (`_dl_init_paths`); the trampoline's env revert cannot retract it, so later `dlopen`s from the FHS process still find farm libs |
| Removing the fallback dir | ❌ failure returns — proves the fallback dir is the operative mechanism |

Also probed: among all mise-installed tools in this jail (node, neovim,
python 3.11–3.14, uv, just, go), **node is the only one that hits the loader
problem** — the rest are glibc-only, static, or musl.

### Implementation blueprint (amendments from adversarial review folded in)

1. **flake.nix:** retarget the `/lib64` (and `/lib`) dynamic-linker symlink
   (`flake.nix:361-364`) → nix-ld. Two variants:
   - **A (preferred): custom nix-ld derivation with baked defaults.**
     `DEFAULT_NIX_LD` is a build-time `option_env!` in nix-ld's source — bake
     it to `${imagePkgs.stdenv.cc.bintools.dynamicLinker}` so the real loader
     always resolves with zero env vars and zero runtime wiring. This
     eliminates the cryptic-SIGABRT class outright: the panic was an unwrap on
     a *missing loader*; with the loader compiled in, a missing lib dir
     degrades to the familiar readable `libstdc++.so.6: cannot open` error.
     The default *library* dir is a hardcoded const in current source
     (`/run/current-system/sw/share/nix-ld/lib`), so either keep the
     entrypoint `/run` symlink (step 4) for the lib half, or add a one-line
     `substituteInPlace` pointing it at the baked `/usr/share/nix-ld/lib` and
     drop the runtime wiring entirely.
   - **B (zero-build fallback): stock `pkgs.nix-ld`** — substitutable from
     cache.nixos.org (~30 KiB) for both arches; works via the `/run` fallback
     wiring (step 4) + baked `NIX_LD` env (step 3). Fine as a first commit or
     if the custom-derivation plumbing ever regresses.

   *Delivery note (corrected 2026-07-19):* an earlier draft mandated variant B
   on the grounds that any recompile breaks the macOS "zero-Linux-builder"
   image build. That premise is stale: the image **already** contains
   repo-source `aarch64-linux` derivations that are never on cache.nixos.org
   (`yolo-jail-conf`, the entrypoint pkg, the stream script — see
   `handoff-cachix-cache.md`), so a custom nix-ld adds no new requirement — it
   rides the same delivery paths as the rest of the image: the CI Linux image
   build (`ci.yml`), the release-gated Cachix publish (`publish.yml`
   `push-image-cache`, wired pending account), and the on-demand macOS Linux
   builder (see `../research/macos-container-builder-exploration.md`). The flake's
   "no-Linux-builder property" comment (`flake.nix:52-60`) is specifically
   about host-cross-compiled Go binaries, not an image-wide invariant. A
   no_std Rust shim builds in seconds on the builder.
2. **flake.nix:** bake a defaults dir at a fixed **non-store** image path
   (e.g. `/usr/share/nix-ld/lib/`) in the `binPathLinks` derivation:
   `ld.so → ${imagePkgs.stdenv.cc.bintools.dynamicLinker}` plus symlinks to
   the core farm trio (glibc, `stdenv.cc.cc.lib`, zlib) lib files. Non-store
   paths survive the host `/nix/store:ro` shadow mount exactly like `/lib64`
   does. Keep it **minimal** — do *not* mirror the whole ~189-entry farm: the
   injected path outranks `DT_RUNPATH` for the FHS binary itself, so a smaller
   dir means a *smaller* shadow surface than today's. Grow on proven need.
3. **flake.nix:** under variant B, add `NIX_LD=<real ld.so store path>` to
   image `config.Env` as belt-and-suspenders — covers any FHS exec in the
   window before the entrypoint wires `/run` (nix-ld checks `NIX_LD` before
   its compiled-in fallback path, and `LD_LIBRARY_PATH` **cannot** mask a
   missing fallback: it locates *libraries*, not the loader). Under variant A
   this is redundant (the default is compiled in) but harmless.
4. **entrypoint (Python + Go twins):** ~~idempotently create
   `/run/current-system/sw/share/nix-ld/lib → /usr/share/nix-ld/lib`~~
   **NOT NEEDED under variant A (as shipped).** The flake patches the lib-dir
   const to the baked `/usr/share/nix-ld/lib` (a `substituteInPlace` on the
   `const`) *and* bakes `DEFAULT_NIX_LD` to the real glibc `ld.so` — verified
   the built binary has **zero** `/run/current-system` references. So there is
   no runtime symlink to create on any boot path. This step existed only for
   variant B's `/run` fallback.
5. **cli (both twins):** ~~give `--tmpfs /run` an explicit mode~~ **moot for
   nix-ld under variant A** (no `/run` dependency). `--tmpfs /run` is already
   unconditional in both mount modes (`internal/cli/run/runmount.go`) for
   unrelated reasons, so nothing changed here.
6. **KEEP the baked `LD_LIBRARY_PATH` (`flake.nix:718`).** Adversarial review
   found deleting it is a feature regression, not a cleanup: it is the only
   discovery mechanism for **dlopen-by-soname from nix-built processes**
   (the documented user-packages contract, `src/cli/config_ref_cmd.py:89-91`,
   and its integration test) — a class nix-ld structurally cannot reach, since
   nix binaries never pass through `/lib64`. One baked line is not the
   whack-a-mole; the *per-call-site re-assertions* were.
7. **DELETE, staged — DONE (`1d614e1`, `d38463a`).** Removed the
   `LD_LIBRARY_PATH` export lines from all three MCP wrappers
   (`internal/entrypoint/mcp_wrappers.go`: node, npx, chrome); `FONTCONFIG_*`
   stay (chromium font config, unrelated to the loader). Nested-jail verified:
   the wrappers regenerate without the line and the node wrapper still runs
   under `env -i`. The cli `-e` re-export (`assemble.go`) was **evaluated and
   kept** — it is byte-identical to the baked `config.Env` (confirmed via
   `podman image inspect`), so it is redundant on the podman path, but it is
   retained (with a comment) to keep the launch env self-describing and as the
   dlopen-by-soname path for nix processes. The custom-`mcp_servers` gap
   **closed for free** — bare `node` commands now survive scrubbed-env spawns
   with no wrapper. (Note: `keytar.node` still fails env-free, but that is a
   pre-existing **farm gap** — `libsecret-1.so.0` is not in the farm at all,
   and it failed *with* the wrapper `LD_LIBRARY_PATH` too — not a regression
   from this deletion. See "Known residuals".)
8. **Validation gate — DONE (`d6d2e65`).** Added an in-jail-only `yolo check`
   section that runs `env -i <mise node> --version` and reports PASS (nix-ld
   intact) / FAIL-with-remedy (regressed). Scrubbing is in the argv (`env -i`),
   not the check `Exec` env slice — `realExec` appends to `os.Environ()` and
   cannot scrub, so an empty env slice would falsely pass. Verified it FAILs on
   the old baked image and PASSes on the new nix-ld image. The broader `env -i`
   smoke matrix (Claude native binary, `copilot --version`, an MCP spawn, a
   ctypes `dlopen`, aarch64) remains a **host-gated acceptance step** before the
   maintainer ships the image via `just load` — the core mise-node crux is
   proven in-jail.

### Known residuals (stated honestly)

- The fix bounds the whack-a-mole; it doesn't abolish library curation.
  Binaries needing libs missing from the farm (e.g. downloaded
  playwright/puppeteer chromiums want libnspr4/nss) **already fail today with
  the env var set** — that's a one-line farm/extraLibPackages addition, in one
  place, not a call-site hunt. Nix `/usr/bin/chromium` remains the supported
  browser.
- glibc version coupling (mise updates a tool past the image glibc's baseline)
  is **byte-identical to today** — same nix `ld.so`, same cc.lib libstdc++ —
  with large measured headroom (node 22 needs GLIBC_2.28/GLIBCXX_3.4.21 vs
  image glibc 2.42/gcc-15's 3.4.34). Periodic nixpkgs bumps cover it.
- Descendants of FHS processes see `LD_LIBRARY_PATH=""` (the trampoline can
  blank the value but not remove the envp entry — nix-ld README footnote b).
  Probed: glibc treats empty exactly as unset; only presence-*tests* could
  notice. Recorded here so a future weird bug report is greppable.
- Container use of nix-ld outside NixOS is maintainer-blessed (issue #50:
  "just do a symbolic link" in lieu of tmpfiles.d) but dockerTools precedent
  is thin (issue #60 open) — yolo-jail is an early adopter; the NixOS module
  (buildEnv of libs + ld.so symlink) is the blueprint we're mirroring.
- No setuid FHS binaries exist in the image; nix-ld's AT_SECURE behavior is
  unverified. Record "no setuid FHS binaries" as an image invariant.

### Alternatives considered and rejected

| Alternative | Why rejected |
|---|---|
| **Custom glibc interpreter with `user-defined-trusted-dirs=/lib /usr/lib`** (upstream make flag; semantically purest — default dirs rank *last*, zero env manipulation) | *Buildable* — the CI/Cachix + on-demand-builder delivery (step 1 note) means a custom glibc is no longer impossible on macOS hosts — but still rejected: a second glibc paired with farm libs is a GLIBC_PRIVATE minefield (interpreter and `libc.so.6` are build-locked; farm core-lib symlinks would need retargeting to the variant), and it's a full glibc recompile on every nixpkgs pin bump versus a seconds-long Rust shim, for a marginal purity gain. Recorded so it isn't re-litigated. |
| **Cache-reading glibc interpreter** (one-hunk patch: `LD_SO_CACHE=/etc/ld.so.cache`, making the existing `generate_ld_cache` output authoritative — this doc's B.3) | Same GLIBC_PRIVATE + recompile-cadence costs as above, and cache outranks default dirs (worse shadow ordering than trusted-dirs). Strictly dominated. |
| **`/etc/ld-nix.so.preload` baked at image build** (the table's "Blocked" verdict was wrong — see correction above) | Genuinely available and zero-new-components, but preloads farm libstdc++ into **every** process including nix-built ones. Kept as a documented emergency stopgap only. |
| **De-mise-ing infrastructure alone** (pin bootstrap npm + agent launchers + MCP to nix `/bin/node`) | Insufficient as the *only* fix: a live custom MCP server in this jail (`cerebras-mcp`, bare command → mise shims → mise node) escapes any config-layer rewrite, and any future FHS binary re-enters the class. See below for its residual value. |
| **buildFHSEnv / steam-run** | bubblewrap needs nested user namespaces that fail in rootless podman; per-call-site anyway. |
| **musl/static node, source-compiled node** | Breaks the host-shared `/mise` constraint (binary must load on the host too) and fixes only node, not the class. |
| **ld-floxlib (LD_AUDIT)** | env-var-dependent — same scrub weakness we're eliminating. |
| **autoPatchelfHook / patchelf the real binary** | Mutates the host-shared binary; forbidden (unchanged from the original analysis). |

### On "maybe we rely on mise too much"

Half right, and worth naming precisely: the problem was never mise-for-projects
— it's that **jail infrastructure resolves `node` by PATH accident** (mise
shims precede `/bin`), so npm-global installs and `#!/usr/bin/env node`
shebangs run under the FHS node while the MCP *presets* already use nix
`/bin/node`. With nix-ld in place this stops being a correctness issue
entirely. Keep mise exactly where it's good: per-project tooling declared in
`mise.toml`. Optionally, as low-priority hygiene (not a fix): pin the
bootstrap's npm installs and agent launchers to nix `/bin/node` — four
call-site groups, zero version-skew risk today (both nodes are 22.23.1,
NODE_MODULE_VERSION constant across 22.x). The opposite philosophy
("everything via nix, no foreign runtimes") has a poor maintainability record
at scale — nixpkgs removed its entire `nodePackages` set as unmaintainable —
and is explicitly not the direction.

### Sources

- nix-ld: <https://github.com/nix-community/nix-ld> (v2.0.6, Oct 2025; Rust
  rewrite with compiled-in fallback `/run/current-system/sw/share/nix-ld/lib`;
  `option_env!("DEFAULT_NIX_LD")` exists but recompiling is rejected above).
  Issues #17 (env-scrub → fallback rationale), #50 (container/no-systemd use),
  #60 (dockerTools precedent, open).
- mise docs recommending nix-ld on NixOS:
  <https://mise.jdx.dev/installing-mise.html> (NixOS section).
- NixOS module blueprint: `nixos/modules/programs/nix-ld.nix` in nixpkgs.
- nixpkgs glibc preload-path patch: `dont-use-system-ld-so-preload.patch`
  (reads `/etc/ld-nix.so.preload`).
- Replit's fallback-not-override custom loader (`replit_rtld_loader`) — the
  closest precedent for the LD_LIBRARY_PATH-inheritance breakage class that
  nix-ld's trampoline revert avoids on x86_64/aarch64.
- Empirical artifacts from the validation run: scratch dir
  `nixld-test/` (patched `node-test`, `nvim-test`, `nix-ld`/`patchelf`
  out-links) — session scratchpad, not persisted.

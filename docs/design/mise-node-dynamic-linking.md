# The mise-node / `LD_LIBRARY_PATH` problem (investigation + handoff)

**Status:** open. This documents an empirically-proven root cause, why the
obvious structural fixes are blocked, and the one feasible-but-imperfect
direction — as a handoff for a fresh look. Nothing here is implemented; the
current mitigation (baked `LD_LIBRARY_PATH` + MCP wrapper scripts) is unchanged.

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
| `/etc/ld.so.preload` (a file `ld.so` honors unconditionally — scrub-proof) | ❌ **Blocked** | `/etc` is **read-only**, and `ld.so.preload` is **not** a symlink to a writable target (unlike `ld.so.cache → /run`). Can't create it at runtime. |
| Keep `LD_LIBRARY_PATH` in the image env (current) | ⚠️ Works, scrubbable | Baked in `config.Env`; fine except when a launcher scrubs the child env. |
| Wrapper scripts re-asserting the var (current) | ✅ Works, scrub-proof, but **per-call-site** | The whack-a-mole surface: presets route through it; custom servers don't (the open gap). |

The takeaway: given (a) read-only `/nix/store`, (b) a **host-shared** mise
binary that must stay host-loadable, and (c) a nix `ld.so` that ignores the FHS
cache, **there is no runtime way to make the mise node self-sufficient.** The
`LD_LIBRARY_PATH` manipulation is not gratuitous — it is the only lever left
*because of* those three constraints.

---

## Directions worth a fresh look

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
  container `-e`: `src/cli/run_cmd.py:1937` / `internal/runcmd/assemble.go:381`.
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

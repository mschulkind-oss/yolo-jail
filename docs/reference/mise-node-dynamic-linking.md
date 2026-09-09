---
status: current
verified: 2026-09-09
verified_commit: d8cf1cf8
covers:
  - flake.nix
  - internal/cli/check/section_nixld.go
  - internal/entrypoint/mcp_wrappers.go
tags: [nix-ld, dynamic-linking, ld-library-path, image, mise, node]
summary: "Why an FHS binary in this image could not find libstdc++ without LD_LIBRARY_PATH, and how nix-ld at /lib64 fixed it env-free. Covers the /lib farm, the baked nix-ld fallback lib dir, the one thing the baked LD_LIBRARY_PATH is still for (dlopen-by-soname from nix processes), the tripwire that catches a regression, and the alternatives that must not be re-litigated."
---

# Dynamic linking for FHS binaries — nix-ld, the `/lib` farm, and `LD_LIBRARY_PATH`

**Status:** CURRENT as of 2026-09-09, verified against `d8cf1cf8`.

The jail image is a pure-nix filesystem, so a stock FHS binary — anything mise or npm
downloads, anything with `#!/usr/bin/env node` behind it — has an ELF interpreter path
(`/lib64/ld-linux-*.so.2`) that a nix image does not naturally have. **The image supplies
that path with [nix-ld](https://github.com/nix-community/nix-ld), built with its two
defaults compiled in**, so an FHS binary resolves both the real loader and a minimal
library search path with **zero environment dependence** — including under a fully
scrubbed environment.

That is what closes the failure class this document exists for: an FHS `node` dying with
`libstdc++.so.6: cannot open shared object file` whenever a launcher scrubbed the child
environment.

| Component | Lives in |
| :--- | :--- |
| The nix-ld derivation, its two baked defaults, and the `/lib64` + `/lib` interpreter links | `flake.nix` (`nixLd`, `mkBinPathLinks`) |
| The `/lib` + `/usr/lib` library farm, and the baked nix-ld fallback dir | `flake.nix` (`mkBinPathLinks`, `extraLibPackages`) |
| The baked `LD_LIBRARY_PATH` and its per-launch re-export | `flake.nix` (image `Env`), `internal/cli/run/assemble.go` |
| The regression tripwire | `internal/cli/check` (`sectionNixLD`) |
| The MCP wrappers this used to be a per-call-site fix in | `internal/entrypoint/mcp_wrappers.go` |

**Reads with:** [`mcp-configuration.md`](mcp-configuration.md#the-nodenpx-wrapper) (the
wrappers, and what is left of their job),
[`image-staging-vs-baking.md`](image-staging-vs-baking.md) (what the image bakes and why),
[`nix-across-backends.md`](nix-across-backends.md) (how `packages:` reaches each backend).

---

## The mechanism

Two `node`s exist in a jail, with **different ELF interpreters**, and that is the whole
story:

- **The nix `/bin/node`** has a store-path `PT_INTERP` pointing at the nix glibc loader and
  a correct `RPATH` into the store. It runs env-free and never passes through `/lib64`.
- **A mise or npm-downloaded `node`** has `PT_INTERP` = `/lib64/ld-linux-<arch>.so.2`, the
  conventional FHS path, which in this image is a symlink the image lays down.

> [!WARNING]
> **The naive diagnosis is wrong, and it was believed once.** "The FHS binary has no RPATH,
> so it needs `LD_LIBRARY_PATH`" would apply to any distro — and it does not: on an ordinary
> FHS system a dynamically-linked node finds `libstdc++.so.6` with no `LD_LIBRARY_PATH`,
> because the system loader reads the FHS `ld.so.cache`. Stripping the variable there is
> harmless. The failure is **entirely a property of what this image points the FHS
> interpreter path at**, which is why the fix is an image-build change and not a per-binary
> one.

Before nix-ld, that symlink pointed at the **raw nix glibc `ld.so`**, and a nix loader does
not behave like a system loader. It consults only its own store-baked cache — which does not
exist in this image — and a search path derived from its own store build. It never looks in
`/lib` or `/usr/lib`, and **never reads the FHS loader cache** under `/etc`. Since `libstdc++.so.6` lives in
a *different* store path (gcc's lib output) that is not on that baked path, the lookup missed
entirely, and `LD_LIBRARY_PATH` pointed at the `/lib` farm was the only remaining lever.

nix-ld replaces that symlink target. It is a tiny static-PIE shim designed to sit at exactly
that path: it locates the real loader, points it at a library directory, `exec`s it, and a
per-architecture entry trampoline **reverts its own environment edit before the application's
entry point runs**, so children inherit nothing. Because *every* FHS `exec` re-enters the shim
at `/lib64`, the defaults are re-established per process with no environment dependence and no
propagation.

**Both defaults are compiled in**, which is what makes it env-free and runtime-wiring-free:

- the real loader's store path is baked into nix-ld's build-time loader default, so a
  fully-scrubbed environment still resolves the loader;
- the library-path default — a plain source constant upstream, not a build-time option — is
  retargeted by a build-time substitution to a **baked, non-store** image directory.

A non-store path is required for the second one, because a jail bind-mounts the host's
`/nix/store` read-only over the image's, exactly as `/lib64` itself must survive.

> [!WARNING]
> **A broken nix-ld cannot brick a jail's boot, and that asymmetry is load-bearing.** Nix-built
> binaries keep their store-path `PT_INTERP` and never pass through `/lib64`, so nix-ld affects
> FHS binaries only. Do not "simplify" by routing nix binaries through it.

## The three library paths, and what each is for

They look redundant and are not. Each covers a case the others structurally cannot.

**1. The baked nix-ld fallback dir.** The **only** library search path an FHS binary gets
under a fully scrubbed environment, because it is what nix-ld's compiled-in default names. It
holds deliberately just the **core trio** — glibc, libstdc++/libgcc_s, zlib — plus the real
loader under the name `ld.so`.

> [!WARNING]
> **Do not mirror the whole `/lib` farm into the nix-ld fallback dir.** The injected path
> outranks the FHS binary's own `DT_RUNPATH`, so a *smaller* directory means a *smaller*
> shadowing surface. Keeping it to the trio is what makes this cleaner than the baked variable
> it replaced; grow it only on a proven need.

**2. The `/lib` + `/usr/lib` farm.** Symlinks to every lib output in the image, plus the lib
outputs of everything the workspace declared in `packages:`. This is what
`LD_LIBRARY_PATH=/lib:/usr/lib:…` searches.

**3. The baked `LD_LIBRARY_PATH`** in the image environment, re-exported on the container
argv.

> [!WARNING]
> **Do not delete the baked `LD_LIBRARY_PATH` as leftover cleanup.** It is the **only**
> discovery mechanism for `dlopen`-by-soname from **nix-built** processes — the documented
> contract behind `packages:` adding a library for something to `dlopen` — and that is a class
> nix-ld structurally cannot reach, since a nix binary never passes through `/lib64`. One baked
> line was never the whack-a-mole; the *per-call-site re-assertions* were, and those are the
> ones that went.

The FHS `ld.so.cache` — the conventional `/etc` path — is a symlink into a tmpfs, populated at boot. It exists for tools that read
it *directly* — `ldconfig -p`, diagnostics — and is **inert for FHS lookup**, because the nix
loader does not consult it. Do not treat a populated cache as evidence that a library is
findable.

## The regression tripwire

`yolo check` runs an in-jail-only probe: a mise-installed node under `env -i`, asserting it
prints a version. That is the exact case nix-ld exists to cover, and if a nixpkgs bump or a
flake change regresses the wiring, the probe fails here with a remedy naming the interpreter
symlink and the baked fallback dir — rather than surfacing as a cryptic MCP-spawn failure deep
inside an agent.

Two details of the probe are deliberate:

- **The scrub is in the argv (`env -i`), not in the exec helper's environment slice.** That
  helper appends to the current environment and cannot scrub, so an empty slice would pass
  falsely no matter how broken the wiring was.
- **It skips silently** on the host (the FHS node and the interpreter wiring are jail state)
  and when no mise node is installed (nothing to probe is not a failure).

## Known residuals

These are permanent properties of the fix, not open work.

- **Library curation is bounded, not abolished.** An FHS binary needing a library that is in
  no farm still fails — a downloaded browser build wanting NSS, say. That is a one-line
  addition in one place (the farm / the declared package list), not a call-site hunt. This was
  equally true with the old baked variable, so it is not a regression from anything.
- **Descendants of an FHS process see `LD_LIBRARY_PATH=""`.** The trampoline can blank the
  value but not remove the envp entry. glibc treats empty exactly as unset; only a
  presence-*test* could notice. Written down so a future odd bug report is greppable.
- **glibc version coupling is unchanged** by nix-ld — same loader, same libstdc++ — so a mise
  tool built against a newer glibc than the image's is the same risk it always was, covered by
  periodic nixpkgs bumps.
- **No setuid FHS binaries exist in the image**, and nix-ld's `AT_SECURE` behaviour is
  therefore unexercised. Treat "no setuid FHS binaries" as an image invariant; adding one needs
  that behaviour verified first.

## Alternatives that must not be re-litigated

Each of these was investigated to a verdict. They are listed so the next person does not spend
the afternoon again.

| Alternative | Why not |
| :--- | :--- |
| `patchelf` the FHS binary's RPATH or interpreter | The mise store is **shared with the host**. Rewriting the binary to nix-store paths breaks it on the host, where those paths do not exist. |
| Make the FHS `ld.so.cache` authoritative for FHS lookup at runtime | The nix loader reads only its own read-only store cache and its store-derived search path. It cannot be redirected at runtime. |
| A custom glibc interpreter with `/lib /usr/lib` as trusted dirs | Semantically the purest option and genuinely buildable, but a second glibc paired with farm libs is a `GLIBC_PRIVATE` minefield — interpreter and `libc.so.6` are build-locked, and the farm's core-lib symlinks would have to be retargeted — and it is a full glibc recompile on every pin bump versus a seconds-long Rust shim. Marginal purity gain. |
| A cache-reading glibc interpreter (patch the compiled-in cache path) | Same recompile cadence and `GLIBC_PRIVATE` cost as above, and a cache outranks the default dirs, which is *worse* shadow ordering. Strictly dominated. |
| `/etc/ld-nix.so.preload`, baked at image build | Genuinely available — nixpkgs' glibc reads that literal path — and needs no new component, but it preloads a farm library into **every** process, nix-built ones included. Emergency stopgap only. |
| Pinning yolo's own infrastructure to the nix `/bin/node` and calling it done | Insufficient as the only fix: a live custom MCP server with a bare `node` command escapes any config-layer rewrite, and any future FHS binary re-enters the class. Still fine as hygiene. |
| `buildFHSEnv` / `steam-run` | bubblewrap needs nested user namespaces that fail under rootless podman, and it is per-call-site regardless. |
| A musl or statically-linked node | Breaks the host-shared mise store constraint, and fixes node rather than the class. |
| `LD_AUDIT`-based shims | Environment-variable dependent, which is the exact weakness being removed. |

> [!NOTE]
> **The root problem was never "we rely on mise too much."** It is that jail infrastructure
> used to resolve `node` **by PATH accident** — mise shims precede `/bin`, so npm-global
> installs and `env node` shebangs ran under the FHS node while the MCP presets already used
> the nix one. With nix-ld in place that stops being a correctness issue at all. mise stays
> where it is good: per-project tooling declared in a project's own config.

## What this does not license

- **Not** a per-call-site `LD_LIBRARY_PATH` re-assertion. That is the whack-a-mole this
  replaced; the loader wiring covers the class.
- **Not** deleting the baked `LD_LIBRARY_PATH`. It serves a different class entirely.
- **Not** growing the nix-ld fallback dir for convenience. Every entry there shadows an FHS
  binary's own `DT_RUNPATH`.
- **Not** treating the FHS `ld.so.cache` as an FHS discovery path. It is a diagnostics artifact.
- **Not** routing nix-built binaries through nix-ld.

## Current values

Verified at `d8cf1cf8`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| FHS ELF interpreter | nix-ld, linked at `/lib64/<loader basename>` and `/lib/<loader basename>` | `flake.nix` (`mkBinPathLinks`) |
| nix-ld's baked loader default | the image glibc's real dynamic linker, store path | `flake.nix` (`nixLd`, `DEFAULT_NIX_LD`) |
| nix-ld's baked library dir | `/usr/share/nix-ld/lib`, substituted over the upstream source constant | `flake.nix` (`nixLd` `postPatch`) |
| Contents of that dir | the core trio (glibc, `stdenv.cc.cc.lib`, zlib) plus `ld.so` | `flake.nix` (`mkBinPathLinks`) |
| Library farm | `/lib` and `/usr/lib` symlink farms | `flake.nix` (`mkBinPathLinks`, `extraLibPackages`) |
| Baked loader path | `LD_LIBRARY_PATH=/lib:/usr/lib:/usr/lib/<multilib>` | `flake.nix` image `Env`; re-exported by `internal/cli/run/assemble.go` |
| FHS cache (diagnostics only) | the `ld.so.cache` under `/etc`, a symlink to a tmpfs path written at boot | `flake.nix`, `internal/entrypoint` (`generateLdCache`) |
| Tripwire | `env -i <mise node> --version` under the "FHS loader (nix-ld)" section | `internal/cli/check/section_nixld.go` |

## Upstream

- [nix-ld](https://github.com/nix-community/nix-ld) — the shim, its env-free fallback, and
  the trampoline that reverts the environment edit. Its README footnote is the source for the
  empty-`LD_LIBRARY_PATH` residual above.
- [mise's installation docs](https://mise.jdx.dev/installing-mise.html) recommend nix-ld for
  precompiled binaries on NixOS, which is the same problem in a different host.
- nixpkgs' `nixos/modules/programs/nix-ld.nix` is the blueprint the image mirrors: a buildEnv
  of libraries plus a loader symlink.

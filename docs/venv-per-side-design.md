# Venvs across host and jail — share one, or split cleanly?

**Date:** 2026-07-03 · **Status:** analysis; decision surface is
[jail-state-separation-design.md](jail-state-separation-design.md), which
bundles this doc's recommendation (S2b) with F/F+ — answer open questions
there. Implemented 2026-07-03 (S2b shipped: per-side venv shadow mounts
backed by `ws_state/venv-shadows/`)
**Context:** options F/F+ in
[mise-host-jail-path-mismatch.md](mise-host-jail-path-mismatch.md) split the
mise store host↔jail and (F+) drop the host-path mirror. That surfaces the
venv question: workspace-resident `.venv`s are the one derived artifact that
crosses the host↔jail boundary. This doc evaluates the cleanest shared
design and the cleanest split design.

## Anatomy: what makes a venv side-specific

A uv/pip venv has three absolute-path dependencies:

| Component | Points at | Same on host & jail today? |
|---|---|---|
| `bin/python` symlink + `pyvenv.cfg` `home =` | the interpreter in the mise store (`$MISE_DATA_DIR/installs/python/<ver>/…`) | **Yes** — this is exactly what the same-path mise mount buys |
| Console-script shebangs (`bin/pytest` → `#!<venv>/bin/python`) | the **venv's own absolute path** | **No** — host venv lives at `/home/matt/code/<proj>/.venv`, jail sees it at `/workspace/.venv` |
| `site-packages` content | mostly portable; source-built C extensions link against the *building* side's libs | Mostly, until a source build happens |

Two conclusions fall out immediately:

1. **Venv sharing is already half-broken today.** The same-path mise mount
   keeps `python` and `python -m …` working across sides, but any console
   script installed into a host-created venv (`.venv/bin/pytest`,
   `.venv/bin/ruff`, …) has a `/home/matt/code/…` shebang that dangles
   in-jail, and vice versa. The workspace path mismatch (`/workspace` vs
   `~/code/<proj>`) — the same root cause as the rust-symlink bug — was
   always inside the venv too.
2. **Host and jail are different OS userlands** (Arch vs NixOS container).
   The mise interpreters are portable prebuilt binaries, and manylinux
   wheels are fine, but any *source-built* extension links against the side
   that built it. A shared venv with one source-built wheel is subtly
   broken on the other side with no error at install time.

## S1. The cleanest possible *shared* venv

For one `.venv` to be genuinely correct on both sides, all four must hold:

1. **Same venv path string** on both sides → requires option A (workspace
   mounted at its real host path, `/workspace` a symlink) so shebangs
   resolve. Nothing less fixes the shebang class.
2. **Same interpreter path string** → requires keeping the mise-store
   path mirror (F without F+). With split backing stores this still works
   — each side materializes its own copy at the same address — but it
   forecloses F+ and keeps the host-username path baked into jails.
3. **Exact-pinned interpreter versions** in `mise.toml` (e.g. `3.13.2`,
   not `3.13`) — otherwise host mise and jail mise resolve the range to
   different patch versions, the store dirs differ, and `pyvenv.cfg`
   dangles on one side.
4. **No source-built native deps** — prebuilt wheels only, forever, or
   accept silent cross-OS breakage.

**Verdict: reject.** Even at its cleanest, sharing requires a four-way
lockstep, forecloses F+ (which we want independently), and its failure mode
(condition 4) is invisible until runtime. The root problem is conceptual:
a venv is *derived, per-environment state* — sharing one across two OS
userlands is like sharing a build directory between two machines. It
mostly-works right up until it doesn't.

## S2. Split venvs — the candidates

If venvs are per-side, the design question becomes: where does the jail's
venv live, and do both sides keep the idiomatic `./.venv` path?

### S2a. mise-config override to a jail-local path — dead on arrival

There is no clean channel for a jail-only mise override:

- The jail's global mise config (`~/.config/mise/config.toml`, where
  `YOLO_MISE_TOOLS` is injected — entrypoint/mise.py:40) **loses** to the
  project's `mise.toml` in mise's precedence order, so it can't override
  `_.python.venv`.
- Writing `.mise.local.toml` into the project dir would win, but the
  workspace is **shared** — the host's mise would pick it up too. (The
  earlier option-D note in the mismatch doc claimed yolo already generates
  one; verified false — nothing in `src/` does.)
- `MISE_ENV=jail` + a `mise.jail.toml` in the project is host-inert (the
  host never sets `MISE_ENV`) and is a good *per-project escape hatch*,
  but it's a checked-in file per project — whack-a-mole as a default
  mechanism. (Precedence of env-specific configs: verify against mise
  docs before relying on it.)

And none of these hide the host's (broken-in-jail) `.venv` from agents.

### S2b. Shadow mount — bind a jail-side dir over `/workspace/.venv` ★

At jail start, mount a per-workspace host-side state dir over the venv
path inside the workspace mount:

```
-v {ws_state}/venv:/workspace/.venv
```

Host processes see the host's `.venv`; jail processes see the jail's — at
the **same relative path**, kernel-enforced, with zero project config and
zero mise tricks. Everything that hardcodes `./.venv` (uv defaults,
pytest, editors, LSP servers, scripts) works identically on both sides.

Why this is the clean one:

- **The footgun fixes itself.** The venv pre-create hook (shell.py:291)
  skips when `.venv` exists — today a host-created venv would sit broken
  in-jail. Under the shadow, the jail's view starts empty on first boot,
  so the hook creates a correct jail venv naturally.
- **Recreation is cheap and shared.** `~/.cache` (including uv's wheel
  cache) is already mounted from `GLOBAL_CACHE` into every jail
  (run_cmd.py:1062, 1108), so the second jail's venv creation hits a warm
  cache. If `ws_state` and `GLOBAL_CACHE` live on the same filesystem, uv
  hardlinks instead of copying.
- **Persistent per workspace.** Backing the shadow with `ws_state/venv`
  (not tmpfs) means the jail venv survives restarts — no per-boot rebuild.
- **Composes with F+ and nesting.** The jail venv's `pyvenv.cfg` points
  into the (neutral-path) jail-land store, valid in every jail; a nested
  jail shadows the outer's shadow the same way.
- **Generalizes.** The same mechanism extends to a `per_side_paths`
  config (default `[".venv"]`) for other side-specific derived state —
  e.g. a workspace `.cargo` (`CARGO_HOME = {{config_root}}/.cargo`,
  polyclav) whose rustup metadata and bin shebangs are equally per-side.
  (Note: shadowing `.cargo` does *not* fix the jail↔jail mise
  `installs/rust` symlink residue — that still needs option A or a prune.)

Edge cases:

- **Custom venv paths.** Default-shadow `.venv`; additionally parse
  `env._.python.venv.path` from `mise.toml`/`.mise.toml` at mount-assembly
  time (the CLI already ships a parser for exactly this key in the
  pre-create script) and shadow that path too. Explicit `venv_paths` /
  `per_side_paths` in `yolo-jail.jsonc` covers monorepos and oddballs.
- **Mountpoint creation.** If the host workspace has no `.venv`, podman
  creates the mountpoint dir — an empty `.venv/` appears in the host
  workspace (gitignored in practice; create it proactively to be tidy).
- **Venvs already outside the workspace** (under `$HOME`): nothing to do —
  they're per-side for free since HOME differs.
- **uv "is this a venv?" check:** uv happily creates into an existing
  empty directory; the empty first-boot shadow is fine.

### S2c. Recreate-in-place on mismatch — rejected

Keep one shared `.venv`, detect a dangling interpreter, rebuild. This
ping-pongs: every host↔jail switch rebuilds the venv for the current side
and breaks it for the other — the exact never-converges pathology of the
rust symlink bug, now applied to a much bigger artifact.

### S2d. Convention: venvs under `$HOME` in every project — not ours to impose

`_.python.venv.path = "{{env.HOME}}/.venvs/<name>"` in each project's
`mise.toml` is per-side for free and arguably good practice, but it means
editing every project and fighting every tool that assumes `./.venv`.
Fine as user preference; not a yolo mechanism.

## Recommendation

**Split, via S2b shadow mounts.** Sharing is rejected on principle (derived
state across OS userlands), and S2b is the only split variant that needs no
project changes, keeps the idiomatic path on both sides, hides the other
side's venv entirely, and turns the existing pre-create hook from a footgun
into the mechanism that populates the jail venv. Ship as a general
`per_side_paths` mount feature with `[".venv"]` + the parsed
`_.python.venv` path as defaults.

Combined with F (split store) and F+ (neutral store path), this completes
the separation story: **no jail state references a host path, no host state
references a jail path, and the only things crossing the boundary are the
workspace sources themselves.**

## Open Questions

Moved to
[jail-state-separation-design.md](jail-state-separation-design.md) (shadow
list parsing vs config, shadow backing location, `MISE_ENV=jail`) — answer
them there.

# mise shared-state / host↔jail path mismatch — findings

**Reference — incident record (2026-07-02 → 07-04).** The investigation behind
today's host↔jail state separation: why a shared mise store with mismatched
workspace paths corrupts `mise install`, and the `.mise.toml` trust-hook fix.
The *design* that resolved it is
[jail-state-separation-design.md](../design/jail-state-separation-design.md)
(split store + neutral `/mise` path + per-side venvs); this doc is kept as the
root-cause narrative and rejected alternatives.

**All resolved:**
- **rust residual closed (2026-07-04):** jails export `RUSTUP_HOME=/mise/rustup`
  + `CARGO_HOME=/mise/cargo`, so the rust backend stops crashing on the
  read-only `~/.rustup` and records `installs/rust/<ver>` symlinks that resolve
  identically in every jail — the jail↔jail collision this doc's "Residual
  issue" analyzed no longer produces rust entries at all.
- **trust-hook fixes (2026-07-03):** un-gated `mise trust --all --quiet` +
  `MISE_TRUSTED_CONFIG_PATHS=/workspace` (replacing the no-op `MISE_TRUST=1`),
  covering the `.mise.toml` filename variant that jail auto-trust missed.
- **provisioning fails loudly (2026-07-03):** persisted
  `/workspace/.yolo/startup.log`, red banner, continue/abort prompt.
- Only the upstream mise issue (option E — store key should include the target)
  remains outside our control.

**Observed in:** `~/code/songtv` (symptom surface), `~/code/polyclav` (origin),
host-global. mise `2026.6.11` host / `2026.2.1` in-jail at the time.

## Summary

`MISE_DATA_DIR` (`~/.local/share/mise`) is deliberately bind-mounted into every
jail **at the same absolute host path** so absolute venv/shebang paths resolve
identically (see `storage-and-config.md`). But the **workspace** is mounted at
a *different* path (`/workspace` in-jail vs `~/code/<project>` on host). Any
mise backend that writes an absolute workspace-derived path into the shared
data dir produces state that is valid on exactly one side and dangling on the
other. mise's rust backend does exactly this, and a single dangling entry
makes **every** `mise install` on the host fail — in unrelated projects.

A second, independent bug was found along the way: jail auto-trust only covers
`mise.toml`, not the `.mise.toml` filename variant.

## Symptoms as the user experiences them

1. `mise install` in *any* project on the host (observed in songtv, whose own
   tools install fine) exits nonzero with:

   ```
   mise ERROR failed to rebuild runtime symlinks
   mise ERROR failed rm: ~/.local/share/mise/installs/rust/1.95
   mise ERROR No such file or directory (os error 2)
   ```

   **Update 2026-07-03:** this fatal behavior is version- and/or
   state-dependent, not universal. Inside the yolo-jail jail (mise
   `2026.2.1`, where *both* rust entries dangle — see below), `mise install`
   exits 0 with "all tools are installed". The host failure was observed on
   mise `2026.6.11`. Either newer mise made the symlink-rebuild rm fatal, or
   the rebuild pass only fires when there is something to prune on that side.

2. A recurring "rust install thing": `cd ~/code/polyclav` on the host prints
   `mise WARN missing: rust@1.95.0` and mise wants to (re)install rust; inside
   a jail the *other* rust entry is the dangling one. Each side "repairs" rust
   with a path the other side can't resolve, so it never converges.

3. (Separate bug) Inside the songtv jail, provisioning's `mise install` fails
   with `Config files in /workspace/.mise.toml are not trusted`, and the boot
   continues anyway — the failure scrolls past and the jail comes up with
   project tools missing.

## State on disk (evidence)

```
$ ls -la ~/.local/share/mise/installs/rust/
1.95.0 -> /workspace/.cargo/bin      # created Jul 2 16:39, from inside the polyclav jail
1.96.0 -> /home/matt/.cargo/bin      # created Jun 28,       from the host
```

- `1.95.0` is dangling **on the host** (`/workspace` doesn't exist there).
- `1.96.0` is dangling **inside any jail** (jail home is `/home/agent`;
  `/home/matt/.cargo` isn't mounted).
- **Re-verified 2026-07-03:** both entries still on disk (remediation below
  not yet applied). In the yolo-jail jail *both* dangle — `/workspace/.cargo`
  only exists in the polyclav jail — which concretely confirms the jail↔jail
  variant of the collision (two workspaces both named `/workspace` writing
  into shared state), not just host↔jail.
- Sharing confirmed empirically: running `mise install` inside the songtv jail
  created `installs/go-github-com-air-verse-air/` in the **host's** data dir
  with a matching timestamp.

Origin: polyclav's `mise.toml` pins `rust = "1.95.0"` and sets
`CARGO_HOME = "{{ config_root }}/.cargo"`. mise's rust backend symlinks
`installs/rust/<version>` → `$CARGO_HOME/bin`. Inside the jail
`config_root = /workspace`, so the shared data dir received a jail-only
absolute path. The toolchain *files* are fine — polyclav's `.cargo/` is inside
the bind-mounted workspace and shared; only mise's recorded symlink is
side-specific.

Blast radius: mise's "rebuild runtime symlinks" pass runs on every
`mise install` regardless of directory, so one dangling entry breaks the
command globally on the host, not just in polyclav.

## Reproduction

1. In a project whose mise config sets `CARGO_HOME`/`RUSTUP_HOME` (or any
   tool-install target) relative to `{{ config_root }}`, run `mise install`
   **inside a jail** so a rust toolchain gets installed.
2. On the host, run `mise install` in any project →
   `failed rm: installs/rust/<ver>` error above.
3. `rm` the dangling symlink, reinstall on the host → the in-jail side is now
   the broken one. Repeat forever.

## Secondary finding: `.mise.toml` never auto-trusted

All **three** trust hooks are gated on the un-dotted filename only
(line refs current as of 2026-07-03):

- `src/entrypoint/shell.py:100` — `mise trust --quiet /workspace/mise.toml`
- `src/entrypoint/__init__.py:421` — `if Path("/workspace/mise.toml").exists(): mise trust --quiet ...`
- `src/cli/run_cmd.py:2018` — `(if [ -f mise.toml ]; then mise trust --quiet; fi)`

songtv uses `.mise.toml`, so its config is never trusted; provisioning's
`mise install` errors and the error is swallowed (boot continues). Manual
`mise trust` inside the jail fixed it. mise's own config resolution accepts
`mise.toml`, `.mise.toml`, `mise.local.toml`, `.mise/config.toml`, etc. — the
trust hooks should cover the same set (or just run `mise trust --quiet` with
no path argument, which trusts whatever config mise resolved).

**Confirmed 2026-07-03:** `run_cmd.py:1194` sets `MISE_TRUST=1`, which is a
no-op — `mise settings ls --all | rg -i trust` lists only
`trusted_config_paths`; there is no `trust` setting for the env var to map
to. Note `MISE_YES=1` *is* already set two lines below (`run_cmd.py:1196`),
but it only auto-answers interactive prompts; in non-TTY provisioning mise
doesn't prompt, it just errors — which is exactly what happened in songtv.
Drop `MISE_TRUST=1` or replace it with `MISE_TRUSTED_CONFIG_PATHS=/workspace`.

## Candidate solutions (for evaluation)

### A. Mount the workspace at its real host path; keep `/workspace` as a symlink

Precedent already exists: `MISE_DATA_DIR` is same-path-mounted for exactly
this reason. If `/home/matt/code/polyclav` is the bind target and
`/workspace → /home/matt/code/polyclav` is a symlink, `config_root` becomes
identical on both sides and the **whole class** dies: rust symlinks,
project-local venvs, node_modules shebangs, anything recording
`{{ config_root }}`-derived absolute paths.

- ‒ Touches a core invariant; `/workspace` is baked into docs, agent
  briefings, shims, LSP config, `.mise.local.toml` generation.
- ‒ Symlinked cwd can confuse tools that canonicalize paths (`pwd -P`,
  go toolchain caching, watchers) — needs a test pass.
- ‒ Leaks the host username/layout into the jail (mild; MISE_DATA_DIR mount
  already does).
- \+ Fixes host↔jail *and* jail↔jail (two workspaces both called `/workspace`
  writing into shared state would also collide today).

### B. Stop sharing `installs/` — per-side overlay

Keep the shared CAS for downloads/cache, but bind-mount a jail-local (or
per-workspace) directory over `$MISE_DATA_DIR/installs` (or set
`MISE_DATA_DIR` jail-local and share only `MISE_CACHE_DIR`).

- \+ Small, contained change; no path-invariant changes.
- ‒ Loses install sharing: every jail reinstalls node/go/python (~30s–minutes
  per jail boot, disk duplication) — the cost the current design explicitly
  paid to avoid.
- ± Middle ground: share `installs/` but overlay only backends known to write
  absolute config-root paths (today: `rust`). Fragile allow-list.

### C. Self-heal: prune dangling `installs/` symlinks

At jail boot (and/or via a `yolo doctor` host command), scan
`$MISE_DATA_DIR/installs/*/*` for symlinks whose target doesn't exist on the
current side and remove them, letting mise reinstall.

- \+ Cheap, fixes the global `mise install` breakage immediately.
- ‒ Treats the symptom: rust reinstalls on every host↔jail switch for
  projects like polyclav (rustup re-link is fast since `.cargo/` is shared,
  but it's still churn and still surprises users).
- ‒ Host-side healing needs a host-side hook; yolo currently has no host
  daemon — would have to run on `yolo` invocation.

### D. Config-level escape hatch: disable specific tools in-jail

Have a jail-side mise override set `MISE_DISABLE_TOOLS=rust` or override the
tool when the jail provides the toolchain another way (polyclav's flake
already ships rust in-jail). *(Correction 2026-07-03: this originally said a
generated `.mise.local.toml` already exists — verified false, nothing in
`src/` writes one, and it couldn't work anyway since the workspace is shared
with the host. The viable channel is `MISE_ENV=jail` + a checked-in
`mise.jail.toml`, host-inert — see
[jail-state-separation-design.md](../design/jail-state-separation-design.md).)*

- \+ Zero core changes; works today per-project.
- ‒ Per-project whack-a-mole; doesn't protect the shared state dir from the
  next project; polyclav-on-host still writes `~/code/polyclav/.cargo/bin`
  symlinks that dangle in-jail.

### F. Split host↔jail; keep one **jail-land shared** store (macOS already does this)

Stop sharing the host's mise dir with jails, but keep all jails sharing a
single store so installs are cached across jails (no per-jail reinstall —
the cost that killed option B). The trick that makes it cheap: keep the
**mount path string identical** (`/home/<user>/.local/share/mise`) and only
swap what backs it — a host-side dir like `~/.local/share/yolo/mise-jail`
(or a named volume). All in-jail plumbing, shims, shebangs, and docs are
untouched because the path never changes; only the host connection is
severed.

**Precedent: this is already how macOS works.** run_cmd.py:1070 mounts a
named volume `yolo-mise-data` at the host path string (host tree has Mach-O
binaries that can't run in Linux jails), while Linux is the only branch
doing a true same-path host bind (run_cmd.py:1163). Adopting F = making
Linux behave like the already-shipping macOS path (a one-line mount-source
change), unifying the storage model across runtimes.

- \+ Kills the **entire host↔jail class**: host-written symlinks
  (`/home/matt/.cargo/bin`) never enter jail-land; jail-written ones
  (`/workspace/.cargo/bin`) never break the host. Host `mise install`
  can't be broken by jails again, ever.
- \+ Also insulates jails from the **host's mise version** — the skew guard /
  host-pinning problem evaporates for jails; jail-land runs one mise version per
  image (managed by `flake.lock` + the weekly `update-flake-lock` CI bump).
- \+ Keeps jail↔jail install sharing — no reinstall per jail boot.
- \+ Seeding is possible because the path string is unchanged: one-time
  `cp --reflink=auto`/rsync of the host dir into the jail store (pruning
  symlinks that point outside the data dir) and existing host-installed
  tools resolve in-jail immediately. Or start empty — first jail per tool
  installs once for all of jail-land.
- ‒ Does **not** fix jail↔jail workspace-path collisions (polyclav's
  `installs/rust/1.95.0 → /workspace/.cargo/bin` still dangles in other
  jails) — A or C is still needed to finish that smaller class.
- ‒ Host and jail-land each install their own copy of shared toolchains
  (disk duplication ~one extra copy; host no longer benefits from
  jail-side installs and vice versa).
- ‒ Nested jails: inner `yolo` derives the mise path from `MISE_DATA_DIR`
  (already exported in-jail, run_cmd.py:1183), so nesting keeps working —
  but verify with a nested-jail run.

#### F+ — once the host is out, drop the host-path mirroring too

The same-path mount exists *only* so host-written absolute paths resolve
in-jail — the mount comment (run_cmd.py:1153-1156) says exactly this and
records that a `/mise` alias was rejected for that reason (an empty `/mise`
mount point still exists in the image, flake.nix:507). With F, the host
never writes into jail-land, so the constraint collapses: mount the
jail-land store at a **fixed neutral path** (e.g. `/mise`), identical in
every jail on every machine.

What this buys beyond F:

- Jails become host-layout-independent — the mount table no longer embeds
  the host username/home, which is both the "ugly detail" and a real
  predictability win (identical jails on gauss, macOS, anywhere).
- The `YOLO_OUTER_MISE_PATH` plumbing (run_cmd.py:1895 → storage.py:154)
  exists purely to propagate the host path string into nested jails — with
  a constant path it can be deleted.
- Everything in-jail already derives from `MISE_DATA_DIR` env
  (`MISE_SHIMS` at entrypoint/__init__.py:67, shell venv hooks, node
  warmup) — no other code knows the host path.

What it costs beyond F:

- **No warm-seeding** from the host store (host-installed shebangs embed
  `/home/<user>/…`); jail-land starts cold, each tool installs once.
- **Workspace-resident venvs stop crossing the boundary.** A `.venv` in
  the shared workspace symlinks python into the mise store; under F with
  the path string kept, a host-created venv still resolves in-jail iff
  jail-land installed the same python version. Under F+, it never does —
  and worse, the jail's venv pre-create hook (shell.py:291) sees the
  existing `.venv` dir and skips, leaving a broken venv. F+ therefore
  needs a venv strategy — the shipped answer is
  [jail-state-separation-design.md](../design/jail-state-separation-design.md):
  per-side venvs via a shadow mount (`ws_state/venv` bound over
  `/workspace/.venv`). Note venv sharing was already half-broken today:
  console-script shebangs embed the venv's own absolute path, which
  differs host↔jail regardless of the mise mount.

### E. Upstream fix in mise

mise could store installs-dir symlinks relative to the data dir or tolerate
dangling entries during symlink rebuild (arguably `failed rm` on a
nonexistent path shouldn't be fatal). Worth an upstream issue regardless —
but not a fix yolo can wait on.

**Leaning (updated 2026-07-03):** **F first** — it's a one-line mount
change with shipping macOS precedent, kills the whole host↔jail class, and
solves the version-skew problem as a side effect. **F+ close behind** —
dropping the host-path mirror makes jails host-layout-independent and
deletes plumbing, at the cost of a venv-recreate story (see F+). Then A
(or C as a cheap prune) for the remaining jail↔jail workspace-path
collisions. Fix the `.mise.toml` trust gap independently — it's a
one-liner class bug.
Additionally, provisioning should **fail loudly** (or at least summarize at
the end of boot) when `mise install` errors, instead of scrolling past.

## Immediate remediation (host, no code changes)

**Resolved (2026-07-22) — host `mise install` works fine.** The state-separation
bundle (neutral `/mise` path + split store, shipped 2026-07-04) means jails no
longer write workspace-derived rust entries into the host's
`~/.local/share/mise`, so the dangling-symlink class cannot recur. The
maintainer confirms `mise` on the host is healthy; the one-time leftover cleanup
below is no longer outstanding and is kept only as the historical repro command.

```bash
# Historical — the one-time cleanup that unblocked the host before separation
# shipped. No longer needed; kept for the incident record.
rm ~/.local/share/mise/installs/rust/1.95.0   # unblocked `mise install` on host
```

## Open Questions

### Should `/workspace` become a symlink to the real host path (option A)?

This changes a documented invariant that agents and shims rely on. Everything
under `/workspace` keeps working via the symlink, but tools that canonicalize
paths would start seeing `/home/<user>/code/<project>`.

_Leaning:_ Yes — it matches the precedent set by the `MISE_DATA_DIR`
same-path mount and kills the whole bug class, not just rust.
_(Revised 2026-07-03: with the accepted separation bundle, A's host↔jail
benefit is obsolete and it doesn't cover the same-version jail↔jail
collision — see the "Residual issue" section in
[jail-state-separation-design.md](../design/jail-state-separation-design.md); new
leaning is boot-time prune (C) instead.)_

**Answer:**
> No (2026-07-03). Superseded by the accepted separation bundle; the
> jail↔jail residue is handled by boot-time prune per the "Residual issue"
> section of jail-state-separation-design.md.

### Does Apple Container support arbitrary same-path bind targets?

Option A assumes the runtime can mount at `/home/<host-user>/...`. Podman can;
verify the Apple Container backend before committing to A.

_Leaning:_ Unverified; needs a check on macOS.

**Answer:**
> Moot (2026-07-03). Option A is rejected and the accepted bundle mounts at
> the fixed path `/mise` — no arbitrary same-path targets needed on any
> runtime.

### Is `MISE_TRUST=1` (run_cmd.py:1194) actually a mise env var?

It doesn't appear in mise's documented env vars. If it's a no-op the trust
story silently rests on the three filename-gated `mise trust` calls, which is
how the `.mise.toml` gap shipped.

_Leaning:_ Probably a no-op; replace with `MISE_TRUSTED_CONFIG_PATHS=/workspace`
and un-gate the `mise trust` calls (run without a path argument).

**Answer:**
> Confirmed a no-op (2026-07-03): `mise settings ls --all` has no `trust`
> setting, only `trusted_config_paths`. Replace with
> `MISE_TRUSTED_CONFIG_PATHS=/workspace` and fix the filename-gated trust
> calls (three sites, see above).

### Under F+, how do workspace venvs get recreated per side?

A shared-workspace `.venv` can only point at one side's interpreter. The
pre-create hook currently skips when the dir exists, so an existing host-side
venv would sit broken in-jail. Shipped answer:
[jail-state-separation-design.md](../design/jail-state-separation-design.md).

_Leaning (revised 2026-07-03):_ Shadow mount — bind `ws_state/venv` over
`/workspace/.venv` so each side sees its own venv at the idiomatic path;
no project config, and the pre-create hook populates the jail side
naturally. (The earlier `.mise.local.toml` idea is unworkable: the file
would live in the shared workspace and leak to the host; also nothing in
`src/` generates one today — option D's claim above was wrong.)

**Answer:**
> _(empty — fill in when decided)_

### Should provisioning failures abort jail boot?

Today a failed `mise install` during provisioning scrolls past and the jail
comes up half-provisioned (this is how the songtv trust failure went
unnoticed).

_Leaning:_ Don't abort (agents can often self-serve), but print a red
end-of-boot summary line so it can't be missed.

**Answer:**
> Decided 2026-07-03 — three parts:
> 1. **Pause with a prompt.** A summary line isn't enough because it
>    scrolls away the moment the agent starts. On provisioning errors,
>    boot pauses with an interactive continue/abort prompt — the user's
>    choice. (Implementation nuance: headless/CI runs need a bypass, e.g.
>    a config flag or env var that picks "continue" and relies on the
>    breadcrumbs below.)
> 2. **Persist the startup log.** Provisioning output is written to a file
>    (e.g. under the jail's `.yolo/` state) instead of existing only in
>    scrollback.
> 3. **Breadcrumb for agents.** The generated agents file (agents_md.py)
>    gets the log path written into it, plus a prominent error indicator
>    when provisioning failed — so an agent reading its briefing may
>    notice and self-serve the fix. Unproven that agents will act on it,
>    but cheap and worth trying.

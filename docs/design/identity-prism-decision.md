# git / jj identity: the prism port decision (and the jj question)

**Status:** OPEN — decision needed. Everything else in the config-composition
cutover (`docs/design/config-migration-to-prism.md`) has landed; git/jj identity
is the one deliberately-deferred surface. This doc lays out why it was deferred,
what porting it actually costs, and a separate, cheaper question that surfaced
while scoping it: **whether to keep jj support at all.**

Written 2026-07-23.

---

## 1. How identity works today (two halves, two machines)

Identity is a host→jail forward, not a mount. It has a **collector** on the host
and a **setter** in the jail.

**Host collector** — `internal/cli/run/identity.go` `collectIdentityEnv()`:
runs, on the host, four read commands and forwards each non-empty result as a
`-e` env var into the container:

| env var forwarded | read from (host) |
|---|---|
| `YOLO_GIT_NAME`  | `git config --get user.name` |
| `YOLO_GIT_EMAIL` | `git config --get user.email` |
| `YOLO_JJ_NAME`   | `jj config get user.name` (quotes stripped) |
| `YOLO_JJ_EMAIL`  | `jj config get user.email` (quotes stripped) |

`YOLO_GLOBAL_GITIGNORE` is set separately by the run assembler
(`assemble_parts.go`) to a fixed in-jail path (`~/.config/git/ignore`).

**Jail setter** — `internal/entrypoint/identity.go`, called from `boot.go` (and
`darwin.go` for the macOS backend):

- `configureGit(e)` — if `git` is on PATH: for each of `YOLO_GIT_NAME` /
  `YOLO_GIT_EMAIL` (when non-empty) run `git config --global user.name/email`;
  if `YOLO_GLOBAL_GITIGNORE` points at a real regular file, set
  `core.excludesFile`.
- `configureJJ(e)` — if `jj` is on PATH: for each of `YOLO_JJ_NAME` /
  `YOLO_JJ_EMAIL` (when non-empty) run `jj config set --user user.name/email`.

The macOS-user (Seatbelt) backend does its own thing:
`internal/macosuser/orchestrator.go` forwards only `YOLO_GIT_*` (never jj), and
`runplan.go` re-derives identity from any `YOLO_GIT*`/`YOLO_JJ*` key in the
sandbox env.

---

## 2. The core problem the prism port is meant to fix

**Both setters are add-only. There is no unset path.** Each key is written to the
persistent `~/.gitconfig` / `~/.jjconfig` (which live in the persistent jail home)
**only when its env var is present and non-empty**, and is never removed.

So if a host later *clears* `user.email` (or renames, or drops the global
gitignore), the jail keeps writing nothing — and the **previously-written value
stays in `~/.gitconfig` forever.** There is no snapshot to roll back from (unlike
Claude/pi settings) and no managed-MCP sidecar (unlike gemini/codex/opencode).
That is exactly why the migration doc rates identity **HIGH** stale-risk
alongside mise: its output accretes and never leaves.

The prism fixes this class of bug structurally — it regenerates from the current
env every boot, so a removed env var means the key is simply *absent from the
render* and (with a proper codec) removed from the file. That is the whole point
of porting it.

---

## 3. Why the port is the hard one — the data-loss trap

Every surface ported so far is **yolo-owned in full**: yolo writes the entire
`config.json` / `settings.json` / `mcp-config.json`, so the codec can replace the
whole file. `~/.gitconfig` is **NOT** yolo-owned. It routinely carries
user-authored content yolo has no business touching:

```ini
[user]
    name = Ada Lovelace
    email = ada@example.com          # yolo owns these two lines
[alias]
    st = status                       # user's — must survive
[pull]
    rebase = true                     # user's — must survive
[credential "https://github.com"]
    helper = !gh auth git-credential  # user's — must survive
```

So this surface cannot use the standard "replace the file" render (§3.2 of the
migration doc). It needs a **scoped, key-level reconciler**: a git-config codec
that

1. parses the existing INI **losslessly** (sections, subsections like
   `[credential "url"]`, comments, blank lines, ordering, repeated keys,
   multi-valued keys, includes),
2. asserts *only* yolo's enumerated owned keys (`user.name`, `user.email`,
   `core.excludesFile`),
3. **removes** an owned key when its env var is gone this boot,
4. writes everything else back **byte-for-byte untouched**.

A naïve whole-file overwrite here is a **data-loss regression** — it would erase
the user's aliases, rebase config, and credential helpers. The lossless-INI
round-trip is real work and the failure mode is severe, which is why this surface
was consciously held back while the safe surfaces landed. The migration doc (§4.2)
explicitly permits keeping identity **imperative** (the current `git config`
subprocess approach) as the fallback if the codec isn't worth it.

### 3.1 A cheaper middle path

The current setters already shell out to `git config`, which *is* a scoped
key-level reconciler — git itself edits only the named key and preserves the rest.
The only thing missing is the **unset path**. A minimal fix that captures ~90% of
the prism's benefit without a new codec:

> Track the set of identity keys yolo wrote last boot (a tiny sidecar, or just
> "yolo's owned key list"), and on each boot run `git config --global --unset
> <key>` for any owned key whose env var is now absent, before setting the
> present ones.

This keeps `git config` as the safe editor (no INI parser to get wrong), adds the
missing removal semantics, and sidesteps the lossless-codec project entirely. It
is *not* the full prism model (no overlay/last_render, no `yolo config render`
visibility), but it closes the actual bug. Worth weighing against the full port.

---

## 4. The jj question — drop it?

While scoping the port, a decisive fact surfaced:

**jj is not in the jail image at all.**

- It is in neither `corePackages` nor `fullPackages` in `flake.nix` (grep for
  `jujutsu`/`jj` in `flake.nix` → nothing).
- `command -v jj` inside a jail → not found.
- Therefore `configureJJ`'s `exec.LookPath("jj")` guard **always fails**, so
  `configureJJ` has been a **permanent no-op in every jail, forever.** The
  `YOLO_JJ_NAME` / `YOLO_JJ_EMAIL` env vars are collected on the host, forwarded
  into the container, and then **used by nothing.**

And it is **falsely advertised** in two user-facing places:

- `internal/cli/config_ref.txt:674` and `internal/cli/briefing.txt:49` both list
  `jj` in "CLI tools" available in the jail.
- `config_ref.txt:667` / `briefing.txt:21` both say "Git/jj identity … is injected
  from the host" — true for git, a no-op for jj.

So jj support today is: **dead code + a false promise.** Dropping it is not a
behavior change — nothing in any jail uses it — it is removing code that never
runs and fixing docs that lie.

### What "drop jj" concretely means

- Delete `configureJJ` and its two call sites (`boot.go`, `darwin.go`).
- Drop the two `YOLO_JJ_*` lines from `collectIdentityEnv` (host stops shelling
  out to `jj config get`, which also removes a spurious `jj` host dependency
  probe).
- Drop the `YOLO_JJ` handling in `macosuser/runplan.go`.
- Fix `config_ref.txt` + `briefing.txt`: remove `jj` from the CLI-tools list and
  reword the identity line to "Git identity."
- Net: less code, one fewer host probe, honest docs. The git port (§3) proceeds
  unaffected.

### The only reason NOT to drop it

If jj is intended to be **added to the image** later (it's a reasonable VCS to
support), then the plumbing is already there and dropping it just means re-adding
it. But shipping-jj is not on any current roadmap, and "wire it back when we
actually package jj" is cheap. Given "I don't really care about it anymore," the
plumbing is all cost and no benefit today.

---

## 5. The decisions to make

1. **jj:** drop it (recommended — it's dead code + false docs), or keep the
   plumbing dormant against a future where jj is packaged?
2. **git identity port depth:**
   - (a) **Full prism port** — new lossless git-config INI codec, scoped
     owned-key reconcile, `yolo config render` visibility. Most work, highest
     data-loss risk if the INI round-trip is imperfect, but uniform with every
     other surface.
   - (b) **Minimal unset fix** (§3.1) — keep `git config` as the editor, add the
     missing `--unset` path for departed keys. Closes the actual bug, no codec,
     no data-loss surface. Not on the prism, so no render visibility.
   - (c) **Leave imperative as-is** — accept the add-only staleness (migration
     doc §4.2 explicitly allows this). Zero work, bug remains.

**Recommendation:** drop jj (1), and do the **minimal unset fix (2b)** for git —
it removes the real bug (stale identity keys accreting) at a fraction of the cost
and risk of the full INI codec, and identity is a poor fit for the
replace-the-file prism model anyway. Revisit the full codec (2a) only if we later
want `yolo config render` to preview `~/.gitconfig` or find other yolo-owned git
keys worth managing.

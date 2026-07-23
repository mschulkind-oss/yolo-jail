# git identity: the prism port decision

**Status:** OPEN — decision needed. Everything else in the config-composition
cutover (`docs/design/config-migration-to-prism.md`) has landed; git identity is
the one deliberately-deferred surface. This doc lays out why it was deferred and
what porting it actually costs.

> **jj was removed (2026-07-23).** yolo previously carried plumbing to forward a
> host `jj` (Jujutsu) identity into the jail, but jj was never in the image, so
> that code was a permanent no-op. It has been ripped out entirely (host
> collector, jail setter, macOS backend, tests, and the docs that advertised it).
> This doc is now git-only.

Written 2026-07-23.

---

## 1. How git identity works today (two halves, two machines)

Identity is a host→jail forward, not a mount. It has a **collector** on the host
and a **setter** in the jail.

**Host collector** — `internal/cli/run/identity.go` `collectIdentityEnv()`:
runs, on the host, two read commands and forwards each non-empty result as a
`-e` env var into the container:

| env var forwarded | read from (host) |
|---|---|
| `YOLO_GIT_NAME`  | `git config --get user.name` |
| `YOLO_GIT_EMAIL` | `git config --get user.email` |

`YOLO_GLOBAL_GITIGNORE` is set separately by the run assembler
(`assemble_parts.go`) to a fixed in-jail path (`~/.config/git/ignore`).

**Jail setter** — `internal/entrypoint/identity.go`, called from `boot.go` (and
`darwin.go` for the macOS backend):

- `configureGit(e)` — if `git` is on PATH: for each of `YOLO_GIT_NAME` /
  `YOLO_GIT_EMAIL` (when non-empty) run `git config --global user.name/email`;
  if `YOLO_GLOBAL_GITIGNORE` points at a real regular file, set
  `core.excludesFile`.

The macOS-user (Seatbelt) backend does its own thing:
`internal/macosuser/orchestrator.go` forwards `YOLO_GIT_*`, and `runplan.go`
re-derives identity from any `YOLO_GIT*` key in the sandbox env.

---

## 2. The core problem the prism port is meant to fix

**The setter is add-only. There is no unset path.** Each key is written to the
persistent `~/.gitconfig` (which lives in the persistent jail home) **only when
its env var is present and non-empty**, and is never removed.

So if a host later *clears* `user.email` (or renames, or drops the global
gitignore), the jail keeps writing nothing — and the **previously-written value
stays in `~/.gitconfig` forever.** There is no snapshot to roll back from (unlike
Claude/pi settings) and no managed-MCP sidecar (unlike gemini/codex/opencode).
That is exactly why the migration doc rates git identity **HIGH** stale-risk
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

The current setter already shells out to `git config`, which *is* a scoped
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

## 4. The decision to make

**git port depth:**

- (a) **Full prism port** — new lossless git-config INI codec, scoped
  owned-key reconcile, `yolo config render` visibility. Most work, highest
  data-loss risk if the INI round-trip is imperfect, but uniform with every
  other surface.
- (b) **Minimal unset fix** (§3.1) — keep `git config` as the editor, add the
  missing `--unset` path for departed keys. Closes the actual bug, no codec,
  no data-loss surface. Not on the prism, so no render visibility.
- (c) **Leave imperative as-is** — accept the add-only staleness (migration
  doc §4.2 explicitly allows this). Zero work, bug remains.

**Recommendation:** do the **minimal unset fix (b)** — it removes the real bug
(stale identity keys accreting) at a fraction of the cost and risk of the full
INI codec, and identity is a poor fit for the replace-the-file prism model
anyway. Revisit the full codec (a) only if we later want `yolo config render` to
preview `~/.gitconfig` or find other yolo-owned git keys worth managing.

# git identity: the propagation decision

**Status:** OPEN — decision needed. Everything else in the config-composition
cutover (`docs/design/config-migration-to-prism.md`) has landed; git identity is
the one deliberately-deferred surface. This doc frames the decision as two
separable questions — an **allowlist policy** (what git settings should cross
into the jail at all) and a **mechanism** (how to apply them without going
stale) — and gives a recommendation for each.

> **jj was removed (2026-07-23).** yolo previously carried plumbing to forward a
> host `jj` (Jujutsu) identity into the jail, but jj was never in the image, so
> that code was a permanent no-op. It has been ripped out entirely (host
> collector, jail setter, macOS backend, tests, and the docs that advertised it).
> This doc is now git-only.

Written 2026-07-23. Recommendation revised 2026-07-23 (allowlist framing).

---

## 1. How it works today: a two-key allowlist

Identity is a host→jail **forward**, not a mount of the host's `~/.gitconfig`.
It has a **collector** on the host and a **setter** in the jail, and it forwards
an explicit, enumerated set of two keys — nothing else.

**Host collector** — `internal/cli/run/identity.go` `collectIdentityEnv()`:
runs, on the host, two read commands and forwards each non-empty result as a
`-e` env var into the container:

| env var forwarded | read from (host) |
|---|---|
| `YOLO_GIT_NAME`  | `git config --get user.name` |
| `YOLO_GIT_EMAIL` | `git config --get user.email` |

`YOLO_GLOBAL_GITIGNORE` is set separately by the run assembler
(`assemble_parts.go`) to a **fixed in-jail path** (`~/.config/git/ignore`) — it
is not read from host config, so it is not part of the identity allowlist.

**Jail setter** — `internal/entrypoint/identity.go` `configureGit(e)`, called
from `boot.go` (and `darwin.go` for the macOS backend): if `git` is on PATH,
for each of `YOLO_GIT_NAME` / `YOLO_GIT_EMAIL` that is non-empty, run
`git config --global user.name/email`; if `YOLO_GLOBAL_GITIGNORE` points at a
real regular file, set `core.excludesFile`.

The macOS-user (Seatbelt) backend does its own thing:
`internal/macosuser/orchestrator.go` forwards `YOLO_GIT_*`, and `runplan.go`
re-derives identity from any `YOLO_GIT*` key in the sandbox env.

---

## 2. The security & sanity posture is an allowlist — and that's the point

The design forwards **only the two keys it names.** Everything else in the
host's git config simply never crosses. This is not incidental; it is exactly
the property we want, and it satisfies both of the stated goals for free:

- **No credentials cross.** `credential.helper`, `user.signingkey`,
  `url.*.insteadOf` rewrites, OAuth/PAT helpers — none are named, so none are
  forwarded. The jail gets identity without ever getting the means to
  authenticate as the user. (Credentials that *do* need to work in the jail
  arrive through their own explicit channels — a workspace deploy key, a token
  in `.env` — never by inheriting host git config.)
- **No UI / no agent-confusing settings cross.** `core.pager`, `pager.*`,
  `core.editor`, `color.ui`, pretty-print/format defaults — none are named. The
  jail deliberately runs its own env hygiene for agents (`PAGER=cat`,
  `GIT_PAGER=cat`, `EDITOR=cat`; see AGENTS.md "Env hygiene"), and inheriting the
  host's pager/editor preferences would fight that and confuse agents. An
  allowlist means we never have to *strip* these — they were never in scope.

**Corollary — why we must NOT inherit `~/.gitconfig` wholesale.** The tempting
shortcut of pointing `GIT_CONFIG_GLOBAL` at (or `[include]`-ing) the host's real
`~/.gitconfig` breaks the allowlist and is actively dangerous:

- It drags in `core.pager` / `core.editor` (the exact agent-confusion we avoid).
- It drags in credential helpers (the exact leak we avoid).
- **It can break committing entirely.** Verified 2026-07-23: with
  `commit.gpgsign=true` and no gpg/key material in the jail, `git commit`
  fails hard — `error: gpg failed to sign the data` / `fatal: failed to write
  commit object`. A signing host would make every in-jail commit fail. This
  alone rules the wholesale-inherit approach out.

So the allowlist stays. The only questions are *what's on it* (§3) and *how it's
applied* (§5).

---

## 3. What should be on the allowlist? (the "are there other settings?" question)

Candidates, judged against the two rules — **must be author identity /
harmless workflow preference, must not be a credential, must not be UI, must not
point at a host path that won't exist in the jail:**

| git setting | forward? | why |
|---|---|---|
| `user.name` | ✅ (current) | author identity — the whole point |
| `user.email` | ✅ (current) | author identity — the whole point |
| `core.excludesFile` | ✅ (special) | already set to a **fixed in-jail path**, not host-derived — correct as-is |
| `user.signingkey` | ❌ | credential-adjacent; useless without key material in jail |
| `commit.gpgsign` / `tag.gpgsign` | ❌ | **breaks every commit** if forwarded without a key (proven, §2) |
| `credential.helper` | ❌ | credential leak; jail auth is a separate explicit channel |
| `url.*.insteadOf` | ❌ | can silently reroute fetches through host credential paths |
| `core.pager`, `pager.*`, `color.ui` | ❌ | UI — fights the jail's `PAGER=cat` agent hygiene |
| `core.editor` | ❌ | jail forces `EDITOR=cat` deliberately (stops `git commit` hanging) |
| `commit.template`, `core.hooksPath` | ❌ | point at host file paths that don't exist in the jail |
| `init.defaultBranch` | 🟡 maybe | harmless preference; makes in-jail `git init` match host (`main` vs `master`). Marginal value, zero risk. |
| `pull.rebase`, `rebase.autosquash`, `merge.conflictstyle` | 🟡 maybe | harmless workflow prefs; no credential/UI/path risk. Agents rarely depend on them. |

**Read of the table:** everything genuinely *useful* to forward is either a
credential (exclude), UI (exclude — the whole reason for the jail's env
hygiene), or a host-path reference that won't resolve in the jail (exclude). The
only clean *additions* are cosmetic preferences (`init.defaultBranch` and a few
workflow toggles), and their value is marginal. **The honest conclusion is that
the allowlist is essentially just `user.name` + `user.email`, and that is a
feature, not a gap.** If we ever add one, `init.defaultBranch` is the only
low-regret candidate.

---

## 4. The one real bug: the setter is add-only (staleness)

`configureGit` writes each key to the persistent `~/.gitconfig` **only when its
env var is present and non-empty**, and **never removes it.** So if a host later
*clears* `user.email`, the jail writes nothing new — and the previously-written
value **stays in `~/.gitconfig` forever.** There is no snapshot to roll back
from and no managed sidecar; the value accretes and never leaves. That is why
the migration doc rates git identity HIGH stale-risk.

**Severity, honestly assessed.** For `user.name`/`user.email` specifically this
bug is *low-frequency*: a **changed** value still works fine (the setter
overwrites — a rename to a new email just re-sets it). The bug only bites when a
key is **removed entirely** on the host and the user expects the jail to forget
it — an uncommon event. So this is a real correctness gap, but not an urgent
one. That matters for the cost/benefit of the options below.

---

## 5. Mechanism options (all preserve the §3 allowlist)

- **(a) Leave imperative as-is.** Keep `git config --global`, accept the
  add-only staleness (§4). Zero work. The migration doc §4.2 explicitly permits
  this. Bug remains (low severity, §4).

- **(b) Imperative + unset (the minimal fix).** Keep `git config --global` as
  the editor — git itself is already a safe scoped key-level reconciler, editing
  only the named key and leaving the rest of `~/.gitconfig` untouched. Add the
  one missing piece: track yolo's owned key list, and each boot run
  `git config --global --unset <key>` for any owned key whose env var is now
  absent, before setting the present ones. Closes the bug, **no new file, no
  codec, no data-loss surface**, and `~/.gitconfig` stays the effective global
  config so a user's own in-jail `git config` edits still work. Not on the prism
  → no `yolo config render` visibility.

- **(c) Full prism port via a yolo-owned file.** Set
  `GIT_CONFIG_GLOBAL=~/.config/yolo/gitconfig` (git ≥2.32; image has **2.54**,
  verified) pointing at a file yolo owns **completely** — containing *only* the
  allowlisted keys, **with NO `[include]` of the user's `~/.gitconfig`** (the
  include is the dangerous variant ruled out in §2). Because yolo owns the whole
  file, the render is a trivial replace-the-file surface — an ordinary prism
  member, no lossless-INI codec needed — and staleness vanishes for free
  (regenerated from the current env each boot). Cost: `~/.gitconfig` is no longer
  the effective global file, so a user's manual in-jail `git config --global`
  edits would write to a file git no longer reads — a mild surprise that needs a
  note. Gains `yolo config render` visibility.

Note the earlier draft's headline idea — `GIT_CONFIG_GLOBAL` **with** an
`[include]` of the host `~/.gitconfig` — is **rejected**: it breaks the
allowlist and can break committing (§2). Option (c) keeps the `GIT_CONFIG_GLOBAL`
relocation but drops the include, which is what makes it safe.

---

## 6. Recommendation

**Policy (§3): keep the allowlist at `user.name` + `user.email`.** It already
satisfies "maintain author identity, pass no credentials, don't confuse agents
with UI settings." Optionally add `init.defaultBranch` — the one low-regret
extra — if matching the host's default branch in the jail is worth it. Do not
add anything else.

**Mechanism (§5): do (b), imperative + unset.** It fixes the actual bug at the
lowest cost and risk, preserves the exact allowlist semantics, keeps
`~/.gitconfig` as the real global file (no in-jail-edit surprise), and needs no
codec. Given the bug is low-severity (§4), (b)'s small footprint is a better fit
than (c)'s file relocation + render machinery.

Reach for **(c)** only if we later decide `yolo config render` *must* preview
git identity like every other surface — then the yolo-owned-file (no-include)
form is the safe way to get there. And if even the unset path feels like
over-engineering for a rare failure mode, **(a)** is a defensible do-nothing:
the allowlist is correct today; only stale removal is imperfect.

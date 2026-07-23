# git identity: the propagation decision

**Status:** IMPLEMENTED (2026-07-23, commit `c250c72`). git identity was the one
deliberately-deferred surface of the config-composition cutover
(`docs/design/config-migration-to-prism.md`); it now lands as a **host-composed,
read-only-mounted** config file on the container backends. This doc frames the
decision as two separable questions — an **allowlist policy** (what git settings
should cross into the jail at all) and a **mechanism** (how to apply them without
going stale) — records the chosen answer to each, and keeps the rejected options
for the rationale.

> **jj was removed (2026-07-23).** yolo previously carried plumbing to forward a
> host `jj` (Jujutsu) identity into the jail, but jj was never in the image, so
> that code was a permanent no-op. It has been ripped out entirely (host
> collector, jail setter, macOS backend, tests, and the docs that advertised it).
> This doc is now git-only.

Written 2026-07-23. Recommendation revised 2026-07-23 (allowlist framing).
Implemented 2026-07-23 (host-compose + `:ro` mount; the (c′) variant in §5).

---

## 1. How it works now: a host-composed, read-only config

Identity is a host→jail **forward of an enumerated two-key allowlist**
(`user.name` + `user.email`) — never a mount of the host's `~/.gitconfig`. On the
**container backends** (podman + Apple Container) the host composes a small
`gitconfig` **fresh every run** from those two keys and delivers it read-only; on
the **macOS-user (Seatbelt)** backend, which has no mount namespace, the same two
keys are still forwarded as env vars and replayed imperatively.

**Container path** — `internal/cli/run/assemble_parts.go`
`gitIdentityMountArgs()`:

1. reads `user.name` / `user.email` from the host git config (`git config
   --get`, so a repo-local value for the host CWD wins, matching the old
   collector);
2. renders a minimal INI (`composeGitconfig`) containing *only* `[user]`
   (name/email, each omitted when empty) and, when a global gitignore resolves, a
   `[core] excludesFile` pointing at the **in-jail** ignore path;
3. delivers it so git reads it at `~/.config/git/config`:
   - **podman:** writes `<wsState>/yolo-gitconfig` and bind-mounts it
     `-v …:/home/agent/.config/git/config:ro` (kernel-enforced read-only, even
     against the jail's root);
   - **Apple Container** (no nested `:ro` bind): materializes the composed file
     into `<wsState>/.config/git/config`, which AC mounts as part of the whole
     `wsState → /home/agent` bind.

The global gitignore is carried the same way (a `:ro` bind for podman,
`acMaterialize` for AC) and `core.excludesFile` points at its in-jail path. With
**no identity and no gitignore**, nothing is emitted — a bare, identity-less jail,
which keeps the identity-less golden argv byte-identical.

**macOS-user path (unchanged)** — `internal/macosuser/orchestrator.go`
`MacosSandboxEnv` derives `YOLO_GIT_NAME`/`YOLO_GIT_EMAIL` from
`deps.GitConfig(...)` and forwards them; `internal/entrypoint/identity.go`
`configureGit(e)` (called from `darwin.go`) replays them via `git config
--global`. Seatbelt has no mount namespace, so it cannot bind a `:ro` file — it
keeps the imperative path, exactly the way the global gitignore already diverges
there. This mirrors the container/macos-user split used for every other
mount-vs-imperative surface.

The old container-path forward (host `collectIdentityEnv()` → `-e YOLO_GIT_*` →
in-jail `configureGit`'s `git config --global` replay) is **gone** on the
container backends; `configureGit` remains only for the macOS-user path.

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
`~/.gitconfig`, or bind-mounting it directly, breaks the allowlist and is
actively dangerous:

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

## 3. What is on the allowlist? (the "are there other settings?" question)

Candidates, judged against the two rules — **must be author identity /
harmless workflow preference, must not be a credential, must not be UI, must not
point at a host path that won't exist in the jail:**

| git setting | forward? | why |
|---|---|---|
| `user.name` | ✅ (current) | author identity — the whole point |
| `user.email` | ✅ (current) | author identity — the whole point |
| `core.excludesFile` | ✅ (special) | composed to point at the **in-jail** gitignore path, not host-derived — correct as-is |
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
feature, not a gap.** The implementation ships exactly those two (plus the
in-jail `core.excludesFile`); `init.defaultBranch` was explicitly declined.

---

## 4. The bug the port fixes: the old setter was add-only (staleness)

The old `configureGit` wrote each key to the persistent `~/.gitconfig` **only
when its env var was present and non-empty**, and **never removed it.** So if a
host later *cleared* `user.email`, the jail wrote nothing new — and the
previously-written value **stayed in `~/.gitconfig` forever.** There was no
snapshot to roll back from and no managed sidecar; the value accreted and never
left. That is why the migration doc rated git identity HIGH stale-risk.

**How the port fixes it.** The whole config file is **regenerated from the
current host identity every run** and mounted read-only, so there is no
persistent file to accrete into: a cleared or changed host key is reflected on
the very next boot (a cleared `user.email` simply produces a file with no email
line). Verified live 2026-07-23 — clearing `user.email` on the host makes it
vanish from the jail's composed config while `user.name` survives
(`TestGitIdentityMountStaleClearedEmail` pins the regression). This was the
concrete correctness win that motivated doing the port now rather than the
minimal (b) unset-patch.

---

## 5. Mechanism options (all preserve the §3 allowlist)

- **(a) Leave imperative as-is.** Keep `git config --global`, accept the
  add-only staleness (§4). Zero work. Bug remains. *Rejected — leaves the bug.*

- **(b) Imperative + unset (the minimal fix).** Keep `git config --global` as
  the editor, track yolo's owned key list, and each boot `--unset` any owned key
  whose env var is now absent before setting the present ones. Closes the bug,
  no new file, keeps `~/.gitconfig` writable in-jail. Not on the prism → no
  `yolo config render` visibility. *Rejected — see (c′).*

- **(c) Full prism port via a yolo-owned file behind `GIT_CONFIG_GLOBAL`.** Set
  `GIT_CONFIG_GLOBAL=~/.config/yolo/gitconfig` (git ≥2.32; image has 2.54)
  pointing at a file yolo owns completely — allowlisted keys only, **no
  `[include]`** of the user's `~/.gitconfig`. Staleness vanishes (regenerated
  each boot); gains render visibility. *Superseded by (c′), which needs no env
  relocation.*

- **(c′) Host-compose + `:ro` bind at the default path (IMPLEMENTED).** The
  refinement actually shipped: instead of relocating `GIT_CONFIG_GLOBAL`, the
  **host** composes the same yolo-owned, allowlist-only file each run and
  bind-mounts it **read-only at git's default global path**
  (`~/.config/git/config`). This mirrors the existing global-gitignore mechanism
  exactly (same `:ro`-bind-for-podman / `acMaterialize`-for-Apple-Container
  split), so it reuses machinery already trusted in production rather than
  introducing a new env var. Staleness vanishes for the same reason as (c)
  (fresh composition each boot). Because the file is `:ro`, in-jail
  `git config --global` edits fail — accepted deliberately: every other `:ro`
  surface behaves the same way, and the file is regenerated each run regardless,
  so a persisted edit would be a lie. macOS-user can't bind a `:ro` file, so it
  keeps (b)-style imperative forwarding (§1).

The earlier draft's headline idea — `GIT_CONFIG_GLOBAL` **with** an `[include]`
of the host `~/.gitconfig` — is **rejected**: it breaks the allowlist and can
break committing (§2).

---

## 6. Decision (as implemented)

**Policy (§3): the allowlist is `user.name` + `user.email`** (plus the in-jail
`core.excludesFile` for the gitignore). It satisfies "maintain author identity,
pass no credentials, don't confuse agents with UI settings" with nothing else in
scope. `init.defaultBranch` was considered and declined.

**Mechanism (§5): (c′) — host-compose + `:ro` mount** on the container backends,
imperative forward retained on macOS-user. This fixes the staleness bug by
construction (fresh composition each run), reuses the gitignore mount machinery,
and keeps the "no credentials, no UI" allowlist properties. Trade-off accepted:
the mounted file is read-only, so in-jail `git config --global` edits do not
persist — consistent with every other `:ro` surface, and moot since the file is
regenerated each boot anyway.

Implementation: `internal/cli/run/assemble_parts.go` (`gitIdentityMountArgs`,
`composeGitconfig`, `gitConfigValue`, `hostGitConfigGet`), wired at
`assemble.go`; the container-path `collectIdentityEnv` + entrypoint
`configureGit` call were removed (`configureGit` stays for macOS-user). Tests in
`internal/cli/run/gitidentity_test.go`.

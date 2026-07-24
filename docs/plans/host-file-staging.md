# Host-file staging — a user-widenable set of host files copied into the jail

**Status:** design, not yet built (2026-07-24).
**Supersedes:** the `## 10` retirement decisions in
[agent-settings-composition.md](agent-settings-composition.md) — specifically
**D4** ("hard-error, as if it never existed"). This plan *reopens* the
user-scope knob that D4 removed, while keeping everything else D1–D3 landed
(commit `a84b11c`).

## The decision, in one paragraph

There are **two** ways a host file reaches the jail, and conflating them is what
made `host_*_files` a mess. yolo **composes** a tiny, fixed set of files
(`settings.json` today) — decode, deep-merge layers, re-assert a managed block,
re-encode — and that set is yolo-shipped Go, non-widenable, because composition
needs a yolo-authored codec + managed policy that only exists in code. Every
*other* host file the user wants in the jail is **raw**: copied verbatim, yolo
manages nothing. The raw set has a sensible **baked default**, and the user can
**add to it** through a single generic, **user-scope-only** config key
(`host_files`) — files *or* directories, copied into the jail home. A
**workspace** config can never widen it (the credential boundary; exact
`cache_relocations` precedent). No codec, no per-agent scoping, no management: it
is "also bring these host paths into the jail home, as-is."

## Background: where we are, and why this reopens

Commit `a84b11c` retired `host_claude_files` / `host_pi_files` per the §10.4
decisions. It did three good things and one wrong thing:

| Landed | Verdict |
|---|---|
| **D1/D2** — moved the *composed* set to a fixed, yolo-declared registry (`internal/agents.AgentSpec.HostFiles`: claude ⇒ `.claude/settings.json`, pi ⇒ `.pi/agent/settings.json`) | **Keep.** Composition genuinely can't be user-authored — there is no codec or managed policy for an ad-hoc file. |
| **D3** — deleted the bespoke per-agent pathways (`appendSettingsScripts`, the claude/pi `syncHost*Files` twins) | **Keep.** The special-casing is gone for good. |
| **D4** — dropped both keys from `knownTopLevelConfigKeys` so any occurrence **hard-errors** | **Reverse (partially).** This also killed the *legitimate* user-scope ability to bring raw files (pi's `models.json`, `themes/*.json`) into the jail. That was the baby in the bathwater. |

The error in D4 was treating *"the set of host files that cross into the jail is
a credential boundary"* as *"no config may ever widen it."* The real boundary is
narrower: a **workspace** config may not widen it (it travels with the repo and
is agent-editable). A **user** config — the human's own machine — is exactly
where widening *should* happen. That's the whole point of the mechanism.

## Two mechanisms on one axis: composed vs. raw

The axis is simply **does yolo reshape the file?**

|  | **Composed** | **Raw (this plan)** |
|---|---|---|
| What yolo does | decode → merge `defaults < host < … < managed` → Lua transform → re-encode | copies bytes verbatim (files) or trees (dirs) |
| Requires | a codec (`json`/`toml`/…) **and** a yolo-authored managed policy | nothing — opaque bytes |
| Fits | `settings.json` (yolo enforces `permissions`, strips MCP servers, forces theme) | `models.json`, `themes/`, a helper script — anything yolo has no opinion on |
| Declared in | `internal/agents.AgentSpec.HostFiles` (Go, fixed) + the prism manifest | baked default **+** user `host_files` key |
| User-extensible? | **No — impossible.** No codec/managed policy for an arbitrary file, so "compose this" is undefined. | **Yes — user scope only.** This is the knob. |
| In-jail mutability | **read-only** (bind-mount): the managed block must not be strippable | writable copy is fine — no managed content to protect; host original is never exposed |
| Delivery | bind-mount `:ro` at `/ctx/host-<agent>/`, entrypoint reads + composes | **copy** into the jail home (portable across all backends) |

The last two rows carry a real design consequence, below.

## The boundary: user-scope widens, workspace-scope cannot

*Which* host files leave the host is a **credential boundary**: a config that can
add entries can forward `~/.ssh/id_ed25519`, `~/.aws/credentials`, or any secret
into the jail. So the source of the key matters, and there are exactly three
places a key can come from — two of them jail-writable:

| Source | Jail-writable? | Verdict for `host_files` |
|---|---|---|
| Workspace `yolo-jail{,.local}.jsonc` | **Yes** — `/workspace` is bind-mounted rw | **Rejected** (hard error) |
| `<workspace>/.yolo/config-snapshot.json` | **Yes** — same mount; read verbatim in-jail by `LoadConfig` | Never consulted for this key |
| Host `~/.config/yolo-jail/config.jsonc` (+ `include_if_found`) | **No** — mounted `:ro`, host-owned | **The only source** |

This is **not new machinery** — it is byte-for-byte the `cache_relocations`
model. `LoadCacheRelocations` (`internal/config/relocations.go`) reads
`paths.UserConfigPath()` **directly**, never the merged/workspace/snapshot
config, so workspace scope is *inexpressible by construction*;
`validateCacheRelocations` then hard-errors if the key nonetheless appears at
workspace scope — *"a workspace config is agent-editable, so it cannot grant
read-write host mounts"* — as **defense-in-depth against a silent no-op**, not
as the boundary itself. `host_files` gets the same two-part treatment: read only
from the user config, and hard-error on any workspace occurrence.

> **User scope = the human is trusted.** Nothing blocks a user from listing
> `~/.ssh` in their *own* `host_files` — that is their call on their own
> machine, and a blocklist is unenforceable anyway (symlinks). The boundary is
> that the **repo** cannot make that choice on their behalf.

## Design

### The key: `host_files`

A single generic, **user-scope-only** list of `~`-rooted host paths. Not
per-agent — the destination is derived from the path itself, so there is nothing
to scope to an agent.

```jsonc
// ~/.config/yolo-jail/config.jsonc  — USER SCOPE ONLY
{
  "host_files": [
    "~/.pi/agent/models.json",     // a file
    "~/.pi/agent/themes/"          // a directory (trailing slash optional)
  ]
}
```

- **Home-rooted only.** Every entry must resolve under the host `$HOME`. The
  destination is *the same path under the jail home*: host `~/.pi/agent/models.json`
  → jail `$HOME/.pi/agent/models.json`. This makes it a "bring my own dotfiles
  into the agent's home" mechanism with a zero-surprise destination. Arbitrary
  host→container paths outside `$HOME` remain the job of `mounts` (ro, into
  `/ctx`). Rationale for home-only, expanded in *Open questions*.
- **Files and directories.** A directory is copied **recursively**; symlinks are
  **followed and materialized** (a plain copy of the target), matching the
  briefing/skills precedent (`_copy_skill_subdirs` follows host symlinks at
  generation time). A broken symlink or missing source **warns and is skipped**,
  never fatal (matches `mounts` / `cache_relocations`).
- **Additive.** The user list is *added to* the baked raw default set (see
  below). It never replaces the composed set, and it cannot subtract from it.

### Raw copy, not bind-mount — and why it's portable

The composed set bind-mounts `:ro` because the entrypoint must *read* the file to
compose it, and read-only-ness protects the managed block. Raw files have no
managed block and nothing to read-compose, so we **copy** them. Copying is the
right primitive for three reasons:

1. **Backend portability.** `macos-user` has **no bind mounts of any kind** — the
   agent runs natively with a real `/Users/_yolojail` home. A `/ctx` bind is
   impossible there; a copy into the home works on *every* backend (podman,
   Apple Container, macos-user) with one code path.
2. **Directories are trivial** — a recursive copy, no per-file mount plumbing,
   no Apple-Container single-file-mount workaround (apple/container#1089).
3. **It's a restoration, not an invention** — the retired `syncHostPiFiles`
   already *copied* siblings into `~/.pi/agent/`. We are reinstating that copy,
   generalized and moved behind the user-scope boundary.

The "ro" the user asked for is satisfied in the sense that *matters*: the **host
original is never exposed live** — the jail gets a snapshot. That an in-jail
agent can edit its own copy is harmless (edits never reach the host) and is
actually correct for a file that is the agent's own config.

### Where the copy happens: host-side, into the home overlay

The copy runs **host-side in the CLI**, like briefing/skill staging
(`_refresh_jail_briefings`, `_prepare_skills`) — *not* in the entrypoint:

- The entrypoint runs *inside* the container and doesn't exist on macos-user;
  host-side staging is the only portable place.
- No `/ctx` mount and no `YOLO_HOST_*_FILES` env are introduced — the entrypoint
  is not involved in the raw path at all (it stays involved only for the
  composed set, unchanged).
- Container backends: copy into the `ws_state` home overlay
  (`<workspace>/.yolo/home/...`, the same overlay briefings materialize into).
  macos-user: copy into `/Users/_yolojail/...` directly.

### Refresh semantics

Re-copy on **every** `yolo` invocation (fresh launch *and* attach-to-running),
mirroring `_refresh_jail_briefings`, so editing a host file propagates to a
running jail on the next `yolo` command. The host file is the source of truth,
so overwriting an in-jail-edited copy is intended (same contract as a
`:ro`-mounted briefing, achieved by re-copy instead of by the kernel).

### Precedence with the composed set

A raw-staged **directory** can contain a file yolo also **composes** — e.g.
staging the whole `~/.pi/agent/` dir, which contains `settings.json`. Ordering
is **raw-copy first, compose second**: the prism's managed write lands *after*
the raw copy and wins. This is the same `defaults < managed` ordering §3.3
already applies to the skills tree (built-in skills staged *under* host skills),
so it is not new behavior — just an ordering invariant to preserve. Document it,
test it.

### The baked raw default

Today the baked raw default is **empty** — after `a84b11c`, only `settings.json`
crosses (composed), and no agent declares a raw sibling. The mechanism ships with
an empty default and a place to put one: a yolo-shipped constant (leaf registry,
e.g. alongside `AgentSpec.HostFiles` or a sibling field) that the user list is
appended to. If a future agent needs a raw file to cross for *every* jail, it
goes there; a user who wants it only for *their* jails uses `host_files`.

### Validation (`yolo check` + preflight)

Mirror `checkCacheRelocations` structure — one checker shared by the loader and
the validator so the error text matches the drop behavior verbatim:

- entry must be a non-empty string;
- expands `~`; must resolve **under `$HOME`** (reject absolute-outside-home and
  `..`-escapes → hard error, this is a real footgun/attack shape);
- reject `:` in the path (podman/mount-option footgun; harmless for a pure copy
  but keeps the path clean and future-proof);
- **missing source** → warn + skip (non-fatal);
- **workspace-scope occurrence** → hard error (the `cache_relocations` message,
  reworded): *"config.host_files: user-scope only — move it to
  ~/.config/yolo-jail/config.jsonc (a workspace config is agent-editable, so it
  cannot decide which host files cross into the jail)."*
- **in-jail**: like `cache_relocations`, the feature is host-side; the loader
  returns nothing in-jail and the validator gates only the filesystem probe on
  `inJail()` (the user config is visible in-jail via the `:ro` mount and the
  snapshot, but its host paths aren't in the jail's namespace — probing them
  would turn a valid host config into a fatal error on every nested run).

## Worked examples

### Example 1 — the motivating case (pi models + a themes dir)

The maintainer runs pi with a custom model provider and a themes directory. On
their **host** `~/.config/yolo-jail/config.jsonc`:

```jsonc
{
  "agents": ["claude", "pi"],
  "host_files": [
    "~/.pi/agent/models.json",   // pi reads this verbatim; yolo has no opinion
    "~/.pi/agent/themes/"        // whole directory, copied recursively
  ]
}
```

Result in every jail this user launches:

```
$HOME/.pi/agent/settings.json          ← COMPOSED (baked AgentSpec.HostFiles):
                                          host theme/defaultProjectTrust merged,
                                          yolo's managed block enforced, :ro
$HOME/.pi/agent/models.json            ← RAW COPY of the host file
$HOME/.pi/agent/themes/catppuccin.json ← RAW COPY (dir copied recursively)
$HOME/.pi/agent/themes/gruvbox.json    ← RAW COPY
```

`settings.json` is composed even though it lives in the same dir; the raw copy of
the *dir* does not clobber it because compose runs last (precedence rule above).

### Example 2 — a claude helper script, plus a shared dotfile

```jsonc
// ~/.config/yolo-jail/config.jsonc
{
  "agents": ["claude"],
  "host_files": [
    "~/.claude/statusline.sh",   // a helper the host settings.json references
    "~/.gitignore_global"        // any home dotfile, not agent-specific
  ]
}
```

`~/.claude/statusline.sh` → `$HOME/.claude/statusline.sh` (raw, executable bit
preserved best-effort); `~/.gitignore_global` → `$HOME/.gitignore_global`. Note
this deliberately replaces the retired `appendSettingsScripts` auto-discovery
(D3): scripts a settings file references are **no longer auto-mounted**; the user
names them explicitly here. Explicit beats magic.

### Example 3 — what a **workspace** config canNOT do (hard error)

A repo ships this in its checked-in `yolo-jail.jsonc`:

```jsonc
// /workspace/yolo-jail.jsonc  — attacker-influenceable, travels with the repo
{
  "agents": ["claude"],
  "host_files": ["~/.ssh/id_ed25519", "~/.aws/credentials"]  // exfiltration attempt
}
```

`yolo check` and preflight **hard-error**:

```
Invalid jail config:
  config.host_files: user-scope only — move it to ~/.config/yolo-jail/config.jsonc
  (a workspace config is agent-editable, so it cannot decide which host files
  cross into the jail)
```

The key is *inexpressible* at workspace scope by construction (the loader reads
only the user config); the validation error exists so a stray workspace entry
fails loudly instead of being a silent no-op.

## Work items

Phased like `cache-relocation.md`: the feature, then what keeps it honest, then
docs. Ship in one atomic commit per phase; the loader + validator must land
together (a half-migration is a silent no-op).

### Phase 1 — the loader + validator

1. **`internal/config/hostfiles.go` (new)** — mirror `relocations.go`:
   - `const hostFilesKey = "host_files"`.
   - `type HostFileEntry struct { HostPath, RelPath string }` (`RelPath` =
     `$HOME`-relative destination).
   - `LoadHostFiles(warn Warn) ([]HostFileEntry, error)` — reads
     `paths.UserConfigPath()` **only** (+ `include_if_found`), `inJail()` →
     nil, malformed entries skipped-with-warn, unreadable user config → error.
   - `checkHostFiles(v any, probeFS bool) (entries, problems)` — shared shape +
     home-scope + `..`/`:` checks; `probeFS` gates the source-exists stat.
2. **`internal/config/validate.go`** — add `validateHostFiles(config, workspace,
   errs, warns)`: re-read the workspace config (as `validateCacheRelocations`
   does) and hard-error on a workspace occurrence; run `checkHostFiles` with
   `probeFS = !inJail()`.
3. **`internal/config/config.go`** — add `"host_files"` back to
   `knownTopLevelConfigKeys` (reversing that slice of `a84b11c`).

### Phase 2 — stage the files

4. **CLI staging** — in the run pipeline, after briefing/skill staging and
   **before** the prism composes, copy each `HostFileEntry` into the jail home
   (container: `ws_state` overlay; macos-user: `/Users/_yolojail`). Recursive for
   dirs, follow-and-materialize symlinks, skip missing sources. Reuse the
   existing copy helpers (`_copy_skill_subdirs` analog); no new bind mounts, no
   env.
5. **Preserve compose-wins ordering** — assert (and test) that the prism's
   managed write for `settings.json` lands after any raw copy of its containing
   dir.
6. **Baked default hook** — an (empty today) yolo-shipped raw-default constant
   the user list appends to; wire it so a future agent default has a home.

### Phase 3 — docs + config-ref

7. **`internal/cli/config_ref.txt`** — add a `host_files` block (user-scope-only,
   raw-copy, files+dirs, additive; contrast with the composed `settings.json`).
8. **`docs/design/agent-credentials.md`** — add `host_files` to the credential
   matrix and the "which host files cross" narrative; note user-scope boundary.
9. **`docs/design/jail-home.md`** — document raw-staged files landing in the home
   overlay and the compose-wins ordering.
10. **`agent-settings-composition.md` §10** — annotate D4 as *partially reversed
    by* this plan (composed set stays baked per D1/D2; the raw user knob returns
    as `host_files`), with a back-link.

## Test plan

- **Unit (`config`)**: `checkHostFiles` accepts files+dirs+`~`; rejects
  outside-home, `..`, `:`, non-string; missing source → warn not error.
- **Unit (scope)**: `host_files` at workspace scope → validation error;
  `LoadHostFiles` ignores workspace + snapshot, reads only user config; `inJail()`
  → nil.
- **Unit (staging)**: file copied to the right home-relative dest; dir copied
  recursively; symlink materialized; broken symlink skipped; compose-wins when a
  staged dir contains `settings.json`.
- **Nested-jail (mandatory, per AGENTS.md)**: fresh temp workspace + temp `$HOME`
  seeded with a user config listing a raw file and a dir + host files present;
  run `./dist-go/linux-$(go env GOARCH)/yolo -- bash`; confirm the raw copies land
  in the home, `settings.json` is still composed with its managed block, and a
  workspace-scope `host_files` hard-errors.

## Non-goals

- **No codec / no management for raw files.** If yolo ever needs to *reshape* a
  new file, that is a new composed surface (a manifest entry + managed policy in
  Go), not a `host_files` entry.
- **No workspace-scope path.** Permanent, by the credential boundary. A repo that
  needs a file in the jail commits it to the repo (the workspace bind) or asks
  the human to add it to their user config.
- **No arbitrary host→container mapping.** `host_files` is `$HOME`-relative with
  a mirrored destination; arbitrary paths into `/ctx` remain `mounts` (ro).
- **No tree-staging executor / `ctx.stage` glob engine** (the §3.3 vaporware).
  A flat list of paths + recursive dir copy covers the real need without it.

## Open questions

### Should destinations be `$HOME`-relative only? (leaning **yes**)

Restricting to home keeps the destination zero-surprise (same path under the jail
home) and keeps the mechanism a "dotfiles into the agent home" tool rather than a
second, copy-flavored `mounts`. An explicit `host:dest` form (like `mounts`)
would generalize it but reintroduce a destination-choice surface and a bigger
attack shape. Recommend home-only until a concrete need appears.

### Additive vs. replace, if a baked default ever exists (leaning **additive**)

With an empty baked default today the question is moot, but pin the semantics
now: user entries are **added**, never replace the baked set, so a baked default
a future agent relies on can't be silently dropped by a user listing something
else. (This is the safe half of the old additive/replace choice.)

### One flat `host_files` vs. routing a unified list (rejected)

Considered: a single list where yolo *auto-routes* each entry — compose it if a
manifest surface exists, else raw-copy. Rejected: the user can't add composed
entries anyway (no codec/policy to author), so the routing rule would be an
invisible yolo-internal decision ("this one gets a managed block, that one
doesn't"), and it muddies the workspace-can't-widen check. Two explicit
mechanisms (baked-composed, user-raw) are clearer than one magic list.

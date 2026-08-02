# Handoff — close the pack/host gaps that block packifying a real user config

**Audience:** an agent working in the yolo-jail repo.
**Requester's goal (verbatim intent):** *"fully manage my host briefings and skills and
associated files"* from packs, including *"packify my fzf customized file finder"* for
Claude. Explicitly: **fix this in yolo first, before migrating any user config.**

Written 2026-08-01 against **0.7.1+326.g8f5e3b1** (Phase 9 / `autonomy` shipped). Every
claim below was verified by running the binary — commands in
[Appendix](#appendix-reproduction-commands). Downstream analysis lives in
`~/sysadmin/docs/yolo-packification-plan.md` and `yolo-packs-migration.md`.

---

## TL;DR — five gaps, ranked

| # | Gap | Severity | Fix size |
|---|---|---|---|
| **G1** | `apply --host` **silently** skips `skills` + `briefing` — no output line at all | **blocker** for the stated goal | medium |
| **G2** | `allow_exec` is honored **only in user config**, but the error message says to set it in `pack.json` — where it is ignored | **bug** (misleading error) | small |
| **G3** | `files` kind is refused at host, so a pack cannot own a host script/tree | blocker for "associated files" | medium |
| **G4** | `host_files` renders at `0o444` — **cannot carry an executable**, so the fzf hook can't ship that way either | blocker for the fzf case | small |
| **G5** | `yolo config-ref` documents 12 kinds; `autonomy` (13th) is missing | docs | trivial |

**G1 is the one that matters.** The others are each a specific consequence of the same
theme: the pack system is a *jail* content system with a host render bolted to the config
subset, and everything that is not a settings surface falls off the edge — sometimes
loudly, sometimes not.

---

## The concrete user case this must support

A single Claude customization that today spans **three** mechanisms and cannot be packed:

`~/.dotfiles/claude/file-suggestion.sh` — a 20-line `fd | fzf --filter` custom file
finder, wired in via `settings.json`:

```jsonc
"fileSuggestion": { "type": "command", "command": "~/.claude/file-suggestion.sh" }
```

To packify it, a pack must deliver **all three** of:

1. the **settings key** (`fileSuggestion`) → `config` kind. **Works today**, both notches.
2. the **executable script** at `~/.claude/file-suggestion.sh` → `files` kind. Refused at
   host (**G3**), and refused *anywhere* without `allow_exec` (**G2**), and the
   `host_files` alternative strips the exec bit (**G4**).
3. its **host deps** (`fd`, `fzf`, `jq`) → `program` + `install_hints`. Not run at host
   yet (known, Phase 4.3).

So this one small feature hits three of the five gaps. It is the ideal acceptance test:
**when a pack can deliver the fzf finder to both a jail and the host, this handoff is
done.**

Note the script is currently **never staged into any jail** —
`find ~/.local/share/yolo-jail -name 'file-suggestion*'` is empty — so in-jail Claude
silently has a `fileSuggestion` command pointing at a nonexistent file. Packing it fixes a
live (if quiet) breakage, not just a tidiness concern.

---

## G1 — `apply --host` silently skips `skills` and `briefing`

### Evidence

A pack carrying both an `AGENTS.md` and one skill, applied for real:

```console
$ HOME=/tmp/bt … yolo apply --host --assert
apply --host  home /tmp/bt  posture assert (writing)
  claude/config        refused: uses ${workspace}, which has no referent on the host
  claude/settings      rendered  /tmp/bt/.claude/settings.json

$ find /tmp/bt -name 'CLAUDE.md' -o -name 'SKILL.md' | grep -v /pack/
NOTHING WRITTEN outside the pack itself
```

No `CLAUDE.md`, no skill, **and no refusal line**. Compare `files`, which is refused *by
name* in the same run:

```
files      refused — files binds a pack tree into a jail — nothing to bind into off-container
```

### Why it happens

`internal/render/fieldset.go` — `HostFields()` includes `skills` and `briefing` in the
applicable set (they are "prose kinds" that "port"), but `RenderHostPack`
(`internal/entrypoint/hostrender.go`) iterates **`p.Surfaces()` only** — i.e. `config`
contributions. A kind that is *applicable* but has no surface is neither rendered nor
refused. It vanishes.

`refusalReasons` has entries for `program`, `mount`, `reads-host`, `state`, `files` — and
none for `skills`/`briefing`, because the census says they *do* apply.

That is the bug in one line: **the census promises them and the renderer doesn't
implement them.**

### What to build

Host-render `skills` and `briefing`, reusing the jail staging logic:

- **`skills`** — `PrepareSkills` (`internal/agents/skills.go`) already composes
  `built-in < pack < user's own` into a staging dir, then the jail *bind-mounts* it. For
  host, the same composition must **materialize into the real dir** (`~/.claude/skills`).
  Two decisions to make explicitly:
  - **Ownership.** A jail mount is `:ro` and disposable; a host dir is the user's. Do
    **not** `clearDirContents` a real `~/.claude/skills` — that would delete
    hand-written skills. Suggest: write pack skills as individual entries, never clear the
    destination, and skip any name the user already has (mirroring the existing
    `pack < user` precedence). Report per-skill `rendered` / `skipped (yours)`.
  - **Removal.** When a pack drops a skill, does the host copy go? Recommend **no** —
    consistent with "no `--revert`" — but print it.
- **`briefing`** — the jail path concatenates pack prose after a host file
  (`after: "host:.claude/CLAUDE.md"`). On the host, source and destination are the *same
  file*, so a naive concat **duplicates the user's prose on every apply**. This needs a
  delimited managed block, e.g.
  ```markdown
  <!-- yolo:pack-briefing begin (matt-core) -->
  …pack prose…
  <!-- yolo:pack-briefing end -->
  ```
  re-asserted idempotently, with everything outside the markers untouched. Same RMW
  contract as `config`, in Markdown. **Do not ship a plain append.**

Until both land, the minimum honest fix is a **one-line refusal** each, so the census stops
lying:

```
skills     refused — host skills render not implemented (see handoff-pack-host-management-gaps.md G1)
briefing   refused — host briefing render not implemented; a naive append would duplicate your prose
```

Do that first even if the full feature is deferred — a silent skip is the worst of the
three states.

### Acceptance

- `apply --host` on a skills+briefing pack either writes them or names them as refused.
- Re-running `--assert` twice is idempotent: no duplicated briefing prose, no churn.
- A user-authored skill of the same name is preserved (precedence held).
- A hand-written section of `~/.claude/CLAUDE.md` outside the markers survives.

---

## G2 — `allow_exec` is honored only in user config, but the error points at the pack

### Evidence

Pack with an exec-bit file, no `allow_exec` anywhere:

```console
$ yolo pack lint /tmp/ex/pack
✗ pack file files/file-suggestion.sh is executable (mode 755) but the pack does not set
  "allow_exec": true — a pack is content, so shipping an executable is an explicit opt-in
```

Follow that advice exactly — add `"allow_exec": true` to `pack.json` — and **nothing
changes**; the identical error repeats. The flag is a **PackEntry** field read from user
config (`internal/config/packs.go:279`, threaded at `internal/cli/pack.go:439` into
`packstage.Spec.AllowExec`, enforced at `internal/packstage/packstage.go:141`).
`internal/packdecl` — the `pack.json` parser — has **no** `allow_exec` at all.

### Why it's the right design, stated wrongly

Consumer-side opt-in is correct: a pack author must not be able to self-grant the exec
bit, or "a pack comes from someone else's repo" (the message's own rationale) is
meaningless. The *message* is what's broken — it tells the reader to edit the file that
cannot possibly grant it.

### Fix (small)

1. Reword to name the real location and show the real shape:
   ```
   pack file files/file-suggestion.sh is executable (mode 755). A pack cannot self-grant
   the exec bit — the CONSUMER opts in, in ~/.config/yolo-jail/config.jsonc:
       "packs": [{"source": "file:///…/pack", "allow_exec": true}]
   ```
2. Make `"allow_exec"` in `pack.json` a **validation error** ("not a manifest key — set it
   on the pack entry in your user config"), not silently ignored.
3. `yolo pack lint <dir>` has no consumer context, so add `--allow-exec` to let an author
   lint the way a consenting consumer would stage.

---

## G3 — a pack cannot own a host file tree

`files` is refused at host: *"files binds a pack tree into a jail — nothing to bind into
off-container."* True for a **bind mount**; the host equivalent is simply **writing the
tree**, which is what the user wants for `~/.claude/file-suggestion.sh`, pi's
`models.json`, and pi's 6 themes.

Options, in order of preference:

1. **Render `files` at host as a real copy**, `:ro`-equivalent (`0o444`, or `0o555` when
   the source has the exec bit **and** the consumer set `allow_exec`). Needs the same
   never-delete-user-content care as G1's skills.
2. Keep the refusal, and document `host_files` as the sanctioned host path — but that only
   works after **G4**, and it lives in *user config*, not a pack, so it does not satisfy
   "manage it from a pack."

Prefer (1). Without it, "associated files" is unreachable from a pack and every such file
stays on rcm.

---

## G4 — `host_files` cannot carry an executable

`host_files` (shipped 2026-07-25, `docs/plans/host-file-staging.md`) is a strong existing
mechanism and its own reference **already cites the exact user files in question**
(`~/.pi/agent/models.json`, `~/.pi/agent/themes/`). But:

```go
// internal/entrypoint/hostfiles.go:118
return os.Chmod(dest, 0o444)      // readonly mode
// …:120  copy mode → 0o644
```

`readonly` locks `0o444`, `copy` writes `0o644`. **No mode yields an executable**, and
`codec: "raw"` (correct for a `.sh`) doesn't change permissions. So a `host_files` entry
for `file-suggestion.sh` delivers a file Claude will try to exec and get `EACCES`.

Fix: preserve the **source's** exec bit (mask `0o111` from the source into the rendered
mode), or add an explicit `"executable": true`. Source-derived is better — it needs no new
knob and matches "mirror this host file." Note `readonly`'s chmod dance (restore `0o644`
to re-truncate, then re-lock) must become `0o555`-aware.

This is small and independently useful: it makes `host_files` a viable interim answer for
the fzf script even before G3.

---

## G5 — `config-ref` is missing the `autonomy` kind

`yolo config-ref` lists 12 kinds under *"a `kind` from the closed set"*; the binary
implements 13. `autonomy` — the Phase 9 kind that makes `apply --host` safe, with
`autonomous` / `guarded` postures — is absent, so the only way to learn its schema is
reading `packs/claude/pack.json`. The migration guide's table already says thirteen.
Add it, with both posture sub-objects.

---

## Suggested order

1. **G5** + **G2** (docs/messaging; minutes each, remove active misdirection).
2. **G1's refusal lines** — stop the silent skip immediately, even if the render is deferred.
3. **G4** — small, unblocks the fzf script via `host_files` as an interim.
4. **G1 proper** — host `skills` + delimited-block `briefing`. The main event.
5. **G3** — host `files` render, reusing G1's never-clobber discipline.

After 1–5, re-check `apply --host` against the acceptance test in
[the fzf case](#the-concrete-user-case-this-must-support).

## Cross-cutting principles for whoever picks this up

- **A real `$HOME` is not a jail home.** Every jail path here is disposable and
  `:ro`-mounted; the host equivalents are the user's own files. `clearDirContents`,
  wholesale tree replacement, and unconditional overwrite are all safe in a jail and
  destructive on a host.
- **RMW, in every format.** `config` already does key-level RMW. Host `briefing` needs the
  Markdown analogue (delimited block); host `skills`/`files` need the filesystem analogue
  (per-entry, user wins).
- **Never silent.** The `files` refusal is good behavior; the `skills`/`briefing` skip is
  not. Anything not written should say so, by name, in both `observe` and `assert`.
- **Idempotence is the test.** `--assert` twice must be a no-op. That is what catches the
  briefing-duplication bug before a user finds it.
- **The consumer grants host power, not the pack author.** G2's design is right; only its
  message is wrong. Preserve that invariant in G3/G4 (exec bit needs consumer opt-in).

## Appendix: reproduction commands

```console
$ yolo --version                                       # 0.7.1+326.g8f5e3b1

# G1 — silent skip (pack has AGENTS.md + skills/testskill/SKILL.md)
$ HOME=/tmp/bt XDG_CONFIG_HOME=/tmp/bt/.config yolo apply --host --assert
$ find /tmp/bt -name 'CLAUDE.md' -o -name 'SKILL.md' | grep -v /pack/   # → nothing

# G2 — misleading error; adding allow_exec to pack.json changes nothing
$ yolo pack lint /tmp/ex/pack
$ grep -rn 'AllowExec' internal/config/packs.go internal/packstage/packstage.go
$ grep -rn 'allow_exec' internal/packdecl/     # → no match (not a manifest key)

# G3 — files refused at host
$ HOME=/tmp/pitest … yolo apply --host | grep files
files      refused — files binds a pack tree into a jail — …

# G4 — host_files permission modes
$ sed -n '95,125p' internal/entrypoint/hostfiles.go     # 0o444 / 0o644, no exec

# G5
$ yolo config-ref | grep -A 16 'a "kind" from the closed set'   # 12 kinds, no autonomy

# Context: whose skills reach which agent today
$ ls ~/.local/share/yolo-jail/agents/*/skills-*
skills-claude  <the user's 5 skills> + 3 builtins
skills-pi      3 builtins only        # ← pack skills would fix this
```

All `--assert` runs were against throwaway `$HOME`s under `/tmp`, since removed. No real
user config was modified while verifying this.

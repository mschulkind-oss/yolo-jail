---
title: "Config Safety: User/Agent Workflow"
date: 2026-08-18
status: in-review
tags: [config, security, agents, approval]
summary: "Agents may edit the workspace config; humans must approve the change before it takes effect. The snapshot-and-diff flow that implements this, and the two rulings that close the holes in it: the snapshot moves out of the jail's reach, and a non-interactive launch stops accepting silently. Decided and built; one follow-on question (OQ-D3) is open."
---

# Config Safety: User/Agent Workflow

**Status:** DECIDED 2026-08-18, **both rulings implemented 2026-08-18** — and **one follow-on
question, OQ-D3, is still open.** The approval snapshot lives host-side at
`~/.local/share/yolo-jail/approvals/<container-name>.json` (`config.ApprovalSnapshotPath`), and a
non-interactive launch with a changed config is refused unless `--accept-config-changes` is passed.

**Why the frontmatter says `in-review` and not `accepted`.** `status: accepted` is load-bearing and
means *zero* open `💬` questions. This doc has one — OQ-D3, which asks how to close the migration
window OQ-D1's implementation left behind. So the honest reading is **decided and built, with one
follow-on question open**: nothing in the Decision Ledger is provisional, and the frontmatter is not
claiming to be finished while a question is waiting for a ruling. It flips to `accepted` the moment
OQ-D3 is answered and compacted.

**The short version.** An agent may edit the workspace config; a human must approve the change before
it takes effect, via a diff and a prompt at the next launch. Two things made that promise weaker than
it reads, and both are now ruled: the record of what was approved lived somewhere the agent could
rewrite, and a launch with no terminal accepted silently.

**Reads with:** [`loophole-activation.md`](loophole-activation.md) — OQ-A13 makes this diff the
disclosure for enabling a host-reaching loophole, which is what turned OQ-D1 from housekeeping into a
safety property.

---

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built — verified 2026-08-23 |
| :--- | :--- | :--- | :--- | :--- |
| **OQ-D1** | The approval snapshot **moves host-side**, out of the rw bind mount — a record the jail can rewrite is not a record | 2026-08-18 | [§ File Locations](#file-locations) | ✅ `config.ApprovalSnapshotPath` (`internal/config/snapshot.go:40`) joins `paths.ApprovalsDir()` (`internal/paths/paths.go:353`, `$HOME/.local/share/yolo-jail/approvals`) with `runtime.FromWorkspace`'s container name. No mount in `internal/cli/run` or `internal/entrypoint` names that directory. Pinned by `TestCheckConfigChangesSnapshotLandsOutsideTheWorkspace` (`internal/config/config_test.go:177`), which asserts both halves — new path under the host state dir, old workspace path not written |
| **OQ-D2** | Non-interactive + changed config is **fatal**; CI opts in with an explicit flag rather than an implicit yes | 2026-08-18 | [§ User Responses](#user-responses) | ✅ `CheckConfigChanges` returns `*ChangedNonInteractiveError` on `!isTTY && !acceptNonInteractive` (`internal/config/snapshot.go:244–257`), *without* rewriting the snapshot. Rendered by `Options.printChangeRefusal` (`internal/cli/run/preflight.go:220`). The flag constant `config.AcceptConfigChangesFlag` (`snapshot.go:94`) is read back by the parser at `internal/cli/runcmd.go:156`, so the flag the refusal names and the flag the parser accepts cannot drift. Both call sites gate: container fresh-launch at `internal/cli/run/run.go:354`, `macos-user` arm at `run.go:144` |

## Open Questions

1. 💬 **OQ-D3: the migration signal sits inside the mount it is signalling about.**
   OQ-D1 moved the *record* host-side but left the *migration marker* — the presence of
   `<workspace>/.yolo/config-snapshot.json` — as the only thing separating "this workspace was
   launched before, so show the whole config and ask" from "first run, accept silently". That marker
   is a file in the read-write bind mount, so an agent that edits `yolo-jail.jsonc` and `rm`s the
   marker turns its own change into a genuine-looking first run: accepted with no prompt, and
   recorded host-side as approved. **Presence is not more trustworthy than content; it is the same
   bit in the same mount.** Do we close it by prompting on a brand-new workspace's very first
   launch — the trade this document deliberately ruled against — or do we accept a one-shot
   per-workspace window and say so?

   **What it decides:** whether OQ-D1's guarantee is unconditional or holds only *after* a
   workspace's first post-upgrade launch. The window is one-shot per workspace and closes the first
   time a host-side record is written, but it is open on every workspace not yet launched since
   OQ-D1 — which on upgrade day is all of them — and an agent running on the *pre*-upgrade build can
   open it pre-emptively. It also decides, downstream, whether
   [`loophole-activation.md`](loophole-activation.md) OQ-A13 may call this diff a control without a
   caveat. Diagnosis and the ruled-out repairs are in [§ Edge Cases](#edge-cases).

   **Still open, verified against the tree 2026-08-23.** `CheckConfigChanges` still branches on
   `!pathExists(LegacyWorkspaceSnapshotPath(workspace))` (`internal/config/snapshot.go:221`) and, on
   that branch, writes the current config and returns `true` with no prompt (`snapshot.go:222–227`) —
   the silent first-run accept. `LegacyWorkspaceSnapshotPath` (`snapshot.go:73`) still resolves to
   `<workspace>/.yolo/config-snapshot.json`, inside the rw mount. The hole is documented in the code
   itself under the heading `KNOWN RESIDUAL HOLE, stated rather than implied` (`snapshot.go:57–72`),
   carrying the same reasoning as this question. Nothing has been built against it.

   _Leaning:_ **accept the window, and make it self-closing rather than silent.** Prompting on a
   brand-new workspace's first launch is the one trade this doc already rejected, and re-opening it
   to cover a one-shot migration is a permanent cost for a transient hole. The middle option I would
   actually build is to have a first run *say* it is treating the config as a first run — a printed
   line, not a prompt — so the silent branch stops being silent without becoming a gate. I hold this
   loosely: if OQ-A13's disclosure is meant to be a hard control, the caveat is unacceptable and the
   first-launch prompt wins.

   **Answer:**
   > _(empty — fill in when decided)_

---

## Problem

AI agents running inside YOLO Jail can edit workspace files, including
`yolo-jail.jsonc`. Without guardrails, an agent could add packages, mounts,
or change security settings without the human operator noticing. The changes
would silently take effect on the next jail restart.

## Design Goals

1. **Agents CAN edit the config** — they need to request packages for their work
2. **Humans MUST approve changes** — no silent config modifications
3. **Agents MUST self-validate edits** — run `yolo check` after every config change
4. **The flow should be natural** — no special commands or flags needed
5. **Non-interactive use should still work** — CI/scripts shouldn't block

## How It Works

### Config Snapshot

On every new jail startup, the CLI compares the current merged config
(user defaults + workspace config) against a snapshot from the previous run:

- **First run**: Config is accepted and a snapshot is saved at
  `~/.local/share/yolo-jail/approvals/<container-name>.json`
- **No changes**: Startup proceeds normally
- **Changes detected**: A unified diff is displayed and the user is prompted
  with `Accept these config changes? [y/N]`
- **Migrating a workspace that was approved before OQ-D1**: the old
  `<workspace>/.yolo/config-snapshot.json` is a *migration signal only*. Its presence says the
  workspace has been launched before — so the missing host-side record is a migration, not a first
  run, and a first run accepts silently. Its **content is never adopted**: it is precisely the file
  the ruling declares untrustworthy. So the launch diffs against an empty previous config, shows the
  whole current config once, and asks. On acceptance the legacy file is deleted.

The snapshot stores the **normalized** (parsed and re-serialized) config, so
cosmetic changes like reformatting or reordering comments don't trigger a diff.

### What the User Sees

```
⚠  Jail config changed since last run:

--- previous config
+++ current config
@@ -1,4 +1,7 @@
 {
+  "packages": [
+    "postgresql"
+  ],
   "security": {
     "blocked_tools": [

Accept these config changes? [y/N]
```

### User Responses

- **y/yes**: Changes are accepted, snapshot updated, jail starts
- **N/no/empty**: Changes are rejected, jail does not start. The user can
  inspect and revert the config before trying again.
- **Non-interactive** (piped stdin): **the launch FAILS.** A config change that
  nobody can be asked about is not a change that may take effect silently. The refusal names
  `--accept-config-changes`, the workspace and user config files, the host-side approval record, and
  prints the same coloured diff the prompt would have.
- **Non-interactive + `--accept-config-changes`**: the change is approved for **that launch**, and
  the snapshot is rewritten exactly as a `y` rewrites it — so the next run is not asked again.

```console
$ yolo -- true < /dev/null
⚠  Jail config changed since the last approved launch, and this launch has no terminal to approve it on.

--- previous config
+++ current config
@@ -1,3 +1,4 @@
 {
   "packages": [
+    "postgresql",
     "ripgrep"

A changed config is never accepted without a human — …

  workspace config: /home/matt/code/thing/yolo-jail.jsonc
  user config:      /home/matt/.config/yolo-jail/config.jsonc
  approved config:  /home/matt/.local/share/yolo-jail/approvals/yolo-thing-1a2b3c4d.json

Revert the change, or approve it for THIS LAUNCH ONLY by re-running with
  --accept-config-changes
which records the new config as approved exactly as answering `y` would.
```

> **RULED (OQ-D2, 2026-08-18): non-interactive + changed config is fatal, and CI opts in explicitly.**
> This reverses the behaviour described above, which auto-accepted and rewrote the snapshot
> (`config/snapshot.go:38` **as it stood before the ruling** — that line number is a pre-fix
> citation and does not resolve to the auto-accept today; the branch it names is now the refusal at
> `internal/config/snapshot.go:243`, verified 2026-08-23). Auto-accept made Design Goal 2 conditional on someone happening to have
> a terminal attached — and the scripted case is exactly where nobody is watching. A CI pipeline that
> genuinely wants the new config says so with a flag; one that changed its config by accident finds
> out immediately instead of running with silently-approved settings.
>
> Design Goal 5 (*"Non-interactive use should still work"*) is **preserved, not dropped**: it works
> via the explicit opt-in rather than via an implicit yes.

> [!IMPORTANT]
> **This is a breaking change for existing non-interactive callers**, and it is the good kind — a
> pipeline that has been silently accepting config drift starts failing rather than continuing to
> not-tell-anyone. It needs a release note, and the failure message must name the flag, because the
> whole point is that the reader of that message cannot be prompted.
>
> **A flag rather than an environment variable, deliberately**, even though this repo's other
> bypasses (`YOLO_ALLOW_STALE_IMAGE`, `YOLO_ALLOW_UNREACHABLE_SERVICES`) are env vars. Those suppress
> a *diagnosis*; this one grants an *approval*, and approval is the thing the whole document says a
> human must give per-launch. An env var is inherited by every child process and survives in a shell
> for the rest of a session, which is precisely the property an approval must not have.

### Reusing Containers

Config approval checks only run when **creating a new container**. When attaching to
an existing running container (`podman exec`), the config is not re-checked
because the container was already started with its config. This is why agents
must run `yolo check` themselves after every config edit, even mid-session.

**Every backend gates, including `macos-user`.** The check lived inside `runContainer`, which the
`macos-user` arm of the run pipeline returns before ever reaching — so the one backend with no
container around it was the one launching agent-edited configs unprompted, and it reads
`security.blocked_tools`, `mcp_servers`, `lsp_servers` and `packages` straight off that config.
(`mcp_servers` is a command line the agent's own MCP client executes.) Fixed 2026-08-18: the arm
calls the same gate before dispatching. It sits in the arm rather than above the backend dispatch
because "fresh launch only" is a container-path distinction — `macos-user` has no attach, every
invocation is a fresh sandbox. `--dry-run` is exempt: it renders a plan and launches nothing.
Verified 2026-08-23: `if !o.DryRun && !o.checkConfigChanges(cfg)` in the `macos-user` arm at
`internal/cli/run/run.go:144`, and the container fresh-launch gate at `run.go:354`, are the same
`Options.checkConfigChanges` (`internal/cli/run/preflight.go:198`).

## Agent Workflow

The intended flow for agents that need additional packages:

1. Agent determines it needs a package (e.g., `postgresql` for database work)
2. Agent edits `/workspace/yolo-jail.jsonc`:
   ```json
   {
     "packages": ["postgresql"]
   }
   ```
   For C headers + `pkg-config` (e.g. cgo builds), use the `.dev` shorthand —
   propagated dev outputs are pulled in transitively, and the runtime
   libraries ride along so built binaries also run:
   ```json
   {
     "packages": ["gtk4.dev"]
   }
   ```
   For a specific version, using a nixpkgs commit hash:
   ```json
   {
     "packages": [{"name": "freetype", "nixpkgs": "e6f23dc0..."}]
   }
   ```
   Find commits per version at: https://lazamar.co.uk/nix-versions/
3. Agent runs `yolo check` (or `yolo check --no-build` inside a running jail)
   and fixes any reported config/build problems before asking for a restart
4. Agent tells the human: *"I've added `postgresql` to the jail config and ran
   `yolo check`. Please restart the jail so I can use it."*
5. Human exits the jail and runs `yolo` again
6. Human sees the config diff and types `y` to approve
7. Image rebuilds with the new package (takes a minute)
8. Agent can now use `psql` and PostgreSQL tools

### What Agents Should Know

This information is automatically included in the AGENTS.md injected into
every jail. Agents are told:

- They can edit `yolo-jail.jsonc` to add packages
- Package names must match nixpkgs attributes
- They must run `yolo check` after **every** config edit before asking for a restart
- They must ask the human to restart
- They must NOT use apt, nix-env, or other package managers

An agent can check whether its edit has taken effect yet with `yolo config drift`,
which compares the workspace config on disk against the one the running jail was
built from. The jail freezes that baseline (workspace-only) at fresh launch to
`<workspace>/.yolo/config-boot.json` — immutable for the jail's life, distinct from
the every-launch `config-assembled.json`. `drift` re-reads the live config in-jail
(the workspace is bind-mounted, so this is accurate) and diffs the canonical form:
exit `0` in sync, `3` drifted (prints the diff), `4` no baseline. Since the running
jail's config is fixed until restart, a non-zero drift is the signal that a restart
is owed. `yolo config dump` prints the full effective config in the same canonical
form for inspection.

### Security Properties

- **Human-in-the-loop**: Every config change requires explicit approval
- **Visible diff**: The human sees exactly what changed
- **Reversible**: If rejected, the config file is still modified but the
  jail doesn't start. The human can `git checkout yolo-jail.jsonc` to revert.
- **No privilege escalation**: Packages are nix packages, not arbitrary
  binaries. The nix sandbox ensures reproducibility.

## File Locations

| File | Purpose |
|------|---------|
| `yolo-jail.jsonc` | Workspace config (project root) |
| `~/.config/yolo-jail/config.jsonc` | User-level defaults |
| `~/.local/share/yolo-jail/approvals/<container-name>.json` | **Last-approved normalized config — HOST-SIDE, never mounted into any jail** |
| `<workspace>/.yolo/config-assembled.json` | The merged config the host assembled for this launch, delivered into the jail (see below) |
| `<workspace>/.yolo/config-boot.json` | Frozen workspace-only config the jail was built from (`yolo config drift`) |

> **RULED (OQ-D1, 2026-08-18): the snapshot moves out of the jail's reach**, into per-workspace
> host-side state the jail never mounts. **Implemented** — `config.ApprovalSnapshotPath`
> (`internal/config/snapshot.go:40`) over `paths.ApprovalsDir()` (`internal/paths/paths.go:353`);
> verified 2026-08-23.
>
> The old location defeated Design Goal 2: `/workspace` is bind-mounted **read-write**, so
> whatever edits `yolo-jail.jsonc` could also rewrite the only record of what was last approved, and
> the next launch showed nothing to approve. Note the shape of the defect — **deleting** the snapshot
> fails safe, so what is needed is *integrity*, not secrecy, which is why signing it or mounting it
> `:ro` are both more machinery than the problem requires.
>
> It is keyed by `runtime.FromWorkspace`'s deterministic container name, the same key
> `paths.ContainerDir` and `paths.AgentsDir` already use for their per-workspace host state — not a
> new keying scheme. Its one real cost is that a workspace copied or moved elsewhere loses its
> approval baseline and re-prompts — which is the direction to fail in.
>
> **The jail DID read the old file, and that is why there are now two files.** The prompt runs
> host-side, but `config.LoadConfig` also short-circuited on `config-snapshot.json` in-jail for the
> jail's own workspace, because the user-level `include_if_found` overrides it carries are host-side
> files the jail never sees and an in-jail re-assemble silently produces a *reduced* config. One file
> was doing two jobs that pull opposite ways: the approval record must be somewhere the jail cannot
> write, the delivery copy must be somewhere it can read. So they split —
> `config-assembled.json` is the delivery copy, written unconditionally at every fresh launch
> (`internal/config/assembled.go`). Nothing about the delivery copy's integrity is load-bearing: a
> jail that rewrites its own assembled config has only lied to itself about a config it can already
> edit at the source, in the same mount. The security boundary was never there — it is that
> `LoadCacheRelocations` and `LoadHostFiles` read the host user config **directly**, which makes
> workspace scope inexpressible rather than merely rejected.
>
> **What this unblocks:** [`loophole-activation.md`](loophole-activation.md) OQ-A13 ruled that a
> workspace may enable a host-reaching loophole with this diff as the disclosure, and explicitly
> declined to count that as a safety property *because of this defect*. With the snapshot out of
> reach, the disclosure becomes a control.

## Edge Cases

- **No config file**: Empty config `{}` is snapshotted. Adding a config later
  triggers a diff.
- **User config changes**: Since the snapshot stores the merged result,
  changes to user-level config also trigger a diff.
- **Config deleted**: Triggers a diff (previous config → empty config).
- **Snapshot deleted**: fails safe, but not by prompting — an absent record with no legacy file is a
  *first run*, which accepts the current config silently and records it. That is the same trade the
  first-launch case makes. Post-migration this costs nothing: the host-side record is out of the
  jail's reach, so only the human can delete it, and deleting your own baseline loses it without
  hiding anything.
- **⚠ Legacy signal deleted (OQ-D3, open)**: the sentence above does *not* hold during the migration
  window, and the ruling as written assumes it does. Before a workspace has a host-side record, the
  ONLY thing separating "migration → show the whole config and ask" from "first run → accept
  silently" is the presence of `<workspace>/.yolo/config-snapshot.json` — a file in the read-write
  bind mount. **Presence is not more trustworthy than content; it is the same bit in the same
  mount.** An agent that edits `yolo-jail.jsonc` and `rm`s that file makes its own change into a
  genuine-looking first run: silently accepted, and recorded host-side as approved. Confirmed by
  test 2026-08-18. The window is one-shot per workspace and closes the first time a host-side record
  is written — but it is open on every workspace not yet launched since OQ-D1, which on upgrade day
  is all of them, and an agent running on the *pre*-upgrade build can open it pre-emptively.
  No host-side artifact predates the ruling and survives to replace the signal (`ContainerDir`
  tracking files are pruned when the container stops; `AgentsDir` staging is recreated by the same
  launch, before the gate runs), so the only sound repair is to stop letting a missing record mean
  "first run" — i.e. prompt on a brand-new workspace's first launch, which this document
  deliberately ruled against. That trade is the open question, not the diagnosis: it is
  **[OQ-D3](#open-questions)**, which carries the stakes and a leaning.

  > [!WARNING]
  > **Do not "fix" this by adopting the legacy file's content as a baseline.** That is the one repair
  > that looks obvious and reintroduces the exact defect OQ-D1 closed — the content of
  > `<workspace>/.yolo/config-snapshot.json` is by definition a file the jail could have written, so
  > trusting it to skip a prompt carries the hole across the change that was meant to end it. The
  > code says so where someone would reach for it (`internal/config/snapshot.go:47–72`, and again at
  > the branch itself, `snapshot.go:217–220`: *"Its CONTENT is deliberately never read"*). Verified
  > 2026-08-23.
- **Multiple agents**: All share the same config file. If two agents modify
  it, the human sees all changes combined in one diff.

---

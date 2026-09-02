---
title: "Config Safety: User/Agent Workflow"
date: 2026-08-18
status: accepted
tags: [config, security, agents, approval]
summary: "Agents may edit the workspace config; humans must approve the change before it takes effect. The snapshot-and-diff flow that implements this, and the three rulings that close the holes in it: the snapshot moves out of the jail's reach, a non-interactive launch stops accepting silently, and a fresh workspace's first launch confirms its config. Decided and built; no open questions."
---

# Config Safety: User/Agent Workflow

**Status:** DECIDED 2026-08-18, **both rulings implemented 2026-08-18** — and the follow-on
question, **OQ-D3, was closed on 2026-08-29** by
[`scoped-config-approvals.md`](scoped-config-approvals.md)'s OQ-S3 (built the same day,
`27b335ce`). The approval snapshot lives host-side at
`~/.local/share/yolo-jail/approvals/<container-name>.json` (`config.ApprovalSnapshotPath`), a
non-interactive launch with a changed config is refused unless `--accept-config-changes` is passed,
and a fresh workspace with a **non-empty** config now prompts on its very first launch — which is
what removed the migration window OQ-D3 was about.

**The frontmatter says `accepted` because zero `💬` questions remain.** It said `in-review` while
OQ-D3 was open, under this doc's own rule that `accepted` is load-bearing; the flip happened when
OQ-D3 was answered and compacted (2026-09-02 edit, recording the 2026-08-29 ruling).

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
| **OQ-D1** | The approval snapshot **moves host-side**, out of the rw bind mount — a record the jail can rewrite is not a record | 2026-08-18 | [§ File Locations](#file-locations) | ✅ `config.ApprovalSnapshotPath` (`internal/config/snapshot.go:40`) joins `paths.ApprovalsDir()` (`internal/paths/paths.go:417`, `$HOME/.local/share/yolo-jail/approvals`) with `runtime.FromWorkspace`'s container name. No mount in `internal/cli/run` or `internal/entrypoint` names that directory. Pinned by `TestCheckConfigChangesSnapshotLandsOutsideTheWorkspace` (`internal/config/config_test.go:223`), which asserts both halves — new path under the host state dir, old workspace path not written |
| **OQ-D2** | Non-interactive + changed config is **fatal**; CI opts in with an explicit flag rather than an implicit yes | 2026-08-18 | [§ User Responses](#user-responses) | ✅ `CheckConfigChanges` returns `*ChangedNonInteractiveError` on `!isTTY && !acceptNonInteractive` (`internal/config/snapshot.go:235–248`), *without* rewriting the snapshot. Rendered by `Options.printChangeRefusal` (`internal/cli/run/preflight.go:220`). The flag constant `config.AcceptConfigChangesFlag` (`snapshot.go:94`) is read back by the parser at `internal/cli/runcmd.go:156`, so the flag the refusal names and the flag the parser accepts cannot drift. Both call sites gate: container fresh-launch at `internal/cli/run/run.go:354`, `macos-user` arm at `run.go:144` |
| **OQ-D3** | The migration window **closes by prompting on a fresh workspace's first launch** — the trade this doc had rejected, taken deliberately under [`scoped-config-approvals.md`](scoped-config-approvals.md)'s **OQ-S3** | 2026-08-29 | [`scoped-config-approvals.md`](scoped-config-approvals.md) §5.1 | ✅ `27b335ce` deleted the legacy-marker branch: `CheckConfigChanges` no longer reads `LegacyWorkspaceSnapshotPath` at all (`internal/config/snapshot.go:190–265`; the function survives only for tests asserting the old file is never written). A first run with an **empty** workspace config still accepts silently; a first run with a **non-empty** one diffs against `none (initial launch)` and prompts (`snapshot.go:211–222`), refusing non-interactively without `--accept-config-changes` |

## Open Questions

**None.** OQ-D3 — the migration signal living inside the mount it signals about — was this doc's
last open question. It closed on 2026-08-29, **against this doc's own leaning**: the leaning was to
accept the one-shot window and make the silent branch announce itself, but
[`scoped-config-approvals.md`](scoped-config-approvals.md) OQ-S3 ruled that a fresh workspace with a
declared config must confirm it (a cloned repo's `yolo-jail.jsonc` is exactly a config nobody on
this machine has approved), which closes the migration window as a side effect: presence of the
legacy marker no longer means anything, because "no host-side record + non-empty config" always
prompts. The empty-config first run still accepts silently — there is nothing in `{}` to approve.

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

- **First run, empty workspace config**: `{}` is accepted silently and a snapshot is saved at
  `~/.local/share/yolo-jail/approvals/<container-name>.json` — there is nothing to approve
- **First run, non-empty workspace config**: the whole config is shown as a diff against
  `none (initial launch)` and the user is prompted — this is OQ-S3
  ([`scoped-config-approvals.md`](scoped-config-approvals.md)), which also covers the migration
  from pre-OQ-D1 workspaces: the old `<workspace>/.yolo/config-snapshot.json` is **no longer
  consulted at all** (neither its presence nor its content — `27b335ce` deleted the branch), so a
  workspace approved before OQ-D1 simply re-confirms once
- **No changes**: Startup proceeds normally
- **Changes detected**: A unified diff is displayed and the user is prompted
  with `Accept these config changes? [y/N]`

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
- **Snapshot deleted**: fails safe. An absent record with an **empty** workspace config is a first
  run that accepts `{}` silently; with a **non-empty** config it prompts (OQ-S3), so deleting the
  record can never smuggle a config past the human. The host-side record is out of the jail's reach
  anyway, so only the human can delete it, and deleting your own baseline loses it without hiding
  anything.
- **Legacy signal deleted (was OQ-D3 — closed 2026-08-29)**: this used to be the migration-window
  hole. Before a workspace had a host-side record, the ONLY thing separating "migration → ask" from
  "first run → accept silently" was the presence of `<workspace>/.yolo/config-snapshot.json` — a
  file in the read-write bind mount, i.e. the same bit in the same mount. `27b335ce` removed the
  distinction entirely: the legacy marker is no longer read (presence or content), and a fresh
  workspace with a declared config always prompts. Deleting the legacy file now changes nothing.
  The diagnosis and the ruling live in [`scoped-config-approvals.md`](scoped-config-approvals.md)
  §5.1 (OQ-S3) and this doc's Decision Ledger (OQ-D3).

  > [!NOTE]
  > **The legacy file's content was never adopted, and still must not be** — it is by definition a
  > file the jail could have written, which is the defect OQ-D1 closed. The repair that landed
  > (prompt on a fresh workspace's declared config) is safe precisely because it trusts nothing from
  > the mount; the repair that looked obvious (adopt the legacy content as a baseline to skip a
  > prompt) would have carried OQ-D1's hole across the change meant to end it.
- **Multiple agents**: All share the same config file. If two agents modify
  it, the human sees all changes combined in one diff.

---

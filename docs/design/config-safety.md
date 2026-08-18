---
title: "Config Safety: User/Agent Workflow"
date: 2026-08-18
status: accepted
tags: [config, security, agents, approval]
summary: "Agents may edit the workspace config; humans must approve the change before it takes effect. The snapshot-and-diff flow that implements this, and the two rulings that close the holes in it: the snapshot moves out of the jail's reach, and a non-interactive launch stops accepting silently."
---

# Config Safety: User/Agent Workflow

**Status:** DECIDED 2026-08-18. The flow below is built; **two rulings are not yet implemented** —
the snapshot still lives in the workspace, and a non-interactive launch still auto-accepts.

**The short version.** An agent may edit the workspace config; a human must approve the change before
it takes effect, via a diff and a prompt at the next launch. Two things made that promise weaker than
it reads, and both are now ruled: the record of what was approved lived somewhere the agent could
rewrite, and a launch with no terminal accepted silently.

**Reads with:** [`loophole-activation.md`](loophole-activation.md) — OQ-A13 makes this diff the
disclosure for enabling a host-reaching loophole, which is what turned OQ-D1 from housekeeping into a
safety property.

---

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| **OQ-D1** | The approval snapshot **moves host-side**, out of the rw bind mount — a record the jail can rewrite is not a record | 2026-08-18 | [§ File Locations](#file-locations) |
| **OQ-D2** | Non-interactive + changed config is **fatal**; CI opts in with an explicit flag rather than an implicit yes | 2026-08-18 | [§ User Responses](#user-responses) |

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
  `<workspace>/.yolo/config-snapshot.json`
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
  nobody can be asked about is not a change that may take effect silently.

> **RULED (OQ-D2, 2026-08-18): non-interactive + changed config is fatal, and CI opts in explicitly.**
> This reverses the behaviour described above, which auto-accepted and rewrote the snapshot
> (`config/snapshot.go:38`). Auto-accept made Design Goal 2 conditional on someone happening to have
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
the every-launch `config-snapshot.json`. `drift` re-reads the live config in-jail
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
| `<workspace>/.yolo/config-snapshot.json` | Last-approved normalized config — **moving host-side, see below** |

> **RULED (OQ-D1, 2026-08-18): the snapshot moves out of the jail's reach**, into per-workspace
> host-side state the jail never mounts.
>
> As written, the table above defeats Design Goal 2: `/workspace` is bind-mounted **read-write**, so
> whatever edits `yolo-jail.jsonc` can also rewrite the only record of what was last approved, and
> the next launch shows nothing to approve. Note the shape of the defect — **deleting** the snapshot
> fails safe (an absent baseline diffs against empty and prompts), so what is needed is *integrity*,
> not secrecy, which is why signing it or mounting it `:ro` are both more machinery than the problem
> requires.
>
> The move costs nothing at runtime: the prompt already runs host-side in the launcher
> (`internal/cli/run/preflight.go`), so the jail never reads this file. Its one real cost is that a
> workspace copied or moved elsewhere loses its approval baseline and re-prompts — which is the
> direction to fail in.
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
- **Multiple agents**: All share the same config file. If two agents modify
  it, the human sees all changes combined in one diff.

---

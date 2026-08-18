---
title: "Config Safety: User/Agent Workflow"
date: 2026-08-18
status: in-review
tags: [config, security, agents, approval]
summary: "Agents may edit the workspace config; humans must approve the change before it takes effect. The snapshot-and-diff flow that implements this, and the one open question about where the snapshot may live."
---

# Config Safety: User/Agent Workflow

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
- **Non-interactive** (piped stdin): Changes are accepted with a warning
  message. This allows CI and scripted workflows to function.

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
| `<workspace>/.yolo/config-snapshot.json` | Last-approved normalized config |

## Edge Cases

- **No config file**: Empty config `{}` is snapshotted. Adding a config later
  triggers a diff.
- **User config changes**: Since the snapshot stores the merged result,
  changes to user-level config also trigger a diff.
- **Config deleted**: Triggers a diff (previous config → empty config).
- **Multiple agents**: All share the same config file. If two agents modify
  it, the human sees all changes combined in one diff.

---

## Open Questions

1. 💬 **OQ-D1 — may the approval snapshot live inside the workspace, where the jail can rewrite it?**

   **Design Goal 2 above says *"Humans MUST approve changes — no silent config modifications."* The
   File Locations table defeats it.** The snapshot is the *only* record of what was last approved, it
   lives at `<workspace>/.yolo/config-snapshot.json` (`config/snapshot.go:18-20`), and `/workspace` is
   bind-mounted **read-write** into the jail. So an agent that edits `yolo-jail.jsonc` can rewrite the
   baseline the diff is computed against, and the next launch shows nothing to approve.

   **Deleting the snapshot fails safe** — an absent baseline diffs against empty and prompts. The
   defect is specifically **rewriting** it to match, so what is needed is integrity, not secrecy.

   **What it decides, and why it stopped being small.** This was one clause in `roadmap.md`'s
   catch-all list until [`loophole-activation.md`](loophole-activation.md) OQ-A13 ruled that a
   workspace may turn a host-reaching loophole **on**, with this diff as the disclosure. That ruling
   explicitly declines to bank the disclosure as a safety property *because of this question*. So the
   answer here decides whether the enable-direction disclosure is a control or only a courtesy — and
   with it, whether R5's "enable is either scope" is safe as written.

   | | Option | Cost |
   | :--- | :--- | :--- |
   | **(a)** | **Move the snapshot host-side**, into per-workspace state the jail never mounts | Nothing functional — the prompt already runs host-side in the launcher (`preflight.go`), so the jail never reads it. A workspace copied elsewhere loses its baseline and re-prompts, which fails safe |
   | **(b)** | **Keep it in `.yolo/` but bind that one file `:ro`** | `.yolo/` also holds files the jail legitimately writes (`boot.log`, `startup.log`), so this needs a single-file mount rather than a directory one |
   | **(c)** | **Sign it** with a host-held key so tampering is detectable | Key management for a property (a) gets structurally |
   | **(d)** | **Accept it**, and say in this doc that the gate is advisory against a cooperative agent rather than a control against a hostile one | Free, and it makes Design Goal 2 false as written — which is worse than the current silence, because the goal is what a reader trusts |

   _Leaning:_ **(a).** The snapshot is host-side logic that happens to be stored in the guest. Moving
   it costs nothing at runtime, needs no crypto and no mount gymnastics, and puts the record where
   every other host-authoritative fact already lives. Its one real cost — a workspace moved or copied
   loses its approval baseline — resolves to an extra prompt, which is the direction to fail in.

   **Answer:**
   > _(empty — fill in when decided)_

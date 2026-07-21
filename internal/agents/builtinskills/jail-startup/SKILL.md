---
name: jail-startup
description: First-run skill for agents entering a YOLO Jail. Reads the handover document left by the outer agent and orients you to the jail environment. Invoke this skill immediately when starting a new session inside a jail.
---

# Jail Startup

You are running inside a **YOLO Jail** — an isolated container environment.
This skill helps you pick up where the previous (outer) agent left off.

## Step 1: Read the Handover Document

The outer agent was REQUIRED to write a handover document before you were
launched. Read it now:

**Primary location:** `.yolo/handover.md` (i.e., `/workspace/.yolo/handover.md`)

If it exists, read it carefully — it contains:
- What the outer agent was working on
- What remains to be done
- Key decisions and rationale
- Files to look at first
- Gotchas and context you need

If the file does NOT exist, tell the human:
> "No handover document found at `.yolo/handover.md`. The outer agent should
> have created one. Can you tell me what I should be working on?"

## Step 2: Orient Yourself

The `# YOLO Jail Environment` briefing you already loaded covers the environment
(workspace, home, network, blocked tools, loopholes). The handover tells you the
task. Two more on-demand skills are staged for you: **configuring-the-jail**
(before editing `yolo-jail.jsonc`) and **diagnosing-the-jail** (when a command
misbehaves).

## Step 3: Execute

After reading the handover document, proceed with the tasks described in it.
You have full capability — treat this as your primary working environment.

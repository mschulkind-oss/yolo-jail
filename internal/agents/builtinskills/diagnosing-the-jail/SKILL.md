---
name: diagnosing-the-jail
description: Diagnose a broken jail: provisioning failed, a tool is blocked/shimmed, a loophole is down, or a fix isn't taking effect (stale code). Use when a command errors unexpectedly inside the jail.
---

# Diagnosing the Jail

When something inside the jail misbehaves, work this triage tree top to bottom.
Each step points at a live command — run its `--help` for detail rather than
guessing flags.

## 1. Validate the config first

```
yolo check --no-build
```

This is the fast in-jail preflight: it validates `yolo-jail.jsonc` and the
entrypoint without rebuilding the image. A `[FAIL]` here explains most
"why won't it start / why did provisioning break" symptoms. Fix reported
failures before anything else.

## 2. Provisioning failed → read the startup log

If tools the project expects are missing, the last boot's provisioning may have
failed. Look for `PROVISIONING FAILED` in:

```
/workspace/.yolo/startup.log
```

Self-serve from there: e.g. run `mise install` in `/workspace`, then re-run the
step that failed. (The briefing shows a `⚠ Provisioning failed` banner on the
next attach after a failed boot.)

## 3. A tool is blocked or shimmed

Some tools are intentionally shimmed (e.g. `grep -r` → use `rg`, `find` → use
`fd`). If a command errors oddly or is "not found the way you expect":

- List the active shims: `ls ~/.yolo-shims/`
- Run the real tool for a script/installer that needs it:
  `YOLO_BYPASS_SHIMS=1 <cmd>`
- **The `rg -r` trap:** in `rg`, `-r` means `--replace` and silently corrupts
  match output. Never pass grep-style `-r`/`-rn`; use `rg -n <pattern> [path]`.

## 4. A loophole (host capability) is down

Loopholes wire host capabilities (audio, claude-oauth-broker, host-processes)
into the jail. If one isn't working:

```
yolo loopholes list      # what's enabled + status
yolo broker status       # for the claude-oauth-broker loophole specifically
yolo broker logs         # recent broker output
```

## 5. "My fix isn't taking effect" → you're running stale code

Config edits refresh on the next `yolo` invocation, but the running container's
mounts/limits do NOT change until a restart — the briefing text can be ahead of
reality. And in the yolo-jail source repo, a nested jail reuses the *current*
baked image, not your freshly built one (see the **developing-yolo-jail** skill
for the build/load split). Re-run `YOLO_DEBUG=1 <cmd>` for verbose output when a
command behaves unexpectedly.

## 6. Orphans and logs

- `yolo ps` — list running jails (catches orphaned containers).
- Agent logs, for debugging a specific agent:
  `~/.copilot/logs/`, `~/.cache/gemini-cli/logs/`, `~/.claude/projects/`.

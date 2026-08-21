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

- List the active blockers: `ls ~/.yolo-shims/` (first on PATH — these are the
  refusals). Lazy-install launchers are a different mechanism in a different
  dir: `ls ~/.yolo-launchers/` (last on PATH, after `/bin`, so one is only
  reached when nothing else provides the name).
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

## 5. A port isn't reachable → establish the DIRECTION first

Half the time this is not a broken service, it is the wrong key or a transposed
entry. **`network.ports` is `"HOST:JAIL"`, `network.forward_host_ports` is
`"JAIL:HOST"` — opposite orders.** And there are two loopbacks: `localhost`
inside the jail is the *jail's*, never the host's.

**Jail → host** (you are in here, the service is out there). It should work with
no config at all: `host.containers.internal` reaches the host, including services
bound only to the host's `127.0.0.1`.

```
echo $YOLO_HOST_LOOPBACK     # requested | shared → forwarding is in place
                             # unsupported | unknown → it may genuinely be absent
(exec 3<>/dev/tcp/host.containers.internal/<port>) && echo CONNECT || echo FAIL
```

`requested` plus a service that does not answer is a **fault**, not a
limitation — the jail-facing services yolo itself ships would have refused the
launch — so suspect the service, its bind address, or its port. Only if something
must literally see `localhost:<port>` in here do you need
`network.forward_host_ports`; check that entry reads jail-port-first.

**Host → jail** (your server is in here and the host's browser can't load it).
Two causes, in this order:

1. **It's bound to the jail's loopback.** `network.ports` cannot publish a
   `127.0.0.1` listener. Confirm with `ss -ltnp` — you want `0.0.0.0:<port>`,
   not `127.0.0.1:<port>` — and rebind. Most dev servers need an explicit flag
   (`--host 0.0.0.0`). Binding wide inside the jail is safe.
2. **The mapping is inverted or absent.** `"HOST:JAIL"`, host side first, and it
   is applied at launch — adding it to the config does nothing until a restart.

```
ss -ltnp                                    # what is actually listening, and where
(exec 3<>/dev/tcp/127.0.0.1/<port>) && echo LISTENING-IN-JAIL || echo NOT-LISTENING
```

That second probe splits the two: if the server does not even answer inside the
jail, no port mapping was ever going to help.

## 6. "My fix isn't taking effect" → you're running stale code

Config edits refresh on the next `yolo` invocation, but the running container's
mounts/limits do NOT change until a restart — the briefing text can be ahead of
reality. And in the yolo-jail source repo, a nested jail reuses the *current*
baked image, not your freshly built one (see the **developing-yolo-jail** skill
for the build/load split). Re-run `YOLO_DEBUG=1 <cmd>` for verbose output when a
command behaves unexpectedly.

## 7. Orphans and logs

- `yolo ps` — list running jails (catches orphaned containers).
- Agent logs, for debugging a specific agent:
  `~/.copilot/logs/`, `~/.claude/projects/`.

---
title: "RUNBOOK — the Claude credential view's measures, M1 to M11 and H1 to H5, on a real host"
status: accepted
stage: CURRENT
next: "The maintainer runs Part A on the host (M1, M2 and M4 to M6 against a hand-made view), then Parts B and C"
date: 2026-09-29
tags: [runbook, host, claude, credentials, oauth, broker, measures]
summary: "The measures the Claude credential view owes before the interception can be deleted, written as one sitting's procedure for the maintainer: Part A runs M1, M2 and M4 to M6 in an ordinary interception jail against a hand-made view, with the terminator's log as the witness for any token request; Part B turns the view on for one throwaway workspace and runs M3 and M7 to M10 against the broker's own rewrites; Part C exercises /login, /logout and yolo claude-auth logout; Part D is the Mac measure M11 and the day on a rootless host; Part H runs the same switch at `yolo host -- claude` (H1 to H5, CL-D27). Nothing here prints a token."
---

# RUNBOOK — the Claude credential view's measures, M1 to M11 and H1 to H5, on a real host

**Status:** A procedure. None of it has run yet. It is what
[OQ-CL1](../../design/claude-login-without-interception.md#OQ-CL1)'s ruled order puts between the
built view and the deletion of the interception: the measures pass, the view runs a day on a real
rootless host, then the interception goes ([CL-D7](../../design/claude-login-without-interception.md#CL-D7)).

That none of it has run was last checked on 2026-09-30, against the host broker's log, which
records no view write.

**Audience:** the maintainer, on the **host**. No automated test may run any of this: every step
needs a real Claude Code session on a real login ([`AGENTS.md`](../../../AGENTS.md#testing), "No
agent tests").
**Time:** about ninety minutes for Parts A to C; Part D is a Mac session and a day of ordinary use.
**Needs:** a host `yolo` built from this commit or later (`just install`), a working Claude
subscription login on the machine, and `jq` (it is in the jail image).
**Writes:** two throwaway workspaces under `~/tmp`. Part A replaces one jail's credential link with
a file and Part C signs this machine out once; each part says how to put things back.

## Terms

- **Credential view** (the design's term) — a per-workspace `~/.claude/.credentials.json` the host
  broker writes: the current access token and its real expiry, and no refresh token
  ([§2](../../design/claude-login-without-interception.md#2-terms)).
- **Hand-made view** *(coined here)* — a view you make yourself, in an interception jail, by
  deleting the refresh token from a copy of the shared file. Part A uses one so the terminator's
  log can witness whether Claude ever asks for a token.
- **The switch** — `YOLO_CLAUDE_CREDENTIAL_VIEW`, read by the host launcher
  ([CL-D10](../../design/claude-login-without-interception.md#CL-D10)). `1` turns the view on for a
  launch; on podman it is off unless set.
- **Fingerprint** — the first 8 hex digits of a token's SHA-256, which `yolo claude-auth` prints in
  place of the token (the broker's `TokenFP`). Two fingerprints differ when the tokens differ.

## Rules for the whole sitting

- **Never print a token.** Read credential files only through `yolo claude-auth inspect <file>`
  (field names, expiry, fingerprints) or `jq 'keys'`. Every `jq` below writes to a file, never to
  the terminal.
- **Two logs are the witnesses:**
  - In a jail, the terminator's log, `~/.local/state/yolo-jail-daemons/claude-oauth-broker.log`.
    Every request to the token endpoint leaves a line ending `is_refresh=True` or
    `is_refresh=False`.
  - On the host, the broker's log, `~/.local/share/yolo-jail/logs/host-service-claude-oauth-broker.log`
    (or `yolo host-daemon logs claude-oauth-broker`). It logs each refresh, each view write
    (`view: wrote`), each enrollment and each `/logout`, with fingerprints.
- **Record each result** in the table at the end, with the Claude Code version the jail ran
  (`claude --version`).

## Part A — M1, M2, M4, M5 and M6, in an interception jail against a hand-made view

This is how [§7](../../design/claude-login-without-interception.md#7-what-must-be-measured-before-building)
means them to run: the interception is still in place, so any request Claude makes to the token
endpoint reaches the terminator and is logged.

### A0. Set up

1. On the host, give the machine a fresh access token first, so the hand-made view has hours to
   live and no background refresh interrupts the part:

   ```console
   $ yolo claude-auth refresh
   $ yolo claude-auth status
   ```

   **Observe:** the canonical login's `refresh token: PRESENT` and `expires: … (in 7h5…)`.
2. Launch an ordinary jail in a throwaway workspace. Leave the switch unset:

   ```console
   $ mkdir -p ~/tmp/cv-a && cd ~/tmp/cv-a && yolo -- bash
   ```

3. In the jail, replace the credential link with a hand-made view:

   ```console
   $ ls -l ~/.claude/.credentials.json            # a link to ../.claude-shared-credentials/…
   $ jq 'del(.claudeAiOauth.refreshToken)' ~/.claude/.credentials.json > /tmp/cv-view.json
   $ rm ~/.claude/.credentials.json && install -m 600 /tmp/cv-view.json ~/.claude/.credentials.json
   $ cp ~/.claude/.credentials.json /tmp/cv-good.json
   $ yolo claude-auth inspect ~/.claude/.credentials.json
   ```

   **Observe:** a regular file, `refresh token: absent`, and `scopes`, `subscriptionType` and
   `rateLimitTier` listed.
4. Note the terminator log's length, so each measure can read only its own lines:

   ```console
   $ LOG=~/.local/state/yolo-jail-daemons/claude-oauth-broker.log; wc -l < $LOG
   ```

### M1. A view with no refresh token counts as logged in

```console
$ claude -p 'Reply with the single word: ok'
$ tail -n +<the count from A0.4> $LOG | grep -c 'oauth/token'
```

**Pass:** Claude prints `ok`, and the count is `0`. A success on a subscription access token is the
OAuth path; the beta header is not visible from here without a proxy, so the design's "with the
OAuth beta header" is inferred from the success rather than observed.

### M2. An expired view gets no refresh attempt, and fails loud

```console
$ jq '.claudeAiOauth.expiresAt = 0' /tmp/cv-good.json > /tmp/cv-expired.json
$ install -m 600 /tmp/cv-expired.json ~/.claude/.credentials.json
$ claude -p 'Reply with the single word: ok'; echo "rc=$?"
$ tail -n +<count> $LOG | grep -c 'oauth/token'
$ yolo claude-auth inspect ~/.claude/.credentials.json
$ install -m 600 /tmp/cv-good.json ~/.claude/.credentials.json     # put the good view back
```

**Pass:** no `oauth/token` line; Claude reports the login as expired (or asks for `/login`) rather
than crashing; and `inspect` shows the same access-token fingerprint as before, so Claude did not
blank the file. Record the exact message.

### M4. A 401 on a wrong access token recovers from the file

This needs Claude to hold the wrong token in memory while the file already holds the right one.
Claude re-reads the file only when its modification time changes
([F5](../../design/claude-login-without-interception.md#F5)), so the correction keeps the wrong
file's time.

```console
$ jq '.claudeAiOauth.accessToken = "sk-ant-oat01-deliberately-wrong"' /tmp/cv-good.json > /tmp/cv-wrong.json
$ install -m 600 /tmp/cv-wrong.json ~/.claude/.credentials.json
$ claude                                     # interactive; leave it open
```

1. Send `say ok`. **Expect:** an authentication error (Claude read the wrong token).
2. In a second shell in the same jail (`yolo -- bash` from `~/tmp/cv-a` attaches), restore the good
   bytes under the wrong file's time:

   ```console
   $ touch -r ~/.claude/.credentials.json /tmp/cv-stamp
   $ install -m 600 /tmp/cv-good.json ~/.claude/.credentials.json && touch -r /tmp/cv-stamp ~/.claude/.credentials.json
   ```

3. Send `say ok` again in the open session.

**Pass:** the second prompt is answered with no restart and no `/login`. The design's reading is
that the 401 handler re-reads the store, finds a different token and retries
(`tengu_oauth_401_recovered_from_keychain`). If step 1 already succeeded, Claude never used the
wrong token; record that and treat M4 as not exercised.

### M5. /login in a view jail writes a credential that carries a refresh token

⚠ In Part A the interception is on, so this `/login` also re-enrolls the machine through the proxy
mirror, exactly as a jail `/login` does today. Do it only if a fresh login is acceptable.

```console
$ claude            # then /login, and complete the manual paste in the host browser
$ yolo claude-auth inspect ~/.claude/.credentials.json
$ tail -n +<count> $LOG | grep 'oauth/token'
```

**Pass:** `inspect` shows `refresh token: PRESENT`: Claude wrote a whole credential into the view,
which is what [CL-D4](../../design/claude-login-without-interception.md#CL-D4)'s enrollment adopts.
The log shows one `is_refresh=False` line (the code exchange) and no `is_refresh=True` one.

### M6. /status and /usage show the subscription on a view

Put the view back first if M5 ran (repeat A0.3), then in an interactive `claude`, run `/status` and
`/usage`.

**Pass:** `/status` names the subscription (Team, Max or Pro, not "Claude API"), and `/usage` shows
the plan's usage.

### A9. Put Part A back

```console
$ rm ~/.claude/.credentials.json        # in the jail; the next boot's hook relinks it
$ exit
$ yolo stop                             # on the host, in ~/tmp/cv-a
```

## Part B — the switch on, end to end: M3, M7, M8, M9 and M10

### B0. Set up

```console
$ mkdir -p ~/tmp/cv-b && cd ~/tmp/cv-b
$ YOLO_CLAUDE_CREDENTIAL_VIEW=1 yolo -- bash
```

**Observe at the launch:** no `could not register this workspace's Claude credential view`
warning. Then check that the interception is really gone and the view is really there:

```console
$ getent hosts platform.claude.com                  # a public address, NOT 127.0.0.1
$ env | grep -c NODE_EXTRA_CA_CERTS                  # 0
$ ls ~/.local/state/yolo-jail-daemons/ | grep -c claude-oauth-broker   # 0: no terminator
$ ls -l ~/.claude/.credentials.json                  # a regular file, not a link
$ yolo claude-auth inspect ~/.claude/.credentials.json   # refresh token: absent
```

And on the host:

```console
$ yolo claude-auth status        # the ~/tmp/cv-b view listed as `live`, with the canonical's fingerprint
```

### M3. A rewritten view reaches an idle session with no restart

1. In the jail, start `claude` and send `say ok`. Leave it idle.
2. On the host: `yolo claude-auth refresh`. **Observe** in the broker log: `refreshed by`,
   then `view: wrote …/cv-b/.yolo/home/claude/.credentials.json`.
3. In a second jail shell: `yolo claude-auth inspect ~/.claude/.credentials.json` shows the new
   access-token fingerprint.
4. Send `say ok` in the idle session.

**Pass:** answered, with no restart and no `/login`.

### M7. The revocation window, at view-rewrite delays of 0, 2 and 10 seconds

A refresh appears to revoke the access token it replaces at once
([CL-D12](../../design/claude-login-without-interception.md#CL-D12)). So a jail is broken from the
upstream refresh until its view's rename lands, and recovery from the 401 is a main path, not a
fallback. `--view-delay` holds the view writes back for the given time, under the broker's lock.

For each delay `D` in `0s`, `2s` and `10s`, run it twice:

1. **Idle:** with `claude` open and idle, run `yolo claude-auth refresh --view-delay D` on the
   host, and send `say ok` in the jail **while the command is still running**.
2. **Mid-stream:** send `Count from 1 to 300, one number per line`, and while it streams, run
   `yolo claude-auth refresh --view-delay D`.

**Pass, each time:** the session recovers on its own once the view lands: no "Login expired", no
`/login`, and `inspect` afterwards shows the new fingerprint (the view was not blanked). Record, per
delay, whether the prompt failed first and then worked, or worked at once.

### M8. Does an open stream survive a refresh?

Unknown today. Send `Count from 1 to 2000, one number per line`, and while it streams, run
`yolo claude-auth refresh` (no delay).

**Record:** whether the stream finishes; if it breaks, the error text, and whether the next prompt
works with no restart. There is no pass or fail: the answer decides whether the broker should avoid
refreshing while a jail is streaming, which nothing does today.

### M9. An MCP login survives a view rewrite

The credentials file also holds Claude's MCP servers' OAuth (`mcpOAuth`), and the broker rewrites
only `claudeAiOauth`, under Claude's own `.storage-write` lock
([CL-D13](../../design/claude-login-without-interception.md#CL-D13)).

1. In the jail, add a remote MCP server that signs in with OAuth (any hosted one you use), and
   authenticate it with `/mcp`.
2. `yolo claude-auth inspect ~/.claude/.credentials.json`: the top-level keys include `mcpOAuth`.
3. On the host: `yolo claude-auth refresh`.
4. `inspect` again, then `/mcp` in the session, then `yolo claude-auth status` on the host.

**Pass:** `mcpOAuth` is still there, the access-token fingerprint is the new one, `/mcp` shows the
server still authenticated, and `status` does **not** show `~/tmp/cv-b` as signed out. The last one
matters: an MCP save that wrote the file without `claudeAiOauth` would read, to the broker, as the
jail's `/logout` (claude-code#45551's class).

### M10. A background session picks up a rewrite

Start a background session in the jail (`claude --bg '<a small task>'`, or one from
`claude agents`; check the installed version's `claude --help` for the spelling). On the host run
`yolo claude-auth refresh`, then give the background session a second task.

**Pass:** the second task runs with no "Login expired". The supervisor hands workers a credential
snapshot, which is why this is measured separately from M3.

## Part C — /login, /logout and the machine sign-out, with the switch on

These are [OQ-CL2](../../design/claude-login-without-interception.md#OQ-CL2)'s rulings, built as
[CL-D4](../../design/claude-login-without-interception.md#CL-D4) and
[CL-D14](../../design/claude-login-without-interception.md#CL-D14).

### C1. A jail's /login enrolls the machine

In the `~/tmp/cv-b` jail, run `/login` in `claude`. Within a minute (the broker's tick):

- the broker log shows `enrollment: … carries a refresh token`, then `adopted the /login in …`;
- `yolo claude-auth inspect ~/.claude/.credentials.json` in the jail shows `refresh token: absent`
  again and a new access-token fingerprint;
- `yolo claude-auth status` on the host shows a new canonical refresh-token fingerprint.

**Pass:** all three, and `say ok` still works in the session with no restart.

### C2. A jail's /logout signs out that workspace only, until its next launch

1. Launch a second view workspace: `mkdir -p ~/tmp/cv-c && cd ~/tmp/cv-c && YOLO_CLAUDE_CREDENTIAL_VIEW=1 yolo -- bash`.
2. In the `~/tmp/cv-b` jail, run `/logout` in `claude`. Within a minute the broker log shows
   `… was signed out by /logout in its jail`.
3. On the host: `yolo claude-auth refresh`, then `yolo claude-auth status`.

**Pass:** `status` shows `~/tmp/cv-b` as `SIGNED OUT …` and `~/tmp/cv-c` as `live` with the new
fingerprint; `claude -p ok` works in `~/tmp/cv-c`. Then exit and relaunch `~/tmp/cv-b` with the
switch: the launch prints `/logout in this workspace's jail had signed it out of the machine's
login; this launch signs it back in`, and `claude -p ok` works there again.

### C3. yolo claude-auth logout signs the machine out

⚠ This signs every workspace and every jail out until the next `/login`.

```console
$ yolo claude-auth logout
$ yolo claude-auth status
```

**Pass:** the canonical is gone, the shared file holds no login, and each view shows
`no claudeAiOauth login`. A `/login` in either view jail then re-enrolls the machine (C1), and the
other workspace gets the login within a minute.

### C9. Put Parts B and C back

`yolo stop` in `~/tmp/cv-b` and `~/tmp/cv-c`, then `rm -rf ~/tmp/cv-a ~/tmp/cv-b ~/tmp/cv-c`. A
deleted workspace's registration is dropped by the broker's next tick.

## Part D — the Mac, and the day on a rootless host

### M11. macos-user: does the sandbox's Claude read the view?

The view is off by default on `macos-user`
([CL-D11](../../design/claude-login-without-interception.md#CL-D11)), because Claude on macOS keeps
its login in the Keychain first and the file only as a fallback, and which one a sandbox account's
Claude reads is unmeasured. On a Mac with the backend set up:

```console
$ mkdir -p ~/tmp/cv-mac && cd ~/tmp/cv-mac
$ YOLO_CLAUDE_CREDENTIAL_VIEW=1 YOLO_RUNTIME=macos-user yolo -- claude -p 'Reply with the single word: ok'
$ yolo claude-auth status
```

If `status` shows no canonical login, run `/login` once in a sandbox `claude` first (C1): on this
backend the broker's migration source is not the sandbox's own shared file.

**Pass:** `ok`, a view listed as `live`, and after `yolo claude-auth refresh` the next
`claude -p ok` still works. **Fail signature:** "Please run /login" while `status` shows a fresh
view: Claude is reading the Keychain, and `macos-user` stays off.

### The day on a real rootless host

Export `YOLO_CLAUDE_CREDENTIAL_VIEW=1` in the shell you launch jails from, and work normally for a
day, including one workspace with `network.mode: "host"` (the case the interception cannot serve,
[OQ-NC2](../notch-convergence.md#OQ-NC2)).

**Record** `podman info --format '{{.Host.Security.Rootless}} {{.Host.RootlessNetworkCmd}}'`.
**Pass:** at least two background refreshes (`bg_refresh: ok` in the broker log), each followed by a
`view: wrote` line for every open workspace; no "Login expired" in any session; and two
host-networked jails running Claude at once.

## Part H — `yolo host -- claude` with the switch on: H1 to H5

The switch reaches the host notch since 2026-10-04
([CL-D27](../../design/claude-login-without-interception.md#CL-D27)): with
`YOLO_CLAUDE_CREDENTIAL_VIEW=1`, `yolo host -- claude` registers a view in a store yolo manages,
`~/.local/share/yolo-jail/host-agents/claude`, and points Claude at it with
`CLAUDE_SECURESTORAGE_CONFIG_DIR`. Your own `~/.claude` is never written. Run Part H after Part C,
so the machine has a login.

### H0. Set up

```console
$ mkdir -p ~/tmp/cv-h && cd ~/tmp/cv-h
$ yolo claude-auth inspect ~/.claude/.credentials.json > ~/tmp/cv-h-own-before.txt
```

On a Mac your own login may live in the Keychain and the file may be absent: then the file's
absence is what H2 compares.

### H1. The launch says what it does, and Claude is logged in

```console
$ YOLO_CLAUDE_CREDENTIAL_VIEW=1 yolo host -- claude -p 'Reply with the single word: ok'
$ yolo claude-auth status
```

**Pass:** the launch prints `claude reads the machine's shared Claude login from a store yolo
manages, ~/.local/share/yolo-jail/host-agents/claude`; the answer is `ok`; and `status` lists that
store's view with its runtime as `host`, marked as `yolo host --`'s store, `live`, and with
`refresh token: absent`.

### H2. Your own login is untouched

```console
$ yolo claude-auth inspect ~/.claude/.credentials.json > ~/tmp/cv-h-own-after.txt
$ diff ~/tmp/cv-h-own-before.txt ~/tmp/cv-h-own-after.txt
```

**Pass:** no difference: the same mode, modification time and fingerprints.

### H3. A rewritten view reaches an idle host session

Start `YOLO_CLAUDE_CREDENTIAL_VIEW=1 yolo host -- claude` and send `say ok`. In a second terminal
run `yolo claude-auth refresh`, and look for `view: wrote …/host-agents/claude/.credentials.json`
in the broker log. Send `say ok` again.

**Pass:** the second answer comes with no restart and no "Login expired", as M3 asks of a jail.

### H4. /login and /logout in a host session

With the machine signed out (`yolo claude-auth logout`, C3), start a host session with the switch:
it prints that the machine has no shared Claude login yet and that `/login` in the session enrolls
it. Run `/login`. Then run `/logout`, exit, and launch again.

**Pass:** the broker log shows `enrollment: … adopted the /login in …/host-agents/claude/…`
within a minute of the `/login`, then a line saying the store was signed out by `/logout` in a
`yolo host` session; the relaunch prints that this launch signs the store back in, and
`claude -p ok` works.

### H5. Without the switch nothing changed, and the Mac's fail signature

```console
$ yolo host -- claude -p 'Reply with the single word: ok'
```

**Pass:** no line about a store yolo manages, and Claude answers on your own login. **On a Mac,
the fail signature for H1:** "Please run /login" while `status` shows a fresh host view means Claude
read a Keychain entry named for the moved store rather than the file, and the host view stays off
on macOS.

### H9. Put Part H back

`rm -rf ~/tmp/cv-h ~/tmp/cv-h-own-*.txt`. The store's registration goes when its directory does:
`rm -rf ~/.local/share/yolo-jail/host-agents/claude`, and the broker drops it on its next tick.

## Results

| # | Date | Claude Code | Result | Notes |
| :- | :- | :- | :- | :- |
| M1 | | | | |
| M2 | | | | |
| M3 | | | | |
| M4 | | | | |
| M5 | | | | |
| M6 | | | | |
| M7 (0s / 2s / 10s) | | | | |
| M8 | | | | |
| M9 | | | | |
| M10 | | | | |
| M11 | | | | |
| C1 to C3 | | | | |
| H1 to H5 (host) | | | | |
| A day, rootless | | | | |

**What a full pass licenses:** turning the switch on for podman, then
[CL-D7](../../design/claude-login-without-interception.md#CL-D7)'s deletion, which removes the
switch with the interception. **What a failure means:** stop, and re-read the design's
[§4](../../design/claude-login-without-interception.md#4-the-options-that-remove-the-hosts-entry)
with the failure in hand; the research's named fallback is a shared
`CLAUDE_SECURESTORAGE_CONFIG_DIR`.

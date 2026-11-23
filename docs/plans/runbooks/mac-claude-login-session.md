---
status: accepted
stage: CURRENT
next: "Ran 2026-10-24/25; results below. The repairs are built (mac-claude-lab-followups-plan.md); the native confirmation steps there are what remains"
date: 2026-10-24
tags: [runbook, macos-user, claude, credentials, oauth, broker, measures, mac-agent]
summary: "One Mac session for the maintainer's Mac agent: the pending pack-tree integration check, then whether macos-user's Claude reads the credential view (M11), whether a running session adopts a refreshed view and recovers from a wrong token, the same at `yolo host -- claude` (H1 to H3, H5), and the native bridge's lock (Phase A). The agent runs what needs no sign-out, and leads the maintainer through the rest."
---

# One Mac session: does Claude on a Mac read the credential view?

**Status:** 2026-10-24. Ran 2026-10-24 to 25 on the maintainer's Mac; see [Results](#results). **Authorized:** the maintainer asked for this brief on
2026-10-24 so that their Mac agent runs it: *"write something up for my mac agent, it will do what
it can then lead me through the rest."* That is the explicit authorization
[the native refresh design](../../design/macos-user-claude-refresh.md) and
[the synthetic runbook](macos-user-claude-synthetic-measures.md) say real-login trials need,
within the limits in [the rules](#rules-for-the-agent). It is not a test: no automated suite runs
any of this ([`AGENTS.md`](../../../AGENTS.md#testing), "No agent tests").

**Why.** The credential view is a file the host broker writes for each workspace: the current access
token, its real expiry, and **no refresh token**
([terms](claude-credential-view-measures.md#terms)). It is the ruled way for every Claude yolo
starts to share one login without the TLS interception
([OQ-CL1, OQ-CI1](../../design/claude-login-without-interception.md#11-decision-ledger)). It is
off by default on every backend until it is measured ([CL-D11](../../design/claude-login-without-interception.md#CL-D11)).
On a Mac, one question decides it: Claude on macOS keeps its login in the Keychain first, so does
the sandbox's Claude read the file at all? If it does, macos-user's default can flip, and the
account mode, which today has a login of its own, joins the machine's one login without any grant
into your home. Part H asks the same of `yolo host -- claude`.

**How Claude is expected to behave** (read from Claude Code 2.1.284's bundle; inferred, which is
why it is measured): it checks the credentials file's modification time before every token use
and drops its cached credential when the time changes; on a 401 it re-reads the store and retries
with a different token if it finds one; and with no refresh token it never refreshes
([F4, F5](../../design/claude-login-without-interception.md#32-when-claude-refreshes-and-when-it-does-not)).

**Reads with:** [the full measures runbook](claude-credential-view-measures.md), whose Part D
(M11) and Part H this session runs on a Mac;
[the native refresh design](../../design/macos-user-claude-refresh.md) (Phase A);
[the Mac checks](../../design/macos-user-model-plan.md#mac-checks).

---

## Rules for the agent

- **Never print a token.** Use `yolo claude-auth status` and `yolo claude-auth inspect <file>`
  (they print fingerprints: the first 8 hex digits of a token's SHA-256) and `jq 'keys'` for
  structure. Any `jq` that edits a credential file redirects straight into a private file.
- **Never sign the machine out.** No `yolo claude-auth logout`, no `/logout` in any Claude, no
  Part C or H4 of the full runbook. Those log out every jail on the Mac; the maintainer runs them
  in [Step 5](#step-5--with-the-maintainer) if they choose to.
- **No `/login` without the maintainer.** If a step needs one, stop and ask them to do it.
- **Interactive sessions run in tmux,** so one session stays open across host commands:
  `tmux new -d -s cv 'cd <dir> && <launch>'`, then `tmux send-keys -t cv 'say ok' Enter` and
  `tmux capture-pane -pt cv`. Kill each with `tmux kill-session -t cv` when its step ends.
- **Work under `~/tmp/cv-mac*`** and delete it at the end.
- **Record every result** in [the results table](#results) below, with `claude --version` as the
  sandbox ran it. Commit that edit, plus a lab report in `docs/lab-reports/` naming each
  step's pass, fail or skip and the output that decided it, as the 2026-10-24 Mac report did.

## Step 0 — prerequisites

1. The Mac's `yolo` is built from current `main` (`git pull`, then `just deploy`).
2. Restart the broker so it is the new build's:
   `yolo host-daemon restart claude-oauth-broker`. A broker an older yolo started never refreshes
   a `yolo host` view ([CL-D28](../../design/claude-login-without-interception.md#CL-D28)).
3. `yolo claude-auth status` shows a canonical login with at least an hour left. If it shows none,
   stop and ask the maintainer to run `/login` once in a sandbox `claude`: on macos-user the
   broker's migration source is not the sandbox's own shared file.
4. Note which macos-user modes this Mac has set up: the account mode (`_yolojail`) and/or the
   run-as-you mode (`"macos_user_mode": "self"`). Run Steps 1 to 3 in **each** mode set up, and
   record them separately.

The broker's log is the witness for every refresh and view write:
`~/.local/share/yolo-jail/logs/host-service-claude-oauth-broker.log`.

## Step 1 — the pack-tree check still pending from the last report

```console
$ YOLO_TEST_MACOS_USER=1 go test -count=1 -timeout 0 -v -run '^TestMacosUserPackTreeProgram' ./integration
```

**Pass:** the three `/self` subtests pass. What each failure means is in
[the Mac checks](../../design/macos-user-model-plan.md#mac-checks).

## Step 2 — M11: does the sandbox's Claude read the view?

```console
$ mkdir -p ~/tmp/cv-mac && cd ~/tmp/cv-mac
$ YOLO_CLAUDE_CREDENTIAL_VIEW=1 YOLO_RUNTIME=macos-user yolo -- claude -p 'Reply with the single word: ok'
$ yolo claude-auth status
```

**Pass:** the answer is `ok`, and `status` lists this workspace's view as `live` with
`refresh token: absent`. **Fail signature:** "Please run /login" while `status` shows a fresh view.
That means Claude read the Keychain; record it, skip Step 3 for this mode, and go on.

## Step 3 — a running session adopts a changed file

**3a. A refresh reaches an idle session** (M3 on a Mac). Keep one interactive session open:

```console
$ tmux new -d -s cv 'cd ~/tmp/cv-mac && YOLO_CLAUDE_CREDENTIAL_VIEW=1 YOLO_RUNTIME=macos-user yolo -- claude'
```

1. Send `say ok`. Expect an answer.
2. On the host: `yolo claude-auth inspect <the view's path from status>` (note the fingerprint),
   then `yolo claude-auth refresh`. Look for `view: wrote` naming this workspace in the broker log,
   and check that `inspect` now shows a different fingerprint.
3. Send `say ok` again.

**Pass:** the second answer comes with no restart, no "Login expired" and no `/login`. **Caveat:**
this is conclusive only if a refresh revokes the old access token, which is
[sourced, not measured](../../design/claude-login-without-interception.md#6-costs-and-risks). 3b
settles it either way.

**3b. A 401 on a wrong token recovers from the file** (M4 on a Mac). This uses a private copy,
because the broker rewrites the live view within 60 seconds of any edit
([CL-D13](../../design/claude-login-without-interception.md#11-decision-ledger)). Inside a sandbox
shell for the same workspace (`YOLO_CLAUDE_CREDENTIAL_VIEW=1 YOLO_RUNTIME=macos-user yolo -- bash`
from `~/tmp/cv-mac`), with `VIEW` set to the view's path from `status`:

```console
$ CV_DIR=$(mktemp -d ~/tmp/cv-mac/m4.XXXXXX) && chmod 700 "$CV_DIR"
$ cp -- "$VIEW" "$CV_DIR/good.json" && chmod 600 "$CV_DIR/good.json"
$ jq '.claudeAiOauth.accessToken = "sk-ant-oat01-deliberately-wrong"' "$CV_DIR/good.json" > "$CV_DIR/wrong.json"
$ chmod 600 "$CV_DIR/wrong.json" && mv -f "$CV_DIR/wrong.json" "$CV_DIR/.credentials.json"
$ echo "$CV_DIR"
```

Start Claude on it in tmux from that sandbox shell:
`CLAUDE_SECURESTORAGE_CONFIG_DIR="$CV_DIR" claude`.

1. Send `say ok`. **Expect** an authentication error: Claude read the wrong token from the file.
   An answer instead means it read something else (the Keychain): record that as the result.
2. From a second sandbox shell, put the good file back **with the wrong file's modification
   time**, so Claude's change check does not fire and only the 401 path can pick it up:

   ```console
   $ touch -r "$CV_DIR/.credentials.json" "$CV_DIR/stamp"
   $ cp -- "$CV_DIR/good.json" "$CV_DIR/restore.json" && chmod 600 "$CV_DIR/restore.json"
   $ touch -r "$CV_DIR/stamp" "$CV_DIR/restore.json" && mv -f "$CV_DIR/restore.json" "$CV_DIR/.credentials.json"
   ```

3. Send `say ok` again.

**Pass:** the second prompt is answered with no restart and no `/login`. Delete `$CV_DIR` after.

## Step 4 — Part H at `yolo host -- claude`, without signing anything out

Run [Part H](claude-credential-view-measures.md#part-h--yolo-host----claude-with-the-switch-on-h1-to-h5)'s
H0, H1, H2, H3 and H5 as written. They run on the host, not in a sandbox. **Skip H4**: it signs the
machine out. On a Mac your own login may be in the Keychain with no file; then H2 compares the
file's absence. H5's Mac fail signature is "Please run /login" while `status` shows a fresh host
view.

## Phase A — the native bridge's lock (optional, needs PR 62 on `main` for the run-as-you mode)

With the view **off**, two sandbox sessions of two workspaces share one credentials file and
Claude's own lock files through `CLAUDE_SECURESTORAGE_CONFIG_DIR`
([CL-D22](../../design/claude-login-without-interception.md#CL-D22)). In the run-as-you mode that
variable named a directory that does not exist until
[PR 62](https://github.com/mschulkind-oss/yolo-jail/pull/62) landed. Skip this phase in that mode
if it has not. Then:

1. Open one interactive `YOLO_RUNTIME=macos-user yolo -- claude` in each of `~/tmp/cv-mac-a` and
   `~/tmp/cv-mac-b`, and send `say ok` in both.
2. Record the store each one uses: `yolo claude-auth inspect` on the shared file before and after,
   and whether the file's modification time moves when a session starts.
3. Ask the maintainer whether to force a refresh in one session now; it spends the machine's
   refresh token from inside a sandbox. If yes, `yolo claude-auth status` before and after.
4. Send `say ok` in both again. **Pass:** both answer, and neither asks for `/login`.

## Step 5 — with the maintainer

Lead the maintainer through what the agent did not run, one item at a time, and record each:

1. Anything above that stopped for a `/login` or a decision.
2. **Optionally,** H4 of the full runbook (`/login` and `/logout` in a host session) and its
   Part C. Both sign this machine out once. Say that before starting, and let them decline.

## Results

| Step | Mode | Date | Claude Code | Result | Notes |
| :--- | :--- | :--- | :--- | :--- | :--- |
| 1 | both | 2026-10-24 | — | **pass** | All six `TestMacosUserPackTreeProgram` subtests pass (`/self` ×3 included) at `9a1e0d9c9`. |
| 2 (M11) | account | 2026-10-24 | 2.1.310 | **pass** | `ok`; the view is `live`, `refresh token: absent`, and its fingerprint is the canonical login's. |
| 2 (M11) | self | 2026-10-24 | 2.1.304 | **fail as shipped; pass with two workarounds** | Claude exits on `EPERM … mkdir '/tmp/claude-501'`. With `CLAUDE_CODE_TMPDIR=$TMPDIR` it says `Not logged in`, because the view is written to `.yolo/home/claude/` and Claude reads `.yolo/home/.claude/`. With `CLAUDE_SECURESTORAGE_CONFIG_DIR=$HOME/claude` as well, it answers `ok`: it reads the file, not the Keychain. |
| 3a | account | 2026-10-24 | 2.1.310 | **pass** | Fingerprint `f69329e6` → `e5436b50`, then a second `ok` with no restart. Interactive first run needed `hasCompletedOnboarding` set (see the section below and the backlog entry it names). The broker log has no `view: wrote` line for a `claude-auth refresh`. |
| 3a | self | 2026-10-24 | 2.1.304 | **pass with the workarounds** | `e5436b50` → `d05558da`, then a second `ok`. |
| 3b | account | 2026-10-24 | 2.1.310 | **pass (order changed)** | Starting on the wrong token exits at once (*"Your organization requires remote managed settings to load"*), so the order was good → wrong with a new mtime (401) → good with the wrong file's mtime, which was answered. |
| 3b | self | 2026-10-24 | 2.1.304 | **pass with the workarounds (order changed)** | As in account mode. |
| 4 (H1–H3, H5) | host | 2026-10-24 | 2.1.310 | **pass** | H1 `ok`, view `live`, runtime host. H2: no file, Keychain entry's metadata unchanged. H3 `d05558da` → `3620240f`, second `ok`. H5 `ok`, no store line. Pointing the store at a wrong-token copy fails, so host Claude reads the file even with a Keychain login. Needed `YOLO_ALLOW_MISSING_PROGRAMS=1`. |
| Phase A | self | 2026-10-25 | 2.1.304 | **pass with `CLAUDE_CODE_TMPDIR`** | At `482763948` (PR 62), both sessions name the shared store. A forced expiry made session A refresh in the sandbox (rt `96ac6130` → `74a4c6c4`); the broker adopted it within seconds and both sessions answered. Account mode cannot run it: one session at a time. |
| C1 | account | 2026-10-25 | 2.1.310 | **fail** | `/login` in a view jail: *"Couldn't save your login. If your Mac's keychain is locked…"*; nothing enrolled. Same as without the view. Cause: `security add-generic-password` hangs as `_yolojail`, and Claude skips the file on a timeout. With a fail-fast `security` shim on PATH, the `/login` enrolled the machine (see the section below and the backlog entry it names). |
| C2 | account | 2026-10-25 | 2.1.310 | **fail** | `/logout` deletes the view file; the broker counts a /logout only when the file remains without `claudeAiOauth` (`internal/oauthbroker/views.go:386`), so it rewrote the view. |
| C3 | — | 2026-10-25 | — | **pass** | Canonical gone, shared file and both views `no claudeAiOauth login`. |
| H4 | host | 2026-10-25 | 2.1.310 | **fail** | The launch printed the enrolment line; `/login` saved to a new Keychain entry `Claude Code-credentials-5d0efea8`, the store stayed empty, nothing enrolled. |

### What the table abbreviates (the 2026-10-25 lab report, retired into this section)

Host: macOS 26.5, arm64, Claude Code 2.1.310 (account mode, host) and 2.1.304 (self mode); Steps 0 to
5 ran on `9a1e0d9c9` and Phase A on `482763948`. Where each failure stands on main at `61d860711`:

- **Self mode's `/tmp/claude-<uid>` denial** (M11): the run-as-you profile closes the shared `/tmp`
  ([OQ-MM8](../../design/macos-user-model.md#OQ-MM8)), and Claude makes `/tmp/claude-<uid>` whatever
  `TMPDIR` says. Built: the Claude pack declares `sandbox_tmpdir_env = ["CLAUDE_CODE_TMPDIR"]`
  (`60549a751`). Native confirmation is owed ([the follow-ups plan](../../design/mac-claude-lab-followups-plan.md)).
- **Self mode wrote the view where Claude does not read it**: fixed by `829717e46` (measured fixed on the
  Mac 2026-10-27 at `12acfa691`, with `CLAUDE_CODE_TMPDIR` set by hand and no store variable).
- **C1, a sandbox `/login` cannot save in account mode.** Claude saves by running `security -i` with
  `add-generic-password` and writes its file only when the Keychain failure is not *transient*; a timeout
  is transient. As `_yolojail`, `add-generic-password` hangs (no default keychain), so the file fallback
  is skipped and `/login` throws *"Couldn't save your login. If your Mac's keychain is locked…"*. A
  fail-fast `security` shim (exit 50 on `add-generic-password`, everything else passed through) made a
  `/login` enroll the machine through the view. Built as the `claudesecurity` adapter (`173b67c77`,
  `61d860711`); native confirmation owed.
- **H4, a host `/login` with the view on went to the Keychain** entry `Claude Code-credentials-<hash>`,
  named for the moved store, and that entry then shadowed the view (Claude reads it first, the file only
  when there is none). A shim hiding that one service (find and delete exit 44, add exit 50) made the
  `/login` enroll through the store's file with no Keychain entry created. Built for the host with the
  view on (`173b67c77`); native confirmation owed.
- **C2, a Mac `/logout` deletes the view file**, which the broker read as `missing`, not as a sign-out,
  and rewrote. Fixed in `b2f28da6b` (a view the broker wrote and later found missing is a workspace
  logout on Darwin).
- **Self mode's `security add-generic-password`** fails in 1 s with exit 152, a non-transient failure, so
  Claude's file fallback works there and self mode needs no shim.

Findings with no fix yet are in [`BACKLOG.md`](../BACKLOG.md#mac-claude-login-residue).

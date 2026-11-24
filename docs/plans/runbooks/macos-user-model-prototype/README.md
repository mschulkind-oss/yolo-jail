---
title: "The macos-user model prototype"
status: accepted
stage: CURRENT
next: "Done: both modes passed against the generated profile on 2026-10-23 (self 43 PASS, account 42 PASS, no FAIL)"
tags: [runbook, macos-user, seatbelt, safehouse, prototype]
summary: "A hand-written deny-by-default Seatbelt profile and a check script that prove or disprove the ruled macos-user model on a Mac, with no yolo code involved"
---

# The macos-user model prototype

**Status:** written 2026-10-22 without a Mac, then first run the same day on macOS 26.5 (arm64) in
both modes: every tool check passed and two deny checks failed. Apple Events reached Finder (b8), and
the keychain check could not tell a refusal from "not found" (b4). The profile and script are revised
for both, and for b12 and b13, which had targeted a process that had already exited. The results are
recorded in the design's [first Mac run](../../../design/macos-user-model.md#first-mac-run-2026-10-22) section.

## What this proves

The [macos-user model](../../../design/macos-user-model.md) rules that macos-user moves to a
Seatbelt profile that denies everything and allows a list, run as you by default and optionally as
the second account. Its [§6](../../../design/macos-user-model.md#6-what-a-prototype-must-show)
names what a prototype must show before anything is built. This directory is that prototype:

- [`profile.sb`](profile.sb) is the profile, hand-written from
  [Agent Safehouse](https://github.com/eugene1g/agent-safehouse)'s layered profiles (Apache-2.0;
  the notice and a per-block source tag are at the top of the file). It takes its paths as
  `sandbox-exec -D` parameters and nothing else.
- [`run-checks.sh`](run-checks.sh) builds a throwaway workspace and agent home, runs each check
  under the profile and prints `PASS` or `FAIL` for each, with the command and its output.

No yolo binary or config is involved, and the script changes nothing on the Mac beyond what it
creates and removes again: its temp directory, a decoy file in your home, a decoy project directory,
a loopback listener, a sleeping process, and one throwaway item in your login keychain.

**The keychain check briefly adds and removes one item in your login keychain.** Before the checks
run, the script adds a generic password named `yolo-prototype-<nonce>` with a random value, readable
by `/usr/bin/security` without a prompt, and it deletes that item when it exits, on any exit path
that runs its cleanup. In account mode the item is added and deleted as you, and only the read runs
as the account. If the script is killed outright, delete it by hand with
`security delete-generic-password -s yolo-prototype-<nonce>`.

The checks, keyed to [§6](../../../design/macos-user-model.md#6-what-a-prototype-must-show):

| Check | What must happen |
| :--- | :--- |
| 0 | The profile loads and `/bin/bash` starts under it. If not, nothing else runs. |
| a3t, a3h | A write to the shared `/tmp` is refused ([`OQ-MM8`](../../../design/macos-user-model.md#OQ-MM8), ruled 2026-10-23). With `--profile-from-yolo`, the `macos_user_shared_tmp` hatch's profile makes it work again (a3h). |
| a1–a12 | The workspace, `$HOME` and own temp dirs are writable; git init and commit work; a C build runs; a Homebrew and a nix binary run; HTTPS works, certificate trust included (`curl`, `gh`, `git ls-remote`); the system fonts are readable. |
| b1–b14 | Each fails: reading a decoy secret in your home and `~/.ssh`, writing in your home, reading the throwaway login-keychain item's secret (b4) and the keychain file, the ssh agent, the clipboard, Apple Events to Finder and System Events, `open -a TextEdit`, `launchctl submit`, `kill -0` on a long-lived process of yours, reading that process's command line through `ps` and through `sysctl kern.procargs2`, and reading a 0600 file of yours in the shared `/tmp` (b14). |
| c1–c4 | A decoy project at `/opt/src/...` (or `/Library/Caches/...` where `/opt` is not writable) is unreadable; `/opt/homebrew` and the nix store are readable. |
| d1, d3 | A loopback listener of yours is reachable from inside, and so is a loopback server the agent starts itself: [`OQ-MM7`](../../../design/macos-user-model.md#OQ-MM7) (ruled 2026-10-23) allows the whole loopback. |
| e1–e2 | With Full Disk Access granted to the terminal, profile-denied files stay unreadable. |
| g1–g8 | A unix socket binds and connects under `$TMPDIR` and in the workspace, and a workspace symlink to a socket outside those roots (one of the script's, g7; your colima or Docker Desktop socket when there is one, g8) does not connect. With `--profile-from-yolo`: the session's host-services liveness lock is refused; with the shared `/tmp` closed, the session's own endpoint file is readable, a sibling session's is not, and `/private/tmp` cannot be listed. |
| f1 | Optional: Claude Code installs into the sandbox's own home and `claude --version` runs. |

b14 was an `INFO` line until [`OQ-MM8`](../../../design/macos-user-model.md#OQ-MM8) closed the shared `/tmp`: the first run read that file in self
mode, and in account mode Unix permissions refused it.

## How to run it

Copy this directory to the Mac, or use a checkout, then from a terminal:

```console
$ ./run-checks.sh --mode self
```

Self mode runs the sandbox as you, the ruled default. It puts the workspace in your home
(`~/yolo-prototype-<nonce>`), because the model allows that.

Account mode runs the same profile as the second account, through `sudo -u`, on a Mac where yolo's
macOS setup already created it. Elsewhere it prints a skip message and exits 0:

```console
$ ./run-checks.sh --mode account --account _yolojail
```

Its workspace is under `/Users/Shared/yolo/prototype-<nonce>`. It asks for your password once for
`sudo`.

Options:

- `--profile-from-yolo YOLO` runs every check against the profile yolo generates instead of
  [`profile.sb`](./profile.sb). `YOLO` is a yolo binary, for example one built from a checkout with
  `go build -o /tmp/yolo ./cmd/yolo`. The script renders the profile for its own workspace, home and
  temp dirs and a fake host-services dir with `YOLO internal macos-user-profile`, and renders it
  again with `--shared-tmp shared`, the hatch's profile, for check a3h. To compare the two profiles by eye, run that command yourself with `--mode`,
  `--workspace` and `--tmp` (`--help` lists the rest) and diff its output against [`profile.sb`](./profile.sb).
- `--with-claude` runs the native Claude Code installer inside the sandbox, into its own home, then
  `claude --version`. It makes network calls to download the installer, but never logs in and never
  calls the API.
- `--keep` keeps the workspace and home and prints the command for a sandboxed login shell under the
  same profile.

Before you run it, two things widen what it covers:

- **Copy any text to the clipboard.** Check b7 compares what `pbpaste` sees inside with what you
  copied. With an empty clipboard it skips, and it never writes to your clipboard.
- **Optionally, grant the terminal Full Disk Access** (System Settings, Privacy & Security), restart
  it and run self mode again for checks e1 and e2. The script prints whether access appears
  granted; confirm that in System Settings.

Checks b9 and b10 send Apple Events and ask LaunchServices to open TextEdit. If the profile is
wrong, TextEdit opens or macOS shows an automation prompt; deny the prompt, close TextEdit, and
report it, because that is a `FAIL`.

## Confirming Claude stays logged in

The script never logs in. To check [§6](../../../design/macos-user-model.md#6-what-a-prototype-must-show) item 6 by hand:

1. Run `./run-checks.sh --mode self --with-claude --keep`.
2. Paste the printed sandboxed shell command into a terminal.
3. Run `~/.local/bin/claude`, log in, then quit.
4. Start it again. It must not ask you to log in, and `~/.claude/.credentials.json` must exist
   under the sandbox's home.
5. Report whether it stayed logged in. Never paste the credentials file.
6. Remove the kept directory with the `rm -rf` line the script printed.

## Reading the output

Each check prints one line, then the command it ran and its output, indented:

```text
PASS b1   read the decoy secret in your home
    $ cat "/Users/you/yolo-prototype-decoy-123.txt"
    | cat: /Users/you/yolo-prototype-decoy-123.txt: Operation not permitted
    (control without the profile: allowed, so a refusal is the profile's)
```

- **`PASS`** means the expected thing happened: an allow worked, or a deny held.
- **`FAIL` on an a (other than a3t), c3–c4, d or g1–g2, g4 check** means the allow list is too narrow: a tool broke. That is
  expected on a first run, and it is a fix to [`profile.sb`](./profile.sb).
- **`FAIL` on a3t, a b, c1–c2, e, g3 or g5–g8 check** means something the model must deny got through. That is
  the finding that matters most.
- **`SKIP`** means the check could not run here (no Homebrew, no nix, no Command Line Tools, empty
  clipboard), and says what would let it run.
- **The control line.** Every deny check that is safe to run outside the sandbox runs once without
  it first. In account mode many controls are "also denied", because Unix permissions already keep
  the account out of your home. That is the backstop the second account adds, and it means that
  check does not test the profile in that mode.

The script exits non-zero when any check fails. On a failure it prints a `log show` command that
pulls the sandbox's own denial messages, which name the operation and path the profile refused.

## What to paste back

- The whole output of `--mode self`, and of `--mode account` if the Mac has the account.
- For every `FAIL`, the `log show` excerpt the script suggests.
- Whether Full Disk Access was granted, and the Claude login result if you ran it.
- The macOS version, which the script prints on its second line.

## What the profile decides

The decisions, each traced to a design ruling. The file comments every allow on its own line.

- **Read-only system and software trees:** `/usr` (with `/usr/local`), `/bin`, `/sbin`,
  `/opt/homebrew` (never all of `/opt`), `/Applications`, `/nix/store`, `/nix/var/nix/profiles`,
  `/System/Library`, a few `/Library` framework and font folders, and the Command Line Tools
  binaries Safehouse curates. `/opt` itself gets metadata only, so `/opt/homebrew` resolves while
  `/opt` cannot be listed.
- **Read-write:** the workspace, the agent's home (`<workspace>/.yolo/home`), the launch's own temp
  dir and the running user's `DARWIN_USER_TEMP_DIR`. The shared `/tmp` is closed ([`OQ-MM8`](../../../design/macos-user-model.md#OQ-MM8)). Safehouse grants all of
  `/var/folders`; this grants one folder of it, and not the user cache folder beside it.
- **Your home:** nothing but three locale preference files, read-only. Every directory above the
  workspace is readable as a single directory (its listing, nothing below it), as Safehouse does
  for Claude Code's start-up walk. So with the workspace in your home, the agent can list your
  home's top-level names but read none of them. No per-language toolchain folder (`~/.nvm`,
  `~/.cargo` and the rest) is granted: the agent has its own `HOME`.
- **System services:** Safehouse's list minus LaunchServices, which `open` uses. Trust evaluation
  (`com.apple.trustd.agent`) stays, so HTTPS clients work. trustd's system daemon
  (`com.apple.trustd`) is not reachable; check a4t runs a Go client (`gh`) through a TLS handshake,
  and an x509 or certificate error there, behind a proxy whose CA the Mac trusts, is the evidence
  for adding it ([PS-D15](../../../design/provisioner-sets.md#PS-D15)). The keychain, clipboard, Apple Events
  and LaunchServices are also denied by name at the end. The keychain's services are denied under
  their real names, `com.apple.SecurityServer` and `com.apple.securityd`, beside the earlier
  guesses, which do no harm if wrong. Check b4 is what proves the keychain closed, not those names.
- **Apple Events:** denied by Seatbelt's own operation, `(deny appleevent-send)`, as the last rule
  in the file so nothing re-allows it. The first run's Finder answer was a false alarm (`get name`
  sends no event); a real event fails even without that rule.
- **Processes:** any program may run; signals and process inspection reach only processes inside
  the sandbox; another process's command line and environment are denied on both channels.
- **Network:** open, the whole loopback included ([`OQ-MM7`](../../../design/macos-user-model.md#OQ-MM7)). Unix sockets bind and connect only under
  the workspace, the agent's home and the launch's temp dir; the ssh agent and container runtime
  sockets are also denied by name.

## What may need adjusting

These are the parts written blind, most likely first:

- **Start-up reads.** Under `(deny default)`, a framework that reads one file not on the list
  stops a tool cold. Safehouse's list is the starting point, but this profile drops some of it and
  yolo's tools differ. The `log show` excerpt names the path.
- **The `-D` parameter forms.** The profile builds rules from parameters with `(param …)`,
  `string-append` and `if`. Safehouse's profiles use `define` and `string-append`; `param` and a
  top-level `if` are recalled from Chromium's profiles, not checked here. If the profile fails to
  load on one of those lines, report it.
- **The unix-socket filters.** `(local unix-socket (subpath …))` and `(remote unix-socket (subpath …))`
  are Codex's form, not Safehouse's. Checks g1 and g2 show whether they load and match, a relative
  socket path included.
- **Whether `localhost` in a network rule covers both `127.0.0.1` and `::1`.** Check d1 tests only
  the first; the integration suite's d2 case tests `::1`.
- **Whether `launchctl submit` is refused by Seatbelt or by launchd.** Check b11 judges by whether the
  job appears outside, either way.
- **The b13 readers.** `/bin/ps` is setuid root, and the first run showed the sandbox refusing to
  run it at all, so b13 also reads `kern.procargs2` through a few lines of Python using `sysctl(3)`,
  where the Command Line Tools are installed. Its control proves that reader works outside.
- **The b12 and b13 controls in account mode.** The long-lived process is yours, so the account
  cannot signal it or read its command line even without the profile; the control says so.
- **`osascript -e 'do shell script …'`** with no target application runs inside the sandbox and
  escapes nothing, so check b9 sends it to System Events instead.

---
title: "Mac native witnesses: the Copilot keychain boundary (W0–W8) and issue #66"
date: 2026-10-30
tags: [lab-report, macos, macos-user, copilot, keychain]
summary: "Copilot's HTTPS under Seatbelt needs SecurityServer and read access to two public root-certificate files, not SecurityServer alone. With both allowed, secrets stay unreadable but a jail can lock every keychain in its session. Account mode shares the owner's security session. Issue #66's fix holds natively."
---

# Mac witnesses: the Copilot keychain boundary, and issue #66

**Run 2026-10-30 on the maintainer's Mac** (macOS 26.5 build 25F71, arm64). The source is `main`;
the launcher deployed for this run is `yolo-jail 0.15.1+950.gfa808c2f8ef5`. This report answers the
[Mac witnesses](../research/copilot-keychain-boundary.md#3-mac-witnesses) of the
[Copilot keychain-boundary research](../research/copilot-keychain-boundary.md), and checks the fix
for [issue #66](https://github.com/mschulkind-oss/yolo-jail/issues/66).

**Terms.** These are the research doc's, restated here:

- *p0* is the shipped run-as-you Seatbelt profile, from
  `yolo internal macos-user-profile --mode self`. It denies the Mach service
  `com.apple.SecurityServer` (securityd) and reads of `/Library/Keychains`,
  `/System/Library/Keychains` and the user's `~/Library/Keychains`.
- *p2* is p0 plus `(allow mach-lookup (global-name "com.apple.SecurityServer"))`, the doc's option
  (a).
- *vd* is **coined in this report**: p2 plus `file-read*` on exactly two files,
  `/System/Library/Keychains/SystemRootCertificates.keychain` and
  `/System/Library/Keychains/SystemTrustSettings.plist`. It is the smallest profile found that lets
  Copilot's HTTPS work ([§2](#2-w2-copilots-own-tls)).
- *Account mode* runs the agent as the dedicated user `_yolojail`; *run-as-you* (self mode) runs it
  as the owner ([MMP-D12](../design/macos-user-model-plan.md#MMP-D12)).
- `yolo-witness` (uid 504) and `yolo-witness2` (uid 505) are disposable, hidden, standard accounts,
  reached over SSH from this Mac. They were made for this run by
  `/Users/Shared/yolo/witness-account.sh`, which for `yolo-witness2` also installed one sudo rule:
  `yolo-witness` may run `/usr/bin/security` as `yolo-witness2`.

**Scope.** Every Security.framework call that touched a keychain ran in `yolo-witness`, against
synthetic keychains it created, as the research's
[standing constraint](../research/copilot-keychain-boundary.md#3-mac-witnesses) requires. W0, W1 and
the profile half of W8 ran in the owner's account and touched no keychain. The native Copilot runs
(W2, W7) and the `SecKeychainResetLogin` call (W5) were authorized by the maintainer for this run.
Copilot ran with a fresh empty `COPILOT_HOME` and no login; its only requests were GitHub's public
release check and a loopback stub. In this report, *rc 44* is `errSecItemNotFound` (-25300) and
*rc 36* is `errSecInteractionNotAllowed` (-25308), the low byte `security` exits with; both were
decoded with `security error`.

## 1. Results

| Witness | Result |
| :--- | :--- |
| W0 Copilot's signature | As expected ([§3](#3-w0-w1-and-w8-in-the-owners-account)) |
| W1 does `sudo -u _yolojail` share the owner's session? | **Yes**, over SSH and at the desktop ([§3](#3-w0-w1-and-w8-in-the-owners-account)) |
| W2 Copilot's TLS under p0 and p2 | **p2 is not enough.** Copilot needs SecurityServer **and** the two root files (vd) ([§2](#2-w2-copilots-own-tls)) |
| W3 pathless queries | Refused under p0, p2 and vd ([§4](#4-w3-and-w4-reads-and-path-aliases)) |
| W4 path aliases | Refused from inside the sandbox; a hard link made **outside** it exposes the raw file, and under p2 a prompt ([§4](#4-w3-and-w4-reads-and-path-aliases)) |
| W5 session routines | Another UID in the session can lock all; p2 and vd can lock all; p0 cannot; reset-login refused everywhere ([§5](#5-w5-session-routines)) |
| W6 `SessionCreate` | **A LaunchAgent with `SessionCreate` gets its own session without root**, and a lock-all there leaves the desktop session's keychains unlocked ([§8](#8-w6-sessioncreate)) |
| W7 BYOK offline over loopback | **Fails under p0** before connecting; works under vd only ([§6](#6-w7-byok-offline-over-loopback)) |
| W8 message filtering | **Closed**: `sandbox-exec` refuses any message filter ([§3](#3-w0-w1-and-w8-in-the-owners-account)) |
| Issue #66 | **Fixed**, in account mode and in run-as-you ([§7](#7-issue-66)) |

Consequences for the research's options:

- **(a) "Allow SecurityServer only; keep the file denies"** does not make Copilot work. The working
  profile also opens two public files. Neither holds a secret: both are root-owned, mode 0644, on
  the sealed system volume, and hold Apple's root certificates and trust settings.
- **Under that profile, secrets stay unreadable, but a jail can lock every keychain unlocked in its
  security session** (W5), and probably raise a keychain dialog through any path it can reach (W4).
  In run-as-you, that session is the owner's.
- **(c1) BYOK does not avoid native TLS**: the client loads its trust roots before any request.
- **(e) message filtering is not available** to `sandbox-exec`.
- **(b) a separate security session is buildable without root** (W6), so it is the route that
  closes the lock-all hazard for run-as-you and account mode alike.

## 2. W2: Copilot's own TLS

The binary is Copilot 1.0.105's `copilot`, SHA-256
`5fd7d15ff02deb08a58593b1b488015bb52c1686014b80b5509e28263cb3ad70`, the one the research read.
p0 was built inside `yolo-witness` with the research's command (446 lines, SHA-256 `8c35bc1d…`).
Each run was `copilot update` with `HOME` and `COPILOT_HOME` in a fresh directory inside the
workspace.

**Two setup conditions came first.** The research's command line does not meet either.

- With `HOME` left as the account's real home, every sandboxed run failed on
  `mkdir ~/Library/Caches/copilot`. A real launch sets `HOME` to the workspace sidecar.
- With the binary outside the workspace, every sandboxed run exited 139 (SIGSEGV) with no output
  and no crash report. Copied into the workspace, `copilot --version` printed
  `GitHub Copilot CLI 1.0.105.` under p0 and p2. The likeliest reading is that the single-executable
  binary reads itself at start, and p0 does not allow reading it outside the workspace.

| Profile | `copilot update`, last line |
| :--- | :--- |
| Unsandboxed control | `No update needed, current version is 1.0.105, fetched latest release is v1.0.105` |
| p0 | `Error during update: Failed to fetch latest release: invalid extra CA certificate: no trust roots available for proxy TLS` |
| p2 | the same error as p0 |
| p0 + read `/System/Library/Keychains` (subpath) | the same error as p0 |
| p2 + read `/System/Library/Keychains` (subpath) | works |
| p2 + `SystemRootCertificates.keychain` alone | the same error as p0 |
| p2 + the directory + `SystemRootCertificates.keychain` + `X509Anchors` | the same error as p0 |
| **vd**: p2 + `SystemRootCertificates.keychain` + `SystemTrustSettings.plist` | works |
| p0 + the directory + both files (no SecurityServer) | the same error as p0 |

The research expected `bad certificate format` under p0 and a version line under p2. Neither
appeared: 1.0.105 reports the missing root store instead. No dialog could be observed, because the
SSH session has no GUI. The account-mode half of W2 was not run: the compatibility guard still
refuses Copilot 1.0.93 and later before any request
([OQ-MT11](../design/macos-native-tls.md#OQ-MT11)).

## 3. W0, W1 and W8 in the owner's account

**W0.** `codesign -dv` on the binary above: `Identifier=copilot`, `TeamIdentifier=VEKTX9H2N7`
(`Developer ID Application: GitHub`), `flags=0x10000(runtime)`. Its entitlements are
`allow-jit`, `allow-unsigned-executable-memory` and `disable-library-validation`, with no
`allow-dyld-environment-variables`. This matches the research's expectation for option (d).

**W1.** The probe from the research (`SessionGetInfo` on the caller's session), built at
`/Users/Shared/yolo/sid/sid`, was run directly and as `_yolojail`. The run used
`sudo -n -u _yolojail /usr/bin/env …`, because `/usr/bin/env` is the only command the sudo rule for
`_yolojail` allows without a password.

| Where | Owner | As `_yolojail` |
| :--- | :--- | :--- |
| SSH shell (tmux) | `0 100099 20512` | `0 100099 20512` |
| Local Ghostty window, run by the maintainer | `0 100023 24624` | `0 100023 24624` |

The fields are status, session id and attribute bits. 20512 is `0x5020` (`sessionHasTTY`,
`sessionIsRemote`), and 24624 is `0x6030` (`sessionHasGraphicAccess`, `sessionHasTTY`). Both also
carry bits the public `AuthSession.h` does not name. **An account-mode jail runs in the owner's
security session, including the desktop one.** This is the assumption
[§1.5](../research/copilot-keychain-boundary.md#15-run-as-you-versus-the-dedicated-account) of the
research rests on.

**W8.** `lockAll` is message **1023** in securityd's MIG subsystem, counted from
[`ucsp.defs`](https://github.com/apple-oss-distributions/Security/blob/main/OSX/libsecurityd/mig/ucsp.defs)
on Apple's `main` (`subsystem ucsp 1000;`). The count skips the `#if 0` block, which holds a
`routine` before `lockAll`. That the shipped securityd uses this numbering is not verified.
Applying profiles to `/usr/bin/true` with `sandbox-exec -f`:

| Profile | Result |
| :--- | :--- |
| `(allow default)` | rc 0 |
| p0, and p2 | rc 0 |
| p0 + SecurityServer with `(apply-message-filter (deny mach-message-send (message-number 1023)) (allow mach-message-send))` | `sandbox_apply: Operation not permitted`, rc 71 |
| `(allow default)` + SecurityServer with an allow-only filter | `sandbox_apply: Operation not permitted`, rc 71 |
| the same on `com.apple.cfprefsd.daemon` | `sandbox_apply: Operation not permitted`, rc 71 |
| a misspelled `apply-message-fliter` | `unbound variable`, rc 65 |

The parser knows the form, but applying any profile that uses it is refused for this caller. yolo
applies its profile through `/usr/bin/sandbox-exec -f`
([`capture.go`](../../internal/macosuser/capture.go#L654)), so option (e) is closed for yolo. W8's
reruns of W2 and W5 were therefore not possible.

## 4. W3 and W4: reads and path aliases

W3 set up a synthetic keychain `wit.keychain-db` in `yolo-witness`, as the research's commands do,
and made it that account's default and only search-list entry. Unsandboxed,
`security find-generic-password -s wit -a a -w` read the secret (rc 0).

| Attempt | p0 | p2 | vd |
| :--- | :--- | :--- | :--- |
| Pathless read | rc 44, `SecKeychainSearchCreateFromAttributes: One or more parameters … not valid` | rc 44, `… could not be found` | rc 44, `… could not be found` |
| Pathless read with the real `HOME` | — | rc 44 | rc 44 |
| Add to the default keychain | — | rc 152, `Unable to obtain authorization for this operation` | rc 152, the same |
| Create a new keychain in the workspace | — | rc 0 | rc 0 |
| `ln` the keychain file into the workspace | — | `Operation not permitted` | `Operation not permitted` |
| Read via `/System/Volumes/Data` + path | — | rc 1 | rc 1 |
| Symlink in the workspace, then read | — | rc 1 | rc 1 |
| Direct file read | — | rc 1 | rc 1 |

The different p0 and p2 errors show that p2's appended allow does reach securityd. Creating a
keychain left the search list and default keychain unchanged, as on
[10-29](2026-10-29-mac-native-witnesses-runbook.md). `tmutil listlocalsnapshots /` listed no
snapshots, so the snapshot path could not be tried.

**A hard link made outside the sandbox gets past the file deny.** `ln` was run unsandboxed into the
workspace (link count 2), then read under each profile:

| Through the outside hard link | p0 | p2 | vd |
| :--- | :--- | :--- | :--- |
| `cat` (raw encrypted bytes) | rc 0 | rc 0 | rc 0 |
| `find-generic-password … <link>` | rc 44 | **rc 36** | **rc 36** |

The file deny is by path, so the raw bytes are readable even under p0. Under p2 and vd the lookup
reached the item and wanted user interaction, which this session cannot give. A desktop session would
probably show a keychain dialog; that was not observed.

## 5. W5: session routines

All lines ran in one `yolo-witness` SSH session. The secret read's rc shows the state: 0 means
unlocked, and 36 means locked.

| Action | Result |
| :--- | :--- |
| `sudo -n -u yolo-witness2 /usr/bin/security lock-keychain -a` | rc 0, **keychain locked** |
| `lock-keychain -a` under p0 | rc 206 (`One or more parameters … not valid`), stays unlocked |
| `lock-keychain -a` under p2 | rc 0, **keychain locked** |
| `lock-keychain -a` under vd | rc 0, **keychain locked** |
| `lock-keychain <path>` under p2 | `UNIX[Operation not permitted]`, stays unlocked |

With W1, this means any process `_yolojail` runs **outside** Seatbelt can lock the owner's
keychains. This was not tried against the owner's session. Account mode's own profile still denies
SecurityServer.

**`SecKeychainResetLogin`** (private, `SecKeychainPriv.h`, declared by hand) was called with a
synthetic password and `resetSearchList = false`:

| Profile | Status | Effect |
| :--- | :--- | :--- |
| p0 | -50 (`One or more parameters … not valid`) | none |
| p2 | 100001 (`UNIX[Operation not permitted]`) | none |
| Unsandboxed control | 0 | created `login.keychain-db`, made it the default, added it to the search list |
| p0, p2, vd, with that login keychain present | -50, 100001, 100001 | inode, mtime and size unchanged |

## 6. W7: BYOK offline over loopback

A Python stub on `127.0.0.1:18787` logged each request line and answered 400. Copilot ran as
`copilot -p "reply ok"` with `COPILOT_OFFLINE=true`,
`COPILOT_PROVIDER_BASE_URL=http://127.0.0.1:18787/v1` and `COPILOT_MODEL=stub-model`.

| Profile | Copilot | Stub saw |
| :--- | :--- | :--- |
| p0 | `Execution failed: Invalid native model HTTP root certificate: no trust roots available for proxy TLS` | nothing |
| p0 + the two root files (no SecurityServer) | the same | nothing |
| vd | `400 stub` | `POST /v1/chat/completions` |

A plain `http://` provider still needs the root store, because the native client builds it before
connecting. Option (c1) therefore needs the same allowances as vd.

## 7. Issue #66

[Issue #66](https://github.com/mschulkind-oss/yolo-jail/issues/66): a workspace that once ran under
podman holds empty, mode-0700 files at `.yolo/home/config/yolo-jail/config.jsonc` and
`inherited-launch.jsonc`, and a macos-user launch refuses them. The fix is `b02778989`. Each
fixture below was a fresh workspace under `/Users/Shared/yolo` with `{}` as its config, and those two
files empty, mode 0700, dated Sep 30. The maintainer's user config is legacy JSONC (it has a
top-level `loopholes` key).

| Launcher | Mode | Fixture | Result |
| :--- | :--- | :--- | :--- |
| `+889` (before the fix) | account | empty placeholders | rc 1: `refusing macos-user inherited preflight scope: … could overwrite existing user file …/config.jsonc` |
| `+950` (the fix) | account | the same workspace | rc 0: `Moved the empty file at …/config.jsonc aside to …/config.jsonc.podman-placeholder: it held no data …`; `config.jsonc` delivered `-r--r--r--` with two links |
| `+950` | account | relaunch | rc 0, no notice, no refusal |
| `+950` | account | a 9-byte `config.jsonc` | rc 1; the file is untouched, and the refusal names `mv …/config.jsonc …/config.jsonc.moved-aside` |
| `+950` | run-as-you | empty placeholders | The file moved aside with the same notice. The launch then stopped on the separate Copilot 1.0.93+ guard ([#65](https://github.com/mschulkind-oss/yolo-jail/issues/65)), whose refusal names `YOLO_ALLOW_MISSING_PROGRAMS=1` |
| `+950` | run-as-you | a fresh copy, with `YOLO_ALLOW_MISSING_PROGRAMS=1` | rc 0, the file moved aside; the user config arrived `-r--r--r--` at `.yolo/home/.config/yolo-jail/config.jsonc`, the run-as-you path |

Run-as-you was selected for its rows by a temporary user-scope override. The issue's reporter only suspected the JSONC path ("I *think* it still refuses"). The `+889` row
confirms it. The first `+950` launch took 2045.8 s, because the new build compiled mise from source,
its test suite included.

## 8. W6: `SessionCreate`

`yolo-witness` was recreated with the maintainer's admin authorization (`sysadminctl -addUser …
-adminUser matt`). The first copy had been made without it, and its password never worked at the
login window; `sysadminctl -resetPasswordFor` on it failed with `commitChanges failed`. The
maintainer then logged in to it at the desktop with fast user switching. Two LaunchAgents in its
`~/Library/LaunchAgents`, loaded by that login, ran the W1 probe:

| Agent | Session id | Attributes |
| :--- | :--- | :--- |
| Plain (`SessionCreate` false) | 110464 | 24624 (`0x6030`, graphics and TTY) |
| `SessionCreate` true | **110631** | **0** |
| The SSH shell, for comparison | 110650 | 20512 (TTY, remote) |

A new session id does not by itself show that securityd keeps the sessions apart, so agents started
from SSH with `launchctl bootstrap gui/504` then ran in order. The plain ones reported attributes 48
(`0x30`) under the same session id.

| Step | Session | Result |
| :--- | :--- | :--- |
| Add and read a synthetic item in the login keychain the desktop login unlocked | 110464 | add rc 0, read rc 0 |
| `security lock-keychain -a` | **110652**, `SessionCreate` | rc 0 |
| Read the item again | 110464 | **rc 0: still unlocked** |
| Control: `security lock-keychain -a` | 110464 | rc 0 |
| Read the item again | 110464 | rc 152: locked |

**A process in a `SessionCreate` session cannot lock the desktop session's keychains**, and a user's
own LaunchAgent can create one without root. This is the research's option (b). Not measured: whether
yolo's launch path (a `sudo -u _yolojail` hop in account mode, a direct child in run-as-you) can
start the jail through such an agent, and what else the separate session takes away (for example
dialogs, pasteboard or other services keyed by session).

## 9. Left in place, and cleanup

- `yolo-witness` was recreated for W6, so W2–W5's files went with the first copy. The W6
  LaunchAgents were booted out and deleted. Its login keychain, holding one synthetic item, goes
  with the account.
- `yolo-witness2` holds nothing beyond its new home.
- `/Users/Shared/yolo/sid` holds the W1 probe.
- The #66 fixture workspaces were removed. Run-as-you was set for those launches only, by a temporary
  `~/.config/yolo-jail/overrides.jsonc` holding `{"macos_user_mode": "self"}`, deleted after each.

---
status: in-review
stage: SKETCH
next: "Feeds the grant inventory walkthrough in ../design/macos-user-privilege-lifecycle.md (OQ-MP10); keep keychain policy in its existing design"
verified: 2026-10-07
tags: [macos-user, sandvault, privileges, sudo, keychain]
summary: "SandVault authorizes passwordless runtime operations during privileged setup. Its pattern can address yolo's repeated prompts, but its rules do not cover yolo's dynamic session artifacts and must not be copied as broad root access. Its dedicated-keychain fix also informs reported first-login failures."
---

# SandVault's macOS privileges and what yolo can reuse

Yolo manages an agent's environment, with a separate macOS account and Apple's Seatbelt sandbox
as one confinement option. This comparison concerns that option's privilege lifecycle, not a
change to yolo's environment or credential policy.

**Source-checked 2026-10-07:** SandVault v1.32.0 at
[`ad05889e9f5f7d63460d6e56a2586c724b14bcd0`](https://github.com/webcoyote/sandvault/tree/ad05889e9f5f7d63460d6e56a2586c724b14bcd0).
Yolo was checked at `94d6337e09a905b400b1606c12767d1081df8747`. No native execution,
sudo-policy installation, account changes or keychain operations were performed. This is a
recommendation for design work, not an authorization to install passwordless privileges.

**Forward route:** [the proposed privilege lifecycle](../design/macos-user-privilege-lifecycle.md)
owns the bounded setup/switch/session-file/cleanup contract and its standing-authority question.
The research remains a comparison, not installation consent or a keychain ruling.

## Findings

### Normal launches are passwordless because setup authorizes them

SandVault still uses `sudo` to switch to its restricted account. During a privileged build or
rebuild, it installs a policy under the system's sudo configuration directory that lets the
named host user run selected commands without a password. `NOPASSWD` is sudo's setting for
that behavior; it does not mean the commands stop needing authorization. The
[sudo policy reference](https://www.sudo.ws/docs/man/sudoers.man/) defines its command and
run-as-user matching.

The current generated policy permits:

| Runs as | Commands | Purpose |
| :--- | :--- | :--- |
| That host user's SandVault account | `/bin/zsh`, `/usr/bin/env`, `/usr/bin/true` | Enter the restricted account and verify access |
| Root | One installed home-sync script | Refresh the restricted account's home |
| Root | Fixed `launchctl bootout user/<uid>` and `pkill -9 -u <account>` forms | User-wide removal operations |

The shell and environment entries allow general execution **as the restricted account**, not
root. They are not a command allowlist for the agent; the sandbox rules still provide the
confinement. The root permissions are a different and more sensitive part of the policy.

Before the normal sudo launch, SandVault runs a noninteractive `true` as the target account.
A failure refuses entry and names `build --rebuild`; it does not fall back to an unexpected
password prompt. Setup writes a root-created temporary policy, validates it with `visudo`, and
moves the validated file into place. See the [setup policy](https://github.com/webcoyote/sandvault/blob/ad05889e9f5f7d63460d6e56a2586c724b14bcd0/sv#L1489-L1579)
and [launch probe and user switch](https://github.com/webcoyote/sandvault/blob/ad05889e9f5f7d63460d6e56a2586c724b14bcd0/sv#L2089-L2142).

### This is not a new October removal of startup passwords

The home-refresh change was committed on
[2025-09-20](https://github.com/webcoyote/sandvault/commit/1e5cec7c8bb5c581d7971ecf4822148f4836b71c).
The [v1.1.0 changelog](https://github.com/webcoyote/sandvault/blob/ad05889e9f5f7d63460d6e56a2586c724b14bcd0/CHANGELOG.md#L717-L740)
records refreshing the home without a password prompt. The
[January 2026 restriction](https://github.com/webcoyote/sandvault/commit/e2878267803102104b8b7b8740edb456a81fe045)
replaced an all-commands restricted-account rule with named executables and added the
noninteractive launch probe.

The v1.23.0 source already contains this policy. That is the version our
[original hardware notes](../plans/runbooks/mac-sandvault-session.md) identify.
Our own [macos-user runbook](../plans/runbooks/mac-macos-user-e2e.md) explicitly says setup
installs no passwordless sudo rule. The contrast is a missing privilege mechanism in yolo,
not evidence that SandVault recently stopped switching users.

### Root-owned home syncing needed a security repair

SandVault installs a fixed script in its root-managed state directory, then grants the host
user passwordless execution of that script as root. It copies managed home content and changes
ownership of the changed paths; it is not a general root shell command supplied by a caller.

That narrower shape still needed a
[June 2026 repair](https://github.com/webcoyote/sandvault/commit/e87cd796c7dcc8c7271a155c0449582633a6769d):
the maintainers recorded a race where a guest replaced a destination with a symbolic link
before root changed its ownership. They added `chown -h` to act on the link rather than its
target. This review did not reproduce that exploit or establish that every intermediate-path
race is now closed. A symbolic link redirects path resolution; changing only the final link's
handling is not proof that every parent directory is safe.

**Implication:** borrow the separation between privileged setup and limited runtime actions,
not the assumption that a root-owned script is sufficient security review.

### Recent cleanup changes preserve sibling sessions

October 3 changes keep a supervising shell alive so its exit cleanup actually runs; the old
`exec` replaced that shell and discarded its cleanup trap. Ordinary exit now cleans up its own
browser and simulator resources. User-wide `launchctl`/`pkill` cleanup is reserved for uninstall,
not ordinary session exit. See the [supervision repair](https://github.com/webcoyote/sandvault/commit/7b9dcd1334fcd06bc504fde0ca74e1f17c5654bc)
and [cleanup scope repair](https://github.com/webcoyote/sandvault/commit/14974f6f329207caf7c0e5859c88ca4d0730334c).

This is relevant to yolo's [last-session service lifetime](../design/jail-lifetime-last-session-wins.md),
but copying an account-wide kill would be wrong: yolo's `_yolojail` account can serve multiple
workspaces. Stopping one session must not terminate another workspace's processes.

### The dedicated keychain addresses missing-keychain startup failures

SandVault's [guest configuration](https://github.com/webcoyote/sandvault/blob/ad05889e9f5f7d63460d6e56a2586c724b14bcd0/guest/home/configure#L42-L104)
explicitly addresses a first-login dialog that says a keychain cannot be found to store a
credential. It creates a dedicated
account-owned keychain, unlocks it with an empty password, disables automatic locking, and
sets it as both the default and the sole entry in the account's keychain search list.

The [August 2026 change](https://github.com/webcoyote/sandvault/commit/9c830bb19cceefb2ee2c6ec9652859e7ab63b174)
replaced the login keychain because macOS could synchronize that keychain's password with the
account password after reboot. It migrates the account's existing Claude credential when the
old keychain can be unlocked and otherwise preserves it and asks for reauthentication. These
are SandVault's rationale and implementation, not a reboot behavior measured in this review.

For yolo, this improves the proposed fix: do not blindly create an empty-password login
keychain and assume it remains usable. A dedicated service-account keychain is a concrete
candidate. Its password, lock lifecycle, search list and cross-workspace access remain the
policy of [the keychain design](../design/keychain-from-a-jail.md#8-decision-ledger), not a
ruling made by this comparison; on 2026-10-10 its [OQ-KC4](../design/keychain-from-a-jail.md#8-decision-ledger) ruled the keychain always the host's,
so the dedicated account keychain is not taken. Never migrate the host user's personal keychain wholesale.

## Adoption assessment

| Approach | Disposition | Reason |
| :--- | :--- | :--- |
| One privileged setup authorizes routine runtime actions | Shortlisted | Removes reliance on a password cache surviving an agent session |
| Noninteractive runtime checks with a repair command | Shortlisted | Broken setup should refuse clearly, not prompt at an arbitrary step |
| Copy SandVault's sudo policy verbatim | Rejected | It neither authorizes yolo's session-file operations nor scopes to our workspace/session model |
| Grant passwordless root shell, environment, copy or removal commands | Rejected | Caller-controlled commands or paths would create general root authority |
| Add only passwordless switching to `_yolojail` | Insufficient | Profile and session-file installation and deletion would still require root authorization |
| Dedicated account keychain rather than login keychain | Shortlisted | Directly addresses the reported dialog and upstream's reboot problem; policy remains unruled |
| Account-wide killing on normal exit | Rejected | Would terminate unrelated yolo workspaces |

Yolo's [root-file installer](../../internal/macosuser/real.go) uses privileged directory
creation, writing and mode changes. Its [session cleanup](../../internal/macosuser/sessionfiles.go)
removes root-owned profiles and environment files; those files can carry credentials. They are
per-session, unlike SandVault's reusable account profile. Authorizing the account switch alone
would leave reported profile-write denials and exit prompts unresolved.

An installed management program could perform a small set of root-only operations on behalf
of an authenticated host caller, rather than accepting general commands to execute. For yolo,
a design must cover:

- Root-protected executable and parent directories, including an explicit upgrade/uninstall path.
- Actual caller identity and allowed target account; an environment marker is not authentication.
- Fixed operations on validated session names and owned paths, never arbitrary argv or shell text.
- Safe source reads, writes and deletion under concurrent guests, including symbolic links in
  parent directories, replacement races and preservation of live sessions.
- One authority model for profile installation, scoped credential files, user switching and
  cleanup, with no agent-editable hook executing as root.
- Noninteractive failures, visible leftover-credential warnings and the existing safe sweep,
  not silently successful cleanup.

This could be an installed helper authorized by a narrowly generated sudo policy; SandVault
shows that a new long-running service is not inherently required. It is not yet a settled
architecture. A managed Mac still needs an administrator or IT to authorize installation:
passwordless runtime does not grant a non-admin permission to change system policy.

## Verification before adopting

Use an explicitly prepared native Mac, not a Linux cross-compile or a nested container:

1. Install as the intended host user with the expected one-time administrator approval.
2. Clear sudo's cached authorization, then start and exit without password prompts.
3. Verify profile/environment-file ownership and complete credential-file removal.
4. Refuse management operations from the guest account and refuse unsafe paths or requests.
5. Exercise simultaneous sessions and separate workspaces; end one without killing the others.
6. Test missing, stale and restricted managed-Mac policy with actionable refusals.
7. Create, read and remove a harmless account-keychain item across launches and after reboot,
   without logging into an agent or copying real credentials.

Upstream's [test setup](https://github.com/webcoyote/sandvault/blob/ad05889e9f5f7d63460d6e56a2586c724b14bcd0/scripts/tests#L2334-L2342)
clears cached sudo authorization before its tests. That is a useful test condition, not proof
that yolo has implemented or passed this behavior.

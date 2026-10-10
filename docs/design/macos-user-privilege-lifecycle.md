---
status: in-review
stage: DESIGN
next: "Walk through the grant inventory (§8) with the owner, then record OQ-MP10; no build or installation is authorized"
verified: 2026-10-09
tags: [macos-user, privileges, security, lifecycle, sudo]
summary: "Proposed contract: after explicit administrator setup, routine native startup and exit never ask for a sudo password. A root-protected, caller-authenticated helper installs only named guest artifacts, switches only to the fixed sandbox account and removes only owned session files. No implementation or installation is authorized; keychain, workspace and lifetime policies remain independent."
vantage:
  status-chip: true
---

# Authorize native startup once, without giving the launcher a root shell

**Status:** 2026-10-10 — direction ruled: adopt SandVault's model
([OQ-MP1](#decision-ledger)); the exact grants await the owner's walkthrough of
[the grant inventory](#8-the-grant-inventory). Source verified 2026-10-09 at `b2eeffc12`. The
privilege helper is unbuilt; its native security and password-free behavior are **UNMEASURED**.
This is not setup or build authorization.

> **In short.** Yolo manages the agent's environment; its native macOS confinement option should
> authorize bounded routine operations at administrator setup, not ask for a password at every
> startup or exit. That requires a protected privilege boundary, not broader sudo commands.

**The shape.** An administrator installs a protected helper; the normal-user launcher supplies
bounded requests; guest code runs only after the helper permanently drops root privileges.

**Cost.** A new root execution surface, explicit administrative updates and retained crash artifacts.

**Start at [the grant inventory](#8-the-grant-inventory)**, then [the authority boundary](#3-the-authority-boundary).

**Needs your ruling:** [OQ-MP10](#OQ-MP10), after the walkthrough.

**Reads with:** [the privilege research](../research/sandvault-macos-privileges.md),
[the keychain design](keychain-from-a-jail.md), [workspace policy](configurable-workspace-root.md),
[session lifetime](jail-lifetime-last-session-wins.md#99-the-keeper-at-yolo-host-and-macos-user),
and [guest-tree retention](jail-daemon-on-macos-user-plan.md#ownership-and-cleanup-unknown-keeps-the-bytes).
No implementation plan exists for this proposal.

---

## 1. The proposed contract, not permission to deploy

My recommendation is **password-free routine startup and exit after explicit administrator
setup**, including an expired sudo authentication cache and a launch with no terminal. A broken
installation refuses early with a repair step; it never silently returns to interactive sudo.
This is the proposed product contract. [OQ-MP1](#decision-ledger) accepted its standing
authority in principle on 2026-10-10, on SandVault's model; [OQ-MP10](#OQ-MP10) decides the
exact grants.

- **P1 — Setup is administrative; routine use is not.** Account provisioning, installing or
  updating the protected helper and changing its authorization require an administrator or IT.
  Existing admin setup requirements do not authorize this new standing grant.
- **P2 — Root performs fixed operations on fixed target classes.** It never executes a supplied
  command, shell script, pack hook or installer. A host-controlled guest binary is not root code.
- **P3 — Authenticate the caller, then bind ownership.** A session name or environment marker
  selects nothing outside that caller's recorded authority.
- **P4 — Publish safe files before dispatch.** Credentials never pass through argv or logs;
  root never follows a guest-controlled path to read, write, change ownership or remove a target.
- **P5 — Unknown lifetime retains evidence and bytes.** A launcher's exit is not proof of
  descendant death, and password-free cleanup does not confer broader reclamation authority.

### Non-goals and authorities that still win

| Subject | What remains unchanged |
| :--- | :--- |
| Account password | `_yolojail` keeps its random password. No empty account password, login-window account or stored account password is proposed. |
| Keychain | [OQ-KC1](keychain-from-a-jail.md#8-decision-ledger) to [OQ-KC4](keychain-from-a-jail.md#8-decision-ledger), ruled 2026-10-10, own credential sharing and consumers; the keychain is always the host's, so no account keychain is provisioned, and [OQ-KC5](keychain-from-a-jail.md#OQ-KC5) owns how native clients reach it. No machine-wide System keychain, permanently unlocked empty-password default or host-credential import is selected here. |
| Workspace | [OQ-CW2](configurable-workspace-root.md#OQ-CW2) and its sibling questions still own the whitelist and root policy. The current neutral-ground check is not proof that every real home is excluded; this design neither widens it nor claims to fix it. |
| Runtime and authentication | No auto-detection change, provider/auth change, pack consent change or credential-refresh ownership change. Password-free sudo operations do not guarantee a client never presents a keychain or login dialog. |
| Services and stop | [The keeper's ownership](jail-lifetime-last-session-wins.md#994-what-the-keeper-owns-there), [stop semantics](jail-lifetime-last-session-wins.md#996-what-the-first-terminal-sees-and-how-the-keeper-ends) and held [HD-R1](host-daemon-ownership.md#HD-R1) stay independent. No resident root service, keeper migration or host-daemon deployment is proposed. |
| Guest trees | [JD-10](jail-daemon-on-macos-user-plan.md#JD-10) remains the retention contract, including possible writer dispatch. Its native overlap/restart/capture proof is still owed. |
| Other Mac feedback | A reported edit-to-check failure needs its own reproduction; this source review proves no such bug. Storage relocation and VM/backend work are separate. |

## 2. What the source actually does today

These are readings of the accepted source at `b2eeffc12`, not new Mac measurements.
There is no separate setup source file: the administrative command body is in
[`commands.go`](../../internal/macosuser/commands.go).

| Seam | Verified behavior | Gap addressed here |
| :--- | :--- | :--- |
| [Account setup](../../internal/macosuser/commands.go#L12-L59), [password setup](../../internal/macosuser/commands.go#L60-L69), [setup verdict](../../internal/macosuser/commands.go#L160-L172) | Creates/reuses the account and repairs a missing home; sets a random password for a newly created account; explicitly says sudo prompts per run and policy is unchanged. | Account existence does not authorize runtime operations. |
| [Shared preconditions](../../internal/macosuser/preconditions.go#L81-L259), [check caller](../../internal/cli/check/sections_macos.go#L71-L93), [launch caller](../../internal/macosuser/orchestrator.go#L866-L870) | Launch and check ask the same machine/workspace predicates. The list has no helper authorization probe. | Add the same noninteractive privilege finding to every real caller, before costly or privileged work. |
| [Root-file installer](../../internal/macosuser/real.go#L174-L189), [env protection](../../internal/macosuser/envfile.go#L187-L225) | Uses sudo mkdir, tee with content on stdin, then chmod. Env directory protection precedes writing and the guest read grant follows it. | A missing grant or expired cache can stop file installation; an account-switch-only rule is insufficient. |
| [Staging](../../internal/macosuser/runplan.go#L698-L710), [binary staging](../../internal/macosuser/macosuser.go#L202-L226) | Runtime stages guest binaries and trees using root commands. The running host yolo is copied to a fresh inode for guest execution. | Copying that host-writable program to a root-owned location must not make it the privileged helper. |
| [Bootstrap](../../internal/macosuser/runplan.go#L348-L386), [provisioning](../../internal/macosuser/provision.go#L146-L176), [session switch](../../internal/macosuser/macosuser.go#L1154-L1188) | Switches to `_yolojail` through sudo/env; the generator bootstrap is outside Seatbelt, vendor provisioning and the session are under it. No sudo login-shell rewriting. | All crossings need noninteractive authorization and a permanent privilege drop before guest execution. |
| [Supervisor](../../internal/macosuser/jaildaemon.go#L359-L378), [background stop](../../internal/macosuser/real.go#L370-L391) | Starts under sudo `-n`; stop signals its process group with a bounded grace. | Noninteractive does not mean cache-independent, nor does wrapper termination prove every descendant died. |
| [Session teardown](../../internal/macosuser/sessionfiles.go#L189-L230), [signal teardown](../../internal/macosuser/orchestrator.go#L1427-L1445) | Failed removal retains the host liveness record and warns about possible credentials. Only a signaled teardown adds sudo `-n`; ordinary exit keeps plain sudo. | Make every routine cleanup noninteractive without losing failure evidence or changing the session's exit status. |
| [Automatic capture/fork calls](../../internal/macosuser/capture.go#L705-L778), [scratch prepare](../../internal/macosuser/capture.go#L381-L406), [cleanup](../../internal/macosuser/capture.go#L810-L819) | Startup can reach separate root scratch preparation/removal and guest driver crossings. Protected capture files use the capture's cname, not a random launch session key. | Cover these callers without treating guest-writable scratch as a safe recursive root target. |
| [Guest-tree finish](../../internal/macosuser/orchestrator.go#L1393-L1425), [writer mark](../../internal/macosuser/orchestrator.go#L1188-L1209), [consumer mark](../../internal/macosuser/orchestrator.go#L1274-L1275) | Only an owned reservation with no writer/consumer dispatch is removable; otherwise exact-path disclosure and retention. | Preserve this rule, even if a new helper makes staging synchronous. |

The [SandVault research](../research/sandvault-macos-privileges.md) verifies the setup/runtime
split in v1.32.0 and already in v1.23.0. Its rules cannot be copied: yolo has dynamic
credential-bearing session files and a shared service account. The upstream root sync race is
reason to review every parent component, not merely use no-follow handling on the final leaf.

## 3. The authority boundary

### A protected helper, distinct from guest yolo

**Privilege helper** *(coined here)* means the installed program allowed to perform the fixed
root operations below for an enrolled host user. It is not the guest's staged yolo, a pack
program, a general shell or a machine-wide daemon.

The proposed substrate is a **short-lived helper started through noninteractive sudo**. This
is a mechanical recommendation, not a separate owner blocker. It stays alive for one request,
or becomes the guest child after dropping privileges; it owns no service after the launch.
A proposed fixed install location is
`/Library/PrivilegedHelperTools/dev.yolo-jail.macos-privilege`. No file is installed today.

The executable, policy, authorization registry and every mutable parent on their resolved
paths must be root-owned, with no host-user/group/guest write permission through mode bits
or an ACL. Account for macOS's system path aliases rather than treating `/var`'s system alias
as an attacker link. Beneath the established protected directory, follow no symbolic links.
Never authorize root execution from a Homebrew prefix, checkout, `os.Executable()` result,
pack tree, guest binary prefix or a symlink to any of them.

Administrator setup installs a fresh inode, checks its expected digest and platform, verifies
ownership/modes/ACLs and validates the generated sudo policy with `visudo` before publishing
it atomically. The digest comes from the artifact the administrator approved, **not** from a
host-writable file beside the executable. Code signing can supplement this trust; it cannot
replace protection of the binary and its parents.

### Authenticate an operating-system identity

Use numeric host UID plus its directory-service account identity, not `$USER`, `$LOGNAME`,
`SUDO_USER`, a request's claimed UID or a `YOLO_*` marker. Setup records the enrolled account's
UID and stable account UUID, and the fixed `_yolojail` UID/GID/UUID. Account replacement or UID
reuse refuses routine use until administrator repair; silently adopting a same-named account
would transfer the grant.

The proposed runtime entry in sudo policy matches **one enrolled host account and one exact
helper command with that account's numeric UID argument**, as root, with no `SETENV` or
preserved arbitrary environment. It does not match helper admin/update verbs, wildcard
commands, `/bin/sh`, `/usr/bin/env`, `cp`, `rm`, `chmod` or `chown`. The helper clears its
inherited environment, uses absolute system paths and refuses a non-root invocation.

For caller authentication independent of environment fields, the helper creates one private
local Unix socket under a protected control directory. Its fresh instance name is generated
by the helper, never a caller-supplied destination. Only the enrolled host UID can connect;
the helper checks the kernel-reported peer UID through macOS
[`getpeereid`](https://keith.github.io/xcode-man-pages/getpeereid.3.html) against its enrollment
and the exact sudo command's UID. The client connects as the normal host user, not through a
root or guest proxy. Socket permissions are a second check, not the identity source.

- The startup channel reports only protocol version and socket location, never secrets.
- A peer of `_yolojail`, another UID, an unenrolled user or an unresolved account is rejected
  before parsing a payload. Direct invocation without root also fails.
- No accepted descriptor, listener or privileged control connection reaches the guest exec.
  Standard terminal descriptors remain separate from the request channel, avoiding secrets
  on argv and avoiding sudo descriptor-preservation grants.
- There is no resident listener or root process to reuse between invocations. Failed handshake
  or client disconnect closes this instance; interrupted mutations retain their receipt.

This design needs native proof of socket peer checks and terminal/signal behavior. Linux can
exercise equivalent test seams, not prove macOS sudo or socket semantics.

### Session ownership is not caller-supplied authority

The [session key](../reference/macos-user-provisioning.md#the-session-key) stays
`<cname>.<16 lowercase hex digits>`; the workspace name is `[a-z0-9-]+`. A root-only receipt
maps the key to the authenticated host UID/UUID, fixed guest identity, resolved workspace,
artifact types and dispatch state. It is written before any artifact, and never stores a
credential value. Another owner's key or an existing unknown destination is a collision,
not something to adopt, overwrite or delete.

**Receipt** *(coined here)* is this root-written ownership/operation record, not a liveness
proof. The host's existing [liveness record](../reference/macos-user-provisioning.md#the-session-key)
still decides which ended sessions its sweep may submit. No root scan infers that a session
is gone because a receipt, launcher PID or age says so.

## 4. The complete bounded runtime surface

These are proposed protocol verbs, not installed CLI commands. Unknown verbs, fields,
versions, artifact types, malformed names, duplicate entries and oversized requests refuse
without dispatch. There is no root `exec`, path-based `copy`, recursive `chown` or general
`remove` request.

| Verb | Accepted input and target | Root may do | Root must not do |
| :--- | :--- | :--- | :--- |
| `probe` | Protocol version; authenticated enrolled caller | Check protected install, enrollment, guest identity and managed-directory integrity; report readiness without modifying session state. | Repair policy, install bytes, access a keychain or treat cached sudo authentication as a grant. |
| `prepare` | Session key, resolved workspace and a typed manifest of guest artifacts | Reserve the receipt; create profile, session env, daemon env and CA files; stage fixed guest-binary names and selected trees under `/var/yolo-jail`. | Accept destination paths, host source filenames, arbitrary modes/owners, hooks or executable root code. |
| `enter` | Owned key and one role: `bootstrap`, `provision`, `supervise`, `witness`, `session`, `capture` or `fork-build` | Verify committed artifacts and role, mark possible dispatch, then permanently switch to `_yolojail` and exec that role. | Choose another run-as account, source environment content as root, or retain a saved root identity. |
| `probe-access` | Typed context/cache access checks for the launch; paths are guest-side inputs | Drop to `_yolojail` before checking access; post-profile writes use that session's profile. | Open caller paths as root, run caller scripts as root or change their ACL/ownership. |
| `cleanup` | Owned key and caller's ended-session claim | Remove only the five fixed session-file types and private incomplete artifacts recorded for that key; record each result. | Accept a glob or deletion path; delete another owner/session, live/unknown sweep target, shared staging root or dispatched guest pack tree. |

### Staging without a root file reader

All source reads happen **as the host caller**. The helper receives a typed byte stream, not
paths to open as root. This prevents an authorized caller using staging to copy root-only
host files into a guest-readable directory. Tree entries contain relative names and bytes,
not archives with implicit path traversal or links to source inodes.

- **Binaries:** Only the fixed guest ship-set names may land in the guest prefix. The caller
  may supply their bytes for guest execution, never for helper replacement. Publish by fresh
  inode as the [existing Mach-O staging rule](../../internal/macosuser/macosuser.go#L202-L226)
  requires. No guest-prefix executable is ever run with root credentials.
- **Pack trees:** Use the host tree's unique leaf and the existing
  [JD-10 destination rule](jail-daemon-on-macos-user-plan.md#source-behavior-one-destination-one-writer-no-reuse).
  Exclusive reservation, no collision adoption, no merge/replacement, no mutation after commit.
- **Overlay/context trees:** Only their existing workspace-derived target classes. Replacement
  is serialized with the existing workspace staging/provisioning lock and an additional
  helper mutation lock on the exact target; it does not become a per-session lock.
- **Links:** Preserve declared guest-visible symbolic links as links, never follow them while
  materializing or changing permissions. Validate relative entry names, forbid descendants
  beneath a link and reject hardlinks, devices, sockets and other special nodes. A context
  link's guest-side target still passes existing context siting/profile rules; root never
  traverses that target. No source ACL, owner, set-ID bit or writable extended attribute is
  imported. Regular executable bits are preserved; permissions are set on opened objects.
- **Capture entries:** The fixed capture-store target and validated content key are data-only
  staging, with the current best-effort download fallback preserved. See
  [automatic capture](#automatic-capture-has-the-same-boundary-not-a-root-scratch-walker)
  for the separate preparation/guest execution/cleanup callers.
- **Endpoints:** Host-owned service endpoint files and their search/read grants remain with
  their host owner, using safe descriptor-based changes without escalation. An endpoint this
  caller cannot grant refuses with its owning service's remedy, not a root `chmod <path>`.
  [Keeper grant reuse](jail-lifetime-last-session-wins.md#995-how-a-second-launch-joins) is unchanged.

### Automatic capture has the same boundary, not a root scratch walker

The observed capture/fork preparation/removal callers are in
[the source map](#2-what-the-source-actually-does-today). Startup can reach them, so they
cannot retain a hidden interactive sudo path.

- **Scratch:** Administrator setup establishes the fixed neutral scratch root
  `/Users/Shared/yolo-captures`; per-program/per-build locks and target derivation remain as
  today. Subsequent scratch creation, materialization and deletion run as the host/guest,
  never root walking or changing ownership under a guest-writable tree.
- **Guest execution:** The proposed fixed `capture` and `fork-build` roles drop privileges
  before their driver or build script runs, with the capture profile and staging home, not
  the session profile/shared account home.
- **Protected artifacts:** Capture profile/env/CA files need unique per-attempt keys and
  helper ownership receipts; current cname naming is not evidence of per-attempt isolation.
  They remain under capture's own best-effort cleanup, not the ordinary-session sweep
  without a host liveness record.
- **Existing behavior:** Preserve capture fallback, admission ordering, success/failure
  results and possible-dispatch pack-tree retention. Standalone capture management is not
  a blanket new root grant.
- **Engineering gap:** Can shared scratch permissions permit host/guest cleanup even after
  installer permission changes? Exercise generated preparation/deletion against hostile
  fixture trees first, then a separately authorized native ACL experiment. Failed scratch
  cleanup must not turn a successful capture into failure or fall back to broad root removal.
  Native viability is unmeasured, not a new owner-policy question or prerequisite blocking
  source design/build.

### Profile and credential-file publication

The profile and four env/CA paths are exactly the existing
[session file set](../../internal/macosuser/sessionfiles.go#L97-L109). The helper derives them
from the validated key, never from request paths. The profile is root-owned `0444`; env and
CA files are root-owned `0600` with the existing guest-user read-only ACL. The profile stays directly under the protected state root, readable as today. Only the
env/CA parent is root-only `0700` with guest search, not list or write; those files receive
no other local account's read grant.
These grants name one shared `_yolojail` UID, as today: caller/session ownership prevents root
operations crossing host owners, but does **not** create per-workspace isolation of guest
secrets. Another guest process that obtains a file's name may have the same account rights;
[the keychain ruling](keychain-from-a-jail.md#8-decision-ledger) ([OQ-KC4](keychain-from-a-jail.md#8-decision-ledger): always the host's keychain) is not served by this helper.

Atomic publication means readers see either no committed file or its complete bytes and final
permissions. Creation uses an exclusive temporary regular file in the protected directory,
no-follow opens and descriptor-based ownership/ACL changes, then publishes the complete set
behind one commit receipt. `enter` accepts only that committed receipt. No partial file or
intermediate world-readable credential is reachable by a guest or another user.

Every parent beneath the trusted root is opened and checked without following links, including
owner, ACL and directory type; holding those directory descriptors prevents a check/use
replacement from redirecting later work. Refuse hardlinked destination files and unexpected
existing inodes. Publication never overwrites an existing session artifact. On write, flush,
ACL, commit or rename failure, no guest starts; rollback touches only this attempt's recorded
inodes, and failed rollback keeps its receipt and reports a precise remedy.

The helper treats profile text and environment bytes as **data**. Seatbelt compilation and
any shell sourcing happen only after privilege drop. An authorized host can choose a weaker
guest profile or malicious guest command, as it controls the launch today; this proposal is
not a sandbox against that host user. It must still prevent that user gaining arbitrary root
execution or root-file reads. No untrusted profile syntax is evaluated in the root process.

### Bounds and concurrency

Proposed non-configurable request limits: 64 KiB of control metadata, 1 MiB of profile text,
4 MiB per env/CA file, 8 GiB and 131,072 entries per tree stream, and 128 path components.
Reject absolute entry names, empty/dot/dot-dot components, NULs, conflicting case-folded names
and names exceeding the target filesystem's limits. Empty optional artifacts mean absent;
a required empty artifact refuses. No credentials are echoed in a limit error.

Handshake and mutation-lock waits are bounded to 10 seconds each; an input stream idle for
30 seconds fails. These are proposed safety budgets, not measured costs. Reconcile them
against real artifact fixtures before implementation; changing a bound requires a documented
revision, not an implicit unlimited fallback. Guest runtime has no helper-imposed deadline.

One helper invocation owns each mutation, serialized by root-managed per-target locks. Two
sessions of one workspace have different receipts and file names; one cannot replace the
other's files. Two requests for one key serialize: an identical completed operation returns
its recorded result, a conflicting payload refuses, and a partial attempt resumes only its
recorded work. Cleanup is idempotent for recorded missing files. It cannot race an `enter` or
unfinished preparation into deleting artifacts before dispatch; those state transitions share
the key's mutation lock. Dispatch is marked before attempting guest exec.

## 5. Setup, preflight and update are explicit

### Administrator setup and existing state

Run the front door as the intended **normal admin user**, never by prefixing the whole
command with sudo, preserving the [public setup guidance](../../userguide/guides/macos.md#L194-L199)
and [native runbook audience](../plans/runbooks/mac-macos-user-e2e.md#L6-L19). The future
`yolo macos-setup` reports the new standing authority and calls a separately admin-authorized
installation act. The routine sudo rule cannot invoke that act or enroll a new principal.
A managed/restricted installation that denies authorization is unsupported until its
administrator permits/repairs this exact setup; it stops with that remedy, not a repeated
password loop or a wider grant.

Setup verifies account identity/home, root install integrity, the runtime policy and a fresh
noninteractive `probe` **as the exact enrolled setup principal**. It declares ready only after
that exact probe passes. Workspace sharing and the native package floor keep their own
findings/remedies. No non-admin enrollment, wildcard group, all-users grant or transfer of
privilege is proposed. The audience guidance is not a new runtime admin-group membership
check and is not consent to deploy this standing grant.

Existing sessions continue under their old launcher. Setup never kills them or adopts their
flat root files or legacy guest trees. New helper-owned artifacts get receipts; unknown older
files remain untouched by routine helper cleanup. Existing host liveness records remain for
the old safe sweep, but upgrading must not preserve a broad passwordless root-removal path to
reach them. Administrator repair inspects exact legacy targets only after the corresponding
old sessions end; guest-tree no-holder uncertainty still retains the tree.

### One preflight shared by launch, check and automatic capture

The privilege probe joins the shared native readiness predicates before the native build,
automatic capture, context/cache guest probes or root staging. `RunMacosUser`, the top-level
run preflight and [automatic capture caller](../../internal/cli/run/autocapture.go#L112-L130)
must all consume it; testing only the probe's callee
would miss a removed caller. The [fork executor](../../internal/macosuser/capture.go#L1244-L1250)
shares capture's privileged steps and must consume the same readiness result. `yolo check --no-build` reports the same outcome without
provisioning, policy changes or a keychain write.

| Observation | Disposition and easiest next step |
| :--- | :--- |
| Helper/policy absent, unenrolled caller, authorization denied | Refuse before expensive work. Run `yolo macos-setup` as the documented normal admin user with explicit approval; if managed policy denies it, ask the administrator to authorize/repair that exact setup. |
| Old protocol or helper digest/install differs | Refuse; update through administrator setup for this yolo version, then run `yolo check --no-build`. Never auto-copy host yolo over the helper. |
| Wrong owner, writable parent/ACL, link substitution, guest UUID/UID mismatch | Refuse as unsafe; administrator repair through `yolo macos-setup`, then the same check. Do not execute the suspect helper merely to ask if it is safe. |
| Guest home absent | Existing `yolo macos-setup` home repair, independent of the privilege rule. |
| Workspace in a home or unshared | Existing move-to-neutral-ground or `yolo macos-fix-permissions <resolved-workspace>` remedy. No privilege repair or broader ACL grant substitutes for it. |
| Probe timeout or inaccessible install fact | Unknown is a refusal, not ready. Run the same check to inspect the named fact; if persistent, ask the administrator to repair setup. |

All routine helper starts use sudo `-n`. A noninteractive pass with an invalidated cache is
the acceptance test; sudo's cached permission to run a broader command is not evidence that
this installation works. Runtime operations repeat identity/path checks at use, because a
preflight pass is not an enduring authorization token.

### Administrative updates and revocation

Protected helper upgrades require a new administrator-approved artifact and policy validation.
Publish a fresh generation atomically; an already executing helper keeps its inode. Compatible
old guest sessions remain untouched. Incompatible new clients refuse with the setup remedy,
not a restart of somebody else's session. Keep old cleanup protocol support while its receipts
exist, or refuse the update with an exact outstanding-session explanation; do not strand
credential cleanup silently.

Revocation first prevents new helper starts, then permits an explicitly administrative,
exact-receipt cleanup after affected launchers end. Uninstall removes only enrolled helper
state it can prove unused. Account teardown stays a separate administrator act. No automatic
account deletion, keychain reset, user-wide `launchctl bootout` or `pkill -u` is introduced.

## 6. Normal switch, exit, stop and restart

### The crossing into the guest

`enter` validates its owned committed receipt, selects the fixed role and closes control,
root-directory and lock descriptors before guest execution. It sets the `_yolojail` primary
and supplementary groups and permanently drops real, effective and saved UID/GID; any error
aborts before exec. A test must attempt to regain UID 0, not just print an effective UID.
No root process sources the env file or resolves a command on the caller's PATH.

The fixed system sandbox executable is `/usr/bin/sandbox-exec`, using only this attempt's
profile. The role's executable is fixed for bootstrap/supervision/witness/capture/fork driving;
provisioning scripts, fork build lines and session argv are guest commands only. Bootstrap
remains the existing generator-only step outside Seatbelt **as the guest account**; this
proposal does not move hooks or vendor installers into that exception. Provisioning, witness,
supervisor, session and capture/fork drivers use their respective Seatbelt profile.

Build the guest environment from the existing
[protected identity/PATH set](../reference/macos-user-provisioning.md#the-session-key) and
[`SandboxPath`](../../internal/macosuser/macosuser.go#L1093-L1111), not the helper's environment:
fixed home/user/shell, home-tool prefixes in their current order, the native package profile,
the staged guest prefix and system directories. Composed values cross in owned files; no
sudo `--login` conversion, caller PATH lookup or secret-bearing argv appears.

The helper must preserve the current child/terminal signal contract. This includes forwarding
SIGTERM/SIGHUP during the session and allowing terminal SIGINT/SIGQUIT to reach the guest,
with teardown still reached by the host launcher's
[signal arm](../../internal/cli/run/macosuserarm.go#L13-L40). The new crossing needs its own
native proof; current sudo relay behavior is not proof that a replacement works.

### Cleanup always returns without a password prompt

Normal exit, failed startup and signal teardown submit the same bounded `cleanup` operation
noninteractively. The guest supervisor stop precedes its daemon env-file removal; the
session's fixed credential/profile/CA removal precedes ending its host liveness record.
The cleanup reports **each file's result**, including already absent, rather than just the
helper process's exit code.

A failed or interrupted cleanup:

1. Keeps the host liveness record, releasing its lock as today, and retains the root receipt
   for any possibly remaining file. It does not turn the command's successful exit into a failure.
2. Warns that a credential-bearing file may remain, naming the session key and affected artifact
   types, never values. No success claim follows an unknown result.
3. Names the next launch by this same host user as the safe retry, and the installation repair
   `yolo macos-setup` when authorization is broken. A proposed convenience command,
   `yolo macos-cleanup --session <key>`, invokes this same owned-key operation after the existing
   liveness claim; it is **not available today** and accepts no path/glob/account argument.

The existing same-user sweep keeps held, malformed, unreadable or replaced host records.
Only a successfully claimed free record may request cleanup. The helper additionally verifies
root ownership receipts; a forged host record cannot delete another UID's artifacts.
A missing host record still warns as today, not a new launch prerequisite. A root receipt can
support administrator diagnosis but does not authorize automatic sweeping without the
existing liveness proof. Deleting files revokes their future reads, not credentials a process
already copied into memory, and is not a secure-erasure claim.

### Overlap and retained trees are separate from file cleanup

- **Overlap:** Same-workspace sessions keep separate root session files, guest supervisors and
  guest pack trees; shared host services remain the keeper's. The account-home hold still
  refuses a different workspace while links are in use. No helper allows a refused overlap.
- **Stop:** `yolo stop` addresses recorded launchers/keeper for this workspace/notch as today.
  No helper verb signals arbitrary PIDs, the whole `_yolojail` account or other workspaces.
- **Restart:** Guest daemon restarts read the same committed tree they started from. Neither
  helper update nor a later session may substitute that tree. A joiner's existing restart
  restrictions and additive-service design are not settled by this proposal.
- **Retention:** Mark both writer and guest-consumer possible dispatch before sending a request.
  Loss of an acknowledgement, helper exit or supervisor stop is not a no-holder proof.
  [JD-10](jail-daemon-on-macos-user-plan.md#ownership-and-cleanup-unknown-keeps-the-bytes) retains
  the exact guest tree after either dispatch, even on normal return. A session-file sweep never
  removes it. Pre-dispatch cleanup can remove only its own exclusive reservation; collision
  recovery still recommends a fresh invocation and exact-path inspection, not deletion.

## 7. Security costs, alternatives and verification

### Costs and rejected shortcuts

| Approach | Verdict and cost |
| :--- | :--- |
| Protected, authenticated, short-lived helper | **Proposed.** Adds a root parser, installation/update trust and root ownership receipts; buys cache-independent startup/cleanup without a resident service. |
| SandVault's rule list copied verbatim | **Rejected.** Its model is adopted ([OQ-MP1](#decision-ledger)), but its rules lack yolo's session-file grants and include account-wide removal; see [the grant inventory](#8-the-grant-inventory). |
| Passwordless account switching only | **Insufficient.** Root file writes, staging and cleanup still need authorization. |
| Root `yolo internal ...` from a host-writable install | **Rejected.** Every host/pack dependency of that executable becomes root code. Root-owned guest staging does not repair the provenance of a privileged update. |
| Root shell/copy/remove/chmod rules with flexible paths | **Rejected.** Dynamic session inputs turn them into general root file authority. |
| Keep refreshing sudo's timestamp | **Rejected.** It retains broad cached authority and fails on long sessions, terminal changes or cache expiry. |
| Resident root daemon / giving the keeper guest ownership | **Not proposed.** Neither is required to remove prompts; either needs its own lifetime/security decision and cannot silently supersede the current keeper boundary. |

| Residual risk | Required mitigation or limitation |
| :--- | :--- |
| Root request-parser or filesystem bug | Closed verbs/types, finite limits, kernel peer identity, no root source reads, descriptor-based no-follow operations and independent security review before installation. |
| Compromised enrolled host user | Can request the bounded operations and choose guest content/profile. That is standing authority beyond a password prompt, accepted in principle by [OQ-MP1](#decision-ledger); [OQ-MP10](#OQ-MP10) settles its extent. It is never arbitrary root code or host credential export. |
| Account or executable replacement during a session | Revalidate at use, root-protected update chain, stable identity matching and compatible cleanup; fail closed with administrator remedy. |
| Disk full / process killed during credential write or deletion | Incomplete artifacts remain private, receipts retained, per-file failure warning and safe retry. SIGKILL cannot promise immediate cleanup. |
| Guest processes survive wrapper termination | No descendant-death assertion; preserve tree retention and record observations separately from stop/file-removal results. |
| Managed policy or native ACL/terminal difference | Real Mac verification; Linux checks are structural only. A denied managed installation names IT, not an authentication loop. |

### Test strategy: callers first, before granting privileges

The following are **tests to add**, not tests claimed to exist or pass. Existing
[session-file tests](../../internal/macosuser/sessionfiles_test.go),
[env-file tests](../../internal/macosuser/envfile_test.go),
[signal tests](../../internal/macosuser/terminate_test.go) and
[guest-tree tests](../../internal/macosuser/packroot_test.go) provide useful source-named seams;
they do not implement this helper.

| Layer | Required observations | Mutation that must fail a test |
| :--- | :--- | :--- |
| Actual launch/check/capture callers, unit-first | Missing/stale/unsafe/denied helper refuses before build, guest probe and capture; same check finding/remedy; dry-run creates no root state; every runtime invocation is noninteractive. | Delete one caller's preflight, reintroduce plain sudo or continue after a refused probe. |
| Root request boundary | Wrong UID/UUID, guest UID, unenrolled caller, spoofed environment/claimed identity, foreign key, unknown verb/type/version and over-limit inputs are refused before mutation. | Trust `$USER`/`SUDO_UID` alone, accept a different peer or allow a caller destination path. |
| File and tree operations | Parent/final symlinks, hardlinks, link-to-external targets, special files, case collisions and deterministic replacement races never read/write/chown outside the owned target; interrupted publish grants no reader a partial secret. | Replace descriptor walk with path-based open, follow a link during chmod, publish before mode/ACL completion or follow a host source path as root. |
| Privilege drop and exec | Role argv/profile/PATH correct; guest cannot regain root; no request/root descriptors leak; bootstrap exception runs only fixed guest generator, vendor code remains confined. | Exec before dropping saved UID/GID, retain an extra group/descriptor or move env sourcing before the drop. |
| Teardown and lifetime callers | Removal failure retains both records and warning; all return paths use bounded cleanup; sibling files and dispatched pack trees remain; lost response means unknown; exit status preserved. | Delete teardown/sweep call, remove record after failure, mark dispatch only after return or infer tree death from a supervisor's exit. |
| Native prepared Mac, separately authorized | Administrator setup once; invalidate sudo cache before startup and again before exit; no password prompt for probe, files, bootstrap, stage, supervisor, witness, session or cleanup. Check terminal and no-terminal launches. | A cached broad sudo rule must not hide missing installed runtime policy. |
| Native signal/overlap/failure matrix | SIGINT/QUIT, TERM/HUP during setup/session/teardown, SIGKILL leftovers; same-workspace overlap, other-workspace refusal, stop, daemon restart, capture; faulted file install/delete, stale/revoked policy and helper update with old sessions. | A's exit or B's start must not change A's restart bytes, delete sibling files or claim descendants dead without observation. |

Use harmless fixture commands and secret canaries, not interactive agents, model APIs or real
credential migration. Native tests check ownership, ACLs, readable/unreadable paths, no root
capability and the absence of sudo prompts with invalidated timestamps. Harmless keychain
item/reboot tests remain [the keychain design's probe](keychain-from-a-jail.md#41-macos-user-no-seam-and-no-keychain-provisioning),
not an acceptance shortcut or unlock-policy ruling here.

### What may happen next

First walk through [the grant inventory](#8-the-grant-inventory) with the owner and record
[OQ-MP10](#OQ-MP10). Then author a code-grounded implementation plan,
review the root request/installation boundary independently, and implement and test the source
contract. Native installation and measurement are separate: obtain explicit authorization for
an isolated native test installation before either occurs. That installation approval is not
an additional prerequisite for source planning, implementation or offline verification.
No setup, sudoers, account, service, credentials or keychain action follows from writing or
accepting this document.

A future new `cmd/` binary must satisfy [the repository's ship-set rule](../../AGENTS.md#architecture):
add it to both [`flake.nix`](../../flake.nix) and
[`stage-source-bundle.sh`](../../scripts/stage-source-bundle.sh), keep imported source inside
`goSrc`'s fileset and preserve the host/guest install distinction. A helper is not implicitly
host-installed by `just install`, whose host ship set is currently only yolo. If changed
packages alter an official pack program, [the digest re-pin rule](../../AGENTS.md#build--deploy--the-traps)
applies; a standalone helper does not invent a pack pin for itself. Administrator-approved
helper publication is a separate distribution contract, not today's guest staging path.

## 8. The grant inventory

[OQ-MP1](#decision-ledger) settled the direction on 2026-10-10: adopt SandVault's model. Setup,
with an administrator's password, installs a validated sudo policy naming one host user; every
routine launch then runs only what that policy names, probes it noninteractively first, and
refuses with a repair command when it fails. What is **not** settled is the list itself. This
section is that list, for one sitting with the owner; [OQ-MP10](#OQ-MP10) is the sign-off.

SandVault citations are to v1.32.0's
[`sv`](https://github.com/webcoyote/sandvault/blob/ad05889e9f5f7d63460d6e56a2586c724b14bcd0/sv)
at the commit [the research](../research/sandvault-macos-privileges.md) checked. Every SandVault
fact here is from reading that source, not from running it. ⚠ marks a grant beyond SandVault's.

### What SandVault's setup grants, exactly

Its [generated policy](https://github.com/webcoyote/sandvault/blob/ad05889e9f5f7d63460d6e56a2586c724b14bcd0/sv#L1553-L1565),
installed at `/etc/sudoers.d/50-nopasswd-for-sandvault-<host user>` through a root-owned
temporary file, `visudo -c` and a rename
([L1566-L1579](https://github.com/webcoyote/sandvault/blob/ad05889e9f5f7d63460d6e56a2586c724b14bcd0/sv#L1566-L1579)):

```text
<host user> ALL=(sandvault-<host user>) NOPASSWD: /bin/zsh
<host user> ALL=(sandvault-<host user>) NOPASSWD: /usr/bin/env
<host user> ALL=(sandvault-<host user>) NOPASSWD: /usr/bin/true
<host user> ALL=(root) NOPASSWD: /var/sandvault/buildhome-sandvault-<host user>
<host user> ALL=(root) NOPASSWD: /bin/launchctl bootout user/<sandbox uid>
<host user> ALL=(root) NOPASSWD: /usr/bin/pkill -9 -u sandvault-<host user>
```

Three facts about that model matter for yolo:

- **Each host user gets their own sandbox account** (`sandvault-<host user>`,
  [L135](https://github.com/webcoyote/sandvault/blob/ad05889e9f5f7d63460d6e56a2586c724b14bcd0/sv#L135)).
  Yolo has one machine-wide `_yolojail`.
- **Everything that varies per launch runs as the sandbox account, not root.** SandVault's
  Seatbelt profile is written once, at setup
  ([L1693-L1694](https://github.com/webcoyote/sandvault/blob/ad05889e9f5f7d63460d6e56a2586c724b14bcd0/sv#L1693-L1694)),
  and the session's environment crosses on `env -i`'s argv
  ([L2119-L2133](https://github.com/webcoyote/sandvault/blob/ad05889e9f5f7d63460d6e56a2586c724b14bcd0/sv#L2119-L2133)).
  Its one per-launch root act is the home sync
  ([L1771-L1779](https://github.com/webcoyote/sandvault/blob/ad05889e9f5f7d63460d6e56a2586c724b14bcd0/sv#L1771-L1779)).
- **Its home sync reads its source as root.** The script
  ([L1495-L1541](https://github.com/webcoyote/sandvault/blob/ad05889e9f5f7d63460d6e56a2586c724b14bcd0/sv#L1495-L1541))
  runs `rsync --copy-unsafe-links` as root from the install's `guest/home`, which is the
  checkout or Homebrew prefix the script ran from
  ([L8-L21](https://github.com/webcoyote/sandvault/blob/ad05889e9f5f7d63460d6e56a2586c724b14bcd0/sv#L8-L21)).
  That flag copies the file a link pointing outside the tree names. From the source alone, a
  host user who can write that tree can have root copy a root-only file into the sandbox
  home. Not reproduced. Yolo's design deliberately does not copy this: root reads no source path
  ([staging](#staging-without-a-root-file-reader)).

### What yolo's setup would grant

Setup itself (**G0**) runs with an administrator's password and grants nothing by itself. The
standing grants are **G1** to **G10**. "Today" means the current plain-`sudo` source.

| # | Grant | Exact command or system call | Why root, or another account | SandVault's equivalent | Bound and authentication | Without it |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| G0 | Setup: install the helper and the policy | Write `/Library/PrivilegedHelperTools/dev.yolo-jail.macos-privilege`, digest-checked; write the policy to a root temporary file, `visudo -c -f`, rename into `/etc/sudoers.d/`; create `/var/yolo-jail` and `/Users/Shared/yolo-captures` | Writes system directories | Same act: `sv build --rebuild` | Administrator password, once; [setup](#administrator-setup-and-existing-state) | Nothing is granted |
| G1 | Run a fixed role as `_yolojail`: bootstrap, provision, supervise, witness, session, capture, fork-build, and the access probes | Today `sudo --user=_yolojail /usr/bin/env -i … <role>` ([session](../../internal/macosuser/macosuser.go#L1154-L1188), [provision](../../internal/macosuser/provision.go#L146-L176)). Proposed: helper `enter` calls `setgroups`, `setgid`, `setuid` to `_yolojail`, then `execve` | Changing to another account needs root; today sudo is that root | `(sandvault-<user>) NOPASSWD: /bin/zsh, /usr/bin/env` | Run-as `_yolojail` only. ⚠ The proposal puts a yolo root process in front of every guest start; SandVault leaves the switch to sudo. A rule `(_yolojail) NOPASSWD: /usr/bin/env` is SandVault's exact shape and needs no helper. ⚠ The guest account is shared by every enrolled host user | Password prompt whenever sudo's cache has expired |
| G2 | Readiness probe | SandVault: `sudo -n --user=<account> /usr/bin/true` ([L2094-L2101](https://github.com/webcoyote/sandvault/blob/ad05889e9f5f7d63460d6e56a2586c724b14bcd0/sv#L2094-L2101)). Proposed: helper `probe`, read-only | Reading the root-owned install and enrollment | Same purpose. ⚠ The proposed probe runs as root; SandVault's runs as the sandbox account | Read-only; [shared preflight](#one-preflight-shared-by-launch-check-and-automatic-capture) | A broken install prompts or fails mid-launch |
| G3 | Write this launch's Seatbelt profile | Today `sudo mkdir -p`, `sudo tee /var/yolo-jail/profile-<key>.sb`, `sudo chmod 0444` ([installer](../../internal/macosuser/real.go#L174-L189)). Proposed: exclusive no-follow create in a held directory, `fchmod 0444`, `renameat` | The confined guest must not edit its own profile; the directory is root-owned | ⚠ None at runtime: SandVault writes one profile at setup. Yolo's differs per launch | Path derived from the session key; data only, never compiled as root | Per-launch prompt |
| G4 | Write the session and daemon env files, which hold credentials | Today `mkdir`, `chmod 0700`, `chmod +a "user:_yolojail allow search"` on `env/`; `sudo tee`, `chmod 0600`, a guest read ACL on each file ([env protection](../../internal/macosuser/envfile.go#L187-L225)) | Guest can read but not write or list; no other local account can read | ⚠ None: SandVault puts the environment on argv | Fixed names from the key; root-only `0700` parent; [publication](#profile-and-credential-file-publication) | Per-launch prompt |
| G5 | Write the CA bundle and extra-CA files | Same commands as G4, only when the System keychain adds a CA | As G4 | ⚠ None | As G4 | Per-launch prompt |
| G6 | Stage yolo's guest binaries | Today `sudo mkdir -p`, `cp -f <host yolo> <dst>.new`, `chmod a+rX`, `mv -f` ([staging](../../internal/macosuser/macosuser.go#L202-L226)). Proposed: the caller streams bytes; the helper writes a fresh inode | The guest may run them but not alter them; the prefix is root-owned | Root home sync. Its result ends guest-owned; yolo's stays root-owned | Fixed ship-set names only; root reads no source path and runs none of these bytes | Per-launch prompt |
| G7 | Stage pack, overlay and context trees | Today root copy and permission commands ([staging](../../internal/macosuser/runplan.go#L698-L710)). Proposed: a typed tree stream, no links followed | As G6 | Root home sync | Existing target classes; [JD-10](jail-daemon-on-macos-user-plan.md#JD-10) reservation, no merge | Per-launch prompt |
| G8 | Remove this session's own files at exit or sweep | Today `sudo rm -f` on the [five fixed paths](../../internal/macosuser/sessionfiles.go#L97-L109) ([teardown](../../internal/macosuser/sessionfiles.go#L189-L230)). Proposed: helper `cleanup`, `unlinkat` on recorded inodes | The files are root-owned in a root-only directory | ⚠ None per session. SandVault's root removals are account-wide | Owned key, receipt and a claimed liveness record; no path or glob | Prompt at exit; on failure the credential file stays, with today's warning |
| G9 | Capture and fork-build's protected files | G3 to G5 and G8 again, keyed per capture attempt ([scratch](../../internal/macosuser/capture.go#L381-L406), [cleanup](../../internal/macosuser/capture.go#L810-L819)) | As G3 to G5 | ⚠ None | Per-attempt key and receipt; scratch itself runs without root | Prompt when startup reaches capture |
| G10 | Ownership receipts | Helper writes and reads root-only records under its protected directory | Records must not be forgeable by the host or guest | ⚠ None | Written only by the helper; [receipts](#session-ownership-is-not-caller-supplied-authority) | No helper, so nothing to record |

### What yolo would not take

- **SandVault's two account-wide root removals**, `launchctl bootout user/<uid>` and
  `pkill -9 -u <account>`. SandVault now runs them only at uninstall
  ([L470-L499](https://github.com/webcoyote/sandvault/blob/ad05889e9f5f7d63460d6e56a2586c724b14bcd0/sv#L470-L499)),
  yet still grants them standing. Yolo stops its own process group without root
  ([stop](../../internal/macosuser/real.go#L370-L391)), and `_yolojail` serves other workspaces.
- **A root program reading host-writable paths**, unlike the home sync above.
- **A per-user rule by name alone.** SandVault matches the host user name. The proposal also
  checks the numeric UID and account UUID ([identity](#authenticate-an-operating-system-identity)).

### Points to settle in the walkthrough

1. **G1's shape.** SandVault's literal run-as-guest rule, or the helper's `enter`?
2. **Root for G3 to G9 at all.** These are beyond SandVault because yolo writes per-launch files
   into root-owned `/var/yolo-jail`. A host-owned directory might give the guest the same
   read-only view without root. Nobody has evaluated that.
3. **The shared guest account.** SandVault has one account per host user; yolo has one per
   machine. Is enrolling a second host user allowed?
4. **The shape of the policy.** SandVault writes several narrow sudo rules for system
   commands. This design has one rule for its own helper, and the helper's verbs are the grants.

## 9. Open question

1. 💬 **OQ-MP10: Does the owner sign off on the grant inventory as listed?**

   The direction is ruled ([OQ-MP1](#decision-ledger)). Signing off accepts G1 to G10 in
   [the grant inventory](#8-the-grant-inventory), with each walkthrough point decided. That
   could mean removing a grant or taking SandVault's form. It still authorizes no build or
   installation.

   <!-- vantage: question id=OQ-MP10 leaning="Undecided until the walkthrough. Expected: G1 in SandVault's literal run-as-guest form, and G3 to G9 only if a host-owned directory cannot do the job." -->

   _Leaning:_ Undecided until the walkthrough. Expected: G1 in SandVault's literal run-as-guest
   form, and G3 to G9 only if a host-owned directory cannot do the job.

   **Answer:**

   > _(empty — fill in when decided)_

## 10. Decision record and planning filter

Helper substrate and safety budgets are **proposals**, not accepted decisions. Existing keychain,
workspace, keeper and guest-tree rulings remain in
[their authorities](#non-goals-and-authorities-that-still-win); no answered card is reopened.

### Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-MP1 | **Partial: yes to A in principle.** Explicit administrator setup may grant the enrolled host user standing, bounded authority for routine startup and exit, and SandVault's model is the one to adopt. The owner's words: *"Yes, but before we do this, we need to talk about precisely what it is we're granting and how and why. This is exactly what we need to adopt from SandVault. We should use their model, but still, got to go through this more specifically."* Not authorization to build or install; the exact grants wait on [OQ-MP10](#OQ-MP10). | 2026-10-10 | [§8](#8-the-grant-inventory) | No: nothing built or installed |

For this privilege policy alone, paste this into Vantage's planning-page Filter box:

```text
path:/docs/design/macos-user-privilege-lifecycle.md is:open
```

For the independent native privilege/keychain/workspace policies together:

```text
path:/docs/design/macos-user-privilege-lifecycle.md path:/docs/design/keychain-from-a-jail.md path:/docs/design/configurable-workspace-root.md is:open
```

These are document filters, not a copied question/status inventory. Open the planning page
with `g p`, then press `/` to paste the filter. To generate a checkout-specific planning link:

```console
UV_OFFLINE=1 /workspace/scripts/vantage-check.sh index --filter 'path:/docs/design/macos-user-privilege-lifecycle.md is:open'
```

Use the command from the checkout Vantage serves; a link generated in an isolated worktree
identifies that worktree, not another served checkout. Neither filter changes roadmap order,
settles the other policies or authorizes native operations.

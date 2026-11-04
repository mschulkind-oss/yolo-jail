---
title: "Breaking the security-session chain that `sudo -u _yolojail` keeps: why it holds, how to re-verify it, and every way to cut it"
date: 2026-11-03
status: draft
stage: DESIGN
next: "Owner's Mac agent runs witnesses R1–R6 (§2) after [OQ-KS6](#OQ-KS6) authorizes R3 and R5, and H1 (§3.3) once the owner allows a witness root helper; the owner rules [OQ-KS1](#OQ-KS1) to OQ-KS9"
tags: [research, design-input, macos, macos-user, keychain, securityd, audit-session, copilot]
summary: "sudo changes a process's UIDs and nothing else in its kernel credential, so the macOS audit session id (ASID) and the audit user id (AUID) cross `sudo -u` unchanged, and securityd keys lock-all, its password dialogs and its keybag routines by that session. That is why an account-mode jail can lock the owner's keychains and raise dialogs on the owner's screen, while it still cannot read a keychain secret: the database bytes come from a file that POSIX permissions keep from `_yolojail`. Making a new session takes root in the kernel, but launchd does it for any user's own LaunchAgent that sets SessionCreate, which the 10-30 run measured without root. The owner then asked whether one passwordless sudo rule to a root helper that makes a fresh session would solve everything: it would cut lock-all and dialogs off the owner's session in both modes, and with it Copilot can reach SecurityServer in run-as-you without the owner's session exposed, which closes #65 for practical purposes. It does not solve everything: run-as-you would need setup for the first time, a root program runs at every launch, and the Mach bootstrap (clipboard, notifications) and the keybag routines are not keyed by the session alone. It is now the leaning, with the no-root launchd session host as the fallback; the cheap stopgap is denying SecurityServer in the account profile, which the 10-30 report wrongly says is already done."
---

# Breaking the security-session chain that `sudo -u _yolojail` keeps

**Status:** draft, 2026-11-03. Research and witness design only, written from a Linux jail at
`b1d489f2b`: nothing ran on a Mac, and no keychain was touched. It answers the owner's question of
2026-11-03:

> allowing lock and prompt doesn't sound too bad if it's documented. can we break that chain sudo
> -u creates? that seems weird that after a sudo something as sensitive as keychain is still
> connected? I'd like to really verify that's true.

The owner's follow-up, the same day, is answered in [§3.2](#32-route-4-in-depth-one-passwordless-rule-to-a-root-session-helper)
("pseudo" is sudo and "route" is root in the dictation):

> If all we need is a sudo command to make this safe, we already have provisions for setting up
> certain commands that can be run without sudo [a password]. For both modes, can we just require
> that you set that up and then just use root to swap out the security session? Would that solve
> all of our issues?

It builds on, and does not repeat, the
[Copilot keychain-boundary research](../research/copilot-keychain-boundary.md) ([§1.2](../research/copilot-keychain-boundary.md#12-what-allowing-that-one-service-exposes), [§1.5](../research/copilot-keychain-boundary.md#15-run-as-you-versus-the-dedicated-account), option
(b)) and the [10-30 witness run](../lab-reports/2026-10-30-mac-copilot-keychain-witnesses-and-issue-66.md)
(W1, W5, W6).

## Terms

- **Audit session**, or **security session**: the macOS kernel's grouping of processes for auditing.
  Every process credential carries an `auditinfo_addr` with an **ASID** (audit session id), an
  **AUID** (audit user id, the user who logged in) and session **flags**. securityd keeps one
  `Session` object per ASID. Not a POSIX `setsid()` session, and not a terminal.
- **Originator UID**: securityd's name for the session's AUID (`Session::originatorUid` returns
  `mAudit.uid()`).
- **The chain**: coined here for the owner's phrase. It is the fact that the agent of an
  account-mode jail, which runs as `_yolojail`, is in the owner's audit session with the owner's AUID,
  because every hop from the owner's shell to the agent inherits both.
- **Account mode** runs the agent as the dedicated user `_yolojail`; **run-as-you** (self mode,
  `"macos_user_mode": "self"`) runs it as the owner
  ([macOS user model plan](../design/macos-user-model-plan.md)).
- **The crossing**: yolo's one way into `_yolojail`, a sudo rule
  `<owner> ALL=(_yolojail) NOPASSWD: /usr/bin/env`
  ([`privilege.go`](../../internal/macosuser/privilege.go)). The agent's command line is
  `/usr/bin/sudo -n --set-home --user=_yolojail /usr/bin/env -i <env> /usr/bin/sandbox-exec -f <profile> -- …`
  (`LaunchArgvWithGuard` in [`macosuser.go`](../../internal/macosuser/macosuser.go)). No `launchctl` is
  used anywhere in the launch path, and the account-mode profile is `(allow default)` with **no**
  `mach-lookup` deny ([`seatbelt.go`](../../internal/macosuser/seatbelt.go); the deny of
  `com.apple.SecurityServer` is in the run-as-you profile only, in
  [`seatbeltdeny.go`](../../internal/macosuser/seatbeltdeny.go)).
- **p0, p2, vd**: the run-as-you profile; p0 plus `com.apple.SecurityServer`; p2 plus read of
  `SystemRootCertificates.keychain` and `SystemTrustSettings.plist`. They are defined in the
  [10-30 report](../lab-reports/2026-10-30-mac-copilot-keychain-witnesses-and-issue-66.md).
- **Session host**: coined here. It is a small yolo process that launchd starts in a fresh audit
  session, which then starts the jail, so that everything the jail runs inherits the fresh session
  instead of the owner's ([§3](#3-every-way-to-break-the-chain)).
- **A, B**: the disposable accounts `yolo-witness` and `yolo-witness2` that the witnesses use
  ([§2](#2-re-verification-witnesses)).

**Evidence marks:** **[S]** read from Apple's or sudo's open source (`main`, fetched 2026-11-03);
**[A]** Apple documentation or man page; **[M]** measured on the owner's Mac, with the witness named;
**[I]** inferred; **[U]** unverified, with the witness that would settle it.

## 1. Why the session survives `sudo -u`

### 1.1 The mechanism

| # | Claim | Mark |
| :--- | :--- | :--- |
| 1 | The ASID, AUID and flags live in the process credential (`cr_audit`), beside the POSIX UIDs. The Mach audit token, which the kernel stamps on every Mach message, is built from that credential: `val[0]` is the AUID, `val[6]` the ASID ([`kern_prot.c`, `proc_calc_audit_token`](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_prot.c)) | [S] |
| 2 | `fork` copies the parent's credential and bumps the session's process count (`AUDIT_SESSION_PROCNEW` in [`kern_fork.c`](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_fork.c)); `exec` keeps the credential | [S] |
| 3 | `setuid`/`setresuid` rewrite only `cr_uid`, `cr_ruid`, `cr_svuid` and `cr_gmuid`; `cr_audit` is untouched (`kauth_cred_model_setresuid` in [`kern_credential.c`](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_credential.c)). So the process keeps the ASID **and** the AUID, which stays the owner's | [S] |
| 4 | sudo never sets the audit session. Its BSM code only *reads* it (`getaudit_addr`) to write audit records ([`plugins/sudoers/bsm_audit.c`](https://github.com/sudo-project/sudo/blob/main/plugins/sudoers/bsm_audit.c)). macOS ships sudo with its own build and PAM stack, which this research did not read | [S] for upstream sudo; [I] for Apple's build |
| 5 | Changing the session needs root: `setaudit_addr` calls `suser()` ([`audit_syscalls.c`](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/security/audit/audit_syscalls.c)). Security.framework's `SessionCreate` is now only that syscall with `AU_ASSIGN_ASID` (`SessionCreate` in [`Authorization.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/OSX/libsecurity_authorization/lib/Authorization.cpp), `AuditInfo::create` in [`ccaudit.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/OSX/libsecurity_utilities/lib/ccaudit.cpp)) | [S] |
| 6 | The one unprivileged way into another session is `audit_session_join` with a send right to that session's port; the kernel checks nothing else ([`audit_session.c`](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/security/audit/audit_session.c)). A process gets such a right only from a process already in the session (`audit_session_self`), and the default session cannot be joined | [S] |
| 7 | securityd re-reads the caller's ASID from the audit token of every request (`Server::connection` → `Process::checkSession`, and `changeSession` at setup, in [`server.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/securityd/src/server.cpp)). The client cannot choose it | [S] |
| 8 | sudo by root's own program cannot be the session break either: the root half of sudo could call `setaudit_addr`, but does not (row 4) | [S, I] |

**Measured:** `sudo -n -u _yolojail` reported the owner's session id and flags, over SSH and at the
desktop (**[M]** W1). The witnesses in [§2](#2-re-verification-witnesses) re-measure it with the
ASID and AUID read directly, through the real yolo launch too.

### 1.2 What securityd keys by session

| Session-keyed behavior | Source | What it means for an account-mode jail |
| :--- | :--- | :--- |
| `lockAll` locks every keychain unlocked in the caller's session (`ucsp_server_lockAll` → `Session::processLockAll`) | [`transition.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/securityd/src/transition.cpp), [`session.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/securityd/src/session.cpp) [S] | The jail can lock the owner's login keychain. Another UID in the same session locked a keychain (**[M]** W5, disposable accounts); a different session did not (**[M]** W6, same UID only) |
| Keychain dialogs: securityd refuses UI unless the session has `sessionHasGraphicAccess`, and otherwise starts SecurityAgent **as the session's originator UID** (`xpc_connection_set_target_uid(…, originatorUid)`) | [`agentquery.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/securityd/src/agentquery.cpp) [S] | A dialog the jail causes appears on the owner's screen, drawn by the owner's SecurityAgent, when the owner launched from the desktop **[S, I; U: R4]**. From SSH there is no graphic access, so it fails with `errSecInteractionNotAllowed` (**[M]** W4, rc 36) |
| `verifyKeyStorePassphrase`, `changeKeyStorePassphrase`, `resetKeyStorePassphrase` act on the keybag named by `{sessionId, originatorUid, caller audit token}` (`get_current_service_context`) | [`session.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/securityd/src/session.cpp), [`ucsp.defs`](https://github.com/apple-oss-distributions/Security/blob/main/OSX/libsecurityd/mig/ucsp.defs) [S] | The context names the **owner's** UID, because the AUID crossed sudo ([§1.1](#11-the-mechanism) row 3). The keybag service that receives it is not public, so whether it also checks the caller's own UID from the audit token is unknown **[U: R5]**. Reset from a sandbox was refused (**[M]** W5); reset as another UID in the session was never tried |
| Reuse of an unlocked database requires the same session **and** the database blob, which the client reads from the file (`KeychainDbCommon::find`, `decodeDb`) | [`kcdatabase.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/securityd/src/kcdatabase.cpp), [`transition.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/securityd/src/transition.cpp) [S] | Sharing the session gives nothing here: `_yolojail` cannot read the owner's keychain files (**[M]** W3/W4 show path-based reads fail without the file) |

Not securityd, but keyed the same way: Authorization Services (`authd`) dialogs, such as "X wants
to make changes", are per session and drawn in the session's GUI **[I; U: R4b]**. The Mach
bootstrap port is a separate thing from the ASID; sudo leaves it as inherited, so `_yolojail` may
look up per-user GUI services in the owner's domain (the pasteboard among them, which the account
profile does not deny) **[U: R6]**. That is an adjacent finding, not part of the keychain question.

### 1.3 What stays protected

| Asset | What protects it in account mode | Mark |
| :--- | :--- | :--- |
| Secrets in the owner's file-based keychains | The database bytes are read by the client from a file `_yolojail` cannot open (owner's home, mode 0700, file 0600) | [S, M: W3/W4] |
| Secrets in the data-protection keychain | secd is per user and checks the caller's entitled access groups | [A: TN3137, I] |
| The owner's password typed into a real securityd dialog | It goes to securityd, not to the caller; the caller learns at most success or failure | [S, I] |

**The plain answer to the owner:** sudo changes *who* the process is and nothing about *which login
it belongs to*. macOS deliberately keeps the login (the audit session) across user switches, so
that audit records still name the person at the keyboard. securityd uses that same login as the
scope of "lock everything" and of "who sees the dialog". So the agent cannot read the owner's
keychain, but it can lock it and make it ask the owner for a password. Cutting that takes a new
session, which only root or launchd can make.

### 1.4 A correction to the 10-30 report

The [10-30 report §5](../lab-reports/2026-10-30-mac-copilot-keychain-witnesses-and-issue-66.md#5-w5-session-routines)
says "Account mode's own profile still denies SecurityServer." The source says otherwise: the
account profile is `(allow default)` and denies no Mach service, as the
[audit §1](../lab-reports/2026-10-29-mac-securityserver-audit.md#1-the-boundary-as-shipped-and-what-b4b5-established)
table also records. So **the shipped account-mode agent itself, not only an unconfined process of
`_yolojail`, can call lock-all on the owner's session** **[S, I; U: R3b]**. The report's sentence
should be corrected when this lands.

## 2. Re-verification witnesses

**For the owner's Mac agent.** Each witness names the account it runs in, what it may touch, and
its expected result. Unless a witness says otherwise, it touches only disposable accounts and
synthetic keychains. **R3 is the one disruptive witness:** it locks every keychain unlocked in the
owner's session, the real login keychain included, and must be run deliberately.

### 2.0 Setup (owner, once)

1. Recreate `yolo-witness` (A) and `yolo-witness2` (B) with `/Users/Shared/yolo/witness-account.sh`,
   A with the owner's admin authorization so that it can log in at the desktop, as in
   [10-30 §8](../lab-reports/2026-10-30-mac-copilot-keychain-witnesses-and-issue-66.md#8-w6-sessioncreate).
2. Give A a rule **shaped like yolo's**, so the hop matches the crossing (the 10-30 rule named
   `/usr/bin/security` instead):

   ```zsh
   echo 'yolo-witness ALL=(yolo-witness2) NOPASSWD: /usr/bin/env' | sudo tee /etc/sudoers.d/yolowitness >/dev/null
   sudo visudo -cf /etc/sudoers.d/yolowitness
   ```

   The file name has no dot, or sudo ignores it.
3. Build three probes into `/Users/Shared/yolo/sid` (mode 755, readable by every account):

   `asid.c` prints the caller's audit identity, unprivileged (`getaudit_addr` needs no root **[S]**):

   ```c
   #include <bsm/audit.h>
   #include <Security/AuthSession.h>
   #include <stdio.h>
   #include <unistd.h>
   int main(void) {
       auditinfo_addr_t a;
       if (getaudit_addr(&a, sizeof a)) { perror("getaudit_addr"); return 1; }
       SecuritySessionId s = 0; SessionAttributeBits b = 0;
       OSStatus st = SessionGetInfo(callerSecuritySession, &s, &b);
       printf("pid=%d ruid=%d euid=%d auid=%d asid=%d aflags=0x%llx | SessionGetInfo=%d sid=%u attrs=0x%x\n",
              getpid(), getuid(), geteuid(), a.ai_auid, a.ai_asid,
              (unsigned long long)a.ai_flags, (int)st, s, (unsigned)b);
       return 0;
   }
   ```

   `kcstate.c` reports a keychain's lock state **without ever raising a dialog**, as seen from the
   caller's session:

   ```c
   #include <Security/Security.h>
   #include <stdio.h>
   int main(int argc, char **argv) {
       SecKeychainRef k; SecKeychainStatus st = 0;
       SecKeychainSetUserInteractionAllowed(false);
       OSStatus s = SecKeychainOpen(argv[1], &k);
       if (!s) s = SecKeychainGetStatus(k, &st);
       printf("%s status=%d unlocked=%d\n", argv[1], (int)s, (st & kSecUnlockStateStatus) ? 1 : 0);
       return s ? 2 : ((st & kSecUnlockStateStatus) ? 0 : 36);
   }
   ```

   `newsess.c` asks the kernel for a fresh session, then execs its arguments (for B1):

   ```c
   #include <bsm/audit.h>
   #include <errno.h>
   #include <stdio.h>
   #include <string.h>
   #include <unistd.h>
   int main(int argc, char **argv) {
       auditinfo_addr_t a;
       if (getaudit_addr(&a, sizeof a)) { perror("getaudit_addr"); return 1; }
       if (a.ai_termid.at_type != AU_IPv6) a.ai_termid.at_type = AU_IPv4;
       a.ai_asid = AU_ASSIGN_ASID;
       if (setaudit_addr(&a, sizeof a)) { fprintf(stderr, "setaudit_addr: %s\n", strerror(errno)); return 2; }
       fprintf(stderr, "new asid=%d auid=%d\n", a.ai_asid, a.ai_auid);
       if (argc > 1) { execv(argv[1], argv + 1); perror("execv"); return 3; }
       return 0;
   }
   ```

   ```zsh
   cd /Users/Shared/yolo/sid
   cc -o asid asid.c -framework Security
   cc -o kcstate kcstate.c -framework Security   # deprecation warnings are expected
   cc -o newsess newsess.c
   chmod 755 asid kcstate newsess
   ```

**Other ways to read an ASID**, for cross-checking the probe:

- `sudo launchctl procinfo <pid>` prints a process's launchd context, and is expected to include
  its audit session **[U: confirm the field name on 26.5]**. It reads any process, so it can show the
  real agent's ASID while it runs.
- `ps` has no ASID or AUID column **[I; confirm with `ps -L`]**.
- `praudit` on `/var/audit` shows a subject token with `auid` and `sid`, only where BSM auditing is
  still enabled; it is deprecated on current macOS **[A, I]**. Not needed.

### 2.1 R1: the chain, measured on the real crossing (owner account, read-only)

Run once in a desktop terminal and once over SSH into the owner's account:

```zsh
P=/Users/Shared/yolo/sid/asid
$P
sudo -n -u _yolojail /usr/bin/env $P                                   # the rule's exact shape
sudo -n --set-home -u _yolojail /usr/bin/env -i HOME=/Users/_yolojail $P   # the launch's shape
```

Then, in a throwaway account-mode workspace under `/Users/Shared/yolo`:

```zsh
yolo -- /Users/Shared/yolo/sid/asid
```

**Expected:** every line shows the same `asid` and `auid` (the owner's UID), while `ruid`/`euid` are
`_yolojail`'s on the last three. The desktop and SSH runs differ from each other. A different ASID
on any `_yolojail` line falsifies [§1.1](#11-the-mechanism). The `yolo --` line is new: it measures the shipped launch,
not a hand-made hop.

### 2.2 R2: the session, not the UID, decides lock-all (A and B only)

This adds the control the 10-30 run lacked: **a different UID in a different session**. Open two
SSH logins as A (*S1*, *S2*) and one as B (*S3*).

In S1 make the keychain:

```zsh
K=$HOME/Library/Keychains/r2.keychain-db
security create-keychain -p r2 "$K" && security set-keychain-settings "$K" && chmod 600 "$K"
KC=/Users/Shared/yolo/sid/kcstate
```

Then, re-unlocking with `security unlock-keychain -p r2 "$K"` in S1 before each row:

| Row | Where | Command | Expected `kcstate "$K"` in S1 |
| :--- | :--- | :--- | :--- |
| R2a | S1 | `sudo -n -u yolo-witness2 /usr/bin/env /usr/bin/security lock-keychain -a` | locked (36) |
| R2b | S2, same UID, other session | `security lock-keychain -a` | **unlocked (0)** |
| R2c | S3, other UID, other session | `security lock-keychain -a` | **unlocked (0)** |
| R2d | S1 | `sudo -n -u yolo-witness2 /usr/bin/env /usr/bin/security find-generic-password -s r2 "$K"` | still unlocked; the command fails reading the file (POSIX) |

Also run `$KC "$K"` in S2 right after unlocking in S1: **expected locked**, because an unlock is
per session too. R2a with R2b and R2c together show the key is the session. R2d shows that UID and
file permissions still guard the bytes.

### 2.3 R3: lock-all in the owner's real session (owner account, DISRUPTIVE, deliberate)

**What it does:** locks every keychain unlocked in the session it runs in, **including the owner's
real login keychain**. Apps that need it will then ask for the login password until it is unlocked
again. It reads and writes no item in any real keychain. Run it only when the owner says so, at a
moment when a re-unlock prompt is acceptable.

```zsh
mkdir -m 700 -p ~/yolo-r3 && S=~/yolo-r3/syn.keychain-db KC=/Users/Shared/yolo/sid/kcstate
saved=("${(@f)$(security list-keychains -d user | sed -e 's/^ *"//' -e 's/"$//')}")
security create-keychain -p syn "$S"
security list-keychains -d user -s "${saved[@]}"        # take the synthetic one back off the search list
security unlock-keychain -p syn "$S"
$KC "$S"; $KC ~/Library/Keychains/login.keychain-db    # both expected unlocked
```

| Row | Command | Expected |
| :--- | :--- | :--- |
| R3a | `sudo -n -u _yolojail /usr/bin/env /usr/bin/security lock-keychain -a` | `$KC` shows `$S` **and** the login keychain locked |
| R3b | (re-unlock `$S` and the login keychain first) in an account-mode workspace: `yolo -- /usr/bin/security lock-keychain -a` | the same: the shipped agent can do it ([§1.4](#14-a-correction-to-the-10-30-report)) |
| R3c | (re-unlock) the same command over an SSH login of the owner | the desktop's `$S` stays **unlocked** |

Re-unlock the login keychain with `security unlock-keychain ~/Library/Keychains/login.keychain-db`
(it asks for the password on the terminal). Clean up with `rm -rf ~/yolo-r3`.

### 2.4 R4: dialogs reach the owner's screen (A at the desktop, B through sudo)

Log in to A at the desktop with fast user switching, as for W6, and use a Terminal window there.

```zsh
D=/Users/Shared/yolo/r4b && sudo -n --set-home -u yolo-witness2 /usr/bin/env /bin/mkdir -m 700 -p $D
sudo -n --set-home -u yolo-witness2 /usr/bin/env /usr/bin/security create-keychain -p b $D/b.keychain-db
sudo -n --set-home -u yolo-witness2 /usr/bin/env /usr/bin/security lock-keychain $D/b.keychain-db
sudo -n --set-home -u yolo-witness2 /usr/bin/env /usr/bin/security find-generic-password -s none $D/b.keychain-db
```

**Expected:** a keychain-unlock dialog appears on A's screen, drawn by A's SecurityAgent, though the
requester is B. Cancel it. Run the last line again from an SSH login of A: expected rc 36 and no
dialog. **R4b:** `sudo -n -u yolo-witness2 /usr/bin/env /usr/bin/security authorize -u system.preferences`
at A's desktop; expected: an authorization dialog on A's screen **[U]**.

### 2.5 R5: the keybag routines across UIDs (A and B only, OPTIONAL, destructive to A)

This settles the one unknown in [§1.2](#12-what-securityd-keys-by-session) that can do lasting harm: whether `resetKeyStorePassphrase`,
called by B in A's session, touches **A's** keybag. It may destroy A's keybag and login keychain.
Run it only if the owner accepts losing A's keychain state; otherwise mark it untested. Use the
10-30 `SecKeychainResetLogin` probe, run as B through the env-shaped sudo in an SSH login of A
(which has a login keychain after W6), then check whether A can still unlock
`login.keychain-db` with its old password, and record its inode, mtime and size. **Expected:**
unknown; refused would mean the keybag service checks the caller's UID.

### 2.6 R6: the bootstrap side (adjacent, optional; A at the desktop, B through sudo)

```zsh
echo r6-from-A | pbcopy
sudo -n -u yolo-witness2 /usr/bin/env /usr/bin/pbpaste
```

**Expected unknown.** Printing `r6-from-A` would mean `_yolojail` can read the owner's clipboard
in account mode today, a separate leak that a session host would also cut **[U]**.

## 3. Every way to break the chain

Every route below has to change the session **before** the first `_yolojail` process starts,
because nothing after the crossing is root ([§1.1](#11-the-mechanism) row 5). The ranks are this
draft's first ones; the owner's offer of a setup step moves route 4 to the leaning
([§3.2.6](#326-route-4-against-route-1)).

| Rank | Route | Root needed? | Cuts lock-all and dialogs? | What it costs or breaks | Evidence | Witness |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| 1 | **Session host via launchd `SessionCreate`**: a per-user LaunchAgent with `SessionCreate` true runs a yolo session host; the launcher hands it a pty slave over a unix socket (`SCM_RIGHTS`), and the host runs the unchanged sudo crossing on that pty, so the new session carries through sudo ([§1.1](#11-the-mechanism) rows 2–4) | **No**: launchd makes the session (W6) | Yes: lock-all hits a session with nothing unlocked, and the session has no graphic access, so dialogs fail ([§1.2](#12-what-securityd-keys-by-session)) | A pty bridge (yolo already has [`internal/ttyproxy`](../../internal/ttyproxy)); exit status and signals relayed over the socket; a plist written and booted out per launch (or one installed). Possible macOS "background item added" notice **[U]**. The AUID is likely still the owner's **[U]**, so the keybag context may not change | `SessionCreate` gives a new session without root, and lock-all there spared the desktop **[M]** W6; **[A]** `man launchd.plist` | B2, B3 |
| 2 | **Join variant of 1**: the session-created agent only vends its session port (`audit_session_self`) over a Mach service; a short-lived child of the launcher calls `audit_session_join`, then execs the sudo crossing on the real terminal | No | Yes, as 1 | No pty bridge, the terminal stays native. But it needs Mach IPC from Go without cgo (yolo has none), and a bootstrap lookup that works from an SSH launch **[U]** | **[S]** `audit_session_join` needs only the port | B2j |
| 3 | **A LaunchDaemon installed at setup**, `UserName` `_yolojail`, `SessionCreate` true, socket-activated; it checks the peer is the owner (`LOCAL_PEERCRED`) and starts the jail as `_yolojail` itself | Once, at setup (setup already uses sudo for the account and the rule) | Yes, and the AUID becomes `_yolojail`'s or none **[U]**, which also moves the keybag context off the owner | A resident root-installed service; a second crossing beside the sudo rule, against the one-rule contract ([OQ-MP10](../design/macos-user-privilege-lifecycle.md#decision-ledger)); the same pty bridge | **[A]** launchd keys; daemon behavior **[U]** | B4 |
| 4 | **A root session helper behind a passwordless sudo rule**: a tiny root-owned program calls `setaudit_addr` with a new ASID, no session flags and the target's AUID, drops to the target UID (the owner in run-as-you, `_yolojail` in account mode), and execs the unchanged launch | Every launch, through one `NOPASSWD` rule set up once | **Yes, in both modes**, with the terminal kept ([§3.2](#32-route-4-in-depth-one-passwordless-rule-to-a-root-session-helper)) | Root code at every launch; run-as-you needs setup for the first time; overturns "no root helper" in [OQ-MP10](../design/macos-user-privilege-lifecycle.md#decision-ledger). Details in [§3.2](#32-route-4-in-depth-one-passwordless-rule-to-a-root-session-helper) | **[S]** | B1, H1 |
| 5 | **`ssh _yolojail@localhost`** | Setup writes a key; the owner must turn on Remote Login | Yes: SSH logins get their own session **[M]** W1, runbook [§2](#2-re-verification-witnesses) | Opens sshd to the network for the whole Mac; often blocked by device management; a second login path into the account | **[M]** | B5 |
| 6 | **Deny `com.apple.SecurityServer` (and `com.apple.authd`) in the account profile.** Not a break of the chain: it stops the jail from *using* it | No | Lock-all and keychain dialogs, yes (p0 could not lock, **[M]** W5); authd dialogs only if authd is also denied **[U]** | Breaks SecureTransport TLS in account mode (Copilot ≥1.0.93), and any keychain of the agent's own (it has none). Leaves the bootstrap side (R6) open. One profile line | **[M]** W5 for p0 | R3b repeated with the deny |
| — | `sudo -i`, `sudo -s`, `su -l` | — | **No**: none of them sets the audit session ([§1.1](#11-the-mechanism) row 4; `su` also needs `_yolojail`'s password or root) | — | [S, I] | B6 |
| — | `launchctl asuser <uid>` | Root | Joins the target user's *existing* context; `_yolojail` has no login session to join | — | [A] `man launchctl` | — |
| — | `login -f`, `sandbox-exec` | `login -f` needs root; Seatbelt never touches the credential | No | — | [I] | — |

**Run-as-you gets the same benefit from routes 1–3** (without the sudo hop): Copilot under vd in a
fresh session would leave lock-all with nothing to lock and dialogs with no screen. W2 already ran
Copilot under vd in an SSH session, which is not the desktop session, and it worked **[M]**, so TLS
does not need the GUI session.

**What a session break may also take away, all [U]** until B2/B3 run: the agent's own keychain
prompts (none expected: `_yolojail` has no keychain, and a refused prompt is safer than a dialog on
the owner's screen); user notifications and `open`, which go through the bootstrap rather than the
ASID; and whatever the owner's GUI session vends that the jail should not reach anyway (R6).

### 3.1 Session-break witnesses

All in A and B, except B6.

- **B1, kernel rule.** As A: `/Users/Shared/yolo/sid/newsess /usr/bin/true`. Expected:
  `setaudit_addr: Operation not permitted`. As root:
  `sudo /Users/Shared/yolo/sid/newsess /usr/bin/sudo -n -u yolo-witness2 /Users/Shared/yolo/sid/asid`.
  Expected: a new ASID, carried to B. Proves [§1.1](#11-the-mechanism) rows 3–5 and route 4.
- **B2, the session host shape (A at the desktop).** Bootstrap into `gui/$(id -u)` a LaunchAgent
  with `SessionCreate` true whose `ProgramArguments` are
  `/usr/bin/sudo -n -u yolo-witness2 /usr/bin/env /Users/Shared/yolo/sid/asid`, with
  `StandardOutPath` in `/Users/Shared/yolo/b2`. Expected: B's line shows an ASID different from A's
  desktop and `attrs=0`; record the AUID. Then the same agent with
  `… /usr/bin/security lock-keychain -a`: A's desktop keychain stays unlocked (`kcstate`). Then with
  the R4 dialog command: no dialog on A's screen, rc 36.
- **B2j, join.** Only if route 2 is chosen: a prototype must show `asid` after
  `audit_session_join` equals the agent's, and that the following `sudo -u` keeps it.
- **B3, headless.** B2 from an SSH login of A with **no** desktop login, into `user/$(id -u)`.
  Expected unknown: whether launchd honors `SessionCreate` there decides whether route 1 works for
  an owner who launches only over SSH.
- **B4, daemon.** A root-installed LaunchDaemon with `UserName` `yolo-witness2` and `SessionCreate`
  running `asid`. Record the ASID and AUID.
- **B5, SSH.** Only if Remote Login is already on: `ssh -t yolo-witness2@localhost /Users/Shared/yolo/sid/asid`.
- **B6, non-routes (owner account).** `sudo -n -i -u yolo-witness2` is outside the env-shaped rule,
  so run it from A with a temporary `NOPASSWD: ALL` rule for B: `sudo -i`, `sudo -s` and `su -l`
  (with a known password set on B) each print the same ASID as A. Remove the temporary rule after.

**Cleanup:** boot out and delete every witness agent and daemon, remove
`/etc/sudoers.d/yolowitness`, `/Users/Shared/yolo/r4b`, `/Users/Shared/yolo/b2`, and delete A and B
with the witness script.

### 3.2 Route 4 in depth: one passwordless rule to a root session helper

**The owner's question:** setup already installs a passwordless sudo rule, so require one more for
both modes and let root swap the session. **Short answer: it solves the session problem in both
modes, and with it the Copilot problem ([#65](https://github.com/mschulkind-oss/yolo-jail/issues/65)),
but not everything**; the residue is in [§3.2.5](#325-what-it-does-not-solve).

#### 3.2.1 What setup allows today

`yolo macos-setup` writes exactly one file, `/etc/sudoers.d/yolo-jail` (`SudoersRule` in
[`privilege.go`](../../internal/macosuser/privilege.go)):

```text
Defaults>_yolojail secure_path="/usr/bin:/bin:/usr/sbin:/sbin"
<owner> ALL=(_yolojail) NOPASSWD: /usr/bin/env
```

- It lets the owner run **any** command as `_yolojail`, without a password, and **nothing as root**.
  `/usr/bin/env` takes any argv, so the rule is "be `_yolojail`", by design
  ([OQ-MP10](../design/macos-user-privilege-lifecycle.md#decision-ledger), walkthrough
  [point 1](../design/macos-user-privilege-lifecycle.md#points-settled-in-the-walkthrough-2026-10-15)).
- yolo only ever spells it `/usr/bin/sudo -n [--set-home] --user=_yolojail /usr/bin/env …`
  (`RunAsSandboxArgv`; `RunAsArgvProblems` refuses any other shape), and every crossing goes through
  `RunAs.Argv` in [`mode.go`](../../internal/macosuser/mode.go).
- **Run-as-you uses no sudo and needs no setup at all**: its crossing is a bare `/usr/bin/env`
  (`RunAs.Argv`, self mode), and its tests pin that it runs no sudo
  ([`capture_selfmode_test.go`](../../internal/macosuser/capture_selfmode_test.go)).
- The ruled privilege design kept a root helper only as a fallback and chose "no root helper"
  ([the lifecycle design's summary](../design/macos-user-privilege-lifecycle.md)).

So the "provision for passwordless commands" exists, but it has never granted root, and
run-as-you has never needed it.

#### 3.2.2 The narrowest rule

The helper is coined here as the **session helper**: a separate, root-owned program whose whole
job is "give me a fresh audit session, then become the target user". The narrowest rule that does
it for both modes:

```text
Cmnd_Alias YOLO_SESSION = sha256:<digest> /Library/PrivilegedHelperTools/dev.yolo-jail.session-exec
Defaults!YOLO_SESSION env_reset
<owner> ALL=(root) NOPASSWD: YOLO_SESSION
```

- **`sha256:` pins the bytes** in sudo itself (sudoers(5) command digests): a replaced binary is
  refused by sudo before it runs **[A, U: H1j]**. Setup writes the digest; an upgrade reruns setup.
- **The path is root-owned all the way up.** `/Library/PrivilegedHelperTools` is root's; never the
  staged yolo, the bundle, `/usr/local` (Homebrew may own it) or anything under a home **[I]**.
- **Not the `yolo` binary.** A full yolo run as root would put its whole start-up path (config
  reading, environment, pack code) in front of root. The helper is a separate program of a few
  dozen lines **[I]**.
- **The rule can replace the `_yolojail` rule** rather than join it, since the helper also performs
  the switch to `_yolojail`: still one rule, but now a root one ([OQ-KS8](#OQ-KS8)).

**The helper's whole root phase**, in order, with nothing read from a caller-writable path:

1. Accept exactly `session-exec self -- <abs path> [args…]` or `session-exec account -- <abs path> [args…]`;
   refuse anything else, a relative path included.
2. Read the target from a **root-owned enrollment record** written at setup (the owner's numeric
   UID, `_yolojail`'s numeric UID), never from argv or the environment. Require sudo's `SUDO_UID`
   to equal the enrolled owner, so that only the enrolled owner's sudo reaches step 3.
3. `setaudit_addr` with `ai_asid = AU_ASSIGN_ASID`, `ai_auid` = the target UID,
   **`ai_flags` = 0**, an empty mask and a zero IPv4 terminal id. Root may set any flags on a new
   session, graphic access included, and the kernel runs no immutability check for a new ASID
   (`audit_session_setaia`, [`audit_session.c`](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/security/audit/audit_session.c)) **[S]**, so the helper
   must clear them itself: **copying the caller's flags would give the new session graphic access,
   and securityd would draw dialogs again.** After that, a non-root member can set no flag
   (`audit_session_member_set_sflags_mask = 0`) **[S]**, so the jail cannot win graphic access back.
4. `initgroups`, `setgid`, `setuid` to the target; then check that `setuid(0)` fails and that the
   real, effective and saved IDs are the target's.
5. `execv` the absolute path. Everything it runs, it runs as a user the owner could already run as.

**What the rule adds to what a compromised owner account could already do:** create an audit
session with no flags and an AUID of its own UID or `_yolojail`'s. No root code execution follows
unless the helper has a bug in steps 1–4, which is why they must stay that small **[I]**.

**Building it.** yolo has no cgo, and `setaudit_addr` has no wrapper in `golang.org/x/sys/unix`
**[I]**. The options are a Go program calling it through `syscall.Syscall` on darwin (deprecated by
Apple but working, **[U: H1]**) or a small C program, which the Linux-hosted nix build cannot
cross-compile easily. A new `cmd/` binary must also join `flake.nix`'s `shippedBinaries` and the
bundle script ([AGENTS.md](../../AGENTS.md#architecture)).

#### 3.2.3 Does it cut lock-all and dialogs in both modes?

| Surface | Account mode | Run-as-you | Mark |
| :--- | :--- | :--- | :--- |
| Lock-all | Hits the new session, where nothing is unlocked | The same | [S, M by analog: W6] |
| securityd dialogs | Refused: the session has no graphic access | The same | [S: `agentquery.cpp`] |
| authd dialogs | Refused for the same reason | The same | [I; U: H1d] |
| Reuse of the owner's **unlocked** login keychain | Not possible (POSIX already) | **Newly closed**: the unlocked state is per session, so even the hard-link escape W4 found would meet a locked database and no way to prompt | [S: `kcdatabase.cpp`; U: H1h] |
| Keybag routines' context | `{new session, _yolojail}` | `{new session, owner}`, unless the AUID is set to none ([OQ-KS9](#OQ-KS9)) | [S; U: R5, H1i] |

So, yes for both modes, on the source; H1 measures it.

#### 3.2.4 What it breaks, and Copilot

- **The terminal: nothing.** The helper execs in place; an audit session is not a POSIX session or
  a controlling terminal, so the fds, the process group and Ctrl-C are untouched. Account mode
  already runs through sudo and its signal relay ([`sessionguard.go`](../../internal/macosuser/sessionguard.go));
  run-as-you would gain that sudo hop **[S, I; U: H1f]**.
- **Notifications, `open`, the clipboard: unchanged.** They go through the Mach bootstrap port the
  process inherited, which the helper does not touch; run-as-you's profile denies the pasteboard,
  LaunchServices and Apple Events anyway, and account mode's does not ([R6](#26-r6-the-bootstrap-side-adjacent-optional-a-at-the-desktop-b-through-sudo)) **[I; U: H1k]**.
- **The agent's own keychain use: no dialog, ever.** An item that needs a prompt fails with
  `errSecInteractionNotAllowed`. Copilot's token write, which looks for a default keychain the jail's
  `HOME` does not have, then fails instead of raising a "keychain not found" dialog on the owner's
  screen, and Copilot falls back to its own plain-text consent prompt
  ([token storage](copilot-token-storage.md)) **[I]**. Anything that needs Touch ID or a GUI
  password prompt (1Password's CLI, `ssh-add --apple-use-keychain`) stops working in the jail **[I]**.
- **Copilot's TLS works.** It needs SecurityServer and the two root-certificate files (vd), not a
  graphic session: W2 ran Copilot under vd in an SSH session, which has no graphic access, and it
  worked **[M]**.
- **So #65 closes for practical purposes.** In run-as-you, vd plus the session helper lets Copilot
  reach SecurityServer while lock-all, dialogs and unlocked-keychain reuse all stop at the jail's
  own session. What vd still exposes: keychain files the jail can read (its own), notification
  spoofing (`postNotification`, per session **[U]**), and the keybag routines, which W5 found refused
  under vd anyway (`SecKeychainResetLogin`, **[M]**). The upstream fix
  ([#5085](https://github.com/github/copilot-cli/issues/5085)) is still the clean end.
- **Every crossing must use it**, or a daemon started outside it stays in the owner's session: the
  bootstrap, provisioning, the supervisor and the agent. They already meet in `RunAs.Argv`, so the
  helper goes there once, and a test can pin that no crossing skips it **[S]**.

#### 3.2.5 What it does not solve

1. **Run-as-you stops being setup-free.** Today it needs no account, no rule and no sudo; with the
   helper, every run-as-you launch needs `yolo macos-setup` and an administrator once
   ([OQ-KS7](#OQ-KS7)).
2. **Root code runs at every launch**, the thing the privilege design ruled out. Its surface is
   steps 1–4 and the install path; a bug there is a local root escalation for the owner account.
3. **The Mach bootstrap side** (clipboard, notifications, other per-user GUI services) is not keyed
   by the audit session, so account mode's `(allow default)` still reaches them (R6). The fix is a
   profile deny, not a session.
4. **The keybag context in run-as-you** still names the owner unless the AUID is set to none, and the
   keybag service's own check is unknown (R5).
5. **File-level escapes** stay file-level: a keychain file the jail can read is still the jail's to
   use, with no unlock prompt possible.
6. **A stale helper** after an upgrade must refuse the launch and name `yolo macos-setup`, like the
   existing readiness probe G2 ([`preconditions.go`](../../internal/macosuser/preconditions.go)).

#### 3.2.6 Route 4 against route 1

| | Route 4: root session helper | Route 1: launchd session host |
| :--- | :--- | :--- |
| Root | One `NOPASSWD` root rule; a root program at every launch | None |
| Setup for run-as-you | Required, new | None |
| Terminal | Native: exec in place | A pty bridge, exit status and signals over a socket |
| Session flags and AUID | Chosen explicitly: flags 0, AUID the target's | launchd's: flags 0 measured (W6); AUID unknown |
| SSH-only launches | Work the same way | Unknown whether launchd honors `SessionCreate` there (B3) |
| macOS side effects | None expected | A plist bootstrapped per launch; a "background item" notice possible |
| Rulings it overturns | "No root helper" in [OQ-MP10](../design/macos-user-privilege-lifecycle.md#decision-ledger) | None |
| Evidence | Kernel source; B1 and H1 to run | W6 measured the session; B2/B3 to run |

**My leaning moves to route 4**, since the owner offers the setup step: it is simpler, keeps the
terminal native, works over SSH, and sets the flags and AUID itself rather than trusting launchd's
defaults. Route 1 stays the fallback for a run-as-you user who declines setup; such a launch keeps
p0 and gets no vd.

### 3.3 H1: the session helper, on the Mac (A and B, with the owner's admin password once)

A stands for the owner and B for `_yolojail`, so the owner's real session is never the target.
Install a **witness** helper, never yolo's:

```c
/* sessexec.c: build with -DOWNER_UID=<A's uid> -DACCOUNT_UID=<B's uid> */
#include <bsm/audit.h>
#include <grp.h>
#include <pwd.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
int main(int argc, char **argv) {
    if (argc < 4 || strcmp(argv[2], "--") || argv[3][0] != '/') { fputs("usage\n", stderr); return 64; }
    uid_t t;
    if (!strcmp(argv[1], "self")) t = OWNER_UID; else if (!strcmp(argv[1], "account")) t = ACCOUNT_UID;
    else { fputs("bad mode\n", stderr); return 64; }
    const char *su = getenv("SUDO_UID");
    if (!su || (uid_t)strtoul(su, NULL, 10) != OWNER_UID) { fputs("not the enrolled owner\n", stderr); return 77; }
    auditinfo_addr_t a; memset(&a, 0, sizeof a);
    a.ai_auid = t; a.ai_asid = AU_ASSIGN_ASID; a.ai_flags = 0; a.ai_termid.at_type = AU_IPv4;
    if (setaudit_addr(&a, sizeof a)) { perror("setaudit_addr"); return 71; }
    struct passwd *pw = getpwuid(t);
    if (!pw || initgroups(pw->pw_name, pw->pw_gid) || setgid(pw->pw_gid) || setuid(t)) { perror("drop"); return 71; }
    if (setuid(0) == 0 || getuid() != t || geteuid() != t) { fputs("drop failed\n", stderr); return 71; }
    execv(argv[3], argv + 3); perror("execv"); return 71;
}
```

```zsh
cc -DOWNER_UID=$(id -u yolo-witness) -DACCOUNT_UID=$(id -u yolo-witness2) -o /tmp/sessexec sessexec.c
sudo install -o root -g wheel -m 755 /tmp/sessexec /Library/PrivilegedHelperTools/dev.yolo-witness.sessexec
D=$(shasum -a 256 /Library/PrivilegedHelperTools/dev.yolo-witness.sessexec | cut -d' ' -f1)
echo "yolo-witness ALL=(root) NOPASSWD: sha256:$D /Library/PrivilegedHelperTools/dev.yolo-witness.sessexec" |
  sudo tee /etc/sudoers.d/yolowitnessroot >/dev/null && sudo visudo -cf /etc/sudoers.d/yolowitnessroot
```

Then, as A, at A's desktop unless a row says SSH, with `H=/Library/PrivilegedHelperTools/dev.yolo-witness.sessexec`
and the R2 keychain `$K` unlocked in A's desktop session before each lock row:

| Row | Command | Expected |
| :--- | :--- | :--- |
| H1a | `sudo -n $H account -- /Users/Shared/yolo/sid/asid` | euid B, a new ASID, AUID B, `attrs=0` |
| H1b | `sudo -n $H self -- /Users/Shared/yolo/sid/asid` | euid A, a new ASID, AUID A, `attrs=0` |
| H1c | `sudo -n $H account -- /usr/bin/security lock-keychain -a`, then the same with `self` | `kcstate "$K"` in A's desktop: **unlocked** both times |
| H1d | the R4 dialog command through `$H account --`, and `… /usr/bin/security authorize -u system.preferences` | no dialog on A's screen; an interaction-not-allowed error |
| H1e | `sudo -n $H self -- /Users/Shared/yolo/sid/newsess /usr/bin/true` | `setaudit_addr: Operation not permitted`: the jail cannot make or reflag a session |
| H1f | `sudo -n $H self -- /bin/zsh -i`; inside: `tty`, `stty size`, `less /etc/hosts`, Ctrl-C a `sleep 100`, resize the window | all behave as in a plain shell |
| H1g | in A's workspace, `sudo -n $H self -- /usr/bin/sandbox-exec -f "$W/vd.sb" "$W/copilot" update`, as in [W2](../lab-reports/2026-10-30-mac-copilot-keychain-witnesses-and-issue-66.md#2-w2-copilots-own-tls) | a version line; no dialog |
| H1h | the W4 outside hard link of A's unlocked `$K`, read under vd through `$H self --` | **rc 36 or 152, never the secret, no dialog** |
| H1i | the W5 `SecKeychainResetLogin` probe under vd through `$H self --` (optional, as R5) | refused, as in W5 |
| H1j | negative: a relative path; mode `root`; from B's SSH login (no rule); from a root shell (`SUDO_UID` unset); `DYLD_INSERT_LIBRARIES=/x sudo -n $H …`; after `sudo cp /usr/bin/true $H` | each refused; the last one by sudo, on the digest |
| H1k | `sudo -n $H self -- /usr/bin/osascript -e 'display notification "h1k"'`; `sudo -n $H account -- /usr/bin/pbpaste` after `echo h1k \| pbcopy` | record: these show the bootstrap side, which the helper is not expected to change |
| H1l | over an SSH login of A, H1a–H1c again | the same results |

**Cleanup:** remove `/etc/sudoers.d/yolowitnessroot` and the helper, before deleting A and B.

## 4. Recommendation

1. **Run R1–R4 first** (R3 at a moment the owner picks). They turn the two inherited claims, W1 and
   W5, into measurements of the shipped launch path, with the cross-session control W5 lacked.
2. **Disclose now**, whatever else is chosen. Account mode as shipped already exposes lock-all and
   dialogs ([§1.4](#14-a-correction-to-the-10-30-report)), so the launch should say so. A draft line:

   > macos-user (account mode): this jail shares your login's security session. It can lock your
   > keychains and make macOS ask you for your password; it cannot read your keychains.

3. **Build route 4, the session helper, for both modes**, once H1 passes
   ([§3.2](#32-route-4-in-depth-one-passwordless-rule-to-a-root-session-helper)). Setup installs
   the digest-pinned root rule; every crossing goes through it. It also removes the dialog and
   authd surfaces that a profile deny cannot reach. The disclosure then becomes "this jail runs in
   its own security session: it cannot lock your keychains or show you keychain dialogs."
   **Route 1** (the launchd session host, no root) is the fallback for a run-as-you user who
   declines setup; if B2/B3 also pass, it can replace route 4 there.
4. **Allow vd for launches that select Copilot in run-as-you, behind the session helper.** With
   it, Copilot reaches SecurityServer without the owner's session exposed, which closes #65 for
   practical purposes until [github/copilot-cli#5085](https://github.com/github/copilot-cli/issues/5085)
   ships. A run-as-you launch without the helper keeps p0 and the existing Copilot refusal, which
   then names `yolo macos-setup`.
5. **Until the helper ships, deny `com.apple.SecurityServer` in the account profile** for launches
   that do not select Copilot: one line, measured to stop lock-all.
6. **Deny the pasteboard and other GUI services in the account profile too** (R6), separately: a
   session does not cover them.

## 5. Open questions for the owner

| OQ | Question | Options | My leaning |
| :--- | :--- | :--- | :--- |
| <a id="OQ-KS1"></a>[OQ-KS1](#OQ-KS1) | Adopt a session break for macos-user? | (a) no, disclose only; (b) route 1, session host via `SessionCreate` LaunchAgent; (c) route 3, setup-installed daemon; (d) route 4, the root session helper ([§3.2](#32-route-4-in-depth-one-passwordless-rule-to-a-root-session-helper)) | **(d)**, gated on H1, with (b) as the no-setup fallback for run-as-you. This overturns "no root helper" in [OQ-MP10](../design/macos-user-privilege-lifecycle.md#decision-ledger), so it is the owner's call |
| <a id="OQ-KS2"></a>[OQ-KS2](#OQ-KS2) | One fresh session per launch, or one shared by every jail? | (a) per launch; (b) shared | **(a)**: jails cannot lock each other's state, and the session dies with the launch |
| <a id="OQ-KS3"></a>[OQ-KS3](#OQ-KS3) | Deny SecurityServer in the account profile until KS1 ships? | (a) yes, except launches that select Copilot; (b) yes, always (Copilot then needs run-as-you vd); (c) no | **(a)** |
| <a id="OQ-KS4"></a>[OQ-KS4](#OQ-KS4) | Allow vd (SecurityServer plus the two root-certificate files) for Copilot in run-as-you? | (a) yes, gated to Copilot launches, disclosed, removed when #5085 ships; (b) only behind the session helper ([KS1](#OQ-KS1) (d)); (c) no | **(b)**: with the helper, lock and prompt no longer reach the owner, so nothing is left to accept; (a) only if the owner wants Copilot before the helper ships |
| <a id="OQ-KS5"></a>[OQ-KS5](#OQ-KS5) | The launch disclosure line | the drafts in [§4](#4-recommendation), shown on every macos-user launch that shares the session or allows vd | as drafted; the lock and prompt clauses are the ones that matter |
| <a id="OQ-KS7"></a>[OQ-KS7](#OQ-KS7) | Run-as-you has needed no setup so far. With the helper, does it require `yolo macos-setup`? | (a) required for every run-as-you launch; (b) optional: without it, p0 and no Copilot; (c) never, use route 1 there | **(b)**: the setup-free mode keeps working, and the setup step buys Copilot and the session break |
| <a id="OQ-KS8"></a>[OQ-KS8](#OQ-KS8) | Does the root rule replace the `(_yolojail) NOPASSWD: /usr/bin/env` rule or join it? | (a) replace: the helper also does the switch, still one rule; (b) both | **(a)**: one rule to audit, and no crossing can skip the session |
| <a id="OQ-KS9"></a>[OQ-KS9](#OQ-KS9) | The new session's AUID | (a) the target UID; (b) none (`AU_DEFAUDITID`), so the keybag context never names the owner; (c) the owner's, as today | **(a)** for account mode; **(b)** for run-as-you if H1 shows it works, since it takes the owner off the keybag context |
| <a id="OQ-KS6"></a>[OQ-KS6](#OQ-KS6) | Authorize R3 (locks the owner's real keychains once), R5 (may destroy A's keybag) and H1 (installs a witness root helper and rule for A, removed after)? | each yes or no | R3 **yes**, it is the test the owner asked for; R5 **yes** on A only, since it is the one unknown that could do lasting harm; H1 **yes**, it decides KS1 |

## Sources

- XNU, `main` on 2026-11-03: [`audit_syscalls.c`](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/security/audit/audit_syscalls.c)
  (`setaudit_addr`, `getaudit_addr`), [`audit_session.c`](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/security/audit/audit_session.c)
  (`audit_session_self`, `audit_session_join`), [`kern_credential.c`](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_credential.c)
  (`kauth_cred_model_setresuid`), [`kern_fork.c`](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_fork.c),
  [`kern_prot.c`](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_prot.c) (`proc_calc_audit_token`).
- Security, `main` on 2026-11-03: [`session.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/securityd/src/session.cpp),
  [`server.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/securityd/src/server.cpp),
  [`transition.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/securityd/src/transition.cpp),
  [`agentquery.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/securityd/src/agentquery.cpp),
  [`kcdatabase.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/securityd/src/kcdatabase.cpp),
  [`ucsp.defs`](https://github.com/apple-oss-distributions/Security/blob/main/OSX/libsecurityd/mig/ucsp.defs),
  [`Authorization.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/OSX/libsecurity_authorization/lib/Authorization.cpp),
  [`ccaudit.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/OSX/libsecurity_utilities/lib/ccaudit.cpp).
- sudo, `main` on 2026-11-03: [`plugins/sudoers/bsm_audit.c`](https://github.com/sudo-project/sudo/blob/main/plugins/sudoers/bsm_audit.c).
  Apple's shipped sudo build was not read.
- Apple: `man launchd.plist` (`SessionCreate`), `man launchctl` (`asuser`, `procinfo`),
  [TN3137](https://developer.apple.com/documentation/technotes/tn3137-on-mac-keychains).
- yolo: [`privilege.go`](../../internal/macosuser/privilege.go), [`mode.go`](../../internal/macosuser/mode.go),
  [`macosuser.go`](../../internal/macosuser/macosuser.go), [`seatbelt.go`](../../internal/macosuser/seatbelt.go),
  [`seatbeltdeny.go`](../../internal/macosuser/seatbeltdeny.go); the
  [10-30 witness run](../lab-reports/2026-10-30-mac-copilot-keychain-witnesses-and-issue-66.md), the
  [10-29 runbook run](../lab-reports/2026-10-29-mac-native-witnesses-runbook.md), the
  [SecurityServer audit](../lab-reports/2026-10-29-mac-securityserver-audit.md) and the
  [boundary research](../research/copilot-keychain-boundary.md).

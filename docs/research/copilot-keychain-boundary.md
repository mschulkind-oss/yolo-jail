---
title: "Copilot in a run-as-you jail: what allowing securityd would really expose, and every way around it"
date: 2026-10-30
status: draft
tags: [research, copilot, macos, macos-user, seatbelt, keychain, tls, securityd]
summary: "The owner's understanding is close but too broad. Copilot 1.0.93 and later needs exactly one Mach service, com.apple.SecurityServer (securityd), and none of the data-protection keychain services. Allowing that one name does not by itself hand over the login keychain's secrets: a file-based keychain is a database the calling process reads itself, so yolo's file deny on ~/Library/Keychains stays a second, independent wall, and the search list follows the jail's HOME. It does expose session-wide securityd operations that need no file: lock-all, keychain-password prompts, and a keybag reset whose gate is not in public source. 'Any binary, signed or not' is true only when the jail can reach the service and also read the keychain file, which is what Safehouse grants and yolo does not. Account mode already leaves securityd reachable, in the owner's own security session. The best paths are the upstream fix, Copilot in account mode, and a narrowly measured service-only allow; the owner rules on the last."
---

# Copilot in a run-as-you jail: what allowing securityd would really expose

**Status:** draft, 2026-10-30. Research only, done from a Linux jail: no Mac command ran, no
keychain was touched, Copilot was not executed. It answers the owner's question on
[yolo-jail #65](https://github.com/mschulkind-oss/yolo-jail/issues/65) before the ruling
[MT-D7](../design/macos-native-tls.md#decision-ledger) is made final. It builds on, and does not
repeat, the [SecurityServer audit](../lab-reports/2026-10-29-mac-securityserver-audit.md), the
[Copilot TLS research](copilot-native-networking.md), the
[exposure-by-program table](macos-keychain-exposure-by-program.md) and the
[token-storage measurement](copilot-token-storage.md).

**Evidence classes**, marked on every claim:

- **[M]** measured on the maintainer's Mac (cited lab report);
- **[A]** documented by Apple;
- **[S]** read from Apple's open source (`apple-oss-distributions`, `main` on 2026-10-30), XNU or
  WebKit;
- **[B]** read from the shipped Copilot 1.0.105 bytes in this research (static only, hashes in
  [Sources](#sources));
- **[R]** reported by a third party, not reproduced;
- **[I]** inferred from the above;
- **[U]** unverified: needs the [Mac witness](#3-mac-witnesses) named beside it.

**Terms.**

- **securityd** is the daemon behind `com.apple.SecurityServer`. It holds the unlocked keys of
  *file-based* keychains (the older kind, such as `~/Library/Keychains/login.keychain-db`) and does
  their cryptography.
- **secd** is the per-user agent for the *data-protection* keychain (the newer kind, the iOS one).
- **trustd** evaluates certificate trust; yolo already allows `com.apple.trustd.agent`.
- **Security session** means the macOS audit session: securityd keeps one `Session` object per
  audit session id ([`session.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/securityd/src/session.cpp)) **[S]**.
- **p0** is yolo's unchanged run-as-you profile; **p2** is p0 plus one allow of
  `com.apple.SecurityServer`. Both terms come from the
  [audit](../lab-reports/2026-10-29-mac-securityserver-audit.md#2-disposable-keychain-measurements).
- **Option (a)** is coined here: p2 used as a real launch profile, with every other keychain deny
  kept.

## 1. The precise statement

The owner's understanding, in their words: in run-as-you mode, Copilot works only if the jail may
"access keychain secrets from that user inside the jail, even if they were set from outside the
jail. And that goes for literally any binary, signed or not, that could exist both outside and
inside the jail and store things in the user's keychain."

### 1.1 Which services are involved, and which one Copilot needs

| Mach service | Vended by | What it fronts | Copilot ≥1.0.93 TLS needs it? |
| :--- | :--- | :--- | :--- |
| `com.apple.SecurityServer` | securityd ([plist](https://github.com/apple-oss-distributions/Security/blob/main/securityd/etc/com.apple.securityd.plist)) **[S]** | File-based keychains, the CDSA crypto service, session-wide keychain operations | **Yes, and only this one** |
| `com.apple.securityd.xpc` (+ `.general`, `.ckks`, `.sos`, `com.apple.security.octagon`, …) | **secd**, not securityd ([plist](https://github.com/apple-oss-distributions/Security/blob/main/OSX/sec/ipc/com.apple.secd.plist)) **[S]** | Data-protection keychain, iCloud Keychain | No |
| `com.apple.secd` | not in the current secd plist **[S]**; yolo denies it as defense in depth | — | No |
| `com.apple.trustd.agent` | trustd ([plist](https://github.com/apple-oss-distributions/Security/blob/main/trust/trustd/macOS/com.apple.trustd.agent.plist)) **[S]** | Certificate evaluation | Already allowed ([`seatbeltdeny.go`](../../internal/macosuser/seatbeltdeny.go#L685)) |

Evidence: a 40-line SecureTransport client fails with `-9808` under p0 and succeeds under p2
**[M]** ([audit §3](../lab-reports/2026-10-29-mac-securityserver-audit.md#3-certificate-only-mechanisms-compared-in-the-harness)).
Copilot 1.0.103 and 1.0.105 fail under p0 and work outside Seatbelt
**[R]** ([#65](https://github.com/mschulkind-oss/yolo-jail/issues/65)). The upstream reporter denied
one service at a time: only `com.apple.SecurityServer` broke `native-tls`, and denying `securityd`,
`security.keychaind`, `secd` or `security.agent` changed nothing
**[R]** ([github/copilot-cli#5085](https://github.com/github/copilot-cli/issues/5085)). Copilot itself
under p2 has **not** been measured **[U, W2]**.

### 1.2 What allowing that one service exposes

**Correction: a service grant is not a secret grant.** Reading a file-based keychain item takes
four things, and the service is only one of them:

1. **The service lookup** (`com.apple.SecurityServer`).
2. **The keychain file's bytes.** The CDSA data library runs *inside the calling process* and opens
   the database itself (`libsecurity_filedb`'s `AtomicFile`) **[S]**. securityd's `decodeDb` takes
   the database blob *from the client*
   ([`transition.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/securityd/src/transition.cpp)) **[S]**.
   With the file denied and SecurityServer allowed, every path-based operation still failed,
   with the client's own `EPERM` **[M]** ([audit, table 2b](../lab-reports/2026-10-29-mac-securityserver-audit.md#2b-a-keychain-whose-file-the-sandbox-cannot-read)).
   yolo denies `~/Library/Keychains`, `/Library/Keychains` and `/System/Library/Keychains`
   ([`seatbeltdeny.go`](../../internal/macosuser/seatbeltdeny.go#L841-L855)).
3. **An unlocked database in the caller's security session.** securityd reuses an unlocked
   database only when the caller's session *and* the database identity match
   (`KeychainDbCommon::find` in [`kcdatabase.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/securityd/src/kcdatabase.cpp)) **[S]**.
   Run-as-you shares the owner's session, so this layer gives nothing there **[I]**.
4. **The item's access list.** The access list and partition list must trust the calling program,
   or the user must answer a dialog. The dialog is raised by securityd, so it appears in the
   owner's desktop session whatever the sandbox denies **[S, I]**.

**The pathless default does not reach the owner's keychain either.** The user search list is read
from `~/Library/Preferences/com.apple.security.plist`, and `~` is taken from `$HOME`
(`getPwInfo` in [`DLDBListCFPref.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/OSX/libsecurity_keychain/lib/DLDBListCFPref.cpp)) **[S]**.
A run-as-you launch sets `HOME` to the jail's home ([`runplan.go`](../../internal/macosuser/runplan.go#L417)),
so `security find-generic-password` with no keychain named searches the jail's own (empty) home
**[I, U: W3]**. An agent that sets `HOME` back to the real home meets the file deny again.

**Session-wide operations that take no file are exposed.** In securityd's MIG interface
([`ucsp.defs`](https://github.com/apple-oss-distributions/Security/blob/main/OSX/libsecurityd/mig/ucsp.defs))
**[S]**, these routines take no database handle:

| Routine | What it does | Gate found in public source |
| :--- | :--- | :--- |
| `lockAll` | Locks every keychain unlocked in the caller's session | none **[S]**. In run-as-you that is the owner's login keychain: denial of service, not disclosure ([audit, not tested](../lab-reports/2026-10-29-mac-securityserver-audit.md#what-this-does-and-does-not-say-about-the-login-keychain)) |
| `verifyKeyStorePassphrase`, `changeKeyStorePassphrase` | Make securityd show a keychain-password dialog to the user | none **[S]**. A prompt, so a phishing surface **[I]** |
| `resetKeyStorePassphrase` | Calls `kb_reset` on the session's keybag with a passphrase the caller supplies | none in securityd **[S]**. The keybag service that receives it is not in the public tree, so its own check is **unknown [U: W5]**. If unchecked, this is destructive |
| `postNotification` | Posts keychain events to other listeners | none **[S]**. Spoofing only |
| `stashDb*`, `generateDerivedEntropy` | Login and stash plumbing | entitlement-checked **[S]** |

### 1.3 File-based versus data-protection keychain

- Copilot stores its own token in the **file-based** keychain
  (`SecKeychainAddGenericPassword`, [token storage §3.1](copilot-token-storage.md#31-keychain-first)) **[B, earlier]**.
- The **data-protection** keychain is reached only through secd's services. Option (a) keeps them
  denied, so its items (`ssh-add --apple-use-keychain` passphrases, iCloud Keychain) stay out of
  reach whatever happens to SecurityServer **[S, I]**.
- secd keeps its database itself and checks the caller's entitled access groups, so a file deny
  would not protect it; only the service deny does
  ([TN3137](https://developer.apple.com/documentation/technotes/tn3137-on-mac-keychains)) **[A]**.

### 1.4 "Any binary, signed or not"

**True only for the Safehouse-style grant**, the service *plus* the keychain files *plus* secd,
which is what MT-D7 names. Under that grant the access list is still enforced, and the exposure
comes from what it already trusts **[M]**
([OQ-MT3/MT4](../design/macos-native-tls.md#decision-ledger)):

- an item that trusts `/usr/bin/security`, such as `gh` or Claude Code, is readable with nothing
  downloaded;
- an item that trusts a program is readable through that program, or through a copy whose
  designated requirement or `teamid:` partition matches (signed);
- an unsigned program matches by its hash, so the identical file works;
- an item allowing "any application" is readable outright
  ([exposure table](macos-keychain-exposure-by-program.md#the-three-answers)) **[A, R]**.

An item with a confirm-dialog access list raises a dialog instead.

**Not true for option (a).** With the files denied, none of those routes produces a secret from
the login keychain. That is measured for path-based operations on a synthetic keychain **[M]** and
derived from source for the default search list **[S, I]**. It stays true for any keychain file
the jail *can* read, such as one inside the workspace. Hard links, snapshots and firmlink paths to
the real file are the open escape class, because the file deny becomes the only wall
**[U: W4]**.

### 1.5 Run-as-you versus the dedicated account

**In account mode the keychain at stake is `_yolojail`'s, and it has none.** Setup provisions no
keychain ([keychain design §4.1](../design/keychain-from-a-jail.md#41-macos-user-no-seam-and-no-keychain-provisioning)).
The owner's keychain files are unreadable to that UID by POSIX permissions. The account profile is
`(allow default)` and does **not** deny securityd
([audit §1](../lab-reports/2026-10-29-mac-securityserver-audit.md#1-the-boundary-as-shipped-and-what-b4b5-established)) **[M, source]**.
So Copilot's TLS should already work there **[I, U: W2]**.

**What account mode does not separate is the session.** `sudo -u` keeps the owner's security
session **[M]** ([witness report §2](../lab-reports/2026-10-29-mac-native-witnesses-runbook.md#2-the-disposable-account)),
and securityd keys `lockAll` and the keybag routines by that session, with the session's
originator UID in the keybag context (`get_current_service_context`, [`session.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/securityd/src/session.cpp)) **[S]**.
Account mode therefore *already* exposes the [§1.2 session table](#12-what-allowing-that-one-service-exposes)
to `_yolojail`, whose target is the owner's session **[I, U: W5]**. This is a finding about the
shipped boundary, independent of Copilot.

### 1.6 The corrected statement

> Copilot 1.0.93+ needs `com.apple.SecurityServer` and nothing else from the keychain stack.
> Allowing it does not by itself expose the owner's keychain secrets: securityd decrypts only what
> the caller hands it from a keychain file, and yolo's file deny, kept, stops that for every
> keychain on the Mac. It does expose session-wide securityd operations: locking the owner's
> keychains, raising password dialogs, and a keybag reset whose safety is unknown. Plus any keychain
> file the jail can read, and any path alias to the real file that the deny misses. "Any binary,
> signed or not" describes Safehouse's grant (service + files + secd), not the service alone.
> Account mode already reaches securityd, in the owner's session, and is protected for secrets by
> UID and file permissions.

## 2. Every workaround, pushed

### (a) Allow SecurityServer only; keep the file denies and every secd deny

- **Mechanism.** One allow line in the Copilot launch's profile. Everything else in p0 is kept.
- **Preserves.** Every secret in every keychain file the jail cannot read; the data-protection
  keychain.
- **Exposes.** The [§1.2 session routines](#12-what-allowing-that-one-service-exposes); keychain
  files inside readable roots; any path alias the deny misses. securityd dialogs may also reach the
  owner's screen, for example when Copilot's token write finds no default keychain
  ([keychain design §4.1](../design/keychain-from-a-jail.md#41-macos-user-no-seam-and-no-keychain-provisioning)).
- **Feasibility.** SSLHandshake works under p2 **[M]**; Copilot under p2 is **[U: W2]**.
- **Falsified by:**
  - any secret or attribute read of a synthetic *default* keychain without its file (W3);
  - a hard-link or snapshot read (W4);
  - `resetKeyStorePassphrase` succeeding from a sandbox (W5).
- **Cost.** One line, gated to launches that select Copilot. It contradicts MT-D1 and MT-D4 as
  written ("the deny stays"), though not MT-D7, whose text names the service *and* the files *and*
  secd.

### (b) A separate security session, search list or default keychain

- **Mechanism.** Run the jail in its own audit session, so `lockAll` and unlocked-database reuse
  stop at the session boundary ([§1.2](#12-what-allowing-that-one-service-exposes), point 3)
  **[S]**. SSH logins already get one **[M]**.
- **The catch.** An unprivileged process cannot make one: `setaudit_addr` calls `suser`
  ([XNU `audit_syscalls.c`](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/security/audit/audit_syscalls.c)) **[S]**,
  and `launchctl asuser` needs root. The routes left are:
  - a transient LaunchAgent with launchd's `SessionCreate` key (`man launchd.plist`) **[A]**,
    whether launchd honors it for a `gui/` agent being **[U: W6]**. It has no terminal, so yolo
    would need a pty bridge;
  - in account mode, a sudo rule that launches through a session-creating wrapper. That is setup
    work and needs root.
- **Search list and default keychain.** These are already the jail's, through `HOME`
  ([§1.2](#12-what-allowing-that-one-service-exposes)). `security list-keychains -d dynamic`
  belongs to smart-card tokens and adds nothing **[I]**. A jail-only default keychain would give
  Copilot a keychain to write to, against [OQ-KC4](../design/keychain-from-a-jail.md#8-decision-ledger)
  ("always the host's keychain").
- **Preserves.** It turns lock-all into a no-op and makes a stolen database blob decode locked.
  It does **not** cover the keybag routines: their context carries the originator UID **[S]**.
- **Cost.** High: a session launcher, a pty bridge, and a launch path that differs from
  `sandbox-exec`. It is worth doing only as a hardening layer under (a).

### (c) Make Copilot speak no SecureTransport

- **No TLS-backend selector exists** in 1.0.105: every `COPILOT_*` name was listed **[B]**,
  confirming the [audit §4](../lab-reports/2026-10-29-mac-securityserver-audit.md#4-the-current-copilot-cli-by-static-inspection).
- **An HTTP proxy does not help.** `HTTPS_PROXY` opens a CONNECT tunnel, so the client still runs
  TLS ([networking research](copilot-native-networking.md#answer-and-boundary)) **[I]**.
- **Plain-HTTP endpoints exist and are documented:**
  - **c1, BYOK.** `COPILOT_PROVIDER_BASE_URL` selects a custom model provider, and "GitHub
    authentication is not required". `COPILOT_OFFLINE=true` then "skips all network access:
    GitHub authentication, telemetry, web tools, GitHub MCP server, and auto-update". Both texts
    are from 1.0.105's own help **[B]**. A loopback `http://` base URL served by a TLS-terminating
    proxy (a Go one works under p0, [`cagodebug.go`](../../internal/macosuser/cagodebug.go)) means
    no native TLS at all **[I, U: W7]**. It gives up GitHub-billed Copilot models: the user brings
    their own provider.
  - **c2, a redirected GitHub API.** `COPILOT_GH_HOST` is documented, and its normalizer
    keeps an `http://` scheme as given (`Rae()` in `cli-main.js`) **[B]**.
    `COPILOT_API_URL` overrides the Copilot API base but is undocumented **[B]**. A loopback
    reverse proxy could front both. Unknowns **[U]**:
    - whether a non-`github.com` host switches Copilot to Enterprise URL shapes;
    - which other native HTTPS calls stay (MCP, web tools, telemetry), each still failing;
    - whether a proxy that holds the user's GitHub token is acceptable.
- **Cost.** c1 is cheap and fully documented but changes the product. c2 is fragile and rests
  on an undocumented variable.

### (d) Inject a rustls shim (`DYLD_INSERT_LIBRARIES`)

- **The shipped binary refuses it.** `@github/copilot-darwin-arm64` 1.0.105 is Developer ID signed
  by GitHub (`VEKTX9H2N7`) with the **hardened runtime** flag set **[B]**. Its entitlements are:
  - `allow-jit`;
  - `allow-unsigned-executable-memory`;
  - `disable-library-validation`;
  - **not** `allow-dyld-environment-variables`.
- So dyld ignores `DYLD_*` for it
  ([Apple](https://developer.apple.com/documentation/bundleresources/entitlements/com.apple.security.cs.allow-dyld-environment-variables)) **[A, B]**.
  `disable-library-validation` lets it load unsigned code once something *inside* it asks, for
  example through `NODE_OPTIONS` in a Node single-executable app **[U]**.
- **The release tarball** carries `cli-main.js` plus `runtime.node`, which yolo's own unhardened
  Node could load, with `DYLD_INSERT_LIBRARIES` honored **[I]**.
- **Either way it is function patching**, ruled out by
  [MT-D3](../design/macos-native-tls.md#decision-ledger). Confirm the signature on the Mac with W0.

### (e) A Seatbelt filter finer than the service name

- **Mechanism.** Seatbelt can filter Mach messages after the lookup: WebKit's open-source profile
  uses `apply-message-filter` with `(message-number …)` lists, behind
  `have_message_filtering?`
  ([`com.apple.WebProcess.sb.in`](https://github.com/WebKit/WebKit/blob/main/Source/WebKit/WebProcess/com.apple.WebProcess.sb.in)) **[S]**.
  securityd's MIG subsystem starts at 1000 (`subsystem ucsp 1000;`) **[S]**, so the
  [§1.2 routines](#12-what-allowing-that-one-service-exposes) have fixed numbers.
- **Unknowns.** Whether `sandbox-exec` honors message filters in a third-party profile, and which
  message numbers SecureTransport's CSSM attach sends **[U: W8]**.
- **Cost.** It is undocumented for third parties, and the numbers follow Apple's private protocol,
  so MT-D3 applies. It is the only option that would close `lockAll` and the keybag routines while
  keeping TLS.

### (f) Pin Copilot 1.0.92

**Ruled out permanently** by the owner on 2026-11-03 ([§4](#4-recommendation)). What follows is
the analysis as written, kept for the record.

- **Feasibility.** 1.0.92 reached GitHub and updated itself inside the unchanged profile **[R]**
  ([#65](https://github.com/mschulkind-oss/yolo-jail/issues/65)). Copilot's own updater must be off
  (`COPILOT_AUTO_UPDATE=false`, documented **[B]**), or it replaces itself.
- **Cost.** Security fixes and server-side API drift accumulate. The pin is a stopgap with an
  expiry, not a ruling.

### (g) The upstream fix

- **The fix.** Route native requests through the `rustls-platform-verifier` already linked into
  `runtime.node` **[B]**. Its verifier API works under p0 **[M]**.
- **Status.** [#5085](https://github.com/github/copilot-cli/issues/5085) was opened 2026-10-08; on
  2026-10-30 it is open, labeled `area:networking`, unassigned, with no comments **[R]**. Another
  sandbox, nono, denies the same service, so yolo is not alone.
- **What yolo can do.** Add its measurements to #5085 from the host; a write from the jail is
  refused by the broker.

### (h) Other routes found

- **h1, Copilot in account mode.** It uses the existing boundary: the owner's secrets are behind
  the UID and file permissions, and securityd is reachable already ([§1.5](#15-run-as-you-versus-the-dedicated-account)).
  Unknowns are Copilot's TLS there, a possible "no keychain found" dialog, and the session hazard
  that applies to every account-mode launch anyway **[U: W2, W5]**.
- **h2, a container backend.** The Linux Copilot reads a CA bundle, so this failure cannot occur
  there **[I]**.
- **h3, a wider profile for the Copilot process only, with its shells re-sandboxed under p0.**
  Seatbelt profiles are inherited, so every MCP server, hook and plugin Copilot spawns would get
  the wider profile unless re-wrapped. Copilot's inner shell sandboxing
  ([config reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-config-dir-reference#configuration-file-settings))
  is Copilot's to configure, and the workspace can edit it. **Rejected**: yolo cannot enforce it.

### Ranking

| Rank | Option | Boundary kept | Works today? | Main unknown |
| :--- | :--- | :--- | :--- | :--- |
| 1 | (g) upstream rustls | Full | No; open, unassigned | GitHub's schedule |
| 2 | (h1) account mode for Copilot | UID and file permissions; session shared, as for every account launch | Probably | W2, W5 |
| 3 | (a) SecurityServer only, files and secd denied | Secrets; not session-wide operations | Probably | W2–W5 |
| 4 | (c1) BYOK over loopback HTTP | Full | Probably, without GitHub models | W7 |
| 5 | (f) pin 1.0.92 | Full | Ruled out for good ([§4](#4-recommendation)) | none: never offered |
| 6 | (b) own audit session | Adds a layer under (a) | Unbuilt | W6 |
| 7 | (c2) redirected GitHub API | Full | Unknown | URL shapes and other hosts |
| 8 | (e) message filter | Closest to full | Unknown | W8; MT-D3 |
| 9 | (d) dylib injection | Full | Blocked for the SEA binary | MT-D3 |

## 3. Mac witnesses

**Constraint, standing.** Nothing below mutates a keychain in the owner's session, and nothing
runs `security lock-keychain` there. Every Security.framework call that touches a keychain runs
only in the disposable account `yolo-witness`, reached over SSH so that it has its own session, as
in the [runbook's scope](../plans/macos-native-comparison-runbook.md#scope-and-stop-conditions).
`sudo bash /Users/Shared/yolo/witness-account.sh create` makes it. W0 and W1 are read-only and may
run in the owner's account. In zsh, inside `yolo-witness`, set `W=$(mktemp -d)`, then write p0 and
p2:

```zsh
yolo internal macos-user-profile --mode self --workspace "$W" --tmp "$W/tmp" >"$W/p0.sb"
{ cat "$W/p0.sb"; echo '(allow mach-lookup (global-name "com.apple.SecurityServer"))'; } >"$W/p2.sb"
```

**W0, Copilot's signature (owner account, read-only).** Confirms [(d)](#d-inject-a-rustls-shim-dyld_insert_libraries).

```zsh
B=$(npm root -g)/@github/copilot/node_modules/@github/copilot-darwin-arm64/copilot
[ -x "$B" ] || B=$(npm root -g)/@github/copilot-darwin-arm64/copilot
codesign -dv --verbose=4 "$B" 2>&1 | rg 'Identifier|TeamIdentifier|flags|Runtime'
codesign -d --entitlements - "$B"
```

Expected: TeamIdentifier `VEKTX9H2N7`, `flags=0x10000(runtime)`, and the three entitlements above
with no `allow-dyld-environment-variables`.

**W1, does `sudo -u _yolojail` share the owner's security session? (Owner account, read-only.)**
`SessionGetInfo` reads the audit session and touches no keychain. [§1.5](#15-run-as-you-versus-the-dedicated-account)
rests on it.

```zsh
mkdir -p /Users/Shared/yolo/sid && cd /Users/Shared/yolo/sid
printf 'import Security\nvar s=SecuritySessionId(0);var a=SessionAttributeBits(rawValue:0)\nprint(SessionGetInfo(callerSecuritySession,&s,&a),s,a.rawValue)\n' >sid.swift
swiftc -O sid.swift -o sid && chmod 755 sid && ./sid && sudo -n -u _yolojail ./sid
```

Expected: the same session id on both lines.

**W2, Copilot's own TLS under p0, p2 and account mode (`yolo-witness`, no Copilot login).** The
request is the credential-free release check from #65. With `COPILOT_HOME` fresh there is no token
to read. Set `B` as in W0, inside `yolo-witness`.

```zsh
for p in p0 p2; do COPILOT_HOME=$(mktemp -d) sandbox-exec -f "$W/$p.sb" "$B" update 2>&1 | tail -2; done
```

Then repeat the line with no `sandbox-exec`, as the control. For account mode, run the
[runbook's Copilot row](../plans/macos-native-comparison-runbook.md#copilot-native-tls-under-the-keychain-deny)
with `"macos_user_mode": "account"` on a Mac where `yolo-witness` may be account mode's user.
Expected: p0 shows `bad certificate format`; p2 and the control show a version line. Record any
dialog that appears.

**W3, pathless queries under p2 against a synthetic default keychain (`yolo-witness` only).** The
witness account has no default keychain **[M]**, so this run makes one. It is the only witness that
sets a default keychain, and it does so for the disposable account alone.

```zsh
K=$HOME/Library/Keychains/wit.keychain-db
security create-keychain -p wit "$K" && security default-keychain -s "$K"
security list-keychains -d user -s "$K" && security set-keychain-settings "$K" && security unlock-keychain -p wit "$K"
security add-generic-password -s wit -a a -w synthetic -T /usr/bin/security "$K"
for p in p0 p2; do sandbox-exec -f "$W/$p.sb" security find-generic-password -s wit -a a -w >/dev/null 2>&1; echo "$p pathless rc=$?"; done
sandbox-exec -f "$W/p2.sb" env HOME="$HOME" security find-generic-password -s wit -a a -w >/dev/null 2>&1; echo "p2 realHOME rc=$?"
```

Expected: a non-zero exit on all three lines; the outside control prints the secret. A zero
falsifies option (a).

**W4, path aliases under p2 (`yolo-witness` only).** These are the escapes that matter once the
file deny is the only wall.

```zsh
sandbox-exec -f "$W/p2.sb" ln "$K" "$W/alias.db"; echo "hardlink rc=$?"
[ -e "$W/alias.db" ] && sandbox-exec -f "$W/p2.sb" security find-generic-password -s wit -a a -w "$W/alias.db" >/dev/null 2>&1; echo "alias read rc=$?"
sandbox-exec -f "$W/p2.sb" cat "/System/Volumes/Data$K" >/dev/null 2>&1; echo "firmlink rc=$?"
tmutil listlocalsnapshots / | head -3   # snapshot paths: try a read of each mounted one under p2 the same way
```

Expected: every rc non-zero. Any zero means option (a) needs a broader file deny first.

**W5, session routines (`yolo-witness` plus a second disposable UID, never the owner).** Make a
second hidden account `yolo-witness2` with the same script. In `yolo-witness`'s SSH session,
unlock the synthetic `$K`, then:

```zsh
sudo -n -u yolo-witness2 security lock-keychain -a; security show-keychain-info "$K"
```

This shows whether a different UID in the same session can lock it, which is the account-mode
hazard. Then repeat as `yolo-witness` under p2, which is option (a)'s hazard. For
`resetKeyStorePassphrase`, a probe calling `SecKeychainResetLogin` is the nearest public entry;
run it **only** if the maintainer accepts losing `yolo-witness`'s keybag, and otherwise mark it
untested.

**W6, `SessionCreate` for a gui-domain agent (`yolo-witness`, needs its GUI login).** Bootstrap a
LaunchAgent plist with `SessionCreate` true that runs `/Users/Shared/yolo/sid/sid` into
`gui/$(id -u)`, and compare the session id with an SSH shell's. A different id means (b) is
buildable without root.

**W7, BYOK offline over loopback (`yolo-witness`).** Point `COPILOT_PROVIDER_BASE_URL` at a
loopback port with `COPILOT_OFFLINE=true` under p0, with a stub server that logs the first
request line. A logged request and no TLS error confirm (c1).

**W8, message filtering.** Under p2, add an `apply-message-filter` that denies only the `lockAll`
message number and rerun W2 and W5. If `sandbox-exec` rejects the profile, (e) is closed.

## 4. Recommendation

**The owner's ruling, 2026-11-03:** *"yes I want to build 1 now of the keychain thing
regardless., the rest I think we need to wait on, and I don't want to do 4 ever"*. In this
research's terms:

- **Built: the refusal** (#65's option 1, item 1 below without its pin). In run-as-you mode the
  Copilot launcher reads the installed version with `copilot --version` before its update act and
  refuses 1.0.93 and later, naming [#5085](https://github.com/github/copilot-cli/issues/5085). It
  names two routes, as commands typed outside the jail: the dedicated-account mode
  (`yolo config set macos_user_mode account --user`) and a container backend, for one launch
  (`YOLO_RUNTIME=podman yolo -- copilot`) or for good (`yolo config set runtime podman --user`).
  It offers no `YOLO_USE_INSTALLED` and no "Start anyway:", and the SecurityServer deny is
  unchanged. The rule is the copilot pack's
  [`copilot.json`](../../packs/copilot/home/.config/yolo-program-compatibility/copilot.json); the
  gate and its next steps are
  [`programcompatibility.go`](../../internal/entrypoint/programcompatibility.go).
- **Never: a Copilot 1.0.92 pin**, option (f) here and #65's temporary pin. No refusal, finding
  or guide offers one.
- **Waiting on the owner: (a) and (h1).** Neither is built, and both still need W2–W5.

**Mac witness for the built refusal (W9).** On the owner's Mac, in run-as-you mode with Copilot
1.0.93 or later installed, run `yolo -- copilot`, and again with `YOLO_USE_INSTALLED=copilot`.
Expected both times: the refusal above, before any "Checking GitHub" line, and no "Start anyway:".
Then each of the three commands it names leads to a running Copilot.

**My opinion** (written before the ruling; the pin in item 1 is now ruled out).

1. **Keep p0 as shipped, and make the refusal useful.** Replace "Start anyway"
   (`YOLO_USE_INSTALLED`) with the routes that work: account mode, a container backend, or a
   1.0.92 pin. Post yolo's measurements to #5085 from the host.
2. **Run W0–W5 next.** They decide (h1) and (a), and W1 and W5 matter even if Copilot is dropped:
   they test the account-mode boundary that ships today.
3. **Offer (a) only if W2–W5 all come back clean**, and only for launches that select Copilot.
   Disclose it at launch, as "this jail can lock your keychains and raise keychain dialogs; it
   cannot read them". Remove it when #5085 ships.
4. **Do not adopt (d) or (e)**; MT-D3 already rules them out. Offer (c1) as a documented recipe,
   not a default.

**What the owner must decide.**

- **Re-word MT-D7, or not.** MT-D7 forbids the service *and* the files *and* secd. This research
  shows the files carry most of the weight for secrets, and the service carries session-wide
  operations. Is "may lock and prompt, may not read" acceptable for a Copilot launch, given the
  W2–W5 results? This overturns MT-D1 for that one launch shape.
- **Whether account mode's shared session is acceptable** once W1 and W5 report. If it is not,
  account mode needs its own session (b), whatever happens to Copilot.
- **Whether a Copilot pin is allowed at all**: ruled 2026-11-03, never.

## Sources

- Apple open source, `main` on 2026-10-30:
  - [`ucsp.defs`](https://github.com/apple-oss-distributions/Security/blob/main/OSX/libsecurityd/mig/ucsp.defs),
    [`transition.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/securityd/src/transition.cpp),
    [`session.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/securityd/src/session.cpp)
    and [`kcdatabase.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/securityd/src/kcdatabase.cpp);
  - [`DLDBListCFPref.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/OSX/libsecurity_keychain/lib/DLDBListCFPref.cpp)
    and [`AtomicFile.cpp`](https://github.com/apple-oss-distributions/Security/blob/main/OSX/libsecurity_filedb/lib/AtomicFile.cpp);
  - the launchd plists linked in [§1.1](#11-which-services-are-involved-and-which-one-copilot-needs);
  - XNU [`audit_syscalls.c`](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/security/audit/audit_syscalls.c).
- Copilot 1.0.105, downloaded and read statically, never run:
  - the npm `@github/copilot-darwin-arm64` tarball, SHA-256
    `f4d91f1d5679110809280919bdee406ef50775975040ee3bb0a1fb54dd3b3b9a`, whose Mach-O `copilot` is
    `5fd7d15ff02deb08a58593b1b488015bb52c1686014b80b5509e28263cb3ad70`. Its code-signature blob was
    parsed for flags, team ID and entitlements;
  - the [v1.0.105 release](https://github.com/github/copilot-cli/releases/tag/v1.0.105) darwin-arm64
    tarball, SHA-256 `bf394d7c2f3dcd81efa8d4b7adcacfe4fd748103601d334b948678f67f00fa59`, whose
    `cli-main.js` is `8b6ce892c34775028d9f8c31e39a346e24becdd94f660a6985f8ecb53e8b3fde`.
- Upstream: [github/copilot-cli#5085](https://github.com/github/copilot-cli/issues/5085), read
  2026-10-30.
- WebKit: [`com.apple.WebProcess.sb.in`](https://github.com/WebKit/WebKit/blob/main/Source/WebKit/WebProcess/com.apple.WebProcess.sb.in),
  for message-filter syntax.
- Apple: [TN3137](https://developer.apple.com/documentation/technotes/tn3137-on-mac-keychains) and
  the [hardened-runtime entitlements](https://developer.apple.com/documentation/bundleresources/entitlements/com.apple.security.cs.allow-dyld-environment-variables).
- yolo's own records: the [SecurityServer audit](../lab-reports/2026-10-29-mac-securityserver-audit.md),
  the [witness run](../lab-reports/2026-10-29-mac-native-witnesses-runbook.md),
  [`macos-native-tls.md`](../design/macos-native-tls.md) and
  [`seatbeltdeny.go`](../../internal/macosuser/seatbeltdeny.go).

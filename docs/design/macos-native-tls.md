---
title: "mise cannot reach HTTPS inside macos-user, and the keychain deny is why"
date: 2026-10-23
status: in-review
stage: DESIGN
next: "Run OQ-MT6 on a managed Mac whose network intercepts TLS, and find a way past the Copilot compatibility guard for OQ-MT11; leave SecurityServer denied"
tags: [design, macos, macos-user, seatbelt, tls, mise, ca-trust]
summary: "Inside the macos-user Seatbelt profile, yolo's nixpkgs mise fails every HTTPS call with 'bad certificate format'. It is built with native-tls, whose SecureTransport handshake reads the server key through Apple's old CDSA code, which checks in with securityd (com.apple.SecurityServer). The profile denies that service on purpose because it fronts the login keychain. The proposal keeps the deny and ships a mise build that verifies through rustls-platform-verifier, which talks to trustd only. Measured on the maintainer's Mac 2026-10-23."
---

# mise cannot reach HTTPS inside macos-user, and the keychain deny is why

**Status:** 2026-10-29. The mise fix, the certificate/keychain boundary and the Safehouse comparison are measured on a Mac ([the native audit](#mac-side-follow-up--audit-the-securityserver-boundary), [the witness run](../lab-reports/2026-10-29-mac-native-witnesses-runbook.md)); two witnesses remain, [OQ-MT6](#OQ-MT6) (untested) and [OQ-MT11](#OQ-MT11) (unreachable on a shipped launch).

> **In short.** Keep securityd denied and change mise, the same way the Go fix changed Go: ship
> a mise build whose TLS verifies through the trust daemon the profile already allows, so
> nothing in the sandbox needs the keychain's front door.

**Why it matters.** On a run-as-you launch every tool a repo's `mise.toml` declares (node, go, uv,
just, …) fails to install, so the agent starts without them. It was reported from a managed Mac;
the cause in [§2](#2-the-cause) predicts every Mac.

**Cost.** Each Mac compiles mise once per nixpkgs bump: no binary cache holds this variant.

**Start at [§2](#2-the-cause).**

**Mac-side follow-up:** The yolo [SecurityServer boundary audit](../lab-reports/2026-10-29-mac-securityserver-audit.md) is measured; the Copilot request path remains open before graduation ([OQ-MT11](#OQ-MT11)).

**Owed by the Mac agent:** the native witnesses [OQ-MT6](#OQ-MT6) and [OQ-MT11](#OQ-MT11) only, with their commands in the [comparison runbook](../plans/macos-native-comparison-runbook.md#native-witnesses-one-per-open-question).

**Needs your ruling:** none until the [OQ-MT12](#OQ-MT12) research reports. **Ruled 2026-10-23:** [OQ-MT1](#OQ-MT1) (a mise yolo installs,
ideally from Nix, no maintained patches; never securityd), [OQ-MT2](#OQ-MT2) (account mode may
miss login-keychain CAs; documented).

**Reads with:** [`provisioner-sets.md`](provisioner-sets.md) (the CA trust rulings, PS-D10 and
PS-D14), [`macos-user-model.md`](macos-user-model.md) (the profile and its keychain promise),
[`copilot-native-networking.md`](../research/copilot-native-networking.md) (yolo's native TLS controls and bounded Copilot/Safehouse comparison; not a native Copilot reproduction).

---

## 1. What was seen

From a run-as-you launch on a managed Mac with an MDM-installed corporate CA:

- **mise fails every HTTPS call** inside the sandbox with `bad certificate format`.
- **curl works** inside the same sandbox, and mise works outside it and under a fully permissive
  profile, with either CA bundle.
- **One deny is responsible.** Removing the profile's denies one at a time, then the service names
  one at a time, isolates `(global-name "com.apple.SecurityServer")`
  (`internal/macosuser/seatbeltdeny.go:881`, commented "securityd: the login keychain"). Allowing
  that one name makes mise work under the otherwise unchanged profile.

## 2. The cause

Traced from source; the witnesses in [§5](#5-witnesses-for-the-mac) measure it.

1. **The mise in the sandbox is yolo's.** nixpkgs `mise` is in the core floor (`flake.nix:1495`),
   which feeds the macos-user tool profile, and that profile ranks ahead of Homebrew on the
   sandbox PATH (`internal/macosuser/macosuser.go:1429`).
2. **That build uses native-tls.** mise's `Cargo.toml` defaults to `native-tls`, and nixpkgs does
   not change the default features (Homebrew's formula is unchecked). On macOS native-tls hands the handshake to
   SecureTransport.
3. **SecureTransport reaches securityd before any trust check.** It reads the server's public key
   with the old CDSA `SecTrustCopyPublicKey`, which builds a CSSM key and so opens CSSM's module
   directory, whose first act is to contact securityd at `com.apple.SecurityServer`. Denied, the
   key comes back NULL and the handshake fails with `errSSLBadCert`, whose documented text is "bad
   certificate format".
4. **Nothing is detected at run time.** Which trust path a program takes is fixed when it is
   built: native-tls compiled for macOS always calls the Security framework, and that framework
   has no environment variable or file that points it elsewhere. So the sandbox cannot steer it;
   only a different build can.
5. **CA files play no part.** mise loads no certificate file and reads no `SSL_CERT_FILE` on
   macOS, so which CAs the launch's bundle holds ([`cabundle.go`](../../internal/macosuser/cabundle.go))
   changes nothing. Dropping the login keychain's CAs would not help.

> [!NOTE]
> curl and nix git read the bundle file, as every TLS client in a podman jail does, which is why
> they work. Native macOS programs ask the Security framework instead. Go is the one such program
> fixed so far: the launch sets a `GODEBUG` value so Go verifies against the bundle
> ([`cagodebug.go`](../../internal/macosuser/cagodebug.go)). mise has no equivalent switch.

## 3. The proposal

**Keep the deny, and build nixpkgs' own mise with its other TLS feature on darwin.** mise offers
`rustls-native-roots` as a cargo feature beside the default `native-tls`; mise's official release
binaries are built with it. On macOS it verifies through rustls-platform-verifier, which creates
and evaluates a `SecTrust` (the newer API). The [native control](#mac-side-follow-up--audit-the-securityserver-boundary) confirms this certificate-evaluation API succeeds under the unchanged deny and rejects an untrusted chain. That sends evaluation to trustd, and
`com.apple.trustd.agent` is already allowed (`seatbeltdeny.go:653`); this verifier API does not need the CDSA path
in [§2](#2-the-cause) step 3. trustd reads the System keychain's trust settings itself, so an MDM CA
should be trusted with no bundle at all.

- **Delivery:** the macos-user tool profile takes nixpkgs mise with its features switched, set in
  `flake.nix`: a build setting, not a patch, so nothing of nixpkgs' recipe is copied or kept.
  Linux and the container backends keep stock nixpkgs mise.
- **Cost:** no binary cache holds this variant, so each Mac compiles mise once per nixpkgs bump.
  If that proves too slow, option A (mise's official binary, hash-pinned) replaces it.
- **This is the fix, not a bridge.** Proposing the switch to nixpkgs (option F) is not pursued
  ([MT-D6](#decision-ledger)): outside a sandbox that denies securityd, nixpkgs' users gain nothing.
- **Done looks like:** in a run-as-you launch with the profile unchanged, `mise ls-remote node`
  prints versions, and a repo's `mise.toml` tools install.

## 4. Alternatives

| Option | Cost | Security | Verdict |
| :--- | :--- | :--- | :--- |
| **A — release mise on darwin** | A second version pin; nixpkgs patches lost | The keychain deny stays | Fallback if B compiles too slowly |
| **B — rebuild nixpkgs mise with `rustls-native-roots`** | No binary cache: mise compiles on each Mac after every nixpkgs bump, and its TLS crate may need cmake | Same as A | Chosen bridge ([§3](#3-the-proposal), MT-D5) |
| **C — allow `com.apple.SecurityServer`** | One line | **Weakens run-as-you's boundary:** a Mach service lookup is not blanket authorization; per-item access controls still apply. But the [Mac audit](#mac-side-follow-up--audit-the-securityserver-boundary) measured a no-prompt read/write/delete/lock of an unlocked, readable synthetic file-based keychain trusted for `/usr/bin/security` when only this service was re-allowed. File denial still stopped tested *path-based* operations on an unreadable keychain; pathless and data-protection operations remain unmeasured. The name-only filter cannot distinguish TLS requests from keychain operations | Rejected under MT-D4 and [MT-D7](#decision-ledger); no boundary change is proposed here |
| **D — no mise in the sandbox** | Repos' tools missing on macos-user | None | Rejected; it is today's failure |
| **E — ask upstream to make rustls the macOS default** | Slow; not in our hands | — | Worth filing; not a fix |
| **F — ask nixpkgs to build mise with `rustls-native-roots`** | One nixpkgs pull request; yolo then keeps nixpkgs mise everywhere | Same as A | Not pursued (MT-D6) |
| **G — our own gatekeeper in front of securityd**, passing only the requests TLS needs | Apple's securityd protocol is private and versioned, and the service name is a compiled-in constant (`SECURITYSERVER_BOOTSTRAP_NAME` in Apple's `OSX/libsecurityd/lib/sscommon.h`; only an in-process C++ call overrides it), so steering a program to the gatekeeper means intercepting its service lookups | Keeps the deny if the filter is right; leaks the keychain under the gatekeeper's identity if not | Rejected (MT-D3): not simple and documented |
| **H — replace the one CDSA call at program start** (`DYLD_INSERT_LIBRARIES`) | A small library, fixes every SecureTransport program | Keeps the deny | Rejected (MT-D3): monkey patching |

## 5. Witnesses for the Mac

For the maintainer's Mac agent. Run in zsh on Apple silicon (on Intel, replace `arm64` with `x64`).
Each run uses fresh mise data and cache directories, so no cached remote list can stand in for a
network call.

```zsh
W=$(mktemp -d) && cd "$W"
printf '(version 1)\n(allow default)\n(deny mach-lookup (global-name "com.apple.SecurityServer"))\n' > d.sb
N=$(nix build --no-link --print-out-paths github:nixos/nixpkgs/fe13242281a67641c0102cdb1087f0ebe586c7df#mise)/bin/mise
curl -fsSL https://github.com/jdx/mise/releases/download/v2026.10.3/mise-v2026.10.3-macos-arm64.tar.gz | tar xz; R=$W/mise/bin/mise
p(){ MISE_DATA_DIR=$(mktemp -d) MISE_CACHE_DIR=$(mktemp -d) "$@" ls-remote node 2>&1 | tail -1; }
echo "A $(p $N)"; echo "B $(p sandbox-exec -f d.sb $N)"; echo "C $(p sandbox-exec -f d.sb $R)"
echo "D $(p env -u SSL_CERT_FILE sandbox-exec -f d.sb $N)"
echo | openssl s_client -connect nodejs.org:443 -servername nodejs.org 2>/dev/null | openssl x509 -noout -issuer
```

| Line | Expected | If not |
| :--- | :--- | :--- |
| **A**: nixpkgs mise, no sandbox | A Node version, such as `25.x.y` | The control failed; nothing below means anything |
| **B**: nixpkgs mise, only securityd denied | Contains `bad certificate format` | A version means [§2](#2-the-cause) is wrong |
| **C**: release mise, only securityd denied | A Node version | Any TLS error means option A fails; go to B or rule on C |
| **D**: as B, with `SSL_CERT_FILE` unset | Same as B | A version means the bundle path is involved after all, and [§2](#2-the-cause) step 5 is wrong |
| **issuer** | The issuer the Mac sees | If it names the corporate CA and C passed, the platform verifier trusts the MDM CA with no bundle |

### Measured on the maintainer's Mac, 2026-10-23

macOS 26.5 (25F71), arm64; nixpkgs mise `/nix/store/nf7990y6…-mise-2026.10.3`, release mise
`v2026.10.3` (`macos-arm64`, sha256 `0999bd4943523ccd…` of `bin/mise`). Every line came out as
expected, so [§2](#2-the-cause) holds and option A works:

| Line | Result |
| :--- | :--- |
| **A** | `26.12.1` |
| **B** | `bad certificate format` (after `error sending request`, `client error (Connect)`) |
| **C** | `26.12.1` |
| **D** | Same as B. The session already had `SSL_CERT_FILE` unset, so D also ran with it **set** to `/etc/ssl/cert.pem`: still `bad certificate format`, which confirms step 5 more directly |
| **issuer** | `C=US, O=Google Trust Services, CN=WE1`: this network does not intercept TLS, so the corporate CA was not in the path and the platform verifier's MDM trust is **not** exercised here |

**Under yolo's own profile too**, not only the one-deny witness: the profile
`yolo internal macos-user-profile --mode self` renders at `3c35c9f87` (which keeps the
`com.apple.SecurityServer` deny), with `env -i` and fresh mise dirs in its workspace. nixpkgs mise fails
the same way. Release mise lists `26.12.1` and `mise install node@22` installs `22.23.4`. So option A
needs no profile change. The run-as-you launch that hit this (with `mise_tools` from the user config)
failed in provisioning with the same error for `pipx:swarf` and the neovim plugin.

Still unmeasured: a host behind a TLS-intercepting proxy whose CA only the MDM installed, which is what
the issuer line would show.

**Then, once a build delivers [MT-D5](#decision-ledger):** a real run-as-you launch with the shipped profile, running
`mise ls-remote node` and `mise install` in a repo with a `mise.toml`. Expected: versions listed and
tools installed, with no profile change.

## 6. What this does not cover

- **Other native macOS programs** may use SecureTransport; a standalone `SSLHandshake` probe now reproduces the CDSA/`SecurityServer` coupling under the unchanged profile. `SecTrustEvaluateWithError` and URLSession work under that deny. This is a difference between tested APIs, not a claim about every Apple HTTPS implementation; audit the client's actual path before generalizing.
- **Account mode's login-keychain CAs.** A corporate CA kept only in the user's own login keychain
  is not trusted by mise in the separate-account mode. Ruled acceptable and documented for users
  ([MT-D2](#decision-ledger)).
- **Container backends.** Their mise is Linux and reads the bundle file.

## Mac-side follow-up — audit the SecurityServer boundary

**Yolo baseline measured; the Safehouse comparison was measured 2026-10-29 ([OQ-MT3](#OQ-MT3), [OQ-MT4](#OQ-MT4)); Copilot runtime still pending.** The [October 29 native report](../lab-reports/2026-10-29-mac-securityserver-audit.md) (pinned report blob `2b308915f3bbfb60cd683c0560fd57b10fe42acb`; [served revision](https://github.com/mschulkind-oss/yolo-jail/blob/f2a317c4b4515cb160d47dfbd6ea14d9bea86314/docs/lab-reports/2026-10-29-mac-securityserver-audit.md)) ran against deployed yolo `d159a57f0` (`0.15.1+705`) on macOS 26.5. The shipped real `yolo -- bash` self-mode launch and its generated unchanged `p0` deny both blocked the **tested** secret/attribute read, fixture enumeration, add, delete, lock and unlock operations on a disposable unlocked file-based keychain *readable in the workspace*. With **only** `com.apple.SecurityServer` allowed in a throwaway `p2` profile, the same `/usr/bin/security`-trusted no-prompt fixture admitted those operations. For a second disposable file-based keychain *outside* readable roots, path-based operations failed under both `p0` and `p2`. Here, securityd denial is the sole barrier for the readable permissive specimen, and the file deny independently stops these **path-based** operations for the inaccessible specimen. That does not prove access to every real login item, ACL bypass, pathless session operations, or data-protection (`secd`) behavior. No owner credential was read/printed or login keychain modified; default/search-list metadata matched before/after, and fixtures were deleted. [MT-D1, MT-D3 and MT-D4](#decision-ledger) stand; **do not turn on SecurityServer**.

**TLS is not password access.** The [same Mac harness](../lab-reports/2026-10-29-mac-securityserver-audit.md#3-certificate-only-mechanisms-compared-in-the-harness) evaluated a public certificate chain with `SecTrustCreateWithCertificates` + `SecTrustEvaluateWithError` under unchanged `p0` and rejected a self-signed chain (`-67843`). URLSession fetched public HTTPS under `p0`; curl and Node with their own CA inputs also worked (Node's system-keychain certificate listing remained unavailable under the file deny). Only a legacy SecureTransport `SSLHandshake` probe returned `-9808` under `p0` and succeeded under `p2`. That is an API/implementation coupling: legacy peer-key/CDSA setup needs the coarse `SecurityServer` lookup despite *server-cert validation* not requesting user passwords. Rust's crate named `native-tls` uses this failing path; **native OS trust** (`SecTrust` via trustd) is a different working path. URLSession's negative-cert control and `SecTrust`'s hostname-mismatch/SSL-policy check were not recorded, and an MDM-only TLS interception CA was not exercised; do not declare every validation case proved. Client mTLS/private-key operations are outside these server-certificate controls. No Copilot TLS request was run. The separate [research](../research/copilot-native-networking.md#two-public-10105-mac-distributions-and-the-callsite-limit) now distinguishes the opaque npm Mach-O from the release JS/Rust addon, which links *both* TLS paths without establishing which GitHub HTTP caller uses which.

**Remaining bounded handoff — no repeat of the yolo baseline.** The owner has authorized the synthetic-only security-envelope goal; the blocker is a Mac executor, not another owner policy decision. Use an available disposable account/workspace and *only* newly created synthetic keychain fixtures, no real login/default keychain item, owner credential, global search-list change, live Copilot login/prompt/API request or production-policy edit. The first comparison is an unsandboxed no-prompt read plus add/read-back positive control on one explicitly named fixture, then the **same UID, real HOME and exact fixture** under rendered Safehouse Copilot-selected policy, its no-keychain-profile baseline, and unchanged yolo `p0`. Pin effective policy hashes, selection, file/service grants and child inheritance. Run only a small harmless Security.framework probe and child, **not Copilot**: the outer profile's process capability is not proof of Copilot's optional inner shell-tool permissions or actual auth storage. Every query must explicitly bind the sole fixture keychain and fixed class/service/account using applicable documented API selectors; if an add/update cannot be scoped, mark untested. A fresh HOME without a new UID/search list is not isolation. Separately contrast a preexisting dedicated guest UID with its own *synthetic* keychain only if this can be done without owner-account changes. Verify restricted item/ACL behavior after the permissive control, then conditionally expand to attributes, fixture-only enumeration, update/delete, certificate and key operations as independently scoped. Identify file-byte reads separately from daemon item calls, and record prompts, sandbox denies, item ACL denies and missing items separately. No printing secrets. If a restrictive item can only be installed with an unexpected prompt, stop and mark it untested.

**Never run the native report's proposed `security lock-keychain -a` on the owner's session/login keychain.** It is pathless and would touch real state. If a pathless operation must be tested, use a *separate* disposable UID whose sole default keychain is synthetic, without changing any owner search list; otherwise leave that class untested. Native TLS follow-up checks a valid peer **and hostname**, plus both invalid chain and hostname mismatch with verification enabled, while keeping the service deny. A miniature client does not prove Copilot's native caller. The next Copilot test requires a credential-free request via its actual native HTTP path or vendor callsite confirmation; an actual agent login/API request needs a separately human-chosen run. Keep the outcome private and distinguish intended Safehouse broadened access from a demonstrated policy/ACL violation. Only after the actual Copilot path is understood should this design graduate into [`docs/reference/`](../reference/). The remaining witnesses, [OQ-MT6](#OQ-MT6) and [OQ-MT11](#OQ-MT11), are open questions in [§7](#7-open-questions).

## 7. Open questions

1. ✅ <a id="OQ-MT1"></a>**[OQ-MT1](#OQ-MT1): How does mise get TLS inside macos-user?**

   - **A — Release mise on darwin**, mise's own official binary, hash-pinned, keychain deny kept.
   - **B — nixpkgs mise built with rustls**, keychain deny kept, compiled on each Mac.
   - **C — Allow securityd**, which ends run-as-you's keychain promise.

   **Answer:**
   > **Ruled in review 2026-10-23, narrower than the options:** C is out: "We absolutely cannot
   > allow access to the user's keychain and all their passwords inside any jail containment."
   > For the mise yolo delivers into the environment: "I'm fine with picking something as
   > appropriate that we install ourselves. I guess ideally it would come from Nix. That would be
   > more convenient. But I don't want to maintain our own patches or anything."

   Applied in [MT-D5](#decision-ledger): B, kept as the fix ([MT-D6](#decision-ledger)). Witness C passed on
   the maintainer's Mac ([the Mac run](#measured-on-the-maintainers-mac-2026-10-23)), under yolo's
   own profile too, so a rustls mise works under the deny.

2. ✅ <a id="OQ-MT2"></a>**[OQ-MT2](#OQ-MT2): May the release mise miss login-keychain CAs in account mode?**

   You ruled that the host's trust, the login keychain's included, is the sandbox's
   ([`provisioner-sets.md`](provisioner-sets.md)). In account mode the sandbox runs as a separate
   account, so trustd consults that account's login keychain, not yours; a corporate CA only in your
   login keychain is then untrusted by mise, as it already is by Go programs there. Run-as-you mode
   is unaffected.

   - **A — Accept the gap**, disclosed at launch as Go's is, and close both together later.
   - **B — Hold option A** of [OQ-MT1](#OQ-MT1) until a way exists to hand trustd those CAs.

   _Leaning:_ A: the gap already exists for Go, it touches only account mode with a CA kept
   outside the System keychain, and holding the fix leaves every Mac without mise.

   **Answer:**
   > **Ruled in review 2026-10-23, as leaned (A):** "I'm absolutely okay with having the corporate
   > CAs not work in some subset of the guest separate account mode. That's totally fine, and we'll
   > just document that."

**Open questions.** [OQ-MT12](#OQ-MT12) is research. Two witnesses of the 2026-10-29 [Mac run](../lab-reports/2026-10-29-mac-native-witnesses-runbook.md) stay open; the other seven are in the [Decision Ledger](#decision-ledger).

<a id="OQ-MT6"></a>**🔒 OQ-MT6: Does trustd under P0 trust a CA that only the MDM installed?**

<!-- vantage: question id=OQ-MT6 -->

**Untested.** The 2026-10-29 run's network does not intercept TLS ([report](../lab-reports/2026-10-29-mac-native-witnesses-runbook.md#1-results)), so no row could run. [§3](#3-the-proposal) claims it does; the witness needs a managed Mac on a network that intercepts TLS with a CA only the MDM installed.

**Witness:** [MDM-installed CA under P0](../plans/macos-native-comparison-runbook.md#mdm-installed-ca-under-p0).

<a id="OQ-MT11"></a>**🔒 OQ-MT11: Does current Copilot still fail native TLS under the unchanged SecurityServer deny?**

<!-- vantage: question id=OQ-MT11 -->

**Unreachable on a shipped launch.** The compatibility guard refuses Copilot 1.0.93 and newer before Copilot makes any request, so the `bad certificate format` line the runbook expects cannot appear; the rendered profile still contains `com.apple.SecurityServer` ([report §4.2](../lab-reports/2026-10-29-mac-native-witnesses-runbook.md#42-capture-context-and-copilot)). The question stays open until a way past the guard exists. Tracked as [yolo-jail #65](https://github.com/mschulkind-oss/yolo-jail/issues/65), which is open. The deny stays whatever the answer.

**Witness:** [Copilot native TLS under the keychain deny](../plans/macos-native-comparison-runbook.md#copilot-native-tls-under-the-keychain-deny).

<a id="OQ-MT12"></a>**🔒 OQ-MT12: Which programs would hand a keychain secret to a jail granted Safehouse's keychain access?**

<!-- vantage: question id=OQ-MT12 -->

**Research, not a ruling yet.** First pass: [keychain exposure by program](../research/macos-keychain-exposure-by-program.md). [MT-D7](#decision-ledger) keeps the keychain denied. The research names programs that keep secrets in the macOS login keychain, the access list each one gives its items, and whether a process that can run that program, a downloaded copy of it, or `security` can read the secret back with no prompt. It covers the newer data-protection keychain too. The answer decides whether any mode may allow the keychain.

Questions 6 and 11 are witnesses and 12 is research, not rulings; and a row that could not run is untested, not answered. Run any remaining one in a wholly disposable synthetic-only account, under the runbook's [scope and stop conditions](../plans/macos-native-comparison-runbook.md#scope-and-stop-conditions).

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| MT-D1 | The `com.apple.SecurityServer` deny stays while any option that keeps it can work: it is how run-as-you keeps the agent away from the user's login keychain | 2026-10-23 | [§4](#4-alternatives) | — |
| MT-D2 | Account mode may leave CAs held only in the user's login keychain untrusted by mise; the user guide says so ([OQ-MT2](#OQ-MT2) A) | 2026-10-23 | [§6](#6-what-this-does-not-cover) | ✅ 2026-10-23, [Company certificates](../../userguide/guides/macos.md) in the macOS guide |
| MT-D3 | No stand-in for securityd and no patched function: a gatekeeper only if it is simple and documented, which [G](#4-alternatives) is not; the maintainer: "If it requires undocumented things and hacking service names and monkey patching, I don't want that. I also don't want to monkey patch" | 2026-10-23 | [§4](#4-alternatives) | — |
| MT-D4 | Keychain access inside any containment is a non-starter; the mise yolo delivers is ours to choose, ideally from Nix, with no patches we maintain ([OQ-MT1](#OQ-MT1)) | 2026-10-23 | [§3](#3-the-proposal) | — |
| MT-D5 | Bridge: on darwin, the macos-user tool profile takes nixpkgs mise with its cargo features switched from `native-tls` to `rustls-native-roots` (a build setting, not a patch). Fallback if the per-Mac compile proves too slow: option A. Witness C passed 2026-10-23 | 2026-10-23 | [§4](#4-alternatives) | ✅ 2026-10-23, `noncontainerFloorSubstitutes` in `flake.nix`, with the `rustls` feature: at mise v2026.10.3 it and `rustls-native-roots` map every crate to the same `reqwest/rustls`. |
| MT-D6 | The nixpkgs change (option F) is not pursued; the `flake.nix` substitute stays the fix. The maintainer: "I will not pursue this upstream" | 2026-10-23 | [§3](#3-the-proposal) | — |
| MT-D7 | Safehouse-style keychain access (allowing `com.apple.SecurityServer`, `com.apple.securityd.xpc`, `com.apple.secd` and the keychain files) stays off in every mode, account mode included, until [OQ-MT12](#OQ-MT12) is answered. The reason: a keychain item's access list names a *program* by its code signature, or by its contents if it is unsigned, and never the containment that runs it. A jail that can reach the service and read the keychain file therefore gets every item a program it can run, or download a copy of, is trusted for. The vendor that signed that program assumed the keychain was separating users, not jails. The maintainer's example: a cloud CLI that kept its credentials in the login keychain would hand them to the agent | 2026-10-29 | [§4](#4-alternatives) | — |
| <a id="OQ-MT3"></a>[OQ-MT3](#OQ-MT3) | Safehouse's effective Copilot policy admits the synthetic keychain read that yolo's deny blocks: `safehouse-copilot` read the permissive item (status 0, `match=true`), while the no-keychain overlay returned -25300 (`errSecItemNotFound`, `match=false`). This is Safehouse's documented broadened access, not a violation | 2026-10-29 | [report §3.2](../lab-reports/2026-10-29-mac-native-witnesses-runbook.md#32-safehouse) | — |
| <a id="OQ-MT4"></a>[OQ-MT4](#OQ-MT4) | No ACL bypass: an item whose access list trusts no application read -25293 unsandboxed and inside `safehouse-copilot`; under the yolo P0 harness it read -25300, because the service deny stops the call before the ACL is consulted | 2026-10-29 | [report §3.3](../lab-reports/2026-10-29-mac-native-witnesses-runbook.md#33-restrictive-acl) | — |
| <a id="OQ-MT5"></a>[OQ-MT5](#OQ-MT5) | The current tree's P0 blocks the keychain (read permissive and restricted -25300, add -25308) and holds all four mandatory service denies. Identical unsandboxed and under P0: example.com trusted (HTTP 200), self-signed.badssl.com -67843 and wrong.host.badssl.com -67602 not trusted (URLSession -1202) | 2026-10-29 | [report §3.4](../lab-reports/2026-10-29-mac-native-witnesses-runbook.md#34-current-p0-and-tls-controls) | — |
| <a id="OQ-MT7"></a>[OQ-MT7](#OQ-MT7) | The second account-mode launch reuses the inherited user config: both launches ran as `_yolojail`, found it delivered read-only, refused an append and exited 0 with no `refusing` line. `TestMacosUserInheritedUserScopeArrivesReadOnly` fails natively because it probes `config.jsonc` while a TOML-representable scope arrives as `config.toml`; the test is stale (a copy probing `config.toml` passed) | 2026-10-29 | [report §4.1](../lab-reports/2026-10-29-mac-native-witnesses-runbook.md#41-inherited-account-relaunch) | — |
| <a id="OQ-MT8"></a>[OQ-MT8](#OQ-MT8) | A native `yolo capture copilot` carries a valid launch context: no `invalid macos-user launch context` line, and the version probe now gives the intended 1.0.93+ refusal instead of a parse error | 2026-10-29 | [report §4.2](../lab-reports/2026-10-29-mac-native-witnesses-runbook.md#42-capture-context-and-copilot) | — |
| <a id="OQ-MT9"></a>[OQ-MT9](#OQ-MT9) | Turning the Claude credential view Off retires the workspace's registration: status went from 2 views to 1 (the host store's, unchanged), and the workspace credentials file became a symlink to the shared path. Both launches printed a false `not a recognized launch setting` warning for `YOLO_CLAUDE_CREDENTIAL_VIEW`, which the launches nonetheless obeyed | 2026-10-29 | [report §4.3](../lab-reports/2026-10-29-mac-native-witnesses-runbook.md#43-claude-view-on-then-off) | — |
| <a id="OQ-MT10"></a>[OQ-MT10](#OQ-MT10) | Apple Container 1.1.0 emits the JSON the prune reads (every image row had a name and a `sha256:` digest) and the prune works: the dry run and `--apply` printed no `FAILED` line, `--apply` removed three `yolo-jail` tags, and `yolo-jail:latest`, the builder image and every non-yolo image were kept | 2026-10-29 | [report §4.4](../lab-reports/2026-10-29-mac-native-witnesses-runbook.md#44-apple-container-prune) | — |

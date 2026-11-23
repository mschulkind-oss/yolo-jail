---
title: "Synthetic native macOS comparison runbook"
status: accepted
stage: DECIDED
next: "Run each native witness below only in a wholly disposable synthetic-only UID/session, and record its outcome under its open question in macos-native-tls.md"
tags: [runbook, macos, security, tls]
summary: "Copy-pastable commands and decisive outputs for every outstanding native-macOS witness (Safehouse, restrictive ACL, P0/TLS negatives, MDM CA, the inherited relaunch, capture context and Claude view transitions, Apple Container JSON, Copilot native TLS); the yolo baseline is prior evidence, not a result of this runbook."
---

# Synthetic native macOS comparison

**Preparation only, written 2026-10-29 against `e66398b09c0571a98927b6b9eb5256a480ae4dac`; no Mac execution.** The [D159 SecurityServer audit](../design/macos-native-tls.md#mac-evidence-boundary) is existing evidence against yolo `d159a57f0`, not a rerun and not proof for another build. The [native TLS design](../design/macos-native-tls.md#mac-side-follow-up--audit-the-securityserver-boundary) and [Copilot networking research](../research/copilot-native-networking.md) own the behavior and source provenance.

## Scope and stop conditions

The [design's settled scope](../design/macos-native-tls.md#mac-side-follow-up--audit-the-securityserver-boundary) owns behavior. This document prepares the remaining Safehouse comparison, restrictive synthetic ACL, and native SSL-policy/hostname controls, and holds the commands for every other outstanding native witness. Each witness answers one open question in the design, listed under [Native witnesses, one per open question](#native-witnesses-one-per-open-question). Do not reopen the yolo baseline or claim current Copilot works.

- **The 2026-10-29 run used the disposable account `yolo-witness`** ([Mac evidence](../design/macos-native-tls.md#mac-evidence-witness-run)); the maintainer removed it afterwards, and `sudo bash /Users/Shared/yolo/witness-account.sh create` makes it again for a later run. Answered rows are in the [Decision Ledger](../design/macos-native-tls.md#decision-ledger); only [OQ-MT6](../design/macos-native-tls.md#OQ-MT6) and [OQ-MT11](../design/macos-native-tls.md#OQ-MT11) remain.
- **Every yolo macos-user launch is a Security.framework call too.** At each launch the host-side yolo exports certificates from the System keychain and from the launching user's login keychain with `/usr/bin/security find-certificate` ([`cabundle.go`](../../internal/macosuser/cabundle.go), the `ReadLoginKeychain` seam in [`orchestrator.go`](../../internal/macosuser/orchestrator.go)). So every launch, capture and integration run below happens only in the disposable account's own session, never the owner's.
- Use a wholly disposable, synthetic-only macOS UID/session for **every** Security.framework call in this runbook, including fixture creation, setup, preflight, comparison rows, and cleanup. That session must not use or contain the owner's login keychains or credentials. A disposable workspace or separate `HOME` under the owner's UID/session is not sufficient.
- The retained [`SecKeychainCreate` header](https://github.com/apple-oss-distributions/Security/blob/main/OSX/libsecurity_keychain/lib/SecKeychain.h) documents creation at a supplied file path but says nothing about whether creation changes the current user's default keychain or search list; no implementation source is retained here to settle that side effect. Treat it as **unknown**. Do not call `SecKeychainCreate` in an owner UID/session, even transiently. If a wholly disposable synthetic-only UID/session is unavailable, stop before setup; never snapshot/restore owner state as a workaround.
- Keep the fixture database at an explicit path under the disposable workspace and open that path directly. Never pass a null keychain selector that falls back to a user's default/search list. Explicit path selection does not remove the isolation requirement above.
- The tested file path should be readable in the comparison workspace. This measures mediated item operations with a readable file; it does not test Safehouse's host-home keychain-file grants.
- Do not read/print credentials, change default/search lists, alter owner keychains, inspect owner default/search-list metadata, use `lock-keychain -a`, run agents or login/API tests, or deploy a profile. Set Security.framework user interaction off and report prompt/interaction errors separately from ACL and sandbox errors.
- Preserve the production `com.apple.SecurityServer`, `com.apple.securityd.xpc`, `com.apple.secd` and other yolo denies. Do not add a service allow to yolo.
- Stop and mark the relevant row untested if the fixture cannot be created, ACL behavior cannot be distinguished from interaction, or the effective rendered policy does not match the described case. Do not repair a failing production deny in this comparison.
- No pathless operation is authorized here. It remains untested unless a disposable UID with its own sole synthetic default keychain is already available without changing owner state.
- Copilot actual native HTTP remains untested until public/vendor evidence identifies a credential-free supported request path. Do not invent an endpoint, selector, constructor or request; no Copilot login, prompt or API call is part of this runbook.

The host that prepared this document was Linux without `sandbox-exec`. That is an executor gap, not a request for another policy ruling. The existing yolo measurements remain the baseline; no RED callsite or production-code change is applicable to this docs-only preparation.

## Pinned inputs and what they prove

| Input | Use | Boundary |
| :--- | :--- | :--- |
| [Safehouse at `f1c6a6a355b1d82cc3f4b267c53f1f9ce456f9af`](https://github.com/eugene1g/agent-safehouse/tree/f1c6a6a355b1d82cc3f4b267c53f1f9ce456f9af) | Parse `--stdout`, `--workdir`, `--append-profile`; render and inspect selected Copilot policy | Pin and hash the executable/source. The Copilot profile requires the keychain module; do not infer inactivity from an optional-module label. |
| [Safehouse Copilot profile](https://github.com/eugene1g/agent-safehouse/blob/f1c6a6a355b1d82cc3f4b267c53f1f9ce456f9af/profiles/60-agents/copilot-cli.sb) and [keychain profile](https://github.com/eugene1g/agent-safehouse/blob/f1c6a6a355b1d82cc3f4b267c53f1f9ce456f9af/profiles/55-integrations-optional/keychain.sb) | Verify the selected agent's requirement and exact file/service grants | Profile selection is policy evidence only, not item access or actual Copilot behavior. |
| [Safehouse policy architecture](https://github.com/eugene1g/agent-safehouse/blob/f1c6a6a355b1d82cc3f4b267c53f1f9ce456f9af/docs/docs/policy-architecture.md), [launch implementation](https://github.com/eugene1g/agent-safehouse/blob/f1c6a6a355b1d82cc3f4b267c53f1f9ce456f9af/bin/lib/runtime/launch.sh), and [debugging guide](https://github.com/eugene1g/agent-safehouse/blob/f1c6a6a355b1d82cc3f4b267c53f1f9ce456f9af/docs/docs/debugging.md) | Confirm appended rules render after selected profiles and child processes inherit the outer sandbox | A policy probe is not the Copilot CLI's optional inner shell sandbox. |
| [`yolo internal macos-user-profile`](../../internal/cli/macosuserprofile.go) | Render a yolo P0 harness profile for an explicit workspace and temp directory | This helper omits production launch inputs; label it harness P0, never full launch equivalence. |
| [`DenyDefaultProfile`](../../internal/macosuser/seatbeltdeny.go) and the [D159 audit](../design/macos-native-tls.md#mac-evidence-boundary) | Check the unchanged deny and identify prior measured baseline | Audit ran against `d159a57f0`; do not silently attribute its results to the current tree. |
| [Apple Security framework headers](https://github.com/apple-oss-distributions/Security) | Confirm legacy fixture APIs and access-list arguments before compiling | Read-only header snapshot was unversioned `main`; record the actual macOS SDK/Xcode version and recheck its installed headers before execution. |

## Prepare an isolated fixture

Use a fresh temporary workspace inside the wholly disposable synthetic-only UID/session, with restrictive directory permissions and no symlinked fixture paths. Record that session's UID, physical workspace path and real process `HOME`; use the same caller binary, child process, UID, `HOME`, working directory and exact fixture across each newly run comparison row. **Do not use the owner's UID/session, login keychains, or credentials for any Security.framework operation.** A separate `HOME` directory does not create a credential boundary or isolate Security.framework state.

The retained [`SecKeychainCreate` header](https://github.com/apple-oss-distributions/Security/blob/main/OSX/libsecurity_keychain/lib/SecKeychain.h) describes creating a keychain at the supplied file path but does not specify whether the call also changes the current user's default keychain or search list. No Apple implementation source is present in the retained source snapshot, so that effect remains unknown. The isolation requirement is therefore mandatory even though the fixture uses an explicit path. If a synthetic-only UID/session cannot be used, stop before `setup`; do not call the API as a test, inspect owner search-list state, or attempt restoration afterward.

1. Build the disposable keychain probe as described under [Build the disposable probes](#build-the-disposable-probes), then run its `setup` mode once outside all sandboxes **within the synthetic-only UID/session**. Create one new, non-login file keychain at `$W/fixture.keychain-db` using the installed Security.framework API with a synthetic password and no user interaction. Do not add it to any search list or make it default. If creation/unlock would touch or prompt for owner state, stop.
2. In that same one-time setup, create two preexisting generic-password items in the explicit keychain. Fix the class to generic password and use constant test-only service/account labels, for example `yolo-native-comparison` / `synthetic-permissive` and `yolo-native-comparison` / `synthetic-restricted`. Give each item a distinct fixed, test-only byte string embedded in the private probe source; the program compares bytes internally and prints only `match=true|false`.
3. Build access objects explicitly with `SecAccessCreate(descriptor, trustedApplications, &access)`. The permissive item's list contains a `SecTrustedApplicationRef` made from the exact probe executable path; the restrictive item's list is an empty `CFArray`. Apple's header says `NULL` instead means default trust for the creating application. Never use `NULL` accidentally for the restrictive fixture. Confirm the resulting ACLs using the API and report creation/readback errors.
4. Call `SecKeychainSetUserInteractionAllowed(false)` before fixture operations. Create the file keychain at the absolute workspace path with `SecKeychainCreate(path, syntheticPasswordLength, syntheticPassword, false, NULL, &keychain)`, then explicitly reopen it with `SecKeychainOpen(path, &keychain)` in each probe process. If it is locked, use `SecKeychainUnlock(keychain, syntheticPasswordLength, syntheticPassword, true)` with that non-null fixture reference. Do not pass a null keychain reference to unlock, find, add or read. Use `SecKeychainFindGenericPassword(keychain, ..., fixedService, ..., fixedAccount, ...)` for the legacy path-based item checks. The keychain argument must never be a search-list array. Pass returned password bytes only to an in-process equality check, then free them; emit no value, length, hash or buffer.
5. Create each preexisting item with `SecKeychainItemCreateFromContent(kSecGenericPasswordItemClass, attributesForFixedServiceAndAccount, syntheticValueLength, syntheticValue, keychain, access, &item)`: the attributes list contains only the fixed test labels, `keychain` is the explicit fixture reference, and `access` is the ACL from step 3. The checked headers confirm this call accepts both selectors. If using modern APIs instead, use `SecItemAdd` with `kSecUseKeychain` naming the fixture and `SecItemCopyMatching` with `kSecMatchSearchList` containing only that fixture; first verify both selectors against the installed SDK. Do not treat a modern `SecItem*`/`secd` result as interchangeable with the CDSA `SecKeychain*` path measured in D159.
6. Still outside all sandboxes, use this same executable to establish the permissive item's no-prompt read positive control and the restrictive item's expected ACL denial. Before interpreting every policy row, run an unsandboxed preflight that explicitly opens the fixture, confirms it is unlocked, and reads/matches the preexisting permissive item. If preflight fails, stop and classify the policy result as unknown; do not attribute auto-lock or fixture drift to a sandbox. Run each comparison row against the same preexisting items; never rerun `setup` inside a policy. If an explicit unlock is needed, unlock only the fixture reference with its synthetic password, then repeat preflight.
7. For each row's auth-like synthetic add/readback check, use a unique fixed account label reserved for that row (for example `synthetic-add-safehouse-copilot`), add only that item to the fixture, read it back in-process, and then run `cleanup-item` outside the sandbox to find/delete only that exact label. If add partly succeeds but readback fails, perform the same narrow cleanup. Never reuse an account label across rows or repeat a prior row without cleanup.

The headers checked while preparing this document are public Apple headers downloaded from unversioned `main`; recheck all signatures against the executor's SDK. In particular, no outcome is inferred from an ACL constructor alone.

## Render and compare Safehouse policies

Use the pinned Safehouse executable built/checked from the revision above. Run its policy-render path only (`safehouse.sh --stdout --workdir W [--append-profile …] copilot`; the pinned source has no `policy` subcommand, and `policy` would be taken as the command to run); it writes the policy to stdout and does not execute the caller. The pinned parser accepts `--stdout`, `--workdir` and `--append-profile`. The Copilot profile declares the keychain integration as a requirement, so do not rely on an invented module-disable switch or add an explicit `--enable` to make the selected policy appear active.

```bash
safehouse.sh --stdout --workdir "$W" copilot >"$W/safehouse-copilot.sb"
```

Read the complete generated profile. Confirm the Copilot profile and keychain module appear, identify grants for the home Keychains path and named Mach services, and confirm the actual workdir and appended-profile ordering. If the source-pinned executable rejects this documented invocation or does not select the Copilot profile, stop rather than trying guessed flags.

Prepare a separate overlay file at `$W/no-keychain.sb` containing these final denies for the paths and services granted by the pinned keychain module:

```scheme
(deny file-read* file-write* (home-subpath "/Library/Keychains"))
(deny file-read* file-write* (home-literal "/Library/Preferences/com.apple.security.plist"))
(deny file-read* (literal "/Library/Preferences/com.apple.security.plist"))
(deny file-read* (literal "/Library/Keychains/System.keychain"))
(deny file-read* (subpath "/private/var/db/mds"))
(deny mach-lookup
  (global-name "com.apple.SecurityServer")
  (global-name "com.apple.security.agent")
  (global-name "com.apple.securityd.xpc")
  (global-name "com.apple.security.authhost")
  (global-name "com.apple.secd")
  (global-name "com.apple.trustd"))
(deny ipc-posix-shm-read-data ipc-posix-shm-write-create ipc-posix-shm-write-data
  (ipc-posix-name "com.apple.AppleDatabaseChanged"))
```

Keep the normal workspace read/write grant so the synthetic file remains readable. Then render the comparison variant through the supported append mechanism:

```bash
safehouse.sh --stdout --workdir "$W" \
  --append-profile "$W/no-keychain.sb" copilot >"$W/safehouse-no-keychain.sb"
```

Confirm the appended overlay occurs after the selected profiles and all earlier appended policies. The pinned renderer then emits append-file protections and terminal denies afterward; verify those later rules do not include an allow that reopens any capability the overlay is intended to deny, and confirm each intended deny remains effective. The deny overlay deliberately narrows the effective policy; it is not a claim that Safehouse supports disabling the required module. Because denying `trustd` may affect certificate validation, record keychain and TLS results separately; do not attribute a TLS failure in this variant to the SecurityServer path alone.

Run the probe in `spawn-child` mode under each saved policy, with the same UID, real `HOME`, working directory and absolute fixture path. This process is the parent: it must `fork`, then have only the child `execve` the identical hashed probe binary by its absolute path with the child mode; the parent must `waitpid` for that child. Both processes record PID, PPID, UID, real `HOME`, executable path and SHA-256. Require the parent and child to match each other and the row's expected UID, `HOME`, executable path and binary hash. A failed `fork`, child `execve` or parent `waitpid` is an untested/failed control, never a pass. Do not invoke a shell. The child runs the fixture-only operation; child sandbox inheritance is part of the observation. The harness makes no Copilot process and no agent request. The fixed item labels are selected from a compiled whitelist; never accept a caller-supplied service/account string:

```bash
# Outside every sandbox, immediately before each policy row:
"$W/keychain-probe" --mode preflight --keychain "$W/fixture.keychain-db" \
  --item synthetic-permissive

/usr/bin/sandbox-exec -f "$W/safehouse-copilot.sb" -- \
  "$W/keychain-probe" --mode spawn-child --child-mode read \
  --keychain "$W/fixture.keychain-db" --item synthetic-permissive
/usr/bin/sandbox-exec -f "$W/safehouse-no-keychain.sb" -- \
  "$W/keychain-probe" --mode spawn-child --child-mode read \
  --keychain "$W/fixture.keychain-db" --item synthetic-permissive
/usr/bin/sandbox-exec -f "$W/safehouse-copilot.sb" -- \
  "$W/keychain-probe" --mode spawn-child --child-mode add-readback \
  --keychain "$W/fixture.keychain-db" --item synthetic-add-safehouse-copilot
# After every add-readback attempt, outside the sandbox:
"$W/keychain-probe" --mode cleanup-item --keychain "$W/fixture.keychain-db" \
  --item synthetic-add-safehouse-copilot
```

Repeat the same preflight/parent/child sequence for the restrictive preexisting item and each yolo P0 policy, substituting only fixed policy-specific add labels. The parent and child must report the same probe-binary hash; reject a row if it differs. Record the Safehouse child-probe hash and environment. These rows measure the outer selected Copilot policy applied to a harmless test process, not the policy around Copilot's optional shell-tool subprocess and not actual CLI authentication or TLS.

## Render yolo harness P0; preserve the measured baseline separately

Build/use the yolo binary matching the source revision under test; do not deploy it. Render the helper profile with the fixture workspace, a private temp directory, the actual host home as `--host-home`, and a separate agent-home path beneath the workspace. Keep the process environment `HOME` equal to the real host `HOME`; never pass the real home as `--home`, which would grant the profile's agent-home rules against owner state.

```bash
yolo internal macos-user-profile --mode self \
  --workspace "$W" --home "$W/.yolo/home" --host-home "$HOME" \
  --tmp "$W/tmp" >"$W/yolo-p0-harness.sb"
```

Inspect the result and verify the four mandatory service denies are present: `com.apple.SecurityServer`, `com.apple.securityd`, `com.apple.securityd.xpc` and `com.apple.secd`. Run the same probe, with the same real `HOME` and fixture, under this profile. Label the row **yolo P0 harness profile**, not production launch: `macosUserProfileMain` explicitly leaves machine dirs, context mounts, cache relocations, readonly-workspace state, staged home content and devices empty. It cannot establish complete launch equivalence.

The prior measured yolo results remain exactly those in the [D159 report](../design/macos-native-tls.md#mac-evidence-boundary): shipped launch and generated p0 blocked the tested operations on the then-readable fixture; p2 reallowed only SecurityServer and exposed the trusted permissive specimen. Do not repeat p2, do not construct a SecurityServer allow, and do not update the old report with this unexecuted runbook.

## Build the disposable probes

No native probe source or helper binary is checked in. Materializing the temporary C/Swift sources, confirming their APIs compile against the selected Mac SDK, establishing fixture behavior, and running any native policy row are **unverified prerequisites**, not successful controls. The algorithm below is the source contract for that disposable harness; stop if the API does not compile or differs on the target SDK rather than substituting unreviewed code. On the Mac, write the described probes inside `$W`, then compile them against the active SDK; these commands build those local source files and do not imply they already exist:

```bash
xcrun --sdk macosx clang -std=c11 -Wall -Wextra -Werror -Wno-deprecated-declarations \
  -framework Security -framework CoreFoundation \
  "$W/keychain-probe.c" -o "$W/keychain-probe"
xcrun --sdk macosx swiftc \
  -framework Security -framework Foundation \
  "$W/tls-probe.swift" -o "$W/tls-probe"
```

Record `xcode-select -p`, `xcrun --show-sdk-version`, compiler version and SHA-256 of both source and binaries. Compile once and reuse the identical hashes in every sandbox row. Do not add the temporary source, fixture or binary to this repository.

The temporary keychain probe must implement these modes and selectors; materialization, SDK compilation and native execution remain unverified prerequisites:

1. Parse one required absolute `--keychain` path; reject every other path and never fall back to a default.
2. `setup` runs once outside all sandbox policies. Disable Security.framework interaction, create the keychain only at that path with `SecKeychainCreate`, create the two preexisting ACL-controlled items with `SecKeychainItemCreateFromContent`, close references, then verify them with the unsandboxed positive controls. Store only fixed synthetic test bytes in the temporary private probe source; do not log them.
3. `preflight` runs outside policy rows: open the fixture by path, confirm it is unlocked, and require a no-prompt matching read of the preexisting permissive item. `spawn-child` runs as a parent that records its PID/PPID and identity, forks, and has only the child `execve` the identical hashed probe binary by absolute path with the selected child mode; the parent `waitpid`s and records its result. Parent and child each record PID, PPID, UID, real `HOME`, executable path and SHA-256; require both to match each other and the row's expected UID, `HOME`, executable path and hash. A failed `fork`, child `execve` or parent `waitpid` is an untested/failed control, never a pass. Do not fork a shell or call `sandbox-exec` from inside the probe. The child opens the same fixture reference and executes only the selected fixture mode.
4. Build two `SecAccessRef` values with `SecAccessCreate`: one from a one-element `CFArray` of `SecTrustedApplicationRef` for the exact executable, and one from an empty `CFArray`. Create `kSecGenericPasswordItemClass` records with `SecKeychainItemCreateFromContent`, a `SecKeychainAttributeList` containing only the fixed `kSecServiceItemAttr` and `kSecAccountItemAttr` labels, the fixed synthetic bytes, that explicit `SecKeychainRef`, and the corresponding access object.
5. For `read` and `add-readback`, call `SecKeychainFindGenericPassword` with the explicit keychain reference and exact service/account byte strings and lengths. The `add-readback` mode uses a unique compiled-in per-row account, adds that item with the same explicit keychain reference and fixture ACL, then reads it back. Report `operation`, OSStatus name/code, `match=true|false`, and failure class. Compare returned bytes in memory to fixed synthetic values; print no secret-derived bytes, length or digest. Release item/keychain/access references and API buffers with their matching release/free functions.
6. `cleanup-item` runs outside the sandbox and may find/delete only the exact unique per-row synthetic item label. `setup` never runs within a comparison policy. No mode queries all items or enumerates a user's keychain.

The Swift TLS probe must use no authentication, cookies or API request. For a certificate control, capture the server-presented PEM certificates with `openssl s_client -showcerts -connect "$host:443" -servername "$host" </dev/null`, split each PEM certificate, and convert each to DER using `openssl x509 -in chain-N.pem -outform DER -out chain-N.der`. This fetch is only certificate material; do not treat OpenSSL's connection outcome as a validation result. In the probe, load each DER file with `SecCertificateCreateWithData`, place the server leaf first followed by its presented intermediates in a `CFArray`, create `SecPolicyCreateSSL(true, expectedHostname)` using the exact DNS host, then call `SecTrustCreateWithCertificates(chain, policy, &trust)` and `SecTrustEvaluateWithError`. Record the complete-chain result and `CFError` domain/code without weakening verification. Repeat with the self-signed endpoint's DER chain and its matching DNS hostname (chain must fail), then with the `wrong.host.badssl.com` DER chain and expected hostname `wrong.host.badssl.com` (hostname policy must fail). Keep the certificate source host and expected policy hostname separately recorded.

Separately use `URLSessionConfiguration.ephemeral` and, after SDK-checking the public properties, set `urlCredentialStorage = nil`, `httpCookieStorage = nil` and `httpShouldSetCookies = false`. The [URLSessionConfiguration API](https://developer.apple.com/documentation/foundation/urlsessionconfiguration) supplies the ephemeral mode and credential/cookie properties; confirm the named properties exist in the selected SDK before compiling. The `URLSessionTaskDelegate` redirect callback must return `nil` for every redirect. For its authentication-challenge callback, use `.performDefaultHandling` only when the protection-space method is server trust, with the original challenge unchanged; cancel every other challenge. This keeps URLSession's default server-trust checks and prevents redirect/authentication paths from supplying owner credentials. Issue credential-free GETs to `https://example.com/`, `https://self-signed.badssl.com/` and `https://wrong.host.badssl.com/`: the valid HTTPS request should succeed; the latter two must fail certificate verification. If an endpoint is unreachable or interception changes its chain, record unavailable/unknown, not a pass. The URLSession source and all outcomes remain unverified until a native Mac compiles and runs the temporary source.

Run the native TLS controls under unsandboxed and unchanged yolo P0 harness profiles. Safehouse rows may be useful comparison data but are not Copilot TLS evidence. Do not use SecureTransport success under an added SecurityServer allow as a control: that weakens the boundary and is out of scope.

The TLS probe takes exactly two modes, so the witness commands below can call it:

- `--mode sectrust --chain-dir DIR --host HOST` loads `DIR/chain-1.der`, `chain-2.der`, … in that order, leaf first, and evaluates them against `SecPolicyCreateSSL(true, HOST)`.
- `--mode urlsession --url URL` makes the one credential-free GET described above.

Each prints only the mode, the host, the trust result or HTTP status, and the error domain and code.

## Native witnesses, one per open question

Each subsection below answers one 🔒 open question in [the native TLS design](../design/macos-native-tls.md#7-open-questions). Run them in one sitting, in the disposable account. Record each outcome against its question; a row that could not run is **untested**, never a pass.

**Common preamble, in every shell.** Each block is zsh. `DISPOSABLE` is the account name the owner chose for this run, and the guard stops the shell in any other session:

```zsh
DISPOSABLE='<the disposable account the owner chose>'
[ "$(id -un)" = "$DISPOSABLE" ] || { echo "STOP: not the disposable account"; exit 1; }
sw_vers; uname -m; yolo --version
W=$(mktemp -d "$HOME/native-witness.XXXXXX") && cd "$W" && echo "W=$W"
```

**What no witness may do.** Read or change the owner's keychains, default keychain or search list, even transiently; snapshot or restore them; run `security lock-keychain -a` in the owner's session; add a `com.apple.SecurityServer` allow to any yolo profile; or log in to Copilot or Claude anywhere but a disposable account the owner chose separately for that login.

### Safehouse effective policy

Answers [OQ-MT3](../design/macos-native-tls.md#OQ-MT3). Needs the fixture from [Prepare an isolated fixture](#prepare-an-isolated-fixture) and the probe from [Build the disposable probes](#build-the-disposable-probes). Render both policies as in [Render and compare Safehouse policies](#render-and-compare-safehouse-policies), then:

```zsh
shasum -a 256 "$(command -v safehouse)" "$W/safehouse-copilot.sb" "$W/safehouse-no-keychain.sb"
rg -n 'com\.apple\.(SecurityServer|securityd\.xpc|secd)|Library/Keychains' "$W/safehouse-copilot.sb"
for P in safehouse-copilot safehouse-no-keychain; do
  "$W/keychain-probe" --mode preflight --keychain "$W/fixture.keychain-db" --item synthetic-permissive
  /usr/bin/sandbox-exec -f "$W/$P.sb" -- "$W/keychain-probe" --mode spawn-child --child-mode read \
    --keychain "$W/fixture.keychain-db" --item synthetic-permissive
done
```

| Output | Expected | Decisive |
| :--- | :--- | :--- |
| `rg` over the Copilot policy | Lines granting the three service names and the home `Library/Keychains` path | No such line: the pinned renderer did not select the keychain module; stop, the row is untested |
| `safehouse-copilot` child read | `match=true`, parent and child hashes equal | `match=true` is Safehouse's documented broadened access, as yolo's `p2` was; it is not a vulnerability by itself |
| `safehouse-no-keychain` child read | Non-zero status (`errSecItemNotFound` -25300 or `errSecInteractionNotAllowed` -25308, not a sandbox error), `match=false` | `match=true`: the overlay did not take effect; record the policy hash and its rule order |

### Restrictive keychain ACL

Answers [OQ-MT4](../design/macos-native-tls.md#OQ-MT4). Same fixture and probe; the restrictive item's access list is the empty array from step 3 of the fixture.

```zsh
"$W/keychain-probe" --mode read --keychain "$W/fixture.keychain-db" --item synthetic-restricted
for P in safehouse-copilot yolo-p0-harness; do
  "$W/keychain-probe" --mode preflight --keychain "$W/fixture.keychain-db" --item synthetic-permissive
  /usr/bin/sandbox-exec -f "$W/$P.sb" -- "$W/keychain-probe" --mode spawn-child --child-mode read \
    --keychain "$W/fixture.keychain-db" --item synthetic-restricted
done
```

| Output | Expected | Decisive |
| :--- | :--- | :--- |
| Unsandboxed read | Non-zero status in the interaction or ACL category, `match=false` | `match=true`: the access list was not empty; the fixture is wrong, stop |
| `safehouse-copilot` child read | Non-zero status in the interaction or ACL category, `match=false` | `match=true` is an item-ACL bypass inside a sandbox: a demonstrated violation; report it privately |
| `yolo-p0-harness` child read | Non-zero status, `errSecItemNotFound` (-25300) or `errSecInteractionNotAllowed` (-25308), not a sandbox error | An ACL-category status: the service deny did not stop the call first |

### Current P0, hostname and URLSession negative controls

Answers [OQ-MT5](../design/macos-native-tls.md#OQ-MT5). Render the current-source harness profile first, as in [Render yolo harness P0](#render-yolo-harness-p0-preserve-the-measured-baseline-separately); the TLS probe's two modes are listed under [Build the disposable probes](#build-the-disposable-probes).

```zsh
rg -c 'com\.apple\.(SecurityServer|securityd|securityd\.xpc|secd)"' "$W/yolo-p0-harness.sb"
for I in synthetic-permissive synthetic-restricted; do
  "$W/keychain-probe" --mode preflight --keychain "$W/fixture.keychain-db" --item synthetic-permissive
  /usr/bin/sandbox-exec -f "$W/yolo-p0-harness.sb" -- "$W/keychain-probe" --mode spawn-child \
    --child-mode read --keychain "$W/fixture.keychain-db" --item "$I"
done
for H in example.com self-signed.badssl.com wrong.host.badssl.com; do
  mkdir -p "$W/chain-$H" && cd "$W/chain-$H"
  openssl s_client -showcerts -connect "$H:443" -servername "$H" </dev/null 2>/dev/null >presented.pem
  awk '/BEGIN CERTIFICATE/{n++} n{print > ("chain-" n ".pem")}' presented.pem
  for f in chain-*.pem; do openssl x509 -in "$f" -outform DER -out "${f%.pem}.der"; done
  cd "$W"
done
for S in "" "/usr/bin/sandbox-exec -f $W/yolo-p0-harness.sb --"; do
  for H in example.com self-signed.badssl.com wrong.host.badssl.com; do
    ${=S} "$W/tls-probe" --mode sectrust --chain-dir "$W/chain-$H" --host "$H"
    ${=S} "$W/tls-probe" --mode urlsession --url "https://$H/"
  done
done
```

| Output | Expected | Decisive |
| :--- | :--- | :--- |
| `rg -c` | `4` or more | Fewer: a mandatory deny is missing; stop and report, do not repair it here |
| P0 keychain reads | Sandbox-deny category and `match=false`, for both items | `match=true` for either: the current tree lost the boundary the `d159a57f0` audit measured |
| `example.com`, both modes, both columns | Trust succeeds; HTTP status 200 | A failure under P0 only: trustd evaluation is not reachable under the current profile |
| `self-signed.badssl.com`, both modes | Trust fails with a `CFError`; URLSession fails certificate verification | Success: verification is not enforced; stop |
| `wrong.host.badssl.com`, both modes | The hostname policy fails; URLSession fails certificate verification | Success: the hostname check is not enforced; stop |

An unreachable host or a changed chain is **unknown** for that row.

### MDM-installed CA under P0

Answers [OQ-MT6](../design/macos-native-tls.md#OQ-MT6). Needs a managed Mac on a network that intercepts TLS with a CA only the MDM installed; without one, the row stays untested. The workspace runs in self mode, the mode [§5 of the design](../design/macos-native-tls.md#5-witnesses-for-the-mac) measured.

```zsh
echo | openssl s_client -connect nodejs.org:443 -servername nodejs.org 2>/dev/null | openssl x509 -noout -issuer
printf '{"runtime": "macos-user"}\n' >"$W/yolo-jail.jsonc"
mkdir -p "$HOME/.config/yolo-jail" && printf '{"macos_user_mode": "self"}\n' >"$HOME/.config/yolo-jail/overrides.jsonc"   # user scope only; remove after the row
yolo check --no-build
yolo -- mise ls-remote node 2>&1 | tail -1
/usr/bin/sandbox-exec -f "$W/yolo-p0-harness.sb" -- "$W/tls-probe" --mode urlsession --url https://nodejs.org/
```

| Output | Expected | Decisive |
| :--- | :--- | :--- |
| `issuer` | Names the corporate CA | A public issuer: the network does not intercept; the row is untested |
| `mise ls-remote node` | A Node version | `bad certificate format` or an unknown-issuer error: trustd under P0 does not trust the MDM CA, and the claim in [§3 of the design](../design/macos-native-tls.md#3-the-proposal) is wrong |
| URLSession under P0 | HTTP status 200 | A trust failure: the same conclusion, without mise |

### Inherited account relaunch

Answers [OQ-MT7](../design/macos-native-tls.md#OQ-MT7), the native half of the [inherited account relaunch plan](../design/inherited-relaunch-plan.md). Account mode is the default `macos_user_mode`; the disposable account must pass the macos-user integration gate. `YOLO_SRC` is a checkout at the commit under test.

```zsh
git -C "$YOLO_SRC" rev-parse HEAD
(cd "$YOLO_SRC" && go test -count=1 -v -run '^(TestMacosUserInheritedUserScopeArrivesReadOnly|TestMacosUserOverlayInstallKeepsTheAgentStateBesideItsSkills)$' ./integration 2>&1 | rg -- '^(--- |ok|FAIL)')
mkdir -p "$HOME/.config/yolo-jail" && printf '{"packs": ["guardrails"]}\n' >"$HOME/.config/yolo-jail/config.jsonc"
printf '{"runtime": "macos-user"}\n' >"$W/yolo-jail.jsonc"
for N in 1 2; do
  yolo -- sh -c 'test -f "$HOME/.config/yolo-jail/config.toml" && echo DELIVERED' 2>&1 \
    | rg 'DELIVERED|refusing .*inherited|could overwrite existing user file'
  echo "launch $N rc=${pipestatus[1]}"
done
```

| Output | Expected | Decisive |
| :--- | :--- | :--- |
| Both integration tests | `--- PASS` | `--- SKIP` is not native success: record its reason. `--- FAIL` is a native defect |
| Launch 1 | `DELIVERED`, `rc=0` | No `DELIVERED`: the inherited scope did not arrive; the row is untested |
| Launch 2 | `DELIVERED`, `rc=0`, no `refusing` line | `could overwrite existing user file` at the config JSONC: the relaunch repair does not hold natively |

The `config.jsonc` written above is the disposable account's; remove it after the row. A scope TOML can represent (this one) arrives as `config.toml`, so the delivery probe tests that file; `config.jsonc` is only the legacy fallback for a scope TOML cannot represent. The integration test `TestMacosUserInheritedUserScopeArrivesReadOnly` probes the delivered path since `14c00e994` and passed natively at `3cad08c6a`; before that commit it probed `config.jsonc` and failed for that reason, which was a stale test and not a relaunch defect.

### Capture compatibility context

Answers [OQ-MT8](../design/macos-native-tls.md#OQ-MT8), the native half of the [capture context plan](../design/native-capture-compatibility-context-plan.md). The capture runs Copilot's vendor installer once against an empty home; no login, prompt or API request follows.

```zsh
printf '{"runtime": "macos-user", "packs": ["copilot"]}\n' >"$W/yolo-jail.jsonc"
mkdir -p "$HOME/.config/yolo-jail" && printf '{"macos_user_mode": "self"}\n' >"$HOME/.config/yolo-jail/overrides.jsonc"   # user scope only; remove after the row
yolo capture copilot >"$W/capture.log" 2>&1; echo "capture rc=$?"
rg 'invalid macos-user launch context|cannot parse .*version probe' "$W/capture.log"
yolo -- copilot --version >"$W/version.log" 2>&1; tail -3 "$W/version.log"
```

| Output | Expected | Decisive |
| :--- | :--- | :--- |
| `capture rc` | `0` | Non-zero with neither line below: a different capture failure; keep `capture.log` |
| `rg` over the capture log | Nothing | `invalid macos-user launch context`: the capture still lacks its resolved context |
| `version.log` tail | A version line, or the native TLS stop of [OQ-MT11](../design/macos-native-tls.md#OQ-MT11) | `cannot parse copilot's declared version probe`: keep the raw `version.log`, the emitter output the plan still owes |

### Claude credential view, On then Off

Answers [OQ-MT9](../design/macos-native-tls.md#OQ-MT9), the native half of the [Claude view retirement plan](../design/claude-view-retirement-plan.md). The view switch is `YOLO_CLAUDE_CREDENTIAL_VIEW` ([`claudeview.go`](../../internal/claudeview/claudeview.go)). The On launch needs a Claude login, made only in a disposable account the owner chose for that login; without one, the row is untested.

```zsh
printf '{"runtime": "macos-user", "packs": ["claude"]}\n' >"$W/yolo-jail.jsonc"
YOLO_CLAUDE_CREDENTIAL_VIEW=1 yolo -- true 2>&1 | rg -i 'claude'
yolo claude-auth status >"$W/status-on.txt" 2>&1; cat "$W/status-on.txt"
YOLO_CLAUDE_CREDENTIAL_VIEW=0 yolo -- true 2>&1 | rg -i 'claude'
yolo claude-auth status >"$W/status-off.txt" 2>&1; cat "$W/status-off.txt"
```

| Output | Expected | Decisive |
| :--- | :--- | :--- |
| On launch, then `status-on.txt` | No registration warning; the status lists this workspace's view | A registration failure: there is nothing to retire, and the row is untested |
| Off launch | `Claude: disabled this workspace's credential view; the broker will no longer maintain its view.` | `could not retire this workspace's Claude credential view registration`: retirement fails natively |
| `status-off.txt` | This workspace's view is gone; any other view is unchanged | Still listed: the Off transition does not retire natively |

### Apple Container JSON and image prune

Answers [OQ-MT10](../design/macos-native-tls.md#OQ-MT10), the native half of the [Apple Container image prune plan](../design/apple-container-image-prune-plan.md). Needs Apple Container started for the disposable account (`container system start`) with only yolo images in its store.

```zsh
container --version
container image list --format json | jq -c '.[] | {name: .configuration.name, digest: .configuration.descriptor.digest}'
container ls --all --format json | jq -c '.[] | {ref: .configuration.image.reference, digest: .configuration.image.descriptor.digest}'
YOLO_RUNTIME=container yolo prune --no-containers >"$W/prune-dry.txt" 2>&1; rg 'FAILED' "$W/prune-dry.txt"
YOLO_RUNTIME=container yolo prune --no-containers --apply >"$W/prune-apply.txt" 2>&1; rg 'FAILED' "$W/prune-apply.txt"
container image list --format json | jq -c '.[] | .configuration.name'
```

| Output | Expected | Decisive |
| :--- | :--- | :--- |
| `container --version` | 1.1.0, the release the plan read | Another release: the JSON shape is unverified for it; record the version |
| Both `jq` lines | Every row has a non-null name or reference and a `sha256:` digest | `null` fields: the plan's source-derived shape is wrong natively |
| Dry run and apply `rg` | Nothing | `FAILED — could not list this runtime's images`: the listing still declines natively |
| Final image list | Every image a workspace's current pointer or a container names is still listed | One of them gone: a deletion the retention rules forbid; report it at once |

### Copilot native TLS under the keychain deny

Answers [OQ-MT11](../design/macos-native-tls.md#OQ-MT11), tracked as [yolo-jail #65](https://github.com/mschulkind-oss/yolo-jail/issues/65). Run it in a disposable account with **no** Copilot login. The compatibility guard refuses Copilot 1.0.93 and newer before any request, so this witness needs a way past it; without one the row is untested ([Mac evidence](../design/macos-native-tls.md#mac-evidence-witness-run); the 2026-10-30 run reached Copilot only through hand-built profiles, [results](../research/copilot-keychain-boundary.md#mac-results-2026-10-30)): the request it makes is the credential-free release check the issue names. The SecurityServer deny stays.

```zsh
printf '{"runtime": "macos-user", "packs": ["copilot"]}\n' >"$W/yolo-jail.jsonc"
mkdir -p "$HOME/.config/yolo-jail" && printf '{"macos_user_mode": "self"}\n' >"$HOME/.config/yolo-jail/overrides.jsonc"   # user scope only; remove after the row
yolo internal macos-user-profile --mode self --workspace "$W" --tmp "$W/tmp" | rg -c 'com\.apple\.SecurityServer'
yolo -- copilot --version >"$W/copilot.log" 2>&1
rg 'bad certificate format|Failed to fetch latest release|^[0-9]+\.[0-9]+\.[0-9]+' "$W/copilot.log"
```

| Output | Expected | Decisive |
| :--- | :--- | :--- |
| `rg -c` | `1` or more | `0`: the deny is gone from the profile; stop and report |
| Launch, while #65 is open | `bad certificate format` for `api.github.com/repos/github/copilot-cli/releases/latest` | Copilot 1.0.93 or later with no TLS error: upstream moved this request off SecureTransport; record the version, keep the deny, and revisit #65's options |

## Record outcomes and report only what ran

Store a redacted private run log with this schema; never commit fixture values or raw secrets:

| Field | Record |
| :--- | :--- |
| Run identity | Date, macOS version/build, architecture, SDK/Xcode, current source revision and exact binary hashes |
| Process identity | UID, real `HOME`, physical workdir, caller/child executable hash and child environment; state whether a policy render or a real launch was used |
| Fixture | Disposable keychain path/hash, item class, fixed service/account labels, ACL definition/readback and whether the permissive/restrictive control behaved as expected |
| Policy | Safehouse revision, selected Copilot/keychain modules, rendered policy SHA-256, appended overlay SHA-256 and confirmed final deny list; yolo profile helper binary/revision and harness-only label |
| Operation result | Row, operation, exit status, OSStatus/domain/code, `match` boolean only, sandbox deny/prompt/ACL/missing-item/timeout/unknown category |
| TLS result | URL hostname, explicit SSL policy hostname, presented-chain fingerprint only, trust result/error code, URLSession default-verification result; no headers, cookies, auth or response body |
| Cleanup | Fixture and temp source/binary removed; no Security.framework calls used the owner UID/session, and no owner default/search-list or keychain state was read, changed or restored |

After all rows, close every Security.framework reference, then remove only the temporary source, binaries, private fixture manifest (if any), and the known disposable keychain file/sidecars under `$W`. Do not call a broad keychain delete or search-list operation. Keep categories distinct: a sandbox deny is not an ACL denial; a prompt-disabled interaction failure is not proof of an ACL decision; a missing item is not a denied secret read; a timeout or unreachable endpoint is unknown. Report a result only for the exact tested selector, ACL, policy hash and process identity. Do not generalize to every keychain item, every Safehouse configuration, Copilot's live inner sandbox, pathless session access, `secd` data-protection items or client-certificate/private-key operations.

No Mac execution or fixture operation occurred while preparing this runbook. The temporary probe sources do not yet exist: first materialize them, confirm their APIs compile against the selected Mac SDK and verify fixture behavior **inside the wholly disposable synthetic-only UID/session**, and only then run eligible controls. Do not perform any of those steps from the owner's UID/session; if the required isolation is unavailable, leave the rows untested.

## Still unexecuted

Every row is an open question in the design; its witness is the matching subsection of [Native witnesses, one per open question](#native-witnesses-one-per-open-question).

- Safehouse permissive and overlay rows under the effective Copilot policy: [OQ-MT3](../design/macos-native-tls.md#OQ-MT3).
- Restrictive ACL rows: [OQ-MT4](../design/macos-native-tls.md#OQ-MT4).
- Current-source yolo P0 harness row, and the native SSL-policy hostname and URLSession negative controls under it: [OQ-MT5](../design/macos-native-tls.md#OQ-MT5). The existing real-launch baseline is from `d159a57f0` and is not replaced by it.
- trustd trusting an MDM-only CA under P0: [OQ-MT6](../design/macos-native-tls.md#OQ-MT6).
- The native transitions now on main: inherited account relaunch, [OQ-MT7](../design/macos-native-tls.md#OQ-MT7); capture context, [OQ-MT8](../design/macos-native-tls.md#OQ-MT8); Claude view On to Off, [OQ-MT9](../design/macos-native-tls.md#OQ-MT9).
- Apple Container's emitted JSON and the prune that reads it: [OQ-MT10](../design/macos-native-tls.md#OQ-MT10).
- Copilot's native HTTP path under the unchanged deny: [OQ-MT11](../design/macos-native-tls.md#OQ-MT11). Public release assets linking several TLS stacks do not identify the active caller; the credential-free release check is the one request this witness makes.
- Any pathless session or data-protection keychain operation stays untested and has no question: no owner, default or search-list operation is authorized.

**Gates for this document:** repository-pinned Vantage style-guide read; `scripts/vantage-check.sh docs/plans/macos-native-comparison-runbook.md`; `git diff --check`; manual API/source and safety review. No Go test, nested jail, Mac test or full integration gate applies to this docs-only edit.

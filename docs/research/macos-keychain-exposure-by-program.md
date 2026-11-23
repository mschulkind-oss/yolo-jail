---
title: "Which programs would hand a keychain secret to a jail granted Safehouse's keychain access"
date: 2026-10-29
status: draft
tags: [research, macos, keychain, macos-user, seatbelt, credentials, threat-model]
summary: "Many everyday developer tools keep their login in the file-based login keychain under an access list that trusts either the tool itself or Apple's security command-line tool. A confined process that can reach the keychain service and run that tool, or the security tool, gets the secret with no prompt. The GitHub CLI and Claude Code are the clearest cases. Partition lists and the data-protection keychain narrow this exposure but do not remove it. The research supports keeping MT-D7's deny in place."
---

# Which programs would hand a keychain secret to a jail granted Safehouse's keychain access

**Status:** draft, 2026-10-29. This answers the research question
[OQ-MT12](../design/macos-native-tls.md#OQ-MT12) for the ruling
[MT-D7](../design/macos-native-tls.md#decision-ledger). It is desk research only. No keychain
was touched, nobody logged in, and nothing was measured. Each row cites the vendor's source,
documentation or issue tracker. A row the sources do not settle is marked *unconfirmed*.

**Scope.** This document classifies exposure for a design decision. It records whether a
program's secret is readable and why. On purpose, it gives no commands or steps for reading
any secret.

## Terms

- **File-based keychain.** The older keychain kind, which keeps items in a file such as
  `~/Library/Keychains/login.keychain-db`. Its access control is a per-item *access list*
  ([Apple TN3137](https://developer.apple.com/documentation/technotes/tn3137-on-mac-keychains)).
- **Access list, or trusted-application list.** The list of programs that may read an item's
  secret without a dialog. Apple's
  [`SecAccessCreate`](https://developer.apple.com/documentation/security/secaccesscreate(_:_:_:))
  states that a nil list "contains only the calling app". So an item a program creates through
  the API without naming an access list trusts *that program*.
- **Designated requirement.** The rule in a program's code signature that the keychain uses to
  decide whether a program is "the same app" as the one on the access list. For an unsigned
  program, the keychain matches its contents (a hash) instead. This term comes from Apple's code
  signing. The sentence about unsigned programs is from MT-D7, not from an Apple source.
- **Partition list.** An extra part of the access list, added in macOS Sierra, that "limits
  access to the item based on an application's code signature"
  ([`security(1)`](https://www.manpagez.com/man/1/security/osx-10.12.3.php)). Its entries are
  `apple:`, `apple-tool:`, `teamid:<id>` or `cdhash:<hash>`
  ([Cody Thomas, OBTS v5](https://objectivebythesea.org/v5/talks/OBTS_v5_cThomas.pdf);
  [openradar 28524119](https://openradar.appspot.com/28524119)).
- **Data-protection keychain.** The newer keychain, shared with iOS. Its access control is
  *keychain access groups* taken from the program's signed entitlements
  ([TN3137](https://developer.apple.com/documentation/technotes/tn3137-on-mac-keychains)).
- **`security`.** Apple's `/usr/bin/security` command-line tool. Many programs shell out to it
  instead of linking against Security.framework. An item created that way trusts `security`.
- **"Readable by the jail".** This document uses the phrase to mean: a process confined by a
  Safehouse-style profile, which reaches the keychain Mach services and the keychain files, can
  get the secret with no dialog. It can do so by running the program, a downloaded copy of the
  program, or `security`. The
  [2026-10-29 Mac run](../design/macos-native-tls.md#mac-evidence-witness-run) measured that
  the access list is still enforced under that profile. So "readable" here always means "the
  access list already trusts something the jail can run".

## In short

Yes. Several common developer tools are exposed. They split into three groups.

1. **Trusts `security`, so it is readable by anything that can run `/usr/bin/security`.** This
   group covers the GitHub CLI `gh`, and every Go program built on `zalando/go-keyring`
   ([go-keyring source](https://github.com/zalando/go-keyring/blob/master/keyring_darwin.go)).
   It also covers Claude Code: its maintainers' tracker records that the `Claude Code-credentials`
   item's access list "contains only `/usr/bin/security` itself", so any same-user process reads
   it silently ([anthropics/claude-code#103019](https://github.com/anthropics/claude-code/issues/103019)).
   This is the worst group. `security` is on every Mac, and the jail does not need to fetch or
   run the vendor's program at all.
2. **Trusts the program itself, and the program prints its secret on request.** This covers
   `git-credential-osxkeychain`, `docker-credential-osxkeychain`, Copilot CLI, Python `keyring`
   users such as `twine`, `aws-vault` and Granted. A jail that can run the same program, or a copy
   whose designated requirement matches, gets the secret.
3. **Not in the login keychain, so the keychain grant is irrelevant.** This covers the AWS CLI's
   SSO cache, `gcloud`, the Azure CLI on macOS, npm, and Codex CLI's default mode. Each keeps
   plain files instead, which the jail's *file* rules decide.

Partition lists do not change this, because they check the signature of the calling *program*
and not the containment it runs in. The data-protection keychain does narrow it: an unsigned
command-line tool cannot use it at all. It does not help when the entitled program itself will
print the secret.

## Table

"Readable?" means readable by the jail, as defined in [Terms](#terms).

| Program | Keychain kind | Access list (trusted program) | Readable? | Source |
| :--- | :--- | :--- | :--- | :--- |
| **gh** (GitHub CLI) | file-based (login) | `/usr/bin/security`: go-keyring creates the item through `security`, with no `-T` or `-A` flag | **Yes**, through `security` or `gh` itself. Item `gh:github.com` | [go-keyring `keyring_darwin.go`](https://github.com/zalando/go-keyring/blob/master/keyring_darwin.go); [cli/cli#13330](https://github.com/cli/cli/issues/13330) |
| **Claude Code** | file-based (login) | `/usr/bin/security` only | **Yes.** Reported as a silent read by any same-user process | [anthropics/claude-code#103019](https://github.com/anthropics/claude-code/issues/103019); [#81707](https://github.com/anthropics/claude-code/issues/81707); [keychain-from-a-jail E10](../design/keychain-from-a-jail.md#8-decision-ledger) |
| Any Go tool on **zalando/go-keyring** (for example kubelogin with keyring cache) | file-based | `/usr/bin/security` | **Yes** | [go-keyring](https://github.com/zalando/go-keyring/blob/master/keyring_darwin.go); kubelogin's keyring option: [int128/kubelogin](https://github.com/int128/kubelogin). That kubelogin uses go-keyring is **unconfirmed** |
| **git-credential-osxkeychain** | file-based (`SecItemAdd` with no data-protection flag) | the helper itself (no access list set, so the calling program) | **Yes**, through the helper, whose `get` writes the password to stdout | [git `git-credential-osxkeychain.c`](https://github.com/git/git/blob/master/contrib/credential/osxkeychain/git-credential-osxkeychain.c); [`SecAccessCreate`](https://developer.apple.com/documentation/security/secaccesscreate(_:_:_:)) |
| **docker-credential-osxkeychain** / Docker Desktop | file-based (internet password, label "Docker Credentials") | the helper itself (no access list set) | **Yes**, through the helper's `get`. Docker Desktop writes `credsStore: desktop`, a pass-through helper. Its storage is **unconfirmed** | [docker-credential-helpers `osxkeychain.go`](https://github.com/docker/docker-credential-helpers/blob/master/osxkeychain/osxkeychain.go); [docker/for-mac#6295](https://github.com/docker/for-mac/issues/6295) |
| **GitHub Copilot CLI** | file-based (`SecKeychainAddGenericPassword` via Rust `apple-native-keyring-store`) | the Copilot executable (calling program) | **Likely**, through Copilot itself. Whether Copilot prints the token is **unconfirmed** | [copilot-token-storage §3.1](copilot-token-storage.md#31-keychain-first) |
| **Codex CLI** | `file` by default (`~/.codex/auth.json`). With `keyring`, the keychain through the Rust `keyring` crate | Keyring mode: the Codex executable. Codex strips `DYLD_` variables and denies debugger attach | Default: not a keychain question. Keyring mode: **unconfirmed** | [OpenAI auth docs](https://learn.chatgpt.com/docs/auth); [Safehouse Codex report](https://agent-safehouse.dev/docs/agent-investigations/codex) |
| **Python `keyring`** (twine and pip users) | file-based (`SecItemAdd` with no data-protection flag and no access list) | the Python interpreter that created the item | **Yes**, through any program the same interpreter runs, which includes `keyring`'s own command line | [jaraco/keyring `macOS/api.py`](https://github.com/jaraco/keyring/blob/main/keyring/backends/macOS/api.py) |
| **aws-vault** | file-based, in a *separate* `aws-vault` keychain | the calling program: `KeychainTrustApplication: true`, a nil list | **Only while that keychain is unlocked.** It auto-locks (15 min by default) and asks for its own password. While unlocked, readable through `aws-vault` | [aws-vault `cli/global.go`](https://github.com/99designs/aws-vault/blob/master/cli/global.go); [99designs/keyring `keychain.go`](https://github.com/99designs/keyring/blob/master/keychain.go); [lock timeout](https://tatusl.dev/posts/aws-vault) |
| **Granted / assume** | file-based, login keychain (entries starting `granted`) | `granted` (with `assumego` symlinked "so that they can both share keychain items without additional prompts") | **Likely**, through `granted`. The exact access list is **unconfirmed** | [Granted troubleshooting](https://docs.commonfate.io/granted/troubleshooting); [getting started](https://docs.commonfate.io/granted/getting-started) |
| **AWS CLI v2** SSO | none: plain JSON in `~/.aws/sso/cache` | — | Not a keychain question. A file rule decides it | [dev.to, AWS builders](https://dev.to/aws-builders/using-aws-identity-center-sso-tokens-to-script-across-multiple-accounts-352l) |
| **gcloud** | none: SQLite `credentials.db` and `access_tokens.db` in `~/.config/gcloud` | — | Not a keychain question | [Netskope](https://www.netskope.com/blog/gcp-oauth-token-hijacking-in-google-cloud-part-1); [jpassing.com](https://jpassing.com/2020/01/14/google-application-default-credentials-vs-your-personal-gcloud-credentials) |
| **Azure CLI** (MSAL) | none by default: plain `~/.azure/msal_token_cache.json`. Keychain encryption is an opt-in setting, off by default | — | Not a keychain question by default | [Microsoft Learn](https://learn.microsoft.com/en-us/cli/azure/msal-based-azure-cli?view=azure-cli-latest); [Azure/azure-cli#27176](https://github.com/Azure/azure-cli/issues/27176) |
| **npm / yarn** tokens | none: `_authToken` in `.npmrc` | — | Not a keychain question | [npm `.npmrc` docs](https://docs.npmjs.com/cli/v11/configuring-npm/npmrc) |
| **1Password CLI `op`** | not the keychain. The desktop app authorizes it | — | Not a keychain question. The app's own session authorization decides it. **Unconfirmed** in detail | [1Password app integration](https://www.1password.dev/cli/app-integration) |
| **VS Code** (`SecretStorage`) | file-based: an encryption key `Code Safe Storage`. The secrets are encrypted in `state.vscdb` | VS Code (Electron app). Signed with hardened runtime: **unconfirmed** | Key: only through VS Code itself. A [ControlPlane proof of concept](https://control-plane.io/posts/abusing-vscode-from-malicious-extensions-to-stolen-credentials-part-2) shows that any extension *inside* VS Code reads every secret | [vscode-discussions#748](https://github.com/microsoft/vscode-discussions/discussions/748); [Electron `safeStorage`](https://electronjs.org/docs/latest/api/safe-storage) |
| **Chrome / Electron "Safe Storage"** | file-based (`<app> Safe Storage`) | the browser or app | Through the app only. Chrome 136 stopped honoring remote debugging on the default profile, which closed one route that used the browser itself | [electron/electron#34614](https://github.com/electron/electron/issues/34614); [Chrome blog](https://developer.chrome.com/blog/remote-debugging-port) |
| **ssh-add `--apple-use-keychain`** | the passphrase goes to "Local Items", the data-protection keychain, and `security` cannot see it | Apple's `/usr/bin/ssh-add` and `ssh` | `security`: **no**. Running Apple's `ssh`/`ssh-add` uses the stored passphrase without a prompt, so a jail that can read `~/.ssh` and run them gets the *use* of the key (INFERRED) | [netmeister.org](https://www.netmeister.org/blog/sshkeys-macos-op.html); [GitHub Docs](https://docs.github.com/en/authentication/troubleshooting-ssh/error-ssh-add-illegal-option----apple-use-keychain) |
| **Tailscale** (GUI variants) | the keychain for node state (the App Store variant always does). Node-key sealing is on by default | the Tailscale app or extension | **Unconfirmed**. No source shows a command-line path that prints the node key | [App Store notes](https://apps.apple.com/us/app/tailscale/id1475387142); [macOS variants](https://tailscale.com/docs/concepts/macos-variants) |
| **Homebrew tools in general** | depends on the library: go-keyring means `security`, the SecItem API means the tool itself | — | The library decides, as above | — |

## The three answers

### 1. Do partition lists stop a same-signer or `security`-trusted read from a sandboxed process?

No. A partition list is checked against the code signature of the program making the call:
`apple:`, `apple-tool:`, `teamid:` or `cdhash:`
([`security(1)`](https://www.manpagez.com/man/1/security/osx-10.12.3.php);
[OBTS v5](https://objectivebythesea.org/v5/talks/OBTS_v5_cThomas.pdf)). A Seatbelt profile
changes neither the program's signature nor its team ID. So the program, or `security` (which is
in the `apple-tool:` partition, per the
[openradar](https://openradar.appspot.com/28524119) thread), passes the partition check under
confinement exactly as it does outside. The same talk records that an *older* signed build of an
app, run from another path, still matches both the access list's requirement and the partition
([OBTS v5](https://objectivebythesea.org/v5/talks/OBTS_v5_cThomas.pdf)). "Download a copy" is
therefore a real route for a signed program, and partitions do not close it.

### 2. Does the data-protection keychain protect against this, and can unsigned command-line code use it?

Partly, and no. Data-protection access groups come from the program's entitlements, and those
entitlements "must be authorized by a provisioning profile", which needs "an app-like bundle
structure", a structure that is "not [standard] for command-line tools"
([TN3137](https://developer.apple.com/documentation/technotes/tn3137-on-mac-keychains)). So
unsigned or ad-hoc-signed command-line code cannot use it. Neither can `security`, which does not
see Local Items ([netmeister.org](https://www.netmeister.org/blog/sshkeys-macos-op.html)). That
closes route 1 above. It does not close route 2: if the entitled program will use or print the
secret when the jail runs it (the `ssh-add` row), the access group does not help. Library code
gets "the entitlements of the host process's main executable"
([TN3137](https://developer.apple.com/documentation/technotes/tn3137-on-mac-keychains)). This is
also why most command-line tools in the table use the file-based keychain.

### 3. Does securityd refuse a call from a non-hardened trusted program launched with `DYLD_INSERT_LIBRARIES`?

No, per the published research. A program without the hardened runtime that has an item on its
access list keeps that trust when code is added to it at launch, and an older non-hardened build
of a now-hardened program is still accepted, because the keychain checks the requirement and not
the version ([Wojciech Reguła](https://wojciechregula.blog/post/stealing-macos-apps-keychain-entries);
[OBTS v5](https://objectivebythesea.org/v5/talks/OBTS_v5_cThomas.pdf)). The hardened runtime is
what blocks this route. `/usr/bin/security` is an Apple platform binary and needs no such step,
because it already prints what it is trusted for.

## What this means for MT-D7

Facts only, to feed the ruling. Route 1, items that trust `security`, is reachable with no
download and no added code. It covers at least the GitHub login and the Claude login on a
typical developer Mac
([go-keyring](https://github.com/zalando/go-keyring/blob/master/keyring_darwin.go);
[anthropics/claude-code#103019](https://github.com/anthropics/claude-code/issues/103019)).
Route 2 covers git, Docker and Python-keyring credentials (table rows above). Neither route needs
an access-list bypass, which matches the
[2026-10-29 Mac run](../design/macos-native-tls.md#mac-evidence-witness-run): the access list
held, and the exposure comes from what the access list trusts.

## Not confirmed

- Whether Copilot CLI or Codex CLI (keyring mode) have a command that prints the stored token.
- The exact access list Granted's items get, and whether it uses go-keyring or 99designs/keyring.
- Whether kubelogin's keyring cache uses go-keyring, which would mean it trusts `security`.
- How Docker Desktop's `docker-credential-desktop` stores credentials.
- Whether VS Code, Copilot, granted and gh ship with the hardened runtime. This decides whether
  the step-3 route applies to their current builds.
- Tailscale: whether any path returns node state to a caller other than its own extension.
- 1Password `op`: how its desktop-app authorization scopes a session, and whether a confined
  process in an authorized terminal inherits it.
- Partition lists of items created through the SecItem API by a non-Apple-signed or unsigned tool
  (`cdhash:` vs `teamid:` vs `unsigned:`). The sources here describe the entries but do not tie
  each tool to one.

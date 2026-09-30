---
title: "Where GitHub Copilot CLI keeps its login, and why one manifest line cannot share it"
date: 2026-09-29
status: in-review
stage: DESIGN
next: "Rule OQ-CT1 together with the keychain design: accepting that design answers it (the keychain route first, the copilotTokens copy only where the route is off)"
depends-on:
  - ../design/keychain-from-a-jail.md
tags: [copilot, authentication, credentials, packs, research, measured]
summary: "Copilot CLI 1.0.89 stores its GitHub token in the system keychain first, on Linux and macOS alike. Only when that fails, and only after the user says yes to a prompt, does it write the token in plain text into ~/.copilot/config.json, a file that also holds folder trust and plugin state. So there is no credential file for the shared_credentials hook to link, no directory for the shared_directory hook to share, and the copilot pack was left unchanged."
vantage:
  status-chip: true
---

# Where GitHub Copilot CLI keeps its login

**Status:** 2026-09-29 — measured from the shipped package bytes. Copilot was never run: no
login, no `--version`, no API call. One ruling is owed, [OQ-CT1](#OQ-CT1). The maintainer's
direction on it asked for a keychain design first, and that design,
[`keychain-from-a-jail.md`](../design/keychain-from-a-jail.md), came back on 2026-09-29: accepting
it answers this question.

**Needs your ruling:** [OQ-CT1](#OQ-CT1), together with the keychain design.

**The ask:** the backlog item "Copilot logs in once per machine, not once per workspace", which is
the `copilot` half of gap [G16](../plans/setup-support-gaps.md#2-ranked-gap-backlog). The
`claude` and `agy` packs already work this way. Each declares a `scope: machine` state directory
and a `shared_credentials` hook that turns the agent's credential file into a symlink into that
directory ([Shared credentials](../reference/jail-home.md#shared-credentials)). G16 sized the
`copilot` half as those two declarations, pending one fact: whether Copilot keeps its login in a
single file. The backlog item itself named the directory form of the same mechanism, a
`shared_directory` hook as `packs/pi` declares it
([Shared directories](../reference/jail-home.md#shared-directories)), which needs the matching
fact: a directory that holds the login and nothing else. The measurement below says Copilot has
neither.

---

## 1. The short answer

- **The keychain comes first, on both operating systems.** On Linux that is the
  [Secret Service](https://specifications.freedesktop.org/secret-service/latest/), a standard
  password-store API reached over the [D-Bus](https://www.freedesktop.org/wiki/Software/dbus/)
  session bus. On macOS it is the user's login keychain, through Apple's
  [Keychain Services](https://developer.apple.com/documentation/security/keychain_services) API.
- **A file is written only as a fallback.** When the keychain fails, the login ends in a state
  the code calls `needs-plaintext-consent` and asks: *"System keychain unavailable. Store token in
  plaintext config file? (y/N)"*. If the user says no, the token is not saved at all. The
  `storeTokenPlaintext` setting skips the keychain entirely.
- **The fallback file is not a credential file.** It is `~/.copilot/config.json`, Copilot's own
  "managed automatically" state file, where the token is one key (`copilotTokens`). The same file
  holds the folder-trust list, the installed-plugin registry, marketplaces, recent models and
  first-launch markers.
- **So the pack was not changed.** Linking `config.json` would move all of that state to the
  machine tier with the token. On macOS it could also report a successful link while the token
  sits in the keychain. No subdirectory of `~/.copilot` holds the login alone, so a
  `shared_directory` hook has nothing narrower to share either.
  [§5](#5-why-the-hook-was-not-shipped) gives the costs, and [§6](#6-the-options) the three
  routes that remain.

## 2. What was read

The `copilot` pack installs the npm package `@github/copilot`, which is evergreen. The latest
published version on 2026-09-29 was **1.0.89**. In 1.0.89 that package is only a loader
(`npm-loader.js`). It became one at 1.0.64: `npm view` gives 1.0.63's package as about 505 MB
unpacked and 1.0.64's as about 13 KB. The program itself ships in a per-platform package as a
[Node single executable application](https://nodejs.org/api/single-executable-applications.html),
a Node binary with one appended blob, found by its `NODE_SEA_BLOB` marker. Here the blob carries
one asset, `copilot.tgz`, which unpacks to the JavaScript bundle `app.js` and a Rust native module
`prebuilds/<platform>/runtime.node`. Authentication lives in that native module: its embedded source
paths include `src/runtime/src/auth/token_store.rs` and `src/runtime/src/auth/credentials.rs`.

| Artifact | SHA-256 |
|---|---|
| `@github/copilot-linux-x64@1.0.89` tarball | `b1cf735a62e246965f306ae27a3c7b501e6436c3e70375dd5bebbf8fdc438c65` |
| `@github/copilot-darwin-arm64@1.0.89` tarball | `7292cd2b3f0275e90bed9306bee23c5b7b1e22d604d8ea121f67c24f7192ad49` |
| `app.js`, byte-identical on both platforms | `7b87966eb665ebb7fc5fdfb4e6069dde8c7f668e212b4ad6962bbdb1a5c89113` |
| `runtime.node`, linux-x64 | `b2c718203574d42589e0bf438524df4b7766243cf66d097a7fc7b2e16e8db12e` |
| `runtime.node`, darwin-arm64 | `19a6664b9849b07081cb53adfb3b5c6184304d088decf64653cb3b89da9fe57d` |
| `@github/copilot@1.0.48` `app.js`, installed in this jail | `4643c8fe2dc55bb3a988a16001cf9b3d2b70fdbdaec5b7d567102fa7066327db` |
| `@github/copilot@1.0.60` `app.js` | `31f867a4f5dba13aa4794e33f61ed888b9e488bf41268ab60c954ecfe315c1cb` |
| `@github/copilot@1.0.63` `app.js` | `4e546b0f9bd7fefaf70613fa6ab6fa970bef0690694a0e6d7a80791b8a0abc03` |
| `@github/copilot-linux-x64@1.0.64` `app.js`, from its appended blob | `23381e064bbf123f9493fc6b2f7a77ba16e329fff811fa120fa977a752b3420f` |
| `@github/copilot-linux-x64@1.0.64` `runtime.node`, from its appended blob | `ff0c2eff3c5263ec2a1c461df0e6c81ef1557784e8fd4a22512d519676640f88` |

The native module is compiled, so for 1.0.89 the evidence is its strings: crate names, messages
and key names, with byte offsets below. The *logic* can be read only where the store is still
JavaScript, and it stopped being JavaScript at 1.0.64. That release's `app.js` hands every store
call to the native module (`tokenStoreGetToken`, `tokenStoreStoreToken` and four more, offsets
635214–636077), whose linux-x64 strings now include `src/runtime/src/token_store.rs` (36477175) and the
`keyring-3.6.3` crate (35533494). Every JavaScript version read here has the same store, function
for function: 1.0.48, the copy installed in this jail, whose offsets [§3](#3-the-store-step-by-step) cites; 1.0.60 (`app.js`
7190530–7192119); and 1.0.63, the release before the move (4802155–4803748). 1.0.89 keeps every
name and message that logic uses, but whether its compiled logic still matches is inferred, not
read.

## 3. The store, step by step

### 3.1 Keychain first

| Evidence | linux-x64 `runtime.node` | darwin-arm64 `runtime.node` |
|---|---|---|
| Store abstraction, `keyring-core-1.0.0` | offset 6722091 | offset 72911340 |
| Platform store | `zbus-secret-service-keyring-store-1.0.1` (Secret Service over D-Bus), 6692450 | `apple-native-keyring-store-1.0.2/src/keychain.rs` (legacy keychain), 72677520, calling `SecKeychainAddGenericPassword`, 78108182 |
| Failure messages | `no secret service provider or dbus session found`, 10428444; `Keychain unavailable while locating credential`, 7715331; `Keytar disabled: system keychain unreachable`, 10221145 | the last two, at 63177080 and 65655234 |

In 1.0.48 the store is function `aWs` (`app.js` offset 7185930). It saves the token under service
`copilot-cli`, account `<host>:<login>`. When the keychain read throws or finds nothing, it reads
`copilotTokens` from `config.json` instead. When the keychain write throws, it returns `false`,
which is what leads to the consent prompt.

### 3.2 When the keychain fails: a prompt, then plain text

- `copilot login` (1.0.89 `app.js` 7341164–7344703). On `needs-plaintext-consent` it asks
  *"System keychain unavailable. Store token in plaintext config file? (y/N)"*. A "no" prints
  *"Login succeeded, but the token was not saved. Install a system keychain or rerun login and
  accept plaintext storage."* (7342161).
- The interactive `/login` screen offers *"Yes, store in plain text (insecure)"* (5238044).
- The native side holds `needs-plaintext-consent` and `No login is awaiting plaintext consent`
  (linux 14719478 and 14718061; darwin 70165964 and 70164497).
- The plain-text write. In 1.0.48, `storeCurrentTokenInConfig` runs
  `ho.writeKey("copilotTokens", …)` (7187421), where `ho` is the `config` file.
  In 1.0.89 the key name `copilotTokens` is still present (linux 7984146, darwin 63451657),
  along with the header Copilot writes at the top of `config.json`:
  `// User settings belong in settings.json.` / `// This file is managed automatically.`
  (linux 14728840, darwin 70175946). yolo's own tests quote an older on-disk spelling,
  `copilot_tokens`. 1.0.48 reads either spelling and writes `copilotTokens`.

### 3.3 `storeTokenPlaintext`

This is a user setting, described as *"Store auth token in plaintext (less secure)."* (linux
7264609). Since 1.0.35 it lives in `~/.copilot/settings.json`. The runtime still reads a legacy
copy in `config.json`, and that copy wins. In 1.0.48, when the setting is true, both read and write
go to `copilotTokens` without trying the keychain (7186411). The 1.0.89 runtime reads the setting
(linux 7682985, darwin 63144240). Whether it still skips the keychain is inferred from 1.0.48, not
read.

### 3.4 Tokens from the environment, and from the GitHub CLI

`copilot login` reads `COPILOT_GITHUB_TOKEN`, `GH_TOKEN` and `GITHUB_TOKEN` in that order (1.0.89
`app.js` 7341164), and the 0.0.354 changelog entry says `COPILOT_GITHUB_TOKEN` takes precedence over
`GH_TOKEN`. A token found there is used instead of the stored login: when the model list cannot be
fetched, the native module's advice is to check the variable *"or unset it to use your Copilot CLI
login"* (linux 7177923). Its error text also names the kind of token that works: *"If using a Fine-Grained PAT,
ensure it has the 'Copilot Requests' permission enabled"* (linux 7174794). A classic `ghp_` token
does not work; since 1.0.5 Copilot says so instead of exiting silently.

One more source is the [GitHub CLI](https://cli.github.com/), `gh`. Copilot runs
`gh auth token --hostname <host>` and uses the token it prints. In 1.0.48 that is
`tryGhCliTokenLogin` (`app.js` 7193335), the last entry in the list of login methods Copilot tries
in order (7196371), after the environment variables and the stored login; its helper runs `gh`
with those arguments and skips a classic `ghp_` token (4400027). 1.0.89 keeps the route, in the native
module: `` `gh auth token` process started `` (linux 7699244), `` `gh auth token` exited non-zero ``
(14727911), `Failed to fetch GitHub CLI user login` (14720271) and a `gh-cli` login type (14727435;
`app.js` 6279927). When no login is found, its message lists *"Run 'gh auth login' to authenticate
with the GitHub CLI"* as one way to log in (`app.js` 4941107). So a `gh auth login` also logs Copilot in,
wherever the two run side by side. [§6](#6-the-options) says why that is not a machine-wide route.

### 3.5 What else lives in `config.json`

The 1.0.48 schema (`app.js` 688898–689024) and the 1.0.89 callers of the global-state writer, which
writes one key of `config.json` at a time, list these keys next to the token:
`lastLoggedInUser`, `loggedInUsers`, `trustedFolders`, `installedPlugins`, `marketplaces`,
`firstLaunchAt`, `askedSetupTerminals`, `recentModelIds`, `sandboxOnboardingShown`,
`appTipShown` and `appInstallNudgeResponded`. The yolo `copilot` pack also renders this file (the
`copilot/config` surface, read-modify-write, default `"yolo": true` and a status line).

## 4. What that means on each setup

| Setup | Binary that runs | Keychain reachable? | Where a persisted login lands |
|---|---|---|---|
| `podman` / Linux, `podman` / macOS, `container` / macOS | Linux: every jail is a Linux container | **No.** A jail has no D-Bus session bus: this jail has no `DBUS_SESSION_BUS_ADDRESS`, no `XDG_RUNTIME_DIR` and no `dbus-daemon` | `copilotTokens` in `~/.copilot/config.json`, and only if the user answers yes to the prompt |
| `macos-user` / macOS | darwin, run as the machine's one hidden sandbox account | **Unmeasured.** The Seatbelt profile allows by default and leaves the account's own home writable, so nothing in it denies the login keychain. Whether that account has a usable default keychain when started without a login session is the open fact | the account's keychain if it works, which would already be machine-wide: every workspace runs as that one account, and yolo links no part of `~/Library` per workspace. Otherwise `config.json`, as above |

## 5. Why the hook was not shipped

A `shared_credentials` hook with `from: ".copilot/config.json"` is about ten declarative lines, and
it would pass every existing test. Here is what it would actually do:

1. **It shares everything in the file, not just the login.** Every container jail sees its
   project at `/workspace`, so one `trustedFolders` entry would trust every repository on the
   machine. `installedPlugins` would list plugins in workspaces whose per-workspace
   `~/.copilot/installed-plugins` does not hold them.
2. **The first link throws state away.** The hook's rule is that the shared side always wins
   ([Shared credentials](../reference/jail-home.md#shared-credentials)). For a credential file,
   that costs one duplicate login. Here, the first workspace to boot would set the machine file,
   and every other workspace's `config.json` would be discarded the first time it linked: its
   login, its trust answers and its plugin registry.
3. **Writes are serialized within one Copilot process, not across processes.** 1.0.48 writes
   `config.json` with `writeFile` under an in-memory mutex (`y1e.runExclusive`, 683171) and no file
   lock. Two jails writing one shared file can lose each other's keys, the same shape as sharing
   Claude's credential without its lock
   ([Why a broker exists](../reference/claude-oauth-interposition.md#why-a-broker-exists)).
   Whether 1.0.89's `persistence_write_raw_json_locked` locks across processes is unmeasured.
4. **Whether the link survives a write is unmeasured for 1.0.89.** 1.0.48 writes through the
   symlink. A writer that replaces the file by rename would turn the link back into a plain file
   on the first write (`firstLaunchAt`, on first launch), and sharing would stop without a sound.
5. **On `macos-user` it can report success while sharing nothing.** If the keychain works there,
   the token never reaches `config.json`. The boot log would still record a linked file.

### The directory hook, `shared_directory`, fits worse

The backlog item named this hook, as `packs/pi` declares it: pi's extension package store,
`~/.pi/agent/npm`, becomes a link to a machine-scope directory
([Shared directories](../reference/jail-home.md#shared-directories)). That needs a directory which
holds the login and nothing else, and Copilot has none. The token is either in the keychain or one
key of the top-level `config.json` ([§3](#3-the-store-step-by-step)), so a `shared_directory`
hook on any subdirectory of `~/.copilot` shares no login at all. On `~/.copilot` itself it shares
all of Copilot's state. 1.0.89 names that state in the code that migrates an
[XDG](https://specifications.freedesktop.org/basedir-spec/latest/)-located copy of the directory
into `~/.copilot`, moving these entries one by one (`app.js` 7304385 and 7304499): `session-state`,
`session-store.db`, `command-history-state` and `installed-plugins`, then `config.json`,
`mcp-config`, `lsp-config`, `permissions-config`, `copilot-instructions.md`, `mcp-oauth-config`
and `hooks`. The `copilot` pack renders three of those files per workspace (`config.json`,
`mcp-config.json` and `lsp-config.json`) and stages the briefing and the skills into the same
directory. Every cost in the list above would then apply to the whole directory, and every
repository's sessions and command history would be shared along with the login.

## 6. The options

The `gh` source ([§3.4](#34-tokens-from-the-environment-and-from-the-github-cli)) is not a route to
a machine-wide login. The host's `gh` login never reaches a jail
([the credential boundary](../reference/agent-credentials.md#the-credential-boundary)), so `gh`
has to log in inside the jail too. There it is per-workspace like Copilot's own: with no keychain
reachable, `gh auth login` falls back to a plain-text file (its `--help` says so), and that file is
under `~/.config`, which is a per-workspace overlay on the container backends
([the mount stack](../reference/jail-home.md#the-mount-stack), tier 2) and a link into the
workspace's sidecar on `macos-user`
([the layout](../reference/macos-user-home-tiers.md#the-layout-what-is-a-symlink-what-is-a-mirror)).
Whether `gh` reaches the keychain on `macos-user` is the same open fact as Copilot's
([§4](#4-what-that-means-on-each-setup)).

| Route | What changes | What it costs |
|---|---|---|
| **A. Force plain text and share `config.json`** | The pack sets `storeTokenPlaintext: true` and adds the `scope: machine` state plus a `shared_credentials` hook on `.copilot/config.json` | Everything in [§5](#5-why-the-hook-was-not-shipped) except item 5. The token is stored in plain text on `macos-user` too, even where a keychain might have worked. It also relies on two unmeasured behaviors of 1.0.89. |
| **B. A token from `env_sources`** | Nothing in yolo. The user puts `COPILOT_GITHUB_TOKEN=github_pat_…` (fine-grained, with the Copilot Requests permission) in a dotenv file listed under user-scope `env_sources` | The user creates and renews the token. Like every `env_sources` key, it is in the environment of every jail, and whether Copilot hides it from the shells it spawns is unmeasured. Use `COPILOT_GITHUB_TOKEN`, never `GH_TOKEN` or `GITHUB_TOKEN`: `gh` reads those two, so a token there would also log `gh` in, and one with more than the Copilot Requests permission would give every jail GitHub API and git access beyond Copilot. No host GitHub credential reaches a jail otherwise ([the credential boundary](../reference/agent-credentials.md#the-credential-boundary)), so the token's permissions are all that this route adds to every jail. Whether every Copilot feature works with this kind of token is unmeasured. |
| **C. Keep one login per workspace** | Nothing. This is how it works today | One `copilot login` per repository, and on container setups the user must also answer yes to plain-text storage, or the login is not saved. |

1. 💬 **OQ-CT1: Which route makes Copilot's login machine-wide?** A shares state that was never
   meant to be shared. B shares exactly one token, but asks the user to create a token.

   <!-- vantage: oq id=OQ-CT1 leaning="B, documented in the user guide as the way to log Copilot in once per machine, with C as the default; not A, which shares folder trust and plugin state across repositories and depends on two unmeasured write behaviors." -->

   _Leaning:_ B, as a documented user-guide recipe, with C left as the default. Not A.

   **Answer:**
   > **Directed 2026-09-29, not yet ruled.** The maintainer: *"31D is reasonable, although we should
   > maybe disclose this somehow. Is there no chance that we can involve the system keychain? It
   > seems like they're taking the more responsible path for credential storage. We shouldn't just
   > steamroll over that. We should work with it. I would much prefer to just handle this
   > correctly."* A fourth route is on the table (D: yolo copies only the `copilotTokens` entry into
   > each workspace's `config.json`, disclosed), but the preferred direction is to make the system
   > keychain reachable from the jail so Copilot's own keychain-first path works. A design for that
   > comes back before the ruling.

## 7. What is still unmeasured

Three facts could not be measured here. Each changes the costs above, not the conclusion:

- whether a `macos-user` launch can reach the sandbox account's login keychain;
- whether Copilot 1.0.89 writes `config.json` through a symlink or replaces it by rename (a Linux
  jail can answer this too, but only by running Copilot, which this measurement did not do);
- whether 1.0.89 serializes `config.json` writes across processes.

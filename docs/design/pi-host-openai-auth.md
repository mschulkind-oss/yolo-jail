---
title: "Why host Pi cannot select OpenAI Codex — and how to seed its auth"
date: 2026-09-30
status: in-review
stage: DESIGN
next: "Rule OQ-3, whose login host pi uses when the user has their own, and OQ-2; OQ-1 was decided as D2 on its leaning and built, open to revision"
tags: [pi, host, openai-auth, credentials, model-picker]
summary: "Pi gates a provider's models on a stored credential or a configured key. Inside a jail yolo seeds ~/.pi/agent/auth.json before launch; yolo host withholds that write under NC-D37, so openai-codex was unconfigured at the host notch although the broker socket and the extension are live. Pi also accepts a provider marked configured with no stored credential, which serves the host without writing the user's file: OQ-1 was decided that way (D2) on its leaning on 2026-10-04 and built, and OQ-2 and OQ-3 stay open."
---

# Why host Pi cannot select OpenAI Codex — and how to seed its auth

**Status:** 2026-10-04. [OQ-1](#OQ-1) decided as D2 on its leaning under the maintainer's delegation of that
day, open to his revision, and built ([§5.1](#51-as-built-the-native-registration)); a session of host pi has not been
run on it yet. [OQ-2](#OQ-2) and [OQ-3](#OQ-3) are open. Diagnosis measured live on the maintainer's host
2026-09-30, then re-verified against pi 0.99.2 and the tree at `2f579bb9` with offline probes of pi's own
model runtime, whose commands and output are in [Appendix A](#appendix-a-the-probes).

> **In short.** Host pi listed no ChatGPT model because pi counts `openai-codex` as configured only
> when a credential is stored for it, and yolo stores one only inside a jail. Pi also accepts a
> provider that declares itself configured with no stored credential, so the host can serve the
> subscription through the socket NC-D37 already hands it, without writing the user's `auth.json`;
> D2 does that, on pi 0.81.0 and later ([§5.1](#51-as-built-the-native-registration)).

**Why it matters.** Running `yolo host -- pi` under the `codex` profile booted pi into a fallback
model (`local/qwen3.8-27b`) and hid every ChatGPT subscription model from `/model`, which said
*"Only showing models from configured providers. Use /login to add providers."* D2 fixes that
([§5.1](#51-as-built-the-native-registration)), though a start can still land on the fallback through
a start-up race in pi, with `/model` listing the subscription a moment later.

**The shape.** Three families: write the entry into the user's `auth.json` (by the launch, the
extension, `/login` or `yolo host apply`), give host pi an agent directory of yolo's own, as host
codex already has, or mark the provider configured from the extension and write nothing.

**Cost.** None for jail launches. The no-write family changes yolo's `openai-codex` registration
(D2 moves it onto pi's native provider interface), the managed directory is the largest build, and
the write families mutate the user's credential file.

**Start at [§1](#1-the-diagnosis-why-host-pi-ignores-openai-codex)** for the runtime gate, then
[§3.1](#31-what-nc-d37-protects) for what the host is protecting.

**Needs your ruling:** [OQ-2](#OQ-2), [OQ-3](#OQ-3). **Decided under your delegation of 2026-10-04, yours to
overrule:** [OQ-1](#OQ-1) D2, on its leaning ([PH-D1](#PH-D1)).

**Reads with:** [`notch-convergence.md`](../plans/notch-convergence.md) (NC-D37's host prelaunch rule),
[`agent-credentials.md`'s OpenAI service](../reference/agent-credentials.md#the-openai-subscription-credential-service) (the one refresh owner, and the pi view it serves),
[`agent-directory-map.md`](agent-directory-map.md) (`auth.json`'s class and its host column),
[`pi-codex-provider-shadowing.md`](pi-codex-provider-shadowing.md) (Pi provider catalog layering),
[`model-lists-and-pickers.md`](model-lists-and-pickers.md) (ML-D3, MM-D23, and
[OQ-MM5](model-lists-and-pickers.md#OQ-MM5) on a host `-p`).

---

## 1. The Diagnosis: Why host Pi ignores OpenAI Codex

**Evidence tags used in this doc.** **MEASURED**: run and observed, either live on the maintainer's
host (2026-09-30) or by an offline probe whose command and output are in
[Appendix A](#appendix-a-the-probes), cited by its label (P1 to P8, W1, E1, O1). **SOURCED**: read at the
cited file and line, or URL. **INFERRED**: reasoned from sourced code and not run. A statement of what
an option *would* do is INFERRED unless it carries another tag.

**The probes** *(throwaway, not in the tree)* import pi's own `ModelRuntime` from the installed
package. They run with a scratch `HOME`, scratch `auth.json` and `models.json`, a fake `yolo` first on
`PATH`, and a `fetch` that records the URL and throws. They start no CLI, call no model and log
nothing in. A test built from them is a build step ([§5](#5-build-order-and-the-tests-that-pin-each-step)).

**Paths.** pi-coding-agent and its pi-ai dependency are both 0.99.2 (MEASURED, E1).
`$PI` is `@earendil-works/pi-coding-agent/dist` and `$AI` is that package's
`node_modules/@earendil-works/pi-ai/dist`; a bare `docs/` path is the package's own documentation.
The `pi` binary runs `$PI/bundle/cli.js` (the package's `bin`), a bundle that keeps these
identifiers. Upstream's `packages/ai/src/models.ts` carries the same `checkProviderAuth` at tag
`v0.87.1` and on `main`, read 2026-10-01 (SOURCED,
[`main`](https://github.com/earendil-works/pi/blob/main/packages/ai/src/models.ts),
[`v0.87.1`](https://github.com/earendil-works/pi/blob/v0.87.1/packages/ai/src/models.ts)).

### 1.1 The gate, step by step

1. **The extension layer** (SOURCED). [`yolo-openai-auth.js`](../../packs/pi/extensions/yolo-openai-auth.js)
   registers `openai-codex` with `pi.registerProvider("openai-codex", { ... })` (everywhere but
   `yolo host -- pi` on a pi that takes the native provider, since [PH-D2](#PH-D2) and
   [PH-D4](#PH-D4)). Its model list comes from
   `~/.pi/agent/yolo-openai-codex-models.json` (`CODEX_LIST_FILE`), and its `oauth` block wires
   `login`, `refreshToken` and `getApiKey` to the yolo auth broker.
2. **Registrations land before selection** (SOURCED, `$PI/core/agent-session-services.js:72-111`).
   Pi applies every extension registration and then awaits `modelRuntime.refresh({ allowNetwork: false })`
   before it creates the session.
3. **The check** (SOURCED, `$AI/models.js:263-284`). Each availability pass asks `checkProviderAuth`
   per provider:
   ```javascript
   async checkProviderAuth(provider, credential, signal) {
       if (credential?.type === "oauth") {
           return provider.auth.oauth ? { source: "OAuth", type: "oauth" } : undefined;
       }
       const apiKey = provider.auth.apiKey;
       if (!apiKey) return undefined;
       ...
   }
   ```
   The passes are `runAvailabilityRefresh` and `refreshProviderAvailability`
   (`$PI/core/model-runtime.js:184-209`, `:224-277`). `hasConfiguredAuth` reads only the set those
   passes cache (`:361-363`).
4. **openai-codex has no key method to fall back on** (SOURCED). The built-in provider is OAuth-only
   (`$AI/providers/openai-codex.js:6-20`). The composer adds no API-key method to an OAuth-only provider
   unless an extension or `models.json` supplies `apiKey` (`$PI/core/provider-composer.js:231-233`:
   *"OAuth-only providers get no fabricated API-key login method"*). With no stored oauth entry the
   check returns `undefined`.
5. **No callback probes availability** (MEASURED, P2). A stored oauth entry with `expires: 1` counts as
   configured, and the fake `yolo` behind the extension's `login` and `refreshToken` is never called.
   Expiry is not part of the check.
6. **The divergence between notches** (SOURCED).
   - **In a jail** the launcher's prelaunch ([`shims.go`](../../internal/entrypoint/shims.go#L990))
     runs `yolo internal openai-auth-client token --pi-auth=$HOME/.pi/agent/auth.json` before pi
     starts, which merges the broker's view into `auth.json`. The pi pack declares that prelaunch
     whenever `openai-codex` is any entry of the active set ([`derive.lua`](../../packs/pi/derive.lua#L1360-L1363)).
   - **At the host** [`openaiauthhost`](../../internal/openaiauthhost/host.go#L165-L166) serves the
     pi view as `YOLO_OPENAI_AUTH_HOST_SOCKET` and writes nothing, as
     [NC-D37](../plans/notch-convergence.md#NC-D37) states
     ([§3.1](#31-what-nc-d37-protects)).
7. **The failure** (MEASURED live; SOURCED `$PI/core/model-resolver.js:503-530`).
   - With no `openai-codex` entry in `~/.pi/agent/auth.json`, the provider reports
     `{ configured: false }` (P1: the same result with an empty `auth.json`).
   - `findInitialModel` keeps the saved default (`openai-codex/gpt-6.1-sol`) only when
     `hasConfiguredAuth` is true. Otherwise it takes a known provider's default model from
     `getAvailableSnapshot()`, else that snapshot's first model.
     A restored session applies the same gate (`$PI/core/sdk.js:92-100`).
   - `local` declares the literal key `"local"`, which counts as configured (P1: `local` configured,
     and `local/qwen3.8-27b` the only available model), so pi starts on `local/qwen3.8-27b`.
   - The `/model` picker lists `getAvailableSnapshot()` (`$PI/modes/interactive/components/model-selector.js:103-109`),
     so no `openai-codex` model appears.

### 1.2 The live confirmation

Running `/login` in host pi and choosing *OpenAI Codex (yolo shared login)* logged in at once with
no browser, saved the credential to `~/.pi/agent/auth.json`, and made the `openai-codex` models
available (MEASURED live). The path is SOURCED: `showLoginDialog` → `modelRuntime.login(id, "oauth", …)`
(`$PI/modes/interactive/interactive-mode.js:5142-5165`) → the composer's adapter around the
extension's `login` (`provider-composer.js:183-193`) → [`brokerLogin`](../../packs/pi/extensions/yolo-openai-auth.js),
which asks `status`, logs in only when it must, and returns `brokerToken`. Pi writes the result
under its own lock and marks the provider configured. A probe with a fake broker reproduced it (P3):
configured went from false to true, `isUsingSubscription` was true, no lock was left behind, and
another provider's entry survived beside an entry of the shape `WritePiAuth` writes.

Running `yolo internal openai-auth-client token --pi-auth ~/.pi/agent/auth.json` by hand has the same
effect, through [`WritePiAuth`](../../internal/openauthclient/pi.go#L26-L73).

### 1.3 Corrections to the first draft of this section

Each of these is about pi's mechanism. None changes the diagnosis.

| The first draft said | What pi 0.99.2 does (SOURCED) |
| :--- | :--- |
| `updateModelSnapshot` queries `checkProviderAuth` | It filters by the cached configured set (`model-runtime.js:176-183`). Only the two availability passes run the check. |
| The credential is read directly from `auth.json` | It is read through the runtime-credentials overlay (`$PI/core/runtime-credentials.js:17-21`), where a `--api-key` override wins over the file. |
| The picker's *"Only showing models from configured providers"* line is the filter's message | It is a static hint, shown whenever no models are scoped (`model-selector.js:60-69`). The filter is `getAvailableSnapshot()`. |
| (unstated) | A registration counts as configured at once only when a credential is already stored or `apiKey` is configured (`model-runtime.js:641-655`, `:672`). Every other case waits for the availability pass in step 2. |

---

## 2. Secondary Symptom: Catalog Refresh Failures under Credential Scoping

The same session shows, once `/model` is opened:

```text
Could not refresh 3 model catalogs (cerebras, deepseek, openrouter); showing cached models.
```

1. **How the rows get there** (SOURCED). The models derive in [`derive.lua`](../../packs/pi/derive.lua#L587-L620)
   writes a `models.json` row for each composed provider, with `"apiKey": "${CEREBRAS_API_KEY}"` and
   the like ([L729-L739](../../packs/pi/derive.lua#L729-L739)).
2. **Credential scoping** (MEASURED live). Under the `codex` profile, `yolo host` scopes credentials to
   the selection ([`providers.md`'s credential gate](../reference/providers.md#the-credential-gate)) and withholds
   `CEREBRAS_API_KEY`, `DEEPSEEK_API_KEY` and `OPENROUTER_API_KEY`.
3. **What fails, and where** (SOURCED; MEASURED by probe).
   - **No network call is made and nothing returns 401.** For a row whose `apiKey` names an unset
     variable, the composed resolver throws locally (`provider-composer.js:276-278`), and the catalog
     refresh records that as the provider's error (`$AI/models.js:200`, `:209-212`). P5: the error
     *Failed to resolve API key for provider "cerebras" from environment variable: CEREBRAS_API_KEY*,
     with no `fetch` at all.
   - **Only a row that overlays one of pi's built-in providers can fail.** Every built-in except
     `radius` carries a remote-catalog refresh hook (`model-runtime.js:87-91`). A built-in with no row
     and no key is skipped silently (P5: `deepseek` with no row, no error). A provider that exists
     only in `models.json` has no hook (`provider-composer.js:384`; P5: `local`, no error).
   - **A keyed refresh asks pi.dev, not the provider, and sends no credential** (MEASURED, P5: three
     attempts at `https://pi.dev/api/models/providers/cerebras?types=…` once the variable is set;
     SOURCED `$PI/core/remote-catalog-provider.js:76-85`).
4. **When the user sees it** (SOURCED). Pi's startup catalog refresh swallows its errors
   (`interactive-mode.js:796-803`). The warning appears only in the `/model` picker
   (`model-selector.js:144`) and in `/model <term>` (`interactive-mode.js:4255-4257`). So it is not a
   startup warning.
5. **What pi offers to turn it off** (SOURCED). No per-provider enable flag and no per-provider refresh
   switch exist in the `models.json` schema (`$PI/core/model-config.js:195-206`). The one switch is global,
   `PI_OFFLINE` or `--offline` (`model-runtime.js:92`, `$PI/main.js:446-448`), and it would also stop
   the `openai-codex` catalog refresh.
6. **Impact.** Noise only. A row whose variable is unset is already unconfigured
   (`provider-composer.js:255-260`), so its models are already hidden, and this does not cause the
   `openai-codex` absence.

---

## 3. Potential Solutions

Every option has to answer four things: what it does to NC-D37 ([§3.1](#31-what-nc-d37-protects)),
what happens to a seeded token afterwards ([§3.2](#32-what-a-seeded-entry-does-afterwards-refresh-rotation-the-lock)),
what it does to a host pi with its own login ([§3.3](#33-a-host-pi-that-already-has-its-own-openai-codex-login)),
and what the user sees.

### 3.1 What NC-D37 protects

[NC-D37](../plans/notch-convergence.md#NC-D37) is an implementation decision (notch convergence item
15, row C6), not a maintainer ruling. In full:

> *Implementation decision, item 15, row C6.* The host's OpenAI prelaunch reads
> `YOLO_AUTH_PRELAUNCH_<BIN>_FLAG` and `_LOGIN` from the launch's composed variables
> (`hostComposition.prelaunch`), spelled by `openaiauthhost.PrelaunchVar` exactly as the jail's
> launcher spells them; a test pins the two spellings together. `openaiauthhost.Prepare` does nothing
> when nothing is declared. It serves a declared view the host's way: the pi view as the host
> credential socket, since a jail's pi auth file is the user's own file at the host; the codex view
> as a managed `CODEX_HOME` under `host-agents/<declaring pack>` (`EnvFoldEntry.Pack` carries the
> declarer; the shipped pack's path is unchanged); a login-only prelaunch proves the login and serves
> nothing; any other view refuses by name. With no login it starts the browser login only when stdin
> is a terminal (the host gate's own probe, `hostGateCanPrompt`); off one it prints the jail
> launcher's two lines and runs the command without the credential

**What it protects is the user's own credential file.** The reason is in its own words: *"a jail's
pi auth file is the user's own file at the host"*. Four things stand behind it:

- **The rule is older than NC-D37.** `6d118252` (2026-09-15) returned before any write with
  `if agent == "pi" { return launch, nil }` (SOURCED, `git show 6d118252`). NC-D37 carried that
  forward and gave the reason.
- **The broker design states the rule for codex only.** Its user experience section says *"Yolo never
  rewrites the user's ordinary `~/.codex/auth.json` or silently changes a directly launched host Codex"*
  ([`agent-credentials.md`'s OpenAI service](../reference/agent-credentials.md#openai-login)). Its ledger rules
  *"`yolo host -- codex` shares the broker through a managed Codex home; direct host Codex remains
  untouched"* ([OQ-OA3](../reference/agent-credentials.md#oq-oa3)). For pi it says only that
  *"`yolo host -- pi` and the Pi extension use the private socket directly"*
  ([the OpenAI service's backend routes](../reference/agent-credentials.md#openai-backends)). Nothing says how pi's first `auth.json` entry
  appears at the host. **That is the design gap this doc fills.**
- **The directory map classes `auth.json` as state and credential** (*"every provider's login and API
  key"*), and its host column says yolo writes nothing there: *"pi itself persists the broker's view
  that yolo's extension returns"* ([`agent-directory-map.md`](agent-directory-map.md#62-the-map)). The
  map gives no `host_management` mode a write to it.
- **It protects only the first write.** Once an entry exists, pi refreshes it through yolo's
  `refreshToken` and writes the broker's view back into the same file
  ([§3.2](#32-what-a-seeded-entry-does-afterwards-refresh-rotation-the-lock)). So the broker's
  credential already reaches the user's file after a `/login`. What NC-D37 decides is **who makes the
  first write**: today the user, by running `/login`.

| Option ([§3.4](#34-for-the-authjson-seeding-the-option-space)) | Relation to NC-D37 |
| :--- | :--- |
| A, A′ | **Amend it**: the host launch writes the user's file |
| B | Keeps its letter (the launch writes nothing) and defeats its reason: yolo's code writes the user's file from inside pi |
| C | **Leaves it as is**: the user makes the first write, as today |
| D1, D2 | **Leave it as is**: the socket becomes sufficient on its own |
| E | **Amends it** for pi, in the direction its codex half already took: a managed directory instead of the socket |
| F | Leaves NC-D37's letter (it is about the launch) and **moves the line host management holds**: a config verb writes a credential |

The related rulings: [OQ-NC7](../plans/notch-convergence.md#OQ-NC7) A (*"Host claude keeps its own
login … a shared store without interception races the broker"*, with OpenAI having *"one
machine-wide login lineage for the host and the jails"*); NC-D1 (*"host is supposed to act like
everywhere else"*, [ledger](../plans/notch-convergence.md#7-decision-ledger)); and
[OQ-HS3](host-notch-services.md#OQ-HS3) (*"fine with agents either being broken or not having
features … if not launched correctly"*).

### 3.2 What a seeded entry does afterwards: refresh, rotation, the lock

These facts hold for every option that stores an entry (A, A′, B, C, E, F).

- **Pi never holds the canonical refresh token.** The entry is an access token, its expiry and the
  non-secret marker `yolo-broker:<generation>` in pi's required `refresh` field
  ([the OpenAI service's one-writer rule](../reference/agent-credentials.md#openai-one-writer)).
- **Pi refreshes it itself** (SOURCED; MEASURED). At request time, with under five minutes left, pi
  runs `oauth.refresh` under its lock with a 15 s timeout (`$AI/auth/resolve.js:46-92`). That is the
  extension's [`refreshToken`](../../packs/pi/extensions/yolo-openai-auth.js), which asks the
  broker for the current generation. A catalog refresh does the same once the entry has expired
  (`$AI/models.js:230-245`). Pi writes the result back before it releases the lock
  (`$PI/core/auth-storage.js:378-395`). P4: an entry with 60 s left gave a new token and a stored
  `refresh: yolo-broker:2` after one refresh call.
- **Nothing flows back to the broker, and nothing needs to.** The broker is the one writer of the
  lineage. It returns a cached generation or performs one upstream refresh under its machine lock,
  however many pi files ask.
- **A seeded token goes stale only outside `yolo host`.** Plain `pi` still counts the provider as
  configured, because the check ignores expiry ([§1.1](#11-the-gate-step-by-step) step 5). Requests
  work on the stored access token until it expires. The next refresh then fails with
  [`brokerFailure`](../../packs/pi/extensions/yolo-openai-auth.js)'s advice to launch
  through `yolo host`. A live bearer token sits in the user's file in the meantime, as it already
  does after a `/login`.
- **Pi's lock** (SOURCED). `auth.json.lock` is a proper-lockfile 4.1.2 directory with `realpath: false`
  (`auth-storage.js:39`, `:85-90`). The async holder (refresh, login) uses `stale: 30_000` and retries
  for 30 s (`:76-115`). The sync holder uses the library's 10 s default, ten attempts 20 ms apart, and
  serves the constructor's `reload()`, which silently keeps the old snapshot when it cannot lock
  (`:33-56`, `:307-321`). Reads take the lock too, and re-read only when the file's revision changed
  (`:330-368`).
- **yolo's writer** takes the same lock by `mkdir` and judges staleness by pi's 30 s rule
  ([`pi.go`](../../internal/openauthclient/pi.go#L75-L90)). When a running pi holds it past the wait,
  the client exits 75, `ExitPiAuthLockBusy` ([`client.go`](../../internal/openauthclient/client.go#L32)).
  The jail's launcher then keeps the existing file ([`shims.go`](../../internal/entrypoint/shims.go#L1042)).
  The host has no such handling yet.
- **Pi's writer and yolo's differ on the file itself.** Pi creates `auth.json` as `{}` with mode 0600
  only when it is missing, and writes in place, so an existing file keeps its mode, its ACLs and a
  symlink (`auth-storage.js:14-32`; MEASURED, P3 and P4: a 0644 file stayed 0644 through pi's login
  and refresh). [`WritePiAuth`](../../internal/openauthclient/pi.go#L38-L44) chmods `~/.pi/agent` to
  0700 on every call and replaces the file by rename
  ([`codex.go`](../../internal/openauthclient/codex.go#L74-L111)). That resets the mode to 0600,
  turns a symlinked `auth.json` into a regular file and leaves a dotfiles target with the stale copy
  (MEASURED, W1). A replaced file also loses its ACLs (INFERRED: a rename installs a new inode). It
  is harmless in a jail and a mutation of the user's file at the host.

### 3.3 A host pi that already has its own openai-codex login

**This is broken today, whichever option is chosen** (SOURCED). An extension's `oauth` replaces pi's
built-in one (`provider-composer.js:296`), and yolo's `refreshToken` ignores the credential it is
handed and asks the broker (`refreshToken`). With yolo's
extension installed in `~/.pi/agent/extensions/`:

- **`/login` for `openai-codex` is yolo's broker login**, so the user cannot log pi into their own
  ChatGPT account at all.
- **An own login is replaced at its first refresh under `yolo host` with the `codex` profile.** Pi
  writes the broker's view over the user's refresh token. If the two accounts differ, that is a silent
  account switch, and the user's own login cannot be restored while the extension is loaded.
- **Under a direct launch, or `yolo host -p zai -- pi`, that refresh fails.** In the `-p zai` case the
  message wrongly says pi *"was not started through `yolo host`"*: no OpenAI prelaunch is declared, so
  the launch hands pi no socket, and [`brokerFailure`](../../packs/pi/extensions/yolo-openai-auth.js)
  cannot tell that case from a direct launch.

[OQ-HS3](host-notch-services.md#OQ-HS3) covers the direct launch. It does not obviously cover a
correct launch replacing the user's account.

**A buildable fix** *(the marker dispatch, coined here)* (INFERRED). `refreshToken` sends a credential
whose `refresh` matches the broker's marker (`^yolo-broker:[1-9][0-9]*$`,
[`codex.go`](../../internal/openauthclient/codex.go#L13)) to the broker, and any other credential to
pi's built-in `openai-codex` refresh. The extension already imports `builtinProviders`
(`builtinCodexProvider`). Pi supports one oauth method
per provider, so `/login` stays yolo's broker login. An own login made before the extension arrived
survives; a new one cannot be made while the extension is loaded. Whether the user's login or yolo's
should win at all is [OQ-3](#OQ-3). Its options in full:

- **A — The user's own login wins.** A stored own login keeps serving and refreshes through OpenAI,
  using the marker dispatch. yolo's login serves only a missing or broker-marked entry. `/login`
  stays yolo's, so a new own login cannot be made while the extension is loaded, and the extension
  says when an own login is serving.
- **B — yolo's login wins under `yolo host` with the `codex` profile.** An own login is replaced, as
  today, but disclosed. Pi's resolve order puts a stored credential first, so B forces a file write,
  ruling out D for [OQ-1](#OQ-1).
- **C — Two lineages, kept apart.** [OQ-1](#OQ-1)'s E: the managed directory serves `yolo host`, and the
  user's `~/.pi/agent` keeps its own login untouched.

What each option does to an own login is a column of the table in [§3.4](#34-for-the-authjson-seeding-the-option-space).

### 3.4 For the auth.json seeding: the option space

A, B and C are the first draft's [OQ-1](#OQ-1) options, and E takes the place of its third
seeding option; A′, D and F come from reading pi's runtime and the codex precedent.

In brief, as [OQ-1](#OQ-1) first listed them:

- **A — Host prelaunch merges `auth.json` (amend NC-D37).** `openaiauthhost.Prepare` calls
  `WritePiAuth` on `~/.pi/agent/auth.json` on every `codex`-profile launch. It keeps other
  providers, but deletes an own login and replaces a symlinked file.
- **A′ — Marker-gated merge (amend NC-D37).** Writes in place only when the entry is absent or is
  already a broker view.
- **B — `yolo-openai-auth.js` seeds `auth.json` on extension load.** When `openai-codex` is
  missing it writes `brokerToken()`'s view, through pi's exported runtime and so under pi's own
  lock.
- **C — Require explicit `/login` once on the host**, with a line that says so. `/login` reuses
  the broker's login with no browser. No amendment; no jail parity.
- **D — Mark the provider configured with no file write.** D2, a native provider with a key check
  gated on the host socket, keeps the subscription label and blocks nothing. No amendment.
- **E — A managed pi agent directory**, as host codex has. Two lineages; the user's other pi logins
  are absent under `yolo host`.
- **F — `yolo host apply` seeds under `host_management`.** Moves the host-management credential
  line.

#### A — Host prelaunch seeding with `WritePiAuth` (amend NC-D37)

`openaiauthhost.Prepare` calls `WritePiAuth` on `~/.pi/agent/auth.json` for the pi view.

- **The user sees** the codex default at start, with nothing said unless a launch line is added.
- **It writes** the user's `auth.json` on every `codex`-profile launch (precisely, every launch whose
  active set has an `openai-codex` entry, [`derive.lua`](../../packs/pi/derive.lua#L1360-L1363)), with
  the side effects of [§3.2](#32-what-a-seeded-entry-does-afterwards-refresh-rotation-the-lock)'s last
  bullet.
- **It ignores a relocated agent directory.** The prelaunch path is fixed at `.pi/agent/auth.json`
  ([`derive.lua`](../../packs/pi/derive.lua#L1362)), while pi honors `PI_CODING_AGENT_DIR`, which a host
  shell passes through to pi.
- **Lock:** pi's, as above. The host would need the jail's exit-75 handling.
- **An own login is deleted** on every `codex`-profile launch (MEASURED, W1, a scratch test of
  `WritePiAuth` on a host-shaped home: it replaced the entry, replaced a symlinked file with a regular
  one, moved `~/.pi/agent` from 0755 to 0700, and kept and reformatted every other provider, `!command`
  values included). So the first draft's claim that it works *"without clobbering existing host
  credentials"* holds for other providers only.

#### A′ — Marker-gated prelaunch merge (amend NC-D37)

A with three changes: it writes only when the entry is absent or is already a broker view (the
marker above); it writes in place, through a symlink, and leaves the directory's mode alone; and it
resolves the agent directory as pi does.

- **The user sees** what A shows.
- **It writes** the user's file, on a launch that finds no entry.
- **Refresh** is [§3.2](#32-what-a-seeded-entry-does-afterwards-refresh-rotation-the-lock)'s.
- **Lock:** on exit 75 with an entry present it keeps the file; with none it runs pi without the
  credential and says so.
- **An own login is kept.**

#### B — Extension-side initialization seeding

The extension's factory checks `auth.json` when `YOLO_OPENAI_AUTH_HOST_SOCKET` is set, and writes
`brokerToken()`'s view when `openai-codex` is missing.

- **The user sees** the codex default at start. The factory is awaited before the post-registration
  availability pass ([§1.1](#11-the-gate-step-by-step) step 2), so the first pass would read the new
  entry (INFERRED).
- **It writes** the user's file, from inside pi.
- **Lock:** the extension context has no credential setter, `ModelRegistry.runtime` is private
  (`$PI/core/model-registry.d.ts:22`), and the package's one credential export is a reader,
  `readStoredCredential` (`$PI/index.d.ts:4`). But the package root also exports `ModelRuntime`
  (`:15`), and the extension already imports that root (`piVersion`).
  A second runtime over the same `auth.json` would write through pi's own lock code, as the `/login`
  probe's runtime did (INFERRED for an extension; not run there). Hand-written
  proper-lockfile-compatible code in JavaScript is the alternative.
- **An own login is never touched.**

#### C — One-time `/login` on the host

Today's behavior, documented and announced: when `openai-codex` is selected and not configured, the
extension says once per session *"run /login → OpenAI Codex (yolo shared login)"*. It can find out
from the registry's available models at `session_start` without reading `auth.json` (INFERRED).

- **The user sees** the fallback model once, and a line telling them the fix.
- **It writes** nothing of yolo's. Pi writes the entry, on an explicit user action, under its own lock,
  in the shape `WritePiAuth` writes (MEASURED, [§1.2](#12-the-live-confirmation)).
- **An own login is overwritten**, but only when the user asks.
- **Jail parity is lost**: a jail starts on the subscription and the host does not.

#### D — Mark the provider configured with no file write

Pi accepts a provider that is configured by a key method rather than a stored credential. Two
supported forms exist (SOURCED, MEASURED by probe):

- **D1 — `apiKey: "!<command>"` on the existing registration.**
  - The availability check counts a command value as configured **without running it**
    (`provider-composer.js:252-254`). Pi runs the command fresh on every request
    (`:276-281`; `$PI/core/resolve-config-value.js:192-201`; pi's `docs/models.md:64` says so). P7:
    configured, source `models_json_command`, the command did not run during the check and did run at
    `getAuth`.
  - A stored oauth entry still wins over the key (`$AI/auth/resolve.js:24-28`), so a jail and an own
    login behave as today.
  - **Costs:** the command runs through `execSync`, blocking pi's event loop, with a 10 s timeout and
    stderr discarded (`resolve-config-value.js:159-171`), which loses `brokerFailure`'s advice.
    `openai-auth-client token` prints a JSON view, so it would need a raw-token mode. `isUsingOAuth`
    becomes false (MEASURED, P7), so the footer shows a dollar cost without *"(sub)"*
    (`$PI/modes/interactive/components/footer.js:162-167`), and `/login` lists an extra API-key method
    (`interactive-mode.js:4739-4747`). The extension must add the key only when the host socket is
    set, or plain pi counts the provider configured and fails every request.
- **D2 — A native provider registered with `pi.registerProvider(provider: Provider)`.**
  - The overload is SOURCED in `$PI/core/extensions/types.d.ts:1313` and applied by
    `registerNativeProvider` (`model-runtime.js:626-635`). Pi's own docs recommend native auth for
    custom resolution (`docs/custom-provider.md:83`).
  - `auth.apiKey` takes an async `check()` and `resolve()` (`$AI/auth/types.d.ts:170-196`), and a check
    may answer with type `"oauth"` (`:94-97`).
  - **MEASURED** (2026-10-01, P8): the built-in `openai-codex` provider spread with an added `auth.apiKey`
    whose `check` returns `{ type: "oauth", source: "yolo shared login" }`, over an empty `auth.json`:
    configured, `isUsingOAuth` and `isUsingSubscription` both true, no `resolve` during the check, and
    `getAuth` returned the stand-in broker token while `auth.json` stayed `{}`.
  - The same probe with a stored own login (P8): `getAuth` returned the own access token, because a
    stored oauth entry wins. Near expiry pi refreshed it against `https://auth.openai.com/oauth/token`,
    the built-in refresh, not the broker. Which oauth the native provider carries is therefore
    [OQ-3](#OQ-3)'s question, and a stored broker view (a jail's, or the one `/login` already wrote on
    the maintainer's host) still needs a broker-capable refresh: the marker dispatch of
    [§3.3](#33-a-host-pi-that-already-has-its-own-openai-codex-login).
  - `resolve` stays asynchronous, so `brokerFailure`'s messages survive. The implementer may keep the
    broker's view in memory until pi's five-minute window rather than ask the client per request.
  - The launch sets the socket only after it has proved the login
    ([`host.go`](../../internal/openaiauthhost/host.go#L158-L161)), so a configured answer at start is
    one the broker can serve (INFERRED).
  - **Costs** (INFERRED): a native registration replaces the `ProviderConfig` layer
    (`model-runtime.js:629`, `:660`), so the model list, the display name and the `streamSimple`
    refusal move onto the provider object. `/login` lists an extra API-key method, since the picker
    offers one for any `auth.apiKey` (`interactive-mode.js:4739-4747`). The `check` must answer only
    when `YOLO_OPENAI_AUTH_HOST_SOCKET` is set, so plain pi keeps today's advice to launch through
    `yolo host`.
- **Both forms:** nothing is stored, so nothing goes stale and no lock is taken. Plain pi after a
  `yolo host` launch sees the provider unconfigured, exactly as today.
- **Not usable:** a runtime API key does nothing for an OAuth-only provider (`resolve.js:29-33`), and
  no extension API sets a credential.

#### E — A managed pi agent directory (the codex precedent)

The first draft's option 3 rejected this because pi has no `--auth-path` flag. That is true and does
not matter: `PI_CODING_AGENT_DIR` relocates pi's whole agent directory, and
`PI_CODING_AGENT_SESSION_DIR` can keep sessions where they are (SOURCED `$PI/config.js:434-456`; the
directory map already declares both, [`agent-directory-map.md`](agent-directory-map.md#62-the-map)). Pi has
no auth-only variable or flag (MEASURED, E1: every `PI_` variable `$PI` reads, and the flag list in
`$PI/cli/args.js`).

- **It writes** a directory of yolo's under `~/.local/share/yolo-jail/host-agents/pi`, as host codex
  gets one ([`host.go`](../../internal/openaiauthhost/host.go#L173), [`prepareCodexHome`](../../internal/openaiauthhost/host.go#L396)).
  It holds its own `auth.json` with the broker view, and links into `~/.pi/agent` for everything else:
  settings, `models.json`, extensions, skills, `AGENTS.md`, keybindings, prompts, themes, packages.
- **The user sees** the subscription, and **loses their other pi logins under `yolo host`**, because
  the managed `auth.json` holds only the broker's entry. Copying their OAuth entries in would race
  refresh-token rotation, which is [OQ-NC7](../plans/notch-convergence.md#OQ-NC7) B's problem.
  Copying API-key entries is safe (INFERRED).
- **An own login is never touched**: two lineages, kept apart, the [OQ-NC7](../plans/notch-convergence.md#OQ-NC7) A shape.
- **Lock:** concurrent `yolo host -- pi` launches share the managed file, as concurrent codex
  launches share their managed home.
- **A trap:** the extension reads its model list from `homedir()/.pi/agent/`
  (`CODEX_LIST_FILE`), not from pi's agent directory. A link
  hides that, but any yolo code that spells `~/.pi/agent` has to be audited.
- **It is also the lever [OQ-MM5](model-lists-and-pickers.md#OQ-MM5) B anticipates** *"for pi, whose
  selection is also file-held"*: a managed directory could carry a host `-p`'s selection.
- **Cost:** the largest build here. Pi's agent directory has many children, the directory map walks it
  strictly, and the link set has to track pi's layout.

#### F — `yolo host apply` seeds under `host_management`

- **It writes** the entry into the user's `auth.json` at apply time, not per launch. An absent
  `host_management` key means `assert` today ([`hostmanagement.go`](../../internal/config/hostmanagement.go#L109-L112)).
- **The seeded token goes stale between applies** and is refreshed only by a later `yolo host` launch.
- **Lock** and **own login:** as A or A′, depending on the gate.
- **It moves the credential line** host management holds ([§3.1](#31-what-nc-d37-protects)).
- **My read:** dominated by A′, which writes at the moment the token is needed.

#### The options side by side

| Option | Writes the user's `auth.json` | NC-D37 | An own login | Plain `pi` after a `yolo host` launch | Build |
| :--- | :--- | :--- | :--- | :--- | :--- |
| A | every codex launch, by rename | amends | deleted | configured; works until expiry | small |
| A′ | when absent, in place | amends | kept | configured; works until expiry | small |
| B | when absent, from pi | defeats its reason | kept | configured; works until expiry | small, through pi's runtime |
| C | only on the user's `/login` | as is | overwritten on request | configured; works until expiry | tiny |
| D1 | never | as is | kept, but see [§3.3](#33-a-host-pi-that-already-has-its-own-openai-codex-login) | unconfigured | small, with UI costs |
| D2 | never | as is | kept, but see [§3.3](#33-a-host-pi-that-already-has-its-own-openai-codex-login) | unconfigured | medium (native provider) |
| E | never (yolo's own file) | amends | untouched | unconfigured | large |
| F | at apply time | moves the host-management line | as A or A′ | configured; works until expiry | small |

"Configured; works until expiry" is [§3.2](#32-what-a-seeded-entry-does-afterwards-refresh-rotation-the-lock)'s
stale-token case: the check ignores expiry, so the provider stays listed after its token has lapsed.

### 3.5 For catalog refresh noise

The letters are [OQ-2](#OQ-2)'s.

In brief, as [OQ-2](#OQ-2) first listed them:

- **A — Only render providers with available credentials in `models.json`.** Omit a row whose key
  credential scoping withholds, at the host and in a jail. Cannot follow a launch's `-p` at the
  host, and loses rows an attach or a direct launch needs.
- **B — Leave `models.json` intact.** The warning stays, in the picker only.
- **C — Leave `apiKey` off a built-in's row when the row's variable is pi's own name for it.**
  Silences the warning for those rows, with the same key when it is set. Other rows keep it.

In full:

- **A — Filter `models.json` by the active profile.** Write a row only when its credential is delivered
  this launch, or its key is a literal such as `local`.
  - **At the host** (INFERRED): `models.json` is rendered by `yolo host apply` from the user-scope
    composition, not per launch, so it cannot follow each launch's `-p`. Rewriting it per launch would
    race concurrent launches with different `-p`, the hazard [MM-D27](model-lists-and-pickers.md#MM-D27)
    solves for codex's menu. A direct launch, and a key from the user's own shell, need the rows.
  - **In a jail** (INFERRED): `models.json` is rendered at boot, while an attach rewrites the agent's
    env file whole ([`active-provider-sets.md`](active-provider-sets.md)), so a key an attach delivers
    would find no row. [AP-D16](active-provider-sets.md#AP-D16) assumes rows exist for providers
    outside the set.
- **B — Accept the warning as harmless.** Pi degrades to cached models, and the warning appears only in
  the picker.
- **C — Leave `apiKey` off a row for one of pi's built-in providers when the row's variable is pi's own
  name for it** — `cerebras`, `deepseek` and `openrouter` here (`$AI/env-api-keys.js:85-93`). Pi's
  inherited key lookup then answers nothing when the variable is unset and the same key when it is
  set, and every row stays. **MEASURED** (2026-10-01, P6): a `cerebras` row with no `apiKey`
  refreshed with no error and no `fetch` while `CEREBRAS_API_KEY` was unset, and was configured with
  the same key, and its row's `baseUrl`, once it was set. A built-in whose configured variable has
  another name keeps the warning. A via row keeps its `apiKey`, because that key is the via
  service's caller token ([`derive.lua`](../../packs/pi/derive.lua#L728-L730)), not pi's variable.

**The mid-session switching cost**, which the first draft gave as B's reason, is small for
every option. Pi's environment is fixed when it starts, so a withheld key cannot arrive mid-session,
and a profile change is a new launch. The one mid-session path is `/login` storing a key for that
provider: a stored key makes it configured (`provider-composer.js:244-251`), and under A it
would lose its row's `baseUrl` and model overrides.

---

## 4. The opencode parallel

opencode 1.18.34 has the same kind of gate (SOURCED from the binary's strings, O1 for the loader
gate, the `OPENCODE_AUTH_CONTENT` read, the refresh host and the headless method; the rest INFERRED):

- **A plugin's OAuth loader runs only when its auth store has an entry**, so the ChatGPT subscription
  is live only with an `openai` entry, or with `OPENAI_API_KEY` set (the API-key route).
- **The store** is `$XDG_DATA_HOME/opencode/auth.json`, else `~/.local/share/opencode/auth.json`.
  `OPENCODE_AUTH_CONTENT` replaces the whole store read, so it could seed without a file, but the save
  after a refresh would write that content plus the new entry to the file, dropping the file's other
  entries (INFERRED).
- **Refresh posts the stored refresh token to `https://auth.openai.com`**, so a `yolo-broker:` view
  would fail at opencode's first refresh. A native opencode login is a separate grant, the
  [OQ-NC7](../plans/notch-convergence.md#OQ-NC7) A shape.

**It does not need this doc's fix today.** The opencode pack declares no OpenAI prelaunch, so yolo
writes nothing for it at either notch. At the host a native login lives in the user's own file. In a
jail a native login is per workspace, and its `localhost:1455` browser callback cannot be reached
from the host; the *"ChatGPT Pro/Plus (headless)"* device-code method avoids that.

---

## 5. Build order, and the tests that pin each step

What I would build, in order, on the leanings below. Each step names the ruling it waits on. Each
test has to fail when the production call site is deleted, not only when the callee changes.

1. **The catalog noise ([OQ-2](#OQ-2) C).** The pi models derive leaves `apiKey` off a
   built-in's row when the row's variable is pi's own name for it.
   - A derive test that renders through the real surface loop: no `apiKey` on a `cerebras` row keyed by
     `CEREBRAS_API_KEY`, and the `${…}` reference kept on a row whose variable has another name.
   - A pi-backed test, skipped when pi is not installed, that runs the rendered `models.json` through
     pi's own runtime offline: no refresh error with the variable unset, configured with it set.
2. **The marker dispatch ([OQ-3](#OQ-3) A).** `refreshToken` sends a `yolo-broker:` credential to the
   broker and any other to pi's built-in refresh.
   - Through the extension's node harness: a marker credential reaches the client; an own credential
     reaches the stubbed built-in refresh and never the client.
3. **The native registration ([OQ-1](#OQ-1) D2).** The extension registers `openai-codex` as a native
   provider whose key check answers only when `YOLO_OPENAI_AUTH_HOST_SOCKET` is set. The model list,
   name and refusal move onto it. **Built 2026-10-04** ([§5.1](#51-as-built-the-native-registration)).
   - The existing list, refusal and naming tests in
     [`pi_openai_auth_extension_test.go`](../../internal/entrypoint/pi_openai_auth_extension_test.go) and
     [`pi_openai_auth_refusal_test.go`](../../internal/entrypoint/pi_openai_auth_refusal_test.go) move to
     the native shape and still pass.
   - A pi-backed test, skipped without pi, loading the shipped extension into pi's own runtime over an
     empty `auth.json`: configured with the socket variable set, unconfigured without it, and
     `auth.json` byte-identical afterwards.
   - [`TestPreparePiUsesHostSocketWithoutStartingAdapter`](../../internal/openaiauthhost/host_test.go#L23)
     stays as it is: the host writes nothing.
4. **The disclosures.** When a stored own login serves the launch, the extension says so once per
   session. When pi runs at the host without the socket, `brokerFailure` stops claiming it was not
   started through `yolo host`, which needs a host witness the launch provides.
   - Through the node harness, at `session_start` and on a failed refresh.

**What done looks like.** On a host with no `openai-codex` entry, `yolo host -- pi` under the `codex`
profile starts on the saved `openai-codex` default, lists the subscription models in `/model`, shows
*"(sub)"* in the footer, and leaves `~/.pi/agent/auth.json` with the same hash it had before. Plain
`pi` afterwards shows no `openai-codex` model and advises `yolo host -- pi`. A pre-existing own
login is still the stored entry after a session.

If [OQ-1](#OQ-1) is ruled for a file write instead, step 3 becomes A′'s gated writer, with exit-75
handling and a test that an own entry and a symlinked file survive a `codex`-profile launch.

### 5.1 As built: the native registration

Built 2026-10-04 in [`yolo-openai-auth.js`](../../packs/pi/extensions/yolo-openai-auth.js) alone. No Go
code changed: `yolo host -- pi` already hands pi the broker's socket and writes nothing
([`TestPreparePiUsesHostSocketWithoutStartingAdapter`](../../internal/openaiauthhost/host_test.go) still
pins that).

- **Two registrations, one per route** ([PH-D2](#PH-D2)). The **host route**: where
  `YOLO_OPENAI_AUTH_HOST_SOCKET` is set, pi's loader takes a provider object (its root's `ModelRuntime`
  has `registerNativeProvider`) and pi exports its built-in `openai-codex` provider serving
  `openai-codex-responses`, the extension registers that provider natively, `pi.registerProvider(provider)`,
  named *OpenAI Codex*, with yolo's broker login as its `auth.oauth` and a key method as its
  `auth.apiKey`. The key method's `check` answers `{ type: "oauth", source: "yolo shared login" }`
  while the socket is set and nothing otherwise; its `resolve` returns the broker's access token. The
  **jail route**, everywhere else: the `ProviderConfig` registration the extension made before, unchanged.
- **The list, the name and the refusal ride on the native provider.** Its `getModels` is the rendered
  list, each entry completed as a model of this provider at the subscription's address, and no catalog
  refresh replaces it. With the list's `enforce` on, its `stream` and `streamSimple` throw the
  [MM-D23](model-lists-and-pickers.md#MM-D23) refusal for a model outside the list and hand a listed one
  to pi's built-in stream.
- **The key method has no `login`.** Pi's `/login` still lists an API-key entry for any provider with a
  key method, and for one with no `login` choosing it shows the method's name and *"is configured
  outside pi"*, here *"yolo shared login (through `yolo host`) is configured outside pi."* (SOURCED, pi
  1.0.1 `interactive-mode.js`, `getLoginProviderOptions` and `showAmbientAuthDialog`). Not run in an
  interactive pi.
- **`resolve` reuses the broker's view** until five minutes before it expires ([PH-D3](#PH-D3)).
- **Fallback** ([PH-D4](#PH-D4)): with no built-in to register, or a pi whose loader cannot take a
  provider object, the host route registers the jail route's `ProviderConfig`. pi 0.80.10 is such a
  pi: it exports the built-in, but its `registerProvider` takes the object for a name, throws nothing,
  and fails applying it, which, when a `try`/`catch` around the call chose the route, reported the
  extension as failed and lost yolo's login (MEASURED 2026-10-04). Asking for
  `registerNativeProvider` keeps 0.80.10 on the `ProviderConfig`, unconfigured with no stored login
  and carrying *OpenAI Codex (yolo shared login)*, the login [§1.2](#12-the-live-confirmation) chose
  in `/login` (MEASURED by a throwaway probe of `createAgentSessionServices`).

**What is measured.** MEASURED 2026-10-04 on pi 1.0.1 by the two `TestPiOpenAIAuthUnderPisOwnRuntime`
tests in [`pi_openai_auth_native_test.go`](../../internal/entrypoint/pi_openai_auth_native_test.go),
which load the shipped file into pi's own `createAgentSessionServices` offline, with a stand-in client
and the boot render's `codex` list. With the socket set over an empty `auth.json`, pi counts
`openai-codex` configured as an OAuth subscription, lists the profile's models, resolves the broker's
token, and ends an unlisted model's turn with yolo's refusal; `auth.json` is byte-identical afterwards.
Without the socket it does none of that and asks the client nothing. With an own login stored, the own
access token wins and the client is not asked. The test skips where pi's package is not installed,
which includes CI; the node harnesses in the same package pin both routes against stand-ins there. It
also skips on a pi lacking a runtime method it reads, naming them and the version, once it has checked
that the extension loads there without an error and leaves `auth.json` alone. MEASURED 2026-10-04: it
passes on pi 0.87.1, 0.99.2, 1.0.1 and 1.0.2, and skips on 0.81.0 (no `isUsingSubscription`) and
0.80.10 (no `registerNativeProvider` either), where a throwaway probe of the same call showed 0.81.0
configured on the host route and 0.80.10 on the `ProviderConfig`, unconfigured, with no extension
error.

**A start-up race in pi, not closed here.** Each registration starts a model refresh pi does not
await, and the refresh `createAgentSessionServices` does await can finish while one of those has
superseded its availability pass and is still running. A provider pi counts as configured only through
that pass, as the host route's is, can then be missing from the snapshot pi reads to pick the start
model, so pi starts on its fallback; `/model` lists the subscription models a moment later. A stored
login is not exposed, since pi marks a provider with one configured at registration. MEASURED
2026-10-04 on pi 1.0.1: the snapshot predated the pass in 2 of 36 starts run twelve at a time, and in 0
of 20 run one at a time (a throwaway probe of the call above), and in 2 of 24 runs of the native test's
host-socket case, six test processes at a time; its assertions read after one more awaited refresh and
passed in all 24. The mechanism is SOURCED: `ModelRuntime.registerNativeProvider`, `refresh` and
`runAvailabilityRefresh`. The fix belongs upstream:
pi's awaited refresh should resolve only once the newest availability pass has landed.

**A failed broker call** reads *"API key auth failed for provider openai-codex: OpenAI credential
service: …"*: pi's prefix, then [`brokerFailure`](../../packs/pi/extensions/yolo-openai-auth.js)'s words
(MEASURED 2026-10-04 by a throwaway probe on pi 1.0.1 with a failing stand-in client). The extension's
half is pinned against the real client by `TestPiOpenAIAuthWithAHostRouteKeepsTheClientsMessage`.

**Not yet checked, and needing a person.** On a host with no `openai-codex` entry: note
`sha256sum ~/.pi/agent/auth.json`; `yolo host -- pi` on the `codex` profile starts on `openai-codex`,
`/model` lists the subscription models and the footer shows *"(sub)"*; the hash is unchanged; plain `pi`
afterwards shows no `openai-codex` model. A start on another model whose `/model` then lists the
subscription is the race above.

**What stays as it was.** [§3.3](#33-a-host-pi-that-already-has-its-own-openai-codex-login): the native
provider carries yolo's oauth, so a stored own login still refreshes through the broker under
`yolo host`, and [OQ-3](#OQ-3) A's marker dispatch is still step 2. [§2](#2-secondary-symptom-catalog-refresh-failures-under-credential-scoping)'s
catalog noise is [OQ-2](#OQ-2)'s. **macos-user** takes the jail route: the host socket reaches an agent
only through `yolo host --`'s OpenAI prelaunch, the one caller of `openaiauthhost.Prepare` (SOURCED), and
a macos-user launch hands it only to the services and doorways it runs outside the sandbox.

---

## 6. What this does not decide

- **The jail's seeding.** The jail keeps writing its own `auth.json` before launch. Whether D2 makes
  that redundant is a later question.
- **A host `-p` moving pi's file-held selection.** That is [OQ-MM5](model-lists-and-pickers.md#OQ-MM5)'s
  subject; E would enable it and nothing here requires it.
- **The `host_management` default.** Retiring `assert` moves the default to `none`
  ([`config-ownership-and-promotion.md`](config-ownership-and-promotion.md)). That is not built, and
  no option here depends on it.
- **macos-user.** The directory map says that backend's merge lands in a sidecar; this doc changes
  nothing there.
- **A token OpenAI rejects before its recorded expiry.** That stays as
  [OQ-OA7](../reference/agent-credentials.md#oq-oa7) left it.
- **opencode's subscription wiring** ([§4](#4-the-opencode-parallel)).
- **Changes to pi upstream.** Every option here uses an interface pi 0.99.2 already ships.

---

## Open Questions

1. ✅ **OQ-1: How host Pi receives its initial `openai-codex` credential.**
   Pi counts `openai-codex` as configured only with a stored credential or a key method, and the host
   writes neither today. What should bridge this gap? The answer decides whether yolo's login ever
   enters the user's `auth.json`,
   and whether plain `pi` keeps a working subscription after a `yolo host` launch. Each option is in
   [§3.4](#34-for-the-authjson-seeding-the-option-space).

   - **A — Host prelaunch merges `auth.json` (amend NC-D37).**
   - **A′ — Marker-gated merge (amend NC-D37).**
   - **B — `yolo-openai-auth.js` seeds `auth.json` on extension load.**
   - **C — Require explicit `/login` once on the host.**
   - **D — Mark the provider configured with no file write.**
   - **E — A managed pi agent directory.**
   - **F — `yolo host apply` seeds under `host_management`.**

   <!-- vantage: question id=OQ-1 leaning="D2 — a native openai-codex provider whose key check answers only with the host socket: it writes nothing, so NC-D37 stands as written, and pi then acts at the host as it does in a jail. If a file write is preferred, A′ rather than A, because A deletes an own login." -->

   _Leaning:_ D2. It writes nothing, so NC-D37 stands as written and the host acts as a jail does at
   start. The plain-pi loss is what [OQ-HS3](host-notch-services.md#OQ-HS3) already accepts. A's
   stated basis, that `WritePiAuth` preserves existing host credentials, is false for its own key
   ([§3.4](#34-for-the-authjson-seeding-the-option-space), option A). If a file write is
   preferred, A′ rather than A.

   **Answer:**
   > **D2**, decided 2026-10-04 on its leaning under the maintainer's delegation of that day
   > ("make them and build it … adjust later"), open to his revision: a native `openai-codex` provider
   > whose key check answers only with the host socket, so nothing is written into the user's
   > `auth.json` and [NC-D37](../plans/notch-convergence.md#NC-D37) stands as written. Built
   > ([§5.1](#51-as-built-the-native-registration), [PH-D1](#PH-D1)).

2. 💬 **OQ-2: Handling catalog refresh failures for unscoped providers.**
   Under credential scoping, `models.json` rows for unselected providers make pi's model picker report
   refresh failures. Should yolo suppress unselected providers in `models.json`? The stakes are noise
   only: no option shows or hides a model the others do not
   ([§2](#2-secondary-symptom-catalog-refresh-failures-under-credential-scoping)). Options are in
   [§3.5](#35-for-catalog-refresh-noise).

   - **A — Only render providers with available credentials in `models.json`.** Omit a row whose key
     credential scoping withholds, at the host and in a jail.
   - **B — Leave `models.json` intact.** The warning stays, in the picker only.
   - **C — Leave `apiKey` off a built-in's row when the row's variable is pi's own name for it.**
     Silences the warning for those rows, with the same key when it is set.

   <!-- vantage: question id=OQ-2 leaning="C — leave apiKey off a built-in's row when the row's variable is pi's own name for it: measured to silence the refresh error with the same key when set, and every row stays, which A cannot manage at the host or across an attach." -->

   _Leaning:_ C. It is measured to silence the error and keeps every row, which A cannot do at the host
   or across an attach. A's basis was a startup network failure, and there is none. This may be an
   implementation decision rather than a ruling: if you agree it is the one sensible answer, it closes
   without one.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-3: Whose OpenAI login does host pi use when the user has their own?**
   A host pi may hold an `openai-codex` login of its own, made before yolo's extension was installed.
   Today the first refresh under `yolo host` replaces it with yolo's
   ([§3.3](#33-a-host-pi-that-already-has-its-own-openai-codex-login), which also has each option
   in full). The answer decides what [OQ-1](#OQ-1)'s
   options may do to that entry and which refresh pi runs.

   - **A — The user's own login wins.**
   - **B — yolo's login wins under `yolo host` with the `codex` profile.** It forces a file write,
     ruling out D for [OQ-1](#OQ-1).
   - **C — Two lineages, kept apart.** [OQ-1](#OQ-1)'s E.

   <!-- vantage: question id=OQ-3 leaning="A — the user's own login wins and refreshes through OpenAI by the marker dispatch; yolo's login serves only a missing or broker-marked entry. It fixes today's silent replacement without a file write and matches OQ-NC7 A; codex's precedent, OQ-OA3, is C's shape, which costs E's managed directory." -->

   _Leaning:_ A. It fixes today's silent replacement without a file write, so it composes with [OQ-1](#OQ-1)'s
   D2, and it matches [OQ-NC7](../plans/notch-convergence.md#OQ-NC7) A, where host claude keeps its own
   login. The codex precedent is C's shape instead: `yolo host -- codex` uses the broker through a
   managed home while a direct host codex stays untouched
   ([OQ-OA3](../reference/agent-credentials.md#oq-oa3)). A keeps that second half, and C would cost E,
   the largest build here. [OQ-KC1](keychain-from-a-jail.md#8-decision-ledger), ruled 2026-10-10,
   went the other way for Copilot: one login, the host Copilot's own, shared by the host and
   every jail.

   **Answer:**
   > _(empty — fill in when decided)_

---

## Decision Ledger

Every row is reversible. [PH-D1](#PH-D1) is [OQ-1](#OQ-1), which was the maintainer's, decided on its
leaning on 2026-10-04 under his delegation of that day; the rest are implementation decisions taken
building it.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="PH-D1"></a>PH-D1 | **[OQ-1](#OQ-1): D2.** Decided on its leaning under the maintainer's 2026-10-04 delegation (*"make them and build it … adjust later"*), open to his revision. Host pi gets a native `openai-codex` provider whose key check answers `oauth` only while the host socket is set, so pi counts the provider configured with nothing stored, and yolo writes nothing into the user's `auth.json`: [NC-D37](../plans/notch-convergence.md#NC-D37) stands as written | 2026-10-04 | [§3.4](#34-for-the-authjson-seeding-the-option-space) D, [§5](#5-build-order-and-the-tests-that-pin-each-step) step 3 | ✅ 2026-10-04 ([§5.1](#51-as-built-the-native-registration)); a host pi session not yet run on it |
| <a id="PH-D2"></a>PH-D2 | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* **The native registration only where the host socket is set; everywhere else the `ProviderConfig` as it was.** The alternative was the native provider everywhere, which a jail would notice in two ways: pi wraps only its own built-ins in its remote catalog refresh (`ModelRuntime.create`, SOURCED on pi 1.0.1), so a jail with no rendered list would lose that refresh, and pi's `/login` would gain an API-key entry, since it lists one for every key method. A jail gains nothing in return, having stored its login before pi starts. The socket is set only by `yolo host --`, so a jail's and a directly started pi's registration stay what they were, field for field | 2026-10-04 | [§5.1](#51-as-built-the-native-registration) | ✅ 2026-10-04; `TestPiOpenAIAuthJailRouteNeverRegistersNatively` fails with the gate removed |
| <a id="PH-D3"></a>PH-D3 | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* **The key method's `resolve` reuses the broker's view until five minutes before it expires**, pi's own refresh window for a stored login (pi-ai `auth/resolve.js`), instead of asking the client on every request. A token the broker replaced meanwhile is used until then, exactly as pi uses a stored login's, the jail's included | 2026-10-04 | [§5.1](#51-as-built-the-native-registration) | ✅ 2026-10-04; `TestPiOpenAIAuthHostRouteRegistersPisBuiltInAsANativeProvider` and `TestPiOpenAIAuthHostRouteAsksAgainForATokenNearItsEnd` |
| <a id="PH-D4"></a>PH-D4 | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* **Nothing to register natively falls back to the `ProviderConfig`**: a pi whose root's `ModelRuntime` has no `registerNativeProvider`, the method its loader hands a provider object to, or whose `providers/all` exports no `builtinProviders`, none for `openai-codex`, or one not serving `openai-codex-responses`. Such a pi keeps the host behavior it had before D2, `/login` included. The route is chosen by asking for the method, not by a throw: pi 0.80.10 exports the built-in, and its `registerProvider` took a provider object for a name, threw nothing, and failed applying it, which lost the registration and reported the extension as failed under the `try`/`catch` this row first named (MEASURED 2026-10-04). 0.81.0, the next release, has the method, as do 0.87.1, 0.99.2, 1.0.1 and 1.0.2 (MEASURED), and 0.81.0's changelog records the registration (SOURCED, its `CHANGELOG.md`) | 2026-10-04 | [§5.1](#51-as-built-the-native-registration) | ✅ 2026-10-04; `TestPiOpenAIAuthHostRouteFallsBackToTheProviderConfig`, whose cases fail with the method check or the api check removed |

---

## Appendix A: the probes

Run 2026-10-01 in a jail, against pi 0.99.2 and the tree at `2f579bb9`, by the review of this doc.
Every one is offline. P1 to P8 write only under a scratch directory, and W1 only under its
`t.TempDir()`, its test file removed again afterwards, so none is in the tree.

**E1, the installed versions and pi's agent-directory switches.** No `PI_` variable and no flag
relocates `auth.json` alone; `ENV_AGENT_DIR` and `ENV_SESSION_DIR` are `PI_CODING_AGENT_DIR` and
`PI_CODING_AGENT_SESSION_DIR` (`$PI/config.js:435-436`).

```console
$ PKG=~/.npm-global/lib/node_modules/@earendil-works/pi-coding-agent; PI=$PKG/dist
$ jq -r .version $PKG/package.json $PKG/node_modules/@earendil-works/pi-ai/package.json
0.99.2
0.99.2
$ rg -o -N --no-filename 'process\.env\.PI_[A-Z_]+|process\.env\[ENV_[A-Z_]+\]' $PI -g '*.js' -g '!**/bundle/**' | sort -u
process.env.PI_CLEAR_ON_SHRINK
process.env.PI_CODING_AGENT
process.env.PI_EXPERIMENTAL
process.env.PI_HARDWARE_CURSOR
process.env.PI_INSTALLER_API_BASE
process.env.PI_MANAGED_INSTALL_ROOT
process.env.PI_OFFLINE
process.env.PI_PACKAGE_DIR
process.env.PI_SHARE_VIEWER_URL
process.env.PI_SKIP_VERSION_CHECK
process.env.PI_STARTUP_BENCHMARK
process.env.PI_TELEMETRY
process.env.PI_TIMING
process.env[ENV_AGENT_DIR]
process.env[ENV_RADIUS_GATEWAY]
process.env[ENV_SESSION_DIR]
$ rg -o -N '"--[a-z-]+"' $PI/cli/args.js | sort -u | rg -i 'auth|agent-dir|config' || echo none
none
```

**P1 to P8, pi's own model runtime.** The command, then the fake `yolo` and the script it runs:

```console
$ REPO=/path/to/yolo-jail    # this checkout
$ mkdir -p /tmp/pi-review-probe/bin /tmp/pi-review-probe/home    # bin/yolo and probe.mjs below go here
$ cd /tmp/pi-review-probe && env -i PATH=/tmp/pi-review-probe/bin:/usr/bin:/bin:"$(dirname "$(readlink -f "$(command -v node)")")" \
    HOME=/tmp/pi-review-probe/home PI_CODING_AGENT_DIR=/tmp/pi-review-probe/home/.pi/agent \
    PI_DIST=$HOME/.npm-global/lib/node_modules/@earendil-works/pi-coding-agent/dist \
    YOLO_EXT=$REPO/packs/pi/extensions/yolo-openai-auth.js PROBE_ROOT=/tmp/pi-review-probe/cases \
    node probe.mjs
```

<details>
<summary><code>bin/yolo</code>, the stand-in for the credential client</summary>

```sh
#!/bin/sh
# Fake yolo for the probe: answers `internal openai-auth-client <action>` and logs each call.
echo "$*" >> "$PROBE_DIR/yolo-calls"
case "$3" in
  status) echo '{"logged_in":true,"login_required":false}' ;;
  login) echo '{}' ;;
  token) now=$(date +%s); echo "{\"access_token\":\"broker-tok-${PROBE_GEN:-1}\",\"expires_at\":$(( (now+3600)*1000 )),\"generation\":${PROBE_GEN:-1}}" ;;
  *) echo "unexpected: $*" >&2; exit 2 ;;
esac
```

</details>

<details>
<summary><code>probe.mjs</code></summary>

```javascript
// Offline probe of pi's model runtime. Scratch HOME and files, a fake `yolo` first on PATH,
// and a fetch that records the URL and throws: no CLI, no model call, no login.
import { mkdirSync, writeFileSync, readFileSync, existsSync, statSync, chmodSync, rmSync } from "node:fs";
import { join } from "node:path";

const { PI_DIST: PI, YOLO_EXT: EXT, PROBE_ROOT: ROOT } = process.env;
const { ModelRuntime } = await import(join(PI, "core/model-runtime.js"));
const { refreshModelCatalogs } = await import(join(PI, "modes/interactive/model-catalog-refresh.js"));
const { builtinProviders } = await import(join(PI, "../node_modules/@earendil-works/pi-ai/dist/providers/all.js"));

const fetches = [];
globalThis.fetch = async (url) => {
	fetches.push(String(url));
	throw new Error("probe: network disabled");
};
let n = 0;
function scratch(auth, models = { providers: {} }) {
	const dir = join(ROOT, `case${++n}`);
	rmSync(dir, { recursive: true, force: true });
	mkdirSync(dir, { recursive: true });
	process.env.PROBE_DIR = dir; // the fake yolo logs its calls here
	writeFileSync(join(dir, "auth.json"), JSON.stringify(auth, null, 2));
	writeFileSync(join(dir, "models.json"), JSON.stringify(models, null, 2));
	return dir;
}
const calls = (dir) => (existsSync(join(dir, "yolo-calls")) ? readFileSync(join(dir, "yolo-calls"), "utf8").trim().split("\n") : []);
const stored = (dir) => JSON.parse(readFileSync(join(dir, "auth.json"), "utf8"));
const mode = (dir) => (statSync(join(dir, "auth.json")).mode & 0o777).toString(8);
const runtimeFor = (dir) => ModelRuntime.create({ authPath: join(dir, "auth.json"), modelsPath: join(dir, "models.json") });
// The shipped extension's registration, applied as agent-session-services.js applies it.
async function yoloExtension(rt, extra = {}) {
	const regs = [];
	await (await import(`${EXT}?n=${n}`)).default({ registerProvider: (id, c) => regs.push([id, c]), on: () => {} });
	for (const [id, c] of regs) rt.registerProvider(id, { ...c, ...extra });
	await rt.refresh({ allowNetwork: false });
}
const local = { local: { baseUrl: "http://127.0.0.1:9/v1", api: "openai-completions", apiKey: "local", models: [{ id: "qwen3.8-27b" }] } };
const cerebras = (apiKey) => ({ cerebras: { baseUrl: "https://api.cerebras.ai/v1", api: "openai-completions", ...apiKey, models: [{ id: "probe-model" }] } });
const out = (label, value) => console.log(`${label}: ${JSON.stringify(value)}`);
const refreshAll = async (rt) => {
	fetches.length = 0;
	const r = await refreshModelCatalogs(rt, new AbortController().signal);
	return [...r.errors].map(([id, e]) => `${id}: ${e.message}`);
};

{ // P1: no openai-codex entry.
	const dir = scratch({}, { providers: local });
	const rt = await runtimeFor(dir);
	await yoloExtension(rt);
	out("P1 openai-codex configured / status", [rt.hasConfiguredAuth("openai-codex"), rt.getProviderAuthStatus("openai-codex")]);
	out("P1 local configured", rt.hasConfiguredAuth("local"));
	out("P1 available", rt.getAvailableSnapshot().map((m) => `${m.provider}/${m.id}`));
}
{ // P2: a stored entry that expired long ago.
	const dir = scratch({ "openai-codex": { type: "oauth", access: "a", refresh: "yolo-broker:1", expires: 1 } });
	const rt = await runtimeFor(dir);
	await yoloExtension(rt);
	out("P2 configured / yolo calls", [rt.hasConfiguredAuth("openai-codex"), calls(dir).length]);
}
{ // P3: /login through the extension, another provider beside it, file mode 0644.
	const dir = scratch({ zai: { type: "api_key", key: "zai-key" } });
	chmodSync(join(dir, "auth.json"), 0o644);
	const rt = await runtimeFor(dir);
	await yoloExtension(rt);
	const before = rt.hasConfiguredAuth("openai-codex");
	process.env.PROBE_GEN = "1";
	await rt.login("openai-codex", "oauth", { prompt: async () => "", notify: () => {}, signal: new AbortController().signal }, {});
	out("P3 configured before / after", [before, rt.hasConfiguredAuth("openai-codex")]);
	out("P3 isUsingSubscription / lock left", [rt.isUsingSubscription("openai-codex"), existsSync(join(dir, "auth.json.lock"))]);
	out("P3 stored", { keys: Object.keys(stored(dir)), entry: Object.keys(stored(dir)["openai-codex"]).sort(), refresh: stored(dir)["openai-codex"].refresh, mode: mode(dir) });
	out("P3 yolo calls", calls(dir));
}
{ // P4: an entry with 60 s left, refreshed at request time.
	const dir = scratch({ "openai-codex": { type: "oauth", access: "old", refresh: "yolo-broker:1", expires: Date.now() + 60_000 } });
	chmodSync(join(dir, "auth.json"), 0o644);
	const rt = await runtimeFor(dir);
	await yoloExtension(rt);
	process.env.PROBE_GEN = "2";
	const auth = await rt.getAuth("openai-codex");
	out("P4 token / stored refresh / mode", [auth?.auth?.apiKey, stored(dir)["openai-codex"].refresh, mode(dir)]);
	out("P4 yolo calls", calls(dir));
}
{ // P5: a row naming an unset variable, a built-in with no row (deepseek), a models.json-only provider.
	const rt = await runtimeFor(scratch({}, { providers: { ...local, ...cerebras({ apiKey: "${CEREBRAS_API_KEY}" }) } }));
	out("P5 errors / fetches", [await refreshAll(rt), fetches]);
	process.env.CEREBRAS_API_KEY = "probe-key";
	const rt2 = await runtimeFor(scratch({}, { providers: cerebras({ apiKey: "${CEREBRAS_API_KEY}" }) }));
	out("P5 variable set: errors / fetches", [await refreshAll(rt2), fetches]);
	delete process.env.CEREBRAS_API_KEY;
}
{ // P6: the same row with no apiKey.
	const rt = await runtimeFor(scratch({}, { providers: cerebras({}) }));
	out("P6 unset: errors / fetches / configured", [await refreshAll(rt), fetches.length, rt.hasConfiguredAuth("cerebras")]);
	process.env.CEREBRAS_API_KEY = "probe-key";
	const rt2 = await runtimeFor(scratch({}, { providers: cerebras({}) }));
	out("P6 set: configured / key / row baseUrl", [rt2.hasConfiguredAuth("cerebras"), (await rt2.getAuth("cerebras"))?.auth?.apiKey, rt2.getModel("cerebras", "probe-model")?.baseUrl]);
	delete process.env.CEREBRAS_API_KEY;
}
{ // P7 (D1): apiKey "!command" added to the extension's registration.
	const dir = scratch({});
	const counter = join(dir, "cmd-runs");
	const runs = () => (existsSync(counter) ? readFileSync(counter, "utf8").trim().split("\n").length : 0);
	const rt = await runtimeFor(dir);
	await yoloExtension(rt, { apiKey: `!echo run >> ${counter}; echo cmd-key` });
	out("P7 configured / status / runs", [rt.hasConfiguredAuth("openai-codex"), rt.getProviderAuthStatus("openai-codex"), runs()]);
	out("P7 getAuth key / runs / isUsingOAuth", [(await rt.getAuth("openai-codex"))?.auth?.apiKey, runs(), rt.isUsingOAuth("openai-codex")]);
}
// P8 (D2): pi's built-in openai-codex provider plus an auth.apiKey whose check answers "oauth".
async function d2(auth) {
	const dir = scratch(auth);
	const rt = await runtimeFor(dir);
	const base = builtinProviders().find((p) => p.id === "openai-codex");
	const count = { checks: 0, resolves: 0 };
	rt.registerNativeProvider({
		...base,
		auth: {
			...base.auth,
			apiKey: {
				name: "yolo shared login",
				check: async () => (count.checks++, { type: "oauth", source: "yolo shared login" }),
				resolve: async () => (count.resolves++, { auth: { apiKey: "broker-token" }, source: "yolo shared login" }),
			},
		},
	});
	await rt.refresh({ allowNetwork: false });
	return { dir, rt, count };
}
{
	const { dir, rt, count } = await d2({});
	out("P8 configured / isUsingOAuth / isUsingSubscription", [rt.hasConfiguredAuth("openai-codex"), rt.isUsingOAuth("openai-codex"), rt.isUsingSubscription("openai-codex")]);
	out("P8 resolves after the check", count.resolves);
	out("P8 getAuth key / resolves / auth.json", [(await rt.getAuth("openai-codex"))?.auth?.apiKey, count.resolves, readFileSync(join(dir, "auth.json"), "utf8")]);
	const own = { type: "oauth", access: "own-access", refresh: "own-refresh" };
	const { rt: rt2 } = await d2({ "openai-codex": { ...own, expires: Date.now() + 3_600_000 } });
	out("P8 own login: getAuth key", (await rt2.getAuth("openai-codex"))?.auth?.apiKey);
	const { rt: rt3 } = await d2({ "openai-codex": { ...own, expires: Date.now() + 60_000 } });
	fetches.length = 0;
	const err = await rt3.getAuth("openai-codex").then(() => "none", (e) => e.message);
	out("P8 own login near expiry: error / fetches", [err, fetches]);
}
```

</details>

Output:

```text
P1 openai-codex configured / status: [false,{"configured":false}]
P1 local configured: true
P1 available: ["local/qwen3.8-27b"]
P2 configured / yolo calls: [true,0]
P3 configured before / after: [false,true]
P3 isUsingSubscription / lock left: [true,false]
P3 stored: {"keys":["zai","openai-codex"],"entry":["access","expires","refresh","type"],"refresh":"yolo-broker:1","mode":"644"}
P3 yolo calls: ["internal openai-auth-client status","internal openai-auth-client token"]
P4 token / stored refresh / mode: ["broker-tok-2","yolo-broker:2","644"]
P4 yolo calls: ["internal openai-auth-client token"]
P5 errors / fetches: [["cerebras: Failed to resolve API key for provider \"cerebras\" from environment variable: CEREBRAS_API_KEY"],[]]
P5 variable set: errors / fetches: [["cerebras: probe: network disabled"],["https://pi.dev/api/models/providers/cerebras?types=chat%2Cimage%2Cclassifier","https://pi.dev/api/models/providers/cerebras?types=chat%2Cimage%2Cclassifier","https://pi.dev/api/models/providers/cerebras?types=chat%2Cimage%2Cclassifier"]]
P6 unset: errors / fetches / configured: [[],0,false]
P6 set: configured / key / row baseUrl: [true,"probe-key","https://api.cerebras.ai/v1"]
P7 configured / status / runs: [true,{"configured":true,"source":"models_json_command"},0]
P7 getAuth key / runs / isUsingOAuth: ["cmd-key",1,false]
P8 configured / isUsingOAuth / isUsingSubscription: [true,true,true]
P8 resolves after the check: 0
P8 getAuth key / resolves / auth.json: ["broker-token",1,"{}"]
P8 own login: getAuth key: "own-access"
P8 own login near expiry: error / fetches: ["OAuth refresh failed for openai-codex: OpenAI Codex token refresh error: probe: network disabled",["https://auth.openai.com/oauth/token"]]
```

**W1, `WritePiAuth` on a host-shaped home**, as a throwaway test in the package, from the worktree root:

```console
$ cp writepiauth_probe_test.go internal/openauthclient/zz_probe_test.go
$ go test -mod=vendor -count=1 -run TestProbeWritePiAuthOnAHostShapedHome -v ./internal/openauthclient/
$ rm internal/openauthclient/zz_probe_test.go
```

<details>
<summary><code>writepiauth_probe_test.go</code></summary>

```go
package openauthclient

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// WritePiAuth on a host-shaped agent directory: 0755, a symlinked auth.json, an own login.
func TestProbeWritePiAuthOnAHostShapedHome(t *testing.T) {
	root := t.TempDir()
	agent, target := filepath.Join(root, ".pi", "agent"), filepath.Join(root, "dotfiles", "pi-auth.json")
	for _, d := range []string{agent, filepath.Dir(target)} {
		if err := os.MkdirAll(d, 0o755); err != nil || os.Chmod(d, 0o755) != nil {
			t.Fatal(err)
		}
	}
	own := `{"zai":{"type":"api_key","key":"!pass show zai"},"openai-codex":{"type":"oauth","access":"own-access","refresh":"own-refresh","expires":4102444800000}}`
	path := filepath.Join(agent, "auth.json")
	if os.WriteFile(target, []byte(own), 0o644) != nil || os.Symlink(target, path) != nil {
		t.Fatal("setup")
	}
	if err := WritePiAuth(path, json.RawMessage(`{"access_token":"broker","expires_at":4102444800000,"generation":3}`)); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Lstat(path)
	di, _ := os.Stat(agent)
	after, _ := os.ReadFile(path)
	left, _ := os.ReadFile(target)
	t.Logf("symlink after: %v, file mode %o, dir mode %o", fi.Mode()&os.ModeSymlink != 0, fi.Mode().Perm(), di.Mode().Perm())
	t.Logf("auth.json:\n%s", after)
	t.Logf("dotfiles target:\n%s", left)
}
```

</details>

Output:

```text
=== RUN   TestProbeWritePiAuthOnAHostShapedHome
    zz_probe_test.go:31: symlink after: false, file mode 600, dir mode 700
    zz_probe_test.go:32: auth.json:
        {
          "openai-codex": {
            "type": "oauth",
            "access": "broker",
            "refresh": "yolo-broker:3",
            "expires": 4102444800000
          },
          "zai": {
            "type": "api_key",
            "key": "!pass show zai"
          }
        }
    zz_probe_test.go:33: dotfiles target:
        {"zai":{"type":"api_key","key":"!pass show zai"},"openai-codex":{"type":"oauth","access":"own-access","refresh":"own-refresh","expires":4102444800000}}
--- PASS: TestProbeWritePiAuthOnAHostShapedHome (0.00s)
PASS
ok  	github.com/mschulkind-oss/yolo-jail/internal/openauthclient	0.004s
```

**O1, opencode's gate** ([§4](#4-the-opencode-parallel)), from the strings of the installed binary:

```console
$ OC=~/.npm-global/lib/node_modules/opencode-ai; jq -r .version $OC/package.json
1.18.34
$ strings -n 6 $OC/node_modules/opencode-linux-x64/bin/opencode > oc.txt
$ rg -o 'if\(!\(yield\*Q\.get\(a\)\.pipe\(v\.orDie\)\)\)continue;if\(!n\.auth\.loader\)continue|if\(process\.env\.OPENCODE_AUTH_CONTENT\)try\{return JSON\.parse|we="https://auth\.openai\.com",rn=1455|ChatGPT Pro/Plus \(headless\)' oc.txt | sort | uniq -c
      2 ChatGPT Pro/Plus (headless)
      1 if(!(yield*Q.get(a).pipe(v.orDie)))continue;if(!n.auth.loader)continue
      1 if(process.env.OPENCODE_AUTH_CONTENT)try{return JSON.parse
      1 we="https://auth.openai.com",rn=1455
```

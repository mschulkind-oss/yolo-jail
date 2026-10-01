---
title: "Why host Pi cannot select OpenAI Codex — and how to seed its auth"
date: 2026-09-30
status: in-review
stage: DESIGN
next: "Rule OQ-1: How the host notch makes Pi's openai-codex provider configured, and OQ-3 with it"
tags: [pi, host, openai-auth, credentials, model-picker]
summary: "Pi gates a provider's models on a stored credential or a configured key. Inside a jail yolo seeds ~/.pi/agent/auth.json before launch; yolo host withholds that write under NC-D37, so openai-codex is unconfigured at the host notch although the broker socket and the extension are live. Pi also accepts a provider marked configured with no stored credential, which serves the host without writing the user's file."
---

# Why host Pi cannot select OpenAI Codex — and how to seed its auth

**Status:** 2026-10-01. Nothing built. Diagnosis measured live on the maintainer's host 2026-09-30, then
re-verified against pi 0.99.2 and the tree at `2f579bb9` with offline probes of pi's own model runtime.

> **In short.** Host pi lists no ChatGPT model because pi counts `openai-codex` as configured only
> when a credential is stored for it, and only the jail stores one. Pi also accepts a provider that
> declares itself configured with no stored credential, so the host can serve the subscription
> through the socket NC-D37 already hands it, without writing the user's `auth.json`.

**Why it matters.** Running `yolo host -- pi` under the `codex` profile boots pi into a fallback
model (`local/qwen3.8-27b`) and hides every ChatGPT subscription model from `/model`, which says
*"Only showing models from configured providers. Use /login to add providers."*

**The shape.** Three families: write the entry into the user's `auth.json` (by the launch, the
extension, `/login` or `yolo host apply`), give host pi an agent directory of yolo's own, as host
codex already has, or mark the provider configured from the extension and write nothing.

**Cost.** None for jail launches. The no-write family moves yolo's `openai-codex` registration
onto pi's native provider interface; the write families mutate the user's credential file.

**Start at [§1](#1-the-diagnosis-why-host-pi-ignores-openai-codex)** for the runtime gate, then
[§3.1](#31-what-nc-d37-protects) for what the host is protecting.

**Needs your ruling:** [OQ-1](#OQ-1), [OQ-2](#OQ-2), [OQ-3](#OQ-3).

**Reads with:** [`notch-convergence.md`](../plans/notch-convergence.md) (NC-D37's host prelaunch rule),
[`openai-auth-broker.md`](openai-auth-broker.md) (the one refresh owner, and the pi view it serves),
[`agent-directory-map.md`](agent-directory-map.md) (`auth.json`'s class and its host column),
[`pi-codex-provider-shadowing.md`](pi-codex-provider-shadowing.md) (Pi provider catalog layering),
[`model-lists-and-pickers.md`](model-lists-and-pickers.md) (ML-D3, MM-D23, and
[OQ-MM5](model-lists-and-pickers.md#OQ-MM5) on a host `-p`).

---

## 1. The Diagnosis: Why host Pi ignores OpenAI Codex

**Evidence tags used in this doc.** **MEASURED**: run and observed, either live on the maintainer's
host (2026-09-30) or by an offline probe. **SOURCED**: read at the cited file and line.
**INFERRED**: reasoned from sourced code and not run.

**The probes** *(throwaway, not in the tree)* import pi's own `ModelRuntime` from the installed
package. They run with a scratch `HOME`, scratch `auth.json` and `models.json`, and a `fetch` that
records the URL and throws. They start no CLI, call no model and log nothing in. A test built from
them is a build step ([§5](#5-build-order-and-the-tests-that-pin-each-step)).

**Paths.** pi-coding-agent and its pi-ai dependency are both 0.99.2 (MEASURED, `package.json`).
`$PI` is `@earendil-works/pi-coding-agent/dist` and `$AI` is that package's
`node_modules/@earendil-works/pi-ai/dist`. The `pi` binary runs `$PI/bundle/cli.js`, a bundle that
keeps these identifiers. Upstream's v0.87.1 sources match at every point checked, and upstream main,
read 2026-10-01, keeps `checkProviderAuth` unchanged (SOURCED).

### 1.1 The gate, step by step

1. **The extension layer** (SOURCED). [`yolo-openai-auth.js`](../../packs/pi/extensions/yolo-openai-auth.js#L305-L330)
   registers `openai-codex` with `pi.registerProvider("openai-codex", { ... })`. Its model list comes
   from `~/.pi/agent/yolo-openai-codex-models.json` ([L110](../../packs/pi/extensions/yolo-openai-auth.js#L110)),
   and its `oauth` block wires `login`, `refreshToken` and `getApiKey` to the yolo auth broker
   ([L314-L320](../../packs/pi/extensions/yolo-openai-auth.js#L314-L320)).
2. **Registrations land before selection** (SOURCED, `$PI/core/agent-session-services.js:72-111`).
   Pi applies every extension registration and then awaits `modelRuntime.refresh({ allowNetwork: false })`
   before it creates the session.
3. **The check** (SOURCED, `$AI/models.js:263-284`). Each availability pass asks `checkProviderAuth`
   per provider:
   ```javascript
   checkProviderAuth(provider, credential, signal) {
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
5. **No callback probes availability** (MEASURED). A stored oauth entry with `expires: 1` counts as
   configured, and the probe's `login`, `refresh` and `getApiKey` counters all stay at zero. Expiry is
   not part of the check.
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
     `{ configured: false }` (probe: the same result with an empty `auth.json`).
   - `findInitialModel` keeps the saved default (`openai-codex/gpt-6.1-sol`) only when
     `hasConfiguredAuth` is true, and otherwise takes the first model of `getAvailableSnapshot()`.
     A restored session applies the same gate (`$PI/core/sdk.js:92-100`).
   - `local` declares the literal key `"local"`, which counts as configured (probe: `local: true`),
     so pi starts on `local/qwen3.8-27b`.
   - The `/model` picker lists `getAvailableSnapshot()` (`$PI/modes/interactive/components/model-selector.js:103-109`),
     so no `openai-codex` model appears.

### 1.2 The live confirmation

Running `/login` in host pi and choosing *OpenAI Codex (yolo shared login)* logged in at once with
no browser, saved the credential to `~/.pi/agent/auth.json`, and made the `openai-codex` models
available (MEASURED live). The path is SOURCED: `showLoginDialog` → `modelRuntime.login(id, "oauth", …)`
(`$PI/modes/interactive/interactive-mode.js:5142-5165`) → the composer's adapter around the
extension's `login` (`provider-composer.js:183-193`) → [`brokerLogin`](../../packs/pi/extensions/yolo-openai-auth.js#L93-L99),
which asks `status`, logs in only when it must, and returns `brokerToken`. Pi writes the result
under its own lock and marks the provider configured. A probe with a fake broker reproduced it:
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
   the selection ([`provider-credential-scope.md`](provider-credential-scope.md)) and withholds
   `CEREBRAS_API_KEY`, `DEEPSEEK_API_KEY` and `OPENROUTER_API_KEY`.
3. **What fails, and where** (SOURCED; MEASURED by probe).
   - **No network call is made and nothing returns 401.** For a row whose `apiKey` names an unset
     variable, the composed resolver throws locally (`provider-composer.js:276-278`), and the catalog
     refresh records that as the provider's error (`$AI/models.js:200`, `:209-212`). Probe: the error
     *Failed to resolve API key for provider "cerebras" from environment variable: CEREBRAS_API_KEY*,
     with no `fetch` at all.
   - **Only a row that overlays one of pi's built-in providers can fail.** Every built-in except
     `radius` carries a remote-catalog refresh hook (`model-runtime.js:87-91`). A built-in with no row
     and no key is skipped silently (probe: `deepseek` with no row, no error). A provider that exists
     only in `models.json` has no hook (`provider-composer.js:384`; probe: `local`, no error).
   - **A keyed refresh asks pi.dev, not the provider, and sends no credential** (MEASURED: three
     attempts at `https://pi.dev/api/models/providers/cerebras?types=…`; SOURCED
     `remote-catalog-provider.js:76-85`).
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
  `if agent == "pi" { return launch }`. NC-D37 carried that forward and gave the reason.
- **The broker design states the rule for codex only.** Its user experience section says *"Yolo never
  rewrites the user's ordinary `~/.codex/auth.json` or silently changes a directly launched host Codex"*
  ([`openai-auth-broker.md`](openai-auth-broker.md#1-user-experience)). Its ledger rules
  *"`yolo host -- codex` shares the broker through a managed Codex home; direct host Codex remains
  untouched"* ([OQ-OA3](openai-auth-broker.md#8-decision-ledger)). For pi it says only that
  *"`yolo host -- pi` and the Pi extension use the private socket directly"*
  ([§4](openai-auth-broker.md#4-backend-transport)). Nothing says how pi's first `auth.json` entry
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
  ([`openai-auth-broker.md` §2](openai-auth-broker.md#2-one-writer-and-two-views)).
- **Pi refreshes it itself** (SOURCED; MEASURED). At request time, with under five minutes left, pi
  runs `oauth.refresh` under its lock with a 15 s timeout (`$AI/auth/resolve.js:46-92`). That is the
  extension's [`refreshToken`](../../packs/pi/extensions/yolo-openai-auth.js#L318), which asks the
  broker for the current generation. A catalog refresh does the same once the entry has expired
  (`$AI/models.js:230-245`). Pi writes the result back before it releases the lock
  (`$PI/core/auth-storage.js:378-395`). Probe: an entry with 60 s left gave a new token and a stored
  `refresh: yolo-broker:2` after one refresh call.
- **Nothing flows back to the broker, and nothing needs to.** The broker is the one writer of the
  lineage. It returns a cached generation or performs one upstream refresh under its machine lock,
  however many pi files ask.
- **A seeded token goes stale only outside `yolo host`.** Plain `pi` still counts the provider as
  configured, because the check ignores expiry ([§1.1](#11-the-gate-step-by-step) step 5). Requests
  work on the stored access token until it expires. The next refresh then fails with
  [`brokerFailure`](../../packs/pi/extensions/yolo-openai-auth.js#L34-L42)'s advice to launch
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
  symlink (`auth-storage.js:14-32`; MEASURED: a 0644 file stayed 0644 through pi's login and refresh).
  [`WritePiAuth`](../../internal/openauthclient/pi.go#L38-L44) chmods `~/.pi/agent` to 0700 on every
  call and replaces the file by rename ([`codex.go`](../../internal/openauthclient/codex.go#L74-L111)).
  That resets the mode to 0600, drops ACLs and turns a symlinked `auth.json` into a regular file,
  leaving a dotfiles target with the stale copy. It is harmless in a jail and a mutation of the user's
  file at the host.

### 3.3 A host pi that already has its own openai-codex login

**This is broken today, whichever option is chosen** (SOURCED). An extension's `oauth` replaces pi's
built-in one (`provider-composer.js:296`), and yolo's `refreshToken` ignores the credential it is
handed and asks the broker ([L318](../../packs/pi/extensions/yolo-openai-auth.js#L318)). With yolo's
extension installed in `~/.pi/agent/extensions/`:

- **`/login` for `openai-codex` is yolo's broker login**, so the user cannot log pi into their own
  ChatGPT account at all.
- **An own login is replaced at its first refresh under `yolo host` with the `codex` profile.** Pi
  writes the broker's view over the user's refresh token. If the two accounts differ, that is a silent
  account switch, and the user's own login cannot be restored while the extension is loaded.
- **Under a direct launch, or `yolo host -p zai -- pi`, that refresh fails.** In the `-p zai` case the
  message wrongly says pi *"was not started through `yolo host`"*: no OpenAI prelaunch is declared, so
  the launch hands pi no socket, and [`brokerFailure`](../../packs/pi/extensions/yolo-openai-auth.js#L34-L42)
  cannot tell that case from a direct launch.

[OQ-HS3](host-notch-services.md#OQ-HS3) covers the direct launch. It does not obviously cover a
correct launch replacing the user's account.

**A buildable fix** *(the marker dispatch, coined here)* (INFERRED). `refreshToken` sends a credential
whose `refresh` matches the broker's marker (`^yolo-broker:[1-9][0-9]*$`,
[`codex.go`](../../internal/openauthclient/codex.go#L13)) to the broker, and any other credential to
pi's built-in `openai-codex` refresh. The extension already imports `builtinProviders`
([L143-L170](../../packs/pi/extensions/yolo-openai-auth.js#L143-L170)). Pi supports one oauth method
per provider, so `/login` stays yolo's broker login. An own login made before the extension arrived
survives; a new one cannot be made while the extension is loaded. Whether the user's login or yolo's
should win at all is [OQ-3](#OQ-3).

What each option does to an own login is a column of the table in [§3.4](#34-for-the-authjson-seeding-the-option-space).

### 3.4 For the auth.json seeding: the option space

The first three are the maintainer's; the rest come from reading pi's runtime and the codex precedent.

#### A — Host prelaunch seeding with `WritePiAuth` (amend NC-D37)

`openaiauthhost.Prepare` calls `WritePiAuth` on `~/.pi/agent/auth.json` for the pi view.

- **The user sees** the codex default at start, with nothing said unless a launch line is added.
- **It writes** the user's `auth.json` on every `codex`-profile launch, with the side effects of
  [§3.2](#32-what-a-seeded-entry-does-afterwards-refresh-rotation-the-lock)'s last bullet.
- **It ignores a relocated agent directory.** The prelaunch path is fixed at `.pi/agent/auth.json`
  ([`derive.lua`](../../packs/pi/derive.lua#L1362)), while pi honors `PI_CODING_AGENT_DIR`, which a host
  shell passes through to pi.
- **Lock:** pi's, as above. The host would need the jail's exit-75 handling.
- **An own login is deleted** on every `codex`-profile launch (MEASURED 2026-09-30 by a scratch test of
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
  (`:15`), and the extension already imports that root ([L253](../../packs/pi/extensions/yolo-openai-auth.js#L253)).
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
    (`:276-281`; `resolve-config-value.js:192-201`; pi's `docs/models.md` says so). Probe: configured,
    source `models_json_command`, the command did not run during the check and did run at `getAuth`.
  - A stored oauth entry still wins over the key (`$AI/auth/resolve.js:24-28`), so a jail and an own
    login behave as today.
  - **Costs:** the command runs through `execSync`, blocking pi's event loop, with a 10 s timeout and
    stderr discarded (`resolve-config-value.js:159-171`), which loses `brokerFailure`'s advice.
    `openai-auth-client token` prints a JSON view, so it would need a raw-token mode. `isUsingOAuth`
    becomes false (MEASURED), so the footer shows a dollar cost without *"(sub)"*
    (`$PI/modes/interactive/components/footer.js:162-167`), and `/login` lists an extra API-key method
    (`interactive-mode.js:4739-4747`). The extension must add the key only when the host socket is
    set, or plain pi counts the provider configured and fails every request.
- **D2 — A native provider registered with `pi.registerProvider(provider: Provider)`.**
  - The overload is SOURCED in `$PI/core/extensions/types.d.ts:1313` and applied by
    `registerNativeProvider` (`model-runtime.js:626-635`). Pi's own docs recommend native auth for
    custom resolution (`docs/custom-provider.md:83`).
  - `auth.apiKey` takes an async `check()` and `resolve()` (`$AI/auth/types.d.ts:170-196`), and a check
    may answer with type `"oauth"` (`:94-97`).
  - **MEASURED** (2026-10-01): the built-in `openai-codex` provider spread with an added `auth.apiKey`
    whose `check` returns `{ type: "oauth", source: "yolo shared login" }`, over an empty `auth.json`:
    configured, `isUsingOAuth` and `isUsingSubscription` both true, no `resolve` during the check, and
    `getAuth` returned the broker's token while `auth.json` stayed `{}`.
  - The same probe with a stored own login: `getAuth` returned the own access token, because a stored
    oauth entry wins. Near expiry pi refreshed it against `https://auth.openai.com/oauth/token`, the
    built-in refresh, not the broker. Which oauth the native provider carries is therefore
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
no auth-only variable or flag (MEASURED: every `process.env` read in `$PI` and the flag list in
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
  ([L110](../../packs/pi/extensions/yolo-openai-auth.js#L110)), not from pi's agent directory. A link
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
| A | every codex launch, by rename | amends | deleted | configured until expiry | small |
| A′ | when absent, in place | amends | kept | configured until expiry | small |
| B | when absent, from pi | defeats its reason | kept | configured until expiry | small, through pi's runtime |
| C | only on the user's `/login` | as is | overwritten on request | configured until expiry | tiny |
| D1 | never | as is | kept, but see [§3.3](#33-a-host-pi-that-already-has-its-own-openai-codex-login) | unconfigured | small, with UI costs |
| D2 | never | as is | kept, but see [§3.3](#33-a-host-pi-that-already-has-its-own-openai-codex-login) | unconfigured | medium (native provider) |
| E | never (yolo's own file) | amends | untouched | unconfigured | large |
| F | at apply time | moves the host-management line | as A or A′ | configured until expiry | small |

### 3.5 For catalog refresh noise

1. **Filter `models.json` by the active profile.** Write a row only when its credential is delivered
   this launch, or its key is a literal such as `local`.
   - **At the host** (INFERRED): `models.json` is rendered by `yolo host apply` from the user-scope
     composition, not per launch, so it cannot follow each launch's `-p`. Rewriting it per launch would
     race concurrent launches with different `-p`, the hazard [MM-D27](model-lists-and-pickers.md#MM-D27)
     solves for codex's menu. A direct launch, and a key from the user's own shell, need the rows.
   - **In a jail** (INFERRED): `models.json` is rendered at boot, while an attach rewrites the agent's
     env file whole ([`active-provider-sets.md`](active-provider-sets.md)), so a key an attach delivers
     would find no row. [AP-D16](active-provider-sets.md#AP-D16) assumes rows exist for providers
     outside the set.
2. **Accept the warning as harmless.** Pi degrades to cached models, and the warning appears only in
   the picker.
3. **Leave `apiKey` off a row for one of pi's built-in providers when the row's variable is pi's own
   name for it** — `cerebras`, `deepseek` and `openrouter` here (`$AI/env-api-keys.js:85-93`). Pi's
   inherited key lookup then answers nothing when the variable is unset and the same key when it is
   set, and every row stays. **MEASURED** (2026-10-01, probe): a `cerebras` row with no `apiKey`
   refreshed with no error and no `fetch` while `CEREBRAS_API_KEY` was unset, and was configured with
   the same key once it was set. A built-in whose configured variable has another name keeps the
   warning.

**The mid-session switching cost**, which the first draft gave as option 2's reason, is small for
every option. Pi's environment is fixed when it starts, so a withheld key cannot arrive mid-session,
and a profile change is a new launch. The one mid-session path is `/login` storing a key for that
provider: a stored key makes it configured (`provider-composer.js:244-251`), and under option 1 it
would lose its row's `baseUrl` and model overrides.

---

## 4. The opencode parallel

opencode 1.18.34 has the same kind of gate (SOURCED from the binary's strings; the rest INFERRED):

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

1. **The catalog noise ([OQ-2](#OQ-2), option 3).** The pi models derive leaves `apiKey` off a
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
   name and refusal move onto it.
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
  [OQ-OA7](openai-auth-broker.md#OQ-OA7) left it.
- **opencode's subscription wiring** ([§4](#4-the-opencode-parallel)).
- **Changes to pi upstream.** Every option here uses an interface pi 0.99.2 already ships.

---

## Open Questions

1. 💬 **OQ-1: How host Pi receives its initial `openai-codex` credential.**
   Pi counts `openai-codex` as configured only with a stored credential or a key method, and the host
   writes neither today. The answer decides whether yolo's login ever enters the user's `auth.json`,
   and whether plain `pi` keeps a working subscription after a `yolo host` launch. Each option is in
   [§3.4](#34-for-the-authjson-seeding-the-option-space).

   - **A — Host prelaunch merges `auth.json` (amend NC-D37).** `WritePiAuth` on every
     `codex`-profile launch. Deletes an own login and replaces a symlinked file.
   - **A′ — Marker-gated merge (amend NC-D37).** Writes in place only when the entry is absent or is
     already a broker view.
   - **B — `yolo-openai-auth.js` seeds `auth.json` on extension load.** Writes through pi's exported
     runtime, so under pi's own lock.
   - **C — Require explicit `/login` once on the host**, with a line that says so. No amendment; no jail
     parity.
   - **D — Mark the provider configured with no file write.** D2, a native provider with a key check
     gated on the host socket, keeps the subscription label and blocks nothing. No amendment.
   - **E — A managed pi agent directory**, as host codex has. Two lineages; the user's other pi logins
     are absent under `yolo host`.
   - **F — `yolo host apply` seeds under `host_management`.** Moves the host-management credential
     line.

   <!-- vantage: oq id=OQ-1 leaning="D2 — a native openai-codex provider whose key check answers only with the host socket: it writes nothing, so NC-D37 stands as written, and pi then acts at the host as it does in a jail. If a file write is preferred, A′ rather than A, because A deletes an own login." -->

   _Leaning:_ D2. It writes nothing, so NC-D37 stands as written and the host acts as a jail does at
   start. The plain-pi loss is what [OQ-HS3](host-notch-services.md#OQ-HS3) already accepts. A's
   stated basis, that `WritePiAuth` preserves existing host credentials, is false for its own key
   ([§3.4](#34-for-the-authjson-seeding-the-option-space), option A). If a file write is
   preferred, A′ rather than A.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-2: Handling catalog refresh failures for unscoped providers.**
   Under credential scoping, `models.json` rows for unselected providers make pi's model picker report
   refresh failures. The stakes are noise only: no option shows or hides a model the others do not
   ([§2](#2-secondary-symptom-catalog-refresh-failures-under-credential-scoping)). Options are in
   [§3.5](#35-for-catalog-refresh-noise).

   - **A — Only render providers with available credentials in `models.json`.** Cannot follow a
     launch's `-p` at the host, and loses rows an attach or a direct launch needs.
   - **B — Leave `models.json` intact.** The warning stays, in the picker only.
   - **C — Leave `apiKey` off a built-in's row when the row's variable is pi's own name for it.**
     Silences the warning for those rows, with the same key when it is set. Other rows keep it.

   <!-- vantage: oq id=OQ-2 leaning="C — leave apiKey off a built-in's row when the row's variable is pi's own name for it: measured to silence the refresh error with the same key when set, and every row stays, which A cannot manage at the host or across an attach." -->

   _Leaning:_ C. It is measured to silence the error and keeps every row, which A cannot do at the host
   or across an attach. A's basis was a startup network failure, and there is none. This may be an
   implementation decision rather than a ruling: if you agree it is the one sensible answer, it closes
   without one.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-3: Whose OpenAI login does host pi use when the user has their own?**
   A host pi may hold an `openai-codex` login of its own, made before yolo's extension was installed.
   Today the first refresh under `yolo host` replaces it with yolo's
   ([§3.3](#33-a-host-pi-that-already-has-its-own-openai-codex-login)). The answer decides what [OQ-1](#OQ-1)'s
   options may do to that entry and which refresh pi runs.

   - **A — The user's own login wins.** A stored own login keeps serving and refreshes through OpenAI,
     using the marker dispatch. yolo's login serves only a missing or broker-marked entry. `/login`
     stays yolo's, so a new own login cannot be made while the extension is loaded, and the extension
     says when an own login is serving.
   - **B — yolo's login wins under `yolo host` with the `codex` profile.** An own login is replaced, as
     today, but disclosed. Pi's resolve order puts a stored credential first, so B forces a file write,
     ruling out D for [OQ-1](#OQ-1).
   - **C — Two lineages, kept apart.** [OQ-1](#OQ-1)'s E: the managed directory serves `yolo host`, and the
     user's `~/.pi/agent` keeps its own login untouched.

   <!-- vantage: oq id=OQ-3 leaning="A — the user's own login wins and refreshes through OpenAI by the marker dispatch; yolo's login serves only a missing or broker-marked entry. It fixes today's silent replacement without a file write, and matches OQ-NC7 A and the broker design's untouched direct codex." -->

   _Leaning:_ A. It fixes today's silent replacement without a file write, so it composes with [OQ-1](#OQ-1)'s
   D2. It matches [OQ-NC7](../plans/notch-convergence.md#OQ-NC7) A, where host claude keeps its own
   login, and the broker design, where a direct host codex stays untouched. The nearest unruled
   question, [OQ-KC1](keychain-from-a-jail.md#OQ-KC1), leans the same way: a yolo-owned login, kept
   separate from the host's own.

   **Answer:**
   > _(empty — fill in when decided)_

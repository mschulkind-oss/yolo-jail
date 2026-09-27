---
title: "Credential Sources vs. Environment Sources — Explicit Secret Boundaries vs. Automatic Variable Withholding"
date: 2026-09-27
status: in-review
tags: [providers, profiles, credentials, env-sources, security, ergonomics, cli]
summary: "Evaluate replacing automatic credential withholding from env_sources with a dedicated credential_sources configuration key, analyzing developer surprise, two-file dotenv sprawl, leak regressions, and ad-hoc shell execution."
vantage:
  status-chip: true
---

# Credential Sources vs. Environment Sources — Explicit Secret Boundaries vs. Automatic Variable Withholding

**Status:** DESIGN, 2026-09-27. Evidence verified at `d6f875df`. Nothing built.

> **In short.** Yolo currently conflates general environment variables with provider
> credentials inside `env_sources`, causing the credential gate to automatically
> withhold any variable matching a provider's `api_key_env_name` from processes that
> did not select that provider. Moving credentials into a dedicated `credential_sources`
> key aligns the mental model so that `env_sources` is delivered to all processes
> unconditionally while `credential_sources` is filtered per-agent; however, it
> introduces configuration sprawl, requires strict enforcement against credential
> leakage in `env_sources`, and leaves ad-hoc shell debugging unsolved without an
> explicit credential grant flag.

**Why it matters.** A developer adding `ZAI_API_KEY=...` or `DEEPSEEK_API_KEY=...` to
an environment file expects `yolo host -- bash -c 'echo $ZAI_API_KEY'` to print the
value. Instead, yolo withholds it from `bash` because `bash` is not an agent whose
profile selected `zai`. This surprises developers: they configured an "environment
source," but the variable was filtered out of their command's environment. While the
security intent (preventing cross-agent credential leakage) is sound, doing it by
retroactively censoring general environment files creates confusion over whether
variables were loaded at all.

**The shape.** Partition configuration into:
1. `env_sources`: General environment variables (e.g. `PORT`, `DEBUG`, `NODE_ENV`)
   delivered unconditionally to all processes.
2. `credential_sources`: Explicit authentication secrets partitioned by the
   Credential Gate (`packload.ScopeCredentials`) and routed strictly to selected agents.
3. Enforcement: A validation gate refusing known provider credentials in `env_sources`,
   preventing accidental security regressions.
4. Ad-hoc Delivery: A `--with-credentials <provider...>` (or profile mapping) flag
   allowing non-agent commands like `bash` to explicitly request credentials on the host.

**Cost.** Requires developers to manage two separate environment sources (`.env` vs.
`.credentials.env` or config blocks); breaks existing `env_sources` setups that store
API keys alongside general variables; requires migration tooling; adds an extra flag
when running ad-hoc shell commands with provider credentials.

**Start at [§3](#3-the-mental-model-mismatch-why-withholding-from-env_sources-surprises)** —
the collision between general environment files and provider credential claims.

**Needs your ruling:** [OQ-1](#OQ-1), [OQ-2](#OQ-2), [OQ-3](#OQ-3), [OQ-4](#OQ-4).

**Reads with:** [`credential-sources-separation-plan.md`](credential-sources-separation-plan.md)
(the companion implementation sketch),
[`provider-credential-scope.md`](provider-credential-scope.md)
(the design of the Credential Gate built on 2026-09-26),
[`../reference/agent-credentials.md`](../reference/agent-credentials.md)
(canonical reference for agent credentials and boundaries in yolo-jail).

---

## Terms, in plain words

- **`env_sources`**. The existing configuration key in `yolo-jail.jsonc` that hydrates
  process environment from dotenv files and inline maps.
- **`credential_sources`** *(proposed here)*. A separate configuration key dedicated
  exclusively to provider API keys, tokens, and authentication secrets.
- **Credential Gate (`packload.ScopeCredentials`)**. The subsystem that inspects
  hydrated variables, matches them against provider declarations (`api_key_env_name`),
  and isolates them into per-agent environment files (`~/.config/yolo-agent-env/<agent>.sh`).
- **Claimed Variable**. An environment variable whose identifier matches an active
  provider's declared `api_key_env_name` (e.g. `ZAI_API_KEY`, `OPENROUTER_API_KEY`).
- **Shared Environment (`yolo-user-env.sh`)**. The file sourced by `/etc/profile` and
  `.bashrc` in container jails, intended for variables visible to every shell and process.
- **Ad-hoc Process**. Any command or shell (such as `bash`, `curl`, `jq`, `python`)
  that is not an agent pack's recognized binary.
- **Leak Regression**. Re-introducing the security vulnerability closed on 2026-09-26
  where one agent or shell process can read credentials intended for another provider.

---

## 1. The Incident and Problem Statement

On 2026-09-27, running a basic host command to verify a provider API key:

```console
$ yolo host -- bash -c 'echo $ZAI_API_KEY'
yolo-jail 0.10.0+653.gd6f875df | linux/x86_64 | host
yolo host: Credential scope: a provider's credential reaches only the agents whose profile selects it.
  DEEPSEEK_API_KEY (provider deepseek): withheld from every process — no agent in this launch selected it
  OPENROUTER_API_KEY (provider openrouter): withheld from every process — no agent in this launch selected it
  ZAI_API_KEY (provider zai): withheld from every process — no agent in this launch selected it
  CEREBRAS_API_KEY (provider cerebras): withheld from every process — no agent in this launch selected it
```

The command printed an empty string. The developer had added `ZAI_API_KEY` to their
`env_sources` file, expecting it to be available. Instead:

1. Yolo identified that `ZAI_API_KEY` was claimed by the `zai` provider.
2. The invoked command was `bash`, which is not an agent with an active profile
   selecting `zai`.
3. The Credential Gate withheld `ZAI_API_KEY` from `bash`'s process environment.
4. Yolo emitted four lines of disclosure warnings explaining why the keys were withheld.

The developer found this behavior surprising:
> *"I find it confusing that you can add something to the environment sources of a jail
> and then start up the jail and then it's not there because it's been filtered out.
> I think instead we should move credentials into their own files that are very clearly
> marked as credentials, basically the same thing as environment sources, but it'll be
> clear that they're credentials and not expected to all make it into the environment."*

---

## 2. Load-Bearing Principles

- **P1. Explicit declaration beats implicit interception.**
  When a developer places a file in `env_sources`, their expectation is that its
  contents will be in their environment. Intercepting and withholding variables based
  on external pack provider definitions creates a spooky action-at-a-distance.
- **P2. No cross-agent credential leakage ([`OQ-BR4`](provider-credential-scope.md#OQ-BR4) holds).**
  The maintainer's ruling on 2026-09-25 stands firm: credentials scoped to one
  provider must not be ambiently visible to other agents. Any design change must not
  re-introduce the vulnerability where Pi or Claude can ambiently read each other's keys.
- **P3. Least astonishment in interactive workflows.**
  Running `yolo host -- <cmd>` or launching a debug subshell inside a jail should behave
  predictably. If a secret is withheld, the contract must make it obvious before launch
  why it was withheld.
- **P4. Non-secret environment must remain ubiquitous.**
  General environment variables (`DEBUG`, `NODE_ENV`, `HTTP_PROXY`, `DATABASE_URL`)
  must never be subject to provider-scoping or withholding.

---

## 3. The Mental Model Mismatch: Why Withholding from `env_sources` Surprises

The current architecture and the developer's mental model diverge in a fundamental way:

```
Developer Mental Model:
  "env_sources is where I put variables for my jail."
  Input: .env (contains PORT=8080, ZAI_API_KEY=sk-...)
  Expectation: All processes inside the jail (and yolo host) have $PORT and $ZAI_API_KEY.

Current Yolo Implementation:
  "env_sources is a raw stream that I will censor against provider claims."
  Input: .env
  Pass 1: Detect that 'ZAI_API_KEY' matches packs/zai/pack.json:api_key_env_name.
  Pass 2: Strip 'ZAI_API_KEY' from shared yolo-user-env.sh.
  Pass 3: Deliver 'ZAI_API_KEY' only to pi.sh (if pi selected zai).
  Outcome: bash, subshells, and non-profiled agents receive $PORT, but NOT $ZAI_API_KEY.
```

### Why this produces surprise:
1. **Reactive, pack-driven classification:** The user never marked `ZAI_API_KEY` as a
   secret. Yolo classified it as a secret solely because a selected pack (`packs/zai`)
   declared `"api_key_env_name": "ZAI_API_KEY"`. If the pack had not been selected,
   the variable would have passed through to `bash`!
2. **Conflation of secrets with configuration:** Standard industry practice uses `.env`
   for both settings and keys. Yolo attempts to separate them post-hoc by variable name.
3. **The ad-hoc shell dilemma:** Developers frequently run `yolo host -- bash` or
   open an interactive container shell to test curl commands, run CLI scripts, or
   inspect environment variables. Because `bash` does not have an agent profile, all
   provider credentials are automatically withheld.

---

## 4. The Proposed Architecture: Dedicated `credential_sources`

To resolve the mental model collision, we evaluate splitting environment ingestion into
two explicit channels:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ yolo-jail.jsonc                                                             │
│                                                                             │
│   "env_sources": [                                                          │
│     ".env"                   ──► Unfiltered: goes to ALL processes          │
│   ],                                                                        │
│                                                                             │
│   "credential_sources": [                                                   │
│     ".credentials.env"       ──► Gated: goes ONLY to matching agents        │
│   ]                                                                         │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 4.1 Channel 1: `env_sources` (General Environment)
- **Role:** General runtime configuration (`PORT`, `LOG_LEVEL`, `DEBUG`, `DATABASE_URL`).
- **Delivery:** Unconditional. Written to `~/.config/yolo-user-env.sh` in jails and
  inherited directly by `yolo host -- <cmd>`.
- **Filtering:** **None.** No variable declared in `env_sources` is ever withheld by
  the Credential Gate.
- **Invariant Guard:** To prevent developers from accidentally putting API keys into
  `env_sources` and leaking them to all processes, yolo validates `env_sources` against
  active provider claims. If a known provider credential (like `ANTHROPIC_API_KEY`) is
  found in `env_sources`, yolo **refuses** the launch with a clear diagnostic:
  ```
  error: ZAI_API_KEY is a provider credential and cannot be loaded via env_sources.
  Move it to credential_sources to ensure it is securely scoped per-agent.
  ```

### 4.2 Channel 2: `credential_sources` (Provider Credentials)
- **Role:** Model and API authentication secrets (`ZAI_API_KEY`, `DEEPSEEK_API_KEY`,
  `AWS_SECRET_ACCESS_KEY`).
- **Syntax:** Mirrors `env_sources` (accepts dotenv file paths and inline JSON maps):
  ```jsonc
  "credential_sources": [
    "~/.config/yolo-jail/credentials.env",
    ".env.secrets",
    { "CUSTOM_KEY": "secret-value" }
  ]
  ```
- **Delivery:** Controlled by `packload.ScopeCredentials`. Delivered **only** to
  `~/.config/yolo-agent-env/<agent>.sh` for agents whose active profile selects the
  claiming provider.
- **Mental Model Alignment:** Because the developer explicitly placed the file in
  `credential_sources`, they *expect* it to be scoped. Withholding it from a non-profiled
  process is no longer a surprise; it is the stated purpose of the configuration key.

### 4.3 Handling Ad-hoc Host Commands (`yolo host -- bash`)
Even with `credential_sources`, running `yolo host -- bash -c 'echo $ZAI_API_KEY'` will
still result in an empty string if `bash` does not select `zai`.

To solve the ad-hoc command problem, introduce an explicit credential grant mechanism:
1. **Targeted flag:**
   ```console
   $ yolo host --with-credentials zai -- bash -c 'echo $ZAI_API_KEY'
   ```
   Or reuse `-p`:
   ```console
   $ yolo host -p zai -- bash -c 'echo $ZAI_API_KEY'
   ```
2. **Behavior:** Explicitly grants the specified provider's credentials to the invoked
   host process. The developer is in complete control, and no disclosure warning is
   printed.

---

## 5. Trade-off Analysis: The Real Costs

While separating `credential_sources` is conceptually cleaner, it carries substantial
trade-offs that must be weighed honestly:

| Dimension | Current Architecture (`env_sources` only) | Proposed Architecture (`credential_sources` split) |
| :--- | :--- | :--- |
| **Dotenv File Sprawl** | **Low.** Single `.env` file per workspace/user. | **High.** Requires two files (`.env` and `.credentials.env`). Developers must maintain both. |
| **Tool Compatibility** | **High.** Standard tooling (Vite, Next.js, Django, Cargo) reads `.env` directly. | **Medium/Low.** Standard tooling does not know about `.credentials.env` unless wrapped by yolo. |
| **Developer Surprise** | **High.** Variables in `.env` vanish from shells without warning when claimed by a provider. | **Low.** Variables in `env_sources` always appear; variables in `credential_sources` are expected to be scoped. |
| **Security Leak Prevention** | **Automatic.** Any variable matching a provider is secured, even if the user didn't know it was a provider key. | **Requires Enforcement.** If `env_sources` does not refuse provider keys, users will accidentally leak them. |
| **Configuration Friction** | **Zero.** Add key to `.env`, select profile `-p zai`, done. | **Higher.** Must classify every variable as either general env or credential. |
| **Ad-hoc Shell Ergonomics** | Needs `-p` or wrapper. Withheld with warning. | Still needs `--with-credentials` or `-p` if stored in `credential_sources`. |

### The Critical Hazard: The "Refusal vs. Leak" Dilemma
If we introduce `credential_sources`, what happens when a developer puts `ZAI_API_KEY=...`
into `.env` under `env_sources`?
- **Option 1 (Pass-through):** `ZAI_API_KEY` is exported to every process.
  *Result:* Re-opens cross-agent credential leakage ([`OQ-BR4`](provider-credential-scope.md#OQ-BR4) regression). Claude can read
  Zai's key; Bedrock keys leak to Pi. This violates P2.
- **Option 2 (Silent Withholding):** `ZAI_API_KEY` is withheld from `env_sources`.
  *Result:* We have re-created the exact original problem. The developer is still surprised.
- **Option 3 (Hard Refusal):** Yolo refuses to start if `env_sources` contains a variable
  matching an active provider's `api_key_env_name`.
  *Result:* Safe and unambiguous. It forces the developer to move the secret to
  `credential_sources`, guaranteeing that the user understands it is scoped.

---

## 6. Alternatives Considered

| Alternative | Description | Verdict |
| :--- | :--- | :--- |
| **A. Separate `credential_sources` with Refusal on `env_sources` (Proposed)** | Add `credential_sources`, deliver `env_sources` without filtering, hard-refuse provider keys in `env_sources`. | **Viable, but high friction.** Cleanest conceptual separation, but forces two-file management on all projects. |
| **B. Improve `yolo host` Ad-Hoc Command Support (Low Friction)** | Keep `env_sources` as-is, but allow `yolo host -p <provider> -- <cmd>` for any binary (e.g. `bash`). | **Recommended alternative.** Directly solves the user's immediate frustration (`bash -c 'echo $KEY'`) without breaking configuration schemas or requiring two `.env` files. |
| **C. Add `--all-credentials` / `--with-credentials` Flag to `yolo host`** | Add an explicit CLI flag to `yolo host` that disables withholding for that single invocation. | **Recommended addition.** Solves developer testing and ad-hoc scripts while preserving containment inside jails. |
| **D. Inline `unscoped: true` Annotations in `env_sources`** | Allow inline maps in `env_sources` to mark variables as exempt from scoping: `{"ZAI_API_KEY": "...", "unscoped": true}`. | **Rejected.** Clunky syntax, violates single `.env` file standard, confuses precedence. |

---

## 7. Open Questions

1. 💬 **OQ-1: Should yolo split `credential_sources` from `env_sources`?**
   Should we introduce a dedicated `credential_sources` configuration key, or does the
   resulting two-file `.env` sprawl outweigh the mental-model clarity?

   <!-- vantage: oq id=OQ-1 leaning="Keep env_sources unified, but add explicit CLI bypass/grant flags (Alternative B/C). Splitting into two config keys forces every user to maintain two dotenv files and coordinate external tooling, whereas the core frustration was simply that yolo host refused to give bash the key." -->

   _Leaning:_ Keep `env_sources` unified, but provide explicit CLI flags (`--with-credentials`
   or generalized `-p`) for ad-hoc host and shell commands (Alternative B/C).
   Splitting into `credential_sources` forces two dotenv files onto every repository and breaks
   external tools (like Next.js or Vite) that expect a single `.env`. The user's actual pain
   point was that `yolo host -- bash` withheld the key without an obvious way to ask for it.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-2: If `credential_sources` is adopted, how should `env_sources` handle provider keys?**
   If a developer puts `OPENAI_API_KEY` into `env_sources`, should yolo hard-refuse,
   warn and withhold, or pass it through to all processes?

   <!-- vantage: oq id=OQ-2 leaning="Hard refusal at validation. Allowing it to pass through re-opens OQ-BR4's cross-agent leak; silently withholding it recreates the original surprise. A hard refusal makes the separation unmistakable." -->

   _Leaning:_ Hard refusal with an actionable migration error. If `env_sources` passes it
   through, we regress on [`OQ-BR4`](provider-credential-scope.md#OQ-BR4) and leak credentials across agents. If it silently
   withholds, the surprise persists. A refusal clearly instructs: *"ZAI_API_KEY is a provider
   credential; declare it under credential_sources."*

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-3: Enabling `yolo host` to deliver credentials to arbitrary commands.**
   Currently, `yolo host` only scopes credentials to recognized agent pack binaries.
   How should an arbitrary command (like `bash` or `curl`) request provider credentials?

   <!-- vantage: oq id=OQ-3 leaning="Allow yolo host -p <provider> -- <cmd> to deliver that provider's credentials to any invoked command, defaulting to 'withhold' only when no profile/provider is specified." -->

   _Leaning:_ Allow `yolo host -p <provider> -- <cmd>` to deliver that provider's credentials
   to any command, regardless of whether `<cmd>` is an agent binary. When someone runs
   `yolo host -p zai -- bash -c 'echo $ZAI_API_KEY'`, yolo delivers `ZAI_API_KEY` directly to
   `bash`.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-4: Backward compatibility and workspace scope.**
   If `credential_sources` is added, should it be allowed at workspace scope
   (`yolo-jail.jsonc`), or restricted to user scope (`~/.config/yolo-jail/config.jsonc`)?

   <!-- vantage: oq id=OQ-4 leaning="Allow at both scopes, matching env_sources. A project workspace often maintains its own test keys in an untracked .env file." -->

   _Leaning:_ Allow at both scopes with load-time path anchoring, matching `env_sources`.
   Workspaces frequently maintain project-specific provider credentials in `.credentials.env`
   ignored by git.

   **Answer:**
   > _(empty — fill in when decided)_

---

## 8. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-1 | Pending user ruling: `credential_sources` split vs. CLI grant flags | — | [§4](#4-the-proposed-architecture-dedicated-credential_sources) | — |
| OQ-2 | Pending user ruling: Refusal vs. pass-through for keys in `env_sources` | — | [§4.1](#41-channel-1-env_sources-general-environment) | — |
| OQ-3 | Pending user ruling: Ad-hoc credential delivery to arbitrary commands | — | [§4.3](#43-handling-ad-hoc-host-commands-yolo-host----bash) | — |
| OQ-4 | Pending user ruling: Scope permissions for `credential_sources` | — | [§7](#7-open-questions) | — |

---
title: "Credential sources: implementation sketch"
status: draft
stage: SKETCH
next: "Once the design rules OQ-ES6 (depends-on), turn §3's shared-name entry into build steps"
depends-on:
  - credential-sources-separation.md#OQ-ES5
  - credential-sources-separation.md#OQ-ES6
tags: [providers, profiles, credentials, env-sources, plan]
---

# Credential sources: implementation sketch

**Status:** 2026-09-27; re-read 2026-09-30. [§1](#1-ship-now-es-d1-to-es-d5) is built (2026-09-27; the
design's [ledger](credential-sources-separation.md#10-decision-ledger) has the commits and the
mechanism choices and the review's fixes, ES-D6 to ES-D12). Everything after it is incomplete and blocked on the question
it names, except [§3](#3-blocked-on-the-other-open-questions)'s [OQ-ES5](credential-sources-separation.md#OQ-ES5) host half, ruled and built
the same day (ES-D13 to ES-D17). [§2](#2-only-if-the-split-is-ruled-in) is moot since 2026-09-30,
when the design's [OQ-ES1](credential-sources-separation.md#OQ-ES1) was answered no, and so is
[§3](#3-blocked-on-the-other-open-questions)'s [OQ-ES7](credential-sources-separation.md#OQ-ES7)
entry. [OQ-ES5](credential-sources-separation.md#OQ-ES5)'s jail half was ruled and built
2026-10-05 (the design's ES-D31 to ES-D36). What is left waits on
[OQ-ES6](credential-sources-separation.md#OQ-ES6). Codebase facts were verified at `8da7840d`.

> **Precedence.** This sketch accompanies
> [`credential-sources-separation.md`](credential-sources-separation.md). The design wins on
> behavior, and nothing here makes a design decision. [§1](#1-ship-now-es-d1-to-es-d5) builds the design's ES-D1 to ES-D5
> ([ledger](credential-sources-separation.md#10-decision-ledger)).

---

## 1. Ship now: ES-D1 to ES-D5

The host grant already works. `yolo host -p <profile> -- <cmd>` keys the one-agent table by the
command's basename (`effectiveHostProfiles`). `packload.ScopeCredentials` builds a delivery for
it, and `composeHostVars` step (2), `scope.EnvSourcesFor(agent)`, appends that profile's claimed
`env_sources` values. What remains to build is a pin, some wording, and one refusal.

| File / symbol | Change | Decision |
| :--- | :--- | :--- |
| `internal/cli/hostcredentialgate_test.go` | New cells over `hostGateLaunchWith`, with a non-agent basename (`bash`): see [§4](#4-tests-and-done-conditions) | ES-D1 |
| `internal/cli/host.go`: `hostUsage` | The `-p` line says "wrapped command", and says an ad-hoc command receives that profile's claimed `env_sources` values | ES-D3 |
| `internal/packload/credentialscope.go`: `CredentialScope.Disclosure` | Two wording changes at the host notch only. A withheld line names `yolo host -p <profile> -- <cmd>` (ES-D2). A withheld name that the invoking shell holds is worded as not added by yolo (ES-D4). Today `Disclosure` reads only `envSources` and knows nothing of the notch or the shell, while the host caller has `os.LookupEnv`. The implementer chooses the mechanism: a wording input on the scope, or host-side post-processing of the lines. The jail's lines must not change | ES-D2, ES-D4 |
| `internal/cli/host.go`: `composeHostVars` | A `use_profiles` key for the launched basename that no pack installs is refused, with the validator's wording plus the `-p` spelling. A typed `-p` is exempt. "Installs" means the validator's own namespace, `config.UseProfileCLINames`, which is every resolvable pack, selected or not; this row first said `binOwner` over the selected packs and claimed the two agree, which they do not ([ES-D9](credential-sources-separation.md#10-decision-ledger)) | ES-D5 |
| `docs/reference/providers.md` (the host-notch bullet under "The credential gate") and `docs/reference/host-agent-environment.md` | State the grant and the `yolo host env --agent <name> -p <profile>` front door | ES-D3 |

**Two traps:**

- **`yolo host` never runs `ValidateConfig`.** It reads `config.UserScopeConfigOrEmpty`. That is
  why `use_profiles: {"bash": "zai"}` delivers at the host today while `yolo check` refuses it.
  ES-D5's refusal has to live in the host path itself.
- **`unknownProfileCLIMessage` is unexported** in `internal/config`. Reuse its text through an
  exported helper, or restate it, but keep one source for the wording.

## 2. Only if the split is ruled in

**Moot (2026-09-30).** [OQ-ES1](credential-sources-separation.md#OQ-ES1) was answered no, so there
is no split and nothing below is built. The table stays as the record of what a second key would
have touched.

Blocked on [OQ-ES1](credential-sources-separation.md#OQ-ES1). Its leaning is no, and a no deletes
this section. These are the call sites a second key would touch. Two of them are easy to miss:
the shared file is narrowed where `deliverChannel` calls `writeUserEnvFile`, not inside it; and
`userlayer.go`, not only `load.go`, anchors entries.

| File / symbol | Why it is touched |
| :--- | :--- |
| `internal/config/validate.go` | Schema for the new key. It could also carry [OQ-ES2](credential-sources-separation.md#OQ-ES2)'s refusal, but only for the jail and `yolo check` (see [§1](#1-ship-now-es-d1-to-es-d5)'s first trap) |
| `internal/config/envsources.go`: `ResolveEnvSourcesFull`, `AnchorEnvSources` | Hydration and anchoring of a second key |
| `internal/config/load.go`, `internal/config/userlayer.go` | Both call `AnchorEnvSources`; `userlayer.go` is the user-scope loader the host notch reads |
| `internal/packload/credentialscope.go`: `ScopeInput`, `ScopeCredentials` | A second input stream. What it does with a name no provider claims is undefined (the design's [§4.2](credential-sources-separation.md#42-what-it-would-revisit)), and it is a design question, not a sketch entry |
| `internal/cli/run/profilechannel.go`: `(*Options).composePackChannel` | The jail notch's gate call |
| `internal/cli/host.go`: `composeHostVars` | The host notch's gate call. It needs its own pre-flight for any refusal |
| `internal/cli/check/envoverrides.go` | The third `ScopeCredentials` caller (`NoDerives`), which is `yolo check`'s prediction |
| `internal/cli/run/agentenvfiles.go`: `deliverChannel` | Writes the shared file from `SharedEnvSources` and each agent's file. `writeUserEnvFile` (`userenv.go`) only writes what it is handed |
| `internal/cli/run/profilechannel.go`: `(*packChannel).launchEnv`; `internal/cli/run/credentialnotes.go`: `noteMacosUserCredentialScope` | The macos-user vehicle ([CN-D12](provider-credential-scope.md#CN-D12)); [OQ-CN5](../reference/providers.md#oq-cn5) requires all three vehicles to ship together |
| `internal/wirebridged/keyfile.go`: `resolveKey` | Reads a served agent's key from that agent's file, then the shared file ([CN-D11](provider-credential-scope.md#CN-D11)) |
| `internal/entrypoint/mcp.go`: `loadMCPTables` | `requires_env` is asked per agent ([CN-D19](provider-credential-scope.md#CN-D19)) |

[OQ-ES2](credential-sources-separation.md#OQ-ES2) and
[OQ-ES4](credential-sources-separation.md#OQ-ES4) are blocked on [OQ-ES1](credential-sources-separation.md#OQ-ES1) as well.

## 3. Blocked on the other open questions

- **[OQ-ES5](credential-sources-separation.md#OQ-ES5)**, a provider-naming grant flag. **Its
  host half is ruled and BUILT** (2026-09-27, the design's
  [§5.1](credential-sources-separation.md#51-the-explicit-grant---with-credentials-built)): the
  sibling input is `ScopeInput.Grants`, fed from `parseHostExecFlags` and `hostEnv`. **Its jail
  half is ruled and BUILT** (2026-10-05, the design's
  [§5.2](credential-sources-separation.md#52-the-jail-half---with-credentials-at-a-jail-launch-built)),
  not as this sketch guessed: the ruling makes the set the jail's, so it is held by every
  process of the jail for its life rather than by one entry's `--` command. It still never rides
  `deliverChannel`'s files, which an attach rewrites whole
  ([CN-D7](provider-credential-scope.md#CN-D7)): a per-launch grant file outside the
  runtime's command line and environment (the design's ES-D37), and on macos-user `launchEnv`,
  which the session env file carries. The flag joined `runFlags`, `refuseHostOnlyFlags` is deleted, and
  its disclosure is not suppressible
  ([`OQ-RO3`](../reference/report-tiers.md#why-its-this-way)).
- **[OQ-ES6](credential-sources-separation.md#OQ-ES6)**, a user-scope shared-name
  acknowledgment. It would be read through `UserScopeConfig`, never the merged config. The gate
  would treat an acknowledged name as unclaimed for `SharedEnvSources` and `EnvSourcesFor`, and
  `Disclosure` would name it on every launch.
- **[OQ-ES7](credential-sources-separation.md#OQ-ES7)**, moot since
  [OQ-NC5](../plans/notch-convergence.md#OQ-NC5): a typed host `-p` for an ad-hoc command is now
  refused, so nothing here is built. As filed, a CLI-less pack's gated env under a
  typed host `-p`. This touches `gateFiresFor` and `EnvFold` for the host's typed `-p` only, and
  the comment on `gateFiresFor` (which currently says the host leans on the no-activation rule)
  has to be restated. The pin is `yolo host -p bedrock -- bash` receiving aws-auth's
  `AWS_CONTAINER_CREDENTIALS_FULL_URI`.

## 4. Tests and done conditions

All the [§1](#1-ship-now-es-d1-to-es-d5) cells drive `hostMain` through `hostGateLaunchWith`, as the `TestHostGate*` cells do.
They use packs `claude`, `pi` and `zai`, with `env_sources` carrying `ZAI_API_KEY` and an
unclaimed `PORT`. The scratch versions of cells 1 to 3 passed at `8da7840d` unchanged, which is
why ES-D1 is a pin and not a feature.

1. `-p zai -- bash`: `ZAI_API_KEY` delivered, `PORT` delivered, and stderr contains
   `ZAI_API_KEY (provider zai): bash only`.
2. `-- bash`: `ZAI_API_KEY` absent, `PORT` delivered, and the withheld line names
   `yolo host -p` (ES-D2).
3. The key both in `env_sources` and exported in the shell, then `-- bash`: the shell's value
   reaches `bash` ([CN-D13](provider-credential-scope.md#CN-D13)), and no line says
   "withheld" for it (ES-D4).
4. `use_profiles: {"bash": "zai"}`, then `-- bash`: refused with the `-p` spelling (ES-D5). The
   same config with a typed `-p zai` still delivers.
5. `yolo host env --agent bash -p zai`: stdout carries `export ZAI_API_KEY=…`.
6. `hostUsage` names "command" for `-p` (ES-D3).

**The call-site check.** Before landing, delete each of the following in turn and see a cell
fail: the host's `ScopeCredentials` call; the `profile != "" && agent != ""` override in
`effectiveHostProfiles`; and the ES-D5 refusal. If a deletion passes, the cell pins the callee
and not the call site.

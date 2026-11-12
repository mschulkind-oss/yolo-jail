---
title: "Secret env scoping: implementation plan"
date: 2026-11-09
status: accepted
stage: BUILT
next: "Run the Mac checks below; then graduate with the design into providers.md"
depends-on:
  - secret-env-scoping.md
tags: [credentials, env_sources, providers, credential-gate, config, plan]
---

# Secret env scoping: implementation plan

**Status:** 2026-11-09. Completed against the tree, written against `b36be7d53`. Built, Tasks 1 to 8. MEASURED: the jail notch and `yolo host`, on Linux. UNMEASURED: macos-user and Apple Container; the [Mac checks](#mac-checks) are owed.
**Design:** [`secret-env-scoping.md`](secret-env-scoping.md), `status: accepted`, no question open
(rulings [SE-D2](secret-env-scoping.md#SE-D2) and [SE-D12](secret-env-scoping.md#SE-D12) to
[SE-D15](secret-env-scoping.md#SE-D15)).

> **Precedence.** The design wins on behavior. The tree wins on fact, for cosmetic drift only (a moved
> line, a renamed helper with the same role): follow the tree and say so in the report and the commit.
> This plan is advice and is the first thing to be wrong; never twist the code to match it.
>
> **Stop and report, and do not commit,** when: a task's first test does not fail the way the task
> predicts; a file the task names is dirty with someone else's work; a symbol the task names is gone; the
> fix needs a file outside the task's fence; a gate fails outside the task's scope.

**Roles.** The planner wrote this file from the tree; the implementer builds one task per commit from it
and does not read the design except where a task links it. Quoted rulings below are marked **do not
re-open**.

**Depends on nothing open.** The neighbouring [`bedrock-key-provider.md`](bedrock-key-provider.md) waits on
[OQ-BK3](bedrock-key-provider.md#OQ-BK3), which asks where that provider's bearer file is declared. This
plan builds the general rule only and nothing specific to `bedrock-key`: no task names that provider,
moves its file, or writes its docs. Whatever [OQ-BK3](bedrock-key-provider.md#OQ-BK3) rules, the gate built here applies unchanged; the
bedrock-key plan, once written, depends on this one, not the other way round.

## Words this plan uses

- **The public set**, **the private set**, **a claim**, **the credential gate**, **a delivery vehicle**,
  **reach** — the design's, defined in [its §1](secret-env-scoping.md#1-words-this-doc-uses).
- **Row 1 to row 4** — the four outcomes of [the design's §3.4 table](secret-env-scoping.md#34-the-four-outcomes):
  public-unclaimed, public-claimed-by-a-provider (the refusal), private-claimed, private-unclaimed.
- **The both-sets refusal** — [§4.8](secret-env-scoping.md#48-the-same-name-in-both-sets)'s fatal error.
- **The set bit** *(coined in the design, [SE-D1](secret-env-scoping.md#SE-D1))* — one boolean per hydrated
  name: true when `[env.secrets]` declared it.
- **`HydratedEnv`** *(coined here)* — the new value type of Task 1 that carries the hydration with its set
  bit. Not the config: it is what a launch read out of the dotenv files and inline tables.
- **SEP-D*n*** *(coined here)* — a planner's mechanism decision where the design is silent;
  [listed below](#plan-decisions).

## The map

| Path | Change | Task |
| :--- | :--- | :--- |
| [`internal/config/envsources.go`](../../internal/config/envsources.go) | `envEntry` gains `private`; `envEntries` walks `env.secrets.files` and `env.secrets.set`; the old public-only readers skip private entries, then are deleted in Task 4 | 1, 4 |
| `internal/config/envsets.go` (new) | `HydratedEnv`, `ResolveEnv`, `EnvPlace` | 1 |
| [`internal/packload/credentialscope.go`](../../internal/packload/credentialscope.go) | `ScopeInput.Private`, `MCPClaims`, `MCPAgents`, `UnselectedClaims`; `receives`/`delivers`/`EnvSourcesFor`/`DisclosureWith`; `PublicClaims()` | 2 |
| `internal/packload/unselectedclaims.go` (new) | `UnselectedProviderClaims`, mirroring `UnselectedAdaptations` | 2 |
| `internal/packload/mcpclaims.go` (new) | `MCPEnvClaims`, `MCPAgents` | 2 |
| [`internal/packload/grant.go`](../../internal/packload/grant.go) | `GrantFor` reads only private names | 2 |
| [`internal/config/schema.go`](../../internal/config/schema.go) | `env.secrets` rows (object, `files`, `files.*`, `set`, `set.*`, both scopes) | 3 |
| [`internal/config/envsourcescope.go`](../../internal/config/envsourcescope.go) | `validateEnvSourceScope` checks `env.secrets.files`; `envSourceScopeRefusal` spells `[env.secrets] files` for it | 3 |
| [`internal/config/inherit.go`](../../internal/config/inherit.go) | the `env` case copies `secrets` too | 3 |
| [`internal/config/validationprojection.go`](../../internal/config/validationprojection.go) | `readableEnvValidationShape` accepts `secrets` (`files`, `set`), and `validateReadableEnvironment` names a bad `env.secrets.set` name; without it a TOML `[env.secrets]` is refused as the removed legacy `env` key (found by the Task 3 nested launch) | 3 |
| [`internal/config/removals.go`](../../internal/config/removals.go) | `env.secrets.files` clears with an empty list, as `env.files` does (`TestReadableSchemaRemovalPoliciesCoverAcceptedEmptyListsAndNativeSelection`) | 3 |
| [`internal/cli/run/profilechannel.go`](../../internal/cli/run/profilechannel.go) | `composePackChannel` takes `*config.HydratedEnv`; `packChannel.userEnv` type; gate inputs | 3 |
| [`internal/cli/run/run.go`](../../internal/cli/run/run.go), [`packtree.go`](../../internal/cli/run/packtree.go), [`loopholegrants.go`](../../internal/cli/run/loopholegrants.go), [`seal.go`](../../internal/cli/run/seal.go), [`assemble.go`](../../internal/cli/run/assemble.go) | the `userEnv` type follows | 3 |
| [`internal/cli/run/jailgrant.go`](../../internal/cli/run/jailgrant.go) | `resolveJailGrant` reads the private values | 3 |
| [`internal/cli/run/programcapture.go`](../../internal/cli/run/programcapture.go) | the sealed-identity check reads both sets | 3 |
| [`internal/cli/run/credentialnotes.go`](../../internal/cli/run/credentialnotes.go) | `noteCredentialScope` passes the scope note for row 4 and row 1 lines | 3 |
| [`internal/cli/check/entrypoint.go`](../../internal/cli/check/entrypoint.go) | the boot preflight's env is the public set only | 3 |
| [`internal/cli/host.go`](../../internal/cli/host.go) | `composeHostVarsWith`, `hostScopedEnvSources`, `resolveHostGrant`, `credentialScopeLines` | 4 |
| [`internal/cli/check/envoverrides.go`](../../internal/cli/check/envoverrides.go), `internal/cli/check/secretenv.go` (new), [`packs.go`](../../internal/cli/check/packs.go) | `overrideScope` reads `HydratedEnv`; row 4 and row 1 lines as WARN | 4 |
| `internal/config/envlocate.go` (new) | `LocateEnvName`, `ReadEnvScopeFiles`, `EnvNameScope`, `EnvPlace.Describe`: which scope, set and place set a name, read from each scope's own top-level file (the file a move edits; an include's entry is not editable) | 5 |
| `internal/config/dotenvedit.go` (new) | remove and append one name's line in a dotenv file | 5 |
| `internal/cli/configsecret.go` (new) | `yolo config secret\|unsecret <NAME> [--drop]`, `--dotenv <path>` ([SEP-D17](#SEP-D17)) | 5 |
| [`internal/configedit/edit.go`](../../internal/configedit/edit.go) | removing a TOML inline-table member takes its separating comma (`inlineTableMemberSpan`); before it, removing `A` from `set = { A = "1", B = "2" }` planned unparseable TOML (found by `TestConfigSecretMovesAnInlineValue`) | 5 |
| [`internal/cli/check/secretenv.go`](../../internal/cli/check/secretenv.go), [`packs.go`](../../internal/cli/check/packs.go) | `secretEnvGap(scope, workspace)` fills `DisclosureNotes.Scope` from `EnvNameScope` | 5 |
| [`internal/cli/config.go`](../../internal/cli/config.go) | dispatch, `configHelpToken`, usage text | 5 |
| [`internal/cli/config_ref.txt`](../../internal/cli/config_ref.txt) | `[env.secrets]` in the env entry (Task 3), the verbs (Task 5), the refusal sentence (Task 7) | 3, 5, 7 |
| [`internal/cli/configreftoml_test.go`](../../internal/cli/configreftoml_test.go) | `TestConfigRefTOMLSectionNamesTheVerbsTheCLIHas` names the two verbs | 5 |
| unit and integration fixtures holding a provider key in the public set | moved to the private set | 6 |
| `internal/config/envsecretsrefusal.go` (new) | the row 2 and both-sets refusal text | 7 |
| run, host, check call sites | the refusals | 7 |
| `integration/secretenv_test.go` (new) | the end-to-end tests | 3, 7 |
| docs listed in [Ships with](#ships-with) | the old "unclaimed reaches every process" claims | 8 |

## Reuse before you write

- **The hydration walk:** `envEntries` and `ResolveEnvSourcesFull` in
  [`envsources.go`](../../internal/config/envsources.go). `ResolveEnv` is the same later-wins walk with a
  second map; copy its `assign`/`remove` closures, do not write a second dotenv reader. `ParseDotenv`
  parses; `ResolveEnvSourcePath` resolves.
- **Per-file anchoring already covers `env.secrets.files`:** `anchorReadableEnvSources` anchors
  `env.files` per file; add `env.secrets.files` there, beside it (that is [SE-D1](secret-env-scoping.md#SE-D1)'s
  "assigned where the entry is read").
- **The merge already composes `[env.secrets]`:** `mergeConfigTables` in
  [`load.go`](../../internal/config/load.go) recurses into tables and concatenates lists, so user then
  workspace `env.secrets.files` arrive in layer order and `env.secrets.set` is later-wins per name. Write no
  merge code.
- **Wording the shipped set:** `UnselectedAdaptations` and `unselectedEmbedded` in
  [`protocolresolution.go`](../../internal/packload/protocolresolution.go) are the sibling; its docstring
  already argues why reading the embedded set for wording is allowed.
- **Per-pack provider claims:** `ComposeProvidersAt(nil, []*Pack{p}, nil, ServedDaemons{})` then
  `credentialClaims` (both [`served.go`](../../internal/packload/served.go) and `credentialscope.go`).
- **The composed MCP table:** `ComposeMCPServers(user, packs, home)` in
  [`mcpcompose.go`](../../internal/packload/mcpcompose.go); `requires_env` is read off each entry. `home` only
  rewrites `~/` command words, which the claim does not read: pass `""`.
- **A delivery with no profile:** the grant-only `AgentDelivery` shape in `ScopeCredentials` (a process
  with `Granted` and no `Profile` runs no derive). An MCP-claimed agent with no profile is the same shape.
- **Provenance and scope roles:** `LoadReadableConfigWithSources` ([`load.go`](../../internal/config/load.go))
  records every list element's and member's writer; `srcNode.elem(i).origins()` gives the file, its
  `editReadRole` (`editRoleUser`, `editRoleProject`, `editRoleLocal`, `editRoleInherited`), and its format.
  `validateEnvSourceScope` is the worked example of reading it, including the legacy-format test
  `origins[0].format() != configdoc.TOML`.
- **Typed edits without printing values:** `configedit.ResolveTarget` then `configedit.ApplyTargeted`
  ([`targeted.go`](../../internal/configedit/targeted.go)); `runConfigEditWithMode` in
  [`configeditverbs.go`](../../internal/cli/configeditverbs.go) shows flag parsing, `addMissingKey`, and the
  in-jail user-scope path (`configEditNeedsHostForJailedUserScope`, `printConfigEditHostRun`).
- **Test fixtures:** `testconfig.User` and `testconfig.Workspace` (`internal/testconfig`) for verb tests;
  `twoProviders` and `hydrated` in [`credentialscope_test.go`](../../internal/packload/credentialscope_test.go)
  for gate tests; `packHome`, `goldenOptions`, `bareConfig`, `setPacks` for run tests; `writeUserTOML`,
  `writeWorkspaceTOML`, `runYolo` in [`readableconfig_test.go`](../../integration/readableconfig_test.go)
  and [`harness_test.go`](../../integration/harness_test.go) for integration.

## House style that is not obvious

- **Every refusal names one command** ([happy-path principle](../reference/happy-path-principle.md)), a
  `yolo` verb and never a file to hand-edit. Gate: read the text against the design's quoted blocks.
- **Names only, never a value**, in every line, refusal, verb output, `launch.log` and `boot.log`
  ([CN-D16](../reference/providers.md#CN-D16)). Every new test asserts the value's absence as well as the
  name's presence.
- **No `YOLO_ALLOW_*` hatch** for any refusal here ([SE-D10](secret-env-scoping.md#SE-D10)).
- **Event-driven tests:** no sleep, no elapsed-time pass condition; integration waits on the launch's exit
  or a readiness line, as [`jailgrant_test.go`](../../integration/jailgrant_test.go) does.
- **Doc comments carry the why** in this repo, at length; a new exported symbol gets one naming the
  design anchor it implements.

## Traps

- **The hydrated map is re-used across recompositions.** `composePackChannel` hydrates only when handed
  nil; `run.go` (the attach rekey), `packtree.go` and `loopholegrants.go` pass `channel.userEnv` back. The
  set bit must live in that value, or a recomposition silently makes every secret public.
- **`config` imports `packload`, never the reverse.** Refusal wording that needs config (scope, legacy
  format) lives in `config`; the gate in `packload` returns structured facts only.
- **The fallback must keep today's rule.** `LookupFor` relays a claimed name from the launching shell
  (`ScopeInput.Fallback`) only to its claimants. A name that no config set hydrated is in neither set;
  if `receives` starts answering "true" for every non-private name, the launching shell's `ZAI_API_KEY`
  reaches every agent's derive. Symptom: `TestTheFallbackStillScopesAClaimedNameNobodyHydrated` (Task 2)
  goes red.
- **The old public-only readers silently drop private values.** `ResolveEnvSources`,
  `ResolveEnvSourcesFull`, `HydrateEnvSources`, `SplitHydratedEnvSources`, `EnvSourceRemovals` must all be
  gone by the end of Task 4, so no caller can read half the hydration. `rg -n
  'ResolveEnvSources|HydrateEnvSources|SplitHydratedEnvSources|EnvSourceRemovals' internal cmd` must print
  only `ResolveEnvSourcePath` hits.
- **The generic edit verb prints the value.** `applyConfigEdit` writes hunks (`writeConfigEditHunks`), which
  quote the inline value. The secret verbs call `configedit.ApplyTargeted` themselves and print only names
  and places.
- **Tests that compose a provider key as a plain map will refuse after Task 7.** About 49
  `composePackChannel` call sites in `internal/cli/run` tests and the integration files listed in Task 6
  hand a provider key in the public set. Task 6 moves them before Task 7 lands; do not land Task 7 first.
- **Jail env skews unit results** (`YOLO_VERSION`, `YOLO_ALLOW_MISSING_PROGRAMS` are exported in this jail).
  Run unit gates as `env -u YOLO_VERSION -u YOLO_ALLOW_MISSING_PROGRAMS go test …`.
- **Integration reaps through the host store.** Run in-jail integration with
  `YOLO_NO_AUTO_IMAGE_REAP=1`; `git add` new files before an image build, since nix sees tracked files only.
- **macos-user and Seatbelt reuse the jail's channel.** `launchChannel` composes above the backend dispatch
  in `run.go`, so podman, Apple Container and macos-user (the `seatbelt-self` and `seatbelt-yolo-account`
  setups) read one gate answer. Change no vehicle; if a macos-user test fails, the bug is in the gate
  input, not in [`envfile.go`](../../internal/macosuser/envfile.go).

## Plan decisions

| ID | Decision | Reason |
| :--- | :--- | :--- |
| <a id="SEP-D1"></a>SEP-D1 | `HydratedEnv` holds one ordered value map (both sets, hydration order), the set bit per name, the removals, and per name the places that set its winning value (two places on a both-sets name, whose map value is the private copy) | One value replaces the nil-encoded map `packChannel.userEnv` carries today; one map keeps every gate reader that iterates `EnvSources` working |
| <a id="SEP-D2"></a>SEP-D2 | `[env] unset` and a legacy inline null remove a name from both sets: every name in the public walk's removal list is deleted from the private map too | [SE-D4](secret-env-scoping.md#SE-D4) says one `unset` removes from both; a legacy null is the same removal spelled earlier |
| <a id="SEP-D3"></a>SEP-D3 | A name is "in both sets" when it holds a value in both maps after removals | A name `unset` removes from both is set in neither, so nothing is in conflict |
| <a id="SEP-D4"></a>SEP-D4 | The gate keeps scoping a **public** provider-claimed name as today (claimants only) and reports it through `PublicClaims()`; the callers refuse | Task 2 lands with no behavior change, and if a caller ever skipped the refusal the value still would not widen ([P2](secret-env-scoping.md#32-principles)) |
| <a id="SEP-D5"></a>SEP-D5 | An MCP claim's agents are the bins of every selected `[[program]]` whose pack ships a `derive.lua` (`DeriveScript(p) != ""`) | Each such agent's derive renders the one composed MCP table, so these are "the agents whose config names that server"; no manifest field says which agents render MCP, and a program with no derive (`guardrails`, `chrome-devtools`, `muse-code`) renders none |
| <a id="SEP-D6"></a>SEP-D6 | Scope (`--user`, `--project`, `--local`) for a refusal or a line comes from `LocateEnvName`, read on the cold path only (when something is printed); an `editRoleInherited` writer reads as `--user` | The hot path carries only the set bit ([SE-D1](secret-env-scoping.md#SE-D1)); the verb needs the same lookup, so one function serves both |
| <a id="SEP-D7"></a>SEP-D7 | A place prints as `([env], from <entry>)`, `([env] set)`, `([env] sources)` for an inline `env.sources` table, `(env_sources, from <entry>)` for a legacy file, and `([env.secrets], from <entry>)` or `([env.secrets] set)`; `<entry>` is the entry as the loader left it (a `~` entry stays `~`, a relative one is anchored) | Matches `envSourceScopeRefusal`'s spelling of an entry; the design's examples use `~` entries |
| <a id="SEP-D8"></a>SEP-D8 | Both refusals are collected and printed together, both-sets lines first, in one refusal | One launch tells the user everything to fix; both-sets is a config-shape error independent of the selection |
| <a id="SEP-D9"></a>SEP-D9 | `yolo check` grades row 4 and the unselected-pack row 1 line as WARN, and both refusals as FAIL, in the Packs block beside `envOverrideGap` | The launch runs on a WARN and stops on a FAIL, which is check's existing vocabulary; the Packs block is where the selected packs are in hand |
| <a id="SEP-D10"></a>SEP-D10 | The verbs write the destination first, then remove the source | A failure between the two writes leaves both copies, which the both-sets refusal then names with its `--drop` remedy; the reverse order could lose the value |
| <a id="SEP-D11"></a>SEP-D11 | With no scope flag, a verb acts in the one scope whose public (or private) set holds the name; found in several, it refuses and prints one command per scope; found in none, it refuses naming where the name is set instead, if anywhere | The design says "the scope that holds the entry unless a scope flag says otherwise" and is silent on several |
| <a id="SEP-D12"></a>SEP-D12 | A dotenv move copies the source line verbatim (an `export ` prefix and quoting kept), removes every line in the source file that sets the name, and creates a missing destination file listed in config with mode `0600`; an existing file keeps its mode | A verbatim line cannot change the value's parse; `ParseDotenv` reads the last assignment, so a leftover duplicate line would keep the name public |
| <a id="SEP-D13"></a>SEP-D13 | An `env.sources` path entry is moved like a `files` entry; a name in an inline `env.sources` table moves into the other set's `set` | The design names "an inline `set` value or a line in a listed dotenv file"; `env.sources` is the same two shapes in one list |
| <a id="SEP-D14"></a>SEP-D14 | The remedy when no private file is listed suggests `~/.config/yolo-jail/secrets.env` at user scope and `secrets.env` (anchored beside the config file) at project and local scope; the `unsecret` mirror suggests `~/.config/yolo-jail/defaults.env` and `defaults.env` | The design gives the user-scope spelling; a workspace private file must lie inside the workspace |
| <a id="SEP-D15"></a>SEP-D15 | Several providers on one line: `the zai, foo providers, which this launch does not select, claim that name. If it is one of theirs: …`; row 4 with claimants in one pack names that pack, with several packs `To use it as a provider's key: yolo pack add <a> (or <b>).` | Cheap wording; the design shows the one-provider case only |
| <a id="SEP-D16"></a>SEP-D16 | **Superseded by [SE-D16](secret-env-scoping.md#SE-D16) (owner, 2026-11-10).** A nested jail's inherited user layer (`InheritNested`) no longer carries `env.secrets`; it used to, for parity | The owner ruled that nested jails get no privilege the outer jail lacks |
| <a id="SEP-D17"></a>SEP-D17 | **One dotenv file listed in both `[env] files` and `[env.secrets] files` is one refusal, not one per name**, and its remedy is `yolo config secret --dotenv <path>` (or `unsecret --dotenv`), which moves the file's entry from one list to the other in the scope that lists it. Task 5 adds `--dotenv`; Task 7 checks for the shared file before the per-name both-sets check. | Every name in the file is in both sets, and a per-name `--drop` would delete the one line both sets read. The fix is to move the file, and a verb must name it (happy-path principle). Decided by the planner, 2026-11-09; was design gap 1. Spelled `--dotenv`, not the planner's `--file`, by Task 5: the design names `--file` as the generic verbs' config-file scope flag ([SE-D9](secret-env-scoping.md#SE-D9)), and the design wins. |
| <a id="SEP-D18"></a>SEP-D18 | **A legacy JSONC workspace config holding a provider key gets the same remedy as a legacy user config:** `yolo config migrate`, then the verb with `--project`. | Parity across scopes: [SE-D6](secret-env-scoping.md#SE-D6) states the rule for the user file, and nothing about the workspace scope differs. Decided by the planner, 2026-11-09; was design gap 2. |

## Tasks

Build order: **1 → 2 → 3 → 4 → 5 → 6 → 7 → 8.** Tasks 1 and 2 touch disjoint files and may run in
parallel worktrees, landing 1 first. Task 5 depends only on Task 3 (the schema rows) and may run beside
Task 4. Task 6 must land before Task 7; Task 7 is the breaking change. Task 8 lands last.

### Task 1 — the hydration keeps which set declared each name

**Independent.** No behavior change: no schema row accepts `[env.secrets]` yet, so no config can write it.

- **Settled** *(do not re-open)*: "Hydration keeps which set declared each value, assigned where the entry
  is read beside the per-file anchoring of relative paths, and carried through the merge"
  ([SE-D1](secret-env-scoping.md#SE-D1)). "`[env.secrets]` has `files` and `set`, no `sources`"
  ([SE-D3](secret-env-scoping.md#SE-D3)). "One `unset`, under `[env]`, removes a name from both sets"
  ([SE-D4](secret-env-scoping.md#SE-D4)). Order within the private set: `files`, then `set`, later wins.
- **Preconditions:** `git status` clean; `rg -n 'func envEntries|func ResolveEnvSourcesFull|func anchorReadableEnvSources' internal/config` prints three hits.
- **Tests first:** `internal/config/envsets_test.go` (new).

  ```go
  package config

  import (
  	"os"
  	"path/filepath"
  	"strings"
  	"testing"

  	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
  )

  func omap(kv ...any) *jsonx.OrderedMap {
  	m := jsonx.NewOrderedMap()
  	for i := 0; i+1 < len(kv); i += 2 {
  		m.Set(kv[i].(string), kv[i+1])
  	}
  	return m
  }

  func writeEnv(t *testing.T, path, body string) {
  	t.Helper()
  	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
  		t.Fatal(err)
  	}
  }

  func TestResolveEnvKeepsWhichSetDeclaredEachName(t *testing.T) {
  	dir := t.TempDir()
  	pub, priv := filepath.Join(dir, "defaults.env"), filepath.Join(dir, "secrets.env")
  	writeEnv(t, pub, "GH_TOKEN=g\nDEBUG=1\n")
  	writeEnv(t, priv, "ZAI_API_KEY=z\n")
  	cfg := omap("env", omap(
  		"files", []any{pub},
  		"set", omap("EDITOR_THEME", "dark"),
  		"secrets", omap("files", []any{priv}, "set", omap("OPENROUTER_API_KEY", "sk-or")),
  	))
  	h := ResolveEnv(dir, cfg, nil)
  	if got := strings.Join(h.Values().Keys(), ","); got != "GH_TOKEN,DEBUG,EDITOR_THEME,ZAI_API_KEY,OPENROUTER_API_KEY" {
  		t.Errorf("values in hydration order = %s", got)
  	}
  	for name, want := range map[string]bool{"GH_TOKEN": false, "DEBUG": false, "EDITOR_THEME": false,
  		"ZAI_API_KEY": true, "OPENROUTER_API_KEY": true} {
  		if h.IsPrivate(name) != want {
  			t.Errorf("IsPrivate(%s) = %v, want %v", name, !want, want)
  		}
  	}
  	if p := h.Places("ZAI_API_KEY"); len(p) != 1 || !p[0].Private || p[0].Entry != priv || p[0].Key != "env.secrets.files" {
  		t.Errorf("ZAI_API_KEY's place = %+v, want the private file", p)
  	}
  	if p := h.Places("EDITOR_THEME"); len(p) != 1 || p[0].Private || p[0].Key != "env.set" {
  		t.Errorf("EDITOR_THEME's place = %+v, want [env] set", p)
  	}
  	if len(h.BothSets()) != 0 {
  		t.Errorf("no name is in both sets here: %v", h.BothSets())
  	}
  }

  func TestOneUnsetRemovesANameFromBothSets(t *testing.T) {
  	cfg := omap("env", omap(
  		"unset", []any{"AWS_PROFILE"},
  		"secrets", omap("set", omap("AWS_PROFILE", "dev", "KEEP", "k")),
  	))
  	h := ResolveEnv(t.TempDir(), cfg, nil)
  	if _, ok := h.Values().Get("AWS_PROFILE"); ok {
  		t.Error("[env] unset must remove a private value of the name too (SE-D4)")
  	}
  	if got := strings.Join(h.Removals(), ","); got != "AWS_PROFILE" {
  		t.Errorf("removals = %s, want AWS_PROFILE", got)
  	}
  	if v, _ := h.Values().Get("KEEP"); v != "k" {
  		t.Errorf("an unrelated private value survives, got %v", v)
  	}
  }

  func TestANameInBothSetsIsReportedWithBothPlaces(t *testing.T) {
  	dir := t.TempDir()
  	pub, priv := filepath.Join(dir, "defaults.env"), filepath.Join(dir, "secrets.env")
  	writeEnv(t, pub, "ZAI_API_KEY=old\n")
  	writeEnv(t, priv, "ZAI_API_KEY=new\n")
  	cfg := omap("env", omap("files", []any{pub}, "secrets", omap("files", []any{priv})))
  	h := ResolveEnv(dir, cfg, nil)
  	if got := strings.Join(h.BothSets(), ","); got != "ZAI_API_KEY" {
  		t.Fatalf("BothSets = %s, want ZAI_API_KEY", got)
  	}
  	places := h.Places("ZAI_API_KEY")
  	if len(places) != 2 || places[0].Private || !places[1].Private {
  		t.Errorf("places = %+v, want the public place then the private one", places)
  	}
  	if v, _ := h.Values().Get("ZAI_API_KEY"); v != "new" {
  		t.Errorf("a both-sets name holds the private copy (SEP-D1), got %v", v)
  	}
  }

  func TestDescribeEnvSourcesNamesThePrivateEntriesConsulted(t *testing.T) {
  	cfg := omap("env", omap("secrets", omap("files", []any{"/x/secrets.env"})))
  	if got := strings.Join(DescribeEnvSources("/w", cfg), "|"); !strings.Contains(got, "/x/secrets.env") {
  		t.Errorf("the pre-flight's consulted list must name a private file: %s", got)
  	}
  }
  ```

  Red: `env -u YOLO_VERSION go test ./internal/config -run 'ResolveEnv|OneUnset|BothSets|DescribeEnvSourcesNamesThePrivate' -count=1`
  → build failure, `undefined: ResolveEnv`.
- **Change:**
  - `internal/config/envsets.go` (new). Contract shapes:

    ```go
    type EnvPlace struct {
    	Private bool   // the set bit of this place
    	Key     string // "env_sources", "env.sources", "env.files", "env.set", "env.secrets.files", "env.secrets.set"
    	Entry   string // the dotenv entry as the loader left it; "" for an inline table
    }
    type HydratedEnv struct{ /* unexported: values, private, removals, places */ }
    func ResolveEnv(workspace string, cfg *jsonx.OrderedMap, warn Warn) *HydratedEnv
    func NewHydratedEnv() *HydratedEnv
    func (h *HydratedEnv) SetPublic(name, value string, place EnvPlace)
    func (h *HydratedEnv) SetPrivate(name, value string, place EnvPlace)
    func (h *HydratedEnv) Remove(name string)
    func (h *HydratedEnv) Values() *jsonx.OrderedMap   // assignments only, never a nil value
    func (h *HydratedEnv) IsPrivate(name string) bool
    func (h *HydratedEnv) Private() map[string]bool    // a copy; nil-safe on a nil receiver
    func (h *HydratedEnv) Removals() []string
    func (h *HydratedEnv) Places(name string) []EnvPlace
    func (h *HydratedEnv) BothSets() []string          // hydration order
    ```

    Every method answers on a nil receiver as an empty hydration (the existing nil-map contract).
  - `envsources.go`: `envEntry` gains `private bool`; `envEntries` appends, after `env.set` and before
    `env.unset`, every `env.secrets.files` path then `env.secrets.set` as one inline table, `private: true`.
    `anchorReadableEnvSources` anchors `env.secrets.files` too. `ResolveEnvSourcesFull` skips private
    entries (temporary; deleted in Task 4). `DescribeEnvSources` lists private entries;
    `describeInline` labels them `env.secrets.set`. `EnvEntries` (the migration proof's reader) skips
    private entries: a legacy file never has one.
  - `HasEnvFileEntry` and `HasEnvEntries` count private entries (the sealed check reads them).
- **Gates:** the red command, green; then `env -u YOLO_VERSION go test ./internal/config/... -count=1`.
- **Fences:** nothing outside `internal/config`. No schema row (Task 3).
- **Commit:** `feat(config): hydration keeps which env set declared each name`
- **Report:** the red output, the green run, and whether `envEntries`' ordering comment needed more than
  one added list item.

### Task 2 — the gate scopes by the set bit and words the new lines

**Independent of Task 1** (packload only); land after it. No behavior change for a caller that passes no
`Private` ([SEP-D4](#SEP-D4)).

- **Settled** *(do not re-open)*: the four outcomes of [§3.4](secret-env-scoping.md#34-the-four-outcomes);
  "An MCP server's `requires_env` claims a name too, for the agents whose config names that server, and
  delivers a private value to those agents only" ([SE-D13](secret-env-scoping.md#SE-D13)); "Only a
  provider's claim makes a public value a refusal … a public name only an MCP server claims stays public"
  ([SE-D15](secret-env-scoping.md#SE-D15)); "The shipped set is read to word a line, never to decide a
  reach" ([SE-D8](secret-env-scoping.md#SE-D8)). Line texts, exactly, from
  [§3.6](secret-env-scoping.md#36-the-lines):
  - row 4: `  NAME: withheld from every process — nothing in this launch claims it.` then one remedy in the
    same string: ` To use it as zai's key: yolo pack add zai.` when a shipped unselected pack's provider
    claims it, else ` If no provider or MCP server needs it, it belongs in the public set: yolo config unsecret NAME<scope>`
  - row 1, unselected: `  NAME reaches every process from your public environment; the zai provider, which this launch does not select, claims that name. If it is zai's key: yolo config secret NAME<scope>`
  - MCP group: `  TAVILY_API_KEY (mcp tavily): claude, pi only`
- **Preconditions:** `rg -n 'func ScopeCredentials|func \(s \*CredentialScope\) receives|func \(s \*CredentialScope\) DisclosureWith|func unselectedEmbedded' internal/packload` prints four hits.
- **Tests first:** `internal/packload/credentialscope_secret_test.go` (new).

  ```go
  package packload

  import (
  	"strings"
  	"testing"
  )

  func TestAPrivateNameNothingClaimsReachesNobody(t *testing.T) {
  	scope, err := ScopeCredentials(ScopeInput{
  		Providers:  twoProviders(t),
  		Profiles:   map[string]string{"pi": "zai-profile"},
  		Resolved:   map[string]ResolvedProfile{"zai-profile": {Provider: "zai"}},
  		EnvSources: hydrated("ZAI_API_KEY", "z", "STRIPE_KEY", "s", "GH_TOKEN", "g"),
  		Private:    map[string]bool{"ZAI_API_KEY": true, "STRIPE_KEY": true},
  	})
  	if err != nil {
  		t.Fatal(err)
  	}
  	if got := strings.Join(scope.SharedEnvSources().Keys(), ","); got != "GH_TOKEN" {
  		t.Errorf("shared = %s, want only the public GH_TOKEN", got)
  	}
  	if got := strings.Join(scope.Agent("pi").EnvSources.Keys(), ","); got != "ZAI_API_KEY" {
  		t.Errorf("pi's own = %s, want its provider's private key", got)
  	}
  	if scope.DeliversEnvSource("STRIPE_KEY") {
  		t.Error("a private name nothing claims reaches no process (row 4)")
  	}
  	lines := strings.Join(scope.DisclosureWith(DisclosureNotes{}), "\n")
  	want := "  STRIPE_KEY: withheld from every process — nothing in this launch claims it. " +
  		"If no provider or MCP server needs it, it belongs in the public set: yolo config unsecret STRIPE_KEY"
  	if !strings.Contains(lines, want) || strings.Contains(lines, "=s") {
  		t.Errorf("row 4's line, names only:\n%s", lines)
  	}
  }

  func TestAnMCPServersRequiresEnvClaimsAPrivateNameForItsAgents(t *testing.T) {
  	scope, err := ScopeCredentials(ScopeInput{
  		Providers:  twoProviders(t),
  		EnvSources: hydrated("TAVILY_API_KEY", "t"),
  		Private:    map[string]bool{"TAVILY_API_KEY": true},
  		MCPClaims:  map[string][]string{"TAVILY_API_KEY": {"tavily"}},
  		MCPAgents:  []string{"claude", "pi"},
  	})
  	if err != nil {
  		t.Fatal(err)
  	}
  	for _, agent := range []string{"claude", "pi"} {
  		if _, ok := scope.EnvSourcesFor(agent).Get("TAVILY_API_KEY"); !ok {
  			t.Errorf("%s names the server, so it receives the key", agent)
  		}
  	}
  	if _, ok := scope.EnvSourcesFor("").Get("TAVILY_API_KEY"); ok {
  		t.Error("a bare shell does not name the server and must not receive the key")
  	}
  	if lines := strings.Join(scope.DisclosureWith(DisclosureNotes{}), "\n"); !strings.Contains(lines, "  TAVILY_API_KEY (mcp tavily): claude, pi only") {
  		t.Errorf("the MCP claim's line:\n%s", lines)
  	}
  }

  func TestAPublicNameOnlyAnMCPServerClaimsStaysShared(t *testing.T) {
  	scope, _ := ScopeCredentials(ScopeInput{
  		EnvSources: hydrated("TAVILY_API_KEY", "t"),
  		MCPClaims:  map[string][]string{"TAVILY_API_KEY": {"tavily"}},
  		MCPAgents:  []string{"claude"},
  	})
  	if _, ok := scope.SharedEnvSources().Get("TAVILY_API_KEY"); !ok {
  		t.Error("SE-D15: an MCP claim never narrows a public value")
  	}
  	if len(scope.PublicClaims()) != 0 {
  		t.Error("only a provider's claim makes a public value a refusal")
  	}
  }

  func TestAPublicNameAProviderClaimsIsReportedAndNotWidened(t *testing.T) {
  	scope, _ := ScopeCredentials(ScopeInput{
  		Providers:  twoProviders(t),
  		EnvSources: hydrated("ZAI_API_KEY", "z"),
  	})
  	pc := scope.PublicClaims()
  	if len(pc) != 1 || pc[0].Name != "ZAI_API_KEY" || strings.Join(pc[0].Providers, ",") != "zai" {
  		t.Errorf("PublicClaims = %+v, want ZAI_API_KEY claimed by zai", pc)
  	}
  	if _, ok := scope.SharedEnvSources().Get("ZAI_API_KEY"); ok {
  		t.Error("SEP-D4: a public claimed name stays scoped while the caller refuses")
  	}
  }

  func TestAnUnselectedShippedProviderWordsAPublicNamesLine(t *testing.T) {
  	scope, _ := ScopeCredentials(ScopeInput{
  		EnvSources:       hydrated("ZAI_API_KEY", "z"),
  		UnselectedClaims: map[string][]ShippedClaim{"ZAI_API_KEY": {{Provider: "zai", Pack: "zai"}}},
  	})
  	if _, ok := scope.SharedEnvSources().Get("ZAI_API_KEY"); !ok {
  		t.Error("row 1: the public declaration stands when nothing selected claims the name")
  	}
  	want := "  ZAI_API_KEY reaches every process from your public environment; the zai provider, " +
  		"which this launch does not select, claims that name. If it is zai's key: yolo config secret ZAI_API_KEY"
  	if lines := strings.Join(scope.DisclosureWith(DisclosureNotes{}), "\n"); !strings.Contains(lines, want) {
  		t.Errorf("row 1's unselected line:\n%s", lines)
  	}
  }

  func TestAPrivateNameAnUnselectedPackClaimsNamesThePack(t *testing.T) {
  	scope, _ := ScopeCredentials(ScopeInput{
  		EnvSources:       hydrated("ZAI_API_KEY", "z"),
  		Private:          map[string]bool{"ZAI_API_KEY": true},
  		UnselectedClaims: map[string][]ShippedClaim{"ZAI_API_KEY": {{Provider: "zai", Pack: "zai"}}},
  	})
  	want := "  ZAI_API_KEY: withheld from every process — nothing in this launch claims it. To use it as zai's key: yolo pack add zai."
  	if lines := strings.Join(scope.DisclosureWith(DisclosureNotes{}), "\n"); !strings.Contains(lines, want) {
  		t.Errorf("row 4's pack remedy:\n%s", lines)
  	}
  }

  func TestTheFallbackStillScopesAClaimedNameNobodyHydrated(t *testing.T) {
  	scope, _ := ScopeCredentials(ScopeInput{
  		Providers: twoProviders(t),
  		Profiles:  map[string]string{"pi": "routes-profile"},
  		Resolved:  map[string]ResolvedProfile{"routes-profile": {Provider: "routes"}},
  		NoDerives: true,
  		Fallback:  func(string) (string, bool) { return "from-shell", true },
  	})
  	if v, ok := scope.LookupFor("pi")("ZAI_API_KEY"); ok {
  		t.Errorf("a claimed name only the launching shell holds reaches its claimants only, got %q", v)
  	}
  }
  ```

  Red: `go test ./internal/packload -run 'PrivateName|MCPServer|PublicName|UnselectedShipped|FallbackStill' -count=1`
  → build failure, `unknown field Private in struct literal of type ScopeInput`.
- **Change:**
  - `ScopeInput` gains `Private map[string]bool`, `MCPClaims map[string][]string` (name → server names),
    `MCPAgents []string`, `UnselectedClaims map[string][]ShippedClaim`; each with a doc comment naming its
    SE anchor. New `type ShippedClaim struct{ Provider, Pack string }`, `type PublicClaim struct{ Name string; Providers []string }`.
  - `CredentialScope` keeps `private`, `mcpClaims`, `mcpAgents`, `unselected`. The rule
    `receives`/`delivers` answer, in this order: a name hydrated **public** → every process, unless a
    provider claims it (then today's claimant rule, [SEP-D4](#SEP-D4)); a name hydrated **private** →
    provider claimants as today, plus every `MCPAgents` agent when `MCPClaims` names it, else nobody; a
    name **not hydrated** → today's rule unchanged.
  - `ScopeCredentials`: the shared loop (~`credentialscope.go:245`) keeps a name when the rule above gives
    it to every process; `deliveryAgents` adds every `MCPAgents` agent that some private MCP-claimed name
    reaches, with no profile (the grant-only shape: no derive runs for it).
  - `PublicClaims()`: public names any composed provider claims, hydration order, providers sorted.
  - `DisclosureWith`: MCP groups use `(mcp <servers>)` in place of `(provider …)`; append row 4 lines and
    unselected row 1 lines after the existing groups, hydration order. `DisclosureNotes` gains
    `Scope func(name string) string`, returning `" --user"`, `" --project"`, `" --local"` or `""`; nil is
    `""`. At the host, a row 4 name `Inherited` reports keeps the existing `inherited` wording and appends
    the row 4 remedy ([§4.22](secret-env-scoping.md#422-the-same-cases-at-yolo-host)).
  - `GrantFor` and `ClaimingProviders` read private names only when `Private` is non-nil.
  - `unselectedclaims.go` (new): `UnselectedProviderClaims(selected []*Pack) map[string][]ShippedClaim`,
    over `unselectedEmbedded(selected)`, one `ComposeProvidersAt(nil, []*Pack{p}, …)` per pack.
  - `mcpclaims.go` (new): `MCPEnvClaims(servers *jsonx.OrderedMap) map[string][]string` from each entry's
    `requires_env`; `MCPAgents(packs []*Pack) []string` per [SEP-D5](#SEP-D5).
  - Cheap and yours: internal field names, and whether the row 4 list is a slice or recomputed in
    `DisclosureWith`.
- **Gates:** the red command, green; `go test ./internal/packload/... -count=1`.
- **Fences:** `internal/packload` only. Every existing `credentialscope_test.go` case stays green unedited:
  a red one means the nil-`Private` path changed behavior; stop and report.
- **Commit:** `feat(packload): the credential gate scopes by the declared env set and MCP claims`
- **Report:** red and green runs, and the count of existing gate tests that ran unedited.

### Task 3 — `[env.secrets]` becomes writable and reaches the jail

**After 1 and 2.** Behavior change: the private set works at the jail notch (rows 3 and 4 and the new
lines). A public provider key still behaves as today; the refusal is Task 7.

- **Settled** *(do not re-open)*: [§3.3](secret-env-scoping.md#33-the-two-sets)'s key table;
  "A workspace private file must lie inside the workspace, refused with the user-scope spelling as `[env]
  files` is today" ([SE-D5](secret-env-scoping.md#SE-D5)); "A private name nothing claims starts the
  launch, reaches nobody, and prints a line … on every launch" ([SE-D12](secret-env-scoping.md#SE-D12));
  vehicles unchanged ([§3.7](secret-env-scoping.md#37-what-each-notch-writes)).
- **Preconditions:** Tasks 1 and 2 landed; `rg -n 'env.unset.\*' internal/config/schema.go` prints one hit.
- **Tests first:**
  1. `internal/config/envsourcescope_test.go`, new case `TestAWorkspacePrivateFileOutsideTheWorkspaceIsRefused`:
     mirror the existing `env.files` refusal case in that file with `[env.secrets] files =
     ["~/.config/yolo-jail/secrets.env"]` in `yolo-jail.local.toml`; assert the error contains
     `config.env.secrets.files:` and `` `yolo config add env.secrets.files '"~/.config/yolo-jail/secrets.env"' --user` ``.
  2. `internal/cli/run/secretenv_test.go` (new):

     ```go
     func TestTheSharedFileCarriesNoPrivateValue(t *testing.T) {
     	home := packHome(t)
     	o := goldenOptions(t.TempDir(), home)
     	var stderr bytes.Buffer
     	o.Stderr = &stderr
     	o.ProfileName = "zai"
     	h := config.NewHydratedEnv()
     	h.SetPublic("GH_TOKEN", "gh-test", config.EnvPlace{Key: "env.set"})
     	h.SetPrivate("ZAI_API_KEY", "tok-zai", config.EnvPlace{Private: true, Key: "env.secrets.set"})
     	h.SetPrivate("STRIPE_KEY", "tok-stripe", config.EnvPlace{Private: true, Key: "env.secrets.set"})
     	channel, err := o.composePackChannel(bareConfig(), setPacks(t), h)
     	if err != nil {
     		t.Fatal(err)
     	}
     	shared := string(userEnvFileBody(channel.sharedEnvSourceWinners(), channel))
     	// The YOLO_PROVIDERS table names ZAI_API_KEY as zai's api_key_env_name, so the check is the export.
     	if !strings.Contains(shared, "export GH_TOKEN=") || strings.Contains(shared, "export ZAI_API_KEY=") ||
     		strings.Contains(shared, "STRIPE_KEY") || strings.Contains(shared, "tok-") {
     		t.Errorf("yolo-user-env.sh must hold the public set only:\n%s", shared)
     	}
     	if _, ok := channel.scope.EnvSourcesFor("claude").Get("ZAI_API_KEY"); !ok {
     		t.Error("claude on zai receives the private, claimed key")
     	}
     	o.noteCredentialScope(channel)
     	if out := stderr.String(); !strings.Contains(out, "STRIPE_KEY: withheld from every process — nothing in this launch claims it.") ||
     		strings.Contains(out, "tok-") {
     		t.Errorf("row 4's line, names only:\n%s", out)
     	}
     }
     ```

  3. `integration/secretenv_test.go` (new):

     ```go
     func TestAPrivateKeyReachesOnlyItsClaimingAgent(t *testing.T) {
     	requireJail(t)
     	writeUserTOML(t, "packs = [\"claude\", \"zai\"]\n\n[active_provider]\nclaude = \"zai\"\n\n"+
     		"[env]\nset = { GH_TOKEN = \"gh-probe\" }\n\n"+
     		"[env.secrets]\nset = { ZAI_API_KEY = \"zai-probe-not-a-real-key\", STRIPE_KEY = \"stripe-probe\" }\n")
     	dir := writeWorkspaceTOML(t, "")
     	r := runYolo(t, dir, `echo "SHELL=[${ZAI_API_KEY-}][${STRIPE_KEY-}][${GH_TOKEN-}]"; `+
     		`grep -c ZAI_API_KEY ~/.config/yolo-agent-env/claude.sh`)
     	if r.rc != 0 {
     		t.Fatalf("rc %d\n%s", r.rc, r.combined())
     	}
     	if !strings.Contains(r.stdout, "SHELL=[][][gh-probe]") {
     		t.Errorf("a bare shell holds the public set only:\n%s", r.combined())
     	}
     	if !strings.Contains(r.stdout, "\n1\n") && !strings.HasSuffix(strings.TrimSpace(r.stdout), "1") {
     		t.Errorf("claude's agent env file carries the private key:\n%s", r.combined())
     	}
     	if !strings.Contains(r.stderr, "STRIPE_KEY: withheld from every process — nothing in this launch claims it.") ||
     		strings.Contains(r.combined(), "stripe-probe") {
     		t.Errorf("row 4's line, never the value:\n%s", r.combined())
     	}
     }
     ```

  Red: `env -u YOLO_VERSION -u YOLO_ALLOW_MISSING_PROGRAMS go test ./internal/cli/run -run TheSharedFileCarriesNoPrivateValue -count=1`
  → build failure, `cannot use h (variable of type *config.HydratedEnv) as *jsonx.OrderedMap value`.
- **Change:**
  - `schema.go` ~`:230`: rows `env.secrets` (object), `env.secrets.files` (list), `env.secrets.files.*`
    (string), `env.secrets.set` (object), `env.secrets.set.*` (string), all `Scope: both`.
  - `envsourcescope.go`: `validateEnvSourceScope` adds `check("env.secrets.files", …)` over
    `wsNode.key("env").key("secrets").key("files")`; `hasEnvSourceFile` sees private files (Task 1 made
    `HasEnvFileEntry` count them); `envSourceScopeRefusal` spells `[env.secrets] files = [...]` and
    `yolo config add env.secrets.files … --user` when `key` is `env.secrets.files`.
  - `inherit.go` ~`:643`: `copyKeys(m, out, "sources", "files", "set", "unset")`: `"secrets"` was cut ([SEP-D16](#SEP-D16), superseded by [SE-D16](secret-env-scoping.md#SE-D16)).
  - `profilechannel.go`: `composePackChannel(cfg, packs, userEnv *config.HydratedEnv)`; nil hydrates with
    `config.ResolveEnv`. `ScopeInput`: `EnvSources: userEnv.Values()`, `EnvSourceRemovals:
    userEnv.Removals()`, `Private: userEnv.Private()`, `MCPClaims:
    packload.MCPEnvClaims(packload.ComposeMCPServers(cfgMap(cfg, "mcp_servers"), packs, ""))`,
    `MCPAgents: packload.MCPAgents(packs)`, `UnselectedClaims: packload.UnselectedProviderClaims(packs)`.
    `packChannel.userEnv` becomes `*config.HydratedEnv`; the type change follows in `run.go`, `packtree.go`,
    `loopholegrants.go`, `seal.go`, `assemble.go` (`assembleInput.userEnv`).
  - `jailgrant.go`: `packload.ResolveGrant(o.WithCredentials, channel.providers, channel.userEnv.Values())`.
  - `programcapture.go` ~`:254`: read `config.ResolveEnv("", cfg, nil).Values()` (both sets: a private value
    still reaches an agent's runtime).
  - `check/entrypoint.go` ~`:63`: the preflight's `vars` take only the public values (`!IsPrivate`): a
    private value never enters the boot environment.
  - `credentialnotes.go`: `DisclosureNotes.Scope` from `config.LocateEnvName` once Task 5 lands; until
    then nil (the remedy prints without a scope flag). Mark the call site with a one-line comment naming
    Task 5's function so the follow-up is a one-line change.
  - `config_ref.txt` env entry (~`:735`): a `[env.secrets]` paragraph — `files` and `set`, both scopes,
    reaches only the agents whose selected provider or MCP server claims the name, nobody otherwise.
  - Rewrite (not repair) every run test that hands `composePackChannel` a `*jsonx.OrderedMap`: the fixture
    helpers (`zaiAndRouterKeys` in [`activeset_test.go`](../../internal/cli/run/activeset_test.go),
    `cerebrasKey` in [`cerebraspack_test.go`](../../internal/cli/run/cerebraspack_test.go), and the rest
    `rg -n 'composePackChannel\(' internal/cli/run --glob '*_test.go'` lists) return a `*config.HydratedEnv`
    with every provider key `SetPrivate` and every other name `SetPublic`. A test asserting a provider key
    reaches its claimant keeps its assertion.
- **Gates:** the red command, green; `env -u YOLO_VERSION -u YOLO_ALLOW_MISSING_PROGRAMS go test ./internal/config/... ./internal/packload/... ./internal/cli/... -count=1`;
  `go test ./internal/setupcensus -count=1` (a new schema key may need a census row: add it, do not skip it);
  `YOLO_NO_AUTO_IMAGE_REAP=1 go test -count=1 -run 'APrivateKeyReachesOnlyItsClaimingAgent|ATOMLEnvSetReachesTheJail|AJailLaunchedWithCredentials|EnvGatedMCP' ./integration`;
  nested-jail check per [AGENTS.md Workflow step 2](../../AGENTS.md#workflow): `just build-dev-bundle`, then
  its launcher from a throwaway workspace holding the integration test's user config, reading
  `~/.config/yolo-agent-env/claude.sh` and `~/.config/yolo-user-env.sh`.
- **Parity:** podman and Apple Container share `deliverChannel` and `writeAgentEnvFiles`; macos-user
  (both Seatbelt setups) reads the same `launchChannel` answer through
  [`envfile.go`](../../internal/macosuser/envfile.go), unchanged, and its integration test
  [`macosuserenvgatedmcp_test.go`](../../integration/macosuserenvgatedmcp_test.go) is in the run list above
  (it skips off a Mac; say so in the report). `yolo host` is Task 4.
- **Fences:** no file under `internal/cli/host*.go`, `internal/cli/check/envoverrides.go`, or any
  `integration/` file other than `secretenv_test.go`.
- **Commit:** `feat(env): [env.secrets] reaches only the agents that claim each name`
- **Report:** red and green runs, the integration list's results, the nested-jail transcript lines, the
  number of run fixtures rewritten.

### Task 4 — the host notch and `yolo check` read the private set

**After 3.** Behavior change at `yolo host --`, `yolo host env`, and `yolo check` (rows 3 and 4 and the
new lines, as WARN in check per [SEP-D9](#SEP-D9)).

- **Settled** *(do not re-open)*: "`yolo host -- <cmd>` composes one process from your user config only";
  "a private name withheld that your shell also exports … the command holds your shell's value; the line
  reads `GH_TOKEN: not added by yolo — …` and still names `yolo config unsecret`"
  ([§4.22](secret-env-scoping.md#422-the-same-cases-at-yolo-host)); "`yolo check` predicts the same four
  outcomes from the same gate" ([§3.7](secret-env-scoping.md#37-what-each-notch-writes)).
- **Preconditions:** Task 3 landed; `rg -n 'config.ResolveEnvSourcesFull' internal/cli` prints exactly the
  `host.go` and `check/envoverrides.go` hits.
- **Tests first:**
  1. `internal/cli/hostsecretenv_test.go` (new), in the shape of
     [`hostcredentialgate_test.go`](../../internal/cli/hostcredentialgate_test.go): a user config with
     `packs = ["zai"]`, `[env.secrets] set = { ZAI_API_KEY = "z-probe", STRIPE_KEY = "s-probe" }`, `[env]
     set = { GH_TOKEN = "g" }`; `composeHostVars(cfg, ws, "bash", "", warn)` → the vars hold `GH_TOKEN`
     and neither key; `credentialScopeLines()` contains `STRIPE_KEY: withheld from every process — nothing
     in this launch claims it.` and ` --user`; with `t.Setenv("STRIPE_KEY", "shell")` the line says `not
     added by yolo` and still names `yolo config unsecret STRIPE_KEY --user`.
  2. `internal/cli/check/secretenv_test.go` (new): a merged config with `[env.secrets] set = { STRIPE_KEY = "s" }`
     and no claiming pack → `secretEnvGap` returns one warning whose message contains `STRIPE_KEY: withheld
     from every process` and not its value (`s-probe`; a one-letter value matches every message); and
     the Packs section prints it (the call site, not only the helper).

  Red: `env -u YOLO_VERSION go test ./internal/cli -run HostSecretEnv -count=1` → the vars assertion
  passes (Task 1 made `ResolveEnvSourcesFull` skip private entries, so the host already drops them) and
  the line assertion fails: `credentialScopeLines` lacks the row 4 line.
- **Change:**
  - `host.go` `composeHostVarsWith` (~`:2838`): `h := config.ResolveEnv(workspace, scoped, warn)`; pass
    `h.Values()`, `h.Removals()`, `h.Private()`, MCP claims over the host's own `packs` and user
    `mcp_servers`, `UnselectedClaims`. `hostScopedEnvSources` (~`:3724`) filters `env.secrets.files` the way
    it filters `env.files`. `resolveHostGrant` takes `h.Values()`. `credentialScopeLines` sets
    `Scope: func(string) string { return " --user" }` (the host reads user scope only).
  - `check/envoverrides.go` `overrideScope`: same inputs from `config.ResolveEnv`; `envOverrideGap`
    returns the scope it composed, so the Packs section reads the env-set lines from the one hydration.
  - `packload` `CredentialScope.EnvSetLines(notes)`: `DisclosureWith`'s env-set lines alone.
  - `check/secretenv.go` (new): `secretEnvGap(scope)` → one warning per `EnvSetLines` line (row 4 and
    the unselected row 1); `packs.go` ~`:472` calls it beside `envOverrideGap`. The scope flag waits
    for Task 5's `LocateEnvName`, as `credentialnotes.go`'s does.
  - Delete `ResolveEnvSources`, `ResolveEnvSourcesFull`, `HydrateEnvSources`, `SplitHydratedEnvSources`,
    `EnvSourceRemovals` and their tests; move any still-meaningful case of
    [`envsourceshydrate_test.go`](../../internal/config/envsourceshydrate_test.go) onto `ResolveEnv`
    (the later-file-cancels-a-null case must survive).
- **Gates:** red, green; `env -u YOLO_VERSION -u YOLO_ALLOW_MISSING_PROGRAMS go test ./internal/... -count=1`;
  the trap's `rg` prints no reader; `YOLO_NO_AUTO_IMAGE_REAP=1 go test -count=1 -run 'Host|Check' ./integration`
  restricted by `rg -l 'yolo host|"check"' integration` to the files that run those verbs.
- **Parity:** `yolo host` is the one exec'd process; macOS host and Linux host share `host.go`.
- **Fences:** no `internal/cli/run` file.
- **Commit:** `feat(env): yolo host and yolo check read the private env set`
- **Report:** red and green runs, the deleted functions, the integration subset run.

### Task 5 — `yolo config secret|unsecret <NAME> [--drop]`

**After 3** (needs the schema rows). May run beside Task 4.

- **Settled** *(do not re-open)*, [SE-D9](secret-env-scoping.md#SE-D9) and
  [§3.5](secret-env-scoping.md#35-the-refusal): `secret NAME` finds the public entry setting `NAME` and moves
  it into the private set of the same scope, "an inline value to `[env.secrets] set` and a dotenv line to
  the first file `[env.secrets] files` lists. When that scope lists no private file yet, the verb refuses and
  names the one to run first". `unsecret` is the inverse. `--drop` "removes the other set's copy of the name
  instead of moving one, so `secret NAME --drop` keeps the private copy and `unsecret NAME --drop` the
  public one". "Neither prints a value. The scope flags are the generic verbs' own: `--user`, `--project`,
  `--local`, `--file`." On a legacy JSONC config the text names `yolo config migrate` first. In a jail, a
  user-scope edit prints the `host-run` line.
- **Preconditions:** Task 3 landed; `rg -n 'case "set", "add", "unset":' internal/cli/config.go` prints one hit.
- **Tests first:** `internal/cli/configsecret_test.go` (new).

  ```go
  func TestConfigSecretMovesOneDotenvLineToThePrivateFile(t *testing.T) {
  	user := testconfig.User(t, "[env]\nfiles = [\"defaults.env\"]\n\n[env.secrets]\nfiles = [\"secrets.env\"]\n")
  	t.Setenv("HOME", user.Root)
  	t.Setenv("YOLO_VERSION", "")
  	dir := filepath.Dir(user.Path)
  	pub, priv := filepath.Join(dir, "defaults.env"), filepath.Join(dir, "secrets.env")
  	os.WriteFile(pub, []byte("GH_TOKEN=g\nexport ZAI_API_KEY=\"zai-value\"\n"), 0o600)
  	var out, errw bytes.Buffer
  	rc := configRunW([]string{"secret", "ZAI_API_KEY", "--user"}, &out, &errw)
  	if rc != 0 {
  		t.Fatalf("rc %d: %s", rc, errw.String())
  	}
  	if got, _ := os.ReadFile(pub); string(got) != "GH_TOKEN=g\n" {
  		t.Errorf("the public file keeps only the other line: %q", got)
  	}
  	if got, _ := os.ReadFile(priv); string(got) != "export ZAI_API_KEY=\"zai-value\"\n" {
  		t.Errorf("the private file gains the line verbatim: %q", got)
  	}
  	if info, _ := os.Stat(priv); info.Mode().Perm() != 0o600 {
  		t.Errorf("a created private file is 0600, got %v", info.Mode().Perm())
  	}
  	if strings.Contains(out.String()+errw.String(), "zai-value") {
  		t.Error("the verb never prints a value")
  	}
  }

  func TestConfigSecretMovesAnInlineValue(t *testing.T) {
  	user := testconfig.User(t, "[env]\nset = { ZAI_API_KEY = \"zai-value\", DEBUG = \"1\" }\n")
  	t.Setenv("HOME", user.Root)
  	t.Setenv("YOLO_VERSION", "")
  	var out, errw bytes.Buffer
  	if rc := configRunW([]string{"secret", "ZAI_API_KEY", "--user"}, &out, &errw); rc != 0 {
  		t.Fatalf("rc %d: %s", rc, errw.String())
  	}
  	got, _ := os.ReadFile(user.Path)
  	cfg := string(got)
  	if !strings.Contains(cfg, "[env.secrets]") || strings.Count(cfg, "ZAI_API_KEY") != 1 || !strings.Contains(cfg, "DEBUG") {
  		t.Errorf("the inline value moved to [env.secrets] set and DEBUG stayed:\n%s", cfg)
  	}
  	if strings.Contains(out.String()+errw.String(), "zai-value") {
  		t.Error("the verb never prints a value")
  	}
  }

  func TestConfigSecretWithNoPrivateFileNamesTheOneToAddFirst(t *testing.T) {
  	user := testconfig.User(t, "[env]\nfiles = [\"defaults.env\"]\n")
  	t.Setenv("HOME", user.Root)
  	t.Setenv("YOLO_VERSION", "")
  	os.WriteFile(filepath.Join(filepath.Dir(user.Path), "defaults.env"), []byte("ZAI_API_KEY=z\n"), 0o600)
  	var out, errw bytes.Buffer
  	rc := configRunW([]string{"secret", "ZAI_API_KEY", "--user"}, &out, &errw)
  	want := `no private file to move ZAI_API_KEY into; run yolo config add env.secrets.files '"~/.config/yolo-jail/secrets.env"' --user, then this command again`
  	if rc == 0 || !strings.Contains(errw.String(), want) {
  		t.Errorf("rc %d, stderr %q", rc, errw.String())
  	}
  }

  func TestConfigSecretDropKeepsThePrivateCopy(t *testing.T) {
  	user := testconfig.User(t, "[env]\nset = { ZAI_API_KEY = \"old\" }\n\n[env.secrets]\nset = { ZAI_API_KEY = \"new\" }\n")
  	t.Setenv("HOME", user.Root)
  	t.Setenv("YOLO_VERSION", "")
  	var out, errw bytes.Buffer
  	if rc := configRunW([]string{"secret", "ZAI_API_KEY", "--drop", "--user"}, &out, &errw); rc != 0 {
  		t.Fatalf("rc %d: %s", rc, errw.String())
  	}
  	got, _ := os.ReadFile(user.Path)
  	if strings.Contains(string(got), "old") || !strings.Contains(string(got), "new") {
  		t.Errorf("--drop removes the public copy and keeps the private one:\n%s", got)
  	}
  }
  ```

  Plus `TestConfigUnsecretMovesBack` (the mirror of the first test) and, in
  [`configreftoml_test.go`](../../internal/cli/configreftoml_test.go),
  `TestConfigRefTOMLSectionNamesTheVerbsTheCLIHas`'s phrase list gains `"yolo config secret"` and
  `"yolo config unsecret"`.

  Red: `env -u YOLO_VERSION go test ./internal/cli -run 'ConfigSecret|ConfigUnsecret|ConfigRefTOMLSectionNamesTheVerbs' -count=1`
  → `TestConfigSecret…` fail with rc 1 and the existing `unknown subcommand` text from `configRunW`;
  `TestConfigRefTOMLSectionNamesTheVerbsTheCLIHas` fails with `does not name \`yolo config secret\``.
- **Change:**
  - `config.go` `configRunW`: `case "secret", "unsecret": return runConfigSecret(args, out, errw, configEditWorkspace())`;
    `configHelpToken` gains `secret`, `unsecret`, `--drop`; the usage text gains the two lines.
  - `internal/cli/configsecret.go` (new): `runConfigSecret(args []string, out, errw io.Writer, workspace string) int`.
    Flag parsing mirrors `runConfigEditWithMode`; exactly one positional, a name matching `^[A-Za-z_][A-Za-z0-9_]*$`.
    Steps: locate ([SEP-D11](#SEP-D11)); refuse legacy (`config.LocateEnvName` reports the format) with
    `run yolo config migrate, then yolo config secret NAME --user`; refuse in-jail user scope through
    `printConfigEditHostRun`; write destination, then remove source ([SEP-D10](#SEP-D10)); print one line
    `moved ZAI_API_KEY from <place> to <place>` per [SEP-D7](#SEP-D7)'s place spelling.
  - `internal/config/envlocate.go` (new): `LocateEnvName(workspace, name string, userOnly bool) ([]EnvSite, error)`,
    `type EnvSite struct{ Place EnvPlace; Scope string /* "--user", "--project", "--local" */; Legacy bool; ConfigFile string }`;
    reads `LoadReadableConfigWithSources`, walks `env.files`, `env.sources`, `env.set`, `env.secrets.files`,
    `env.secrets.set`, reads each listed dotenv with `ParseDotenv`, maps the writer's `editReadRole` to the
    flag ([SEP-D6](#SEP-D6)).
  - `internal/config/dotenvedit.go` (new): `RemoveDotenvName(path, name string) (line string, found bool, err error)`
    and `AppendDotenvLine(path, line string) error`; atomic write by temp file and rename in the same
    directory, mode kept or `0600` ([SEP-D12](#SEP-D12)).
  - Inline moves go through `configedit.ResolveTarget` + `configedit.ApplyTargeted` with
    `configedit.Mutation{Action: configedit.Add|configedit.Remove, Path: …}` (`env.secrets.set.NAME` /
    `env.set.NAME`); `addMissingKey`'s parent-table behavior covers a missing `[env.secrets]`.
  - Wire `DisclosureNotes.Scope` in `credentialnotes.go` to `LocateEnvName` (the Task 3 marker), and in
    `check/secretenv.go` (the Task 4 marker), through `config.EnvNameScope`.
  - **As built:** `--dotenv <path>` moves a file entry ([SEP-D17](#SEP-D17)); the inline test asserts the parsed
    values, not the substring `[env.secrets]`, because `addMissingKey` writes a missing table as the
    `[env.secrets.set]` header, which says the same; `LocateEnvName` reads each scope's top-level file with
    `configdoc.Parse` rather than `LoadReadableConfigWithSources`, because a move edits that file.
  - `config_ref.txt`: the TOML section's verb list (~`:140`) gains
    `yolo config secret|unsecret <NAME> [--drop] [--user|--project|--local]` with a one-line gloss; the env
    entry names both verbs.
- **Gates:** red, green; `env -u YOLO_VERSION go test ./internal/cli/... ./internal/config/... -count=1`.
  No integration test reaches these verbs today (`rg -n 'config secret' integration` is empty); none is
  added: the unit tests drive `configRunW`, the real dispatch.
- **Parity:** host-only verb, like every `yolo config` edit; in a jail the user scope answers with the
  host-run line, a workspace scope edits in place (the workspace is the same bytes).
- **Fences:** no gate, run or host file except the one `credentialnotes.go` line.
- **Commit:** `feat(config): yolo config secret and unsecret move a name between env sets`
- **Report:** red and green runs; any case where `ApplyTargeted` printed or required a value in its error
  text (that would leak; stop and report).

### Task 6 — fixtures hold provider keys in the private set

**After 3 and 4.** Tests only; every test still passes before and after, because the private set already
works. This is what keeps Task 7 from going red across the suite.

- **Change:** every test that hands a provider-claimed name to a launch or a gate through the public set
  moves it to the private set, rewritten (not relaxed): the assertion about who receives the key stays.
  - Integration, legacy JSONC `env_sources` with a provider key → `writeUserTOML` with `[env.secrets]`
    (the legacy spelling has no private set, [SE-D6](secret-env-scoping.md#SE-D6)). The files, from
    `rg -l '_API_KEY|AWS_BEARER|AWS_ACCESS_KEY|AWS_PROFILE' integration` crossed with an env spelling:
    [`codex_selection_test.go`](../../integration/codex_selection_test.go),
    [`jailgrant_test.go`](../../integration/jailgrant_test.go),
    [`launchshellcredential_test.go`](../../integration/launchshellcredential_test.go),
    [`macosuserbridge_test.go`](../../integration/macosuserbridge_test.go),
    [`modelroles_test.go`](../../integration/modelroles_test.go),
    [`omp_active_set_test.go`](../../integration/omp_active_set_test.go),
    [`opencode_active_set_test.go`](../../integration/opencode_active_set_test.go),
    [`pi_active_set_test.go`](../../integration/pi_active_set_test.go),
    [`pi_opencode_selection_test.go`](../../integration/pi_opencode_selection_test.go),
    [`profiles_options_test.go`](../../integration/profiles_options_test.go),
    [`providers_test.go`](../../integration/providers_test.go),
    [`requiredservicehatch_test.go`](../../integration/requiredservicehatch_test.go),
    [`servedports_test.go`](../../integration/servedports_test.go),
    [`sessionhome_test.go`](../../integration/sessionhome_test.go),
    [`wirebridge_test.go`](../../integration/wirebridge_test.go),
    [`wirebridgecallertoken_test.go`](../../integration/wirebridgecallertoken_test.go), and
    [`bedrock_test.go`](../../integration/bedrock_test.go) and
    [`macosuserenvgatedmcp_test.go`](../../integration/macosuserenvgatedmcp_test.go) where their configs carry
    a claimed name. Re-run the census before starting; the tree wins.
  - Unit: host tests under `internal/cli` that write `env_sources` with a claimed name
    (`rg -ln 'env_sources|env\.files|\[env\]' internal/cli --glob '*_test.go'` then the claimed-name grep);
    check tests under `internal/cli/check`; gate tests stay as Task 2 left them (they pin the nil-`Private`
    path on purpose).
  - A test whose subject **is** a public key (one asserting today's "a claimed public value is scoped")
    is left for Task 7 to rewrite into a refusal test; list them in the report.
- **Gates:** `env -u YOLO_VERSION -u YOLO_ALLOW_MISSING_PROGRAMS go test ./internal/... -count=1`; every
  changed integration test by name with `YOLO_NO_AUTO_IMAGE_REAP=1 go test -count=1 -run '<names>' ./integration`;
  `python3 scripts/completion-census.py` and commit its output.
- **Fences:** `*_test.go` files and test fixtures only; a production file in the diff is a stop.
- **Commit:** `test: provider keys in fixtures live in the private env set`
- **Report:** the file list, any test left for Task 7, the integration results.
- **As built:** the TOML conversion selects a `file://` pack through a bare pack source, so its handle
  is `packs/<name>`; that handle exposed a production fault (a host session directory named with a
  slash), fixed in its own commit ahead of Task 6. The `internal/cli/run` unit fixtures needed no
  change: they already hold claimed keys private through `secretenvfixture_test.go`'s `hydrate`.

### Task 7 — the refusals

**After 5 and 6.** The breaking change.

- **Settled** *(do not re-open)*: row 2's refusal text, exactly, from [§3.5](secret-env-scoping.md#35-the-refusal):

  ```text
  config.env.files: ZAI_API_KEY is set in your public environment ([env], from
  ~/.config/yolo-jail/defaults.env), and the zai provider this launch selects claims it as its key. A
  public value reaches every process in the jail, so a provider's key may not live there. Move it to the
  private set: yolo config secret ZAI_API_KEY --user
  ```

  The both-sets refusal, exactly, from [§4.8](secret-env-scoping.md#48-the-same-name-in-both-sets):

  ```text
  config.env: ZAI_API_KEY is set in both your public environment ([env], from
  ~/.config/yolo-jail/defaults.env) and your private one ([env.secrets], from
  ~/.config/yolo-jail/secrets.env). A name lives in one set. Keep the private copy:
  yolo config secret ZAI_API_KEY --drop --user
  or keep the public copy: yolo config unsecret ZAI_API_KEY --drop --user
  ```

  The line breaks are the design's wrapping; the message is one paragraph, joined by spaces, except the
  both-sets text's two commands, each on its own line. Fatal "whatever the name, whether or not anything
  claims it, and whatever order the entries merge in" ([SE-D2](secret-env-scoping.md#SE-D2)). Fires at
  "a fresh launch, an attach, `yolo check`, `yolo host --` and `yolo host env`", nothing written before
  it ([SE-D7](secret-env-scoping.md#SE-D7)). On a legacy JSONC user config the remedy reads `run yolo
  config migrate, then yolo config secret ZAI_API_KEY --user`
  ([SE-D6](secret-env-scoping.md#SE-D6)). In a jail the user-scope remedy carries the host-run line. No
  hatch ([SE-D10](secret-env-scoping.md#SE-D10)).
- **Tests first:**
  1. `internal/config/envsecretsrefusal_test.go` (new): build a `HydratedEnv` with `ZAI_API_KEY` public at
     `EnvPlace{Key: "env.files", Entry: "~/.config/yolo-jail/defaults.env"}` and call
     `SecretEnvRefusals(h, []packload.PublicClaim{{Name: "ZAI_API_KEY", Providers: []string{"zai"}}}, scopeOf)`
     with `scopeOf` returning `EnvSite{Scope: "--user"}`; assert the joined text equals the row 2 block above
     with its newlines replaced by spaces. Second case: both sets, assert the both-sets block. Third:
     `Legacy: true` → contains `run yolo config migrate, then yolo config secret ZAI_API_KEY --user`.
  2. `internal/cli/run/secretenv_test.go`: `TestAPublicProviderKeyRefusesBeforeAnythingIsWritten` —
     `composePackChannel` with `ZAI_API_KEY` public and `zai` selected returns an error whose text contains
     `yolo config secret ZAI_API_KEY`; `launchChannel`'s caller prints `Refusing to launch:` and no
     `yolo-user-env.sh` exists under the workspace state directory.
  3. Host: `TestHostRefusesAPublicProviderKey` in `hostsecretenv_test.go` — `composeHostVars` returns a
     composition whose `err` text contains the row 2 block's last sentence; and `hostEnv([]string{…})`
     exits non-zero with nothing on stdout.
  4. Check: a FAIL carrying the same text from `secretEnvGap`.
  5. `integration/secretenv_test.go`:

     ```go
     func TestAPublicProviderKeyRefusesTheLaunch(t *testing.T) {
     	requireJail(t)
     	writeUserTOML(t, "packs = [\"zai\"]\n\n[env]\nset = { ZAI_API_KEY = \"zai-probe-not-a-real-key\" }\n")
     	dir := writeWorkspaceTOML(t, "")
     	r := runYolo(t, dir, `echo SHOULD-NOT-RUN`)
     	if r.rc == 0 || strings.Contains(r.stdout, "SHOULD-NOT-RUN") {
     		t.Fatalf("a provider key in the public set must refuse the launch:\n%s", r.combined())
     	}
     	if !strings.Contains(r.stderr, "Move it to the private set: yolo config secret ZAI_API_KEY --user") ||
     		strings.Contains(r.combined(), "zai-probe") {
     		t.Errorf("the refusal names the move and never the value:\n%s", r.combined())
     	}
     }

     func TestANameInBothSetsRefusesTheLaunch(t *testing.T) {
     	requireJail(t)
     	writeUserTOML(t, "packs = []\n\n[env]\nset = { FOO = \"a\" }\n\n[env.secrets]\nset = { FOO = \"b\" }\n")
     	dir := writeWorkspaceTOML(t, "")
     	r := runYolo(t, dir, `echo SHOULD-NOT-RUN`)
     	if r.rc == 0 || !strings.Contains(r.stderr, "yolo config secret FOO --drop --user") ||
     		!strings.Contains(r.stderr, "yolo config unsecret FOO --drop --user") {
     		t.Errorf("both sets is fatal, naming both --drop commands:\n%s", r.combined())
     	}
     }
     ```

  Red: `env -u YOLO_VERSION go test ./internal/config -run SecretEnvRefusal -count=1` → `undefined: SecretEnvRefusals`.
- **Change:**
  - `internal/config/envsecretsrefusal.go` (new): `SecretEnvRefusals(h *HydratedEnv, claims []packload.PublicClaim,
    site func(name string, private bool) EnvSite) []string`, both-sets lines first ([SEP-D8](#SEP-D8));
    the label is `config.env.files`, `config.env.set`, `config.env.sources` or `config.env_sources` by the
    public place's `Key`; `config.env` for both-sets.
  - Run: in `composePackChannel`, after `ScopeCredentials`, `refusals := config.SecretEnvRefusals(userEnv,
    scope.PublicClaims(), …)`; non-empty → return an error joining them. `launchChannel`'s existing
    `printProviderRefusal([]string{"Refusing to launch: " + err.Error()})` prints it (`run.go` ~`:499`);
    every other caller of `composePackChannel` (the grant recomposition at `run.go` ~`:1775`, the attach
    rekey near `run.go:3311`) already turns its error into a refusal through the same printer, so the
    refusal reaches each without new wiring. Confirm each with a test, not by reading.
  - Host: `composeHostVarsWith` sets `c.err` from the same call (site with `userOnly = true`).
  - Check: `secretEnvGap` adds FAILs from the same call.
  - Rewrite the tests Task 6 left behind into refusal tests.
  - `config_ref.txt` env entry: one sentence — a selected provider's key in `[env]` refuses the launch and
    names `yolo config secret`; a name in both sets refuses.
- **Gates:** red, green; `just check-ci`; `YOLO_NO_AUTO_IMAGE_REAP=1 go test -count=1 -timeout 0 ./integration`
  (a launch-path change: the full suite, once, per [AGENTS.md Workflow step 4](../../AGENTS.md#workflow));
  nested-jail check with the integration test's config, showing the refusal and an empty workspace state.
- **Parity:** podman, Apple Container and macos-user (both Seatbelt setups) refuse in `launchChannel`,
  above the backend dispatch; `yolo host --` and `yolo host env` refuse in `composeHostVarsWith`; `yolo
  check` predicts both. A Mac run is owed for macos-user and Apple Container: say so in the report, do not
  claim it.
- **Fences:** no vehicle file (`userenv.go`, `agentenvfiles.go`, `envfile.go`).
- **Commit:** `feat(env)!: a provider key in the public env set, or a name in both sets, refuses the launch`
- **Report:** red and green runs, the full integration result, the nested-jail transcript.
- **As built:**
  - The load lowers a legacy `env_sources` to the place key `env.sources`, so the label and place
    come from the place's `Key` *and* the site's `Legacy` bit: a legacy file reads
    `config.env_sources: … (env_sources)` (`asWritten` in `envsecretsrefusal.go`).
  - The run test drives `Run` on macos-user with a stub backend
    (`TestAPublicProviderKeyRefusesBeforeAnythingIsWritten`, `TestANameInBothSetsRefusesBeforeAnythingIsWritten`
    in `internal/cli/run/secretenv_test.go`), so it pins `launchChannel`'s caller, the printed
    `Refusing to launch:` and the absence of `yolo-user-env.sh` in one launch.
  - The attach is pinned end to end by `TestAnAttachRefusesAPublicProviderKey` (integration): the
    attach passes the same `launchChannel` call before the rekey at `run.go`'s attach branch.
  - `yolo check`'s second hydration reports on `r.configWarn`, which prints a message once
    (`warningchannel_test.go` refuses a discarded warning channel).
  - Task 6 left one test: `TestHostGrantWithheldLineNamesAUserProfileOverTheProvider` (a legacy
    user profile over a provider, spellable only in the legacy file, whose key is therefore
    public). It is rewritten as `TestHostRefusesALegacyPublicKeyNamingMigrateFirst`; the remedy's
    profile ordering stays pinned by `TestRemedyProfilePrefersTheProvidersOwnName`.
  - Task 6's note above was right for the `hydrate` fixtures and wrong for the `internal/cli/run`
    tests that write a user config file, or hand `composePackChannel` a config to hydrate itself:
    their keys sat in a legacy `env_sources`, which the refusal now stops. They are rewritten here,
    to a TOML private set (`writeUserConfigTOML`, `inEnvSecrets`, `awsAuthUserTOML`) or, for a
    config map, to the readable `[env]` table (`setFixtureEnv`). A config with no key keeps its
    legacy spelling, so `overrideNativeLaunch`, `macosUserGrantRun` and `sealFixture` write either
    file by the body's first character.

### Task 8 — docs describe the two sets

**After 7.** Docs only.

- **Change:** each file in [Ships with](#ships-with)'s docs list; the design's ledger `Built` column for
  SE-D1 to SE-D15 with the test names that pin each; the design's frontmatter `stage: BUILT` and `next`
  naming graduation into [`providers.md`](../reference/providers.md#the-credential-gate).
- **Gates:** `uvx vantage-check` on every edited doc; `just lint-ci`.
- **Commit:** `docs: the public and private env sets`
- **Report:** the files, the check results.
- **As built:** also [`providers-and-models.md`](../../userguide/guides/providers-and-models.md), outside
  the list above: its three steps put a provider key in `env_sources`, which now refuses, so they
  name `[env.secrets]` and the two verbs.

## Ships with

- **Unit tests, by case:** set bit and places; unset across both sets; both-sets with places; consulted
  list names private entries (Task 1). Row 4 nobody; MCP claim to its agents only; MCP claim never narrows
  public; public provider claim reported and not widened; unselected row 1 line; unselected row 4 pack
  remedy; fallback still scoped (Task 2). Workspace private file outside the workspace; shared file holds
  no private value (Task 3). Host rows 3 and 4 and the inherited wording; check WARN (Task 4). Verb dotenv
  move, inline move, no private file, `--drop`, `unsecret`, legacy refusal, in-jail host-run line (Task 5).
  Refusal texts, run, host, check (Task 7).
- **Integration:** `integration/secretenv_test.go` (new)'s three tests. The private-key
  test is the one that catches a recomposition dropping the set bit (an attach or a rekey); the refusal
  test catches a notch that skips the refusal.
- **Tests that break, and should** — rewrite to the new behavior, never relax: every fixture of Task 6;
  [`envsourceshydrate_test.go`](../../internal/config/envsourceshydrate_test.go) (Task 4); any test
  asserting [CN-D3](../reference/providers.md#CN-D3)'s "unclaimed reaches every process" for a value now
  in the private set.
- **Docs that describe the old thing:**
  - [`docs/reference/providers.md`](../reference/providers.md#the-credential-gate): the gate section and
    the CN-D3 ledger row (true now only of the public set); the disclosure's line kinds.
  - [`docs/reference/agent-credentials.md`](../reference/agent-credentials.md#env_sources--the-sanctioned-secret-channel):
    the channel now has two sets.
  - [`userguide/reference/configuration.md`](../../userguide/reference/configuration.md): the `[env]` entry
    gains `[env.secrets]` and the two verbs.
  - [`userguide/reference/settings-per-setup.md`](../../userguide/reference/settings-per-setup.md): the "Use
    API keys" and "Give a shell or script a provider's key" rows and the two setup bullets that tell a user
    to put keys in `env_sources`.
  - [`docs/reference/host-agent-environment.md`](../reference/host-agent-environment.md): the P2 paragraph
    naming `env_sources` as the env channel.
  - [`internal/cli/config_ref.txt`](../../internal/cli/config_ref.txt): Tasks 3, 5 and 7.
- **Surfaces neither code nor docs:** the schema rows (Task 3); `yolo config` usage and `configHelpToken`
  (Task 5); the setup census row if `go test ./internal/setupcensus` asks for one (Task 3); the completion
  census output (Task 6).
- **Observable surface:** the two new disclosure line kinds (every launch, `launch.log`), the two refusals,
  the verbs' `moved NAME from … to …` line. No counter or log beyond these: every launch already tees
  stderr to `launch.log`.
- **No `CHANGELOG.md` entry** ([AGENTS.md](../../AGENTS.md#the-changelog-is-written-at-release-time)); the
  `!` in Task 7's commit marks the break for the release writer.
- **Cheap and yours:** internal names; slice against map for the row 4 list; the verbs' help wording
  beyond the usage line.
- **Stop and ask** (report, do not decide): anything that would print a value; any path that writes a user
  file at launch (a launch never rewrites config, [§5](secret-env-scoping.md#5-migration-the-first-launch-after-the-change));
  a gate that would make a reach wider than today for any input.

## Migration

No automatic migration, by the design ([§5](secret-env-scoping.md#5-migration-the-first-launch-after-the-change)):
the first launch that selects a provider whose key sits in `[env]` refuses with `yolo config secret NAME`,
which moves it. `yolo config migrate` converts a legacy JSONC file as today (its `env_sources` lands in the
public set) and gains nothing. A user with no claimed key in `[env]` sees nothing change. The existing
`yolo-user-env.sh` is truncated and rewritten on every entry, so a moved key leaves it at the next entry;
a running jail keeps its processes' environment until it restarts.

## Don't

- Don't widen claims to every shipped pack to decide reach — the shipped set words lines only
  ([SE-D8](secret-env-scoping.md#SE-D8), the design's [alternative A](secret-env-scoping.md#6-alternatives-considered)).
- Don't add a `YOLO_ALLOW_*` variable or a `--force` to the refusals ([SE-D10](secret-env-scoping.md#SE-D10)).
- Don't route the verbs through `runConfigEditWithMode` — its hunk printer shows the value.
- Don't change a vehicle (`userenv.go`, `agentenvfiles.go`, [`envfile.go`](../../internal/macosuser/envfile.go),
  the host exec) — only what the gate hands them changes.
- Don't put a `secret = true` flag on list entries or a top-level `[secrets]` table (alternatives C and D,
  rejected).
- Don't touch [`bedrock-key-provider.md`](bedrock-key-provider.md) or plan anything for the `bedrock-key`
  provider: [OQ-BK3](bedrock-key-provider.md#OQ-BK3) is open.

## Mac checks

Owed at `<landing sha>`; the jail notch and `yolo host` were run on Linux only.

- **macos-user refusal parity.** A user config with `packs = ["zai"]` and `ZAI_API_KEY` under `[env] set`:
  `yolo -- true` exits non-zero naming `yolo config secret ZAI_API_KEY --user`, the value never printed,
  and no per-session env file is written. Witness: the stderr and `ls` of the session directory.
- **Apple Container refusal parity.** The same config on `apple-container-self`: the same refusal, and
  `<workspace>/.yolo/home/.config/yolo-agent-env/` gains no file.
- **macos-user private delivery.** The key under `[env.secrets] set` with `-p claude=zai`: claude's
  session file holds it, a bare `yolo -- env` does not.
- **`integration/macosuserbridge_test.go`** skipped on Linux (darwin-only); run it on a Mac.

## Design gaps

None open. The two found while planning are decided as [SEP-D17](#SEP-D17) and [SEP-D18](#SEP-D18).

## Backlog

Found while building, left for later:

- **`yolo config secret` on an inline `env` table.** A user file that spells `env = { set = {...} }`
  inline still gets invalid TOML when the verb appends a `[env.secrets.set]` table, and a dotted key
  fails with a raw struct dump. The fix is in `configedit`, and it is larger than this plan.
- **The remedy inside a jail has no scope.** In a jail, `LocateEnvName` cannot see the user config, so
  the refusal's remedy names neither `--user` nor a `yolo host-run` line.
- **Inline public `env.sources` values reach the assembled copy unredacted.** This predates the plan;
  the redaction covers the private set only.


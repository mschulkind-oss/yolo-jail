---
title: "Readable config: component implementation plan"
date: 2026-10-26
status: accepted
stage: DECIDED
next: "Graduate the design to a system doc and retire this plan; everything else is built (readable-config.md's ledger)"
depends-on:
  - readable-config.md
  - config-user.md
  - config-project.md
  - config-commands.md
  - config-migration.md
  - pack-manifests.md
tags: [config, packs, migration, editing, plan]
summary: "Tree-grounded implementation handoff for the readable-config redesign, now largely built: TOML configs and pack.toml manifests are read at runtime, the named verbs and migrate commands exist, and the shipped packs are converted. §1 records what is built and the remainder still owed; §2–§9 are the hand-off as written."
---

# Readable config: component implementation plan

**Written against:** `c729692223d8d8a884887875b44da5639a154448`, 2026-10-26; the
[verified implementation state](#verified-implementation-state) was re-checked 2026-10-30 against
`ff750a9f4`. Sections 2 to 9 are the hand-off as it was written for the build, kept until
graduation; where their "present", "pending" or "unbuilt" wording disagrees with that list, the
list is the current state.

**Precedence:** the [accepted design](readable-config.md) wins on behavior; the tree wins on
cosmetic facts; this plan's mechanism advice is the first thing to be wrong. Never twist code to
match this map. **Constraint** means must; **advice** means the default with its reason. All
settled behavior below is binding, not advice. No unrelated design's open question blocks this
build. No new approval of this engineering plan is needed.

**Common stop conditions:** an owned file is dirty with another writer's work; a named symbol
has disappeared; a test fails for a different reason than predicted; a required edit crosses the
fence; or a gate fails outside scope. Report the exact contract and evidence, do not repair an
unrelated subsystem or silently enlarge the fence. New files below are explicitly marked new.

## 1. Grounding, reuse and traps

| Read seam | Reuse / change / trap |
| :--- | :--- |
| [`load.go`](../../internal/config/load.go), [`userlayer.go`](../../internal/config/userlayer.go) | `loadWithIncludes`, `loadUserScope`, `MergeConfig`, `ReadWorkspaceForGate` are distinct reads. Change all user readers through their common boundary; do not fix only `LoadConfig`. Preserve explicit `--user-layer` above includes/fragments and below project scope. |
| [`sources.go`](../../internal/config/sources.go), [`wsroot.go`](../../internal/config/wsroot.go) | Preserve `srcNode` merge order, duplicate writers, workspace containment, include origin and `Sources.Locations`. A format adapter must not turn a path outside the workspace into an eligible broker request. |
| [`json5/locate.go`](../../internal/json5/locate.go) | `Index.Locate` already returns value spans, including array steps, and refuses ambiguous duplicate keys; sources uses it. Extend an adapter, not a second JSONC parser. |
| [`tomlx.go`](../../internal/tomlx/tomlx.go) | `DecodeOrdered` retains source metadata for authored ordering. The c729 base exposed F4's singleton nested-array-table regression (`[a z]`, expected `[z a]`); lead-integrated commit `91f3f81d8ea5b9a135aaf8f61d121d320db757b7` keeps raw array-table identity through conversion and splits singleton rows. The targeted HIGH review passed on that source; final-candidate equality and Root gates remain. TOML dates remain invalid config scalars. |
| [`configdoc/document.go`](../../internal/configdoc/document.go), [`configdoc/encode.go`](../../internal/configdoc/encode.go) | A1's positioned document/index and encoder exist; keep offsets byte-based and comments unattached. The 91f3 foundation source, including F4, passed targeted HIGH review; final-candidate equality and Root acceptance gates remain. |
| [`config/schema.go`](../../internal/config/schema.go), [`config/renames.go`](../../internal/config/renames.go), [`config/validateproviders.go`](../../internal/config/validateproviders.go) | B1 extracted schema/rename metadata and provider validators; preserve the extracted 72 validator bodies and all key scope/type/removal policy. These are library seams only: config readers still do not discover TOML. |
| [`packdecl/toml.go`](../../internal/packdecl/toml.go), [`packdecl/packdecl.go`](../../internal/packdecl/packdecl.go) | D1 grouped TOML decoding and F8's two descriptors exist; no discovery/launch activation follows. D2's `[[launch]]`, install/run defaults, and contribution parity are still unbuilt. |
| [`entrypoint/tomltrivia.go`](../../internal/entrypoint/tomltrivia.go) | Existing scanner attaches/drops comments on round-trip. It is **not** an editor or migration policy; importing entrypoint into config would also invert layering. Do not reuse its comment-ownership semantics. |
| [`workspacefile.go`](../../internal/config/workspacefile.go), [`transaction.go`](../../internal/brokerlock/transaction.go) | Reuse atomic 0600 temp/rename discipline and lock/re-read pattern, not broker-directory locking directly: user configs may be symlinks and locks must identify the physical target. Never lock an inode that the rename replaces. |
| [`config.go`](../../internal/config/config.go), [`validate.go`](../../internal/config/validate.go), [`inherit.go`](../../internal/config/inherit.go) | B1 extracted provider/profile validators and schema/rename metadata; B2 still needs the config-reader census, merge and provenance changes. Preserve every unchanged key and scope; the new shape is not permission to erase rarely used knobs. |
| [`packdecl.go`](../../internal/packdecl/packdecl.go), [`contributes.go`](../../internal/packdecl/contributes.go), [`kinds.go`](../../internal/packdecl/kinds.go) | D1's grouped TOML codec now maps into normalized `Manifest` accessors; retain all live kinds. It remains a codec, not a runtime registry. Strict authoring, host use/skew and tolerant jail reads stay separate; D2 defaults and activation remain owed. |
| [`packload.go`](../../internal/packload/packload.go), [`mergedest.go`](../../internal/packload/mergedest.go), [`packoverlay.go`](../../internal/packoverlay/packoverlay.go) | Manifest discovery tries `pack.jsonc` then `pack.json`. `launchFlagClaims` currently replaces posture flags; overlays currently replace arrays via merge patch. New format must append by default without changing legacy reader semantics. |
| [`packs.go`](../../internal/config/packs.go), [`packresolve.go`](../../internal/config/packresolve.go), [`addr.go`](../../internal/packsrc/addr.go) | Identity currently comes from entry/address, not manifest. `Slug` already escapes handles. `?ref=` is currently mandatory; default-branch resolution must not make source/build/lock identities collide. Keep fork/source address grammar distinct from authored pack handles. |
| [`providers.go`](../../internal/packload/providers.go), [`profiles.go`](../../internal/packload/profiles.go), [`modellists.go`](../../internal/packload/modellists.go) | `ComposeProviders` is the one host composition; deep-copy ownership, endpoint adapters, all-add-before-only model narrowing and tombstones are load-bearing. Do not compose independently in the jail. |
| [`modelcatalog.go`](../../internal/modelcatalog/modelcatalog.go), [`modelvendor.go`](../../internal/packdecl/modelvendor.go) | Catalog reader currently collects IDs, **not facts**. Maker validation does not infer maker. C7 and maker/default folding are new work, not existing helpers. |
| [`brokered.go`](../../internal/config/brokered.go), [`gateread.go`](../../internal/config/gateread.go), [`scopeapproval.go`](../../internal/config/scopeapproval.go) | Keep user ceiling, workspace request, concrete scope and approved/live grant separate. Gate projects broker config out of JSON and uses scope + scope-sources records. A pretty rename must not skip that gate. |
| [`https.go`](../../internal/gitbroker/https.go), [`broker.go`](../../internal/gitbroker/broker.go#L270-L327), [`https_test.go`](../../internal/gitbroker/https_test.go) | `Broker.Run` already admits HTTPS upload-pack anonymously before scope/read-grant checks; `credentialPolicy` rechecks live scope/grants before private credential fallback. `TestHTTPSPublicFetchWithoutReadGrantOrApprovedURL` and `TestHTTPSPrivateAnonymous401RefusesWithoutCredential` pin those caller-visible paths. G3 must preserve/consume them while adapting new vocabulary; do not reorder admission or add a second public transport. |
| [`snapshot.go`](../../internal/config/snapshot.go), [`drift.go`](../../internal/config/drift.go), [`launchorigins.go`](../../internal/config/launchorigins.go) | Canonical JSON is approval-sensitive. Migration must respell config, scope-source file labels and origins, not silently bless new authority. macos-user boot digest identifies the session's baseline; stale-session evidence must not be relabeled current. |
| [`liveapplyplan.go`](../../internal/cli/run/liveapplyplan.go), [`inheritscope.go`](../../internal/cli/run/inheritscope.go) | Keep per-key apply timing, boot-baseline folding, own-workspace assembled snapshot shortcut and preflight/nested inheritance separate. Generated user copy becomes TOML; machine-only records remain JSON in new vocabulary. |
| [`config.go`](../../internal/cli/config.go), [`dispatch.go`](../../internal/cli/dispatch.go), [`pack.go`](../../internal/cli/pack.go), [`init.go`](../../internal/cli/init.go) | Six composition verbs move to surface; apply/drift/dump stay config. CLI registry also controls argv rewriting. `init` currently creates a long template and edits .gitignore; neither remains the default. |

### Verified implementation state

Verified 2026-10-30 against `ff750a9f4`. The front door's
[ledger](readable-config.md#decision-ledger) carries each ruling's landing commit and pinning test;
this list is the state by component.

- **Built.** The document/index/encoder foundation and the splice library (`internal/configdoc`,
  `internal/configedit`); format-aware loading, in which the runtime facades read
  `yolo-jail.toml` and `config.toml` before the legacy names and refuse both in one place; the
  grouped `pack.toml` codec, read beside `pack.json`; qualified pack handles and default-branch
  locks; `active_provider`, `native`, providers and variants in one selection namespace at launch;
  the `allow`/`requests` ceiling and request model through the approval gate to the brokers,
  writable mount requests included; the `yolo check` pack nudge; `yolo pack add/remove`, a
  pointer-file `yolo init`, `yolo allow`, `yolo request`, `yolo active-provider`,
  `yolo config set/add/unset/why` and the `yolo surface` namespace; `yolo config migrate` and
  `yolo pack migrate` (`internal/configmigrate`, `internal/packmigrate`) with the launch's
  conversion offer and `convert_later`; every shipped pack as a `pack.toml`; the TOML reference
  in `yolo config-ref`, the user guide and the pack READMEs; and the TOML integration tests.
- **Legacy window.** Legacy JSONC configs and `pack.json` manifests are still read, as
  [RC-D19](readable-config.md#RC-D19) rules; the old reader's removal is a later release's work.
- **Not built:**
  - this repository's own `yolo-jail.jsonc` is not converted, which waits on the owner installing
    a TOML-reading `yolo` on the host first ([RC-D22](readable-config.md#RC-D22)); the shipped
    loophole manifests are `loophole.toml` ([RC-D28](readable-config.md#RC-D28)).

## 2. First-landed contracts

**A1/B1/D1 foundation is present; F4's repair is implemented in lead-integrated commit
`91f3f81d8ea5b9a135aaf8f61d121d320db757b7`.** The targeted HIGH review passed on that source;
the final documentation candidate must prove source equality, and Root's final gates/landing
remain pending. The signatures below describe implemented code, not final acceptance evidence;
field factoring remains cheap, but byte ranges, original key steps and ordering guarantees are not.

```go
// internal/configdoc; imports jsonx/json5/tomlx, never config/cli/entrypoint.
type Format uint8 // JSONC=1, TOML=2
type Step struct { Key string; Index int; IsIndex bool }
type Span struct { Start, End int } // half-open byte offsets into original bytes
type Document struct {
    Format Format
    Bytes []byte
    Values *jsonx.OrderedMap
    Index *Index
}
func Parse(data []byte, format Format) (*Document, error)
func Encode(values *jsonx.OrderedMap, format Format) ([]byte, error)
func EncodeValue(value any, format Format) ([]byte, error)
func (i *Index) Value(path ...Step) (Span, bool, error)
func (i *Index) Position(offset int) (line, col int)
func (i *Index) Comments() []Span
// Member/table/item boundaries are index queries, not comment attachments.
```

**Advice:** isolate `github.com/pelletier/go-toml/v2/unstable` v2.4.3 behind this index. Its
`KeepComments`, `Node.Raw`, `Range`, `Shape`, table/array-table and key-value APIs were read in
the standard module cache. The complete upstream tree is about 2.0 MiB; vendor only the imported
packages. BurntSushi remains the semantic validator initially; compare both parser acceptances
on the fixture corpus. Copy all node ranges before `NextExpression`: its arena is reused.
This avoids owning another complete TOML grammar or reusing trivia's lossy comment attachment.
Pin the dependency and run `go mod vendor`; no network is used by hermetic builds.

The foundation includes filename constants in paths (user/project/local, legacy JSONC and JSON fallback,
manifest names), workspace constant aliases, and `internal/testconfig` helpers which accept
explicit format and document text and return symlink-resolved fixture roots. Default helpers
write the new format; legacy tests explicitly request format 1. Helpers do not select packs,
start agents, touch real HOME, or depend on config (avoids test import cycles).

**B1 contract (implemented in this candidate):** move-only provider/model/profile validation extraction from common
`validate.go`/`config.go` to provider-owned files, then retain all existing tests green.
The extraction preserves 72 validator bodies. `SchemaKeys`, per-path scope/type/removal policy,
data-backed rename descriptors and F8's two descriptors are present. This is not B2: it does
not activate TOML config discovery or runtime behavior. Rename descriptors enumerate
transformations and replacements; they do not contain a second converter. New-format effective
maps and records use the new vocabulary; legacy files remain format 1 and are lowered only on
their read path. No old spelling becomes an alias in TOML. Runtime projections may preserve
existing typed/wire APIs while consumer migration completes, but never expose an old authored
key as accepted TOML.

**D1 contract (implemented in this candidate):** grouped TOML decodes into the existing
`packdecl.Manifest` accessors. Carry format origin and explicit replace metadata into normalized
contributions so legacy replacement and new append semantics can coexist during the reader
window. D1 declares the new provider/model fields; D2's `[[launch]]` and install/run defaults
remain unbuilt. No F writer changes D-owned structs.

**After A1/B1/D1 are accepted on the F4-reviewed integrated candidate**, each dependent job must
compile against its integrated base. No worker references a not-yet-landed function, uses stubs,
or hands back uncompilable cross-lane fragments. Library work lands before its callsite
activation; activation is a named serial job below, not an unowned cleanup pass.

## 3. Exclusive component/file ownership

A **component** below is a file fence, not a single unlimited assignment. Each numbered job is
one bounded writer/worktree and coherent commit; one component's jobs run serially. Parent owns
review and integration. No component writer owns the whole redesign.

Tests beside an owned production file belong to its owner, including existing tests that
assert old behavior; rewrite those to the new behavior, do not relax them until green. New test
files use the component's named seam. Explicit exclusions override directory globs.

| Owner | Exclusive files / map of changes | Dependencies |
| :--- | :--- | :--- |
| A — document contracts | New `internal/configdoc/**`, new `internal/testconfig/**`; `internal/tomlx/**`; `internal/json5/locate.go` and locate tests only if adapter needs boundaries; `go.mod`, `go.sum`, `vendor/**`; filename hunks of `internal/paths/paths.go` and `internal/config/config.go` in A1 only, handed off to B/E afterward | First |
| B — config schema/read/provenance | `internal/config/config.go`, `load.go`, `sources.go`, `userlayer.go`, `validate.go` **after B1 extraction**, `envsources.go`, `mounts.go`, `portforwards.go`; new `schema.go`, `renames.go`, `removals.go`, `readableconfig.go`; associated config tests excluding E/F/G/H ownership | A1; B1 precedes F |
| D — manifest schema/codec | All `internal/packdecl/**`; new TOML codec/group/default files; normalized fields and schema comments; **no packs/** and no packload changes | A1 |
| E — source identity/resolution | `internal/config/packs.go`, `packresolve.go`, `selectedpacks.go`, `packselection.go`, `packpolicy.go`; all `internal/packsrc/**`; pack-related path helpers in `internal/paths/paths.go` after A1; their tests | A1, B1; D1 for resolved manifests |
| F — providers/models/selection | `internal/config/profiles.go`, `profileselection.go`, `viaselection.go`, new extracted `validateproviders.go`, `validateprofiles.go`, `providerschema.go`; provider/model/profile tests; `internal/packload/providers.go`, `profiles.go`, `modellists.go`, `modelreferences.go`, provider/model tests; `internal/modelcatalog/**`; `packs/*/derive.lua`, pi model extension files that consume facts; **exclude manifest files** | B1, D1 |
| G — allowance/request/broker authority | `internal/config/brokered.go`, `gateread.go`, `scopeapproval.go`, `validate_loopholes.go`, `validate_loopholesettings.go`, `workspacefile.go`, new allow/request files; associated tests; `internal/loopholes/**`, `internal/loopholedecl/**`; `internal/gitbroker/**`, `internal/ghbroker/**`; `internal/cli/check/broker*.go` and tests | B1, E1, D1; H activation after G |
| I — pack conventions/composition/delivery | `internal/packload/packload.go`, `mergedest.go`, `footprint.go`, `governance.go`, `laterwins.go`, new convention files and tests, install-hint/launch-flag tests; `internal/packoverlay/**`, affected `internal/agentcfg` overlay/list implementation and tests; `internal/cli/run/packfiles*.go`; `internal/entrypoint/hostfilestree.go`, `hostfilesretire.go` and tests; affected host file-delivery CLI files **not apply.go/applyhostskills.go** | D1, E1; F1 for model conventions |
| H — runtime records/inheritance/apply | `internal/config/inherit.go`, `launchorigins.go`, `assembled.go`, `snapshot.go`, `drift.go` and tests; `internal/cli/run/**` **except I packfiles fence**; `internal/entrypoint` config/env/provider-table ingestion files, not surfaces/hostfiles; backend snapshots and new-vocabulary consumer wiring | B2, E2, F2, G2, I2; mechanics boundary below |
| J — splice engine/targeting | New `internal/configedit/**` (library only); no CLI registry, no source schema, no shared atomic helper modifications | A1, B1; semantic checks after B2/F2/G2 |
| L — migration libraries | New `internal/configmigrate/**`, new `internal/packmigrate/**`; no edits to shared schema, packload, snapshot or CLI files; consumes exported contracts and rename descriptors | B2, D2, E2, F2, G2, I2, H1, J1 |
| K — editing/discovery CLI | `internal/cli/config*.go` except read-only reference coverage files assigned N; `dispatch.go`, `help.go` if changed, `pack.go`, `init.go`, new allow/request/active-provider/surface/migration adapters and tests; `internal/cli/check/**` **except G broker fence and F model-catalog/model-list tests**; remediation strings in CLI | J2, L2, H2; staged K1/K2/K3 below; excludes owner templates/briefings and stream-owned source seams |
| M — bundled config/pack assets/fixtures | `packs/*/pack.json` → `pack.toml`, extracted overlays/home assets, nested human-authored loophole manifests; `packs/embed.go`, `packs/executables.txt`, release decode tests; root `yolo-jail.jsonc` → TOML and tracked examples; **exclude F derive/extension files**; `flake.nix`/staging-script fixture references only if required, no image identity change by assumption | D2, I2, L2, F2; same final reader integration |
| N — reference/docs/advice | `internal/cli/config_ref.txt`, configref coverage tests; code advice sites outside prior fences; `AGENTS.md`, README, packs READMEs, userguide, overlapping docs/reference; scripts completion census/output | All behavior integrated; mechanics rendering ownership below; excludes owner briefing/personalpack/userhome |
| O — acceptance/integration fixtures | `integration/**` and remaining test-only filename/callsite fixtures outside owner fences; source-named new acceptance tests; no production code | Combined stable candidate |

**Common-file transfers are explicit:** A1 filename hunks precede B/E; B1 provider extraction
precedes F; D owns all new manifest fields before I/F; K alone touches the CLI registry and
pack dispatch; H alone changes runtime activation after other libraries land. If another owner
needs a common-file edit it supplies the exact contract to the owner, not a competing patch.
No parallel blanket search-and-replace of `.jsonc` or profile/broker strings.

**Mechanics lane boundary:** its owner controls the applied-config argument additions at the two
`applyHostSkills` calls in `internal/cli/apply.go`, `internal/cli/applyhostskills.go`/hostskills
composition and the applied-facts/runtime API. H2/H3 and K must wait until Root has landed the
runtime API they consume; an unlanded candidate or plan is not a callable contract. Do not remove
or duplicate those edits. H adapters consume the landed stream before touching
`internal/cli/run/prepare.go`, `liveapply.go`, `liveapplyplan.go`, `persistencemap.go` or attach
refresh. TOML schema/reference stays exclusive here; H3 adapts vocabulary to those shared facts,
not a second mechanics renderer. Mechanics also owns the narrow canonical user-scope predicate
and extraction in config; B consumes it instead of maintaining a key census, and B's TOML scope
mapping remains separate. The stream owner retains the narrow `commands.go`/`config.go`
presentation, stdout, hidden-prompt and color seams before K; K must wait for that Root-landed
runtime/source contract and must not edit those seams concurrently. GPU rebuild facts use actual
image inputs, not a second heuristic. N routes mechanics-owned generated text through its owner.

## 4. Exhaustive requirement → component → test matrix

The design links in the first column are the authority. Test names are required new or revised
cases at the named owner's seam; all must exercise production readers/dispatch/callers, not
just a codec detached from the caller. Unit tests use temporary fixture HOME/workspaces and
local git/HTTP servers only, no agent interaction or real API calls.

| Authority / requirement | Components and production effect | Required evidence |
| :--- | :--- | :--- |
| [Format](config-commands.md#5-the-format-underneath): every human-authored config/manifest TOML; machine-only JSON | A/B/D/M/H; inventory nested loophole manifests too | `TestHumanAuthoredFilesUseTOML`, generated snapshots still JSON; all shipped assets load |
| [Version](config-migration.md#2-how-a-yolo-knows-which-config-it-has): filenames, absent format=2, future version refusal | A/B/K | absent/2 accepted, 3 refused before runtime starts with upgrade command; invalid types/unsupported numbers refused; migration never emits format |
| [Transition](config-migration.md#3-the-order-of-releases): next release reads old and new; later removal separate | B/D/K/M | same legacy behavior after decline; TOML and old siblings refused; both user/project/local and pack manifest cases; no automatic old-reader deletion |
| [Offer](config-migration.md#1-what-youd-see): y converts then launches, n continues, no TTY continues, manual convert_later silences | K/H/L | producer/run tests for all four; old user key only; config command never writes it; TOML rejects it; no illustrative release numbers hardcoded |
| [Layering](config-user.md#8-lines-you-never-write): ordered config.d TOML, symlinks, explicit overrides | B/E | base → includes → sorted fragments → inherited/explicit layer → project/local precedence; missing fragment links; duplicates/cycles deterministic; convention shown by show/why |
| [Includes](config-migration.md#appendix-evidence): relative per including file, migrate twins | B/L | nested/optional includes, missing-machine report, cycles, relative env file anchors, no ~ or absolute includes silently accepted |
| [Sources](config-migration.md#5-every-case): file:line:col/key; provenance and origins | A/B/H/K | dotted/quoted keys, slash handles, arrays/inline tables, duplicate keys, invalid TOML; include error location; inherited host location |
| [User shape](config-user.md#2-the-file-proposed): defaults omitted, keys are values, shared facts once | B/F/G/L/M | all six levers parsed and same effective config; negative old spellings give rename recipe, not aliases |
| [Install](config-project.md#1-the-file): install.nix/mise, version string | B/H/M | rich nix package object fields preserved, `mise.python="3.12"` preserved; numeric version refused; image rebuild/apply timing test |
| [Mounts](config-project.md#1-the-file): path or table, host/at/write, default read-only | B/H | both list and array-table form; one spelling per file; write only user scope; optional safety and credential-scope fences unchanged; path with colons not direction magic |
| [Machine](config-project.md#1-the-file): gpu/resources/ports/forward | B/H | host=8000/jail=8000 and reverse forwarding, range/address legacy cases preserved or migration refuses exactly; gpu unavailable warns/starts; cpus/memory/pids/io preserved |
| [Env](config-project.md#3-the-local-file-and-turning-something-off): files/set/unset | B/H/J | literal value "off" stays string; ordered file hydration and unset precedence; inherited env resolved without leaking secret file reads; relative paths |
| [Scope](config-project.md#1-the-file): no inference routing/credential authority from agent-editable project | B/F/G | project/local/inside/outside includes refuse user-only key with host file and command; existing allowed model-fact overrides remain allowed |
| [No file](config-project.md#2-no-file-at-all): launch does not scaffold, init only on request | B/K/H | ordinary absent config launch leaves it absent; init creates pointer only; .yolo excluded via git info/exclude; user's .gitignore byte-unchanged; existing config untouched |
| [Local](config-project.md#3-the-local-file-and-turning-something-off): local after project | B/J/K | local machine override wins; no user config rewrite from unset local |
| [Removal](config-project.md#3-the-local-file-and-turning-something-off): table="off", [] clears, env unset, native | B/F/J | MCP/provider/variant deletion, all other strings where table expected refused; empty overrides list clear; nonempty union/order; literal strings unaffected; type-aware unset |
| [Sources/handles](config-user.md#4-naming-a-pack-that-is-not-shipped): only shipped bare; yolo explicit, local/source/owner-repo | E/K/H | source tree local/git and direct git pack; qualified settings/footprint/ls/lint; no bare path; two authors tools do not collide |
| [Local convention](config-user.md#8-lines-you-never-write): config packs directory and symlink dedup | E/I | local folder/link/folder-link works, directory identity duplicate names both locations; no implicit selection of every folder; preserve existing conventional local-content pack deliberately |
| [Default branch](config-user.md#8-lines-you-never-write): no ref locks remote HEAD once | E | local bare git remote with non-main HEAD; unchanged lock prevents move until update; explicit slash ref; failed symref asks for ref; source/build identity collision regression |
| [Pack switch](config-user.md#7-one-switch-and-the-nudge): packs only, no settings auto-select | E/G/K | selected login/non-login packs activate as allowed; settings unselected offers add; decline/no TTY fatal; typo suggests; empty headings inert |
| [Login reverse nudge](config-user.md#7-one-switch-and-the-nudge): missing profile warning serves nothing | G/K/H | installed aws-auth missing profile starts no credential-serving capability, warning prints setting command; core uses declaration rather than registry of tools |
| [Allow](config-user.md#6-what-a-jail-may-do-with-your-logins): always/on_request same lists, user only | G/K | fetch/read versus push/new_branches/create_pr/create_issue mapping; ceiling not concrete grant; unknown capability error through pack declarations |
| [Request](config-user.md#6-what-a-jail-may-do-with-your-logins): requests outside ceiling skipped, never fatal | G/K/H | two users same project; each granted/skipped request line; allow remediation command; malformed types still errors; ceiling cannot self-authorize workspace |
| [Scope](config-user.md#6-what-a-jail-may-do-with-your-logins): own repositories plus approved extra requests only | G/H | credentialed fetch outside scope refused with request/check/apply command; remote/request source drift re-prompts; no wildcard display or new widening channel |
| **User addition 2026-10-26:** no vacuous git/github scope approvals | G/H/O | independent broker cases: non-Git/no metadata, Git/no remotes, valid remote, explicit allowed requested repo outside Git cwd, request removal/drift; empty concrete scope produces no repo grant, prompt or phantom wildcard record; launch callsite tests fail if concrete-scope resolution is skipped |
| [Public fetch](config-user.md#6-what-a-jail-may-do-with-your-logins): anonymous first, public outside scope passes | G | local HTTP smart-git public succeeds outside scope with helper/token spies never called; 401/403 private fails before credential fill unless scope+grant; redirects never leak credential; anonymous route is read only |
| [Approval](config-migration.md#4-what-yolo-config-migrate-does): same gate and records in new vocabulary | G/H/L | before/after migration no spurious prompt; changed real authority still prompts; N/noTTY writes none; host-only approval path remains outside workspace |
| [Selection](config-user.md#5-which-setup-each-agent-runs-on): provider model, variants and aliases | D/F/K | provider direct -p; provider/variant; alias codex; multiple switchable selections; duplicate provider/variant/alias name errors; missing name prints declared set |
| [Native](config-user.md#5-which-setup-each-agent-runs-on): active_provider.default and native | F/H/K | default reaches declared receivers only; explicit native clears environment/managed provider selection; provider named native fatal; declaration never chooses default |
| [Profiles](config-user.md#5-which-setup-each-agent-runs-on): same-name shipped profiles gone, options retained via variants | D/F/M | options/via/subagents/model menu/protocol/capability behavior unchanged; profile-gated overlays still follow normalized selected provider/variant |
| [Models](config-user.md#2-the-file-proposed): key=id, maker inference, every_model | D/F/L | row overrides shared facts; safe maker inference Bedrock prefixes/routes, non-inferable explicit vendor retained; id rename safe; ordered models preserved |
| [Catalog C7](config-user.md#8-lines-you-never-write): omitted facts inherit catalog, explicit wins | F | offline fixture catalog facts + missing catalog; no invented zero facts; no network/agent invocation; composition and all consuming derives exercise actual fact output |
| [New model facts](pack-manifests.md#4-flags-that-add-and-a-model-said-once): api/thinking/images/price/context/accepts | D/F | validate complete fields; each selected consuming agent render maps same facts or reports unsupported; image dimensions/bytes/quality bounds; thinking mapping preserved |
| [Twin](pack-manifests.md#4-flags-that-add-and-a-model-said-once): twin_of wire identity, compact_at | D/F/L | inherits all facts, same wire id, twin overrides, cycles/missing target refused; full and compact row both selectable and distinguishable |
| [Folders](pack-manifests.md#3-no-manifest-at-all): home/skills/briefing/overlays/patches | D/I/H | manifest-less folder works at host/check/jail; README first line description; folders select nothing; overlays owner absent inert, malformed fatal |
| [home](pack-manifests.md#3-no-manifest-at-all): file-by-file merging | I | two packs distinct siblings work; same-file clash names both; exec bits preserved all routes; no directory mount shadowing owner surface; host user-owned bytes untouched |
| [Overlay files](pack-manifests.md#1-a-small-pack-luna-worker): owner format, @profile gate, codec from extension | D/I/F | pi-settings.jsonc and claude-settings@bedrock.jsonc; hyphenated identity resolution against declared surfaces; ambiguous/unknown names actionable; TOML/YAML/JSON codecs never guessed from content |
| [Grouped manifest](pack-manifests.md#4-flags-that-add-and-a-model-said-once): kind identity once | D | roundtrip every live contribution kind + metadata; strict unknown/retired names; multi-kind order retained deterministically; capability/disclosure/host execution unchanged |
| [Contributions](pack-manifests.md#6-principles): lists add, objects merge, explicit replace | D/I | launch flags append, overlay nested objects merge, arrays append/dedup and replace=true replaces; legacy codec retains former behavior; higher computed/managed wins and capture remains per entry |
| [Launch](pack-manifests.md#4-flags-that-add-and-a-model-said-once): [[launch]] works without copying autonomy flags | D/I/F | agy default permission flags + model flag; replacement drops only explicit replaced set; selected autonomy/notch channel remains gating; no unconditional host bypass |
| [Defaults](pack-manifests.md#6-principles): install string/table/default/bin, scope workspace, run bin | D/I/M | per-manager exceptions, omitted hints, install="ripgrep", install.default; supplied overrides win; source/pack recipe paths still safe; shipped source-cited manager hints retained |
| [Files list](pack-manifests.md#a2-why-ten-files-take-fifty-lines): files.only and pi package route | D/I/L | only selected files delivered once, directories/exec bits; register correct qualified landing at both notches; ext/theme moves remain pi package, not loose copies |
| [Pack lint seed](#1-grounding-reuse-and-traps): derive shadow, loose pi copies, gzip repetition | K/I/F | real computed key overlap reports winning layer; loose extension copy advice; per-contribution ratio ≥0.10 more repetitive than equal-length prose baseline reported; do not treat gzip improvement as success criterion |
| [Splice](config-commands.md#3-what-a-command-does-to-the-file): untouched bytes/comments | A/J | set/add/remove keys/items; multiline/inline arrays, first/middle/last, trailing comment/comma; unchanged prefixes/suffixes byte-equal; no implicit comment movement or deletion |
| [Insertion](config-commands.md#3-what-a-command-does-to-the-file): sibling style, table end, blank line | J | empty JSONC 2 spaces/TOML none; nested/dotted keys, quotes/slashes, CRLF, no final newline, multiline strings containing #; new table after one blank line |
| [Atomicity](config-commands.md#3-what-a-command-does-to-the-file): lock reread, verify restore | J/K | simultaneous command processes serialize; symlink physical target lock; parse/effective mismatch restores exact bytes and prints both; permissions/read/write/rename failure; dry-run/noop no write |
| [Target](config-commands.md#3-what-a-command-does-to-the-file): existing section origin, scope defaults/flags | B/J/K | user section in config.d file chosen; both-scope default project; --user/--project/--local/--file; invalid scope refusal; jail user edit host-run line; only named output file changed |
| [Named commands](config-commands.md#4-the-commands): meaningful verbs over generic engine | E/F/G/J/K | pack add/remove resolves+dedups; allow writes+offers approval; request writes+check/apply; active-provider validates; generic set/add/unset any valid key; check after writes; agent project editing still legal |
| [Explain](config-commands.md#1-what-youd-see): why and show conventions | B/K | why active_provider.claude gives origin/default; show effective defaults and convention annotations; unselected declaration not selection |
| [Namespace](config-commands.md#4-the-commands): config own, six surface verbs, one-release retirement | K/N/O | every new surface verb uses former resolver/notch/color/provenance; old spellings refuse with exact replacement, no silent alias; apply/drift/dump remain; CLI registry dispatch smoke |
| [Advice](config-commands.md#2-the-sixty-places-this-replaces): named/generic/host-run remediation | G/K/N | BrokerGrantAdvice caller tests, gh scope refusal, unowned config-ref/userguide advice and code error sites; command target/value shell quoting; every stop next step; any owner-owned generated briefing/skill hunk is a separate Root handoff |
| [Migration equality](config-migration.md#4-what-yolo-config-migrate-does): whole graph canonical equivalence | L/B/F/G | before normalized new-vocabulary JSON == planned after; defaults/facts folded only with equality; unrepresentable repo-specific grant exact diff and zero writes |
| [Migration files](config-migration.md#4-what-yolo-config-migrate-does): in place/symlink/backup/missing | L/K | four-file graph, local+includes; symlink target and link remain coherent; .pre-migrate original bytes; missing optional file reported; no merging files; declined/dry run no writes |
| [Migration comments](config-migration.md#4-what-yolo-config-migrate-does): keep all in document order | A/L | standalone/inline/block/template comments retained in order, delimiter conversion only; comment after dropped-default key preserved; never inferred ownership |
| [Migration records](config-migration.md#4-what-yolo-config-migrate-does): approval + boot respelling | L/H/G | config/scope/scope-sources, boot and origin copies; unreadable record left with one-time prompt notice; no stale baseline used to grant authority; request actual repo remains scoped |
| [Pack migration](pack-manifests.md#5-the-migration): manifest/comments/files/pi/git mv | L/D/I/K/M | full old manifest to grouped equivalence + file move plan, git-tracked rename via git mv, no checkout no git dependency, collision refuses before mutation; old local/fetched remediation |
| [Shipped assets](config-migration.md#5-every-case): migrate with reader, constants/helper census | M/O/N | all shipped packs parse/load/render and executable census; release decoder compatibility assertions deliberately revised; legacy fixture cases stay explicit |
| [Inherited](config-migration.md#5-every-case): TOML user copy, JSON records new vocabulary | H/B/G/F | host→jail effective equality; host origins names; filter census every live key; single-file ro bind with writable adjacent dir; nested inheritance recursion, no service authority widening |
| [Gates](#8-gates-and-ships-with): backend parity and no regressions | H/O/N | nested fresh/attach/apply plus full combined integration; podman/container/macos-user unit branches; rootless/reachability claims only real host/CI; documentation checks |

### Ruling coverage, including reversals

All 44 ledger entries are accounted for. Historical proposals superseded in the ledger are
**not** additional behaviors to implement. Current section prose and later ruling win.

| ID | Implementation coverage / supersession |
| :--- | :--- |
| OQ-RC1 | B/E/F/G/L: all six user-shape levers |
| OQ-RC2 | A/B/D/H/M: TOML, machine-only JSON |
| OQ-RC3 | F/D/M: direct providers + variants, same-name profiles deleted |
| OQ-RC4 | G: allow; later request spelling supersedes original needs wording |
| OQ-RC5 | D/L/M/K: config + manifest same release break |
| OQ-RC6 | B/K/L/N: upgrade sharing machines first, backup restores old file |
| OQ-RC7 | B/J/F: table off only |
| OQ-RC8 | D/E/K: fetched-old refusal after reader window, author/fork remediation |
| OQ-RC9 | A/J/K: explicit splice only, no side-effect writes |
| OQ-RC10 | K: named verbs over generic set/add/unset |
| OQ-RC11 | B/L: absent format=2; future-version refusal; migration omits key |
| OQ-RC12 | K/N: six composition verbs to surface, one-release refusal |
| OQ-RC13 | B/E/F/I/K: C1/C2/C5/C6/C7/C9; no unaccepted C3/C4 inferred selection |
| OQ-RC14 | F/B: active_provider and providers/variants |
| OQ-RC15 | E/K: qualified source identities, no bare local paths |
| OQ-RC16 | G/H: only actual remotes + approved requests; no grant from ceiling alone |
| OQ-RC17 | B/H/M: project install/machine/env/requests and one top-level mounts list |
| OQ-RC18 | L: equality, comments, folds, in-place graph/records, explicit request |
| OQ-RC19 | D/I/F/L: six manifest levers, bin/default install spelling |
| OQ-RC20 | F/K: native reserved; declaring native fatal |
| OQ-RC21 | G: anonymous public-outside-scope fetch, private refusal |
| RC-D1 | Selection key's job retained; spelling superseded by [OQ-RC14](readable-config.md#decision-ledger) |
| RC-D2 | as_you rejected; allow in G |
| RC-D3 | B/D: collection plurals, singular selection |
| RC-D4 | F: default replaces * selector |
| RC-D5 | false proposal superseded by [OQ-RC20](readable-config.md#decision-ledger)/native |
| RC-D6 | B/G: no string phrases; narrowed by native and table-only off |
| RC-D7 | G/K/E: settings nudge never selects |
| RC-D8 | G/B: two scopes use allow and requests (later spelling) |
| RC-D9 | G/K: declined settings nudge fatal, typo error |
| RC-D10 | B/F/G: empty heading inert, lone provider never chooses itself |
| RC-D11 | E/F/G: declaration never selects |
| RC-D12 | G: outside-ceiling request skipped, not fatal |
| RC-D13 | G: always/on_request same list shape |
| RC-D14 | own-login-is-provider proposal explicitly reversed by RC-D15 |
| RC-D15 | F: native bypasses yolo provider configuration |
| RC-D16 | A/J/L: comments unattached, no moving/removing by splice |
| RC-D17 | K: named command performs resolution/approval/validation beyond edit |
| RC-D18 | B/L/K: data-backed rename recipes, no aliases |
| RC-D19 | B/D/K: dual-reader launch continuation for several releases |
| RC-D20 | A/B: filename format boundary |
| RC-D21 | K/B: manual old-user convert_later only; no command writes it |
| RC-D22 | M/N: TOML next release, no preparatory names-only release |
| RC-D23 | B/F/J: literal env off works; [] clear, env.unset, native, table off |

## 5. Bounded writer briefs and serial integration

Hand a writer its job paragraph, its ownership row, A/B/D contracts, applicable matrix rows,
and common gates. It need not rediscover the design. **Settled: do not re-open** the behavior
quoted in each brief. Every job reports red test name/result, green commands, files, commit SHA,
and any actual fence or contract mismatch. Commit messages below are exact defaults.

### A1 — positioned document/index/fixture foundation (present; final API acceptance pending)

- **Settled:** “the bytes of one value … every other byte untouched”; filename is version.
- **Change:** the candidate contains the configdoc adapter/index, TOML encoder, fixture helpers,
  filename constants and pinned/vendor dependency; no loader or launch activation. Do not treat
  those APIs as accepted until the final documentation candidate proves equality with the
  HIGH-reviewed 91f3 source and Root completes the final gates.
- **Evidence:** before F4, the focused foundation suite passed `configdoc`, `testconfig`, `json5`,
  `paths`, `config`, `packdecl`, and `packload` but failed the singleton ordering test in `tomlx`.
  Lead-integrated F4 now makes `tomlx`, `configdoc`, and `packdecl` pass. The targeted HIGH review
  passed on source 91f3; Root's final candidate gates remain. Captured review and test evidence is
  in durable recovery evidence.
- **Commit:** foundation arrived in the candidate's imported commit series; the original A1
  message was `feat(config): add positioned document and fixture contracts`.

### F4 — preserve raw TOML array-table identity (implemented; HIGH-reviewed source; final gate pending)

- **Settled:** preserve the ordered semantic map, scalar values, `[]any` output and source spans.
  A raw `[]map[string]any` is an array of tables even when it has one row; an assigned/inline
  array remains the `[]any` path. Do not infer array-table identity from metadata count alone.
- **Implementation:** lead-integrated commit `91f3f81d8ea5b9a135aaf8f61d121d320db757b7` changes
  only `internal/tomlx/tomlx.go` and `internal/tomlx/tomlx_test.go`. It retains raw table-array
  identity, segments exact-prefix headers including a singleton, and converts each raw map with
  its whole row span. The `[]any` inline/assigned/mixed-array path remains separate; no blanket
  positive-header-count rule, registry or second parser was added.
- **Tests/evidence:** the singleton public regression was red before the fix. After it,
  `tomlx`, `configdoc` and `packdecl` full suites pass. Coverage includes repeated parents,
  inline/nested/mixed array authored order, both inline-assignment/table-header uses of the same
  child path, and differing child key order/values. Semantic decode values and configdoc spans
  remain unchanged. The lead's captured candidate report carries exact command/output.
- **Remaining gate:** the targeted HIGH review passed on source `91f3f81d8ea5b9a135aaf8f61d121d320db757b7` (receipt in durable `config-recovery-evidence/final-high-review.md`). Prove the final documentation candidate preserves that source, then await Root's final gates/landing. Do not describe the full redesign as accepted/built until those gates.
- **Fence:** no configdoc, schema, F8, packdecl, runtime, source parser/helper or vendor edits.

### B1/B2 — schema seam, then format-aware load/merge/scope

- **B1 present in candidate:** schema keys, scope/type/removal policies, rename descriptors and
  move-only validator extraction; no loader activation. Root's final candidate gates and
  acceptance remain pending.
- **B2 settled:** project installs/requests; user authority unchanged; empty lists clear;
  table-off distinct from literal strings. Implement discovery collision checking, format-aware
  includes/config.d, validation, source maps and schema-specific merge rules; preserve legacy
  reader semantics and error order. Standard readers return new-vocabulary effective config;
  retain only explicitly named compatibility projections for downstream callers while H lands.
  Tests: `readableconfig_test.go`, `removals_test.go`, existing userlayer/sources/hostscope/
  mount/ports/env/injail tests rewritten with both-format fixtures. Starter below is red today
  because LoadWorkspaceConfig ignores TOML. Commit `feat(config): load and validate readable TOML layers`.
- **Gates:** `go test -short ./internal/config ./internal/paths ./internal/tomlx`; no runtime
  launch until H's consumers have integrated. If an authored legacy variant cannot be normalized
  losslessly, preserve old read behavior and return migration-specific differences, not aliases.

```go
// Add to package config's readableconfig_test.go; os/filepath/testing imports.
func TestLoadWorkspaceConfigReadsReadableInstall(t *testing.T) {
    dir := t.TempDir()
    if err := os.WriteFile(filepath.Join(dir, "yolo-jail.toml"),
        []byte("[install]\nnix = ['just']\nmise = { python = '3.12' }\n"), 0600); err != nil {
        t.Fatal(err)
    }
    got, err := LoadWorkspaceConfig(dir, true, nil)
    if err != nil { t.Fatal(err) }
    raw, exists := got.Get("install")
    install, valid := raw.(*jsonx.OrderedMap)
    if !exists || !valid { t.Fatalf("install missing: %#v", got) }
    nix, _ := install.Get("nix")
    if !reflect.DeepEqual(nix, []any{"just"}) { t.Fatalf("nix: %#v", nix) }
}
```

### D1/D2 — grouped manifest codec, then contribution/default parity

- **D1 settled:** “grouped by kind with the identity once”; same schema at every read mode.
  Decode TOML into existing accessors with file/key locations and origin-format metadata; reject
  unknown fields and old authored keys via shared conversion descriptors. Define models/
  providers/variants/replace fields now. Preserve all 22 live kinds and pack-wide metadata,
  including needs, supersedes, session homes, provider keys/env, model checks, autonomy, forks,
  patches, built extension/prebuilt fields. Commit `feat(pack): decode grouped TOML manifests`.
- **D2 settled:** obvious defaults unwritten. Implement install scalar/table/default/bin, run=bin,
  scope=workspace and extension codec defaults; launch contribution lowers through existing
  selected posture channel. Do not let defaults rewrite legacy program needs into recipes.
  Tests: full old manifest → normalized new manifest golden pairs, all accessors equality,
  strict/use/tolerant unknown field cases, explicit manager exceptions, fork build fields,
  both manifest siblings. Commit `feat(pack): normalize readable contribution defaults`.
- **Gates:** `go test -short ./internal/packdecl`; downstream `go test -short ./internal/packload`
  before handing off. Fence: no new tool-name registry or manifest-as-code.

### E1/E2 — qualified pack entries, then locks/resolution

- **E1 settled:** “a pack yolo does not ship is named by its source”; only listed packs selected.
  Parse pack_sources + handles/direct git tables, preserve safe slugs/source provenance, resolve
  local directory identities including symlinks and refuse duplicates with both origin lines.
  Keep implicit conventional content-pack behavior separate from local/<name> source convention;
  migration must not accidentally select every directory. Commit `feat(pack): resolve qualified source handles`.
- **E2 settled:** no ref follows remote default branch once, lock pins until update. Change
  resolution/store/update together, not source Parse alone; keep fork build identities stable and
  explicit refs authoritative. Tests: bare local git fixture with HEAD=trunk, lock/update,
  source tree versus single pack, failed remote default discovery, aliases to same local path,
  qualified settings references. Commit `feat(pack): lock default-branch sources on first fetch`.
- **Gates:** `go test -short ./internal/packsrc ./internal/config ./internal/packload`;
  relevant `integration/packfetch_test.go` only at combined activation. No network API tests.

### F1/F2 — selection namespace, then shared model facts/agent projection

- **F1 settled:** providers select directly; variants provider/variant; native means untouched.
  Use B's extracted validators and D's types; retain profile resolver typed/wire contracts
  internally while adapting inputs. Providers/aliases/variants have one validated namespace;
  -p and active_provider consult it, capabilities/via/options/subagents remain intact.
  Tests: new `activeprovider_test.go`, rewritten profileselection/profile sets/via tests,
  native env-cleanup and no-declaration-selects tests. Commit `feat(provider): select providers and variants by name`.
- **F2 settled:** model facts written once for every agent. Expand every_model then explicit
  row; catalog below explicit facts, twin graph and maker inference; emit api/thinking/images/
  compact_at to actual consuming derives. Keep old flat wire facts supported internally if
  needed, but config records/display use new names. Tests: twins cycle/missing/self, unknown
  maker, catalog absent, explicit overrides, image bounds and all agent render goldens.
  Existing catalog only enumerates ids; add fact extraction/fallback through declared catalog
  sources, never an agent/API probe or invented default. Commit `feat(provider): compose shared readable model facts`.
- **Gates:** `go test -short ./internal/config ./internal/packload ./internal/modelcatalog ./internal/entrypoint`;
  Lua syntax/derive tests as provided by existing pack tests. Preserve one host composition.

### G1/G2/G3 — ceiling/request normalization, concrete scope, anonymous transport

- **G1 settled:** always vs on_request is not two forms of grant; outside ceiling skipped.
  Map declared broker kinds and pack settings into allow/requests; remove enabled/default_enabled
  as new-format selection mechanisms; maintain legacy input behavior. Nudge unselected settings
  before fatal check; reverse warning must serve nothing. Workspace owned switch JSON uses off,
  remains machine state; no inline host daemon can bypass pack selection. Commit
  `feat(broker): normalize readable allowances and requests`.
- **G2 settled:** resolve repository/remotes and explicit requests **before** concrete grants.
  Empty concrete git/github scope has no inherited repo grant, no scope prompt and no phantom
  wildcard record, including non-Git and no-remotes Git workspaces. Explicit allowed additional
  repo requests still work outside a Git cwd. Preserve gate containment and provenance, scope
  source change/removal drift, live grant withdrawal and granted/skipped prompt rows.
  Tests: `TestConcreteScopeBeforeInheritedGrants` parameterized independently per broker and
  launch/check/apply producer callers; mocks record gate input, not only its final output.
  Commit `fix(broker): materialize grants only for concrete workspace scope`.
### G3 — adapt readable scope to the existing anonymous public-read route

- **Settled:** an HTTPS fetch tries anonymously before any scope/read-grant decision. Public content outside scope can pass without lending a login; a private 401 reaches the host credential helper only when the live `credentialPolicy` approves scope and read grant. This broker order and transport already exist; do not reimplement either.
- **Change:** consume and protect the existing route while adapting new-vocabulary scope/remediation. No producer/admission-order implementation. Add a producer/launch/apply caller regression only if the new-vocabulary adapter could bypass or alter the current route; assert no phantom approval is created.
- **Tests first:** existing `TestHTTPSPublicFetchWithoutReadGrantOrApprovedURL`, `TestHTTPSPrivateAnonymous401RefusesWithoutCredential`, `TestHTTPSCredentialFillRechecksLiveReadGrant` and `TestHTTPSCredentialApprovalRechecksLiveReadGrant` pin public, private and credential-fallback behavior. Any adapter-specific regression must exercise its production caller and retain those outcomes; no duplicate transport fixture is needed.
- **Fences:** do not reorder `Broker.Run`, change the existing anonymous HTTPS leg or broaden private credential fallback. SSH and every push retain their existing hard scope/grant preconditions; `create_pr`/`issue` remain refused at use until their separately scheduled delivery.
- **Commit:** `feat(config): adapt readable scopes to anonymous HTTPS`.
- **Gate:** focused config/broker/caller tests for the changed adapter; run the existing `gitbroker` HTTPS cases above as a regression, not as proof of a new transport.

### I1/I2 — conventional content discovery, then append/replace and delivery

- **I1 settled:** home laid file-by-file; overlay owner formats in overlays; no manifest needed.
  Expand conventions once in packload for host/check/jail, use shared file-destination resolution
  and claims. home cannot mount a shared parent wholesale; disjoint children coexist and real
  conflicts remain explicit. Preserve exec modes, local/fetched link policy, host ownership/
  retirement. Tests: two home packs disjoint files, same-file collision, nested surface,
  manifest-less overlays/profile gates/README. Commit `feat(pack): discover conventional home and overlay content`.
- **I2 settled:** contribution lists add/objects merge, replace explicit. Update posture flag
  fold and overlay/list integration while preserving captured/computed/managed precedence and
  provenance. files.only uses declared source tree filtering; pi package registration stays one
  addressed tree, stable qualified landing. Tests: agy permission+model flags on both notches,
  legacy replacement unchanged, overlay arrays append, replace=true, host/jail file parity.
  Commit `feat(pack): compose readable contributions additively`.
- **Gates:** `go test -short ./internal/packload ./internal/packoverlay ./internal/agentcfg ./internal/entrypoint ./internal/cli/run`.

### H1/H2/H3 — records, consumers, then mechanics vocabulary adapter

- **H1 settled:** machine records JSON in new vocabulary, inherited user copy TOML.
  Export conversion/read/write seams L consumes; do not grant approval during conversion. Update
  inheritance census/filter/origins, canonical snapshots and boot baselines. Tests: host/jail
  equality, own-workspace shortcut, nested explicit layer, unknown-key census, ro file bind,
  macos-user session digest safety. Commit `feat(config): deliver readable inheritance and snapshot records`.
- **H2 settled:** config apply remains same approval with per-change timing. Wire new keys into
  all run/attach/check/env/package/resource/network/GPU/loophole/provider consumers and grouped
  per-path apply planning; folding one nested change must not mark unrelated siblings applied.
  Preserve running tree on attach, no restage. Add launch-level empty-scope tests from G2.
  Commit `feat(run): consume readable config across launch and live apply`.
- **H3 after mechanics stream candidate:** retain applied-config arguments/fact ownership;
  adapt TOML vocabulary to its shared change-timing/facts contract. No second mechanics renderer.
  Commit `refactor(config): adapt readable vocabulary to applied mechanics facts`.
- **Gates:** `go test -short ./internal/config ./internal/cli/run ./internal/entrypoint ./internal/cli/check`;
  targeted nested launch/apply/attach gates in O. Fence: I packfiles, K CLI dispatch, mechanics
  applied-config calls and hostskills implementation remain their owners'.

### J1/J2 — byte splice, then scope-aware effective edit verification

- **J1 settled:** a comment belongs to nothing; only requested value/item/key bytes change.
  Implement parsed byte edits, style-copy insertions, lock physical target/re-read and atomic
  write, no-op and dry-run; expose planned edit/result/diff API to K/L. Tests are byte-golden
  documents in both formats with concurrent process test, corruption restoration and IO errors.
  Commit `feat(config): splice requested edits without reformatting`.
- **J2 settled:** target file by effective section origin; schema-specific removals, validate
  intended effective result. Resolve all target flags once and disclose actual file; only named
  file written. Validation reparses/recomposes; mismatch restores original while still holding
  lock. Test includes/fragments/union clear/off/env.unset/native/unknown key/jail scope.
  Commit `feat(config): verify targeted edits against effective layer semantics`.
- **Gates:** `go test -short ./internal/configedit ./internal/config`; `go test -race ./internal/configedit`.
  No generic TOML round-trip writer and no comment ownership heuristics.

### L1/L2/L3 — config graph, records, then pack migration

- **L1 settled:** proves equality or writes nothing. Build plan from complete readable old graph,
  normalize both sides with same B/F/G contracts, fold defaults/shared facts only when byte-equal
  canonical JSON; preserve **all** comments document order. Resolve symlink targets and sibling
  names without merging files; include paths respelled; absent optional files reported.
  Test full graph + unsupported repo-specific grant exact diff/zero writes; do not serialize
  undocumented escape keys. Commit `feat(config): plan lossless explicit TOML migration`.
- **L2 settled:** requested rewrite plus .pre-migrate, respell approvals/boot records.
  Apply planned files/backups atomically per target under shared locks; preflight all reachable
  targets and collisions before first write; report/restore on failure without blessing authority.
  User files host only; project migration in jail cannot touch host records or files outside scope.
  Preserve readable record safety on failure and say workspace will prompt if record unreadable.
  Commit `feat(config): migrate files and respell existing records safely`.
- **L3 settled:** pack migration beside config migration, git mv plain files to home/pi package.
  Use D/I normalized parity (including file modes/content and posture), extract overlays in owner
  format, keep comments, no source-code changes, refuse destination collisions up front; folders
  with no checkout use filesystem moves. Commit `feat(pack): migrate manifests and conventional content layout`.
- **Gates:** `go test -short ./internal/configmigrate ./internal/packmigrate ./internal/config ./internal/packdecl ./internal/packload`.
  Record-only JSON and config.lua remain untouched unless explicitly part of the required record
  conversion; config.lua is never migration cleanup.

### K1/K2/K3 — command namespace, named edits, migration offer and diagnostics

- **K1 settled:** config means own configuration; six verbs move to surface with exact old
  refusal for one release. Update registry/argv/help and testable dispatch, reuse composition
  resolver/color target unchanged. show/why effective provenance; apply/drift/dump stay config.
  Commit `feat(cli): separate config editing from surface composition`.
- **K2 settled:** named verbs sugar over generic validated engine and writes finish their step.
  Implement pack add/remove, allow, request, active-provider and generic set/add/unset flags;
  check after writes, grant approval offer through existing gate/live apply, never auto-approve.
  Host-run remediation in jail, correct quoted argv, no new permission hatch. init pointer/no
  default scaffolding/info-exclude; missing section target reported. Commit
  `feat(cli): edit readable config with validated named commands`.
- **K3 settled:** explicit migration offered per launch, decline/nonTTY legacy continues.
  Wire config/pack migrate libraries, real run offer hook H owns; convert_later manual only.
  Add derive-overlap/pi-loose-copy/compression lint; diagnostics for old local/fetched manifest,
  both siblings, future format and rename recipe. Commit `feat(cli): expose migration offers and readable config diagnostics`.
- **Gates:** `env -u NODE_OPTIONS go test -short ./internal/cli ./internal/cli/check` (baseline
  ambient failure noted, not a blanket permission to strip runtime environment in production).
  K3 run hook edit is handed to H before final activation, not a cross-fence write.

### M1/M2 — bundled manifests/config, then repository-wide fixture conversion

- **M1 settled:** shipped packs migrate with new reader; source-cited per-manager hints retained.
  Use L3 on all actual shipped packs, review output; currently **30** tracked pack.json files,
  not design appendix's older 21. Move content/executables without losing metadata/exec bits.
  Revise release-skew decoder tests to the intentional new-name boundary, do not delete skew
  coverage. Convert root project config and examples only through proven L1/L2 mapping.
  Commit `feat(pack): ship readable TOML manifests and conventional assets`.
- **M2/O fixture handoff:** M migrates pack test assets; O converts remaining scattered Go test
  strings to A fixture helpers after owners finish semantic tests. Legacy-path/unknown-key tests
  stay format-1 cases; filenames under real generated snapshots update to format 2. No blind
  textual JSON→TOML substitution. Commit `test(config): centralize readable config fixtures`.
- **Gates:** `go test -short ./packs ./internal/packdecl ./internal/packload ./internal/entrypoint ./internal/cli/run`;
  embedding/executable/shipped-client checks; source bundle build. No CHANGELOG entry.

### N1/N2 and O1/O2 — docs/advice, acceptance, landing evidence

- **N1 settled:** every unowned stop prints named command or generic/host-run counterpart.
  Sweep current unowned advice sites and overlaps; update authoritative config_ref.txt plus
  schema coverage. It is currently embedded hand-maintained reference, **not a generated doc to
  skip editing**. Update README/AGENTS/pack READMEs/userguide/config-safety/pack-system/providers/
  loophole-system/storage docs. Preserve environment-first description. Route owner-owned
  briefing/skill/rendering hunks through Root/the owner; N does not edit them. Commit
  `docs(config): explain readable config and command-driven edits`.
- **N2:** review roadmap links/source stage after actual implementation; mark built only after
  gates, retain plan until graduation. Regenerate completion-reader census when cross-package
  tests changed; gate script owns generated output. Commit `docs(config): record verified redesign delivery`.
- **O1:** end-to-end subprocess CLI tests: edits have one named target, exact unrelated bytes,
  migration no writes on loss/decline, old-format decline launches, surface retired messages,
  init absent behavior; independent both-broker concrete-scope cases at launch/apply producer.
  New `integration/readableconfig_test.go` and `integration/configmigration_test.go`, gated
  requireJail, serial. Commit `test(config): cover readable launch editing and migration end to end`.
- **O2:** run combined gates, nested launcher absolute path from durable throwaway workspace,
  source bundle and host/CI backend checks. Report exact commands/results; gate failures fix
  forward by owner. No worker runs full slow suite independently or invokes agents interactively.

### Integration waves (not concurrent edits to a shared base)

1. Use the lead-integrated **F4** implementation already HIGH-reviewed on source
   `91f3f81d8ea5b9a135aaf8f61d121d320db757b7`; do not commission or repeat that review. Final
   documentation integration must prove source equality with 91f3. Root's acceptance gates and
   landing remain pending; the foundation APIs are not yet called accepted in the final candidate.
2. **Config/source/manifest contracts:** continue B2, E1/E2 and D2 from a stable integrated
   foundation, with each importer tested before the next step. Preserve distinct local source,
   authored handle, lock and staged identities. `[[launch]]` and default normalization belong to
   D2.
3. **Selection, models and scope:** F1 follows B1/D1; F2 implements actual catalog facts and
   consumer projections (not an assumed helper). G1/G2 follow their schema/source prerequisites;
   independently test git and github concrete scope, including no-Git/no-remotes and explicitly
   allowed extra repositories. G3 preserves the existing anonymous HTTPS route while adapting
   the new vocabulary; it must not re-order broker admission or duplicate transport.
4. **Pack composition and editing:** I1/I2 follow D2, E1 and F1. J1 may begin once F4's document
   spans are accepted; J2 follows B2/F2/G1 and verifies scope-targeted semantic equality under
   the write lock. Keep config and manifest list-merging semantics distinct.
5. **Records and lossless migration:** H1 follows stable B/E/F/G/I schemas. L1/L2/L3 follow
   B2/D2/E2/F2/G2/I2/H1/J1 as listed in their task contracts. Migration preserves every comment,
   proves exact semantic equality, locks and backs up physical targets, and writes nothing when
   any graph or grant is unrepresentable; old readers stay available for the approved window.
6. **Runtime and CLI:** H2/H3 and K1/K2/K3 wait for the required runtime API and CLI source seams
   to be landed by Root. H owns launch/attach/apply/migration-offer activation; K owns registry and
   editing commands, and may not edit mechanics/stream-owned `internal/cli/apply.go`,
   `internal/cli/applyhostskills.go`, `internal/cli/commands.go` or the fenced presentation,
   stdout, hidden-prompt and color sections of `internal/cli/config.go`. H3 consumes, never
   duplicates, the mechanics applied-facts path.
7. **Assets, docs and acceptance:** M1/M2 follow integrated readers and normalized manifests;
   N1 follows integrated behavior; O1/O2 test the combined candidate. N2 updates roadmap/source
   stage only after actual implementation and acceptance evidence. Full integration and landing
   gates remain the landing authority's one combined run.

No intermediate commit is a release candidate: the dual-reader/new-asset/default switch is
released only after the combined acceptance wave. Internal compatibility adapters are deleted
in their owning consumer integration job, not left as indefinitely accepted aliases or TODOs.

## 6. Engineering details settled below product altitude

- **Canonical comparison:** convert old inputs to a new-vocabulary semantic map, including
  ordered list meaning, scope, options, removals and effective defaults; compare `SnapshotJSON`
  bytes. Compare old pack projection to new pack projection plus delivery content/modes and
  selected-posture effects; comparing only manifest text is insufficient.
- **Comment migration:** retain a document-order comment stream; changing `//`/block comment
  delimiters to `#` is format conversion, not inferred attachment. Place before the next emitted
  key, or at end when its following key was omitted. Do not drop “template” comments.
- **Symlink writes:** edit the canonical physical target, rename the user's link to the new name
  and name it in output ([RC-D41](readable-config.md#RC-D41)); migration additionally respells link/include entry consistently. Refuse ambiguous
  sibling targets/backups before mutation; never replace the symlink with a regular file by
  accident. Relative include/fragment links preserve their graph, not merely current values.
- **List semantics:** [] clearing is the accepted removal override; nonempty config lists union
  in order. New manifest contribution arrays append unless replace=true. These are distinct
  merges; do not reuse one generic deep-merge helper without a schema/format parameter.
- **Configuration keys not exemplified:** preserve each live key by census; moves are limited to
  accepted shape/renames. Rich GPU/network/package options must not be reduced to the simplified
  example if a legacy configuration carries more facts. Only lossless folds proceed.
- **Generic value arguments:** schema determines scalar/list/table type; command input conversion
  must preserve version/name strings and explicit TOML typed values, validate before touching
  bytes, and show exact resulting hunk. No guess based on whether a string looks numeric.
- **Source identity:** authored handle, physical directory, lock address and staged slug are
  distinct. Track origin spans for duplicate diagnostics; no alias accepted only to hide a
  collision. Migrate explicit old names/references consistently or report loss and refuse.
- **Conventions:** explicit keys win; explain supplied values and origin. No heading, lone
  provider, manifest on disk, packs source folder or empty table selects/authorizes anything.
- **Approval records:** respelling is not approval. Host-only records remain host-side; source
  label changes caused only by renamed file may be rewritten, real repository/permission drift
  must still prompt. Read failures left unchanged are reported as one-time prompts.
- **Empty broker scope (user addition):** ceiling/default capability is abstract; materialize
  grants from actual remotes plus allowed explicit repository requests. Skip vacuous scope
  work for git and github independently; do not globally disable GitHub outside Git cwd.

## 7. Conflicts, representation limitations and non-goals

The parent confirmed the following conservative constraints for implementation planning; they
are **not** new owner product rulings and do not authorize wider credentials or autonomy.

| Observation | Binding implementation treatment |
| :--- | :--- |
| Migration transcript drops template comments, but approved migration rule says nothing dropped | Normative rule wins: preserve all comments in document order, including template comments. Report line shrink truthfully. |
| Local-old pack refusal examples conflict with approved dual config+manifest reader window | Window wins: old readable packs remain usable in next release; refusal after old-reader removal names migrate/author/fork. No reader-removal version invented. |
| Accepted local MCP example places `mcp_servers.tavily` after another server's table heading | TOML parser follows actual scope; generated unset writes root dotted key before headings or proper parent table. Do not copy invalidly scoped example bytes. |
| pack_sources example places packs after its table heading | Same: root packs list before first heading; parser never treats TOML bare keys as magically root. |
| Current launch kind retired to preserve autonomy, accepted doc introduces [[launch]] | Accepted syntax wins; lower into selected existing posture/autonomy channel, preserving notch behavior and demonstrating old/new equivalence. No unconditional bypass. |
| Legacy legal grants can scope a branch/read to one repo; accepted flat requests may not represent that exact scope | Migration prints exact differences and writes **nothing** for unrepresentable cases. Preserve old-reader usability in transition; never broaden to all remotes or add an undocumented escape key. Required regression fixture: two scoped remotes, legacy branch grant for only one. This is a representation limitation, not a reason to delay the whole build. |
| Legacy null model facts have no universal TOML null equivalent | Table-valued off, env unset, [] clear and native are settled. Preserve proven representable removals; unrepresentable scalar fact tombstones fail migration equality with exact path. Do not reserve "off" where a string fact is legal or erase fact tombstone semantics internally. |
| Catalog design says facts “pending catalog check”; current Go reader has ids only | F2 supplies actual declared-source fact/fallback projection; missing catalog stays unavailable, not fabricated. Existing per-agent catalog inheritance is exercised in render tests. No model-list redesign prerequisite. |
| Seed predicts a jail-mechanics schema coverage test; that sibling design is accepted but not built at base | Use actual liveapplyplan tests plus new key/path census here. Consume mechanics stream when available as directed, do not claim absent tests already enforce coverage. |
| Seed says config-ref regenerated; current source explicitly says hand-maintained | N edits embedded config_ref.txt and its coverage tests; generating it is not invented scope. |

**Owner and runtime fences:** never edit owner briefing, exported personal-pack or user-home
content, including tempting `internal/cli/briefing.txt`/templates and
`internal/jailcontent/briefing.go` or built-in/generated briefing hunks. If this redesign later
needs related advice or rendering, route a narrow handoff through Root/the owning lead; N does
not take that write. Do not implement the separate computed-briefings work or introduce new
permission design beyond the settled broker contract, and do not spend paid Astra/Fable research
on this build. Do not activate TOML discovery before the foundation is accepted or start any
later wave before its stable predecessors and required Root-landed runtime API.

**Don't:** patch current-main legacy prompts; activate every pack by default; add agent/tool
registries; silently widen grant scope; infer provider selection from declaration; reformat
unrelated bytes; delete config.lua; auto-migrate; move embedded packs into writable cache;
restage attach trees; introduce source code in manifests; fix unrelated designs; write a
CHANGELOG entry; change .vantage.toml; clear Go caches; deploy/install in jail; push.

## 8. Gates and Ships with

**Builder gates:** each owner works from the integrated base, formats only its changed Go files
with `gofmt`, runs its scoped seam tests and build checks, and asserts its owned-file/index
fence. Writers do not run `just format`, `just done`, `just check-ci`, whole-tree snapshots or the
full integration suite. Keep ordinary HOME and shared cache locations; do not reset or relocate
caches. No env override is used for ordinary focused gates. Automated test harnesses retain their
normal temporary fixture HOME/workspace directories; this does not relax the developer's manual
matched-build fence below.

**Current foundation+docs checkpoint (this handoff):** Root grants the serial lease for this
checkpoint only after committed handoffs, source equality with the 91f3 HIGH-reviewed foundation and
a clean candidate; that HIGH review is complete and must not be replayed. This source-equality and
review evidence do not predicate the future full-redesign activation tree, which will include later
accepted changes, and are not perpetual review certification. Only after Root grants the lease, run
`env -u TMPDIR -u NODE_OPTIONS just done` once on the clean candidate before handing it to Root for
main landing; record the commands actually selected and retain the output. If `just done` selects only
a completion subset and full `check-ci` has not already run on this exact candidate, also run
`env -u TMPDIR -u NODE_OPTIONS just check-ci` once. If `just done` selects full `check-ci`, do not
duplicate it. Preserve ordinary HOME and shared-cache defaults; remove TMPDIR/NODE_OPTIONS only for
these authorized completion invocations.

**Future final combined activation gate:** Root grants one serial activation lease only after committed
handoffs and a clean integrated tree; no fixed equality with 91f3 or additional paid HIGH review is
required. Run `env -u TMPDIR -u NODE_OPTIONS just check-ci` and the combined integration suite once on
the final activation tree. Root's separate `just done` completion receipt remains owed after main
landing: run `env -u TMPDIR -u NODE_OPTIONS just done` once on the clean landed candidate, record the
commands it actually selects, and retain its output. No whole-tree or completion gate runs before the
lease. Preserve ordinary HOME and shared-cache defaults; remove TMPDIR/NODE_OPTIONS only for the
authorized completion invocations.

```bash
# Root-authorized future final activation only, on the clean integrated candidate.
# Mandatory landing gates: whole-tree check-ci and combined slow suite once; targeted integration cases run by their owner.
env -u TMPDIR -u NODE_OPTIONS just check-ci
go test -count=1 -timeout 0 ./integration
# Manual matched-bundle build: preserve normal HOME/shared Go caches; use durable paths.
mkdir -p /workspace/.yolo/durable/readable-config/tmp /workspace/.yolo/durable/readable-config/bundle-home
TMPDIR=/workspace/.yolo/durable/readable-config/tmp python3 scripts/matched-bundle.py build-dev --repo /absolute/path/to/owned-repository --home /workspace/.yolo/durable/readable-config/bundle-home
# Use its emitted absolute launcher from a durable throwaway workspace for fresh launch,
# attach, config apply/drift, migrated includes and pack-tree checks. Do not publish this bundle
# into real HOME or alter HOME. The automated harness keeps normal temporary fixture HOME dirs.
uvx vantage-check docs/design/readable-config-plan.md docs/plans/roadmap.md
uvx vantage-check index
scripts/vantage-check.sh userguide/
# Root-owned completion receipt after main landing; record commands selected:
env -u TMPDIR -u NODE_OPTIONS just done
```

- Builders run only integration tests reading their seam, e.g. packs/packfetch/profiles_options/
  gitbroker/githubbroker/jailgrant/attachprofile/attachpacktree and new readableconfig/configmigration;
  Root runs the full slow suite once on final combined tree. `requireJail`, no `t.Parallel`.
- `go test -short ./...` includes integration compile. No automated interactive agents or API
  calls. Local git and HTTP fixtures exercise credentials with spies rather than real secrets.
- Nested podman cannot validate rootless-only delivery or host-loopback reachability; real host
  or CI must cover those backend claims and report podman rootless/network facts. All backend
  argv, inheritance, scope and apply outcomes also have unit producer tests.
- Docs ship with current implementation vocabulary, not future-reader-removal promises:
  config-ref, CLI help, README, AGENTS, pack READMEs, userguide configuration/settings-per-setup,
  config-safety, pack-system, providers, loophole-system, storage-and-config and relevant
  environment/credential refs. N checks all overlap claims, not just filename links.
- Fixtures ship both formats deliberately; default helper new, legacy/error/skew fixtures explicit.
  Every literal old filename remaining after census has a named reason, not a broad allowlist.
- No old-reader removal now. Later release uses existing migration/rename machinery and refuses
  format 1 with command; it is separately scheduled, never hidden inside this implementation.

## 9. Completion record owed by the build

Each owner returns its red/green evidence and commit. The integration record enumerates matrix
rows with candidate test evidence, residual representation limitations, mechanics adapter
candidate, baseline environmental failures, backend limitations, and no staged files. Stamp
this plan BUILT only after the requested redesign is integrated and verified, retaining it until
system-reference graduation. The roadmap already places this work and its five authorities;
its link to this completed handoff changes in the same planning commit, without copying state.

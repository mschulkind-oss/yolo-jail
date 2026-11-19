---
title: "Plan: managed MCP sign-in, capture, delivery, status and callback ownership"
date: 2026-11-17
status: accepted
stage: DECIDED
next: "Finish the retained candidate's synthetic gate and independent review; native/rootless/Mac/provider witnesses and full landing suite remain separate"
depends-on:
  - managed-mcp-sign-in.md
  - mcp-auth-sharing.md
tags: [plan, mcp, oauth, packs, broker, status]
summary: "Accepted serial build contract with a retained synthetic implementation candidate: bundles, capture, bounded issuance, delivery, callback ownership and effect-free status/advice. The coverage inventory records account-free evidence; independent landing and native/provider witnesses remain separate."
---

# Plan: managed MCP sign-in

**Current scope:** generic MCP authentication sharing only, under the owner's
[MS-R6 correction](managed-mcp-sign-in.md#decision-ledger). Provider-specific bundling is
withdrawn. The remaining original task snippets and receipts below are historical planning
examples, not instructions to install or ship any provider integration; the synthetic
user-supplied fixtures replace that assumption.

**Current implementation:** see the [candidate checkpoint](#candidate-implementation-checkpoint) and [coverage inventory](managed-mcp-sign-in-coverage.md). The following source/planner receipts are historical baseline evidence, not candidate greens.

**Original hand-off written against:** `15da1299ffe8c65db4a3f2beef9f8421dabbb99c`, 2026-11-17. The original complete planner hand-off read source and ran baseline fixture tests at `897a7951a44fa79fef305b7d90be9e652e086f01`, then checked HEAD `a4dfc67d847d7d7c0b5fd06515cf93e86a06ad54`. The range from that HEAD to this stamp changes only the scoped TOML pack-import design, plan, grounding and roadmap; no code, MCP authority or tested fixture changed. This publication preserves that source/test map and incorporates the accepted MS-D14 closure. Primary/index were clean before publication. No runtime behavior is implemented here.

**Authority:** [accepted managed design](managed-mcp-sign-in.md), especially its [decision ledger](managed-mcp-sign-in.md#decision-ledger), [engineering closure](managed-mcp-sign-in.md#engineering-closure) and [acceptance scenarios](managed-mcp-sign-in.md#12-acceptance-scenarios). [Sharing design](mcp-auth-sharing.md) keeps identity, authority, canonical state and sharing; [sharing plan](mcp-auth-sharing-plan.md) keeps its independent native-origin and backend witnesses. The designer seeded; the planner completed against source; the implementer builds the tasks below. MS-D14 closes the provisioning-data seam through embedded-FS advice only. There are zero engineering or policy questions. Native witnesses remain validation owed after the synthetic gate, not build blockers.

**Evidence boundary:** source and tests were read, not real credentials. The original planner ran four account-free Go commands, without shell overrides of HOME, TMPDIR, GOENV or Go caches:

```console
go test -short ./internal/cli -run '^TestMCPAuthHeaderThroughMainPrintsOnlyTheHeaderValue$' -count=1
# PASS: internal/cli, 0.009s — baseline still explicitly rejects status.
go test -short ./internal/mcpauthdaemon -run '^Test(OnceStatusListsTheOneSignInServers|ServeRunsTheOnePollAndReportsItInStatus)$' -count=1
# PASS: internal/mcpauthdaemon, 0.040s — synthetic loopback issuer only.
go test -short ./internal/entrypoint -run '^Test(ClaudeRemoteMCPServerDialect|PiMcpTakesTheHeaderRouteForAOneSignInServerWhereTheBrokerRuns)$' -count=1
# PASS: internal/entrypoint, 0.097s — rendering only, no agent execution.
go test -short ./integration -run '^(TestMCPAuthBrokerSeedsButDoesNotAdoptUnprovedCodexSignIn|TestMCPAuthUserOffNeverStartsProducerOrDaemon|TestMCPAuthFinalHarvestLockFailureFailsSelectedLaunch|TestMCPAuthAttachRefusesMissingKeeperRecordWithoutLog|TestARootModReachesARealJail)$' -count=1
# PASS: integration, 0.016s — compile/short-gate only; no container/native witness.
```

Public-original provisioning was also checked without credentials or execution: `curl -q --fail --location --proto '=https' --proto-redir '=https' --max-time 60 https://codeload.github.com/slackapi/slack-skills-plugin/tar.gz/5b8802105740b5c045c97247fded2e73b205e95e -o /workspace/.yolo/durable/managed-mcp-plan-88a63b17-public/upstream.tar.gz` passed. Read-only tar inventory found 116 members, no symlink/hardlink/escaping path; three upstream executable files. Archive SHA-256: `c27db0793dbde76521a119b3be3cca81603577584429472a28c9e1ce7cbecb49`. Root `.mcp.json` SHA-256: `1b299e504b12f6be46955af4e5aaf3caaf31c88d991663e5605b9785230d5a61`; it uses `mcpServers.slack.type="http"`, URL, `oauth.clientId` and port 3118. Native manifest name is `slack`, version 1.8.0. MIT LICENSE SHA-256: `ccbd532eabd9c4a87884b19caa6738bd6541e047460a736aca3bf1ac4fd91300`. The archive is evidence/public source only; nothing was installed or extracted into primary.

Publication also ran `go test -short ./internal/packload -run '^TestEmbeddedProjectSkillDirsReadsMetadataWithoutMaterializing$' -count=1` at the stamped baseline plus this docs-only delta: PASS (`internal/packload`, 0.033s). That existing synthetic fixture proves the pure metadata-read pattern does not create the embedded cache; it is not a test of the proposed advice. Offline Vantage 0.12.0 targeted check and planning index cover this publication, not runtime behavior.

These baseline greens establish fixtures/callers, not acceptance of the proposed behavior. Future red commands below were not run: the original artifact-only planner and this docs-only publication authored no source tests or implementation. Gap confirmation is direct source evidence: no bundle declaration/reader/lease; `OnceServer` drops configured client ID; header refreshes below five minutes; `onceStatus` creates its store and suppresses source-free rows; CLI rejects status; Claude derive renders no helper.

## Precedence and execution fence

1. **Constraint:** design wins on behavior; managed design governs its four amended subjects over older sharing prose.
2. **Constraint:** tree wins on cosmetic facts only. This plan is advice and the first thing to be wrong. Never twist code to match a stale map.
3. **Constraint:** stop and report before committing if the predicted red is absent, a touched file belongs to another writer, a named symbol disappeared, a fix crosses the task fence, or an unrelated gate fails. Genuine behavior gaps go to the supervisor, not an implementer's guess.
4. **Constraint:** one source writer; serial commits in this order, no child fanout. No push, deploy, release, CHANGELOG, live owner config/grants/profiles/IAM, real credential-store reads, real agent/installers, or authenticated AWS/OAuth/provider calls. Tests use source, fake fixtures, public originals and loopback fake issuers only.
5. **Constraint:** manual scratch/evidence lives under `/workspace/.yolo/durable`; preserve default process HOME/TMPDIR/GOENV and shared Go caches. Existing test helpers may isolate *their synthetic child/test home*; never export a replacement HOME or TMPDIR for the entire Go invocation. Do not rebuild a cold cache or disable the completion collector.
6. **Constraint:** no quiet launch mode; pack read/exec trust disclosures remain unconditional. Native materialization callbacks must not call automatic installer capture. Emitted probes disable **both** readiness and auto-capture, not just readiness.
7. **Advice:** internal type/helper names below are planner-selected defaults, not new behavior rulings. Cheap choices: table versus slice internals, exact private note/lease basename, and unexported helper factoring. Keep one spelling per concept, one schema constant, explicit types. Externally significant behavior is not cheap.

## Interface map

Paths below are repository-relative. New paths are marked **new**; test suffixes belong to the named source's package. No new binary or Go dependency is needed.

| Path | Change and production boundary |
| :--- | :--- |
| `internal/packdecl/packdecl.go`, `kinds.go`, `toml.go`, `tomlencode.go`; **new** `mcpsigninbundle.go` | Typed `[[mcp_sign_in_bundle]]` schema, validation, encode/decode parity; agent format/helper/paste capability metadata. Format lists use `[native_sign_in] formats = [...]`: the actual v0.18.0 reader rejects any new top-level array, but tolerates this metadata table and retains Claude's program. |
| `internal/packdecl/*mcpsignin*_test.go` (**new**), `parity_test.go`, `tomlencode_test.go` | Strict/use/tolerant TOML declaration and round-trip regressions; existing legacy reads remain unchanged, with no new legacy spelling. |
| `internal/packload/packload.go`, `plugins.go`; **new** `mcpsigninbundle.go`, `mcpsigninbundle_test.go` | Selected pack-relative bundle/license validation through filesystem-backed and embedded readers; no launch fetch. |
| **new** `internal/mcpsignin/{inventory.go,resolve.go,delivery.go,advice.go}` and corresponding tests | Format-named confined reader, pure sign-in point / delivery decisions, and no-point-only embedded-FS provisioning advice; no server composition, egress or state creation. |
| `internal/agentcfg/luahook/derive.go`, `mcpauth.go`; **new** `mcpsignin.go`, `mcpsignin_test.go` | Pure native-name function declared by agent pack; per-agent decision facts and both helper command forms in derive context. |
| `internal/hostskills/plugin.go`, `plugin_test.go`; `internal/jailcontent/skills.go`, **new** `mcpsignin_test.go` | Preserve server content across whole-tree delivery; expose actual placement/refusal results, not a prediction that the agent loaded it. |
| Local synthetic `fixture-mcp` test integrations | Whole-plugin placement and identity retention; never embedded or offered as a shipped integration. |
| `packs/embed.go`, `executables.txt`, embedding regression | Remove unauthorized provider content from both inventories; test the real catalog and original embedded bytes. |
| `packs/claude/pack.toml`, `derive.lua`; `packs/pi/pack.toml` | Agent-owned formats/native-name/paste declarations; Claude JSON helper and pi bare declaration. Codex unchanged. |
| `internal/cli/run/launchinputmcp.go`, `launchinput.go`, `mcpauthshare.go`, `mcpauthboundary.go`, `mcpauthheader.go`, `mcpauthnotice.go`, `prepare.go`, `run.go`, `runcmd.go` | Freeze user-selected bundle provenance and actual delivered-tree inventory before creating share/render/lease decisions; carry expected client; all backend callsites. |
| `internal/cli/hostinputs.go`, `applyhostskills.go`, `host.go`, `hostmcponce.go`; `internal/entrypoint/hostrender.go` | Host apply/managed launch uses same placement and pure decision; preserve Linux session and Mac no-source boundary. |
| `internal/mcpauth/once.go`, `share.go`, `source.go`, `store.go`; **new** `outcome.go`, `status.go`, tests | Expected-client check, shared source classification, versioned token-free note, strict read-only descriptors/status projection, nonrefreshing token read. |
| `internal/mcpauthdaemon/main.go`, `once.go`, `header.go`; **new** `status.go`, tests | One launch-owned poll, immediate expired-record work, full source-free status, framing, cancellation-independent durable commit. |
| `internal/cli/mcpauth.go`, `mcpauth_test.go`, CLI help/subhelp sources | `status [SERVER] [--json]`; header form argument; common recovery; retain early dispatch and stdout purity. |
| `internal/entrypoint/packsurfaces.go`; **new** `managedmcpsignin_test.go`; `remotemcp_output_test.go`, `pi_mcp_remote_test.go` | Real derive-context / surface-loop wiring from frozen launch facts. |
| **new** `internal/cli/run/mcpcallback.go`, `mcpcallbacklease.go`, tests; `assemble.go`, `keeperplan.go`, `keeper.go`, `jailmain.go`, `trackingcleanup.go` | Optional exclusive IPv4 lease, pure publication plan, create fallback, keeper ownership and confirmed-gone release. |
| `internal/entrypoint/runtime.go`, **new** `mcpcallback_test.go` | Reuse `setupPublishedPortLocalnet`; callback DNAT failure gains paste next step. |
| `internal/setupcensus/kinds.go`, tests; `userguide/reference/settings-per-setup.md` | New declaration-kind cells; placement versus callback versus capturability distinguished. |
| **new** `integration/managed_mcp_sign_in_test.go`, `managed_mcp_callback_test.go`; existing `mcpauth*_test.go`, `packrootmod_test.go` | Account-free real jail placement/render/status/teardown and synthetic runtime callback wiring. |
| [Managed design](managed-mcp-sign-in.md), [this plan](managed-mcp-sign-in-plan.md), [sharing design](mcp-auth-sharing.md); `docs/plans/roadmap.md`; `packs/mcp-auth/README.md`, `docs/reference/mcp-configuration.md`, `internal/cli/config_ref.txt` | Current build boundaries, remaining authority corrections, user next steps, command contract; no graduation or native-success claim. |

## Reuse before writing

| Existing seam | Reuse and constraint |
| :--- | :--- |
| `packdecl.DecodeTOML`, `DecodeTOMLForUse`, `DecodeTOMLTolerant`, `EncodeTOML` | Grouped metadata uses established host strict/use and jail skew behavior. Add known fields everywhere; a tolerated skip cannot become authority. |
| `packload.LoadDirForUse`, `ReadSelectedPackFS`, `config.ResolvePack` | Validate retained bundle/license paths in the selected filtered view. A filtered-out manifest/license is unavailable, not silently restored. |
| `pluginpack.Load`, `ManifestPath`, `Plugin.Components` | Recognition and disclosure only. Do not mistake component presence for parsed remote-server inventory. |
| `hostskills.DeliverPlugin`, `deliverPluginTree`, `markManifest`, `PackLayer` | Existing whole-tree copy, two ownership marks, delivered namespace/name, flat downgrade/refusal. No second plugin installer. |
| `jailcontent.SkillPlan`, `composeSkillLayers`, `PrepareSkillsWith` | Actual jail tree is composed through `hostskills.ComposeInto`. Read the resulting delivered tree; publish placement/refusal outcome from the writer. |
| `run.frozenMCPForLaunch`, `freezeLaunchMCPComposition`, `sameSelectedMCPPacks` | Existing pre-staging snapshot and post-staging equality proof. Extend it with the verified user pack subset/order; do not infer user authority from all staged packs. |
| `packload.MCPServerScopes`, `run.mcpIdentity`, `onceServersOf` | Stable authority is same name plus digest of URL/transport/headers/client ID. Preserve default `http` and empty headers/client ID; inventory is separate. |
| `packload.EmbeddedFS`, `EmbeddedNamesFromFS`, `EmbeddedProjectSkillDirs`; `config.LoadPackEntries`, `PackEntry.Embedded`, `EmbeddedName`, `Name` | Pure registered FS enumeration/parser pattern and lowered user source mappings for advisory exclusion. Never call `packload.Embedded()` or resolve an unselected custom pack for advice; see the finite baseline seam below. |
| `onceShare`, `onceShareFrom`, `sharedStoreDir`, `HostOnceSession`, `HostHeaderShare` | Existing source qualification and Linux host program-owner boundary. Credential view/alternate storage/Mac host remain noncapturable. |
| `luahook.MCPAuthMatch`, `MCPHeaderCommand`, `buildDeriveCtxTable`, `entrypoint.deriveCtx` | Agent-owned name function and helper rendering belong here; no core agent/server switch. Preserve eligibility before stripping `sign_in`. |
| `CanonicalStore.ReadStatus`, `paths.OpenStateDirRoot`, `OpenRegularFileBeneath` | Strict canonical snapshot on existing safe lock; construct descriptor literally, with 2s wait, not `NewCanonicalStore`/`NewOnceStore`. |
| `SourceStore.Read`, `OnceBroker.candidate`, `expiryOf`, `ours`, `current` | Unlocked rename snapshot, same classification for status/adoption; project only presence/expiry/refresh-presence. Native writes alone take agent lock. |
| `OnceBroker.Pass`, `adopt`, `refresh`, `LockRefresh`, `writeBack`, `signOut` | Preserve record-lock re-read and commit-before-writeback. Status never calls them. Expired jail issuance delegates work to the *existing poll owner*. |
| `broker.serve`, `runOnce`, `BoundaryPass`, `ServeOnce`, `OnceSession.Start` | Existing resident/boundary/session ownership. One poll only; source-free record refresh must not disappear behind `len(Sources)==0`. |
| `hostservice.Session.JailID`, framed endpoint, `svcendpoint.Dial` | Local authenticated read/issuance, bound to this launch. No status fallback into a host canonical path from a jail. |
| `assembleRunCmd` publish/sysctl/env block; `setupPublishedPortLocalnet` | Runtime publication and jail-loopback DNAT are one plan. Callback port must enter `YOLO_PUBLISHED_PORTS`, not only `-p`. |
| `keeperPlan`, `forgetGoneContainer`, runtime tri-state liveness | Lease identity/lifecycle frozen with the running owner; unknown liveness retains lease, just as pack-tree teardown declines. |
| `newOnceDaemonFixture`, `setOnceSeams`, `askBroker`, `newOnceFixture`, `newASServer` | Existing declared Claude adapter/Lua, synthetic single-use issuer, event-controlled ticks, real framed handler. |
| `siblingEnv`, `ConfigurePackByName`, `bootJail`, `piMCPEnv`; `hostMCPFixtureUnder`, `stubDeclaredBins`, `applyHost` | Render/apply caller regressions without a real agent. Missing-binary checks use inert stubs, never agent binaries. |
| `dispatchOptions`, `recordOnceTier`, `defaultKeeperSpawner`; `mcpAuthHome`, `runYolo` | Stubbed production Run/keeper tests and real-jail integration. Preserve harness shared runtime-store links in integration fixtures. |

## Non-obvious traps

- `UserScopeSelectedPacks` is a declaration reader, **not permission to materialize**. Current detailed selection has FS-backed views; ordinary `loadedHostPacks → ResolvePackForProcess` can materialize embedded/filtered trees. Status must use only already-staged views plus effect-free configuration/provenance reads, and report unknown coverage rather than invoking that path.
- `NewCanonicalStore` calls `MkdirAll`; normal `Read` creates lock/repairs modes. Current `onceStatus → onceStore` violates zero effects before its strict reader runs. Replica status must also use a descriptor; its `canonicalFor` helper itself is a descriptor, but inherited waits are 10s.
- Status returning empty once rows with no source hides usable canonical records. Source-free header launches are legitimate and must remain fully inspectable.
- `SourceStore.Read` currently conflates absent/malformed/unsafe in a boolean/error. Add a structured projection shared with adoption, not a second status parser. Missing is not malformed; no clean parse means no sign-out.
- `Record.AdoptedFrom` is a **pack**; `SourceKey` is the native key. Derive the native name through the source pack's match function; never print a key/hash in place of the native name or reinterpret `AdoptedFrom`.
- `OnceBroker.Header` currently refreshes and writes back; daemon creates a fresh broker for each request. A helper must not inherit that broker's reset backoff/refusal memory or own token redemption.
- `runOnce` currently only calls `Pass` through sources. A no-source live record still needs host-owned refresh work; separate record maintenance from source reconciliation, without treating no source as sign-out.
- Lua `mcp_one_sign_in` currently has only `header_command`, is controlled by one global route bool, and sees `sign_in` before it is stripped. Replace the eligibility decision with frozen per-agent facts, retaining pi's default command bytes.
- Whole-plugin manifest is not byte-identical: ownership marks and namespace rename are intentional. Protect `.mcp.json` bytes and the manifest's `mcpServers` **value**, not a hash of the whole manifest.
- The bundle reader cannot use `pluginpack`'s lenient disclosure parser as identity proof; reject unsupported identity fields, duplicates and symlinks explicitly. An `os.Root` alone follows in-root links: use an O_NOFOLLOW component walk, including final leaf.
- Pack namespaces, names, filters and composition order are real identity inputs. `plugin:slack:slack` is an example; a namespaced delivered manifest yields a different native name.
- Skills are staged on the host before assembly; surface rendering runs in jail. Capture/read decisions must flow through a frozen launch input, not a post-boot read of mutable host config or credentials.
- Attach reads the running `.live` pack tree and existing owner facts; it never takes a callback lease or restages to match newly selected packs.
- A configured publication of P takes precedence; no callback lease is taken. A probe that finds P free is not proof it stays free: classify and recover from the runtime's bind failure too.
- Retrying all of `Run` after callback bind failure repeats pack staging/daemons/installer capture. `startJailMain` proves process spawn, **not container creation**: podman's bind error arrives on drained stderr and early client exit through `keeper.beforeReady`. Optional-route fallback must cover that path before ordinary teardown, while preserving lock/lifeline/signal handling; handling only `exec.Cmd.Start` errors misses the actual defect.
- Readiness opt-out does **not** stop `autoCaptureInstallerPrograms`. Fixture materialization stubs must explicitly make that path inert; emitted probes use both environment off-switches.

## Finite MS-D14 source seam and future serial integration

**Baseline read, 2026-11-17 at the stamped HEAD:**

- [`packload.EmbeddedFS`](../../internal/packload/embedded.go#L79-L84) returns the registered immutable `fs.FS` without materialization. `SetEmbeddedFS` registration also creates nothing. `packload.Embedded()` instead loads/materializes and leases a tree: it is forbidden on the status/advice path.
- [`EmbeddedProjectSkillDirs`](../../internal/packload/projectskilldirs.go) is the existing pure enumeration/manifest-read pattern, not an advice or identity reader. Reuse `EmbeddedNamesFromFS` and the same TOML sign-in declaration parser added in Task 1. New capability fields remain TOML only; the pattern's legacy parser is not permission to extend `pack.json`.
- [`loadPackEntries`](../../internal/config/packs.go#L286-L329) reads user scope and lowers source handles, then appends default/local entries; [`PackEntry.Embedded`](../../internal/config/packs.go#L212-L215) recognizes both `IsEmbedded` and `EmbeddedName`. [`SelectedPackDeclarationsDetailedWithStore`](../../internal/config/selectedpacks.go#L99-L135) resolves selected embedded declarations as FS-backed views. These facts do not make full pack resolution a status adapter: custom declaration resolution can read custom trees, and process resolution can materialize.
- Advice takes the already-composed user config's source mapping and this launch's selected/effective pack facts as inputs. Only after diagnosis is exactly `no sign-in point` may its adapter enumerate embedded names or lower mappings against those names. Match shadow exclusions by authored name; match selected shipped packs also by canonical `EmbeddedName` (including `yolo/<name>` aliases). Never resolve/read an unselected custom tree to establish a shadow. If the mapping or selected agent-format fact cannot be established without an effect, exclude that candidate; status does not repair or stage it.
- Reuse Task 1's format reader over an FS-backed original bundle view, but project only URL, transport and public client id. Do not derive a native name or feed advice into resolution, delivery, capture or callback input. This makes A32's zero embedded-read count on every other verdict enforceable at the adapter, not just inside a pure filter.

**Future serial integration seam, not a dependency:** the separate [scoped TOML pack-import plan](toml-scoped-pack-imports-plan.md) owns its candidate and [effective skill-only declaration](toml-scoped-pack-imports.md#effective-declaration-and-supporting-data). Do not touch, copy, cherry-pick or build on that incomplete candidate. At this baseline no such projection is integrated. MCP can begin in its own isolated candidate now. Once both coherent candidates are accepted, the integrating writer must consume the effective selected declaration before closure/format eligibility and inventory; a skill-only projection contributes no native plugin/sign-in bundle, agent capability, callback request or operation authority. Retained supporting plugin bytes or a selected directory are not native bundle/agent loading proof. Add a serial integration regression through the real producer and status: projected skill-only copies of an agent or integration pack yield no sign-in point, format capability, lease or publication, and never become advice simply because source bytes still contain an omitted declaration. Full unprojected user-selected packs retain Tasks 1–3 behavior. Preserve no-access defaults: selecting or scoping any pack never grants operation authority.

## Identity and version boundaries — do not reopen

| Boundary | Required treatment |
| :--- | :--- |
| Existing canonical `version=1` / `storeVersion=1` | Preserve bytes/schema semantics; no migration, live-record rewrite or new identity. Add sidecar outcome schema separately. |
| `Share.Version=1`, optional `Home`, legacy empty Home | Preserve legacy `home` mapping and replica semantics. New once expected-client/inspection facts are additive; absent expected client means baseline matching. Validate new present fields; unknown/skipped declaration means no new capability. |
| `OnceServer` | Add `ExpectedClientID` serialized as `expected_client_id`; carry transport for URL+transport bundle matching (default `http`). Do not derive expected ID from a discovered/native client. |
| Sign-in identity digest | Remains SHA-256 canonical JSON `{url,transport,headers,client_id}`. Do not add bundle name, pin or callback lease. Own client already changes the digest. |
| Native identity | Delivered manifest name + server key, via pack pure function. Managed/unmanaged inventory does not promote bundle servers into scope tables or bless Codex origins. |
| Outcome / lease | Explicit independent schema versions; unknown version reported unreadable/unavailable, never repaired by status. Notes keyed by approved identity; lease per user+port carries launch/runtime identity, not a token. |
| `yolo.mcp-auth.status/1` | Preserve existing `agents` and `once` envelope for framework clients; additive typed layer rows/verdict/next-step can remain version 1. Do not remove old fields silently; incompatible changes require reporting before implementation. |
| Header command | Keep `header SERVER` and pi command exactly bare by default. Planner spelling for explicit JSON form: `header SERVER --format json`; accept only `bare`/`json`. New framed optional `format`; missing means bare. |
| New pack vocabulary / Lua API | Host strict validation and jail tolerant skip remain existing policies. An older jail cannot be claimed capable of a new helper just because unknown API is tolerated; missing decision facts fall back to baseline plain. Source-skew rules remain intact. |
| Withdrawal | Next launch recomposes; running launches remain frozen. Pack removal removes point/route, not adopted record. Turning off once preserves retained-unused record; no new revoke/logout path. |

## Tasks

Every task is **after the preceding task**, one coherent commit, with no parallel source work. Each begins by checking current HEAD/status against the hand-off. Each runs `just format` before its targeted greens. Record predicted red, actual red, green and deletion-control evidence under durable scratch. Restore deletion mutations before formatting/commit; deletion controls run only in the implementer's authorized candidate, never this read-only primary.

Tests shown are complete regression bodies (imports are the ordinary package imports). Where a new test fixture is named, its contract is specified in the task; it must wrap existing production callers, not construct the expected answer. Add the fixture before the red run. Compile errors due to missing implementation are not the required red; regressions must execute and fail their behavioral assertions.

### Task 1 — declaration and confined inventory reader

**Settled — do not reopen:** one `[[mcp_sign_in_bundle]]` per bundle; required clean pack-relative bundle/license, `format="claude-plugin"`, HTTPS origin, 40-hex revision; callback false by default. Agent pack declares formats it loads and a pure delivered-name/server-key→native-name function. Reader accepts literal HTTPS remote HTTP/SSE only, with optional public client ID and callback 1024–65535; unsupported identity shape is never a point. Command servers ignored. Duplicate key across sources is ambiguous. Referenced files never escape/follow links. Inventory never enters `mcp_servers`.

**Preconditions:** clean fenced paths; baseline `DecodeTOML` is strict. First add this test in `internal/packdecl/mcpsigninbundle_test.go`:

```go
func TestManagedMCPSignInBundleThroughManifestDecode(t *testing.T) {
    manifest, problems := DecodeTOML([]byte(`name = "fixture"
[[mcp_sign_in_bundle]]
bundle = "skills/fixture"
format = "claude-plugin"
origin = "https://example.invalid/fixture-plugin"
revision = "0123456789abcdef0123456789abcdef01234567"
license = "skills/fixture/LICENSE"
`))
    if len(problems) != 0 || manifest == nil {
        t.Fatalf("accepted sign-in declaration refused: %v", problems)
    }
    encoded, err := EncodeTOML(manifest)
    if err != nil { t.Fatal(err) }
    if !strings.Contains(string(encoded), "[[mcp_sign_in_bundle]]") {
        t.Fatalf("sign-in declaration lost on encode: %s", encoded)
    }
    _, again := DecodeTOML(encoded)
    if len(again) != 0 { t.Fatalf("round-trip refused: %v", again) }
}
```

**Red:** `go test -short ./internal/packdecl -run '^TestManagedMCPSignInBundleThroughManifestDecode$' -count=1`; expected unknown contribution/table rejection, not compiler failure.

**Change:** packdecl TOML grouped decoder/encoder, `Manifest`/contribution metadata and kind footprint; packload path/license validation; new `internal/mcpsignin` reader. Advice: use a distinct kind `mcp-sign-in-bundle` matching grouped spelling and census, because existing grouped tables already carry provenance and skew handling. Keep agent capabilities at manifest level, not generic `client_id` on MCPAuth. Default internal names: `MCPSignInFormats`, `MCPHeaderForm`, `MCPSignInPaste`; pure `MCPNativeName` Lua bridge mirroring `MCPAuthMatch` registration shape. New authored declarations are TOML only: do not add legacy JSONC/pack.json feature spellings. Preserve existing legacy reads and test their non-extension separately from new TOML round trips.

**Additional permanent tests:** native remote `type` maps to inventory transport (the pinned Slack tree actually uses `type`, not config's `transport`); reader table has exact root direct server map, root `mcpServers` wrapper, inline manifest `mcpServers`, relative manifest-file reference, same key in root+manifest, headers, `${VAR}` URL/client, unknown identity field, invalid port, omitted transport default http, stdio, symlink at root/intermediate/leaf, `../escape.json`, malformed manifest, and 2×matching server keys. Only the accepted shapes yield inventory; each refusal retains its reason. Test duplicate keys within JSON without relying on map last-wins. Preserve raw root bytes and raw manifest MCP value for delivery guard.

**Gates:** `go test -short ./internal/packdecl ./internal/packload ./internal/mcpsignin ./internal/agentcfg/luahook ./internal/setupcensus`; update new-kind census and guide together. Config check fixture through CLI must reject unknown format/missing license, not simply a helper validation test. Deletion control: remove decoder table mapping, packload validation caller, Lua name bridge call separately; each caller regression fails.

**Fence:** declaration/inventory and their tests/census only; no live-state/readout/render behavior, no actual vendor fetch in tests.

**Commit:** `feat(mcp): declare and inventory managed native sign-in bundles`.

**Report:** executed red, new schema spellings, full reader matrix and callsite deletion results.

### Task 2 — synthetic user-supplied whole-plugin placement

**Scope:** [MS-R6](managed-mcp-sign-in.md#decision-ledger) withdraws the unauthorized
provider bundling task. Yolo ships no provider-specific MCP sign-in bundle, client identity
or server recipe. A user may explicitly supply and select their own integration; no existing
user integration or credential is removed or rewritten.

**Tests:** `TestEmbeddedPacksContainNoSlackIntegration` reads the real embedded catalog and
original files, including hidden plugin metadata. It must fail against the unauthorized tree.
`TestUserSuppliedMCPBundleIsAPluginNotAServerOrInstaller` uses local synthetic content and
keeps whole-plugin recognition, license retention and no-server/no-installer coverage.
Production launch/boot and integration fixtures use a synthetic plugin, URL and client ID,
not a replacement shipped pack.

**Preserved delivery contract:** ownership marks and namespace rename are permitted; root
`.mcp.json` remains byte-identical and manifest `mcpServers` value-identical. Identity-changing
or foreign delivery is refused. Generic capture, refresh, sharing, callback and bundle support
remain unchanged. Placement still proves neither native loading nor provider acceptance.

**Gates:** run pack/packload tests and targeted managed sign-in, plugin delivery, composition,
advice, rendering and status tests. Runtime verification is separate and must respect the
owner's host-state safety fence.

### Task 3 — frozen scope, delivered inventory and per-launch decision facts

**Settled — do not reopen:** only user-selected pack composition and user-scope once servers can create a point or callback request. URL+transport equality, expected-client equality when set, earliest composition order tie-break with both candidates shown. Native name comes from *delivered* manifest after namespacing. Placement is `placed`, never `loaded`. Resolution and delivery are pure. Missing source does not mean missing record. Workspace cannot redirect/create/remove the user point; it can drop its launch's server.

**Tests first**, `internal/cli/run/mcpauthonce_test.go`, through current producer:

```go
func TestMCPProducerCarriesExpectedClientNotJustItsDigest(t *testing.T) {
    text := `{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once","oauth":{"client_id":"owner-client"}}}`
    in := onceLaunchInput(t, text, text)
    data, err := mcpShareFromPacks(in, []*packload.Pack{shippedPack(t, "claude")})
    if err != nil { t.Fatal(err) }
    var share struct {
        Once []struct { Expected string `json:"expected_client_id"` } `json:"once"`
    }
    if err := json.Unmarshal(data, &share); err != nil { t.Fatal(err) }
    if len(share.Once) != 1 || share.Once[0].Expected != "owner-client" {
        t.Fatalf("producer lost expected client: %s", data)
    }
}
```

**Red:** `go test -short ./internal/cli/run -run '^TestMCPProducerCarriesExpectedClientNotJustItsDigest$' -count=1`; current producer omits the field.

**Change:** extend frozenLaunchMCP with verified user selection/order and delivered placement inventory; `LaunchInputContext` carries those facts, capturability and backend/helper reachability; share once entries carry expected client/transport. Source/input producer uses the actual skills staging path (`refreshJailBriefings/prepare.go`) after placement, before runtime create/keeper input publication. Pass token-free per-agent decisions to boot/host render on a narrow launch wire. Default spelling `YOLO_MCP_SIGN_IN` with an explicit schema version; do not put tokens, host canonical paths or adopted client IDs into that environment. Public client IDs belong in inventory/status, not derive output.

Host composition applies whole plugins before resolving their delivered names. Reuse `applyHostSkills`/`composeHostInputs` rather than running an agent; use strict canonical reader for usable-record fact. Credential view, alternate storage and Mac host produce no source; `capturable=false` is frozen independently of `placed=true`. Boundary/session launchers carry the same facts without gaining an endpoint.

**New caller fixture contract:** `managedMCPRunFixture(t)` wraps `packHome`, `dispatchOptions`, fake `Exec`, stub image load and keeper spawner; stages a fixture integration pack and real Claude derive; spies on production `LaunchInputProducer.Produce` input and captured keeper plan. AutoCapture is inert and counted (must be zero); no runtime/agent executable runs. Its options vary user selection, CLI `-p`, Namespace, effective workspace override and credential mode; it returns *observed* staged manifest, production share bytes, assembly argv and frozen decision JSON, never a hand-built point.

**Full real-caller regression using that fixture** (add helper first; predicted behavioral red before wiring):

```go
func TestRunResolvesOnlyUserSelectedDeliveredSignInPoints(t *testing.T) {
    for _, tc := range []struct {
        name, namespace string
        cliOnly, changedURL, credentialView bool
        native string
        capturable bool
    }{
        {name:"bare", native:"plugin:slack:slack", capturable:true},
        {name:"namespaced", namespace:"team", native:"plugin:team-slack:slack", capturable:true},
        {name:"CLI-only", cliOnly:true},
        {name:"workspace redirect", changedURL:true},
        {name:"view", credentialView:true, native:"plugin:slack:slack"},
    } {
        t.Run(tc.name, func(t *testing.T) {
            f := managedMCPRunFixture(t)
            got := f.Run(tc.namespace, tc.cliOnly, tc.changedURL, tc.credentialView)
            if got.NativeName != tc.native || got.Capturable != tc.capturable {
                t.Fatalf("actual launch resolution: %+v", got)
            }
            if got.AutoCaptures != 0 { t.Fatal("materialization executed auto-capture") }
            if (tc.cliOnly || tc.changedURL) && (got.LeaseAttempts != 0 || got.CallbackPublished) {
                t.Fatal("workspace authority became a callback route")
            }
        })
    }
}
```

`Run` above invokes production Run once and observes it; the future callback fields are zero until Task 8. Red/green: `go test -short ./internal/cli/run -run '^(TestMCPProducerCarriesExpectedClientNotJustItsDigest|TestRunResolvesOnlyUserSelectedDeliveredSignInPoints)$' -count=1`.

**Additional cases/gates:** earliest of two matches and both named; mismatched own client; malformed/filtered/not delivered bundle; unsupported transport; non-once; removed server; pack removed retaining record; launch without broker pack; host own composition. Run `go test -short ./internal/cli/run ./internal/jailcontent ./internal/cli ./internal/entrypoint ./internal/mcpsignin`. Preserve `TestRunSnapshotsAndFreezesSelectedMCPBeforeKeeperSpawn`, `TestFrozenMCPForLaunchUsesOnlyProvenSelectedPackSources`, source-free fallback tests and all boundary/session caller tests. Delete freeze→producer and prepare→inventory assignments separately; actual Run test fails each.

**Fence:** frozen input and pure decisions; no native store adoption, no callback lease effect yet, no blanket CLI/global embedded authority.

**Commit:** `feat(mcp): freeze user-scoped managed sign-in decisions at launch`.

**Report:** actual Run snapshots, scope adversaries, native-name namespacing and capturability facts; no secret in wire.

### Task 4 — capture expected-client check and durable outcomes

**Settled — do not reopen:** expected configured client mismatch refuses before *any* discovery/redemption, writes no record. Empty expected client preserves unmanaged URL/issuer matching. Latest sign-in wins. Only broker pass writes schema-versioned token-free outcome note under record refresh lock; atomic replace; note write failure logged but cannot fail pass. Definitive observed codes only; provider text and token hashes forbidden. Source malformed/unsafe is never sign-out/rewrite. Successful rotation with failed canonical commit records `rotated-not-saved` if possible; after restart a failed note is not reconstructed as fact.

**Tests first**, actual daemon `serve → runOnce → Pass`, based on `newOnceDaemonFixture`:

```go
func TestServeRefusesExpectedClientBeforeAnyHTTP(t *testing.T) {
    f := newOnceDaemonFixture(t)
    body, err := json.Marshal(f.b.share)
    if err != nil { t.Fatal(err) }
    var wire map[string]any
    if err := json.Unmarshal(body, &wire); err != nil { t.Fatal(err) }
    wire["once"].([]any)[0].(map[string]any)["expected_client_id"] = "owner-client"
    body, _ = json.Marshal(wire)
    f.b.share, err = mcpauth.DecodeShare(body)
    if err != nil { t.Fatal(err) }
    var calls atomic.Int64
    previous := http.DefaultTransport
    http.DefaultTransport = countingMCPTransport{Next:previous, Calls:&calls}
    t.Cleanup(func() { http.DefaultTransport = previous })
    ticks := make(chan time.Time)
    passes := make(chan struct{}, 1)
    restore := setOnceSeams(ticks, func() { passes <- struct{}{} })
    defer restore()
    stop, done := make(chan struct{}), make(chan error, 1)
    // Test-only lifecycle helper mints short socket with testtmp.ShortDir and cleans it.
    socket := managedMCPSocket(t)
    go func() { done <- f.b.serve(socket, stop) }()
    <-passes
    close(stop)
    if err := <-done; err != nil { t.Fatal(err) }
    if calls.Load() != 0 { t.Fatalf("client mismatch made %d HTTP calls", calls.Load()) }
    store, err := mcpauth.NewOnceStore(filepath.Join(f.b.canonical.Dir, "once"))
    if err != nil { t.Fatal(err) }
    file, err := store.Read()
    if err != nil || len(file.Records) != 0 { t.Fatal("mismatched client was adopted") }
    reply := askBroker(t, f.b, map[string]any{"op":"status"})
    if reply.rc != 0 || !strings.Contains(reply.stdout, "refused-client-mismatch") {
        t.Fatalf("durable mismatch outcome absent: %+v", reply)
    }
}
```

Test-only `countingMCPTransport.RoundTrip` increments an atomic counter before delegating; `managedMCPSocket` returns one disposable short fixture socket. Neither supplies production data. Red: `go test -short ./internal/mcpauthdaemon -run '^TestServeRefusesExpectedClientBeforeAnyHTTP$' -count=1`; current JSON loses expected-client and makes fake issuer requests. Use a generous channel failure guard only to turn hangs red.

**Change:** shared source classifier in `source.go`, candidate/adopt client's early check in `once.go`, propagate native entry name from pack match, outcome store writer/strict reader. Closed note fields: schema version, server, identity, native name, code, time, generation. Codes: adopted, refused-issuer-mismatch, refused-client-mismatch, refused-by-server, unsupported-token-type, rotated-not-saved, deferred-network, source-malformed, source-unsafe. Absence means `none recorded`. Do not put outcomes in token-bearing Record or broaden audit event fields with arbitrary response text.

**Additional permanent failures:** two brokers/single-use fake issuer→one redemption/loser reread; newer native entry→generation+1; clean origin deletion signs out, other source absence/torn file does not; failed canonical persist after success with writable/unwritable note then restarted broker actual definitive refusal; transient discovery/token failures; DPoP rejection; source-lock writeback pending; note unknown schema; all emitted note/log/audit bytes scanned for fixture access/refresh values and their SHA-256 values. Need failure injection at existing store persistence effect, not a fake adopter bypassing production commit order.

**Gates:** `go test -short ./internal/mcpauth ./internal/mcpauthdaemon ./internal/cli/run ./internal/cli -run 'Once|MCPAuth|ManagedMCP|Outcome|Source' -count=1`; `go test -race -short ./internal/mcpauth ./internal/mcpauthdaemon -run 'Once|ExpectedClient|Outcome' -count=1`. Deletion: drop expected-client from producer separately from adopt guard, note writer separately from restart reader. Both layer tests and combined caller chain must fail.

**Fence:** no confidential clients, new adapters, account/team selection, Keychain, grant/profile/credential identity changes. Keep legacy SourceDir empty fallback and Codex native proof gate.

**Commit:** `fix(mcp): fence adopted clients and persist token-free capture outcomes`.

**Report:** zero counting-transport calls on mismatch; rotation/loss restart evidence; note lock ownership and token-free scans.

### Task 5 — bounded header issuance and daemon-owned expired-record work

**Settled — do not reopen:** 8s total command deadline from start; canonical lock ≤2s; unexpired access immediately, no provider/refresh-lock/source-writeback. Expired live in jail requests one immediate pass from launch poll owner, waits ≤5s; work commits after request disappears. One pass/record, record-lock re-read, transient bypass ≤once/30s, definitive refused tokens never retried. Host never redeems, even expired; poll handles refresh at its ordinary 30s interval. Never return expired/dead tokens. Bare remains default; JSON stdout is one string map.

**First red**, real framed handler, `internal/mcpauthdaemon/header_test.go`:

```go
func TestHeaderDoesNotRefreshAnUnexpiredToken(t *testing.T) {
    f := newOnceDaemonFixture(t)
    once, err := f.b.newOnceBroker()
    if err != nil { t.Fatal(err) }
    if err := f.b.passAll(context.Background(), once, f.b.onceSources()); err != nil { t.Fatal(err) }
    <-f.refreshs
    store, err := mcpauth.NewOnceStore(filepath.Join(f.b.canonical.Dir, "once"))
    if err != nil { t.Fatal(err) }
    if err := store.WithLock(func(file *mcpauth.CanonicalFile) error {
        file.Records[0].ExpiresAtMS = time.Now().Add(time.Minute).UnixMilli()
        return nil
    }); err != nil { t.Fatal(err) }
    reply := askBroker(t, f.b, map[string]any{"op":"header", "server":"slack"})
    if reply.rc != 0 || reply.stdout != "Bearer at-2\n" { t.Fatalf("header: %+v", reply) }
    select {
    case token := <-f.refreshs: t.Fatalf("unexpired issuance redeemed %q", token)
    default:
    }
}
```

Red: `go test -short ./internal/mcpauthdaemon -run '^TestHeaderDoesNotRefreshAnUnexpiredToken$' -count=1`; baseline sends `rt-2` to fake issuer.

**Second red**, `internal/cli/mcpauth_test.go`, preserves early Main dispatch:

```go
func TestMCPAuthJSONHeaderThroughMain(t *testing.T) {
    t.Setenv("YOLO_VERSION", "")
    previous := mcpAuthHostHeader
    t.Cleanup(func() { mcpAuthHostHeader = previous })
    mcpAuthHostHeader = func(context.Context, string, func(string)) (string, error) {
        return "fixture-access", nil
    }
    rc, out, errs := captureStd(t, func() int {
        return Main([]string{"yolo", "mcp-auth", "header", "slack", "--format", "json"})
    })
    if rc != 0 || out != "{\"Authorization\":\"Bearer fixture-access\"}\n" || errs != "" {
        t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out, errs)
    }
}
```

Red: `go test -short ./internal/cli -run '^TestMCPAuthJSONHeaderThroughMain$' -count=1`; current usage rc=2.

**Change:** split strict record read/issue from refresh; daemon poll owner holds persistent OnceBroker state and request queue. `broker.serve` owns startup/shutdown; `runOnce` accepts immediate per-record work as well as ticks and can maintain records with no local Sources. Poll-owned worker context is not requester context; durable commit finishes before shutdown final harvest. Reuse TokenClient timeout as outer worker failure guard, not helper timeout. Short-lived host header uses strict descriptor/read only; no launcherBroker/audit initialization accidentally starts refresh. Header audit remains token-free and records issuance only. This independently shippable commit uses existing `HeaderRefusal` with token-free busy/expired/dead next steps; Task 6 then replaces wording through the shared diagnosis, without a temporary compatibility flag.

**Permanent event-driven budgets:** hold fake provider RoundTrip on a channel; valid host/jail header must finish via completion channel with zero provider calls even while refresh lock is held. Inject deadlines/clock advancement to prove 8s total includes dial/framing, 2s read-lock busy and 5s request wait. Expired jail: advance waiter to timeout, release provider, observe canonical commit/generation then next header success. Two callers coalesce; two launch brokers serialize under same refresh lock. Transient bypass at 0/29s/30s; dead/refused never sent. Expired host counts zero calls. No sleeps or real elapsed-time pass conditions.

**Gates:** `go test -short ./internal/cli ./internal/mcpauth ./internal/mcpauthdaemon -run 'Header|Once|ManagedMCP' -count=1`; race gate on daemon concurrent expired issuance. Rewrite `once_header_test.go`'s old margin-redemption expectations to new behavior, not relax tests to restore old refresh. Delete CLI form dispatch, daemon poll wake wiring and caller-independent commit ownership separately; full caller tests fail.

**Fence:** no request-scoped OAuth redemption, no new poll/process/binary, no timeout changes to ordinary source maintenance except requester budgets. Do not return expired token to meet deadline.

**Commit:** `fix(mcp): issue bounded headers through the durable poll owner`.

**Report:** exact stdout forms; zero-call valid/host cases; injected-budget and post-cancellation commit events.

### Task 6 — zero-effect status and shared diagnosis/recovery

**Settled — do not reopen:** host reads composed user scope, already-staged declarations/delivered bundles, strict canonical read, unlocked source snapshot, note/lease plain reads. No provider/egress, staging/resolving writes, browser/agent/poll, directory/file/lock creation, repair, agent-store lock or header issuance. Jail authenticates only to its own endpoint. No endpoint→nonzero with host-status next step. Readable unhealthy status returns 0, not health-gate rc. No token, prefix or token hash anywhere.

**First red**, `internal/mcpauthdaemon/status_test.go`:

```go
func TestStatusHandlerCreatesNoOnceState(t *testing.T) {
    f := newOnceDaemonFixture(t)
    target := filepath.Join(f.b.canonical.Dir, "once")
    if _, err := os.Lstat(target); !os.IsNotExist(err) { t.Fatalf("fixture already has once state: %v", err) }
    reply := askBroker(t, f.b, map[string]any{"op":"status"})
    if reply.rc != 0 { t.Fatalf("readable missing state: %+v", reply) }
    if _, err := os.Lstat(target); !os.IsNotExist(err) {
        t.Fatalf("status created state: %v", err)
    }
}

func TestStatusHandlerIncludesHeaderOnlyServers(t *testing.T) {
    f := newOnceDaemonFixture(t)
    f.b.share.Sources = nil
    reply := askBroker(t, f.b, map[string]any{"op":"status"})
    if reply.rc != 0 { t.Fatalf("status: %+v", reply) }
    var body struct { Once []map[string]any `json:"once"` }
    if err := json.Unmarshal([]byte(reply.stdout), &body); err != nil { t.Fatal(err) }
    if len(body.Once) != 1 || body.Once[0]["name"] != "slack" {
        t.Fatalf("header-only launch invisible: %s", reply.stdout)
    }
}
```

Red: `go test -short ./internal/mcpauthdaemon -run '^TestStatusHandler(CreatesNoOnceState|IncludesHeaderOnlyServers)$' -count=1`; baseline creates once directory / emits empty rows.

**CLI red**, `internal/cli/mcpauth_test.go`:

```go
func TestMCPAuthStatusThroughMainHasNoEffectsOnMissingState(t *testing.T) {
    home := t.TempDir()
    t.Setenv("HOME", home)
    t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
    t.Setenv("XDG_DATA_HOME", "")
    t.Setenv("XDG_STATE_HOME", "")
    t.Setenv("YOLO_USER_LAYER", "")
    t.Setenv("YOLO_VERSION", "")
    writeUserConfig(t, `{"packs":[],"mcp_servers":{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once"}}}`)
    before := managedMCPTreeSnapshot(t, home)
    rc, out, errs := captureStd(t, func() int {
        return Main([]string{"yolo", "mcp-auth", "status", "slack", "--json"})
    })
    if rc != 0 || !json.Valid([]byte(out)) { t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out, errs) }
    if !reflect.DeepEqual(before, managedMCPTreeSnapshot(t, home)) { t.Fatal("status changed fixture state") }
    if strings.Contains(out+errs, "fixture-access") || strings.Contains(out+errs, "fixture-refresh") {
        t.Fatal("status leaked token fixture")
    }
}
```

Snapshot fixture walks only its explicit root and records path/type/mode/content; it never reads default HOME. Add independent effect spies (filesystem writes, pack materializers, transport, poll starter, source lock, header seam) and fail on any call. Red: `go test -short ./internal/cli -run '^TestMCPAuthStatusThroughMainHasNoEffectsOnMissingState$' -count=1`; baseline rc=2.

**Change:** literal safe store descriptions, shared typed status layers + pure closed diagnosis, CLI parsing/read adapter, daemon endpoint projection with full rows. Rows include configuration/provenance/client preference, each point and candidate reason, callback, source classification/capturability, outcome, canonical freshness/native origin, refresher, delivery and writeback current/pending. Retained configuration-orphan records are `retained, unused`. Existing stored note or unsafe/malformed state reported, never repaired. Public IDs/paths/expiry/generation allowed. Canonical lock existing only, ≤2s→busy. Host packs not staged→`not determinable without effect`; next step launch once or status inside jail.

**Diagnosis literals:** `ready`, `not one-sign-in here`, `no sign-in point`, `sign-in needed`, `source unreadable`, `awaiting adoption`, `adoption refused`, `record dead`, `record expired`, `delivered plain`, `broker unreachable`, `not capturable here`, `sign-in lost`, `busy`. Callback never changes verdict. Use one diagnosis in status, launch block and helper stderr. Native authenticate name is resolved, not configured alias. Under `no sign-in point` only, attach MS-D14 [provisioning candidates](../glossary.md#provisioning-candidate) through the [pure embedded-FS seam](#finite-ms-d14-source-seam-and-future-serial-integration); one yields `yolo pack add <pack>, then relaunch`, several list every command in pack-name order and say any one serves, none uses the generic URL/own-client line. Every form includes `yolo config set mcp_servers.<server>.oauth.client_id <id> --user` as the alternative. No Slack/server/agent switch or new advice declaration field. Nonowner/unpublished/IPv4-only row selects full-callback paste next step; noncapturable row names mode and bridged-managed sign-in. Dead header-rendered source without point says restart agent to restore plain native entry. `delivered plain` explains duplicate needs no extra sign-in.

**MS-D14 tests first (A30–A32):** extend the existing `TestMCPAuthStatusThroughMainHasNoEffectsOnMissingState` fixture with registered `fstest.MapFS` originals and selected agent-format declarations. Use non-Slack names `fixture-provision` / `fixture-server`, URL `https://mcp.fixture.invalid/mcp`, transport `http`, public client `fixture-client`; candidate origin `https://example.invalid/fixture`, revision `0123456789abcdef0123456789abcdef01234567`. The real Main/status call must yield one `candidate`, origin/revision, and `yolo pack add fixture-provision, then relaunch`, with no point/native name, lease/publication, source poll, render mutation, write or materializer call. Before advice wiring, this assertion is the behavioral red: `go test -short ./internal/cli -run '^TestMCPAuthStatusProvisioningAdviceThroughMain$' -count=1`. Add the paired `op=status` caller test `TestStatusHandlerProvisioningAdviceHasNoEffects`, using the same observed snapshot/counters. Neither fixture directly constructs a candidate result.

**Full advice matrix:** two matching packs (`a-provision`, `z-provision`) list both commands sorted, choose neither; zero gives `none derivable` and the exact generic line from [recovery](managed-mcp-sign-in.md#8-recovery-lines), exit 0. Independently vary URL, transport, expected client, selected format, selected pack itself, custom source shadow, unreadable mapping, malformed declaration/bundle, ambiguous key, absent registered FS and inaccessible staged capability. Each exclusion yields no candidate, not an error. An unselected custom pack on disk is never read/named. For every verdict other than `no sign-in point`, a read-counting embedded FS observes zero reads. Advice failures never block a selected supported path. Delete diagnosis→advice caller wiring and source-shadow exclusion separately; caller/matrix tests must fail. Reads use only the original embedded bundle, with no `Embedded()` invocation or materialization/lease cache change.

**Permanent tests:** every verdict through pure function **and Main wording**, missing/unsafe/healthy modes and source parse; source lock held has no effect; canonical lock held→busy on injected deadline; note survives restart; notes unreadable and unsupported schema; all fixture secret bytes and hashes absent in text/JSON/logs/audit; filtered pack/embedded cache absent causes no materialization; jail no endpoint never falls back host; no-source launch still record/delivery/refresher row. Rewrite baseline Main's explicit status-rejection assertion deliberately.

**Gates:** `go test -short ./internal/cli ./internal/mcpauth ./internal/mcpauthdaemon -run 'Status|Diagnosis|MCPAuth|Outcome' -count=1`; existing `TestMCPAuthStatusRequestNeverRepairsCanonicalPermissions` remains. Delete CLI early dispatch, status strict-descriptor caller and once-row inclusion separately; real caller tests fail. Check mutation counters at dispatch, not only inside pure reader.

**Fence:** status no live health probe, self-check, header, constructor, pack fetch/checkout/materialization; no secrets in refusal error wrapping or debug logs. MS-D14 adds only advisory original-tree reads under `no sign-in point`: no declaration field, client rendering, scope, selection, lease, capture or delivery effect; never an unselected user/custom-pack read.

**Commit:** `feat(mcp): expose zero-effect status and native recovery`.

**Report:** full zero-effect snapshots/counters; all layer/verdict cases; token-free text/JSON/restart evidence.

### Task 7 — Claude JSON header route through apply and boot

**Settled — do not reopen:** nonsource supported+reachable consumers always header. Source uses header iff capturable managed point **or usable record** exists at launch. Otherwise plain native sign-in. Unsupported/unreachable uses baseline plain with reason. Remove every case variant of Authorization; preserve other headers; no OAuth block for header route; vendor plugin untouched. Public/adopted client ID is never borrowed/rendered into another agent. Mid-session adoption does not re-render; native source continues writeback, consumers reconnect normally. Codex unchanged.

**Tests first**, `internal/entrypoint/managedmcpsignin_test.go`: Task 3 fixture extension `managedMCPSurfaceEnv(t, native, capturable, usable, reachable)` creates `siblingEnv`, freezes Task 3's token-free wire facts through production wire decoder, and selects real shipped Claude pack. It does not populate Lua context directly.

```go
func TestClaudeManagedMCPRouteThroughConfigurePack(t *testing.T) {
    for _, tc := range []struct {
        name string
        native, capturable, usable, reachable, header bool
    }{
        {"managed capturable", true,true,false,true,true},
        {"plain bootstrap", false,false,false,true,false},
        {"record without bundle", false,false,true,true,true},
        {"credential view", true,false,false,true,false},
        {"unreachable", true,true,true,false,false},
    } {
        t.Run(tc.name, func(t *testing.T) {
            e := managedMCPSurfaceEnv(t, tc.native, tc.capturable, tc.usable, tc.reachable)
            e.Vars["YOLO_MCP_SERVERS"] = `{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once","headers":{"aUtHoRiZaTiOn":"stale","X-Team":"t1"}}}`
            if err := ConfigurePackByName(e, "claude"); err != nil { t.Fatal(err) }
            config := decodeJSONFile(t, filepath.Join(e.Home, ".claude.json"))
            slack := config["mcpServers"].(map[string]any)["slack"].(map[string]any)
            _, helper := slack["headersHelper"]
            if helper != tc.header { t.Fatalf("wrong route: %#v", slack) }
            if tc.header {
                if slack["headersHelper"] != "yolo mcp-auth header slack --format json" { t.Fatal("wrong helper form") }
                if _, ok := slack["oauth"]; ok { t.Fatal("helper route started own OAuth") }
                h := slack["headers"].(map[string]any)
                if h["X-Team"] != "t1" { t.Fatal("lost nonauth header") }
                for k := range h { if strings.EqualFold(k, "Authorization") { t.Fatal("stale Authorization survived") } }
            }
        })
    }
}
```

Red: `go test -short ./internal/entrypoint -run '^TestClaudeManagedMCPRouteThroughConfigurePack$' -count=1`; baseline Claude always plain.

**Change:** pack declares JSON header support and native-name function/paste capability; pi declares bare; derive context supplies `header_command` and `header_json_command` only for decision-approved servers. Claude derive alone writes `headersHelper`; core knows capabilities, not Claude. Existing pi derive remains bare-byte compatible. Hook real host apply/launch decision producer, boot decoder and `deriveCtx` call, not a test-only context seam. Host apply respects existing management/first-apply protections.

**Additional caller regressions:** `applyHost` over `hostMCPFixtureUnder`/inert declared bins plus synthetic record and delivered namespaced bundle; emitted `.claude.json` exact helper form, no tokens/client borrowing; plugin `.mcp.json` untouched; both record-freshness and source capturability change the route. Apple Container and podman-Mac Run fixtures feed unreachable fact→plain/status explanation; Linux host session and macos-user boundary remain existing behavior. Integration Task 9 tests launch→freeze→boot→derive, so deleting input production also fails, not only deleting `MCPHeaderRoute` field.

**Gates:** `go test -short ./internal/entrypoint ./internal/agentcfg/luahook ./internal/cli ./internal/cli/run -run 'MCP|ManagedMCP|ApplyHost' -count=1`; baseline remote dialect and pi header tests remain. Delete Claude derive helper branch, `deriveCtx` wiring and host/launcher producer assignment separately; at least one actual apply/boot caller fails each.

**Fence:** no Codex helper or origin repair; no plugin rewrite; no agent/native sign-in test; no native success claim. Agent reconnect/OAuth semantics remain native measurement owed.

**Commit:** `feat(claude): derive the managed MCP shared-header route`.

**Report:** apply/boot caller output with synthetic fixtures only; supported/plain/capturability matrix; pi byte-compatibility.

### Task 8 — optional exclusive callback lease, publication and safe fallback

**Settled — do not reopen:** authority requires user-selected declared bundle callback=true + own port + managed match to user-scope once server. Linux bridge podman only, TCP `127.0.0.1:P:P`; no all-interface/IPv6 route. Per-user/per-port serial lease before runtime create. Second launch starts without route and names owner. User publication P means no lease/extra route. Foreign busy fails only convenience, not launch. Attach never acquires/moves lease. Release/reclaim only after owner runtime confirmed gone; unknown retains. Host/macos-user `not needed`; other backends `not published on this backend`; no route still ordinary paste sign-in.

**Preconditions:** Task 3's actual delivered point reaches callback planner. Keep pure assembly separate from acquiring/writing lease and actual runtime create.

**Test fixture contract:** `callbackMCPLaunchFixture(t)` extends production Run fixture from Task 3 and current keeper tests. The test's fake runtime create records actual argv and reports controlled bind-busy/other failure; a controlled liveness table represents owner-live/confirmed-gone/unknown; temporary private lease root injected at the effect boundary. A lease observer counts attempted acquire/release. Do not return a fabricated callback disposition; status is read through production status reader. Attach uses actual attach branch with existing running-tree/keeper fixture.

**Full red-first caller regression**, `internal/cli/run/mcpcallback_test.go`:

```go
func TestManagedMCPCallbackIsOptionalExclusiveAndLoopback(t *testing.T) {
    f := callbackMCPLaunchFixture(t)
    first := f.Start("first", "podman", "bridge", true, false)
    if first.RC != 0 || !first.HasArgPair("-p", "127.0.0.1:3118:3118/tcp") {
        t.Fatalf("owner publication missing: %+v", first)
    }
    if !first.PublishedPort("3118/tcp") { t.Fatal("published callback omitted jail DNAT input") }
    second := f.Start("second", "podman", "bridge", true, false)
    if second.RC != 0 || second.HasCallbackPublication() || !strings.Contains(f.Status("second"), "held by first") {
        t.Fatalf("second launch stole callback: %+v", second)
    }
    before := f.LeaseSnapshot()
    if rc := f.Attach("first"); rc != 0 { t.Fatalf("attach rc=%d", rc) }
    if !reflect.DeepEqual(before, f.LeaseSnapshot()) { t.Fatal("attach moved lease") }
    f.SetLiveness("first", "unknown")
    f.Forget("first") // invokes actual forgetGoneContainer
    if f.LeaseOwner(3118) != "first" { t.Fatal("unknown liveness released lease") }
    f.SetLiveness("first", "gone")
    f.Forget("first")
    if f.LeaseOwner(3118) != "" { t.Fatal("confirmed-gone owner retained lease") }
    f.SetForeignBusy(3118, true)
    third := f.Start("third", "podman", "bridge", true, false)
    if third.RC != 0 || third.HasCallbackPublication() || f.LeaseOwner(3118) != "" {
        t.Fatalf("foreign holder failed launch or leaked lease: %+v", third)
    }
    if !strings.Contains(f.Status("third"), "port busy") { t.Fatal("busy fallback invisible") }
}
```

Red: `go test -short ./internal/cli/run -run '^TestManagedMCPCallbackIsOptionalExclusiveAndLoopback$' -count=1`; baseline no publication/lease. Fixture helpers are observations/actions on Run/keeper/cleanup, not a second lease algorithm.

**Change:** pure callback request/status plan; serialized private lease operations; planned optional publications passed on `assembleInput` and `keeperPlan`. Combine declared network publications and callback ones for `route_localnet` and `YOLO_PUBLISHED_PORTS`, without modifying config/network. Keep callbacks disjoint from `forward_host_ports` (opposite direction). Keeper create fallback removes only optional callback specs and their DNAT input and releases their leases, then starts the same usable launch without reentering Run. Wire both the synchronous `startJailMain` error and, crucially, drained early runtime-client exit in `keeper.beforeReady`; `exec.Cmd.Start` success is not a publication receipt. Capture a bounded token-free startup error/disposition alongside existing relay output for classification, retain its failure receipt, and only retry when optional-port conflict plus runtime absence are established. Reset only the failed jail-main/running-observation attempt, not host services, private input, source poll or capture/staging. A genuinely foreign bind failure can trigger fallback; unrelated image/mount/runtime failure cannot become successful. If failure left a partial runtime, confirm its absence before cleanup/recreate; unknown never just delete ownership. Dry-run never takes lease.

Lease stores schema, port, launch/runtime identity under user-private state; keeper retains it and `forgetGoneContainer` checks identity before releasing. Compare-and-delete prevents an old teardown deleting new owner lease. No current-config restaging on attach. Status plain-reads lease and launch disposition, never tests liveness or binds a port. `setupPublishedPortLocalnet` failure warning includes full-callback paste; owner means lease+publication, not redirect success.

**Permanent cases:** callback=false/no port/no point/CLI-only/workspace redirected→no acquire; user P publication→no acquire/extra bind, `port busy` convenience row per acceptance scenario; simultaneous launches serialized; crash before create/confirmed no runtime→release; foreign process wins race after successful preflight→runtime fallback using a fake process that starts successfully then writes bind-conflict stderr and exits 125 before BootReadyLine; multiple optional callback ports remove affected publication without dropping declared ports; unrelated create failure still fails; failed bind doesn't rerun capture/materialization; dead-owner reclaim, unknown retain; old-generation teardown doesn't release replacement; all backend/network branches; dry-run/attach effect counters; default callback false. Entry-point fake iptables observes exact tcp DNAT-to-127.0.0.1 rule only for owner input.

**Gates:** `go test -short ./internal/cli/run ./internal/entrypoint -run 'Callback|PublishedPort|PackTree|Tracking|Keeper|ManagedMCP' -count=1`; race test concurrent acquire/release. Existing `TestAPackTreeStaysUnlessItsContainerIsKnownGone`, `TestTrackingStaysUnlessTheContainerIsKnownGone` and keeper teardown tests stay. Delete acquire→assembly threading, `YOLO_PUBLISHED_PORTS` callback merge, create fallback and confirmed-gone release separately; caller tests fail. No nested green accepted as reachability/rootless proof.

**Fence:** no host gateway, new backend support, changed vendor port, widened interfaces, moving owner's route, agent listener mediation or source support. Callback cannot become a launch refusal.

**Commit:** `feat(mcp): lease optional native callback publications per launch`.

**Report:** actual fake-runtime argv/DNAT, simultaneous/foreign/unknown-liveness/attach results; no all-interface bind; no rootless reachability claim.

### Task 9 — integration, authority corrections and combined synthetic gate

**Settled — do not reopen:** all accepted scope above ships; synthetic gate precedes landing, native measurement afterward. Failed Claude helper measurement withdraws pack header declaration, restoring baseline next launch. No provider success implied by account-free green.

**Tests first:** account-free `integration/managed_mcp_sign_in_test.go` joins package `integration`, `requireJail(t)`, no parallelism. Use `mcpAuthHome`/`withPrivateFixtureYoloStore` to preserve harness's runtime/mise/nix links while isolating yolo state. Use immutable local fixture pack to exercise namespacing and MIT-original pack to exercise embedding. The probe starts only shell/Go file readers, never an agent/hook/plugin validator.

Add `TestManagedMCPSignInLaunchPlacesAndDerivesWithoutExecutingAgents`: user selects synthetic Claude/pi consumer adapters and a local `fixture-mcp` integration, with a user-scope once server at `https://mcp.fixture.invalid/mcp`. Actual launch renders both consumer forms; source-refresh absent record makes Claude header through capturable point; assert synthetic `.mcp.json` byte equality, delivered manifest native name, status verdict/native-name/paste route, no token in files/readout. No native entry is written to a real store. Add `TestManagedMCPSignInHeaderOnlyStatusReadsSyntheticRecord`: seed fixture canonical live access-only response, launch pi+mcp-auth without source, run status and header, compare helper result privately without printing token; no refresh request. Add workspace URL/client override and withdrawal relaunch cases. Compile test file first and run red before each wiring repair; status/output/placement assertions must execute, not a missing-symbol red.

Add `TestManagedMCPCallbackLaunchPlanRetiresLease`: fake runtime probe in unit suite proves exact create path and busy fallback; real integration checks frozen callback disposition, confirmed-gone retirement and no residual private input. In a nested jail it asserts the documented *no-publication* branch, never the real bridge owner branch. Actual browser callback witness remains separate.

**Reached existing integration tests, run these after changing the launcher/shared producer:**

```console
go test -count=1 -timeout 0 ./integration -run '^(TestMCPAuthBrokerSeedsButDoesNotAdoptUnprovedCodexSignIn|TestMCPAuthUserOffNeverStartsProducerOrDaemon|TestMCPAuthFinalHarvestLockFailureFailsSelectedLaunch|TestMCPAuthAttachRefusesMissingKeeperRecordWithoutLog|TestARootModReachesARealJail|TestManagedMCPSignInLaunchPlacesAndDerivesWithoutExecutingAgents|TestManagedMCPSignInHeaderOnlyStatusReadsSyntheticRecord|TestManagedMCPCallbackLaunchPlanRetiresLease)$'
```

The integration harness builds a fresh CLI; it does not prove installed host/source compatibility. Preserve all off-switch and final-harvest outcomes rather than overwriting them with callback success. Add both `YOLO_NO_PROGRAM_READINESS=1` and `YOLO_NO_AUTO_CAPTURE=1` to emitted-launcher and relevant integration fixture env (the suite's readiness opt-out alone is insufficient). Stub native materialization callback and assert auto-capture call count zero before launching any new fixture.

**Authority corrections at this HEAD:** unlike the older grounding packet, sharing introduction, bundle placement disclaimer, status pointers, withdrawn login section and `packs/mcp-auth/README.md` have already been corrected. Do not reintroduce old text or redo already-made pointer changes. Still correct overlapping body claims when implementation changes them:

| Authority | Required closure |
| :--- | :--- |
| [Managed design](managed-mcp-sign-in.md), this completed companion, `docs/plans/roadmap.md` | Built rows per completed implementation task say **synthetic gate only**; retain native verification next step. Publication already integrated MS-D14 and the completed hand-off; implementation updates task evidence and source metadata, no duplicated state table. |
| [Sharing design](mcp-auth-sharing.md) | Reconcile remaining body claims of login/logout/revocation/native support, source plain entry choice and issuance margin with managed pointers. Update MA-D27 Built cell for pi and new synthetic Claude route; do not claim broader replica/watch/adapter slices built. |
| [Sharing versions / delivery](mcp-auth-sharing.md#96-delivery-routes) | Codex 0.166.0 `http_headers_helper` versus 0.145.0 absence remains **unmeasured native version difference**, not permission to enable Codex. Mark measurement owed; preserve native-origin blocker. |
| `packs/mcp-auth/README.md` | Replace currently accurate unbuilt status/Claude-route statements only once tasks pass; no native provider success claim. |
| `packs/mcp-auth/README.md`, `docs/reference/mcp-configuration.md`, `internal/cli/config_ref.txt` and CLI help | Pack add/config check/relaunch; own-client option; native-name status; headersHelper JSON, bare compatibility, bounded issuance and paste. Existing command registry/help sources gain status grammar, no login/logout verb. |
| `userguide/reference/settings-per-setup.md`, census | Placement everywhere supported by agent formats; source and helper limitations distinct; callback convenience only Linux bridge podman, native witness owed. |

**Final gate once, on combined clean candidate**, not per task/agent:

```console
just format
# Targeted tests and deletion controls above, then refresh cross-package reader census:
python3 scripts/completion-census.py
# Commit intended changes and generated census output through the authorized writer.
just check-ci
# Targeted integration command above, then launch-path full suite once:
go test -count=1 -timeout 0 ./integration
# After final committed clean tree:
just done
git status --porcelain=v1
git diff --cached --name-only
```

If completion census encounters the known foreign-workspace guard for Python scratch, place **only Python tempfile's scratch** inside candidate `.yolo/durable/completion-census-scratch`; preserve Go subprocess TMPDIR/HOME/GOENV/caches and collector filters. Do not weaken census or rerun the full suite per small commit. Keep failed receipts. Landing operator runs final combined gate/full suite once; individual builders run targeted reached tests.

**Fence:** no CHANGELOG/release/deploy/push/live configuration; no native or provider calls. No graduation to system-doc until separate runtime/native verification is honestly reconciled.

**Commit:** `test(mcp): pin managed sign-in launch regressions and current authority`.

**Report:** combined commit/HEAD, test counts/results, source-stamped nested probe, clean/index evidence, native witness list still owed, no live config touched.

## Candidate implementation checkpoint

Tasks 1–8 have serial commits through `99130ff312b064b31f78fb46a907519d40037ec4`; Task 9 retains their evidence and adds real account-free launch regressions. The [A1–A32 inventory](managed-mcp-sign-in-coverage.md) records concrete callers, deletion controls and limits, rather than equating compile/short skips with jail or native success. All `Built` entries mean **synthetic only**, not landing or provider verification.

Task 9 found two real caller gaps: authenticated frontend principal versus container name, and nonsource helper rendering after workspace identity override. Core now supplies the actual private endpoint locator to the host child after inherited/manifest env; Main binds its exact frontend principal and the trusted container name to the validated private share. Backend socket location is not that locator. A supplied-invalid packet remains distinct from absent legacy input; valid frozen facts require nonsource eligibility, not a bundle.

Private integration consumer adapters use the actual shipped Lua/surfaces through inert program adapters. Their legacy leaf-name selection remains a fixture choice, not a qualified-handle limitation: the forward review repair separates logical `Source.Pack` from its exact declared program/Lua `Source.Adapter` and explicit private store paths. `TestManagedMCPQualifiedSourceThroughTOMLShareAdoptionAndHostHeader` exercises readable qualified TOML selection through validated share, synthetic adoption and actual host header issuance; the same-leaf source adversary checks separate scripts, roots and provenance. Absent adapters preserve legacy leaf descriptors, replica filenames remain leaf-only, and no source key, canonical version, client identity or digest is migrated. This is checked Claude-compatible adapter evidence, not arbitrary provider/backend parity. Internal synthetic core provisioning ran; external/vendor hooks, agents, installers and auto-capture did not.

The final full slow suite and landing belong to the parent. Native/rootless/Mac/provider/plugin-loading/reconnect/writeback/OAuth witnesses below remain owed. Completion reports do not authorize cleanup or release.

## Source-snapshot and nested probe discipline

**Constraint:** nested verification for nonnetwork logic is mandatory for `internal/` changes; callback reachability/rootless behavior is explicitly outside what it can prove. Synthetic probe always takes a source-at-probe stamp, not a later HEAD substituted into an earlier log.

1. Record `git rev-parse HEAD`, `git status --porcelain=v1`, `git diff --binary` hash, selected pack tree digest and source file hashes into durable evidence before build/probe. Record the same after; concurrent source drift invalidates matched-source claim. A baseline build cannot verify candidate code.
2. Run `just build-dev-bundle`; record its emitted **absolute** launcher path. Its bundle must match the candidate Linux binaries and source/pack snapshot. Do not run bare installed `yolo`, `just install` or `just deploy-dev`.
3. Mint throwaway workspace and controlled fake user/pack/state fixture under durable scratch using the existing fixture/user-layer inputs, not a global shell HOME/TMPDIR replacement. Preserve shared runtime/tool/cache links. Keep selected fixture agents inert: no program declaration/capture or fake installed capture entries, verified by fixture counters; no imported owner credential tree.
4. Invocation shape (substitute the emitted absolute path, never infer a stable path):

```console
cd /workspace/.yolo/durable/managed-mcp-probe/workspace
YOLO_NO_PROGRAM_READINESS=1 YOLO_NO_AUTO_CAPTURE=1 YOLO_NO_UPDATE_CHECK=1 YOLO_NO_AUTO_IMAGE_REAP=1 \
  /ABSOLUTE/EMITTED/LAUNCHER --user-layer /workspace/.yolo/durable/managed-mcp-probe/user.toml \
  -- bash -c 'yolo mcp-auth status --json; test -f "$HOME/.claude/skills/fixture/.mcp.json"'
```

5. Verify before executing that fixture effective config has no inherited owner sources, installer programs or live tokens. `--user-layer` is an overlay, **not a replacement of owner scope**; use a prepared synthetic-only source snapshot fixture/harness to eliminate inherited selection safely. If this cannot be proved without reading owner credentials/config or widening the brief, do not run; report nested validation blocked and use production fake Run + real integration fixture instead. The command is a probe shape, not authorization to run it against ambient owner config.
6. Ensure process/container confirmed gone, private inputs/leases/staging cleaned, fake state never leaked into real owner store. Assert absence of capture/install markers and no real agent executable calls. Keep before/after manifests and failed receipts. Source snapshot plus no-install evidence accompany any green.

## Acceptance coverage and deletion controls

| Accepted scenarios | Permanent driver / task |
| :--- | :--- |
| A1, A2, A24, A25 | Decode/selected-pack validation, actual whole-tree writer, real Run→producer and staged inventory; Tasks 1–3. Delete placement→inventory or user-selection wiring. |
| A3, A4, A13–A16, A29 | `serve → runOnce → Pass`, counted transport and locked commit/restart; Task 4. Delete expected-client guard/producer or note writer/readback. |
| A8, A9, A27 | Main and real framed header handler→launch poll owner, controlled clock/cancellation; Task 5. Delete expired wake and commit-independent worker wiring. |
| A10–A12, A28 | Main and `op=status` missing/unsafe/healthy/source-free fixture; Task 6. Delete strict descriptor or CLI dispatch/full-row wiring. |
| A30–A32 | No-point-only embedded original advice through pure decision, Main and `op=status`; Task 6. Delete diagnosis→advice wiring or source-shadow exclusion; counting filesystems catch all materialization/custom reads and reads on other verdicts. |
| A5–A7, A26 | Real boot surface loop and host apply, with real producer witness in integration; Task 7. Delete derive context builder assignment, not only a Lua callee. |
| A17, A20–A23 | Actual Run/keeper create/assembly/attach/confirmed-gone cleanup + entrypoint DNAT fake; Task 8. Delete acquisition→argv, DNAT merge, bind fallback and teardown caller independently. |
| A18 | Next-launch frozen recomposition and clean-origin sign-out; Tasks 3–4/9. Removing once/pack does not rewrite/revoke retained canonical record. |
| A19 | Existing Linux host resident, Apple Container start, keeper confirmed exit and macos-user session caller tests unchanged; Tasks 3–9. |

Deletion controls are **permanent tests**, plus temporary authorized mutations proving they reach wiring. A green helper test whose production caller can be deleted green is rejected. A fixture that directly manufactures `capturable`, verdict or publication cannot be the only evidence of the producer; paired actual Run/integration tests prove those facts cross the real caller chain.

## Native/rootless/Mac/provider limits and rollback witness

- **Not claimed:** Slack OAuth, client-id-only refresh, second presenter acceptance, real agent loaded plugin, reconnect/401 helper rerun, own-OAuth suppression, native paste, origin in-memory token takeover, or native behavior after refresh token removal.
- **Rootless host witness owed:** native callback for an explicitly supplied integration on a real rootless Linux bridge launch, observing `podman info --format '{{.Host.Security.Rootless}}'` and `podman info --format '{{.Host.RootlessNetworkCmd}}'`, installed version, source stamp and IPv4-only limitation. Nested podman forces host networking/userns and cannot substitute. Browser `localhost`→`::1` preference must be explicitly measured or limitation confirmed.
- **Mac witness owed:** file versus Keychain/alternate secure storage; macos-user source location and writable host source lock; Apple Container/podman-Mac start/confirmed-exit boundaries. Static backend fixtures and cross-compilation are not native verification. No new Mac host adoption/header capability is enabled here.
- **Provider witness owed:** an explicitly supplied integration's provider accepts adopted public-client refresh and access from a second consumer; source continues after refresh-token removal; single-use takeover race behavior. Public documents/metadata are not measured provider success. No particular provider integration is required or shipped.
- **Claude helper witness owed after synthetic landing:** installed native version loads placed bundle and exposes resolved native name; helper reruns on reconnect and after 401, does not start OAuth; paste works. If helper measurement fails, withdraw only pack header-route declaration and next launch returns baseline plain. Preserve note/status/pack placement and unrelated trust banners.
- **Codex remains blocked:** native-origin/header provenance and measured helper/version support are separate; no metadata field repairs either. Broader sharing plan Tasks 13–15, confidential refresh, revoke/logout, account selection and unknown adapters are outside this hand-off.

## Ships with / don't

- Ship schema/path/inventory validation, actual pinned licensed tree, native-name/capability declarations, frozen scope/capturability, expected-client enforcement, durable outcomes, zero-effect host/jail status, no-point-only embedded provisioning advice, closed truthful recovery, bounded bare/JSON issuance, Claude derive, callback ownership/fallback and every caller regression above. No status-only subset may be called complete.
- Keep no-access defaults, existing grants, user-only credential settings, own client priority, stable identity/version, source-safe sign-out and per-record concurrency. No whole-store rewrite, token hashes in status, client borrowing, unknown native promotion or broader bundle sharing.
- Do not stage anything while inspecting status, treat a placed bundle as loaded, hide callback/placement failure, suppress trust banners, bind `0.0.0.0`, retry all of Run, run a real agent/installer to test a render, or confuse nested host-network green with callback success.
- This repository companion uses canonical doc-relative authority links. Original planner source/test receipts remain historical baseline evidence, not candidate greens. Publication checked the offline Vantage 0.12.0 style guide and targeted documents/index; no package fetch, native agent or provider execution was involved.

## Blockers and hand-off readiness

**Build blockers:** none. MS-D14 is accepted and published in the [sign-in point](managed-mcp-sign-in.md#4-the-sign-in-point), [recovery](managed-mcp-sign-in.md#8-recovery-lines), ledger and A30–A32 acceptance rows. No engineering or policy question remains. Advice is embedded-FS-only, no-point-only and effect-free, with no new declaration field or authority from an unselected pack.

**Verification owed:** native/rootless/Mac/provider witnesses above, after synthetic gate. Runtime unavailable or a safely isolated emitted probe unavailable is a verification limitation, never permission to bypass isolation or a reason to invent another login flow.

**Planner readiness:** original source/caller/test gaps confirmed and fixture commands passed; finite delta source facts re-read at the stamped HEAD; every accepted scenario A1–A32 mapped; nine serial tasks, fences, real-caller predicted red commands, deletion controls, integration targets and the single combined landing gate are named. The plan is a complete accepted build hand-off, not an implementation claim. The future scoped-pack candidate is not a dependency and cannot be used as evidence that bundles or agents load. Recheck isolated-candidate HEAD and touched paths before Task 1. Independent review remains required.

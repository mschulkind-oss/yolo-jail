---
title: "Plan: Bedrock from a host SSO login"
date: 2026-09-17
status: accepted
tags: [aws, bedrock, sso, credentials, loopholes, packs, implementation]
summary: "Build hand-off for sso-backed-bedrock.md, written against the tree: the files that change, the openai-auth machinery to copy, the traps, the eight-step order, and which steps a nested jail can and cannot verify."
---

# Plan: Bedrock from a host SSO login

**Status:** DECIDED, 2026-09-24 — steps **1–4, 6 and 6a are built** (`internal/awsauth`,
`internal/awsauthdaemon`, `internal/awscredadapter`, `packs/aws-auth`; step 6's exclusivity
refusal and `~/.aws` grant conflict landed 2026-09-18, and step 6a moved both into the pack's
manifest and added the static key pair on 2026-09-25), and [Blockers](#blockers) 7 — the
missing endpoint variable — was **fixed 2026-09-20** (`62e553a8`), so the chain is wired end to
end in code (its census test is unit-level; no nested run is recorded since); steps 5 and 8 are
not built, step 7 is deleted, and nothing has run on a real host. Ruled 2026-09-17, when
nothing was built; a sketch before that, and a **hand-off** promoted against the tree at
`6ded2789`. See [Progress](#progress) for what landed and what a real host still has to
settle.

**Scope, per [`OQ-SSO7`](sso-backed-bedrock.md#13-decision-ledger) (ruled 2026-09-24).** yolo
supports three Bedrock credentials — a bearer (`AWS_BEARER_TOKEN_BEDROCK`), a static access key
and secret, and an SSO session — and the SSO session is **the primary one**. This plan builds
that one. The other two are existing `env_sources` channels and need nothing from it; the bearer
had an arm here (step 7), retired by
[`OQ-SSO9`](sso-backed-bedrock.md#OQ-SSO9). Step 6's refusal covered the pointer beside a bearer
only, and lived in core; [`OQ-SSO8`](sso-backed-bedrock.md#OQ-SSO8) ruled that it moves into
`aws-auth`'s manifest and also covers a static key pair, never with a false positive, and step
6a built exactly that ([Progress](#progress)).

**Design:** [`sso-backed-bedrock.md`](sso-backed-bedrock.md) — behaviour in
[§8](sso-backed-bedrock.md#8-behaviour-this-design-specifies), order in
[§12](sso-backed-bedrock.md#12-what-i-would-build-in-order), rulings in the
[Decision Ledger](sso-backed-bedrock.md#13-decision-ledger). **Closest built precedent:** the
OpenAI credential service ([`openai-auth-broker.md`](openai-auth-broker.md), `packs/openai-auth`)
— every host↔jail mechanism this needs already runs there; only the AWS half is new.

**Precedence.** The design wins on behaviour. The tree wins on fact — a moved symbol below is
followed, and the commit says so. This file is advice, and the first thing to be wrong.

## Map

| Path | Change |
| :--- | :--- |
| `packs/aws-auth/pack.json` | **BUILT** — `kind: "loophole"` (`from: loopholes/aws-auth`) + one `kind: "env"` gated on the `bedrock` profile ([Blockers](#blockers) 2), carrying the `overridden_by` declaration since step 6a |
| `packs/aws-auth/loopholes/aws-auth/manifest.jsonc` | **BUILT** — `publishes: "socket"`, `scope: "host"`, `state_files: [".mount-sentinel"]`, a `settings` block, `doctor_cmd` |
| `packs/aws-auth/README.md` | **BUILT** — the four items [§12](sso-backed-bedrock.md#12-what-i-would-build-in-order) step 4 owes it, plus the [`OQ-BR4`](./provider-credential-scope.md#OQ-BR4) instance |
| `packs/embed.go` | **BUILT** — `all:aws-auth` added to the `//go:embed` list (explicit, test-enforced) |
| `internal/awsauth/` | **BUILT** — cache state keyed by profile, host-wide lock, mint + narrowing; mirrors `internal/openaiauth` |
| `internal/awsauthdaemon/` | **BUILT** — `Main`, handler, `--self-check`; mirrors `internal/openaiauthdaemon` |
| `internal/awscredadapter/` | **BUILT** — container-credentials HTTP on jail loopback; mirrors `internal/openaiauthadapter` |
| `internal/cli/internal.go` | **BUILT** (`700d7699`) — one `case "aws-auth":` in `runInternalDaemon` |
| `cmd/yolo-jaild/main.go` | **BUILT** — one `case "aws-credential-adapter":` plus the usage line |
| `internal/cli/run/assemble_parts.go` | **BUILT** (`62e553a8`, 2026-09-20) — `hostScopedEndpoints` emits `YOLO_SERVICE_<NAME>_ENDPOINT` for every active host-scoped loophole, keyed on the manifest's scope rather than on a name ([Blockers](#blockers) 7) |
| `internal/awschain/` | **DELETED** by step 6a. It held the rule keyed on the variables themselves, which [`OQ-SSO8`](sso-backed-bedrock.md#OQ-SSO8) moved into the pack |
| `internal/packdecl/envoverride.go` | **BUILT** (step 6a) — the `overridden_by` schema on a `kind: "env"` contribution; its doc comment is the reference |
| `internal/packload/envoverride.go` | **BUILT** (step 6a) — `EnvOverrideRefusal`, the generic evaluation and its wording, plus the delivery-channel phrases; names no variable |
| `internal/config/renderedhostfiles.go` | **BUILT** (step 6a) — `RenderedHostFilePaths`, the `host_files` destinations a launch actually renders |
| `internal/cli/run/envoverrides.go` | **BUILT** — the pre-flight (step 6 as `awschannels.go`, generalized by 6a), beside `providerpreflight.go`, at all THREE of its call sites |
| `internal/cli/check/envoverrides.go` | **BUILT** — the same refusal PREDICTED, calling `packload.EnvOverrideRefusal` rather than restating it (`protocols.go`'s precedent, not `capabilities.go`'s) |
| `internal/config/validate_loopholes.go` | the `~/.aws`-grant conflict built here in step 6 is **DELETED** by 6a; the grant is now the pack declaration's `host_file` entry, refused by the pre-flight above |
| `packs/claude/pack.json` | `needs: [{"pack": "aws-auth"}]` (step 5) |
| `integration/awsauth_test.go` | **new** — the end-to-end transport test ([Ships with](#ships-with)) |

No new `cmd/` binary, so `flake.nix`'s `shippedBinaries` and `scripts/stage-source-bundle.sh` are
untouched; `packs/` is already in the `goSrc` fileset.

## Reuse

- **The config surface is the loophole `settings` mechanism. Add no top-level key.** A manifest
  declares typed keys (`internal/loopholedecl/settings.go`: `string | bool | int | string_list`,
  `scope` defaulting to `user`); `internal/config/validate_loopholesettings.go` validates
  `loopholes.aws-auth.settings.*` and its `loopholeSettingsScopeViolations` refuses a workspace
  spelling; `internal/cli/run/loopholesettings.go` → `loopholes.WriteSettings` writes a flat JSON
  file at launch, and the daemon gets its path through the `{settings}` argv token. Copy
  `packs/journal`'s `full` key (`scope: "user"`). This is
  [`OQ-SSO4`](sso-backed-bedrock.md#13-decision-ledger) for free; the sketch's top-level
  `aws_auth` key would have been the second scope grammar that ruling refuses.
- **Host daemon:** `internal/openaiauthdaemon/main.go` — `Main`'s flag set, `serveSockets`
  (fronted socket plus the `.host` sibling, `HostSocketPath`), `selfCheck`, `runProactive`. The
  handler is a `hostservice.Handler` (`internal/hostservice`) over `Session.Get/JSON/Stderr/Exit`,
  dispatching on an `action` field; `Session.JailID` is the host-asserted caller for audit lines.
- **State + lock:** `internal/openaiauth/broker.go` `Broker.withLock` (a flock beside the state
  file) and `state.go`'s atomic `writeState`.
- **Jail adapter:** `internal/openaiauthadapter/handler.go` — `Main` reads its endpoint path from
  the `YOLO_SERVICE_*_ENDPOINT` variable; `Serve(listener, fn)` is the loop. Here the variable is
  `YOLO_SERVICE_AWS_AUTH_ENDPOINT` (`hostServiceEnvVar`, `internal/cli/run/helpers.go`) and the
  file `/run/yolo-services/aws-auth.endpoint`.
- **Jail→host request:** `internal/openauthclient/client.go` `Request` — `svcendpoint.Dial`, then
  the `frameproto` stdout/stderr/exit loop. Its error strings say "OpenAI"; copy the ~40 lines
  rather than import.
- **Manifest rules:** `internal/loopholedecl/packshipped.go` `PackShippedProblems`;
  `yolo pack lint packs/aws-auth` runs it.
- **Framework, keyed off the manifest — write none of it:** `startLoopholesDisclosed`
  (`internal/cli/run/packloopholes.go`) spawns the `scope: "host"` singleton; `svcendpoint` fronts
  the socket and publishes the endpoint; `enabledServiceEndpoints`
  (`internal/entrypoint/reachability.go`) probes every `YOLO_SERVICE_*_ENDPOINT` at boot and
  refuses the launch under the `requested`/`shared` dispositions;
  `internal/cli/check/sections_loopholes.go` runs `doctor_cmd` and `reportSelfCheckLines` grades
  `OK:` / `NOTE:` / `FAIL:` lines — the remaining-session-lifetime line lands there.
- **Pre-flight shape:** `checkProviderCredentials` (`internal/cli/run/providerpreflight.go`) —
  returns `(lines, refuse)`, asks `channel.deliveryLookup` what the launch delivers, honours its
  escape hatch loudly. Mirror it for the exclusivity refusal.
- **`needs`:** `packload.ResolveNeeds` (`internal/packload/needs.go`) — embedded packs only, join
  never override; `packs/claude/pack.json` already names `openai-auth` and `wire-bridge`.
- **Profile-gated env:** `packload.EnvFold` + `profileActive` (`internal/packload/packload.go`).
  The wide pass fires for a CLI-less pack when *any* selected pack's active profile has the name
  — the `packs/zai` case — so an `aws-auth` env gated on `"profile": "bedrock"` activates on
  `-p bedrock` with claude.

## Traps

- **Resolve the AWS half by shelling out to `aws` CLI v2; do not vendor the SDK.** Advice, three
  reasons: `vendor/` holds no AWS code and the nix build is hermetic (`aws-sdk-go-v2` is a large
  committed tree); `aws configure export-credentials --profile X --format process` refreshes the
  SSO access token itself when a refresh token exists, which is P2 by construction; and
  [§8](sso-backed-bedrock.md#8-behaviour-this-design-specifies)'s failure table already presumes
  the CLI ("missing on the host → fails loudly at spawn"). N2 is
  `aws sts assume-role --policy … --duration-seconds 3600` from the same shell-out.
- **`--format process` spells the token `SessionToken`; the container protocol wants `Token`.**
  The SDK rejects the missing field without naming it. Done, and NOT in the adapter as this
  line first said: `awsauth.Credential.ContainerCredentials` is the one place it happens, and
  the adapter forwards that body verbatim so there is one spelling in the tree.
- **Nothing gates a loophole on a setting.** `Setting` has no `required`, and the framework has
  no "declared but unconfigured → do not spawn" state ([Blockers](#blockers) 1).
- **A jail-daemon spawn failure is logged, not silent — since 2026-09-18** (`eb02ad86`).
  `superviseOne` writes `spawn failed: …` to `~/.local/state/yolo-jail-daemons/aws-auth.log`
  and then obeys the manifest's restart policy. A stale `yolo-jaild` in a nested image is still
  the usual cause, so read that log before anything else.
- **The census tests fail until the rows exist, and should.** `shippedManifestHome`,
  `wantDefaultEnabled` and the name list in `internal/loopholedecl/shipped_test.go`;
  `shippedLoopholes` in `internal/loopholes/shipped_test.go`; `TestEmbedMatchesTree`
  (`internal/packload/embeddrift_test.go`) for `packs/embed.go`.
- **Jail loopback ports already fixed:** `1460` (OpenAI adapter), and the wire bridge's two
  adaptations at `8214` (`openai → anthropic`) and `8215` (`openai-responses → anthropic`).
  ⚠ This read `8214`/`8216` (cerebras/kilo) until 2026-09-18: the ports are the ADAPTER's, not
  one per provider, and `8216` was eliminated rather than moved. Anything else is cheap and yours.
- **Pre-mint on `runProactive`'s ticker; never mint inside a request** — the design's R1.
- **Detect the SSO config form from `~/.aws/config`** (`sso_session` under `[profile X]`). No INI
  parser is vendored; ~30 lines by hand.
- **`git add` before any nested rebuild** — the image build sees tracked files only, so an
  untracked `packs/aws-auth/` vanishes while every report says success.

## Build order

Follows [§12](sso-backed-bedrock.md#12-what-i-would-build-in-order). Every step ends green on
`just test-fast`; **Proves** is the extra command or measurement. **Verify** names the instrument:
*unit* (host, no container); *nested* (`just build-go`, then from `/tmp/yolo-nested`:
`YOLO_REPO_ROOT=/workspace /workspace/dist-go/linux-$(go env GOARCH)/yolo -- bash`); or **real
rootless host**. A nested jail is podman-in-podman forced to `--net=host`, so the jail's loopback
*is* the launcher's and any host-loopback reachability claim gets a free green (AGENTS.md's
carve-out; [`loopback-tls-reachability.md`](../reference/loopback-tls-reachability.md#a-nested-jail-is-structurally-blind-to-this)).
Report a real-host result with `podman info --format '{{.Host.RootlessNetworkCmd}}'`.

| # | Step | Proves | Verify |
| :--- | :--- | :--- | :--- |
| 1 | **BUILT** ([Progress](#progress); the dispatch row landed in `700d7699`) — `internal/awsauth` + `internal/awsauthdaemon`: resolve via `aws`, cache by profile, flock, pre-mint ticker, `--self-check` minting once and printing the four keys with the secret elided. The `runInternalDaemon` row is pinned by `TestInternalDaemonDispatchRoutesAWSAuth` | `go test ./internal/awsauth/... ./internal/awsauthdaemon/...`; `yolo internal daemon aws-auth --self-check --settings <file>` | unit; the self-check wants a host with an `aws` login |
| 2 | **BUILT** — the narrowing setting, inside step 1's settings file: absent → refuse at spawn naming the key; un-narrowed by name → serve, plus the disclosure line. Two of its three call sites exist (spawn log, `--self-check` `NOTE:`); the LAUNCH line is a call to `Narrowing.DisclosureLine` from `writeLoopholeSettings` | unit cases: absent, N2 role + policy, un-narrowed by name | unit |
| 3 | **BUILT**, and reachable since [Blockers](#blockers) 7's fix (`62e553a8`) — `internal/awscredadapter` + the `yolo-jaild` row; the manifest; `packs/embed.go`; the census rows | `yolo pack lint packs/aws-auth`; in a nested jail selecting the pack, `curl -s $AWS_CONTAINER_CREDENTIALS_FULL_URI` returns the four keys, or the 4xx `Code`/`Message` with no session | nested proves the transport is **wired**; that the jail reaches the front is **real rootless host** only |
| 4 | **BUILT** — `packs/aws-auth/pack.json` (the gated `env` pointer) and README | `yolo pack footprint packs/aws-auth`: one env key, one loophole, no host grant | unit |
| 5 | `needs` on `packs/claude`; done-conditions 1 and 4 | a claude turn on Bedrock; lapse, `aws sso login`, next turn succeeds with no relaunch | **real rootless host** (an SSO login, a browser, the forwarding hop) |
| 6 | **BUILT** ([Progress](#progress)) — exclusivity refusal (the pointer **and** `AWS_BEARER_TOKEN_BEDROCK` both delivered), at the launch's three arms and predicted by `yolo check`; the `~/.aws`-grant conflict in `internal/config`, so `yolo check` and launch both refuse. Both moved into the pack by 6a | each of the three call sites deleted in turn, one named test red for each (measured, not assumed); `just check-ci` **and** `env -u YOLO_VERSION go test -short ./...` | unit |
| 6a | **BUILT** 2026-09-25 ([Progress](#progress)) — step 6's two refusals moved out of core into `aws-auth`'s manifest, per [`OQ-SSO8`](sso-backed-bedrock.md#OQ-SSO8): the pack declares which delivered variables override its pointer (the bearer; both halves of the static pair, unless `AWS_PROFILE` is also delivered) and the `~/.aws` grant that disables it; core refuses generically and names no AWS variable. The chain order of claude, codex and opencode was measured first — all environment-first ([design §11](sso-backed-bedrock.md#11-evidence-and-how-to-re-check-it)) — so the pair ships | a manifest declaring a made-up variable is refused beside its contribution with no core change (`TestEnvOverrideRefusesAMadeUpVariable`, `TestSectionPacksPredictsTheOverrideRefusal`); a lone `AWS_ACCESS_KEY_ID` and the pair plus `AWS_PROFILE` both launch (`TestEnvOverrideLetsTheNonOverridingShapesThrough`); a bearer or pair only in the invoking shell launches (`TestEnvOverrideIgnoresTheShellYoloWasLaunchedFrom`); a directory grant counts only where the backend binds it (`TestEnvOverrideCountsADirectoryGrantOnlyWhereItIsBound`); the package is gone | unit; the call-site deletions of step 6, repeated in a private copy — each named test red |
| 7 | ~~N1 arm~~ — **deleted**: option D is retired ([`OQ-SSO9`](sso-backed-bedrock.md#OQ-SSO9), 2026-09-25). A user who wants the N1 narrowing mints the key on the host with AWS's generator and delivers it as a bearer | — | — |
| 8 | Fold into [`agent-credentials.md`](../reference/agent-credentials.md); retire the design via `system-doc`; delete this file | `uvx vantage-check docs/` clean | — |

**Expensive if late:** step 2 inside step 1 (a widening default retrofitted breaks working
setups — the design says so); the census rows in step 3 (every later `just test-fast` is red).

## Progress

**Step 6a's review, 2026-09-25**, found two false positives, one false negative, an ordering
defect, and a source pin too weak to catch two weakenings. All are fixed with tests, and each
fix was proved by mutation in a private copy of the tree: ten mutations, each turning its named
test red. One more false positive needs a ruling and is not fixed (see the end of this entry).

- **Only what reaches the jail counts.** The override lookup counted a variable found only in
  the shell `yolo` was launched from, which no backend forwards. That refused a host-shell
  bearer or key pair the jail would never see, and it let a host-shell `AWS_PROFILE` excuse a
  pair delivered through `env_sources`. `jailOriginLookup` (`internal/cli/run/envoverrides.go`)
  now drops that answer, and `yolo check` no longer reads its own environment for this rule.
  The credential pre-flight still reads the launch environment, since a derive relays from it.
  The call-site tests now deliver overrides through `env_sources`.
- **A directory grant counts per backend.** `config.RenderedHostFilePaths` takes whether the
  backend delivers directories: not on macos-user, and not on Apple Container below the
  read-only-bind floor (`hostFileDirsDeliver`). `yolo check` counts a directory grant only off
  macOS. Every call site passes its own `rt`, and a source pin checks that.
- **The attach arm refuses before it writes** the running jail's `yolo-user-env.sh`. This covers
  the credential pre-flight too, which had the same order.
- **The container arm's source pin** now checks that the refusal returns 1, releases the lock,
  and is handed `envPairs(runCmd)`. Two weakenings used to pass it.

The `~/.aws` grant test now seeds a key pair rather than a region-only config. Refusing a
`~/.aws` with no credentials in it is a false positive in the JavaScript SDKs. The shipped
`host_file` entry still does so, pending a ruling recorded under
[`OQ-SSO8`](sso-backed-bedrock.md#OQ-SSO8).

**Step 6a landed 2026-09-25**, unit-verified, with every call site measured by deletion in a
private copy of the tree. It moves step 6's two refusals into `packs/aws-auth/pack.json` and adds
the third that [`OQ-SSO8`](sso-backed-bedrock.md#OQ-SSO8) ruled in — so the core half of this
feature now knows no AWS variable at all.

- **The measurement came first**, because the ruling made it a precondition: the static-pair
  refusal is a false positive for any consumer that consults the container provider before the
  environment. claude 2.1.282, codex 0.157.0 and opencode 1.18.32 were read from their published
  packages; all three are environment-first, and only the JavaScript SDKs step aside for
  `AWS_PROFILE` ([design §11](sso-backed-bedrock.md#11-evidence-and-how-to-re-check-it)). So the
  pair shipped, with `unless: ["AWS_PROFILE"]`.
- **`overridden_by`** — a key on a `kind: "env"` contribution (`internal/packdecl/envoverride.go`
  is the schema reference): entries of `vars` (all delivered), optional `unless` (any delivered
  steps aside), or `host_file` (a home-relative jail path), each with a mandatory `because` the
  refusal quotes. Strictly decoded; refused on any other kind and in every shape that would do
  nothing or refuse everything (a contribution overriding itself, an `unless` it always
  delivers). `yolo pack lint` and `yolo pack footprint` print one `overridden-by` line per entry.
- **`packload.EnvOverrideRefusal`** evaluates any pack's declaration and words the refusal; the
  launch's three arms (`checkEnvOverrides`) and `yolo check` (`envOverrideGap`) assemble their
  inputs and call it. The delivery-channel phrases moved with it from `internal/awschain`.
- **`config.RenderedHostFilePaths`** answers which `host_files` destinations would actually land
  in the jail, for the `host_file` half.

**Three decisions this round.**

1. **A DECLARATION IS EVALUATED ONLY WHILE ITS CONTRIBUTION IS DELIVERED** — unconditional, or
   its `profile` gate active by the env fold's own rule. That is what "overridden" means, and it
   is stricter than step 6 was for the `~/.aws` half: that rule fired whenever the loophole was
   ENABLED, so `aws-auth` on without `-p bedrock` refused a `~/.aws` grant beside a pointer that
   was not there. It also dropped the loophole from the rule, so `aws-auth` delivered with the
   loophole off is refused beside a bearer, as it already was.
2. **A GRANT COUNTS ONLY WHEN IT RENDERS SOMETHING** — a source-less entry, a source that exists
   as the declared kind, or a `managed`/`defaults` layer to fall back to; the mount side skips a
   missing bind source, so such an entry overrides nothing. This replaces step 6's in-jail
   downgrade to a warning rather than keeping it. The downgrade guarded a nested launch against an
   entry it could not fix, and there is none: an inherited user config carries no `host_files`
   (`config.FilterInherit` drops the key), so a nested launch sees only its own workspace's
   source-less entries, which the in-jail user can edit. The grant half is now a launch
   pre-flight like the variables, not a `ValidateConfig` error, and `yolo check` reports it in
   the Packs block with them.
3. **`LoopholeInfo.Serves` and `DefaultEnabled` are removed** with the resolver copy that filled
   them and the test that pinned that copy: the `~/.aws` validator was their only reader, and the
   declaration keys on the POINTER's delivery rather than on the capability the loophole serves.

---

**Step 6 landed 2026-09-18**, unit-verified at both ends and with the call sites measured
rather than asserted. It is the first step that lands a REFUSAL, so what it had to settle was
mostly about severity and about where a rule may be spelled.

- `internal/awschain` — the two `Forbidden` clauses of
  [§8](sso-backed-bedrock.md#8-behaviour-this-design-specifies), and nothing else: no file
  reads, no commands, no yolo config shapes. Each caller assembles its own inputs and asks
  this package, so the launch, `yolo check` and the config validator cannot word one problem
  three ways.
- The exclusivity pre-flight at **three** call sites — the macos-user arm, `runContainer` and
  `deliverChannelOnAttach` — each beside `checkProviderCredentials` and immediately BEFORE it.
- The `yolo check` prediction, and the `~/.aws` grant conflict in `config.ValidateConfig`.

**Four decisions this round.**

1. **THE PREDICATES KEY ON THE VARIABLE AND ON THE CAPABILITY, NEVER ON A NAME**, which is
   what [Blockers](#blockers) 6 asked for one layer over. The exclusivity rule fires for
   whatever DELIVERS `AWS_CONTAINER_CREDENTIALS_FULL_URI` — `packs/aws-auth`'s gated `env`
   contribution today, a hand-written `env_sources` entry, whatever ships the channel next —
   and the grant conflict fires for whatever loophole declares
   `serves: ["aws-container-credentials"]`. `if lp.Name == "aws-auth"` appears nowhere.
2. **The `~/.aws` conflict is an ERROR on the host** ([Blockers](#blockers) 4, now resolved).
   [§8](sso-backed-bedrock.md#8-behaviour-this-design-specifies) lists the clause under
   *Forbidden* and step 6 asks for `yolo check` **and** the launch; a `config.ValidateConfig`
   error is both, and a warning is only the first. It takes the standard in-jail DOWNGRADE to
   a warning, because a source-bearing `host_files` entry is user-scope only and in a jail the
   user config is the host-generated snapshot — an error there would refuse every nested
   launch over an entry the in-jail user cannot fix at its source. Both arms are pinned
   (`hostScope`/`jailScope`), which is the CI-only class `hostscope_test.go` documents.
3. **The exclusivity refusal gets NO escape hatch, deliberately.** A hatch here would not let
   a user proceed with a known gap the way `YOLO_ALLOW_MISSING_PROVIDERS` does — it would let
   them proceed into the silent wrong answer the rule exists to make loud. The remedy is one
   line of config either way and the refusal names both.
4. **`ConfigEnabledOverride`'s body moved to `internal/config`** (`LoopholeEnabledOverride`),
   with `internal/loopholes` delegating. A third reader arrived that cannot live in
   `internal/loopholes` — the grant conflict is a `ValidateConfig` error, and
   `internal/config` cannot import that package — and re-implementing the user's
   `enabled` switch there would have been the two-copies-can-disagree shape that function's
   own history is a case study in.

**What `yolo check` cannot see, stated where it is enforced**
(`internal/cli/check/awschannels.go`, `envoverrides.go` since step 6a): the assembled container argv (so a pack-shipped
loophole's `jail_env`), the provider environment (composing it runs the env-derive runner),
and `-p` — which matters here more than for the pairing gate, because `packs/aws-auth`'s
pointer is gated on the `bedrock` profile, so `-p bedrock` is exactly the flag that turns a
clean config into the refused one. The prediction is narrower than the launch on purpose:
wrong in places the launch is not would refuse a config that launches fine.

---

**Steps 3 and 4 landed 2026-09-18**, behind steps 1 and 2 the same morning. What the two
rounds have in common is what they could not measure; see
[What a real host still has to settle](#what-a-real-host-still-has-to-settle), which steps 3
and 4 add to rather than shorten.

- `internal/awscredadapter` — the jail-side container-credentials endpoint, plus the
  `yolo-jaild aws-credential-adapter` row. It is a PASS-THROUGH: the host handler's JSON
  already IS the body, so the adapter picks the HTTP status and nothing else, and the
  `SessionToken` → `Token` rename stays in `awsauth.Credential.ContainerCredentials`.
- `packs/aws-auth` — the pack, the loophole manifest, the `bedrock`-gated `env` pointer, the
  README and the census rows, with the manifest's settings scopes pinned by a new
  `TestShippedAWSAuthFields`.

**Three decisions this round, each recorded where it is enforced.**

1. **EVERY failure the adapter emits is a 4xx**, including a transport fault that is nobody
   in the jail's doing. [§5](sso-backed-bedrock.md#5-the-recommended-shape)'s table is why:
   `{Code, Message}` is surfaced on exactly that class and *"any other status is a bare
   failure"*. A tidier 502 for an unreachable host deletes the sentence naming what to fix,
   which is the one thing [`OQ-SSO6`](sso-backed-bedrock.md#13-decision-ledger) requires. A
   census test enumerates every refusal so a later "more correct" status has to argue with it.
2. **The framed client is copied from `internal/openauthclient`, and the reason is not the
   error strings.** Its `Request` DISCARDS stdout on a nonzero exit — and the 4xx body
   carrying `aws sso login --profile X` arrives exactly that way, since the daemon writes it
   to stdout and exits 1. Here the exit code is data on the answer.
3. **An incomplete success body becomes a NAMED 4xx rather than a forwarded 200.** The host
   already refuses to cache one, so reaching it is a bug — and it is the bug an SDK reports
   worst, rejecting a body with no `Token` without naming the field.

**What a nested jail proved, measured 2026-09-18** with a FAKE `aws` on the launcher's
PATH (`--version` plus one `--format process` body; no real login exists inside a jail):
the pack is selected, the loophole is enabled, the host daemon spawns and mints, the front
publishes `/run/yolo-services/aws-auth.endpoint` at `0600` inside the jail,
`YOLO_JAIL_DAEMONS` carries the manifest's adapter argv verbatim, the supervisor starts it,
and a `curl` inside the jail gets the four-key body with `Token` — design done-condition 2,
short of a real credential. The lapse arm answers `400` with the login command verbatim, and
an unknown path answers `404`. What a nested jail can never prove is the hop itself; see
[What a real host still has to settle](#what-a-real-host-still-has-to-settle) item 6.

⚠ **THE JAIL'S ADAPTER COULD NOT FIND THE FRONT ON ITS OWN**, and that is [Blockers](#blockers)
7 rather than a defect in anything this round built. The `curl` above needed
`YOLO_SERVICE_AWS_AUTH_ENDPOINT` exported by hand. Nothing else in the chain was touched, and
the whole chain then worked through the real authenticated front.

---

**Steps 1 and 2 landed 2026-09-18**, unit-verified and nothing else — which is the whole
story that round: `--self-check` wants a host with a live `aws sso login`, and the transport
half is structurally invisible to a nested jail.

What the two new packages do:

- `internal/awsauth` — the minted-credential cache (keyed by profile, atomic 0600-in-0700
  replacement), the host-wide flock beside it, the mint, the narrowing, and the SSO
  config-form report. The AWS surface is ONE exec seam (`Runner`), so no test runs the real
  binary or reaches the network. `Credential.ContainerCredentials` is the only place the
  `SessionToken` → `Token` rename happens.
- `internal/awsauthdaemon` — `Main`, the fronted socket plus its `.host` sibling, the
  pre-mint ticker, the request handler, and `--self-check`. Its `prepare` makes every spawn
  decision and is split out of `Main` so the decisions AND THEIR ORDER are testable without
  binding a socket.

**The settings keys, which were [cheap and yours](#blockers) and are now spent.** Declared
`scope: "user"`, every one of them, with `journal`'s `full` as the shape. Step 3's manifest
needs exactly these four:

| Key | Type | Default | Meaning |
| :--- | :--- | :--- | :--- |
| `profile` | `string` | `""` | the AWS profile the service resolves. Absent → refuse at spawn |
| `role_arn` | `string` | `""` | the role the N2/N3 arms assume |
| `session_policy` | `string` | `""` | the inline session policy the N2 arm attaches (JSON, checked at spawn) |
| `unnarrowed` | `bool` | `false` | serve the permission set as-is. The one widening, asked for by name |

`unnarrowed` is a **bool** for the reason `packs/journal`'s manifest gives at length: the
type set is closed with no `enum`, so core cannot refuse a misspelled string, and a typo must
never be the spelling that grants.

**The daemon's argv**, for the manifest step 3 writes:

```jsonc
"host_daemon": {
  "cmd": ["yolo", "internal", "daemon", "aws-auth",
          "--socket", "{socket}", "--state-file", "{state}/credentials.json",
          "--settings", "{settings}"],
  "publishes": "socket",
  "scope": "host"
},
"doctor_cmd": ["yolo", "internal", "daemon", "aws-auth", "--self-check",
               "--state-file", "{state}/credentials.json", "--settings", "{settings}"]
```

⚠ **THE DISPATCH ROW WAS NOT WIRED WHEN STEPS 1–2 LANDED** (2026-09-18), deliberately: it is
one case in `internal/cli/internal.go`'s `runInternalDaemon`, one import, and the usage
string, and that file had two other changes in flight. **It landed the same day in
`700d7699`**, so the manifest above can spawn. Grep the switch before believing this sentence
either way. Its dispatch test pins on the self-check's exit
code: `aws-auth --self-check --state-file <abs>` returns **0** while a dispatch miss returns
2, so the row cannot be deleted with the test green.

**Step 2 is inside step 1, as the plan asked.** `Settings.Resolve` is the single gate: absent
narrowing refuses and names both keys; un-narrowed by name serves and carries a disclosure
line. Two refusals the design does not mention were added rather than guessed, because both
alternatives resolve in the direction that grants — a `session_policy` with no `role_arn` (an
inline policy is an argument to `AssumeRole`, so there is nothing to attach it to), and
`unnarrowed: true` beside a `role_arn` (preferring either one silently discards what the
other asked for).

**The disclosure has two of its three call sites.** The daemon prints it at spawn (to
`~/.local/share/yolo-jail/logs/host-service-aws-auth.log`) and `--self-check` grades it as a
`NOTE:`, which is what reaches `yolo check`. The LAUNCH line
[§6](sso-backed-bedrock.md#6-narrowing--shape-scoped-and-policy-scoped) asks for is a call to
`Narrowing.DisclosureLine` from `writeLoopholeSettings` (`internal/cli/run/loopholesettings.go`),
which this round did not own.

**Two additions to the plan's own reading of the design.** Neither contradicts it; both are
places where following [§8](sso-backed-bedrock.md#8-behaviour-this-design-specifies)
literally would have produced a body an SDK rejects, or wrong advice.

- **A credential with no session token is a named 4xx, not a serve.**
  [§8](sso-backed-bedrock.md#8-behaviour-this-design-specifies) says a non-SSO
  profile (static keys, `credential_process`) is "served the same way", and it is — there is
  no SSO check anywhere in `internal/awsauth`, and the un-narrowed arm resolves whatever the
  `aws` CLI can resolve. What the container-credentials protocol cannot CARRY is a credential
  with no `Token` and no `Expiration`, and the SDK rejects that body without naming the field,
  so the refusal says which two fields are missing and what to set instead.
- **A missing profile is its own failure kind.** `aws sso login --profile X` for a profile
  that is not in `~/.aws/config` is wrong advice rather than unhelpful advice, so the
  classifier keeps `profile_missing` apart from `login_required` and `cli_missing`.

### What a real host still has to settle

Steps 1 and 2 are unit-only by construction. These are the measurements no unit test in this
repo can stand in for:

1. **The dispatch row**, then `yolo internal daemon aws-auth --self-check --settings <file>`
   against a live `aws sso login`. This is the first thing that exercises `ExecRunner`, the
   real `aws` argv, and the real output shapes — everything upstream of the seam is fixture
   JSON written from AWS's documented formats, never from a recorded invocation.
2. **The lapsed-session signatures.** `classify` matches stderr fragments AWS CLI v2 emits.
   They are written from the documented and widely-reported wordings and each has a fixture,
   but a miss falls through to `MintFailed` (safe: AWS's own words are forwarded) and only a
   real expiry on a real host proves the set is complete. A false positive is the one thing
   the set is written to avoid, so the direction of any correction should be to ADD a
   fragment, never to loosen one.
3. **Both SSO config forms**, read from a real `~/.aws/config`. The fixtures cover the legacy
   and `sso-session` forms and the bare `[default]` section; what they cannot cover is a
   real-world file with `[services]` blocks, `credential_process` profiles and nested
   includes.
4. **The narrowing, demonstrated rather than asserted** — done-condition 5, which needs an
   `aws s3 ls` denied from inside a jail holding an N2 credential. ⚠ Not
   `sts:GetCallerIdentity`, for the reason
   [§8](sso-backed-bedrock.md#8-behaviour-this-design-specifies) states.
5. Everything step 5 already owed a real rootless host.
6. **The transport, end to end, over a REAL host-loopback hop.** A nested jail proved the
   wiring — the manifest spawns, the daemon binds, the front publishes, the supervisor starts
   the adapter and a `curl` inside the jail gets a body — with a FAKE `aws` on the launcher's
   PATH, because no real SSO login exists in a jail. What it cannot prove is the hop itself:
   podman-in-podman forces `--net=host`, so the jail's loopback IS the launcher's and the
   whole loopback-forwarding class gets a free green
   ([`loopback-tls-reachability.md`](../reference/loopback-tls-reachability.md#a-nested-jail-is-structurally-blind-to-this)).
   Report the real-host result with `podman info --format '{{.Host.RootlessNetworkCmd}}'`.

## Ships with

- **Unit**, by case: the four-key shape with RFC3339 `Expiration` and `Token` (not
  `SessionToken`); the lapsed-session 4xx body carrying `aws sso login --profile X` verbatim;
  narrowing absent / N2 / un-narrowed-by-name; a warm-cache serve under 200 ms; legacy vs
  `sso-session` form from two fixture config files; two concurrent fetches, one mint. No agent
  is started anywhere.
- **Package pins** in `internal/cli/run`, mirroring `openaiauthpack_test.go`'s
  `TestShippedOpenAIAuthPackOwnsOneHostSingletonAndAdapter` and
  `TestOpenAIAuthAssemblyPreparesOnlySafeStateMount`, and `TestNoBrokerTokenEnvEmitted`'s shape:
  no `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` or
  `AWS_BEARER_TOKEN_BEDROCK` in the assembled argv, the settings file, or the render sidecar.
- **Integration** `integration/awsauth_test.go`: `packHome` writes the isolated user config
  (`packs: ["aws-auth"]` plus `loopholes.aws-auth.settings`); `runYolo` curls the pointer from
  inside the jail and asserts a JSON body either way. The first end-to-end test of any
  `yolo-jaild` adapter — `openai-auth` has none. `requireJail`, no `t.Parallel()`, and its
  header says it is green in the nested blind spot, as `reachability_test.go`'s does.
- **Tests extended, not repaired:** the two `shipped_test.go` rosters and `TestEmbedMatchesTree`.
- **Docs that enumerate loopholes and will omit one:** `docs/guides/loopholes.md` (the roster),
  [`loophole-system.md`](../reference/loophole-system.md) (R6's "complementary multi-agent case"
  gains a second instance), `packs/embed.go`'s package comment, `AGENTS.md`'s loophole-pack
  sentence, `internal/cli/config_ref.txt`'s `loopholes` section (add an `aws-auth` `settings`
  example beside `host-processes` and `journal`).
- **Docs whose claims move:** [`agent-credentials.md`](../reference/agent-credentials.md) — the
  delivery-channel list, the per-backend table, "Host-service loopholes". The pack README owes
  option A refused in prose, R6, R7, and that the request shape is
  [`boundary-broker.md`](boundary-broker.md)'s ([`OQ-SSO6`](sso-backed-bedrock.md#13-decision-ledger)).
- **Verification environment:** the image bakes no `aws` CLI, so done-condition 5 (`aws s3 ls`
  denied from inside the jail) needs `packages: ["awscli2"]` in the verification workspace.
- **Norms:** `just format` per commit; the pre-commit hook runs `just check-ci`;
  `uvx vantage-check` on every doc touched.

## Don't

- No login, browser flow, `~/.aws` watcher, or approval mechanism. A lapsed session is a
  **message** the jail reports — the 4xx body — never a request it files
  ([`OQ-SSO6`](sso-backed-bedrock.md#13-decision-ledger)); the request shape is
  [`boundary-broker.md`](boundary-broker.md)'s.
- No `requires.command_on_path` probe for `aws` — the shape that removed the Claude broker for
  its own user. Fail loudly at spawn.
- No `awsAuthRefresh` / `awsCredentialExport` in any composed claude settings.
- Profile, role and policy live only in `settings` keys with `scope: "user"`; never read them
  from the merged config.
- No access token held across mints; no mint inside the request path.
- Do not touch `internal/openaiauth*`, `internal/openauthclient`, or `openaiauthbackend.go`'s
  `macos-user` one-off — copy shapes, share nothing. Apple Container is inert for every
  loopback-tls loophole (`backendInertReason`, `internal/cli/run/loopholeinert.go`); leave it.
- Delete this file with the last step and move the roadmap row in the same commit.

## Blockers

⚠ **A sixth, opened 2026-09-18 while wiring steps 1–2: the launch-side disclosure has no
generic mechanism, and it must not get a bespoke one.** `Narrowing.DisclosureLine` has two of
its three call sites — the daemon's spawn log and the `--self-check` `NOTE:`. The third, a
launch-side line, is what [`OQ-SSO1`](sso-backed-bedrock.md#13-decision-ledger)'s *"the
explicit setting is disclosed at every launch"* actually requires, and neither existing caller
satisfies it: the daemon is a host SINGLETON, so its spawn line prints once and then serves
every later launch in silence, which is precisely the widening nobody re-consents to.

It cannot be written yet and should not be written the obvious way:

- **No subject until step 3.** `packs/aws-auth/` does not exist, so no manifest declares
  `unnarrowed`, so `writeLoopholeSettings` never sees this loophole. A call site added today is
  unreachable code.
- **`if lp.Name == "aws-auth"` is forbidden**, not merely ugly: the launch and boot paths
  render every pack in one loop **with no switch on any tool name**
  ([`AGENTS.md`](../../AGENTS.md)), and a settings writer that knows one loophole's name is the
  first such switch.
- **The shape that fits is declarative** — a settings DECLARATION marking a key as widening and
  carrying the sentence, so `writeLoopholeSettings` prints it for any loophole whose resolved
  value is truthy and knows none of their names. That is a `internal/loopholedecl/settings.go`
  vocabulary change, which is a decision rather than wiring, and the second consumer that would
  justify it does not exist yet either.

So: land it **with step 3**, when the manifest gives it a subject, and rule the mechanism then.
Until then the setting is disclosed at daemon spawn and in the self-check, and that gap is
stated here rather than papered over with a call nothing reaches.

⚠ **STEP 3 SHIPPED WITHOUT IT, and gave it its subject.** `packs/aws-auth`'s manifest now
declares `unnarrowed`, so `writeLoopholeSettings` DOES see this loophole — the first bullet
above is spent. The other two are not: the shape that fits is still a declarative one in
`internal/loopholedecl/settings.go`, and writing it any other way means
`if lp.Name == "aws-auth"` in a path that renders every pack in one loop with no switch on any
tool name. It was not written. The disclosure therefore still has two of its three call sites,
and this is now a gap in a SHIPPED feature rather than in an unbuilt one.

⚠ **STEP 6 DID NOT CLOSE IT EITHER, and what it shipped instead is worth knowing before
anyone does.** Its two rules had the same "must not name the loophole" constraint and answered
it WITHOUT a vocabulary change, because neither needed one: the exclusivity rule keys on the
VARIABLE an SDK's chain reads, and the grant conflict keys on the CAPABILITY the manifest
already declares in `serves` — two facts that exist today. The disclosure is not that shape:
there is no existing declaration that says "this settings key widens", which is exactly why it
still wants `internal/loopholedecl/settings.go`. So step 6 is evidence that the constraint is
livable, not that this blocker is smaller than it looked.

✅ **RESOLVED 2026-09-20 (`62e553a8`), and kept as the record of why:** `hostScopedEndpoints`
replaced both named branches with the predicate argued for below — active, may run host code,
scope read from the manifest — and a census test globs the shipped manifests for
`"scope": "host"`, so a fourth host-scoped loophole is wired without a code change. The
nested-launch exception was deleted; the Apple Container one survives as
`hostScopedEndpointIsUnpublishable`. What the fix did **not** do is run the chain on a real host
([What a real host still has to settle](#what-a-real-host-still-has-to-settle) item 6).

⚠ **A seventh, MEASURED 2026-09-18 in a nested jail: no `YOLO_SERVICE_AWS_AUTH_ENDPOINT`
reaches the jail, so the adapter cannot find its front.** Everything else works — the daemon
spawns, the front publishes the endpoint file into the jail at `0600`, the supervisor starts
the adapter — and the adapter then answers every request with its own `ServiceUnreachable`
4xx, because the variable naming that file is never emitted.

The reason is in [Reuse](#reuse)'s "write none of it" list, and that entry is wrong for a
`scope: "host"` loophole. `startHostSingleton` returns a handle with an EMPTY `envVarName` on
purpose — `insertHostServiceEnv` skips it and says why — because a host-scoped service's
variable is emitted much earlier and OPTIMISTICALLY, at argv-assembly time, by
`hostServicesMountArgs` (`internal/cli/run/assemble_parts.go`). That function names exactly
two loopholes: `brokerLoopholeActive` → the Claude broker, and `openAIAuthLoopholeActive` →
`openai-auth-broker`. `aws-auth` is the third host-scoped loophole ever and there is no third
branch, so it is silent.

Three things make it worth a Blocker rather than a patch:

- **It cannot be a third hardcoded name.** Two is already a switch on tool names in the
  launch path; a third would be the one this design has to stop adding to. The shape that
  fits is the predicate those two already compute — *is this loophole ACTIVE and may it run
  host code* — applied to every `scope: "host"` loophole in the set, which is what the
  function's own comment argues for ("THE ENV IS GATED ON THE LOOPHOLE BEING ACTIVE").
- **It is not silent in the jail, and it is silent at the launch.** The adapter's 4xx names
  the missing variable, which is how this was found. But the in-jail reachability witness has
  nothing to probe — it walks `YOLO_SERVICE_*_ENDPOINT`, and the whole fault is that there is
  no such variable — so the launch reports a healthy jail
  ([`loopback-tls-reachability.md` §7.3](../reference/loopback-tls-reachability.md) is the
  rule that makes "never told" quieter than "told and broken").
- **The emission carries the two exception shapes** (`brokerEndpointIsUnpublishable`: a
  nested launch with no singleton, and Apple Container), so generalising it means deciding
  whether those apply per loophole or per launch. That is a ruling, not wiring.

`internal/cli/run` is step 6's file in the [Map](#map) and belongs to nobody this round, so
this is reported rather than taken.

⚠ **STEP 6 LANDED IN THAT FILE AND DELIBERATELY DID NOT TAKE THIS.** The exclusivity
pre-flight sits beside `checkProviderCredentials`, three call sites down; `hostServicesMountArgs`
is a different function answering a different question (*which host-scoped loopholes get an
endpoint variable*), and generalising it still carries the two exception shapes named above,
which is still a ruling. So 7 was unchanged by step 6; it was closed two days later, above.

Stop and ask on each: the tree forces a choice the design does not make. None blocked steps
1–4 or 6; 7 blocked the feature WORKING, not the code landing, until `62e553a8`.

**Measured after steps 1 and 2: none of the five was hit.** They are all step-3-and-later
facts, and the two packages reach none of them — 1 is a manifest spelling (`default_enabled`),
2 is `packs/aws-auth`'s `env` contribution, 3 is step 7's arm, 4 is `internal/config`, and 5 is
[`bedrock-plumbing.md`](./bedrock-plumbing.md). What steps 1–2 DID force were three choices the design leaves open and
the Blockers do not list, all three taken toward refusing rather than guessing and all three
recorded in [Progress](#progress): the two contradictory-narrowing refusals, and a credential
with no session token.

1. **"No profile configured → the loophole does not start."** No setting-conditional spawn
   exists. The only non-code spelling is `default_enabled: false` — unconfigured *is* disabled,
   and enabled-without-a-profile fails loudly at spawn; the alternative is `default_enabled: true`
   with a daemon that self-refuses, which is an error
   [§8](sso-backed-bedrock.md#8-behaviour-this-design-specifies) says this case is not. The value
   also goes in `wantDefaultEnabled`.
2. **Where the pointer lives, and its gate.** [§5](sso-backed-bedrock.md#5-the-recommended-shape)
   puts `AWS_CONTAINER_CREDENTIALS_FULL_URI` in `aws-auth`'s `kind: "env"`;
   [§12](sso-backed-bedrock.md#12-what-i-would-build-in-order) step 4 says selecting the pack
   changes nothing observable. Only a `profile`-gated contribution satisfies both, and the gate is
   a profile **name**: `bedrock` is `packs/claude`'s, while the other three agents' names are open
   in [`bedrock-plumbing.md`](bedrock-plumbing.md) ([`OQ-BR1`](bedrock-plumbing.md#OQ-BR1)).
   [`OQ-BR4`](provider-credential-scope.md#OQ-BR4) (ruled 2026-09-25: the pointer reaches only the
   agents that selected it) and [`OQ-BR8`](providers-and-profiles-redesign.md#OQ-BR8) (whether a
   gate keys on the name or the provider, open in
   [`providers-and-profiles-redesign.md`](providers-and-profiles-redesign.md)) decide its gate. The shipped alternative is consumer-side — `packs/codex` sets
   `CODEX_REFRESH_TOKEN_URL_OVERRIDE` itself.
3. **The N1 bearer's channel and its switch. MOOT: step 7 is deleted** ([`OQ-SSO9`](sso-backed-bedrock.md#OQ-SSO9)). Step 7's "the boot that writes the bearer" is one
   line. Two routes: a host-side mint at launch through the daemon's `.host` socket into
   `writeUserEnvFile`'s pack channel (`internal/cli/run/userenv.go`, mode `0600`), or an in-jail
   boot fetch through the front. And [`OQ-SSO5`](sso-backed-bedrock.md#13-decision-ledger)
   needs "a config enabling both arms" to be expressible, so a settings key selects the arm —
   unnamed anywhere.
4. **Severity of the `~/.aws` grant conflict. RESOLVED by step 6: an ERROR.**
   [§8](sso-backed-bedrock.md#8-behaviour-this-design-specifies) lists it under *Forbidden*;
   step 6 calls it "a `yolo check` line". A `config.ValidateConfig` error is both a check line
   and a launch refusal; a warning is only the first — so the clause being *Forbidden* decided
   it. The one qualification is the standard in-jail downgrade to a warning, which is not a
   softening of the ruling: it is the same asymmetry every sibling validator takes, because in
   a jail the config is the host-generated snapshot and a source-bearing `host_files` entry is
   user-scope only, so erroring would refuse every nested launch over an entry the in-jail
   user cannot fix at its source.
5. **Done-condition 7 is blocked, not late:** codex, pi and opencode have no Bedrock provider or
   region until [`bedrock-plumbing.md`](bedrock-plumbing.md) lands. Step 5 ships claude alone.

Cheap and yours: the adapter port. **Spent by steps 1–2** ([Progress](#progress)): the
settings key names, and one cache file holding one entry per profile rather than one file
each.

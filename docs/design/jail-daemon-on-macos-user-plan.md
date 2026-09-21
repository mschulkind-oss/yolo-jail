---
title: "Plan: start a jail daemon on macos-user"
date: 2026-09-17
status: draft
tags: [macos-user, loopholes, jail-daemon, parity, plan]
summary: "The in-jail half of the loophole lifecycle on the native macOS backend. The host half shipped 2026-09-17; the payload that names the daemons is composed inside a container argv builder and nothing native reads it. Steps 1, 2 and 5 are buildable cold. Steps 3 and 4 are blocked on two questions nobody has filed: how a declared argv resolves with no image, and whether the daemon runs confined."
vantage:
  status-chip: true
---

# Plan: start a jail daemon on macos-user

**Status:** DESIGN, 2026-09-21. Steps 1, 2 and 5 are buildable cold; steps 3 and 4 are blocked on
[OQ-DP8](declaration-parity.md#OQ-DP8) and [OQ-DP9](declaration-parity.md#OQ-DP9). Written against
`6ded2789`, 2026-09-17; re-headed 2026-09-21 because a reader could not tell from it what was
broken.

> **In short.** Half of every loophole is a process that runs *inside* the jail. On
> `macos-user` that half has never run — the list of which daemons to start is composed inside
> a **container argv builder**, and the native backend never reads it. So the backend accepts
> the declarations, starts nothing, and until 2026-09-18 said nothing either.

## What this is for, if you have no context

**The rule.** A loophole's two halves are independently present, and the jail half is decided by the
CONSUMER rather than by the host daemon. The **host daemon** holds what the jail may not — a
credential, a socket — and is what a jail dials over the transport. A **jail daemon** is needed when
the program that will use the capability *inside* the jail is one yolo did not write and can only be
aimed at an address, in a protocol of its own: something then has to sit at that address, terminate
that protocol, and re-issue the request as an ordinary client of the transport
([`aws-auth/manifest.jsonc:130`](../../packs/aws-auth/loopholes/aws-auth/manifest.jsonc#L130) states
exactly that shape). Where yolo wrote the consumer — `yolo-ps`, `yolo-journalctl`,
`yolo-serial`, `yolo-cglimit` — there is no jail daemon at all: the client resolves the endpoint
**file** and dials per invocation, so nothing has to stay alive between runs
([`yolo-ps/main.go:5`](../../cmd/yolo-ps/main.go#L5)). Hence `journal`, `serial` and `host-processes`
— a host daemon, no jail half.

A **jail daemon** here is specifically the `jail_daemon` manifest key: a process the framework
supervises on the jail side of a loophole or a `kind: "service"` pack contribution
([`loopholes.md`](../guides/loopholes.md)). **Not** a `host_daemon`, which `macos-user` has started
since 2026-09-17, and **not** the agent. Every declaration in the tree, two of which are not
features:

| Declaration | What it terminates, in the jail | What aims the consumer at it |
| :--- | :--- | :--- |
| `yolo-jaild oauth-terminator` ([`claude-oauth-broker:119`](../../packs/claude/loopholes/claude-oauth-broker/manifest.jsonc#L119)) | TLS on `127.0.0.1:443` for `platform.claude.com` ([`:71`](../../packs/claude/loopholes/claude-oauth-broker/manifest.jsonc#L71)) | Claude Code's own HTTPS client, whose DNS answer the intercept's `--add-host` points at loopback ([`runtime.go:329`](../../internal/loopholes/runtime.go#L329)) |
| `yolo-jaild openai-auth-adapter --listen 127.0.0.1:1460` ([`openai-auth-broker:30`](../../packs/openai-auth/loopholes/openai-auth-broker/manifest.jsonc#L30)) | the native OAuth token-endpoint shape, because "Codex can override only its refresh URL, not the transport" ([`:26`](../../packs/openai-auth/loopholes/openai-auth-broker/manifest.jsonc#L26)) | `packs/codex`'s `CODEX_REFRESH_TOKEN_URL_OVERRIDE` ([`pack.json:58`](../../packs/codex/pack.json#L58)) |
| `yolo-jaild aws-credential-adapter --listen 127.0.0.1:1461` ([`aws-auth:146`](../../packs/aws-auth/loopholes/aws-auth/manifest.jsonc#L146), `default_enabled: false`) | the container-credentials protocol every AWS SDK already knows how to ask ([`:130`](../../packs/aws-auth/loopholes/aws-auth/manifest.jsonc#L130)) | any AWS SDK, via the `bedrock` profile's `AWS_CONTAINER_CREDENTIALS_FULL_URI` ([`pack.json:11`](../../packs/aws-auth/pack.json#L11)) |
| `yolo-jaild wire-bridge` ([`wire-bridge/pack.json:21`](../../packs/wire-bridge/pack.json#L21)) — a `kind: "service"` contribution, **not a loophole** | Anthropic `POST /v1/messages` on `127.0.0.1:8214`/`:8215` ([`handler.go:3`](../../internal/wirebridged/handler.go#L3), [`pack.json:8`](../../packs/wire-bridge/pack.json#L8)) | `claude`, through the provider base URL the pack's two `adapter` contributions declare |
| `{jail_loophole_dir}/bin/hello` ([`hello-daemon:46`](../../packs/hello-daemon/loopholes/hello-daemon/manifest.jsonc#L46)) — a **test fixture**, off by default | nothing; it is one-shot, `transport: "none"`, no host half ([`:35`](../../packs/hello-daemon/loopholes/hello-daemon/manifest.jsonc#L35)) | nothing — it exists to answer whether a pack can ship its daemon's executable ([`:5`](../../packs/hello-daemon/loopholes/hello-daemon/manifest.jsonc#L5)) |

**Is any of this the transport protocol? No** — and two existence proofs settle it better than an
explanation. `host-processes`, `journal` and `serial` declare `transport: "loopback-tls"` with no
jail daemon; `hello-daemon` declares `transport: "none"` *with* one. The keys are siblings nothing
joins: `transport` "answers exactly one question — *does this loophole have a host daemon a jail
dials, and if so how*" ([`loophole-transport.md:183`](../reference/loophole-transport.md)), every
transport word (`publishes`, `request_end`, `preamble`) lives on `host_daemon`, and
`loopholedecl.JailDaemon` carries `Cmd` and `Restart` and nothing else
([`loopholedecl.go:95`](../../internal/loopholedecl/loopholedecl.go#L95)). `jail_daemon` is a
**lifecycle** slot — one entry in the `YOLO_JAIL_DAEMONS` payload `yolo-jaild supervise` reads
([`supervisorcmd.go:19`](../../internal/supervisor/supervisorcmd.go#L19)) — shared with
`kind: "service"` packs, which is why `wire-bridge` declares one with no host half and no boundary to
cross. Where a jail daemon does meet the transport it is a **client** of it, reading the same
endpoint file `yolo-ps` reads, and it carries no crossing claim for that reason
([`loophole-system.md:519`](../reference/loophole-system.md)).

**What a user actually experiences today.** Nothing dramatic, which is the problem — a bare
`"packs": ["claude"]` on this backend selects three jail daemons and starts none:

- **Codex cannot refresh its token.** `packs/codex` sets
  `CODEX_REFRESH_TOKEN_URL_OVERRIDE` to `127.0.0.1:1460`, and the adapter that should be
  listening there is a jail daemon. The port is dead, so a session works until its first
  refresh.
- **`wire-bridge`'s endpoint file exists with nothing behind it** — published, ACL-granted, and
  connecting to it fails.
- **Claude OAuth refreshes are not serialized**, because the TLS terminator that routes a refresh
  to the host broker *is* the broker's jail half. [Out of scope here](#dont) — and the reason is
  worth reading, because it is not the one you would guess: that terminator may not need to exist
  **on any backend**.

**Why it cannot just be switched on.** The payload naming the daemons is built inside the
container argv assembler, and `macosuser` is a different path that never sees it. Hoisting the
composition above the backend dispatch is the approved mechanism
([`DP-L3`](declaration-parity.md#decision-ledger)) and is most of steps 1 and 2. What it does not
settle is the two things a builder cannot proceed without, and both are now filed:

| Owed | Question |
| :--- | :--- |
| How the argv resolves with no image | [OQ-DP8](declaration-parity.md#OQ-DP8) |
| Whether the daemon is confined | [OQ-DP9](declaration-parity.md#OQ-DP9) |

**Where the vocabulary comes from**, since this file is a plan with no design doc of its own:
[`declaration-parity.md`](declaration-parity.md) is the catalog — `DP-B7` is the defect and
`DP-L3` the approved mechanism, both in
[§6](declaration-parity.md#6-alignable-with-the-mechanism-and-its-cost) — and it delegates every
`macos-user` row to [`macos-user-nix-and-features.md`](../reference/macos-user-nix-and-features.md)
while naming no owner for the loophole lifecycle. The lifecycle terms are
[`loophole-system.md`](../reference/loophole-system.md),
[`loophole-transport.md`](../reference/loophole-transport.md) and
[`wire-bridge.md`](../reference/wire-bridge.md).

**Precedence:** the design wins on behavior, the tree wins on fact, this file is advice and is the
first thing to be wrong — an overtaken line is a note to correct, never a spec to satisfy.

## What is actually broken, measured

`packs/claude` [`needs`](../reference/wire-bridge.md#needs--a-conditional-pack-dependency) both
`openai-auth` and `wire-bridge` **unconditionally**, so a bare `"packs": ["claude"]` on this
backend selects two jail daemons and starts neither, with nothing said:

| Declaration | `jail_daemon.cmd` | Native state |
| :--- | :--- | :--- |
| `openai-auth-broker` loophole (`default_enabled: true`) | `yolo-jaild openai-auth-adapter --listen 127.0.0.1:1460` | host daemon starts **fail-closed**; `packs/codex` still sets `CODEX_REFRESH_TOKEN_URL_OVERRIDE` at that dead port |
| `wire-bridge` service | `yolo-jaild wire-bridge` | endpoint file published and ACL-granted, nothing listening |
| `claude-oauth-broker` loophole (`default_enabled: true`) | `yolo-jaild oauth-terminator` | **out of scope** — see [Don't](#dont) |
| `hello-daemon` loophole (`default_enabled: false`) | `{jail_loophole_dir}/bin/hello` | token resolves to a container mount path |

## Map

| Path | Change |
| :--- | :--- |
| [`internal/loopholes/runtime.go`](../../internal/loopholes/runtime.go) | extract the payload composition (`runtime.go:264-268`, `:307-311`) into an exported producer; `runtimeArgsFor` calls it, so there stays exactly one composer |
| [`internal/cli/run/run.go`](../../internal/cli/run/run.go) | compose the payload **above** the backend dispatch and pass it into `MacosUserRun` |
| [`internal/cli/run/assemble.go`](../../internal/cli/run/assemble.go) | `assemble.go:890` consumes the hoisted value instead of calling `serviceJailDaemons` itself |
| [`internal/macosuser/jaildaemon.go`](../../internal/macosuser) | new — native argv resolution, the runnable/not classifier, the decline line |
| [`internal/macosuser/runplan.go`](../../internal/macosuser/runplan.go) | `RunPlan.JailDaemonArgv`, built beside `ProvisionArgv`; one new `PlanInvariants` rule |
| [`internal/macosuser/orchestrator.go`](../../internal/macosuser/orchestrator.go) | start/stop bracket around `RunWithProxy`; one new `Deps` seam |
| [`internal/cli/internal.go`](../../internal/cli/internal.go) | step 3 only — a dispatch for the in-jail daemons, if that is the ruling |
| [`cmd/yolo-jaild/main.go`](../../cmd/yolo-jaild/main.go) | step 3 only — thin shim over the shared dispatch, so the two cannot drift |
| [`integration/macosuserjaildaemon_test.go`](../../integration) | new — the Mac-runner test |

## Reuse

- **The three sandboxed argvs already share one composer.** `macosuser.sandboxEnvPairs` renders the
  closed `env -i` list; `macosuser.ExecWithEnvFile` wraps the command so the composed environment is
  read from the session env file. A fourth argv is those two plus
  `/usr/bin/sandbox-exec -f <ProfilePath> --` — mirror `LaunchArgv`
  ([`macosuser.go:625`](../../internal/macosuser/macosuser.go#L625)), not `DarwinBootstrapArgv`.
- **The endpoint is already delivered.** `macosuser.EndpointGrantCommands` grants read on every
  `YOLO_SERVICE_*_ENDPOINT` this launch carries plus search on its directory, and `PlanInvariants`
  refuses a plan naming one without a grant. Nothing to add — and
  `TestPlanInvariantsCatchAnUngrantedEndpoint`
  ([`endpointgrants_test.go`](../../internal/macosuser/endpointgrants_test.go)) is the shape to copy
  for "a plan carrying a payload must carry a daemon argv".
- **Never write a supervisor.** `supervisor.ParseEnv` and `supervisor.Main`
  ([`internal/supervisor`](../../internal/supervisor)) own the payload shape, the three restart
  policies, the 1s→30s backoff, the per-daemon log rotated at 5 MB, and SIGTERM→5s→SIGKILL.
  `supervisor.LogDir` is `~/.local/state/yolo-jail-daemons`, under the sandbox home the Seatbelt
  profile already allows writes to.
- **Payload encoding** is `jsonx.DumpsCompact`, the same call at
  [`runtime.go:310`](../../internal/loopholes/runtime.go#L310), so the two payloads cannot differ by
  whitespace.
- **Test fixtures:** `requireMacosUser` and `macosUserWorkspace`
  ([`integration/macosusergate_test.go`](../../integration/macosusergate_test.go)). Subject:
  [`packs/hello-daemon`](../../packs/hello-daemon), which exists for exactly this measurement.

## Traps

- **`yolo-jaild` does not exist on a Mac. Constraint.** `flake.nix`'s `shippedBinaries` produces
  `bin/linux-<arch>` only, `just install` ships `{yolo}`, and `StageBinaryCommands` stages that one
  binary. Three of the four declarations above name it as `argv[0]`.
- **A symlink does not fix that.** Both binaries dispatch on plain `args[0]`, never `argv[0]` —
  stated in [`cmd/yolo-jaild/main.go`](../../cmd/yolo-jaild/main.go), so `yolo-jaild supervise`
  through a symlink reaches `yolo supervise`, which is not a command.
- **`DP-L3`'s literal prescription starts the daemon unconfined.** `DarwinBootstrapArgv`
  ([`runplan.go:166`](../../internal/macosuser/runplan.go#L166)) has no `sandbox-exec` in it, and
  the bootstrap is a one-shot process that exits — so setting the payload in `buildBootstrapEnv`
  would produce an orphan outside the Seatbelt profile. Advice: treat `DP-L3` as approving the
  hoist and the process model, not the placement.
- **Two container mount paths have no native analogue.** `loopholedecl.JailLoopholeDir` returns
  `/etc/yolo-jail/loopholes/<name>` and [`load.go`](../../internal/loopholes/load.go) substitutes
  `{jail_loophole_dir}` into `cmd` before any backend is known; `runtime.go:213-239` mounts that dir
  `:ro` plus the declared `state_files` for any loophole with a jail daemon. The staged pack tree
  (`RunPlan.PackRoot`) is where those bytes actually are here — and the load-time refusal that
  `settings` plus `jail_daemon` must declare `state_files` guards a hazard with no mechanism
  natively.
- **A spawn failure is invisible today.** `supervisor.superviseOne`
  ([`supervisor.go:218`](../../internal/supervisor/supervisor.go#L218)) binds the error from
  `c.start()` and never reads it, then backs off and retries for the life of the jail whatever
  `restart` says — while `openLog` has already created an empty log. The symptom of a wrong
  `argv[0]` is therefore **an empty log and no process**, and that is the first thing this work
  will produce. See [Blockers](#blockers).
- **`1460` is the machine's real loopback here.** `sharesLauncherNetns` returns true for every
  entry in `paths.NativeRuntimes`, and `noteMacosUserPortKeys` already tells users that a port the
  sandbox binds "IS published on this machine's real interfaces". The port is a manifest literal,
  so two concurrent launches collide.
- **Three enforcements you will trip, each fatal.** A new branch on runtime identity in
  `internal/cli/run` needs a trailing `// parity: <Disposition> — <reason>` or
  [`backendparity_test.go`](../../internal/cli/run/backendparity_test.go) names the line — hoisting
  above the dispatch adds no branch, which is the cheaper reason to prefer it. Every test behind
  `requireMacosUser` must be named `TestMacosUser…`, because CI selects the suite with
  `-run '^TestMacosUser'`. And `macosUserWorkspace` fatals on a `packs` key: it is user scope only,
  so it goes in the isolated user config.

## Build order

1. **Extract the payload producer.** One composer, called by `runtimeArgsFor` and by the hoisted
   site. Keep `Set`'s origin gate: an unapproved `SourcePack` contributes nothing.
   → `go test ./internal/loopholes ./internal/cli/run`
2. **Hoist the composition above the backend dispatch, and decline natively by name.** The native
   arm classifies each entry runnable or not and prints one line per declined daemon, naming the
   pack, the daemon and the reason. This closes the *silent* half of `DP-B7` on its own and is the
   only step with no open question under it. Shape (a) of
   [`OQ-DP5`](declaration-parity.md#decision-ledger) — a coded decline plus a banner line, **not**
   a warning. → `go test ./internal/cli/run`
3. **Resolve `argv[0]` natively.** Blocked; see [Blockers](#blockers). Whatever the ruling, the
   in-jail daemon entry points must stay one dispatch shared with
   [`cmd/yolo-jaild`](../../cmd/yolo-jaild) — two spellings of it is the drift the transport
   unification exists to end. → `go test ./internal/cli ./cmd/...`
4. **The confined, supervised child.** `RunPlan.JailDaemonArgv` + a `Deps` seam that starts it and
   returns a stop, called after the provisioning stage and before `RunWithProxy`, stopped on every
   exit path the env-file sweep already covers. Kill the process **group**, matching container
   teardown. Blocked on the same ruling as step 3 plus the confinement half.
   → `go test ./internal/macosuser`
5. **Retract the stale prose.** Independent of 3 and 4, and worth landing with step 2.
   → `uvx vantage-check docs/`

## Verification split

| Step | A Linux `go test` proves | The Mac runner proves | A human must run |
| :--- | :--- | :--- | :--- |
| 1 | the container argv is byte-identical before and after, and the gate still drops an unapproved pack. Mutation: delete the producer call from `runtimeArgsFor` and the container test must fail | — | — |
| 2 | the classifier's verdict per declaration, and that the decline line is emitted for each — asserted at the call site, not on the classifier alone | that the line appears on a real launch | — |
| 3 | `yolo <subcommand> --version`-class dispatch only; no daemon is started by a test | that the resolved path is executable as the sandbox account | — |
| 4 | the argv shape: `sandbox-exec -f <profile>` present, wrapped by `ExecWithEnvFile`, no composed value on the argv, and `PlanInvariants` refusing a payload with no argv | `TestMacosUserJailDaemonStarts` in [`integration/macosuserjaildaemon_test.go`](../../integration): with `hello-daemon` enabled, `~/.local/state/yolo-jail-daemons/hello-daemon.log` exists **and is non-empty** (empty is the spawn-failure symptom), and no supervisor survives the session | two concurrent launches of one workspace, for the `1460` collision; and whether Codex actually refreshes through the adapter — no automated test may start an agent |
| 5 | `vantage-check` link and anchor resolution | — | — |

## Ships with

- **Unit, by case:** an empty payload emits nothing; a loophole-only payload; a service-only
  payload; both, in the container's order; a declaration whose `argv[0]` is unresolvable natively;
  `restart` absent defaulting to `on-failure`.
- **Tests to rewrite, not repair:**
  [`internal/cli/run/packservices_test.go`](../../internal/cli/run/packservices_test.go) asserts
  `serviceJailDaemons` is called from `assemble.go`; its stated mutation test ("delete the call
  from `assemble.go`") is exactly what step 1 does. Re-point it at the hoisted site — do not relax
  it until green. Same for the `YOLO_JAIL_DAEMONS`-appears-exactly-once assertions there and in
  [`internal/cli/run/wirebridgepack_test.go`](../../internal/cli/run/wirebridgepack_test.go).
- **Docs whose claims this makes false**, by path:
  - [`macos-user-nix-and-features.md`](../reference/macos-user-nix-and-features.md) — the
    "Linux-only boot steps … **the daemon supervisor** … are deliberately **not** run" sentence
    (`macos-user-nix-and-features.md:216-218`), which carries no id and no rationale beyond "no-ops
    or nonsensical on a native user". Retract it in place, the way that doc retracts twice already.
    Its "loopholes: mostly moot" section also still says no host service starts here and that
    `EndpointGrantCommands` has no call site — both already falsified on 2026-09-17.
  - [`loopholes.md`](../guides/loopholes.md) — the backend table row giving `macos-user`
    **nothing**, "that arm returns from `Run()` long before `startLoopholes` is reached".
  - [`OQ-T4`](../reference/loophole-transport.md#oq-t4) says the separate-user grant "stays built
    and uncalled" — amend, do not delete; and the second clause of
    [where a loophole does nothing](../reference/loophole-system.md#where-a-loophole-does-nothing).
  - [`declaration-parity.md`](declaration-parity.md) — mark `DP-B7` closed and `DP-L3` built in
    [§11](declaration-parity.md#11-what-i-would-build-in-order)'s note, which that doc names as the
    authority for which rows a wave closed.
- **Roadmap:** 📦 row 5 of [`roadmap.md`](../plans/roadmap.md) moves in the same commit as this
  file's status.
- **Cheap and yours:** the producer's return type (`[]any` matching today's payload is fine — it is
  serialized immediately); the new file's name; one decline line per daemon or per pack.

## Don't

- **Don't set `YOLO_JAIL_DAEMONS` in `buildBootstrapEnv`**, `DP-L3`'s own words notwithstanding —
  the bootstrap is unconfined and short-lived (see [Traps](#traps)).
- **Don't add a second writer of the payload.** One env contract, one writer, is the rule
  `Set.RuntimeArgsForWithJailDaemons` exists to hold; two `-e` lines would make the winner depend
  on duplicate-flag resolution.
- **Don't start `oauth-terminator` natively**, and don't treat it as deferred work either.

  Two things make it impossible here, and both are structural rather than unbuilt. Its default
  port is 443 ([`oauthterminatorcmd.go:37`](../../internal/oauthterminator/oauthterminatorcmd.go#L37)),
  unprivileged only because a container runs as UID 0 — `macos-user` runs as the ordinary
  `_yolojail` account ([`macosuser.go:27`](../../internal/macosuser/macosuser.go#L27)) and cannot
  bind it. And its interception needs the `--add-host` a backend with no container cannot emit, so
  without the DNS redirect nothing would reach a running terminator anyway. Decline it by name in
  step 2 and leave it declined.

  > [!IMPORTANT]
  > **The useful reason is the other one: its existence is under question.** The terminator exists
  > only to serve the OAuth broker; the broker exists only because the credential FILE is shared
  > across jails while the vendor's refresh lock is not
  > ([`claude-oauth-interposition.md`](../reference/claude-oauth-interposition.md)). If
  > [`OQ-CI1`](../reference/claude-oauth-interposition.md#oq-ci1) — *should the credential be
  > shared at all?* — is ruled against sharing, the terminator stops existing everywhere, along
  > with the `/etc/hosts` pin and the CA.
  >
  > The two framings send a reader somewhere different, which is why this note replaced a purely
  > mechanical one: *"cannot work here"* implies waiting for `macos-user` to bind 443, which will
  > never happen. *"May not need to exist"* implies watching [`OQ-CI1`](../reference/claude-oauth-interposition.md#oq-ci1), where this resolves as a
  > consequence rather than as a task.
- **Don't add a crossing claim for `jail_daemon`.** It is claim-free by ruling
  ([what is deliberately not a gate](../reference/loophole-system.md#what-is-deliberately-not-a-gate)).
  The banner class that would carry it is `disclosureJailExec`, and the per-launch "will it
  actually run" answer it was waiting on is what step 2 produces — a follow-up, not this work.
- **Don't reach for `SO_PEERCRED`** to replace the ACL grant: peer credentials verify, file
  permissions restrict, and restriction is the half a boundary needs
  ([threat model](../reference/loophole-transport.md#threat-model)).
- **Don't fix the supervisor here.** It is its own roadmap item; conflating them means the first
  native spawn debugs two things at once.

## Blockers

**FILED 2026-09-21** as [OQ-DP8](declaration-parity.md#OQ-DP8) and
[OQ-DP9](declaration-parity.md#OQ-DP9), against
[`DP-L3`](declaration-parity.md#decision-ledger) — which is where they belong, because a plan is
not where a decision hides. They had been stated only here for four days, which is why
[💬 row 23](../plans/roadmap.md) carried no question id and looked like a row with nothing to
rule.

Steps 3 and 4 are not buildable until both are answered. The full stakes, the candidate table and
the leanings live with the questions; in one line each:

1. **[OQ-DP8](declaration-parity.md#OQ-DP8) — how a declared `jail_daemon.cmd` resolves with no
   image.** Also decides whether `{jail_loophole_dir}` becomes backend-parameterised, which is
   what the `hello-daemon` subject — the only one needing no credential and no network — turns on.
2. **[OQ-DP9](declaration-parity.md#OQ-DP9) — whether the daemon runs under the Seatbelt
   profile.**

**Dependency, not a blocker:** 📦 row 2 of [`roadmap.md`](../plans/roadmap.md) — the supervisor
swallowing a spawn failure. Land it first; otherwise step 4's first run reports an empty log and no
process for any cause, which is unreadable.

**Adjacent:** whether an official pack can ship an executable at all — an embedded pack's files
come back `0444` from `embed.FS`. That bounds the `hello-daemon` subject to a path-configured pack
and is 💬 row 17 of [`roadmap.md`](../plans/roadmap.md).

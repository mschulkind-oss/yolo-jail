---
title: "Plan: start a jail daemon on macos-user"
date: 2026-09-17
status: accepted
tags: [macos-user, loopholes, jail-daemon, parity, plan]
summary: "The in-jail half of the loophole lifecycle on the native macOS backend. The host half shipped 2026-09-17; steps 1, 2 and 5 by 2026-09-24. Steps 3 and 4 are BUILT (2026-09-28) on OQ-DP8 and OQ-DP9: the bundle carries darwin in-jail binaries as bin/darwin-<arch>, the launch stages them into the sandbox's root-owned prefix, and yolo-jaild supervise runs the declared daemons, verbatim and confined by the Seatbelt profile, with caller tokens in a root-owned 0600 file. Its Mac test passed on 2026-09-30."
stage: BUILT
next: "Graduate into docs/reference/macos-user-nix-and-features.md, which already holds the as-built account; its 'None of this has run on a Mac' warning is stale since the 2026-09-30 pass"
vantage:
  status-chip: true
---

# Plan: start a jail daemon on macos-user

**Status:** 2026-09-28 — every step is in the tree. MEASURED: on a Mac,
`TestMacosUserJailDaemonRunsConfinedInTheGuest` passed in `macos-user.yml` run 36719581090
(2026-09-30, at `8f7468dd`). The one earlier run, 36575801495 (2026-09-29, at `4a2f2506`),
failed it before `9280f5e1` moved the test's supervisor probe from the agent's `ps` to the
host's. UNMEASURED: what the [Verification split](#verification-split) leaves to a
human at a Mac — two concurrent launches of one workspace, whether Codex refreshes through the
adapter, and the start time against JD-8's bound. **Steps 1 and 2 shipped 2026-09-18** (`f6387968`): the payload is composed once above the
backend dispatch. Step 5 landed by 2026-09-24. **Steps 3 and 4 shipped 2026-09-28**
(`7b1c1d48`, `068f8fe2`) on the rulings of [OQ-DP8](declaration-parity.md#OQ-DP8) and
[OQ-DP9](declaration-parity.md#OQ-DP9): the jail daemons run in the Seatbelt guest. The
implementation decisions the build took are in the [Decision ledger](#decision-ledger).

> **In short.** Half of every loophole is a process that runs *inside* the jail. On
> `macos-user` that half now runs in the sandbox: the bundle carries a darwin `yolo-jaild`, the
> launch stages it into `/var/yolo-jail/bin` and starts `yolo-jaild supervise` under the session's
> Seatbelt profile as `_yolojail`. Three shapes of daemon are still declined. The launch prints
> one `Declined:` header and names each declined daemon on its own line under it, with its
> reason (see [Decision ledger](#decision-ledger), JD-3).
>
> **Since 2026-09-29 the two shipped credential adapters no longer serve there.** The OpenAI
> refresh adapter and the AWS credential adapter served in the guest until the doorway rule
> ([HS-D15](host-notch-services.md#HS-D15)) moved them outside the sandbox, as listeners the
> launch owns. A daemon that declares such a host argv is a fourth declined shape: each adapter
> is named on its own line under the same `Declined:` header, marked as running outside the
> sandbox for this launch. The supervisor stays for a
> pack that declares a jail daemon without a host argv, which no shipped pack now does, and
> `TestMacosUserJailDaemonRunsConfinedInTheGuest` measures it with a local pack.

**Needs your ruling:** none. One question the build surfaced is recorded as a follow-up, not
decided here: whether the wire bridge's jail daemon should also move into the guest
([JD-4](#decision-ledger)).

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
([`loopholes.md`](../../userguide/guides/loopholes.md)). **Not** a `host_daemon`, which `macos-user` has started
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

**What a user actually experiences today.** A launch line naming three declined daemons, and then
three failures that arrive later and elsewhere — a bare `"packs": ["claude"]` on this backend
selects three jail daemons and starts none:

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

**Why it cannot just be switched on.** The payload naming the daemons used to be built inside the
container argv assembler, where `macosuser` — a different path — never saw it. Hoisting the
composition above the backend dispatch was the approved mechanism
([`DP-L3`](declaration-parity.md#decision-ledger)) and is most of steps 1 and 2; that hoist
shipped, and it bought the decline rather than a daemon. What `DP-L3` does not settle is the two
things a builder cannot proceed without, and both are now filed:

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
`openai-auth` and `wire-bridge` **unconditionally** and declares a loophole of its own, so a bare
`"packs": ["claude"]` on this backend selects three jail daemons and starts none. Two of the three
are what steps 3 and 4 would fix; the third is declined for good ([Don't](#dont)). The launch
names each of them:

| Declaration | `jail_daemon.cmd` | Native state |
| :--- | :--- | :--- |
| `openai-auth-broker` loophole (`default_enabled: true`) | `yolo-jaild openai-auth-adapter --listen 127.0.0.1:1460` | host daemon starts **fail-closed**; `packs/codex` still sets `CODEX_REFRESH_TOKEN_URL_OVERRIDE` at that dead port |
| `wire-bridge` service | `yolo-jaild wire-bridge` | endpoint file published and ACL-granted, nothing listening |
| `claude-oauth-broker` loophole (`default_enabled: true`) | `yolo-jaild oauth-terminator` | **out of scope** — see [Don't](#dont) |
| `hello-daemon` loophole (`default_enabled: false`) | `{jail_loophole_dir}/bin/hello` | token resolves to a container mount path |

## Map

| Path | Change |
| :--- | :--- |
| [`internal/loopholes/runtime.go`](../../internal/loopholes/runtime.go) | **done** — the composition is the exported `Set.JailDaemons`, which `runtimeArgsFor` calls, so there is exactly one composer and no ungated twin |
| [`internal/cli/run/run.go`](../../internal/cli/run/run.go) | **done** — `jailDaemonsFor` is a statement of `Run`'s own body, **above** the backend dispatch; both arms read that one value |
| [`internal/cli/run/assemble.go`](../../internal/cli/run/assemble.go) | **done** — assembly only SERIALIZES the hoisted payload; it no longer calls `serviceJailDaemons` itself |
| [`internal/cli/run/jaildaemondecline.go`](../../internal/cli/run/jaildaemondecline.go) | **done** — the decline printer, and it landed here rather than in `macosuser`: it reads the composed payload, so it needs nothing native |
| [`internal/macosuser/jaildaemon.go`](../../internal/macosuser) | new — native argv resolution and the spawn. **No runnable/not classifier**; see step 2 |
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

- **RESOLVED 2026-09-28 (JD-1, JD-2) — `yolo-jaild` did not exist on a Mac. Constraint, as it stood:** `flake.nix`'s `shippedBinaries` produces
  `bin/linux-<arch>` only, `just install` ships `{yolo}`, and `StageBinaryCommands` stages that one
  binary. Three of the four declarations above name it as `argv[0]`.
- **A symlink does not fix that.** Both binaries dispatch on plain `args[0]`, never `argv[0]` —
  stated in [`cmd/yolo-jaild/main.go`](../../cmd/yolo-jaild/main.go), so `yolo-jaild supervise`
  through a symlink reaches `yolo supervise`, which is not a command.
- **`DP-L3`'s literal prescription starts the daemon unconfined.** `macosuser.DarwinBootstrapArgv`
  ([`runplan.go`](../../internal/macosuser/runplan.go)) has no `sandbox-exec` in it, and
  the bootstrap is a one-shot process that exits — so setting the payload in `buildBootstrapEnv`
  would produce an orphan outside the Seatbelt profile. Advice: treat `DP-L3` as approving the
  hoist and the process model, not the placement.
- **Two container mount paths have no native analogue.** `loopholedecl.JailLoopholeDir` returns
  `/etc/yolo-jail/loopholes/<name>` and [`load.go`](../../internal/loopholes/load.go) substitutes
  `{jail_loophole_dir}` into `cmd` before any backend is known; the container runtime args in
  [`runtime.go`](../../internal/loopholes/runtime.go) mount that dir `:ro` plus the declared `state_files` for any loophole with a jail daemon. The staged pack tree
  (`RunPlan.PackRoot`) is where those bytes actually are here — and the load-time refusal that
  `settings` plus `jail_daemon` must declare `state_files` guards a hazard with no mechanism
  natively.
- **A spawn failure is now readable — it was not when this plan was written.** Until
  2026-09-18 (`eb02ad86`) `supervisor.superviseOne` threw `c.start()`'s error away and retried for
  the life of the jail whatever `restart` said, so a wrong `argv[0]` presented as an empty log and
  no process. It now writes a `spawn failed:` line into the daemon's own log and lets the restart
  policy decide, which is what makes step 4's first run debuggable. See
  [Blockers](#blockers).
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

Steps 1 and 2 shipped 2026-09-18 (`f6387968`) and are kept below because 3, 4 and 5 are written
against them.

1. **Extract the payload producer.** SHIPPED as `loopholes.Set.JailDaemons` — one composer, called
   by `runtimeArgsFor` and by the hoisted site, with `Set`'s origin gate intact (an unapproved
   `SourcePack` contributes nothing) and no ungated package-level twin.
   → `go test ./internal/loopholes ./internal/cli/run`
2. **Hoist the composition above the backend dispatch, and decline natively by name.** SHIPPED —
   `run.jailDaemonsFor` is a statement of `run.Run`'s own body, above the dispatch, and both arms
   read that one value. **The per-entry classifier this step asked for was deliberately not
   built:** on this backend the verdict does not vary, so it would be a function with one branch
   whose second branch was written from guesses
   ([`jaildaemondecline.go`](../../internal/cli/run/jaildaemondecline.go) carries that reasoning).
   The REASON is therefore stated once for the set and the NAMES one per daemon. This closed the
   *silent* half of `DP-B7` and was the only step with no open question under it. Shape (a) of
   [`OQ-DP5`](declaration-parity.md#decision-ledger) — a coded decline plus a banner line, **not**
   a warning. → `go test ./internal/cli/run`
3. **Resolve `argv[0]` natively.** ✅ BUILT 2026-09-28 as [OQ-DP8](declaration-parity.md#OQ-DP8)
   ruled: no rewrite; the declared `yolo-jaild` resolves on the sandbox PATH to a darwin build
   staged into `/var/yolo-jail/bin` (JD-1, JD-2). Whatever the ruling, the
   in-jail daemon entry points must stay one dispatch shared with
   [`cmd/yolo-jaild`](../../cmd/yolo-jaild) — two spellings of it is the drift the transport
   unification exists to end. → `go test ./internal/cli ./cmd/...`
4. **The confined, supervised child.** ✅ BUILT 2026-09-28 as [OQ-DP9](declaration-parity.md#OQ-DP9)
   ruled: `RunPlan.JailDaemonArgv` + the `Deps.StartBackground` seam that starts it and returns a
   stop, called after the provisioning stage and before `RunWithProxy`, stopped on every
   exit path the env-file sweep already covers. Kill the process **group**, matching container
   teardown (JD-5, JD-6).
   → `go test ./internal/macosuser`
5. **Retract the stale prose.** Independent of 3 and 4, and half landed with step 2 — the two
   remaining targets are listed under [Ships with](#ships-with).
   → `uvx vantage-check docs/`

## Verification split

| Step | A Linux `go test` proves | The Mac runner proves | A human must run |
| :--- | :--- | :--- | :--- |
| 1 | **done** — the container argv is byte-identical either way, and the gate still drops an unapproved pack (`TestJailDaemonPayloadArgvIsByteIdentical`, `TestJailDaemonsHonorsTheOriginGate`). Mutation: delete the producer call from `runtimeArgsFor` and the container test fails | — | — |
| 2 | **done** — that one decline line is emitted per declared daemon, asserted by driving `Run` rather than the printer, so deleting the call from the native arm fails the tests | that the line appears on a real launch | — |
| 3 | **done** — the guest set agrees across `flake.nix`, the bundle script and `macosuser.GuestBinaries`; the bundle stages `bin/darwin-<arch>` into its share dir and the host ship set stays `{yolo}` (`guestbundle_test.go`, `shippedguest_test.go`, `TestFlakeAndLauncherAgreeOnThePrefixLayout`); the guest binaries are staged only into `/var/yolo-jail/bin` | that the Linux-built darwin `yolo-jaild` is executable as the sandbox account | — |
| 4 | **done** — the argv shape: `sandbox-exec -f <profile>` before `yolo-jaild supervise`, wrapped by `ExecWithEnvFile`, no composed value on the argv, the declared `cmd` verbatim in the payload, `PlanInvariants` refusing a payload with no or an unconfined supervisor, the order bootstrap → supervisor → agent → stop, the daemon env file's 0600 and `user:` ACE, and the served set flipping codex's and bedrock's pointers to served (`jaildaemon_test.go`, `macosuserjaildaemon_test.go`, `macosuserguestdaemons_test.go`); for JD-8, the argv sending the supervisor's output to `supervisor.log` inside the profile, the wrapper's own bytes appending both streams under `/bin/sh`, "Started" only after this start's readiness line, and the refusal naming the log and its new lines (`supervisorlog_test.go`, `supervisorcmd_test.go`) | `TestMacosUserJailDaemonRunsConfinedInTheGuest` in [`integration/macosuserjaildaemon_test.go`](../../integration/macosuserjaildaemon_test.go): with a bare `"packs": ["claude"]`, `yolo-jaild` resolves to `/var/yolo-jail/bin`, the OpenAI adapter's log says `serving on` **and carries no `spawn failed:` line**, the host's own `ps` sees `/var/yolo-jail/bin/yolo-jaild supervise` run as the sandbox account during the session (the agent's own `ps` did not list it on the first Mac run), and no supervisor survives the session (`hello-daemon` is declined, JD-3); and `supervisor.log` is readable by the host user and carries the readiness line (JD-8) | two concurrent launches of one workspace, for the `1460` collision; whether Codex actually refreshes through the adapter — no automated test may start an agent; and how long the start takes against JD-8's 1.5 s bound, plus what a real `sudo -n` refusal and a `sandbox-exec` denial print |
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
- **Docs whose claims this makes false**, by path. This is step 5, and it is done:
  - **DONE** — [`macos-user-nix-and-features.md`](../reference/macos-user-nix-and-features.md): the
    "Linux-only boot steps … **the daemon supervisor** … are deliberately **not** run" sentence now
    carries a warning retracting it in place, and the "loopholes: mostly moot" section names both of
    its 2026-09-17 falsehoods (that no host service starts here, and that `EndpointGrantCommands`
    has no call site) as retracted.
  - **DONE** — [`loopholes.md`](../../userguide/guides/loopholes.md): the backend table row no longer gives
    `macos-user` **nothing**. It gives "every host daemon, and no jail daemon", and points at the
    decline printer.
  - **DONE** — [`OQ-T4`](../reference/loophole-transport.md#oq-t4) no longer rests the
    separate-user grant on the backend starting "no host services at all"; it was amended in place
    on 2026-09-24 to say the grant is called. The backend clause of
    [where a loophole does nothing](../reference/loophole-system.md#where-a-loophole-does-nothing)
    now says `macos-user` starts the whole host-service set.
  - **DONE** — [`declaration-parity.md`](declaration-parity.md): `DP-B7`'s silence half is marked
    closed both in its row and in [§11](declaration-parity.md#11-what-i-would-build-in-order)'s
    note, the authority that doc names for which rows a wave closed. `DP-L3` stays **approved and
    unbuilt**, which is correct — steps 3 and 4 are what would build it.
- **Roadmap:** the roadmap's link to this plan, and the reason beside it, are reviewed in the
  same commit as this file's status.
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
  actually run" answer it was waiting on is what step 2 produces — a follow-up, not this work,
  and since built: the launch names each loophole jail daemon its jail runs
  ([`OQ-TP10`](trust-paths.md#oq-tp10--a-wrapped-plugins-hooks-reach-the-agents-lifecycle-and-appear-in-no-launch-banner)).
- **Don't reach for `SO_PEERCRED`** to replace the ACL grant: peer credentials verify, file
  permissions restrict, and restriction is the half a boundary needs
  ([threat model](../reference/loophole-transport.md#threat-model)).
- **Don't touch the supervisor here.** Its spawn-failure fix shipped separately on 2026-09-18
  (`eb02ad86`); a native spawn that fails should be read from its log, not fixed in `supervisor`.

## Decision ledger

The rulings are [OQ-DP8](declaration-parity.md#OQ-DP8) and [OQ-DP9](declaration-parity.md#OQ-DP9).
These are the implementation decisions the build took under them (JD is this plan's own prefix,
coined here).

| ID | Decision | Date | Built |
| :--- | :--- | :--- | :--- |
| JD-1 | **The guest set is `yolo-jaild` alone.** It is the supervisor and every in-jail daemon. `yolo` is staged from the running host binary as before; `yolo-entrypoint` is not needed (the bootstrap is `yolo internal darwin-bootstrap`); the four loophole clients belong to Linux-only loopholes. One list in three spellings — `macosuser.GuestBinaries`, `flake.nix`'s `guestBinaries`, `stage-source-bundle.sh`'s `GUEST_BINARIES` — pinned together by tests | 2026-09-28 | `7b1c1d48` |
| JD-2 | **The guest prefix is `/var/yolo-jail/bin`, and the staged `yolo` moved into it.** `SandboxPath` derives its one entry from the staged `yolo`'s directory, so both names resolve and no PATH list is reordered. A bundle ships `bin/darwin-<arch>` for both release arches; a checkout builds `.#guestPrefix` (a darwin cross-compile from `goSrc`) only when the launch has a daemon to run. A failed build refuses the launch | 2026-09-28 | `068f8fe2` |
| JD-3 | **What the guest declines is one split, `loopholes.JailDaemonsRunIn`**, read by the launch's served set, `yolo check`'s prediction and the decline printer: an intercepting loophole's daemon (the OAuth terminator, keyed on the manifest's `intercepts`), a pack service's (its host half runs instead, notch-convergence NC-D65), and an argv naming the container's `/etc/yolo-jail/loopholes` (so `hello-daemon`, whose files an embedded pack cannot make executable anyway, is declined; `{jail_loophole_dir}` is not parameterised) | 2026-09-28 | `068f8fe2` |
| JD-4 | **The wire bridge keeps its host half on macos-user.** Its jail daemon publishes at `/run/yolo-services`, which the guest has no counterpart of, and NC-D65 already serves it launch-owned. Moving it into the guest would retire that half; it is a follow-up question for the maintainer, not decided here | 2026-09-28 | — |
| JD-5 | **The supervisor reads its own env file**, `/var/yolo-jail/env/<session>.daemons.env`, installed like the session file (0700 directory, content on stdin, 0600, one `user:_yolojail` read ACE) and swept after the supervisor stops. It carries the payload, the shared channel values, every caller token its daemons demand — a scoped one exported, since no agent reads this file — and their endpoints. The agent's session file gains the unscoped tokens a container's shared channel exports | 2026-09-28 | `068f8fe2` |
| JD-6 | **Started after the provisioning stage and before the agent, stopped after it.** `sudo -n` (it must fail, not prompt, beside the agent's terminal), its own process group, SIGTERM then 10 s then SIGKILL; `sudo` relays the SIGTERM. The launch prints one line naming the daemons it started | 2026-09-28 | `068f8fe2` |
| JD-7 | **macos-user picks served addresses** for the daemons its guest runs, because it shares the Mac's loopback — which closes the `1460` collision trap above for two concurrent launches | 2026-09-28 | `068f8fe2` |
| JD-8 | **The supervisor's own stdout and stderr go to `<ws>/.yolo/home/local/state/yolo-jail-daemons/supervisor.log`, and "Started" waits for its readiness line.** They were `/dev/null`, so a `sudo -n` refusal, a `sandbox-exec` denial or an exec failure left no trace while the launch still printed "Started". A `/bin/sh` wrapper inside `sandbox-exec`, ahead of the env-file reader, makes the directory and appends both streams to the log, so the guest writes it as the sandbox account and the host user reads it through the workspace's inherited `group:_yolojail` ACEs, the model the daemons' own logs already use. It appends rather than truncates: the per-workspace launch lock covers the start, so the launch reads only the bytes this start added. `yolo-jaild supervise` writes one readiness line (`supervisor.StartedLinePrefix`) before it starts anything. The launch waits up to 1.5 s for that line or for the process to exit. A failure exits within milliseconds, so the bound only limits a start that is still running and has said nothing. An exit refuses the launch and quotes the log's new lines plus what sudo or `sandbox-exec` printed before the log took over, which the launcher now captures instead of discarding. A process still running and silent at the bound is reported as unconfirmed, and the launch goes on | 2026-09-29 | — |

## Blockers

**RESOLVED 2026-09-28**, both ruled and built. **FILED 2026-09-21** as [OQ-DP8](declaration-parity.md#OQ-DP8) and
[OQ-DP9](declaration-parity.md#OQ-DP9), against
[`DP-L3`](declaration-parity.md#decision-ledger) — which is where they belong, because a plan is
not where a decision hides. They had been stated only here for four days, which is why the roadmap's 💬 row for them
carried no question id and looked like a row with nothing to rule.

Steps 3 and 4 are not buildable until both are answered. The full stakes, the candidate table and
the leanings live with the questions; in one line each:

1. **[OQ-DP8](declaration-parity.md#OQ-DP8) — how a declared `jail_daemon.cmd` resolves with no
   image.** Also decides whether `{jail_loophole_dir}` becomes backend-parameterised, which is
   what the `hello-daemon` subject — the only one needing no credential and no network — turns on.
2. **[OQ-DP9](declaration-parity.md#OQ-DP9) — whether the daemon runs under the Seatbelt
   profile.**

**Dependency — CLEARED 2026-09-18 (`eb02ad86`):** the supervisor swallowing a spawn failure. It
now logs `spawn failed:` and obeys the restart policy, so step 4's first run is readable.

**Adjacent:** whether an official pack can ship an executable at all — an embedded pack's files
come back `0444` from `embed.FS`. That bounds the `hello-daemon` subject to a path-configured pack
and is [`OQ-BP5`](broker-as-a-pack.md#OQ-BP5)'s, decided 2026-09-30 as [BP-D1](broker-as-a-pack.md#BP-D1): a digest-pinned download cached with its exec bit, which an embedded pack can carry.
It still holds after the embedded tree became one leased on-disk tree per build: that tree is
sealed read-only (`packload.sealEmbeddedTree`), so its files are still `0444`.

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

**Design:** [`declaration-parity.md`](declaration-parity.md) — `DP-B7` is the defect, `DP-L3` the
approved mechanism, both in
[§6](declaration-parity.md#6-alignable-with-the-mechanism-and-its-cost). **There is no design doc
for this work**: that one is a catalog, it delegates every macos-user row to
[`macos-user-nix-and-features.md`](../reference/macos-user-nix-and-features.md), and it names no
owner for the loophole lifecycle — so the vocabulary comes from
[`loophole-system.md`](../reference/loophole-system.md),
[`loophole-transport.md`](../reference/loophole-transport.md) and
[`wire-bridge.md`](../reference/wire-bridge.md). This file hangs off `DP-L3` and owes two rulings
back to it ([Blockers](#blockers)).

**Status:** DESIGN, 2026-09-17. Steps 1, 2 and 5 are buildable cold; steps 3 and 4 are not.
Written against `6ded2789`, 2026-09-17. **Precedence:** the design wins on behavior, the tree wins
on fact, this file is advice and is the first thing to be wrong — an overtaken line is a note to
correct, never a spec to satisfy.

A **jail daemon** here is the `jail_daemon` manifest key: a process the framework supervises on the
jail side of a loophole or a `kind: "service"` pack contribution
([`loopholes.md`](../guides/loopholes.md)). Not a `host_daemon`, which this backend already starts,
and not the agent.

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
- **Don't start `oauth-terminator` natively.** Its default port is 443
  (`internal/oauthterminator/oauthterminatorcmd.go:37`), unprivileged only because the container
  runs as UID 0, and its interception needs the `--add-host` this backend cannot emit. Decline it
  by name in step 2 and leave it declined.
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

Two questions, and **neither is filed anywhere** — not in
[`declaration-parity.md`](declaration-parity.md), whose seven questions are all ruled and none of
which names `DP-L3`, and not in
[`macos-user-nix-and-features.md`](../reference/macos-user-nix-and-features.md). They are not
forked into this file, because a plan is not where a decision hides. File them as `OQ-DP` questions
against `DP-L3` first; steps 3 and 4 are not buildable until then.

1. **How a declared `jail_daemon.cmd` resolves on a backend with no image.** Three candidates, and
   the choice is visible outside the code: give `yolo` the in-jail dispatch and rewrite `argv[0]`;
   ship `yolo-jaild` for darwin, against "the host ship set is just `{yolo}`"; or stage a shim at
   the declared name so the argv is untouched. It also decides whether
   `{jail_loophole_dir}` becomes backend-parameterised, which is what `hello-daemon` — the only
   subject that needs no credential and no network — turns on. **Stop and ask.**
2. **Whether the daemon runs under the Seatbelt profile.** The faithful reading of "in-jail" says
   yes; `DP-L3` says "an ordinary child" and is silent. Getting it wrong puts a pack-declared
   process outside the only confinement this backend has. **Stop and ask.**

**Dependency, not a blocker:** 📦 row 2 of [`roadmap.md`](../plans/roadmap.md) — the supervisor
swallowing a spawn failure. Land it first; otherwise step 4's first run reports an empty log and no
process for any cause, which is unreadable.

**Adjacent:** whether an official pack can ship an executable at all — an embedded pack's files
come back `0444` from `embed.FS`. That bounds the `hello-daemon` subject to a path-configured pack
and is 💬 row 17 of [`roadmap.md`](../plans/roadmap.md).

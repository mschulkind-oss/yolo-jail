---
title: "OrbStack Podman host — checked build handoff"
date: 2026-10-06
status: in-review
stage: DESIGN
next: "Build the independently settled jail-stdin slice; setup ownership waits on [OQ-ORB1](orbstack-as-a-podman-host.md#OQ-ORB1)"
depends-on:
  - orbstack-as-a-podman-host.md#OQ-ORB1
tags: [plan, macos, orbstack, podman]
summary: "Source map, component boundaries, first buildable slice and native runner proof for OrbStack support."
vantage:
  status-chip: true
---

# OrbStack Podman host — checked build handoff

**Status:** 2026-10-06. Source map checked against `d5bc7a4188badeb56e1a2cb5591916bf69249723`.
The [research](orbstack-as-a-podman-host.md) owns behavior and evidence; this plan is implementation
advice, and is the first thing to correct when the tree moves. Precedence: research rulings win on
behavior, the current tree wins on facts, this handoff is advice.

## Build boundaries and owners

Each component has one later implementation owner; keep their file sets separate. Run components
independently where noted. The [setup surface](orbstack-as-a-podman-host.md#OQ-ORB1) is not settled;
no command spelling or machine-ownership behavior is authorized yet.

| Component / sole owner | Files owned | Independent proof |
| :--- | :--- | :--- |
| **A. Setup, connection and image delivery.** One implementation owner after the ruling; that owner owns any new setup verb and its check diagnosis. | `internal/cli/check/sections_macos_platform.go`, `internal/cli/check/sections_macos_platform_test.go`; the CLI dispatch/help files selected by [OQ-ORB1](orbstack-as-a-podman-host.md#OQ-ORB1); `internal/cli/run/preflight.go` only if connection readiness needs a launch gate; `internal/runtime/machineshares.go` only if a new share source is required; `internal/image/autoload.go`, `internal/image/storespec.go` only if the native run disproves archive delivery. | Fake-runner tests for non-mutating diagnosis and setup refusal/repair; native test asserts the selected Podman connection reaches an OrbStack kernel and the archive-loaded image launches. Existing archive behavior is the default until disproved. |
| **B. Mounts and jail I/O.** One owner for jail-main stdin handling; no setup or CI workflow edits. | Owns `internal/cli/run/jailmain.go`, `internal/cli/run/jailmain_test.go`. Read-only bounded caller audit: `-i` in `assemble.go`, `jailmain.go`, `run.go`, and stdin forwarding in `proxy_other.go`; the audit does not transfer ownership of other components' files. Touch session callers only if a reproducer shows their intended EOF behavior is wrong. | A unit regression distinguishes an open, unwritten jail-main input channel from EOF. Native proof covers the fresh launch's output through OrbStack's direct SSH proxy, then independently proves first-session and attach commands still receive EOF when the invoking stdin is closed. |
| **C. Host daemon reachability.** One owner for the host-loopback contract, runtime fact flow and endpoint advertisement; no setup or CI workflow edits. | `internal/cli/run/hostloopback.go`, `assemble.go`, `podmanready.go`, `loopholesruntime.go`, `keeperplan.go`, `keeperspawn.go`, `keeper.go` and their source-named tests; `internal/svcendpoint/listen.go` and its tests; `internal/entrypoint/reachability.go` and its tests only if disposition wiring changes. | Caller-level regressions prove a positively identified OrbStack Podman answer survives readiness and fresh-Options keeper construction, sets the assembled jail disposition, and reaches a spawned loopback-TLS daemon as its advertised host. Native jail dials at least one enabled loopback-TLS endpoint and agrees with the boot witness. |
| **D. Native CI.** One owner for runner workflow and its source-named integration tests; no production source changes. | New `.github/workflows/orbstack.yml`; new `integration/orbstack_test.go`. The generic self-hosted trigger scan already finds every workflow and needs no allowlist edit. | On the existing `[self-hosted, apple-container]` Mac, dispatch the OrbStack-only integration selection with an explicit `CONTAINER_CONNECTION`; assert OrbStack kernel identity, completed image load, workspace read/write, first-session output and EOF for first-session/attach inputs, plus host-service reachability. |

Components B, C and D can be implemented independently once the build order reaches them. Component A's
setup API and ownership stop at [OQ-ORB1](orbstack-as-a-podman-host.md#OQ-ORB1); do not build an assumed command. A manual connection alone is
not the supported result. Docker remains an out-of-scope fallback until Podman is shown unable to meet
ORB-D1; the permission to use it is not permission to restore the removed backend.

## Reuse and traps

- `startJailMain` in [`jailmain.go`](../../internal/cli/run/jailmain.go) sets `exec.Cmd.Stdin` to
  `nil`, which is `/dev/null`; its comment says the hold reads nothing. Keep stdout/stderr pipes,
  `readyRelay`, process-group setup and exit/drain behavior unchanged when fixing stdin. The
  open-but-unwritten input is only for this container-main client lifetime, so the Podman `run -i`
  channel stays open after the host client receives EOF while boot output is relayed.
- The other interactive callers are distinct: `assemble.go` adds `-i` to the container main command,
  `jailmain.go` adds `exec -i` for the first session, and `run.go` adds `exec -i` for attach; the
  non-Linux `runWithProxy` and `runArmedSession` in `proxy_other.go` forward `os.Stdin` (the former
  currently has no production caller). Audit these bounded call sites separately. First-session and
  attach commands intentionally receive the invoking stdin, including EOF; do not apply the held
  main-process pipe to them or change EOF semantics without a reproducer.
- [`jailmain_test.go`](../../internal/cli/run/jailmain_test.go) already launches a stand-in process,
  waits for `BootReadyLine`, and verifies output and exit. Extend that fixture for the open-channel
  invariant rather than adding an OrbStack-only unit harness. The integration harness leaves
  `exec.Cmd.Stdin` unset (therefore `/dev/null`/EOF); native fresh-launch and attach cases must keep
  proving that such session input reaches EOF and does not hang.
- [`runtime.ReadMachineShares`](../../internal/runtime/machineshares.go) deliberately returns
  unknown without probing when `CONTAINER_CONNECTION` or `CONTAINER_HOST` is set. Unknown means no
  false refusal; it is not proof the selected machine shares the workspace. Preserve this tri-state.
- [`sections_macos_platform.go`](../../internal/cli/check/sections_macos_platform.go) currently
  reports Podman Machine readiness via `podman machine info`; a remote Podman connection is not
  evidence that this is the selected runtime. Any new diagnostic must distinguish the selected
  endpoint instead of declaring OrbStack unavailable because it is not Podman Machine.
- [`internal/image/autoload.go`](../../internal/image/autoload.go) sends podman-on-macOS through
  `deliverViaArchive`: the runtime's own `podman load -i` writes inside its VM, where a local Mac
  `containers-storage:` copy cannot land. Retain the archive path unless native OrbStack proves it
  fails; the content-addressed OCI loader and one full-archive retry already exist.
- [`hostloopback.go`](../../internal/cli/run/hostloopback.go) owns the assembled `YOLO_HOST_LOOPBACK` disposition/facts, but it currently returns empty facts on macOS. Podman readiness only stores `podmanFacts` for the non-machine case in [`podmanready.go`](../../internal/cli/run/podmanready.go), so the selected remote endpoint's answer must be deliberately carried into this path rather than queried inconsistently.
- The daemon's published host is selected by `advertiseHostFor` in [`loopholesruntime.go`](../../internal/cli/run/loopholesruntime.go), called by `startLoopholesMatching` there and forwarded as `YOLO_SVC_ADVERTISE_HOST` to the loopback-TLS child. The host services start in the separate keeper, whose fresh `Options` are built by [`newKeeper`](../../internal/cli/run/keeper.go); the launch-to-keeper plan in [`keeperplan.go`](../../internal/cli/run/keeperplan.go) / [`keeperspawn.go`](../../internal/cli/run/keeperspawn.go) must carry any launch facts required by that decision.
- [`svcendpoint/listen.go`](../../internal/svcendpoint/listen.go) consumes the explicit `YOLO_SVC_ADVERTISE_HOST` override and otherwise uses its existing default. The OrbStack-specific host and the jail's disposition must come from the same positively identified facts; test the real assembled disposition and daemon-spawn caller, not only helper return values.
- A Linux/nested run cannot validate OrbStack's SSH proxy, macOS path sharing, native connection,
  host loopback, license or native runner. No Linux green substitutes for the native workflow.

## Build order

1. **B0 — first buildable slice, after adding the failing test.** Add a unit regression proving the
   jail-main Podman run client keeps stdin open with no writes until that client exits; run it and
   observe failure against the current `/dev/null` assignment. Then give only that client an open,
   unwritten pipe whose lifetime ends with it, so OrbStack can relay stderr after its SSH client
   half-closes stdin. Do not change the first-session or attach `exec -i` input: they keep the
   invoking session's stdin/EOF behavior. Audit the bounded `-i` call sites above and cover fresh
   launch plus attach EOF behavior in the native suite. Run `go test ./internal/cli/run -run
   'Test.*MainProcess'`. The runtime-independent assertion is necessary but not sufficient: retain a
   native proxy check in D.
2. **A — setup and remote image path, after [OQ-ORB1](orbstack-as-a-podman-host.md#OQ-ORB1).** Add the ruled first-party setup/check behavior
   without claiming or rewriting an existing machine/connection absent explicit consent. Prove setup
   diagnosis with fake command runners. On native Mac, use an explicitly selected Podman connection;
   confirm `podman info` reports the OrbStack guest kernel before running the integration launch. Keep
   Mac Podman image delivery as an OCI archive through `podman load -i`; change it only with a failing
   native reproducer. Run focused `internal/cli/check`, `internal/runtime`, `internal/image` tests.
3. **C — OrbStack host services.** Carry the positively identified OrbStack Podman facts from readiness through `hostLoopbackFactsFor`, into the assembled `YOLO_HOST_LOOPBACK` disposition and across the fresh launch's `keeperPlan` to its new `Options`. The daemon caller must use the same facts: `startLoopholesMatching` calls `advertiseHostFor`, and `startExternalService` passes the resulting host to a loopback-TLS child. Add regressions through those production callers proving the assembled disposition and daemon's `YOLO_SVC_ADVERTISE_HOST` agree; do not test only isolated helpers. Run `go test ./internal/cli/run ./internal/svcendpoint ./internal/entrypoint`, then verify enabled endpoint dials from a real jail on OrbStack. A nested jail is structurally blind to this reachability class.
4. **D — native proof.** Add the dispatch-only workflow and dedicated `TestOrbStack…` integration cases after B and C. Run the exact selection from the runner procedure below; do not substitute the general macOS unit workflow or an ordinary `podman machine` job. Run `go test -short ./integration` locally for compilation/fast guards and the targeted native test on OrbStack. Full integration remains the landing owner's combined-tree gate.

## Native runner contract

- Use the existing Apple-Silicon self-hosted Mac runner (`runs-on: [self-hosted, apple-container]`),
  where OrbStack is already installed. The separate workflow must be **`workflow_dispatch` only**,
  include `if: github.repository == 'mschulkind-oss/yolo-jail'`, and declare `permissions: contents: read`.
  Its required `workflow_dispatch.inputs.connection` is a non-secret Podman connection name. The generic
  `integration/selfhostedtriggers_test.go` checks both protections. Do not add push,
  pull-request, schedule, or other fork-causable triggers.
- Dispatch is manual, by the exact branch/ref, from an authenticated maintainer account with
  repository write and Actions-workflow-dispatch permission (the existing runner runbook uses the
  user's `gh` login; it does not read a token into the job):

  Define the reviewed ref and the non-secret name of the already-configured direct connection, then
  dispatch and watch:

  ```console
  $ TEST_BRANCH=reviewed-branch
  $ ORBSTACK_CONNECTION=the-direct-connection-name
  $ gh workflow run orbstack.yml --repo mschulkind-oss/yolo-jail --ref "$TEST_BRANCH" \
      -f connection="$ORBSTACK_CONNECTION"
  $ gh run watch
  ```

  A deliberate repeat uses `gh run rerun <run-id>`. The dispatcher's GitHub credential is used only
  to request the run and is not passed to it. The job's `GITHUB_TOKEN` is `contents: read`; no
  runner-admin PAT, repository secret, or workflow write token is needed. `workflow_dispatch` runs
  branch-supplied code on the maintainer's account, so only reviewed, trusted branch code may be
  dispatched; the repository guard is still required even though dispatch is write-restricted.
- The workflow requires a non-secret `connection` dispatch input and passes it through `env`, never
  interpolated into shell source. Its checkout and Go setup match the existing workflow: checkout
  with `set-safe-directory: false`, then `actions/setup-go@v7` from `go.mod`. The actual test run must
  reuse the macOS source-bundle/prefix route and must not use a live-checkout `/nix/store` prefix,
  which the Podman VM may not share. Mirror the existing `apple-container.yml` staging and image
  preparation sequence, with Podman selected for delivery:

  ```sh
  set -euo pipefail
  test -n "$CONTAINER_CONNECTION"
  scripts/stage-source-bundle.sh "$RUNNER_TEMP/yolo-flake-bundle"
  SPEC="$(scripts/mac-ac-linux-builder.sh)"
  export NIX_CONFIG="builders = $SPEC"
  printf 'NIX_CONFIG=builders = %s\n' "$SPEC" >> "$GITHUB_ENV"
  image="$(nix build --impure --accept-flake-config --no-link --print-out-paths \
    .#ociImage --builders "$SPEC")"
  copier="$(nix build --impure --accept-flake-config --no-link --print-out-paths \
    .#imageCopier --builders "$SPEC")"
  archive="$(mktemp -t yolo-orbstack-image.XXXXXX.oci)"
  rm -f "$archive"
  trap 'rm -f "$archive"' EXIT
  "$copier/bin/skopeo" --insecure-policy copy \
    "nix:$image" \
    "oci-archive:$archive:yolo-jail:latest"
  CONTAINER_CONNECTION="$CONTAINER_CONNECTION" podman load -i "$archive"
  ```

  Build both the image and its separate copier from [`flake.nix`](../../flake.nix), using the
  returned store paths rather than assuming preexisting output links. Require image realization
  and `podman load -i` to succeed before tests; the integration harness
  deliberately does not load an absent image on a real host. This OCI-archive seed uses the selected
  remote connection rather than `containers-storage:` on the Mac; the actual launch resolves its
  content-addressed image ref from the same realized image and uses yolo's archive delivery path.
  Pass the staged bundle as `YOLO_REPO_ROOT` to the targeted test so the jail's mounted Linux binary
  prefix and flake bundle are both reachable from OrbStack. `scripts/mac-ac-linux-builder.sh`
  prepares the builder and supplies its current address; the checkout's image is realized through it,
  then the OCI archive is loaded into the selected Podman connection. The OrbStack test selection is
  uncached (`-count=1`):

  ```sh
  YOLO_REPO_ROOT="$RUNNER_TEMP/yolo-flake-bundle" \
    CONTAINER_CONNECTION="$CONTAINER_CONNECTION" YOLO_RUNTIME=podman \
    go test -count=1 -timeout 0 ./integration -run '^TestOrbStack'
  ```

  The workflow's separate proof step checks the selected endpoint as follows:

  ```yaml
  env:
    CONTAINER_CONNECTION: ${{ inputs.connection }}
    YOLO_RUNTIME: podman
  run: |
    test -n "$CONTAINER_CONNECTION"
    facts=$(podman info --format '{{.Host.Security.Rootless}}|{{.Host.Kernel}}')
    test "${facts%%|*}" = true
    kernel=${facts#*|}
    printf 'Selected Podman guest kernel: %s\n' "$kernel"
    case "$kernel" in *-orbstack*) ;; *) echo "not an OrbStack Podman endpoint" >&2; exit 1 ;; esac
  ```

  The integration selection sets both `YOLO_RUNTIME=podman` and the explicit
  `CONTAINER_CONNECTION` for every host Podman/yolo subprocess, and first asserts the selected
  endpoint is rootless and runs the OrbStack guest kernel. Fail closed if no connection is named or
  either fact differs. This proves the requested OrbStack path, not whichever Podman endpoint happens
  to be default.
- The dedicated integration test then exercises the actual yolo launch and asserts all outcomes:
  `podman system connection list --format json` maps the input connection to OrbStack's SSH proxy
  at port 32222, not the port-2222 tunnel workaround; `podman info` says rootless with the OrbStack
  kernel; an uncached image variant is archived into that connection and runs; the jail sees the
  Mac workspace and a jail-written sentinel appears there; boot/first-session output reaches the
  test process even though the main launch client input is held open; first-session and attach
  commands still see EOF from a closed invoking stdin and exit; and an enabled loopback-TLS service
  dials from inside the jail with the witness agreeing. Parse only the selected connection's
  name/URI and never print its identity material or endpoint bearer-token contents. Start no
  interactive agent or treat a generic Podman jail as OrbStack evidence.
- Native execution is the only proof of the OrbStack connection, SSH half-close, mount/ownership and
  host-daemon loopback. Linux unit/CI runs prove only their host-independent logic. Functional success
  does not establish commercial-license compliance; the maintainer owns that prerequisite. The
  workflow does not install OrbStack, alter its machine configuration, create/remove connections,
  or run `orb create`; those management mutations await
  [OQ-ORB1](orbstack-as-a-podman-host.md#OQ-ORB1) and explicit owner consent. It does launch
  containers, load the temporary image variant and write disposable test workspaces on the selected
  Podman endpoint.

## Ships with

- **Unit:** open-but-unwritten child stdin versus EOF; normal child exit closes the retained pipe; boot
  refusal still drains final output; no deadlock or leak when the child exits early.
- **Native integration:** explicit OrbStack connection/kernel guard; workspace bind and write ownership;
  output after the main launch client's stdin EOF; separate fresh first-session and attach EOF proofs;
  image realization/load/launch; at least one enabled host endpoint and witness. Keep these source-named
  under `integration/` and run serially (no `t.Parallel`).
- **Rewrite:** any current `startJailMain` assertion that assumes nil stdin/`/dev/null`; preserve existing
  output, readiness, exit status and process-group assertions.
- **Docs:** update [`orbstack-as-a-podman-host.md`](orbstack-as-a-podman-host.md) from proposed work to
  built evidence only after native success. No roadmap or changelog edit belongs to this component.
- **No new runtime, Docker backend, package dependency, public environment dial or config key** unless a
  separate ruling explicitly requires it.

## Blocker

- **Stop before setup API, machine lifecycle or connection ownership.** The maintainer must rule
  [OQ-ORB1](orbstack-as-a-podman-host.md#OQ-ORB1). The closed-stdin slice B0 and the source/test map are
  ready without that ruling; setup implementation is not.

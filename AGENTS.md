# YOLO Jail: Agent Developer Guide

yolo-jail manages the environment a coding agent works in — the agents, their config, skills and
house rules, tools and credentials — declaratively and per workspace, and renders it wherever the
agent runs. Confinement is one setting of that description: a jail (a container, or a dedicated
macOS account) is the strongest and the default, and `yolo host` is none.

**Describe yolo environment-first and confinement second** in every tagline, summary and guide.
The charter is [`what-yolo-is.md`](docs/reference/what-yolo-is.md), ruled in its
[one-sentence answer](docs/reference/what-yolo-is.md#the-one-sentence-answer).

**This file is the guide for developing yolo-jail itself**, and it is deliberately terse: each rule keeps
its imperative, the file that enforces it, and its escape hatch — the linked authority carries the
measurement and the history that produced it. Where this file and a linked authority disagree, the
authority wins. **Keep it that way:** a new entry is one rule plus its enforcer and hatch; a rule a test
already enforces with a clear failure needs only the test's name. Usage and config reference material is
not here; see [Where things live](#where-things-live).

## Packs, and what core does not know

**AGENTS ARE PACKS. Core does not know what an agent is.** No agent registry, no `agents` config key, no
`YOLO_AGENTS`. Config carries ONE list of `packs`; shipped packs live in `packs/*/pack.toml` and are
selected by BARE NAME — `packs = ["claude"]`, which `yolo pack add claude` writes.
[`internal/config/validate.go`](internal/config/validate.go) hard-errors on `agents` on the host (and
warns in-jail, where the config is the generated snapshot).

**A pack that installs an agent is just one that declares a `[[program]]` surface** (`kind: "program"` in the legacy `pack.json` spelling, which is
still read), and most shipped packs install no CLI at all. `rg -l '^\[\[program\]\]' packs/*/pack.toml`
lists the packs that install something and `rg -l loophole packs/*/pack.toml` the loophole list; writing either membership down here is
what rots. Two structural facts, not guessable from a manifest:

- `codex-auth` declares the `openai-codex` PROVIDER (its capabilities, its Responses endpoint and
  the subscription's ONE model list — no credential pointer) and the `codex` profile selecting it,
  rather than an agent pack owning it. Consumers render that list, so a model is added there and
  nowhere else ([ML-D1](docs/design/model-lists-and-pickers.md#ML-D1)).
- `wire-bridge` is the only `kind: "service"` pack — one in-jail daemon and its endpoint file, joined to a
  launch by any SELECTED pack whose `needs` names it. `packs/claude` names it UNCONDITIONALLY; `cerebras`
  and `kilo` only when their `when_bins` lists `claude` or `copilot`
  ([`needs`](docs/reference/wire-bridge.md#needs--a-conditional-pack-dependency)).

**Every loophole yolo ships is a pack's, and there is no other channel**
([`OQ-A10`](docs/reference/loophole-system.md#oq-a10)). `claude-oauth-broker` is a contribution of
`packs/claude-auth`, which `packs/claude` needs, not a pack of its own
([OQ-PE5](docs/design/provider-enablement.md#OQ-PE5)). The subscription packs are named
`<agent>-auth` and their profiles for the agent; a former pack or profile name is refused naming
the new one through the manifest's `formerly` key ([PE-D7](docs/design/provider-enablement.md#PE-D7)). Do not reintroduce `bundled_loopholes/`,
`paths.BuiltinLoopholeNames` or `loopholes.ReservedLoopholeNames`; the top-level `journal` and
`host_processes` config keys are refusals naming their replacements, and `publishes: "endpoint"`,
`jail_env` and an absolute `requires.file_exists` are refused for every manifest yolo reads.

**Nothing is active by default**: an empty config yields a jail with no coding agent, and says so at launch
(`run.warnIfNoPacks`). The one exception is a pack whose `pack.toml` sets `selected_by_default`, honored for
shipped packs only and set only on `cgroup-delegate`, which can only limit the jail
([PD-13](docs/reference/loophole-grants.md#PD-13)).

A pack declares (`internal/packdecl`, read through `internal/packload`) an install spec, mounts,
writable/shared dirs, host-file grants, composed `surfaces`, launch flags and named `hooks`. The boot path
renders every one in a single loop (`entrypoint/surfaceloop.go`, which `yolo check` and `yolo host apply`
run too) with **no switch on any tool name**. Three rules before debugging it — the mechanics are in
[`pack-system.md`](docs/reference/pack-system.md):

- **Each launch stages its OWN tree of the SELECTED packs** (`stagePacks`,
  [`packtree.go`](internal/cli/run/packtree.go), [`OQ-PK2`](docs/reference/pack-system.md#oq-pk2)), and
  ⚠ **an attach NEVER re-stages**: it reads the RUNNING jail's tree (`pack-trees/.live`) for every
  host-side reader, says when its own staging differs, and discards it. A tree is removed only once its
  container is KNOWN gone (`forgetGoneContainer`, tri-state).
- **Name reservation covers only the SELECTED packs**
  ([`OQ-BH14`](docs/reference/jail-home.md#reserving-a-name-is-not-creating-a-directory),
  [`OQ-BH15`](docs/reference/jail-home.md#oq-bh15)): validation resolves the selection itself
  (`resolveSelectedPacks`, [`selectedpacks.go`](internal/config/selectedpacks.go)). **Do not read
  `packload.Embedded*` to reserve a name.** The one shipped-set reader allowed is
  `storage.EnsureGlobalStorage`, which runs before config loads and makes every shipped pack's shared dir,
  a bind source a jail mounts only when it selects the pack.
- **`packload.Embedded()` leases ONE immutable tree per build** under
  `~/.local/share/yolo-jail/embedded-packs/<hash>`
  ([`embeddedcache.go`](internal/packload/embeddedcache.go)). ⚠ **Never move it under `cache/`**, which
  every jail mounts read-write (`paths.EmbeddedPacksDir` states why). An exit path that skips defers must
  call `ReleaseEmbedded`, and a second process-lifetime copy is the bug to watch for: a verb staging a
  configured pack uses the process pack tree (`packload.ProcessPackDir`, filled only by
  [`config.ResolvePack`](internal/config/packresolve.go)).

`agentcfg.BuiltinManifest()` is core's own surfaces only (`mise/config`); callers wanting the full set merge
pack surfaces via `ManifestWith`. `internal/jailcontent` keeps only what was never per-agent: skills
staging, briefing composition, loophole descriptions, the source-tree probe.

Backends are `podman`, `container` (Apple Container) and `macos-user` (macOS Seatbelt, no VM). Docker is
not one; validate.go hard-errors on it.

## Architecture

Every command lives in `cmd/`, and the table below is the list — do not restate a count in prose.
Everything is Go; the only bash/Python left is generated *content* (shims, `.bashrc`) emitted by
`internal/entrypoint` — **no generated in-jail CLIENT**
([`loophole-transport.md`](docs/reference/loophole-transport.md)).

| Binary | Runs where | Role |
|---|---|---|
| `yolo` | host **and** in-jail | the CLI; also every host daemon |
| `yolo-entrypoint` | container PID 1-ish | provisions the jail at startup |
| `yolo-jaild` | container, and the macos-user guest | in-jail daemons |
| `yolo-ps` | container, and the macos-user guest | host-process view (the `host-processes` loophole) |
| `yolo-cglimit` | container | cgroup-delegate client (the one AF_UNIX consumer left) |
| `yolo-journalctl` | container | journal-bridge client (loopback-TLS) |
| `yolo-serial` | container, and the macos-user guest | serial-bridge client (loopback-TLS; the `serial` loophole) |
| `yolo-log` | the macos-user guest (shipped to containers too) | macOS unified-log bridge client (loopback-TLS; the `macos-log` loophole) |
| `goprobe` | nowhere | deployment tripwire; excluded from runtime PATH |

**A new `cmd/` binary must be added to [`flake.nix`](flake.nix)'s `shippedBinaries` AND to
[`scripts/stage-source-bundle.sh`](scripts/stage-source-bundle.sh)'s `SHIPPED_BINARIES`** or it silently
vanishes from the jail or from a shipped bundle while `go build ./...` stays green.
[`shippedclients_test.go`](internal/entrypoint/shippedclients_test.go) pins the spellings together, and
`run.TestFlakeAndLauncherAgreeOnThePrefixLayout` the resulting prefix layout.

**`just install` ships the host `yolo` and publishes a flake-bundle GENERATION**: a fresh
`~/.local/share/yolo-jail/flake-bundles/<stamp>/` with the stable `flake-bundle` symlink swapped at it
(`internal/flakebundle`). **Never hand [`stage-source-bundle.sh`](scripts/stage-source-bundle.sh) the
stable path** — restaging in place deletes pid1 out from under every RUNNING jail, whose bind mount pins
the old inode.

**Reapers never treat "I could not ask the runtime" as "unreferenced"** — a reaper that cannot ask
declines. Generations are collected by liveness, but not every reaper is liveness-only: the nix GC-root
reaper ages out, after one week, every root no container is running on
([`OQ-LS1`](docs/reference/image-retention.md#why-its-this-way),
[`OQ-LS4`](docs/reference/image-retention.md#why-its-this-way)). Pick per reaper.

**Daemons are subcommands, not separate binaries.** Host daemons are hidden self-exec subcommands of
`yolo` (`yolo internal daemon <name>`); in-jail daemons are `yolo-jaild <name>` (`supervise` reads
`YOLO_JAIL_DAEMONS`). Run either group bare for its members. Both dispatch on plain `args[0]` —
**not** argv[0]/symlink.

CLI code lives under `internal/cli` (top level), `internal/cli/run` (the run pipeline) and
`internal/cli/check`.

**Self-bootstrapping:** this project is developed from inside its own jail. `/workspace` is bind-mounted
live, so edits are visible on the host instantly — there is no sync step.

## Build & deploy — the traps

The mechanics behind each of these are in
[`image-staging-vs-baking.md`](docs/reference/image-staging-vs-baking.md).

- `just build-go` → `dist-go/<goos>-<goarch>/` is the **cross-compile-for-shipping** step only; it feeds
  no in-jail run. `just deploy` is `just install` plus Claude-broker priming. `just deploy-dev` is the
  same with `$GOBIN/<name>` a symlink to [`dev-wrapper.sh`](scripts/dev-wrapper.sh), current at every run. Both take an
  optional launcher name (default `yolo`, never `*.test`). The slot is replaced only when it holds a
  yolo build (named by its Go build info, never run before that), a link to one, this checkout's dev
  link, a pre-Go Python script under a `hostmigrate.LegacyNames` name, or a dangling link; anything
  else, another checkout's dev link included, is refused untouched ([`launcher-slot.py`](scripts/launcher-slot.py),
  [`test_launcher_slot.py`](scripts/test_launcher_slot.py)). Deployed launchers share ONE installed
  bundle, so a second deployed name is refused; a deployed `yolo` beside a dev `yolo-dev` is the
  side-by-side pair. `just undeploy [name]` removes one.
- **THE IMAGE DOES NOT CONTAIN YOLO.** `/opt/yolo-jail` is TWO `:ro` bind mounts the launch supplies —
  linux binaries at `bin/`, the flake bundle at `share/yolo-jail/`
  ([`jailprefix.go`](internal/cli/run/jailprefix.go); the image bakes only the mountpoints and
  `/bin/<name>` symlinks, `jailPrefixLinks` in [`flake.nix`](flake.nix)). So a `cmd/`- or
  `internal/`-only commit does not move the image; only [`flake.nix`](flake.nix),
  [`flake.lock`](flake.lock) or `packages:` do. What executes in the jail is host-mutable with no rebuild
  ([the security delta](docs/reference/image-staging-vs-baking.md#the-security-delta)).
- ⚠ **Rootless podman: the image copy runs as `podman unshare -- <copier> copy …` and names the STORE on
  the destination** ([`storespec.go`](internal/image/storespec.go)); rootful takes the bare copy and bare
  ref. The branch is decided from `podman info` BEFORE the copy, never by retrying a failure. On Linux
  podman `just load` copies through `yolo internal image-copy`, never a shell spelling; macOS podman and
  Apple Container take an archive and their own loader, in a launch and in `just load` alike
  (`image.TestJustLoadTakesTheLaunchsDeliveryBranch`).
- **A failed nix build STOPS the jail** — it never falls back to a stale image.
  `YOLO_ALLOW_STALE_IMAGE=1` opts back in. `SkipBuild` is untouched: no build ran, so nothing failed.
- **The `goSrc` fileset trap** ([`flake.nix`](flake.nix)): the hermetic Go build only sees
  [`go.mod`](go.mod), [`go.sum`](go.sum), `vendor/`, `cmd/`, `internal/` and `packs/`. A Go package
  outside that set silently vanishes from the jail while `go build ./...` stays green; add it to the
  fileset by hand. `tools/` and `integration/` are excluded on purpose.
- **A launch REFUSES when the host `yolo` and the source it would mount disagree** across a host↔jail
  contract: `version.SourceSkew` compares the binary's commit stamp against HEAD through the `goSrc`
  fileset + flake files, naming `just install`; `YOLO_ALLOW_SOURCE_SKEW=1` overrules it. It is silent
  for a docs-only commit, for uncommitted work, and for anything it cannot prove. **The
  integration suite cannot see this class** — it always builds a fresh CLI.
- **The cwd does not choose the flake.** A complete bundle beside the running executable wins; an
  installed launcher uses only its matching installed generation. `YOLO_REPO_ROOT` is retired and
  refused. To run the checkout, see [Workflow](#workflow) step 2. A dangling or cyclic dev alias fails
  in the OS before the wrapper starts; repair it with `just deploy-dev <name>` on the host when the
  alias is in `$GOBIN`, or
  `ln -sfn /absolute/path/to/checkout/scripts/dev-wrapper.sh /path/to/yolo-dev`.
- `vendor/` is committed and the nix build is hermetic (`-mod=vendor`, no network). A new dependency needs
  `go mod vendor` committed, or the image build breaks while `go test` passes.
- Image reload sentinel is `BUILD_DIR/last-load-<runtime>` (not `.last-load`). `nix build --impure` exists
  so `builtins.getEnv` can read `YOLO_EXTRA_PACKAGES` from the config's `packages` list.
- **An official pack declares no `binaries`**: every official loophole program is a
  `yolo internal daemon <name>` or `yolo-jaild <name>` subcommand
  ([OQ-BP8](docs/reference/pack-binaries.md#OQ-BP8); `TestNoOfficialPackDeclaresABinary`).

## Testing

- `just test-fast` = `go test -short ./...` — unit tests plus the short-gated compile of `integration/`. No
  containers. `just check-ci` (= `lint-ci` + `test-fast`) is the LANDING gate ([Workflow](#workflow)
  step 4). `just test` adds `go test -count=1 -timeout 0 ./integration`. Run by CI.
- **Run the slow suite once per landing, not once per agent.** A builder or fixer runs the integration
  tests that read what it changed (`rg integration/` for the symbol, flag, env var or message, then
  `go test -run '<those>' ./integration`). A reviewer runs only what reproduces a finding. Whoever lands
  runs the full suite once, on the combined tree.
- **`integration/` rules**: all files are package `integration`, gated by `requireJail(t)` (skipped under
  `testing.Short()`). Do **not** add `t.Parallel()` — the package runs serially by design. The macOS
  nightly shards across separate jobs from `go test -list`, never a hand-maintained `-run` regex. Each `run*` helper honors
  `YOLO_TEST_JAIL_TIMEOUT` (integer seconds, default 300).
- **The suite refuses to run against a STALE jail image**: `ensureJailImage` compares
  `nix eval --raw .#imageIdentity` against `/etc/yolo-jail-image-identity` and aborts with the fix
  command. ⚠ **`imageIdentity` must stay a content hash declared OUTSIDE `eachDefaultSystem`**
  (`TestImageIdentityIsSystemInvariant`, [`imageskew_test.go`](integration/imageskew_test.go)).
  `YOLO_TEST_REBUILD_IMAGE=1` forces a rebuild+reload; `YOLO_TEST_IMAGE_SKEW=warn|off` downgrades the
  check (`fail` is the default on every host, darwin included: there is no per-platform downgrade,
  see the ruling above `parseImageIdentity`). **`git add` before rebuilding** — nix sees tracked
  files only.
- **A test that pins the CALLEE while the CALL SITE is unpinned is not a test** — nothing fails when the
  production caller is deleted. Nor is one asserting the SENTENCE a comment makes rather than the system.
  When adding one, ask: **does it fail if I delete the call site?**
- **The darwin PATH-RESOLUTION class is reproducible on Linux.** On macOS `t.TempDir()` is under a
  symlink (`/var` → `/private/var`), so a fixture path compared against code that resolves symlinks
  passes on Linux and fails on darwin. Reproduce with
  `mkdir -p /tmp/real && ln -sfn /tmp/real /tmp/link && TMPDIR=/tmp/link go test -short ./...`; fix by
  handing out `EvalSymlinks(t.TempDir())` where the path is MINTED.
- **No agent tests.** Automated tests must never start `claude`/`copilot`/`codex`/etc. interactively or make
  API calls. `--version` probes only.
- **Nested-jail verification is mandatory** for `cmd/` and `internal/` changes ([Workflow](#workflow)
  step 2), **except for two classes it gives a FREE GREEN however broken the change is.** Podman-in-podman
  forces `--net=host` and `--userns=host`, so a nested jail can verify neither **reachability**
  (`--network`, [`hostloopback.go`](internal/cli/run/hostloopback.go), `internal/svcendpoint`'s
  bind/advertise, the `host.containers.internal` hop, the in-jail reachability probe) nor **rootless-only
  paths** (ID mapping, `/etc/subuid`, image delivery into a rootless store). Only a REAL jail on a rootless
  host, or CI, settles either; report `podman info --format '{{.Host.RootlessNetworkCmd}}'` or
  `'{{.Host.Security.Rootless}}'` with the claim
  ([the blindness section](docs/reference/loopback-tls-reachability.md#a-nested-jail-is-structurally-blind-to-this)).

## Invariants & gotchas

- **Every stop names the next step.** A refusal, a `yolo check` finding, a launcher line or a command's
  last line that reports a problem also gives the easiest next step that keeps the user's trust: yolo does
  it, asks, prints the one command, or names who can. A message that only reports is a defect
  ([`happy-path-principle.md`](docs/reference/happy-path-principle.md); not to be confused with
  [`fill-the-matrix-principle.md`](docs/reference/fill-the-matrix-principle.md)).
- **Run `yolo check` after every edit** to [`yolo-jail.toml`](yolo-jail.toml) (`yolo-jail.jsonc` where a
  workspace has not converted) or `~/.config/yolo-jail/config.toml` (legacy `config.jsonc`), before asking a
  human to restart. `yolo check --no-build` is the fast in-jail preflight. The y/N startup diff prompt is
  not a substitute.
- **Config is TOML; legacy JSONC is read for a few releases** ([RC-D19](docs/design/readable-config.md#decision-ledger)).
  One format per scope, a TOML file beside a legacy one is refused, and a launch offers
  `yolo config migrate` (`convert_later` in a legacy user file silences it). **Advice names a verb, not a
  file to hand-edit** ([`config-commands.md`](docs/design/config-commands.md)): `yolo allow` (your
  ceiling, host), `yolo request` (a project's ask, in a jail too), `yolo active-provider`,
  `yolo pack add|remove|migrate`, `yolo config set|add|unset|why|migrate`. A new advice string names
  one, or `yolo config set`; `TestConfigRefTOMLSectionNamesTheVerbsTheCLIHas` pins config-ref's list of
  them to the dispatch registry.
- **Nothing is blocked by default; a generated shim is unconditional.** `defaultBlockedList()` is EMPTY;
  `grep -r` and `find` blocks live in the opt-in **`guardrails`** pack. A blocker is generated only when
  its `replacement` is on the agent's PATH (`agentPath`), and `YOLO_BYPASS_SHIMS=1` bypasses one. A user's
  `security.blocked_tools` entry naming the same tool as a pack's REPLACES it whole. ⚠ A shell function
  outranks PATH: Claude Code's Bash tool defines `grep`/`find` as functions, so Claude's own commands
  never meet those blocks ([HE-D11](docs/reference/host-agent-environment.md#he-d11)).
- **Use `shquote.Join`** (`internal/shquote`) for anything crossing into the container's `bash -c`.
- **A LAUNCH HAS NO QUIET MODE, by ruling** ([`OQ-RO3`](docs/reference/report-tiers.md#why-its-this-way)).
  Progress may be compressed; a **disclosure is never suppressible** — the pack read/exec banners are the
  trust boundary. `YOLO_NO_BANNER` hides the version line and nothing else, and
  `TestTheLaunchHasNoQuietFlag` fails if another flag appears on `runFlags`. Everything printed is teed to
  `<workspace>/.yolo/launch.log`, beside the entrypoint's `boot.log`.
- **Podman-in-podman**: inside a container the CLI uses `--userns=host` and forces `--net=host`; inner
  containers also need `--cgroups=disabled` — both are image defaults in
  `/etc/containers/containers.conf`.
- **Host loopback forwarding and the reachability witness**
  ([`loopback-tls-reachability.md`](docs/reference/loopback-tls-reachability.md)). On a rootless podman
  with a recognised `rootlessNetworkCmd`, the default `network.mode: bridge` adds the option forwarding
  the host's loopback into the jail ([`hostloopback.go`](internal/cli/run/hostloopback.go) is the whole
  decision; `YOLO_NO_HOST_LOOPBACK=1` is the hatch; an explicit `network.mode` is never overridden). The
  launcher reports its decision as `YOLO_HOST_LOOPBACK=requested|shared|unsupported|unknown` on every
  launch. The in-jail witness ([`reachability.go`](internal/entrypoint/reachability.go)) **REFUSES the
  launch** when an enabled jail-facing service is unreachable, escalating only for `requested` and
  `shared`; `YOLO_ALLOW_UNREACHABLE_SERVICES=1`, forwarded from the host env, is the hatch, and it also covers a required in-jail
  service that did not start ([`requiredservice.go`](internal/entrypoint/requiredservice.go)).
- **A WORKSPACE MAY NOT CONTAIN THE CREDENTIAL BOUNDARY, AND THOSE DIRECTORIES MAY NOT HOLD A `.yolo`.** A
  workspace that IS or CONTAINS `$HOME`, `~/.config/yolo-jail` or `~/.local/share/yolo-jail` — or sits
  INSIDE either of the latter two — is refused by `refuseWorkspaceScope`
  ([`workspacescopeguard.go`](internal/cli/run/workspacescopeguard.go)), the SECOND thing `Run` does,
  before the launch log. The predicate is
  [`paths.WorkspaceScopeBreach`](internal/paths/workspacescope.go), and
  `paths.EnsureWorkspaceStateDir` is the `.yolo` CREATION chokepoint. There is deliberately no
  `YOLO_ALLOW_*`, and `workspace_readonly` does not soften it. ⚠ The one exemption, `paths.scopeExempt`,
  names the `yolo capture` scratch store, for a workspace only (a read-write `mounts` source runs
  `paths.WritableSourceScopeBreach` without it).
- **Writable host-TOOL-owned binds are host CACHES on a fixed list in code, never a tool's config or
  credentials** (`internal/hostcas`, [`hostcasalias.go`](internal/cli/run/hostcasalias.go);
  [the alias](docs/reference/disk-footprint.md#host-cas-aliasing),
  [OQ-BF11](docs/reference/disk-footprint.md#OQ-BF11)). Alias the cache SUBDIRECTORY only where a tool
  keeps config beside it (`~/.cargo/registry`, never `~/.cargo`);
  `TestNoStoreNamesAConfigOrCredentialPath` fails an entry that crosses that line. Every failure degrades
  to the private copy; none can refuse a launch. The launch discloses each alias by name and `yolo stores`
  lists it as `not yolo's`, so no reclaimer offers to delete host bytes. A path-keyed store needs rootless podman, so a nested
  jail only ever shows the decline. No store is aliased on macOS except under macos-user's run-as-you
  mode, `"macos_user_mode": "self"` ([MMP-D12](docs/design/macos-user-model-plan.md#MMP-D12)). The only other writable host binds are the workspace, yolo's own state
  and cache dirs, and ones the user named in user-scope config (`cache_relocations`, read-write `mounts`).
- **Nix inside the jail** delegates to the host daemon: the CLI mounts `/nix/var/nix/daemon-socket` +
  `/nix/store:ro` and sets `NIX_REMOTE=daemon`. Without this you get "build users group has no members".
- **Claude YOLO** is `--dangerously-skip-permissions` + `IS_SANDBOX=1` (the env var bypasses the UID-0
  refusal). `settings.json` sets `permissions.allow` to **`[]`** and `defaultMode: acceptEdits` — it is not
  an allowlist mechanism.
- **Bootstrap installs only** a Node floor a selected pack declares and — LAST — every program a
  selected pack declares, through its launcher in
  `~/.yolo/bin/launch/` run install-only. **A program it cannot install STOPS the launch**;
  `YOLO_ALLOW_MISSING_PROGRAMS=1` on the host is the hatch, and the integration suite sets
  `YOLO_NO_PROGRAM_READINESS=1` ([`OQ-JR1`](docs/reference/program-readiness.md#OQ-JR1),
  [`readiness.go`](internal/entrypoint/readiness.go)). **It installs no language server**: `lsp_servers`
  only renders config ([`OQ-LSP1`](docs/reference/mcp-configuration.md#oq-lsp1)).
- **PATH order is `BootPath`** ([`boot.go`](internal/entrypoint/boot.go)); the `.bashrc` export
  ([`shell.go`](internal/entrypoint/shell.go)) and macos-user's `macosuser.SandboxPath` (head derived
  from `entrypoint.HomePathDirs`) are tested against it. `~/.yolo/bin/block` (blockers) is FIRST and
  `~/.yolo/bin/launch` (lazy installers) SECOND, **ahead of every install prefix** — after them, a
  launcher becomes unreachable once it installs ([`OQ-PD12a`](docs/design/program-delivery.md#decision-ledger)).
  ⚠ **Do not add `/opt/yolo-jail/bin` to PATH** — the `/bin/<name>` symlinks reach it. ⚠ **The launcher
  collision check ([`launchercollision.go`](internal/entrypoint/launchercollision.go)) must never consider
  the install prefixes**, or no launcher is written after the first install. Both dirs share one
  bind-mount anchor at `~/.yolo/bin`, cleared contents-only (`resetAnchorDir`); never put that parent on
  PATH.
- **`YOLO_STORE_PACKAGES=1` takes `packages:` from the mounted nix store instead of the image** (podman +
  Linux + nix daemon only), per LAUNCH and never per package, with a farm at `/run/yolo/packages`
  ([`storepackages.go`](internal/entrypoint/storepackages.go)). **Exactly one mechanism is live in any
  jail**: a package both baked and staged silently runs the baked copy. It also selects the lean image, so
  `chromium` and `/etc/fonts` move
  ([store-delivered packages](docs/reference/image-staging-vs-baking.md#store-delivered-packages)).
- **Env hygiene** (agents can't handle interactive UI): `PAGER`/`GIT_PAGER`=`cat`, `BAT_PAGER=""`,
  `EDITOR=cat` but `VISUAL=nvim`, npm's update notifier and funding lines off, the host's `TERM` and
  `NO_COLOR` forwarded — every color decision goes through `tty.Color`
  ([`cli-color.md`](docs/reference/cli-color.md)) — and `OVERMIND_SOCKET=/tmp/overmind.sock`.
  ⚠ **`LD_LIBRARY_PATH` never names `/lib` or `/usr/lib`** (their glibc crashes older nix binaries with
  `GLIBC_PRIVATE`); FHS libraries reach binaries through nix-ld's path instead
  ([`mise-node-dynamic-linking.md`](docs/reference/mise-node-dynamic-linking.md)).
- **Skills.** The built-in skills (`configuring-the-jail`, `diagnosing-the-jail`) are injected into every
  jail. Priority: **the workspace** < built-in < shared packs < **the conventional local pack**
  (`~/.config/yolo-jail/local`, appended LAST by `config.LoadPacks`)
  ([`agent-briefings.md`](docs/reference/agent-briefings.md#the-workspace-layer)). ⚠ **The workspace layer
  is read by [`workspaceskills.go`](internal/jailcontent/workspaceskills.go)'s `confinedTree`, NEVER by
  `hostskills.ComposeInto`**, which follows symlinks
  ([P5](docs/reference/agent-briefings.md#ws-p5)). Nor an `os.Root` for the read itself: it follows a
  link that stays inside it, which a race turns into a read of host bytes, so the bytes are read by an
  `O_NOFOLLOW` walk along the path `os.Root` classified. ⚠ A skill in `~/.claude/skills` on the host reaches
  the jail by no path; there is no host user-level layer. ⚠ A skills contribution may FENCE children of its
  destination (`reserved`; `packs/claude` reserves `synced`,
  [`OQ-ST2`](docs/reference/pack-system.md#oq-st2)). The host→jail handoff (`.yolo/handover.md`) is a
  briefing section, not a skill ([`host-to-jail-handoff.md`](docs/reference/host-to-jail-handoff.md)).

## Where things live

| Topic | Authority |
|---|---|
| Config *file* keys, all of them | `yolo config-ref` |
| Pack manifest schema | [`internal/packdecl/packdecl.go`](internal/packdecl/packdecl.go) (the doc comments ARE the reference) |
| Pack authoring + the `packs` key | `yolo pack --help`, [`pack-system.md`](docs/reference/pack-system.md) |
| CLI surface | `yolo --help` |
| End-user usage, devices/GPU, mise tools, `yolo-cglimit` | [User guide](userguide/README.md), published at [docs.yolo-jail.mschulkind.dev](https://docs.yolo-jail.mschulkind.dev) |
| Every config key and pack contribution kind, per setup (backend × host OS) | [`settings-per-setup.md`](userguide/reference/settings-per-setup.md) |
| Mounts, overlays, home layout | [`jail-home.md`](docs/reference/jail-home.md) |
| Briefing generation, skills staging | [`agent-briefings.md`](docs/reference/agent-briefings.md) |
| MCP/LSP config, the node/npx wrappers | [`mcp-configuration.md`](docs/reference/mcp-configuration.md) |
| `LD_LIBRARY_PATH` / nix-ld, the `/lib` farm | [`mise-node-dynamic-linking.md`](docs/reference/mise-node-dynamic-linking.md) |
| Loopholes: what each one opens | [`loopholes.md`](userguide/guides/loopholes.md), [`loophole-protocol.md`](docs/reference/loophole-protocol.md) |
| The loophole system: activation, trust, the pack-shipped subset | [`loophole-system.md`](docs/reference/loophole-system.md) |
| The wire-bridge service pack and `needs` | [`wire-bridge.md`](docs/reference/wire-bridge.md) |
| Config-change confirmation flow | [`config-safety.md`](docs/reference/config-safety.md) |
| `--timing`, `perf_logging`, the host perf log | [`perf-logging.md`](docs/reference/perf-logging.md) |
| Storage paths and state separation | [`storage-and-config.md`](docs/reference/storage-and-config.md) |
| What the image bakes vs. what a launch delivers; the mounted prefix; the rebuild/reload cost model | [`image-staging-vs-baking.md`](docs/reference/image-staging-vs-baking.md) |
| Image and GC-root retention, the reapers | [`image-retention.md`](docs/reference/image-retention.md) |
| Host self-update channels, checks, state and trust boundary | [`self-update.md`](docs/reference/self-update.md) |
| Loopback-TLS reachability, the witness, its severity rule | [`loopback-tls-reachability.md`](docs/reference/loopback-tls-reachability.md) |
| Report tiers, the launch stream, why there is no quiet mode | [`report-tiers.md`](docs/reference/report-tiers.md) |
| Terminal color: rich markup, the color gate, the terminal probe, `NO_COLOR` | [`cli-color.md`](docs/reference/cli-color.md) |
| Program delivery: launchers, evergreen deps, the PATH ruling | [`program-delivery.md`](docs/design/program-delivery.md) |
| Disk footprint: the housekeeping slot, offered reclaim, `yolo stores`, host-cache aliasing | [`disk-footprint.md`](docs/reference/disk-footprint.md) |
| In-jail nix roots: translated roots, the root watcher, `yolo nix-roots` | [`in-jail-nix-roots.md`](docs/reference/in-jail-nix-roots.md) |
| Cgroup delegate security model | [`security-shim.md`](docs/reference/security-shim.md) |
| macOS backends | [`macos.md`](userguide/guides/macos.md) |
| macos-user nix integration, disabled-feature surface | [`macos-user-nix-and-features.md`](docs/reference/macos-user-nix-and-features.md) |
| The standing macOS direction | [`macos-no-vm-direction.md`](docs/reference/macos-no-vm-direction.md) |
| The one-time host→jail handoff | [`host-to-jail-handoff.md`](docs/reference/host-to-jail-handoff.md) |
| Releasing: the gates, partial states, resuming | [`release-process.md`](docs/reference/release-process.md) |
| The docs site and its Cloudflare deploy | [`docs-website.md`](docs/reference/docs-website.md) |

**`YOLO_*` environment dials have no authority row, because they have no authority.** `config-ref`'s
`ENVIRONMENT VARIABLES` section lists only `YOLO_RUNTIME`, `YOLO_BYPASS_SHIMS`, the two record-only timing
gates and `YOLO_EXTRA_PACKAGES`. Every other dial, including every `YOLO_ALLOW_*` hatch, is documented
where it is ENFORCED: the invariant above that names it, the refusal message that offers it, or the design
doc that ruled it. Grep the source for the spelling before believing any prose about one.

Agent logs, for debugging: `~/.copilot/logs/` and `~/.claude/projects/` inside the jail. On the host they
live in `<workspace>/.yolo/home/`, under `copilot/` and `claude/` on podman and macos-user, and under
`.copilot/` and `.claude/` on Apple Container.

## The changelog is written at release time

**Landing work adds no [`CHANGELOG.md`](CHANGELOG.md) entry.** The section for a version is written
once, when the release is cut, from the commits since the last tag, and it IS the GitHub release
body. How to write it, and the gates `just release` runs, are in the
[`cutting-releases`](.pi/skills/cutting-releases/SKILL.md) skill (the maintainer's ruling,
2026-10-23: *"We don't have to have an ongoing changelog maintained."*).

## Workflow

**The `Workers Builds: yolo-jail-docs` check is Cloudflare's**: renaming [`scripts/build-site.sh`](scripts/build-site.sh) needs the
dashboard's build command changed too ([`docs-website.md`](docs/reference/docs-website.md)).

1. **Image change** → edit [`flake.nix`](flake.nix), then verify as in step 2: the nested run rebuilds
   the flake and runs the new image. A host `just load` only ships a flake change; it does not validate it.
   Watch the output: a failed build is fatal.
2. **Logic change** → edit `cmd/`/`internal/`, run `just build-dev-bundle`, and invoke its emitted
   launcher **by absolute path from a throwaway workspace** (`just dev -- <args>` does the same from the
   caller's cwd). NOT bare `yolo` — that is the installed launcher, and the outer jail's binaries are
   frozen for the session. Never `just install` or `just deploy-dev` in-jail; on the host,
   `just deploy-dev [name]` installs the same wrapper as an evergreen launcher. For a **reachability-shaped** or
   **rootless-only** change, a nested jail proves nothing — see the carve-outs under [Testing](#testing).
3. `just format` (gofmt) before committing.
4. **Committing and landing are different steps.** Commit as often as the work has logical steps, with
   conventional messages; a commit runs nothing, and there is **no pre-commit hook, by ruling**.
   **Landing** — moving `main` to include the commits, or handing them to the human to push — runs the
   gate once, on the final tree: `just check-ci`, the integration tests the change reaches, and, for a
   launch-path change, the full integration suite. CI is the backstop. If the landing gate rejects, fix
   forward with a new commit — never `--amend` a landed commit. ⚠ `just check-ci` is a WHOLE-TREE gate,
   so during a fan-out land from a clean worktree.
5. End of task: commit, then `just done` green. It is **change-aware and read-only**
   ([`change-aware-completion.md`](docs/reference/change-aware-completion.md)): it refuses a dirty tree,
   then runs only what the commits since the last verified ancestor reach, or the full `just check-ci`
   when there is none. It never formats. Before landing a change to a test that reads files outside its
   package, rerun `python3 scripts/completion-census.py` and commit its output. `just done` is not the
   landing gate (step 4).
6. **Doc change that makes a claim about the code** → check it before writing it. A number, a
   `file:line`, a commit SHA, or a negative ("X has no caller") is the exact place a reader stops
   checking, so a wrong one is worse than none. The sweeps that keep this corpus honest are in
   [docs/plans/README.md](docs/plans/README.md#keeping-this-corpus-honest--the-five-checks-so-they-are-re-runnable).
   Run them when a sprint closes: **drift clusters at status lines**, not evenly.

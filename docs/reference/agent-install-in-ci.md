---
status: current
verified: 2026-09-23
verified_commit: 7ad8358c
covers:
  - integration/installmechanism_test.go
  - integration/agents_test.go
  - integration/harness_test.go
  - .github/workflows/packs.yml
  - Justfile
tags: [ci, packs, testing, npm, integration]
summary: "How the integration suite tests agent-CLI installation without letting a vendor's release decide whether main is green. The every-push gate installs only pinned fixture bytes, one cell per install mechanism, and renders every shipped pack's config without installing it. Real vendor installs run in a separate pack x arch workflow on a manifest or image change and weekly, and hard-fail like any other job. Suite warmup is paid before any timed test."
---

# Agent installs in CI — the pinned gate and the vendor-install workflow

**Status:** CURRENT as of 2026-09-23, verified against `7ad8358c`. MEASURED in CI on Linux: the
Pack Installs workflow was green at `7ad8358c` (run 35820702398) and on its 2026-09-21 weekly
schedule (run 35620029721). **Not measured on macOS:** the macOS nightly runs no vendor install,
and its warmup, skipped there until 2026-09-25, runs again and is being measured
([Suite warmup](#suite-warmup)).

Installing an agent CLI is a real yolo feature, and it has broken for real. It is also the one
part of the integration suite whose result can be decided by someone else: a vendor's release,
the npm registry's publish ordering, a registry outage. The suite handles this by asking two
questions on two different triggers. The **every-push gate** proves yolo's install *mechanisms*
from bytes this repository pins, and proves every shipped pack's config renders without
installing anything. The **Pack Installs workflow** asks whether the shipped packs' vendors'
current releases still install. It runs when a commit touches what could break that, and weekly,
and it fails like any other job.

| Component | Lives in |
| :--- | :--- |
| The two pinned install-mechanism cells | `integration/installmechanism_test.go` (`TestPinnedNpmProgramInstallsTheDeclaredVersion`, `TestInstallerProgramRunsThePacksOwnScript`) |
| The per-pack matrix and its completeness check | `integration/agents_test.go` (`packMatrix`, `packCase`, `TestPackMatrixCoversEveryShippedProgram`) |
| The network-free per-pack render test | `integration/agents_test.go` (`TestPackRendersConfigAndLauncher`) |
| The real vendor-install tests | `integration/agents_test.go` (`TestPackInstallsVersionsAndConfigures`, `TestAgentToolsAvailable`) |
| The real-install gate and suite warmup | `integration/harness_test.go` (`requireRealPackInstalls`, `autoCaptureEnvForSuite`, `warmJail`, `warmupTimeout`) |
| The vendor-install workflow | `.github/workflows/packs.yml` |
| The local full run | the `Justfile` `test` recipe |
| The launcher that performs an install | `internal/entrypoint` (the npm and installer launcher templates; `npmInstallSpec`) |

**Reads with:** [`image-staging-vs-baking.md`](image-staging-vs-baking.md#what-a-launch-delivers)
(why agent CLIs are delivered by a launch and not baked into the image, which is the constraint
everything here works inside), and [`program-delivery.md`](../design/program-delivery.md) (the
launchers, and the ruling that an agent CLI takes its vendor's installer wherever one exists).

---

## Principles

<a id="p1"></a>**P1. A blocking gate must be a pure function of the repository's contents.** If a
green main can turn red without a commit, the gate is measuring something other than the commit.
Every other rule in this doc follows from P1.

<a id="p2"></a>**P2. Coverage is per *mechanism*, not per *package*.** A pack's program installs in
one of exactly two ways. Testing four npm packages tests one code path four times, and the extra
three runs measure npm and the vendors, not yolo.

<a id="p3"></a>**P3. Installing is not the same act as being installed.** Most assertions need the
binary, or its launcher, to *be there*. Only a few need to watch it *arrive*. Conflating the two
is what made every test pay the install cost.

<a id="p4"></a>**P4. Schedule and severity are separate dials, and "advisory" welds them
together.** The push-path gate is the wrong *cadence* for a question no commit can affect. That is
no argument for the check to stop failing loudly: a weekly job that hard-fails is an ordinary
failure with an ordinary owner. This is why no job here is advisory, and it generalises past CI.

## The two failure modes

The design is shaped around two ways an install-shaped test goes red for a reason that is not a
defect in the commit under test.

<a id="mode-a"></a>**Mode A — the registry chooses the bytes.** An unversioned npm declaration
installs `<pkg>@latest`, a dist-tag the registry resolves at install time, so the bytes are chosen
by the registry and not by this repository, which violates [P1](#p1). The failure that established
it: a vendor published a parent package whose linux-arm64 platform tarball arrived tens of minutes
later. Because a failed `optionalDependency` is non-fatal by npm's design, `npm install` in that
window produced an installed but unrunnable CLI, silently, and three tests went red with nothing
wrong in the repository.

> [!WARNING]
> **Do not conclude from a quiet month that Mode A is gone.** Until that release, the vendor had
> always published platform binaries *before* the parent, which made the race structurally
> impossible. That ordering was silently protecting CI, it lives in someone else's release
> pipeline, and nobody is notified when it changes. Mode A is also **not arm-specific**: other
> releases in the same series showed the x64 tarball lagging too. The arm cell drew the short
> straw.

<a id="mode-b"></a>**Mode B — a one-time cost charged to a per-test budget.** Whichever container
test runs first pays for the runtime's first container create, the entrypoint's mise provisioning
and bootstrap's npm downloads into a cold cache. It is then judged against `YOLO_TEST_JAIL_TIMEOUT`,
a per-command deadline sized for steady-state work. The first test looked like the suite's
heaviest for reasons unrelated to its assertions, every later test was judged against its work
alone, and runner variance on a slow night pushed the first test past the cap. Mode B is entirely
yolo's own and has no coverage question in it. The fix is attribution
([Suite warmup](#suite-warmup)), not a wider cap.

## Invariants

- **The every-push gate installs no shipped pack's program from its vendor.** Every test that
  would do so calls `requireRealPackInstalls` first and skips, with a message naming the variable
  that un-skips it, unless `YOLO_TEST_REAL_PACK_INSTALLS` is set. The skip is on *which question is
  asked*, never a weakened assertion: the tests are the same tests, and they run on the triggers
  that can cause them to fail.
- **Every install the gate does perform is pinned.** The npm cell declares an exact version and
  asserts the installed version equals it, read from `node_modules`, where npm records what it
  installed, rather than from a `--version` flag, which is the package's business. The installer
  cell's script lives inside the fixture pack's own tree. Neither can go red without a commit.
- **Exactly one pinned cell per install mechanism.** No more, because a second npm cell re-buys a
  code path already bought ([P2](#p2)). No fewer, because each mechanism's cell is the only thing
  on the push path that watches an install *arrive* ([P3](#p3)).
- **Every shipped pack that declares a program has a `packMatrix` row.**
  `TestPackMatrixCoversEveryShippedProgram` enumerates the embedded packs and fails for any that
  declares an install contribution without a row. It runs under `-short`, so a forgotten row fails
  the pre-commit hook and the `check-go` job, where packs are added, and not only a container run.
  `.github/workflows/packs.yml`'s `pack:` list is a hand-maintained mirror of `packMatrix`. The
  test's failure message names that file, and nothing checks the mirror itself.
- **A vendor that cannot publish for an arch is skipped by a field on its row, not removed from
  the matrix.** `packCase.vendorSkipArch` and `vendorSkipReason` keep the pack's row present, so
  the completeness check still sees it, while the per-arch subtest skips and states why.
- **No vendor-install job is `continue-on-error`.** A red cell in Pack Installs is a real failure
  with the vendor's name in the job name ([P4](#p4), [OQ-CI3](#oq-ci3)).
- **Suite warmup is never fatal.** If it cannot launch, it reports the elapsed time and the error
  through `degraded` and every test still runs and reports its own diagnosis. A fatal warmup would
  turn one unexplained environment fault into a suite that says nothing at all.

## How it works

### What a launch does when a program is absent

A pack's program reaches a jail through a lazy launcher in `~/.yolo/bin/launch`
([`program-delivery.md`](../design/program-delivery.md)). On first use, when the real binary is
absent from its install prefix, the launcher installs it and then execs it. On the launch path a
failed install is not the verdict: the check that follows asks whether something exists to exec,
which answers correctly for a failed upgrade over a still-runnable old version too.

- **npm programs** install with `npm install -g --prefer-online <spec>` into `NPM_CONFIG_PREFIX`.
  `npmInstallSpec` in `internal/entrypoint` is the one place `@latest` is spelled: an unversioned
  declaration gets it, and a declared selector is used as written. `--prefer-online` revalidates
  against the registry even when the tarball is already cached. A **pinned** program (one whose
  declaration names a selector) is never polled for a newer release. It reinstalls only when the
  recorded spec file (`<bin>.spec` in the stamp directory) stops matching the declaration.
- **Installer programs** first try the machine's capture store, and otherwise download the
  vendor's script with `curl` and run it. Its contract is to leave an executable at
  `$HOME/.local/bin/<bin>`.

The prefix is **per-workspace**, and that is the fact every cost here comes from. `.npm-global`
and `.local` are bound read-write from the workspace's own state dir over the read-only home, and
the harness gives each test a fresh `t.TempDir()` workspace. So every test that invokes an agent
binary starts from an empty prefix and installs from scratch. That is the jail-isolation property
working as designed. The npm **HTTP cache** is *not* per-workspace: `~/.cache` is bound from
`paths.GlobalCache()`, so downloaded tarballs are reused across tests within a run.

> [!WARNING]
> **Do not cut install cost by removing the global `~/.cache` mount.** It is what makes a
> marginal install after the first cost seconds rather than a full download. It is load-bearing in
> the right direction.

### Two install mechanisms

A pack's `program` contribution declares `via: "npm"` or `via: "installer"`, and those are the
only two install mechanisms. Which shipped packs use which moves over time: codex moved from npm
to its vendor's installer, and one npm pack now pins its version. So the census lives in the
manifests and is not restated here:

```console
$ rg -l '"kind": "program"' packs/*/pack.json        # packs that install a program
$ rg -n '"via"|"package"' packs/*/pack.json          # which mechanism, which package
```

Two properties of the shipped packs matter to CI. Most npm packs declare no version, so they
install `@latest` and are exposed to [Mode A](#mode-a) wherever they are really installed. And an
installer pack's bytes are whatever the vendor's script fetches that day. Neither is under this
repository's control, which is why neither runs on the push path.

### What each assertion needs

A per-pack install test used to make three assertions in one shell command, and they have
different dependencies:

| Assertion | What it proves | Needs a vendor install? |
| :--- | :--- | :--- |
| `<bin> --version` exits 0 | the launcher exists, is on PATH, installs, and execs | **yes** — this is the install path |
| `~/.cache/yolo-agent-stamps/<bin>.stamp` exists | the install ran to completion | yes, as a side effect of the above |
| the generated config contains the pack's marker | the pack's `surfaces` rendered: layers, codec, path | **no** |

The third assertion is the one that is genuinely per-pack. Packs have different config codecs,
paths and marker keys, and a render bug in one says nothing about another. So it runs on every
push, for every `packMatrix` row, in `TestPackRendersConfigAndLauncher`, paired with a check that
the pack's launcher file exists. Asserting the *launcher* rather than the running binary is the
[P3](#p3) line: its existence proves yolo read and rendered the install declaration, which is
yolo's job. Whether the vendor's current release then installs is the vendor's job, and
`TestPackInstallsVersionsAndConfigures` asks that on the triggers that can cause it.

The other every-push agent tests follow the same line. `TestAgentToolsAvailableDirect` guards a
real regression (an agent CLI not found on the non-login PATH), and it asserts *resolution* with
`command -v`, because resolution is what that bug broke. `TestPackSelectionPrunesUnselected`
asserts which launchers and configs yolo generated and withheld, and none of that needs the
vendor's tarball.

### Three triggers, matched to three causes

The trigger should match the causation. Each row below answers a question no other row can, and no
row asks a question its trigger cannot cause:

| Trigger | What runs | Why |
| :--- | :--- | :--- |
| **Every push and PR** (`ci.yml`, the whole `./integration` package) | the two pinned mechanism cells, the per-pack render test, and every other container test. No vendor installs | A pure function of the repository ([P1](#p1)) |
| **A push or PR touching `packs/**`, `flake.nix`, `flake.lock`, `internal/image/**` or `packs.yml`** | Pack Installs: every `packMatrix` pack's real install on both arches, plus the coexistence job | A manifest edit, or a change to which image a jail gets, is commit-caused and can break a real install. Without this trigger a manifest typo would reach main and surface a week later |
| **Weekly schedule**, and manual dispatch | the same Pack Installs jobs | Vendor drift is not commit-caused, so it does not belong on the push path ([P4](#p4)) |

The path filter names what decides the image as well as the pack manifests, because a delivery
change breaks every real install as surely as a manifest change does. When it filtered on
`packs/**` alone, a change entirely inside the image-delivery code broke every install job, and
the workflow caught it only because the same push happened to carry an unrelated manifest edit.
The filter is not dropped entirely, because ci.yml already covers the launch path on every push.
What Pack Installs adds is real vendor installs, and the axis that makes those fragile is the
image.

A manifest-triggered run installs **every** pack in the matrix, not only the changed ones. There
is no changed-files detection. It is still exposed to [Mode A](#mode-a) for every unpinned pack,
but only on the rare PR that triggers it, and there the diagnosis is one question: *did this
change touch the package?* If not, the failure is upstream.

> [!WARNING]
> **Do not make Pack Installs a required check while it is path-filtered.** GitHub reports no
> status for a job a `paths` filter skipped, so a check that is both required and filtered leaves
> every PR that does not match waiting forever. Making it required needs a companion job that
> always runs and reports success when this one is skipped. No such job exists.

### The every-push mechanism cells

Both cells select a **local** `file://` fixture pack written into a temp dir. That matters twice.
A local pack's origin is allowed to declare an `installerUrl` at all. And a staged pack's tree is
copied whole into `/ctx/packs/<name>/`, so a pack can carry its own installer.

- **The npm cell** declares a small, pure-JavaScript package at an exact version, installs it
  through the launcher, asserts it resolved through `~/.yolo/bin/launch`, runs it, and compares the
  installed version to the declared one. The specimen has no platform `optionalDependencies`, so
  it cannot express Mode A's failure at all.
- **The installer cell** is fully hermetic. Its `installerUrl` is a `file:///ctx/packs/<name>/…`
  path into the pack's own tree, and the jail's `curl` reads the `file` protocol, so no server and
  no network are involved. The fixture entry names the pack explicitly, because a staged pack
  directory takes its name from the source URL's last path segment rather than the manifest's
  `name`, and under `t.TempDir()` that segment is a counter.

> [!WARNING]
> **The npm specimen's bin must not be baked into the image.** Launcher generation writes no
> launcher for a name the image already provides, so a fixture naming a baked binary would get no
> launcher, would resolve to the image's copy, and would pass while installing nothing. The
> resolve-through-the-launcher assertion is what catches this. Keep it when changing the
> specimen.

### The vendor-install workflow

`.github/workflows/packs.yml` builds the minimal jail image once per arch, then fans out:

- **`install`** is one job per pack × arch with `fail-fast: false`. Each runs only its own subtest
  of `TestPackInstallsVersionsAndConfigures`, so a red cell names its vendor in the job name
  rather than in a log. One vendor's break cannot mask the others ([OQ-CI3](#oq-ci3)). Both arches
  run, because a version can be fine on linux-x64 and broken on linux-arm64 at the same instant: a
  vendor that passes on one arch has not passed.
- **`coexistence`**, per arch, runs `TestAgentToolsAvailable`: one jail whose `packs` name two
  agents must end up with both installed. It is not per-pack, so it is not in the matrix.

Both jobs set `YOLO_TEST_REAL_PACK_INSTALLS`. The workflow **verifies only**: it records no
version and bumps nothing. The image-build steps duplicate `ci.yml`'s deliberately, because
artifacts do not cross workflow runs. Change one, check the other.

### The real-install gate

`YOLO_TEST_REAL_PACK_INSTALLS` is the one switch between the two questions. It is set by the Pack
Installs jobs and by `just test`, so a local full run keeps every test. It is unset in `ci.yml` and
in the macOS nightly. The same variable decides `autoCaptureEnvForSuite`: when unset, every launch
the suite makes carries `YOLO_NO_AUTO_CAPTURE=1`. Otherwise a launch selecting an installer pack
would run that vendor's installer to populate the capture store first, a large third-party download
on every push, through the side door. The capture store is shared with the machine even under the
suite's isolated `HOME`, so the suite cannot assert that a given launch captured.

### Suite warmup

`TestMain`'s body, `runSuite`, builds the CLI under test, reconciles the jail image, and then calls
`warmJail` before `m.Run()`. The warmup launches one `true` jail in a throwaway workspace, which
pays [Mode B](#mode-b)'s one-time costs where nothing is timed and prints how long it took. This is
a **measurement** fix, not a cost fix: the wall clock barely moves, but no test's budget contains
the warmup any more, so the per-command cap measures what it was sized for and per-test durations
are comparable.

- It runs under its **own redirected `HOME`**, a *sibling* of the workspace, seeded with an empty
  user config. The ambient `HOME` breaks on a developer machine whose real config names host
  paths, and a home *inside* the workspace is refused by the workspace-scope guard. `HOME` goes to
  the subprocess only, never `os.Setenv`, because a process-wide change before `m.Run()` would
  redirect every test.
- An empty config selects no packs, so the warmup installs no agent CLI. Bootstrap's own downloads
  still land in the shared HTTP cache for every later test.
- `warmupTimeout` bounds it far below a test's budget. A warmup is worth waiting for only while it
  costs less than the misattribution it removes, and a failed one has to be cheap because nothing
  depends on it.
- **It runs on darwin again, and that measurement is IN FLIGHT (2026-09-25).** Until then it was
  skipped there. On the 2026-08-22 and 2026-08-23 nightlies a darwin warmup realised a full image
  rather than starting a container, and it spent its whole bound warming nothing. Two changes
  removed the reason for that. [OQ-IP1](image-staging-vs-baking.md#why-its-this-way) made the
  image identity a content hash that any host computes, and the stock short-circuit returns
  before the build when the runtime already holds a matching image, which every nightly shard's
  `Load jail image` step preloads. Nobody has measured the result yet, so the next macOS nightly is
  the measurement. **How to read it:** each shard's log carries one of two lines.
  - `[integration] warmed the jail in <d> on darwin`: the warmup ran. It earned its place if the
    first container test in that shard got cheaper by about `<d>`.
  - `[integration] DEGRADED: warmup jail failed after <d> (…)`: the warmup failed. A failure at
    `warmupTimeout`'s bound whose output is pages of `Fetching …` is the 2026-08-23 shape again,
    and the skip should come back, citing that run. A failure for any other reason is a separate
    defect.
- **The Apple Container parity job warms too, deliberately, and its line is not that evidence.**
  `apple-container.yml`'s parity step sets no `YOLO_RUNTIME`, so the harness picks `container` on
  that Mac and warms it. The premise above does not hold there: yolo gives Apple Container images
  no stock name, so that backend has no stock short-circuit and builds on every launch
  (`tagStockImage` in `internal/image/stockimage.go`). Its warmup buys only the ordinary benefit:
  the cold build and load land before the first parity test instead of inside that test's budget.
  Read its `warmed the jail in <d> on darwin` or `DEGRADED` line for that job alone. Neither line is
  a reason to bring the nightly's skip back. If the warmup costs that job more than it moves, the
  remedy is a `warmupSkipReason` case for `YOLO_TEST_APPLE_CONTAINER`, with its row of
  `TestWarmupSkipReason` flipped, and not a platform skip.
- **The one skip left is a job, not a platform.** `warmupSkipReason` skips the warmup when
  `YOLO_TEST_MAC_ARCHIVE_DELIVERY` declares an image-delivery job. Such a job exists to observe a
  first load from a known-empty store, and a warmup launch would pay that load itself. The skip
  prints `[integration] skipping the jail warmup: …`. `TestWarmupSkipReason` pins the rule, and
  `TestWarmJailConsultsItsSkipRule` pins `warmJail`'s call of it.

### What runs on macOS

The macOS nightly shards the whole `./integration` package but never sets
`YOLO_TEST_REAL_PACK_INSTALLS`, so no vendor install runs there. The mechanism cells and the render
test do run, so the podman-VM install path is exercised on macOS by the pinned fixtures only
(INFERRED from the workflow's env; not observed in a run log for this stamp).

## Traps

> [!WARNING]
> **Do not widen a cap to fit a slow first test.** Check first whether the time is the test's own
> or the suite's warmup ([Mode B](#mode-b)). A wider cap preserves the misattribution and has to be
> re-widened every time warmup grows. The `packages:` tests in `integration/packages_test.go` that
> take `nixBuildJailTimeout` get a larger budget legitimately, because their cost is a real
> per-workspace image build and not warmup.

> [!WARNING]
> **Do not retry a failed vendor install.** A Mode A window is minutes to tens of minutes long,
> and a retry re-resolves the same absent tarball.

> [!WARNING]
> **Do not gate a pack out of an arch's matrix for a transient.** `vendorSkipArch` is for a vendor
> that does not publish for that arch at all. A vendor that publishes but lagged is exactly the
> coverage the arch cell exists for.

## Non-goals

- **Agent CLIs are not baked into the image** to speed up tests. The image deliberately leaves
  them to a launch ([`image-staging-vs-baking.md`](image-staging-vs-baking.md#what-a-launch-delivers)),
  and reversing that for CI would be pinning by staleness.
- **What users get is not pinned.** An unversioned shipped pack still installs its vendor's
  current release. CI pins only its own fixtures.
- **No bump collector exists.** Pack Installs verifies and records nothing. `packsrc.LockEntry`
  has no package-version field, so there is nowhere for a resolved version to be written. A
  collector would need a new ruling on where a version lives ([OQ-CI3](#oq-ci3)).
- **No warm-prefix seeding.** Tests do not share a pre-populated npm prefix ([OQ-CI5](#oq-ci5)).
- **CI is not offline-capable.** Pinning decides *who chooses* the bytes. A pinned tarball can
  still be unpublished, and the registry can still be down.
- **Loopback-TLS reachability is out of scope.** It is a separate class with its own carve-out
  ([`loopback-tls-reachability.md`](loopback-tls-reachability.md)).

## Current values

Verified at `7ad8358c`. The prose above explains what each of these is for; this table is the only
place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Real-install gate | `YOLO_TEST_REAL_PACK_INSTALLS` (any non-empty value) | `realPackInstallsEnv`, `integration/harness_test.go` |
| Auto-capture off-switch sent by the suite | `YOLO_NO_AUTO_CAPTURE=1` when the gate is unset | `autoCaptureEnvForSuite`, `integration/harness_test.go` |
| Per-command jail deadline, default | 300s | `defaultJailTimeoutSeconds`, `integration/harness_test.go` |
| Per-command deadline, override | `YOLO_TEST_JAIL_TIMEOUT` (integer seconds); Pack Installs and the macOS nightly set 1200 | `jailTimeout`; `.github/workflows/packs.yml`, `.github/workflows/nightly-macos.yml` |
| Warmup bound | 5 minutes, or the per-command deadline if smaller | `warmupTimeout`, `integration/harness_test.go` |
| Warmup skipped | only when `YOLO_TEST_MAC_ARCHIVE_DELIVERY` is set (an image-delivery job); every GOOS warms | `warmupSkipReason`, `integration/harness_test.go` |
| npm mechanism specimen | `cowsay@1.6.0` | `pinnedNpmPackage`, `integration/installmechanism_test.go` |
| Installer fixture URL | `file:///ctx/packs/local-installer-fixture/install.sh` | `TestInstallerProgramRunsThePacksOwnScript` |
| Unversioned npm spec | `<pkg>@latest` | `npmInstallSpec`, `internal/entrypoint` |
| npm install flags | `-g --prefer-online` | the npm launcher template, `internal/entrypoint` |
| Per-workspace install prefixes | `/home/agent/.npm-global`, `/home/agent/.local` | `internal/cli/run` (`assemble_parts.go`) |
| Shared npm HTTP cache | `/home/agent/.cache/npm`, from `paths.GlobalCache()` | `internal/cli/run` |
| Pack Installs weekly schedule | `0 9 * * 1` (Monday 09:00 UTC) | `.github/workflows/packs.yml` |
| Pack Installs path filter | `packs/**`, `flake.nix`, `flake.lock`, `internal/image/**`, `.github/workflows/packs.yml` | `.github/workflows/packs.yml` (listed twice, for `push` and `pull_request`) |
| Pack Installs arches | `ubuntu-latest`, `ubuntu-24.04-arm` | `.github/workflows/packs.yml` |
| Per-job timeout | 60 minutes | `.github/workflows/packs.yml` |

## Why it's this way

Rulings a maintainer reading only the normative text would otherwise undo. [OQ-CI3](#oq-ci3) is cited from
`.github/workflows/packs.yml`. The CI runs named here are the evidence each ruling was settled on.

> [!NOTE]
> **Two reference docs define an `oq-ci1` anchor.** [This doc's](#oq-ci1) is the CI-pinning
> ruling. [`claude-oauth-interposition.md#oq-ci1`](claude-oauth-interposition.md#oq-ci1) is an
> unrelated open question about sharing the Claude credential. Cite either one as a file-qualified
> link, never as bare text.

| ID | Ruling | Why it holds |
| :--- | :--- | :--- |
| <a id="oq-ci1"></a>[**OQ-CI1**](#oq-ci1) — the gate's pin is **committed in the repository**, not resolved per run | A version resolved once per run is deterministic *within* the run, but a green main can still go red tomorrow with no commit, which is the property [P1](#p1) exists to rule out. A committed pin also makes any change to it a reviewable diff |
| <a id="oq-ci2"></a>[**OQ-CI2**](#oq-ci2) — **two** pinned install cells, one per mechanism, not a third npm cell | The one per-package difference with a bug history, a scoped versus a bare npm name, is string parsing. `TestSplitNpmSpec`'s table in `internal/entrypoint` covers both spellings, using real shipped package names, without a container. The saving went to the thinner installer mechanism instead |
| <a id="oq-ci3"></a>[**OQ-CI3**](#oq-ci3) — vendor installs run as **separate hard-failing jobs per pack × arch** | A single sequential job dies on the first bad install and masks every vendor after it. `continue-on-error` would turn a loud wrong signal into an ignored one ([P4](#p4)). The ruling's second half, *bump what passed*, is **not built**: it needed a place to record a resolved version, and `LockEntry` has none. A future collector would also have to run after partial failure, and commit only versions it verified on every arch it serves |
| <a id="oq-ci4"></a>[**OQ-CI4**](#oq-ci4) — suite warmup runs in `TestMain`'s seam, outside any timed test; **no cap widening** | [Mode B](#mode-b) was a misattribution. On x64 (run 32419507352) the first test's two installs cost about ten times what the same two cost later in the run, and on the macOS nightly most of a blown 1200s cap was warmup. After the warmup landed (run 32597479510) it took 1m56s, moving that one-time cost into a line that belongs to no test, while the job's wall clock barely moved |
| <a id="oq-ci5"></a>[**OQ-CI5**](#oq-ci5) — **no warm-prefix seeding** | Measured in run 32597479510: with vendor installs off the push path, the whole residual per-test install cost is the two pinned cells, about 18 seconds. A seeded prefix has nothing left to remove, and it would risk silently deleting the one cold install per mechanism |
| <a id="oq-ci6"></a>[**OQ-CI6**](#oq-ci6) — the push gate uses **fixture packs**, and real packs get a **path-filtered trigger** | A fixture pins with shipped mechanisms only (a `file://` pack and a version in its `package` string), and lets the specimen be a small, fast package instead of a large agent CLI. The cost is that the gate no longer proves the shipped manifests install. The path-filtered and weekly triggers exist to cover exactly that |

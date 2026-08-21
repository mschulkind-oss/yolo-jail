---
title: "Nine cold installs a run — what CI buys by downloading six agent CLIs"
date: 2026-08-21
status: accepted
tags: [ci, packs, testing, npm, cost]
summary: "The integration suite installs agent CLIs from the live npm registry nine times per run, unpinned and with --prefer-online, because the npm prefix is per-workspace and every test gets a fresh workspace. That buys two mechanisms' worth of coverage nine times over, and imports every registry hazard into the blocking gate. Two CI failures in two days, neither of them a bug in this repo. All six questions settled; the design is decided and nothing is built. One claim retracted along the way."
---

# Nine cold installs a run — what CI buys by downloading six agent CLIs

**Status:** DECIDED 2026-08-21. **Nothing built.** All six questions are settled and compacted into
the [Decision Ledger](#decision-ledger); §11 is the build order. **One claim was retracted along the
way:** the argument that a lockfile pin could not serve CI was wrong, and the manifest-pin shape it
motivated is withdrawn ([§5.1](#51-mode-a-is-already-ruled-and-waiting-on-a-field)) — the trap that
made it plausible is preserved there, because it is easy to re-derive. Every claim about current
behaviour is traced to code and dated; the cost figures are read off two real CI runs.

**The short version.** The integration suite performs **nine agent-CLI installs per run**, each
into a cold, per-workspace npm prefix, each resolving an **unpinned `@latest`** with
**`--prefer-online`**. Eight of the nine exercise the *same* npm code path with a different
package string. What that buys is real but small — two install mechanisms and one config-render
assertion — and we re-buy it nine times while importing the public registry's uptime, publish
ordering, and throughput into a **blocking** gate. On 2026-08-20 an upstream publish race turned
three tests red; on 2026-08-21 the *first* of them blew a 20-minute cap it was already using 86% of —
two-thirds of that budget being suite warmup it pays for by running first ([§4.1](#41-the-first-test-is-the-suites-warmup-sink)).
Neither was a defect in this repo. My proposal is a principle with three consequences: **the
blocking gate is a pure function of this repo's contents**, so it installs pinned bytes, once per
mechanism rather than once per test, and upstream drift is checked by a **weekly maintenance job that
fails like any other job** — never by an "advisory" cell. The gate's three triggers, each matched to a
cause no other trigger can raise, are [§6.1.1](#611-three-triggers-matched-to-three-causes).

**The most important section is [§6](#6-what-advisory-gets-wrong)** — it is the one that decides
whether the rest is worth building, and it is where I think the usual answer to this problem is
actively wrong.

**Reads with:** [`trust-paths.md`](trust-paths.md) (OQ-TP4 owns pinning for the **shipped** packs — a
`LockEntry` field to record an npm version. Whether *this* doc depends on it is now OQ-CI6: a fixture
pack can be pinned with mechanisms that already ship),
[`image-staging-vs-baking.md`](image-staging-vs-baking.md) (why the CLIs are not baked into the
image, which is the constraint §5.2 works inside).

---

## 1. The verdict, and the principles behind it

**P1. A blocking gate must be a pure function of the repository's contents.** If a green main can
turn red without a commit, the gate is measuring something other than the commit. Everything else
in this doc follows from P1.

**P2. Coverage is per *mechanism*, not per *package*.** Six packs declare a program; there are
exactly **two** ways they install (§2.3). Testing four npm packages tests one code path four
times. The marginal three runs measure npm and the vendors, not yolo.

**P3. Installing is not the same act as being installed.** Most assertions in the suite need the
binary to *be there*. Exactly one needs to watch it *arrive*. Conflating the two is what makes
every test pay the install cost.

**P4. Schedule and severity are separate dials, and "advisory" welds them together.** The push-path
gate is the wrong *cadence* for a question no commit can affect. That is not an argument for the check
to stop failing loudly — a weekly job that hard-fails is an ordinary failure with an ordinary owner.
This is the reason I will not propose an advisory job (§6), and it generalises past this doc.

The verdict: keep testing that installs work — it is a real feature and it has broken for real —
but pay for it **once per mechanism, from pinned bytes**, and move "do the six vendors' current
releases still install?" off the merge path onto a weekly cadence where it may still fail hard.

## 2. What happens today, precisely

### 2.1 The launcher installs whenever the binary is absent

The lazy launcher's launch path is a single test
([`shims.go:500`](../../internal/entrypoint/shims.go#L500)):

```bash
if [ ! -x "$REAL_BIN" ]; then _do_install; fi
```

`REAL_BIN` is `$NPM_CONFIG_PREFIX/bin/<bin>`, and `NPM_CONFIG_PREFIX` defaults to
`$HOME/.npm-global` ([`shims.go:348,353`](../../internal/entrypoint/shims.go#L348-L353)). So
"should I install?" is answered entirely by whether that one directory has the binary.

The install itself ([`shims.go:384`](../../internal/entrypoint/shims.go#L384)):

```bash
YOLO_BYPASS_SHIMS=1 npm install -g --prefer-online "$SPEC"
```

with `SPEC` = `<pkg>@latest` for every shipped pack, because none of them declares a version and
`npmInstallSpec` appends `@latest` when the selector is empty
([`npmspec.go:64`](../../internal/entrypoint/npmspec.go#L64)).

Two properties of that command matter later. `@latest` is a **dist-tag resolved at install time**,
so the bytes are chosen by the registry, not by this repo — a direct P1 violation.
`--prefer-online` **forces revalidation against the registry** rather than trusting the local
cache, so the round-trip happens even when the tarball is already on disk.

### 2.2 The npm prefix is per-workspace, and every test gets a new workspace

`/home/agent` is mounted `:ro` from the global home, and `.npm-global` is bound over it
read-write from the **per-workspace** state dir
([`assemble_parts.go:70`](../../internal/cli/run/assemble_parts.go#L70)):

```go
"-v", filepath.Join(ws, "npm-global")+":/home/agent/.npm-global",
```

where `ws` is `<workspace>/.yolo/home`
([`assemble.go:38`](../../internal/cli/run/assemble.go#L38)). The integration harness creates each
workspace with `t.TempDir()` ([`harness_test.go`](../../integration/harness_test.go), `writeProject`).

Compose those three facts and the behaviour is forced: **a fresh temp workspace has an empty npm
prefix, so `REAL_BIN` is missing, so every test that invokes an agent binary installs it from
scratch.** Nothing is shared between tests, and nothing is wrong with any one of those decisions —
the per-workspace prefix is the jail-isolation property working as designed. The cost is emergent.

> [!NOTE]
> The npm **HTTP cache** is *not* per-workspace: `.cache` is bound from `paths.GlobalCache()`
> ([`assemble_parts.go:48,81`](../../internal/cli/run/assemble_parts.go#L48)) and
> `NPM_CONFIG_CACHE=/home/agent/.cache/npm`
> ([`assemble.go:564`](../../internal/cli/run/assemble.go#L564)). So tarball *bytes* can be reused
> within a run. This is the one thing that keeps the current cost from being far worse: measured on
> x64, a marginal install once the cache is warm is **~12s**, against 124.5s for the first test
> ([§4.1](#41-the-first-test-is-the-suites-warmup-sink)). So the nine installs are cold in the
> *prefix* sense — every one re-unpacks and re-links — but warm in the *download* sense after the
> first. `--prefer-online` still spends a registry round-trip per install to revalidate, and a fresh
> CI runner starts with the cache empty regardless. Do not "fix" the cost by deleting the global
> `.cache` mount; it is load-bearing in the right direction.

### 2.3 Two mechanisms, six packs, nine installs

Read off `packs/*/pack.json`, 2026-08-21:

| Pack | `via` | Package / installer |
| :--- | :--- | :--- |
| codex | `npm` | `@openai/codex` |
| copilot | `npm` | `@github/copilot` |
| opencode | `npm` | `opencode-ai` |
| pi | `npm` | `@earendil-works/pi-coding-agent` |
| claude | `installer` | curl-piped |
| agy | `installer` | curl-piped |

**Four npm, two installer.** (This matches `trust-paths.md`'s independent count of *"the four packs
that declare npm programs — pi, copilot, codex, opencode"*.)

What the suite actually installs, per run
([`agents_test.go`](../../integration/agents_test.go)):

| Test | Installs | Count |
| :--- | :--- | :--- |
| `TestAgentToolsAvailable` | codex, copilot | 2 |
| `TestAgentToolsAvailableDirect` | copilot | 1 |
| `TestPackInstallsVersionsAndConfigures` | claude, copilot, opencode, pi, codex | 5 |
| `TestPackSelectionPrunesUnselected` | codex | 1 |
| | | **9** |

**Eight of the nine are the npm path.** codex is installed three times, copilot three times. The
`installer` path is exercised **once** (claude), and `agy` — the other `installer` pack — is not in
`packMatrix` at all, so one of the two mechanisms is covered by a single cell while the other is
covered eight times. That inversion is the clearest statement of the problem: the redundancy is not
merely wasteful, it is *pointed away from* the thinner coverage.

## 3. What we actually learn — the honest ledger

`TestPackInstallsVersionsAndConfigures` makes three assertions per pack. They have very different
dependencies:

| Assertion | What it proves | Needs a network install? |
| :--- | :--- | :--- |
| `<bin> --version` exits 0 | the generated launcher exists, is on PATH, installs, and execs | **yes** — this is the install path |
| `~/.cache/yolo-agent-stamps/<bin>.stamp` exists | `_do_install` ran to completion | yes, but only as a side effect of the above |
| generated config contains the pack's marker | the pack's `surfaces` rendered — `managed`/`defaults` layers, codec, path | **no** |

The third assertion is the one that is genuinely per-pack: five packs have five different config
codecs, paths, and marker keys, and a render bug in one says nothing about the others. **It does not
need the CLI installed at all.** It is currently gated behind an install only because the test
bundles it with `<bin> --version` in one shell command.

The first assertion is genuinely valuable and has caught real defects — the docstrings record them:
`TestAgentToolsAvailableDirect` exists because `yolo -- copilot` once failed with "command not
found" when `/mise/shims` was absent from the non-login PATH, and
`TestPackSelectionPrunesUnselected` exists because packs were once staged wholesale and every pack
rendered regardless of selection. Both are real regressions in *yolo*. Neither needed five npm
packages to find.

So the ledger: of nine installs, **two** buy mechanism coverage (one npm, one installer), and
**seven** re-buy the npm path with different strings. Those seven are the load-bearing answer to
"what do we learn" — nothing about this repo. They test that four vendors published working
tarballs today.

## 4. The cost, measured

Read off run `32419507352` (Linux, 2026-08-20) and runs `32340742162` / `32455532488` (macOS
nightly, 2026-08-20 pass and 2026-08-21 fail).

| Test | x64 Linux | macOS (passing) | macOS (failing) |
| :--- | ---: | ---: | ---: |
| `TestAgentToolsAvailable` | 124.5s | 1032.9s | **timed out at 1200s cap** |
| `TestPackInstallsVersionsAndConfigures` | 68.9s | 802.4s | 1397.7s |
| `TestAgentToolsAvailableDirect` | 12.9s | 155.1s | 246.9s |
| `TestPackSelectionPrunesUnselected` | 11.7s | 122.7s | 186.9s |
| **agent-install subtotal** | **218s** | **2113s (35 min)** | — |
| whole job | 803s (13m23s) | 4936s (1h22m) | 7552s (2h06m) |

**Roughly 27% of the x64 integration job and ~43% of the macOS nightly is agent-CLI installation.**
On macOS the same work is ~8× slower than on x64 (124.5s → 1032.9s for the same test), because
every byte goes through podman's VM.

This is not merely slow; it is what makes the suite *fragile*. `TestAgentToolsAvailable` consumed
**1033s of the nightly's 1200s cap**
([`nightly-macos.yml:103`](../../.github/workflows/nightly-macos.yml#L103)) on a healthy night — 14%
headroom. On 2026-08-21 every test ran 1.6–2.1× slower (`PackInstalls/claude` 179.5s → 384.9s) and
it blew the cap.

### 4.1 The first test is the suite's warmup sink

The obvious reading of that number — *"it is the heaviest test, being the only one that installs two
CLIs"* — is **wrong**, and it took a second look at the ordering to see it.
`TestAgentToolsAvailable` is the **first** container test to run, and whatever runs first pays for
podman's image load, the first container start, mise provisioning, and the cold shared npm cache.

| x64, same run | installs | position | time |
| :--- | ---: | :--- | ---: |
| `TestAgentToolsAvailable` | 2 | **first** | **124.5s** |
| `TestAgentToolsAvailableDirect` | 1 | second | 12.9s |
| `PackInstalls/codex` | 1 | later | 11.6s |
| `PackInstalls/copilot` | 1 | later | 12.2s |
| `PackInstalls` (whole matrix) | 5 | later | 68.9s |

Two installs cost 124.5s at the head of the suite; the same two cost ~24s anywhere else, and *five*
cost 68.9s. So ~100s of that first test is not its own work — and the penalty is confined to the
very first test, since the second is already down to 12.9s. The macOS numbers say the same thing
more loudly: 1033s against an expected ~350s for two installs plus a boot, i.e. **roughly 680s of
the timing that blew the cap was suite warmup.**

> [!WARNING]
> This is a **measurement** defect, not a cost defect. A per-command cap sized for steady-state work
> is being applied to a test that also carries a one-time suite cost, so the cap has to be large
> enough for warmup + work while every later test is judged against work alone. Widening the cap
> preserves the misattribution and buys nothing else; §5.2 says what to do instead. Note also that
> nothing in §6.1 touches this — cutting the pack matrix shrinks `PackInstalls`, while
> `TestAgentToolsAvailable` keeps its two installs and keeps running first.

## 5. Diagnosis: two failure modes, one cause

Both CI failures of the last two days are the same structural fact — *the gate's verdict depends on
installing third-party CLIs over the network* — expressed twice.

**Mode A — the registry chooses the bytes (P1).** On 2026-08-20 `@openai/codex@latest` resolved to
`0.149.0`, published at 21:09:05Z; its `linux-arm64` platform tarball was not published until
21:46:29Z. For 37 minutes `npm install -g @openai/codex@latest` on linux-arm64 produced an
installed-but-unrunnable CLI, silently, because a failed `optionalDependency` is non-fatal by
design. CI ran at 21:38. Three tests red, nothing wrong in the repo.

The publish-ordering lag per stable release, from the registry's own `time` map:

| version | date | linux-x64 lag | linux-arm64 lag |
| :--- | :--- | ---: | ---: |
| 0.144.5 – 0.147.0 | 07-16 → 08-07 | −1 to −2m | −1 to −2m |
| 0.148.0 | 08-18 | −0m | +2m |
| 0.149.0 | 08-20 | +2m | **+37m** |

> [!WARNING]
> Through 0.147.0 the vendor published platform binaries **before** the parent — negative lag, which
> makes this race *structurally impossible*. That ordering was silently protecting us and nobody
> knew it was load-bearing. It broke on 2026-08-18. Do not conclude from a quiet month that the
> hazard is gone: the invariant lives in someone else's release pipeline and we get no notice when
> it changes. This is also why the failure is **not arm-specific** — 0.149.0 had x64 at +2m and the
> intervening alphas showed x64 lags of +20m and +57m. The arm cell simply drew the short straw.

**Mode B — a one-time cost is charged to a per-test budget.** §4.1. A test at 86% of its cap fails on
runner variance — but two-thirds of what filled that budget was suite warmup the test happens to run
first and therefore pays for.

### 5.1 Mode A is already ruled, and waiting on a field

`trust-paths.md` OQ-TP5 (2026-08-18) ruled: *"I don't want magical evergreen npm packages. If
there's a committed lockfile, install installs from that version, update is how you get new
versions."* Both behavioural halves shipped — the hourly poll only reports now, and
`yolo pack update` is the only act that resolves. What did not ship is the **record**: OQ-TP4 asks
where an embedded pack's npm version gets pinned, and its answer is empty. `LockEntry`'s fields are
all about a *git* pin and none about a package one, so there is no slot to write a version into.

That gap is exactly Mode A. `install` has no version to obey, so it falls back to `@latest`.

**CI needs nothing beyond that field.** The lockfile lives beside the user config —
`LockPath` is `filepath.Dir(userConfigPath) + "/packs.lock.json"`
([`lock.go:107-110`](../../internal/packsrc/lock.go#L107-L110)) — and the integration harness
**already owns that exact directory**: `packHome` redirects `HOME` to a temp dir and writes
`.config/yolo-jail/config.jsonc` into it
([`packs_test.go:224-235`](../../integration/packs_test.go#L224-L235)). Dropping a
`packs.lock.json` beside that config is one more `os.WriteFile` in a helper that exists, at the same
path, read by the same loader, with no new concept anywhere. **A partial lockfile is also fine** — a
missing row is *"the normal state"* for `LoadLock` and simply falls back to current behaviour, so CI
needs rows only for the packs whose install the blocking gate exercises (§6.1: two of them).

So the mechanism CI wants for **the shipped packs** is **OQ-TP4 option (b), unmodified**, and its only
dependency on that question is that the field ship — not how the ruling comes out.

### 5.1.1 …and the blocking gate may not need to wait for it at all

**Ruled 2026-08-21 (OQ-CI1): the pin is a committed fixture, bumped weekly.** A per-run resolve is
deterministic *within* a run, which is not the property P1 asks for — a green main that goes red
tomorrow with no commit is exactly what it still permits. And once the weekly workflow (§6) is what
moves the pin, committing it is what makes that movement a reviewable diff instead of an invisible
resolve. There is no argument left for the harness authoring rows itself.

But "committed" does not have to mean "a lockfile row", and there is a route using only **shipped**
mechanisms:

- The suite already builds a **synthetic pack** in a temp dir, writes a `pack.json` declaring
  `{"kind": "program", "via": "npm", "package": "fzf"}`, and selects it with `packs: ["file://<path>"]`
  ([`launcherdir_test.go:129-145`](../../integration/launcherdir_test.go#L129-L145)).
- A pack's `package` string has carried a version selector since 2026-08-17
  ([`npmspec.go`](../../internal/entrypoint/npmspec.go)).

Compose those and a fixture pack declaring `"package": "<pkg>@1.2.3"` gives the blocking gate a
**pinned, in-repo, deterministic** npm install today — no `LockEntry` field, no OQ-TP4 dependency.

The trade is what the gate then covers. A fixture exercises the install **mechanism**; it does not
prove that `packs/codex/pack.json`'s declared package installs. Under P2 that is the correct split —
the shipped manifests' real installability is precisely the weekly workflow's question, not the merge
path's — but it is a real difference and it is [OQ-CI6](#open-questions) below.

A fixture also lets the specimen be chosen for the job: a **small, stable, binary-shipping npm
package** is a better mechanism test than a 100 MB agent CLI, and a much faster one. The `fzf`
precedent above is already exactly that shape. The `installer` mechanism is the harder half — a
deterministic fixture needs an installer URL the test controls, not a vendor's — and that asymmetry is
called out in §6.1.

> [!WARNING]
> ### ⚠ Retracted: "(b) fixes the product and leaves CI where it is"
>
> An earlier revision of this section (2026-08-21, commits `81c56e8`/`2a458dd`) argued that because
> the lockfile is user-scoped and `.yolo/` is `.gitignore`d, **a fresh CI runner has no row to obey**,
> so option (b) could not serve CI — and proposed a fourth shape, *"the manifest carries a known-good
> pin and the lockfile overrides it."*
>
> **That reasoning was wrong, and the fourth shape is withdrawn.** It treated CI as a passive consumer
> of whatever lockfile happens to exist on the runner, when in fact the harness *manufactures* the
> entire user-config environment — including the very directory the lockfile lives in. "No committed
> lockfile" is not a constraint on a test suite that writes its own config from scratch on every run.
>
> Keeping the trap visible because it is easy to re-derive: the lockfile's *user scope* is about where
> a **user's** pin lives. It says nothing about what a harness can synthesize, and the two questions
> look identical from the file path alone.
>
> The manifest-pin idea may still have an independent *product* argument — what a fresh user with no
> lockfile gets on first run, which is (b)'s acknowledged hole. That argument belongs to OQ-TP4 and is
> no longer made here; nothing in this doc depends on it.

### 5.2 Mode B is ours alone, and the fix is attribution

No upstream involvement, and — given §4.1 — no coverage question either. **Ruled 2026-08-21
(OQ-CI4):** the suite's one-time costs are paid **outside any timed test** — a suite-level warmup
that launches one jail and installs
nothing of consequence, run once before the first assertion is timed. `TestMain` already does
suite-level work (it builds a fresh host `yolo` and reconciles the image), so this is a third thing
in an existing seam rather than a new concept.

What that buys, in order of importance: the per-command cap goes back to measuring what it was sized
for; every test's reported duration becomes comparable to every other's; and the cap stops needing
headroom for a cost only one test pays.

> [!NOTE]
> I originally proposed simply raising the cap to 2400s, on the grounds that Mode B is ours and one
> line is cheap. **Withdrawn** — §4.1 is why. Widening a budget to fit a misattributed cost hides the
> thing worth knowing (the first test is 10× the others for reasons unrelated to its assertions) and
> would have to be re-widened the next time suite warmup grows. A stopgap is still available if the
> nightly's redness is costing something while the real change is in flight, but it is a stopgap and
> should be labelled one.

Baking the CLIs into the image is **not** a lever — `image-staging-vs-baking.md` deliberately keeps
agent CLIs out of the image so a jail launch delivers them lazily, and reversing that to speed up a
test would be the tail wagging the dog.

## 6. What "advisory" gets wrong

The obvious move — keep installing all six from `@latest`, mark the job `continue-on-error`, call it
advisory — is the one I want to argue against, because I think it is worse than doing nothing.

An advisory cell has **no owner and no artifact** (P4). It goes yellow, nobody is paged, and the
next reader learns only that this job is allowed to be yellow. Within a month its state carries no
information: a *real* upstream break and a *stale* upstream break look identical, so the first thing
anyone does with a yellow cell is stop looking at it. We would have converted a loud, wrong signal
into a quiet, ignorable one and called it a fix. The failure mode is not hypothetical — it is the
same shape as the trap `AGENTS.md` already warns about under Testing: *a test that pins the callee
while the call site is unpinned is not a test.* An advisory job pins nothing; it merely observes.

The thing to separate here is **schedule** from **severity**, because "advisory" quietly conflates
them. What makes the push-path gate wrong for this question is its *schedule* — it runs on every
commit, and no commit caused a vendor's release to break. Nothing about that argues the check should
stop failing loudly. **A scheduled job that hard-fails is a normal failure that happens to have a
weekly cadence**, and it has everything `continue-on-error` lacks: a red cell means something, a
human is expected to look, and the history is legible.

So the shape is a **weekly maintenance workflow**, not merely a version bump. **Ruled 2026-08-21
(OQ-CI3):**

- **It fails like anything else.** No `continue-on-error`. If codex genuinely stopped installing, that
  is a real failure and should read as one.
- **Separate jobs per vendor, fanned out.** Six packs, six independent cells. One vendor's break must
  not mask the other five, which is exactly what a single sequential job does when it dies on the
  first `npm install`.
- **It advances the pins that passed, and only those.** A broken vendor does not hold the other five
  back. The run still reports failure — the red cell is the report — while the bump it produces
  carries every vendor that verified.

### 6.0 The shape "bump what passed" forces

Partial commit is not a flag on the previous bullet; it decides the workflow's topology, and it has
two invariants worth stating before anyone builds it.

**Fan-out, then collect.** Each per-vendor cell verifies *and emits the version it verified*; one
downstream job collects the emissions and writes the lockfile rows. The alternative — each cell
committing for itself — is either six PRs a week or six jobs racing to write one branch.

> [!WARNING]
> **The collector must not be gated on the fan-out succeeding.** A collector that runs only when every
> upstream job is green turns "bump what passed" back into "hold the whole set" the first time one
> vendor breaks — and it does so silently, because the workflow is already red for an unrelated
> reason, so nobody reads the skipped collector as the bug. The whole ruling lives or dies on the
> collector running after partial failure while the run still reports failure overall.

**The pin that gets committed is the pin that was verified.** *"Passed for sure"* has to mean a real
jail installed that exact version and ran it — not that `npm view` reported a `latest` which was then
committed unexercised. Resolving and verifying must be one act in one job, or the workflow becomes a
new way to land the 2026-08-20 failure: a pin nobody ran, chosen by the registry, committed by us.

**Verification spans the arches the pin will serve.** This doc exists because a version was fine on
linux-x64 and broken on linux-arm64 *at the same instant* (§5, Mode A). A bump verified on one arch
and committed for both would have cheerfully pinned `0.149.0` during the 37-minute window. So the
fan-out is vendor × arch, not vendor — and a vendor that passes on one arch and fails on the other has
**not** passed.

That satisfies P4 without a second reporting channel: the artifact is the run and the bump it lands,
the cadence is weekly, and the severity is ordinary.

### 6.1 The blocking gate: pinned, and one cell per mechanism

**Ruled 2026-08-21 (OQ-CI2): two cells.** Under P1 + P2 the required matrix is **one npm and one
`installer`**, installed from **pinned** bytes — not three, and not five. The other packs keep their
config-render assertion (§3, row 3) which needs no install at all, so per-pack surface coverage is
*unchanged* — that is the coverage worth having, and today it is hostage to an install it does not
need.

**The saving goes to the thin side, not to a fourth npm name.** §2.3 counts eight npm installs against
one `installer` install, with `agy` — the other `installer` pack — having no cell at all. So the
mechanism with one-eighth the coverage should get the second cell.

That is also where the work is, and the doc should not pretend otherwise: a pinned npm fixture is a
version string (§5.1.1), while a pinned `installer` fixture needs an installer URL the test controls
rather than a vendor's — a local server, or a `file://`-shaped equivalent if the declaration permits
one. The npm half is nearly free; the `installer` half is the actual engineering.

Which npm pack is the representative does not matter much, and an earlier revision of this section
overthought it. The four packs differ in one way that has a bug history — a scoped name
(`@openai/codex`) versus a bare one (`opencode-ai`), the distinction `splitNpmSpec` exists to get
right — but **that difference is a pure string-parsing question and it is already covered where it
belongs**: `npmlauncher_test.go`'s table pins both spellings, using the real package names, in
microseconds and with no container
([`npmlauncher_test.go:36-55`](../../internal/entrypoint/npmlauncher_test.go#L36-L55)).

Spending a container install to re-cover what a unit-test table already covers is precisely the waste
this doc is about, so it would be self-defeating to argue for a third cell on those grounds. Pick
either spelling for the integration cell; the parsing is not what the integration test is measuring.

### 6.1.1 Three triggers, matched to three causes

**Ruled 2026-08-21 (OQ-CI6): fixture pack for the blocking gate — and the real packs get a
path-filtered trigger.** That second half is what closes the coverage hole the fixture opens, and it is
a sharper statement of P1 than the version I wrote: *the trigger should match the causation.*

| Trigger | What runs | Why |
| :--- | :--- | :--- |
| **Every push / PR** | fixture cells: one pinned npm, one pinned `installer` | Mechanism coverage, fully deterministic. A pure function of the repo (P1). |
| **Push / PR touching `packs/**`** | real install for the **changed** packs | A manifest change *is* commit-caused. This is the only trigger on which "does `packs/codex/pack.json`'s declared package install?" is a question the commit raises. |
| **Weekly schedule** | all six vendors × arch, hard-fail, bump what passed (§6, §6.0) | Vendor drift is not commit-caused, so it does not belong on the push path (P4). |

Each row answers a question no other row can, and no row asks a question its trigger cannot cause.
That is the whole design in three lines, and the middle row is the reviewer's contribution — without it
a typo in a pack manifest would reach main and surface a week later, which was the honest cost of the
fixture route.

**What the middle row does not fix.** It is exposed to Mode A, because the shipped packs remain
unpinned until OQ-TP4's field lands, so it installs `@latest` and the registry still chooses the bytes.
That exposure is now *narrow* rather than eliminated: it lands only on PRs that touch `packs/**`, which
are rare, and it is diagnosable in one question — *did you change the package name?* If no, it is
upstream. Scoping the job to the **changed** packs rather than all six narrows it further. Worth
stating plainly rather than letting the three-row table imply Mode A is gone from the push path
entirely.

> [!WARNING]
> **A required check behind a `paths` filter can wedge a PR.** GitHub does not report a status for a
> job that a path filter skipped, so a check that is both *required* and *path-filtered* leaves every
> PR that does not touch `packs/**` waiting forever for a status that will never arrive. The escapes
> are a companion always-runs job that reports success when the real one is skipped, or not marking it
> required. Do not discover this by making it required and wondering why unrelated PRs stopped merging.
>
> On whether it *should* be required: yes, and the blast radius argument is what makes that safe. A
> vendor's bad publish can then block one rare PR instead of main — and the person who just edited
> `packs/**` is exactly the person equipped to say "that failure is not my change."

### 6.2 Warm prefix, one cold install (P3) — DEFERRED

> [!NOTE]
> **Ruled 2026-08-21 (OQ-CI5): deferred.** §6.1 and the warmup relocation (§5.2) land first, then the
> per-test install cost gets re-measured. This section is built only if a residual cost survives
> both — it is a cost optimisation resting on a prediction, and the prediction is cheap to check.
> Kept here in full because the invariant and the `SPEC_FILE` wrinkle below are the load-bearing parts
> and would have to be re-derived otherwise.

The per-workspace prefix is a product property and must stay. But a *test* may legitimately arrive
with a prefix that is already populated: the suite provisions one npm prefix once, and each test's
workspace state is **seeded** from it. Then §3's presence-and-config assertions cost no network, and
exactly one designated test per mechanism starts from an empty prefix to prove the install path
itself still works.

The invariant that keeps this honest: **exactly one test per install mechanism uses a cold prefix,
and it is named as such.** Without that, seeding silently deletes the install-path coverage — the
`AGENTS.md` "does it fail if I delete the call site?" test would go unanswered, which is how this
class of hole gets shipped.

One wrinkle the seeding must respect: the launcher reads a `SPEC_FILE`
(`~/.cache/yolo-agent-stamps/<bin>.spec`) to tell *"the declaration moved"* from *"the registry
moved"* ([`shims.go:398`](../../internal/entrypoint/shims.go#L398)). A seeded prefix whose spec file
is absent or stale is a different starting state than a warm jail, and the seed has to carry it or
the launcher's pinned branch behaves differently under test than in production.

## 7. Is this par for the CI course?

Partly. Long integration suites and slow macOS runners are ordinary. What is *not* ordinary is
letting a **required** check resolve a floating third-party dist-tag at test time. The
lockfile-everywhere convention exists precisely so that CI installs bytes chosen by the repo; the
`@latest` here is a deliberate product choice for *users* (an agent CLI should be fresh) that leaked
into the test path, where freshness has no value and determinism has all of it.

So the accurate framing is not "CI is slow, that's life" but: **the product wants floating versions
and the gate wants frozen ones, and today they share one mechanism.** §5.1 splits them.

## 8. What this does not propose

- **Not** baking agent CLIs into the image (§5.2).
- **Not** pinning what *users* get. §5.1's shape exists so `yolo pack update` still moves a user
  forward without a yolo release.
- **Not** dropping the install assertion. P3 keeps one cold install per mechanism, and §6.1 adds a
  cell for `installer` coverage that today rests on one pack.
- **Not** touching `network.mode`, the reachability witness, or anything in the loopback-TLS story.
  The `reachability_test.go:98` line in both runs (*"this jail published no endpoints"*) says that
  class is untested in CI, which is true, expected under the nested/shared-namespace carve-out, and
  a different doc's problem.
- **Not** a claim that upstream will stay broken. 0.149.0's arm64 tarball exists now and the codex
  cell passed on 2026-08-21. The proposal is about who chooses the bytes, not about this vendor.

## 9. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| Mark the agent-install job `continue-on-error` (advisory) | **Rejected**, §6. No owner, no artifact; converts a wrong signal into an ignored one. |
| Retry the install on failure | **Rejected.** The window was 37 minutes and a retry re-resolves the same absent tarball. |
| Gate codex out of the arm matrix, per the existing policy note (`agents_test.go:17`, `ci.yml:149`) | **Rejected for this case.** That policy is written for an agent with *no* linux-arm64 build; codex ships one. Applying it here surrenders real coverage to a transient. |
| Vendor/bake the CLIs into the image | **Rejected**, §5.2 — reverses a deliberate design and is pinning-by-staleness. |
| Lockfile pin (OQ-TP4 option (b), as written) | **Adopted for the SHIPPED packs.** The harness already owns the directory the lockfile lives in, so CI can author rows at the same path with no new concept (§5.1) — but it needs the `LockEntry` field first. |
| Fixture pack pinned via its `package` string | **Leading candidate for the blocking gate** (OQ-CI6). Uses only shipped mechanisms — the `file://` pack fixture and `npmspec`'s selector parsing — so it is unblocked today, and it lets the specimen be a small fast package instead of a 100 MB CLI (§5.1.1). Costs coverage of the shipped manifests, which §6 moves to the weekly job. |
| Manifest pin, or manifest-pin-plus-lockfile-override | **Withdrawn**, §5.1 — proposed in an earlier revision on reasoning that turned out to be wrong. Not needed for CI; any remaining argument for it is about a *user's* first run and belongs to OQ-TP4. |
| Have CI resolve a version once per run and reuse it across tests | **Rejected as insufficient**, OQ-CI1. Deterministic within a run, but a green main can still go red tomorrow with no commit — which is the P1 property being bought. |
| Raise the macOS cap and change nothing else | **Rejected**, §5.2. It does not fix Mode B, it *hides* it: §4.1 shows two-thirds of the blown budget was suite warmup, so a wider cap preserves the misattribution and must be re-widened whenever warmup grows. Available as a labelled stopgap, not as the answer. |
| Drop the macOS nightly's agent tests entirely | **Rejected.** macOS is the only place the podman-VM install path runs at all; deleting it trades a slow signal for none. |

## 10. Risks

| Risk | Mitigation |
| :--- | :--- |
| Seeding a warm prefix silently removes install-path coverage | The §6.2 invariant: one named cold-prefix test per mechanism, and it must fail if the launcher's install branch is deleted. |
| A pinned version gets unpublished (npm permits it within 72h) or the registry is down | P1 is about *who chooses* the bytes, not about eliminating the network. This reduces exposure to a publish race; it does not make CI offline-capable. Say so rather than implying immunity. |
| A pinned CI representative drifts far from what users run, hiding a real incompatibility | The weekly maintenance workflow (§6) is the detector, and it runs on a cadence rather than never. |
| Reducing the matrix to one npm pack hides a per-pack install quirk | The quirk that plausibly differs is scoped-vs-bare naming, and that is already a unit-test table (`npmlauncher_test.go:36-55`), not a container concern — §6.1. |
| A fixture pin, never bumped, eventually names a tarball that is unpublished or incompatible with the image's node | Deliberate for a *mechanism* test — stability is the point, and freshness is the weekly job's question (§6). But it is a pin with no owner unless the weekly workflow also touches it, so it should not be silently exempt from the bump. |
| A fixture pack diverges from how real packs are shaped, so the gate passes on a manifest form no shipped pack uses | The shipped packs' `surfaces` still render in every run (§3, row 3); what the fixture replaces is only the *install*. If a fixture ever needs a field no real pack declares, that is the signal it has drifted. |
| The two `installer` packs stay thinly covered | Called out in §2.3; `agy` has no cell at all today. Adding one is in scope for §6.1's "one cell per mechanism". |

## 11. What I would build, in order

First, move suite warmup out of the first timed test (§5.2). Mode B is entirely ours, it needs no
ruling on pinning, and it is the only item here that makes the *existing* numbers trustworthy — until
it lands, every duration in §4 overstates the first test and understates the rest. I would not split
`TestAgentToolsAvailable` to achieve it: two agents in one jail is the assertion that test exists to
make, and its cost was never really about the second install.

Second, the blocking matrix points at pinned bytes: one npm cell, one `installer` cell (§6.1), from
fixture packs. Per OQ-CI6 this needs nothing from `trust-paths.md` — a fixture pinned via its
`package` string uses only shipped mechanisms (§5.1.1) — so the previous revision of this section was
wrong to put "wait for the `LockEntry` field" ahead of it. The npm half is close to free; the
`installer` half, which needs an installer URL the test controls, is the real work.

Third, the `packs/**` path-filtered job (§6.1.1), which is what makes step two safe to take — it is
the trigger that keeps a manifest typo from reaching main once the every-push gate stops installing
real packs. Land it in the same change as step two, not after: between the two, the shipped manifests
have no install coverage at all. Read the required-check warning in §6.1.1 before marking it required.

Fourth, and independently of all the above, `LockEntry` grows OQ-TP4's npm-version field so the
**shipped** packs can be pinned for users. That belongs to `trust-paths.md` and is no longer a blocker
here — it is what makes "no evergreen npm" true of the product rather than only of CI, and it is also
what finally removes Mode A from the `packs/**` trigger.

Fifth, the weekly maintenance workflow (§6, §6.0): separate vendor × arch jobs, hard-failing,
fanning out to a collector that lands the pins which verified and leaves the rest alone. This is what
replaces "advisory", and it is deliberately after the pin — before it, the workflow would have nothing
to advance. Build the collector's partial-failure path first, or the "bump what passed" ruling is
unobservable until the week something breaks.

Sixth, re-measure, and only then decide whether §6.2's warm-prefix seam has anything left to remove
(OQ-CI5 defers exactly this).

## Open Questions

**None.** All six are settled — see the Decision Ledger below. The design is decided; nothing is built.

The one *external* dependency that remains is not a question of this doc's: OQ-TP4 in
[`trust-paths.md`](trust-paths.md) still owns the `LockEntry` npm-version field, which is what would
let the **shipped** packs be pinned for users. Per OQ-CI6 the blocking gate no longer waits on it
(§5.1.1), so it is product work rather than a blocker — but until it lands, the path-filtered
`packs/**` job installs `@latest` and Mode A survives on that one narrow trigger (§6.1.1).

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-CI1 | The pin is a **committed fixture, bumped weekly** — not harness-generated. Determinism within a run is not P1; and once the weekly workflow moves the pin, committing it makes that movement a reviewable diff rather than an invisible resolve. Supersedes a retracted manifest-pin proposal (§5.1) | 2026-08-21 | [§5.1.1](#511-and-the-blocking-gate-may-not-need-to-wait-for-it-at-all) |
| OQ-CI2 | **Two** install cells in the required matrix (one npm, one `installer`) — not three. The third-npm-cell idea is withdrawn: scoped-vs-bare parsing is already a unit-test table. The saving goes to the thin mechanism, where `agy` has no cell at all | 2026-08-21 | [§6.1](#61-the-blocking-gate-pinned-and-one-cell-per-mechanism), [§2.3](#23-two-mechanisms-six-packs-nine-installs) |
| OQ-CI3 | Weekly maintenance workflow: **separate jobs per vendor**, hard-failing, and **bump what passed** — a broken vendor does not hold the other five back. Forces fan-out-then-collect, a collector that survives partial failure, and verify-then-pin on every arch served | 2026-08-21 | [§6](#6-what-advisory-gets-wrong), [§6.0](#60-the-shape-bump-what-passed-forces) |
| OQ-CI4 | Suite warmup is paid in `TestMain`'s existing seam, before any timed assertion. The macOS cap raise (1200→2400s) is **withdrawn**: it padded a misattribution rather than fixing it | 2026-08-21 | [§5.2](#52-mode-b-is-ours-alone-and-the-fix-is-attribution), [§4.1](#41-the-first-test-is-the-suites-warmup-sink) |
| OQ-CI5 | Warm-prefix seeding **deferred**. Land §6.1 + OQ-CI4, re-measure, build the seam only if a residual per-test install cost survives | 2026-08-21 | [§6.2](#62-warm-prefix-one-cold-install-p3--deferred) |
| OQ-CI6 | **Fixture pack** for the blocking gate — unblocked today via the `file://` pack fixture and `npmspec`'s selector parsing — **plus a path-filtered trigger** so a `packs/**` change runs the real install for the packs it changed. Three triggers, each matched to a cause no other trigger can raise | 2026-08-21 | [§6.1.1](#611-three-triggers-matched-to-three-causes), [§5.1.1](#511-and-the-blocking-gate-may-not-need-to-wait-for-it-at-all) |

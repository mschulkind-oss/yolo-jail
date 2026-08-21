---
title: "Nine cold installs a run — what CI buys by downloading six agent CLIs"
date: 2026-08-21
status: in-review
tags: [ci, packs, testing, npm, cost]
summary: "The integration suite installs agent CLIs from the live npm registry nine times per run, unpinned and with --prefer-online, because the npm prefix is per-workspace and every test gets a fresh workspace. That buys two mechanisms' worth of coverage nine times over, and imports every registry hazard into the blocking gate. Two CI failures in two days, neither of them a bug in this repo."
---

# Nine cold installs a run — what CI buys by downloading six agent CLIs

**Status:** DESIGN SKETCH, 2026-08-21. Nothing built. Every claim about current behaviour is
traced to code and dated; the cost figures are read off two real CI runs.

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
mechanism rather than once per test, and upstream drift is discovered by a job that **produces an
artifact with an owner** — never by an "advisory" cell.

**The most important section is [§6](#6-what-advisory-gets-wrong)** — it is the one that decides
whether the rest is worth building, and it is where I think the usual answer to this problem is
actively wrong.

**Reads with:** [`trust-paths.md`](trust-paths.md) (OQ-TP4 is the *product-side* half of this
doc's §5.1 — the pin has nowhere to live; this doc cannot be fully built until that is ruled),
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

**P4. A signal with no owner and no artifact is not a signal.** This is the reason I will not
propose an advisory job (§6), and it generalises past this doc.

The verdict: keep testing that installs work — it is a real feature and it has broken for real —
but pay for it **once per mechanism, from pinned bytes**, and move "do the six vendors' current
releases still install?" out of the merge path and into something that leaves a durable trace.

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

### 5.1 Mode A is already ruled, and stuck on where a pin lives

`trust-paths.md` OQ-TP5 (2026-08-18) ruled: *"I don't want magical evergreen npm packages. If
there's a committed lockfile, install installs from that version, update is how you get new
versions."* Both behavioural halves shipped — the hourly poll only reports now, and
`yolo pack update` is the only act that resolves. What did not ship is the **record**: OQ-TP4 asks
where an embedded pack's npm version gets pinned, and its answer is empty.

That gap is exactly Mode A. `install` has no version to obey, so it falls back to `@latest`.

**A fact that constrains OQ-TP4's own leaning.** The doc leans to option (b), "the lockfile grows an
embedded section". The lockfile lives at `~/.config/yolo-jail/packs.lock.json`
([`lock.go:15,109`](../../internal/packsrc/lock.go#L15)) — beside the *user config* — and `.yolo/`
is `.gitignore`d, so there is no committed workspace state either. **A fresh CI runner therefore has
no lockfile row to obey**, hits (b)'s own acknowledged first-run hole, resolves `@latest`, and is as
exposed as today. (b) fixes the product and leaves CI where it is.

So I want to put a fourth shape in front of that question, and it belongs to OQ-TP4, not here:
**the manifest carries a known-good pin and the lockfile overrides it.** `install` obeys the
lockfile row if present, else the manifest pin, never `@latest`; `yolo pack update` writes the
lockfile, which wins from then on. This dissolves the trilemma using the doc's own stated costs —
(a)'s release-cadence coupling is gone because a user is never capped at yolo's release rate, (c)'s
do-nothing default is gone because something is always pinned, and (b)'s first-run hole is gone
because the manifest *is* the first-run answer. It is also the only shape where CI needs no setup:
the pin is in-repo by construction. A pack version string already carries a selector
([`npmspec.go`](../../internal/entrypoint/npmspec.go), 2026-08-17), so nothing new is expressible.

That is [OQ-CI1](#-oq-ci1--does-this-doc-presuppose-a-fourth-shape-for-oq-tp4) below, and it is
**blocked on the maintainer's ruling in `trust-paths.md`** — I am not going to fork a second pinning
policy in a second doc.

### 5.2 Mode B is ours alone, and the fix is attribution

No upstream involvement, and — given §4.1 — no coverage question either. The suite's one-time costs
should be paid **outside any timed test**: a suite-level warmup that launches one jail and installs
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

The property that makes a signal survive being ignored is that **it accumulates into something with
a name**. Two shapes have it:

- **A bump PR.** A scheduled job runs `yolo pack update`, commits the resolved versions, opens a PR.
  Upstream breakage shows up as *that PR being red*, pre-merge, attributed to the bump. It is
  reviewable, it is closeable, and it never touches main. This repo already runs a weekly
  `flake.lock` bump, so the cadence and the review habit exist.
- **A tracked issue.** A scheduled job that files (and auto-closes) one issue per broken vendor. An
  issue has an assignee and a history; a yellow cell has neither.

Either satisfies P4. `continue-on-error` satisfies neither. **My recommendation is the bump PR**,
because it is the same artifact that carries the fix — the PR that tells you codex is broken is the
PR you don't merge — and because it needs no new reporting machinery.

### 6.1 The blocking gate: pinned, and one cell per mechanism

Under P1 + P2 the required matrix becomes: **one npm pack and one `installer` pack**, installed from
**pinned** bytes. Two cells, both deterministic. The other packs keep their config-render assertion
(§3, row 3) which needs no install at all, so per-pack surface coverage is *unchanged* — that is the
coverage worth having, and today it is hostage to an install it does not need.

Which npm pack should be the representative is a real question, not a coin flip: the four differ in
scoped-vs-bare package naming, which is precisely what `splitNpmSpec` exists to get right. A bare
name (`opencode-ai`) and a scoped name (`@openai/codex`) are different inputs to the one function
whose bug history is about exactly that distinction.

### 6.2 Warm prefix, one cold install (P3)

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
| Pin in the manifest only (OQ-TP4 option (a)) | **Rejected** on the doc's own grounds: yolo's release cadence becomes the ceiling on CLI freshness. §5.1's shape keeps (a)'s CI benefit without that ceiling. |
| Lockfile-only pin (OQ-TP4 option (b), as written) | **Insufficient alone** for CI: the lockfile is user-scoped and `.yolo/` is gitignored, so a fresh runner has nothing to obey (§5.1). |
| Raise the macOS cap and change nothing else | **Rejected**, §5.2. It does not fix Mode B, it *hides* it: §4.1 shows two-thirds of the blown budget was suite warmup, so a wider cap preserves the misattribution and must be re-widened whenever warmup grows. Available as a labelled stopgap, not as the answer. |
| Drop the macOS nightly's agent tests entirely | **Rejected.** macOS is the only place the podman-VM install path runs at all; deleting it trades a slow signal for none. |

## 10. Risks

| Risk | Mitigation |
| :--- | :--- |
| Seeding a warm prefix silently removes install-path coverage | The §6.2 invariant: one named cold-prefix test per mechanism, and it must fail if the launcher's install branch is deleted. |
| A pinned version gets unpublished (npm permits it within 72h) or the registry is down | P1 is about *who chooses* the bytes, not about eliminating the network. This reduces exposure to a publish race; it does not make CI offline-capable. Say so rather than implying immunity. |
| A pinned CI representative drifts far from what users run, hiding a real incompatibility | The bump PR (§6) is the detector, and it runs on a cadence rather than never. |
| Reducing the matrix to one npm pack hides a per-pack install quirk | The quirk that plausibly differs is scoped-vs-bare naming (§6.1); whichever representative is chosen, the other spelling should keep a cell. |
| The two `installer` packs stay thinly covered | Called out in §2.3; `agy` has no cell at all today. Adding one is in scope for §6.1's "one cell per mechanism". |

## 11. What I would build, in order

First, move suite warmup out of the first timed test (§5.2). Mode B is entirely ours, it needs no
ruling on pinning, and it is the only item here that makes the *existing* numbers trustworthy — until
it lands, every duration in §4 overstates the first test and understates the rest. I would not split
`TestAgentToolsAvailable` to achieve it: two agents in one jail is the assertion that test exists to
make, and its cost was never really about the second install.

Second, settle OQ-CI1 by settling OQ-TP4, because everything about pinning downstream of it is
unbuildable until the record has a home. That is a ruling, not a build.

Third, once a pin exists, point the blocking matrix at pinned bytes and add the missing `installer`
cell. This is the P1 fix and it is small: the manifests already accept a selector.

Fourth, seed the warm prefix and name the cold-install tests (§6.2). This is the largest change and
the only one that touches the harness's shape, which is why it is last — it is a cost fix, and by
this point the correctness fix has already landed.

Fifth, the weekly bump PR, replacing "advisory" with an artifact that has an owner.

## Open Questions

1. 🔒 **OQ-CI1: does this doc presuppose a fourth shape for OQ-TP4?** §5.1 argues the lockfile-only
   pin (OQ-TP4 option (b)) cannot serve CI, because the lockfile is user-scoped and `.yolo/` is
   gitignored, and proposes *manifest pin + lockfile override*. Everything in §6.1 depends on some
   pin existing that a fresh CI runner can see.

   **What it blocks:** the entire P1 half of this proposal. Without a repo-visible pin, the blocking
   gate keeps resolving `@latest` and Mode A stays live no matter what else we change.

   _Leaning:_ manifest-pin-plus-lockfile-override, for the reasons in §5.1 — it is the only shape
   that is simultaneously deterministic for CI, cadence-independent for users, and answers (b)'s
   first-run hole. But this is a ruling on **`trust-paths.md`'s** question, not this doc's, and I
   would rather it be recorded there and referenced here than forked.

   **Blocked on:** OQ-TP4 in [`trust-paths.md`](trust-paths.md).

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-CI2: how many npm packs does the *blocking* matrix need?** P2 says one; §6.1 notes the
   four differ in scoped (`@openai/codex`) vs bare (`opencode-ai`) naming, which is exactly the
   distinction `splitNpmSpec` exists to handle and has had a bug in.

   **What it decides:** whether the required gate has two install cells (one npm, one installer) or
   three (both npm spellings, one installer) — and therefore whether ~7 of today's 9 installs
   disappear or ~6.

   _Leaning:_ **three cells** — one scoped npm, one bare npm, one installer. One extra install is a
   cheap price for keeping the one input distinction with a real bug history, and it still deletes
   two-thirds of the current install load.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-CI3: where does upstream drift get reported?** §6 rejects `continue-on-error` on P4 and
   recommends a weekly bump PR over an auto-filed issue.

   **What it decides:** whether "do the six vendors' current releases still install?" is asked by
   something with an owner, or drops off the board entirely. Note that "nothing" is a defensible
   answer — if we pin and the bump PR is the only detector, that *is* the cadence, and no separate
   drift job is needed.

   _Leaning:_ the **bump PR**, and no separate advisory job at all. The PR that tells you a vendor is
   broken is the PR you don't merge; a second reporting channel would be a signal competing with an
   artifact.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-CI4: where does suite warmup get paid?** §4.1 measured that the first container test
   carries ~100s (x64) / ~680s (macOS) of one-time suite cost — image load, first container start,
   mise provisioning, cold npm cache — and is then judged against a per-command cap sized for
   steady-state work.

   > This question originally asked *"how much headroom should the macOS cap have?"* and leaned
   > 2400s. That was the wrong question: it accepted the misattribution and negotiated its size.
   > Same ID, restated — nothing outside this doc referenced it.

   **What it decides:** whether Mode B is fixed or merely padded, and whether per-test durations in
   this suite are comparable to each other at all.

   _Leaning:_ an explicit **suite-level warmup** in `TestMain`'s existing seam, before any timed
   assertion. It changes no coverage, makes every test's duration mean the same thing, and leaves the
   1200s cap measuring what it was sized for. The alternative — keep warmup inside whichever test
   happens to sort first and widen the cap to fit — needs re-widening every time warmup grows.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 **OQ-CI5: does the warm-prefix seam (§6.2) earn its complexity once §6.1 has landed?** Cutting
   the matrix to two or three pinned cells already removes most of the 9 installs. Seeding adds a
   harness component, a new starting state to keep faithful (the `SPEC_FILE` wrinkle), and an
   invariant someone must maintain.

   **What it decides:** whether step four of §11 happens at all.

   _Leaning:_ **defer it.** §6.1 cuts the `PackInstalls` matrix from five install cells to two or
   three — on x64 that is 68.9s → ~28s, on macOS 802s → ~320s — and OQ-CI4 relocates the warmup that
   dominates the rest. Between them I expect little residual per-test install cost for a seam to
   remove. I would rather re-measure after those two land than build the seam on a prediction; if the
   numbers land where I expect, this question closes as "not needed".

   **Answer:**
   > _(empty — fill in when decided)_

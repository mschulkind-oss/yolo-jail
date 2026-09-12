# Active plans & designs

**Status:** INDEX — **rebuilt against the tree 2026-08-23.** The rows for the macOS track, the
guest-notch handoff, `pack-system`, `environment-manager-plan`, `agent-config-packs`,
`antigravity-agy-support` and `cli-color-audit` were re-checked against the code that day, and
**four were wrong in the direction that matters** (D1 retired, D2 reverted, D3 superseded, and
`cli-color-audit` was finished — that row listed two remaining items, both of which had landed). The remaining rows carry their doc's own dated status,
unverified here. **If a row disagrees with the doc it points at, trust the doc and fix the row.**

**Extended 2026-09-12** with a section for [the five designs the last sprint built](#the-2026-09-sprint--five-designs-all-built),
which had no rows at all — the same failure one level up, since a doc this file never names cannot
be caught disagreeing with it.

This directory holds the **active** work — plans and designs we're currently
implementing or still discussing. Reference docs (how live systems work) live in
[`../design/`](../design) and [`../research/`](../research); done/obsolete
working docs are archived in git history (see [`doc-triage.md`](doc-triage.md)
for the classification and `git log --follow` to recover any).

> **Where to start:** [`roadmap.md`](roadmap.md) — the living forward plan, and the
> only doc here that answers "what is left?". Everything else in this directory is
> either a design/handoff for one piece of work or a historical record.
>
> [`sequencing-2026-07.md`](sequencing-2026-07.md) is the retired predecessor: a
> 2026-07-22 snapshot of the dependency ordering, kept for "why was this done in
> that order" rather than "what is next".
>
> **For the composed-config / packs cluster specifically**, start at
> [`BACKLOG.md`](BACKLOG.md): that cluster's design spans 8 docs, and BACKLOG is the
> only place that lists the implementable items in order, with a pointer per item to
> the doc holding its reasoning.

<!-- Three docs outside this file link the anchor below by its old literal text
     (AGENTS.md, roadmap.md, further-roadmap-ideas.md). The heading dropped its count
     when a sixth check was added; this keeps those links resolving. Do not delete it
     without fixing all three. -->
<a id="keeping-this-corpus-honest--the-five-checks-so-they-are-re-runnable"></a>

## Keeping this corpus honest — the checks, so they are re-runnable

The 2026-08-23 audit ran the first five by hand; four of them found something, and the fifth is
worth keeping because it is now clean and would not stay that way silently. The 2026-09-12 sprint
close-out ran all five again and added a **sixth**, which found a class the other five are
structurally unable to see. They are not wired into `just`
yet — that proposal, with the allowlists it needs, is
[`further-roadmap-ideas.md`](further-roadmap-ideas.md) §I1. Until then, run them when a sprint
closes; the drift clusters there rather than spreading evenly.

```console
# 1. Every relative doc link resolves.              (found: 5, now 0)
# 2. Every live open question is countable.         (found: 6 invisible to the first regex)
$ rg -c '^(#{2,4} |\s*[0-9]+[a-z]?\. |\s*[-*] )(<a id="[^"]*"></a> ?)?💬' docs/ --sort path
# 3. Every backticked SHA resolves in THIS repo.    (found: 3 phantoms cited as evidence)
$ git rev-parse --verify --quiet <sha>^{commit}
# 4. Every backticked code path exists.             (found: 1 real rot among 15 hits)
# 5. Every in-doc heading link resolves.            (clean — but only with a CORRECT slugger:
#    GitHub maps each space to its own hyphen, so an em-dash heading yields `--`. A naive
#    slugger collapses them and reports 67 false positives.)
# 6. Every file:line citation points where it says. (no one-liner — see below)
```

**Checks 3 and 4 need an allowlist or they cry wolf**: upstream `flake.lock` revs and other
projects' source are legitimately unresolvable, and a doc *recording a deletion* is supposed to name
the thing it deleted. The signal is a path or SHA offered as **evidence**, not one named as history.

**`uvx vantage-check docs/` subsumes checks 1 and 5** and is the gate a doc commit passes:
`link/missing-target` is check 1, and `link/dead-section-anchor` is check 5 for both same-document
and cross-document anchors (verified 2026-09-12). It does **not** subsume 2, 3, 4 or 6 — it
validates that links resolve, never that a claim is true.

### Check 6 — every `file:line` citation points where it says it does

Added 2026-09-12. It is the only one of the six that finds **moved** things: checks 3 and 4 ask
whether a SHA or a path *exists*, and a citation whose file exists and whose line has drifted
eighty lines passes both of them while being exactly the wrong kind of wrong — a `file:line` is
where a reader stops checking.

There is no one-liner. It is a five-stage pipeline, and the stages exist because each one's output
is the next one's input:

1. **Extract.** Walk `docs/**/*.md` and match `<path>.<ext>:<start>[-<end>]` for
   `go|nix|sh|py|ts|jsonc|json|toml|lua|md`, recording the citing doc, its line number, the raw
   citation, and the whole source line as context. Track fenced code blocks and **flag** the
   citations inside them rather than dropping them — a pasted transcript is not a claim.
2. **Resolve.** Build a basename index of the repo, skipping `.git`, `vendor`, `dist-go`, `.yolo`,
   `node_modules`, `bin`, `.direnv`, `result`, `.claude` and `examples`, then map each citation to a
   real file: exact relative path first, then unique basename, then unique suffix match. The
   basename step carries most of the corpus, which writes citations short (`apply.go:170`,
   `run.go:82`) far more often than it writes them from the repo root.
3. **Disambiguate.** A basename with several candidates is resolved from the **citing document's
   own context** — the nearest other citation, or bare path mention, in that same doc that names
   one of the candidates wins. This is what decides *which* `prism.go` a bare `prism.go:494-511`
   meant, and it is why the pipeline cannot be one pass.
4. **Check bounds.** A citation whose start line is past the resolved file's EOF is stale, full
   stop. This half needs no judgement and should be treated as a finding, not a candidate.
5. **Check the symbol.** Take the backticked identifiers adjacent to the citation on its own line,
   keeping only plausible code names — CamelCase or `_`-bearing, five characters or more, so
   `HostRenderResult` counts and `the` does not — and ask whether **any** of them appears within
   **±6 lines** of the cited range. If none does, find where the nearest one actually lives and
   record the distance. **That distance is the triage order**: a symbol nine hundred lines from its
   citation is drift, and a symbol seven lines away is the window being one line too tight.

**The output is candidates, not findings.** The ±6 window is a guess and the near end of the
distance ranking is full of honest near-misses, so every row wants a human look. What the check buys
is the ranking: it takes every `file:line` in the corpus down to a couple of hundred worth reading,
sorted worst-first.

**What it found, and where the residue is.** Run against the tree at `1118cc52^` — before the
sprint's two citation sweeps — the corpus held 1,831 `file:line` citations outside code fences and
the pipeline flagged **234**: 128 in `docs/design/`, 93 in `docs/plans/`, 13 in `docs/research/`.
Re-run on 2026-09-12 against the swept tree it flagged **137**, and the split is the whole finding
— `docs/design/` fell from 128 to 33, `docs/plans/` moved from 93 to 91, and `docs/research/` did
not move at all. **The residue is a queue, not a false-positive tail**: the sweeps walked the
sprint's own design docs and never walked the other two trees, which is where the next run starts.
Both totals move whenever anyone edits a doc, so re-run rather than trust them; the durable part is
the concentration, not the count.

**Where the scripts are.** They were written in the close-out session's scratchpad as
`extract.py` → `resolve.py` → `disambig.py` → `check.py` → `symcheck2.py`, passing JSON between
stages. A scratchpad does not survive its session, which is why the method is written above as prose
precise enough to rebuild from: a check that lives only in a scratchpad is not re-runnable, and this
file carries no scripts for the other five either.

### Check 2 has two known errors, and a convention question under each

Both were found on 2026-09-12 by tallying two docs by hand against what the regex scored them.
Neither is fixable by editing the regex alone, because each rests on a convention the corpus has
not settled.

**It counts 💬 only, so a 🔒 question is invisible.**
[`minimal-disk-footprint.md`](../design/minimal-disk-footprint.md) states in its own header that
[`OQ-DF4`](../design/minimal-disk-footprint.md#112-open-questions) is its **only live question**
and that it is blocked on a measurement rather than undecided. The question is written
`4. 🔒 **…**`, so the regex scores that file **zero** and the doc does not appear in the output at
all. A check whose stated purpose is *every live open question is countable* cannot be blind to the
marker that means **live, but blocked**. — **The convention question:** is 🔒 a state a *question*
can be in, in which case the regex adds it and the corpus-wide count goes up? Or is 🔒 reserved for
roadmap **rows**, in which case a blocked question stays 💬 and names its blocker in prose? Both
readings are in use today, and one has to lose before the regex can be corrected.

**It over-reads where 💬 marks sequencing rather than a question.**
[`broker-ca-and-nested-hosts.md`](../design/broker-ca-and-nested-hosts.md) scores **5** and holds
**3** questions. [§7](../design/broker-ca-and-nested-hosts.md#7-open-questions) has three;
[§8](../design/broker-ca-and-nested-hosts.md#8-sequencing) then uses 💬 as a **status marker** on
build-order items 3 and 4, each of which points back at one of those same three. The regex counts
list items beginning with 💬 and cannot see that two of them are references to questions counted
already. — **The convention question:** does 💬 mean *an open question lives here* or *this item is
not done*? Sequencing lists use it for the second, Open Questions sections for the first, and the
check only works once it means exactly one of them.

### One cluster of `vantage-check` errors is data, not rot

**Almost every error the corpus has left is in a single file, and all of them are deliberate.**
[`../research/vantage-check-0.5.9-findings.md`](../research/vantage-check-0.5.9-findings.md) was
**49 of the 103 errors** `uvx vantage-check docs/` reported at the start of the 2026-09-12
close-out, and **48 of the 53** it reported an hour later, after the other clusters had been
worked. Its own number barely moves while the corpus total collapses toward it, so any figure
written here is stale on arrival — **the shape is the durable fact, not the count**: this one file
is essentially the whole residual, and every one of its errors is `ref/unlinked-section` or
`ref/unlinked-oq` fired on an unlinked section number or open-question id.

That document is a defect report **about vantage-check's own reference rules**, written for
upstream, and every one of those bare ids is a **specimen** it quotes to show what the rules do to
them. They are data, not references: linking a specimen destroys the thing being shown. The doc
says so in a note at its top and files the behaviour as its own defect D12. (This paragraph
deliberately does not quote one, for the same reason — quoting a specimen here would move the
cluster rather than describe it.)

A future reader working the corpus toward zero needs this, or they will make a doc's examples wrong
in the course of making its link check green — and the closer the rest of the corpus gets to clean,
the more this one file looks like the only thing left to fix. It is not. (The doc's own header
quotes a figure of its own, taken when it was written; it drifts for the same reason.)

---

## macOS revival + distribution

The two macos-user designs the 2026-09 sprint built are listed with the rest of that sprint, in
[The 2026-09 sprint](#the-2026-09-sprint--five-designs-all-built) below.

| Doc | What it is | Status |
|---|---|---|
| [roadmap.md](roadmap.md) | **THE forward plan.** Everything still to do and nothing else — grouped by state (💬 needs you / 📦 ready / 🔒 waiting / 🧊 icebox), counted from its own contents, and citing every open question by ID in the design doc that holds it. As of 2026-09-03 (post-compaction): **twelve** 💬 rows and **four** 📦 items. ⚠ *On 2026-09-02 this cell claimed thirteen and five while the file held twelve and four* — off by one in both columns, found 2026-09-03 by tallying instead of reading. Trust the roadmap's own header, which counts from its contents; this cell is a hand-maintained summary and has been wrong once. | **Start here for "what is left?"** |
| [further-roadmap-ideas.md](further-roadmap-ideas.md) | **Candidates, not a queue.** Seven proposals from the 2026-08-23 doc audit — five build, two rule-first — plus **three rows that should LEAVE the roadmap** ([§4](further-roadmap-ideas.md#4-two-rows-already-on-the-roadmap-that-i-would-drop), §[4a](further-roadmap-ideas.md#4a-a-third-row-i-would-drop-added-after-the-deeper-pass)) and §[4b](further-roadmap-ideas.md#4b-what-the-verification-pass-changed-about-this-file), which records what an adversarial re-check did to the file itself. Nothing here is committed. | **Read when the queue is empty** |
| [shipped-2026-08-pack-batch.md](shipped-2026-08-pack-batch.md) | HISTORY of the ten-item pack batch: the rulings, the notch-divergence audit, and the NINE defects that surfaced only by running the lifecycle. Kept for the reasoning, not for planning. | ✅ done 2026-08-04 |
| [handoff-macos-user-open-threads.md](handoff-macos-user-open-threads.md) | What the FIRST end-to-end hardware run of the macos-user runbook left open (2026-09-12). All ten items now measured and three defects fixed that day, so this is not a "does it work" doc — it is one confirmed product defect (`lsp_servers` installs nothing here, and the ruling on whether to wire it or restore the warning is the maintainer's), the twin suite poisoning itself in test order on a persistent Mac, three items with no automated twin, and an unfiled `--dry-run` secret-disclosure question. | **Handoff** — one ruling needed; the rest is work nobody has done. |
| [handoff-guest-notch-macos.md](handoff-guest-notch-macos.md) | The `guest` notch (env-manager Phase 7) plus every other Mac-gated item, in one place so one trip to a Mac can close all of it. Its old lead — the G3 bug, macos-user rendering ZERO pack surfaces — was **fixed on 2026-08-12** (`2bb792ff`); the live lead is now the Mac's config still using the removed `agents` key, which refuses every launch there. | **Handoff** — Phase 7 not built; host/Mac-gated, not design-blocked. |
| [macos-revival-and-distribution-plan.md](macos-revival-and-distribution-plan.md) | The macOS-backend revival + source-distribution roadmap (Tracks J/D/M). | **In progress, and three of those letters do not mean what this row said** (re-checked 2026-08-23): **D1 landed and was RETIRED** (`20a8ce9f`), **D2 landed and was REVERTED** (`5d34dece` — a missing repo root is fatal again), **D3 was superseded** by the prebuilt-bundle cutover, and J1.3 shipped inside `internal/builder`, which is deleted. J1.1/J1.2/J1.4, J2 and J3 hold. Track M M0/M1/M2 passed on real HW 2026-07-21 but **M2 has lapsed** — that Mac is 531 commits stale on the removed `agents` key. D4 needs a first push and a Mac download. |
| [handoff-cachix-cache.md](handoff-cachix-cache.md) | Procedure to publish the prebuilt OCI image to a Cachix binary cache (= revival plan **D4**). | **Working, and this row was the correct half of a disagreement that doc carried** — settled from the Actions log 2026-09-02: run `31749547095` (`v0.8.0`, both arches) pushed both variants AND substituted the four this-repo-source paths back from the cache. Substituter live at `flake.nix:13-16`. Only the **Mac download proof** remains (hardware-gated). Two caveats: the push is **tag-triggered only**, so the cache holds `v0.8.0` and nothing newer; and the six CI `nix build` sites were missing `--accept-flake-config`, so the flake's substituter was being discarded — fixed 2026-09-02. |

## Post-Go-port backlog

The archived `go-port-post-transition.md` (git history) queued work for after the
Python→Go cutover. Its distribution section landed. The still-open items are now tracked
here:

| Doc | What it is | Status |
|---|---|---|
| [nix-ld-dynamic-linking.md](nix-ld-dynamic-linking.md) | Replace the `LD_LIBRARY_PATH=/lib:/usr/lib` whack-a-mole with nix-ld so the mise node + MCP servers link env-free (closes the custom-`mcp_servers` startup gap). | **Shipped** — IMPLEMENTED 2026-07-22 as Variant A; the design doc and this plan both say so, and this row did not. Corrected 2026-09-09. |
| [cli-color-audit.md](cli-color-audit.md) | Make `prune`/`builder`/`macos-*` render rich markup to ANSI instead of stripping it; consolidate the duplicated printers. | ✅ **DONE** — and this row was two items stale (fixed 2026-08-23). Both things it listed as remaining have landed: `run/console.go` consolidated onto `internal/richtext` (`67454a8`; verified at `internal/cli/run/console.go:1-18`) and the TTY probe unified onto `internal/tty` (`b76b2ba`). The doc itself has said DONE since 2026-07-22. |
| [module-consolidation-and-cleanup.md](module-consolidation-and-cleanup.md) | Collapse the ~34 Python-mirroring `internal/*` packages into native-Go structure; drop parity machinery; [§4 OSS-hygiene remnants](module-consolidation-and-cleanup.md#4-oss-hygiene-remnants-mostly-done--verify--close). | **Done** (2026-07-21); package-merge declined. |

## Test-suite speed

| Doc | What it is | Status |
|---|---|---|
| [integration-parallelism.md](integration-parallelism.md) | Bounded `t.Parallel()` for the container suite, after per-test GlobalStorage isolation unsticks the shared `last-load` sentinel race. | **Parked** — CI is free + the fast local loop skips these tests; the launch-merges (done 2026-07-20) were the cheaper win. Pick up only if the full local `just test` becomes a friction. |

## Other

| Doc | What it is | Status |
|---|---|---|
| [agent-settings-composition.md](agent-settings-composition.md) | Design of record: layered regeneration of any generated config (agent settings + MCP/LSP/mise/identity) + a Lua transform (format-agnostic, user-scope-only, no source mutation). | **Phase C complete 2026-07-22** — the prism is the unconditional config path at boot + check; the bespoke agent-config `Configure*` writers are deleted. mise/identity surfaces still deferred. |
| [cache-relocation.md](cache-relocation.md) | User-scope-only `cache_relocations` so a large cold cache subdir (`huggingface`, 185 GiB) can live on other storage, mounted read-write nested inside `.cache`. Read straight from the user config — never the merged config or the jail-writable snapshot. Also unblinds `prune`/`purge` and fixes the hint that recommends the symlink trick that dangles in-jail. | **Implemented 2026-07-21** — work items 1–10 landed and verified end to end in a nested jail; `yolo cache relocate` (item 11) deferred; one host-gated acceptance step (a real cross-filesystem move) outstanding. |
| [antigravity-agy-support.md](antigravity-agy-support.md) | Support Google Antigravity CLI (`agy`) as a native agent inside `yolo-jail`. | **✅ Done 2026-07-22**, and since RESHAPED — agy is a **pack** now and the agent registry it was added to is deleted (see the warning atop that doc). Born directly on the prism; all eight touchpoints landed (registry, `agySettings` surface, `AgyDir`, `ConfigureAgyPrism`, boot, preflight, docs, tests). |
| [agent-config-packs.md](agent-config-packs.md) | Proposal: share agent environment config (skills, AGENTS.md fragments, settings) between people by `(repo, path, branch)` with no PR — user-scope `packs`, host-side blobless fetch, content-addressed trees, pin/rollback, cross-agent projection. Includes the scope verdict (in yolo-jail, one extractable package) and the landscape research it rests on. | **Proposal** — largely OVERTAKEN: the `packs` key, host-side fetch, the lockfile and the origin gate all shipped in the 2026-07/08 pack work, so read this for the *landscape research* and the scope verdict rather than as a plan. (It used to cite "ROADMAP open item 5", a numbering the 2026-08-17 restructure retired.) |
| [../reference/pack-system.md](../reference/pack-system.md) | The pack system, whole: the `contributes[]` manifest, the kinds + footprints + conflict rules, the one-writer rule, the compose engine + `derive`, selection/fetch/origin-gate. | **Shipped** — this is the current design of record for authoring/debugging/changing a pack (the reform that produced it is complete and its plan is retired). |
| [environment-manager-plan.md](environment-manager-plan.md) | Sequences [../design/yolo-as-environment-manager.md](../design/yolo-as-environment-manager.md) into buildable phases: the render-path collapse (= BACKLOG Stage G), the `confinement` dial, `apply`/`describe`/`--at`/`--sealed`, dep-provisioning, the `guest` notch, self-describing briefings. | **Mostly BUILT** (re-checked 2026-08-23) — Phases 0–6, 8 and 9 have shipped: the data-loss fix, `internal/render` + `Target`, the confinement dial, `describe`/`apply`/`--at`/`--sealed`, `check-deps`, the notch briefing, and autonomy-as-a-notch-policy. **Phase 7 (the `guest` notch) is the only unbuilt phase** and is host-gated, not design-blocked. |
| [perf-logging.md](perf-logging.md) | Performance logging across the yolo lifecycle behind `--timing` (grown) and a new global `--verbose`; `internal/perf` spans the launch, the child window (with podman-cleanup attribution), and both shutdown arms, because "who holds my shell prompt 30s after the agent exits?" had no answerable spelling. Reference, as built: [../reference/perf-logging.md](../reference/perf-logging.md). | **BUILT 2026-09-06** (`f9eee104`..`03b18afb`) — flag, env gates, spans, Window A attribution, unit + integration pins, verified in a nested jail. Its OQ list was triaged 2026-09-08 and is **empty**: the rename was ruled, `--verbose`'s vocabulary was always a policy rather than a pending decision, and the remaining four are fix candidates the spans exist to NAME — decided, each waiting on a span, none waiting on a person. **H1 is still unconfirmed** — a nested jail is structurally blind to it, so the settling measurement is one `--timing` quit on the real host. |

| [../design/the-load-sentinel-is-not-a-liveness-oracle.md](../design/the-load-sentinel-is-not-a-liveness-oracle.md) | Why a ten-entry list of recently-LOADED store paths is the wrong evidence for "is anything using this image" — the 2026-09-08 incident that killed four long-running jails, the two reapers that consult it, and the one that still does. Image half fixed in `feddc5e0`. | **Draft 2026-09-08; all three RULED 2026-09-08** — [OQ-LS1](../design/the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger) went AGAINST its leaning (the GC-root reaper gets an age cutoff, not the liveness veto — liveness is a wrong predictor of future want in both directions), [OQ-LS2](../design/the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger) replaced a "dim line" with an error where the user asked and loudness where a decline is impossible, and [OQ-LS3](../design/the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger) found `keep` is the wrong MECHANISM, not the wrong number — the window is global, so its unit becomes the configuration. Records a contradiction between `imageroots.go` and `autoload.go` worth fixing on sight. |
| [../design/layer-aware-image-delivery.md](../design/layer-aware-image-delivery.md) | Replace `streamLayeredImage` + `podman load` with nix2container + `skopeo copy`, so a rebuild ships the layers that changed instead of all 3.47 GB. Measured: 81.0s of a 96.1s load is the stream; the customisation layer is 0.78% of the image. | **Draft 2026-09-08; all five RULED 2026-09-08.** [OQ-LI1](../design/layer-aware-image-delivery.md#91-decision-ledger) was right-sized rather than answered as posed: the cachix was already shipped, `cache.nixos.org` is the default underneath it, and the patched skopeo is a **34-second** cold build (MEASURED) of which only two closure paths are built — so the copier is simply built when needed, and the cache may never be load-bearing. [OQ-LI5](../design/layer-aware-image-delivery.md#91-decision-ledger) DELETED the legacy stream instead of sizing a rollback window, which retires risk R3 and makes the pre-flip measurement the safety property (R8). Remaining gate is a human one: the go/no-go plus one `nix:`-copy boot per backend. |

## Provider & profile machinery

Docs written 2026-08-29 → 2026-09-09 around one question: how does a provider (a z.ai key, a
Bedrock role) reach every selected agent. One parent design, the counter-design it answers, the
first consumer, two review docs that measured what actually shipped — and, added 2026-09-09, the
two docs in this family that are still live: Bedrock's native arm and the switching problem it
split off. The two implementation plans that carried the build order are **deleted** (2026-09-09):
both shipped whole on 2026-09-02, and a plan that outlives its work is a trap for the next reader,
so their surviving traps went into [providers.md](../reference/providers.md) as warnings and the
rest is in `git log`. **Live questions: 💬 17, 💬 24 and 💬 26** in
[roadmap.md](roadmap.md); 💬 18 and 💬 19 left that file on 2026-09-02 with the commits that closed
them. Rows checked against the tree 2026-09-09.

| Doc | What it is | Status |
|---|---|---|
| [../design/profiles-as-pack-variants.md](../design/profiles-as-pack-variants.md) | The parent design. `kind: "profile"` is a named variant of **one pack's own** declarations — the shipped `autonomy` contribution given an open selector, not a new merge engine — `providers` stays a config key with a stricter schema, and the missing process-env channel is what had Claude's Bedrock case hardcoded in Go. [§2](../design/profiles-as-pack-variants.md#2-what-already-exists--measured-not-assumed) measures what already shipped instead of assuming it. | **DECIDED 2026-09-01** (ledger, [§14](../design/profiles-as-pack-variants.md#14-decision-ledger)) and **built** — the implementation shipped through `980aed71`, including [§12](../design/profiles-as-pack-variants.md#12-what-i-would-build-in-order) step 6's `config-overlay` `profile` gate. Its own follow-up note reports the re-measurement clean and files the edge defects as [OQ-PT1](../reference/providers.md#why-its-this-way)–PT5 in [providers.md](../reference/providers.md). |
| [../design/pack-profiles.md](../design/pack-profiles.md) | The counter-design the parent answers: a dual layer of `kind: "provider"` + `kind: "pack-fragment"` under RFC-7386 merging, plus `env_sources`/`api_key_env` secret hygiene. | **DRAFT 2026-08-29, nothing built, superseded as a direction.** Read [§2](../design/pack-profiles.md#2-diagnosis-what-exists-today-and-why-it-breaks)'s diagnosis and [§4](../design/pack-profiles.md#4-the-secrets-issue-decoupling-configuration-from-credentials)'s credential architecture (adopted by the parent as recommendation plus mechanism), not the schemas — its own note says the doc is unchanged and points at the parent's [§9](../design/profiles-as-pack-variants.md#9-where-this-differs-from-pack-profilesmd-and-why) point-by-point diff. |
| [../reference/zai-plumbing.md](../reference/zai-plumbing.md) | The first real consumer: both routes to "one provider, every agent" — name the protocol and fill the values ([§3](../reference/zai-plumbing.md#the-resolution-table)), or ship a layered zai pack the user drops a key into ([§4](../reference/zai-plumbing.md#what-a-user-actually-does)) — and the endpoint-by-protocol resolution behind `-p zai` ([§5](../reference/zai-plumbing.md#what-a-user-actually-does)). | **DECIDED 2026-09-01** (ledger, [§8](../reference/zai-plumbing.md#why-its-this-way)); **and now BUILT** (corrected 2026-09-09): `packs/zai` ships the provider + profile, `internal/cli/run/providerpreflight.go` is in the tree, and pi/opencode selection landed in `6d1d7c54`. [§5](../reference/zai-plumbing.md#what-a-user-actually-does)'s resolution table now speaks the canonical vocabulary (`db6aff96`), and its follow-up note hands the codex dialect question to [providers.md §3](../reference/providers.md). |
| [providers.md](../reference/providers.md) (was design/docs/reference/providers.md, distilled 2026-09-03) | The defect report on that shipped machinery: **eleven** defects, D1–D11, of which three share one cause — a value validated against a set yolo owns and handed verbatim to consumers that own different sets. D1 is the only one that puts a wrong value in a file an agent reads (`wire_api`, the protocol field, was four borrowed spellings naming three protocols). D9 outgrew the doc to [`trust-paths.md`](../design/trust-paths.md)'s census. | **DECIDED 2026-09-01** (ledger, [§11](../reference/providers.md)) and **built** — D1's three-name vocabulary + per-agent dialect maps (`0f04632d`), D2's composer refusal (`5d8bd1fe`), D4 (`868b610f`), D5's `--timing` split (`886a9191`) and D6's census (`67f87f36`) are in the tree, `integration/providers_test.go` pins D1 (`cee9c1fc`), and D10/D11 — filed 2026-09-02 while paying §[3.0a](../reference/providers.md)'s verification debt — landed the same day (`7fa624ba`). |
| [providers.md](../reference/providers.md) (was design/docs/reference/providers.md, distilled into the same reference) | Splits the knot the two above left tangled: **catalog** (the agent's directory of providers it *could* use) and **selection** (which one it *does* use) are two features, and one table drives both — measured in a live jail, `-p zai` changes the behaviour of one agent in four. Also dissolves disable-without-deleting. | **DECIDED 2026-09-01** (ledger, [§10](../reference/providers.md); a tenth question was withdrawn as never having been a design question) and **built** 2026-09-02 — [§3](../reference/providers.md)'s empty pi row was filled from source (`070a3574`), which is what unblocked pi's and opencode's selection keys (`6d1d7c54`), and selection landed for all four agents. [§8](../reference/providers.md)'s own order has one residue: step 4, option C's explicit disable, is still unbuilt. |
| [../design/bedrock-plumbing.md](../design/bedrock-plumbing.md) | Bedrock's native arm: codex, opencode and pi each ship an `amazon-bedrock` provider on one credential, so what yolo owes is a region, a key and a model id — but the three default to different endpoint families with different model-id spellings, so the design ships the family twice. | **Design sketch, nothing built** (verified 2026-09-09). Seven questions, routed 2026-09-09 as 💬 24 in [roadmap.md](roadmap.md). Added to this section 2026-09-09 — it was the live doc the section did not list. |
| [../design/provider-switching.md](../design/provider-switching.md) | Split out of the doc above: a model id is provider-local, so every provider switch is a rename, and the selection state machine has no fourth row for it — a deselected profile leaves the agent asking a new endpoint for the old provider's model. | **Design, nothing built.** Four questions, routed 2026-09-09 as 💬 26 in [roadmap.md](roadmap.md). Added to this section 2026-09-09. |

## The 2026-09 sprint — five designs, all built

The sprint that closed on 2026-09-12 built five accepted designs, and **none of them had a row
here**. That is this index's own rule failing one level up: a row that disagrees with its doc is
wrong and gets fixed, but a doc with no row cannot be caught disagreeing with anything. Each status
below is the doc's own header, read 2026-09-12.

Whether each has earned a `system-doc` graduation was assessed the same day, per doc, walked against
the code rather than against the status line:
[`doc-triage.md`](doc-triage.md#the-2026-09-12-graduation-assessment--the-five-docs-the-sprint-built).
**One graduates**; the other four are held, each for a different named reason, and the sequencing is
that file's [OQ-DT1](doc-triage.md#open-question). No graduation has been performed.

| Doc | What it is | Status |
|---|---|---|
| [../design/report-tiers.md](../design/report-tiers.md) | `yolo host apply` stated every fact and never its **result**, so the reader was left adding the lines up. One **verdict line** per run, and a **report tier** on every line beneath it — notch facts, run facts, losses and blockers — where the tier, not the emitter, decides whether a line prints by default, once per run, or only under `--verbose`. A declared dependency that is missing becomes a blocker rather than a line in the middle. | **SHIPPED 2026-09-12** — all eight steps of [§9](../design/report-tiers.md#9-what-i-would-build-in-order) landed and all seven questions are ruled ([§11](../design/report-tiers.md#11-decision-ledger)). Measured after the build: the default report is **30 lines where it was 278**, with the 267-line detail view behind `--verbose`. `AGENTS.md` cites its [OQ-RO3](../design/report-tiers.md#11-decision-ledger) as the standing *a launch has no quiet mode* rule. Its implementation sketch, [../design/report-tiers-plan.md](../design/report-tiers-plan.md), is CONSUMED history — the design wins on any disagreement. **The one of the five that should graduate first.** |
| [../design/config-ownership-and-promotion.md](../design/config-ownership-and-promotion.md) | Who owns an agent's config file. yolo infers it from the confinement notch, and adopting `yolo host apply` is precisely the act of leaving the world that inference is sound in — so ownership becomes one declared user-scope key (`host_management: none \| assert \| own`), `own` makes the host render like a jail, and `yolo config promote` lifts a captured in-jail edit into the conventional local pack instead of discarding it. | **BUILT 2026-09-12.** All eight of [§10](../design/config-ownership-and-promotion.md#10-what-i-would-build-in-order)'s steps landed, step 8's **one-time adoption archive** last: `entrypoint.archiveAdoption`, one call from the stateful writer both notches share ([§6.3.3](../design/config-ownership-and-promotion.md#633-what-survives-as-a-guard), [OQ-CO7](../design/config-ownership-and-promotion.md#13-decision-ledger)). Every question the design opened is ruled, and so is the one the BUILD opened — [OQ-CO12](../design/config-ownership-and-promotion.md#13-decision-ledger) settled the `assert` → `own` switch as **keys-and-values invariant, not byte-invariant** (2026-09-12), leaving the two key deletions it measured as bugs rather than as conformance — **both fixed the same day**. ⚠ It **reverses four rulings** recorded in [environment-manager-plan.md](environment-manager-plan.md#open-questions-to-resolve-before-their-phase). |
| [../design/lua-transform-removal.md](../design/lua-transform-removal.md) | Remove the `config.lua` transform slot between the merge and the managed-enforce step: no user, its one worked example now served by the declarative `autonomy` kind, its determinism requirement stated in a doc and enforced by nothing. What makes it a design rather than a `git rm` is that `luahook`'s VM is also the shipped packs' `derive.lua` path — so the cut runs along a seam the doc draws, and carries the managed floor (`Enforce`) out into `internal/agentcfg` where it belonged. | **SHIPPED 2026-09-12** — both questions ruled ([§13](../design/lua-transform-removal.md#13-decision-ledger)) and the removal landed in [§10](../design/lua-transform-removal.md#10-what-i-would-do-in-order)'s order: `internal/agentcfg` no longer links gopher-lua, `internal/packload` still does, and the derive path renders every shipped pack at boot. Two documentation rows of [§5.6](../design/lua-transform-removal.md#56-documentation) are deliberately open. Held from graduation on purpose: a removal doc has no system to describe, and [pack-system.md](../reference/pack-system.md) is already the reference for the half that survived. |
| [../design/macos-user-home-tiers.md](../design/macos-user-home-tiers.md) | macos-user has one sandbox home, so the machine, workspace and session tiers are the same directory — which content delivery turned from a static leak between workspaces into a **write-write race on the briefing an agent reads as instructions**. `HOME` stays where it is; every directory the container backends bind from `<workspace>/.yolo/home/` becomes a symlink into that same sidecar, and every pack-declared machine-scope directory is mirrored so the credential hook's relative link keeps resolving. | **BUILT 2026-09-12**, with no migration step, as ruled. All four questions are settled ([Decision Ledger](../design/macos-user-home-tiers.md#decision-ledger)). [§10](../design/macos-user-home-tiers.md#10-what-shipped) is written to separate what a test pins from what is reasoned and still owed a Mac — read it before trusting any runtime claim. |
| [../design/macos-user-provisioning.md](../design/macos-user-provisioning.md) | The other half of the same gap: a container jail gets its tools from an image **floor** and an imperative **stage**, and macos-user had neither, so `mise_tools`, `lsp_servers` and `mcp_presets` render config and install nothing ([§2](../design/macos-user-provisioning.md#2-what-this-costs-today)). Two separable halves in order — a core set for the noncontainer nix profile, then the container's own `setupScript` body run as a new Seatbelt-confined step between the bootstrap and the agent. | **BOTH HALVES BUILT 2026-09-12** ([§9](../design/macos-user-provisioning.md#9-what-shipped-half-one), [§10](../design/macos-user-provisioning.md#10-what-shipped-half-two)); all four questions ruled ([Decision Ledger](../design/macos-user-provisioning.md#decision-ledger)). ⚠ **Every runtime claim is UNMEASURED** — both halves were implemented from a Linux jail, where there is no `sandbox-exec` and no `_yolojail` account. What *is* measured is the nix evaluation and the Go half's unit tests; that the closure builds on a Mac, that the confined stage can reach the network, and what a first launch costs are owed a hardware run ([§10.8](../design/macos-user-provisioning.md#108-what-a-mac-has-to-settle)). |

Both macOS rows are held from graduation for the same structural reason, and it is not editorial:
every doc in [`../reference/`](../reference) carries a `verified_commit` in its frontmatter, and
neither of these can carry one until a Mac has actually run what they built.

## Track M verification runbooks

[`runbooks/`](runbooks/) holds the Mac hardware verification procedures — they
are the revival plan's Track M gates, not user-facing reference (they moved here
from `docs/guides/runbooks/`). See the [sequencing-2026-07](sequencing-2026-07.md#runbooks) for their
status:

| Doc | What it is | Status |
|---|---|---|
| [runbooks/mac-macos-user-e2e.md](runbooks/mac-macos-user-e2e.md) | You-drive macos-user acceptance-bar test (the M1 anchor). | **Passed** (2026-07-21); M1 gate green; kept as repeatable procedure. |
| [runbooks/mac-ac-container-builder.md](runbooks/mac-ac-container-builder.md) | Zero-sudo Apple Container builder proof; Track-M/J3-adjacent. | **Passed** (2026-07-17) — kept as the repeatable procedure. |
| [runbooks/mac-go-port-verification.md](runbooks/mac-go-port-verification.md) | Go-vs-Python diff verification of the port. | **Stale** — recommended for `git rm` (its diff-against-Python method is dead post-wipe). |

Related live tracker: [`../research/macos-support-matrix.md`](../research/macos-support-matrix.md)
is the authoritative state-of-the-macOS-backend matrix.

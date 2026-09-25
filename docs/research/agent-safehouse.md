---
title: "Agent Safehouse — the neighbouring tool, and what yolo should take from it"
date: 2026-09-18
status: in-review
tags: [research, macos, seatbelt, macos-user, packs, threat-model, comparison]
summary: "Agent Safehouse confines an agent that runs AS YOU, by enumerating what it may touch; yolo-jail gives the agent a different machine and a different identity, so there is nothing of yours to enumerate. On the one axis where they build the same artifact — the macos-user Seatbelt profile — theirs is decisively tighter and ours is barely tested, and that is the adoption surface. Their per-agent prose is the better human artifact and the worse machine one: measured on their own repo, the enforced half moved 106 commits to the documented half's 3, and their opencode investigation documents a program three major versions and one whole reimplementation behind the profile that ships beside it."
vantage:
  status-chip: true
---

# Agent Safehouse — the neighbouring tool, and what yolo should take from it

**Status:** CURRENT — gathered 2026-09-18 against Agent Safehouse `v0.12.0`, HEAD `3b22b30`
(2026-09-13), read from a full clone of
[eugene1g/agent-safehouse](https://github.com/eugene1g/agent-safehouse) and from
[agent-safehouse.dev](https://agent-safehouse.dev/). The yolo half was checked against this
tree the same day. Perishable facts are fenced in
[Fast-moving](#11-fast-moving--verify-before-building).

**Three of the five adoptions have shipped** — [§8.1](#81-a-policy-assertion-suite-for-macos-user--highest-value-moderate-cost),
[§8.2](#82-tighten-the-profile-toward-deny-default--highest-ceiling-highest-cost)'s three
incremental denies, and [§8.4](#84-pin-the-agent-launch-argv--tiny-cost-real-bug-class), on
2026-09-18. What each one left standing is stated at its own heading; the yolo-side columns in
[§3.1](#31-the-base-posture-is-inverted) moved with them. **Re-checked 2026-09-24:** the [§8.1](#81-a-policy-assertion-suite-for-macos-user--highest-value-moderate-cost)
suite has since executed on a macOS runner, and all fifteen cases pass.

**Reads with:** [`sandbox-comparison.md`](sandbox-comparison.md), which does the same job for
Claude Code's own built-in sandbox and states the default-deny argument this doc does not
repeat.

> **In short.** The two projects answer different questions, and almost every difference falls
> out of that one fact. Safehouse asks **"what may this agent touch on my machine?"** and
> answers with a policy. yolo asks **"what machine does this agent get?"** and answers with an
> environment. A Safehouse agent runs as you, in your home, holding your `gh` token; a yolo
> agent runs as a different identity with an empty wallet. Neither is the strict superset —
> Safehouse's policy is far more precise about what it allows, and yolo's boundary is far more
> structural about what it never has to allow.

---

## 1. Verdict first

| Axis | Who is ahead | Confidence |
| :--- | :--- | :--- |
| Precision of the Seatbelt policy | **Safehouse, decisively** | High — read both sources |
| Evidence the Seatbelt policy works | **Safehouse, still ahead** | High — 389 policy assertions on macOS CI against our 15 runtime cases, which [§8.1](#81-a-policy-assertion-suite-for-macos-user--highest-value-moderate-cost) added on 2026-09-18 and which now run green on the `macos-user` CI job (checked on the 2026-09-24 run) |
| Structural strength of the credential boundary | **yolo** | High — separate account and separate home vs. grants inside your own |
| Predictable tooling inside the boundary | **yolo** | High — their own docs concede the category |
| Human-readable per-agent knowledge | **Safehouse** | High — 10,020 lines of it, and yolo publishes none |
| Per-agent knowledge that is *current* | **yolo** | High — resolved against ground truth in [§5.4](#54-where-they-disagree-about-the-same-agent) |
| Onboarding cost | **Safehouse, by a lot** | High — two commands, no image, no config file |
| Disclosure at launch | **yolo** | High — theirs prints nothing unless asked |
| Protection against a repo that supplies its own policy | **Safehouse** | Medium — see [§7.2](#72-where-they-genuinely-disagree) |

**The one-line thesis.** Safehouse is the best-in-class version of the layer yolo's own
[`macos-no-vm-direction.md`](../reference/macos-no-vm-direction.md) calls *"the part borrowed
from prior art"* — so everything worth adopting sits in that borrowed layer, and nothing worth
adopting threatens the layer above it.

---

## 2. What Safehouse is — measured, not from the tagline

A macOS-only sandbox wrapper: pure Bash plus [SBPL](https://newosxbook.com/src.jl?tree=listings&file=sbtool.c)
— the **Sandbox Profile Language**, Apple's undocumented Scheme-like policy language read by
`sandbox-exec(1)`. There is no daemon, no container, no VM and no compiled binary.

Its own `AGENTS.md` self-describes as *"a macOS sandbox wrapper for coding agents … Policy
model is strict by default: start from `(deny default)`, then add explicit allow rules via
layered `.sb` profiles."*

| Fact | Value | Source |
| :--- | :--- | :--- |
| Version / HEAD | `v0.12.0` / `3b22b30`, 2026-09-13 | clone, 2026-09-18 |
| Age | 312 commits since 2026-02-09 | `git log` |
| Licence | Apache-2.0 | `LICENSE` |
| Runtime | 5,783 lines of Bash under `bin/` | `wc -l` |
| Policy | 2,919 lines of SBPL under `profiles/` | `wc -l` |
| Tests | 77 `.bats` files, **389 test cases** | `grep -c '^@test'` |
| Agent profiles | 14 | `profiles/60-agents/` |
| App profiles | 4 (Claude, Codex, Cursor, VS Code) | `profiles/65-apps/` |
| Per-agent investigations | 13, totalling **10,020 lines** | `docs/docs/agent-investigations/` |

> [!NOTE]
> **The "fourteen agents" figure is the profile count, not the investigation count.** There are
> 14 agent profiles and 13 published investigations: `amp` has a profile and no investigation,
> and `gemini-cli.md` documents the agent whose profile is spelled `gemini.sb`.

The whole product ships as one self-contained file — `dist/safehouse.sh`, policy and runtime
concatenated — installable by `brew install` or a single `curl`.

### 2.1 The policy assembly, which is the design

Rules are concatenated in a fixed order and **SBPL is last-match-wins**, so order *is* the
mechanism. From their
[policy-architecture](https://agent-safehouse.dev/docs/policy-architecture): *"Later rules win.
If behavior is unexpected, check ordering first."*

```text
00-base.sb              (deny default) + HOME_DIR / WORK_DIR macros
10-system-runtime.sb    exec, /usr /bin /opt /nix/store, tmp, ptys, mach services
20-network.sb           network open
30-toolchains/*.sb      node, python, go, rust, ruby, java, … (only the relevant ones)
40-shared/*.sb          ~/.skills, ~/.agents, ~/AGENTS.md, ~/CLAUDE.md
50-integrations-core/   ALWAYS ON: git, scm-clis, and two DEFAULT-DENY modules
55-integrations-optional/  opt-in, one file per --enable=<feature>
60-agents/*.sb          selected by the wrapped command's basename
65-apps/*.sb            selected by app bundle
<dynamic>               --add-dirs-ro, --add-dirs, wide-read, THE WORKDIR GRANT
<appended>              --append-profile, loaded last so its denies win
<terminal denies>       emitted unconditionally last
```

Three things in that order are worth stealing the *idea* of, independent of macOS:

- **The `--enable` vocabulary is derived from the filesystem, not a list.** The feature catalog
  is built by listing `profiles/55-integrations-optional/*.sb` and stripping the extension, so
  adding a file adds a feature and nothing else has to be edited. That is the same structural
  move as yolo's *packs are the vocabulary*.
- **Two always-on modules exist only to deny**, `container-runtime-default-deny.sb` and
  `ssh-agent-default-deny.sb`, each denying both the file path and the `network-outbound`
  `unix-socket` spelling of the same socket, with a comment saying the second is
  defence-in-depth because `connect()` on a UNIX socket is classified as network, not file.
- **Terminal denies are emitted after everything**, including after user-appended profiles, so
  the agent can never be granted write access to the policy that governs it
  ([§7.2](#72-where-they-genuinely-disagree)).

---

## 3. Mechanism — the two Seatbelt profiles, side by side

This is the only axis on which both projects build the same artifact, so it is the only one
where a direct comparison is fair. yolo's is generated by `SeatbeltProfile` in
[`internal/macosuser/seatbelt.go`](../../internal/macosuser/seatbelt.go); there is no `.sb`
template anywhere in the tree, the profile is Go string concatenation.

> [!IMPORTANT]
> **`macos-user` is not yolo's shipped macOS default.** Auto-detection tries `container` then
> `podman`; `macos-user` lives in `paths.NativeRuntimes` and is *deliberately never probed*, so
> it is reachable only by naming it ([`macos-no-vm-direction.md`](../reference/macos-no-vm-direction.md)).
> Everything in this section compares an opt-in yolo backend against Safehouse's only mode.

### 3.1 The base posture is inverted

| | Safehouse | yolo `macos-user` |
| :--- | :--- | :--- |
| Base | `(deny default)` | `(allow default)` |
| Shape | deny-all, then ~2,900 lines of enumerated allows | allow-all, then a short list of targeted denies, each carrying a `#seatbelt-test-id:` that a runtime case proves |
| File **write** | denied by the base; granted per path | `(deny file-write* (subpath "/"))` then a 7-entry allowlist |
| File **read** | denied by the base; granted per path | **allowed by default**, minus the denies named in the rows below |
| Network | `(allow network-outbound (remote ip))` — open, deliberately | no directive at all — open |
| `process-exec` | `(allow process-exec)` — explicit | no directive — open |
| `mach-lookup` | **enumerated allowlist of 16 global names** | no directive — open |
| Cross-process argv/env | **denied**: `(deny sysctl-read (sysctl-name-regex #"procargs"))` + `(deny process-info-pidinfo)`, re-allowed only for `(target same-sandbox)` | **denied, since `474f68a0`** — the same three directives, appended AFTER the `(allow process-info*)` this column used to report, because last-match-wins |
| `file-ioctl` | restricted to tty/pty devices by literal and regex | **restricted, since `474f68a0`** — the same shape, four tty/pty patterns |
| `sysctl-write` | denied by the base | open |

**The honest summary of ours:** yolo's `macos-user` profile is a **write-confinement profile
with four read carve-outs** (`/Volumes` minus the boot volume, raw `disk`/`bpf` devices,
`/Users` minus re-allows, `/Library/Keychains`). Every operation class SBPL can express and the
profile does not name — network, exec, mach lookup, signals, IOKit, `sysctl-write`, and
anything Apple adds later — is permitted. Safehouse's profile denies all of those until
something asks.

**On precision, Safehouse wins and it is not close.** Their profile is also better *commented*:
nearly every grant carries a one-line justification on the same line, which makes the generated
policy readable as an audit artifact rather than merely as configuration.

### 3.2 Where yolo's shape is nonetheless stronger

The comparison inverts on the credential axis, and for a structural reason rather than a policy
one. Safehouse's `HOME_DIR` is **your** home. yolo's `macos-user` runs as a **separate macOS
account**, `_yolajail`'s home being `/Users/_yolojail`, and the profile denies reads of
`/Users` wholesale with re-allows only for `/Users`, `/Users/Shared`, the workspace, and that
sandbox home — each re-allow written `(literal …)` rather than `(subpath …)` precisely so a
sibling checkout is not re-exposed.

So the two tools deny the *host user's* secrets by different means and reach different places:

| | Safehouse | yolo `macos-user` |
| :--- | :--- | :--- |
| `~/.ssh/id_*` | denied (no grant exists) | denied (inside the `/Users` read-deny) |
| `~/.ssh/config`, `~/.ssh/known_hosts` | **granted read**, by `50-integrations-core/git.sb` | denied |
| `~/.config/gh` (GitHub token) | **granted read *and write***, by `50-integrations-core/scm-clis.sb`, always on | denied |
| `~/.aws`, `~/.config/gcloud` | denied unless `--enable=cloud-credentials` | denied |
| macOS Keychain | denied unless `--enable=keychain` or a profile requires it | `/Library/Keychains` denied; `/System/Library/Keychains` is **not** denied and `security` is not blocked from exec |
| The agent's own credential store | **is yours** — `~/.claude`, `~/.codex` are the real ones | a fresh store in a different account |

That `~/.config/gh` row is the single sharpest mechanical difference in the whole comparison.
Safehouse hands the agent your real GitHub token, read **and** write, by default, in an
always-on core module, and says why in the file itself: *"Threat-model note: preventing
exfiltration/C2 is NOT a goal; this keeps SCM workflows available to agents."* yolo's whole
premise is that this cannot happen, because the token was never in the jail's home to begin
with — [`agent-credentials.md`](../reference/agent-credentials.md): *"host credentials are
physically absent from the jail"* and *"There is no deny-read list to get right."*

Neither is wrong. They are different products. But a reader choosing between them should know
that **Safehouse's default posture includes the agent holding a live GitHub credential**, and
that this is a deliberate, documented choice rather than an oversight.

### 3.3 Evidence — where yolo is weakest

Safehouse's 389 `bats` cases run on **macOS runners on every push and PR** touching `bin/` or
`profiles/` (`tests-macos.yml`), and a second workflow drives real agent TUIs through tmux
under the sandbox — scheduled weekly on purpose, *"to detect breakage when unpinned tool
versions change."* The policy suite asserts denials by name, with stable ids embedded in the
profiles themselves (`#safehouse-test-id:container-runtime-socket-deny#`,
`#safehouse-test-id:cross-process-procargs-deny#`) so a rule and the test that proves it are
greppable from each other.

yolo has a macOS CI job for this backend ([`.github/workflows/macos-user.yml`](../../.github/workflows/macos-user.yml))
and it is well built — `YOLO_TEST_MACOS_USER=1` makes a run that executes *zero* macos-user
tests exit non-zero, which is a better guard than Safehouse has. But what it exercises is
**provisioning, the tool floor, home tiers and layout refusals**. The Seatbelt tests in
[`internal/macosuser/seatbelt_readonly_test.go`](../../internal/macosuser/seatbelt_readonly_test.go)
and `seatbeltcapture_test.go` assert the **generated text and its ordering** — 16 tests, none
of which runs a command under the profile and checks that the kernel refused it. *(That was the
state on 2026-09-18; the runtime suite [§8.1](#81-a-policy-assertion-suite-for-macos-user--highest-value-moderate-cost)
describes shipped the same day and has run green on the macOS job since.)*

> [!WARNING]
> **This is the exact "pins the callee while the call site is unpinned" class that
> [`AGENTS.md`](../../AGENTS.md) warns about**, one level up: the tests pin the string yolo
> emits, and nothing pins that the string does anything. A sibling instance was found while
> gathering this doc and is worth its own fix — `PlanInvariants` pins `sandbox-exec -f <profile>`
> for the *provisioning* stage and `CapturePlanInvariants` pins it for the *capture* driver, but
> **nothing pins the argv that actually launches the agent**. It is built correctly; deleting
> that line would leave the suite green.

To be fair to yolo: the claim "no kernel has ever loaded this profile" is **stale and should
not be repeated** — [`internal/macosuser/capture.go`](../../internal/macosuser/capture.go)
records that the session profile was measured on hardware 2026-09-10 via a manual runbook item,
and the capture profile has been kernel-loaded since. The gap is not "unverified"; it is
**"verified once, by hand, and not asserted continuously."**

---

## 4. Threat model, stated precisely for each

Both projects write theirs down, which is rarer than it should be. They are not the same
promise.

### 4.1 Safehouse's, in their words

> "Start from deny-all. Allow only what the agent needs to do useful work. Keep developer
> workflows productive." — `README.md`

> "It is a hardening layer, not a perfect security boundary against a determined attacker." —
> `README.md`

> "Agent productivity is prioritized over paranoid lockdown. The goal is practical damage
> reduction and stronger defaults, not perfect isolation against a determined attacker." —
> [overview](https://agent-safehouse.dev/docs/overview)

Their stated non-goals are explicit and unusually complete:

> Safehouse does **not** fully protect against: "Data exfiltration over network from files the
> sandbox is allowed to read. Sandbox escapes (it is not a VM/hypervisor boundary). Abuse
> through already-allowed IPC/credential channels. Leakage of data from explicitly allowed
> paths." — [overview](https://agent-safehouse.dev/docs/overview)

And in the network profile itself: *"blocking exfiltration/C2 is explicitly NOT a goal for this
sandbox."*

**So the promise is: the blast radius of a mistake or an injection is bounded to the project
directory plus the paths you granted.** It is a *write-containment and read-minimisation*
promise, and it explicitly does not cover anything leaving the machine.

### 4.2 yolo's, in its words

The boundary is the **credential boundary** and it is stated as a structural property, not a
policy one:

> "yolo-jail's credential story is **structural, not a policy one**: host credentials are
> *physically absent* from the jail … There is no deny-read list to get right."
> — [`agent-credentials.md`](../reference/agent-credentials.md)

> "The network is open; the wallet is empty." — the same text every jail briefing carries

Since 2026-09-17 the boundary has a hard floor: a workspace that **is** or **contains** `$HOME`
or either of yolo's own state dirs is refused before any side effect, by
`paths.WorkspaceScopeBreach` and `refuseWorkspaceScope`, and — alone among yolo's guards — it
carries **no escape hatch at all**: *"an env var that voids the credential boundary would be a
documented way to void it."*

yolo's stated non-goals are equally explicit and are concentrated in
[`host-execution-from-the-workspace.md`](../reference/host-execution-from-the-workspace.md):

> "`/workspace` is a live bind of a host directory, and it is the product … a jail session
> writes a file there, and some *host* program later reads that file and executes what it says,
> as you."

> "**The honest claim, with no inflation: this stops zero prompt injections.**"

### 4.3 The two promises, contrasted

| Threat | Safehouse | yolo |
| :--- | :--- | :--- |
| Agent deletes/overwrites files outside the project | **Blocked** by the kernel | Blocked — the host filesystem is not mounted (containers) / write-denied (macos-user) |
| Agent reads `~/.ssh/id_ed25519` | **Blocked** | Blocked |
| Agent reads and uses your GitHub token | **Allowed by default** ([§3.2](#32-where-yolos-shape-is-nonetheless-stronger)) | Impossible — it is not there |
| Agent exfiltrates what it *can* read | Out of scope, stated | Out of scope; what it can read is smaller |
| Poisoned dependency running in-project | Out of scope — it runs with the agent's grants | Same, but the grants reach no host credential |
| Credentials the agent legitimately holds | Out of scope, stated | Out of scope — `env_sources` and the OAuth brokers are *deliberate* channels |
| A repo whose content makes the **host** execute something later | Partly: the `.safehouse` config is deny-write, `--append-profile` files are deny-write | Not solved. `workspace_readonly` and per-side shadowing shipped; the `.git` control plane is a **declined** mitigation with a stated reopen condition |
| Sandbox escape | Out of scope, stated | Out of scope on `macos-user`; container backends claim a stronger boundary and say so |

**The one place their coverage genuinely exceeds ours** is the last-but-one row, and it is
narrow but real: Safehouse guarantees the sandboxed agent cannot rewrite the policy file that
governs the next run. yolo has an analogue — `workspace_readonly` self-locks the workspace
config file when any entry is active — but that self-lock **does not hold on `macos-user`**,
which is the backend where the comparison applies.

---

## 5. Per-agent knowledge — the most interesting axis

Both projects encode the same class of fact: *what does agent X read and write?* They encode it
in opposite forms, and the trade is real in both directions.

### 5.1 The two artifacts

**Safehouse publishes prose.** Each investigation is a structured report with a pinned upstream
commit, a version, a source-availability line, `[INFERRED]` markers on inferences, numbered
sections for credentials / config / filesystem / network / extension points / sandboxing
recommendations, three summary tables (*All Filesystem Paths Accessed*, *All Network
Endpoints*, *All System Interactions*), an *Attack Surface Summary*, and a *Files Analyzed*
list at the end.

That provenance discipline is **better than what this repo asks of itself** and is worth
naming: analysis date, pinned commit, explicit inference marking, and the evidence list. It is
a genuinely good format.

**yolo declares data.** The same knowledge lives in `packs/<agent>/pack.json` as typed
contributions that the boot path renders. The real field names, since they matter for any
proposal built on them:

- kinds: `program`, `skills`, `briefing`, `files`, `config`, `config-overlay`, `state`,
  `reads-host`, `mount`, `env`, `hook`, `autonomy`, `profile`, `provider`, `loophole`,
  `service`, `blocked-tool`, `requires`
- a config surface carries `agent`, `name`, `path`, `codec`, `mode`, `defaults`, `managed`,
  and **`readsHost`** — a boolean on a surface, spelled exactly that way, used by exactly two
  shipped packs ([`packs/claude/pack.json`](../../packs/claude/pack.json) and
  [`packs/pi/pack.json`](../../packs/pi/pack.json))

> [!WARNING]
> **`writable_home_dirs`, `host_files` and `mise_tools` are user-config keys, not pack schema.**
> `grep -rn 'writable_home_dirs\|host_files\|mise_tools' packs/` returns zero hits. Do not write
> a proposal that puts them in a manifest. And `readsHost` (a surface boolean) is a different
> thing from `kind: "reads-host"` (a contribution, taking a `host` field, used by no shipped
> agent pack).

### 5.2 The trade, honestly

| | Safehouse prose | yolo declarations |
| :--- | :--- | :--- |
| Auditable by a human with no tooling | **Yes** | Poorly — a reader must know 18 kinds |
| Portable to another tool | **Yes** — it is just facts about the agent | No — it is yolo's schema |
| Covers *why*, and what the agent does at runtime | **Yes** — network endpoints, telemetry, hooks, attack surface | No — only what yolo provisions |
| Cannot drift from what runs | No — it is a separate artifact | **Yes** — it *is* what runs |
| Machine-checkable against the agent | No | Partly — probe tests pin the strings |

Their prose covers things yolo's declarations structurally cannot: which telemetry endpoints an
agent contacts, what its hook system can execute, what its settings *cannot* control. yolo has
no artifact of that kind and would benefit from one.

### 5.3 The drift, measured on their own repo

The argument that a declaration cannot drift and a document can is usually asserted. Here it is
measured, over Safehouse's full history (2026-02-09 → 2026-09-13):

| Tree | Commits touching it |
| :--- | ---: |
| `profiles/` | **106** |
| `docs/docs/agent-investigations/` | **3** |

Last edit per artifact, for the five agents both projects cover:

| Agent | Investigation last edited | Profile last edited |
| :--- | :--- | :--- |
| `claude-code` | 2026-02-16 | 2026-03-23 |
| `codex` | 2026-02-16 | **2026-08-14** |
| `copilot-cli` | 2026-03-10 | **2026-08-16** |
| `opencode` | 2026-03-27 | 2026-03-22 |
| `pi` | 2026-02-16 | 2026-02-19 |

The in-document *Analysis Date* fields are older still: eleven of thirteen say `2026-02-12`, and
two (`auggie`, `kilo-code`) say `2025-07-01` — fourteen months before this reading.

**And the two artifacts disagree inside Safehouse itself.** The claude-code investigation's
*All Filesystem Paths Accessed* table lists `~/.claude.json` and `~/.mcp.json` with access
`Read`; `profiles/60-agents/claude-code.sb` grants both **read and write**. A defensible reason
exists — a profile may legitimately be wider than observed behaviour, since `mkdir -p` and
atomic-rename need write on things a reader only reads — but the doc's column header is
literally *Access*, so a reader using the published doc to reason about blast radius gets the
wrong answer.

The systematic version of that: **every one of the five profiles grants paths its own
investigation never mentions**, and it is always the same three classes — the installed binary
path (`~/.local/bin/<agent>`: zero hits in all five investigations), the caches, and the macOS
app-bundle / plist / VS Code integration paths. The prose documents the agent's own config and
state; the policy additionally encodes what it takes to *launch and update* it. If the doc is
the reference a reader uses to predict the policy, it under-predicts every time.

### 5.4 Where they disagree about the same agent

This is the finding the round was worth running for. Three disagreements, two of them resolved
against ground truth in this jail.

**1. `opencode` — their published investigation documents a different program from their own
profile.** `docs/docs/agent-investigations/opencode.md` pins
`Repository: https://github.com/opencode-ai/opencode`, `Latest Version: 0.0.55`, describes it as
*"written in Go (version 1.24.0)"* with a Bubble Tea TUI, Viper config, a SQLite database at
`$CWD/.opencode/opencode.db` and a dotted `.opencode.json` config file. Meanwhile
`profiles/60-agents/opencode.sb` grants `~/.local/share/opentui`, `~/.config/opencode`,
`~/.cache/opencode` and `~/.local/state/opencode` — none of which appear anywhere in the
investigation, and `opentui` is the TUI library of the **TypeScript** opencode.

*Resolved:* the `opencode-ai` npm package installed in this jail is **version 1.18.31**, ships
`bin/opencode.exe` with per-platform optional dependencies, and is the TypeScript
implementation. yolo's [`packs/opencode/pack.json`](../../packs/opencode/pack.json) declares
`"package": "opencode-ai"` and a config path of `~/.config/opencode/opencode.json`, and
[`workspace-skills.md`](../design/workspace-skills.md) records `opencode 1.18.31` measured from
the installed bundle. **yolo tracks the current program; the investigation documents a
superseded one, three major versions back.** Their *profile* tracks the current one too — it is
only the prose that is behind.

**2. `pi` — the npm package name disagrees across the two projects, and yolo is right.**
Safehouse's `pi.md` says *"published as `@mariozechner/pi-coding-agent`"* at version `0.52.9`.
yolo's [`packs/pi/pack.json`](../../packs/pi/pack.json) declares
`"package": "@earendil-works/pi-coding-agent"`.

*Resolved:* the registry entry for `@earendil-works/pi-coding-agent` exists, its latest is
`0.85.1`, its author is Mario Zechner and its repository is `github.com/earendil-works/pi` — the
same author moved the package to an org scope. The copy installed in this jail is
`@earendil-works/pi-coding-agent@0.85.1`, matching yolo's declaration and yolo's own measured
table exactly.

**3. `pi` — a gap between Safehouse's own two halves that neither resolves.** `pi.md` lists
`~/.aws/credentials`, `~/.aws/config` and
`~/.config/gcloud/application_default_credentials.json` as credential locations pi reads, in
both its credential-storage section and its summary table. `profiles/60-agents/pi.sb` grants **none of them** and declares
no `$$require=` line, so they are reachable only through the opt-in
`--enable=cloud-credentials`. Unlike the `copilot` case below there is no always-on core module
filling the gap. Either the investigation overstates what pi reads, or pi's cloud-auth path is
broken by default under Safehouse. **Unresolved — settling it needs a Mac and a pi login.**

**A fourth, subtler one that is not an error in either project** but which any future
comparison table must handle: Safehouse marks `~/.copilot/copilot-instructions.md`,
`~/.pi/agent/extensions/` and `~/.codex/skills` as read-only, *"No (user-created)"*. yolo
**generates** all three. Both are true — Safehouse describes the agent's access, yolo describes
its own writes — so the two representations answer different questions and a naive merge
silently inverts the direction of access on three of five agents.

### 5.5 What each knows that the other does not

| Credential-bearing path | Named by |
| :--- | :--- |
| `~/.claude/.credentials.json` | **yolo only** (via a `hook`, `shared_credentials`) |
| `.claude-shared-credentials` (machine scope) | **yolo only** |
| `~/.codex/.credentials.json`, `~/.codex/.env` | **Safehouse only** |
| `~/.config/gh/hosts.yml` | **Safehouse only** |
| `~/.aws/*`, `~/.config/gcloud/*` | **Safehouse only** |
| `~/.config/github-copilot/{hosts,apps}.json` | **Safehouse only** (and it grants opencode read of it) |
| `~/.codex/auth.json`, `~/.pi/agent/auth.json` | **both** |

Safehouse's copilot investigation also leads its own profile in one place: it ranks
`~/.config/gh/hosts.yml` fourth of five in copilot's token-precedence order, a fact
`copilot-cli.sb` does not encode — the grant arrives from the always-on `scm-clis.sb` instead,
broader than the doc's claim (whole subtree, read **and** write, against the doc's one file,
read). So the knowledge is real and the enforcement is real, but neither points at the other.

---

## 6. Configuration surface, onboarding, and the `llm-instructions.txt` channel

### 6.1 Who the config author is meant to be

| | Safehouse | yolo |
| :--- | :--- | :--- |
| Primary surface | CLI flags on every invocation | `yolo-jail.jsonc`, per workspace |
| Durable personal config | a shell function in `~/.zshrc` | user-scope `~/.config/yolo-jail/config.jsonc` |
| Repo-supplied config | `<workdir>/.safehouse`, **untrusted by default** | `yolo-jail.jsonc`, in the repo, trusted |
| Discovery aid | [policy-builder.html](https://agent-safehouse.dev/policy-builder) | `yolo config-ref`, `yolo init`, `yolo pack --help` |
| Assumed knowledge | which integrations your task needs | which packs exist, and the config vocabulary |

Safehouse assumes the author is **the developer, deciding per task** — the README's own
recommended pattern is a shell function that shadows the agent's name, so sandboxed is the
default and `command claude` is the opt-out. yolo assumes the author is **the developer,
deciding per project, once**.

The **Policy Builder** deserves specific attention. It is a 1,249-line browser-side component
that takes ticked agents and integrations and emits: the complete `.sb` text, the
`sandbox-exec -f my-safehouse.sb -- <command>` invocation, shell functions for zsh/bash/fish,
and a downloadable `.command` script that builds macOS Desktop `.app` launchers. Crucially it
emits a **standalone** artifact — you can leave with a working policy and never install
`safehouse` at all. The tool makes itself optional on purpose.

**Their onboarding is simpler than ours by a wide margin, and this should be said plainly.** Two
commands, no config file, no runtime to install, no image to build, and the agent runs in the
directory you were already standing in. yolo requires a container runtime or nix, an image (or
a bundle) to materialise, and a config file that selects packs — because *nothing is active by
default*, an empty yolo config yields a jail with no coding agent at all. That is the right
default for yolo's model and it is still a much steeper first step.

### 6.2 `llm-instructions.txt` is not what its name suggests

The brief for this round guessed it was a machine-readable "how to use this tool" aimed at the
agent inside the sandbox. It is not. It is a **prompt for an LLM acting as a policy author for
a human**, and it explicitly tells the model to produce something that does **not** depend on
Safehouse:

> "Generate a custom macOS `sandbox-exec` profile for me, modeled on Agent Safehouse."

> "Do not invent placeholder tokens such as `__SAFEHOUSE_WORKDIR__`. Do not rely on
> marker-based post-processing blocks. Generate the concrete workdir rules directly when the
> wrapper runs."

It lists their source files as reading material, tells the model to auto-detect the user's
toolchains rather than interrogate them, to ask exactly **one** combined follow-up question,
and to emit a profile, a wrapper, a shell snippet, a table explaining each grant, and a
verification checklist. It is an *authoring assistant brief*, the same genre as the Policy
Builder, in a different modality.

**So the channels do not compete; they do not even overlap.** yolo's environment briefing
([`agent-briefings.md`](../reference/agent-briefings.md)) tells an agent *inside* a jail what
world it is in. Safehouse ships nothing of that kind — its only in-band signal to the
sandboxed process is one environment variable, `APP_SANDBOX_CONTAINER_ID=agent-safehouse`, set
as a default-if-missing. **A Safehouse-confined agent is essentially not told it is
confined**, and will discover the boundary by hitting it. That is a real gap on their side and
the clearest thing yolo does better in this area.

---

## 7. Philosophy — where they agree, and where they actually disagree

### 7.1 Convergence worth noticing

Both projects independently concluded that **the agent's own permission prompts should be
turned off and replaced by an external boundary**. Safehouse's Policy Builder defaults to
`claude --dangerously-skip-permissions`, `codex --dangerously-bypass-approvals-and-sandbox`,
`amp --dangerously-allow-all`, `gemini --yolo` and `OPENCODE_PERMISSION={"*":"allow"}`; yolo's
Claude YOLO is `--dangerously-skip-permissions` plus `IS_SANDBOX=1`, with
`permissions.allow` set to `[]` because *"it is not an allowlist mechanism."* Two projects, no
contact, same conclusion.

Both also agree on the **category boundary**, from opposite sides. Safehouse calls itself *"a
macOS sandbox wrapper for coding agents."* yolo's [`macos-no-vm-direction.md`](../reference/macos-no-vm-direction.md)
draws its acceptance bar against exactly that category: *"A macOS backend that cannot carry the
nix layer is not a yolo backend"*, and — of the first `macos-user` attempt — *"it read as a
clone of an existing sandbox tool that did nothing yolo does."* The differentiator it names is
predictable declarative packages against a locked nixpkgs, because *"a sandbox wrapper uses
whatever is on the host."* Safehouse would not dispute that description of itself; its
toolchain profiles exist precisely to let whatever is on the host keep working.

And both are honest about the boundary's strength. Safehouse: *"not a perfect security boundary
against a determined attacker"*, recommending VM-plus-Safehouse layering for stronger needs.
yolo: *"Isolation is Seatbelt-grade, not VM-grade. Also accepted, and documented rather than
hidden."*

### 7.2 Where they genuinely disagree

Three disagreements, not vocabulary differences.

**A. Disclosure versus consent, and it is a real inversion.**

yolo's position is settled: the fetched-pack approval prompt was deleted as theatre by
[OQ-TP9](../design/trust-paths.md#decision-ledger), and the rule is *"the boundary today is
DISCLOSURE, not consent"* — with the launch banner as the whole trust surface, and
[`gate-placement-principle.md`](../reference/gate-placement-principle.md) exempting visibility
from the test that killed the gate: *"Visibility is not a gate … Do not delete a message
because the act behind it was permitted."*

Safehouse runs the opposite way on the one path where they overlap. `<workdir>/.safehouse` —
policy supplied by the repo — is **ignored by default**, and is loaded only by
`--trust-workdir-config`, by `SAFEHOUSE_TRUST_WORKDIR_CONFIG`, or by the workdir appearing in
`~/.config/safehouse/trusted-workdirs`, which `--always-trust-workdir-config` writes. That is
trust-on-first-use: a consent gate, persisted, per directory.

**yolo's rule does not clearly hold here, and this is the one place where their design is the
better-argued one.** Test 1 asks whether the actor could already do the thing. For yolo's
deleted gate the answer was yes — installing a pack already required user-scope config access.
For Safehouse's gate the answer is **no**: a `.safehouse` file arrives by `git clone` from
someone who never had access to your machine, and it can widen the sandbox that is about to run
their code. The authority genuinely changes hands at that file. yolo's nearest analogue,
`yolo-jail.jsonc` in the repo, *is* honoured — with the config-change confirmation prompt as the
only thing standing between a cloned repo's `mounts` or `env_sources` and the next launch, a
gap [`trust-paths.md`](../design/trust-paths.md) already records as unscoped.

**B. Whether a launch may be quiet.** yolo rules that it may not — `TestTheLaunchHasNoQuietFlag`
fails if a flag appears, and `YOLO_NO_BANNER` covers the version line and nothing else.
Safehouse prints **nothing** unless `--explain` is passed: `cmd_execute_run` gates the entire
summary behind `cli_policy_explain`, and the rendered policy goes to a temp file that
`cmd_cleanup_rendered_policy` deletes afterwards unless `--output=PATH` was given. So by default
a Safehouse run leaves **no record of what was granted**.

yolo is right here, and it is not a close call — but yolo does not fully live up to it on the
backend under comparison: `macos-user` prints the Seatbelt profile under `--dry-run`
(`section("Seatbelt profile", plan.Seatbelt)`) and, on a real launch, prints one line —
*"Setting up the sandbox (Seatbelt profile + bootstrap)"* — without the profile or even its
path. The profile is installed root-owned at mode `0444` and is readable after the fact, which
is better than a deleted temp file, but the launch does not say where.

**C. Who the durable artifact belongs to.** Safehouse's Policy Builder and
`llm-instructions.txt` both aim at a self-contained `.sb` the user keeps, and their `dist/` is
one file you can read end to end. The policy is the deliverable; the CLI is a convenience. yolo
has no "export your jail" artifact at all — the environment is only reproducible by running
yolo. That is a coherent position (the environment, not the policy, is the product) but it is
a genuine philosophical difference and Safehouse's choice has a real user benefit yolo cannot
match.

### 7.3 One place Safehouse has no stated position

Safehouse has no written **escape-hatch criterion**. It ships many loosening flags —
`--enable=wide-read`, `--env`, `--allow-workdir-config-writes`, `--allow-profile-writes`,
`--enable=launch-services` — and discloses each one's hazard well (the generated policy carries
an inline warning that `wide-read` is emitted late and overrides earlier denies; the docs say
outright that Launch Services *"always starts processes OUTSIDE the sandbox with the current
user's full permissions"*). But there is no rule saying when a hatch is legitimate. yolo's —
*"an escape hatch is for a user's broken configuration, not for a verdict the user dislikes"* —
has no counterpart. **Marked as inference:** I read this as a consequence of their goal, not an
oversight; a tool whose promise is *practical* damage reduction has no principled reason to
withhold a switch.

Worth noting for the loophole vocabulary: `--enable=launch-services` is, in yolo's terms,
**exactly a loophole** — a declared, narrow, documented passage that puts an unconfined process
on the real machine. They made it opt-in recently and on purpose
(`fix(launch-services)!: Disallow 'open' by default unless --enable=launch-services`). Their
`55-integrations-optional/` directory as a whole is a loophole catalogue in yolo's sense:
opt-in, one file per capability, never on by presence alone.

---

## 8. What to adopt — ranked by value against cost

Ordered by value-to-cost. Each names the seam it lands on.

### 8.1 A policy-assertion suite for `macos-user` — **highest value, moderate cost**

> **SHIPPED 2026-09-18** (`7cb98e3f`). `integration/macosuserseatbelt_test.go` runs 15 cases over
> 11 rules under a real `sandbox-exec`, each paired with a bare control so a refusal is
> attributed to the policy rather than to a missing file, and a registry pins every
> `#seatbelt-test-id:` to its proving cases or to a written reason none can exist — enforced in
> BOTH directions on Linux under `-short`. It was written blind from a Linux jail. ⚠ **That
> caveat is spent:** on the `macos-user` CI job's 2026-09-24 run
> ([36050645052](https://github.com/mschulkind-oss/yolo-jail/actions/runs/36050645052)),
> `TestMacosUserSeatbeltProfileEnforcesItsRules` passed with all fifteen subtests, so the
> controls held on real `sandbox-exec`. What it still does not cover is anything outside those
> eleven rules.

**What.** Tests that run a command under the *generated* profile on a macOS runner and assert
the kernel refused it: writing outside the workspace, reading another user's home, reading
`/Library/Keychains`, reading a raw disk device, and — once [§8.2](#82-tighten-the-profile-toward-deny-default--highest-ceiling-highest-cost)
lands — each new deny.

**Why first.** It is the gap with the worst ratio. Our Seatbelt tests assert 16 properties of a
*string*; nothing asserts the string does anything. It is also the prerequisite for every other
profile change: tightening a policy with no runtime assertions is how a backend gets quietly
broken.

**The seam.** [`.github/workflows/macos-user.yml`](../../.github/workflows/macos-user.yml)
already exists, already runs on a macOS runner, and already carries `YOLO_TEST_MACOS_USER=1`
with the property that a run executing zero macos-user tests exits non-zero. New tests go in
`integration/macosuser*_test.go` beside the existing fixtures. **The hard part is already
built.**

**What to copy from them specifically:** the `#safehouse-test-id:<name>#` convention — a stable
id in a comment beside the rule, and the same id in the test that proves it, so each is
greppable from the other. In our tree the natural spelling is a comment beside the directive in
[`internal/macosuser/seatbelt.go`](../../internal/macosuser/seatbelt.go).

### 8.2 Tighten the profile toward deny-default — **highest ceiling, highest cost**

> **The three incremental denies SHIPPED 2026-09-18** (`474f68a0`) — items 1, 2 and 3 below,
> appended at the END of the profile, which is the whole risk: `(allow process-info*)` includes
> pidinfo and has always been last, so both new denies are inert anywhere above it. The base
> stays `(allow default)`; the full inversion is still [OQ-AS1](#OQ-AS1) and is untouched.

**What.** Move `macos-user` from `(allow default)` toward a deny-first posture, or — much
cheaper, and where I would start — keep `(allow default)` and add the specific denies Safehouse
demonstrates are safe to hold while real agents work:

1. `(deny sysctl-read (sysctl-name-regex #"procargs"))` and `(deny process-info-pidinfo)`, with
   `(allow process-info-pidinfo (target same-sandbox))` — this is cross-process argv and
   environment inspection, and it is where another process's secrets leak in plain sight. Their
   comment records that it was determined empirically on macOS 26 that *either* permission is
   sufficient for the read, which is why both are denied.
2. A `file-ioctl` restriction to tty devices.
3. `/System/Library/Keychains`, which our profile does not deny while denying
   `/Library/Keychains`.

Each is a handful of lines, each is independently revertible, and their test suite is evidence
that agents keep working under them.

**Why not the full inversion first.** `(deny default)` means enumerating every mach service,
every framework path and every toolchain — 2,919 lines of it for them — and yolo's substrate
differs enough (a nix profile rather than Homebrew and system Python) that theirs cannot be
copied wholesale. The incremental denies get most of the value at a small fraction of the cost.
The full question is [OQ-AS1](#OQ-AS1).

**The seam.** `SeatbeltProfile` in [`internal/macosuser/seatbelt.go`](../../internal/macosuser/seatbelt.go),
which is one function emitting concatenated directives, plus the ordering invariant already
pinned by `seatbelt_readonly_test.go`.

### 8.3 Disclose the profile on a real launch — **small cost, clear ruling behind it**

**What.** Print the profile path on a live `macos-user` launch, and the profile itself at the
tier `--verbose` already defines. Today the path appears only in the `--dry-run` plan.

**Why.** [`report-tiers.md`](../reference/report-tiers.md)'s ruling is that a launch has no
quiet mode and a disclosure is never suppressible. The Seatbelt profile *is* the trust boundary
on this backend — it is the macos-user analogue of the pack read/exec banners the ruling was
written about — and it is the one thing a launch does not name.

**The seam.** `orchestrator.go`'s launch stream, beside the existing
*"Setting up the sandbox"* line; the stream already tees to `<workspace>/.yolo/launch.log`.

### 8.4 Pin the agent launch argv — **tiny cost, real bug class**

> **SHIPPED 2026-09-18** (`ba340c7f`). `PlanInvariants` now requires
> `sandbox-exec -f <this session's profile>` on the agent's own `LaunchArgv`, consecutively, and
> deleting those words from `LaunchArgv` turns a named test red — mutation-verified on Linux.

`PlanInvariants` pins `sandbox-exec -f <profile>` for the provisioning stage and
`CapturePlanInvariants` pins it for the capture driver; nothing pins it for the argv that runs
the agent. Deleting that line today leaves the suite green. The seam is `PlanInvariants` in
[`internal/macosuser/runplan.go`](../../internal/macosuser/runplan.go), which already has the
helper (`containsArgPair`) and the shape.

### 8.5 A per-agent knowledge document, with their provenance header — **medium value, ongoing cost**

**What.** Give the class of fact yolo already gathers a published home. yolo *does* write
per-agent investigations — [workspace-skills.md §2.1](../design/workspace-skills.md#21-where-each-agent-reads-skills--measured) is one,
with a row per agent, a version per row, and every claim read from the installed bundle's
strings rather than from vendor docs — but it is buried inside a design doc because that is
where a decision happened to need it, and it will be stranded when that design is built.

**What to copy exactly:** their header block — *Analysis Date*, pinned upstream commit,
*Latest Version*, *Source Availability*, `[INFERRED]` markers, and a *Files Analyzed* list at
the end. That discipline is better than what this repo currently asks for and it is free to
adopt.

**What to copy with modification:** their summary table *All Filesystem Paths Accessed* needs a
**direction column per project**, because [§5.4](#54-where-they-disagree-about-the-same-agent)
found the direction inverted on three of five agents — yolo *writes* files their table marks
read-only and user-created.

**The seam.** `docs/research/agent-config-distribution.md` is the existing home for
cross-agent path knowledge, and [`workspace-skills.md`](../design/workspace-skills.md)
[§5](../design/workspace-skills.md#5-the-pack-declares-what-its-agent-reads) — *"The pack declares what its agent reads"* — is the mechanism that would make the
document's project-scope half machine-checkable, since it proposes the pack declare its agent's
project-relative skills directories and pin them with a probe test. The prose document and that
declaration are the two halves of one fact. **This is the candidate with a real cost**, and it
is [OQ-AS2](#OQ-AS2) rather than a recommendation, because this repo's standing lesson is to
delete a drift-prone form rather than keep updating it — and Safehouse's own 106-to-3 commit
ratio is the strongest available evidence for that lesson.

### 8.6 Do not auto-widen to the enclosing repo — **already yolo's behaviour, nothing to adopt**

Safehouse resolves the workdir to the invocation directory and **explicitly does not walk up to
the enclosing git repo**: *"Launching from `repo/subdir` grants `repo/subdir`, not `repo/`."*
yolo arrives at the same anti-surprise default by a different mechanism: **a launch's workspace is
the cwd, full stop.** `Run` fills it from `os.Getwd()`, there is no `--workspace` flag, and the
container gets `-v <that path>:/workspace` — so `yolo` in `repo/subdir` grants `repo/subdir` and
the enclosing repo is invisible to the jail
([`internal/cli/run/runcmd.go`](../../internal/cli/run/runcmd.go),
[`assemble_parts.go`](../../internal/cli/run/assemble_parts.go)).

yolo *does* have an upward walk, and it is the thing that makes this question look open:
`resolveWorkspaceRoot` in [`internal/cli/configtarget.go`](../../internal/cli/configtarget.go),
the CONFIG-TARGET resolver behind the `yolo config` verbs. It walks up looking for a **workspace
marker** — a `.yolo/config-boot.json` or one of the workspace config file names — never for a
`.git`, it stops at a credential-boundary directory, and it decides which config file a verb
reads. It cannot widen a launch's grant.

---

## 9. Negative space — what not to adopt

**1. The consent gate on repo-supplied config.** Despite [§7.2](#72-where-they-genuinely-disagree)
finding their argument the stronger one on the merits, do not port
`--trust-workdir-config` as a prompt. yolo already ruled that prompts at this position are
theatre, and the config-change confirmation flow occupies the slot. If the gap is worth closing,
the fix consistent with [`gate-placement-principle.md`](../reference/gate-placement-principle.md)
is a **scope rule** — the class that survived the placement test — making the widening keys
(`mounts`, `env_sources`) user-scope-only the way source-bearing `host_files` already is, so the
repo's choice is *inexpressible* rather than *prompted*. That is a change to
[`trust-paths.md`](../design/trust-paths.md)'s subject, not a new gate.

**2. Granting the agent the host's SCM credentials.** `scm-clis.sb` giving read **and write** on
`~/.config/gh` by default is the single largest divergence from yolo's premise. It is coherent
for them and it would delete yolo's central claim.

**3. `--enable=wide-read`.** A late `(allow file-read* (subpath "/"))` that overrides earlier
denies, shipped as a convenience mode. yolo has no place to put this and should not find one;
it is the shape [`escape-hatch criterion`](../reference/report-tiers.md) exists to refuse.

**4. A big machine-readable instruction file for the agent.** `llm-instructions.txt` works for
them because its reader is *outside* the sandbox, authoring a policy. yolo's equivalent reader
is inside the jail, and
[`information-at-the-point-of-need.md`](../reference/information-at-the-point-of-need.md) is
the standing argument against putting more prose in front of an agent that has not yet hit the
thing the prose describes. Their `llm-instructions.txt` is a good idea for their audience and a
bad fit for ours.

**5. The single-file `dist/` artifact, as a distribution model.** It is genuinely elegant for a
bash-plus-SBPL tool. yolo's product is an image, a flake bundle and a Go binary; there is no
version of this that survives contact with the nix layer, and chasing it would be chasing their
architecture rather than their ideas.

**6. Bash.** Stated only because it is tempting to read their 5,783 lines as evidence that this
layer is simple. It is simple *for them* because the policy is the whole product. yolo's
equivalent logic is already Go and already tested.

---

## 10. What I could not verify

- **Anything requiring a Mac.** Neither Safehouse's profile nor yolo's was executed during this
  round; this jail is Linux and has no `sandbox-exec`. Every runtime claim about either profile
  is read from source or from the other project's own test suite.
- **Whether Safehouse's policy actually holds** what its tests claim. I read the test names and
  the CI triggers; I did not run them.
- **[§5.4](#54-where-they-disagree-about-the-same-agent) disagreement 3** — whether pi really
  reads `~/.aws/credentials` and gcloud ADC. Settling it needs a Mac and a pi login.
- **The claim that Safehouse's `~/.claude.json` write grant is *needed*** rather than incidental.
  A profile may be legitimately wider than observed behaviour; I did not establish which this is.
- **Their `.safehouse` trust file's own protections.** I read that
  `~/.config/safehouse/trusted-workdirs` is written by a flag and matched per launch; I did not
  check whether a sandboxed agent can write that file (it sits under `~/.config`, which the base
  policy grants only a directory-root read — so probably not, but I did not confirm it).

---

## 11. Fast-moving — verify before building

Re-check these before quoting them; everything here moved within the last six months.

- **Safehouse version, flag set and `--enable` catalogue.** `v0.12.0` at reading. The catalogue
  is derived from a directory listing, so it grows without a release note.
- **Their per-agent investigations' contents.** Three commits in seven months, with analysis
  dates up to fourteen months old. Treat any path list there as a lead, not a fact.
- **Agent package identities.** `@earendil-works/pi-coding-agent` (not `@mariozechner/`),
  `opencode-ai` at 1.x (not the Go `opencode-ai/opencode` at 0.0.x). Both moved during the
  window in which Safehouse's docs were written.
- **`sandbox-exec` itself.** Apple-deprecated; yolo's own CI comment declines to pin the macOS
  runner image partly for this reason. Both projects' entire macOS story rests on it.
- **yolo's `macos-user` default status.** Opt-in today; the direction is for it to become the
  macOS default, which would make everything in [§3](#3-mechanism--the-two-seatbelt-profiles-side-by-side)
  apply to the shipped path rather than an opt-in one.

---

## 12. Open questions

1. 💬 **OQ-AS1: How far should `macos-user`'s Seatbelt profile move toward deny-default?**

   <!-- vantage: oq id=OQ-AS1 leaning="Take the incremental denies now, and only consider the full inversion once a policy-assertion suite exists to catch what it breaks." -->

   Stakes: the profile is a short deny list against Safehouse's 2,919 lines, and the
   difference is not cosmetic — network, exec, mach lookup, signals and IOKit are all open on
   our side. (Cross-process argv inspection was on that list until `474f68a0` closed it.) The full inversion is a large, ongoing enumeration cost against a
   nix substrate theirs does not share; the incremental denies in
   [§8.2](#82-tighten-the-profile-toward-deny-default--highest-ceiling-highest-cost) are cheap
   and independently revertible. This is a ruling about how much this backend is meant to
   promise, which is why it is not a task.

   _Leaning:_ Incremental denies now; the full inversion only after
   [§8.1](#81-a-policy-assertion-suite-for-macos-user--highest-value-moderate-cost) exists.

   ⚠ **Both preconditions of the leaning are now met, and that does not rule it** (2026-09-24):
   the incremental denies shipped (`474f68a0`), and [§8.1](#81-a-policy-assertion-suite-for-macos-user--highest-value-moderate-cost)'s suite exists and runs green on the
   `macos-user` CI job. What is left is the question itself — how much this backend is meant to
   promise.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-AS2: Does yolo want a published per-agent investigation series at all?**

   <!-- vantage: oq id=OQ-AS2 leaning="No series; instead give the existing measured tables one home in docs/research/ and adopt their provenance header, so the format is available without committing to thirteen documents." -->

   Stakes: Safehouse's 10,020 lines of per-agent prose are the best human artifact in this
   comparison and they rotted in seven months, measurably — 106 commits to the policy against 3
   to the prose, and one investigation now documents a superseded implementation. This repo's
   own standing lesson is to remove a drift-prone form rather than keep updating it. But yolo
   currently publishes *nothing* of this kind, and the knowledge exists, scattered across design
   docs that will be archived when their designs are built.

   _Leaning:_ No per-agent series. Consolidate the measured tables that already exist into one
   research document, adopt their header discipline, and let the pack declarations stay the
   enforcement.

   **Answer:**
   > _(empty — fill in when decided)_

3. <a id="OQ-AS3"></a>💬 **OQ-AS3: Should `mounts` and `env_sources` become user-scope-only?**

   <!-- vantage: oq id=OQ-AS3 leaning="Yes for env_sources at least — source-bearing host_files is already user-scope-only for exactly this reason, and env_sources reaches the same host files by another name." -->

   *(Opened 2026-09-18 by the maintainer while reading this comparison's trust-boundary
   section, and verified against `internal/config` before filing.)*

   *Paired 2026-09-25 with [`workspace-config-trust.md`](../design/workspace-config-trust.md), which
   adds a third answer — grants allowed in the local file only against a host-side trust record —
   and argues that §9 item 1's scope rule holds against the repo author but not against the in-jail
   agent, who can write the local file.*

   Stakes: the comparison's sharpest finding about Safehouse is that they gate a repo-supplied
   policy behind explicit per-directory trust while yolo honours a repo-committed
   `yolo-jail.jsonc`. The recommendation there was a SCOPE rule rather than a trust prompt —
   and the tree turns out to apply that rule unevenly. `packs`, `profiles`/`use_profiles`,
   source-bearing `host_files`, loophole `settings` and a provider's `base_url` are each refused
   in a workspace config by ruling, because that file is agent-editable and travels with the
   repo. **`mounts` and `env_sources` are not: neither has a workspace-scope refusal anywhere in
   `internal/config` (verified 2026-09-18).** So a repo-committed config can name a host
   directory to mount at `/ctx` and a host dotenv whose values become the jail's environment,
   disclosed only by the config-change diff.

   `env_sources` is the sharper half: source-bearing `host_files` is user-scope-only precisely
   because it carries host bytes into the jail, and `env_sources` reaches the same class of host
   file by another name — the shipped example is `~/.config/claude/env`.

   The counter-argument is real and should be answered rather than assumed away: yolo's stated
   boundary is DISCLOSURE, not consent ([`OQ-TP9`](../design/trust-paths.md#decision-ledger)),
   and the config-change prompt does disclose. The question is whether these two keys belong in
   the set where disclosure was already judged insufficient.

   _Leaning:_ Yes for `env_sources`, on the `host_files` precedent — the same authority under a
   different key name should not have a different scope. Less certain for `mounts`, where the
   host path is visible in the diff and the jail gets it read-only; that one may be genuinely
   fine as disclosure.

   **Answer:**
   > _(empty — fill in when decided)_

---

## 13. Sources

All read 2026-09-18.

- [github.com/eugene1g/agent-safehouse](https://github.com/eugene1g/agent-safehouse) — the
  source. `profiles/` is where the real comparison lives; `bin/lib/policy/render.sh` holds the
  assembly order and the terminal-deny mechanism.
- [agent-safehouse.dev](https://agent-safehouse.dev/) — landing page; source of the
  *"Go full `--yolo`. We've got you."* and *"macOS-native sandboxing for local agents. Move
  fast, break nothing."* taglines and the kernel-enforcement claim.
- [agent-safehouse.dev/docs/overview](https://agent-safehouse.dev/docs/overview) — their
  philosophy and, more usefully, their explicit *Important Limitations* list.
- [agent-safehouse.dev/docs/policy-architecture](https://agent-safehouse.dev/docs/policy-architecture)
  — assembly order, last-match-wins, terminal denies, the `HOME_DIR` placeholder.
- [agent-safehouse.dev/docs/default-assumptions](https://agent-safehouse.dev/docs/default-assumptions)
  — the allow/deny/opt-in matrix; the most useful single page they publish.
- [agent-safehouse.dev/docs/options](https://agent-safehouse.dev/docs/options) — the flag
  surface, the trust precedence order, and the `.safehouse` key list.
- [agent-safehouse.dev/docs/agent-investigations](https://agent-safehouse.dev/docs/agent-investigations/)
  — the thirteen per-agent reports.
- [`sandbox-comparison.md`](sandbox-comparison.md) — the sibling comparison against Claude
  Code's built-in sandbox; read it for the default-deny-at-the-right-layer argument.
- [`macos-no-vm-direction.md`](../reference/macos-no-vm-direction.md) — yolo's acceptance bar,
  and the sentence that frames this whole document.
- [`agent-credentials.md`](../reference/agent-credentials.md) — the credential boundary, stated
  as a structural property and enumerated channel by channel.
- [`trust-paths.md`](../design/trust-paths.md) — the inbound census and
  [OQ-TP9](../design/trust-paths.md#decision-ledger), which deleted the approval gate.
- [`host-execution-from-the-workspace.md`](../reference/host-execution-from-the-workspace.md) —
  the outbound mirror, and the one threat both projects mostly decline.
- [`gate-placement-principle.md`](../reference/gate-placement-principle.md) — the test that
  decides whether Safehouse's trust gate is one yolo should want.
</content>
</invoke>

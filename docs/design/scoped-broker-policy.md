---
title: "Scoped broker policy: one allow / ask / deny contract, with resource scopes, for every operation broker"
date: 2026-11-17
status: accepted
stage: DECIDED
next: "Confirm the four facts in section 13 in the plan's first task"
depends-on:
  - pack-brokers.md
  - github-write-scopes.md
  - boundary-broker.md
tags: [design, brokers, policy, approvals, scopes, aws, git, github, circleci, routing]
summary: "Every operation broker (git, github, aws, circleci) answers each classified operation with run, ask or refuse from one model: a cell (read or write) on a resource, decided by the most specific scope that has a line for it, in the user's ceiling and in the workspace's request, the stricter of the two winning. Reads become cells, so a read can be scoped, asked for, answered once or for a time, exactly as a write is today. Nothing configured stays refused and every existing broad grant keeps its meaning. A host-only route table picks the execution context (for AWS, a host profile) from the resource; the context's declared identity is bound into the approval. No new approval flow, store or UI: the shipped request store, digest, wait, hook and notification machinery is extended, and the generic broker daemon is brought to parity with it."
---

# Scoped broker policy: one allow / ask / deny contract, with resource scopes, for every operation broker

**Status:** 2026-11-17 — accepted contract, proposed and unbuilt. No owner question is open; [OQ-SBP1](#OQ-SBP1) was ruled B on 2026-11-17. **Shipped** describes the baseline, not this revision. Everything marked **Proposed** is new behavior. [Section 13](#13-unverified-claims-this-design-relies-on) records which source claims were reproduced, which gaps they confirmed, and four facts still to confirm.

**Design authority:** Fable 5.2's engineering-closure fragment and corrective overlay, 2026-11-17. The overlay closes hard-deny fact-read handling and declaration-only gate behavior; it supersedes the earlier closure wording where they differ.

> **In short.** Read and write operations should follow one scoped allow / ask / deny contract across operation brokers, bounded by host authority and served by the existing approval lifecycle.

**Needs your ruling:** None.

**Evidence boundary.** This design rests on [source-read grounding](../research/scoped-broker-policy-grounding.md) against baseline `a97a0e055f20de4524063b70729595ea8794bef8` and on a direct read of [`github-write-scopes.md`](github-write-scopes.md). A later engineering-closure pass ran six synthetic probes through a Go test overlay and fifteen existing tests against the same baseline; [section 13](#13-unverified-claims-this-design-relies-on) records each result. No integration suite, API call, agent run or credential read supports it. Shipped TOML spellings and timing constants quoted below come from a read-only source scan, not from running the commands.

**Reads with:** [`scoped-broker-policy-plan.md`](scoped-broker-policy-plan.md) (the companion seed, not a build hand-off), [`github-write-scopes.md`](github-write-scopes.md) (cells, ceiling, ask, working set; several rulings are reused and two superseded here), [`pack-brokers.md`](pack-brokers.md) (the generic broker), [AWS operations](../reference/aws-operation-broker/README.md) (AWS authority and execution).

---

## 1. Terms

The existing **cell**, **ceiling**, **ask** and **working set** vocabulary comes from
[GitHub write scopes](github-write-scopes.md); **classifier** and **executor** come from
[pack brokers](pack-brokers.md#2-terms). Their proposed read generalization is specified here.

| Term | Meaning |
| :--- | :--- |
| **Operation broker** | A host service that classifies each agent call into operations on resources and decides each one. Shipped: `git`, `github`, `aws`, `circleci`. |
| **Root** | A resource the workspace asked for and a human approved at the config-change gate. A root admits; it grants nothing. |
| **Cell** | One named kind of operation a source declares. **Shipped:** every cell is a write. **Proposed:** a cell has a kind, `read` or `write`. |
| **Scope** *(as used here)* | A resource a rule is written for: a root, or a resource inside one by the source's own containment. The **source scope** is "every admitted resource of this source" and is the least specific. |
| **Rule** | One line: a scope, a cell (or the kind word `read`), and a value. |
| **Ceiling** | The user's host config: the most any workspace may hold. **Shipped:** a token sits in the `on_request` or `always` list, or is absent. **Proposed:** a third list, `never`, is an explicit host refusal ([§3.6](#36-spelling)). |
| **On-switch** *(coined here)* | **Shipped:** a source's source-scope `read` grant, without which every call is refused, writes included. **Proposed:** retired, with a compatibility rule ([§3.7](#37-read-and-write-are-independent)). |
| **Candidate cells** *(coined here)* | The finite set of cells an operation may turn out to be before a provider fact is read ([§4.1](#41-fact-reads)). |
| **Ask** | The workspace's request: `true`, `"ask"`, `false`, or no line. `true` needs a human's `y` at the gate. |
| **Decision** | `run`, `ask` or `refuse`, for one operation. |
| **Request** | A durable record that an operation is waiting for a human. Not a grant. |
| **Exact answer** | A one-use approval bound to one call's digest. |
| **Timed answer** | An approval of one cell on one full resource chain for a bounded time. |
| **[Execution context](../glossary.md#execution-context)** | The host-side environment a brokered call runs in. For AWS: which host profile. |
| **[Route](../glossary.md#route-broker-policy)** | A host-only line mapping a scope to an execution context. |

## 2. What is shipped, and what it cannot say

### 2.1 The four operation brokers

| Source | Front and admission (shipped) | Reads (shipped) | Writes (shipped) |
| :--- | :--- | :--- | :--- |
| `git` | `[brokered]`; remote rewriting to `git-remote-yolo`; exact approved URLs; `internal/gitbroker` | Host-credential fetch needs scope and read authority. A public HTTPS fetch is tried anonymously first, with no grant. | Each ref of a push must be covered by a branch or tag grant, the working set or a timed entry; otherwise one exact whole-push request. |
| `github` | `[brokered]`; `gh` interception; approved github.com repositories; `internal/ghbroker` | Standing after the user's broad read grant and repository admission. Unknown grammar and account-wide calls are refused, never requestable. | Creation, runs and approve-run grants; working-set comment and edit defaults; other supported writes ask. |
| `aws` | `[call_broker]`; `bin = "aws"` forwarded to `aws-broker`; declared service roots | S3 list, download and head, and four Cost Explorer queries, all standing under one shared read grant. | S3 `put_objects` and `delete_objects` through the generic write policy. |
| `circleci` | `[call_broker]`; generic CLI front; remotes map to `gh/{owner}/{repo}` | `run list --project` with bounded flags on an exact root. | `rerun_workflows` is declared, defaults false, and the classifier refuses every write. |

**Shipped floor, unchanged.** Selecting a pack grants nothing. The git manifest says "No switch, and no access by default"; github says "with none, every call is refused". `requestable_by_default = true` on aws and circleci makes read a candidate at the gate, not a grant (`config.ResolveLoopholeGrants`).

### 2.2 Services that are not operation brokers

| Service | What it is | Treatment in this revision |
| :--- | :--- | :--- |
| `aws-auth` | Mints and delegates SDK credentials | **Unchanged.** A separate boundary; see [§7.5](#75-aws-auth-is-a-separate-boundary). |
| `mcp-auth-broker`, `claude-oauth-broker`, `openai-auth-broker` | Share sign-ins, serialize OAuth refresh | Out of scope. They classify no operations and inherit no operation policy. |
| `journal`, `macos-log`, `host-processes`, `serial`, `audio` | Grant-gated bridges with valued grants (`read`, `read_all`, `view`, `devices`, `use`) | Out of scope for per-operation approvals. Their valued grants already are their scopes. Shared vocabulary only: ceiling, ask, granted, withdrawn. |
| `cgroup-delegate`, `hello-daemon`, `wire-bridge` | Self-narrowing, an experiment, a protocol adapter | Out of scope. |

None of these gains a request store entry per call. A later design may promote one to an operation broker by giving it a classifier; this one does not ([SBP-D14](#SBP-D14)).

### 2.3 The gaps

1. **Reads are not policy subjects.** `callbroker.Classification.Reads` carries resources only: no cell, no description. `decideCall` returns `standing read` for any call with reads and no writes. `callRequest`, `revalidate`, `RequestEntries` and `ApprovalTimedLimit` all require writes. `parseCallBrokerCells` refuses a cell named `read`.
2. **Scoped read is refused in config.** `scopedCeilingReadProblem` refuses `read` in a scoped ceiling; `readableScopedCeiling` refuses generic call brokers and any-host git as scoped-ceiling owners.
3. **One AWS read grant covers S3 and Cost Explorer alike.** Roots pick services; the grant cannot.
4. **The generic daemon is behind the native ones.** `callbroker.Main` runs no request hook, installs no `PublishFailed` report and does not call `Store.End`; an ask returns at once with exit 75.
5. **No execution routing.** One captured host environment serves every AWS call. The declaration schema (`loopholedecl.CallBroker`) has host environment names only.
6. **No explicit host refusal.** The ceiling's `off` is computed from absence. `[allow.<pack>] off = true` switches a whole pack off; nothing refuses one capability under a broader allowance.
7. **Read is the on-switch.** Generic `currentAuthority` requires the broad read grant for every call; git and GitHub use read as a floor for writes, pushes included. A write-only grant is impossible. Reproduced: a write-only call with no read grant is refused before filing.
8. **Workspace cell changes do not reach a running generic broker.** `config apply` publishes roots and grant kinds only; the launch input is immutable and a byte change refuses rather than reloads. A cell-only change is still reported "applied now". Reproduced.
9. **Generic exact answers do not survive a relaunch.** `boundWrite` and `boundHTTPWrite` revalidate the stored holder unchanged. Reproduced for the CLI path.
10. **Generic revalidation always checks the `all` table**, so a working-set-only ceiling runs in prediction and is refused at dispatch. Reproduced.
11. **The generic host program skips the native placement check.** `pinCLI` accepts a program inside the workspace that `brokercore.PlacementRefusal` rejects. Reproduced.
12. **A declaration alone raises a repository-scope prompt.** Observed on an installed build (`a2b6554ce`) in a workspace that selected `aws` and approved no root: the gate shows "Repository scope changed … read from .git/config … no scope approved … 0 added / removed / source changed". In `compareScope` a non-empty declaration disclosure that differs from the record counts as a change and turns off the skip for an empty source; the scope block then names this workspace's `.git/config` as origin by default. Cancelling records nothing, so the prompt returns. Read by the parent, not reproduced here. The prompt asks a human to approve a scope that does not exist, for a source that can do nothing.

## 3. The policy model (proposed)

### 3.1 Rules

A rule is *(scope, cell or `read`, value)*. It lives in one of two layers, as today:

- **The ceiling**, user config on the host. Workspace files cannot write it.
- **The ask**, workspace config, read through the gate (`config.ReadWorkspaceForGate`).

Both layers accept the same scopes. A scope is the source scope, a root, or a resource inside a root. The source declares how its resources contain each other ([§9](#9-each-source)); there is no glob and no cross-source string matching. A scope that no admitted root contains is accepted and inert, and `yolo check` reports it.

**Read cells.** Each source declares at least one read cell. The word `read` at a scope stands for every read cell of the source at that scope. A cell-named line is more specific than `read` at the same scope. No line implies another ([GW-D38](github-write-scopes.md#GW-D38)): `read` never covers a write cell, and a write cell never covers a read. "Read and write on this prefix" is two lines.

**Write cells and tables are unchanged.** `working_set` and `all`, their shipped defaults, the invariant `working_set ≥ all` ([GW-D58](github-write-scopes.md#GW-D58)) and "pick the table first" ([GW-D57](github-write-scopes.md#GW-D57)) hold at every scope. Read cells have no working-set table.

### 3.2 Which rule governs

For one operation, in each layer separately:

1. Collect the rules whose scope contains the operation's resource and whose cell is the operation's cell (in the picked table, for writes) or `read` (for a read).
2. The rule with the **most specific scope** governs. This is the owner's "the more specific governs" ([OQ-GW7](github-write-scopes.md#OQ-GW7)) applied to resources.
3. At the same scope, the cell-named line governs over `read`.
4. Two lines with the same scope and the same cell in one layer, after file merge, are a config error at load. `yolo check`, a launch and `yolo config apply` refuse the same file.

5. In the ceiling, a cell token (`all.put_objects`, `read.get_objects`) is more specific than a table or kind token (`all`, `read`) at the same scope.

The governing line's value is final for its layer. A scope with no line for the cell does not stop the search; it continues to the next broader scope. So a narrower line overrides a broader one in either direction: a narrower ceiling `never` or ask `false` under a broad grant refuses, and a narrower `always` under a broad `never` runs. A workspace line cannot escalate, because the ceiling still bounds it.

### 3.3 How the two layers combine

The shipped write table (`config.CallBrokerTablePolicy`) with one new column, applied to reads too:

| Governing ask ↓ / governing ceiling → | no line at any scope | `never` | `on_request` | `always` |
| :--- | :--- | :--- | :--- | :--- |
| `false` | refuse | refuse | refuse | refuse |
| `"ask"` | refuse | refuse | ask | ask |
| `true` (after the gate's `y`) | refuse; reported at load, counts as absent | refuse; reported at load | run | run |
| absent | refuse | refuse | ask | run |

For Git reads, `refuse` in this table denies host credentials, not the supported anonymous public fetch/import path ([§9.1](#91-git-fetch-paths)). It grants no anonymous write or arbitrary command.

"No ceiling line" and `never` both refuse and file nothing. They differ only in the search: `never` is a line and governs; absence lets a broader line govern. An ask is filable only where the host has said this operation may be asked for.

**One wording change from baseline.** The shipped pure decision predicts "ask" under an absent ceiling, and the shipped transaction then refuses to file. Proposed: prediction says "refuse" too. No outcome changes.

A shipped default on a write cell (the working set's comment and edit cells) keeps its shipped meaning and needs no ceiling token ([GW-D42](github-write-scopes.md#GW-D42)). A governing `never` refuses it. No read cell ships a default.

### 3.4 Existing broad grants

A shipped broad read grant is read as a `read` line at the source scope, in the list it was written in. With no new-form line written, every run / ask / refuse decision and every digest is the same as today. Timing of an asking call on the generic broker is not: it now holds ([§6.1](#61-budgets)). Scoped lines only take effect when someone writes them. No file is migrated and no default becomes `"ask"` ([SBP-D3](#SBP-D3)). The on-switch meaning of the read grant is retired under the compatibility rule of [§3.7](#37-read-and-write-are-independent).

### 3.5 The owner's AWS example, as rules

| Layer | Scope | Line | Effect |
| :--- | :--- | :--- | :--- |
| ceiling | `ce://host-login` | `read` `always` | Cost Explorer queries run |
| ceiling | source | `read` `on_request` | every other read asks |
| ceiling | `s3://a/team/` | `read` `always`; `put_objects`, `delete_objects` `on_request` | reads run; writes may be asked for |
| ask | `s3://a/team/` | `put_objects = true`, `delete_objects = true` | writes run there after the gate's `y` |
| ceiling | `s3://b/reports/` | `read` `always` | reads run there. This line says nothing about writes: a write there is still decided by the nearest broader write line. |
| ceiling | `s3://b/reports/` | `never` for `all` and `working_set` | the read-only exception: every write there is refused, even when the source scope lets S3 writes be asked for |

A read line never refuses a write. Read-only under a broader write allowance always takes the explicit `never` line.

### 3.6 Spelling

This is the contract. **Shipped** lines keep their spelling and meaning.

**Lists and tokens (ceiling, user config only).**

| Item | Shipped | Proposed |
| :--- | :--- | :--- |
| Lists | `always`, `on_request` | adds `never` |
| Tokens | `read`; `all`, `working_set`; `all.<cell>`, `working_set.<cell>` | adds `read.<cell>` for one read cell |
| Coverage | an `all` or `all.<cell>` token in `always` or `on_request` also covers the working-set table | unchanged; a `never` token covers exactly what it names |

One token in two lists at one scope is a load error. `off` is not used as a list name: `[allow.<pack>] off = true` is the shipped whole-pack switch and keeps that meaning.

**Values (ask, workspace config).** `read = true | "ask" | false` for every read cell, or a `read` table with one key per read cell; TOML permits only one of the two forms per scope. Write cells keep `[…requests.<source>.all]` and `[…working_set]` with `true | "ask" | false`. This supersedes [GW-D8](github-write-scopes.md#GW-D8): `read = "ask"` is valid.

**Scopes.**

| Source | Source scope | Narrower scope |
| :--- | :--- | :--- |
| `aws`, `circleci`, any `[call_broker]` source | `[allow.<source>]`, `[requests.<source>]` (shipped) | **Proposed:** `[scopes.<alias>]` with `source = "<source>"` and `resource = "<canonical resource ID>"`; ceiling lists in `[scopes.<alias>.allow]`; asks in `[scopes.<alias>.requests]` and its `all`, `working_set` and `read` tables |
| `github` | `[allow.github]`, `[requests.github]` (shipped) | `[repos.<alias>.allow.github]` and `[repos.<alias>.requests.github]` (shipped for writes). **Proposed:** they accept `read` tokens and values, and `never`. |
| `git` | `[allow.git]`, `[requests.git]` (shipped) | **Proposed, read only:** `[repos.<alias>.allow]` lists holding the token `read`, and `[repos.<alias>.requests] read = …`. Every other git line under a name stays refused as unbuilt, as today. |

- An alias is a TOML bare key and a local label. A user scope and a workspace scope match by *(source, resource)*, as `[repos]` tables match by repository identity today.
- `resource` must be in the source's canonical form ([§9](#9-each-source)); anything else is a load error. Two aliases with one *(source, resource)* in one layer are a load error.
- `[scopes.<alias>]` naming `git` or `github` is a load error that names `[repos.<alias>]`.
- **The invariant, per scope.** For every scope that has a line, the governing values for `working_set.X` and `all.X` at that scope are compared in both layers; `working_set.X` below `all.X` is a load error ([GW-D58](github-write-scopes.md#GW-D58)). In the ceiling the order is `never` < absent < `on_request` < `always`.

```toml
# user config: the ceiling
[allow.aws]
on_request = ["read", "all.put_objects", "all.delete_objects"]

[scopes.cost]
source = "aws"
resource = "ce://host-login"
[scopes.cost.allow]
always = ["read"]

[scopes.reports]
source = "aws"
resource = "s3://b/reports/"
[scopes.reports.allow]
always = ["read"]
never = ["all", "working_set"]
```

```toml
# workspace config: the ask
[scopes.team]
source = "aws"
resource = "s3://a/team/"
[scopes.team.requests]
read = true
[scopes.team.requests.all]
put_objects = true
```

**Verbs.** This revision adds no named verb form. **Shipped and kept:** `yolo allow <source> always|on_request read`, `yolo request <source> read`, and the `repos` forms. Every other line above is written with the shipped editing verbs, and every refusal or advice message names the exact command:

- `yolo config add allow.aws.never all.delete_objects --user`
- `yolo config set scopes.reports.resource s3://b/reports/ --user`
- `yolo config set scopes.team.requests.all.put_objects true --project`

These forms are derived from source, not from a run; the plan confirms them ([§13](#13-unverified-claims-this-design-relies-on)).

### 3.7 Read and write are independent

**Proposed.** A read line decides reads and nothing else. A write is decided by its own lines. A source runs a call when the pack is selected, the launch is live and the resource is under an approved root; no read grant is a precondition for a write. Refusing reads on one scope disables no write.

**Compatibility rule for the retired on-switch.** The gate record gains a policy-model marker. A record without it is a **legacy record**.

| Workspace state | Behavior |
| :--- | :--- |
| Legacy record; source holds its source-scope read grant | No difference from baseline and no prompt. |
| Legacy record; source holds no source-scope read grant | Every call stays refused, as at baseline. Write lines the on-switch was holding back do not come alive. |
| Next gate approval, for any reason | The record is written with the marker. Each write line the on-switch was holding back is listed at the gate as **added** and takes effect only with that `y`. |
| Marked record; the user later removes the read grant | Reads are refused. Writes keep their own lines. `yolo check` and `yolo config why` say the writes remain and name the shipped `[allow.<pack>] off = true` as the way to switch the source off. |

No old grant, answer or record gains read or routed authority by this rule.

## 4. How one call resolves (pure decision)

The decision is a pure function. Inputs: the classification (ordered operations, each with kind, cell, resource chain and description), admitted roots, the effective rules of both layers, working-set membership, live timed entries, and the execution context identity. It reads no credential, no host environment value and no provider. It files nothing.

1. **Admission first.** The classifier refuses unknown grammar. Each chain must end at an approved root (`validateChain`). A refusal here is never requestable, whatever the rules say.
2. **Each operation is decided alone**, by [§3](#3-the-policy-model-proposed). For a write, the table is picked first by one selector: `working_set` when a live entry of the same workspace, other than a timed `call:` entry, matches the resource or an ancestor; otherwise `all`. The same selector, on the entries current at that moment, is used by prediction, filing, approval, late coverage and spend ([§8](#8-authority-state-concurrency-withdrawal-failure)).
3. **A timed entry** for the operation's cell on its full chain, in the same execution context, turns `ask` into `run`. It never overrides `refuse`.
4. **The call's decision** is the strictest of its operations: any `refuse` refuses the whole call and files nothing; otherwise any `ask` files **one** request for the whole call; otherwise it runs.
5. **A prediction** (`evaluate`, `config why`) runs steps 1–4 and stops. It never files, spends, dispatches or performs a fact read that is not itself `run`.

### 4.1 Fact reads

Some classifications need a provider fact first (GitHub: is this number a PR; who triggered this run; which run a job belongs to).

**Shipped.** GitHub reads facts directly at three call sites (`evaluateWrite`, `fileAsk`, `coveredWrite`), outside any read decision, and a prediction may fetch them. The repository is known before every fact read. The final cell (PR or issue) and a job's run are not.

**Proposed.**

1. **A fact read is a read.** Its resource is a scope known before the read (GitHub: the repository) and its cell is a declared read cell. It is decided by [§3](#3-the-policy-model-proposed). Every fact call site, and every declared generic lookup, goes through that decision.
2. **Unknown identity is a candidate set.** Before the fact, the adapter names the finite candidate cells the operation may be (`comment_prs` or `comment_issues`). The operation's pre-fact decision is taken over the candidates.
3. **A call makes at most four fact reads**, all on the bound scope. A fifth refuses the call.
4. **A refused cell is never reachable through an approval.** No once or timed answer, old or new, lets a write run while one of its candidates is `refuse` and the fact that would rule that candidate out has not been read.
5. **A refused fact read stays unread.** No answer to any request forces it.
6. **A grant that needs a fact's value** (the default branch, a run's actor, a job's run) covers nothing while that fact read is not `run`. The write is then decided as the uncovered write it would otherwise be. No fact is read for it.

**When a write depends on a fact.** Only when its candidates' decisions differ and at least one is `refuse`. In every other case the write is decided without the fact and is independent of read authority.

Each candidate's decision below already includes live timed entries ([§4](#4-how-one-call-resolves-pure-decision), step 3).

| Candidates' decisions | Fact read `run` | Fact read `ask` | Fact read `refuse` |
| :--- | :--- | :--- | :--- |
| All `refuse` | Refuse. No fact read. | Refuse. No request. | Refuse. No request. |
| All `run` | Read as today; runs. | Runs. No fact read. | Runs. No fact read. |
| All `ask`, or `run` and `ask` mixed (none `refuse`) | Read as today; decided on the real cell. | Asks. No fact read, before or after the answer. | Asks. No fact read, ever. |
| Some `refuse`, some not | Read as today; decided on the real cell. | Asks for the fact read and the write together. No fact read before the answer. | **Refuse.** No request is filed. The message names the read line and the refusing cell line. |

For a single-cell write the first three rows are the ordinary decision; the table adds nothing.

**What a request binds.** The canonical argv (already in the digest), the candidate cells, and, in the fourth row only, one read entry marked as a fact read on the scope. Its text names the candidates and contains nothing fetched from the provider. The audit record of a write that ran with no fact read lists its candidates as unresolved.

**What an answer covers.**

| Row | Once | `--for` |
| :--- | :--- | :--- |
| Third row (no candidate refused) | The bound argv, whichever candidate it really is. Every possibility was approvable. No fact is read. | A timed entry for each candidate cell on the bound chain, each capped by its `max_for`, all or none. |
| Fourth row, fact read `ask` | The fact reads this call needs on that scope, then the bound argv, in one spend. The answer is spent when the first fact read is sent; there is no refund. If the fact shows a refused candidate, the write is **not sent** and the call is refused naming the line. Otherwise the write is sent under the same answer. | Timed entries for the read cell on the scope and for each non-refused candidate on the bound chain, all or none. The held call then reads the fact as `run` and is decided on its real cell; a refused cell still refuses. |
| Fourth row, fact read `refuse` | No request exists to answer. | No request exists to answer. |

A prediction never performs a fact read that is not itself `run`; in the fourth row it reports "ask" or "refuse" as above. A write-only grant covers a write that needs no fact; it never covers a fact read.

## 5. Approvals: what is bound, what is reused

**Reused unchanged (shipped):** `brokerstore` as the only request store; one-use exact grants (normally 15 minutes); pending life of one hour; three pending requests per jail; the ten-minute cooldown after a deny or expiry; dedup that joins an identical pending request; `FileIfStillAsksWithCoverageContext` and `SpendExactOrFilePreparedContext` for file-or-spend under one source lock; no refund after possible transmission; `yolo approve` as the host-only answer; the refusal of `--add` and `--remember`.

**Proposed extensions:**

| Subject | Shipped | Proposed |
| :--- | :--- | :--- |
| Request record | ordered writes (`CallWrites`) | also ordered reads, when any read asks, and the execution context identity, when not the default |
| Exact digest | canonical argv, repository, stdin digest; generic CLI adds ordered writes and original argv | adds the ordered reads and the context identity **only when present**. A write-only call in the default context digests exactly as today, so stored answers and legacy git and GitHub digests stay valid. |
| Query identity | — | The canonical argv is already in the digest, so an exact answer to a Cost Explorer query covers that query alone. The source must canonicalize the query; the resource ID alone (`ce://host-login/<operation>`) is not the identity. |
| Timed answer | one `call:<cell>` entry per full chain, capped by the smallest touched `max_for`, all-or-none | the same for read cells. A timed read answer covers that cell on that chain in that context for the time: any query, any object body. This is the reusable scope the approver chose by answering `--for`. |
| Revalidation | replays argv and stdin hash through the classifier; checks ordered writes, roots, ceilings; always reads the `all` table; requires the stored holder to be the current launch | also checks ordered reads, candidate cells, the governing rules of both layers and the context identity; reads the table the selector picks now ([§4](#4-how-one-call-resolves-pure-decision)); checks a rebound copy of the holder ([§8](#8-authority-state-concurrency-withdrawal-failure)); runs under the caller's execute budget, never an unbounded one |

A stored answer made before this revision is never read as covering a read or a routed context. New authority is never derived from an old record.

## 6. One lifecycle for every broker (proposed)

The outcome contract below is the same for all four sources. Transport details stay per source.

| Event | Contract |
| :--- | :--- |
| Ask | One durable request, or a join to an identical pending one. Nothing is dispatched. |
| Notification | The user's `on_request` hook runs once per filed request (`brokerhook.Runner`); a join notifies nobody. Hook output is never consent. The Linux doorbell (`approvalnotify`) and `yolo approve --web` are unchanged and gain no authority. |
| In-call hold | Every broker holds an asking call for **30 seconds** and continues in place if answered. Budgets and outcomes are in [§6.1](#61-budgets). |
| Client timeout | The call returns "pending" with the request ID. The request persists. |
| Waiting later | `approvalwait.Wait` and its exit codes are unchanged: allowed 0, used or lapsed 72, pending timeout 75, denied 77, expired 78, closed 79. The wake hint is advice; the retried call is re-authorized at the host. |
| Answer | Once, `--for`, or deny with an optional note. First answer wins. |
| Broker shutdown | Pending requests of that launch close as refused and waiters wake (`Store.End`). Workspace-held grants survive. |
| Publication failure | Reported, never authoritative (`PublishFailed`). |

**Parity work this requires:** the generic daemon (`callbroker.Main`, which also serves selected HTTP) must run the real hook runner, report publication failure and close its pending requests at teardown, in its production entry point and not only in a helper test ([SBP-D9](#SBP-D9)). The launcher's cleanup removes files and processes; it is not a substitute for `Store.End`.

### 6.1 Budgets

All values are fixed in this revision; none is configurable.

| Budget | `git` | `github` | generic CLI and HTTP |
| :--- | :--- | :--- | :--- |
| Decide (classify, facts, lookups) | shipped, unchanged | shipped: inside the 30 s window | shipped: 30 s; 2 s per classifier process; three lookup rounds |
| Hold for an answer | **Shipped:** 30 s (`pushWait`) for a push. **Proposed:** the same 30 s for an asking fetch. | **Shipped:** 30 s, sharing one 30 s window with execution. **Proposed:** 30 s of its own. | **Shipped:** none; exit 75 at once. **Proposed:** 30 s. |
| Execute after an answer | shipped: no overall transfer deadline | **Shipped:** what is left of the window. **Proposed:** a fresh 30 s. | **Shipped:** what is left of the one 30 s call budget. **Proposed:** a fresh 30 s, covering revalidation and the host program. |
| Front connection | shipped: 30 s dial and acknowledgement, then none | shipped: 30 s dial and acknowledgement, then none | **Shipped:** 5 s dial, 40 s connection. **Proposed:** 5 s dial, 100 s connection (30 + 30 + 30, plus 10 s margin). |

A call that does not ask is unchanged everywhere: on the generic broker decide and execute still share one 30 s budget.

**Rules of the hold.**

- The source lock is **not** held while waiting. After an answer the broker takes the lock again, revalidates, spends and dispatches.
- Allowed: the call continues. Denied: refused, with the note. The request expired (possible on a joined request): expired. 30 s pass: "pending" with the request ID (exit 75 on the generic front); the request persists.
- The client disconnects during the hold: the hold ends, nothing is dispatched, the request stays pending.
- The broker shuts down during the hold: `Store.End` closes the request and the call returns "closed".
- Two identical calls hold on one joined request. One exact answer runs one of them; the other returns "used" (72).
- The hold is new behavior on the generic broker, and the separate execute budget is new on GitHub. An asking call's timing therefore differs from baseline by design.

**Reads and the pending cap.** An agent reading widely under an ask policy reaches three pending requests quickly. That is the intended cost of `"ask"`; a timed answer or a narrower `always` line is the remedy. The cap does not change.

## 7. Execution contexts and routing (proposed)

### 7.1 Model

- **The source's declaration owns what a context may change.** A `[call_broker]` declaration may carry a context block: named fields (nonsecret strings), for each field the one host environment name it sets, a list of environment names a routed call unsets, and the resource types that may be routed. Core applies that metadata to a copy of the captured environment. Core has no AWS-specific branch. The block is part of the declaration hash.
- An **execution context** is written in user config only: `[contexts.<name>]` with `source = "<source>"` and one key per declared field. A field value is used exactly as written, with no trimming. It must be non-empty valid UTF-8 of at most 1024 bytes, with no NUL and no control character (U+0000–U+001F, U+007F–U+009F). Spaces and non-ASCII letters are allowed. These are the only limits core imposes: the value is placed in an environment variable and never on a command line, so nothing narrower is needed for safety. The length bound keeps the request record and the identity hash input bounded. A declaration may narrow the rule for its own field; the AWS declaration does not. An undeclared field, a missing field, or a context for a source that declares no context block is a load error.
- A **route** is written in user config only: the key `context = "<name>"` on a `[scopes.<alias>]` table ([§3.6](#36-spelling)). It uses the source's containment, as rules do. `context` on a scope whose resource type the declaration does not list as routable is a load error.
- The shipped behavior is the **default context**: the host environment captured at pin time (`pinCLI`), exactly as today.

```toml
# user config
[contexts.finance]
source = "aws"
profile = "finance-ro"

[scopes.reports]
source = "aws"
resource = "s3://b/reports/"
context = "finance"
```
- Routing is **not permission**. A route grants nothing, admits nothing, and a rule never selects a context.

The workspace cannot write routes or contexts. The classifier never sees a context, a profile or an environment value. The agent cannot name one: profile, region, endpoint, signing and file flags from the jail stay refused, as shipped.

### 7.2 Selection

1. The most specific route whose scope contains the call's resource selects the context. No matching route means the default context.
2. Two routes at the same scope are a config error at load.
3. A call whose operations select different contexts is refused. One call runs in one context.
4. A matched route whose context cannot be built or whose command fails is a failure of that call. It **never falls back** to the default context.

### 7.3 What a routed AWS context runs with

The AWS pack's declaration states the following; core only executes it.

- Field `profile` sets `AWS_PROFILE`. The host's own `AWS_PROFILE` is ignored for a routed call.
- `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` and `AWS_SESSION_TOKEN` are **unset**. Static credentials in the environment would otherwise win over the profile and select the wrong identity silently.
- Routable resource types: S3 buckets and prefixes. Cost Explorer is not routable.
- The other captured names (region, config and credentials file paths, endpoint overrides) pass through as trusted host configuration.
- The profile name is redacted from output like every other declared variable value, as shipped.

The default context is untouched: there, captured static credentials keep winning, as they do today.

### 7.4 What is approval-bound

The **context identity** is a hash of declared, nonsecret facts only: the source, the declaration hash (which includes the set and unset rules), the context's name, its field names and values, and the matched scope's resource. The default context has a fixed identity that adds nothing to the digest. The identity is in new request digests and in timed entry keys. The route is selected again from current user config, and the identity compared, before a request is approved and before a call is dispatched.

**The profile name is a selector, not a verified binding.** It picks an entry in host configuration the user owns. This design proves nothing about which account or role that entry reaches.

| Change | Effect |
| :--- | :--- |
| User edits a route or a context | Picked up live with the rest of the user ceiling (`currentAuthority` rereads fresh user config). The identity changes; a pending request fails revalidation, an exact answer is not spent, a timed entry no longer covers. The next call asks again. |
| A route is added over a resource that had none | Same: the identity moves from default to routed. |
| What the profile *means* changes in the host's AWS config (role, SSO session, rotated key) | **Not detected, and not bound.** No AWS or STS call is made per call and no credential file is read to find out. The profile name is the contract; what it resolves to is host configuration the user owns, as the host login is today. |
| Host environment changes after launch | No effect. The captured set is fixed at pin time. |

The approver sees the context's **name** beside the request. The profile name stays on the host.

### 7.5 `aws-auth` is a separate boundary

`aws-auth` is unchanged: its settings stay user-only, an absent profile serves nothing, `unnarrowed` defaults to false. Credentials it delivers into the jail reach AWS directly, around the broker, wherever IAM permits. No rule or route in this document constrains that path. The shipped full-check report (`reportAWSCredentialBypass`) keeps naming it; no routine launch line is added. This design changes no IAM policy, profile, credential or grant.

## 8. Authority, state, concurrency, withdrawal, failure

**Authority.** The ceiling, routes and contexts are the user's. The ask is the workspace's and takes effect only through the gate. A pack owns its source's declaration: resources, cells and their kinds, containment, classifier. Duplicate owners of one source stay refused (`config.callBrokerInfoBySource`); native git and GitHub authority attaches only to the official pack (`cli.approvalStores`). Removed or unknown sources stay inventory-and-deny only.

**Holder.** A grant is held by the workspace.

- **Shipped.** GitHub checks the same workspace, rebinds a copy of the stored request to the invoking launch and revalidates it (`exactGrantGate`; `TestExactGrantOutlivesItsBrokerForItsWorkspaceOnly`). The generic CLI and HTTP paths revalidate the stored holder unchanged, so a fresh launch is refused; reproduced for the CLI path with zero effects and the answer unspent.
- **Proposed, every broker.** An exact answer may be spent by a later launch of the same source in the same workspace. The broker checks a temporary copy rebound to the invoking launch (jail, start, launch ID). The stored record's history is never rewritten. Workspace, source, digest, declaration, resources and context identity must still match, and both the stored intent and the invoking call are revalidated.
- A **pending** request of a launch that has ended is not approvable: shutdown closed it. Only an answer already given survives its launch.
- Another workspace, source, resource, query or context never reuses an answer.

**Table selection.** One selector ([§4](#4-how-one-call-resolves-pure-decision)) serves every stage. It reads working-set entries under the source lock without taking the store's lock again from inside a store callback. A timed `call:` entry never counts as membership. If membership ends between prediction and dispatch, the write is decided again on `all` and is dispatched only if that decision runs.

**Host program placement.** **Proposed:** the generic broker applies the native placement rule (`brokercore.PlacementRefusal`) to its resolved host program at pin time, after symlink resolution, with the approved workspace. A program inside the workspace refuses the source: its daemon does not serve, and every call is refused with that reason. The native rule's exemptions are unchanged. This is a new refusal for a configuration whose host program lives inside its workspace.

**Live changes.**

| Change | When it takes effect |
| :--- | :--- |
| Ceiling line removed or narrowed; route or context edited | Before the next dispatch, with no relaunch. A stored answer is never honored against a ceiling that no longer admits it. |
| Root withdrawn through the gate | Before the next dispatch (`ReplaceScopeContext` under the source lock). |
| Ask narrowed (`"ask"`, `false`) or widened (`true`), at any scope | With `yolo config apply`, through the live policy snapshot below, or with a fresh launch. A raw workspace edit is not authority and revokes nothing live. |

**Live policy snapshot (proposed).** **Shipped:** the gate's cell values are copied into the immutable launch input; `config apply` replaces roots and grant kinds only. **Proposed:**

- The launch input keeps only what cannot change in a launch: declaration, program and classifier identities, environment names.
- The workspace's gate-approved asks (cells and scoped lines) live in a **snapshot**: a private host file (mode 0600, written by atomic rename) bound to launch ID, source, workspace and declaration hash, with a sequence number.
- `config apply` replaces roots and snapshot in **one** replacement under the source lock. There is no state with new roots and old asks.
- Every effect boundary reads the current snapshot under that lock: decide, file, approve (the standalone `yolo approve` finds it through the validated launch ID), late coverage, spend, dispatch. A snapshot whose binding does not match refuses.
- The ceiling, contexts and routes are not in the snapshot. They are read fresh from user config at the same boundaries, as the ceiling is today.

**When the snapshot cannot be installed.**

| Step | Outcome |
| :--- | :--- |
| Installed | The change is in effect before the next dispatch. Only then does `config apply` report "applied now". A cell change is never reported under "it changes no grant this jail holds". |
| Install fails | `config apply` writes a refusal marker for that launch and source under the same lock. The broker refuses every call for the source, naming `yolo config apply` and relaunch. Apply exits nonzero. |
| The marker cannot be written either | Apply exits nonzero, states that the running jail still holds the old policy, and names the jail to stop. It never reports success or "restart later". |

This holds for a narrowing and a widening alike. A widening that fails leaves the source refused, not widened.

**Declaration changes at the gate (proposed).** A source is **inactive** in a workspace when it has no approved root and this gate requests none. An inactive source can run nothing: selection is not authority, and a grant with no root admits nothing.

| Situation | Gate behavior |
| :--- | :--- |
| Inactive source; its declaration is new or changed | No scope prompt and no record. The declaration by itself is not a scope change. The source stays refused, as the no-access floor already has it. |
| An inactive source gets its first root | One prompt: the roots, and the declaration under which they will be used, shown together. Approval records both. |
| Active source; its declaration changed; roots unchanged | A prompt of its own kind, titled as a broker declaration change for that source and pack. It lists the disclosure lines that were removed and added. It has no repository origin line and no added / removed counts. It needs `y`. |
| Active source; declaration and roots both changed | One prompt with both blocks. |
| Any block with nothing changed | Not rendered. A prompt with no rendered block is not shown. |

- **Until an active source's changed declaration is approved**, that source is refused under both the old and the new declaration. Cancelling records nothing and the prompt returns at the next launch or apply. That repeat is correct: authority is pending a decision.
- **Origin wording follows the source's root mode.** Declared roots name the file that declares them (`WorkspaceRead.CallRoots` keeps it). Roots derived from remotes name `.git/config`. A declared-roots source never shows `.git/config`.
- **An inactive source runs no pack code on a call.** The broker refuses before any classifier or host program starts.
- **Nothing is suppressed or auto-accepted.** Pack read and exec trust disclosures are separate, unchanged, and still shown. A declaration change for a source that holds authority always needs a human's `y`. There is no quiet mode.

**Concurrency.** Decide, file, spend and dispatch happen under the one source lock, as shipped (`boundWrite`), now for reads as well. The in-call hold happens outside it ([§6.1](#61-budgets)). Two retries of one approved call spend it once. Two approvers: the first answer stands. A withdrawal that lands before dispatch blocks it and spends nothing. One that lands after possible transmission changes nothing: no refund, no retry. A multi-operation timed answer commits for all its cells or none.

**Failure.** Any failure to load rules, routes, the launch input or the scope file refuses. Classifier failure refuses. Hook failure is recorded on the request and changes no decision. Publication failure is reported and changes no decision.

## 9. Each source

| Source | Read cells (proposed) | Scope containment (proposed) | Notes |
| :--- | :--- | :--- | :--- |
| `git` | one, `fetch`: a fetch that uses a host credential | A `[repos.<alias>]` repository, matched to remotes by the shipped exact-URL and equivalent-endpoint matcher. Normalized repository identity alone never admits a remote. No containment below it. | Decision points in [§9.1](#91-git-fetch-paths). Push rules, branch and tag grants and the working set are unchanged; a push is decided by its own lines, not by the `fetch` line ([§3.7](#37-read-and-write-are-independent)). The anonymous public HTTPS fetch is never blocked by any line ([OQ-SBP1](#OQ-SBP1), ruled B). |
| `github` | exactly one in this revision, covering every supported read and every fact read | normalized repository identity, and nothing narrower; `[repos.<name>]` tables are the scope spelling already built | Supersedes [GW-D8](github-write-scopes.md#GW-D8) and the read bound of [GW-D80](github-write-scopes.md#GW-D80). Fact reads follow [§4.1](#41-fact-reads). The unbuilt cell tables of [`github-write-scopes.md`](github-write-scopes.md) stay unbuilt by this design. |
| `aws` | S3: list, get, head. Cost Explorer: its four queries. | S3: bucket, then prefix ending in `/`, longest match (the shipped root rule). Cost Explorer: operation under `ce://host-login`. | Routes apply to S3 scopes. Cost Explorer stays host-login-wide and takes no route in this revision. |
| `circleci` | project run listing | exact project root `gh/{owner}/{repo}` | `rerun_workflows` stays refused by the classifier. A rule naming it is accepted, inert, and reported as "declared, not implemented". |

Git and GitHub keep their native transports, stores and digests. They adopt the model through their own adapters; nothing here rewrites their wire protocols.

**Canonical resource forms for `[scopes]`.** S3: `s3://<bucket>/` or `s3://<bucket>/<prefix>/`, always ending in `/`. Cost Explorer: `ce://host-login` or `ce://host-login/<operation>`. CircleCI: `gh/<owner>/<repo>`.

### 9.1 Git fetch paths

**Shipped.** The broker knows the service and the original remote URL before it opens an upstream (`serveRequest`). An SSH fetch, and a push, require the read grant and scope. An HTTPS fetch is sent anonymously first; `credentialPolicy` is re-checked before the send, on a 401 before the credential is filled, after the fill, and before host git runs. Imports decide separately (`DecideImportAccess`, `FetchImport`): HTTPS falls back to anonymous, SSH refuses.

**Proposed.** The `fetch` cell is decided at each of these points, and nowhere else:

| Path | Decision point | `run` | `ask` | `refuse` |
| :--- | :--- | :--- | :--- | :--- |
| SSH fetch | before `ssh` is resolved or started | proceeds | request filed, 30 s hold; no `ssh` process before an answer | refused |
| HTTPS fetch, credentialed leg | every shipped `credentialPolicy` check | credential may be filled and sent | request filed, 30 s hold at the first check that needs a credential; no credential is filled before an answer | no host credential is filled or sent; the request goes on exactly as it would with no valid credential |
| HTTPS fetch, anonymous leg | none: it is not a policy subject | sent | sent | sent |
| Import and cache fetch | the import decision | credentialed import | treated as no credential authority: SSH refuses, HTTPS goes anonymous. **No request is filed**, since no agent call exists to bind or hold. | the same as `ask` |

A live timed answer counts as `run` on every path.

**Refusing a git read means "send it without the credential" ([OQ-SBP1](#OQ-SBP1), ruled B).** An explicit ceiling `never` or ask `false` on `fetch` withholds the host credential and nothing else. A public repository still fetches and imports anonymously. A private one fails as it does today with no credential. SSH has no anonymous form, so it is refused.

This rule is about git's anonymous HTTPS fetch and import only. It does not create an anonymous path for any write, any `gh` or `aws` command, or any other source; there, a refused operation is not sent at all, and no host credential is resolved for it.

## 10. Compatibility

- A configuration with no new-form line (no `never`, no `read.<cell>`, no scoped read, no `[scopes]`, no context) keeps every baseline run / ask / refuse decision, digest and grant, for all four sources.
- Four things differ from baseline for such a configuration, each on purpose: an asking generic call holds for up to 30 s before returning "pending"; an answered GitHub write gets a fresh 30 s to execute; a generic exact answer survives a relaunch; a generic source whose host program sits inside its workspace is refused.
- The on-switch meaning of the read grant is kept under a legacy gate record and retired only through the gate ([§3.7](#37-read-and-write-are-independent)).
- Two shipped defects stop: a working-set-only ceiling is now honored at dispatch, and a cell-only `config apply` now reaches the running broker or says it did not.
- Broad read grants, branch and tag grants, repository-level GitHub grants, working-set defaults and historical `--add` entries keep their meaning.
- Stored requests, exact answers and timed entries stay readable and valid for what they covered. None gains read or routed coverage.
- The launch input (`yolo.call-broker-launch/2`) takes a version step: it carries read cells and the context block, and no longer carries the workspace's cell values, which move to the snapshot ([§8](#8-authority-state-concurrency-withdrawal-failure)). A broker and an input of different versions refuse to pair, naming relaunch. Root-bearing format migration stays refused, as documented for AWS.
- The classifier protocol gains a cell and a description per read. A classifier that reports reads without cells is treated as declaring the source's single read cell if it has exactly one, and is refused otherwise.
- `scopedCeilingReadProblem` and `readableScopedCeiling` change from refusing to accepting, with their tests; the message that pointed at `[allow.<broker>] always` goes.

## 11. Non-goals

- A second approval flow, store, pending directory, wait command or UI.
- Any change to `aws-auth`, IAM, host profiles, credentials or live grants.
- New AWS operations, local-path copies, `sync`, or other AWS services.
- Per-byte or per-action approvals for serial, audio, log or process bridges.
- Implementing CircleCI reruns or the unbuilt GitHub cell tables.
- Proving what identity a host profile resolves to.
- Mutating authority from the browser view.
- New named `yolo allow` or `yolo request` forms for cells, scopes or contexts; the shipped `yolo config` editing verbs are the surface ([§3.6](#36-spelling)).
- Verifying which account or role a profile name reaches.
- File layout and task order (plan work).

## 12. Acceptance scenarios

Each is account-free and uses fake host executables or fixtures. "Refused" means no request is filed and nothing is dispatched.

### 12.1 Floor and compatibility

| # | Given | Then |
| :--- | :--- | :--- |
| A1 | Each source selected, nothing granted | Every credentialed read and write is refused. The anonymous public git fetch still works. |
| A2 | Each source with its existing broad grants, no new-form line | Every run / ask / refuse decision and digest equals baseline. A call that does not ask has baseline timing and exit code. An asking generic call returns 75 after the 30 s hold instead of at once. |
| A3 | A stored pre-revision exact answer and timed entry | Each still covers what it covered; neither covers a read or a routed call. |
| A4 | An older launch input, a call needing a read cell | Refused with a message naming relaunch. |

### 12.2 Rules

| # | Given | Then |
| :--- | :--- | :--- |
| B1 | Ceiling: source `read` `on_request`; `ce://host-login` `read` `always` | A Cost Explorer query runs; an S3 list asks. |
| B2 | B1 plus `s3://a/team/` `read` `always` | Get under `s3://a/team/` runs; under `s3://a/team-other/` and `s3://a/` asks; listing the parent asks. |
| B3 | `s3://b/reports/` `read` `always`, no write line anywhere | Get runs; put and delete there are refused. |
| B4 | B3 plus source-scope `all.put_objects` `on_request` | Put under `s3://b/reports/` asks. The read line neither gave nor took write authority. |
| B4a | B4 plus `never = ["all", "working_set"]` on `s3://b/reports/` | Get runs; put and delete there are refused with no request; put under a sibling prefix still asks. |
| B4b | Source-scope `never = ["read"]`; `s3://a/team/` `always = ["read"]` | Reads under `s3://a/team/` run; every other read is refused. |
| B4c | One token in `always` and `never` at one scope; or `never = ["working_set.put_objects"]` beside `always = ["all.put_objects"]` at one scope | Load fails in `yolo check`, launch and `config apply` alike. |
| B5 | Broad read `always`; ask `false` for `read` on one repository or prefix | Credentialed reads there are refused; public Git fetch/import still proceeds anonymously; elsewhere reads run. |
| B6 | Ask `true` for a read with no covering ceiling line | Reported at load, counts as absent, refused at run time. |
| B7 | Two lines, same scope, same cell, one layer | Load fails in `yolo check`, launch and `config apply` alike. |
| B8 | Ask `"ask"` on a scope under ceiling `always` | Asks. |
| B9 | GitHub: read `always` for repository X only | Reads of X run; reads of admitted Y are refused. With the broad grant instead, both run. |
| B10 | CircleCI: project read `on_request` | The listing asks and runs after an answer. A rerun is refused whatever the rules say. |
| B11 | An unknown service, operation or flag, any rules | Refused by the classifier; no request. |
| B12 | A scope outside every root | Accepted, inert, reported by `yolo check`. |

### 12.3 Approvals

| # | Given | Then |
| :--- | :--- | :--- |
| C1 | A read asks; answered once | The same call runs once. A second run asks again. |
| C2 | A Cost Explorer query answered once | A different query of the same operation asks. |
| C3 | A read answered `--for` | Other calls of that cell on that chain run until expiry, capped by `max_for`; a neighboring prefix or another cell asks. |
| C4 | A call with one asking read and one asking write | One request naming both; one answer; both bound in the digest. |
| C5 | A call with a refused operation and an asking one | Refused; no request. |
| C6 | Fact read `ask`; candidates `false` and `"ask"` | No provider call before the answer; the request lists the fact read and the candidates; its text holds no provider data. |
| C7 | C6 answered once; the fact shows the asking candidate | Fact read, then the write, under one spend. A retry asks again. |
| C7a | C6 answered once; the fact shows the `false` candidate | The answer is spent at the fact read; the write is not sent; the refusal names the line. |
| C7b | Any fact-read decision; every candidate `refuse` | Refused; no request; no provider call. |
| C7c | Fact read `ask` or `never`; every candidate runs | The write runs with no provider read. |
| C7d | Fact read `ask` or `never`; candidates `run` and `"ask"`, none refused | Asks; answered once, the bound argv runs with no provider read, before or after the answer. |
| C7e | **The owner's case.** `comment_prs = false`, `comment_issues = "ask"`, read `never`, `gh issue comment 70` on an item of unknown kind | Refused. No request is filed, so nothing can be answered once or `--for`. No provider call. The message names both lines. |
| C7f | C7e with a live timed entry for `comment_issues` on item 70 | Still refused. |
| C7g | C7e, then read is changed to `always` | The fact is read; an issue asks, a PR is refused. |
| C7h | C6 answered `--for` | Timed entries for the read cell on the repository and the non-refused candidate, or none. The held call reads the fact; a PR is still refused. |
| C7i | A write with one cell and no fact, read `never` | Decided by its write lines alone. |
| C7j | A dispatch grant that needs the default branch; fact read `ask` or `never` | The grant does not cover; the write asks as the uncovered write; no provider read. |
| C7k | A call needing a fifth fact read | Refused. |
| C8 | A prediction on any asking call | Nothing filed, spent, dispatched or fetched. |

### 12.4 Lifecycle

| # | Given | Then |
| :--- | :--- | :--- |
| D1 | An ask on each of the four sources | One request; the hook runs once; an identical retry joins and does not notify. |
| D2 | The client times out | The request is still pending; `wait` returns 0 on allow, 77 on deny, 78 on expiry. |
| D3 | The generic broker shuts down with a pending request | The request closes as refused; a waiter wakes with 79; workspace grants remain. |
| D4 | Four asks from one jail | The fourth is refused by the cap. A denied command retried inside ten minutes is refused by the cooldown. |
| D5 | The hook fails or prints "approved" | Recorded on the request; the decision is unchanged. |
| D6 | An answer arrives inside the in-call hold | The call continues in place and returns the result. |
| D7 | Generic: the answer arrives at second 29 | The program still gets a full 30 s; the front connection does not time out. |
| D8 | Generic: no answer | Exit 75 with the request ID at 30 s; the request is pending. |
| D9 | A second call on the same source during another call's hold | It is decided and runs; the lock was not held. |
| D10 | The client disconnects during the hold | Nothing is dispatched; the request stays pending. |
| D11 | Two identical calls hold on one request; one exact answer | One runs; the other returns 72. |
| D12 | GitHub: answer late in the hold | The write gets a fresh 30 s, not the window's remainder. |
| D13 | The production generic daemon entry point | The hook runs on a filed request, a join does not notify, shutdown closes and publishes, a publication failure is reported. |

### 12.5 Concurrency and withdrawal

| # | Given | Then |
| :--- | :--- | :--- |
| E1 | Two simultaneous retries of one approved call | Exactly one dispatch. |
| E2 | Allow and deny race | The first stands; the second reports the request already answered. |
| E3 | Root, ceiling line or route withdrawn before dispatch | No dispatch; the answer is not spent. |
| E4 | Withdrawal after possible transmission | No refund and no retry. |
| E5 | A timed answer over two cells where one exceeds its cap | Neither entry is written. |
| E6 | Same workspace, fresh launch, unspent exact answer | Spendable after revalidation. Another workspace or source: not. |

### 12.6 Routing

| # | Given | Then |
| :--- | :--- | :--- |
| F1 | Route `s3://a/team/` → context `team` (profile P) | The fake `aws` sees `AWS_PROFILE=P` and none of the three static credential names. |
| F2 | F1 with static credentials in the host environment | They are absent from the routed call and present in a default-context call. |
| F3 | No matching route | The call's environment equals baseline. |
| F4 | Routes on `s3://a/` and `s3://a/team/` | The longer one selects. Two at the same scope fail at load. |
| F5 | The jail passes `--profile` or sets `AWS_PROFILE` | Refused or ignored as at baseline; the route decides. |
| F6 | A route is edited while a request is pending, answered, or covered by a timed entry | The request fails revalidation, the answer is not spent, the entry does not cover; the next call asks. |
| F7 | The routed context fails to run | The call fails; nothing runs in the default context. |
| F8 | A workspace file declares a route or context | Refused at load. |
| F9 | `aws-auth` configured beside the broker | Its behavior and the full-check report are byte-for-byte unchanged. |
| F10 | A context for a source whose declaration has no context block; an undeclared field; `context` on a Cost Explorer scope | Each is a load error. |
| F11 | The declaration's unset list changes | The context identity changes; stored answers and timed entries for the old identity do not cover. |
| F12 | Any routed call | The classifier's input holds no field value and no environment value. |
| F13 | A synthetic source whose declaration sets a different variable | Core applies it with no source-specific code. |
| F14 | Field values `team prod`, `équipe-ro`, and a 1024-byte name | Each loads and reaches the fake program's environment byte for byte. |
| F15 | An empty value, one with a newline or NUL, one of 1025 bytes, invalid UTF-8 | Each is a load error naming the field. |

### 12.7 Independence, live apply, holder, placement, git

| # | Given | Then |
| :--- | :--- | :--- |
| G1 | Legacy gate record; write lines; no source-scope read grant | Every call refused, as at baseline. |
| G2 | G1, then a gate approval | The held-back write lines are listed as added; after `y` a write-only call runs or asks by its own lines; reads are refused. |
| G3 | Legacy record; broad read grant held | No prompt; no decision changes. |
| G4 | Marked record; ask `false` for `read` on prefix P | Reads on P refused; a write on P is decided by its write lines. |
| G5 | Running generic broker; ask `true` → `false`; `config apply` | The next call is refused with no relaunch; apply reports "applied now". |
| G6 | G5 with the snapshot install made to fail | The source refuses every call; apply exits nonzero. With the marker write failing too, apply exits nonzero and says the old policy is still live. |
| G7 | A snapshot bound to another launch, source or declaration | Refused. |
| G8 | Working-set-only ceiling; covered write | Runs through the real transaction. |
| G9 | G8; membership removed between prediction and dispatch | Not dispatched under `working_set`; decided again on `all`. |
| G10 | Generic CLI and HTTP: same workspace, fresh launch, unspent exact answer | Spent once; the stored record's jail, start and launch are unchanged afterwards. |
| G11 | A pending request of an ended launch | `yolo approve` refuses it. |
| G12 | Generic host program inside the workspace, directly or by symlink | The source is refused. A program the native rule exempts is accepted. |
| G13 | Git SSH fetch that asks | No `ssh` process before an answer. |
| G14 | Git HTTPS fetch that asks after a 401 | No credential is filled before an answer. |
| G15 | An import whose `fetch` decision is `ask` | No request filed; SSH refuses; HTTPS goes anonymous. |
| G16 | Marked record; push lines; no `fetch` line | The push is decided by its own lines. |
| G17 | Git read `never` (ceiling) or `false` (ask) on a public HTTPS remote | Fetch and import succeed anonymously; no credential helper runs; no credential is sent. |
| G18 | G17 on a private HTTPS remote | Fails on the 401 exactly as with no credential; no credential is filled after it. |
| G19 | G17 on an SSH remote | Refused; no `ssh` process. |
| G20 | Read `never` on a `gh` or `aws` scope | The read is refused and not sent in any form; no anonymous attempt; no host credential is resolved. |
| G21 | `aws` selected, no root, no prior record | No scope prompt at launch or apply; every `aws` call refused; no classifier or host program starts. Pack trust disclosures appear as before. |
| G22 | G21, then a first root is requested | One prompt showing the root and the declaration; origin names the declaring file, not `.git/config`. |
| G23 | Active source; declaration changes; roots unchanged | A declaration-change prompt with removed and added lines, no origin line, no counts. |
| G24 | G23 cancelled | Nothing recorded; the source is refused; the prompt returns at the next launch. |
| G25 | A remotes-derived source (CircleCI) with a changed remote | The scope block names `.git/config`. |

## 13. Unverified claims this design relies on

| # | Claim | Status | Needed before build |
| :--- | :--- | :--- | :--- |
| U1 | Generic exact answers are rejected across launches | **Reproduced** (CLI): a fresh valid holder is refused, zero effects, the answer keeps its one use. HTTP has the same code path, not separately run. | Closed by the holder rule in [§8](#8-authority-state-concurrency-withdrawal-failure); G10 |
| U2 | Generic revalidation checks `all` even when `working_set` decided | **Reproduced:** prediction runs, the transaction refuses | Closed by the one selector in [§4](#4-how-one-call-resolves-pure-decision); G8, G9 |
| U3 | Cell values are frozen in the launch input | **Confirmed:** a byte change refuses; apply publishes roots and grant kinds only; a cell-only change is reported "applied now" | Closed by the snapshot in [§8](#8-authority-state-concurrency-withdrawal-failure); G5–G7 |
| U4 | No path calls `Store.End` for the generic broker | **Confirmed by source** in the production caller and launcher; no shutdown run | Closed by [§6](#6-one-lifecycle-for-every-broker-proposed); D3, D13 |
| U5 | Generic pinned programs get the native placement refusal | **False, reproduced:** `pinCLI` accepts what the native rule rejects | Closed by the placement rule in [§8](#8-authority-state-concurrency-withdrawal-failure); G12 |
| U6 | Git's credentialed fetch can be decided per remote without transport changes | **Confirmed by source**, with three paths, not one | Closed by [§9.1](#91-git-fetch-paths); G13–G15 |
| U7 | Each GitHub fact read can be named before it is made | **True for the repository; false for the final cell and for a job's run** | Closed by candidate cells and the repository-level fact read in [§4.1](#41-fact-reads); C6–C7k |

**Still to confirm, in the plan's first task.** None reopens a behavior choice; each row says what holds if the fact is otherwise.

| # | Fact | If it is otherwise |
| :--- | :--- | :--- |
| V1 | The `yolo config add` and `yolo config set` forms in [§3.6](#36-spelling) work for the new paths as they do for shipped ones | The messages name whichever shipped editing form does; no named verb is added. |
| V2 | The git broker can hold a fetch for 30 s after the helper's 30 s dial and acknowledgement has completed | If the hold would fall inside that acknowledgement, an asking fetch returns "pending" at once with the request ID and no hold; nothing else changes. |
| V3 | No GitHub call needs more than four fact reads | The call is refused by the cap; the cap is not raised without a design change. |
| V4 | At baseline an inactive generic source already refuses before its classifier starts | If it does not, that refusal is built as part of [§8](#8-authority-state-concurrency-withdrawal-failure)'s gate rule; the gate behavior is the same either way. |

None of V1–V4 decides policy. Each fallback is the stricter or the unchanged behavior: V1 changes only which command a message names, V2 only whether a git fetch waits, V3 and V4 only refuse.

The probes are compatibility evidence against baseline. They are not evidence that any proposed behavior works.

## Decision Ledger

Dates are the date each item was recorded in this document. Owner rulings are restated from the task brief and the evidence packet; the Git ruling includes the owner's supplied quote. Engineering decisions record Fable 5.2's closure and corrective overlay, not independent publisher choices.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-SBP1"></a>[OQ-SBP1](#OQ-SBP1) | **Ruled B.** An explicit read refusal on a git remote never blocks the anonymous public HTTPS fetch or import; it withholds the host credential only. Owner: *"no, never block public fetch ... blocking not allowing for us just means let the request happen exactly as it would if it didn't have a valid credential ... sent without the credentials."* Limited to git's anonymous HTTPS fetch and import: no anonymous write, no anonymous path for any other command or source, no host credential resolution for a refused operation | 2026-11-17 | [§9.1](#91-git-fetch-paths) | shipped for the no-line case; the explicit lines are not built |
| <a id="SBP-D1"></a>SBP-D1 | **Owner's ruling:** unconfigured access stays refused; selecting a pack grants no execution authority | 2026-11-17 | [§2.1](#21-the-four-operation-brokers), [§3.3](#33-how-the-two-layers-combine) | shipped floor, kept |
| <a id="SBP-D2"></a>SBP-D2 | **Owner's ruling:** every applicable broker uses the same allow / ask / deny, standing permission, scoped read and write, notification, bounded wait, timeout and persistent-request lifecycle. It is a shared broker contract, not an AWS mechanism; resource types differ per source | 2026-11-17 | [§3](#3-the-policy-model-proposed), [§6](#6-one-lifecycle-for-every-broker-proposed) | no |
| <a id="SBP-D3"></a>SBP-D3 | **Owner's ruling:** scoped reads are universal. This supersedes write-only scope expression: [GW-D8](github-write-scopes.md#GW-D8) and the "a scoped ceiling lists write capabilities" bound of [GW-D80](github-write-scopes.md#GW-D80). Existing explicit broad grants and shipped defaults keep their meaning; nothing becomes ask-by-default | 2026-11-17 | [§3.4](#34-existing-broad-grants), [§10](#10-compatibility) | no |
| <a id="SBP-D4"></a>SBP-D4 | **Owner's ruling:** AWS must be able to say Cost Explorer always reads, S3 generally asks, with bucket or prefix exceptions that are read-only or read and write. Reads take fine-grained approvals like writes; any reusable scope is explicit | 2026-11-17 | [§3.5](#35-the-owners-aws-example-as-rules), [§5](#5-approvals-what-is-bound-what-is-reused) | no |
| <a id="SBP-D5"></a>SBP-D5 | **Owner's ruling:** the host routes S3 buckets and prefixes to profiles; the agent never picks a host profile; ordinary supported `aws` commands go through the broker | 2026-11-17 | [§7](#7-execution-contexts-and-routing-proposed) | no |
| <a id="SBP-D6"></a>SBP-D6 | **Owner's ruling:** `aws-auth` stays separate and unchanged; credentials it delivers can bypass the broker where IAM permits, and that is disclosed as its own boundary. No live credential, grant or IAM change is made automatically | 2026-11-17 | [§7.5](#75-aws-auth-is-a-separate-boundary) | shipped, kept |
| <a id="SBP-D7"></a>SBP-D7 | The most specific scope with a line governs within a layer and its value is final there; absence continues the search; a cell token beats a table or kind token at the same scope; an exact duplicate is a load error; the stricter layer wins. Applies [OQ-GW7](github-write-scopes.md#OQ-GW7) to resources | 2026-11-17 | [§3.2](#32-which-rule-governs) | no |
| <a id="SBP-D8"></a>SBP-D8 | Read and write never imply each other. A read line neither grants nor refuses a write, so read-only under a broader write allowance is a read line plus an explicit ceiling `never` on the write tables. Amended 2026-11-17 from "a read line with no write line". Applies [GW-D38](github-write-scopes.md#GW-D38) | 2026-11-17 | [§3.5](#35-the-owners-aws-example-as-rules), [§3.7](#37-read-and-write-are-independent) | no |
| <a id="SBP-D9"></a>SBP-D9 | The shipped store, digest, wait, hook and notification machinery is the only approval flow. The generic daemon is brought to parity in its production entry point | 2026-11-17 | [§5](#5-approvals-what-is-bound-what-is-reused), [§6](#6-one-lifecycle-for-every-broker-proposed) | no |
| <a id="SBP-D10"></a>SBP-D10 | Reads and the context identity enter the digest only when present, so every existing digest and stored answer is unchanged | 2026-11-17 | [§5](#5-approvals-what-is-bound-what-is-reused) | no |
| <a id="SBP-D11"></a>SBP-D11 | A fact read is a read on a scope known beforehand (GitHub: the repository), decided like any read, never made before consent and never forced when refused. An identity the fact would reveal is a finite candidate set. A refused candidate is never reachable through an approval: when candidates include a refused one and the fact read is refused, the call is refused and no request is filed; when the fact read asks, one answer covers the fact reads and the bound argv, spent at the first fact read, and a refused real cell is not sent. With no refused candidate the write needs no fact. At most four fact reads per call. Corrected 2026-11-17: the earlier "ask as unreadable, then run without the fact" outcome could pass a refused cell | 2026-11-17 | [§4.1](#41-fact-reads) | no |
| <a id="SBP-D12"></a>SBP-D12 | The approval-bound context identity is the source, declaration hash, context name, field names and values, and matched scope. The profile name is a trusted host-config selector, not a verified account binding; no AWS or STS lookup is made. A routed context never falls back to the default | 2026-11-17 | [§7.3](#73-what-a-routed-aws-context-runs-with), [§7.4](#74-what-is-approval-bound) | no |
| <a id="SBP-D13"></a>SBP-D13 | An exact answer is held by the workspace and is spendable by a later launch of the same source, checked on a temporary rebound copy with the stored history unchanged, for every broker including generic HTTP. A pending request of an ended launch is not approvable | 2026-11-17 | [§8](#8-authority-state-concurrency-withdrawal-failure) | GitHub only; the generic gap is reproduced |
| <a id="SBP-D14"></a>SBP-D14 | Credential, sign-in, log, process, serial, audio and adapter services are not operation brokers and get no per-operation approvals here | 2026-11-17 | [§2.2](#22-services-that-are-not-operation-brokers) | — |
| <a id="SBP-D15"></a>SBP-D15 | A timed answer to a read covers its cell on its full chain in its context for the time, as a timed write answer does; an exact answer covers one canonical query | 2026-11-17 | [§5](#5-approvals-what-is-bound-what-is-reused) | no |
| <a id="SBP-D16"></a>SBP-D16 | The ceiling gains an explicit refusal: a `never` list beside `always` and `on_request`. It governs like any line and covers exactly the tokens it names. `off` stays the shipped whole-pack switch | 2026-11-17 | [§3.3](#33-how-the-two-layers-combine), [§3.6](#36-spelling) | no |
| <a id="SBP-D17"></a>SBP-D17 | Spelling: `[scopes.<alias>]` with `source` and `resource` for call-broker sources; `[repos.<alias>]` for git and GitHub; read tokens `read` and `read.<cell>`; no new named verb, the shipped `yolo config` editing verbs are the surface. Supersedes the "plan work" deferral | 2026-11-17 | [§3.6](#36-spelling) | no |
| <a id="SBP-D18"></a>SBP-D18 | The read grant stops being a source's on-switch. Under a legacy gate record the on-switch is kept; write lines it held back come alive only as added lines at a gate `y`. No prompt for a workspace that holds the read grant | 2026-11-17 | [§3.7](#37-read-and-write-are-independent) | no |
| <a id="SBP-D19"></a>SBP-D19 | What a context may set and unset is declared by the source's pack and hashed with its declaration; core applies it generically and has no AWS branch. Contexts and routes are user config only | 2026-11-17 | [§7.1](#71-model), [§7.3](#73-what-a-routed-aws-context-runs-with) | no |
| <a id="SBP-D20"></a>SBP-D20 | Workspace asks reach a running broker through a private, launch-bound snapshot replaced together with roots under the source lock and read at every effect boundary. A failed install refuses the source; apply never reports success it did not achieve | 2026-11-17 | [§8](#8-authority-state-concurrency-withdrawal-failure) | no |
| <a id="SBP-D21"></a>SBP-D21 | Every broker holds an asking call 30 s outside the source lock, then gives execution its own budget: generic and GitHub a fresh 30 s, git unbounded as shipped. The generic front connection deadline becomes 100 s. Fixed, not configurable. This timing is new behavior, not baseline | 2026-11-17 | [§6.1](#61-budgets) | git push hold only |
| <a id="SBP-D22"></a>SBP-D22 | One table selector serves prediction, filing, approval, late coverage and spend, on the working-set entries current under the lock | 2026-11-17 | [§4](#4-how-one-call-resolves-pure-decision), [§8](#8-authority-state-concurrency-withdrawal-failure) | no |
| <a id="SBP-D23"></a>SBP-D23 | The generic broker applies the native placement rule to its resolved host program; a program inside the workspace refuses the source | 2026-11-17 | [§8](#8-authority-state-concurrency-withdrawal-failure) | no |
| <a id="SBP-D24"></a>SBP-D24 | Git's `fetch` cell is decided before SSH starts, at every HTTPS credential check and in the import decision; an import that would ask files nothing. Git scopes match remotes by the shipped exact-URL matcher | 2026-11-17 | [§9.1](#91-git-fetch-paths) | no |
| <a id="SBP-D25"></a>SBP-D25 | A context field value is used as written: non-empty valid UTF-8, at most 1024 bytes, no NUL or control characters; spaces and non-ASCII allowed. Replaces the earlier 128-character printable-ASCII rule, which could reject a legitimate profile name for no safety gain | 2026-11-17 | [§7.1](#71-model) | no |
| <a id="SBP-D26"></a>SBP-D26 | A declaration by itself is not a scope change. An inactive source (no approved or requested root) raises no scope prompt and runs no pack code. An active source's declaration change is its own prompt, needs `y`, and refuses the source until answered. Origin wording follows the root mode; an empty block is not rendered. Trust disclosures are unchanged | 2026-11-17 | [§8](#8-authority-state-concurrency-withdrawal-failure) | no |
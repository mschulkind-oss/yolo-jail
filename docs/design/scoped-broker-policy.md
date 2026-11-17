---
title: "Scoped broker policy: one allow / ask / deny contract, with resource scopes, for every operation broker"
date: 2026-11-17
status: in-review
stage: DESIGN
next: "Rule OQ-SBP1; close the unverified source claims before completing the companion seed"
depends-on:
  - pack-brokers.md
  - github-write-scopes.md
  - boundary-broker.md
tags: [design, brokers, policy, approvals, scopes, aws, git, github, circleci, routing]
summary: "Every operation broker (git, github, aws, circleci) answers each classified operation with run, ask or refuse from one model: a cell (read or write) on a resource, decided by the most specific scope that has a line for it, in the user's ceiling and in the workspace's request, the stricter of the two winning. Reads become cells, so a read can be scoped, asked for, answered once or for a time, exactly as a write is today. Nothing configured stays refused and every existing broad grant keeps its meaning. A host-only route table picks the execution context (for AWS, a host profile) from the resource; the context's declared identity is bound into the approval. No new approval flow, store or UI: the shipped request store, digest, wait, hook and notification machinery is extended, and the generic broker daemon is brought to parity with it."
---

# Scoped broker policy: one allow / ask / deny contract, with resource scopes, for every operation broker

**Status:** 2026-11-17 — proposed and unbuilt; under review. One owner question is open, [OQ-SBP1](#OQ-SBP1). **Shipped** describes the baseline, not this revision. Everything marked **Proposed** is new behavior. [Section 13](#13-unverified-claims-this-design-relies-on) lists source claims not yet reproduced.

> **In short.** Read and write operations should follow one scoped allow / ask / deny contract across operation brokers, bounded by host authority and served by the existing approval lifecycle.

**Needs your ruling:** [OQ-SBP1](#OQ-SBP1).

**Evidence boundary.** This design rests on [source-read grounding](../research/scoped-broker-policy-grounding.md) against baseline `a97a0e055f20de4524063b70729595ea8794bef8` and on a direct read of [`github-write-scopes.md`](github-write-scopes.md). No test, build, API call or agent run supports it. Code anchors are file-and-symbol pointers from the grounding; they were not re-opened for this design.

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
| **Ceiling** | The user's host config: the most any workspace may hold. Values `on_request`, `always`, or no line. |
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
5. **No execution routing.** One captured host environment serves every AWS call.

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

A narrower line therefore overrides a broader one in either direction within its own layer: a narrower `false` under a broad grant refuses, and a narrower `true` under a broad `"ask"` runs. It cannot escalate, because the other layer still bounds it.

### 3.3 How the two layers combine

Unchanged from the shipped write table (`config.CallBrokerTablePolicy`), now applied to reads too:

| Governing ask ↓ / governing ceiling → | no line | `on_request` | `always` |
| :--- | :--- | :--- | :--- |
| `false` | refuse | refuse | refuse |
| `"ask"` | refuse | ask | ask |
| `true` (after the gate's `y`) | refuse; reported at load, counts as absent | run | run |
| absent | refuse | ask | run |

"No ceiling line" refuses and files nothing. This states as a rule what the shipped revalidation already enforces (a request is not filable under an `off` ceiling). It is the no-access floor: an ask is filable only where the host has said this operation may be asked for.

A shipped default on a write cell (the working set's comment and edit cells) keeps its shipped meaning and needs no ceiling token ([GW-D42](github-write-scopes.md#GW-D42)). No read cell ships a default.

### 3.4 Existing broad grants

A shipped broad read grant is read as a ceiling `always` line for `read` at the source scope. With no scoped line written, every decision is identical to today. Scoped lines only take effect when someone writes them. No file is migrated and no default becomes `"ask"` ([SBP-D3](#SBP-D3)).

### 3.5 The owner's AWS example, as rules

| Layer | Scope | Line | Effect |
| :--- | :--- | :--- | :--- |
| ceiling | `ce://host-login` | `read` `always` | Cost Explorer queries run |
| ceiling | source | `read` `on_request` | every other read asks |
| ceiling | `s3://a/team/` | `read` `always`; `put_objects`, `delete_objects` `on_request` | reads run; writes may be asked for |
| ask | `s3://a/team/` | `put_objects = true`, `delete_objects = true` | writes run there after the gate's `y` |
| ceiling | `s3://b/reports/` | `read` `always` | read-only exception: no write line exists, so a write there falls to the broader write rule or is refused |

The spelling of these lines in TOML and the `yolo allow` / `yolo request` verbs is plan work. The behavior above is the contract.

## 4. How one call resolves (pure decision)

The decision is a pure function. Inputs: the classification (ordered operations, each with kind, cell, resource chain and description), admitted roots, the effective rules of both layers, working-set membership, live timed entries, and the execution context identity. It reads no credential, no host environment value and no provider. It files nothing.

1. **Admission first.** The classifier refuses unknown grammar. Each chain must end at an approved root (`validateChain`). A refusal here is never requestable, whatever the rules say.
2. **Each operation is decided alone**, by [§3](#3-the-policy-model-proposed).
3. **A timed entry** for the operation's cell on its full chain, in the same execution context, turns `ask` into `run`. It never overrides `refuse`.
4. **The call's decision** is the strictest of its operations: any `refuse` refuses the whole call and files nothing; otherwise any `ask` files **one** request for the whole call; otherwise it runs.
5. **A prediction** (`evaluate`, `config why`) runs steps 1–4 and stops. It never files, spends, dispatches or performs a fact read that is not itself `run`.

### 4.1 Fact reads

Some classifications need a provider fact first (GitHub: is this number a PR; who triggered this run). **Proposed rule:** a fact read is a read of its own resource and is decided by [§3](#3-the-policy-model-proposed) like any other.

- If it is `run`, it is performed as today.
- If it is `ask` or `refuse`, it is **not performed**. The outer operation is decided without the fact, in the strictest way the fact could have led to (shipped precedent: "a failed read asks"). The fact read joins the request as one of its bound reads. Request text contains nothing fetched from the provider.
- After an exact answer, the fact is read during the spend transaction. If the fact then shows a different cell or resource than the request bound, the answer is not spent and the call is refused with the stale-approval exit.

## 5. Approvals: what is bound, what is reused

**Reused unchanged (shipped):** `brokerstore` as the only request store; one-use exact grants (normally 15 minutes); pending life of one hour; three pending requests per jail; the ten-minute cooldown after a deny or expiry; dedup that joins an identical pending request; `FileIfStillAsksWithCoverageContext` and `SpendExactOrFilePreparedContext` for file-or-spend under one source lock; no refund after possible transmission; `yolo approve` as the host-only answer; the refusal of `--add` and `--remember`.

**Proposed extensions:**

| Subject | Shipped | Proposed |
| :--- | :--- | :--- |
| Request record | ordered writes (`CallWrites`) | also ordered reads, when any read asks, and the execution context identity, when not the default |
| Exact digest | canonical argv, repository, stdin digest; generic CLI adds ordered writes and original argv | adds the ordered reads and the context identity **only when present**. A write-only call in the default context digests exactly as today, so stored answers and legacy git and GitHub digests stay valid. |
| Query identity | — | The canonical argv is already in the digest, so an exact answer to a Cost Explorer query covers that query alone. The source must canonicalize the query; the resource ID alone (`ce://host-login/<operation>`) is not the identity. |
| Timed answer | one `call:<cell>` entry per full chain, capped by the smallest touched `max_for`, all-or-none | the same for read cells. A timed read answer covers that cell on that chain in that context for the time: any query, any object body. This is the reusable scope the approver chose by answering `--for`. |
| Revalidation | replays argv and stdin hash through the classifier; checks ordered writes, roots, ceilings | also checks ordered reads, the governing rules of both layers, and the context identity |

A stored answer made before this revision is never read as covering a read or a routed context. New authority is never derived from an old record.

## 6. One lifecycle for every broker (proposed)

The outcome contract below is the same for all four sources. Transport details stay per source.

| Event | Contract |
| :--- | :--- |
| Ask | One durable request, or a join to an identical pending one. Nothing is dispatched. |
| Notification | The user's `on_request` hook runs once per filed request (`brokerhook.Runner`); a join notifies nobody. Hook output is never consent. The Linux doorbell (`approvalnotify`) and `yolo approve --web` are unchanged and gain no authority. |
| In-call wait | The broker may hold the call for a bounded time and continue in place if answered. Git's 30 seconds and GitHub's window are kept. The generic front gains a bounded hold that fits inside its forwarding deadline and leaves the executor its budget. The bound is a plan parameter. |
| Client timeout | The call returns "pending" with the request ID. The request persists. |
| Waiting later | `approvalwait.Wait` and its exit codes are unchanged: allowed 0, used or lapsed 72, pending timeout 75, denied 77, expired 78, closed 79. The wake hint is advice; the retried call is re-authorized at the host. |
| Answer | Once, `--for`, or deny with an optional note. First answer wins. |
| Broker shutdown | Pending requests of that launch close as refused and waiters wake (`Store.End`). Workspace-held grants survive. |
| Publication failure | Reported, never authoritative (`PublishFailed`). |

**Parity work this requires:** the generic daemon must run the real hook runner, report publication failure and close its pending requests at teardown, in its production entry point and not only in a helper test ([SBP-D9](#SBP-D9)).

**Reads and the pending cap.** An agent reading widely under an ask policy reaches three pending requests quickly. That is the intended cost of `"ask"`; a timed answer or a narrower `always` line is the remedy. The cap does not change.

## 7. Execution contexts and routing (proposed)

### 7.1 Model

- An **execution context** is declared in user config only: a name and, for AWS, a host profile name. The shipped behavior is the **default context**: the host environment captured at pin time (`pinCLI`), exactly as today.
- A **route** is declared in user config only: *(source, scope, context name)*. Routes use the source's containment, as rules do.
- Routing is **not permission**. A route grants nothing, admits nothing, and a rule never selects a context.

The workspace cannot write routes or contexts. The classifier never sees a context, a profile or an environment value. The agent cannot name one: profile, region, endpoint, signing and file flags from the jail stay refused, as shipped.

### 7.2 Selection

1. The most specific route whose scope contains the call's resource selects the context. No matching route means the default context.
2. Two routes at the same scope are a config error at load.
3. A call whose operations select different contexts is refused. One call runs in one context.
4. A matched route whose context cannot be built or whose command fails is a failure of that call. It **never falls back** to the default context.

### 7.3 What a routed AWS context runs with

- `AWS_PROFILE` is set to the context's profile. The host's own `AWS_PROFILE` is ignored.
- `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` and `AWS_SESSION_TOKEN` are **removed**. Static credentials in the environment would otherwise win over the profile and select the wrong identity silently.
- The other captured names (region, config and credentials file paths, endpoint overrides) pass through as trusted host configuration.
- The profile name is redacted from output like every other declared variable value, as shipped.

The default context is untouched: there, captured static credentials keep winning, as they do today.

### 7.4 What is approval-bound

The **context identity** is a hash of declared, nonsecret facts only: the source, the context's name, its declared fields (the profile name) and the matched route's scope. The default context has a fixed identity that adds nothing to the digest.

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

**Holder.** A grant is held by the workspace. **Proposed uniform rule:** an exact answer may be spent by a later launch of the same source in the same workspace, after full revalidation, as GitHub does today (`TestExactGrantOutlivesItsBrokerForItsWorkspaceOnly`). Another workspace, source, resource, query or context never reuses it.

**Live changes.**

| Change | When it takes effect |
| :--- | :--- |
| Ceiling line removed or narrowed; route or context edited | Before the next dispatch, with no relaunch. A stored answer is never honored against a ceiling that no longer admits it. |
| Root withdrawn through the gate | Before the next dispatch (`ReplaceScopeContext` under the source lock). |
| Ask narrowed (`"ask"`, `false`) or widened (`true`) | With `yolo config apply` or a fresh launch. A raw workspace edit is not authority. Whether cell values reach a running generic broker without relaunch is unverified ([§13](#13-unverified-claims-this-design-relies-on)); the contract is that narrowing must not need a relaunch to stop a dispatch. |

**Concurrency.** Decide, file, spend and dispatch happen under the one source lock, as shipped (`boundWrite`), now for reads as well. Two retries of one approved call spend it once. Two approvers: the first answer stands. A withdrawal that lands before dispatch blocks it and spends nothing. One that lands after possible transmission changes nothing: no refund, no retry. A multi-operation timed answer commits for all its cells or none.

**Failure.** Any failure to load rules, routes, the launch input or the scope file refuses. Classifier failure refuses. Hook failure is recorded on the request and changes no decision. Publication failure is reported and changes no decision.

## 9. Each source

| Source | Read cells (proposed) | Scope containment (proposed) | Notes |
| :--- | :--- | :--- | :--- |
| `git` | one: fetch with host credentials | exact approved remote URL; no containment below it | Push rules, branch and tag grants and the working set are unchanged. Anonymous public fetch: [OQ-SBP1](#OQ-SBP1). |
| `github` | at least one covering every supported read; finer cells are a later choice | normalized repository identity; `[repos.<name>]` tables are the scope spelling already built | Supersedes [GW-D8](github-write-scopes.md#GW-D8) and the read bound of [GW-D80](github-write-scopes.md#GW-D80). Fact reads follow [§4.1](#41-fact-reads). The unbuilt cell tables of [`github-write-scopes.md`](github-write-scopes.md) stay unbuilt by this design. |
| `aws` | S3: list, get, head. Cost Explorer: its four queries. | S3: bucket, then prefix ending in `/`, longest match (the shipped root rule). Cost Explorer: operation under `ce://host-login`. | Routes apply to S3 scopes. Cost Explorer stays host-login-wide and takes no route in this revision. |
| `circleci` | project run listing | exact project root `gh/{owner}/{repo}` | `rerun_workflows` stays refused by the classifier. A rule naming it is accepted, inert, and reported as "declared, not implemented". |

Git and GitHub keep their native transports, stores and digests. They adopt the model through their own adapters; nothing here rewrites their wire protocols.

## 10. Compatibility

- A configuration with no scoped line and no route behaves exactly as at baseline, for all four sources.
- Broad read grants, branch and tag grants, repository-level GitHub grants, working-set defaults and historical `--add` entries keep their meaning.
- Stored requests, exact answers and timed entries stay readable and valid for what they covered. None gains read or routed coverage.
- The launch input (`yolo.call-broker-launch/2`) must carry read cells and rules; that is a version step. A broker handed an older input refuses reads that need the new fields rather than treating them as standing. Root-bearing format migration stays refused, as documented for AWS.
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
- Config spelling, CLI verb names, file layout and task order (plan work).

## 12. Acceptance scenarios

Each is account-free and uses fake host executables or fixtures. "Refused" means no request is filed and nothing is dispatched.

### 12.1 Floor and compatibility

| # | Given | Then |
| :--- | :--- | :--- |
| A1 | Each source selected, nothing granted | Every credentialed read and write is refused. The anonymous public git fetch still works. |
| A2 | Each source with its existing broad grants, no scoped line | Every decision, digest and exit code equals baseline. |
| A3 | A stored pre-revision exact answer and timed entry | Each still covers what it covered; neither covers a read or a routed call. |
| A4 | An older launch input, a call needing a read cell | Refused with a message naming relaunch. |

### 12.2 Rules

| # | Given | Then |
| :--- | :--- | :--- |
| B1 | Ceiling: source `read` `on_request`; `ce://host-login` `read` `always` | A Cost Explorer query runs; an S3 list asks. |
| B2 | B1 plus `s3://a/team/` `read` `always` | Get under `s3://a/team/` runs; under `s3://a/team-other/` and `s3://a/` asks; listing the parent asks. |
| B3 | `s3://b/reports/` `read` `always`, no write line anywhere | Get runs; put and delete there are refused. |
| B4 | B3 plus source-scope `put_objects` `on_request` | Put under `s3://b/reports/` asks. The read-only line gave it nothing. |
| B5 | Broad read `always`; ask `false` for `read` on one repository or prefix | Reads there are refused; elsewhere they run. |
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
| C6 | A fact read that would ask | No provider call before the answer; the request lists it; its text holds no provider data. |
| C7 | C6 answered; the fact shows another cell | Not spent; stale-approval exit. |
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

## 13. Unverified claims this design relies on

| # | Claim | Status | Needed before build |
| :--- | :--- | :--- | :--- |
| U1 | Generic exact answers are rejected across launches because `boundWrite` revalidates without rebinding the holder | Read as code structure; not reproduced | A synthetic reproduction; then E6 defines the fix |
| U2 | Generic write revalidation checks the `all` table even when `working_set` decided | Read as code structure; no failing scenario shown | A reproduction with a working-set-only ceiling |
| U3 | Cell values in the launch input are frozen, so an applied ask change may need a relaunch | Not traced | Trace `config apply` for cells; [§8](#8-authority-state-concurrency-withdrawal-failure)'s narrowing contract must hold either way |
| U4 | No launcher path calls `Store.End` for the generic broker | Scan of inspected callers only | Trace external launcher cleanup before D3 is called a regression |
| U5 | Generic pinned programs get the native placement refusal | Unknown | Check launch validation |
| U6 | Git's authenticated fetch can be decided per remote URL without transport changes | Assumed from the exact-URL admission | Map the fetch path |
| U7 | Each GitHub fact read can be named as a resource before it is made | Assumed | Map the fact readers |

Test names cited in the packet but not read (`TestSelectedCLITwoWriteAllowOnceBindsFullOrderedBundle`, `TestSelectedCLIReadAndEvaluateRefuseCurrentHostReadWithdrawal`) are pointers, not evidence.

## Open Questions

1. 💬 **OQ-SBP1: Does an explicit read `false` on a git remote also stop the anonymous public fetch?**

   Today a public HTTPS fetch is tried without credentials even when the workspace holds no read grant; that is a compatibility exception and uses no host credential. With scoped reads, a user can now write "never read this repository". The evidence does not show whether an explicit read refusal for git exists today or what it does to the anonymous attempt, so neither answer is provably the shipped one.

   - **A. `false` stops it.** A written refusal means the broker fetches nothing from that remote, with or without credentials. "Never" reads cold. Cost: one exception to the exception, and a behavior change if an explicit git read refusal exists today and lets anonymous fetch through.
   - **B. `false` stops only credentialed reads.** The anonymous attempt stays outside policy everywhere, since it carries no authority. Cost: a line that says "never read" and still lets the broker fetch.

   With no line written, the anonymous fetch is unchanged under either option.

   <!-- vantage: question id=OQ-SBP1 leaning="A. false is the only way to say never (OQ-GW8); a refusal that still fetches is a surprise." -->

   _Leaning:_ **A.** `false` is "the only way to say never" ([OQ-GW8](github-write-scopes.md#OQ-GW8)); a refusal that still fetches is a surprise.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

Dates are the date each item was recorded in this document. Owner rulings are restated from the task brief and the evidence packet, not quoted.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="SBP-D1"></a>SBP-D1 | **Owner's ruling:** unconfigured access stays refused; selecting a pack grants no execution authority | 2026-11-17 | [§2.1](#21-the-four-operation-brokers), [§3.3](#33-how-the-two-layers-combine) | shipped floor, kept |
| <a id="SBP-D2"></a>SBP-D2 | **Owner's ruling:** every applicable broker uses the same allow / ask / deny, standing permission, scoped read and write, notification, bounded wait, timeout and persistent-request lifecycle. It is a shared broker contract, not an AWS mechanism; resource types differ per source | 2026-11-17 | [§3](#3-the-policy-model-proposed), [§6](#6-one-lifecycle-for-every-broker-proposed) | no |
| <a id="SBP-D3"></a>SBP-D3 | **Owner's ruling:** scoped reads are universal. This supersedes write-only scope expression: [GW-D8](github-write-scopes.md#GW-D8) and the "a scoped ceiling lists write capabilities" bound of [GW-D80](github-write-scopes.md#GW-D80). Existing explicit broad grants and shipped defaults keep their meaning; nothing becomes ask-by-default | 2026-11-17 | [§3.4](#34-existing-broad-grants), [§10](#10-compatibility) | no |
| <a id="SBP-D4"></a>SBP-D4 | **Owner's ruling:** AWS must be able to say Cost Explorer always reads, S3 generally asks, with bucket or prefix exceptions that are read-only or read and write. Reads take fine-grained approvals like writes; any reusable scope is explicit | 2026-11-17 | [§3.5](#35-the-owners-aws-example-as-rules), [§5](#5-approvals-what-is-bound-what-is-reused) | no |
| <a id="SBP-D5"></a>SBP-D5 | **Owner's ruling:** the host routes S3 buckets and prefixes to profiles; the agent never picks a host profile; ordinary supported `aws` commands go through the broker | 2026-11-17 | [§7](#7-execution-contexts-and-routing-proposed) | no |
| <a id="SBP-D6"></a>SBP-D6 | **Owner's ruling:** `aws-auth` stays separate and unchanged; credentials it delivers can bypass the broker where IAM permits, and that is disclosed as its own boundary. No live credential, grant or IAM change is made automatically | 2026-11-17 | [§7.5](#75-aws-auth-is-a-separate-boundary) | shipped, kept |
| <a id="SBP-D7"></a>SBP-D7 | The most specific scope governs within a layer; a cell line beats `read` at the same scope; an exact duplicate is a load error; the stricter layer wins. Applies [OQ-GW7](github-write-scopes.md#OQ-GW7) to resources | 2026-11-17 | [§3.2](#32-which-rule-governs) | no |
| <a id="SBP-D8"></a>SBP-D8 | Read and write never imply each other; a read-only exception is a read line with no write line. Applies [GW-D38](github-write-scopes.md#GW-D38) | 2026-11-17 | [§3.1](#31-rules) | no |
| <a id="SBP-D9"></a>SBP-D9 | The shipped store, digest, wait, hook and notification machinery is the only approval flow. The generic daemon is brought to parity in its production entry point | 2026-11-17 | [§5](#5-approvals-what-is-bound-what-is-reused), [§6](#6-one-lifecycle-for-every-broker-proposed) | no |
| <a id="SBP-D10"></a>SBP-D10 | Reads and the context identity enter the digest only when present, so every existing digest and stored answer is unchanged | 2026-11-17 | [§5](#5-approvals-what-is-bound-what-is-reused) | no |
| <a id="SBP-D11"></a>SBP-D11 | A fact read is decided like any read; one that would ask is not made before the answer and joins the request | 2026-11-17 | [§4.1](#41-fact-reads) | no |
| <a id="SBP-D12"></a>SBP-D12 | The approval-bound context identity is the declared route and context (names, profile name, scope). What the profile resolves to is host configuration, not bound, and no AWS or STS lookup is made. A routed context drops static credential variables and never falls back to the default | 2026-11-17 | [§7.3](#73-what-a-routed-aws-context-runs-with), [§7.4](#74-what-is-approval-bound) | no |
| <a id="SBP-D13"></a>SBP-D13 | An exact answer is held by the workspace and is spendable by a later launch of the same source after revalidation, for every broker | 2026-11-17 | [§8](#8-authority-state-concurrency-withdrawal-failure) | GitHub only; generic pending U1 |
| <a id="SBP-D14"></a>SBP-D14 | Credential, sign-in, log, process, serial, audio and adapter services are not operation brokers and get no per-operation approvals here | 2026-11-17 | [§2.2](#22-services-that-are-not-operation-brokers) | — |
| <a id="SBP-D15"></a>SBP-D15 | A timed answer to a read covers its cell on its full chain in its context for the time, as a timed write answer does; an exact answer covers one canonical query | 2026-11-17 | [§5](#5-approvals-what-is-bound-what-is-reused) | no |
---
title: "Make approvals understandable and broad without creating a second authority"
date: 2026-11-05
status: draft
stage: SKETCH
next: "Investigate notifier and capture logistics, queue/privacy boundaries, and the S3 recipe in parallel"
tags: [design, approvals, notifications, brokers, packs, aws]
summary: "Umbrella for a constellation of approval-experience proposals: a native toast gallery built from authentic captures, an optional browser request queue that acts only through yolo approve, and repeatable fine-grained service mediation proved first on AWS S3, with the complete service wishlist preserved."
vantage:
  status-chip: true
---

# Make approvals understandable and broad without creating a second authority

**Status:** Checked 2026-11-05 against baseline commit `5d369f5f33b3d064ad0ae1e9c267cdca03a7c961`.

This is the spine of a growing constellation of design proposals, not a shipped-system reference or implementation plan. Nothing proposed here is built.

**Thesis.** Keep `brokerstore` as the sole approval authority while improving how people see requests and expanding the services that can be mediated. Coordinate three workstreams—authentic native-notification evidence, an optional CLI-backed browser queue, and a repeatable service-mediation recipe beginning with a bounded AWS S3 vertical—under shared trust and evidence boundaries.

**Needs your ruling:** None. The owner intent in [1.1](#11-settled-owner-intent) is settled; remaining uncertainties are engineering investigations routed to [the proposed siblings](#7-proposed-sibling-map). No service account, credential access, capture run, configuration edit, implementation or publication is authorized by this document.

## 1. Recommendation and scope

Develop three workstreams under one set of rules:

1. **Native notification gallery (W1).** A web page of varied representative approval requests, each shown beside its genuine OS notification capture, used to improve wording, information, appearance and actionability.
2. **Optional browser request queue (W2).** Outstanding requests and past requests and approvals, rich detail before any decision, live updates; shipped separately over generic yolo interfaces and started in development by one command against the real host machinery.
3. **Repeatable service mediation (W3).** One bounded AWS/S3 vertical that proves a recipe (classification table, scope vocabulary, account-free fixture service, test checklist) which later services follow mostly by filling in tables.

**The honest first slice** is three independent deliverables: a gallery baseline that captures the owner's *current* toasts, a read-only queue experiment over `yolo approve --json` and `--watch --json`, and an S3 pack exercised end to end by a local fixture service. A missing Mac capture runner does not block S3 classification work; an unsettled browser trust boundary does not block the gallery.

**Terms used here.** *Storybook* means an example gallery, not the JavaScript Storybook product, a frontend framework or a packaging format. *Request authority* is the host `brokerstore` plus `yolo approve`, the only thing that turns a request into a grant. *Fixture set* is the shared collection of sanitized representative requests with provenance labels. *Native capture* is a screenshot of a real OS notification server rendering a fixture, with environment provenance; anything else is a *preview*. *Live state channel* carries queue updates from host to browser and is distinct from *development asset reload*, which refreshes code. *Mediation* is brokered classify–decide–execute of a service operation as in [pack-brokers.md](../pack-brokers.md); *credential delegation* hands a jail credentials it then uses directly.

### 1.1 Settled owner intent

The rulings below are settled owner intent. Mechanisms in later sections are proposals until substantiated. **Built** refers to the new work requested here, not the existing broker foundation.

| ID | Ruling | Date | Settled in | Built |
| --- | --- | --- | --- | --- |
| AE-R1 | Start with one overarching document that a constellation of approval improvements hangs from. | 2026-11-05 | This document; [7](#7-proposed-sibling-map) | This document |
| AE-R2 | Show varied real approval examples beside authentic Linux/macOS notifications, including Git and CircleCI. HTML replicas are not native evidence; this task owes no captures. | 2026-11-05 | [4](#4-workstream-1-native-notifications-and-the-example-gallery) | No |
| AE-R3 | Optional queue for outstanding and past requests, rich detail and live updates; preferably separately shipped over generic yolo interfaces, with a development command as a valid first slice. Arrival/navigation is unresolved; WebSocket is a suggestion, and live updates differ from asset reload. | 2026-11-05 | [5](#5-workstream-2-an-optional-browser-request-queue) | No |
| AE-R4 | Fine-grained service permissions and wrapping starting with AWS S3, perhaps EC2 and a justified third AWS service, with repeatable permission/classification/scope/testing machinery so many integrations can ship. No broad authorization inferred from product names. | 2026-11-05 | [6](#6-workstream-3-service-mediation-that-scales-beyond-one-adapter) | No |
| AE-R5 | The complete generic service wishlist is preserved in the umbrella, grouped, with gaps and uncertainty explicit. The private dashboard integration is an internal pack never published to the public repository. | 2026-11-05 | [6.3](#63-complete-coverage-inventory) | No |
| AE-R6 | Public documents use generic product names and generalized engineer workflows. This task performs no credential, configuration, host, capture, implementation or publication actions. | 2026-11-05 | [1.2](#12-non-goals-and-privacy) | No |

### 1.2 Non-goals and privacy

This umbrella is not an implementation plan, ticket list, schema or supersession of [boundary-broker.md](../boundary-broker.md), [pack-brokers.md](../pack-brokers.md) or [herdr-approvals.md](../herdr-approvals.md), which remain authorities for their own machinery; changes to their contracts must be made there under a named question.

It does not propose remote or multi-user approval, an approval quorum, automatic consent, a replacement grant store, or any widening of grants. It does not choose operation scope for any listed service, change the existing AWS credential route, or promise that wrapping a CLI confines every use of the credentials behind it.

Public examples use generalized engineering workflows and sanitized data: no employer identity, private endpoints, personal application inventory, credentials, or internal dashboard topology. The private internal dashboard pack stays private code; private packaging is confidentiality and provenance, not proof of an enforcement boundary, because yolo does not enforce its official-pack safety contract on packs it does not ship.

## 2. What already exists, and what is missing

The remedies proposed here were checked against the existing machinery below; none of them needs a second authorization system. File references are where to verify, not proof the prose has not lagged.

| Existing machinery | What it already handles | Gap this constellation addresses |
| --- | --- | --- |
| [`internal/brokerstore/store.go`](../../../internal/brokerstore/store.go): request/grant storage, `Decide`, `SpendExactOrFileContext` | Binding a request to launch, jail, broker lifetime, workspace, source, repository, set, canonical argv and stdin digest (generic pack writes also bind original argv and resource chains); serialized decisions; expiry; grant spend; audit. | Human comprehension and more classified operations, not replacement authority. |
| [`internal/cli/approve.go`](../../../internal/cli/approve.go): `yolo approve --json`, `--watch --json`, decisions | Host-side listing of pending requests, live grants and working sets; decision, denial and revocation with distinct exit codes; the declared seam for a UI built outside yolo. | Optional browser presentation, safe browser access, and a usable historical view. |
| [`internal/cli/approvesources.go`](../../../internal/cli/approvesources.go) | Discovery and revalidation of official Git/GitHub and selected generic callbroker adapters; removed or unknown sources stay visible but deny/revoke-only; incomplete selection fails closed. | New adapter coverage, not a missing generic-source approval mechanism. |
| [`internal/brokerstore/watch.go`](../../../internal/brokerstore/watch.go) | Polling snapshots and diffs (CLI default one second) behind `--watch --json` schema 1. | Reconciliation, explicit freshness, and history beyond observed events. |
| [`internal/paths/paths.go`](../../../internal/paths/paths.go) | Private host broker directory and `audit.jsonl` locations. The workspace `.yolo/approvals/<id>.json` is a jail-editable wake hint, never authority. | Nothing; surfaces must simply never read the hint as a decision source. |
| [boundary-broker.md](../boundary-broker.md) | Approval rules and the existing toast design: freedesktop D-Bus actions on Linux, `terminal-notifier` ≥ 3 and an eventual signed helper on macOS. External `on_request` notifier hooks exist; their output is never consent. | Native presentation *evidence* and the implementation gap; not permission to revisit settled behavior by accident. |
| [herdr-approvals.md](../herdr-approvals.md) | An adjacent, unbuilt pane/toast/picker design over the `yolo approve` CLI. | A browser experience; mock herdr/store fixtures prove command wiring, not native rendering. |
| [pack-brokers.md](../pack-brokers.md) | Core/classifier/executor contracts, resource types/roots/cells, dry-run and access prediction, store/approvals/wait/audit/on_request, and the requirement for local fixture services. The HTTP front slice is partially built. | Efficient, repeatable application of those contracts across many services. |
| [`packs/git`](../../../packs/git/pack.toml), [`packs/github`](../../../packs/github/pack.toml) | Real call brokers with host-held credentials and audited calls. | Representative fixtures for the gallery and queue; extend only from verified gaps. |
| [`packs/circleci`](../../../packs/circleci/pack.toml) with [`internal/circleciclassify/classifier.go`](../../../internal/circleciclassify/classifier.go) | Explicit project-scoped approved-root `run list` only; a fake host CLI integration fixture uses the real classifier. | No write path (rerun, run approve, pipeline create) and no live Cloud/Server auth proof; write toasts are synthetic. |
| [`packs/aws-auth`](../../../packs/aws-auth/pack.toml) | Minting and narrowing AWS SSO credentials before they reach a jail. | A credential broker, not command-by-command approval; S3 mediation is new work and must not silently widen this route. |

### 2.1 The gaps the owner feels

1. **Presentation.** The owner sees functional but poor toasts. Inspected repository paths contain no shipped OS-native actionable renderer or approval screenshot harness, while the existing design describes a renderer and `on_request` can invoke an external notifier. The origin of the notifier behind the owner's observed toasts is unverified; identify and capture that setup rather than dismissing the observation.
2. **Overview.** The current CLI does not provide a comprehensive history view. Its watch stream is not a durable event log: it can miss transient states, pruning emits no event, and working-set changes emit no events. Audit details are adapter-provided, so there is no universal history API to lean on.
3. **Coverage.** The inspected paths include Git and GitHub broker implementations, a narrow CircleCI read-only classifier, and an AWS SSO credential broker. This investigation did not establish the complete set of mediated surfaces or broad wishlist coverage; verify further paths before making total-absence claims. The reusable service recipe remains to be demonstrated.
4. **Stale prose.** Some text in [pack-brokers.md](../pack-brokers.md) says `approve` knows only two sources; current code revalidates selected generic callbroker adapters too. Siblings must not copy that gap.

### 2.2 Authority facts every surface must preserve

- A request binds its operation; a decision cannot independently choose a holder, set or command. Decisions revalidate current policy under the store lock and can narrow, never widen, the filed ceiling.
- Pending requests currently expire after one hour. Exact Allow-once is one use and at most fifteen minutes, bounded further by policy.
- Grants are held by **workspace**, not isolated to the originating jail or session. Every surface must make that reach visible before confirmation.
- Concurrent approvers compete for one transition; the loser sees "already decided". This is a local lock, not a quorum.
- Spend is persisted before dispatch: a failed or crashed call may consume a use with no refund or replay. Approval is not proof of execution or success.
- Duplicate pending filing is deduplicated per jail, lifetime, set and digest. Reattempt is independently authorized by the host broker, never by the workspace hint.
- The CLI's exit codes 0 through 6 map, in order, to success, I/O, usage, already decided, unknown id, policy refusal and no longer confirmable. Codes 3, 5 and 6 are expected outcomes, not errors.

## 3. Architecture: shared authority, separate surfaces

```mermaid
flowchart LR
    subgraph jail["Jail (untrusted)"]
        prod["Wrapped CLI or HTTP front (git, gh, circleci, aws ...)"]
        hint["Workspace wake hint .yolo/approvals/id.json (not authority)"]
    end
    subgraph host["Host request authority (existing)"]
        cls["Pack classifier (canonical form, no credentials)"]
        store["brokerstore: decide, spend, audit"]
        exec["Core executor (classified canonical form)"]
        cli["yolo approve: --json, --watch --json, decide"]
    end
    subgraph notify["Notification surface (existing hook, proposed renderer)"]
        hook["on_request notifier hook"]
        toast["Native toast (D-Bus / terminal-notifier)"]
    end
    subgraph queue["Optional web request queue (proposed, separate service)"]
        q["Queue and history view in browser"]
    end
    subgraph gallery["Gallery (evidence, no authority)"]
        fx["Sanitized fixture set with provenance"]
        shots["Native captures: Linux / macOS"]
    end
    svc["Upstream service authorization (S3, EC2, GitHub ...)"]
    prod --> cls --> store
    store --> exec --> svc
    store --> hint
    store --> hook --> toast
    toast -- "explicit action, via host" --> cli
    cli <--> store
    q -- "snapshot + events" --> cli
    q -- "decisions only via local CLI, after trust boundary" --> cli
    fx --> toast
    fx --> q
    fx -- "test requests" --> prod
    toast -. "captured" .-> shots
```

The diagram expresses ownership, not completeness: the HTTP mediation front and the native renderer keep their current evidence labels (partially built; designed, not shipped).

### 3.1 Five rules that bind every sibling

1. **One authority.** Only `brokerstore` decides, spends and audits. Toasts, the queue and the gallery produce *intent* or *evidence*; a decision reaches the store only through `yolo approve` on the host, revalidated per action.
2. **Jail reachability grants no approval authority.** A jailed client may be able to reach a host listener; the listener must not give it approval capability or credentials. The browser boundary must authenticate the host user for mutation without relying on loopback binding alone. Host approval credentials and sessions must not leak to jailed processes through the workspace, URLs, browser history or untrusted content. Render request text as untrusted data, never execute it or interpolate it into a shell.
3. **No UX path broadens a grant.** Body click never grants; user dismissal is Deny; expiry is not consent; ignored or unknown notifier output is not approval. A decision made anywhere withdraws stale actionable notifications elsewhere. Stale or changed scope refreshes and explains a refusal; it never converts into a broader grant.
4. **Successful transport is not successful authorization.** Every surface honors the CLI exit code and re-reads state; "allowed" is kept distinct from "executed successfully".
5. **Shared fixtures, honest provenance.** One sanitized fixture set feeds the gallery, the queue and service tests, and every artifact says whether it is a currently supported real request or a synthetic future-service example, a native capture or a preview, a missing capture or a tested pass.

### 3.2 Shared seams without a new prerequisite schema

Reuse current request IDs, host-derived operation detail, source capabilities and CLI outcomes first. No universal request schema may block the first slice. The surfaces need consistent answers to four questions: what operation, resources and workspace this affects; what each available action authorizes, for how long, and for whom; whether the request is current and confirmable and what changed since display; and what is known about the decision and subsequent execution and what is not. Normalization must not erase Git refs, object versions, multi-resource writes or differences between source grant semantics.

| Seam | Used by | Owner |
| --- | --- | --- |
| Fixture set with provenance labels | W1, W2, W3 tests | Gallery sibling defines format; service siblings contribute entries |
| `yolo approve --json` and `--watch --json` schema 1 | W2, herdr design | Existing CLI; changes need a named question in [boundary-broker.md](../boundary-broker.md) |
| Notification withdrawal on decision elsewhere | W1, W2 | [boundary-broker.md](../boundary-broker.md) (existing) |
| Classification table and scope vocabulary | W3 and every later service | Service mediation machinery sibling |
| Local fixture services without external accounts | W3, CircleCI, future services | [pack-brokers.md](../pack-brokers.md) requirement (existing) |

## 4. Workstream 1: native notifications and the example gallery

**Reader question.** What do approval requests actually look like as OS notifications, and what should change so they look good, say enough, and can be acted on safely?

### 4.1 Evidence first, not an HTML imitation

The gallery is a static page generated from the fixture set. Each entry shows the canonical request detail, the notification text and actions the design would send, and a native capture per platform where one exists. Every entry states whether it is a representative request for a currently supported operation or a synthetic future-service scenario; an actual OS capture, a preview, or a missing capture (shown as missing, never as a green placeholder); and a visual observation only or an action/lifecycle behavior that was also tested. Sanitized replay of a supported request is not evidence that a live account was used. An HTML replica may help iterate layout but never stands in for an OS screenshot.

**Baseline before cosmetics.** The first investigation identifies the renderer and `on_request` configuration behind the toasts the owner sees today and captures *those* into the gallery, so improvement is measured against reality and the failure is located: wording, truncation, OS presentation, action discovery, or notifier lifecycle.

### 4.2 Native constraints are part of the design

Preserve the Linux and macOS notification behavior settled in [boundary-broker.md](../boundary-broker.md) unless a focused sibling explicitly reopens a rule under a named question.

Linux needs the launch's own `DBUS_SESSION_BUS_ADDRESS`, a chosen notification daemon and a resident notifier connection; there is no fallback to an unattended desktop bus. A headless Wayland/Sway stack is useful only with those components, because Sway alone renders nothing. Button and menu behavior varies by server (GNOME buttons, KDE connection lifetime, dunst/mako menu actions, swaync buttons) and Do Not Disturb may remove actions. That legacy table is source-backed context, not a fresh runtime measurement; the gallery is where new measurements are recorded.

macOS retains `terminal-notifier` ≥ 3 and an eventual signed helper app. Multiple actions may collapse into Options; Alerts style and per-app permission are user settings. Native proof needs a real GUI session, notification authorization and a capture runner; withdrawal and action handling still need real measurement. Modal AppleScript dialogs are not toasts, and a Linux-rendered facsimile never counts as Mac proof.

### 4.3 Representative gallery coverage

| Example family | Required variation | Evidence boundary |
| --- | --- | --- |
| Git | Single and multiple refs, old/new targets, push options, long names, partially covered scope. | Supported broker requests supply representative fixtures. |
| GitHub | Resource-specific writes, long content, exact versus broader available actions. | Broker support does not establish native renderer support. |
| CircleCI | Project-scoped `run list` (current); rerun, run-approval and pipeline-creation stories (synthetic, marked). | `run list` is the narrow implemented path; do not fabricate a prompt where policy needs none. |
| S3 and later services | Multiple resources, sensitive paths, destructive-looking operations, changed scope. | Synthetic until the operation is implemented and validated by W3. |
| Cross-cutting states | Denial, expiry, stale action, decision elsewhere, unavailable actions, truncation, sensitive-data redaction. | Screenshots alone do not prove decision or withdrawal behavior. |

Capture provenance, OS and notification-server versions, theme, scaling and redaction rules belong to the gallery and capture siblings, not this umbrella. Success means a reader identifies the operation and consequence without mistaking an abbreviated toast for complete scope; when detail cannot fit, the toast offers a safe route to fuller detail rather than concealing the limit or broadening the action.

## 5. Workstream 2: an optional browser request queue

**Reader question.** Can an engineer see outstanding and past requests with enough detail to decide well, watch them update live, and eventually decide safely, without yolo growing a second authority?

### 5.1 Prove the queue before settling how people arrive

A separately shipped optional service, started in development by one command against the real host machinery. It reads `yolo approve --json` for the snapshot and `yolo approve --watch --json` for events, and reconciles by periodic re-snapshot because the watch stream is lossy by design: reconnects, pruning and working-set changes cannot be reconstructed from events. Rich detail means the full host-derived request: argv, repository, resource chain, set, policy ceiling, source revalidation status, workspace-wide grant reach, duration, use limits and time remaining.

How the engineer reaches the page (toast link, terminal hint, menu item, automatic browser launch) is deliberately unresolved by owner ruling. WebSocket is a reasonable live state channel; server-sent events or polling are acceptable and the sibling chooses against real interaction needs. The live channel is separate from development asset reload, and neither carries authority.

### 5.2 Read-only first; mutation only behind a tested host-user boundary

**Decision (derived, not a new owner ruling).** The first queue slice is read-only: it shows and watches; decisions stay in the terminal or toast until the browser trust boundary sibling has settled and *tested* the boundary. This follows from the existing contract in [`internal/cli/approve.go`](../../../internal/cli/approve.go), which says a UI built outside yolo must never accept approvals from the jails it approves because a jail can reach a host port, and from the owner's instruction to prove the experience first. The alternative, mutation in the first slice behind a boundary proven at the same time, is recorded in [9](#9-alternatives-costs-and-risks); it delays the first slice rather than lowering the bar.

Before the queue offers Allow or Deny, even in development, the trust boundary sibling must settle and demonstrate by test, not argument:

- host-user authentication and a trusted bootstrap that gives jailed processes no approval capability;
- origin validation, cross-site request protections, and authentication of the chosen live channel;
- credential storage and lifecycle outside jailed workspaces, URLs, history and browser-delivered content;
- safe rendering of request text, with no shell execution or active content derived from it;
- per-action request binding, source-aware available actions, stale-state handling, and faithful CLI exit-code interpretation.

Read-only access also needs a privacy boundary: "no approve button" does not make request details public to anything that can reach the listener. A fixture-only browser demo can proceed while host-connected privacy is unresolved; the history and privacy sibling settles what a host-connected read-only view may expose. The sidecar invokes the CLI with structured arguments, never exposes raw store paths, and never invents a remote approval protocol for convenience.

### 5.3 Honest history and decision outcomes

The current JSON listing contains pending requests, live grants and live working sets, not comprehensive past requests. History is assembled from what the host exposes (adapter-provided audit and snapshot data) and from the queue's own observed record, and the view states which. An initial "observed since this service started" history is useful if plainly labeled with restart and disconnection gaps; it is not completion of the owner's history requirement. The history and privacy sibling chooses how a host-side projection combines available evidence, what retention applies, which generic interface extensions are necessary, and what is redacted.

Never infer denial, expiry or execution from disappearance. Keep past requests and approvals distinct from currently usable grants. Display spend-before-dispatch honestly: a queue-driven Allow followed by a crashed call has consumed its use, and the queue must not offer a retry that silently re-approves.

## 6. Workstream 3: service mediation that scales beyond one adapter

**Reader question.** What does it take to add one fine-grained mediated service, and can the same path be walked again cheaply for the next ten?

### 6.1 Reuse the pack contract; produce a recipe

[pack-brokers.md](../pack-brokers.md) already assigns the core the decision and execution authority: a pack declares resource types, roots and cells (operation permission categories); the classifier has no credentials and produces a canonical form; unknown calls, flags and routes are denied; read-only lookups are audited through the host; the core executes only the classified form. Upstream service authorization is separate and varies in how far credentials can be narrowed. Build beside none of this. The repeatable machinery this workstream must produce is the *recipe*:

1. Inventory operations and fronts, including alternate flags, endpoints, SDK access, pagination and compound effects.
2. Define resource types, roots and cells with explicit read/write and multi-resource treatment, as a classification table with a shared scope vocabulary.
3. Define canonicalization, scope prediction, denied unknowns, and executor/credential requirements.
4. Exercise the full local broker path (front, transport, classifier, approval, executor) with a fixture service and no external account, including approval, refusal, changed resources and replay attempts.
5. Record live API/auth compatibility separately from fixture proof, and both separately from native presentation evidence.

CLI interception is not comprehensive confinement if a jailed process holds credentials that bypass the wrapper; every adapter documents both the broker policy and the upstream authorization constraint. Confirm the current state of the HTTP front slice before scheduling any HTTP-fronted service. Private and third-party packs may reuse the framework, but yolo does not enforce its official-pack safety contract on them.

### 6.2 AWS: S3 first, EC2 selectively, a third service only for a reason

[`packs/aws-auth`](../../../packs/aws-auth/pack.toml) mints and narrows SSO credentials; it is not a command-by-command approval broker, and this proposal does not silently change its route. Credential delegation and mediated operations are different trust models, and the AWS sibling must say which operations use which.

**S3 first** because object storage has natural resource roots (bucket, prefix, object version), and choosing a deliberately bounded initial operation set can prove the classification and execution recipe. A pack executor for object-store signing is already contemplated. The sibling chooses that set; exact verbs, bucket/key/version identity, multi-object effects, CLI expansion, endpoint/account/region selection, signing location (host executor versus narrowed SSO route) and direct-access bypass are its investigations. S3 as a product name grants nothing.

**EC2 selectively**: only operations a generalized workflow needs, each classified individually, with secondary effects represented honestly. A tag or instance ID is not proof that every related operation is bounded. No "all AWS": each service is its own classification table.

**Third service**: CloudWatch Logs is a candidate because bounded, query-shaped diagnostic retrieval may exercise a recipe shape (query text, time ranges, sensitive reads) that S3 and EC2 leave untested. SQS/SNS (a potentially bounded consequential write set) and STS/IAM read-only (identity inspection overlapping `aws-auth`) are alternatives. The AWS sibling may recommend one after comparing it with gaps exposed by the S3 recipe; that research is not an owner selection.

### 6.3 Complete coverage inventory

Listing is not support, chosen scope, or authorization to use any account. **Current** summarizes the paths inspected for this proposal, not an exhaustive product-wide survey; “Not found in inspected paths” is not proof of absence elsewhere.

| Group | Service | Current | Notes and open gaps |
| --- | --- | --- | --- |
| Source control and CI | Git | Yes (real broker) | Example service for the gallery |
| | GitHub | Yes (real broker) | Representative write fixtures; native presentation unproven |
| | GitLab, including privately hosted deployments | Not found in inspected paths | Deployment identity, base URL and token model are classification questions, not a product name |
| | CircleCI | Narrow (project-scoped `run list`) | Writes (rerun, run approve, pipeline create) are synthetic gallery stories only; no live Cloud/Server auth proof |
| AWS | S3 | No per-operation mediation found in inspected paths (SSO credential broker only) | First vertical, [6.2](#62-aws-s3-first-ec2-selectively-a-third-service-only-for-a-reason) |
| | EC2 | Not found in inspected paths | Selective operations only |
| | Third AWS service | Not found in inspected paths | CloudWatch Logs candidate; AWS sibling recommends after research |
| Observability and incident response | Grafana | Not found in inspected paths | Dashboards and alert rules; separate viewing/querying from configuration |
| | Prometheus, Thanos | Not found in inspected paths | Investigate whether mediating Grafana suffices or the underlying query layer needs its own classification |
| | Sentry | Not found in inspected paths | Issue triage and resolve are writes |
| | Opsgenie | Not found in inspected paths | Paging and acknowledgement are high-consequence writes |
| Collaboration and knowledge | Slack | Not found in inspected paths | Posting as the user is a write with audience scope |
| | Jira | Not found in inspected paths | Issue create/transition writes; project as root |
| | Confluence | Not found in inspected paths | Page writes with space as root |
| | Document360 | Not found in inspected paths | Similar shape to Confluence |
| | Google Workspace, including Drive and Calendar | Not found in inspected paths | OAuth scope narrowing differs sharply per API; Calendar writes affect other people; sharing effects need care |
| Design, meetings and customer workflows | Figma | Not found in inspected paths | File read versus comment/write |
| | Miro | Not found in inspected paths | Board writes |
| | Zoom | Not found in inspected paths | Meeting create/join; likely low priority |
| | Gong | Not found in inspected paths | Mostly read; data sensitivity high |
| Data | Snowflake | Not found in inspected paths | Query classification is hard; do not assume query syntax means read-only; roles may be the realistic cell |
| Identity and uncertain need | Okta | Not found in inspected paths | Relevance uncertain; may be an authentication dependency rather than a mediated service |
| | Claude Teams | Not found in inspected paths | Probably no integration needed; validate the need before proposing an adapter |
| Private | Private internal dashboard pack | Not found in inspected paths | Internal pack, never in the public repository; no topology, URLs or board details here; inherits no official-pack enforcement |

**Prioritization rationale.** S3 first for the reasons above; EC2 second and selective because its operations are consequential and few are needed; no arbitrary "all AWS". After AWS, order by how many *recipe shapes* a service adds (REST with per-resource roots, query languages, chat posting, OAuth scope narrowing) and by a concrete generalized workflow, not by wishlist order; the mediation machinery sibling proposes that order. Defer speculative breadth rather than treating the wishlist as twenty independent commitments.

## 7. Proposed sibling map

A linked label is a sibling that exists (draft); an unlinked one is still a proposed name; each sibling owns one reader question and states acceptance outcomes. A sibling that extends existing machinery links the authority it extends.

| Proposed sibling | Reader question it owns | Must settle before dependent implementation |
| --- | --- | --- |
| [Native notification gallery](native-notification-gallery.md) (draft; fixtures in [`internal/approvalfixtures/fixtures/`](../../../internal/approvalfixtures/fixtures/), page at [`gallery/index.html`](gallery/index.html), served by `just dev-storybook`) | What do requests look like natively, and what should change? | Fixture format and provenance labels; the owner's current renderer identity and baseline capture |
| [Linux notification capture](linux-notification-capture.md) (draft) | How is a headless session bus plus daemon stack built and captured reproducibly? | Daemon matrix, screen metadata, theme and scaling provenance; capture outputs use the gallery fixture format |
| [Mac capture runner](mac-capture-runner.md) (draft) | How are macOS notifications rendered, authorized and captured, and how do action collapse and withdrawal behave? | GUI session logistics; `terminal-notifier` measurements; capture outputs use the gallery fixture format |
| Web request queue experiment | Can one command show a live, detailed, read-only queue against the real store? | Reconcile strategy for the lossy watch stream; live channel choice |
| Browser trust boundary | How does the queue prove a decision came from the host user and never from a jail or untrusted content? | Investigate now; tested boundary required before any mutation path |
| Request history and privacy | What past-request data exists, what is retained, redacted or shown, including in read-only views? | Investigate now; audit fields per adapter, redaction and retention rules, completeness claims |
| Service mediation machinery | What is the repeatable recipe: classification table, scope vocabulary, fixture service, test checklist? | Format and checklist before the second service starts; HTTP front slice status |
| AWS S3 vertical | Which S3 operations, roots and cells ship first, executed and signed how? | Credential model relative to `aws-auth`; bypass analysis; negative scope tests |
| AWS EC2 and third service | Which further AWS operations are justified, and what does the third service teach the recipe? | Follows the S3 recipe |
| Service adapters (one per scheduled service) | What does this service's classification table look like? | Follows the machinery sibling |
| Private internal dashboard pack (internal repository) | How does the private pack use the public extension contract, and what boundary does it actually have? | Lives outside the public repository entirely |

The umbrella owns intent, boundaries, inventory and ordering. Routing of this document and its siblings into delivery order happens through [the roadmap](../../plans/roadmap.md).

## 8. Dependencies and order

1. **Start in parallel:** investigate the current notifier and gallery evidence; develop the fixture format and queue experiment; begin host privacy/security and browser-boundary investigations; begin Linux/macOS capture logistics; and develop the mediation recipe alongside the S3 vertical.
2. **Fixture-dependent outputs:** native capture production and gallery comparisons consume the shared fixture format once defined. Research into capture environments can proceed now.
3. **Queue and trust:** queue experience, host privacy/history, and browser trust investigations can proceed in parallel. Mutation remains gated on a tested host-user boundary; comprehensive history claims remain gated on provenance and privacy findings.
4. **After the S3 recipe is walked once:** use its observed gaps to inform a nonbinding AWS third-service recommendation and EC2 scope; schedule other adapters in recipe-shape order.
5. **Any time, outside the public repository:** the private internal dashboard pack, once the extension contract it relies on is confirmed stable.

Settle before claiming: the browser trust boundary before host-connected mutation; history provenance and gaps before claiming complete history; credential bypass and selected fronts before claiming AWS confinement; actual platform captures before calling presentation native. The existing CLI JSON seam and pack framework are enough to start; no invented schema gates the first slice.

## 9. Alternatives, costs and risks

| Alternative | Benefit | Why not preferred |
| --- | --- | --- |
| Build a native renderer into yolo now instead of a gallery | Visible improvement sooner | Designed blind without captures; [boundary-broker.md](../boundary-broker.md) already specifies the renderer and the gallery produces the evidence it needs |
| Put the browser UI inside core yolo, or give it its own store or HTTP decision API | Fewer components; simpler prototype | A second authority, and it reopens the jail-reachability problem the CLI help warns about; the CLI is the intended seam, as herdr also assumes |
| Mutation in the first queue slice behind a boundary proven at the same time | One slice instead of two | Delays the first slice until the trust sibling settles; visibility and detail are what the owner asked to prove first |
| Treat watch events as the history database | Simple | Missed transitions, pruning and absent working-set events make completeness claims false |
| Use only screenshots or HTML previews | Cheap visual iteration | Neither proves safe actions; HTML does not prove native rendering; keep them as labeled partial evidence |
| One bespoke broker per service | Fast local specialization | Repeats approval and lifecycle machinery the pack framework already owns |
| Wrap `aws` while preserving unrestricted delegated credentials | Convenient command UX | Delegation bypasses the wrapper; cannot be represented as mediation |
| Use the Storybook product | Familiar tooling | A static page from fixtures is cheaper and avoids a front-end toolchain for evidence |
| A remote or multi-user approval protocol or quorum | Shared approvals | Not proposed; none exists and the owner has not asked; recorded so it is not introduced silently |

**Costs.** Mac capture needs real hardware or a hosted Mac runner with GUI authorization. Headless Linux capture needs a maintained daemon matrix. Every service adapter needs a fixture service, the price of testing without accounts. The lossy watch stream means the queue carries reconciliation logic. Browser security review and history privacy analysis are real work before any mutation or completeness claim.

**Risks and mitigations.**

- A UX surface accidentally broadens a grant (body click, timeout, misread output): rule 3 in [3.1](#31-five-rules-that-bind-every-sibling) and gallery fixtures for denial, expiry and stale states.
- The queue ships mutation before the boundary is settled: read-only default and a tested boundary as an explicit prerequisite.
- Previews are mistaken for native evidence, or missing captures for passes: mandatory provenance labels and "missing" rather than placeholders.
- Spend-before-dispatch is misread as a retry opportunity: the queue displays consumed uses honestly.
- Stale prose about source counts could be copied into siblings: state coverage only to the limits of the inspected evidence, as in [2.1](#21-the-gaps-the-owner-feels).
- The private pack is assumed to inherit official-pack enforcement: it does not, and its internal document must say so.
- "AWS support" is read as confinement: every adapter records both broker policy and upstream credential reach.

## 10. Observable success and next investigations

| ID | Observable result | Owner |
| --- | --- | --- |
| AE-E1 | The owner opens the gallery and sees their real current toast beside the proposed one on both platforms, every Git and CircleCI example carrying truthful support and capture labels, with a written list of presentation changes justified by captures. | Native notification gallery; capture siblings |
| AE-E2 | One command starts the queue against a real host store with fixture requests filed; the page shows pending requests with full detail and live updates; reconnect restores current state; historical coverage and gaps are visible rather than guessed. | Web request queue experiment; request history and privacy |
| AE-E3 | Browser and CLI races produce one authoritative decision; stale actions cannot widen scope; a jail cannot approve through the service, shown by test. | Browser trust boundary |
| AE-E4 | A selected S3 operation traverses real local mediation, classification, approval and execution fixtures without external accounts, with the classifier denying everything not in its table. | AWS S3 vertical |
| AE-E5 | A second service adapter is written mostly by filling in a table, while its unknown operations, credentials and unsupported paths stay explicitly bounded. | Service mediation machinery |
| AE-E6 | Public artifacts contain sanitized generic examples; private pack code and topology remain outside the repository. | Every sibling |

**Next investigations (agent work, routed to siblings):**

- Identify the renderer and `on_request` configuration producing the owner's current toasts, and capture them as the baseline (gallery).
- Measure `terminal-notifier` action collapse and withdrawal on real macOS (Mac capture runner).
- Enumerate which audit and snapshot fields each existing adapter exposes, to bound what history the queue can show and what a read-only view may expose (history and privacy).
- Design and test the host-user boundary for a browser decision path (browser trust boundary).
- Decide whether S3 execution signs on the host executor or narrows the existing SSO route, and document the bypass risk either way (S3 vertical).
- Confirm the current state of the HTTP front slice before any HTTP-fronted service is scheduled (mediation machinery).
- Recommend the third AWS service from measured recipe gaps after S3 (AWS EC2 and third service).

No screenshots, runtime security results, live API compatibility or implementation are claimed by this design. The unresolved items above are engineering work, not owner questions to manufacture now.
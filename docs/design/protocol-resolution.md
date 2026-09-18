---
title: "Who says which wire — protocol declaration, and the bridge as a resolution step"
date: 2026-09-18
status: in-review
tags: [design, providers, packs, wire-bridge, protocols, resolution]
summary: "Every party in a launch declares where it lives except the agent, so nothing can tell a provider claude can use from one it cannot — and each derive patches the gap privately, one of them by sending a credential to an address it never emitted. Let the agent declare its wire and the bridge declare what it adapts, and the pairing becomes a resolution with three named outcomes."
vantage:
  status-chip: true
---

# Who says which wire — protocol declaration, and the bridge as a resolution step

**Status:** DESIGN, 2026-09-18. Nothing built. Four decisions were ruled in review the same
day ([§12](#12-decision-ledger)); every current-behaviour claim below was read at `220fffb6`.

> **In short.** A provider declares which wire protocol it speaks and where. An adapter
> declares which wire it turns into which other one. The agent declares nothing — so the
> pairing is settled privately inside each agent's own derive, and yolo cannot tell a
> provider that agent can use from one it cannot.

**Why it matters.** The gap is not theoretical in either direction. A provider declaring
`endpoints.openai` and a key, with no anthropic endpoint, hands claude a credential and **no
address** — the key goes to `api.anthropic.com`
([`cerebras-pack-and-copilot-delivery.md`](cerebras-pack-and-copilot-delivery.md#oq-2)). And the
bridged setups that *do* work only work because a pack author hand-wrote yolo's internal
loopback port into the provider's manifest, which a user declaring their own provider cannot
reasonably be expected to do.

**The shape.** Three declarations and one resolver. The agent pack declares the protocols its
program speaks; the provider declares its endpoints by protocol (this exists); an adapter pack
declares the protocol pair it converts and the address it serves at. The resolver runs once, at
selection, and has exactly three outcomes: direct, adapted, or a refusal that names what is
missing.

**Cost.** The single-protocol `base_url` shorthand is deleted with no transition
([§5](#5-the-single-protocol-shorthand-is-deleted)). The bridge's listen port moves out of
provider manifests, inverting a deliberate single-writer choice
([§6](#6-the-adapter-owns-its-address)). One new vocabulary item, whose shape is the open
question.

**Start at [§3](#3-one-resolution)** — everything else falls out of it.

**Needs your ruling:** [OQ-PR1](#OQ-PR1), [OQ-PR2](#OQ-PR2), [OQ-PR3](#OQ-PR3).

**Reads with:** [`wire-bridge.md`](../reference/wire-bridge.md) (the adapter as built, and
`needs`), [`providers.md`](../reference/providers.md) ([`OQ-CS8`](../reference/providers.md#why-its-this-way)
put the binding in each agent's derive; [`OQ-PT2`](../reference/providers.md#why-its-this-way)
refused the ambiguous shorthand override),
[`cerebras-pack-and-copilot-delivery.md`](cerebras-pack-and-copilot-delivery.md#oq-2) (the defect
this removes structurally),
[`protocol-resolution-plan.md`](protocol-resolution-plan.md) (the implementation sketch —
incomplete, and unstable while these questions are open).

---

## 1. The verdict, and the principles it rests on

**Build the declaration, then the resolver, then delete the shorthand.** The declaration is
inert on its own and can land without changing a single launch; the resolver is what makes it
mean something; the shorthand cannot be deleted until the explicit spelling it is replaced by
resolves to the same behaviour.

- **P1. A protocol pairing is resolved, never inferred.** Two parties that declare what they
  speak either match, match through a declared adapter, or do not match. A third answer —
  compose something and hope — is what produces a credential with no address.
- **P2. Core resolves ADDRESSES; a derive composes VARIABLES.** This is
  [`OQ-CS8`](../reference/providers.md#why-its-this-way)'s line and the design stays on its
  correct side: core never learns that claude reads `ANTHROPIC_BASE_URL`. It learns that claude
  speaks `anthropic`, which the claude pack tells it, and hands back an endpoint. Which variable
  that lands in stays the agent pack's business.
- **P3. A declaration states a fact about its own declarer.** A provider says where it lives; an
  adapter says what it converts and where it listens. The current shape breaks this — the
  adapter's port is a fact stated by its consumers ([§2.2](#22-how-a-bridged-provider-works-today)).
- **P4. A credential travels with the address it was minted for, or not at all.** Neither half
  alone is a usable configuration, and one half alone is a leak.
- **P5. The happy path takes no yolo-specific knowledge.** Declaring a provider means naming the
  service's real URL, its protocol and where its key lives. A user who must type an internal
  loopback port has been handed an implementation detail as a configuration step.

---

## 2. What exists today

### 2.1 Three parties, and only two of them declare

| Party | Declares | Where |
| :--- | :--- | :--- |
| Provider | `endpoints.<protocol>.{base_url, wire_api}`, `api_key_env_name`, `region`, `models`, `options` | `packdecl`'s provider contribution, or a user `providers` entry |
| Adapter (`wire-bridge`) | that it is a `kind: "service"` pack, joined to a launch by any selected pack whose `needs` names it | its manifest |
| **Agent** | **nothing about wire protocols** | — |

The adapter declares that it *exists*, not what it *converts*. The agent declares nothing, so
every agent's derive carries the knowledge privately: claude's prefers
`endpoints.anthropic`, pi's prefers `endpoints.openai`, and neither can be asked.

### 2.2 How a bridged provider works today

`packs/cerebras` declares two endpoints — its real upstream
(`https://api.cerebras.ai/v1`, `wire_api: openai-chat-completions`) and
**`endpoints.anthropic: http://127.0.0.1:8214`**, which is not a Cerebras address at all. It is
the loopback the bridge listens on. [`packs/wire-bridge/README.md`](../../packs/wire-bridge/README.md)
states the arrangement plainly:

> The listen port lives ONLY in the provider's manifest URL (`8214`, clear of every baked
> service) — one writer, no second knob. […] the bridge is why the URL cerebras declares is true.

So a provider manifest asserts a fact that yolo then orchestrates a listener to make true. It
works, and for a pack author it is coherent: one writer, and the bridge reads the URL to learn
where to listen. `packs/claude` `needs` `wire-bridge` unconditionally, so for a claude user the
adapter is already joined to every launch.

### 2.3 What that costs, measured

**1. A user-declared provider cannot be bridged at all.** The only spelling that routes an
OpenAI-speaking service to claude is `endpoints.anthropic: http://127.0.0.1:8214` — yolo's
internal port, in the user's own config. Nothing documents it as a user-facing value, and
[P5](#1-the-verdict-and-the-principles-it-rests-on) says it should not be one.

**2. The adapter's address is spread across its consumers.** `8214` is a literal in
`packs/cerebras/pack.json`; `8215` is `internal/wirebridged`'s `CodexResponsesListenAddr`
constant, hand-copied into `packs/claude/derive.lua`'s openai-codex branch. Making either
configurable today means every consumer tracking one value.

**3. The same user input means different protocols to different agents.** `packs/pi`'s derive
comment is explicit — *"The single-protocol `base_url` shorthand wins; otherwise the openai
endpoint"* — so a bare `base_url` is pi's **openai** URL. `packs/claude`'s derive takes the same
field as its **anthropic** base URL (added 2026-09-16, `af71aa3c`). `packs/opencode` and
`packs/omp` each prefer it too. One line of user config, two contradictory readings, decided by
whichever agent happens to consume it.

**4. A credential is emitted without an address.** In claude's derive, `if p.api_key then
ANTHROPIC_AUTH_TOKEN = p.api_key` fires whether or not a base URL was composed, so a provider
that names `openai` and nothing else sends its key to `api.anthropic.com`. The inverse case is
already handled — `elseif routed` substitutes a dummy token so a routed launch is never keyless
— which makes this the missing half of a rule the file already keeps.

---

## 3. One resolution

The resolver runs **once per launch, at selection**, before any derive composes anything. Its
inputs are three declarations; its output is an endpoint, or a refusal.

```
agent speaks P                      (agent pack declaration — new)
provider offers {Q…}                (endpoints, as today)
adapters: (from, to, address)       (adapter pack declaration — new)

1. P ∈ {Q…}                         → DIRECT: the provider's own P endpoint.
2. ∃ adapter (Q → P), Q ∈ {Q…},
   its pack SELECTED                → ADAPTED: the adapter's address, disclosed.
3. ∃ adapter (Q → P), Q ∈ {Q…},
   its pack NOT selected            → REFUSE, naming the pack to add.
4. otherwise                        → REFUSE, naming both protocols.
```

**Outcome 2 is the whole point.** It is what a pack author writes by hand today, computed
instead — and it is what makes a user-declared provider work with no yolo-specific knowledge.
The upstream the adapter dials is the provider's own `Q` endpoint, which is exactly how the
bridge already selects its upstream at boot.

**Outcomes 3 and 4 are refusals, not warnings.** The key's whole content is "this configuration
does not work"; a launch that says so and proceeds has answered a declaration with a note.
Outcome 3's message names the remedy, which is the discoverable half: *"cerebras speaks openai;
claude speaks anthropic. Add `wire-bridge` to `packs` and this pairing resolves."*

**The resolver never selects a pack on the user's behalf** ([OQ-PR3](#OQ-PR3) is whether that
holds). Joining a pack changes what runs in the jail, and inferring that from a provider choice
is a trust decision made on the user's behalf.

---

## 4. Behaviour, stated exhaustively

### 4.1 Degenerate inputs

| Input | Resolved as |
| :--- | :--- |
| Provider declares NO endpoints, carries a key | **Direct, against the agent's first-party API.** Nothing was repointed; this is the BYO-key launch, and it stays legal ([OQ-PR2](#OQ-PR2) confirms the shape) |
| Provider declares no endpoints and no key | Direct; the agent uses whatever credential it already holds (a subscription OAuth, say) |
| Provider declares two protocols, one of which the agent speaks | Direct on that one. An adapter is never preferred over a native endpoint |
| Two adapters offer the same `Q → P` | A launch error naming both packs. Adapter pairs are sole-owned, like provider names |
| An adapter's own pack is selected but its service cannot start | The launch already refuses on an enabled jail-facing service the jail cannot use; unchanged here |
| Agent declares more than one protocol | Resolve against each in declaration order; the first that resolves wins, and the disclosure names which |
| Agent pack declares no protocols | Direct, always — an agent that states nothing constrains nothing. This is the compatibility shape for a pack that has not been updated |

### 4.2 Failure paths

- **Refusal (outcomes 3 and 4)** exits non-zero before any container starts, like the capability
  gate and the notch gate it sits beside.
- **A provider whose `Q` endpoint is unreachable** is not this resolver's business: it resolves
  declarations, never dials. The failure surfaces where it already does, at the first request.
- **An adapter that declares an address already in use** fails when its service binds, naming the
  port and the setting that moves it ([§6](#6-the-adapter-owns-its-address)).

### 4.3 Ordering, and who writes what

Resolution happens **before** the derives run and its result is an input to them, so a derive
can no longer reach a state where it has a key and no address. One writer for each fact: the
provider writes upstreams, the adapter writes its own address, the agent writes its protocol
list, and **core writes nothing** — it only selects among what was declared.

### 4.4 Forbidden

- No derive may compose a credential when the resolver produced no address
  ([P4](#1-the-verdict-and-the-principles-it-rests-on)).
- Core may not name a protocol. `anthropic` and `openai` appear in declarations and in
  diagnostics quoting them, never in a core switch.
- The resolver may not select, install or enable a pack.

### 4.5 What done looks like

- A user declares `endpoints.openai` + `api_key_env` for their own service, runs claude, and it
  works with no further configuration and no yolo-internal value in their config.
- The same config with `wire-bridge` absent from `packs` refuses, names the pack, and exits
  non-zero.
- `grep -r 8214 packs/` returns nothing.
- A provider that names `openai` and a key never results in a request to `api.anthropic.com`.

---

## 5. The single-protocol shorthand is deleted

**Ruled in review, with no transition** ([§12](#12-decision-ledger)). A bare
`providers.<name>.base_url` becomes a validation refusal naming the explicit spelling, the way
the retired `journal` and `host_processes` top-level keys already do.

The evidence, all of it from [§2.3](#23-what-that-costs-measured): the same value means `openai`
to pi and `anthropic` to claude; its headline use case is a trap, because llama.cpp, ollama and
vLLM all speak OpenAI, so `claude` plus a bare URL points `ANTHROPIC_BASE_URL` at an OpenAI
server and fails at the first request; and a URL with no protocol is precisely the input the
resolver in [§3](#3-one-resolution) cannot reason about.

Deleting it **finishes** [`OQ-PT2`](../reference/providers.md#why-its-this-way) rather than
reversing it: that ruling already refuses `base_url` beside `endpoints`, because the shorthand is
the ambiguous spelling once more than one protocol exists.

⚠ **The address stays user-scope-only either way.** `base_url` and
`endpoints.<protocol>.base_url` are already refused in a workspace config, because a file an
agent can edit must not decide where inference goes. Deleting one spelling does not touch that.

---

## 6. The adapter owns its address

The bridge declares the address it serves, and the resolver reads it. That **inverts a
deliberate choice** — [§2.2](#22-how-a-bridged-provider-works-today)'s "one writer, no second
knob" — so the trade is stated rather than assumed: the current single writer is the *consumer*,
and there are N of them; the proposed single writer is the *owner*, and there is one.

**The address is configurable, with a default.** Ruled in review, and the reason is measured
rather than hypothetical: in a container, `127.0.0.1` is the jail's own private loopback and a
collision is only possible with another baked service. **On `macos-user` there is no container
and no network namespace** — the sandbox is a native host process, so the adapter's ports are
*host* ports and collide with whatever the user is running. That backend cannot start a jail
daemon at all today, so the hazard is latent; it becomes live the moment that half lands.

---

## 7. What this does not propose

- **Not a gateway.** No routing tables, no failover, no budgets, no model remapping. The
  resolver picks an address that was declared; it does not invent one.
- **Not a protocol registry in core.** The key set stays open, exactly as
  `Endpoints` already leaves it: a protocol nothing declares resolves to nothing, which is inert.
- **Not auto-selection of packs** ([§3](#3-one-resolution), and [OQ-PR3](#OQ-PR3)).
- **Not a change to where credentials live.** `api_key_env_name` remains a NAME; the resolver
  never touches a secret.
- **Not a change to `needs`.** An adapter joins a launch exactly as it does now.

---

## 8. Alternatives considered

| | Verdict |
| :--- | :--- |
| **Gate the credential on an anthropic endpoint existing** ([`OQ-2`](cerebras-pack-and-copilot-delivery.md#oq-2)'s first framing) | Rejected as the whole answer: it fixes the leak and leaves the user with a broken pairing and no explanation. Kept as the interim one-liner, because it is correct and ships today |
| **A core table mapping agent → protocol** | Rejected — [`OQ-CS8`](../reference/providers.md#why-its-this-way) ruled it out, and a declaration is strictly better: a new agent needs no core change |
| **Keep the shorthand and infer its protocol from the agent** | Rejected: that is today's behaviour, and it is what makes one config line mean two things |
| **Have every provider pack keep hand-writing the adapter's URL** | Rejected for the user-declared case, which it cannot serve at all |
| **Auto-join the adapter pack when a pairing needs it** | Deferred to [OQ-PR3](#OQ-PR3); it removes a step and adds an inference about what may run in the jail |

---

## 9. Risks

| Risk | Mitigation |
| :--- | :--- |
| **R1.** Deleting the shorthand breaks configs in the wild | It has existed for claude since 2026-09-16 and is refused rather than ignored, so a broken config is diagnosable at `yolo check`, not at the first request |
| **R2.** The declaration lands and nothing enforces it | The census is worthless without the resolver; land them in one sprint, and pin the call site rather than the helper |
| **R3.** An agent pack declares its protocols wrongly and a working setup starts refusing | Outcome 4's message names both protocols and the declaration that produced each, so the wrong one is visible in the refusal |
| **R4.** The resolver becomes the place every future pairing rule accretes | [§7](#7-what-this-does-not-propose) is the boundary; a rule that is not "can these two speak" belongs elsewhere |

---

## 10. What I would build, in order

1. **The pair rule in claude's derive** — one `elseif`, no declaration needed, closes the live
   leak today. Independent of everything below.
2. **The agent's protocol declaration** (`packdecl` field + the packs that ship a program), inert:
   nothing reads it yet, so no launch changes.
3. **The resolver, with outcomes 1 and 4 only** — direct, or refuse on no common protocol. This
   is where the leak becomes unrepresentable rather than guarded.
4. **The adapter declaration and outcome 2**, which is [OQ-PR1](#OQ-PR1)'s subject, plus moving
   `8214`/`8215` out of their consumers.
5. **Outcome 3's refusal**, once an adapter can be named.
6. **Delete the shorthand**, last: its replacement has to resolve identically first.
7. **The adapter's configurable address** ([§6](#6-the-adapter-owns-its-address)), which is only
   tractable after step 4.

---

## 11. Open questions

1. <a id="OQ-PR1"></a>💬 **OQ-PR1: How does a pack declare that it adapts one protocol to another?**
   A new contribution kind (`adapter`, beside `provider` and `service`), or a field on the
   existing `service` kind that `wire-bridge` already uses? Decides whether a second adapter —
   an openai→responses shim, say — is a pack anyone can write or a change to the service kind.
   The rest of this design hangs off the answer's shape, not its content.

   <!-- vantage: oq id=OQ-PR1 leaning="A field on the service kind: the adapter IS a service, its address is the service's address, and a second kind would restate the whole of one." -->

   _Leaning:_ a field on `service`. The adapter already *is* a service with an address; a new
   kind would restate a service's whole declaration to add one pair. The argument against is
   that `service` currently means "a daemon this pack runs" and would grow a second meaning.

   **Answer:**
   > _(empty — fill in when decided)_

2. <a id="OQ-PR2"></a>💬 **OQ-PR2: Does a provider with NO endpoints stay legal?**
   Today it means "use the agent's first-party API with this key" — a deliberate BYO-key launch,
   and [§4.1](#41-degenerate-inputs) keeps it. The alternative is requiring every provider to name
   at least one endpoint, which makes the resolver total but breaks that case. Decides whether
   the resolver has a default branch at all.

   <!-- vantage: oq id=OQ-PR2 leaning="Keep it: a provider that names no address has not repointed anything, and refusing it would break the plain BYO-key launch that works today." -->

   _Leaning:_ keep it. A provider that names no address has repointed nothing, so there is no
   pairing to resolve and nothing to refuse.

   **Answer:**
   > _(empty — fill in when decided)_

3. <a id="OQ-PR3"></a>💬 **OQ-PR3: May the resolver join an adapter pack the user did not select?**
   Outcome 3 refuses and names the pack. The alternative is joining it automatically, the way
   `needs` already joins one for a *selected* pack. Decides whether choosing a provider can
   change what runs inside the jail.

   <!-- vantage: oq id=OQ-PR3 leaning="Refuse and name it. `needs` is a pack author declaring its own dependency; inferring one from a user's provider choice is a different act." -->

   _Leaning:_ refuse and name it. `needs` is a pack author stating its own dependency, which the
   user accepted by selecting that pack; inferring a new pack from a provider choice is yolo
   deciding what runs in the jail. And the cost of being wrong is one line in `packs`, in a
   message that names it — which is [P5](#1-the-verdict-and-the-principles-it-rests-on)-compatible
   because the value is a pack name, not an internal port.

   **Answer:**
   > _(empty — fill in when decided)_

---

## 12. Decision ledger

**Four ruled in review on 2026-09-18**, in the conversation that produced this doc. Three
questions are live ([§11](#11-open-questions)); nothing is built.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| — | **A pack declares the protocols its program speaks.** Asked directly and ruled yes: it carries no new risk and the information is already half-present (providers declare theirs). It satisfies [`OQ-CS8`](../reference/providers.md#why-its-this-way) because the AGENT declares it and core only compares two declarations — the same shape `required_capabilities` has | 2026-09-18 | [§3](#3-one-resolution) | — |
| — | **A declared adapter makes an otherwise-unusable pairing resolve, automatically.** "I would rather that just be an automatic resolution" — the user should not invoke the bridge, and with `packs/claude` already `needs`-ing `wire-bridge` unconditionally, the common case needs no action at all | 2026-09-18 | [§3](#3-one-resolution) | — |
| — | **The adapter's address is configurable.** Raised in review against port collisions, and it is backend-shaped: harmless on a container's private loopback, real on `macos-user`, which has no network namespace | 2026-09-18 | [§6](#6-the-adapter-owns-its-address) | — |
| — | **The single-protocol `base_url` shorthand is deleted, with no transition option.** Ruled after the evidence that one value means `openai` to pi and `anthropic` to claude, and that its headline local-endpoint case fails at the first request for every OpenAI-speaking local server | 2026-09-18 | [§5](#5-the-single-protocol-shorthand-is-deleted) | — |

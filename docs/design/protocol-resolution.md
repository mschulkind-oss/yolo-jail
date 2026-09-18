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

**Status:** DESIGN, 2026-09-18. Nothing built. **Six decisions ruled across two review rounds
the same day** ([§12](#12-decision-ledger)); every current-behaviour claim below was read at
`220fffb6`.

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

**Needs your ruling:** [OQ-PR1](#OQ-PR1) — restated after round two, because the criterion that decides it is *third-party adapters are first-class*, not which vocabulary reads better.

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
- **P6. NO SHIPPED ADAPTER IS PRIVILEGED.** The vocabulary must let anyone declare an adapter
  in a pack of their own and have it resolve exactly as `wire-bridge` does — same declaration,
  same selection, same disclosure, same refusal when it is missing. Core may not name an adapter
  pack, prefer one, or ship a fallback for the case where none is declared. This is a
  REQUIREMENT on the answer to [OQ-PR1](#OQ-PR1), not a preference: an adapter mechanism that
  works better for the pack we ship is a special case wearing a vocabulary's clothes, and the
  `grep` in [§4.5](#45-what-done-looks-like) is how it stays honest.
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

**The adapter set is every SELECTED pack that declares an adaptation** — no registry, no
built-in list, and `wire-bridge` in it by the same route as anyone else's pack
([P6](#1-the-verdict-and-the-principles-it-rests-on)). A protocol PAIR is sole-owned the way a
provider name is: two selected packs declaring `openai → anthropic` is a launch error naming
both, because the alternative is core preferring one.

**The resolver never selects a pack on the user's behalf** (ruled: [§12](#12-decision-ledger)).
The two halves are the whole user story and they are deliberately different acts: with the
adapter pack absent the pairing REFUSES and names it; with the pack present it just works, with
no further configuration — no flag, no endpoint to write, nothing naming the bridge.

---

## 4. Behaviour, stated exhaustively

### 4.1 Degenerate inputs

| Input | Resolved as |
| :--- | :--- |
| Provider declares NO endpoints, carries a key | **Direct, against the agent's first-party API.** Nothing was repointed; this is the BYO-key launch, and it stays legal (ruled: [§12](#12-decision-ledger)) |
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
- Core may not name an ADAPTER. `wire-bridge` appears in `packs/` and in a refusal quoting a
  declaration, never in `internal/`.
- The resolver may not select, install or enable a pack.

### 4.5 What done looks like

- A user declares `endpoints.openai` + `api_key_env` for their own service, runs claude, and it
  works with no further configuration and no yolo-internal value in their config.
- The same config with `wire-bridge` absent from `packs` refuses, names the pack, and exits
  non-zero.
- `grep -r 8214 packs/` returns nothing.
- A provider that names `openai` and a key never results in a request to `api.anthropic.com`.
- **A third-party pack declaring `openai → anthropic` resolves identically to `wire-bridge`,
  and `rg -n 'wire-bridge' internal/` returns nothing.** That grep is the test for
  [P6](#1-the-verdict-and-the-principles-it-rests-on), and it is why the shipped adapter gains
  the declaration rather than keeping a shortcut.

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
- **Not auto-selection of packs** ([§3](#3-one-resolution)) — ruled. An absent adapter is a refusal that names it; a present one needs no configuration at all.
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
| **Auto-join the adapter pack when a pairing needs it** | **Rejected**, ruled 2026-09-18 ([§12](#12-decision-ledger)): it removes a step the user should take, by inferring what may run in their jail from a provider choice. The refusal names the pack, which costs one line and teaches the mechanism |

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

1. <a id="OQ-PR1"></a>💬 **OQ-PR1: What shape lets ANY pack declare an adaptation as a first-class citizen?**
   *(Restated after review round two. It was "a new kind or a field on `service`?", which asked
   which vocabulary reads better — the wrong question. The requirement is
   [P6](#1-the-verdict-and-the-principles-it-rests-on): someone else's adapter pack must resolve
   exactly as the one we ship, so the answer is whichever shape makes that true with no core
   knowledge of either.)*

   Both candidate shapes satisfy "any pack may declare it", so the discriminator is elsewhere,
   and it is this: **must an adapter run the daemon it adapts through?**

   - **(a) A field on the `service` kind.** An adapter is a service that additionally says which
     protocol pair it converts; its address IS the service's address, so there is one fact and
     one writer. Cost: `service` currently means *"a daemon this pack runs"* and would mean two
     things. Consequence: an adaptation cannot be declared without shipping the daemon.
   - **(b) A new `adapter` kind**, carrying the pair and an address. Cost: it restates most of a
     service's declaration. Benefit: a pack could declare an adaptation over a daemon it does not
     itself run — a second pack pointing at an existing bridge, or a local proxy the user already
     runs on a port they name.

   (b) is the more permissive vocabulary and (a) is the smaller one. The question is whether the
   permissive case is real: I have not found an adapter that is not a proxy, and a proxy is a
   daemon, which argues (a) — but "declare an adaptation over a service someone else runs" is
   exactly the kind of thing a third-party pack author might want, and this design has just
   promised them first-class treatment.

   Decides: the vocabulary, and with it whether the sole-ownership rule attaches to a pair alone
   or to a pair plus an address.

   <!-- vantage: oq id=OQ-PR1 leaning="(a), a field on the service kind — unless the 'adapt over someone else's daemon' case is real, in which case (b); nothing else distinguishes them once P6 forces both to be equally open." -->

   _Leaning:_ **(a)**, unless you can name a real adapter that is not the daemon it adapts
   through. Every adaptation I can construct is a translating proxy, and a translating proxy is a
   service — so (b) buys a generality with no instance, at the cost of a second declaration that
   restates a service's. If the instance exists, (b) is right and the extra kind is cheap.

   **Answer:**
   > _(empty — fill in when decided)_

## 12. Decision ledger

**Six ruled on 2026-09-18**, across the conversation that produced this doc and the review round
that followed it. One question is live ([§11](#11-open-questions)); nothing is built.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| — | **No shipped adapter is privileged** ([P6](#1-the-verdict-and-the-principles-it-rests-on)). Anyone must be able to declare an adapter in their own pack and have it resolve exactly as `wire-bridge` does; core may not name one, prefer one, or fall back to one. Raised in review round two as a constraint on the vocabulary rather than a preference about it, and it is what restated [OQ-PR1](#OQ-PR1) | 2026-09-18 | [§1](#1-the-verdict-and-the-principles-it-rests-on), [§4.5](#45-what-done-looks-like) | — |
| — | **A pack declares the protocols its program speaks.** Asked directly and ruled yes: it carries no new risk and the information is already half-present (providers declare theirs). It satisfies [`OQ-CS8`](../reference/providers.md#why-its-this-way) because the AGENT declares it and core only compares two declarations — the same shape `required_capabilities` has | 2026-09-18 | [§3](#3-one-resolution) | — |
| — | **A declared adapter makes an otherwise-unusable pairing resolve, automatically.** "I would rather that just be an automatic resolution" — the user should not invoke the bridge, and with `packs/claude` already `needs`-ing `wire-bridge` unconditionally, the common case needs no action at all | 2026-09-18 | [§3](#3-one-resolution) | — |
| — | **The adapter's address is configurable.** Raised in review against port collisions, and it is backend-shaped: harmless on a container's private loopback, real on `macos-user`, which has no network namespace | 2026-09-18 | [§6](#6-the-adapter-owns-its-address) | — |
| [OQ-PR2](#11-open-questions) | **A provider that declares NO endpoints stays legal** — it means "use the agent's first-party API with this key", the plain BYO-key launch, and nothing was repointed so there is no pairing to resolve. Refusing it would break a case that works today | 2026-09-18 | [§4.1](#41-degenerate-inputs) | — |
| [OQ-PR3](#11-open-questions) | **An absent adapter REFUSES and names the pack; a present one needs no configuration at all.** The two halves are the whole user story: define an openai-only provider, select claude, and see an error that says which pack to add — then add it and it works, with no flag, no endpoint to write, and nothing naming the bridge. The resolver never joins a pack on the user's behalf, because choosing a provider must not change what runs in the jail | 2026-09-18 | [§3](#3-one-resolution) | — |
| — | **The single-protocol `base_url` shorthand is deleted, with no transition option.** Ruled after the evidence that one value means `openai` to pi and `anthropic` to claude, and that its headline local-endpoint case fails at the first request for every OpenAI-speaking local server | 2026-09-18 | [§5](#5-the-single-protocol-shorthand-is-deleted) | — |

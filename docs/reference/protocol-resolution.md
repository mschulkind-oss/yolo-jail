---
status: current
verified: 2026-09-18
verified_commit: 7da1c628
covers:
  - internal/packload/protocolresolution.go
  - internal/packload/providers.go
  - internal/packload/deriveenv.go
  - internal/packdecl/kinds.go
  - internal/packdecl/contributes.go
  - internal/config/adapters.go
  - internal/cli/check/protocols.go
  - packs/wire-bridge/
tags: [providers, packs, protocols, adapters, wire-bridge, resolution]
summary: "An agent pack declares the wire protocols its program speaks, a provider declares an endpoint per protocol, and an adapter declares which wire it converts into which and at what address. Core pairs the declarations: direct, adapted, or a refusal that names what is missing — and names no pack, no adapter and no protocol of its own."
---

# Protocol resolution — pairing an agent with a provider by the wire each declares

**Status:** CURRENT as of 2026-09-18, verified against `7da1c628`.

A **wire protocol** *(the term this system uses)* is the request shape an agent's process can be
pointed at — the key an address is filed under in a provider's `endpoints` map, such as
`anthropic` or `openai`. It is not the same thing as a **dialect**, which is `wire_api`: the
protocol says which map key an address lives under, the dialect says which canonical request
format that address speaks. The protocol vocabulary is open; the dialect vocabulary is closed
([Current values](#current-values)).

Three parties declare, and core resolves. An agent pack declares the protocols its installed
program speaks, in preference order. A provider declares an address per protocol. An **adapter**
*(coined here)* declares a protocol PAIR it converts and the address the converted wire is served
at — and nothing about who runs it. Core compares those declarations once per launch and produces
an address, or a refusal that names what is missing. It never invents an address, never names a
protocol as a condition, and never selects a pack on the user's behalf.

| Component | Lives in |
| :--- | :--- |
| The `protocols` list and the `adapter` kind (schema) | `internal/packdecl` (`Contribution.Protocols`, `AdapterPair`, `KindAdapter`) |
| The resolver and its refusals | `internal/packload` (`ResolveProtocol`, `Adaptation`, `UnselectedAdaptations`) |
| The gate the launch applies | `internal/packload` (`AgentEnv`) |
| Outcome 2's address injection | `internal/packload` (`ComposeProviders`, `adaptEndpoints`) |
| The `adapters` config key | `internal/config` (`LoadAdapterAddresses`, `validateAdapters`) |
| The `yolo check` prediction | `internal/cli/check` (`protocolPairingGap` → `packload.PairingRefusals`) |
| The shipped adapter's declarations | `packs/wire-bridge` |
| The daemon that serves the shipped adapter's addresses | `internal/wirebridged` |

**Reads with:** [`wire-bridge.md`](wire-bridge.md) (the daemon that serves the shipped adapter's
addresses, and [`needs`](wire-bridge.md#needs--a-conditional-pack-dependency), which is how its
pack joins a launch), [`providers.md`](providers.md) (the provider table this resolves over;
[`OQ-CS8`](providers.md#why-its-this-way) is the line between resolving an address and composing
a variable), [`pack-system.md`](pack-system.md) (contribution kinds, sole ownership, and the
collision loop the adapter pair groups onto),
[`cerebras-pack-and-copilot-delivery.md`](cerebras-pack-and-copilot-delivery.md#why-its-this-way)
(the credential-without-an-address defect this makes unrepresentable).

---

## Principles

- **P1. A protocol pairing is resolved, never inferred.** Two parties that declare what they
  speak either match, match through a declared adapter, or do not match. A third answer — compose
  something and hope — is what produces a credential with no address.
- **P2. Core resolves ADDRESSES; a derive composes VARIABLES.** This is
  [`OQ-CS8`](providers.md#why-its-this-way)'s line. Core never learns that claude reads
  `ANTHROPIC_BASE_URL`. It learns that claude speaks `anthropic`, which the claude pack tells it,
  and hands back an endpoint. Which variable that lands in stays the agent pack's business.
- **P3. A declaration states a fact about its own declarer.** A provider says where it lives; an
  adapter says what it converts and where it listens. The shape this replaced broke that: the
  adapter's port was a fact stated by each of its consumers.
- **P4. A credential travels with the address it was minted for, or not at all.** Neither half
  alone is a usable configuration, and one half alone is a leak.
- **P5. The happy path takes no yolo-specific knowledge.** Declaring a provider means naming the
  service's real URL, its protocol and where its key lives. A user who must type an internal
  loopback port has been handed an implementation detail as a configuration step.
- **P6. No shipped adapter is privileged.** Anyone can declare an adapter in a pack of their own
  and have it resolve exactly as `wire-bridge` does — same declaration, same selection, same
  refusal when it is missing. Core may not name an adapter pack, prefer one, or ship a fallback
  for the case where none is declared.

## Invariants

- **One writer per fact.** The provider writes its upstreams, the adapter writes its own address,
  the agent writes its protocol list, and core writes nothing — it only selects among what was
  declared.
- **A protocol PAIR is sole-owned**, the way a provider name and a service name are. Two selected
  packs declaring the same conversion is a launch error naming both, because the alternative is
  core preferring one. One pack declaring several pairs is ordinary; the shipped bridge declares
  two.
- **The agent's declaration order is its preference order.** Resolution walks the list and the
  first protocol the provider offers wins, so a pack that speaks two wires says which one it would
  rather be given. An adapter is never preferred over a native endpoint.
- **Absent means unconstrained.** An agent pack that declares no protocols resolves direct for
  every provider — the compatibility shape for a pack nobody has updated. An agent that DOES
  declare is the one whose broken pairings become refusals.
- **The resolver may not select, install or enable a pack.** An absent adapter is a refusal that
  names the pack to add; the user adds it.
- **A refusal, never a warning.** The declarations' whole content is "this configuration does not
  work", and a launch that says so and proceeds has answered a declaration with a note. It exits
  non-zero before any container starts, beside the capability gate and the notch gate.

> [!IMPORTANT]
> **Core names no protocol, no pack and no adapter as a CONDITION**, and the qualifier is the
> whole rule. Protocol names appear in `internal/packload`'s refusal text only as values quoted
> out of declarations — never in a switch, a constant or a comparison — so a protocol nothing
> declares resolves to nothing, which is inert. Two places in `internal/` do hold protocol-shaped
> literals and neither is a resolution condition: `packdecl`'s closed `wire_api` enum
> (`KnownWireAPIs`, the DIALECT vocabulary, which the protocol key set is deliberately not), and
> `internal/wirebridged`, which IS the shipped adapter and therefore names the two wires it
> translates between. See [What the done-conditions could not be](#what-the-done-conditions-could-not-be).

## The three declarations

**An agent pack declares the wires its program speaks.** A `protocols` list on the `program`
surface, in preference order. It is a fact about a PROGRAM — which wires that binary's process can
be pointed at — so the schema refuses it on every other kind, where it would be a list no consumer
reads. The names are `endpoints` KEYS, not `wire_api` VALUES: the resolver pairs by ADDRESS, so it
compares the things addresses are filed under.

**A provider declares an address per protocol.** This predates the resolver and is unchanged —
`endpoints.<protocol>.{base_url, wire_api}`, plus the credential's variable NAME, the region, the
model aliases and the options. [`providers.md`](providers.md) is its authority.

**An adapter declares `from`, `to` and an ADDRESS — and nothing about who runs it.** That
separation is the ruling ([`OQ-PR1`](#why-its-this-way)) and it is what lets the pieces decompose:
an agent pack brings a protocol, an adapter pack adds another to it, and whether anything is
launched to serve that address is a separate fact the pack states separately if it applies. Three
provisioning shapes, all first-class:

| The address is served by | How it is declared | Example |
| :--- | :--- | :--- |
| a daemon the adapter's own pack ships | the `adapter` contribution **plus** that pack's own `service` contribution, joined to a launch by `needs` | `packs/wire-bridge` |
| a remote service yolo does not run | the `adapter` contribution alone, naming a URL | a hosted gateway you already pay for, fronting your own credentials |
| something the user already runs locally | the `adapter` contribution alone, naming the port they chose | a proxy already running on the host |

Only the first involves a daemon, and nothing assumes the others away: an adapter that is a
running service is the common case, not the definition. The presence of a sibling `service`
contribution is the only thing that tells the three apart.

**The adapter set is every SELECTED pack that declares an adaptation** — no registry, no built-in
list, and `wire-bridge` in it by the same route as anyone else's pack (P6). An `adapter`
contribution is not review-worthy: it declares an ADDRESS, the same class of fact a provider
endpoint is, and whatever SERVES that address declares itself separately and is reviewed there.

## The four outcomes

| | Condition | Result |
| :--- | :--- | :--- |
| **1. Direct** | the agent speaks a protocol the provider offers | the provider's own endpoint for that protocol |
| **2. Adapted** | a SELECTED pack declares a conversion from one of the provider's protocols into one of the agent's | the adapter's address, composed into the provider's entry |
| **3. Refuse, naming the pack** | a pack yolo ships but this launch did NOT select declares that conversion | a refusal that names the pack to add to `packs` |
| **4. Refuse, naming both** | nothing this launch can see bridges them | a refusal naming both sides and the declaration that produced each |

Outcome 2 is the point. It is what a pack author used to write by hand, computed — and it is what
makes a user-declared provider work with no yolo-specific knowledge. Outcome 3's message names the
remedy, which is the discoverable half; the resolver does not join the pack, does not offer to,
and does not fall back to it, because choosing a provider must not decide what runs in your jail
([`OQ-PR3`](#why-its-this-way)).

Outcome 4's message names both sides and where each was declared. A resolver that refuses without
saying which declaration produced which half turns "an agent pack declares its protocols wrongly"
into an unfixable launch failure; with both halves quoted, the wrong one is visible in the refusal
itself.

> [!IMPORTANT]
> **Outcome 2 is produced at COMPOSITION, not at the gate, and the resolver cannot tell an adapted
> pairing from a native one.** `ComposeProviders` runs `adaptEndpoints` LAST, over the finished
> table and below the user layer, writing the adapter's address into every provider entry that
> offers the adapter's `from` and lacks its `to`. By the time the gate asks, an adapted address is
> just an address in the table, so the gate sees outcome 1 and only outcomes 3 and 4 are refusals
> it computes. Two consequences follow. An explicit `endpoints.<protocol>.base_url` always wins —
> an adapter fills a hole, and a user who wrote an address did not leave one. And the injection is
> gated on the union of protocols the SELECTED agents declare, so a launch with only
> openai-speaking agents composes no anthropic address for anyone.

**Outcome 2 is not announced at the launch.** The `adapter` kind is classified
`disclosureSkip` beside `provider` and `service`: it declares an address and a protocol pair,
which are facts about a service rather than a read of this machine, so nothing on the host is
touched and there is nothing to announce at the spawn. The address's effect is visible where it
lands, in the provider table the launch carries. A pack that runs a daemon for the address
declares that separately and the crossing is disclosed from THAT declaration.

## Where the gate lives, and why not in the run pre-flight

The gate is `packload.AgentEnv` — the delivery of an agent's provider environment, and the one
runner both notches reduce through. It sits above the derive, not inside it: whether this agent
can be pointed at this provider at all is a question about two DECLARATIONS, and core answers it,
which is P2's line. It is above the derive-script lookup too, so a pairing nothing can serve is
broken whether or not the pack ships a producer — a silent pass for a pack with no `derive.lua`
would make the gate depend on a file's existence.

> [!WARNING]
> **Do not move this gate to the run pre-flight, and do not add a copy there.** It looks like the
> capability gate's neighbour and it cannot be. The gate needs a resolved SELECTION — it refuses a
> pairing this launch actually asked for, and says nothing about the providers merely sitting in
> the table — and the run pre-flight reads the MERGED USER CONFIG only. It cannot see a
> pack-shipped provider's endpoints at all, so a copy there would refuse a different set of
> launches than the one that ships. `internal/packload/protocolresolution.go` states this where
> the gate is, and `preflight.go`'s own ⚠ records the same boundary for its census.

The resolution runs BEFORE the derives and its result is an input to them, so a derive can no
longer reach a state where it has a key and no address. That is what let claude's derive delete
its interim guard rather than rework it: the provider shape that named a protocol claude could not
speak, and had its key composed with no base URL beside it, cannot reach the producer at all.

## `yolo check` predicts the refusal through the same gate

`yolo check` reports the pairing gate in its Packs block, after `needs` resolution so a pack
pulled in by another can supply the adapter, exactly as at launch. A refused pairing is a FAIL
carrying the launch's own refusal text verbatim; every pairing resolving prints nothing, matching
a launch that never announces a gate it did not trip; inputs that will not assemble are a WARN
naming why, never silence.

> [!IMPORTANT]
> **This is not a second copy, and the difference from `capabilities.go` is the point.** That file
> restates the launch's capability census because reaching the real one would mean exporting a
> method on `run.Options` and dragging its printer in. Nothing of the kind applies here — the gate
> is a free function over inputs a caller can assemble — so `check` calls
> `packload.PairingRefusals`, which is the gate `AgentEnv` applies, through the same owner lookup
> and the same resolver. Two copies of a pairing rule could disagree; there is one.
>
> **What IS duplicated is the input assembly** — composing the providers and resolving the
> profiles the way a launch does — and that is the honest residue. Get one of those wrong and the
> prediction is wrong in a way no shared function can catch.

> [!WARNING]
> **`yolo check` cannot see `-p`.** `check` reads configuration; a `-p <name>` is an argument to a
> launch that has not happened, and the flag folds in above this. So a clean prediction means "the
> `use_profiles` selection pairs", never "any launch from this config pairs". Narrowing that needs
> the flag, not a wider census — widening the census here to guess at flags would make the
> prediction wrong in places the launch is not.

An agent with no profile is not pairing with anything, and both the gate and the prediction return
early on one: the gate is reached through a SELECTION, and a provider merely present in the table
repoints nothing.

## The single-protocol `base_url` shorthand is removed

A bare `providers.<name>.base_url` is a validation refusal naming the explicit spelling, the way
the retired `journal` and `host_processes` top-level keys are. The key stays in the known-key
census on purpose: a bare "unknown key" reads like a typo and sends people hunting for the correct
spelling of a key that no longer exists, so the retirement message is the only one the key earns.

Three facts made it unusable, and the refusal states all three. The same value meant `openai` to
pi and `anthropic` to claude, so one line of config pointed two agents at two different services.
Its headline case is a trap: llama.cpp, ollama and vLLM all speak OpenAI, so `claude` plus a bare
URL aimed `ANTHROPIC_BASE_URL` at a server claude cannot talk to and failed at the first request.
And a URL with no protocol is precisely the input the resolver cannot reason about — which is why
`providerProtocols` does not consult the shorthand either, the same call the bridge's own route
selection makes, for the same reason.

This FINISHES [`OQ-PT2`](providers.md#why-its-this-way) rather than reversing it: that ruling
already refused `base_url` beside `endpoints`, because the shorthand is the ambiguous spelling
once more than one protocol exists. The address stays user-scope-only either way — both spellings
are refused in a workspace config, because a file an agent can edit must not decide where
inference goes, and removing one spelling does not touch that.

> [!WARNING]
> **The shorthand's consuming branches in `packs/codex/derive.lua` and `packs/copilot/derive.lua`
> are NOT dead code. Do not delete them on the grounds that the key is removed.** The retirement is
> an ERROR ON THE HOST and a WARNING IN A JAIL: in-jail the config is the host-generated snapshot,
> refusing there would stop every nested launch over a key the in-jail user cannot fix at its
> source, and the known-key census still passes the key through. So a jail launched by a host
> `yolo` that predates the removal still carries it, and deleting either arm turns that jail's
> provider into one with no address — measured 2026-09-18 by deleting the codex arm, which reddens
> cases in `internal/entrypoint`'s selection-apply tests.
>
> **The retirement condition is "when the snapshot can no longer carry the key", not "when the
> host config refuses it".** Both derives carry this as a comment at the branch; this is where it
> lives permanently.

## The adapter's address

The adapter declares the address it serves and the resolver reads it. That **inverts a deliberate
choice** — the shipped bridge's old "one writer, no second knob", which put the port in each
provider's manifest URL. The trade is stated rather than assumed: the old single writer was the
CONSUMER and there were N of them; the new single writer is the OWNER and there is one. What it
buys is the case the old shape could not serve at all — a user's own `endpoints.openai` plus a key
now reaches an anthropic-speaking agent, because the address is computed rather than typed.

Where the address comes from depends on the shape. An adapter that ships its own daemon declares
the address that daemon binds, and THAT one is configurable with the declaration as its default,
through the user-scope `adapters` config key. An adapter naming a remote or user-run service
carries an address the user already owns, and yolo neither defaults nor moves it.

**Only the address is configurable**, and the reason is backend-shaped rather than hypothetical:
in a container `127.0.0.1` is the jail's own private loopback and a collision is only possible
with another baked service, while on `macos-user` there is no container and no network namespace,
so an adapter's ports are HOST ports and collide with whatever the user is already running. The
PAIR is the declaring pack's claim — a user who wanted a different conversion would be declaring
an adapter, which is a pack's job — so an override replaces the address and changes nothing else.
The key is the conversion, spelled `<from>-><to>`, which is the same identity core groups
collisions on; a key naming a conversion nothing declares is inert, matching the open vocabulary
the protocol names themselves follow.

### Moving the Codex address required giving `openai-codex` an endpoint

The shipped bridge's second adaptation, `openai-responses → anthropic`, serves Claude's Codex
profile. Its address used to be a Go constant hand-copied into `packs/claude/derive.lua`, which
that file had no way to keep true. It moved by making the pairing an ordinary one: the
`openai-auth` pack's `openai-codex` provider now declares the PUBLIC Responses address the
subscription actually serves, the adapter declares its own address, and core composes the pair
into that provider's entry exactly as it does for every other bridged provider. Claude's derive
reads an endpoint like any other.

**The `openai-codex` row stays credential-free.** It names no key variable, and the bridge takes
its short-lived access-token view from the `openai-auth` broker rather than from any generated
configuration file.

> [!WARNING]
> **An override of `openai-responses->anthropic` moves the AGENT and not the LISTENER.** The
> bridge's Codex route is chosen before any endpoint is consulted — it is selected by the agent
> and provider names, ahead of the table read — so the daemon binds its own
> `CodexResponsesListenAddr` constant whatever the `adapters` key says, while the composed
> provider entry (and therefore the agent) follows the override. The other adaptation has no such
> gap: the daemon derives its listen address from the composed provider entry, so an override of
> `openai->anthropic` moves both halves. Verified 2026-09-18; the Go constant's own comment
> records that it is "this side of a contract whose other side moved", but not this consequence.

## Degenerate inputs, and what each resolves to

| Input | Resolved as |
| :--- | :--- |
| Provider declares NO endpoints, carries a key | **Direct, against the agent's first-party API.** Nothing was repointed; this is the BYO-key launch and it stays legal ([`OQ-PR2`](#why-its-this-way)) |
| Provider declares no endpoints and no key | Direct; the agent uses whatever credential it already holds, such as a subscription OAuth |
| Provider declares two protocols, one of which the agent speaks | Direct on that one. An adapter is never preferred over a native endpoint |
| Provider's entry carries only the removed shorthand | Offers nothing, so it resolves as the nothing-to-settle case rather than as a refusal |
| Two selected packs declare the same pair | A launch error naming both, from the generic sole-ownership loop. The resolver itself keeps the first, so a caller that skipped the pre-flight degrades to a stable table |
| Agent declares more than one protocol | Resolved against each in declaration order; the first that resolves wins |
| Agent pack declares no protocols | Direct, always. The compatibility shape |
| No profile selected for this agent, or a profile resolving to no provider | Nothing to ask. Not this gate's to report |

**A provider whose endpoint is unreachable is not this resolver's business.** It resolves
declarations and never dials, so the failure surfaces where it already did, at the first request.
An adapter whose own pack is selected but whose service cannot start is the launch's existing
refusal on an enabled jail-facing service the jail cannot use, unchanged here. An adapter
declaring an address already in use fails when its service binds.

**Only a pack yolo SHIPS can be named in outcome 3.** The unselected-adaptation walk reads the
embedded set, which is deliberately not selection-gated, so a third-party pack the user has not
selected is invisible and its pairing gets outcome 4's message instead. That is a limit on the
REMEDY and never on the rule: the pairing resolves identically once either pack is selected (P6),
and core can only name what it can see.

## What this does not license

- **Not a gateway.** No routing tables, no failover, no budgets, no model remapping. The resolver
  picks an address that was declared; it does not invent one.
- **Not a protocol registry in core.** The protocol key set stays open, exactly as `endpoints`
  already leaves it. Closing it would make a third protocol the `tier` incident again, since
  tolerant decoding validates per entry and cannot skip a value it cannot see.
- **Not auto-selection of packs.** An absent adapter is a refusal that names it; a present one
  needs no configuration at all.
- **Not a change to where credentials live.** The credential travels by NAME and the resolver
  never touches a secret.
- **Not a change to `needs`.** An adapter's pack joins a launch exactly as it did.
- **Not a place for further pairing rules.** A rule that is not "can these two speak" belongs
  elsewhere.

## What the done-conditions could not be

Two of the design's greps were stated as absolutes and cannot be met as written. Both are restated
here in the only form that exists, because the restatement is the checkable rule and the grep is
not.

- **`rg -n 'wire-bridge' internal/` can never return nothing.** `internal/wirebridged` IS the
  shipped adapter — a `yolo-jaild` subcommand dispatched by that literal name, which is also its
  service name, its endpoint file's stem and the string its own diagnostics print. What holds
  instead, and what P6 actually requires: **no rule in `internal/` names a pack, an adapter or a
  protocol as a CONDITION.** The resolver compares two sets of names it was handed; the adapter
  daemon names the two wires it translates between, which is its own declaration and not core's.
- **`grep -r 8214 packs/` can never return nothing either.** The address has to live where it is
  declared, and that is a pack. What holds is that the literal left every CONSUMER: the shipped
  bridge's own manifest and README carry it, and `packs/cerebras` and `packs/kilo` — which
  hand-wrote it and a sibling port into their provider entries — now declare only their real
  upstreams. One `openai → anthropic` adaptation now serves both.

## What is unmeasured

**MEASURED at composition and delivery only**, by unit tests over the shipped packs and their real
`derive.lua` files: the adapted address composes byte-identically to the URL a pack used to
hand-write, the refusals fire at the gate before anything is composed, and every guard's call site
fails a test when it is deleted.

**UNMEASURED: no jail has been launched against any of it.** A real bridged request, the daemon
binding a MOVED address, and the whole of `macos-user` — whose port-collision hazard is the reason
the address is configurable at all, and which runs no pack `service` today — are all unverified.

## Why it's this way

| ID | Ruling | Why a maintainer would otherwise undo it |
| :--- | :--- | :--- |
| <a id="oq-pr1"></a>[**OQ-PR1**](#oq-pr1) | **An adapter is its own contribution kind, declaring `from`, `to` and an ADDRESS — and nothing about who runs it.** | The leaning was a field on the `service` kind, on the argument that every adapter is a proxy and every proxy is a daemon. Three instances answered it: a remote gateway fronting your own credentials, a proxy the user already runs on a port they name, and the plain wish to ship an adapter apart from the thing it adapts. Coupling the declaration to a daemon makes all three inexpressible. |
| <a id="oq-pr2"></a>[**OQ-PR2**](#oq-pr2) | **A provider that declares NO endpoints stays legal.** | It means "use the agent's first-party API with this key" — the plain BYO-key launch. Nothing was repointed, so there is no pairing to resolve, and refusing it would break a case that works. |
| <a id="oq-pr3"></a>[**OQ-PR3**](#oq-pr3) | **An absent adapter REFUSES and names the pack; a present one needs no configuration at all.** | The resolver never joins a pack on the user's behalf, because choosing a provider must not change what runs in your jail. The one line of remedy is what that bought, and it teaches the mechanism. |
| — | **No shipped adapter is privileged** (P6). | A mechanism that works better for the pack yolo ships is a special case wearing a vocabulary's clothes. The requirement is on the vocabulary, not a preference about it. |
| — | **A pack declares the protocols its program speaks.** | It satisfies [`OQ-CS8`](providers.md#why-its-this-way) because the AGENT declares it and core only compares two declarations — the same shape `required_capabilities` has. The rejected alternative is a core table mapping agent → protocol, which makes every new agent a core change. |
| — | **The adapter's address is configurable, with the declaration as the default.** | Harmless on a container's private loopback, real on `macos-user`, which has no network namespace. The reason is backend-shaped, so a reader who only ever launches containers will not see why the knob exists. |
| — | **The single-protocol `base_url` shorthand is removed, with no transition.** | One value meant `openai` to pi and `anthropic` to claude, and its headline local-endpoint case failed at the first request for every OpenAI-speaking local server. Keeping it and inferring its protocol from the agent is the behaviour that was removed. |

## Current values

Verified at `7da1c628`. The prose above explains what each of these is for; this table is the only
place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Shipped adaptations | `openai → anthropic`, `openai-responses → anthropic` | `packs/wire-bridge/pack.json` |
| Shipped adapter addresses | `http://127.0.0.1:8214`, `http://127.0.0.1:8215` | `packs/wire-bridge/pack.json` |
| The Codex route's bind address | `127.0.0.1:8215` | `wirebridged.CodexResponsesListenAddr` |
| The Codex subscription upstream | `https://chatgpt.com/backend-api/codex` | `packs/openai-auth/pack.json`, `wirebridged.CodexResponsesBaseURL` |
| Protocol vocabulary | OPEN — any `endpoints` key | `packdecl`'s `Contribution.Endpoints` |
| Dialect (`wire_api`) vocabulary | CLOSED, three values | `packdecl.KnownWireAPIs` |
| Agent protocol declarations | `program` surfaces in `packs/*/pack.json` | `packdecl`'s `Contribution.Protocols` |
| Address override key | `adapters.<from>-><to>.address`, USER SCOPE ONLY | `yolo config-ref`, `internal/config/adapters.go` |
